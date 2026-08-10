package app

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/vishu42/tflive/internal/queue"
)

func TestMarkStackReadySpecSharesTheStackKey(t *testing.T) {
	t.Parallel()

	payload, err := json.Marshal(MarkStackReadyPayload{StackID: "stack_abc", TenantID: "tenant_1"})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	key, err := MarkStackReadySpec.Key(payload)
	if err != nil {
		t.Fatalf("Key returned error: %v", err)
	}
	// Sharing a key with grant_stack_owner is safe: the unique index is on
	// (kind, resource_key), so each kind only excludes itself.
	if key != "stack:stack_abc" {
		t.Fatalf("Key = %q, want stack:stack_abc", key)
	}
}

func TestMarkStackReadyHandlerIsIdempotentAndChainsNothing(t *testing.T) {
	t.Parallel()

	statuses := &recordingStackStatuses{}
	handler := NewMarkStackReadyHandler(NewService(Service{StackStatuses: statuses}))
	payload, err := json.Marshal(MarkStackReadyPayload{StackID: "stack_abc", TenantID: "tenant_1"})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	for attempt := 0; attempt < 2; attempt++ {
		followUps, err := handler.Deliver(context.Background(), queue.Item{Payload: payload})
		if err != nil {
			t.Fatalf("Deliver attempt %d returned error: %v", attempt, err)
		}
		if len(followUps) != 0 {
			t.Fatalf("follow-ups = %+v, want none", followUps)
		}
	}
	if statuses.calls != 2 {
		t.Fatalf("MarkStackReady calls = %d, want 2", statuses.calls)
	}
	if statuses.tenant != "tenant_1" || statuses.stack != "stack_abc" {
		t.Fatalf("marked %q/%q, want tenant_1/stack_abc", statuses.tenant, statuses.stack)
	}
}

func TestMarkStackReadyHandlerPropagatesRepositoryFailure(t *testing.T) {
	t.Parallel()

	statuses := &recordingStackStatuses{err: errors.New("postgres unavailable")}
	handler := NewMarkStackReadyHandler(NewService(Service{StackStatuses: statuses}))
	payload, _ := json.Marshal(MarkStackReadyPayload{StackID: "stack_abc", TenantID: "tenant_1"})

	if _, err := handler.Deliver(context.Background(), queue.Item{Payload: payload}); err == nil {
		t.Fatal("Deliver swallowed the repository failure — the item must be retried")
	}
}
