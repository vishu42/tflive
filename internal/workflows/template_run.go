package workflows

import (
	"errors"
	"fmt"
	"time"

	"github.com/vishu42/megagega/internal/traits"
	"go.temporal.io/sdk/workflow"
)

var errTemplateRunCanceled = errors.New("template run canceled")

func TemplateRunWorkflow(ctx workflow.Context, input traits.TemplateRunWorkflowInput) error {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: time.Minute,
	})

	run := templateRunWorkflow{
		ctx:   ctx,
		input: input,
	}

	if err := run.execute(); err != nil {
		if errors.Is(err, errTemplateRunCanceled) {
			return nil
		}
		return err
	}
	return nil
}

type templateRunWorkflow struct {
	ctx           workflow.Context
	input         traits.TemplateRunWorkflowInput
	workspacePath string
	terraformPath string
}

func (run *templateRunWorkflow) execute() error {
	if err := run.prepareWorkspace(); err != nil {
		return err
	}

	switch run.input.Operation {
	case traits.OperationPlan:
		return run.planOnly()
	case traits.OperationApply:
		return run.apply()
	case traits.OperationDestroy:
		return run.destroy()
	default:
		if err := run.recordStatus(traits.TemplateRunFailed); err != nil {
			return err
		}
		if releaseErr := run.recordStatus(traits.TemplateRunLockReleased); releaseErr != nil {
			return releaseErr
		}
		return fmt.Errorf("unsupported template run operation %q", run.input.Operation)
	}
}

func (run *templateRunWorkflow) prepareWorkspace() error {
	if err := run.recordStatus(traits.TemplateRunLocked); err != nil {
		return err
	}
	if err := run.prepareLocalWorkspace(); err != nil {
		return err
	}
	if err := run.recordStatus(traits.TemplateRunWorkspacePrepared); err != nil {
		return err
	}
	if err := run.fetchSource(); err != nil {
		return err
	}
	if err := run.recordStatus(traits.TemplateRunSourceFetched); err != nil {
		return err
	}
	if err := run.runTerraform(traits.TerraformCommandInit); err != nil {
		return err
	}
	if err := run.recordStatus(traits.TemplateRunInit); err != nil {
		return err
	}
	if err := run.runTerraform(traits.TerraformCommandSelectWorkspace); err != nil {
		return err
	}
	return run.recordStatus(traits.TemplateRunWorkspaceSelected)
}

// prepareLocalWorkspace schedules the worker-side activity that creates the
// per-run filesystem workspace and returns its absolute path. Workflows cannot
// create directories directly because Temporal workflows must stay deterministic,
// so the side effect lives in PrepareWorkspace. The returned path is stored on
// the workflow helper and reused by later RunTerraform activities as their
// working directory.
func (run *templateRunWorkflow) prepareLocalWorkspace() error {
	input := traits.PrepareWorkspaceActivityInput{
		RunID:    run.input.RunID,
		TenantID: run.input.TenantID,
	}
	var output traits.PrepareWorkspaceActivityOutput
	if err := workflow.ExecuteActivity(
		run.ctx,
		traits.PrepareWorkspaceActivityName,
		input,
	).Get(run.ctx, &output); err != nil {
		return err
	}
	run.workspacePath = output.WorkspacePath
	return nil
}

func (run *templateRunWorkflow) fetchSource() error {
	input := traits.FetchSourceActivityInput{
		RunID:         run.input.RunID,
		TenantID:      run.input.TenantID,
		WorkspacePath: run.workspacePath,
		RepoOwner:     run.input.RepoOwner,
		RepoName:      run.input.RepoName,
		SourceRef:     run.input.SelectedRef,
		RootPath:      run.input.RootPath,
	}
	var output traits.FetchSourceActivityOutput
	if err := workflow.ExecuteActivity(
		run.ctx,
		traits.FetchSourceActivityName,
		input,
	).Get(run.ctx, &output); err != nil {
		return err
	}
	run.terraformPath = output.TerraformPath
	return nil
}

func (run *templateRunWorkflow) planOnly() error {
	if err := run.runTerraform(traits.TerraformCommandPlan); err != nil {
		return err
	}
	if err := run.recordStatus(traits.TemplateRunPlanned); err != nil {
		return err
	}
	return run.complete()
}

