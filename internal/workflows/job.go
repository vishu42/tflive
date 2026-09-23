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

// step records step, then does its work. It is the one path phase code takes
// to the session, so executor work runs inside a named step, and no step is
// recorded after its work has started. Phase code never uses j.session
// directly.
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
