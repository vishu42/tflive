package dispatch

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/vishu42/megagega/internal/traits"
)

func TestDispatchOnceCompletesSuccessfullyStartedWorkflow(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 10, 10, 0, 0, 0, time.UTC)
	outbox := &memoryOutbox{entry: Entry{
		ID: "template-run/tenant_123/run_123",
		Input: traits.TemplateRunWorkflowInput{
			TenantID: traits.TenantID("tenant_123"),
			RunID:    traits.TemplateRunID("run_123"),
		},
	}}
	dispatcher := NewDispatcher(outbox, workflowStarterFunc(func(_ context.Context, input traits.TemplateRunWorkflowInput) error {
		if input.RunID != traits.TemplateRunID("run_123") {
			t.Fatalf("workflow run ID = %q, want run_123", input.RunID)
		}
		return nil
	}), Options{LeaseDuration: 30 * time.Second, RetryDelay: 5 * time.Second})

	dispatched, err := dispatcher.DispatchOnce(context.Background(), now)
	if err != nil {
		t.Fatalf("DispatchOnce returned error: %v", err)
	}
	if !dispatched {
		t.Fatal("DispatchOnce dispatched = false, want true")
	}
	if outbox.completedID != outbox.entry.ID {
		t.Fatalf("completed outbox ID = %q, want %q", outbox.completedID, outbox.entry.ID)
	}
	if outbox.claimedUntil != now.Add(30*time.Second) {
		t.Fatalf("claimed until = %v, want %v", outbox.claimedUntil, now.Add(30*time.Second))
	}
}

func TestDispatchOnceReleasesFailedWorkflowForRetry(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 10, 10, 0, 0, 0, time.UTC)
	startErr := errors.New("temporal unavailable")
	outbox := &memoryOutbox{entry: Entry{ID: "template-run/tenant_123/run_123"}}
	dispatcher := NewDispatcher(outbox, workflowStarterFunc(func(context.Context, traits.TemplateRunWorkflowInput) error {
		return startErr
	}), Options{LeaseDuration: 30 * time.Second, RetryDelay: 5 * time.Second})

	dispatched, err := dispatcher.DispatchOnce(context.Background(), now)
	if !errors.Is(err, startErr) {
		t.Fatalf("DispatchOnce error = %v, want %v", err, startErr)
	}
	if !dispatched {
		t.Fatal("DispatchOnce dispatched = false, want true")
	}
	if outbox.retryID != outbox.entry.ID {
		t.Fatalf("retried outbox ID = %q, want %q", outbox.retryID, outbox.entry.ID)
	}
	if outbox.retryAt != now.Add(5*time.Second) {
		t.Fatalf("retry at = %v, want %v", outbox.retryAt, now.Add(5*time.Second))
	}
	if outbox.lastError != startErr.Error() {
		t.Fatalf("last error = %q, want %q", outbox.lastError, startErr.Error())
	}
}

type memoryOutbox struct {
	entry        Entry
	claimedUntil time.Time
	completedID  string
	retryID      string
	retryAt      time.Time
	lastError    string
}

func (outbox *memoryOutbox) ClaimTemplateRun(_ context.Context, _ time.Time, claimedUntil time.Time) (Entry, bool, error) {
	outbox.claimedUntil = claimedUntil
	if outbox.entry.ID == "" {
		return Entry{}, false, nil
	}
	return outbox.entry, true, nil
}

func (outbox *memoryOutbox) CompleteTemplateRun(_ context.Context, id string) error {
	outbox.completedID = id
	return nil
}

func (outbox *memoryOutbox) RetryTemplateRun(_ context.Context, id string, availableAt time.Time, lastError string) error {
	outbox.retryID = id
	outbox.retryAt = availableAt
	outbox.lastError = lastError
	return nil
}

type workflowStarterFunc func(context.Context, traits.TemplateRunWorkflowInput) error

func (start workflowStarterFunc) StartTemplateRun(ctx context.Context, input traits.TemplateRunWorkflowInput) error {
	return start(ctx, input)
}
