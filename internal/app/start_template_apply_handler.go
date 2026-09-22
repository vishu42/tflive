package app

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/vishu42/tflive/internal/domain"
	"github.com/vishu42/tflive/internal/queue"
)

const KindStartTemplateApply queue.Kind = "start_template_apply"

// StartTemplateApplyPayload is the complete input for the workflow that applies
// an approved run's saved plan. It intentionally has no JSON tags: persisted
// JSON uses its Go field names.
type StartTemplateApplyPayload domain.TemplateRunWorkflowInput

var StartTemplateApplySpec = queue.Spec{Kind: KindStartTemplateApply, Mode: queue.ModeJob, Key: startTemplateApplyKey}

func startTemplateApplyKey(payload json.RawMessage) (string, error) {
	var parsed StartTemplateApplyPayload
	if err := json.Unmarshal(payload, &parsed); err != nil {
		return "", fmt.Errorf("decode start template apply payload: %w", err)
	}
	if parsed.TenantID == "" || parsed.RunID == "" {
		return "", fmt.Errorf("decode start template apply payload: tenant ID and run ID are required")
	}
	return "run:" + string(parsed.TenantID) + "/" + string(parsed.RunID), nil
}

// StartTemplateApplyHandler starts the apply workflow for an approved run.
// Starting is idempotent on the workflow ID, so a redelivered intent finds the
// workflow already started rather than starting a second one.
type StartTemplateApplyHandler struct{ dispatcher WorkflowDispatcher }

func NewStartTemplateApplyHandler(dispatcher WorkflowDispatcher) *StartTemplateApplyHandler {
	return &StartTemplateApplyHandler{dispatcher: dispatcher}
}

func (handler *StartTemplateApplyHandler) Spec() queue.Spec { return StartTemplateApplySpec }

func (handler *StartTemplateApplyHandler) Deliver(ctx context.Context, item queue.Item) ([]queue.Request, error) {
	var payload StartTemplateApplyPayload
	if err := json.Unmarshal(item.Payload, &payload); err != nil {
		return nil, fmt.Errorf("decode start template apply payload: %w", err)
	}
	if err := handler.dispatcher.StartTemplateApply(ctx, domain.TemplateRunWorkflowInput(payload)); err != nil {
		return nil, err
	}
	return nil, nil
}
