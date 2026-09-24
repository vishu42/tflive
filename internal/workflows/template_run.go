package workflows

import (
	"errors"
	"fmt"
	"time"

	"github.com/vishu42/tflive/internal/domain"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// A template run is two workflows, one per phase: TemplatePlanWorkflow and
// TemplateApplyWorkflow. Each hands its work to lifecycle, which alone moves
// the run along its status, and does that work in an executor session, as the
// run's steps.
//
// Both are written as methods on run (below), which holds the control plane,
// and hands the executor session to the methods that work in it.

const (
	// planSessionCreationTimeout fails a plan that finds no executor free in a
	// minute: nothing has been decided yet, and starting again is cheap.
	planSessionCreationTimeout = time.Minute
	// applySessionCreationTimeout is longer, because the run was just
	// approved: failing it for an executor that was briefly full would send
	// its approver back through a whole plan.
	applySessionCreationTimeout = 10 * time.Minute
)

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

// run is a template run, as both workflows carry it out. It holds the
// control-queue context and the run's input, and it is the run's Recorder.
//
// Every method schedules control-plane work, the run's writes and the secrets
// sealed for its executor, on r.ctx. A method that also takes a ctx does
// executor work on it: ctx is the phase's session.
type run struct {
	ctx   workflow.Context
	input domain.TemplateRunWorkflowInput
}

// newRun sets the baseline options for every activity the run schedules
// through it: control-plane work, on the control queue. Executor work goes
// through the session inSession opens, on the execution queue.
func newRun(ctx workflow.Context, input domain.TemplateRunWorkflowInput) *run {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		TaskQueue:           domain.ControlTaskQueue,
		StartToCloseTimeout: time.Minute,
		RetryPolicy:         defaultRunRetryPolicy,
	})
	return &run{ctx: ctx, input: input}
}

// runKind is which of a run's two workflows is carrying it: the plan, or the
// apply of a plan someone approved (or of an auto-approved run).
type runKind int

const (
	runPlan runKind = iota
	runApply
)

// TemplatePlanWorkflow plans a template run. A plan run ends with its plan.
// An apply or destroy run saves a plan with changes, which then waits for
// approval as a database row, not as a workflow: this workflow ends there,
// releasing its executor session, and approving the plan starts
// TemplateApplyWorkflow. Nothing holds an executor while a person decides, and
// no approval can arrive after a session has timed out.
//
// A plan with no changes completes the run. An auto-approved apply run never
// comes here: it starts TemplateApplyWorkflow directly.
//
// Its status is lifecycle's.
func TemplatePlanWorkflow(ctx workflow.Context, input domain.TemplateRunWorkflowInput) error {
	r := newRun(ctx, input)
	return r.lifecycle(runPlan, func() (domain.RunTerraformActivityOutput, error) {
		var planned domain.RunTerraformActivityOutput
		err := r.inSession(planSessionCreationTimeout, func(ctx workflow.Context) (err error) {
			planned, err = r.plan(ctx)
			return err
		})
		return planned, err
	})
}

// TemplateApplyWorkflow applies the plan a run saved, once someone approved it.
// It usually lands on a different executor from the plan, so it fetches the
// same commit again, puts the saved plan and its lock file back, and runs init
// before applying exactly that plan. tofu refuses a saved plan whose state has
// moved on since, so an approval can never apply something other than what
// was reviewed.
//
// An auto-approved apply run starts here with no plan at all, and applies the
// way `tofu apply -auto-approve` does.
func TemplateApplyWorkflow(ctx workflow.Context, input domain.TemplateRunWorkflowInput) error {
	r := newRun(ctx, input)
	return r.lifecycle(runApply, func() (domain.RunTerraformActivityOutput, error) {
		return domain.RunTerraformActivityOutput{}, r.inSession(applySessionCreationTimeout, r.apply)
	})
}

