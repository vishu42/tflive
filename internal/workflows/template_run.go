package workflows

import (
	"errors"
	"fmt"
	"time"

	"github.com/vishu42/tflive/internal/domain"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

var errTemplateRunCanceled = errors.New("template run canceled")

// defaultRunRetryPolicy is the retry policy applied to activities in the
// template-run workflow when no activity-specific override is set.
// MaximumAttempts is temporarily pinned to 1 (no automatic retries) — in
// Temporal, 0 means unlimited attempts, not zero retries, so 1 is the value
// that disables retries.
var defaultRunRetryPolicy = &temporal.RetryPolicy{
	InitialInterval:    30 * time.Second,
	BackoffCoefficient: 2.0,
	MaximumInterval:    5 * time.Minute,
	MaximumAttempts:    1,
	NonRetryableErrorTypes: []string{
		"InvalidConfig",
		"UnsupportedCommand",
	},
}

// TemplateRunWorkflow is the Temporal entry point for a single template run: it
// prepares a workspace, runs the Terraform command implied by the requested
// operation, and persists a status transition at every step. The body is kept
// thin on purpose — the run logic lives on templateRunWorkflow so it can share
// the workflow context and the workspace paths discovered along the way — and
// this function owns the concerns that must not be duplicated per operation:
// the default activity options, operation validation, session lifecycle, and
// terminal error handling.
//
// Cancellation is not a workflow failure. A cancel signal already drove the run
// through its canceled status transitions before errTemplateRunCanceled bubbled
// up, so it is swallowed here and the workflow completes successfully; anything
// else marks the run failed before returning.
//
// If recording that failure also fails, the run's persisted status will not
// match reality, so both errors are surfaced: the original wrapped with %w to
// stay matchable by callers, the persistence error appended as context.
func TemplateRunWorkflow(ctx workflow.Context, input domain.TemplateRunWorkflowInput) error {
	// Baseline options for every activity scheduled on this context. RunTerraform
	// overrides them for the long-running commands; see terraformRetryPolicy.
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: time.Minute,
		RetryPolicy:         defaultRunRetryPolicy,
	})

	run := templateRunWorkflow{
		ctx:   ctx,
		input: input,
	}

	operation, err := run.operation()
	if err != nil {
		if failureErr := run.recordFailure(err); failureErr != nil {
			return fmt.Errorf("%w (also failed to persist failure status: %v)", err, failureErr)
		}
		return err
	}

	sessionCtx, err := workflow.CreateSession(ctx, &workflow.SessionOptions{
		CreationTimeout:  time.Minute,
		ExecutionTimeout: 24 * time.Hour,
	})
	if err != nil {
		if failureErr := run.recordFailure(err); failureErr != nil {
			return fmt.Errorf("%w (also failed to persist failure status: %v)", err, failureErr)
		}
		return err
	}
	run.sessionCtx = sessionCtx

	err = run.execute(operation)
	workflow.CompleteSession(sessionCtx)
	if err != nil {
		if errors.Is(err, errTemplateRunCanceled) {
			return nil
		}
		if failureErr := run.recordFailure(err); failureErr != nil {
			return fmt.Errorf("%w (also failed to persist failure status: %v)", err, failureErr)
		}
		return err
	}
	return nil
}

type templateRunWorkflow struct {
	ctx           workflow.Context
	sessionCtx    workflow.Context
	input         domain.TemplateRunWorkflowInput
	workspacePath string
	terraformPath string
}

// execute prepares the workspace and invokes the already-resolved operation.
// Resolving the operation before creating a session means an unsupported
// operation fails without waiting for session capacity or creating workspace
// side effects.
func (run *templateRunWorkflow) execute(operation func() error) error {
	if err := run.prepareWorkspace(); err != nil {
		return err
	}
	return operation()
}

// operation maps the requested operation to the method that runs it. An
// unsupported operation is rejected here rather than recording a failure status
// directly: returning the error lets TemplateRunWorkflow's error path record the
// single Failed status, with the reason attached as the run's error summary.
func (run *templateRunWorkflow) operation() (func() error, error) {
	switch run.input.Operation {
	case domain.OperationPlan:
		return run.planOnly, nil
	case domain.OperationApply:
		return run.apply, nil
	case domain.OperationDestroy:
		return run.destroy, nil
	default:
		return nil, fmt.Errorf("unsupported template run operation %q", run.input.Operation)
	}
}

func (run *templateRunWorkflow) prepareWorkspace() error {
	if err := run.recordStatus(domain.TemplateRunLocked); err != nil {
		return err
	}
	if err := run.prepareLocalWorkspace(); err != nil {
		return err
	}
	if err := run.recordStatus(domain.TemplateRunWorkspacePrepared); err != nil {
		return err
	}
	if err := run.fetchSource(); err != nil {
		return err
	}
	if err := run.recordStatus(domain.TemplateRunSourceFetched); err != nil {
		return err
	}
	if err := run.runTerraform(domain.TerraformCommandInit); err != nil {
		return err
	}
	return run.runTerraform(domain.TerraformCommandSelectWorkspace)
}

