package temporal

import (
	"context"
	"fmt"
	"time"

	"github.com/vishu42/tflive/internal/domain"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
)

type workflowClient interface {
	ExecuteWorkflow(context.Context, client.StartWorkflowOptions, interface{}, ...interface{}) (client.WorkflowRun, error)
	SignalWorkflow(context.Context, string, string, string, interface{}) error
}

type Dispatcher struct {
	client  workflowClient
	options DispatcherOptions
}

// DispatcherOptions carries the deployment-wide policy applied when a workflow
// is started, as opposed to what the caller asked to run.
type DispatcherOptions struct {
	// TerraformTimeout bounds each Terraform command the run issues. Zero
	// leaves the workflow's own default in place.
	TerraformTimeout time.Duration
}

func NewDispatcher(temporalClient client.Client, options DispatcherOptions) *Dispatcher {
	return newDispatcher(temporalClient, options)
}

func newDispatcher(temporalClient workflowClient, options ...DispatcherOptions) *Dispatcher {
	dispatcher := &Dispatcher{client: temporalClient}
	if len(options) > 0 {
		dispatcher.options = options[0]
	}
	return dispatcher
}

// StartTemplateRun dispatches one TemplateRunWorkflow execution to Temporal.
// The workflow ID is derived from tenant ID and run ID so repeated callers target
// the same logical run. Workflows run on the control queue; the workflow itself
// routes execution activities to the executor.
func (dispatcher *Dispatcher) StartTemplateRun(ctx context.Context, input domain.TemplateRunWorkflowInput) error {
	// The timeout is stamped here rather than carried from the request: it is
	// deployment configuration, not something a caller chose, and stamping it
	// at dispatch means a run that sat in the queue across a reconfiguration
	// starts with the timeout in force now. Once started, the run keeps this
	// value for its whole life, because it is in the workflow's input.
	if input.TerraformTimeout <= 0 {
		input.TerraformTimeout = dispatcher.options.TerraformTimeout
	}
	_, err := dispatcher.client.ExecuteWorkflow(
		ctx,
		client.StartWorkflowOptions{
			ID:                       templateRunWorkflowID(input.TenantID, input.RunID),
			TaskQueue:                domain.ControlTaskQueue,
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
			TaskQueue: domain.ControlTaskQueue,
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