// validateOperation rejects an operation a workflow does not run before any
// session or workspace exists. Returning the error lets lifecycle record the single
// Failed status, with the reason attached as the run's error summary.
//
// The plan workflow runs every operation's plan, but never an auto-approved
// one, which has no plan. The apply workflow applies apply and destroy runs,
// and only an apply run can be auto-approved.
func (r *run) validateOperation(applying bool) error {
	switch r.input.Operation {
	case domain.OperationPlan:
		if !applying && !r.input.AutoApprove {
			return nil
		}
	case domain.OperationApply:
		if applying || !r.input.AutoApprove {
			return nil
		}
	case domain.OperationDestroy:
		if !r.input.AutoApprove {
			return nil
		}
	}
	if r.input.AutoApprove {
		return fmt.Errorf("unsupported template run operation %q with auto-approve", r.input.Operation)
	}
	return fmt.Errorf("unsupported template run operation %q", r.input.Operation)
}

// plan is the plan phase's work. An apply or destroy run keeps a plan that has
// changes, for someone to approve; a plan run keeps nothing but its log.
func (r *run) plan(ctx workflow.Context) (domain.RunTerraformActivityOutput, error) {
	ws, err := r.openWorkspace(ctx, domain.RunPhasePlan)
	defer r.closeWorkspace(ctx, ws, false)
	if err != nil {
		return domain.RunTerraformActivityOutput{}, err
	}
	if ws, err = r.readyWorkspace(ctx, ws, false); err != nil {
		return domain.RunTerraformActivityOutput{}, err
	}

	command := domain.TerraformCommandPlan
	if r.input.Operation == domain.OperationDestroy {
		command = domain.TerraformCommandPlanDestroy
	}
	output, err := r.terraform(ctx, ws, command)
	if err != nil {
		return domain.RunTerraformActivityOutput{}, err
	}
	if !output.HasChanges || r.input.Operation == domain.OperationPlan {
		return output, nil
	}
	return output, r.savePlan(ctx, ws)
}

// apply is the apply phase's work: it applies an approved run's saved plan,
// or, for an auto-approved apply run, applies without one. It records what the
// command does to the stack template around it: a destroy marks the template
// destroying before it starts and destroyed once it succeeds, and an apply
// records what it applied as live, with the counts an auto-approved apply
// reports, since it had no plan to count.
func (r *run) apply(ctx workflow.Context) error {
	ws, err := r.openWorkspace(ctx, domain.RunPhaseApply)
	defer r.closeWorkspace(ctx, ws, true)
	if err != nil {
		return err
	}
	if ws, err = r.readyWorkspace(ctx, ws, !r.input.AutoApprove); err != nil {
		return err
	}

	destroy := r.input.Operation == domain.OperationDestroy
	command := domain.TerraformCommandApply
	switch {
	case r.input.AutoApprove:
		command = domain.TerraformCommandApplyAutoApprove
	case destroy:
		command = domain.TerraformCommandDestroy
	}

	if destroy {
		if err := r.event(domain.TemplateRunDestroying, nil); err != nil {
			return err
		}
	}
	output, err := r.terraform(ctx, ws, command)
	if err != nil {
		return err
	}
	if destroy {
		return r.event(domain.TemplateRunDestroyed, nil)
	}
	var summary *domain.PlanSummary
	if r.input.AutoApprove {
		summary = &output.Summary
	}
	return r.event(domain.TemplateRunApplied, summary)
}

// workspace is one phase's working directory on its executor, and the key the
// executor holds for the run. Each phase opens its own, since the apply
// usually lands on a different executor from the plan.
type workspace struct {
	// phase is the half of the run the workspace serves, which names its
	// commands' logs.
	phase         domain.RunPhase
	path          string
	terraformPath string
	// publicKey is the executor's sealing key for this run; everything secret
	// the executor needs is sealed to it on the control plane.
	publicKey []byte
}

