package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/vishu42/megagega/internal/activities"
	"github.com/vishu42/megagega/internal/artifacts"
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
	RecordTemplateRunLog(context.Context, traits.TemplateRunLog) error
}

type workerDependencies struct {
	// newPostgresPool opens the database connection pool used by the worker.
	newPostgresPool func(context.Context, string) (postgresPool, error)
	// migratePostgres applies the schema migrations required before activities can record state.
	migratePostgres func(context.Context, postgresPool) error
	// newStore builds the persistence adapter shared by worker activities.
	newStore func(postgresPool) (statusRecorder, error)
	// dialTemporal connects to the Temporal namespace where the worker polls for tasks.
	dialTemporal func(context.Context, temporal.Config) (client.Client, error)
	// newWorker creates the Temporal worker bound to the configured task queue.
	newWorker func(client.Client, string) temporalWorker
	// registerWorkflow attaches the workflow implementations this process can execute.
	registerWorkflow func(temporalWorker)
	// registerActivities attaches activity handlers and their shared dependencies to the worker.
	registerActivities func(temporalWorker, statusRecorder, string, activities.TemplateRunLogStore)
	// newLogStore builds the artifact-backed log store used by Terraform activities.
	newLogStore func(config.ArtifactStoreConfig, artifacts.LogMetadataRecorder) (activities.TemplateRunLogStore, error)
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
		registerActivities: func(worker temporalWorker, recorder statusRecorder, runRoot string, logStore activities.TemplateRunLogStore) {
			templateRunActivities := activities.NewTemplateRunActivitiesWithLogStore(recorder, runRoot, logStore)
			worker.RegisterActivityWithOptions(templateRunActivities.PrepareWorkspace, activity.RegisterOptions{
				Name: traits.PrepareWorkspaceActivityName,
			})
			worker.RegisterActivityWithOptions(templateRunActivities.RunTerraform, activity.RegisterOptions{
				Name: traits.RunTerraformActivityName,
			})
			worker.RegisterActivityWithOptions(templateRunActivities.RecordTemplateRunStatus, activity.RegisterOptions{
				Name: traits.RecordTemplateRunStatusActivityName,
			})
		},
		newLogStore: func(cfg config.ArtifactStoreConfig, recorder artifacts.LogMetadataRecorder) (activities.TemplateRunLogStore, error) {
			store, err := artifacts.NewObjectStore(cfg)
			if err != nil {
				return nil, err
			}
			return artifacts.NewRecordedLogStore(store, recorder), nil
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
	logStore, err := deps.newLogStore(cfg.ArtifactStore, store)
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

	worker := deps.newWorker(temporalClient, cfg.TemporalTaskQueue)
	deps.registerWorkflow(worker)
	deps.registerActivities(worker, store, cfg.WorkerRunRoot, logStore)

	if err := worker.Run(deps.interruptCh()); err != nil {
		return fmt.Errorf("run worker: %w", err)
	}

	return nil
}
