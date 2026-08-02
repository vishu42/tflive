package workflows

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/vishu42/tflive/internal/traits"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

const (
	requesterSubject = traits.UserID("6fdb4b4c-2a8f-4cf7-945f-38f67f6a0e91")
	approverSubject  = traits.UserID("cb4afba6-d18d-496f-80ce-8a50b94f09be")
)

// TestTemplateRunWorkflowUsesSessionForWorkspaceActivities protects the worker
// affinity contract for the filesystem-backed workspace. If a future change
// schedules any workspace activity on the normal queue, it could run on a
// different worker that does not have the run's local files. The apply and
// destroy cases also prove that the session survives the approval wait.
func TestTemplateRunWorkflowUsesSessionForWorkspaceActivities(t *testing.T) {
	for _, testCase := range []struct {
		name                   string
		operation              traits.OperationType
		workspaceActivityCount int
	}{
		{name: "plan", operation: traits.OperationPlan, workspaceActivityCount: 5},
		{name: "apply", operation: traits.OperationApply, workspaceActivityCount: 6},
		{name: "destroy", operation: traits.OperationDestroy, workspaceActivityCount: 5},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			env := newTemplateRunWorkflowTestEnvironment(t)
			input := templateRunWorkflowInput(testCase.operation)
			var workspaceTaskQueues []string
			var statusTaskQueues []string

			env.OnActivity(traits.PrepareWorkspaceActivityName, mock.Anything, mock.Anything).
				Return(func(ctx context.Context, _ traits.PrepareWorkspaceActivityInput) (traits.PrepareWorkspaceActivityOutput, error) {
					workspaceTaskQueues = append(workspaceTaskQueues, activity.GetInfo(ctx).TaskQueue)
					return traits.PrepareWorkspaceActivityOutput{WorkspacePath: "run/workspace"}, nil
				})
			env.OnActivity(traits.FetchSourceActivityName, mock.Anything, mock.Anything).
				Return(func(ctx context.Context, _ traits.FetchSourceActivityInput) (traits.FetchSourceActivityOutput, error) {
					workspaceTaskQueues = append(workspaceTaskQueues, activity.GetInfo(ctx).TaskQueue)
					return traits.FetchSourceActivityOutput{TerraformPath: "run/workspace/source"}, nil
				})
			env.OnActivity(traits.RunTerraformActivityName, mock.Anything, mock.Anything).
				Return(func(ctx context.Context, _ traits.RunTerraformActivityInput) error {
					workspaceTaskQueues = append(workspaceTaskQueues, activity.GetInfo(ctx).TaskQueue)
					return nil
				})
			env.OnActivity(traits.RecordTemplateRunStatusActivityName, mock.Anything, mock.Anything).
				Return(func(ctx context.Context, activityInput traits.TemplateRunStatusActivityInput) error {
					statusTaskQueues = append(statusTaskQueues, activity.GetInfo(ctx).TaskQueue)
					if activityInput.Status == traits.TemplateRunWaitingApproval {
						env.SignalWorkflow(traits.ApprovalSignalName, traits.ApprovalSignal{
							ApprovedBy: approverSubject,
						})
					}
					return nil
				})

			env.ExecuteWorkflow(TemplateRunWorkflow, input)

			assertWorkflowCompleted(t, env)
			if len(workspaceTaskQueues) != testCase.workspaceActivityCount {
				t.Fatalf("workspace activity task queues = %#v, want %d activity queues", workspaceTaskQueues, testCase.workspaceActivityCount)
			}
			for _, taskQueue := range workspaceTaskQueues[1:] {
				if taskQueue != workspaceTaskQueues[0] {
					t.Fatalf("workspace activity task queues = %#v, want one shared session queue", workspaceTaskQueues)
				}
			}
			if len(statusTaskQueues) == 0 {
				t.Fatal("status activity task queue is empty")
			}
			for _, taskQueue := range statusTaskQueues {
				if taskQueue != statusTaskQueues[0] {
					t.Fatalf("status activity task queues = %#v, want one normal queue", statusTaskQueues)
				}
			}
			if workspaceTaskQueues[0] == statusTaskQueues[0] {
				t.Fatalf("workspace task queue = %q, want a session queue distinct from status queue", workspaceTaskQueues[0])
			}
		})
	}
}

func TestTemplateRunWorkflowReturnsSessionFailureDuringApproval(t *testing.T) {
	env := newTemplateRunWorkflowTestEnvironment(t)
	env.SetTestTimeout(time.Second)
	env.RegisterWorkflow(templateRunApprovalSessionEndsWorkflow)

	env.ExecuteWorkflow(templateRunApprovalSessionEndsWorkflow)

	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow returned error: %v", err)
	}
}