// openWorkspace creates the phase's filesystem workspace on the executor, and
// a sealing key for the run. Workflows cannot create directories directly
// because Temporal workflows must stay deterministic, so the side effect lives
// in PrepareWorkspace.
func (r *run) openWorkspace(ctx workflow.Context, phase domain.RunPhase) (workspace, error) {
	ws := workspace{phase: phase}
	var output domain.PrepareWorkspaceActivityOutput
	if err := ExecuteStep(
		ctx, r, domain.TemplateRunStepPreparingWorkspace,
		domain.PrepareWorkspaceActivityName,
		domain.PrepareWorkspaceActivityInput{RunID: r.input.RunID, TenantID: r.input.TenantID},
	).Get(ctx, &output); err != nil {
		return ws, err
	}
	ws.path = output.WorkspacePath
	ws.publicKey = output.PublicKey
	return ws, nil
}

// readyWorkspace readies an open workspace for Terraform: the source at the
// run's commit, then init and workspace selection. With restorePlan, the saved
// plan and its lock file are put back once the source is in place, so init
// installs the providers the plan was made with.
func (r *run) readyWorkspace(ctx workflow.Context, ws workspace, restorePlan bool) (workspace, error) {
	var err error
	if ws.terraformPath, err = r.fetchSource(ctx, ws); err != nil {
		return ws, err
	}
	if restorePlan {
		if err := r.restoreSavedPlan(ctx, ws); err != nil {
			return ws, err
		}
	}
	if _, err := r.terraform(ctx, ws, domain.TerraformCommandInit); err != nil {
		return ws, err
	}
	_, err = r.terraform(ctx, ws, domain.TerraformCommandSelectWorkspace)
	return ws, err
}

// closeWorkspace releases the run's key and deletes the workspace, and with
// deletePlan its saved plan too. It is best effort: a session that already
// failed has no executor left to clean up, and what it leaves behind is a
// directory and an unreadable plan file. The key ring evicts abandoned keys on
// its own.
func (r *run) closeWorkspace(ctx workflow.Context, ws workspace, deletePlan bool) {
	if ws.publicKey != nil {
		_ = workflow.ExecuteActivity(
			ctx,
			domain.ReleaseRunKeyActivityName,
			domain.ReleaseRunKeyActivityInput{TenantID: r.input.TenantID, RunID: r.input.RunID},
		).Get(ctx, nil)
	}
	if ws.path != "" {
		_ = workflow.ExecuteActivity(
			ctx,
			domain.CleanupWorkspaceActivityName,
			domain.CleanupWorkspaceActivityInput{
				TenantID:      r.input.TenantID,
				RunID:         r.input.RunID,
				WorkspacePath: ws.path,
				DeletePlan:    deletePlan,
			},
		).Get(ctx, nil)
	}
}

// fetchSource checks the run's commit out into the workspace, with a
// repository token sealed to the workspace's key, and returns the path of the
// Terraform root within it.
func (r *run) fetchSource(ctx workflow.Context, ws workspace) (string, error) {
	var token domain.SealSourceTokenActivityOutput
	if err := workflow.ExecuteActivity(r.ctx, domain.SealSourceTokenActivityName, domain.SealSourceTokenActivityInput{
		RepoOwner: r.input.RepoOwner,
		RepoName:  r.input.RepoName,
		PublicKey: ws.publicKey,
	}).Get(r.ctx, &token); err != nil {
		return "", err
	}

	// A clone of a large repository can outlast the default one-minute budget,
	// so FetchSource gets a longer one of its own rather than raising the
	// default for every other activity in the run.
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 3 * time.Minute,
		RetryPolicy:         defaultRunRetryPolicy,
	})
	var output domain.FetchSourceActivityOutput
	if err := ExecuteStep(ctx, r, domain.TemplateRunStepFetchingSource, domain.FetchSourceActivityName, domain.FetchSourceActivityInput{
		RunID:             r.input.RunID,
		TenantID:          r.input.TenantID,
		WorkspacePath:     ws.path,
		RepoOwner:         r.input.RepoOwner,
		RepoName:          r.input.RepoName,
		SourceRef:         r.input.SelectedRef,
		ResolvedCommitSHA: r.input.ResolvedCommitSHA,
		RootPath:          r.input.RootPath,
		SealedToken:       token.SealedToken,
		FetchHint:         token.FetchHint,
	}).Get(ctx, &output); err != nil {
		return "", err
	}
	return output.TerraformPath, nil
}

