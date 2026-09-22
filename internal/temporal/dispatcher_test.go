package temporal

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/vishu42/tflive/internal/app"
	"github.com/vishu42/tflive/internal/domain"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
)

const (
	requesterSubject = domain.UserID("6fdb4b4c-2a8f-4cf7-945f-38f67f6a0e91")
	approverSubject  = domain.UserID("cb4afba6-d18d-496f-80ce-8a50b94f09be")
)

var _ app.WorkflowDispatcher = (*Dispatcher)(nil)

func TestStartTemplateRunExecutesWorkflow(t *testing.T) {
	t.Parallel()

	workflowClient := &recordingWorkflowClient{}
	dispatcher := newDispatcher(workflowClient)
	input := domain.TemplateRunWorkflowInput{
		RunID:           domain.TemplateRunID("run_123"),
		TenantID:        domain.TenantID("tenant_123"),
		StackTemplateID: domain.StackTemplateID("stack_template_123"),
		Operation:       domain.OperationPlan,
		SelectedRef:     "main",
		WorkspaceName:   "mtp_acme_prod_vpc_a13f9c",
	}

	if err := dispatcher.StartTemplateRun(context.Background(), input); err != nil {
		t.Fatalf("StartTemplateRun returned error: %v", err)
	}

	if workflowClient.executeOptions.ID != "template-run/tenant_123/run_123" {
		t.Fatalf("workflow ID = %q", workflowClient.executeOptions.ID)
	}
	if workflowClient.executeOptions.TaskQueue != domain.ControlTaskQueue {
		t.Fatalf("task queue = %q", workflowClient.executeOptions.TaskQueue)
	}
	if workflowClient.executeOptions.WorkflowIDReusePolicy != enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE {
		t.Fatalf("workflow ID reuse policy = %v, want reject duplicate", workflowClient.executeOptions.WorkflowIDReusePolicy)
	}
	if workflowClient.executeOptions.WorkflowIDConflictPolicy != enumspb.WORKFLOW_ID_CONFLICT_POLICY_USE_EXISTING {
		t.Fatalf("workflow ID conflict policy = %v, want use existing", workflowClient.executeOptions.WorkflowIDConflictPolicy)
	}
	if workflowClient.executeWorkflow != domain.TemplatePlanWorkflowName {
		t.Fatalf("workflow name = %#v", workflowClient.executeWorkflow)
	}
	if len(workflowClient.executeArgs) != 1 {
		t.Fatalf("workflow arg count = %d, want 1", len(workflowClient.executeArgs))
	}
	if !reflect.DeepEqual(workflowClient.executeArgs[0], input) {
		t.Fatalf("workflow input = %#v, want %#v", workflowClient.executeArgs[0], input)
	}
}

// TestStartTemplateRunStampsTheConfiguredTerraformTimeout pins where the
// Terraform ceiling enters a run. Starting is the last moment a queued run can
// pick up the deployment's current setting; after that the value travels in
// the workflow input, where the run keeps it and an operator can read it back
// out of history.
func TestStartTemplateRunStampsTheConfiguredTerraformTimeout(t *testing.T) {
	t.Parallel()

	workflowClient := &recordingWorkflowClient{}
	dispatcher := newDispatcher(workflowClient, DispatcherOptions{TerraformTimeout: 90 * time.Minute})

	if err := dispatcher.StartTemplateRun(context.Background(), domain.TemplateRunWorkflowInput{
		RunID:    domain.TemplateRunID("run_123"),
		TenantID: domain.TenantID("tenant_123"),
	}); err != nil {
		t.Fatalf("StartTemplateRun returned error: %v", err)
	}

	started, ok := workflowClient.executeArgs[0].(domain.TemplateRunWorkflowInput)
	if !ok {
		t.Fatalf("workflow input = %#v, want a TemplateRunWorkflowInput", workflowClient.executeArgs[0])
	}
	if started.TerraformTimeout != 90*time.Minute {
		t.Fatalf("TerraformTimeout = %v, want 90m", started.TerraformTimeout)
	}
}

