package workflows

import (
	"context"
	"reflect"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/vishu42/megagega/internal/traits"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
)

func TestTemplateRunWorkflowRecordsPlanStatuses(t *testing.T) {
	t.Parallel()

	env := newTemplateRunWorkflowTestEnvironment(t)
	input := templateRunWorkflowInput(traits.OperationPlan)
	var statuses []traits.TemplateRunStatus
	env.OnActivity(traits.RecordTemplateRunStatusActivityName, mock.Anything, mock.Anything).
		Return(func(_ context.Context, activityInput traits.TemplateRunStatusActivityInput) error {
			statuses = append(statuses, activityInput.Status)
			return nil
		})

	env.ExecuteWorkflow(TemplateRunWorkflow, input)

	assertWorkflowCompleted(t, env)
	want := []traits.TemplateRunStatus{
		traits.TemplateRunLocked,
		traits.TemplateRunWorkspacePrepared,
		traits.TemplateRunSourceFetched,
		traits.TemplateRunInit,
		traits.TemplateRunWorkspaceSelected,
		traits.TemplateRunPlanned,
		traits.TemplateRunLockReleased,
		traits.TemplateRunCompleted,
	}
	if !reflect.DeepEqual(statuses, want) {
		t.Fatalf("statuses = %#v, want %#v", statuses, want)
	}
}

func TestTemplateRunWorkflowWaitsForApplyApproval(t *testing.T) {
	t.Parallel()

	env := newTemplateRunWorkflowTestEnvironment(t)
	input := templateRunWorkflowInput(traits.OperationApply)
	var statuses []traits.TemplateRunStatus
	env.OnActivity(traits.RecordTemplateRunStatusActivityName, mock.Anything, mock.Anything).
		Return(func(_ context.Context, activityInput traits.TemplateRunStatusActivityInput) error {
			statuses = append(statuses, activityInput.Status)
			return nil
		})
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(traits.ApprovalSignalName, traits.ApprovalSignal{
			ApprovedBy: traits.UserID("user_123"),
		})
	}, 0)

	env.ExecuteWorkflow(TemplateRunWorkflow, input)

	assertWorkflowCompleted(t, env)
	want := []traits.TemplateRunStatus{
		traits.TemplateRunLocked,
		traits.TemplateRunWorkspacePrepared,
		traits.TemplateRunSourceFetched,
		traits.TemplateRunInit,
		traits.TemplateRunWorkspaceSelected,
		traits.TemplateRunPlanned,
		traits.TemplateRunWaitingApproval,
		traits.TemplateRunApproved,
		traits.TemplateRunApplyStarted,
		traits.TemplateRunApplied,
		traits.TemplateRunLockReleased,
		traits.TemplateRunCompleted,
	}
	if !reflect.DeepEqual(statuses, want) {
		t.Fatalf("statuses = %#v, want %#v", statuses, want)
	}
}

func TestTemplateRunWorkflowCancelsApplyWhileWaitingApproval(t *testing.T) {
	t.Parallel()

	env := newTemplateRunWorkflowTestEnvironment(t)
	input := templateRunWorkflowInput(traits.OperationApply)
	var statuses []traits.TemplateRunStatus
	env.OnActivity(traits.RecordTemplateRunStatusActivityName, mock.Anything, mock.Anything).
		Return(func(_ context.Context, activityInput traits.TemplateRunStatusActivityInput) error {
			statuses = append(statuses, activityInput.Status)
			return nil
		})
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(traits.CancelSignalName, traits.CancelSignal{
			RequestedBy: traits.UserID("user_456"),
			Reason:      "superseded",
		})
	}, 0)

	env.ExecuteWorkflow(TemplateRunWorkflow, input)

	assertWorkflowCompleted(t, env)
	want := []traits.TemplateRunStatus{
		traits.TemplateRunLocked,
		traits.TemplateRunWorkspacePrepared,
		traits.TemplateRunSourceFetched,
		traits.TemplateRunInit,
		traits.TemplateRunWorkspaceSelected,
		traits.TemplateRunPlanned,
		traits.TemplateRunWaitingApproval,
		traits.TemplateRunCancelRequested,
		traits.TemplateRunCanceling,
		traits.TemplateRunCanceled,
		traits.TemplateRunLockReleased,
	}
	if !reflect.DeepEqual(statuses, want) {
		t.Fatalf("statuses = %#v, want %#v", statuses, want)
	}
}

func TestTemplateRunWorkflowRecordsDestroyStatuses(t *testing.T) {
	t.Parallel()

	env := newTemplateRunWorkflowTestEnvironment(t)
	input := templateRunWorkflowInput(traits.OperationDestroy)
	var statuses []traits.TemplateRunStatus
	env.OnActivity(traits.RecordTemplateRunStatusActivityName, mock.Anything, mock.Anything).
		Return(func(_ context.Context, activityInput traits.TemplateRunStatusActivityInput) error {
			statuses = append(statuses, activityInput.Status)
			return nil
		})

	env.ExecuteWorkflow(TemplateRunWorkflow, input)

	assertWorkflowCompleted(t, env)
	want := []traits.TemplateRunStatus{
		traits.TemplateRunLocked,
		traits.TemplateRunWorkspacePrepared,
		traits.TemplateRunSourceFetched,
		traits.TemplateRunInit,
		traits.TemplateRunWorkspaceSelected,
		traits.TemplateRunDestroyStarted,
		traits.TemplateRunDestroyed,
		traits.TemplateRunLockReleased,
		traits.TemplateRunCompleted,
	}
	if !reflect.DeepEqual(statuses, want) {
		t.Fatalf("statuses = %#v, want %#v", statuses, want)
	}
}

func newTemplateRunWorkflowTestEnvironment(t *testing.T) *testsuite.TestWorkflowEnvironment {
	t.Helper()

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(TemplateRunWorkflow)
	env.RegisterActivityWithOptions(
		func(context.Context, traits.TemplateRunStatusActivityInput) error {
			return nil
		},
		activity.RegisterOptions{Name: traits.RecordTemplateRunStatusActivityName},
	)
	return env
}

func templateRunWorkflowInput(operation traits.OperationType) traits.TemplateRunWorkflowInput {
	return traits.TemplateRunWorkflowInput{
		RunID:           traits.TemplateRunID("run_123"),
		TenantID:        traits.TenantID("tenant_123"),
		StackTemplateID: traits.StackTemplateID("stack_template_123"),
		Operation:       operation,
		SelectedRef:     "main",
		WorkspaceName:   "mtp_acme_prod_vpc_a13f9c",
	}
}

func assertWorkflowCompleted(t *testing.T, env *testsuite.TestWorkflowEnvironment) {
	t.Helper()

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow did not complete")
	}
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow returned error: %v", err)
	}
}