// savePlan uploads the plan the workspace just made, under a plan key created
// for it.
func (r *run) savePlan(ctx workflow.Context, ws workspace) error {
	return r.transferPlan(ctx, ws, domain.TemplateRunStepSavingPlan, domain.UploadPlanActivityName, true)
}

// restoreSavedPlan puts the saved plan back into the workspace, with the plan
// key it was saved with.
func (r *run) restoreSavedPlan(ctx workflow.Context, ws workspace) error {
	return r.transferPlan(ctx, ws, domain.TemplateRunStepRestoringPlan, domain.DownloadPlanActivityName, false)
}

// transferPlan uploads or downloads the run's saved plan on the executor, as
// step, with the run's plan key sealed to the workspace's key. The plan phase
// creates the plan key; the apply phase reads the one the plan was saved with.
func (r *run) transferPlan(ctx workflow.Context, ws workspace, step domain.TemplateRunStep, activityName string, createKey bool) error {
	var planKey domain.SealPlanKeyActivityOutput
	if err := workflow.ExecuteActivity(r.ctx, domain.SealPlanKeyActivityName, domain.SealPlanKeyActivityInput{
		TenantID:  r.input.TenantID,
		RunID:     r.input.RunID,
		PublicKey: ws.publicKey,
		Create:    createKey,
	}).Get(r.ctx, &planKey); err != nil {
		return err
	}

	// A saved plan can run to tens of megabytes, more than the default budget
	// is sized for.
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Minute,
		RetryPolicy:         defaultRunRetryPolicy,
	})
	return ExecuteStep(ctx, r, step, activityName, domain.PlanArtifactActivityInput{
		TenantID:      r.input.TenantID,
		RunID:         r.input.RunID,
		TerraformPath: ws.terraformPath,
		SealedPlanKey: planKey.SealedPlanKey,
	}).Get(ctx, nil)
}

// terraform runs one Terraform command on the executor as the step it is,
// with the run's credentials sealed to the workspace's key, then records the
// log the executor uploaded for it. A failed command's log is what explains the
// failure, so it is recorded before the error propagates.
func (r *run) terraform(ctx workflow.Context, ws workspace, command domain.TerraformCommandType) (domain.RunTerraformActivityOutput, error) {
	// Sealed for every command rather than once per run: credentials are read
	// fresh each time, and an apply can start a day after its plan.
	var credentials domain.SealRunCredentialsActivityOutput
	if err := workflow.ExecuteActivity(r.ctx, domain.SealRunCredentialsActivityName, domain.SealRunCredentialsActivityInput{
		TenantID:        r.input.TenantID,
		StackTemplateID: r.input.StackTemplateID,
		PublicKey:       ws.publicKey,
	}).Get(r.ctx, &credentials); err != nil {
		return domain.RunTerraformActivityOutput{}, err
	}

	// Every Terraform command gets the configured budget and the more generous
	// retry policy, including init and workspace selection: init downloads
	// providers and modules over the network, which is no more predictable
	// than the plan that follows it.
	//
	// The heartbeat timeout is what distinguishes a command that is working
	// from one whose executor is gone: without it, a dead executor is
	// indistinguishable from a slow apply until the whole Terraform timeout
	// expires.
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: r.terraformTimeout(),
		HeartbeatTimeout:    domain.TerraformHeartbeatTimeout,
		RetryPolicy:         terraformRetryPolicy,
	})
	var output domain.RunTerraformActivityOutput
	if err := ExecuteStep(ctx, r, terraformCommandSteps[command], domain.RunTerraformActivityName, domain.RunTerraformActivityInput{
		RunID:             r.input.RunID,
		TenantID:          r.input.TenantID,
		StackTemplateID:   r.input.StackTemplateID,
		WorkspacePath:     ws.path,
		TerraformPath:     ws.terraformPath,
		WorkspaceName:     r.input.WorkspaceName,
		Command:           command,
		ConfigJSON:        r.input.ConfigJSON,
		RunPhase:          ws.phase,
		SealedEnvironment: credentials.SealedEnvironment,
	}).Get(ctx, &output); err != nil {
		if log, ok := failedCommandLog(err); ok {
			if logErr := r.log(log); logErr != nil {
				return domain.RunTerraformActivityOutput{}, fmt.Errorf("%w (also failed to record its log: %w)", err, logErr)
			}
		}
		return domain.RunTerraformActivityOutput{}, err
	}
	if err := r.log(output.Log); err != nil {
		return domain.RunTerraformActivityOutput{}, err
	}
	return output, nil
}

