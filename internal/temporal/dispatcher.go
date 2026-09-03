package temporal

import (
	"context"
	"fmt"

	"github.com/vishu42/tflive/internal/domain"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
)

type workflowClient interface {
	ExecuteWorkflow(context.Context, client.StartWorkflowOptions, interface{}, ...interface{}) (client.WorkflowRun, error)
	SignalWorkflow(context.Context, string, string, string, interface{}) error
}

type Dispatcher struct {
	client    workflowClient
	taskQueue string
}

func NewDispatcher(temporalClient client.Client, taskQueue string) *Dispatcher {
	return newDispatcher(temporalClient, taskQueue)
}

func newDispatcher(temporalClient workflowClient, taskQueue string) *Dispatcher {
	return &Dispatcher{
		client:    temporalClient,
		taskQueue: taskQueue,
	}
}

// StartTemplateRun dispatches one TemplateRunWorkflow execution to Temporal.
// The workflow ID is derived from tenant ID and run ID so repeated callers target
// the same logical run, and the configured task queue determines which workers
// can pick up the workflow and its activities.
func (dispatcher *Dispatcher) StartTemplateRun(ctx context.Context, input domain.TemplateRunWorkflowInput) error {
	_, err := dispatcher.client.ExecuteWorkflow(
		ctx,
		client.StartWorkflowOptions{
			ID:                       templateRunWorkflowID(input.TenantID, input.RunID),
			TaskQueue:                dispatcher.taskQueue,
			WorkflowIDReusePolicy:    enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
			WorkflowIDConflictPolicy: enumspb.WORKFLOW_ID_CONFLICT_POLICY_USE_EXISTING,
		},
		domain.TemplateRunWorkflowName,
		input)
	if err != nil {
		return fmt.Errorf("start template run workflow: %w", err)
	}

	return nil
}

// StartTemplateSync dispatches one TemplateSyncWorkflow execution to Temporal.
func (dispatcher *Dispatcher) StartTemplateSync(ctx context.Context, input domain.TemplateSyncWorkflowInput) error {
	_, err := dispatcher.client.ExecuteWorkflow(
		ctx,
		client.StartWorkflowOptions{
			ID:        templateSyncWorkflowID(input.TenantID, input.RegistrationID),
			TaskQueue: dispatcher.taskQueue,
		},
		domain.TemplateSyncWorkflowName,
		input)
	if err != nil {
		return fmt.Errorf("start template sync workflow: %w", err)
	}

	return nil
}

func (dispatcher *Dispatcher) ApproveTemplateRun(
	ctx context.Context,
	tenantID domain.TenantID,
	runID domain.TemplateRunID,
	signal domain.ApprovalSignal,
) error {
	if err := dispatcher.client.SignalWorkflow(
		ctx,
		templateRunWorkflowID(tenantID, runID),
		"",
		domain.ApprovalSignalName,
		signal,
	); err != nil {
		return fmt.Errorf("signal template run approval: %w", err)
	}

	return nil
}

func (dispatcher *Dispatcher) CancelTemplateRun(
	ctx context.Context,
	tenantID domain.TenantID,
	runID domain.TemplateRunID,
	signal domain.CancelSignal,
) error {
	if err := dispatcher.client.SignalWorkflow(
		ctx,
		templateRunWorkflowID(tenantID, runID),
		"",
		domain.CancelSignalName,
		signal,
	); err != nil {
		return fmt.Errorf("signal template run cancellation: %w", err)
	}

	return nil
}

func templateRunWorkflowID(tenantID domain.TenantID, runID domain.TemplateRunID) string {
	return fmt.Sprintf("template-run/%s/%s", tenantID, runID)
}

func templateSyncWorkflowID(tenantID domain.TenantID, registrationID domain.TemplateRegistrationID) string {
	return fmt.Sprintf("template-sync/%s/%s", tenantID, registrationID)
}
