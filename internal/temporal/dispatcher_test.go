package temporal

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/vishu42/tflive/internal/app"
	"github.com/vishu42/tflive/internal/traits"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
)

var _ app.WorkflowDispatcher = (*Dispatcher)(nil)

func TestStartTemplateRunExecutesWorkflow(t *testing.T) {
	t.Parallel()

	workflowClient := &recordingWorkflowClient{}
	dispatcher := newDispatcher(workflowClient, "terraform-runs")
	input := traits.TemplateRunWorkflowInput{
		RunID:           traits.TemplateRunID("run_123"),
		TenantID:        traits.TenantID("tenant_123"),
		StackTemplateID: traits.StackTemplateID("stack_template_123"),
		Operation:       traits.OperationApply,
		SelectedRef:     "main",
		WorkspaceName:   "mtp_acme_prod_vpc_a13f9c",
	}

	if err := dispatcher.StartTemplateRun(context.Background(), input); err != nil {
		t.Fatalf("StartTemplateRun returned error: %v", err)
	}

	if workflowClient.executeOptions.ID != "template-run/tenant_123/run_123" {
		t.Fatalf("workflow ID = %q", workflowClient.executeOptions.ID)
	}
	if workflowClient.executeOptions.TaskQueue != "terraform-runs" {
		t.Fatalf("task queue = %q", workflowClient.executeOptions.TaskQueue)
	}
	if workflowClient.executeOptions.WorkflowIDReusePolicy != enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE {
		t.Fatalf("workflow ID reuse policy = %v, want reject duplicate", workflowClient.executeOptions.WorkflowIDReusePolicy)
	}
	if workflowClient.executeOptions.WorkflowIDConflictPolicy != enumspb.WORKFLOW_ID_CONFLICT_POLICY_USE_EXISTING {
		t.Fatalf("workflow ID conflict policy = %v, want use existing", workflowClient.executeOptions.WorkflowIDConflictPolicy)
	}
	if workflowClient.executeWorkflow != traits.TemplateRunWorkflowName {
		t.Fatalf("workflow name = %#v", workflowClient.executeWorkflow)
	}
	if len(workflowClient.executeArgs) != 1 {
		t.Fatalf("workflow arg count = %d, want 1", len(workflowClient.executeArgs))
	}
	if !reflect.DeepEqual(workflowClient.executeArgs[0], input) {
		t.Fatalf("workflow input = %#v, want %#v", workflowClient.executeArgs[0], input)
	}
}

