package workflows

import (
	"time"

	"github.com/vishu42/megagega/internal/traits"
	"go.temporal.io/sdk/workflow"
)

func TemplateSyncWorkflow(ctx workflow.Context, input traits.TemplateSyncWorkflowInput) error {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Minute,
	})

	run := templateSyncWorkflow{
		ctx:   ctx,
		input: input,
	}

	if err := run.recordStatus(traits.TemplateRegistrationStatusActivityInput{
		Status: traits.TemplateRegistrationRunning,
	}); err != nil {
		return err
	}

	output, err := run.syncTemplate()
	if err != nil {
		if recordErr := run.recordStatus(traits.TemplateRegistrationStatusActivityInput{
			Status:       traits.TemplateRegistrationFailed,
			ErrorSummary: err.Error(),
		}); recordErr != nil {
			return recordErr
		}
		return err
	}

	status := output.Status
	if status == "" {
		status = traits.TemplateRegistrationCompleted
	}
	return run.recordStatus(traits.TemplateRegistrationStatusActivityInput{
		Status:            status,
		TemplateID:        output.TemplateID,
		ResolvedCommitSHA: output.ResolvedCommitSHA,
		ErrorSummary:      output.ErrorSummary,
	})
}

type templateSyncWorkflow struct {
	ctx   workflow.Context
	input traits.TemplateSyncWorkflowInput
}

func (run *templateSyncWorkflow) syncTemplate() (traits.TemplateSyncActivityOutput, error) {
	input := traits.TemplateSyncActivityInput{
		RegistrationID: run.input.RegistrationID,
		TenantID:       run.input.TenantID,
		RepoOwner:      run.input.RepoOwner,
		RepoName:       run.input.RepoName,
		SourceRef:      run.input.SourceRef,
		RootPath:       run.input.RootPath,
	}
	var output traits.TemplateSyncActivityOutput
	err := workflow.ExecuteActivity(
		run.ctx,
		traits.SyncTemplateActivityName,
		input,
	).Get(run.ctx, &output)
	return output, err
}

func (run *templateSyncWorkflow) recordStatus(input traits.TemplateRegistrationStatusActivityInput) error {
	input.RegistrationID = run.input.RegistrationID
	input.TenantID = run.input.TenantID
	return workflow.ExecuteActivity(
		run.ctx,
		traits.RecordTemplateRegistrationStatusActivityName,
		input,
	).Get(run.ctx, nil)
}
