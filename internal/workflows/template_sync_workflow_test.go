package workflows

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/vishu42/tflive/internal/domain"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
)

func TestTemplateSyncWorkflowRecordsCompletedRegistration(t *testing.T) {
	t.Parallel()

	env := newTemplateSyncWorkflowTestEnvironment(t)
	input := templateSyncWorkflowInput()
	var statuses []domain.TemplateRegistrationStatus
	env.OnActivity(domain.RecordTemplateRegistrationStatusActivityName, mock.Anything, mock.Anything).
		Return(func(_ context.Context, activityInput domain.TemplateRegistrationStatusActivityInput) error {
			statuses = append(statuses, activityInput.Status)
			if activityInput.RegistrationID != input.RegistrationID {
				t.Fatalf("registration ID = %q, want %q", activityInput.RegistrationID, input.RegistrationID)
			}
			return nil
		})
	env.OnActivity(domain.SyncTemplateActivityName, mock.Anything, mock.Anything).
		Return(func(_ context.Context, activityInput domain.TemplateSyncActivityInput) (domain.TemplateSyncActivityOutput, error) {
			if activityInput.SourceRef != "v0.0.1" {
				t.Fatalf("source ref = %q, want v0.0.1", activityInput.SourceRef)
			}
			return domain.TemplateSyncActivityOutput{
				Status:             domain.TemplateRegistrationCompleted,
				TemplateRevisionID: domain.TemplateRevisionID("template_123"),
				ResolvedCommitSHA:  "abc123",
			}, nil
		})

	env.ExecuteWorkflow(TemplateSyncWorkflow, input)

	assertWorkflowCompleted(t, env)
	want := []domain.TemplateRegistrationStatus{
		domain.TemplateRegistrationRunning,
		domain.TemplateRegistrationCompleted,
	}
	if !reflect.DeepEqual(statuses, want) {
		t.Fatalf("statuses = %#v, want %#v", statuses, want)
	}
}

func TestTemplateSyncWorkflowRecordsInvalidRegistration(t *testing.T) {
	t.Parallel()

	env := newTemplateSyncWorkflowTestEnvironment(t)
	input := templateSyncWorkflowInput()
	var terminal domain.TemplateRegistrationStatusActivityInput
	env.OnActivity(domain.RecordTemplateRegistrationStatusActivityName, mock.Anything, mock.Anything).
		Return(func(_ context.Context, activityInput domain.TemplateRegistrationStatusActivityInput) error {
			terminal = activityInput
			return nil
		})
	env.OnActivity(domain.SyncTemplateActivityName, mock.Anything, mock.Anything).
		Return(domain.TemplateSyncActivityOutput{
			Status:       domain.TemplateRegistrationInvalid,
			ErrorSummary: "sensitive variables are not supported: password",
		}, nil)

	env.ExecuteWorkflow(TemplateSyncWorkflow, input)

	assertWorkflowCompleted(t, env)
	if terminal.Status != domain.TemplateRegistrationInvalid {
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
	var statuses []domain.TemplateRegistrationStatus
	env.OnActivity(domain.RecordTemplateRegistrationStatusActivityName, mock.Anything, mock.Anything).
		Return(func(_ context.Context, activityInput domain.TemplateRegistrationStatusActivityInput) error {
			statuses = append(statuses, activityInput.Status)
			return nil
		})
	env.OnActivity(domain.SyncTemplateActivityName, mock.Anything, mock.Anything).
		Return(domain.TemplateSyncActivityOutput{}, syncErr)

	env.ExecuteWorkflow(TemplateSyncWorkflow, input)

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow did not complete")
	}
	if err := env.GetWorkflowError(); err == nil {
		t.Fatal("workflow error is nil, want sync error")
	}
	want := []domain.TemplateRegistrationStatus{
		domain.TemplateRegistrationRunning,
		domain.TemplateRegistrationFailed,
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
		func(context.Context, domain.TemplateRegistrationStatusActivityInput) error {
			return nil
		},
		activity.RegisterOptions{Name: domain.RecordTemplateRegistrationStatusActivityName},
	)
	env.RegisterActivityWithOptions(
		func(context.Context, domain.TemplateSyncActivityInput) (domain.TemplateSyncActivityOutput, error) {
			return domain.TemplateSyncActivityOutput{}, nil
		},
		activity.RegisterOptions{Name: domain.SyncTemplateActivityName},
	)
	return env
}

func templateSyncWorkflowInput() domain.TemplateSyncWorkflowInput {
	return domain.TemplateSyncWorkflowInput{
		RegistrationID: domain.TemplateRegistrationID("template_registration_123"),
		TenantID:       domain.TenantID("tenant_123"),
		RepoOwner:      "acme",
		RepoName:       "infra-templates",
		SourceRef:      "v0.0.1",
		RootPath:       "modules/vpc",
	}
}
