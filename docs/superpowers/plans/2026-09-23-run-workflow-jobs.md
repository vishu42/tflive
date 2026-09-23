# Run Workflow, Jobs and Steps Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Restructure `internal/workflows/template_run.go` into three types: `run` (the lifecycle), `job` (steps) and `session` (executor work). Each holds only what its level may use, so status can be written only by the run, steps only through a job, and executor work records nothing.

**Architecture:**
- The single `templateRunWorkflow` struct becomes `run`, `job` (with a `recorder`) and `session` (with a `sealer`), in three files.
- `job.step(step, func(*session) error)` is the only way to reach the session.
- Behaviour is unchanged. The existing workflow tests pin the exact order of every write, and two new tests pin failure behaviour.

**Tech Stack:** Go and the Temporal Go SDK (`workflow`, `testsuite`, `testify/mock`).

**Spec:** `docs/superpowers/specs/2026-09-23-run-workflow-jobs-design.md`

## Global Constraints

- **Pure restructure.** Activity order, task queues, timeouts, retry policies, payloads and the status, step, event and log writes must not change. Existing workflow tests must pass without edits.
- **Allowed writes per type:**
  - `run` writes status (`running`, `completed`, `failed`) and may do anything below.
  - `job` writes steps, events and logs through its `recorder`, and holds no status write.
  - `session` holds no recorder and writes no run state.
  - `sealer` seals secrets and may create the plan key. It has no status, step or event write.
- **Names:** `run`, `job`, `recorder`, `session`, `sealer`, `runJob`, `plan`, `apply`, `prepareWorkspace`. "Phase" keeps meaning `domain.RunPhase`.
- **Package-level names kept, because tests reference them:** `defaultRunRetryPolicy` and `terraformRetryPolicy`.
- **One package, `internal/workflows`.** Split into `template_run.go` (run), `job.go` (job and recorder) and `session.go` (session and sealer).
- **Commits:** lowercase prefix, ending with a `Co-Authored-By:` trailer naming the model that wrote them.

## Review Focus

1. **A job that fails still gives its executor back.** It releases the key, deletes the workspace and completes the session, exactly as a successful job does. Pinned by `TestTemplatePlanWorkflowClosesTheSessionOfAFailedJob`.
2. **A step whose record fails never starts its work, and the run ends `failed`.** Pinned by `TestTemplatePlanWorkflowDoesNotStartAStepItCouldNotRecord`.
3. **A failed Terraform command's log is still recorded before the run fails.** Already pinned by `TestTemplatePlanWorkflowRecordsLogOfFailedCommand` and `TestTemplatePlanWorkflowPinsLogIdentityOfFailedCommand`. Keep them passing unchanged.
4. **An apply that loses its claim opens no job and writes nothing.** Already pinned by `TestTemplateApplyWorkflowStopsWhenItLosesTheClaim`.
5. **Every executor activity runs in one session, and every write runs on the control queue.** Already pinned by `TestTemplateWorkflowsUseSessionForWorkspaceActivities` and the two `RoutesActivitiesByPlane` tests.

---

## File Structure

| File | Responsibility |
|---|---|
| `internal/workflows/template_run.go` | The two workflow functions, and `run` with its lifecycle: `newRun`, `finish`, `validateOperation`, `start`, `claim`, `settlePlan`, `complete`, `recordStatus`, `runJob`. Session timeouts and `defaultRunRetryPolicy`. |
| `internal/workflows/job.go` (new) | `job` (`step`, `terraform`, `event`), the phase functions `plan`, `apply` and `prepareWorkspace`, `recorder` (`step`, `event`, `log`), `terraformCommandSteps` and `failedCommandLog`. |
| `internal/workflows/session.go` (new) | `session` (`openSession`, `close`, `prepare`, `fetchSource`, `restorePlan`, `savePlan`, `terraform`, `releaseKey`, `cleanupWorkspace`, `planArtifact`, `terraformTimeout`), `sealer`, `terraformRetryPolicy`. |
| `internal/workflows/template_run_workflow_test.go` | Two new tests. Existing tests are unchanged. |

