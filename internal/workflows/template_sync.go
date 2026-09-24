package workflows

import (
	"fmt"
	"time"

	"github.com/vishu42/tflive/internal/domain"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// syncRetryPolicy is the retry policy for template-sync activities. Git clones
// and HCL parsing can fail due to transient network errors or GitHub rate
// limits, but invalid configuration or missing repositories are permanent
// failures that should not be retried.
var syncRetryPolicy = &temporal.RetryPolicy{
	InitialInterval:    30 * time.Second,
	BackoffCoefficient: 2.0,
	MaximumInterval:    5 * time.Minute,
	MaximumAttempts:    4,
	NonRetryableErrorTypes: []string{
		"InvalidTemplate",
		"RepositoryNotFound",
	},
}

// TemplateSyncWorkflow syncs a template registration: it marks it running,
// syncs the template as the sync's one step, and records the outcome. Any
// error marks the registration failed before the workflow returns it.
func TemplateSyncWorkflow(ctx workflow.Context, input domain.TemplateSyncWorkflowInput) (err error) {
	r := newRegistration(ctx, input)
	defer func() { err = r.fail(err) }()

	if err := r.setStatus(domain.TemplateRegistrationStatusActivityInput{
		Status: domain.TemplateRegistrationRunning,
	}); err != nil {
		return err
	}
	synced, err := r.sync()
	if err != nil {
		return err
	}
	return r.settle(synced)
}

// registration is a template registration, as its sync carries it out. It
// holds the control-queue context and the sync's input, and it is the sync's
// Recorder.
//
// A sync has no executor session: it clones and parses a repository but runs
// none of its code, so all of its work, the step included, schedules on the
// control plane, on r.ctx.
type registration struct {
	ctx   workflow.Context
	input domain.TemplateSyncWorkflowInput
}

// newRegistration sets the options for every activity the sync schedules.
func newRegistration(ctx workflow.Context, input domain.TemplateSyncWorkflowInput) *registration {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		TaskQueue:           domain.ControlTaskQueue,
		StartToCloseTimeout: 5 * time.Minute,
		RetryPolicy:         syncRetryPolicy,
	})
	return &registration{ctx: ctx, input: input}
}

// sync clones the registration's repository at its ref and parses the
// template it holds, as the sync's one step.
func (r *registration) sync() (domain.TemplateSyncActivityOutput, error) {
	var synced domain.TemplateSyncActivityOutput
	err := ExecuteStep(r.ctx, r, domain.TemplateRegistrationStepSyncing, domain.SyncTemplateActivityName, domain.TemplateSyncActivityInput{
		RegistrationID: r.input.RegistrationID,
		TenantID:       r.input.TenantID,
		RepoOwner:      r.input.RepoOwner,
		RepoName:       r.input.RepoName,
		SourceRef:      r.input.SourceRef,
		RootPath:       r.input.RootPath,
	}).Get(r.ctx, &synced)
	return synced, err
}

// Record records the step the sync is starting.
func (r *registration) Record(step domain.TemplateRegistrationStep) error {
	return workflow.ExecuteActivity(
		r.ctx,
		domain.RecordTemplateRegistrationStepActivityName,
		domain.TemplateRegistrationStepActivityInput{
			RegistrationID: r.input.RegistrationID,
			TenantID:       r.input.TenantID,
			Step:           step,
		},
	).Get(r.ctx, nil)
}

// fail marks the registration failed with err, and does nothing without one.
// If recording the failure also fails, both errors are surfaced: the original
// wrapped with %w to stay matchable by callers, the persistence error appended
// as context.
func (r *registration) fail(err error) error {
	if err == nil {
		return nil
	}
	if recordErr := r.setStatus(domain.TemplateRegistrationStatusActivityInput{
		Status:       domain.TemplateRegistrationFailed,
		ErrorSummary: err.Error(),
	}); recordErr != nil {
		return fmt.Errorf("%w (also failed to persist failure status: %v)", err, recordErr)
	}
	return err
}

// settle records what the sync found: the revision it produced, or why the
// template is invalid. A sync that reports no status of its own completed.
func (r *registration) settle(synced domain.TemplateSyncActivityOutput) error {
	status := synced.Status
	if status == "" {
		status = domain.TemplateRegistrationCompleted
	}
	return r.setStatus(domain.TemplateRegistrationStatusActivityInput{
		Status:             status,
		TemplateRevisionID: synced.TemplateRevisionID,
		ResolvedCommitSHA:  synced.ResolvedCommitSHA,
		ErrorSummary:       synced.ErrorSummary,
	})
}

// setStatus moves the registration along its lifecycle.
func (r *registration) setStatus(status domain.TemplateRegistrationStatusActivityInput) error {
	status.RegistrationID = r.input.RegistrationID
	status.TenantID = r.input.TenantID
	return workflow.ExecuteActivity(
		r.ctx,
		domain.RecordTemplateRegistrationStatusActivityName,
		status,
	).Get(r.ctx, nil)
}
