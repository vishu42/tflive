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

// TemplateRunWorkflow plans a template run and saves the plan. A plan with
// changes then waits for approval as a database row, not as a workflow: this
// workflow ends there, releasing its executor session, and approving the plan
// starts TemplateApplyWorkflow. Nothing holds an executor while a person
// decides, and no approval can arrive after a session has timed out.
//
// A plan with no changes completes the run. An auto-approved run is approved
// as its plan finishes, and this workflow goes straight on to apply it.
//
// Cancellation is not a workflow failure. A cancel signal already drove the run
// through its canceled status transitions before errTemplateRunCanceled bubbled
// up, so it is swallowed here and the workflow completes successfully; anything
// else marks the run failed before returning.
func TemplateRunWorkflow(ctx workflow.Context, input domain.TemplateRunWorkflowInput) error {
	run := newTemplateRunWorkflow(ctx, input)
	return run.finish(run.planPhase())
}

// TemplateApplyWorkflow applies the plan a run saved, once someone approved it.
// It usually lands on a different executor from the plan, so it fetches the
// same commit again, puts the saved plan and its lock file back, and runs init
// before applying exactly that plan. tofu refuses a saved plan whose state has
// moved on since, so an approval can never apply something other than what
// was reviewed.
func TemplateApplyWorkflow(ctx workflow.Context, input domain.TemplateRunWorkflowInput) error {
	run := newTemplateRunWorkflow(ctx, input)
	return run.finish(run.applyPhase())
}

// newTemplateRunWorkflow sets the baseline options for every activity
// scheduled on the workflow context. Activities scheduled on ctx itself are
// control-plane work (status writes), so they go to the control queue;
// execution work goes through a session on the execution queue.
func newTemplateRunWorkflow(ctx workflow.Context, input domain.TemplateRunWorkflowInput) *templateRunWorkflow {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		TaskQueue:           domain.ControlTaskQueue,
		StartToCloseTimeout: time.Minute,
		RetryPolicy:         defaultRunRetryPolicy,
	})
	return &templateRunWorkflow{ctx: ctx, input: input}
}

// finish owns terminal error handling for both workflows. If recording a
// failure also fails, the run's persisted status will not match reality, so
// both errors are surfaced: the original wrapped with %w to stay matchable by
// callers, the persistence error appended as context.
func (run *templateRunWorkflow) finish(err error) error {
	if err == nil || errors.Is(err, errTemplateRunCanceled) {
		return nil
	}
	if failureErr := run.recordFailure(err); failureErr != nil {
		return fmt.Errorf("%w (also failed to persist failure status: %v)", err, failureErr)
	}
	return err
}

type templateRunWorkflow struct {
	ctx           workflow.Context
	sessionCtx    workflow.Context
	input         domain.TemplateRunWorkflowInput
	workspacePath string
	terraformPath string
	// publicKey is the executor's sealing key for this run; everything secret
	// the executor needs is sealed to it on the control plane.
	publicKey []byte
	// applying is set for the apply phase, whose setup steps (prepare, fetch,
	// init, select) repeat the plan phase's and are not recorded again: the
	// timeline reads approved, locked, apply_started, apply_finished.
	applying bool
	// applyCommand is the apply phase's command, apply or destroy. Its init and
	// workspace selection log into that command's log rather than their own,
	// which the plan phase already wrote and would otherwise be replaced.
	applyCommand domain.TerraformCommandType
}

// validateOperation rejects an unsupported operation before any session or
// workspace exists. Returning the error lets finish record the single Failed
// status, with the reason attached as the run's error summary.
func (run *templateRunWorkflow) validateOperation() error {
	switch run.input.Operation {
	case domain.OperationPlan, domain.OperationDestroy:
		return nil
	default:
		return fmt.Errorf("unsupported template run operation %q", run.input.Operation)
	}
}

