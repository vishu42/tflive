package app

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/vishu42/tflive/internal/authz"
	"github.com/vishu42/tflive/internal/queue"
	"github.com/vishu42/tflive/internal/traits"
)

// KindProvisionStack grants the founding owner their role on a new stack. It
// touches only OpenFGA; the Postgres half of provisioning is its follow-up,
// KindMarkStackReady.
const KindProvisionStack queue.Kind = "provision_stack"

// ProvisionStackPayload is the founding owner grant for a freshly created
// stack. Unlike a role change it is an event, not desired state: the subject
// is whoever created the stack, and nothing later may overwrite it.
type ProvisionStackPayload struct {
	StackID  string `json:"stack_id"`
	TenantID string `json:"tenant_id"`
	Subject  string `json:"subject"`
}

// ProvisionStackSpec declares the provision_stack kind.
//
// ModeJob, not ModeReconcile: provisioning is an event, and the "do nothing"
// on a duplicate key protects the recorded owner from being overwritten by any
// later enqueue, which a reconcile's "do update" would not.
//
// The key is "stack:<id>", built from the canonical formatter so it cannot
// drift from the identity OpenFGA uses. Sharing that key with
// mark_stack_ready is not a collision: the unique index is on (kind,
// ordering_key), so each kind is only mutually excluded against itself.
//
// Key derivation is a frozen contract; see queue.Spec.
var ProvisionStackSpec = queue.Spec{
	Kind: KindProvisionStack,
	Mode: queue.ModeJob,
	Key:  provisionStackKey,
}

func provisionStackKey(payload json.RawMessage) (string, error) {
	var parsed ProvisionStackPayload
	if err := json.Unmarshal(payload, &parsed); err != nil {
		return "", fmt.Errorf("decode provision stack payload: %w", err)
	}
	stack, err := authz.StackFromID(parsed.StackID)
	if err != nil {
		return "", fmt.Errorf("parse provision stack: %w", err)
	}
	return stack.String(), nil
}

// ProvisionStackHandler is a thin adapter over Service.ProvisionStack, keeping
// the logic where the Authorizer already lives.
type ProvisionStackHandler struct {
	provisioner StackProvisioner
}

func NewProvisionStackHandler(provisioner StackProvisioner) *ProvisionStackHandler {
	return &ProvisionStackHandler{provisioner: provisioner}
}

func (handler *ProvisionStackHandler) Spec() queue.Spec { return ProvisionStackSpec }

// Deliver writes the owner tuple and then chains the status flip. The chain is
// a returned request rather than an enqueue here, so the ordering — access
// first, label second — is visible in the handler's signature.
func (handler *ProvisionStackHandler) Deliver(ctx context.Context, item queue.Item) ([]queue.Request, error) {
	var parsed ProvisionStackPayload
	if err := json.Unmarshal(item.Payload, &parsed); err != nil {
		return nil, fmt.Errorf("decode provision stack payload: %w", err)
	}

	if err := handler.provisioner.ProvisionStack(ctx, ProvisionStackCommand{
		TenantID: traits.TenantID(parsed.TenantID),
		StackID:  traits.StackID(parsed.StackID),
		Subject:  parsed.Subject,
	}); err != nil {
		return nil, err
	}

	followUp, err := json.Marshal(MarkStackReadyPayload{
		StackID:  parsed.StackID,
		TenantID: parsed.TenantID,
	})
	if err != nil {
		return nil, fmt.Errorf("encode mark stack ready payload: %w", err)
	}
	return []queue.Request{{
		Kind:         KindMarkStackReady,
		Payload:      followUp,
		ActorSubject: item.ActorSubject,
		TenantID:     item.TenantID,
	}}, nil
}
