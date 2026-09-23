package workflows

import (
	"errors"
	"fmt"
	"time"

	"github.com/vishu42/tflive/internal/domain"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// A template run is carried out at three levels, and each level is handed only
// what it may use, so where a write belongs follows from the method set each
// level works through:
//
//   - run is the workflow. It alone moves the run along its lifecycle
//     (status), and it opens jobs.
//   - runJob is one executor session, seen as the ordered steps it runs. It
//     embeds the generic job (job.go), records steps, the run's events and
//     command logs, and has no status write.
//   - session is the executor work itself. It records nothing.
//
// Phase code receives a *runJob, never the run, so it has no status write, and
// its path to executor work is step, which records the step first. Go's
// privacy stops at the package, so the compiler does not forbid reaching past
// that path from inside this package (the job's worker, or a recorder's
// context): the method sets make the right path the only obvious one, and
// reaching past it is visible in review. Enforcement by the compiler would
// need each level in a package of its own.

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
// Any error marks the run failed before the workflow returns it.
func TemplatePlanWorkflow(ctx workflow.Context, input domain.TemplateRunWorkflowInput) error {
	r := newRun(ctx, input)
	return r.finish(func() error {
		if err := r.start(); err != nil {
			return err
		}
		var planned domain.RunTerraformActivityOutput
		err := r.openJob(planSessionCreationTimeout, domain.RunPhasePlan, false, func(j *runJob) (err error) {
			planned, err = plan(j)
			return err
		})
		if err != nil {
			return err
		}
		return r.settlePlan(planned)
	}())
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
	return r.finish(func() error {
		claimed, err := r.claim()
		if err != nil || !claimed {
			return err
		}
		if err := r.openJob(applySessionCreationTimeout, domain.RunPhaseApply, true, apply); err != nil {
			return err
		}
		return r.complete()
	}())
}

// run is a template run's workflow: the one place its status changes. It holds
// the control-queue context, so everything it schedules itself is
// control-plane work.
type run struct {
	ctx   workflow.Context
	input domain.TemplateRunWorkflowInput
}

// newRun sets the baseline options for every activity scheduled on the
// workflow context. Activities scheduled on it are control-plane work, so they
// go to the control queue; execution work goes through a job's session on the
// execution queue.
func newRun(ctx workflow.Context, input domain.TemplateRunWorkflowInput) *run {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		TaskQueue:           domain.ControlTaskQueue,
		StartToCloseTimeout: time.Minute,
		RetryPolicy:         defaultRunRetryPolicy,
	})
	return &run{ctx: ctx, input: input}
}

// finish owns terminal error handling for both workflows. If recording a
// failure also fails, the run's persisted status will not match reality, so
// both errors are surfaced: the original wrapped with %w to stay matchable by
// callers, the persistence error appended as context.
func (r *run) finish(err error) error {
	if err == nil {
		return nil
	}
	if failureErr := r.recordStatus(domain.TemplateRunFailed, fmt.Sprintf("template run activity failed: %v", err)); failureErr != nil {
		return fmt.Errorf("%w (also failed to persist failure status: %v)", err, failureErr)
	}
	return err
}

// validateOperation rejects an operation this workflow does not run before any
// session or workspace exists. Returning the error lets finish record the
// single Failed status, with the reason attached as the run's error summary.
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

// start begins the plan workflow's run: queued becomes running.
func (r *run) start() error {
	if err := r.validateOperation(false); err != nil {
		return err
	}
	return r.recordStatus(domain.TemplateRunRunning, "")
}

// claim begins the apply workflow's run: approved (or, auto-approved, queued)
// becomes running, or nothing happens because the plan was discarded first. A
// lost claim is not a failure, since the discard already recorded the run's
// end.
func (r *run) claim() (bool, error) {
	if err := r.validateOperation(true); err != nil {
		return false, err
	}
	var claim domain.BeginApplyActivityOutput
	if err := workflow.ExecuteActivity(r.ctx, domain.BeginApplyActivityName, domain.BeginApplyActivityInput{
		TenantID:    r.input.TenantID,
		RunID:       r.input.RunID,
		AutoApprove: r.input.AutoApprove,
	}).Get(r.ctx, &claim); err != nil {
		return false, err
	}
	return claim.Claimed, nil
}