// prepareLocalWorkspace schedules the worker-side activity that creates the
// per-run filesystem workspace and returns its absolute path. Workflows cannot
// create directories directly because Temporal workflows must stay deterministic,
// so the side effect lives in PrepareWorkspace. The returned path is stored on
// the workflow helper and reused by later RunTerraform activities as their
// working directory.
func (run *templateRunWorkflow) prepareLocalWorkspace() error {
	input := domain.PrepareWorkspaceActivityInput{
		RunID:    run.input.RunID,
		TenantID: run.input.TenantID,
	}
	var output domain.PrepareWorkspaceActivityOutput
	if err := workflow.ExecuteActivity(
		run.sessionCtx,
		domain.PrepareWorkspaceActivityName,
		input,
	).Get(run.sessionCtx, &output); err != nil {
		return err
	}
	run.workspacePath = output.WorkspacePath
	return nil
}

func (run *templateRunWorkflow) fetchSource() error {
	input := domain.FetchSourceActivityInput{
		RunID:             run.input.RunID,
		TenantID:          run.input.TenantID,
		WorkspacePath:     run.workspacePath,
		RepoOwner:         run.input.RepoOwner,
		RepoName:          run.input.RepoName,
		SourceRef:         run.input.SelectedRef,
		ResolvedCommitSHA: run.input.ResolvedCommitSHA,
		RootPath:          run.input.RootPath,
	}
	var output domain.FetchSourceActivityOutput
	if err := workflow.ExecuteActivity(
		run.sessionCtx,
		domain.FetchSourceActivityName,
		input,
	).Get(run.sessionCtx, &output); err != nil {
		return err
	}
	run.terraformPath = output.TerraformPath
	return nil
}

// planOnly stops after planning; unlike apply, it never waits for approval.
func (run *templateRunWorkflow) planOnly() error {
	if err := run.runTerraform(domain.TerraformCommandPlan); err != nil {
		return err
	}
	return run.complete()
}

func (run *templateRunWorkflow) apply() error {
	if err := run.runTerraform(domain.TerraformCommandPlan); err != nil {
		return err
	}

	if err := run.recordStatus(domain.TemplateRunWaitingApproval); err != nil {
		return err
	}

	approved, err := run.waitForApproval()
	if err != nil {
		return err
	}
	if !approved {
		return run.cancel()
	}

	if err := run.runTerraform(domain.TerraformCommandApply); err != nil {
		return err
	}
	return run.complete()
}

func (run *templateRunWorkflow) destroy() error {
	if err := run.recordStatus(domain.TemplateRunWaitingApproval); err != nil {
		return err
	}

	approved, err := run.waitForApproval()
	if err != nil {
		return err
	}
	if !approved {
		return run.cancel()
	}

	if err := run.recordStatus(domain.TemplateRunApproved); err != nil {
		return err
	}
	if err := run.runTerraform(domain.TerraformCommandDestroy); err != nil {
		return err
	}
	return run.complete()
}

func (run *templateRunWorkflow) waitForApproval() (bool, error) {
	approvalCh := workflow.GetSignalChannel(run.ctx, domain.ApprovalSignalName)
	cancelCh := workflow.GetSignalChannel(run.ctx, domain.CancelSignalName)
	selector := workflow.NewSelector(run.ctx)
	approved := false

	selector.AddReceive(approvalCh, func(channel workflow.ReceiveChannel, _ bool) {
		var signal domain.ApprovalSignal
		channel.Receive(run.ctx, &signal)
		approved = true
	})
	selector.AddReceive(cancelCh, func(channel workflow.ReceiveChannel, _ bool) {
		var signal domain.CancelSignal
		channel.Receive(run.ctx, &signal)
		approved = false
	})
	var sessionFailed bool
	selector.AddReceive(run.sessionCtx.Done(), func(_ workflow.ReceiveChannel, _ bool) {
		sessionFailed = true
	})

	selector.Select(run.ctx)
	if sessionFailed {
		return false, fmt.Errorf("template run session failed while waiting for approval: %w", workflow.ErrSessionFailed)
	}
	return approved, nil
}

// cancel records the run as canceled. CancelRequested is already persisted by
// CancelRun before the workflow ever sees the cancel signal, and Canceling and
// LockReleased have no reader and no work happening before the next write
// overwrites them, so only the terminal status is recorded here.
func (run *templateRunWorkflow) cancel() error {
	return run.recordStatus(domain.TemplateRunCanceled)
}

func (run *templateRunWorkflow) complete() error {
	if err := run.recordStatus(domain.TemplateRunLockReleased); err != nil {
		return err
	}
	return run.recordStatus(domain.TemplateRunCompleted)
}

