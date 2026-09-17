package workflows

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/vishu42/tflive/internal/domain"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

const (
	requesterSubject = domain.UserID("6fdb4b4c-2a8f-4cf7-945f-38f67f6a0e91")
	approverSubject  = domain.UserID("cb4afba6-d18d-496f-80ce-8a50b94f09be")
)

// TestTemplateRunWorkflowUsesSessionForWorkspaceActivities protects the executor
// affinity contract for the filesystem-backed workspace. If a future change
// schedules any workspace activity on the normal queue, it could run on a
// different executor that does not have the run's local files. The apply and
// destroy cases also prove that the session survives the approval wait.
func TestTemplateRunWorkflowUsesSessionForWorkspaceActivities(t *testing.T) {
	for _, testCase := range []struct {
		name                   string
		operation              domain.OperationType
		workspaceActivityCount int
	}{
		{name: "plan", operation: domain.OperationPlan, workspaceActivityCount: 5},
		{name: "apply", operation: domain.OperationApply, workspaceActivityCount: 6},
		{name: "destroy", operation: domain.OperationDestroy, workspaceActivityCount: 5},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			env := newTemplateRunWorkflowTestEnvironment(t)
			input := templateRunWorkflowInput(testCase.operation)
			var workspaceTaskQueues []string
			var statusTaskQueues []string

			env.OnActivity(domain.PrepareWorkspaceActivityName, mock.Anything, mock.Anything).
				Return(func(ctx context.Context, _ domain.PrepareWorkspaceActivityInput) (domain.PrepareWorkspaceActivityOutput, error) {
					workspaceTaskQueues = append(workspaceTaskQueues, activity.GetInfo(ctx).TaskQueue)
					return domain.PrepareWorkspaceActivityOutput{WorkspacePath: "run/workspace"}, nil
				})
			env.OnActivity(domain.FetchSourceActivityName, mock.Anything, mock.Anything).
				Return(func(ctx context.Context, _ domain.FetchSourceActivityInput) (domain.FetchSourceActivityOutput, error) {
					workspaceTaskQueues = append(workspaceTaskQueues, activity.GetInfo(ctx).TaskQueue)
					return domain.FetchSourceActivityOutput{TerraformPath: "run/workspace/source"}, nil
				})
			env.OnActivity(domain.RunTerraformActivityName, mock.Anything, mock.Anything).
				Return(func(ctx context.Context, _ domain.RunTerraformActivityInput) (domain.RunTerraformActivityOutput, error) {
					workspaceTaskQueues = append(workspaceTaskQueues, activity.GetInfo(ctx).TaskQueue)
					return domain.RunTerraformActivityOutput{}, nil
				})
			env.OnActivity(domain.RecordTemplateRunStatusActivityName, mock.Anything, mock.Anything).
				Return(func(ctx context.Context, activityInput domain.TemplateRunStatusActivityInput) error {
					statusTaskQueues = append(statusTaskQueues, activity.GetInfo(ctx).TaskQueue)
					if activityInput.Status == domain.TemplateRunWaitingApproval {
						env.SignalWorkflow(domain.ApprovalSignalName, domain.ApprovalSignal{
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

// TestTemplateRunWorkflowRoutesActivitiesByPlane protects the control/data
// plane split. Status writes need the database, so they must stay on the
// control queue; the session must be created on the execution queue, or
// Terraform would run on a host that holds the control plane's secrets.
func TestTemplateRunWorkflowRoutesActivitiesByPlane(t *testing.T) {
	t.Parallel()

	env := newTemplateRunWorkflowTestEnvironment(t)
	queues := map[string][]string{}
	env.SetOnActivityStartedListener(func(info *activity.Info, _ context.Context, _ converter.EncodedValues) {
		queues[info.ActivityType.Name] = append(queues[info.ActivityType.Name], info.TaskQueue)
	})
	env.OnActivity(domain.PrepareWorkspaceActivityName, mock.Anything, mock.Anything).
		Return(domain.PrepareWorkspaceActivityOutput{WorkspacePath: "run/workspace"}, nil)
	env.OnActivity(domain.FetchSourceActivityName, mock.Anything, mock.Anything).
		Return(domain.FetchSourceActivityOutput{TerraformPath: "run/workspace/source"}, nil)
	env.OnActivity(domain.RunTerraformActivityName, mock.Anything, mock.Anything).Return(domain.RunTerraformActivityOutput{}, nil)
	env.OnActivity(domain.RecordTemplateRunStatusActivityName, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(TemplateRunWorkflow, templateRunWorkflowInput(domain.OperationPlan))

	assertWorkflowCompleted(t, env)
	statusQueues := queues[domain.RecordTemplateRunStatusActivityName]
	if len(statusQueues) == 0 {
		t.Fatal("no status activity ran")
	}
	for _, queue := range statusQueues {
		if queue != domain.ControlTaskQueue {
			t.Fatalf("status activity queues = %#v, want all %q", statusQueues, domain.ControlTaskQueue)
		}
	}
	// The SDK creates a session through an internal activity on
	// "<base queue>__internal_session_creation"; the base is what places it.
	wantCreation := domain.ExecutionTaskQueue + "__internal_session_creation"
	if got := queues["internalSessionCreationActivity"]; len(got) != 1 || got[0] != wantCreation {
		t.Fatalf("session creation queues = %#v, want [%q]", got, wantCreation)
	}
}

// The executor has no database, so the log metadata it returns must be
// recorded by the control plane: after the command, before its finished status.
func TestTemplateRunWorkflowRecordsCommandLogsOnControlQueue(t *testing.T) {
	t.Parallel()

	env := newTemplateRunWorkflowTestEnvironment(t)
	var events []string
	var logQueues []string
	env.SetOnActivityStartedListener(func(info *activity.Info, _ context.Context, _ converter.EncodedValues) {
		if info.ActivityType.Name == domain.RecordTemplateRunLogActivityName {
			logQueues = append(logQueues, info.TaskQueue)
		}
	})
	mockPrepareWorkspace(t, env)
	env.OnActivity(domain.FetchSourceActivityName, mock.Anything, mock.Anything).
		Return(domain.FetchSourceActivityOutput{TerraformPath: "run/workspace/source"}, nil)
	env.OnActivity(domain.RunTerraformActivityName, mock.Anything, mock.Anything).
		Return(func(_ context.Context, input domain.RunTerraformActivityInput) (domain.RunTerraformActivityOutput, error) {
			events = append(events, "terraform:"+string(input.Command))
			return domain.RunTerraformActivityOutput{Log: domain.TemplateRunLog{
				TenantID:  input.TenantID,
				RunID:     input.RunID,
				Phase:     string(input.Command),
				ObjectKey: "logs/" + string(input.Command) + ".log",
			}}, nil
		})
	env.OnActivity(domain.RecordTemplateRunLogActivityName, mock.Anything, mock.Anything).
		Return(func(_ context.Context, log domain.TemplateRunLog) error {
			events = append(events, "log:"+log.Phase)
			return nil
		})
	env.OnActivity(domain.RecordTemplateRunStatusActivityName, mock.Anything, mock.Anything).
		Return(func(_ context.Context, input domain.TemplateRunStatusActivityInput) error {
			if input.Status == domain.TemplateRunPlanFinished {
				events = append(events, string(input.Status))
			}
			return nil
		})

	env.ExecuteWorkflow(TemplateRunWorkflow, templateRunWorkflowInput(domain.OperationPlan))

	assertWorkflowCompleted(t, env)
	want := []string{
		"terraform:init", "log:init",
		"terraform:select_workspace", "log:select_workspace",
		"terraform:plan", "log:plan", string(domain.TemplateRunPlanFinished),
	}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %#v, want %#v", events, want)
	}
	for _, queue := range logQueues {
		if queue != domain.ControlTaskQueue {
			t.Fatalf("log activity queues = %#v, want all %q", logQueues, domain.ControlTaskQueue)
		}
	}
}

// A failed command's log explains the failure, so it is recorded from the
// error's details before the run is marked failed.
func TestTemplateRunWorkflowRecordsLogOfFailedCommand(t *testing.T) {
	t.Parallel()

	env := newTemplateRunWorkflowTestEnvironment(t)
	failedLog := domain.TemplateRunLog{TenantID: "tenant_123", RunID: "run_123", Phase: "plan", ObjectKey: "logs/plan.log"}
	var recorded []domain.TemplateRunLog
	var failedStatus domain.TemplateRunStatusActivityInput
	mockPrepareWorkspace(t, env)
	env.OnActivity(domain.FetchSourceActivityName, mock.Anything, mock.Anything).
		Return(domain.FetchSourceActivityOutput{TerraformPath: "run/workspace/source"}, nil)
	env.OnActivity(domain.RunTerraformActivityName, mock.Anything, mock.Anything).
		Return(func(_ context.Context, input domain.RunTerraformActivityInput) (domain.RunTerraformActivityOutput, error) {
			if input.Command != domain.TerraformCommandPlan {
				return domain.RunTerraformActivityOutput{}, nil
			}
			return domain.RunTerraformActivityOutput{}, temporal.NewApplicationErrorWithOptions("run terraform", domain.TerraformCommandFailedErrorType, temporal.ApplicationErrorOptions{
				Cause:   errors.New("plan: exit status 1"),
				Details: []interface{}{failedLog},
			})
		})
	env.OnActivity(domain.RecordTemplateRunLogActivityName, mock.Anything, mock.Anything).
		Return(func(_ context.Context, log domain.TemplateRunLog) error {
			recorded = append(recorded, log)
			return nil
		})
	env.OnActivity(domain.RecordTemplateRunStatusActivityName, mock.Anything, mock.Anything).
		Return(func(_ context.Context, input domain.TemplateRunStatusActivityInput) error {
			if input.Status == domain.TemplateRunFailed {
				failedStatus = input
			}
			return nil
		})

	env.ExecuteWorkflow(TemplateRunWorkflow, templateRunWorkflowInput(domain.OperationPlan))

	if !env.IsWorkflowCompleted() || env.GetWorkflowError() == nil {
		t.Fatalf("workflow completed=%v error=%v, want a failed run", env.IsWorkflowCompleted(), env.GetWorkflowError())
	}
	if len(recorded) != 1 || recorded[0] != failedLog {
		t.Fatalf("recorded logs = %#v, want only the failed plan log", recorded)
	}
	if !strings.Contains(failedStatus.ErrorSummary, "plan: exit status 1") {
		t.Fatalf("failure summary = %q, want the command error", failedStatus.ErrorSummary)
	}
}

// Secrets cross Temporal only sealed to the key the executor returned from
// PrepareWorkspace: the control plane seals, on the control queue, and the
// executor receives only ciphertext. The key is released when the run ends.
func TestTemplateRunWorkflowSealsSecretsToTheExecutorKey(t *testing.T) {
	t.Parallel()

	env := newTemplateRunWorkflowTestEnvironment(t)
	publicKey := []byte("executor-public-key-0123456789ab")
	queues := map[string][]string{}
	env.SetOnActivityStartedListener(func(info *activity.Info, _ context.Context, _ converter.EncodedValues) {
		queues[info.ActivityType.Name] = append(queues[info.ActivityType.Name], info.TaskQueue)
	})
	var sealRequests int
	var terraformEnvironments [][]byte
	var fetchInput domain.FetchSourceActivityInput
	var released domain.ReleaseRunKeyActivityInput
	env.OnActivity(domain.PrepareWorkspaceActivityName, mock.Anything, mock.Anything).
		Return(domain.PrepareWorkspaceActivityOutput{WorkspacePath: "run/workspace", PublicKey: publicKey}, nil)
	env.OnActivity(domain.SealSourceTokenActivityName, mock.Anything, mock.Anything).
		Return(func(_ context.Context, input domain.SealSourceTokenActivityInput) (domain.SealSourceTokenActivityOutput, error) {
			if string(input.PublicKey) != string(publicKey) || input.RepoOwner != "acme" {
				t.Fatalf("seal source token input = %#v", input)
			}
			return domain.SealSourceTokenActivityOutput{SealedToken: []byte("sealed-token"), FetchHint: "; hint"}, nil
		})
	env.OnActivity(domain.FetchSourceActivityName, mock.Anything, mock.Anything).
		Return(func(_ context.Context, input domain.FetchSourceActivityInput) (domain.FetchSourceActivityOutput, error) {
			fetchInput = input
			return domain.FetchSourceActivityOutput{TerraformPath: "run/workspace/source"}, nil
		})
	env.OnActivity(domain.SealRunCredentialsActivityName, mock.Anything, mock.Anything).
		Return(func(_ context.Context, input domain.SealRunCredentialsActivityInput) (domain.SealRunCredentialsActivityOutput, error) {
			if string(input.PublicKey) != string(publicKey) || input.StackTemplateID != "stack_template_123" {
				t.Fatalf("seal credentials input = %#v", input)
			}
			sealRequests++
			return domain.SealRunCredentialsActivityOutput{SealedEnvironment: []byte(fmt.Sprintf("sealed-env-%d", sealRequests))}, nil
		})
	env.OnActivity(domain.RunTerraformActivityName, mock.Anything, mock.Anything).
		Return(func(_ context.Context, input domain.RunTerraformActivityInput) (domain.RunTerraformActivityOutput, error) {
			terraformEnvironments = append(terraformEnvironments, input.SealedEnvironment)
			return domain.RunTerraformActivityOutput{}, nil
		})
	env.OnActivity(domain.ReleaseRunKeyActivityName, mock.Anything, mock.Anything).
		Return(func(_ context.Context, input domain.ReleaseRunKeyActivityInput) error {
			released = input
			return nil
		})
	env.OnActivity(domain.RecordTemplateRunStatusActivityName, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(TemplateRunWorkflow, templateRunWorkflowInput(domain.OperationPlan))

	assertWorkflowCompleted(t, env)
	if string(fetchInput.SealedToken) != "sealed-token" || fetchInput.FetchHint != "; hint" {
		t.Fatalf("fetch source input = %#v, want the sealed token and hint", fetchInput)
	}
	// One fresh seal per command: init, select_workspace, plan.
	want := [][]byte{[]byte("sealed-env-1"), []byte("sealed-env-2"), []byte("sealed-env-3")}
	if !reflect.DeepEqual(terraformEnvironments, want) {
		t.Fatalf("terraform sealed environments = %q, want %q", terraformEnvironments, want)
	}
	for _, name := range []string{domain.SealSourceTokenActivityName, domain.SealRunCredentialsActivityName} {
		for _, queue := range queues[name] {
			if queue != domain.ControlTaskQueue {
				t.Fatalf("%s queues = %#v, want all %q", name, queues[name], domain.ControlTaskQueue)
			}
		}
	}
	if released.RunID != "run_123" || released.TenantID != "tenant_123" {
		t.Fatalf("released key = %#v, want run_123's", released)
	}
	if got, prepare := queues[domain.ReleaseRunKeyActivityName], queues[domain.PrepareWorkspaceActivityName]; len(got) != 1 || got[0] != prepare[0] {
		t.Fatalf("release key queues = %#v, want the session queue %#v", got, prepare)
	}
}

// Opened credentials live on RunTerraformActivityInput inside the executor. The
// field must never serialize, or a workflow that set it would write plaintext
// into history.
func TestRunTerraformActivityInputNeverSerializesEnvironment(t *testing.T) {
	t.Parallel()

	payload, err := converter.GetDefaultDataConverter().ToPayload(domain.RunTerraformActivityInput{
		RunID:       "run_123",
		Environment: map[string]string{"AWS_SECRET_ACCESS_KEY": "canary-7f3a"},
	})
	if err != nil {
		t.Fatalf("ToPayload returned error: %v", err)
	}
	if strings.Contains(string(payload.GetData()), "canary-7f3a") {
		t.Fatalf("payload = %s, want no plaintext environment", payload.GetData())
	}
}

// TestTemplateRunWorkflowGivesFetchSourceALongerTimeout guards against the
// default one-minute activity budget silently swallowing FetchSource again.
// That activity now resolves a GitHub token before it ever invokes git, which
// can cost up to two HTTP round trips on top of the clone/checkout itself, so
// it needs -- and must keep -- a longer StartToCloseTimeout than the rest of
// the run's activities.
func TestTemplateRunWorkflowGivesFetchSourceALongerTimeout(t *testing.T) {
	t.Parallel()

	env := newTemplateRunWorkflowTestEnvironment(t)
	input := templateRunWorkflowInput(domain.OperationPlan)
	var fetchSourceTimeout time.Duration

	env.OnActivity(domain.PrepareWorkspaceActivityName, mock.Anything, mock.Anything).
		Return(domain.PrepareWorkspaceActivityOutput{WorkspacePath: "run/workspace"}, nil)
	env.OnActivity(domain.FetchSourceActivityName, mock.Anything, mock.Anything).
		Return(func(ctx context.Context, _ domain.FetchSourceActivityInput) (domain.FetchSourceActivityOutput, error) {
			fetchSourceTimeout = activity.GetInfo(ctx).StartToCloseTimeout
			return domain.FetchSourceActivityOutput{TerraformPath: "run/workspace/source"}, nil
		})
	var commands []domain.TerraformCommandType
	mockRunTerraform(t, env, &commands)
	env.OnActivity(domain.RecordTemplateRunStatusActivityName, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(TemplateRunWorkflow, input)

	assertWorkflowCompleted(t, env)
	if fetchSourceTimeout != 3*time.Minute {
		t.Fatalf("FetchSource StartToCloseTimeout = %v, want 3m", fetchSourceTimeout)
	}
}

// TestTemplateRunWorkflowBoundsTerraformCommandsByTheConfiguredTimeout pins the
// two halves of how a long Terraform command survives: the budget comes from
// the run's input, so a deployment can raise it, and a heartbeat timeout is
// always set, so a lost executor fails within a couple of heartbeat intervals
// instead of consuming the whole budget. A hard-coded StartToCloseTimeout here is what killed runs
// longer than ten minutes.
func TestTemplateRunWorkflowBoundsTerraformCommandsByTheConfiguredTimeout(t *testing.T) {
	for _, testCase := range []struct {
		name        string
		configured  time.Duration
		wantTimeout time.Duration
	}{
		{name: "configured", configured: 90 * time.Minute, wantTimeout: 90 * time.Minute},
		{name: "unset", configured: 0, wantTimeout: domain.DefaultTerraformTimeout},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			env := newTemplateRunWorkflowTestEnvironment(t)
			input := templateRunWorkflowInput(domain.OperationPlan)
			input.TerraformTimeout = testCase.configured
			var timeouts []time.Duration
			var heartbeatTimeouts []time.Duration

			env.OnActivity(domain.PrepareWorkspaceActivityName, mock.Anything, mock.Anything).
				Return(domain.PrepareWorkspaceActivityOutput{WorkspacePath: "run/workspace"}, nil)
			env.OnActivity(domain.FetchSourceActivityName, mock.Anything, mock.Anything).
				Return(domain.FetchSourceActivityOutput{TerraformPath: "run/workspace/source"}, nil)
			env.OnActivity(domain.RunTerraformActivityName, mock.Anything, mock.Anything).
				Return(func(ctx context.Context, _ domain.RunTerraformActivityInput) (domain.RunTerraformActivityOutput, error) {
					timeouts = append(timeouts, activity.GetInfo(ctx).StartToCloseTimeout)
					heartbeatTimeouts = append(heartbeatTimeouts, activity.GetInfo(ctx).HeartbeatTimeout)
					return domain.RunTerraformActivityOutput{}, nil
				})
			env.OnActivity(domain.RecordTemplateRunStatusActivityName, mock.Anything, mock.Anything).Return(nil)

			env.ExecuteWorkflow(TemplateRunWorkflow, input)

			assertWorkflowCompleted(t, env)
			if len(timeouts) == 0 {
				t.Fatal("no Terraform command ran")
			}
			for _, timeout := range timeouts {
				if timeout != testCase.wantTimeout {
					t.Fatalf("RunTerraform StartToCloseTimeout = %v, want %v", timeout, testCase.wantTimeout)
				}
			}
			for _, timeout := range heartbeatTimeouts {
				if timeout != domain.TerraformHeartbeatTimeout {
					t.Fatalf("RunTerraform HeartbeatTimeout = %v, want %v", timeout, domain.TerraformHeartbeatTimeout)
				}
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
	input := templateRunWorkflowInput(domain.OperationPlan)
	var events []string
	env.OnActivity(domain.PrepareWorkspaceActivityName, mock.Anything, mock.Anything).
		Return(func(_ context.Context, activityInput domain.PrepareWorkspaceActivityInput) (domain.PrepareWorkspaceActivityOutput, error) {
			if activityInput.RunID != input.RunID {
				t.Fatalf("prepare workspace RunID = %q, want %q", activityInput.RunID, input.RunID)
			}
			if activityInput.TenantID != input.TenantID {
				t.Fatalf("prepare workspace TenantID = %q, want %q", activityInput.TenantID, input.TenantID)
			}
			events = append(events, "prepare_workspace")
			return domain.PrepareWorkspaceActivityOutput{WorkspacePath: "/tmp/tflive/runs/tenant_123/run_123"}, nil
		})
	env.OnActivity(domain.FetchSourceActivityName, mock.Anything, mock.Anything).
		Return(func(_ context.Context, activityInput domain.FetchSourceActivityInput) (domain.FetchSourceActivityOutput, error) {
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
			return domain.FetchSourceActivityOutput{TerraformPath: "/tmp/tflive/runs/tenant_123/run_123/source/modules/vpc"}, nil
		})
	env.OnActivity(domain.RunTerraformActivityName, mock.Anything, mock.Anything).
		Return(func(_ context.Context, activityInput domain.RunTerraformActivityInput) (domain.RunTerraformActivityOutput, error) {
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
			return domain.RunTerraformActivityOutput{}, nil
		})
	env.OnActivity(domain.RecordTemplateRunStatusActivityName, mock.Anything, mock.Anything).
		Return(func(_ context.Context, activityInput domain.TemplateRunStatusActivityInput) error {
			events = append(events, string(activityInput.Status))
			return nil
		})

	env.ExecuteWorkflow(TemplateRunWorkflow, input)

	assertWorkflowCompleted(t, env)
	want := []string{
		string(domain.TemplateRunLocked),
		"prepare_workspace",
		string(domain.TemplateRunWorkspacePrepared),
		"fetch_source",
		string(domain.TemplateRunSourceFetched),
		string(domain.TemplateRunInitStarted),
		"terraform:" + string(domain.TerraformCommandInit),
		string(domain.TemplateRunInitFinished),
		"terraform:" + string(domain.TerraformCommandSelectWorkspace),
		string(domain.TemplateRunWorkspaceSelected),
		string(domain.TemplateRunPlanStarted),
		"terraform:" + string(domain.TerraformCommandPlan),
		string(domain.TemplateRunPlanFinished),
		string(domain.TemplateRunLockReleased),
		string(domain.TemplateRunCompleted),
	}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %#v, want %#v", events, want)
	}
}

func TestTemplateRunWorkflowWaitsForApplyApproval(t *testing.T) {
	t.Parallel()

	env := newTemplateRunWorkflowTestEnvironment(t)
	input := templateRunWorkflowInput(domain.OperationApply)
	var statuses []domain.TemplateRunStatus
	var commands []domain.TerraformCommandType
	mockPrepareWorkspace(t, env)
	mockFetchSource(t, env)
	mockRunTerraform(t, env, &commands)
	env.OnActivity(domain.RecordTemplateRunStatusActivityName, mock.Anything, mock.Anything).
		Return(func(_ context.Context, activityInput domain.TemplateRunStatusActivityInput) error {
			statuses = append(statuses, activityInput.Status)
			return nil
		})
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(domain.ApprovalSignalName, domain.ApprovalSignal{
			ApprovedBy: approverSubject,
		})
	}, 0)

	env.ExecuteWorkflow(TemplateRunWorkflow, input)

	assertWorkflowCompleted(t, env)
	want := []domain.TemplateRunStatus{
		domain.TemplateRunLocked,
		domain.TemplateRunWorkspacePrepared,
		domain.TemplateRunSourceFetched,
		domain.TemplateRunInitStarted,
		domain.TemplateRunInitFinished,
		domain.TemplateRunWorkspaceSelected,
		domain.TemplateRunPlanStarted,
		domain.TemplateRunPlanFinished,
		domain.TemplateRunWaitingApproval,
		domain.TemplateRunApplyStarted,
		domain.TemplateRunApplyFinished,
		domain.TemplateRunLockReleased,
		domain.TemplateRunCompleted,
	}
	if !reflect.DeepEqual(statuses, want) {
		t.Fatalf("statuses = %#v, want %#v", statuses, want)
	}
	wantCommands := []domain.TerraformCommandType{
		domain.TerraformCommandInit,
		domain.TerraformCommandSelectWorkspace,
		domain.TerraformCommandPlan,
		domain.TerraformCommandApply,
	}
	if !reflect.DeepEqual(commands, wantCommands) {
		t.Fatalf("commands = %#v, want %#v", commands, wantCommands)
	}
}

func TestTemplateRunWorkflowCancelsApplyWhileWaitingApproval(t *testing.T) {
	t.Parallel()

	env := newTemplateRunWorkflowTestEnvironment(t)
	input := templateRunWorkflowInput(domain.OperationApply)
	var statuses []domain.TemplateRunStatus
	var commands []domain.TerraformCommandType
	mockPrepareWorkspace(t, env)
	mockFetchSource(t, env)
	mockRunTerraform(t, env, &commands)
	env.OnActivity(domain.RecordTemplateRunStatusActivityName, mock.Anything, mock.Anything).
		Return(func(_ context.Context, activityInput domain.TemplateRunStatusActivityInput) error {
			statuses = append(statuses, activityInput.Status)
			if activityInput.Status == domain.TemplateRunWaitingApproval {
				env.SignalWorkflow(domain.CancelSignalName, domain.CancelSignal{
					RequestedBy: requesterSubject,
					Reason:      "superseded",
				})
			}
			return nil
		})

	env.ExecuteWorkflow(TemplateRunWorkflow, input)

	assertWorkflowCompleted(t, env)
	want := []domain.TemplateRunStatus{
		domain.TemplateRunLocked,
		domain.TemplateRunWorkspacePrepared,
		domain.TemplateRunSourceFetched,
		domain.TemplateRunInitStarted,
		domain.TemplateRunInitFinished,
		domain.TemplateRunWorkspaceSelected,
		domain.TemplateRunPlanStarted,
		domain.TemplateRunPlanFinished,
		domain.TemplateRunWaitingApproval,
		domain.TemplateRunCanceled,
	}
	if !reflect.DeepEqual(statuses, want) {
		t.Fatalf("statuses = %#v, want %#v", statuses, want)
	}
	wantCommands := []domain.TerraformCommandType{
		domain.TerraformCommandInit,
		domain.TerraformCommandSelectWorkspace,
		domain.TerraformCommandPlan,
	}
	if !reflect.DeepEqual(commands, wantCommands) {
		t.Fatalf("commands = %#v, want %#v", commands, wantCommands)
	}
}

func TestTemplateRunWorkflowCancelsPlanWhenSignalArrivesDuringTerraform(t *testing.T) {
	t.Parallel()

	env := newTemplateRunWorkflowTestEnvironment(t)
	input := templateRunWorkflowInput(domain.OperationPlan)
	var statuses []domain.TemplateRunStatus
	var commands []domain.TerraformCommandType
	mockPrepareWorkspace(t, env)
	mockFetchSource(t, env)
	mockRunTerraform(t, env, &commands)
	env.OnActivity(domain.RecordTemplateRunStatusActivityName, mock.Anything, mock.Anything).
		Return(func(_ context.Context, activityInput domain.TemplateRunStatusActivityInput) error {
			statuses = append(statuses, activityInput.Status)
			return nil
		})
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(domain.CancelSignalName, domain.CancelSignal{
			RequestedBy: requesterSubject,
			Reason:      "stop retries",
		})
	}, 0)

	env.ExecuteWorkflow(TemplateRunWorkflow, input)

	assertWorkflowCompleted(t, env)
	if len(statuses) == 0 || statuses[len(statuses)-1] != domain.TemplateRunCanceled {
		t.Fatalf("final status = %#v, want canceled", statuses)
	}
	for _, status := range statuses {
		if status == domain.TemplateRunPlanFinished || status == domain.TemplateRunCompleted {
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
	input := templateRunWorkflowInput(domain.OperationDestroy)
	var statuses []domain.TemplateRunStatus
	var commands []domain.TerraformCommandType
	mockPrepareWorkspace(t, env)
	mockFetchSource(t, env)
	mockRunTerraform(t, env, &commands)
	env.OnActivity(domain.RecordTemplateRunStatusActivityName, mock.Anything, mock.Anything).
		Return(func(_ context.Context, activityInput domain.TemplateRunStatusActivityInput) error {
			statuses = append(statuses, activityInput.Status)
			if activityInput.Status == domain.TemplateRunWaitingApproval {
				env.SignalWorkflow(domain.CancelSignalName, domain.CancelSignal{
					RequestedBy: requesterSubject,
					Reason:      "superseded",
				})
			}
			return nil
		})

	env.ExecuteWorkflow(TemplateRunWorkflow, input)

	assertWorkflowCompleted(t, env)
	want := []domain.TemplateRunStatus{
		domain.TemplateRunLocked,
		domain.TemplateRunWorkspacePrepared,
		domain.TemplateRunSourceFetched,
		domain.TemplateRunInitStarted,
		domain.TemplateRunInitFinished,
		domain.TemplateRunWorkspaceSelected,
		domain.TemplateRunWaitingApproval,
		domain.TemplateRunCanceled,
	}
	if !reflect.DeepEqual(statuses, want) {
		t.Fatalf("statuses = %#v, want %#v", statuses, want)
	}
	wantCommands := []domain.TerraformCommandType{
		domain.TerraformCommandInit,
		domain.TerraformCommandSelectWorkspace,
	}
	if !reflect.DeepEqual(commands, wantCommands) {
		t.Fatalf("commands = %#v, want %#v", commands, wantCommands)
	}
}

func TestTemplateRunWorkflowRecordsDestroyStatuses(t *testing.T) {
	t.Parallel()

	env := newTemplateRunWorkflowTestEnvironment(t)
	input := templateRunWorkflowInput(domain.OperationDestroy)
	var statuses []domain.TemplateRunStatus
	var commands []domain.TerraformCommandType
	mockPrepareWorkspace(t, env)
	mockFetchSource(t, env)
	mockRunTerraform(t, env, &commands)
	env.OnActivity(domain.RecordTemplateRunStatusActivityName, mock.Anything, mock.Anything).
		Return(func(_ context.Context, activityInput domain.TemplateRunStatusActivityInput) error {
			statuses = append(statuses, activityInput.Status)
			return nil
		})
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(domain.ApprovalSignalName, domain.ApprovalSignal{
			ApprovedBy: approverSubject,
		})
	}, 0)

	env.ExecuteWorkflow(TemplateRunWorkflow, input)

	assertWorkflowCompleted(t, env)
	want := []domain.TemplateRunStatus{
		domain.TemplateRunLocked,
		domain.TemplateRunWorkspacePrepared,
		domain.TemplateRunSourceFetched,
		domain.TemplateRunInitStarted,
		domain.TemplateRunInitFinished,
		domain.TemplateRunWorkspaceSelected,
		domain.TemplateRunWaitingApproval,
		domain.TemplateRunApproved,
		domain.TemplateRunDestroyStarted,
		domain.TemplateRunDestroyFinished,
		domain.TemplateRunLockReleased,
		domain.TemplateRunCompleted,
	}
	if !reflect.DeepEqual(statuses, want) {
		t.Fatalf("statuses = %#v, want %#v", statuses, want)
	}
	wantCommands := []domain.TerraformCommandType{
		domain.TerraformCommandInit,
		domain.TerraformCommandSelectWorkspace,
		domain.TerraformCommandDestroy,
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
	input := templateRunWorkflowInput(domain.OperationType("migrate"))
	var startedActivityNames []string
	env.SetOnActivityStartedListener(func(activityInfo *activity.Info, _ context.Context, _ converter.EncodedValues) {
		startedActivityNames = append(startedActivityNames, activityInfo.ActivityType.Name)
	})
	var statuses []domain.TemplateRunStatus
	var summaries []string
	var commands []domain.TerraformCommandType
	prepared := false
	env.OnActivity(domain.PrepareWorkspaceActivityName, mock.Anything, mock.Anything).
		Return(func(_ context.Context, _ domain.PrepareWorkspaceActivityInput) (domain.PrepareWorkspaceActivityOutput, error) {
			prepared = true
			return domain.PrepareWorkspaceActivityOutput{}, nil
		})
	mockRunTerraform(t, env, &commands)
	env.OnActivity(domain.RecordTemplateRunStatusActivityName, mock.Anything, mock.Anything).
		Return(func(_ context.Context, activityInput domain.TemplateRunStatusActivityInput) error {
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
	want := []domain.TemplateRunStatus{domain.TemplateRunFailed}
	if !reflect.DeepEqual(statuses, want) {
		t.Fatalf("statuses = %#v, want %#v", statuses, want)
	}
	if !strings.Contains(summaries[0], "unsupported template run operation") {
		t.Fatalf("error summary = %q, want the unsupported operation reason", summaries[0])
	}
	if !reflect.DeepEqual(startedActivityNames, []string{domain.RecordTemplateRunStatusActivityName}) {
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
		func(context.Context, domain.TemplateRunStatusActivityInput) error {
			return nil
		},
		activity.RegisterOptions{Name: domain.RecordTemplateRunStatusActivityName},
	)
	env.RegisterActivityWithOptions(
		func(context.Context, domain.TemplateRunLog) error {
			return nil
		},
		activity.RegisterOptions{Name: domain.RecordTemplateRunLogActivityName},
	)
	env.RegisterActivityWithOptions(
		func(context.Context, domain.SealSourceTokenActivityInput) (domain.SealSourceTokenActivityOutput, error) {
			return domain.SealSourceTokenActivityOutput{}, nil
		},
		activity.RegisterOptions{Name: domain.SealSourceTokenActivityName},
	)
	env.RegisterActivityWithOptions(
		func(context.Context, domain.SealRunCredentialsActivityInput) (domain.SealRunCredentialsActivityOutput, error) {
			return domain.SealRunCredentialsActivityOutput{}, nil
		},
		activity.RegisterOptions{Name: domain.SealRunCredentialsActivityName},
	)
	env.RegisterActivityWithOptions(
		func(context.Context, domain.ReleaseRunKeyActivityInput) error {
			return nil
		},
		activity.RegisterOptions{Name: domain.ReleaseRunKeyActivityName},
	)
	env.RegisterActivityWithOptions(
		func(context.Context, domain.PrepareWorkspaceActivityInput) (domain.PrepareWorkspaceActivityOutput, error) {
			return domain.PrepareWorkspaceActivityOutput{}, nil
		},
		activity.RegisterOptions{Name: domain.PrepareWorkspaceActivityName},
	)
	env.RegisterActivityWithOptions(
		func(context.Context, domain.FetchSourceActivityInput) (domain.FetchSourceActivityOutput, error) {
			return domain.FetchSourceActivityOutput{}, nil
		},
		activity.RegisterOptions{Name: domain.FetchSourceActivityName},
	)
	env.RegisterActivityWithOptions(
		func(context.Context, domain.RunTerraformActivityInput) (domain.RunTerraformActivityOutput, error) {
			return domain.RunTerraformActivityOutput{}, nil
		},
		activity.RegisterOptions{Name: domain.RunTerraformActivityName},
	)
	return env
}

func mockPrepareWorkspace(t *testing.T, env *testsuite.TestWorkflowEnvironment) {
	t.Helper()

	env.OnActivity(domain.PrepareWorkspaceActivityName, mock.Anything, mock.Anything).
		Return(domain.PrepareWorkspaceActivityOutput{WorkspacePath: "/tmp/tflive/runs/tenant_123/run_123"}, nil)
}

func mockFetchSource(t *testing.T, env *testsuite.TestWorkflowEnvironment) {
	t.Helper()

	env.OnActivity(domain.FetchSourceActivityName, mock.Anything, mock.Anything).
		Return(domain.FetchSourceActivityOutput{TerraformPath: "/tmp/tflive/runs/tenant_123/run_123/source/modules/vpc"}, nil)
}

func mockRunTerraform(t *testing.T, env *testsuite.TestWorkflowEnvironment, commands *[]domain.TerraformCommandType) {
	t.Helper()

	env.OnActivity(domain.RunTerraformActivityName, mock.Anything, mock.Anything).
		Return(func(_ context.Context, activityInput domain.RunTerraformActivityInput) (domain.RunTerraformActivityOutput, error) {
			*commands = append(*commands, activityInput.Command)
			return domain.RunTerraformActivityOutput{}, nil
		})
}

func templateRunWorkflowInput(operation domain.OperationType) domain.TemplateRunWorkflowInput {
	return domain.TemplateRunWorkflowInput{
		RunID:           domain.TemplateRunID("run_123"),
		TenantID:        domain.TenantID("tenant_123"),
		StackTemplateID: domain.StackTemplateID("stack_template_123"),
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
	input := templateRunWorkflowInput(domain.OperationPlan)

	var prepareAttempts int
	env.OnActivity(domain.PrepareWorkspaceActivityName, mock.Anything, mock.Anything).
		Return(func(_ context.Context, _ domain.PrepareWorkspaceActivityInput) (domain.PrepareWorkspaceActivityOutput, error) {
			prepareAttempts++
			return domain.PrepareWorkspaceActivityOutput{}, errors.New("connection reset")
		})

	env.OnActivity(domain.RecordTemplateRunStatusActivityName, mock.Anything, mock.Anything).Return(nil)

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
	input := templateRunWorkflowInput(domain.OperationPlan)

	var prepareAttempts int
	env.OnActivity(domain.PrepareWorkspaceActivityName, mock.Anything, mock.Anything).
		Return(func(_ context.Context, _ domain.PrepareWorkspaceActivityInput) (domain.PrepareWorkspaceActivityOutput, error) {
			prepareAttempts++
			return domain.PrepareWorkspaceActivityOutput{}, temporal.NewNonRetryableApplicationError(
				"invalid configuration",
				"InvalidConfig",
				nil,
			)
		})

	env.OnActivity(domain.RecordTemplateRunStatusActivityName, mock.Anything, mock.Anything).Return(nil)

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
	input := templateRunWorkflowInput(domain.OperationPlan)
	var statuses []domain.TemplateRunStatusActivityInput
	env.OnActivity(domain.PrepareWorkspaceActivityName, mock.Anything, mock.Anything).
		Return(func(_ context.Context, _ domain.PrepareWorkspaceActivityInput) (domain.PrepareWorkspaceActivityOutput, error) {
			return domain.PrepareWorkspaceActivityOutput{}, temporal.NewNonRetryableApplicationError(
				"invalid configuration",
				"InvalidConfig",
				nil,
			)
		})
	env.OnActivity(domain.RecordTemplateRunStatusActivityName, mock.Anything, mock.Anything).
		Return(func(_ context.Context, status domain.TemplateRunStatusActivityInput) error {
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
	if len(statuses) == 0 || statuses[len(statuses)-1].Status != domain.TemplateRunFailed {
		t.Fatalf("final statuses = %#v, want failed", statuses)
	}
	if !strings.Contains(statuses[len(statuses)-1].ErrorSummary, "invalid configuration") {
		t.Fatalf("failure summary = %q, want root activity error", statuses[len(statuses)-1].ErrorSummary)
	}
}

func TestTemplateRunWorkflowPreservesActivityErrorWhenFailurePersistenceFails(t *testing.T) {
	t.Parallel()

	env := newTemplateRunWorkflowTestEnvironment(t)
	input := templateRunWorkflowInput(domain.OperationPlan)
	env.OnActivity(domain.PrepareWorkspaceActivityName, mock.Anything, mock.Anything).
		Return(func(_ context.Context, _ domain.PrepareWorkspaceActivityInput) (domain.PrepareWorkspaceActivityOutput, error) {
			return domain.PrepareWorkspaceActivityOutput{}, temporal.NewNonRetryableApplicationError(
				"invalid configuration",
				"InvalidConfig",
				nil,
			)
		})
	env.OnActivity(domain.RecordTemplateRunStatusActivityName, mock.Anything, mock.Anything).
		Return(func(_ context.Context, status domain.TemplateRunStatusActivityInput) error {
			if status.Status == domain.TemplateRunFailed {
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
	input := templateRunWorkflowInput(domain.OperationPlan)

	mockPrepareWorkspace(t, env)
	mockFetchSource(t, env)

	var planAttempts int
	env.OnActivity(domain.RunTerraformActivityName, mock.Anything, mock.Anything).
		Return(func(_ context.Context, activityInput domain.RunTerraformActivityInput) (domain.RunTerraformActivityOutput, error) {
			if activityInput.Command == domain.TerraformCommandPlan {
				planAttempts++
				return domain.RunTerraformActivityOutput{}, errors.New("rate limit exceeded")
			}
			return domain.RunTerraformActivityOutput{}, nil
		})

	env.OnActivity(domain.RecordTemplateRunStatusActivityName, mock.Anything, mock.Anything).Return(nil)

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
	input := templateRunWorkflowInput(domain.OperationPlan)

	mockPrepareWorkspace(t, env)
	mockFetchSource(t, env)

	var terraformAttempts int
	env.OnActivity(domain.RunTerraformActivityName, mock.Anything, mock.Anything).
		Return(func(_ context.Context, activityInput domain.RunTerraformActivityInput) (domain.RunTerraformActivityOutput, error) {
			terraformAttempts++
			if activityInput.Command == domain.TerraformCommandPlan {
				return domain.RunTerraformActivityOutput{}, temporal.NewNonRetryableApplicationError(
					"unsupported terraform command",
					"UnsupportedCommand",
					nil,
				)
			}
			return domain.RunTerraformActivityOutput{}, nil
		})

	env.OnActivity(domain.RecordTemplateRunStatusActivityName, mock.Anything, mock.Anything).Return(nil)

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

// TestTemplateRunWorkflowPinsLogIdentityToTheRun proves the control plane does
// not take the executor's word for whose run a log belongs to.
//
// RunTerraform is a data-plane activity, so its result is attacker-controlled
// once an executor is compromised. RecordTemplateRunLog upserts on
// (tenant_id, run_id, phase) and guards only that the run exists, not that it
// is this run — so a returned TenantID/RunID naming another tenant's run would
// repoint that run's object_key at a key of the executor's choosing.
//
// The workflow knows the identity from its own input and overwrites it. On the
// honest path this changes nothing: PutTemplateRunLog derives both fields from
// the activity input the workflow supplied.
func TestTemplateRunWorkflowPinsLogIdentityToTheRun(t *testing.T) {
	t.Parallel()

	env := newTemplateRunWorkflowTestEnvironment(t)
	mockPrepareWorkspace(t, env)
	env.OnActivity(domain.FetchSourceActivityName, mock.Anything, mock.Anything).
		Return(domain.FetchSourceActivityOutput{TerraformPath: "run/workspace/source"}, nil)

	// A hostile executor claims every log belongs to another tenant's run.
	env.OnActivity(domain.RunTerraformActivityName, mock.Anything, mock.Anything).
		Return(func(_ context.Context, input domain.RunTerraformActivityInput) (domain.RunTerraformActivityOutput, error) {
			return domain.RunTerraformActivityOutput{Log: domain.TemplateRunLog{
				TenantID:  domain.TenantID("tenant_victim"),
				RunID:     domain.TemplateRunID("run_victim"),
				Phase:     string(input.Command),
				ObjectKey: "logs/" + string(input.Command) + ".log",
			}}, nil
		})

	var recorded []domain.TemplateRunLog
	env.OnActivity(domain.RecordTemplateRunLogActivityName, mock.Anything, mock.Anything).
		Return(func(_ context.Context, log domain.TemplateRunLog) error {
			recorded = append(recorded, log)
			return nil
		})
	env.OnActivity(domain.RecordTemplateRunStatusActivityName, mock.Anything, mock.Anything).
		Return(nil)

	input := templateRunWorkflowInput(domain.OperationPlan)
	env.ExecuteWorkflow(TemplateRunWorkflow, input)

	assertWorkflowCompleted(t, env)
	if len(recorded) == 0 {
		t.Fatal("no log metadata recorded")
	}
	for _, log := range recorded {
		if log.TenantID != input.TenantID {
			t.Fatalf("recorded %s log tenant = %q, want %q", log.Phase, log.TenantID, input.TenantID)
		}
		if log.RunID != input.RunID {
			t.Fatalf("recorded %s log run = %q, want %q", log.Phase, log.RunID, input.RunID)
		}
	}
}

// The same pin has to hold on the failure path, where the log metadata arrives
// in an ApplicationError's details rather than the activity result.
func TestTemplateRunWorkflowPinsLogIdentityOfFailedCommand(t *testing.T) {
	t.Parallel()

	env := newTemplateRunWorkflowTestEnvironment(t)
	mockPrepareWorkspace(t, env)
	env.OnActivity(domain.FetchSourceActivityName, mock.Anything, mock.Anything).
		Return(domain.FetchSourceActivityOutput{TerraformPath: "run/workspace/source"}, nil)

	hostileLog := domain.TemplateRunLog{
		TenantID:  domain.TenantID("tenant_victim"),
		RunID:     domain.TemplateRunID("run_victim"),
		Phase:     "plan",
		ObjectKey: "logs/plan.log",
	}
	var recorded []domain.TemplateRunLog
	env.OnActivity(domain.RunTerraformActivityName, mock.Anything, mock.Anything).
		Return(func(_ context.Context, input domain.RunTerraformActivityInput) (domain.RunTerraformActivityOutput, error) {
			if input.Command != domain.TerraformCommandPlan {
				return domain.RunTerraformActivityOutput{}, nil
			}
			return domain.RunTerraformActivityOutput{}, temporal.NewApplicationError(
				"terraform plan failed",
				domain.TerraformCommandFailedErrorType,
				hostileLog,
			)
		})
	env.OnActivity(domain.RecordTemplateRunLogActivityName, mock.Anything, mock.Anything).
		Return(func(_ context.Context, log domain.TemplateRunLog) error {
			recorded = append(recorded, log)
			return nil
		})
	env.OnActivity(domain.RecordTemplateRunStatusActivityName, mock.Anything, mock.Anything).
		Return(nil)

	input := templateRunWorkflowInput(domain.OperationPlan)
	env.ExecuteWorkflow(TemplateRunWorkflow, input)

	if len(recorded) == 0 {
		t.Fatal("no log metadata recorded for the failed command")
	}
	for _, log := range recorded {
		if log.TenantID != input.TenantID || log.RunID != input.RunID {
			t.Fatalf("recorded %s log identity = %q/%q, want %q/%q",
				log.Phase, log.TenantID, log.RunID, input.TenantID, input.RunID)
		}
	}
}
