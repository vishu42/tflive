package workflows

import (
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

func TemplateSyncWorkflow(ctx workflow.Context, input domain.TemplateSyncWorkflowInput) error {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Minute,
		RetryPolicy:         syncRetryPolicy,
	})

	run := templateSyncWorkflow{
		ctx:   ctx,
		input: input,
	}

	if err := run.recordStatus(domain.TemplateRegistrationStatusActivityInput{
		Status: domain.TemplateRegistrationRunning,
	}); err != nil {
		return err
	}

	output, err := run.syncTemplate()
	if err != nil {
		if recordErr := run.recordStatus(domain.TemplateRegistrationStatusActivityInput{
			Status:       domain.TemplateRegistrationFailed,
			ErrorSummary: err.Error(),
		}); recordErr != nil {
			return recordErr
		}
		return err
	}

	status := output.Status
	if status == "" {
		status = domain.TemplateRegistrationCompleted
	}
	return run.recordStatus(domain.TemplateRegistrationStatusActivityInput{
		Status:             status,
		TemplateRevisionID: output.TemplateRevisionID,
		ResolvedCommitSHA:  output.ResolvedCommitSHA,
		ErrorSummary:       output.ErrorSummary,
	})
}

type templateSyncWorkflow struct {
	ctx   workflow.Context
	input domain.TemplateSyncWorkflowInput
}

func (run *templateSyncWorkflow) syncTemplate() (domain.TemplateSyncActivityOutput, error) {
	input := domain.TemplateSyncActivityInput{
		RegistrationID: run.input.RegistrationID,
		TenantID:       run.input.TenantID,
		RepoOwner:      run.input.RepoOwner,
		RepoName:       run.input.RepoName,
		SourceRef:      run.input.SourceRef,
		RootPath:       run.input.RootPath,
	}
	var output domain.TemplateSyncActivityOutput
	err := workflow.ExecuteActivity(
		run.ctx,
		domain.SyncTemplateActivityName,
		input,
	).Get(run.ctx, &output)
	return output, err
}

func (run *templateSyncWorkflow) recordStatus(input domain.TemplateRegistrationStatusActivityInput) error {
	input.RegistrationID = run.input.RegistrationID
	input.TenantID = run.input.TenantID
	return workflow.ExecuteActivity(
		run.ctx,
		domain.RecordTemplateRegistrationStatusActivityName,
		input,
	).Get(run.ctx, nil)
}
