package app

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"go.temporal.io/api/serviceerror"

	"github.com/vishu42/tflive/internal/queue"
	"github.com/vishu42/tflive/internal/traits"
)

type recordingWorkflowIntentDispatcher struct {
	startRunInput  traits.TemplateRunWorkflowInput
	startSyncInput traits.TemplateSyncWorkflowInput
	approval       traits.ApprovalSignal
	cancellation   traits.CancelSignal
	startRunErr    error
	startSyncErr   error
	approvalErr    error
	cancelErr      error
}

func (d *recordingWorkflowIntentDispatcher) StartTemplateRun(_ context.Context, input traits.TemplateRunWorkflowInput) error {
	d.startRunInput = input
	return d.startRunErr
}

func (d *recordingWorkflowIntentDispatcher) StartTemplateSync(_ context.Context, input traits.TemplateSyncWorkflowInput) error {
	d.startSyncInput = input
	return d.startSyncErr
}

func (d *recordingWorkflowIntentDispatcher) ApproveTemplateRun(_ context.Context, _ traits.TenantID, _ traits.TemplateRunID, signal traits.ApprovalSignal) error {
	d.approval = signal
	return d.approvalErr
}

func (d *recordingWorkflowIntentDispatcher) CancelTemplateRun(_ context.Context, _ traits.TenantID, _ traits.TemplateRunID, signal traits.CancelSignal) error {
	d.cancellation = signal
	return d.cancelErr
}

type recordingCancellationReconciler struct {
	err     error
	runID   traits.TemplateRunID
	summary string
}

func (r *recordingCancellationReconciler) ReconcileTemplateRunCancellation(_ context.Context, _ traits.TenantID, runID traits.TemplateRunID, summary string) error {
	r.runID = runID
	r.summary = summary
	return r.err
}

func TestStartTemplateRunHandlerDeliversCompleteInput(t *testing.T) {
	dispatcher := &recordingWorkflowIntentDispatcher{}
	handler := NewStartTemplateRunHandler(dispatcher)
	payload := StartTemplateRunPayload{RunID: "run_1", TenantID: "tenant_1", StackTemplateID: "template_1", Operation: "apply", SelectedRef: "main", WorkspaceName: "workspace", RepoOwner: "owner", RepoName: "repo", RootPath: "module", ConfigJSON: json.RawMessage(`{"x":true}`)}

	followUps, err := handler.Deliver(context.Background(), queue.Item{Payload: marshalWorkflowIntentPayload(t, payload)})
	if err != nil {
		t.Fatalf("Deliver() error = %v", err)
	}
	if len(followUps) != 0 {
		t.Fatalf("follow-ups = %+v, want none", followUps)
	}
	if got := dispatcher.startRunInput; got.RunID != payload.RunID || got.ConfigJSON == nil || string(got.ConfigJSON) != `{"x":true}` {
		t.Fatalf("dispatcher input = %+v, want complete payload", got)
	}
}

func TestStartTemplateAndSignalRunHandlersPropagateDispatcherErrors(t *testing.T) {
	want := errors.New("temporal unavailable")
	tests := []struct {
		name    string
		handler queue.Handler
		payload any
	}{
		{"start run", NewStartTemplateRunHandler(&recordingWorkflowIntentDispatcher{startRunErr: want}), StartTemplateRunPayload{}},
		{"start sync", NewStartTemplateSyncHandler(&recordingWorkflowIntentDispatcher{startSyncErr: want}), StartTemplateSyncPayload{}},
		{"approval", NewSignalRunApprovalHandler(&recordingWorkflowIntentDispatcher{approvalErr: want}), SignalRunApprovalPayload{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.handler.Deliver(context.Background(), queue.Item{Payload: marshalWorkflowIntentPayload(t, tt.payload)})
			if !errors.Is(err, want) {
				t.Fatalf("Deliver() error = %v, want %v", err, want)
			}
		})
	}
}

func TestSignalRunCancellationHandlerReconcilesNotFound(t *testing.T) {
	dispatcher := &recordingWorkflowIntentDispatcher{cancelErr: serviceerror.NewNotFound("closed")}
	reconciler := &recordingCancellationReconciler{}
	handler := NewSignalRunCancellationHandler(dispatcher, reconciler)
	payload := SignalRunCancellationPayload{TenantID: "tenant_1", RunID: "run_1", Signal: traits.CancelSignal{RequestedBy: "user_1", Reason: "stop"}}

	followUps, err := handler.Deliver(context.Background(), queue.Item{Payload: marshalWorkflowIntentPayload(t, payload)})
	if err != nil {
		t.Fatalf("Deliver() error = %v", err)
	}
	if len(followUps) != 0 || reconciler.runID != "run_1" || reconciler.summary == "" {
		t.Fatalf("follow-ups = %+v, reconciliation = %+v, want no follow-ups and reconciliation", followUps, reconciler)
	}
}

func TestSignalRunCancellationHandlerReturnsNonNotFoundAndReconciliationErrors(t *testing.T) {
	dispatcherErr := errors.New("dispatcher unavailable")
	handler := NewSignalRunCancellationHandler(&recordingWorkflowIntentDispatcher{cancelErr: dispatcherErr}, &recordingCancellationReconciler{})
	payload := marshalWorkflowIntentPayload(t, SignalRunCancellationPayload{})
	if _, err := handler.Deliver(context.Background(), queue.Item{Payload: payload}); !errors.Is(err, dispatcherErr) {
		t.Fatalf("Deliver() error = %v, want dispatcher error", err)
	}

	reconcileErr := errors.New("postgres unavailable")
	handler = NewSignalRunCancellationHandler(&recordingWorkflowIntentDispatcher{cancelErr: serviceerror.NewNotFound("closed")}, &recordingCancellationReconciler{err: reconcileErr})
	if _, err := handler.Deliver(context.Background(), queue.Item{Payload: payload}); !errors.Is(err, reconcileErr) {
		t.Fatalf("Deliver() error = %v, want reconciliation error", err)
	}
}

func TestStartTemplateAndSignalRunSpecsUseFrozenKeysAndRejectMalformedPayloads(t *testing.T) {
	tests := []struct {
		spec    queue.Spec
		payload any
		want    string
	}{
		{StartTemplateRunSpec, StartTemplateRunPayload{TenantID: "tenant_1", RunID: "run_1"}, "run:tenant_1/run_1"},
		{StartTemplateSyncSpec, StartTemplateSyncPayload{TenantID: "tenant_1", RegistrationID: "registration_1"}, "registration:tenant_1/registration_1"},
		{SignalRunApprovalSpec, SignalRunApprovalPayload{TenantID: "tenant_1", RunID: "run_1"}, "run:tenant_1/run_1"},
		{SignalRunCancellationSpec, SignalRunCancellationPayload{TenantID: "tenant_1", RunID: "run_1"}, "run:tenant_1/run_1"},
	}
	for _, tt := range tests {
		key, err := tt.spec.Key(marshalWorkflowIntentPayload(t, tt.payload))
		if err != nil || key != tt.want || tt.spec.Mode != queue.ModeJob {
			t.Fatalf("spec = %+v, key = %q, err = %v; want ModeJob and %q", tt.spec, key, err, tt.want)
		}
		if _, err := tt.spec.Key(json.RawMessage(`{`)); err == nil {
			t.Fatal("Key accepted malformed payload")
		}
	}
}

func marshalWorkflowIntentPayload(t *testing.T, payload any) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return encoded
}