func TestStartTemplateSyncExecutesWorkflow(t *testing.T) {
	t.Parallel()

	workflowClient := &recordingWorkflowClient{}
	dispatcher := newDispatcher(workflowClient, "terraform-runs")
	input := traits.TemplateSyncWorkflowInput{
		RegistrationID: traits.TemplateRegistrationID("template_registration_123"),
		TenantID:       traits.TenantID("tenant_123"),
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
	if workflowClient.executeOptions.TaskQueue != "terraform-runs" {
		t.Fatalf("task queue = %q", workflowClient.executeOptions.TaskQueue)
	}
	if workflowClient.executeWorkflow != traits.TemplateSyncWorkflowName {
		t.Fatalf("workflow name = %#v", workflowClient.executeWorkflow)
	}
	if len(workflowClient.executeArgs) != 1 {
		t.Fatalf("workflow arg count = %d, want 1", len(workflowClient.executeArgs))
	}
	if !reflect.DeepEqual(workflowClient.executeArgs[0], input) {
		t.Fatalf("workflow input = %#v, want %#v", workflowClient.executeArgs[0], input)
	}
}

func TestApproveTemplateRunSignalsWorkflow(t *testing.T) {
	t.Parallel()

	workflowClient := &recordingWorkflowClient{}
	dispatcher := newDispatcher(workflowClient, "terraform-runs")
	signal := traits.ApprovalSignal{ApprovedBy: traits.UserID("user_123")}

	err := dispatcher.ApproveTemplateRun(
		context.Background(),
		traits.TenantID("tenant_123"),
		traits.TemplateRunID("run_123"),
		signal,
	)
	if err != nil {
		t.Fatalf("ApproveTemplateRun returned error: %v", err)
	}

	if workflowClient.signalWorkflowID != "template-run/tenant_123/run_123" {
		t.Fatalf("signal workflow ID = %q", workflowClient.signalWorkflowID)
	}
	if workflowClient.signalRunID != "" {
		t.Fatalf("signal run ID = %q, want empty", workflowClient.signalRunID)
	}
	if workflowClient.signalName != traits.ApprovalSignalName {
		t.Fatalf("signal name = %q", workflowClient.signalName)
	}
	if !reflect.DeepEqual(workflowClient.signalArg, signal) {
		t.Fatalf("signal arg = %#v, want %#v", workflowClient.signalArg, signal)
	}
}

func TestCancelTemplateRunSignalsWorkflow(t *testing.T) {
	t.Parallel()

	workflowClient := &recordingWorkflowClient{}
	dispatcher := newDispatcher(workflowClient, "terraform-runs")
	signal := traits.CancelSignal{
		RequestedBy: traits.UserID("user_456"),
		Reason:      "superseded by a newer run",
	}

	err := dispatcher.CancelTemplateRun(
		context.Background(),
		traits.TenantID("tenant_123"),
		traits.TemplateRunID("run_123"),
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
	if workflowClient.signalName != traits.CancelSignalName {
		t.Fatalf("signal name = %q", workflowClient.signalName)
	}
	if !reflect.DeepEqual(workflowClient.signalArg, signal) {
		t.Fatalf("signal arg = %#v, want %#v", workflowClient.signalArg, signal)
	}
}

func TestStartTemplateRunWrapsClientError(t *testing.T) {
	t.Parallel()

	clientErr := errors.New("temporal unavailable")
	dispatcher := newDispatcher(&recordingWorkflowClient{executeErr: clientErr}, "terraform-runs")

	err := dispatcher.StartTemplateRun(context.Background(), traits.TemplateRunWorkflowInput{
		RunID:    traits.TemplateRunID("run_123"),
		TenantID: traits.TenantID("tenant_123"),
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
	dispatcher := newDispatcher(&recordingWorkflowClient{executeErr: clientErr}, "terraform-runs")

	err := dispatcher.StartTemplateSync(context.Background(), traits.TemplateSyncWorkflowInput{
		RegistrationID: traits.TemplateRegistrationID("template_registration_123"),
		TenantID:       traits.TenantID("tenant_123"),
	})
	if !errors.Is(err, clientErr) {
		t.Fatalf("error = %v, want wrapped client error", err)
	}
	if !strings.Contains(err.Error(), "start template sync workflow") {
		t.Fatalf("error = %q, want start context", err.Error())
	}
}

func TestApproveTemplateRunWrapsClientError(t *testing.T) {
	t.Parallel()

	clientErr := errors.New("temporal unavailable")
	dispatcher := newDispatcher(&recordingWorkflowClient{signalErr: clientErr}, "terraform-runs")

	err := dispatcher.ApproveTemplateRun(
		context.Background(),
		traits.TenantID("tenant_123"),
		traits.TemplateRunID("run_123"),
		traits.ApprovalSignal{ApprovedBy: traits.UserID("user_123")},
	)
	if !errors.Is(err, clientErr) {
		t.Fatalf("error = %v, want wrapped client error", err)
	}
	if !strings.Contains(err.Error(), "signal template run approval") {
		t.Fatalf("error = %q, want approval context", err.Error())
	}
}

func TestCancelTemplateRunWrapsClientError(t *testing.T) {
	t.Parallel()

	clientErr := errors.New("temporal unavailable")
	dispatcher := newDispatcher(&recordingWorkflowClient{signalErr: clientErr}, "terraform-runs")

	err := dispatcher.CancelTemplateRun(
		context.Background(),
		traits.TenantID("tenant_123"),
		traits.TemplateRunID("run_123"),
		traits.CancelSignal{RequestedBy: traits.UserID("user_456")},
	)
	if !errors.Is(err, clientErr) {
		t.Fatalf("error = %v, want wrapped client error", err)
	}
	if !strings.Contains(err.Error(), "signal template run cancellation") {
		t.Fatalf("error = %q, want cancellation context", err.Error())
	}
}

func TestTemplateRunWorkflowID(t *testing.T) {
	t.Parallel()

	got := templateRunWorkflowID(traits.TenantID("tenant_123"), traits.TemplateRunID("run_123"))
	if got != "template-run/tenant_123/run_123" {
		t.Fatalf("workflow ID = %q", got)
	}
}

func TestTemplateSyncWorkflowID(t *testing.T) {
	t.Parallel()

	got := templateSyncWorkflowID(traits.TenantID("tenant_123"), traits.TemplateRegistrationID("template_registration_123"))
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
	return workflowClient.signalErr
}
