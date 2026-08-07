package app

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/vishu42/tflive/internal/authz"
	"github.com/vishu42/tflive/internal/queue"
)

func TestProvisionStackSpecKeyNamesTheStack(t *testing.T) {
	t.Parallel()

	key, err := ProvisionStackSpec.Key(provisionPayload(t, "stack_abc", "tenant_1", provisioningSubject))
	if err != nil {
		t.Fatalf("Key returned error: %v", err)
	}
	if key != "stack:stack_abc" {
		t.Fatalf("Key = %q, want stack:stack_abc", key)
	}
	if ProvisionStackSpec.Mode != queue.ModeJob {
		t.Fatal("provision_stack must be ModeJob: creation is an event, not desired state")
	}
}

func TestProvisionStackSpecRejectsMalformedPayload(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		payload json.RawMessage
	}{
		{name: "not json", payload: json.RawMessage(`{`)},
		{name: "empty stack", payload: json.RawMessage(`{"stack_id":""}`)},
		{name: "prefixed stack", payload: json.RawMessage(`{"stack_id":"stack:stack_abc"}`)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := ProvisionStackSpec.Key(test.payload); err == nil {
				t.Fatal("Key accepted a malformed payload")
			}
		})
	}
}

func TestProvisionStackHandlerChainsExactlyOneReadyRequest(t *testing.T) {
	t.Parallel()

	authorizer := &recordingAuthorizer{}
	service := NewService(Service{Authorizer: authorizer})
	handler := NewProvisionStackHandler(service)

	followUps, err := handler.Deliver(context.Background(), queue.Item{
		Kind:         KindProvisionStack,
		Payload:      provisionPayload(t, "stack_abc", "tenant_1", provisioningSubject),
		ActorSubject: provisioningSubject,
		TenantID:     "tenant_1",
	})
	if err != nil {
		t.Fatalf("Deliver returned error: %v", err)
	}
	if authorizer.calls != 1 {
		t.Fatalf("authorization writes = %d, want 1", authorizer.calls)
	}
	if len(followUps) != 1 || followUps[0].Kind != KindMarkStackReady {
		t.Fatalf("follow-ups = %+v, want exactly one mark_stack_ready", followUps)
	}

	var followUp MarkStackReadyPayload
	if err := json.Unmarshal(followUps[0].Payload, &followUp); err != nil {
		t.Fatalf("decode follow-up payload: %v", err)
	}
	if followUp.StackID != "stack_abc" || followUp.TenantID != "tenant_1" {
		t.Fatalf("follow-up payload = %#v, want stack_abc in tenant_1", followUp)
	}
}

func TestProvisionStackHandlerChainsNothingWhenTheWriteFails(t *testing.T) {
	t.Parallel()

	authorizer := &recordingAuthorizer{writeErr: authz.ErrUnavailable}
	handler := NewProvisionStackHandler(NewService(Service{Authorizer: authorizer}))

	followUps, err := handler.Deliver(context.Background(), queue.Item{
		Payload: provisionPayload(t, "stack_abc", "tenant_1", provisioningSubject),
	})
	if err == nil {
		t.Fatal("Deliver swallowed the write failure — the item must be retried")
	}
	if len(followUps) != 0 {
		t.Fatalf("follow-ups = %+v, want none: nothing may announce work that failed", followUps)
	}
}

func TestProvisionStackIsIdempotentWhenOwnerAlreadyGranted(t *testing.T) {
	t.Parallel()

	authorizer := &recordingAuthorizer{grants: []authz.Grant{mustOwnerGrant(t, "stack_abc", provisioningSubject)}}
	handler := NewProvisionStackHandler(NewService(Service{Authorizer: authorizer}))

	for attempt := 0; attempt < 2; attempt++ {
		if _, err := handler.Deliver(context.Background(), queue.Item{
			Payload: provisionPayload(t, "stack_abc", "tenant_1", provisioningSubject),
		}); err != nil {
			t.Fatalf("Deliver attempt %d returned error: %v", attempt, err)
		}
	}
	if authorizer.calls != 0 {
		t.Fatalf("authorization writes = %d, want 0 — a replay must not rewrite the tuple", authorizer.calls)
	}
}
