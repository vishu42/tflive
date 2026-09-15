package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/vishu42/tflive/internal/activities"
	"github.com/vishu42/tflive/internal/app"
	"github.com/vishu42/tflive/internal/artifacts"
	"github.com/vishu42/tflive/internal/config"
	"github.com/vishu42/tflive/internal/domain"
	"github.com/vishu42/tflive/internal/encryption"
	"github.com/vishu42/tflive/internal/githubapp"
	"github.com/vishu42/tflive/internal/postgres"
	"github.com/vishu42/tflive/internal/queue"
	"github.com/vishu42/tflive/internal/temporal"
	"github.com/vishu42/tflive/internal/workflows"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	temporalworker "go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

type temporalWorker interface {
	RegisterWorkflowWithOptions(interface{}, workflow.RegisterOptions)
	RegisterActivityWithOptions(interface{}, activity.RegisterOptions)
	Start() error
	Run(<-chan interface{}) error
	Stop()
}

type postgresPool interface {
	Ping(context.Context) error
	Close()
}

type workerStore interface {
	activities.StatusRecorder
	activities.TemplateSyncStore
	artifacts.LogMetadataRecorder
	queue.Backend
	queue.Enqueuer
	interface {
		ReconcileTemplateRunCancellation(context.Context, domain.TenantID, domain.TemplateRunID, string) error
	}
}

type queueController interface {
	Run(context.Context)
}

type workerDependencies struct {
	// newPostgresPool opens the database connection pool used by the worker.
	newPostgresPool func(context.Context, string) (postgresPool, error)
	// migratePostgres applies the schema migrations required before activities can record state.
	migratePostgres func(context.Context, postgresPool) error
	// newStore builds the persistence adapter shared by worker activities.
	newStore func(postgresPool, *encryption.Cipher) (workerStore, error)
	// dialTemporal connects to the Temporal namespace where the worker polls for tasks.
	dialTemporal func(context.Context, temporal.Config) (client.Client, error)
	// newWorker creates the Temporal worker bound to the configured task queue.
	newWorker func(client.Client, string, temporalworker.Options) temporalWorker
	// newDispatcher creates the Temporal workflow and signal dispatcher used by queue handlers.
	newDispatcher      func(client.Client) app.WorkflowDispatcher
	newQueueController func(workerStore, app.WorkflowDispatcher) (queueController, error)
	// registerWorkflow attaches the workflow implementations to the control worker.
	registerWorkflow func(temporalWorker)
	// registerActivities attaches control activities to the first worker and
	// execution activities to the second.
	registerActivities func(control temporalWorker, execution temporalWorker, store workerStore, runRoot string, logStore activities.TemplateRunLogStore, gitHubTokens activities.GitHubTokenSource)
	// newLogStore builds the artifact-backed log store used by Terraform activities.
	newLogStore func(config.ArtifactStoreConfig, artifacts.LogMetadataRecorder) (activities.TemplateRunLogStore, error)
	// interruptCh provides the shutdown signal consumed by the Temporal worker run loop.
	interruptCh func() <-chan interface{}
}