---

### Task 1: Run, job and session

**Files:**
- Modify: `internal/workflows/template_run.go` (full replacement)
- Create: `internal/workflows/job.go`, `internal/workflows/session.go`
- Test: `internal/workflows/template_run_workflow_test.go` (append two tests)

**Interfaces:**
- **Consumes:**
  - Domain names from the run-status branch: `domain.TemplateRunRunning`, `domain.TemplateRunCompleted`, `domain.TemplateRunFailed`.
  - Steps: `domain.TemplateRunStepWaitingForExecutor`, `…PreparingWorkspace`, `…FetchingSource`, `…RestoringPlan`, `…Initializing`, `…SelectingWorkspace`, `…Planning`, `…SavingPlan`, `…Applying`.
  - Events: `domain.TemplateRunApplied`, `domain.TemplateRunDestroying`, `domain.TemplateRunDestroyed`.
  - Activity names and inputs in `internal/domain/workflow.go`.
  - `domain.RunPhase` (`RunPhasePlan` and `RunPhaseApply`).
- **Produces:** only the unchanged public API: `TemplatePlanWorkflow` and `TemplateApplyWorkflow`, with the same signatures. Everything else is unexported.

- [ ] **Step 1: Write the two failure-behaviour tests**

These pin behaviour the current code already has. They pass before the restructure and must still pass after it. There is no RED here, because this is a refactor guarded by characterization tests.

Append to `internal/workflows/template_run_workflow_test.go`:

```go
// A job that fails still gives its executor back: the run's key is released,
// its workspace deleted and its session completed, as after a job that
// succeeds.
func TestTemplatePlanWorkflowClosesTheSessionOfAFailedJob(t *testing.T) {
	t.Parallel()

	env := newTemplateRunWorkflowTestEnvironment(t)
	var teardown []string
	env.OnActivity(domain.PrepareWorkspaceActivityName, mock.Anything, mock.Anything).
		Return(domain.PrepareWorkspaceActivityOutput{WorkspacePath: "/tmp/tflive/runs/tenant_123/run_123", PublicKey: []byte("run-key")}, nil)
	mockFetchSource(t, env)
	env.OnActivity(domain.RunTerraformActivityName, mock.Anything, mock.Anything).
		Return(func(_ context.Context, input domain.RunTerraformActivityInput) (domain.RunTerraformActivityOutput, error) {
			if input.Command == domain.TerraformCommandPlan {
				return domain.RunTerraformActivityOutput{}, errors.New("plan exploded")
			}
			return domain.RunTerraformActivityOutput{}, nil
		})
	env.OnActivity(domain.ReleaseRunKeyActivityName, mock.Anything, mock.Anything).
		Return(func(context.Context, domain.ReleaseRunKeyActivityInput) error {
			teardown = append(teardown, "release_key")
			return nil
		})
	env.OnActivity(domain.CleanupWorkspaceActivityName, mock.Anything, mock.Anything).
		Return(func(context.Context, domain.CleanupWorkspaceActivityInput) error {
			teardown = append(teardown, "cleanup_workspace")
			return nil
		})

	env.ExecuteWorkflow(TemplatePlanWorkflow, templateRunWorkflowInput(domain.OperationPlan))

	if !env.IsWorkflowCompleted() || env.GetWorkflowError() == nil {
		t.Fatal("workflow succeeded, want the failed plan to fail it")
	}
	if want := []string{"release_key", "cleanup_workspace"}; !reflect.DeepEqual(teardown, want) {
		t.Fatalf("teardown = %#v, want %#v", teardown, want)
	}
}

// A step whose record fails never starts its work, and the run ends failed.
func TestTemplatePlanWorkflowDoesNotStartAStepItCouldNotRecord(t *testing.T) {
	t.Parallel()

	env := newTemplateRunWorkflowTestEnvironment(t)
	fetched := false
	var statuses []domain.TemplateRunStatus
	mockPrepareWorkspace(t, env)
	env.OnActivity(domain.FetchSourceActivityName, mock.Anything, mock.Anything).
		Return(func(context.Context, domain.FetchSourceActivityInput) (domain.FetchSourceActivityOutput, error) {
			fetched = true
			return domain.FetchSourceActivityOutput{}, nil
		})
	env.OnActivity(domain.RecordTemplateRunStepActivityName, mock.Anything, mock.Anything).
		Return(func(_ context.Context, input domain.TemplateRunStepActivityInput) error {
			if input.Step == domain.TemplateRunStepFetchingSource {
				return errors.New("database unavailable")
			}
			return nil
		})
	env.OnActivity(domain.RecordTemplateRunStatusActivityName, mock.Anything, mock.Anything).
		Return(func(_ context.Context, input domain.TemplateRunStatusActivityInput) error {
			statuses = append(statuses, input.Status)
			return nil
		})

	env.ExecuteWorkflow(TemplatePlanWorkflow, templateRunWorkflowInput(domain.OperationPlan))

	if !env.IsWorkflowCompleted() || env.GetWorkflowError() == nil {
		t.Fatal("workflow succeeded, want the failed step write to fail it")
	}
	if fetched {
		t.Fatal("fetch source ran although its step was never recorded")
	}
	if want := []domain.TemplateRunStatus{domain.TemplateRunRunning, domain.TemplateRunFailed}; !reflect.DeepEqual(statuses, want) {
		t.Fatalf("statuses = %#v, want %#v", statuses, want)
	}
}
```