// planPhase plans the run, saves a plan that has changes, and settles what
// happens next.
func (run *templateRunWorkflow) planPhase() error {
	if err := run.validateOperation(); err != nil {
		return err
	}

	var plan domain.RunTerraformActivityOutput
	err := run.withSession(planSessionCreationTimeout, false, func() error {
		if err := run.prepareWorkspace(nil); err != nil {
			return err
		}
		output, err := run.runTerraform(domain.TerraformCommandPlan)
		if err != nil {
			return err
		}
		plan = output
		if !plan.HasChanges {
			return nil
		}
		sealedPlanKey, err := run.sealPlanKey(true)
		if err != nil {
			return err
		}
		return run.planArtifact(domain.UploadPlanActivityName, sealedPlanKey)
	})
	if err != nil {
		return err
	}

	var outcome domain.PlanOutcome
	if err := workflow.ExecuteActivity(run.ctx, domain.FinishPlanActivityName, domain.FinishPlanActivityInput{
		TenantID:        run.input.TenantID,
		RunID:           run.input.RunID,
		StackTemplateID: run.input.StackTemplateID,
		Operation:       run.input.Operation,
		HasChanges:      plan.HasChanges,
		Summary:         plan.Summary,
		AutoApprove:     run.input.AutoApprove,
	}).Get(run.ctx, &outcome); err != nil {
		return err
	}

	switch outcome {
	case domain.PlanOutcomeNoChanges:
		// Nothing left to destroy is a destroy that is done: recording it is
		// what moves the stack template to destroyed.
		if run.input.Operation == domain.OperationDestroy {
			if err := run.recordStatus(domain.TemplateRunDestroyFinished); err != nil {
				return err
			}
		}
		return run.complete()
	case domain.PlanOutcomeApproved:
		return run.applyPhase()
	case domain.PlanOutcomeWaiting, domain.PlanOutcomeCanceled:
		return nil
	default:
		return fmt.Errorf("unknown plan outcome %q", outcome)
	}
}

// applyPhase applies an approved run's saved plan.
//
// It begins by claiming the run: approved becomes locked, or nothing happens
// because the run was canceled after it was approved. A lost claim is not a
// failure, since the cancellation already recorded the run's end.
func (run *templateRunWorkflow) applyPhase() error {
	if err := run.validateOperation(); err != nil {
		return err
	}
	var claim domain.BeginApplyActivityOutput
	if err := workflow.ExecuteActivity(run.ctx, domain.BeginApplyActivityName, domain.BeginApplyActivityInput{
		TenantID: run.input.TenantID,
		RunID:    run.input.RunID,
	}).Get(run.ctx, &claim); err != nil {
		return err
	}
	if !claim.Claimed {
		return nil
	}

	run.applying = true
	command := domain.TerraformCommandApply
	if run.input.Operation == domain.OperationDestroy {
		command = domain.TerraformCommandDestroy
	}
	run.applyCommand = command
	err := run.withSession(applySessionCreationTimeout, true, func() error {
		restorePlan := func() error {
			sealedPlanKey, err := run.sealPlanKey(false)
			if err != nil {
				return err
			}
			return run.planArtifact(domain.DownloadPlanActivityName, sealedPlanKey)
		}
		if err := run.prepareWorkspace(restorePlan); err != nil {
			return err
		}
		_, err := run.runTerraform(command)
		return err
	})
	if err != nil {
		return err
	}
	return run.complete()
}

const (
	// planSessionCreationTimeout fails a plan that finds no executor free in a
	// minute: nothing has been decided yet, and starting again is cheap.
	planSessionCreationTimeout = time.Minute
	// applySessionCreationTimeout is longer, because the run was just
	// approved: failing it for an executor that was briefly full would send
	// its approver back through a whole plan.
	applySessionCreationTimeout = 10 * time.Minute
)

// withSession runs fn inside an executor session and always tears the session
// down afterwards: the run's key is released and its workspace deleted, and on
// the apply phase its saved plan too. Teardown is best effort. A session that
// already failed has no executor left to clean up, and what it leaves behind
// is a directory and an unreadable plan file.
//
// CreateSession takes its base queue from the context's activity options, so
// naming the execution queue here is what places the session, and every
// activity later scheduled on sessionCtx, on an executor host.
func (run *templateRunWorkflow) withSession(creationTimeout time.Duration, deletePlan bool, fn func() error) error {
	executionCtx := workflow.WithTaskQueue(run.ctx, domain.ExecutionTaskQueue)
	sessionCtx, err := workflow.CreateSession(executionCtx, &workflow.SessionOptions{
		CreationTimeout:  creationTimeout,
		ExecutionTimeout: 24 * time.Hour,
	})
	if err != nil {
		return err
	}
	run.sessionCtx = sessionCtx

	err = fn()
	run.releaseKey()
	run.cleanupWorkspace(deletePlan)
	workflow.CompleteSession(sessionCtx)
	run.sessionCtx = nil
	run.publicKey = nil
	return err
}

