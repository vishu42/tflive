package temporal

import (
	"context"
	"reflect"
	"testing"

	"github.com/vishu42/megagega/internal/app"
	"github.com/vishu42/megagega/internal/traits"
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

func TestTemplateRunWorkflowID(t *testing.T) {
	t.Parallel()

	got := templateRunWorkflowID(traits.TenantID("tenant_123"), traits.TemplateRunID("run_123"))
	if got != "template-run/tenant_123/run_123" {
		t.Fatalf("workflow ID = %q", got)
	}
}

type recordingWorkflowClient struct {
	executeOptions   client.StartWorkflowOptions
	executeWorkflow  interface{}
	executeArgs      []interface{}
	signalWorkflowID string
	signalRunID      string
	signalName       string
	signalArg        interface{}
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
	return nil, nil
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
	return nil
}
