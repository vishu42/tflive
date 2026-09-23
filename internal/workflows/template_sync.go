package workflows

import (
	"fmt"
	"time"

	"github.com/vishu42/tflive/internal/domain"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// A template sync is carried out at two levels, drawn the way a template run's
// are (see template_run.go):
//
//   - registration is the workflow. It alone moves the registration along its
//     lifecycle (status).
//   - syncTemplate is the work. It is handed a context and the sync's input,
//     never the registration, so it has no status write.
//
// A sync has no executor session and no steps: it clones and parses a
// repository but runs none of its code, so all of it stays on the control
// plane, as one activity.

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
// syncs the template, and records the outcome. A sync that fails marks the
// registration failed before the workflow returns its error.
func TemplateSyncWorkflow(ctx workflow.Context, input domain.TemplateSyncWorkflowInput) error {
	r := newRegistration(ctx, input)
	if err := r.start(); err != nil {
		return err
	}
	synced, err := syncTemplate(r.ctx, input)
	if err != nil {
		return r.fail(err)
	}
	return r.settle(synced)
}

// registration is a template sync's workflow: the one place its status
// changes. It holds the control-queue context.
type registration struct {
	ctx   workflow.Context
	input domain.TemplateSyncWorkflowInput
}

// newRegistration sets the options for every activity the sync schedules. A
// sync clones and parses a repository but runs none of its code, so all of it
// stays on the control plane.
func newRegistration(ctx workflow.Context, input domain.TemplateSyncWorkflowInput) *registration {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		TaskQueue:           domain.ControlTaskQueue,
		StartToCloseTimeout: 5 * time.Minute,
		RetryPolicy:         syncRetryPolicy,
	})
	return &registration{ctx: ctx, input: input}
}

// start marks the registration running.
func (r *registration) start() error {
	return r.recordStatus(domain.TemplateRegistrationStatusActivityInput{
		Status: domain.TemplateRegistrationRunning,
	})
}

// fail marks the registration failed with the sync's error. If recording the
// failure also fails, both errors are surfaced: the sync's wrapped with %w to
// stay matchable by callers, the persistence error appended as context.
func (r *registration) fail(err error) error {
	if recordErr := r.recordStatus(domain.TemplateRegistrationStatusActivityInput{
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
	return r.recordStatus(domain.TemplateRegistrationStatusActivityInput{
		Status:             status,
		TemplateRevisionID: synced.TemplateRevisionID,
		ResolvedCommitSHA:  synced.ResolvedCommitSHA,
		ErrorSummary:       synced.ErrorSummary,
	})
}

// recordStatus moves the registration along its lifecycle. Only registration
// has it.
func (r *registration) recordStatus(input domain.TemplateRegistrationStatusActivityInput) error {
	input.RegistrationID = r.input.RegistrationID
	input.TenantID = r.input.TenantID
	return workflow.ExecuteActivity(
		r.ctx,
		domain.RecordTemplateRegistrationStatusActivityName,
		input,
	).Get(r.ctx, nil)
}

// syncTemplate clones the registration's repository at its ref and parses the
// template it holds.
func syncTemplate(ctx workflow.Context, input domain.TemplateSyncWorkflowInput) (domain.TemplateSyncActivityOutput, error) {
	var output domain.TemplateSyncActivityOutput
	err := workflow.ExecuteActivity(ctx, domain.SyncTemplateActivityName, domain.TemplateSyncActivityInput{
		RegistrationID: input.RegistrationID,
		TenantID:       input.TenantID,
		RepoOwner:      input.RepoOwner,
		RepoName:       input.RepoName,
		SourceRef:      input.SourceRef,
		RootPath:       input.RootPath,
	}).Get(ctx, &output)
	return output, err
}