- [ ] **Step 2: Run them on the current code**

Run: `go test ./internal/workflows/ -run 'TestTemplatePlanWorkflowClosesTheSessionOfAFailedJob|TestTemplatePlanWorkflowDoesNotStartAStepItCouldNotRecord' -count=1 -v`
Expected: both PASS. If either fails, stop and report. The restructure must not start from behaviour the plan misdescribes.

Then run the whole package once and save the baseline: `go test ./internal/workflows/ -count=1`. Expected: PASS.

- [ ] **Step 3: Replace `internal/workflows/template_run.go`**

```go
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
```

- [ ] **Step 4: Create `internal/workflows/job.go`**

```go
package workflows

import (
	"errors"
	"fmt"

	"github.com/vishu42/tflive/internal/domain"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// job is one executor session, seen as the ordered steps it runs. It records
// the run's steps, events and command logs. It has no status write: only run
// moves the run along its lifecycle.
type job struct {
	input   domain.TemplateRunWorkflowInput
	record  recorder
	session *session
}

// step records step, then does its work. The session is reachable only here,
// so no executor work runs outside a named step, and no step is recorded after
// its work has started.
func (j *job) step(step domain.TemplateRunStep, work func(*session) error) error {
	if err := j.record.step(step); err != nil {
		return err
	}
	return work(j.session)
}

// terraform runs one Terraform command as the step it is, then records the log
// the executor uploaded for it. A failed command's log is what explains the
// failure, so it is recorded before the error propagates.
func (j *job) terraform(command domain.TerraformCommandType) (domain.RunTerraformActivityOutput, error) {
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
func (j *job) event(event domain.TemplateRunEvent, summary *domain.PlanSummary) error {
	return j.record.event(event, summary)
}

// plan is the plan phase's job. An apply or destroy run keeps a plan that has
// changes, for someone to approve; a plan run keeps nothing but its log.
func plan(j *job) (domain.RunTerraformActivityOutput, error) {
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
func apply(j *job) error {
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
func prepareWorkspace(j *job, restorePlan bool) error {
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
```

- [ ] **Step 5: Create `internal/workflows/session.go`**

