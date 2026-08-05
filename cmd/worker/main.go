package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/vishu42/tflive/internal/activities"
	"github.com/vishu42/tflive/internal/artifacts"
	"github.com/vishu42/tflive/internal/authz"
	"github.com/vishu42/tflive/internal/config"
	"github.com/vishu42/tflive/internal/dispatch"
	"github.com/vishu42/tflive/internal/openfga"
	"github.com/vishu42/tflive/internal/postgres"
	"github.com/vishu42/tflive/internal/queue"
	"github.com/vishu42/tflive/internal/temporal"
	"github.com/vishu42/tflive/internal/traits"
	"github.com/vishu42/tflive/internal/workflows"
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

type workerStore interface {
	activities.StatusRecorder
	activities.TemplateSyncStore
	artifacts.LogMetadataRecorder
	dispatch.Outbox
	queue.Backend
}

// workerAuthorizer is the authorization surface the worker needs: decisions
// plus the per-subject read the reconciling grant handler converges against.
type workerAuthorizer interface {
	authz.Authorizer
	authz.SubjectGrantLister
}

type outboxDispatcher interface {
	Run(context.Context)
}

type workerDependencies struct {
	// newPostgresPool opens the database connection pool used by the worker.
	newPostgresPool func(context.Context, string) (postgresPool, error)
	// migratePostgres applies the schema migrations required before activities can record state.
	migratePostgres func(context.Context, postgresPool) error
	// newStore builds the persistence adapter shared by worker activities.
	newStore func(postgresPool) (workerStore, error)
	// dialTemporal connects to the Temporal namespace where the worker polls for tasks.
	dialTemporal func(context.Context, temporal.Config) (client.Client, error)
	// newWorker creates the Temporal worker bound to the configured task queue.
	newWorker func(client.Client, string, temporalworker.Options) temporalWorker
	// newWorkflowStarter creates the idempotent Temporal workflow starter used by the outbox.
	newWorkflowStarter func(client.Client, string) dispatch.WorkflowStarter
	// newOutboxDispatcher builds the durable Postgres-to-Temporal dispatch loop.
	newOutboxDispatcher     func(dispatch.Outbox, dispatch.WorkflowStarter) outboxDispatcher
	newAuthorizationAdapter func(config.OpenFGAConfig) (workerAuthorizer, error)
	newQueueController      func(queue.Backend, workerAuthorizer) (outboxDispatcher, error)
	// registerWorkflow attaches the workflow implementations this process can execute.
	registerWorkflow func(temporalWorker)
	// registerActivities attaches activity handlers and their shared dependencies to the worker.
	registerActivities func(temporalWorker, workerStore, string, activities.TemplateRunLogStore)
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
		newStore: func(pool postgresPool) (workerStore, error) {
			pgxPool, ok := pool.(*pgxpool.Pool)
			if !ok {
				return nil, fmt.Errorf("unexpected postgres pool type %T", pool)
			}
			return postgres.NewStore(pgxPool), nil
		},
		dialTemporal: temporal.Dial,
		newWorker: func(temporalClient client.Client, taskQueue string, options temporalworker.Options) temporalWorker {
			return temporalworker.New(temporalClient, taskQueue, options)
		},
		newWorkflowStarter: func(temporalClient client.Client, taskQueue string) dispatch.WorkflowStarter {
			return temporal.NewDispatcher(temporalClient, taskQueue)
		},
		newOutboxDispatcher: func(outbox dispatch.Outbox, starter dispatch.WorkflowStarter) outboxDispatcher {
			return dispatch.NewDispatcher(outbox, starter, dispatch.Options{})
		},
		newAuthorizationAdapter: func(cfg config.OpenFGAConfig) (workerAuthorizer, error) {
			return openfga.NewAuthorizationAdapter(openfga.Config{APIURL: cfg.APIURL, StoreID: cfg.StoreID, ModelID: cfg.ModelID, APIToken: cfg.APIToken.Value(), HTTPTimeout: cfg.RequestTimeout})
		},
		newQueueController: func(backend queue.Backend, authorizer workerAuthorizer) (outboxDispatcher, error) {
			registry, err := queue.NewRegistry(authz.NewStackGrantHandler(authorizer))
			if err != nil {
				return nil, err
			}
			return queue.NewController(backend, registry, queue.Options{}), nil
		},
		registerWorkflow: func(worker temporalWorker) {
			worker.RegisterWorkflowWithOptions(workflows.TemplateRunWorkflow, workflow.RegisterOptions{
				Name: traits.TemplateRunWorkflowName,
			})
			worker.RegisterWorkflowWithOptions(workflows.TemplateSyncWorkflow, workflow.RegisterOptions{
				Name: traits.TemplateSyncWorkflowName,
			})
		},
		registerActivities: func(worker temporalWorker, store workerStore, runRoot string, logStore activities.TemplateRunLogStore) {
			reader, _ := store.(activities.CredentialReader)
			decryptor, _ := store.(activities.CredentialDecryptor)
			templateRunActivities := activities.NewTemplateRunActivitiesWithCredentials(store, runRoot, logStore, reader, decryptor)
			worker.RegisterActivityWithOptions(templateRunActivities.PrepareWorkspace, activity.RegisterOptions{
				Name: traits.PrepareWorkspaceActivityName,
			})
			worker.RegisterActivityWithOptions(templateRunActivities.FetchSource, activity.RegisterOptions{
				Name: traits.FetchSourceActivityName,
			})
			worker.RegisterActivityWithOptions(templateRunActivities.RunTerraform, activity.RegisterOptions{
				Name: traits.RunTerraformActivityName,
			})
			worker.RegisterActivityWithOptions(templateRunActivities.RecordTemplateRunStatus, activity.RegisterOptions{
				Name: traits.RecordTemplateRunStatusActivityName,
			})

			templateSyncActivities := activities.NewTemplateSyncActivities(store)
			worker.RegisterActivityWithOptions(templateSyncActivities.RecordTemplateRegistrationStatus, activity.RegisterOptions{
				Name: traits.RecordTemplateRegistrationStatusActivityName,
			})
			worker.RegisterActivityWithOptions(templateSyncActivities.SyncTemplate, activity.RegisterOptions{
				Name: traits.SyncTemplateActivityName,
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
	authorizer, err := deps.newAuthorizationAdapter(cfg.OpenFGA)
	if err != nil {
		return fmt.Errorf("create authorization adapter: %w", err)
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

	worker := deps.newWorker(temporalClient, cfg.TemporalTaskQueue, temporalworker.Options{EnableSessionWorker: true})
	deps.registerWorkflow(worker)
	deps.registerActivities(worker, store, cfg.WorkerRunRoot, logStore)
	workflowStarter := deps.newWorkflowStarter(temporalClient, cfg.TemporalTaskQueue)
	outbox := deps.newOutboxDispatcher(store, workflowStarter)
	controller, err := deps.newQueueController(store, authorizer)
	if err != nil {
		return fmt.Errorf("build queue controller: %w", err)
	}

	dispatchCtx, cancelDispatch := context.WithCancel(ctx)
	var dispatchGroup sync.WaitGroup
	dispatchGroup.Add(2)
	go func() {
		defer dispatchGroup.Done()
		outbox.Run(dispatchCtx)
	}()
	go func() { defer dispatchGroup.Done(); controller.Run(dispatchCtx) }()

	workerErr := worker.Run(deps.interruptCh())
	cancelDispatch()
	dispatchGroup.Wait()
	if workerErr != nil {
		return fmt.Errorf("run worker: %w", workerErr)
	}

	return nil
}
