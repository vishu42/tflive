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

// StartTemplateRun dispatches one TemplatePlanWorkflow execution to Temporal.
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
		domain.TemplatePlanWorkflowName,
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

// StartTemplateApply starts the workflow that applies an approved run's saved
// plan. Its ID extends the run's, so a redelivered start intent finds the
// workflow already running instead of starting a second apply.
func (dispatcher *Dispatcher) StartTemplateApply(ctx context.Context, input domain.TemplateRunWorkflowInput) error {
	if input.TerraformTimeout <= 0 {
		input.TerraformTimeout = dispatcher.options.TerraformTimeout
	}
	_, err := dispatcher.client.ExecuteWorkflow(
		ctx,
		client.StartWorkflowOptions{
			ID:                       templateApplyWorkflowID(input.TenantID, input.RunID),
			TaskQueue:                domain.ControlTaskQueue,
			WorkflowIDReusePolicy:    enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
			WorkflowIDConflictPolicy: enumspb.WORKFLOW_ID_CONFLICT_POLICY_USE_EXISTING,
		},
		domain.TemplateApplyWorkflowName,
		input)
	if err != nil {
		return fmt.Errorf("start template apply workflow: %w", err)
	}

	return nil
}

func templateRunWorkflowID(tenantID domain.TenantID, runID domain.TemplateRunID) string {
	return fmt.Sprintf("template-run/%s/%s", tenantID, runID)
}

func templateApplyWorkflowID(tenantID domain.TenantID, runID domain.TemplateRunID) string {
	return templateRunWorkflowID(tenantID, runID) + "/apply"
}

func templateSyncWorkflowID(tenantID domain.TenantID, registrationID domain.TemplateRegistrationID) string {
	return fmt.Sprintf("template-sync/%s/%s", tenantID, registrationID)
}