// terraformTimeout is how long one Terraform command may run before Temporal
// fails it. It comes from the deployment's configuration, stamped onto the
// input when the run was dispatched, so a run keeps the budget it started with
// even if the control plane is reconfigured while it is in flight. Zero means
// nothing configured it, which is the default.
func (r *run) terraformTimeout() time.Duration {
	if r.input.TerraformTimeout > 0 {
		return r.input.TerraformTimeout
	}
	return domain.DefaultTerraformTimeout
}

// terraformCommandSteps is the step each Terraform command is. Planning a
// destroy is planning, and applying one is applying: the run's operation
// already says it is a destroy.
var terraformCommandSteps = map[domain.TerraformCommandType]domain.TemplateRunStep{
	domain.TerraformCommandInit:             domain.TemplateRunStepInitializing,
	domain.TerraformCommandSelectWorkspace:  domain.TemplateRunStepSelectingWorkspace,
	domain.TerraformCommandPlan:             domain.TemplateRunStepPlanning,
	domain.TerraformCommandPlanDestroy:      domain.TemplateRunStepPlanning,
	domain.TerraformCommandApply:            domain.TemplateRunStepApplying,
	domain.TerraformCommandDestroy:          domain.TemplateRunStepApplying,
	domain.TerraformCommandApplyAutoApprove: domain.TemplateRunStepApplying,
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

// Record records the step the run is starting, for the people watching it.
func (r *run) Record(step domain.TemplateRunStep) error {
	return workflow.ExecuteActivity(
		r.ctx,
		domain.RecordTemplateRunStepActivityName,
		domain.TemplateRunStepActivityInput{
			RunID:    r.input.RunID,
			TenantID: r.input.TenantID,
			Step:     step,
		},
	).Get(r.ctx, nil)
}

// inSession runs fn in an executor session, on the session's context, and
// completes the session after, whether fn succeeded or not. Waiting for the
// executor is the run's first step: an approved apply can wait minutes for a
// free one.
//
// CreateSession takes its base queue from the context's activity options, so
// naming the execution queue here is what places the session, and every
// activity fn schedules on its context, on an executor host.
func (r *run) inSession(creationTimeout time.Duration, fn func(workflow.Context) error) error {
	if err := r.Record(domain.TemplateRunStepWaitingForExecutor); err != nil {
		return err
	}
	sessionCtx, err := workflow.CreateSession(workflow.WithTaskQueue(r.ctx, domain.ExecutionTaskQueue), &workflow.SessionOptions{
		CreationTimeout:  creationTimeout,
		ExecutionTimeout: 24 * time.Hour,
	})
	if err != nil {
		return err
	}
	defer workflow.CompleteSession(sessionCtx)
	return fn(sessionCtx)
}

// lifecycle carries a run through its status, and is the only code that writes
// it: every status write is here, and so is every activity that moves the
// status as part of its own work, BeginApply and FinishPlan.
//
// A run whose operation its workflow does not run fails without ever running.
// A plan starts running here. An apply claims its run instead, moving it from
// approved (or, auto-approved, queued) to running; a lost claim ends quietly,
// since the discard that won it already recorded the run's end.
//
// A finished plan is settled: a plan with nothing to change, or a plan run,
// completes, and an apply or destroy run with changes waits for approval,
// which FinishPlan records. A destroy with nothing left to destroy records that
// it is destroyed before it completes, which is what moves its stack template.
// A finished apply completes.
//
// Any error marks the run failed before lifecycle returns it. If recording the
// failure also fails, the run's persisted status will not match reality, so
// both errors are surfaced: the original wrapped with %w to stay matchable by
// callers, the persistence error appended as context.
func (r *run) lifecycle(kind runKind, work func() (domain.RunTerraformActivityOutput, error)) (err error) {
	setStatus := func(status domain.TemplateRunStatus, errorSummary string) error {
		return workflow.ExecuteActivity(
			r.ctx,
			domain.RecordTemplateRunStatusActivityName,
			domain.TemplateRunStatusActivityInput{
				RunID:           r.input.RunID,
				TenantID:        r.input.TenantID,
				StackTemplateID: r.input.StackTemplateID,
				Operation:       r.input.Operation,
				Status:          status,
				ErrorSummary:    errorSummary,
			},
		).Get(r.ctx, nil)
	}
	defer func() {
		if err == nil {
			return
		}
		if failureErr := setStatus(domain.TemplateRunFailed, fmt.Sprintf("template run activity failed: %v", err)); failureErr != nil {
			err = fmt.Errorf("%w (also failed to persist failure status: %w)", err, failureErr)
		}
	}()

	if err := r.validateOperation(kind == runApply); err != nil {
		return err
	}
	switch kind {
	case runPlan:
		if err := setStatus(domain.TemplateRunRunning, ""); err != nil {
			return err
		}
	case runApply:
		var claim domain.BeginApplyActivityOutput
		if err := workflow.ExecuteActivity(r.ctx, domain.BeginApplyActivityName, domain.BeginApplyActivityInput{
			TenantID:    r.input.TenantID,
			RunID:       r.input.RunID,
			AutoApprove: r.input.AutoApprove,
		}).Get(r.ctx, &claim); err != nil {
			return err
		}
		if !claim.Claimed {
			return nil
		}
	}

	output, err := work()
	if err != nil {
		return err
	}
	if kind == runApply {
		return setStatus(domain.TemplateRunCompleted, "")
	}

	var outcome domain.PlanOutcome
	if err := workflow.ExecuteActivity(r.ctx, domain.FinishPlanActivityName, domain.FinishPlanActivityInput{
		TenantID:        r.input.TenantID,
		RunID:           r.input.RunID,
		StackTemplateID: r.input.StackTemplateID,
		Operation:       r.input.Operation,
		HasChanges:      output.HasChanges,
		Summary:         output.Summary,
	}).Get(r.ctx, &outcome); err != nil {
		return err
	}
	switch outcome {
	case domain.PlanOutcomeNoChanges:
		if r.input.Operation == domain.OperationDestroy {
			if err := r.event(domain.TemplateRunDestroyed, nil); err != nil {
				return err
			}
		}
		return setStatus(domain.TemplateRunCompleted, "")
	case domain.PlanOutcomePlanned:
		return setStatus(domain.TemplateRunCompleted, "")
	case domain.PlanOutcomeWaiting:
		return nil
	default:
		return fmt.Errorf("unknown plan outcome %q", outcome)
	}
}

// event records something the run did to its stack template, with the counts
// it carries, if any.
func (r *run) event(event domain.TemplateRunEvent, summary *domain.PlanSummary) error {
	return workflow.ExecuteActivity(
		r.ctx,
		domain.RecordTemplateRunEventActivityName,
		domain.TemplateRunEventActivityInput{
			RunID:           r.input.RunID,
			TenantID:        r.input.TenantID,
			StackTemplateID: r.input.StackTemplateID,
			Operation:       r.input.Operation,
			Event:           event,
			Summary:         summary,
		},
	).Get(r.ctx, nil)
}

// log hands the metadata of a log the executor uploaded to the control plane,
// which owns the database. A command that uploaded nothing is skipped.
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
func (r *run) log(log domain.TemplateRunLog) error {
	if log.ObjectKey == "" {
		return nil
	}
	log.TenantID = r.input.TenantID
	log.RunID = r.input.RunID
	return workflow.ExecuteActivity(
		r.ctx,
		domain.RecordTemplateRunLogActivityName,
		log,
	).Get(r.ctx, nil)
}