func templateRunApprovalSessionEndsWorkflow(ctx workflow.Context) error {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: time.Minute,
	})
	sessionCtx, err := workflow.CreateSession(ctx, &workflow.SessionOptions{
		CreationTimeout:  time.Minute,
		ExecutionTimeout: 24 * time.Hour,
	})
	if err != nil {
		return err
	}
	run := templateRunWorkflow{ctx: ctx, sessionCtx: sessionCtx}
	workflow.CompleteSession(sessionCtx)
	_, err = run.waitForApproval()
	if err == nil {
		return errors.New("approval wait returned nil after session ended")
	}
	if !errors.Is(err, workflow.ErrSessionFailed) {
		return err
	}
	return nil
}

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
		string(traits.TemplateRunInitStarted),
		"terraform:" + string(traits.TerraformCommandInit),
		string(traits.TemplateRunInitFinished),
		"terraform:" + string(traits.TerraformCommandSelectWorkspace),
		string(traits.TemplateRunWorkspaceSelected),
		string(traits.TemplateRunPlanStarted),
		"terraform:" + string(traits.TerraformCommandPlan),
		string(traits.TemplateRunPlanFinished),
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
			ApprovedBy: approverSubject,
		})
	}, 0)

	env.ExecuteWorkflow(TemplateRunWorkflow, input)

	assertWorkflowCompleted(t, env)
	want := []traits.TemplateRunStatus{
		traits.TemplateRunLocked,
		traits.TemplateRunWorkspacePrepared,
		traits.TemplateRunSourceFetched,
		traits.TemplateRunInitStarted,
		traits.TemplateRunInitFinished,
		traits.TemplateRunWorkspaceSelected,
		traits.TemplateRunPlanStarted,
		traits.TemplateRunPlanFinished,
		traits.TemplateRunWaitingApproval,
		traits.TemplateRunApplyStarted,
		traits.TemplateRunApplyFinished,
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
					RequestedBy: requesterSubject,
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
		traits.TemplateRunInitStarted,
		traits.TemplateRunInitFinished,
		traits.TemplateRunWorkspaceSelected,
		traits.TemplateRunPlanStarted,
		traits.TemplateRunPlanFinished,
		traits.TemplateRunWaitingApproval,
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
			RequestedBy: requesterSubject,
			Reason:      "stop retries",
		})
	}, 0)

	env.ExecuteWorkflow(TemplateRunWorkflow, input)

	assertWorkflowCompleted(t, env)
	if len(statuses) == 0 || statuses[len(statuses)-1] != traits.TemplateRunCanceled {
		t.Fatalf("final status = %#v, want canceled", statuses)
	}
	for _, status := range statuses {
		if status == traits.TemplateRunPlanFinished || status == traits.TemplateRunCompleted {
			t.Fatalf("statuses = %#v, canceled plan should not become planned or completed", statuses)
		}
	}
	if len(commands) > 1 {
		t.Fatalf("commands = %#v, cancel should stop before later terraform phases", commands)
	}
}

