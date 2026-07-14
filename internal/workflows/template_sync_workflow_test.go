package workflows

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/vishu42/tflive/internal/traits"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
)

func TestTemplateSyncWorkflowRecordsCompletedRegistration(t *testing.T) {
	t.Parallel()

	env := newTemplateSyncWorkflowTestEnvironment(t)
	input := templateSyncWorkflowInput()
	var statuses []traits.TemplateRegistrationStatus
	env.OnActivity(traits.RecordTemplateRegistrationStatusActivityName, mock.Anything, mock.Anything).
		Return(func(_ context.Context, activityInput traits.TemplateRegistrationStatusActivityInput) error {
			statuses = append(statuses, activityInput.Status)
			if activityInput.RegistrationID != input.RegistrationID {
				t.Fatalf("registration ID = %q, want %q", activityInput.RegistrationID, input.RegistrationID)
			}
			return nil
		})
	env.OnActivity(traits.SyncTemplateActivityName, mock.Anything, mock.Anything).
		Return(func(_ context.Context, activityInput traits.TemplateSyncActivityInput) (traits.TemplateSyncActivityOutput, error) {
			if activityInput.SourceRef != "v0.0.1" {
				t.Fatalf("source ref = %q, want v0.0.1", activityInput.SourceRef)
			}
			return traits.TemplateSyncActivityOutput{
				Status:             traits.TemplateRegistrationCompleted,
				TemplateRevisionID: traits.TemplateRevisionID("template_123"),
				ResolvedCommitSHA:  "abc123",
			}, nil
		})

	env.ExecuteWorkflow(TemplateSyncWorkflow, input)

	assertWorkflowCompleted(t, env)
	want := []traits.TemplateRegistrationStatus{
		traits.TemplateRegistrationRunning,
		traits.TemplateRegistrationCompleted,
	}
	if !reflect.DeepEqual(statuses, want) {
		t.Fatalf("statuses = %#v, want %#v", statuses, want)
	}
}

func TestTemplateSyncWorkflowRecordsInvalidRegistration(t *testing.T) {
	t.Parallel()

	env := newTemplateSyncWorkflowTestEnvironment(t)
	input := templateSyncWorkflowInput()
	var terminal traits.TemplateRegistrationStatusActivityInput
	env.OnActivity(traits.RecordTemplateRegistrationStatusActivityName, mock.Anything, mock.Anything).
		Return(func(_ context.Context, activityInput traits.TemplateRegistrationStatusActivityInput) error {
			terminal = activityInput
			return nil
		})
	env.OnActivity(traits.SyncTemplateActivityName, mock.Anything, mock.Anything).
		Return(traits.TemplateSyncActivityOutput{
			Status:       traits.TemplateRegistrationInvalid,
			ErrorSummary: "sensitive variables are not supported: password",
		}, nil)

	env.ExecuteWorkflow(TemplateSyncWorkflow, input)

	assertWorkflowCompleted(t, env)
	if terminal.Status != traits.TemplateRegistrationInvalid {
		t.Fatalf("terminal status = %q, want invalid", terminal.Status)
	}
	if terminal.ErrorSummary != "sensitive variables are not supported: password" {
		t.Fatalf("error summary = %q", terminal.ErrorSummary)
	}
}

func TestTemplateSyncWorkflowRecordsFailedRegistration(t *testing.T) {
	t.Parallel()

	env := newTemplateSyncWorkflowTestEnvironment(t)
	input := templateSyncWorkflowInput()
	syncErr := errors.New("clone process failed")
	var statuses []traits.TemplateRegistrationStatus
	env.OnActivity(traits.RecordTemplateRegistrationStatusActivityName, mock.Anything, mock.Anything).
		Return(func(_ context.Context, activityInput traits.TemplateRegistrationStatusActivityInput) error {
			statuses = append(statuses, activityInput.Status)
			return nil
		})
	env.OnActivity(traits.SyncTemplateActivityName, mock.Anything, mock.Anything).
		Return(traits.TemplateSyncActivityOutput{}, syncErr)

	env.ExecuteWorkflow(TemplateSyncWorkflow, input)

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow did not complete")
	}
	if err := env.GetWorkflowError(); err == nil {
		t.Fatal("workflow error is nil, want sync error")
	}
	want := []traits.TemplateRegistrationStatus{
		traits.TemplateRegistrationRunning,
		traits.TemplateRegistrationFailed,
	}
	if !reflect.DeepEqual(statuses, want) {
		t.Fatalf("statuses = %#v, want %#v", statuses, want)
	}
}

func newTemplateSyncWorkflowTestEnvironment(t *testing.T) *testsuite.TestWorkflowEnvironment {
	t.Helper()

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(TemplateSyncWorkflow)
	env.RegisterActivityWithOptions(
		func(context.Context, traits.TemplateRegistrationStatusActivityInput) error {
			return nil
		},
		activity.RegisterOptions{Name: traits.RecordTemplateRegistrationStatusActivityName},
	)
	env.RegisterActivityWithOptions(
		func(context.Context, traits.TemplateSyncActivityInput) (traits.TemplateSyncActivityOutput, error) {
			return traits.TemplateSyncActivityOutput{}, nil
		},
		activity.RegisterOptions{Name: traits.SyncTemplateActivityName},
	)
	return env
}

func templateSyncWorkflowInput() traits.TemplateSyncWorkflowInput {
	return traits.TemplateSyncWorkflowInput{
		RegistrationID: traits.TemplateRegistrationID("template_registration_123"),
		TenantID:       traits.TenantID("tenant_123"),
		RepoOwner:      "acme",
		RepoName:       "infra-templates",
		SourceRef:      "v0.0.1",
		RootPath:       "modules/vpc",
	}
}
