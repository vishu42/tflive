package app

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/vishu42/tflive/internal/queue"
	"github.com/vishu42/tflive/internal/traits"
)

const KindStartTemplateRun queue.Kind = "start_template_run"

// StartTemplateRunPayload is the complete input required to start a template run.
// It intentionally has no JSON tags: persisted JSON uses its Go field names.
type StartTemplateRunPayload traits.TemplateRunWorkflowInput

var StartTemplateRunSpec = queue.Spec{Kind: KindStartTemplateRun, Mode: queue.ModeJob, Key: startTemplateRunKey}

func startTemplateRunKey(payload json.RawMessage) (string, error) {
	var parsed StartTemplateRunPayload
	if err := json.Unmarshal(payload, &parsed); err != nil {
		return "", fmt.Errorf("decode start template run payload: %w", err)
	}
	if parsed.TenantID == "" || parsed.RunID == "" {
		return "", fmt.Errorf("decode start template run payload: tenant ID and run ID are required")
	}
	return "run:" + string(parsed.TenantID) + "/" + string(parsed.RunID), nil
}

type StartTemplateRunHandler struct{ dispatcher WorkflowDispatcher }

func NewStartTemplateRunHandler(dispatcher WorkflowDispatcher) *StartTemplateRunHandler {
	return &StartTemplateRunHandler{dispatcher: dispatcher}
}

func (handler *StartTemplateRunHandler) Spec() queue.Spec { return StartTemplateRunSpec }

func (handler *StartTemplateRunHandler) Deliver(ctx context.Context, item queue.Item) ([]queue.Request, error) {
	var payload StartTemplateRunPayload
	if err := json.Unmarshal(item.Payload, &payload); err != nil {
		return nil, fmt.Errorf("decode start template run payload: %w", err)
	}
	if err := handler.dispatcher.StartTemplateRun(ctx, traits.TemplateRunWorkflowInput(payload)); err != nil {
		return nil, err
	}
	return nil, nil
}
