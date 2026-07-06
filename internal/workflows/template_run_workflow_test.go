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
	var events []string
	env.OnActivity(traits.PrepareWorkspaceActivityName, mock.Anything, mock.Anything).
		Return(func(_ context.Context, activityInput traits.PrepareWorkspaceActivityInput) (traits.PrepareWorkspaceActivityOutput, error) {
			if activityInput.RunID != input.RunID {
				t.Fatalf("prepare workspace RunID = %q, want %q", activityInput.RunID, input.RunID)
			}
			if activityInput.TenantID != input.TenantID {
				t.Fatalf("prepare workspace TenantID = %q, want %q", activityInput.TenantID, input.TenantID)
			}
			events = append(events, "prepare_workspace")
			return traits.PrepareWorkspaceActivityOutput{WorkspacePath: "/tmp/megagega/runs/tenant_123/run_123"}, nil
		})
	env.OnActivity(traits.RunTerraformActivityName, mock.Anything, mock.Anything).
		Return(func(_ context.Context, activityInput traits.RunTerraformActivityInput) error {
			if activityInput.RunID != input.RunID {
				t.Fatalf("run terraform RunID = %q, want %q", activityInput.RunID, input.RunID)
			}
			if activityInput.TenantID != input.TenantID {
				t.Fatalf("run terraform TenantID = %q, want %q", activityInput.TenantID, input.TenantID)
			}
			if activityInput.WorkspacePath != "/tmp/megagega/runs/tenant_123/run_123" {
				t.Fatalf("run terraform WorkspacePath = %q", activityInput.WorkspacePath)
			}
			if activityInput.WorkspaceName != input.WorkspaceName {
				t.Fatalf("run terraform WorkspaceName = %q, want %q", activityInput.WorkspaceName, input.WorkspaceName)
			}
			events = append(events, "terraform:"+string(activityInput.Command))
			return nil
		})
	env.OnActivity(traits.RecordTemplateRunStatusActivityName, mock.Anything, mock.Anything).
		Return(func(_ context.Context, activityInput traits.TemplateRunStatusActivityInput) error {
			events = append(events, string(activityInput.Status))
			return nil
		})

	env.ExecuteWorkflow(TemplateRunWorkflow, input)

	assertWorkflowCompleted(t, env)
	want := []string{
		string(traits.TemplateRunLocked),
		"prepare_workspace",
		string(traits.TemplateRunWorkspacePrepared),
		string(traits.TemplateRunSourceFetched),
		"terraform:" + string(traits.TerraformCommandInit),
		string(traits.TemplateRunInit),
		"terraform:" + string(traits.TerraformCommandSelectWorkspace),
		string(traits.TemplateRunWorkspaceSelected),
		"terraform:" + string(traits.TerraformCommandPlan),
		string(traits.TemplateRunPlanned),
		string(traits.TemplateRunLockReleased),
		string(traits.TemplateRunCompleted),
	}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %#v, want %#v", events, want)
	}
}

func TestTemplateRunWorkflowWaitsForApplyApproval(t *testing.T) {
	t.Parallel()

	env := newTemplateRunWorkflowTestEnvironment(t)
	input := templateRunWorkflowInput(traits.OperationApply)
	var statuses []traits.TemplateRunStatus
	var commands []traits.TerraformCommandType
	mockPrepareWorkspace(t, env)
	mockRunTerraform(t, env, &commands)
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
	wantCommands := []traits.TerraformCommandType{
		traits.TerraformCommandInit,
		traits.TerraformCommandSelectWorkspace,
		traits.TerraformCommandPlan,
		traits.TerraformCommandApply,
	}
	if !reflect.DeepEqual(commands, wantCommands) {
		t.Fatalf("commands = %#v, want %#v", commands, wantCommands)
	}
}

