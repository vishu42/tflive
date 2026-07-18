package authdispatch

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/vishu42/tflive/internal/authz"
)

func TestDispatchOnceCompletesConfirmedGrant(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 18, 10, 0, 0, 0, time.UTC)
	outbox := &memoryOutbox{entry: Entry{
		ID:        "grant/user:user_123/stack:stack_123/owner",
		Operation: OperationGrant,
		Subject:   "user:user_123",
		Stack:     "stack:stack_123",
		Role:      "owner",
	}}
	dispatcher := NewDispatcher(outbox, recordingAuthorizer{}, Options{LeaseDuration: 30 * time.Second, RetryDelay: 5 * time.Second})

	dispatched, err := dispatcher.DispatchOnce(context.Background(), now)
	if err != nil {
		t.Fatalf("DispatchOnce() error = %v", err)
	}
	if !dispatched {
		t.Fatal("DispatchOnce() dispatched = false, want true")
	}
	if outbox.completedID != outbox.entry.ID {
		t.Fatalf("completed ID = %q, want %q", outbox.completedID, outbox.entry.ID)
	}
}

func TestDispatchOnceRetriesAuthorizationFailureWithSanitizedError(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 18, 10, 0, 0, 0, time.UTC)
	outbox := &memoryOutbox{entry: Entry{
		ID:        "grant/user:user_123/stack:stack_123/owner",
		Operation: OperationGrant,
		Subject:   "user:user_123",
		Stack:     "stack:stack_123",
		Role:      "owner",
	}}
	dispatcher := NewDispatcher(outbox, recordingAuthorizer{writeErr: authz.ErrUnavailable}, Options{RetryDelay: 5 * time.Second})

	_, err := dispatcher.DispatchOnce(context.Background(), now)
	if !errors.Is(err, authz.ErrUnavailable) {
		t.Fatalf("DispatchOnce() error = %v, want ErrUnavailable", err)
	}
	if outbox.retryAt != now.Add(5*time.Second) {
		t.Fatalf("retry at = %v, want %v", outbox.retryAt, now.Add(5*time.Second))
	}
	if outbox.lastError != "authorization unavailable" {
		t.Fatalf("last error = %q, want authorization unavailable", outbox.lastError)
	}
}

type memoryOutbox struct {
	entry        Entry
	claimedUntil time.Time
	completedID  string
	retryAt      time.Time
	lastError    string
}

func (outbox *memoryOutbox) ClaimAuthorizationRelationship(_ context.Context, _ time.Time, claimedUntil time.Time) (Entry, bool, error) {
	outbox.claimedUntil = claimedUntil
	if outbox.entry.ID == "" {
		return Entry{}, false, nil
	}
	return outbox.entry, true, nil
}

func (outbox *memoryOutbox) CompleteAuthorizationRelationship(_ context.Context, id string) error {
	outbox.completedID = id
	return nil
}

func (outbox *memoryOutbox) RetryAuthorizationRelationship(_ context.Context, _ string, availableAt time.Time, lastError string) error {
	outbox.retryAt = availableAt
	outbox.lastError = lastError
	return nil
}

func (outbox *memoryOutbox) FailAuthorizationRelationship(context.Context, string, string) error {
	return nil
}

type recordingAuthorizer struct {
	writeErr error
}

func (authorizer recordingAuthorizer) Check(context.Context, authz.CheckRequest) (authz.CheckResult, error) {
	return authz.CheckResult{}, nil
}
func (authorizer recordingAuthorizer) BatchCheck(context.Context, authz.BatchCheckRequest) (authz.BatchCheckResult, error) {
	return authz.BatchCheckResult{}, nil
}
func (authorizer recordingAuthorizer) ListAccessibleStacks(context.Context, authz.ListAccessibleStacksRequest) (authz.ListAccessibleStacksResult, error) {
	return authz.ListAccessibleStacksResult{}, nil
}
func (authorizer recordingAuthorizer) ListGrants(context.Context, authz.ListGrantsRequest) (authz.ListGrantsResult, error) {
	return authz.ListGrantsResult{}, nil
}
func (authorizer recordingAuthorizer) WriteRelationships(context.Context, authz.Mutation) error {
	return authorizer.writeErr
}
func (authorizer recordingAuthorizer) DeleteRelationships(context.Context, authz.Mutation) error {
	return nil
}
