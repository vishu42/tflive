package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/vishu42/megagega/internal/activities"
	"github.com/vishu42/megagega/internal/config"
	"github.com/vishu42/megagega/internal/postgres"
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

type postgresPool interface {
	Ping(context.Context) error
	Close()
}

type statusRecorder interface {
	RecordTemplateRunStatus(context.Context, traits.TemplateRunStatusActivityInput) error
}

type workerDependencies struct {
	newPostgresPool    func(context.Context, string) (postgresPool, error)
	migratePostgres    func(context.Context, postgresPool) error
	newStore           func(postgresPool) (statusRecorder, error)
	dialTemporal       func(context.Context, temporal.Config) (client.Client, error)
	newWorker          func(client.Client, string) temporalWorker
	registerWorkflow   func(temporalWorker)
	registerActivities func(temporalWorker, statusRecorder, string)
	interruptCh        func() <-chan interface{}
}

func main() {
	if err := run(context.Background(), os.Getenv); err != nil {
		log.Fatal(err)
	}
}

func defaultWorkerDependencies() workerDependencies {
	return workerDependencies{
		newPostgresPool: func(ctx context.Context, databaseURL string) (postgresPool, error) {
			return pgxpool.New(ctx, databaseURL)
		},
		migratePostgres: func(ctx context.Context, pool postgresPool) error {
			pgxPool, ok := pool.(*pgxpool.Pool)
			if !ok {
				return fmt.Errorf("unexpected postgres pool type %T", pool)
			}
			return postgres.Migrate(ctx, pgxPool)
		},
		newStore: func(pool postgresPool) (statusRecorder, error) {
			pgxPool, ok := pool.(*pgxpool.Pool)
			if !ok {
				return nil, fmt.Errorf("unexpected postgres pool type %T", pool)
			}
			return postgres.NewStore(pgxPool), nil
		},
		dialTemporal: temporal.Dial,
		newWorker: func(temporalClient client.Client, taskQueue string) temporalWorker {
			return temporalworker.New(temporalClient, taskQueue, temporalworker.Options{})
		},
		registerWorkflow: func(worker temporalWorker) {
			worker.RegisterWorkflowWithOptions(workflows.TemplateRunWorkflow, workflow.RegisterOptions{
				Name: traits.TemplateRunWorkflowName,
			})
		},
		registerActivities: func(worker temporalWorker, recorder statusRecorder, runRoot string) {
			templateRunActivities := activities.NewTemplateRunActivities(recorder, runRoot)
			worker.RegisterActivityWithOptions(templateRunActivities.PrepareWorkspace, activity.RegisterOptions{
				Name: traits.PrepareWorkspaceActivityName,
			})
			worker.RegisterActivityWithOptions(templateRunActivities.RecordTemplateRunStatus, activity.RegisterOptions{
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

	pool, err := deps.newPostgresPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("create postgres pool: %w", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("ping postgres: %w", err)
	}

	if err := deps.migratePostgres(ctx, pool); err != nil {
		return fmt.Errorf("migrate postgres: %w", err)
	}

	store, err := deps.newStore(pool)
	if err != nil {
		return fmt.Errorf("wire activities: %w", err)
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
	deps.registerActivities(worker, store, cfg.WorkerRunRoot)

	if err := worker.Run(deps.interruptCh()); err != nil {
		return fmt.Errorf("run worker: %w", err)
	}

	return nil
}