// settlePlan records a finished plan and settles what happens next. An apply
// or destroy run with changes waits for someone to approve its saved plan; a
// plan run, or any plan with nothing to change, completes.
func (r *run) settlePlan(planned domain.RunTerraformActivityOutput) error {
	var outcome domain.PlanOutcome
	if err := workflow.ExecuteActivity(r.ctx, domain.FinishPlanActivityName, domain.FinishPlanActivityInput{
		TenantID:        r.input.TenantID,
		RunID:           r.input.RunID,
		StackTemplateID: r.input.StackTemplateID,
		Operation:       r.input.Operation,
		HasChanges:      planned.HasChanges,
		Summary:         planned.Summary,
	}).Get(r.ctx, &outcome); err != nil {
		return err
	}

	switch outcome {
	case domain.PlanOutcomeNoChanges:
		// Nothing left to destroy is a destroy that is done: recording it is
		// what moves the stack template to destroyed.
		if r.input.Operation == domain.OperationDestroy {
			if err := r.recorder().event(domain.TemplateRunDestroyed, nil); err != nil {
				return err
			}
		}
		return r.complete()
	case domain.PlanOutcomePlanned:
		return r.complete()
	case domain.PlanOutcomeWaiting:
		return nil
	default:
		return fmt.Errorf("unknown plan outcome %q", outcome)
	}
}

func (r *run) complete() error {
	return r.recordStatus(domain.TemplateRunCompleted, "")
}