// prepareWorkspace readies a workspace for Terraform: a directory and a key on
// the executor, the source at the run's commit, then init and workspace
// selection. beforeInit runs once the source is in place; the apply phase uses
// it to put the saved plan and its lock file back, so init installs the
// providers the plan was made with.
func (run *templateRunWorkflow) prepareWorkspace(beforeInit func() error) error {
	if err := run.recordSetupStatus(domain.TemplateRunLocked); err != nil {
		return err
	}
	if err := run.prepareLocalWorkspace(); err != nil {
		return err
	}
	if err := run.recordSetupStatus(domain.TemplateRunWorkspacePrepared); err != nil {
		return err
	}
	if err := run.fetchSource(); err != nil {
		return err
	}
	if err := run.recordSetupStatus(domain.TemplateRunSourceFetched); err != nil {
		return err
	}
	if beforeInit != nil {
		if err := beforeInit(); err != nil {
			return err
		}
	}
	if _, err := run.runTerraform(domain.TerraformCommandInit); err != nil {
		return err
	}
	_, err := run.runTerraform(domain.TerraformCommandSelectWorkspace)
	return err
}

// recordSetupStatus records a workspace setup step, on the plan phase only.
func (run *templateRunWorkflow) recordSetupStatus(status domain.TemplateRunStatus) error {
	if run.applying {
		return nil
	}
	return run.recordStatus(status)
}

// prepareLocalWorkspace schedules the executor-side activity that creates the
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
	run.publicKey = output.PublicKey
	return nil
}

// releaseKey asks the executor to drop the run's sealing key. It is best effort:
// if the session already failed the executor is gone and its keys with it, and
// the key ring evicts abandoned keys on its own.
func (run *templateRunWorkflow) releaseKey() {
	if run.publicKey == nil {
		return
	}
	_ = workflow.ExecuteActivity(
		run.sessionCtx,
		domain.ReleaseRunKeyActivityName,
		domain.ReleaseRunKeyActivityInput{TenantID: run.input.TenantID, RunID: run.input.RunID},
	).Get(run.sessionCtx, nil)
}

// cleanupWorkspace asks the executor to delete the run's workspace, and with
// deletePlan its saved plan. Best effort, like releaseKey.
func (run *templateRunWorkflow) cleanupWorkspace(deletePlan bool) {
	if run.workspacePath == "" {
		return
	}
	_ = workflow.ExecuteActivity(
		run.sessionCtx,
		domain.CleanupWorkspaceActivityName,
		domain.CleanupWorkspaceActivityInput{
			TenantID:      run.input.TenantID,
			RunID:         run.input.RunID,
			WorkspacePath: run.workspacePath,
			DeletePlan:    deletePlan,
		},
	).Get(run.sessionCtx, nil)
}

// sealPlanKey gets the run's plan key sealed to this session's run key. The
// plan phase creates it; the apply phase reads the one the plan was saved with.
func (run *templateRunWorkflow) sealPlanKey(create bool) ([]byte, error) {
	var output domain.SealPlanKeyActivityOutput
	if err := workflow.ExecuteActivity(run.ctx, domain.SealPlanKeyActivityName, domain.SealPlanKeyActivityInput{
		TenantID:  run.input.TenantID,
		RunID:     run.input.RunID,
		PublicKey: run.publicKey,
		Create:    create,
	}).Get(run.ctx, &output); err != nil {
		return nil, err
	}
	return output.SealedPlanKey, nil
}