// TestStartTemplateRunWithoutAConfiguredTimeoutLeavesItToTheWorkflow keeps an
// unconfigured dispatcher from stamping a zero that reads as a deliberate
// choice. The workflow, not the dispatcher, owns the default.
func TestStartTemplateRunWithoutAConfiguredTimeoutLeavesItToTheWorkflow(t *testing.T) {
	t.Parallel()

	workflowClient := &recordingWorkflowClient{}
	dispatcher := newDispatcher(workflowClient)

	if err := dispatcher.StartTemplateRun(context.Background(), domain.TemplateRunWorkflowInput{
		RunID:    domain.TemplateRunID("run_123"),
		TenantID: domain.TenantID("tenant_123"),
	}); err != nil {
		t.Fatalf("StartTemplateRun returned error: %v", err)
	}

	started := workflowClient.executeArgs[0].(domain.TemplateRunWorkflowInput)
	if started.TerraformTimeout != 0 {
		t.Fatalf("TerraformTimeout = %v, want zero so the workflow applies its default", started.TerraformTimeout)
	}
}

func TestStartTemplateSyncExecutesWorkflow(t *testing.T) {
	t.Parallel()

	workflowClient := &recordingWorkflowClient{}
	dispatcher := newDispatcher(workflowClient)
	input := domain.TemplateSyncWorkflowInput{
		RegistrationID: domain.TemplateRegistrationID("template_registration_123"),
		TenantID:       domain.TenantID("tenant_123"),
		RepoOwner:      "acme",
		RepoName:       "infra-templates",
		SourceRef:      "v0.0.1",
		RootPath:       "modules/vpc",
	}

	if err := dispatcher.StartTemplateSync(context.Background(), input); err != nil {
		t.Fatalf("StartTemplateSync returned error: %v", err)
	}

	if workflowClient.executeOptions.ID != "template-sync/tenant_123/template_registration_123" {
		t.Fatalf("workflow ID = %q", workflowClient.executeOptions.ID)
	}
	if workflowClient.executeOptions.TaskQueue != domain.ControlTaskQueue {
		t.Fatalf("task queue = %q", workflowClient.executeOptions.TaskQueue)
	}
	if workflowClient.executeWorkflow != domain.TemplateSyncWorkflowName {
		t.Fatalf("workflow name = %#v", workflowClient.executeWorkflow)
	}
	if len(workflowClient.executeArgs) != 1 {
		t.Fatalf("workflow arg count = %d, want 1", len(workflowClient.executeArgs))
	}
	if !reflect.DeepEqual(workflowClient.executeArgs[0], input) {
		t.Fatalf("workflow input = %#v, want %#v", workflowClient.executeArgs[0], input)
	}
}

// The apply workflow starts on the control queue under an ID derived from the
// run's, and a redelivered start finds it running rather than starting twice.
func TestStartTemplateApplyStartsTheApplyWorkflow(t *testing.T) {
	t.Parallel()

	workflowClient := &recordingWorkflowClient{}
	dispatcher := newDispatcher(workflowClient, DispatcherOptions{TerraformTimeout: 20 * time.Minute})
	input := domain.TemplateRunWorkflowInput{
		RunID:     domain.TemplateRunID("run_123"),
		TenantID:  domain.TenantID("tenant_123"),
		Operation: domain.OperationPlan,
	}

	if err := dispatcher.StartTemplateApply(context.Background(), input); err != nil {
		t.Fatalf("StartTemplateApply returned error: %v", err)
	}

	if workflowClient.executeOptions.ID != "template-run/tenant_123/run_123/apply" {
		t.Fatalf("workflow ID = %q", workflowClient.executeOptions.ID)
	}
	if workflowClient.executeOptions.TaskQueue != domain.ControlTaskQueue {
		t.Fatalf("task queue = %q", workflowClient.executeOptions.TaskQueue)
	}
	if workflowClient.executeOptions.WorkflowIDConflictPolicy != enumspb.WORKFLOW_ID_CONFLICT_POLICY_USE_EXISTING {
		t.Fatalf("conflict policy = %v, want use existing", workflowClient.executeOptions.WorkflowIDConflictPolicy)
	}
	if workflowClient.executeWorkflow != domain.TemplateApplyWorkflowName {
		t.Fatalf("workflow = %v", workflowClient.executeWorkflow)
	}
	started := workflowClient.executeArgs[0].(domain.TemplateRunWorkflowInput)
	if started.TerraformTimeout != 20*time.Minute {
		t.Fatalf("terraform timeout = %v, want the deployment's", started.TerraformTimeout)
	}
}

