package activities

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/vishu42/megagega/internal/traits"
)

func TestRecordTemplateRunStatusDelegatesToRecorder(t *testing.T) {
	t.Parallel()

	recorder := &recordingStatusRecorder{}
	activities := NewTemplateRunActivities(recorder)
	input := traits.TemplateRunStatusActivityInput{
		RunID:           traits.TemplateRunID("run_123"),
		TenantID:        traits.TenantID("tenant_123"),
		StackTemplateID: traits.StackTemplateID("stack_template_123"),
		Operation:       traits.OperationApply,
		Status:          traits.TemplateRunPlanned,
	}

	if err := activities.RecordTemplateRunStatus(context.Background(), input); err != nil {
		t.Fatalf("RecordTemplateRunStatus returned error: %v", err)
	}

	if !reflect.DeepEqual(recorder.input, input) {
		t.Fatalf("recorded input = %#v, want %#v", recorder.input, input)
	}
}

func TestRecordTemplateRunStatusWrapsRecorderError(t *testing.T) {
	t.Parallel()

	recorderErr := errors.New("database unavailable")
	activities := NewTemplateRunActivities(&recordingStatusRecorder{err: recorderErr})

	err := activities.RecordTemplateRunStatus(context.Background(), traits.TemplateRunStatusActivityInput{
		RunID:    traits.TemplateRunID("run_123"),
		TenantID: traits.TenantID("tenant_123"),
		Status:   traits.TemplateRunFailed,
	})
	if !errors.Is(err, recorderErr) {
		t.Fatalf("error = %v, want recorderErr", err)
	}
	if !strings.Contains(err.Error(), "record template run status") {
		t.Fatalf("error = %q, want status context", err.Error())
	}
}

type recordingStatusRecorder struct {
	input traits.TemplateRunStatusActivityInput
	err   error
}

func (recorder *recordingStatusRecorder) RecordTemplateRunStatus(_ context.Context, input traits.TemplateRunStatusActivityInput) error {
	recorder.input = input
	return recorder.err
}
