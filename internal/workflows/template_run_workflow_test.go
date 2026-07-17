package workflows

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/vishu42/tflive/internal/traits"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
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
			return traits.PrepareWorkspaceActivityOutput{WorkspacePath: "/tmp/tflive/runs/tenant_123/run_123"}, nil
		})
	env.OnActivity(traits.FetchSourceActivityName, mock.Anything, mock.Anything).
		Return(func(_ context.Context, activityInput traits.FetchSourceActivityInput) (traits.FetchSourceActivityOutput, error) {
			if activityInput.RunID != input.RunID {
				t.Fatalf("fetch source RunID = %q, want %q", activityInput.RunID, input.RunID)
			}
			if activityInput.TenantID != input.TenantID {
				t.Fatalf("fetch source TenantID = %q, want %q", activityInput.TenantID, input.TenantID)
			}
			if activityInput.WorkspacePath != "/tmp/tflive/runs/tenant_123/run_123" {
				t.Fatalf("fetch source WorkspacePath = %q", activityInput.WorkspacePath)
			}
			if activityInput.RepoOwner != input.RepoOwner {
				t.Fatalf("fetch source RepoOwner = %q, want %q", activityInput.RepoOwner, input.RepoOwner)
			}
			if activityInput.RepoName != input.RepoName {
				t.Fatalf("fetch source RepoName = %q, want %q", activityInput.RepoName, input.RepoName)
			}
			if activityInput.SourceRef != input.SelectedRef {
				t.Fatalf("fetch source SourceRef = %q, want %q", activityInput.SourceRef, input.SelectedRef)
			}
			if activityInput.RootPath != input.RootPath {
				t.Fatalf("fetch source RootPath = %q, want %q", activityInput.RootPath, input.RootPath)
			}
			events = append(events, "fetch_source")
			return traits.FetchSourceActivityOutput{TerraformPath: "/tmp/tflive/runs/tenant_123/run_123/source/modules/vpc"}, nil
		})
	env.OnActivity(traits.RunTerraformActivityName, mock.Anything, mock.Anything).
		Return(func(_ context.Context, activityInput traits.RunTerraformActivityInput) error {
			if activityInput.RunID != input.RunID {
				t.Fatalf("run terraform RunID = %q, want %q", activityInput.RunID, input.RunID)
			}
			if activityInput.TenantID != input.TenantID {
				t.Fatalf("run terraform TenantID = %q, want %q", activityInput.TenantID, input.TenantID)
			}
			if activityInput.WorkspacePath != "/tmp/tflive/runs/tenant_123/run_123" {
				t.Fatalf("run terraform WorkspacePath = %q", activityInput.WorkspacePath)
			}
			if activityInput.TerraformPath != "/tmp/tflive/runs/tenant_123/run_123/source/modules/vpc" {
				t.Fatalf("run terraform TerraformPath = %q", activityInput.TerraformPath)
			}
			if activityInput.WorkspaceName != input.WorkspaceName {
				t.Fatalf("run terraform WorkspaceName = %q, want %q", activityInput.WorkspaceName, input.WorkspaceName)
			}
			if string(activityInput.ConfigJSON) != string(input.ConfigJSON) {
				t.Fatalf("run terraform ConfigJSON = %s, want %s", activityInput.ConfigJSON, input.ConfigJSON)
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
		"fetch_source",
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
	mockFetchSource(t, env)
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
	mockFetchSource(t, env)
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
	mockFetchSource(t, env)
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
	mockFetchSource(t, env)
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
		func(context.Context, traits.FetchSourceActivityInput) (traits.FetchSourceActivityOutput, error) {
			return traits.FetchSourceActivityOutput{}, nil
		},
		activity.RegisterOptions{Name: traits.FetchSourceActivityName},
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
		Return(traits.PrepareWorkspaceActivityOutput{WorkspacePath: "/tmp/tflive/runs/tenant_123/run_123"}, nil)
}

func mockFetchSource(t *testing.T, env *testsuite.TestWorkflowEnvironment) {
	t.Helper()

	env.OnActivity(traits.FetchSourceActivityName, mock.Anything, mock.Anything).
		Return(traits.FetchSourceActivityOutput{TerraformPath: "/tmp/tflive/runs/tenant_123/run_123/source/modules/vpc"}, nil)
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
		RepoOwner:       "acme",
		RepoName:        "infra-templates",
		RootPath:        "modules/vpc",
		ConfigJSON:      []byte(`{"region":"us-east-1"}`),
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

func TestTemplateRunWorkflowRetriesTransientActivityError(t *testing.T) {
	t.Parallel()

	env := newTemplateRunWorkflowTestEnvironment(t)
	input := templateRunWorkflowInput(traits.OperationPlan)

	var prepareAttempts int
	env.OnActivity(traits.PrepareWorkspaceActivityName, mock.Anything, mock.Anything).
		Return(func(_ context.Context, _ traits.PrepareWorkspaceActivityInput) (traits.PrepareWorkspaceActivityOutput, error) {
			prepareAttempts++
			if prepareAttempts < 3 {
				return traits.PrepareWorkspaceActivityOutput{}, errors.New("connection reset")
			}
			return traits.PrepareWorkspaceActivityOutput{WorkspacePath: "/tmp/tflive/runs/tenant_123/run_123"}, nil
		})

	mockFetchSource(t, env)
	mockRunTerraformNoop(t, env)
	env.OnActivity(traits.RecordTemplateRunStatusActivityName, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(TemplateRunWorkflow, input)

	assertWorkflowCompleted(t, env)
	if prepareAttempts != 3 {
		t.Fatalf("prepare attempts = %d, want 3 (2 failures + 1 success)", prepareAttempts)
	}
}

func TestTemplateRunWorkflowStopsOnNonRetryableError(t *testing.T) {
	t.Parallel()

	env := newTemplateRunWorkflowTestEnvironment(t)
	input := templateRunWorkflowInput(traits.OperationPlan)

	var prepareAttempts int
	env.OnActivity(traits.PrepareWorkspaceActivityName, mock.Anything, mock.Anything).
		Return(func(_ context.Context, _ traits.PrepareWorkspaceActivityInput) (traits.PrepareWorkspaceActivityOutput, error) {
			prepareAttempts++
			return traits.PrepareWorkspaceActivityOutput{}, temporal.NewNonRetryableApplicationError(
				"invalid configuration",
				"InvalidConfig",
				nil,
			)
		})

	env.OnActivity(traits.RecordTemplateRunStatusActivityName, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(TemplateRunWorkflow, input)

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow did not complete")
	}
	if err := env.GetWorkflowError(); err == nil {
		t.Fatal("workflow error is nil, want non-retryable error")
	}
	if prepareAttempts != 1 {
		t.Fatalf("prepare attempts = %d, want 1 (non-retryable should not retry)", prepareAttempts)
	}
}

func TestTemplateRunWorkflowRetriesTerraformCommand(t *testing.T) {
	t.Parallel()

	env := newTemplateRunWorkflowTestEnvironment(t)
	input := templateRunWorkflowInput(traits.OperationPlan)

	mockPrepareWorkspace(t, env)
	mockFetchSource(t, env)

	var planAttempts int
	env.OnActivity(traits.RunTerraformActivityName, mock.Anything, mock.Anything).
		Return(func(_ context.Context, activityInput traits.RunTerraformActivityInput) error {
			if activityInput.Command == traits.TerraformCommandPlan {
				planAttempts++
				if planAttempts < 3 {
					return errors.New("rate limit exceeded")
				}
			}
			return nil
		})

	env.OnActivity(traits.RecordTemplateRunStatusActivityName, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(TemplateRunWorkflow, input)

	assertWorkflowCompleted(t, env)
	if planAttempts != 3 {
		t.Fatalf("plan attempts = %d, want 3 (2 failures + 1 success)", planAttempts)
	}
}

func TestTemplateRunWorkflowNonRetryableTerraformError(t *testing.T) {
	t.Parallel()

	env := newTemplateRunWorkflowTestEnvironment(t)
	input := templateRunWorkflowInput(traits.OperationPlan)

	mockPrepareWorkspace(t, env)
	mockFetchSource(t, env)

	var terraformAttempts int
	env.OnActivity(traits.RunTerraformActivityName, mock.Anything, mock.Anything).
		Return(func(_ context.Context, activityInput traits.RunTerraformActivityInput) error {
			terraformAttempts++
			if activityInput.Command == traits.TerraformCommandPlan {
				return temporal.NewNonRetryableApplicationError(
					"unsupported terraform command",
					"UnsupportedCommand",
					nil,
				)
			}
			return nil
		})

	env.OnActivity(traits.RecordTemplateRunStatusActivityName, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(TemplateRunWorkflow, input)

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow did not complete")
	}
	if err := env.GetWorkflowError(); err == nil {
		t.Fatal("workflow error is nil, want non-retryable error")
	}
	// Attempts: init (1), select_workspace (1), plan (1 - non-retryable)
	if terraformAttempts != 3 {
		t.Fatalf("terraform attempts = %d, want 3 (non-retryable should not retry)", terraformAttempts)
	}
}

func TestDefaultRunRetryPolicy(t *testing.T) {
	t.Parallel()

	if defaultRunRetryPolicy.MaximumAttempts != 5 {
		t.Fatalf("MaximumAttempts = %d, want 5", defaultRunRetryPolicy.MaximumAttempts)
	}
	if defaultRunRetryPolicy.InitialInterval != 30*time.Second {
		t.Fatalf("InitialInterval = %v, want 30s", defaultRunRetryPolicy.InitialInterval)
	}
	if defaultRunRetryPolicy.BackoffCoefficient != 2.0 {
		t.Fatalf("BackoffCoefficient = %f, want 2.0", defaultRunRetryPolicy.BackoffCoefficient)
	}
	if defaultRunRetryPolicy.MaximumInterval != 5*time.Minute {
		t.Fatalf("MaximumInterval = %v, want 5m", defaultRunRetryPolicy.MaximumInterval)
	}
	wantNonRetryable := []string{"InvalidConfig", "UnsupportedCommand"}
	if !reflect.DeepEqual(defaultRunRetryPolicy.NonRetryableErrorTypes, wantNonRetryable) {
		t.Fatalf("NonRetryableErrorTypes = %v, want %v", defaultRunRetryPolicy.NonRetryableErrorTypes, wantNonRetryable)
	}
}

func TestTerraformRetryPolicy(t *testing.T) {
	t.Parallel()

	if terraformRetryPolicy.MaximumAttempts != 3 {
		t.Fatalf("MaximumAttempts = %d, want 3", terraformRetryPolicy.MaximumAttempts)
	}
	if terraformRetryPolicy.InitialInterval != time.Minute {
		t.Fatalf("InitialInterval = %v, want 1m", terraformRetryPolicy.InitialInterval)
	}
	if terraformRetryPolicy.BackoffCoefficient != 2.0 {
		t.Fatalf("BackoffCoefficient = %f, want 2.0", terraformRetryPolicy.BackoffCoefficient)
	}
	if terraformRetryPolicy.MaximumInterval != 10*time.Minute {
		t.Fatalf("MaximumInterval = %v, want 10m", terraformRetryPolicy.MaximumInterval)
	}
	wantNonRetryable := []string{"InvalidConfig", "UnsupportedCommand"}
	if !reflect.DeepEqual(terraformRetryPolicy.NonRetryableErrorTypes, wantNonRetryable) {
		t.Fatalf("NonRetryableErrorTypes = %v, want %v", terraformRetryPolicy.NonRetryableErrorTypes, wantNonRetryable)
	}
}

func mockRunTerraformNoop(t *testing.T, env *testsuite.TestWorkflowEnvironment) {
	t.Helper()

	env.OnActivity(traits.RunTerraformActivityName, mock.Anything, mock.Anything).Return(nil)
}
