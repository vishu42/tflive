package workflows

import (
	"fmt"
	"time"

	"github.com/vishu42/tflive/internal/domain"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// A template run is carried out at three levels, and each level is handed only
// what it may use, so where a write belongs is settled by the types, not by
// convention:
//
//   - run is the workflow. It alone moves the run along its lifecycle
//     (status), and it opens jobs.
//   - job is one executor session, seen as the ordered steps it runs. It
//     records steps, the run's events and command logs, and has no status
//     write.
//   - session is the executor work itself. It records nothing.
//
// Go's privacy stops at the package, so the lines are drawn by which value a
// function is given: phase code receives a *job, and executor work is reachable
// only through job.step, which records the step first.

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
		err := r.runJob(planSessionCreationTimeout, domain.RunPhasePlan, false, func(j *job) (err error) {
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
		if err := r.runJob(applySessionCreationTimeout, domain.RunPhaseApply, true, apply); err != nil {
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

// runJob runs fn as one job on an executor and always gives the executor back
// afterwards, whether fn succeeded or not. Waiting for the executor is the
// job's first step: an approved apply can wait minutes for a free one.
//
// fn receives the job, never the run: a job records steps and events, and has
// no way to change the run's status.
func (r *run) runJob(creationTimeout time.Duration, phase domain.RunPhase, deletePlan bool, fn func(*job) error) error {
	record := r.recorder()
	if err := record.step(domain.TemplateRunStepWaitingForExecutor); err != nil {
		return err
	}
	s, err := openSession(r.ctx, r.input, phase, creationTimeout)
	if err != nil {
		return err
	}
	err = fn(&job{input: r.input, record: record, session: s})
	s.close(deletePlan)
	return err
}