// planArtifact uploads or downloads the run's saved plan on the executor.
func (run *templateRunWorkflow) planArtifact(activityName string, sealedPlanKey []byte) error {
	// A saved plan can run to tens of megabytes, more than the default budget
	// is sized for.
	artifactCtx := workflow.WithActivityOptions(run.sessionCtx, workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Minute,
		RetryPolicy:         defaultRunRetryPolicy,
	})
	return workflow.ExecuteActivity(artifactCtx, activityName, domain.PlanArtifactActivityInput{
		TenantID:      run.input.TenantID,
		RunID:         run.input.RunID,
		TerraformPath: run.terraformPath,
		SealedPlanKey: sealedPlanKey,
	}).Get(artifactCtx, nil)
}

func (run *templateRunWorkflow) fetchSource() error {
	var token domain.SealSourceTokenActivityOutput
	if err := workflow.ExecuteActivity(
		run.ctx,
		domain.SealSourceTokenActivityName,
		domain.SealSourceTokenActivityInput{
			RepoOwner: run.input.RepoOwner,
			RepoName:  run.input.RepoName,
			PublicKey: run.publicKey,
		},
	).Get(run.ctx, &token); err != nil {
		return err
	}

	input := domain.FetchSourceActivityInput{
		RunID:             run.input.RunID,
		TenantID:          run.input.TenantID,
		WorkspacePath:     run.workspacePath,
		RepoOwner:         run.input.RepoOwner,
		RepoName:          run.input.RepoName,
		SourceRef:         run.input.SelectedRef,
		ResolvedCommitSHA: run.input.ResolvedCommitSHA,
		RootPath:          run.input.RootPath,
		SealedToken:       token.SealedToken,
		FetchHint:         token.FetchHint,
	}

	// A clone of a large repository can outlast the default one-minute budget,
	// so FetchSource gets a longer one of its own rather than raising the
	// default for every other activity in the run.
	fetchSourceCtx := workflow.WithActivityOptions(run.sessionCtx, workflow.ActivityOptions{
		StartToCloseTimeout: 3 * time.Minute,
		RetryPolicy:         defaultRunRetryPolicy,
	})

	var output domain.FetchSourceActivityOutput
	if err := workflow.ExecuteActivity(
		fetchSourceCtx,
		domain.FetchSourceActivityName,
		input,
	).Get(fetchSourceCtx, &output); err != nil {
		return err
	}
	run.terraformPath = output.TerraformPath
	return nil
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

// terraformTimeout is how long one Terraform command may run before Temporal
// fails it. It comes from the deployment's configuration, stamped onto the
// input when the run was dispatched, so a run keeps the budget it started with
// even if the control plane is reconfigured while it is in flight. Zero means
// nothing configured it, which is the default.
func (run *templateRunWorkflow) terraformTimeout() time.Duration {
	if run.input.TerraformTimeout > 0 {
		return run.input.TerraformTimeout
	}
	return domain.DefaultTerraformTimeout
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
// On the apply phase init and workspace selection are setup that the plan
// phase already recorded once, so they run without statuses.
func (run *templateRunWorkflow) runTerraform(command domain.TerraformCommandType) (domain.RunTerraformActivityOutput, error) {
	statuses := terraformCommandStatusTable[command]
	logCommand := command
	if run.applying && (command == domain.TerraformCommandInit || command == domain.TerraformCommandSelectWorkspace) {
		statuses = terraformCommandStatuses{}
		logCommand = run.applyCommand
	}
	if statuses.before != "" {
		if err := run.recordStatus(statuses.before); err != nil {
			return domain.RunTerraformActivityOutput{}, err
		}
	}

	// Sealed for every command rather than once per run: credentials are read
	// fresh each time, and an apply can start a day after its plan.
	var credentials domain.SealRunCredentialsActivityOutput
	if err := workflow.ExecuteActivity(
		run.ctx,
		domain.SealRunCredentialsActivityName,
		domain.SealRunCredentialsActivityInput{
			TenantID:        run.input.TenantID,
			StackTemplateID: run.input.StackTemplateID,
			PublicKey:       run.publicKey,
		},
	).Get(run.ctx, &credentials); err != nil {
		return domain.RunTerraformActivityOutput{}, err
	}

	input := domain.RunTerraformActivityInput{
		RunID:             run.input.RunID,
		TenantID:          run.input.TenantID,
		StackTemplateID:   run.input.StackTemplateID,
		WorkspacePath:     run.workspacePath,
		TerraformPath:     run.terraformPath,
		WorkspaceName:     run.input.WorkspaceName,
		Command:           command,
		LogCommand:        logCommand,
		ConfigJSON:        run.input.ConfigJSON,
		Destroy:           run.input.Operation == domain.OperationDestroy,
		SealedEnvironment: credentials.SealedEnvironment,
	}

	activityCtx, cancelActivity := workflow.WithCancel(run.sessionCtx)
	defer cancelActivity()

	// Every Terraform command gets the configured budget and the more generous
	// retry policy, including init and workspace selection: init downloads
	// providers and modules over the network, which is no more predictable
	// than the plan that follows it.
	//
	// The heartbeat timeout is what distinguishes a command that is working
	// from one whose executor is gone: without it, a dead executor is
	// indistinguishable from a slow apply until the whole Terraform timeout
	// expires, and a cancel signal has no path to the running process.
	terraformCtx := workflow.WithActivityOptions(activityCtx, workflow.ActivityOptions{
		StartToCloseTimeout: run.terraformTimeout(),
		HeartbeatTimeout:    domain.TerraformHeartbeatTimeout,
		RetryPolicy:         terraformRetryPolicy,
	})

	future := workflow.ExecuteActivity(
		terraformCtx,
		domain.RunTerraformActivityName,
		input,
	)
	cancelCh := workflow.GetSignalChannel(run.ctx, domain.CancelSignalName)
	selector := workflow.NewSelector(run.ctx)

	var output domain.RunTerraformActivityOutput
	var activityErr error
	var canceled bool
	selector.AddFuture(future, func(f workflow.Future) {
		activityErr = f.Get(run.ctx, &output)
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
			return domain.RunTerraformActivityOutput{}, err
		}
		return domain.RunTerraformActivityOutput{}, errTemplateRunCanceled
	}
	if activityErr != nil {
		// A failed command's log is what explains the failure, so it is recorded
		// before the error propagates.
		if log, ok := failedCommandLog(activityErr); ok {
			if err := run.recordLog(log); err != nil {
				return domain.RunTerraformActivityOutput{}, fmt.Errorf("%w (also failed to record its log: %v)", activityErr, err)
			}
		}
		return domain.RunTerraformActivityOutput{}, activityErr
	}
	if err := run.recordLog(output.Log); err != nil {
		return domain.RunTerraformActivityOutput{}, err
	}

	if statuses.after != "" {
		if err := run.recordStatus(statuses.after); err != nil {
			return domain.RunTerraformActivityOutput{}, err
		}
	}
	return output, nil
}

// recordLog hands the metadata of a log the executor uploaded to the control
// plane, which owns the database. A command that uploaded nothing is skipped.
//
// The identity is taken from the workflow's own input, never from the returned
// metadata. RunTerraform runs on the data plane, so everything it returns is
// attacker-controlled once an executor is compromised, and
// RecordTemplateRunLog upserts on (tenant_id, run_id, phase) while checking
// only that the run exists — not that it is this run. A log claiming another
// tenant's run would therefore repoint that run's object_key.
//
// On the honest path this overwrites nothing: PutTemplateRunLog derives both
// fields from the activity input the workflow supplied.
func (run *templateRunWorkflow) recordLog(log domain.TemplateRunLog) error {
	if log.ObjectKey == "" {
		return nil
	}
	log.TenantID = run.input.TenantID
	log.RunID = run.input.RunID
	return workflow.ExecuteActivity(
		run.ctx,
		domain.RecordTemplateRunLogActivityName,
		log,
	).Get(run.ctx, nil)
}

// failedCommandLog recovers the log metadata RunTerraform attaches to a command
// failure whose log was already uploaded.
func failedCommandLog(err error) (domain.TemplateRunLog, bool) {
	var applicationErr *temporal.ApplicationError
	if !errors.As(err, &applicationErr) || applicationErr.Type() != domain.TerraformCommandFailedErrorType || !applicationErr.HasDetails() {
		return domain.TemplateRunLog{}, false
	}
	var log domain.TemplateRunLog
	if err := applicationErr.Details(&log); err != nil {
		return domain.TemplateRunLog{}, false
	}
	return log, true
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
