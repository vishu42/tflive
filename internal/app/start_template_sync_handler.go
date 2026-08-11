package app

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/vishu42/tflive/internal/queue"
	"github.com/vishu42/tflive/internal/traits"
)

const KindStartTemplateSync queue.Kind = "start_template_sync"

// StartTemplateSyncPayload is the complete input required to start template sync.
// It intentionally has no JSON tags: persisted JSON uses its Go field names.
type StartTemplateSyncPayload traits.TemplateSyncWorkflowInput

var StartTemplateSyncSpec = queue.Spec{Kind: KindStartTemplateSync, Mode: queue.ModeJob, Key: startTemplateSyncKey}

func startTemplateSyncKey(payload json.RawMessage) (string, error) {
	var parsed StartTemplateSyncPayload
	if err := json.Unmarshal(payload, &parsed); err != nil {
		return "", fmt.Errorf("decode start template sync payload: %w", err)
	}
	if parsed.TenantID == "" || parsed.RegistrationID == "" {
		return "", fmt.Errorf("decode start template sync payload: tenant ID and registration ID are required")
	}
	return "registration:" + string(parsed.TenantID) + "/" + string(parsed.RegistrationID), nil
}

type StartTemplateSyncHandler struct{ dispatcher WorkflowDispatcher }

func NewStartTemplateSyncHandler(dispatcher WorkflowDispatcher) *StartTemplateSyncHandler {
	return &StartTemplateSyncHandler{dispatcher: dispatcher}
}

func (handler *StartTemplateSyncHandler) Spec() queue.Spec { return StartTemplateSyncSpec }

func (handler *StartTemplateSyncHandler) Deliver(ctx context.Context, item queue.Item) ([]queue.Request, error) {
	var payload StartTemplateSyncPayload
	if err := json.Unmarshal(item.Payload, &payload); err != nil {
		return nil, fmt.Errorf("decode start template sync payload: %w", err)
	}
	if err := handler.dispatcher.StartTemplateSync(ctx, traits.TemplateSyncWorkflowInput(payload)); err != nil {
		return nil, err
	}
	return nil, nil
}