```go
package workflows

import (
	"time"

	"github.com/vishu42/tflive/internal/domain"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// session is one executor session and the work done in it. Its context is the
// session's, so every activity it schedules runs on the executor holding the
// run's workspace. It records nothing: steps, events and logs are the job's,
// and status is the run's.
//
// Its one control-plane dependency is seal: secrets the executor needs are
// sealed on the control plane to this session's key.
type session struct {
	ctx   workflow.Context
	seal  sealer
	input domain.TemplateRunWorkflowInput
	// phase is the half of the run this session serves, which names its
	// commands' logs.
	phase         domain.RunPhase
	workspacePath string
	terraformPath string
	// publicKey is the executor's sealing key for this run; everything secret
	// the executor needs is sealed to it on the control plane.
	publicKey []byte
}

// openSession creates an executor session. CreateSession takes its base queue
// from the context's activity options, so naming the execution queue here is
// what places the session, and every activity later scheduled on it, on an
// executor host.
func openSession(controlCtx workflow.Context, input domain.TemplateRunWorkflowInput, phase domain.RunPhase, creationTimeout time.Duration) (*session, error) {
	executionCtx := workflow.WithTaskQueue(controlCtx, domain.ExecutionTaskQueue)
	ctx, err := workflow.CreateSession(executionCtx, &workflow.SessionOptions{
		CreationTimeout:  creationTimeout,
		ExecutionTimeout: 24 * time.Hour,
	})
	if err != nil {
		return nil, err
	}
	return &session{
		ctx:   ctx,
		seal:  sealer{ctx: controlCtx, input: input},
		input: input,
		phase: phase,
	}, nil
}

// close tears the session down: the run's key is released and its workspace
// deleted, and with deletePlan its saved plan too. Teardown is best effort. A
// session that already failed has no executor left to clean up, and what it
// leaves behind is a directory and an unreadable plan file.
func (s *session) close(deletePlan bool) {
	s.releaseKey()
	s.cleanupWorkspace(deletePlan)
	workflow.CompleteSession(s.ctx)
}

// prepare creates the per-run filesystem workspace on the executor and keeps
// its path and the run's sealing key. Workflows cannot create directories
// directly because Temporal workflows must stay deterministic, so the side
// effect lives in PrepareWorkspace.
func (s *session) prepare() error {
	var output domain.PrepareWorkspaceActivityOutput
	if err := workflow.ExecuteActivity(
		s.ctx,
		domain.PrepareWorkspaceActivityName,
		domain.PrepareWorkspaceActivityInput{RunID: s.input.RunID, TenantID: s.input.TenantID},
	).Get(s.ctx, &output); err != nil {
		return err
	}
	s.workspacePath = output.WorkspacePath
	s.publicKey = output.PublicKey
	return nil
}

// fetchSource checks the run's commit out into the workspace, with a
// repository token sealed to this session's key.
func (s *session) fetchSource() error {
	token, err := s.seal.sourceToken(s.publicKey)
	if err != nil {
		return err
	}

	// A clone of a large repository can outlast the default one-minute budget,
	// so FetchSource gets a longer one of its own rather than raising the
	// default for every other activity in the run.
	fetchSourceCtx := workflow.WithActivityOptions(s.ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 3 * time.Minute,
		RetryPolicy:         defaultRunRetryPolicy,
	})

	var output domain.FetchSourceActivityOutput
	if err := workflow.ExecuteActivity(
		fetchSourceCtx,
		domain.FetchSourceActivityName,
		domain.FetchSourceActivityInput{
			RunID:             s.input.RunID,
			TenantID:          s.input.TenantID,
			WorkspacePath:     s.workspacePath,
			RepoOwner:         s.input.RepoOwner,
			RepoName:          s.input.RepoName,
			SourceRef:         s.input.SelectedRef,
			ResolvedCommitSHA: s.input.ResolvedCommitSHA,
			RootPath:          s.input.RootPath,
			SealedToken:       token.SealedToken,
			FetchHint:         token.FetchHint,
		},
	).Get(fetchSourceCtx, &output); err != nil {
		return err
	}
	s.terraformPath = output.TerraformPath
	return nil
}

// restorePlan puts the saved plan back into the workspace, with the plan key
// it was saved with, sealed to this session's key.
func (s *session) restorePlan() error {
	sealedPlanKey, err := s.seal.planKey(s.publicKey, false)
	if err != nil {
		return err
	}
	return s.planArtifact(domain.DownloadPlanActivityName, sealedPlanKey)
}

// savePlan uploads the plan the session just made, under a plan key created
// for it and sealed to this session's key.
func (s *session) savePlan() error {
	sealedPlanKey, err := s.seal.planKey(s.publicKey, true)
	if err != nil {
		return err
	}
	return s.planArtifact(domain.UploadPlanActivityName, sealedPlanKey)
}

// planArtifact uploads or downloads the run's saved plan on the executor.
func (s *session) planArtifact(activityName string, sealedPlanKey []byte) error {
	// A saved plan can run to tens of megabytes, more than the default budget
	// is sized for.
	artifactCtx := workflow.WithActivityOptions(s.ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Minute,
		RetryPolicy:         defaultRunRetryPolicy,
	})
	return workflow.ExecuteActivity(artifactCtx, activityName, domain.PlanArtifactActivityInput{
		TenantID:      s.input.TenantID,
		RunID:         s.input.RunID,
		TerraformPath: s.terraformPath,
		SealedPlanKey: sealedPlanKey,
	}).Get(artifactCtx, nil)
}

// terraform runs one Terraform command on the executor, with the run's
// credentials sealed to this session's key.
func (s *session) terraform(command domain.TerraformCommandType) (domain.RunTerraformActivityOutput, error) {
	// Sealed for every command rather than once per run: credentials are read
	// fresh each time, and an apply can start a day after its plan.
	sealedEnvironment, err := s.seal.credentials(s.publicKey)
	if err != nil {
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
	terraformCtx := workflow.WithActivityOptions(s.ctx, workflow.ActivityOptions{
		StartToCloseTimeout: s.terraformTimeout(),
		HeartbeatTimeout:    domain.TerraformHeartbeatTimeout,
		RetryPolicy:         terraformRetryPolicy,
	})

	var output domain.RunTerraformActivityOutput
	err = workflow.ExecuteActivity(terraformCtx, domain.RunTerraformActivityName, domain.RunTerraformActivityInput{
		RunID:             s.input.RunID,
		TenantID:          s.input.TenantID,
		StackTemplateID:   s.input.StackTemplateID,
		WorkspacePath:     s.workspacePath,
		TerraformPath:     s.terraformPath,
		WorkspaceName:     s.input.WorkspaceName,
		Command:           command,
		ConfigJSON:        s.input.ConfigJSON,
		RunPhase:          s.phase,
		SealedEnvironment: sealedEnvironment,
	}).Get(terraformCtx, &output)
	return output, err
}

// releaseKey asks the executor to drop the run's sealing key. It is best effort:
// if the session already failed the executor is gone and its keys with it, and
// the key ring evicts abandoned keys on its own.
func (s *session) releaseKey() {
	if s.publicKey == nil {
		return
	}
	_ = workflow.ExecuteActivity(
		s.ctx,
		domain.ReleaseRunKeyActivityName,
		domain.ReleaseRunKeyActivityInput{TenantID: s.input.TenantID, RunID: s.input.RunID},
	).Get(s.ctx, nil)
}

// cleanupWorkspace asks the executor to delete the run's workspace, and with
// deletePlan its saved plan. Best effort, like releaseKey.
func (s *session) cleanupWorkspace(deletePlan bool) {
	if s.workspacePath == "" {
		return
	}
	_ = workflow.ExecuteActivity(
		s.ctx,
		domain.CleanupWorkspaceActivityName,
		domain.CleanupWorkspaceActivityInput{
			TenantID:      s.input.TenantID,
			RunID:         s.input.RunID,
			WorkspacePath: s.workspacePath,
			DeletePlan:    deletePlan,
		},
	).Get(s.ctx, nil)
}

// terraformTimeout is how long one Terraform command may run before Temporal
// fails it. It comes from the deployment's configuration, stamped onto the
// input when the run was dispatched, so a run keeps the budget it started with
// even if the control plane is reconfigured while it is in flight. Zero means
// nothing configured it, which is the default.
func (s *session) terraformTimeout() time.Duration {
	if s.input.TerraformTimeout > 0 {
		return s.input.TerraformTimeout
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

// sealer seals secrets to a session's key on the control plane, so they cross
// Temporal as ciphertext only that executor can open. It reads secrets and
// can create the run's plan key, and nothing else: it has no status, step or
// event write.
type sealer struct {
	ctx   workflow.Context
	input domain.TemplateRunWorkflowInput
}

func (seal sealer) sourceToken(publicKey []byte) (domain.SealSourceTokenActivityOutput, error) {
	var output domain.SealSourceTokenActivityOutput
	err := workflow.ExecuteActivity(seal.ctx, domain.SealSourceTokenActivityName, domain.SealSourceTokenActivityInput{
		RepoOwner: seal.input.RepoOwner,
		RepoName:  seal.input.RepoName,
		PublicKey: publicKey,
	}).Get(seal.ctx, &output)
	return output, err
}

// planKey seals the run's plan key. The plan phase creates it; the apply
// phase reads the one the plan was saved with.
func (seal sealer) planKey(publicKey []byte, create bool) ([]byte, error) {
	var output domain.SealPlanKeyActivityOutput
	if err := workflow.ExecuteActivity(seal.ctx, domain.SealPlanKeyActivityName, domain.SealPlanKeyActivityInput{
		TenantID:  seal.input.TenantID,
		RunID:     seal.input.RunID,
		PublicKey: publicKey,
		Create:    create,
	}).Get(seal.ctx, &output); err != nil {
		return nil, err
	}
	return output.SealedPlanKey, nil
}

func (seal sealer) credentials(publicKey []byte) ([]byte, error) {
	var output domain.SealRunCredentialsActivityOutput
	if err := workflow.ExecuteActivity(seal.ctx, domain.SealRunCredentialsActivityName, domain.SealRunCredentialsActivityInput{
		TenantID:        seal.input.TenantID,
		StackTemplateID: seal.input.StackTemplateID,
		PublicKey:       publicKey,
	}).Get(seal.ctx, &output); err != nil {
		return nil, err
	}
	return output.SealedEnvironment, nil
}
```

