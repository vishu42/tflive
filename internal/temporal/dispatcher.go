package temporal

import (
	"context"
	"fmt"

	"github.com/vishu42/megagega/internal/traits"
	"go.temporal.io/sdk/client"
)

type workflowClient interface {
	ExecuteWorkflow(context.Context, client.StartWorkflowOptions, interface{}, ...interface{}) (client.WorkflowRun, error)
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

func (dispatcher *Dispatcher) StartTemplateRun(ctx context.Context, input traits.TemplateRunWorkflowInput) error {
	_, err := dispatcher.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        templateRunWorkflowID(input.TenantID, input.RunID),
		TaskQueue: dispatcher.taskQueue,
	}, traits.TemplateRunWorkflowName, input)
	if err != nil {
		return err
	}

	return nil
}

func templateRunWorkflowID(tenantID traits.TenantID, runID traits.TemplateRunID) string {
	return fmt.Sprintf("template-run/%s/%s", tenantID, runID)
}