func TestTemplateRunWorkflowCancelsApplyWhileWaitingApproval(t *testing.T) {
	t.Parallel()

	env := newTemplateRunWorkflowTestEnvironment(t)
	input := templateRunWorkflowInput(traits.OperationApply)
	var statuses []traits.TemplateRunStatus
	var commands []traits.TerraformCommandType
	mockPrepareWorkspace(t, env)
	mockRunTerraform(t, env, &commands)
	env.OnActivity(traits.RecordTemplateRunStatusActivityName, mock.Anything, mock.Anything).
		Return(func(_ context.Context, activityInput traits.TemplateRunStatusActivityInput) error {
			statuses = append(statuses, activityInput.Status)
			if activityInput.Status == traits.TemplateRunWaitingApproval {
				env.SignalWorkflow(traits.CancelSignalName, traits.CancelSignal{
					RequestedBy: traits.UserID("user_456"),
					Reason:      "superseded",
				})
			}
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
		traits.TemplateRunWaitingApproval,
		traits.TemplateRunCancelRequested,
		traits.TemplateRunCanceling,
		traits.TemplateRunLockReleased,
		traits.TemplateRunCanceled,
	}
	if !reflect.DeepEqual(statuses, want) {
		t.Fatalf("statuses = %#v, want %#v", statuses, want)
	}
	wantCommands := []traits.TerraformCommandType{
		traits.TerraformCommandInit,
		traits.TerraformCommandSelectWorkspace,
		traits.TerraformCommandPlan,
	}
	if !reflect.DeepEqual(commands, wantCommands) {
		t.Fatalf("commands = %#v, want %#v", commands, wantCommands)
	}
}

func TestTemplateRunWorkflowCancelsPlanWhenSignalArrivesDuringTerraform(t *testing.T) {
	t.Parallel()

	env := newTemplateRunWorkflowTestEnvironment(t)
	input := templateRunWorkflowInput(traits.OperationPlan)
	var statuses []traits.TemplateRunStatus
	var commands []traits.TerraformCommandType
	mockPrepareWorkspace(t, env)
	mockRunTerraform(t, env, &commands)
	env.OnActivity(traits.RecordTemplateRunStatusActivityName, mock.Anything, mock.Anything).
		Return(func(_ context.Context, activityInput traits.TemplateRunStatusActivityInput) error {
			statuses = append(statuses, activityInput.Status)
			return nil
		})
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(traits.CancelSignalName, traits.CancelSignal{
			RequestedBy: traits.UserID("user_456"),
			Reason:      "stop retries",
		})
	}, 0)

	env.ExecuteWorkflow(TemplateRunWorkflow, input)

	assertWorkflowCompleted(t, env)
	if len(statuses) == 0 || statuses[len(statuses)-1] != traits.TemplateRunCanceled {
		t.Fatalf("final status = %#v, want canceled", statuses)
	}
	for _, status := range statuses {
		if status == traits.TemplateRunPlanned || status == traits.TemplateRunCompleted {
			t.Fatalf("statuses = %#v, canceled plan should not become planned or completed", statuses)
		}
	}
	if len(commands) > 1 {
		t.Fatalf("commands = %#v, cancel should stop before later terraform phases", commands)
	}
}

func TestTemplateRunWorkflowRecordsDestroyStatuses(t *testing.T) {
	t.Parallel()

	env := newTemplateRunWorkflowTestEnvironment(t)
	input := templateRunWorkflowInput(traits.OperationDestroy)
	var statuses []traits.TemplateRunStatus
	var commands []traits.TerraformCommandType
	mockPrepareWorkspace(t, env)
	mockRunTerraform(t, env, &commands)
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
	wantCommands := []traits.TerraformCommandType{
		traits.TerraformCommandInit,
		traits.TerraformCommandSelectWorkspace,
	}
	if !reflect.DeepEqual(commands, wantCommands) {
		t.Fatalf("commands = %#v, want %#v", commands, wantCommands)
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
	env.RegisterActivityWithOptions(
		func(context.Context, traits.PrepareWorkspaceActivityInput) (traits.PrepareWorkspaceActivityOutput, error) {
			return traits.PrepareWorkspaceActivityOutput{}, nil
		},
		activity.RegisterOptions{Name: traits.PrepareWorkspaceActivityName},
	)
	env.RegisterActivityWithOptions(
		func(context.Context, traits.RunTerraformActivityInput) error {
			return nil
		},
		activity.RegisterOptions{Name: traits.RunTerraformActivityName},
	)
	return env
}

func mockPrepareWorkspace(t *testing.T, env *testsuite.TestWorkflowEnvironment) {
	t.Helper()

	env.OnActivity(traits.PrepareWorkspaceActivityName, mock.Anything, mock.Anything).
		Return(traits.PrepareWorkspaceActivityOutput{WorkspacePath: "/tmp/megagega/runs/tenant_123/run_123"}, nil)
}

func mockRunTerraform(t *testing.T, env *testsuite.TestWorkflowEnvironment, commands *[]traits.TerraformCommandType) {
	t.Helper()

	env.OnActivity(traits.RunTerraformActivityName, mock.Anything, mock.Anything).
		Return(func(_ context.Context, activityInput traits.RunTerraformActivityInput) error {
			*commands = append(*commands, activityInput.Command)
			return nil
		})
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
