package app

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/vishu42/tflive/internal/domain"
	"github.com/vishu42/tflive/internal/queue"
)

const KindSignalRunApproval queue.Kind = "signal_run_approval"

// SignalRunApprovalPayload carries the complete approval event for one run.
// It intentionally has no JSON tags: persisted JSON uses its Go field names.
type SignalRunApprovalPayload struct {
	TenantID domain.TenantID
	RunID    domain.TemplateRunID
	Signal   domain.ApprovalSignal
}

var SignalRunApprovalSpec = queue.Spec{Kind: KindSignalRunApproval, Mode: queue.ModeJob, Key: signalRunApprovalKey}

func signalRunApprovalKey(payload json.RawMessage) (string, error) {
	var parsed SignalRunApprovalPayload
	if err := json.Unmarshal(payload, &parsed); err != nil {
		return "", fmt.Errorf("decode signal run approval payload: %w", err)
	}
	if parsed.TenantID == "" || parsed.RunID == "" {
		return "", fmt.Errorf("decode signal run approval payload: tenant ID and run ID are required")
	}
	return "run:" + string(parsed.TenantID) + "/" + string(parsed.RunID), nil
}

type SignalRunApprovalHandler struct{ dispatcher WorkflowDispatcher }

func NewSignalRunApprovalHandler(dispatcher WorkflowDispatcher) *SignalRunApprovalHandler {
	return &SignalRunApprovalHandler{dispatcher: dispatcher}
}

func (handler *SignalRunApprovalHandler) Spec() queue.Spec { return SignalRunApprovalSpec }

func (handler *SignalRunApprovalHandler) Deliver(ctx context.Context, item queue.Item) ([]queue.Request, error) {
	var payload SignalRunApprovalPayload
	if err := json.Unmarshal(item.Payload, &payload); err != nil {
		return nil, fmt.Errorf("decode signal run approval payload: %w", err)
	}
	if err := handler.dispatcher.ApproveTemplateRun(ctx, payload.TenantID, payload.RunID, payload.Signal); err != nil {
		return nil, err
	}
	return nil, nil
}