// terraformRetryPolicy is applied to long-running Terraform commands (plan,
// apply). MaximumAttempts is temporarily pinned to 1 (no automatic retries) —
// in Temporal, 0 means unlimited attempts, not zero retries, so 1 is the
// value that disables retries.
var terraformRetryPolicy = &temporal.RetryPolicy{
	InitialInterval:    time.Minute,
	BackoffCoefficient: 2.0,
	MaximumInterval:    10 * time.Minute,
	MaximumAttempts:    1,
	NonRetryableErrorTypes: []string{
		"InvalidConfig",
		"UnsupportedCommand",
	},
}

// terraformCommandStatuses is the before/after status pair recorded around a
// Terraform command. Not every command has a before status — select_workspace
// has no meaningful "about to" signal — so before is left zero-valued there.
type terraformCommandStatuses struct {
	before domain.TemplateRunStatus
	after  domain.TemplateRunStatus
}

var terraformCommandStatusTable = map[domain.TerraformCommandType]terraformCommandStatuses{
	domain.TerraformCommandInit:            {before: domain.TemplateRunInitStarted, after: domain.TemplateRunInitFinished},
	domain.TerraformCommandSelectWorkspace: {after: domain.TemplateRunWorkspaceSelected},
	domain.TerraformCommandPlan:            {before: domain.TemplateRunPlanStarted, after: domain.TemplateRunPlanFinished},
	domain.TerraformCommandApply:           {before: domain.TemplateRunApplyStarted, after: domain.TemplateRunApplyFinished},
	domain.TerraformCommandDestroy:         {before: domain.TemplateRunDestroyStarted, after: domain.TemplateRunDestroyFinished},
}

// runTerraform executes one Terraform command, recording the before/after
// status from terraformCommandStatusTable around it. Callers only record
// statuses that aren't tied to a specific command (e.g. approval statuses).
func (run *templateRunWorkflow) runTerraform(command domain.TerraformCommandType) error {
	statuses := terraformCommandStatusTable[command]
	if statuses.before != "" {
		if err := run.recordStatus(statuses.before); err != nil {
			return err
		}
	}

	input := domain.RunTerraformActivityInput{
		RunID:           run.input.RunID,
		TenantID:        run.input.TenantID,
		StackTemplateID: run.input.StackTemplateID,
		WorkspacePath:   run.workspacePath,
		TerraformPath:   run.terraformPath,
		WorkspaceName:   run.input.WorkspaceName,
		Command:         command,
		ConfigJSON:      run.input.ConfigJSON,
	}

	activityCtx, cancelActivity := workflow.WithCancel(run.sessionCtx)
	defer cancelActivity()

	// Apply a longer timeout and more generous retry policy for Terraform
	// commands that involve cloud API calls (plan, apply). Init and workspace
	// selection use the default workflow-level options.
	terraformCtx := workflow.WithActivityOptions(activityCtx, workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Minute,
		RetryPolicy:         terraformRetryPolicy,
	})

	future := workflow.ExecuteActivity(
		terraformCtx,
		domain.RunTerraformActivityName,
		input,
	)
	cancelCh := workflow.GetSignalChannel(run.ctx, domain.CancelSignalName)
	selector := workflow.NewSelector(run.ctx)

	var activityErr error
	var canceled bool
	selector.AddFuture(future, func(f workflow.Future) {
		activityErr = f.Get(run.ctx, nil)
	})
	selector.AddReceive(cancelCh, func(channel workflow.ReceiveChannel, _ bool) {
		var signal domain.CancelSignal
		channel.Receive(run.ctx, &signal)
		cancelActivity()
		canceled = true
	})
	selector.Select(run.ctx)

	if canceled {
		if err := run.cancel(); err != nil {
			return err
		}
		return errTemplateRunCanceled
	}
	if activityErr != nil {
		return activityErr
	}

	if statuses.after != "" {
		return run.recordStatus(statuses.after)
	}
	return nil
}

func (run *templateRunWorkflow) recordStatus(status domain.TemplateRunStatus) error {
	return run.recordStatusWithSummary(status, "")
}

func (run *templateRunWorkflow) recordFailure(rootErr error) error {
	return run.recordStatusWithSummary(
		domain.TemplateRunFailed,
		fmt.Sprintf("template run activity failed: %v", rootErr),
	)
}

func (run *templateRunWorkflow) recordStatusWithSummary(status domain.TemplateRunStatus, errorSummary string) error {
	input := domain.TemplateRunStatusActivityInput{
		RunID:           run.input.RunID,
		TenantID:        run.input.TenantID,
		StackTemplateID: run.input.StackTemplateID,
		Operation:       run.input.Operation,
		Status:          status,
		ErrorSummary:    errorSummary,
	}
	return workflow.ExecuteActivity(
		run.ctx,
		domain.RecordTemplateRunStatusActivityName,
		input,
	).Get(run.ctx, nil)
}