- [ ] **Step 6: Build, vet and run the package**

Run: `gofmt -l internal/workflows && go build ./... && go vet ./internal/workflows/ && go test ./internal/workflows/ -count=1`
Expected:
- `gofmt -l` prints nothing;
- the build and vet are clean;
- every test passes, including all the existing ones, unedited, and the two from Step 1.

If an existing test fails, the restructure changed behaviour. Find where the activity order, queue, timeout or payload differs from the original `template_run.go` and fix the new code. Do not edit the test.

Then confirm the boundaries hold. Every command below lists the files that match, so its expected output is exact:

```bash
grep -lE 'RecordTemplateRunStatusActivityName' internal/workflows/*.go | grep -v _test   # expected: only template_run.go
grep -lE 'RecordTemplateRunStepActivityName|RecordTemplateRunEventActivityName|RecordTemplateRunLogActivityName' internal/workflows/*.go | grep -v _test   # expected: only job.go
grep -nE 'recorder|recordStatus' internal/workflows/session.go   # expected: no output
```

- [ ] **Step 7: Commit**

```bash
git add internal/workflows/template_run.go internal/workflows/job.go internal/workflows/session.go internal/workflows/template_run_workflow_test.go
git commit -m "refactor: a run writes status, its jobs write steps, its sessions write nothing

Co-Authored-By: <model that wrote this> <noreply@anthropic.com>"
```