// A run's plan workflow has closed by the time its apply workflow runs, so a
// cancel that finds no plan workflow goes on to the apply workflow.
func TestCancelTemplateRunFallsBackToTheApplyWorkflow(t *testing.T) {
	t.Parallel()

	workflowClient := &recordingWorkflowClient{signalErrs: map[string]error{
		"template-run/tenant_123/run_123": serviceerror.NewNotFound("workflow execution already completed"),
	}}
	dispatcher := newDispatcher(workflowClient)

	err := dispatcher.CancelTemplateRun(context.Background(), "tenant_123", "run_123", domain.CancelSignal{RequestedBy: requesterSubject})
	if err != nil {
		t.Fatalf("CancelTemplateRun returned error: %v", err)
	}
	if workflowClient.signalWorkflowID != "template-run/tenant_123/run_123/apply" {
		t.Fatalf("last signaled workflow = %q, want the apply workflow", workflowClient.signalWorkflowID)
	}
}

// With neither workflow running the NotFound comes back, which is what tells
// the cancellation handler to reconcile the run itself.
func TestCancelTemplateRunReportsNotFoundWhenNoWorkflowIsRunning(t *testing.T) {
	t.Parallel()

	notFound := serviceerror.NewNotFound("workflow not found")
	dispatcher := newDispatcher(&recordingWorkflowClient{signalErr: notFound})

	err := dispatcher.CancelTemplateRun(context.Background(), "tenant_123", "run_123", domain.CancelSignal{RequestedBy: requesterSubject})
	var target *serviceerror.NotFound
	if !errors.As(err, &target) {
		t.Fatalf("error = %v, want NotFound", err)
	}
}

func TestCancelTemplateRunSignalsWorkflow(t *testing.T) {
	t.Parallel()

	workflowClient := &recordingWorkflowClient{}
	dispatcher := newDispatcher(workflowClient)
	signal := domain.CancelSignal{
		RequestedBy: requesterSubject,
		Reason:      "superseded by a newer run",
	}

	err := dispatcher.CancelTemplateRun(
		context.Background(),
		domain.TenantID("tenant_123"),
		domain.TemplateRunID("run_123"),
		signal,
	)
	if err != nil {
		t.Fatalf("CancelTemplateRun returned error: %v", err)
	}

	if workflowClient.signalWorkflowID != "template-run/tenant_123/run_123" {
		t.Fatalf("signal workflow ID = %q", workflowClient.signalWorkflowID)
	}
	if workflowClient.signalRunID != "" {
		t.Fatalf("signal run ID = %q, want empty", workflowClient.signalRunID)
	}
	if workflowClient.signalName != domain.CancelSignalName {
		t.Fatalf("signal name = %q", workflowClient.signalName)
	}
	if !reflect.DeepEqual(workflowClient.signalArg, signal) {
		t.Fatalf("signal arg = %#v, want %#v", workflowClient.signalArg, signal)
	}
}