// recordStatus moves the run along its lifecycle. Only run has it.
func (r *run) recordStatus(status domain.TemplateRunStatus, errorSummary string) error {
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

func (r *run) recorder() recorder {
	return recorder{ctx: r.ctx, input: r.input}
}

// openJob runs fn as one job on an executor and always gives the executor back
// afterwards, whether fn succeeded or not. Waiting for the executor is the
// job's first step: an approved apply can wait minutes for a free one.
//
// fn receives the job, never the run: a job records steps and events, and has
// no way to change the run's status.
func (r *run) openJob(creationTimeout time.Duration, phase domain.RunPhase, deletePlan bool, fn func(*runJob) error) error {
	record := r.recorder()
	if err := record.step(domain.TemplateRunStepWaitingForExecutor); err != nil {
		return err
	}
	s, err := openSession(r.ctx, r.input, phase, creationTimeout)
	if err != nil {
		return err
	}
	err = fn(newRunJob(r.input, record, s))
	s.close(deletePlan)
	return err
}

// runJob is a template run's job: one executor session, seen as the ordered
// steps it runs. It records the run's steps, events and command logs. It has
// no status write: only run moves the run along its lifecycle.
type runJob struct {
	job[domain.TemplateRunStep, *session]
	input  domain.TemplateRunWorkflowInput
	record recorder
}

// newRunJob opens a job over s whose steps are recorded as the run's.
func newRunJob(input domain.TemplateRunWorkflowInput, record recorder, s *session) *runJob {
	return &runJob{
		job:    job[domain.TemplateRunStep, *session]{recordStep: record.step, worker: s},
		input:  input,
		record: record,
	}
}

// terraform runs one Terraform command as the step it is, then records the log
// the executor uploaded for it. A failed command's log is what explains the
// failure, so it is recorded before the error propagates.
func (j *runJob) terraform(command domain.TerraformCommandType) (domain.RunTerraformActivityOutput, error) {
	var output domain.RunTerraformActivityOutput
	err := j.step(terraformCommandSteps[command], func(s *session) (err error) {
		output, err = s.terraform(command)
		return err
	})
	if err != nil {
		if log, ok := failedCommandLog(err); ok {
			if logErr := j.record.log(log); logErr != nil {
				return domain.RunTerraformActivityOutput{}, fmt.Errorf("%w (also failed to record its log: %v)", err, logErr)
			}
		}
		return domain.RunTerraformActivityOutput{}, err
	}
	if err := j.record.log(output.Log); err != nil {
		return domain.RunTerraformActivityOutput{}, err
	}
	return output, nil
}

// event records something the job did to the run's stack template.
func (j *runJob) event(event domain.TemplateRunEvent, summary *domain.PlanSummary) error {
	return j.record.event(event, summary)
}

// plan is the plan phase's job. An apply or destroy run keeps a plan that has
// changes, for someone to approve; a plan run keeps nothing but its log.
func plan(j *runJob) (domain.RunTerraformActivityOutput, error) {
	if err := prepareWorkspace(j, false); err != nil {
		return domain.RunTerraformActivityOutput{}, err
	}
	command := domain.TerraformCommandPlan
	if j.input.Operation == domain.OperationDestroy {
		command = domain.TerraformCommandPlanDestroy
	}
	output, err := j.terraform(command)
	if err != nil {
		return domain.RunTerraformActivityOutput{}, err
	}
	if !output.HasChanges || j.input.Operation == domain.OperationPlan {
		return output, nil
	}
	return output, j.step(domain.TemplateRunStepSavingPlan, (*session).savePlan)
}

// apply is the apply phase's job: it applies an approved run's saved plan, or,
// for an auto-approved apply run, applies without one. It records what the
// command does to the stack template around it: a destroy marks the template
// destroying before it starts and destroyed once it succeeds, and an apply
// records what it applied as live, with the counts an auto-approved apply
// reports, since it had no plan to count.
func apply(j *runJob) error {
	if err := prepareWorkspace(j, !j.input.AutoApprove); err != nil {
		return err
	}
	destroy := j.input.Operation == domain.OperationDestroy
	command := domain.TerraformCommandApply
	switch {
	case j.input.AutoApprove:
		command = domain.TerraformCommandApplyAutoApprove
	case destroy:
		command = domain.TerraformCommandDestroy
	}

	if destroy {
		if err := j.event(domain.TemplateRunDestroying, nil); err != nil {
			return err
		}
	}
	output, err := j.terraform(command)
	if err != nil {
		return err
	}
	if destroy {
		return j.event(domain.TemplateRunDestroyed, nil)
	}
	var summary *domain.PlanSummary
	if j.input.AutoApprove {
		summary = &output.Summary
	}
	return j.event(domain.TemplateRunApplied, summary)
}

// prepareWorkspace readies a workspace for Terraform: a directory and a key on
// the executor, the source at the run's commit, then init and workspace
// selection. With restorePlan, the saved plan and its lock file are put back
// once the source is in place, so init installs the providers the plan was
// made with.
func prepareWorkspace(j *runJob, restorePlan bool) error {
	if err := j.step(domain.TemplateRunStepPreparingWorkspace, (*session).prepare); err != nil {
		return err
	}
	if err := j.step(domain.TemplateRunStepFetchingSource, (*session).fetchSource); err != nil {
		return err
	}
	if restorePlan {
		if err := j.step(domain.TemplateRunStepRestoringPlan, (*session).restorePlan); err != nil {
			return err
		}
	}
	if _, err := j.terraform(domain.TerraformCommandInit); err != nil {
		return err
	}
	_, err := j.terraform(domain.TerraformCommandSelectWorkspace)
	return err
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

// recorder writes what a run reports while it works: the step it is on, the
// events it causes, and its command logs. It holds the control-queue context,
// so every write lands on the control plane. It has no status write.
type recorder struct {
	ctx   workflow.Context
	input domain.TemplateRunWorkflowInput
}

// step records the step the run is starting, for the people watching it. It
// is written before the work, never after, so a slow step shows as itself
// rather than as the step before it.
func (rec recorder) step(step domain.TemplateRunStep) error {
	return workflow.ExecuteActivity(
		rec.ctx,
		domain.RecordTemplateRunStepActivityName,
		domain.TemplateRunStepActivityInput{
			RunID:    rec.input.RunID,
			TenantID: rec.input.TenantID,
			Step:     step,
		},
	).Get(rec.ctx, nil)
}

// event records something the run did to its stack template, with the counts
// it carries, if any.
func (rec recorder) event(event domain.TemplateRunEvent, summary *domain.PlanSummary) error {
	return workflow.ExecuteActivity(
		rec.ctx,
		domain.RecordTemplateRunEventActivityName,
		domain.TemplateRunEventActivityInput{
			RunID:           rec.input.RunID,
			TenantID:        rec.input.TenantID,
			StackTemplateID: rec.input.StackTemplateID,
			Operation:       rec.input.Operation,
			Event:           event,
			Summary:         summary,
		},
	).Get(rec.ctx, nil)
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
func (rec recorder) log(log domain.TemplateRunLog) error {
	if log.ObjectKey == "" {
		return nil
	}
	log.TenantID = rec.input.TenantID
	log.RunID = rec.input.RunID
	return workflow.ExecuteActivity(
		rec.ctx,
		domain.RecordTemplateRunLogActivityName,
		log,
	).Get(rec.ctx, nil)
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
