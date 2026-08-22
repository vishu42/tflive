package app

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/vishu42/tflive/internal/authz"
	"github.com/vishu42/tflive/internal/queue"
	"github.com/vishu42/tflive/internal/traits"
)

// KindGrantStackOwner grants the founding owner their role on a new stack. It
// touches only OpenFGA; the Postgres half of provisioning is its follow-up,
// KindMarkStackReady.
const KindGrantStackOwner queue.Kind = "grant_stack_owner"

// GrantStackOwnerPayload is the founding owner grant for a freshly created
// stack. Unlike a role change it is an event, not desired state: the subject
// is whoever created the stack, and nothing later may overwrite it.
type GrantStackOwnerPayload struct {
	StackID  string `json:"stack_id"`
	TenantID string `json:"tenant_id"`
	Subject  string `json:"subject"`
}

// GrantStackOwnerSpec declares the grant_stack_owner kind.
//
// ModeJob, even though reconcile_stack_grant names the same domain and is
// ModeReconcile. The difference is what the payload means: a role change is
// desired state, so the newest one should win, while the founding grant is an
// event that happened once. The "do nothing" on a duplicate key protects the
// recorded owner from being overwritten by any later enqueue on this stack,
// which a reconcile's "do update" would not.
//
// The key is "stack:<id>", built from the canonical formatter so it cannot
// drift from the identity OpenFGA uses. Sharing that key with
// mark_stack_ready is not a collision: the unique index is on (kind,
// resource_key), so each kind is only mutually excluded against itself.
//
// Key derivation is a frozen contract; see queue.Spec.
var GrantStackOwnerSpec = queue.Spec{
	Kind: KindGrantStackOwner,
	Mode: queue.ModeJob,
	Key:  grantStackOwnerKey,
}

func grantStackOwnerKey(payload json.RawMessage) (string, error) {
	var parsed GrantStackOwnerPayload
	if err := json.Unmarshal(payload, &parsed); err != nil {
		return "", fmt.Errorf("decode grant stack owner payload: %w", err)
	}
	stack, err := authz.ObjectFromID(authz.TypeStack, parsed.StackID)
	if err != nil {
		return "", fmt.Errorf("parse grant stack owner stack: %w", err)
	}
	return stack.String(), nil
}

// GrantStackOwnerHandler is a thin adapter over Service.GrantStackOwner, keeping
// the logic where the Authorizer already lives.
type GrantStackOwnerHandler struct {
	provisioner StackProvisioner
}

func NewGrantStackOwnerHandler(provisioner StackProvisioner) *GrantStackOwnerHandler {
	return &GrantStackOwnerHandler{provisioner: provisioner}
}

func (handler *GrantStackOwnerHandler) Spec() queue.Spec { return GrantStackOwnerSpec }

// Deliver writes the owner tuple and then chains the status flip. The chain is
// a returned request rather than an enqueue here, so the ordering — access
// first, label second — is visible in the handler's signature.
func (handler *GrantStackOwnerHandler) Deliver(ctx context.Context, item queue.Item) ([]queue.Request, error) {
	var parsed GrantStackOwnerPayload
	if err := json.Unmarshal(item.Payload, &parsed); err != nil {
		return nil, fmt.Errorf("decode grant stack owner payload: %w", err)
	}

	if err := handler.provisioner.GrantStackOwner(ctx, GrantStackOwnerCommand{
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