func TestStartTemplateRunWrapsClientError(t *testing.T) {
	t.Parallel()

	clientErr := errors.New("temporal unavailable")
	dispatcher := newDispatcher(&recordingWorkflowClient{executeErr: clientErr})

	err := dispatcher.StartTemplateRun(context.Background(), domain.TemplateRunWorkflowInput{
		RunID:    domain.TemplateRunID("run_123"),
		TenantID: domain.TenantID("tenant_123"),
	})
	if !errors.Is(err, clientErr) {
		t.Fatalf("error = %v, want wrapped client error", err)
	}
	if !strings.Contains(err.Error(), "start template run workflow") {
		t.Fatalf("error = %q, want start context", err.Error())
	}
}

func TestStartTemplateSyncWrapsClientError(t *testing.T) {
	t.Parallel()

	clientErr := errors.New("temporal unavailable")
	dispatcher := newDispatcher(&recordingWorkflowClient{executeErr: clientErr})

	err := dispatcher.StartTemplateSync(context.Background(), domain.TemplateSyncWorkflowInput{
		RegistrationID: domain.TemplateRegistrationID("template_registration_123"),
		TenantID:       domain.TenantID("tenant_123"),
	})
	if !errors.Is(err, clientErr) {
		t.Fatalf("error = %v, want wrapped client error", err)
	}
	if !strings.Contains(err.Error(), "start template sync workflow") {
		t.Fatalf("error = %q, want start context", err.Error())
	}
}

func TestCancelTemplateRunWrapsClientError(t *testing.T) {
	t.Parallel()

	clientErr := errors.New("temporal unavailable")
	dispatcher := newDispatcher(&recordingWorkflowClient{signalErr: clientErr})

	err := dispatcher.CancelTemplateRun(
		context.Background(),
		domain.TenantID("tenant_123"),
		domain.TemplateRunID("run_123"),
		domain.CancelSignal{RequestedBy: requesterSubject},
	)
	if !errors.Is(err, clientErr) {
		t.Fatalf("error = %v, want wrapped client error", err)
	}
	if !strings.Contains(err.Error(), "signal template run cancellation") {
		t.Fatalf("error = %q, want cancellation context", err.Error())
	}
}

func TestTemplatePlanWorkflowID(t *testing.T) {
	t.Parallel()

	got := templateRunWorkflowID(domain.TenantID("tenant_123"), domain.TemplateRunID("run_123"))
	if got != "template-run/tenant_123/run_123" {
		t.Fatalf("workflow ID = %q", got)
	}
}

func TestTemplateSyncWorkflowID(t *testing.T) {
	t.Parallel()

	got := templateSyncWorkflowID(domain.TenantID("tenant_123"), domain.TemplateRegistrationID("template_registration_123"))
	if got != "template-sync/tenant_123/template_registration_123" {
		t.Fatalf("workflow ID = %q", got)
	}
}

type recordingWorkflowClient struct {
	executeOptions   client.StartWorkflowOptions
	executeWorkflow  interface{}
	executeArgs      []interface{}
	executeErr       error
	signalWorkflowID string
	signalRunID      string
	signalName       string
	signalArg        interface{}
	signalErr        error
	// signalErrs fails signals to particular workflow IDs; signalErr applies
	// to every other.
	signalErrs map[string]error
}

func (workflowClient *recordingWorkflowClient) ExecuteWorkflow(
	_ context.Context,
	options client.StartWorkflowOptions,
	workflow interface{},
	args ...interface{},
) (client.WorkflowRun, error) {
	workflowClient.executeOptions = options
	workflowClient.executeWorkflow = workflow
	workflowClient.executeArgs = args
	return nil, workflowClient.executeErr
}

func (workflowClient *recordingWorkflowClient) SignalWorkflow(
	_ context.Context,
	workflowID string,
	runID string,
	signalName string,
	arg interface{},
) error {
	workflowClient.signalWorkflowID = workflowID
	workflowClient.signalRunID = runID
	workflowClient.signalName = signalName
	workflowClient.signalArg = arg
	if err, ok := workflowClient.signalErrs[workflowID]; ok {
		return err
	}
	return workflowClient.signalErr
}
