package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/vishu42/tflive/internal/activities"
	"github.com/vishu42/tflive/internal/artifacts"
	"github.com/vishu42/tflive/internal/config"
	"github.com/vishu42/tflive/internal/domain"
	"github.com/vishu42/tflive/internal/runseal"
	"github.com/vishu42/tflive/internal/temporal"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	temporalworker "go.temporal.io/sdk/worker"
)

type temporalWorker interface {
	RegisterActivityWithOptions(interface{}, activity.RegisterOptions)
	Run(<-chan interface{}) error
}

type workerDependencies struct {
	// dialTemporal connects to the Temporal namespace where the worker polls for tasks.
	dialTemporal func(context.Context, temporal.Config) (client.Client, error)
	// newWorker creates the Temporal worker bound to the execution task queue.
	newWorker func(client.Client, string, temporalworker.Options) temporalWorker
	// registerActivities attaches the execution activities to the worker.
	registerActivities func(worker temporalWorker, runRoot string, logStore activities.TemplateRunLogStore, keys *runseal.KeyRing)
	// newLogStore builds the artifact-backed log store used by Terraform activities.
	newLogStore func(config.ArtifactStoreConfig) (activities.TemplateRunLogStore, error)
	// interruptCh provides the shutdown signal consumed by the Temporal worker run loop.
	interruptCh func() <-chan interface{}
}

func main() {
	if err := run(context.Background(), os.Getenv); err != nil {
		log.Fatal(err)
	}
}

func defaultWorkerDependencies() workerDependencies {
	return workerDependencies{
		dialTemporal: temporal.Dial,
		newWorker: func(temporalClient client.Client, taskQueue string, options temporalworker.Options) temporalWorker {
			return temporalworker.New(temporalClient, taskQueue, options)
		},
		registerActivities: func(worker temporalWorker, runRoot string, logStore activities.TemplateRunLogStore, keys *runseal.KeyRing) {
			templateRunActivities := activities.NewTemplateRunActivities(runRoot, logStore, keys)
			worker.RegisterActivityWithOptions(templateRunActivities.PrepareWorkspace, activity.RegisterOptions{
				Name: domain.PrepareWorkspaceActivityName,
			})
			worker.RegisterActivityWithOptions(templateRunActivities.FetchSource, activity.RegisterOptions{
				Name: domain.FetchSourceActivityName,
			})
			worker.RegisterActivityWithOptions(templateRunActivities.RunTerraform, activity.RegisterOptions{
				Name: domain.RunTerraformActivityName,
			})
			worker.RegisterActivityWithOptions(templateRunActivities.ReleaseRunKey, activity.RegisterOptions{
				Name: domain.ReleaseRunKeyActivityName,
			})
		},
		newLogStore: func(cfg config.ArtifactStoreConfig) (activities.TemplateRunLogStore, error) {
			store, err := artifacts.NewObjectStore(cfg)
			if err != nil {
				return nil, err
			}
			return artifacts.NewLogStore(store), nil
		},
		interruptCh: temporalworker.InterruptCh,
	}
}

func run(ctx context.Context, getenv func(string) string) error {
	return runWithDependencies(ctx, getenv, defaultWorkerDependencies())
}

// runWithDependencies runs the executor: a Temporal worker on the execution
// queue, next to tenant Terraform. It deliberately opens no database connection
// and parses no key. Everything secret a run needs arrives sealed to a key this
// process generates for that run, and everything a run produces goes back
// through Temporal to the control plane.
func runWithDependencies(ctx context.Context, getenv func(string) string, deps workerDependencies) error {
	cfg, err := config.LoadWorkerConfig(getenv)
	if err != nil {
		return fmt.Errorf("load worker config: %w", err)
	}

	logStore, err := deps.newLogStore(cfg.ArtifactStore)
	if err != nil {
		return fmt.Errorf("wire log store: %w", err)
	}

	temporalClient, err := deps.dialTemporal(ctx, temporal.Config{
		Address:   cfg.TemporalAddress,
		Namespace: cfg.TemporalNamespace,
	})
	if err != nil {
		return fmt.Errorf("dial temporal: %w", err)
	}
	defer temporalClient.Close()

	// Sessions pin a run's workspace activities, and so its sealing key, to this
	// process.
	worker := deps.newWorker(temporalClient, domain.ExecutionTaskQueue, temporalworker.Options{EnableSessionWorker: true})
	deps.registerActivities(worker, cfg.WorkerRunRoot, logStore, runseal.NewKeyRing())
	if err := worker.Run(deps.interruptCh()); err != nil {
		return fmt.Errorf("run worker: %w", err)
	}

	return nil
}