// newQueueRegistry builds the worker's handlers.
//
// None of them touch authorization. Granting the founding owner, reconciling a
// role change and flipping a stack to ready used to live here, because a tuple
// write could not commit with the domain write that caused it; it can now, so
// all three happen in the API's transaction and the worker is Temporal
// dispatch alone.
func newQueueRegistry(store workerStore, dispatcher app.WorkflowDispatcher) (*queue.Registry, error) {
	return queue.NewRegistry(
		app.NewStartTemplateRunHandler(dispatcher),
		app.NewStartTemplateSyncHandler(dispatcher),
		app.NewSignalRunApprovalHandler(dispatcher),
		app.NewSignalRunCancellationHandler(dispatcher, store),
	)
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
		newStore: func(pool postgresPool, cipher *encryption.Cipher) (workerStore, error) {
			pgxPool, ok := pool.(*pgxpool.Pool)
			if !ok {
				return nil, fmt.Errorf("unexpected postgres pool type %T", pool)
			}
			// The worker both delivers work and enqueues the follow-ups handlers
			// return, so its store needs the specs to derive resource keys.
			specs, err := queue.NewSpecRegistry(app.QueueSpecs()...)
			if err != nil {
				return nil, fmt.Errorf("build queue specs: %w", err)
			}
			return postgres.NewStore(pgxPool, postgres.WithQueueSpecs(specs), postgres.WithCredentialCipher(cipher)), nil
		},
		dialTemporal: temporal.Dial,
		newWorker: func(temporalClient client.Client, taskQueue string, options temporalworker.Options) temporalWorker {
			return temporalworker.New(temporalClient, taskQueue, options)
		},
		newDispatcher: func(temporalClient client.Client) app.WorkflowDispatcher {
			return temporal.NewDispatcher(temporalClient)
		},
		newQueueController: func(store workerStore, dispatcher app.WorkflowDispatcher) (queueController, error) {
			registry, err := newQueueRegistry(store, dispatcher)
			if err != nil {
				return nil, err
			}
			return queue.NewController(store, registry, store, queue.Options{}), nil
		},
		registerWorkflow: func(worker temporalWorker) {
			worker.RegisterWorkflowWithOptions(workflows.TemplateRunWorkflow, workflow.RegisterOptions{
				Name: domain.TemplateRunWorkflowName,
			})
			worker.RegisterWorkflowWithOptions(workflows.TemplateSyncWorkflow, workflow.RegisterOptions{
				Name: domain.TemplateSyncWorkflowName,
			})
		},
		registerActivities: func(control temporalWorker, execution temporalWorker, store workerStore, runRoot string, logStore activities.TemplateRunLogStore, gitHubTokens activities.GitHubTokenSource) {
			reader, _ := store.(activities.CredentialReader)
			decryptor, _ := store.(activities.CredentialDecryptor)
			templateRunActivities := activities.NewTemplateRunActivitiesWithCredentials(store, runRoot, logStore, reader, decryptor, gitHubTokens)
			execution.RegisterActivityWithOptions(templateRunActivities.PrepareWorkspace, activity.RegisterOptions{
				Name: domain.PrepareWorkspaceActivityName,
			})
			execution.RegisterActivityWithOptions(templateRunActivities.FetchSource, activity.RegisterOptions{
				Name: domain.FetchSourceActivityName,
			})
			execution.RegisterActivityWithOptions(templateRunActivities.RunTerraform, activity.RegisterOptions{
				Name: domain.RunTerraformActivityName,
			})
			control.RegisterActivityWithOptions(templateRunActivities.RecordTemplateRunStatus, activity.RegisterOptions{
				Name: domain.RecordTemplateRunStatusActivityName,
			})

			templateSyncActivities := activities.NewTemplateSyncActivities(store, activities.WithTemplateSyncTokenSource(gitHubTokens))
			control.RegisterActivityWithOptions(templateSyncActivities.RecordTemplateRegistrationStatus, activity.RegisterOptions{
				Name: domain.RecordTemplateRegistrationStatusActivityName,
			})
			control.RegisterActivityWithOptions(templateSyncActivities.SyncTemplate, activity.RegisterOptions{
				Name: domain.SyncTemplateActivityName,
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

// scrubConsumedSecret removes a secret from the process environment once its
// value has already been parsed into the in-memory object the worker
// actually uses. It exists as its own function so the scrub can be verified
// by test without exercising the rest of worker startup (real Postgres,
// Temporal, etc.).
//
// os.Unsetenv only errors when given a malformed variable name (one
// containing "="), which never happens for the fixed names this is called
// with, so the error is not worth surfacing.
func scrubConsumedSecret(name string) {
	_ = os.Unsetenv(name)
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

	var credentialCipher *encryption.Cipher
	if !cfg.CredentialEncryptionKey.Empty() {
		credentialCipher, err = encryption.NewCipher(cfg.CredentialEncryptionKey.Value())
		if err != nil {
			return fmt.Errorf("create credential cipher: %w", err)
		}
		// The key text is now sealed inside credentialCipher and never read from
		// the environment again. Leaving it in os.Environ would hand it to every
		// Terraform subprocess this worker starts: runner.CommandExecutor inherits
		// the full process environment (see internal/runner/executor.go) because
		// Terraform legitimately needs PATH, HOME, and provider credentials from
		// it, and that inheritance cannot distinguish "meant for Terraform" from
		// "meant for us."
		scrubConsumedSecret("CREDENTIAL_ENCRYPTION_KEY")
	}

	var gitHubTokens *githubapp.TokenSource
	if cfg.GitHubApp.Enabled() {
		privateKey, err := githubapp.ParsePrivateKey(cfg.GitHubApp.PrivateKey.Value())
		if err != nil {
			// Unreachable in practice: LoadWorkerConfig parses the same key at
			// startup precisely so this cannot fail here.
			return fmt.Errorf("parse github app private key: %w", err)
		}
		gitHubTokens = githubapp.NewTokenSource(githubapp.NewClient(cfg.GitHubApp.AppID, privateKey))
		// The PEM text is now parsed into privateKey and never read from the
		// environment again. This key mints installation tokens for every
		// repository across every installation of the App -- strictly more
		// powerful than the repo-scoped token the rest of this branch works hard
		// to contain -- so it must not sit in os.Environ where every Terraform
		// subprocess (an `external` data source, local-exec, a provider binary)
		// can read it. See the CREDENTIAL_ENCRYPTION_KEY comment above for why
		// executor.go's environment inheritance can't be the place this is fixed.
		scrubConsumedSecret("GITHUB_APP_PRIVATE_KEY")
	}

	store, err := deps.newStore(pool, credentialCipher)
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

	controlWorker := deps.newWorker(temporalClient, domain.ControlTaskQueue, temporalworker.Options{})
	// Sessions live on the execution queue only: they pin a run's workspace
	// activities to the host holding its checkout.
	executionWorker := deps.newWorker(temporalClient, domain.ExecutionTaskQueue, temporalworker.Options{EnableSessionWorker: true})
	deps.registerWorkflow(controlWorker)
	deps.registerActivities(controlWorker, executionWorker, store, cfg.WorkerRunRoot, logStore, gitHubTokens)
	dispatcher := deps.newDispatcher(temporalClient)
	controller, err := deps.newQueueController(store, dispatcher)
	if err != nil {
		return fmt.Errorf("build queue controller: %w", err)
	}

	controllerCtx, cancelController := context.WithCancel(ctx)
	controllerDone := make(chan struct{})
	go func() {
		defer close(controllerDone)
		controller.Run(controllerCtx)
	}()

	if err := controlWorker.Start(); err != nil {
		cancelController()
		<-controllerDone
		return fmt.Errorf("start control worker: %w", err)
	}
	workerErr := executionWorker.Run(deps.interruptCh())
	controlWorker.Stop()
	cancelController()
	<-controllerDone
	if workerErr != nil {
		return fmt.Errorf("run worker: %w", workerErr)
	}

	return nil
}
