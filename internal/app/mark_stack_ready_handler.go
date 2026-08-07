package app

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/vishu42/tflive/internal/authz"
	"github.com/vishu42/tflive/internal/queue"
	"github.com/vishu42/tflive/internal/traits"
)

// KindMarkStackReady records that a stack has finished provisioning. It is the
// Postgres half of the chain KindGrantStackOwner starts, split out so that each
// handler touches exactly one system.
const KindMarkStackReady queue.Kind = "mark_stack_ready"

// MarkStackReadyPayload names the stack whose provisioning has landed.
type MarkStackReadyPayload struct {
	StackID  string `json:"stack_id"`
	TenantID string `json:"tenant_id"`
}

// MarkStackReadySpec declares the mark_stack_ready kind. ModeJob with a
// resource key: the flip happens exactly once and re-running it is harmless.
//
// Key derivation is a frozen contract; see queue.Spec.
var MarkStackReadySpec = queue.Spec{
	Kind: KindMarkStackReady,
	Mode: queue.ModeJob,
	Key:  markStackReadyKey,
}

func markStackReadyKey(payload json.RawMessage) (string, error) {
	var parsed MarkStackReadyPayload
	if err := json.Unmarshal(payload, &parsed); err != nil {
		return "", fmt.Errorf("decode mark stack ready payload: %w", err)
	}
	stack, err := authz.StackFromID(parsed.StackID)
	if err != nil {
		return "", fmt.Errorf("parse mark ready stack: %w", err)
	}
	return stack.String(), nil
}

// MarkStackReadyHandler touches only Postgres and chains nothing.
type MarkStackReadyHandler struct {
	provisioner StackProvisioner
}

func NewMarkStackReadyHandler(provisioner StackProvisioner) *MarkStackReadyHandler {
	return &MarkStackReadyHandler{provisioner: provisioner}
}

func (handler *MarkStackReadyHandler) Spec() queue.Spec { return MarkStackReadySpec }

func (handler *MarkStackReadyHandler) Deliver(ctx context.Context, item queue.Item) ([]queue.Request, error) {
	var parsed MarkStackReadyPayload
	if err := json.Unmarshal(item.Payload, &parsed); err != nil {
		return nil, fmt.Errorf("decode mark stack ready payload: %w", err)
	}
	if err := handler.provisioner.MarkStackReady(ctx, traits.TenantID(parsed.TenantID), traits.StackID(parsed.StackID)); err != nil {
		return nil, err
	}
	return nil, nil
}