// TestTemplateRunWorkflowCancelsDestroyWhileWaitingApproval verifies that a
// destroy workflow transitions to the cancel path when a cancel signal arrives
// during the waiting-for-approval phase. The workflow must not dispatch a
// terraform destroy command and must reach the Canceled terminal status.
func TestTemplateRunWorkflowCancelsDestroyWhileWaitingApproval(t *testing.T) {
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
			if activityInput.Status == traits.TemplateRunWaitingApproval {
				env.SignalWorkflow(traits.CancelSignalName, traits.CancelSignal{
					RequestedBy: requesterSubject,
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
		traits.TemplateRunInitStarted,
		traits.TemplateRunInitFinished,
		traits.TemplateRunWorkspaceSelected,
		traits.TemplateRunWaitingApproval,
		traits.TemplateRunCanceled,
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
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(traits.ApprovalSignalName, traits.ApprovalSignal{
			ApprovedBy: approverSubject,
		})
	}, 0)

	env.ExecuteWorkflow(TemplateRunWorkflow, input)

	assertWorkflowCompleted(t, env)
	want := []traits.TemplateRunStatus{
		traits.TemplateRunLocked,
		traits.TemplateRunWorkspacePrepared,
		traits.TemplateRunSourceFetched,
		traits.TemplateRunInitStarted,
		traits.TemplateRunInitFinished,
		traits.TemplateRunWorkspaceSelected,
		traits.TemplateRunWaitingApproval,
		traits.TemplateRunApproved,
		traits.TemplateRunDestroyStarted,
		traits.TemplateRunDestroyFinished,
		traits.TemplateRunLockReleased,
		traits.TemplateRunCompleted,
	}
	if !reflect.DeepEqual(statuses, want) {
		t.Fatalf("statuses = %#v, want %#v", statuses, want)
	}
	wantCommands := []traits.TerraformCommandType{
		traits.TerraformCommandInit,
		traits.TerraformCommandSelectWorkspace,
		traits.TerraformCommandDestroy,
	}
	if !reflect.DeepEqual(commands, wantCommands) {
		t.Fatalf("commands = %#v, want %#v", commands, wantCommands)
	}
}

// TestTemplateRunWorkflowRejectsUnsupportedOperation verifies that an
// unrecognized operation fails the run before any side effect: no workspace is
// prepared and no terraform command is dispatched. The run records a single
// Failed status carrying the reason, and never claims a lock it would then have
// to release.
func TestTemplateRunWorkflowRejectsUnsupportedOperation(t *testing.T) {
	t.Parallel()

	env := newTemplateRunWorkflowTestEnvironment(t)
	input := templateRunWorkflowInput(traits.OperationType("migrate"))
	var startedActivityNames []string
	env.SetOnActivityStartedListener(func(activityInfo *activity.Info, _ context.Context, _ converter.EncodedValues) {
		startedActivityNames = append(startedActivityNames, activityInfo.ActivityType.Name)
	})
	var statuses []traits.TemplateRunStatus
	var summaries []string
	var commands []traits.TerraformCommandType
	prepared := false
	env.OnActivity(traits.PrepareWorkspaceActivityName, mock.Anything, mock.Anything).
		Return(func(_ context.Context, _ traits.PrepareWorkspaceActivityInput) (traits.PrepareWorkspaceActivityOutput, error) {
			prepared = true
			return traits.PrepareWorkspaceActivityOutput{}, nil
		})
	mockRunTerraform(t, env, &commands)
	env.OnActivity(traits.RecordTemplateRunStatusActivityName, mock.Anything, mock.Anything).
		Return(func(_ context.Context, activityInput traits.TemplateRunStatusActivityInput) error {
			statuses = append(statuses, activityInput.Status)
			summaries = append(summaries, activityInput.ErrorSummary)
			return nil
		})

	env.ExecuteWorkflow(TemplateRunWorkflow, input)

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow did not complete")
	}
	err := env.GetWorkflowError()
	if err == nil {
		t.Fatal("workflow error = nil, want unsupported operation error")
	}
	if !strings.Contains(err.Error(), "unsupported template run operation") {
		t.Fatalf("workflow error = %v, want unsupported operation error", err)
	}
	if prepared {
		t.Fatal("prepare workspace ran, unsupported operation must fail before side effects")
	}
	if len(commands) != 0 {
		t.Fatalf("commands = %#v, want no terraform commands", commands)
	}
	want := []traits.TemplateRunStatus{traits.TemplateRunFailed}
	if !reflect.DeepEqual(statuses, want) {
		t.Fatalf("statuses = %#v, want %#v", statuses, want)
	}
	if !strings.Contains(summaries[0], "unsupported template run operation") {
		t.Fatalf("error summary = %q, want the unsupported operation reason", summaries[0])
	}
	if !reflect.DeepEqual(startedActivityNames, []string{traits.RecordTemplateRunStatusActivityName}) {
		t.Fatalf("started activity names = %#v, want only status persistence without session creation", startedActivityNames)
	}
}

