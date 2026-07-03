package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/vishu42/megagega/internal/activities"
	"github.com/vishu42/megagega/internal/config"
	"github.com/vishu42/megagega/internal/temporal"
	"github.com/vishu42/megagega/internal/traits"
	"github.com/vishu42/megagega/internal/workflows"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	temporalworker "go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

type temporalWorker interface {
	RegisterWorkflowWithOptions(interface{}, workflow.RegisterOptions)
	RegisterActivityWithOptions(interface{}, activity.RegisterOptions)
	Run(<-chan interface{}) error
}

type workerDependencies struct {
	dialTemporal       func(context.Context, temporal.Config) (client.Client, error)
	newWorker          func(client.Client, string) temporalWorker
	registerWorkflow   func(temporalWorker)
	registerActivities func(temporalWorker)
	interruptCh        func() <-chan interface{}
}

var recordTemplateRunStatusActivity = activities.RecordTemplateRunStatus

func main() {
	if err := run(context.Background(), os.Getenv); err != nil {
		log.Fatal(err)
	}
}

func defaultWorkerDependencies() workerDependencies {
	return workerDependencies{
		dialTemporal: temporal.Dial,
		newWorker: func(temporalClient client.Client, taskQueue string) temporalWorker {
			return temporalworker.New(temporalClient, taskQueue, temporalworker.Options{})
		},
		registerWorkflow: func(worker temporalWorker) {
			worker.RegisterWorkflowWithOptions(workflows.TemplateRunWorkflow, workflow.RegisterOptions{
				Name: traits.TemplateRunWorkflowName,
			})
		},
		registerActivities: func(worker temporalWorker) {
			worker.RegisterActivityWithOptions(recordTemplateRunStatusActivity, activity.RegisterOptions{
				Name: traits.RecordTemplateRunStatusActivityName,
			})
		},
		interruptCh: temporalworker.InterruptCh,
	}
}

func run(ctx context.Context, getenv func(string) string) error {
	return runWithDependencies(ctx, getenv, defaultWorkerDependencies())
}

func runWithDependencies(ctx context.Context, getenv func(string) string, deps workerDependencies) error {
	cfg, err := config.LoadWorkerConfig(getenv)
	if err != nil {
		return fmt.Errorf("load worker config: %w", err)
	}

	temporalClient, err := deps.dialTemporal(ctx, temporal.Config{
		Address:   cfg.TemporalAddress,
		Namespace: cfg.TemporalNamespace,
	})
	if err != nil {
		return fmt.Errorf("dial temporal: %w", err)
	}
	defer temporalClient.Close()

	worker := deps.newWorker(temporalClient, cfg.TemporalTaskQueue)
	deps.registerWorkflow(worker)
	deps.registerActivities(worker)

	if err := worker.Run(deps.interruptCh()); err != nil {
		return fmt.Errorf("run worker: %w", err)
	}

	return nil
}
