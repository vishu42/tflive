package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"go.temporal.io/api/serviceerror"

	"github.com/vishu42/tflive/internal/queue"
	"github.com/vishu42/tflive/internal/traits"
)

const KindSignalRunCancellation queue.Kind = "signal_run_cancellation"

// SignalRunCancellationPayload carries the complete cancellation event for one run.
// It intentionally has no JSON tags: persisted JSON uses its Go field names.
type SignalRunCancellationPayload struct {
	TenantID traits.TenantID
	RunID    traits.TemplateRunID
	Signal   traits.CancelSignal
}

// TemplateRunCancellationReconciler resolves a persisted cancellation request
// after Temporal reports the run is already closed.
type TemplateRunCancellationReconciler interface {
	ReconcileTemplateRunCancellation(context.Context, traits.TenantID, traits.TemplateRunID, string) error
}

var SignalRunCancellationSpec = queue.Spec{Kind: KindSignalRunCancellation, Mode: queue.ModeJob, Key: signalRunCancellationKey}

func signalRunCancellationKey(payload json.RawMessage) (string, error) {
	var parsed SignalRunCancellationPayload
	if err := json.Unmarshal(payload, &parsed); err != nil {
		return "", fmt.Errorf("decode signal run cancellation payload: %w", err)
	}
	if parsed.TenantID == "" || parsed.RunID == "" {
		return "", fmt.Errorf("decode signal run cancellation payload: tenant ID and run ID are required")
	}
	return "run:" + string(parsed.TenantID) + "/" + string(parsed.RunID), nil
}

type SignalRunCancellationHandler struct {
	dispatcher WorkflowDispatcher
	reconciler TemplateRunCancellationReconciler
}

func NewSignalRunCancellationHandler(dispatcher WorkflowDispatcher, reconciler TemplateRunCancellationReconciler) *SignalRunCancellationHandler {
	return &SignalRunCancellationHandler{dispatcher: dispatcher, reconciler: reconciler}
}

func (handler *SignalRunCancellationHandler) Spec() queue.Spec { return SignalRunCancellationSpec }

func (handler *SignalRunCancellationHandler) Deliver(ctx context.Context, item queue.Item) ([]queue.Request, error) {
	var payload SignalRunCancellationPayload
	if err := json.Unmarshal(item.Payload, &payload); err != nil {
		return nil, fmt.Errorf("decode signal run cancellation payload: %w", err)
	}
	err := handler.dispatcher.CancelTemplateRun(ctx, payload.TenantID, payload.RunID, payload.Signal)
	if err == nil {
		return nil, nil
	}
	if !isWorkflowClosedError(err) {
		return nil, err
	}
	if err := handler.reconciler.ReconcileTemplateRunCancellation(ctx, payload.TenantID, payload.RunID, "workflow closed before cancellation was processed"); err != nil {
		return nil, err
	}
	return nil, nil
}

func isWorkflowClosedError(err error) bool {
	var notFound *serviceerror.NotFound
	return errors.As(err, &notFound)
}