func newTemplateRunWorkflowTestEnvironment(t *testing.T) *testsuite.TestWorkflowEnvironment {
	t.Helper()

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.SetWorkerOptions(worker.Options{EnableSessionWorker: true})
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

// TestTemplateRunWorkflowExhaustsRetriesOnTransientActivityError asserts a
// persistently transient activity error fails the run after exactly
// defaultRunRetryPolicy.MaximumAttempts attempts, rather than hardcoding the
// current value (1) — so this test doesn't need updating every time the
// policy's attempt count changes.
func TestTemplateRunWorkflowExhaustsRetriesOnTransientActivityError(t *testing.T) {
	t.Parallel()

	env := newTemplateRunWorkflowTestEnvironment(t)
	input := templateRunWorkflowInput(traits.OperationPlan)

	var prepareAttempts int
	env.OnActivity(traits.PrepareWorkspaceActivityName, mock.Anything, mock.Anything).
		Return(func(_ context.Context, _ traits.PrepareWorkspaceActivityInput) (traits.PrepareWorkspaceActivityOutput, error) {
			prepareAttempts++
			return traits.PrepareWorkspaceActivityOutput{}, errors.New("connection reset")
		})

	env.OnActivity(traits.RecordTemplateRunStatusActivityName, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(TemplateRunWorkflow, input)

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow did not complete")
	}
	if err := env.GetWorkflowError(); err == nil {
		t.Fatal("workflow error is nil, want transient error to fail the run once retries are exhausted")
	}
	if want := defaultRunRetryPolicy.MaximumAttempts; prepareAttempts != int(want) {
		t.Fatalf("prepare attempts = %d, want %d (defaultRunRetryPolicy.MaximumAttempts)", prepareAttempts, want)
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

func TestTemplateRunWorkflowPersistsFailedStatusAfterActivityError(t *testing.T) {
	t.Parallel()

	env := newTemplateRunWorkflowTestEnvironment(t)
	input := templateRunWorkflowInput(traits.OperationPlan)
	var statuses []traits.TemplateRunStatusActivityInput
	env.OnActivity(traits.PrepareWorkspaceActivityName, mock.Anything, mock.Anything).
		Return(func(_ context.Context, _ traits.PrepareWorkspaceActivityInput) (traits.PrepareWorkspaceActivityOutput, error) {
			return traits.PrepareWorkspaceActivityOutput{}, temporal.NewNonRetryableApplicationError(
				"invalid configuration",
				"InvalidConfig",
				nil,
			)
		})
	env.OnActivity(traits.RecordTemplateRunStatusActivityName, mock.Anything, mock.Anything).
		Return(func(_ context.Context, status traits.TemplateRunStatusActivityInput) error {
			statuses = append(statuses, status)
			return nil
		})

	env.ExecuteWorkflow(TemplateRunWorkflow, input)

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow did not complete")
	}
	if err := env.GetWorkflowError(); err == nil || !strings.Contains(err.Error(), "invalid configuration") {
		t.Fatalf("workflow error = %v, want invalid configuration", err)
	}
	if len(statuses) == 0 || statuses[len(statuses)-1].Status != traits.TemplateRunFailed {
		t.Fatalf("final statuses = %#v, want failed", statuses)
	}
	if !strings.Contains(statuses[len(statuses)-1].ErrorSummary, "invalid configuration") {
		t.Fatalf("failure summary = %q, want root activity error", statuses[len(statuses)-1].ErrorSummary)
	}
}

func TestTemplateRunWorkflowPreservesActivityErrorWhenFailurePersistenceFails(t *testing.T) {
	t.Parallel()

	env := newTemplateRunWorkflowTestEnvironment(t)
	input := templateRunWorkflowInput(traits.OperationPlan)
	env.OnActivity(traits.PrepareWorkspaceActivityName, mock.Anything, mock.Anything).
		Return(func(_ context.Context, _ traits.PrepareWorkspaceActivityInput) (traits.PrepareWorkspaceActivityOutput, error) {
			return traits.PrepareWorkspaceActivityOutput{}, temporal.NewNonRetryableApplicationError(
				"invalid configuration",
				"InvalidConfig",
				nil,
			)
		})
	env.OnActivity(traits.RecordTemplateRunStatusActivityName, mock.Anything, mock.Anything).
		Return(func(_ context.Context, status traits.TemplateRunStatusActivityInput) error {
			if status.Status == traits.TemplateRunFailed {
				return errors.New("status database unavailable")
			}
			return nil
		})

	env.ExecuteWorkflow(TemplateRunWorkflow, input)

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow did not complete")
	}
	if err := env.GetWorkflowError(); err == nil || !strings.Contains(err.Error(), "invalid configuration") {
		t.Fatalf("workflow error = %v, want original activity error", err)
	}
}

// TestTemplateRunWorkflowExhaustsRetriesOnTerraformError asserts a
// persistently transient plan error fails the run after exactly
// terraformRetryPolicy.MaximumAttempts attempts, rather than hardcoding the
// current value (1) — so this test doesn't need updating every time the
// policy's attempt count changes.
func TestTemplateRunWorkflowExhaustsRetriesOnTerraformError(t *testing.T) {
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
				return errors.New("rate limit exceeded")
			}
			return nil
		})

	env.OnActivity(traits.RecordTemplateRunStatusActivityName, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(TemplateRunWorkflow, input)

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow did not complete")
	}
	if err := env.GetWorkflowError(); err == nil {
		t.Fatal("workflow error is nil, want transient error to fail the run once retries are exhausted")
	}
	if want := terraformRetryPolicy.MaximumAttempts; planAttempts != int(want) {
		t.Fatalf("plan attempts = %d, want %d (terraformRetryPolicy.MaximumAttempts)", planAttempts, want)
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

	if defaultRunRetryPolicy.MaximumAttempts != 1 {
		t.Fatalf("MaximumAttempts = %d, want 1", defaultRunRetryPolicy.MaximumAttempts)
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

	if terraformRetryPolicy.MaximumAttempts != 1 {
		t.Fatalf("MaximumAttempts = %d, want 1", terraformRetryPolicy.MaximumAttempts)
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