func (run *templateRunWorkflow) apply() error {
	if err := run.runTerraform(traits.TerraformCommandPlan); err != nil {
		return err
	}
	if err := run.recordStatuses(traits.TemplateRunPlanned, traits.TemplateRunWaitingApproval); err != nil {
		return err
	}

	approved, err := run.waitForApproval()
	if err != nil {
		return err
	}
	if !approved {
		return run.cancel()
	}

	if err := run.recordStatuses(traits.TemplateRunApproved, traits.TemplateRunApplyStarted); err != nil {
		return err
	}
	if err := run.runTerraform(traits.TerraformCommandApply); err != nil {
		return err
	}
	if err := run.recordStatus(traits.TemplateRunApplied); err != nil {
		return err
	}
	return run.complete()
}

func (run *templateRunWorkflow) destroy() error {
	if err := run.recordStatuses(traits.TemplateRunDestroyStarted, traits.TemplateRunDestroyed); err != nil {
		return err
	}
	return run.complete()
}

func (run *templateRunWorkflow) waitForApproval() (bool, error) {
	approvalCh := workflow.GetSignalChannel(run.ctx, traits.ApprovalSignalName)
	cancelCh := workflow.GetSignalChannel(run.ctx, traits.CancelSignalName)
	selector := workflow.NewSelector(run.ctx)
	approved := false

	selector.AddReceive(approvalCh, func(channel workflow.ReceiveChannel, _ bool) {
		var signal traits.ApprovalSignal
		channel.Receive(run.ctx, &signal)
		approved = true
	})
	selector.AddReceive(cancelCh, func(channel workflow.ReceiveChannel, _ bool) {
		var signal traits.CancelSignal
		channel.Receive(run.ctx, &signal)
		approved = false
	})

	selector.Select(run.ctx)
	return approved, nil
}

func (run *templateRunWorkflow) cancel() error {
	return run.recordStatuses(
		traits.TemplateRunCancelRequested,
		traits.TemplateRunCanceling,
		traits.TemplateRunLockReleased,
		traits.TemplateRunCanceled,
	)
}

func (run *templateRunWorkflow) complete() error {
	return run.recordStatuses(traits.TemplateRunLockReleased, traits.TemplateRunCompleted)
}

func (run *templateRunWorkflow) runTerraform(command traits.TerraformCommandType) error {
	input := traits.RunTerraformActivityInput{
		RunID:         run.input.RunID,
		TenantID:      run.input.TenantID,
		WorkspacePath: run.workspacePath,
		TerraformPath: run.terraformPath,
		WorkspaceName: run.input.WorkspaceName,
		Command:       command,
		ConfigJSON:    run.input.ConfigJSON,
	}

	activityCtx, cancelActivity := workflow.WithCancel(run.ctx)
	defer cancelActivity()

	future := workflow.ExecuteActivity(
		activityCtx,
		traits.RunTerraformActivityName,
		input,
	)
	cancelCh := workflow.GetSignalChannel(run.ctx, traits.CancelSignalName)
	selector := workflow.NewSelector(run.ctx)

	var activityErr error
	var canceled bool
	selector.AddFuture(future, func(f workflow.Future) {
		activityErr = f.Get(run.ctx, nil)
	})
	selector.AddReceive(cancelCh, func(channel workflow.ReceiveChannel, _ bool) {
		var signal traits.CancelSignal
		channel.Receive(run.ctx, &signal)
		cancelActivity()
		canceled = true
	})
	selector.Select(run.ctx)

	if canceled {
		if err := run.cancel(); err != nil {
			return err
		}
		return errTemplateRunCanceled
	}
	return activityErr
}

func (run *templateRunWorkflow) recordStatuses(statuses ...traits.TemplateRunStatus) error {
	for _, status := range statuses {
		if err := run.recordStatus(status); err != nil {
			return err
		}
	}
	return nil
}

func (run *templateRunWorkflow) recordStatus(status traits.TemplateRunStatus) error {
	input := traits.TemplateRunStatusActivityInput{
		RunID:           run.input.RunID,
		TenantID:        run.input.TenantID,
		StackTemplateID: run.input.StackTemplateID,
		Operation:       run.input.Operation,
		Status:          status,
	}
	return workflow.ExecuteActivity(
		run.ctx,
		traits.RecordTemplateRunStatusActivityName,
		input,
	).Get(run.ctx, nil)
}
