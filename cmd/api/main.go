package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/vishu42/tflive/internal/activities"
	"github.com/vishu42/tflive/internal/api"
	"github.com/vishu42/tflive/internal/app"
	"github.com/vishu42/tflive/internal/artifacts"
	"github.com/vishu42/tflive/internal/authn"
	"github.com/vishu42/tflive/internal/authorization"
	"github.com/vishu42/tflive/internal/bootstrap"
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

type postgresPool interface {
	Ping(context.Context) error
	Close()
}

type appRepositories interface {
	app.StackRepository
	app.StackTemplateRepository
	app.StackTemplateInstaller
	app.TemplateRunRepository
	app.TemplateRegistrationRepository
	app.TemplateRevisionMetadataRepository
	app.TemplateRevisionRepository
	app.TemplateRunLogRepository
	app.AuditRepository
	app.UserRepository
	app.UnitOfWork
	queue.Reader
}

// controlStore is what the control plane needs from the store beyond serving
// HTTP: the queue loop claims and settles intents, and the control activities
// write run and registration state.
type controlStore interface {
	queue.Backend
	queue.Enqueuer
	activities.ControlStore
	activities.TemplateSyncStore
}

type temporalWorker interface {
	RegisterWorkflowWithOptions(any, workflow.RegisterOptions)
	RegisterActivityWithOptions(any, activity.RegisterOptions)
	Start() error
	Stop()
}

type queueController interface {
	Run(context.Context)
}

type tokenVerifier interface {
	authn.Verifier
	authn.EndpointSource
	VerifyLogoutToken(context.Context, string) (authn.LogoutToken, error)
	Close(context.Context) error
}

type apiDependencies struct {
	newPostgresPool      func(context.Context, string) (postgresPool, error)
	migratePostgres      func(context.Context, postgresPool) error
	migrateAuthorization func(string) error
	newStore             func(postgresPool, *queue.SpecRegistry, *encryption.Cipher, *encryption.Cipher) (appRepositories, error)
	newLogReader         func(config.ArtifactStoreConfig) (app.TemplateRunLogReader, error)
	newService           func(app.Service) (*app.Service, error)
	newVerifier          func(context.Context, authn.OIDCVerifierConfig) (tokenVerifier, error)
	newAuthorization     func(context.Context, postgresPool, string) (*authorization.Authorization, error)
	listenAndServe       func(context.Context, string, http.Handler) error

	dialTemporal       func(context.Context, temporal.Config) (client.Client, error)
	newWorker          func(client.Client, string, temporalworker.Options) temporalWorker
	newDispatcher      func(client.Client, temporal.DispatcherOptions) app.WorkflowDispatcher
	newQueueController func(controlStore, app.WorkflowDispatcher) (queueController, error)
	// registerControl attaches both workflows and every control activity to the
	// worker polling the control queue.
	registerControl func(temporalWorker, controlStore, activities.GitHubTokenSource)
}

func credentialRepository(store appRepositories) app.CredentialRepository {
	repository, _ := store.(app.CredentialRepository)
	return repository
}

func credentialEncryptor(store appRepositories) app.CredentialEncryptor {
	encryptor, _ := store.(app.CredentialEncryptor)
	return encryptor
}

// controlPlaneStore mirrors sessionStore: the queue loop and control activities
// reach the store through an assertion that fails at startup, rather than at
// the first run the control worker picks up.
func controlPlaneStore(store appRepositories) (controlStore, error) {
	control, ok := store.(controlStore)
	if !ok {
		return nil, fmt.Errorf("store %T does not implement the control plane store", store)
	}
	return control, nil
}

// rootAccountStore is localAccountStore's read-write counterpart: seeding also
// creates, where signing in only reads.
func rootAccountStore(store appRepositories) (bootstrap.Accounts, error) {
	accounts, ok := store.(bootstrap.Accounts)
	if !ok {
		return nil, fmt.Errorf("store %T does not implement bootstrap.Accounts", store)
	}
	return accounts, nil
}

// localAccountStore mirrors sessionStore: the repository is reached through an
// assertion rather than being added to appRepositories, so a deployment that
// never enables local auth is not made to satisfy an interface it does not use.
func localAccountStore(store appRepositories) (authn.LocalAccountStore, error) {
	accounts, ok := store.(authn.LocalAccountStore)
	if !ok {
		return nil, fmt.Errorf("store %T does not implement authn.LocalAccountStore", store)
	}
	return accounts, nil
}

// sessionStore requires the wired store to also satisfy authn.SessionStore.
// A silently swallowed assertion failure here would boot the API clean and
// only surface at the first login callback, as a nil-pointer panic instead of
// a startup error naming the actual defect.
func sessionStore(store appRepositories) (authn.SessionStore, error) {
	sessions, ok := store.(authn.SessionStore)
	if !ok {
		return nil, fmt.Errorf("store %T does not implement authn.SessionStore", store)
	}
	return sessions, nil
}

func main() {
	ctx, stop := shutdownContext()
	err := run(ctx, os.Getenv)
	stop()
	if err != nil {
		writeStartupError(os.Stderr, err)
		os.Exit(1)
	}
}

// shutdownContext returns a context canceled on SIGINT or SIGTERM.
//
// The API is not only an HTTP server: since the control plane moved here it
// also runs the Temporal worker on the control queue and the queue loop. Both
// stop by way of the context this returns -- listenAndServe shuts the server
// down on it, and run's deferred stopControlPlane drains the worker. Handing
// run a context.Background() instead means the process dies on SIGTERM with
// the worker still registered, abandoning in-flight control activities until
// Temporal times them out.
//
// cmd/executor gets the same behavior from worker.Run(InterruptCh()), which is
// what cmd/worker used before the split.
func shutdownContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
}

func writeStartupError(writer io.Writer, err error) {
	log.New(writer, "", log.LstdFlags).Printf("tflive API failed: %v", err)
}

func defaultAPIDependencies() apiDependencies {
	return apiDependencies{
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
		migrateAuthorization: authorization.Migrate,
		newStore: func(pool postgresPool, specs *queue.SpecRegistry, credentialCipher *encryption.Cipher, sessionCipher *encryption.Cipher) (appRepositories, error) {
			pgxPool, ok := pool.(*pgxpool.Pool)
			if !ok {
				return nil, fmt.Errorf("unexpected postgres pool type %T", pool)
			}
			return postgres.NewStore(pgxPool,
				postgres.WithQueueSpecs(specs),
				postgres.WithCredentialCipher(credentialCipher),
				postgres.WithSessionCipher(sessionCipher),
			), nil
		},
		newLogReader: func(cfg config.ArtifactStoreConfig) (app.TemplateRunLogReader, error) {
			store, err := artifacts.NewObjectStore(cfg)
			if err != nil {
				return nil, err
			}
			return artifacts.NewLogStore(store), nil
		},
		newService: func(service app.Service) (*app.Service, error) {
			return app.NewService(service), nil
		},
		newVerifier: func(ctx context.Context, cfg authn.OIDCVerifierConfig) (tokenVerifier, error) {
			return authn.NewOIDCVerifier(ctx, cfg)
		},
		newAuthorization: func(ctx context.Context, pool postgresPool, storeName string) (*authorization.Authorization, error) {
			// The embedded server needs the concrete pool: its writes join the
			// transactions the repositories open on that same pool, which is
			// the whole reason it is embedded rather than dialled.
			concrete, ok := pool.(*pgxpool.Pool)
			if !ok {
				return nil, fmt.Errorf("authorization requires a *pgxpool.Pool, got %T", pool)
			}
			return authorization.New(ctx, concrete, storeName)
		},
		listenAndServe: listenAndServe,
		dialTemporal:   temporal.Dial,
		newWorker: func(temporalClient client.Client, taskQueue string, options temporalworker.Options) temporalWorker {
			return temporalworker.New(temporalClient, taskQueue, options)
		},
		newDispatcher: func(temporalClient client.Client, options temporal.DispatcherOptions) app.WorkflowDispatcher {
			return temporal.NewDispatcher(temporalClient, options)
		},
		newQueueController: func(store controlStore, dispatcher app.WorkflowDispatcher) (queueController, error) {
			registry, err := app.NewQueueRegistry(dispatcher)
			if err != nil {
				return nil, err
			}
			return queue.NewController(store, registry, store, queue.Options{}), nil
		},
		registerControl: registerControl,
	}
}

func registerControl(worker temporalWorker, store controlStore, gitHubTokens activities.GitHubTokenSource) {
	worker.RegisterWorkflowWithOptions(workflows.TemplatePlanWorkflow, workflow.RegisterOptions{
		Name: domain.TemplatePlanWorkflowName,
	})
	worker.RegisterWorkflowWithOptions(workflows.TemplateApplyWorkflow, workflow.RegisterOptions{
		Name: domain.TemplateApplyWorkflowName,
	})
	worker.RegisterWorkflowWithOptions(workflows.TemplateSyncWorkflow, workflow.RegisterOptions{
		Name: domain.TemplateSyncWorkflowName,
	})

	control := activities.NewControlActivities(store, gitHubTokens)
	worker.RegisterActivityWithOptions(control.RecordTemplateRunStatus, activity.RegisterOptions{
		Name: domain.RecordTemplateRunStatusActivityName,
	})
	worker.RegisterActivityWithOptions(control.RecordTemplateRunStep, activity.RegisterOptions{
		Name: domain.RecordTemplateRunStepActivityName,
	})
	worker.RegisterActivityWithOptions(control.RecordTemplateRunEvent, activity.RegisterOptions{
		Name: domain.RecordTemplateRunEventActivityName,
	})
	worker.RegisterActivityWithOptions(control.RecordTemplateRunLog, activity.RegisterOptions{
		Name: domain.RecordTemplateRunLogActivityName,
	})
	worker.RegisterActivityWithOptions(control.SealRunCredentials, activity.RegisterOptions{
		Name: domain.SealRunCredentialsActivityName,
	})
	worker.RegisterActivityWithOptions(control.SealSourceToken, activity.RegisterOptions{
		Name: domain.SealSourceTokenActivityName,
	})
	worker.RegisterActivityWithOptions(control.SealPlanKey, activity.RegisterOptions{
		Name: domain.SealPlanKeyActivityName,
	})
	worker.RegisterActivityWithOptions(control.FinishPlan, activity.RegisterOptions{
		Name: domain.FinishPlanActivityName,
	})
	worker.RegisterActivityWithOptions(control.BeginApply, activity.RegisterOptions{
		Name: domain.BeginApplyActivityName,
	})

	sync := activities.NewTemplateSyncActivities(store, activities.WithTemplateSyncTokenSource(gitHubTokens))
	worker.RegisterActivityWithOptions(sync.RecordTemplateRegistrationStatus, activity.RegisterOptions{
		Name: domain.RecordTemplateRegistrationStatusActivityName,
	})
	worker.RegisterActivityWithOptions(sync.RecordTemplateRegistrationStep, activity.RegisterOptions{
		Name: domain.RecordTemplateRegistrationStepActivityName,
	})
	worker.RegisterActivityWithOptions(sync.SyncTemplate, activity.RegisterOptions{
		Name: domain.SyncTemplateActivityName,
	})
}

func run(ctx context.Context, getenv func(string) string) error {
	return runWithDependencies(ctx, getenv, defaultAPIDependencies())
}

func runWithDependencies(ctx context.Context, getenv func(string) string, deps apiDependencies) error {
	cfg, err := config.LoadAPIConfig(getenv)
	if err != nil {
		return fmt.Errorf("load api config: %w", err)
	}

	sessionSealer, err := encryption.NewCipher(cfg.Security.SessionEncryptionKey.Value())
	if err != nil {
		return fmt.Errorf("create session sealer: %w", err)
	}
	publicURL := strings.TrimRight(cfg.Security.PublicURL.String(), "/")

	// The OIDC half is built only when a provider is configured. Constructing a
	// verifier reaches for a discovery document, so doing it unconditionally
	// made an IdP-less deployment fail at boot rather than run on local
	// accounts alone — which is the deployment #211 exists to allow. Both
	// remain nil in that case, and api.WithAuth registers no OIDC routes.
	var verifier tokenVerifier
	var logoutTokenVerifier api.LogoutTokenVerifier
	var flow api.AuthFlow
	if cfg.Security.OIDC.IssuerURL != nil {
		oidcVerifier, err := deps.newVerifier(ctx, authn.OIDCVerifierConfig{
			IssuerURL: cfg.Security.OIDC.IssuerURL,
			Audience:  cfg.Security.OIDC.ClientID,
		})
		if err != nil {
			return fmt.Errorf("create token verifier: %w", err)
		}
		defer oidcVerifier.Close(context.WithoutCancel(ctx))
		verifier, logoutTokenVerifier = oidcVerifier, oidcVerifier

		oidcFlow, err := authn.NewFlow(authn.FlowConfig{
			ClientID:     cfg.Security.OIDC.ClientID,
			ClientSecret: cfg.Security.OIDC.ClientSecret.Value(),
			RedirectURI:  publicURL + "/v1/auth/callback",
			Endpoints:    oidcVerifier,
			HTTPClient:   &http.Client{Timeout: 10 * time.Second},
		})
		if err != nil {
			return fmt.Errorf("create oidc flow: %w", err)
		}
		flow = oidcFlow
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

	// OpenFGA's schema is versioned by OpenFGA, so it is migrated separately
	// from the application's and from its own DSN rather than the pool.
	if err := deps.migrateAuthorization(cfg.DatabaseURL); err != nil {
		return fmt.Errorf("migrate authorization: %w", err)
	}

	// After both migrations, because the embedded server resolves its store and
	// model against the schema they create. Before the service, because every
	// authorization answer comes from it, and before SeedRoot, which writes
	// root's tuple.
	auth, err := deps.newAuthorization(ctx, pool, cfg.Security.OpenFGA.StoreName)
	if err != nil {
		return fmt.Errorf("start authorization: %w", err)
	}
	defer auth.Close()

	specs, err := queue.NewSpecRegistry(app.QueueSpecs()...)
	if err != nil {
		return fmt.Errorf("build queue specs: %w", err)
	}

	var credentialCipher *encryption.Cipher
	if !cfg.CredentialEncryptionKey.Empty() {
		credentialCipher, err = encryption.NewCipher(cfg.CredentialEncryptionKey.Value())
		if err != nil {
			return fmt.Errorf("create credential cipher: %w", err)
		}
	}
	store, err := deps.newStore(pool, specs, credentialCipher, sessionSealer)
	if err != nil {
		return fmt.Errorf("wire service: %w", err)
	}

	logReader, err := deps.newLogReader(cfg.ArtifactStore)
	if err != nil {
		return fmt.Errorf("wire log reader: %w", err)
	}
	service, err := deps.newService(app.Service{
		Authorization:            auth,
		Work:                     store,
		Stacks:                   store,
		StackTemplates:           store,
		Credentials:              credentialRepository(store),
		CredentialEncryptor:      credentialEncryptor(store),
		StackTemplateInstaller:   store,
		TemplateRuns:             store,
		TemplateRegistrations:    store,
		TemplateRevisionMetadata: store,
		TemplateRevisions:        store,
		TemplateRunLogs:          logReader,
		TemplateRunLogMetadata:   store,
		Audit:                    store,
		Users:                    store,
	})
	if err != nil {
		return fmt.Errorf("wire service: %w", err)
	}

	sessions, err := sessionStore(store)
	if err != nil {
		return fmt.Errorf("wire session store: %w", err)
	}

	// Always wired. Root is a local account that is seeded at every boot and
	// cannot be locked out (#212), so a deployment where the password route is
	// missing is one where the highest-privileged identity in the model exists
	// in the table and cannot sign in — the "no reachable administrator" state
	// #212 refuses to start in, reached through configuration.
	//
	// Failing here is therefore fail-closed rather than pedantic: without this
	// store there is no way into a fresh install at all.
	accounts, err := localAccountStore(store)
	if err != nil {
		return fmt.Errorf("wire local account store: %w", err)
	}
	localAuthenticator := authn.NewLocalAuthenticator(accounts)

	// Before serving, not alongside it. A fresh install has zero
	// administrators and granting admin requires already being one, so until
	// this has run there is no way to administer anything — and the failure is
	// silent, because every route simply answers 403.
	rootAccounts, err := rootAccountStore(store)
	if err != nil {
		return fmt.Errorf("wire root account store: %w", err)
	}
	if err := bootstrap.SeedRoot(ctx, rootAccounts, auth, bootstrap.RootConfig{
		Username: cfg.Security.Root.Username,
		Password: cfg.Security.Root.Password.Value(),
	}, time.Now); err != nil {
		return fmt.Errorf("seed root account: %w", err)
	}

	handler := api.NewAuthenticatedServer(service, cfg.Security.TenantID, cfg.Debug,
		api.WithQueueReader(store),
		api.WithAuth(api.AuthConfig{
			Flow:                flow,
			Verifier:            verifier,
			LocalAuthenticator:  localAuthenticator,
			LogoutTokenVerifier: logoutTokenVerifier,
			Sealer:              sessionSealer,
			PublicURL:           publicURL,
			SecureCookies:       cfg.Security.Mode == config.RuntimeProduction,
			Sessions:            sessions,
			SessionAbsoluteTTL:  cfg.Security.SessionAbsoluteTTL,
			SessionIdleTTL:      cfg.Security.SessionIdleTTL,
		}),
	)
	// Expired rows are swept alongside serving rather than by a separate job:
	// the API is the only process that writes sessions, and a row nobody
	// deletes keeps an encrypted ID token forever. It stops with the server,
	// and a sweep in flight when ctx is cancelled fails harmlessly.
	reaperCtx, stopReaper := context.WithCancel(ctx)
	defer stopReaper()
	go authn.ReapSessions(reaperCtx, sessions, authn.DefaultSessionReapInterval, nil)

	stopControlPlane, err := startControlPlane(ctx, cfg, deps, store)
	if err != nil {
		return err
	}
	defer stopControlPlane()

	if err := deps.listenAndServe(ctx, cfg.HTTPAddress, handler); err != nil {
		return fmt.Errorf("listen and serve api: %w", err)
	}

	return nil
}

// startControlPlane runs the half of the control plane that is not HTTP: the
// Temporal worker on the control queue, which executes both workflows and the
// activities that write product state, and the queue loop, which turns
// committed intents into workflow starts and signals.
//
// It lives in the API because every piece of it needs the database and the
// keys, and the process that runs tenant Terraform must hold neither. The
// returned function stops the queue loop, then the worker, then closes the
// Temporal client.
func startControlPlane(ctx context.Context, cfg config.APIConfig, deps apiDependencies, store appRepositories) (func(), error) {
	control, err := controlPlaneStore(store)
	if err != nil {
		return nil, fmt.Errorf("wire control plane store: %w", err)
	}

	// A nil interface, not a typed nil pointer, when no App is configured.
	var gitHubTokens activities.GitHubTokenSource
	if cfg.GitHubApp.Enabled() {
		privateKey, err := githubapp.ParsePrivateKey(cfg.GitHubApp.PrivateKey.Value())
		if err != nil {
			// Unreachable in practice: LoadAPIConfig parses the same key.
			return nil, fmt.Errorf("parse github app private key: %w", err)
		}
		gitHubTokens = githubapp.NewTokenSource(githubapp.NewClient(cfg.GitHubApp.AppID, privateKey))
	}

	temporalClient, err := deps.dialTemporal(ctx, temporal.Config{
		Address:   cfg.TemporalAddress,
		Namespace: cfg.TemporalNamespace,
	})
	if err != nil {
		return nil, fmt.Errorf("dial temporal: %w", err)
	}

	// TODO: no session worker here?
	worker := deps.newWorker(temporalClient, domain.ControlTaskQueue, temporalworker.Options{})
	deps.registerControl(worker, control, gitHubTokens)
	controller, err := deps.newQueueController(control, deps.newDispatcher(temporalClient, temporal.DispatcherOptions{
		TerraformTimeout: cfg.TerraformTimeout,
	}))
	if err != nil {
		temporalClient.Close()
		return nil, fmt.Errorf("build queue controller: %w", err)
	}
	if err := worker.Start(); err != nil {
		temporalClient.Close()
		return nil, fmt.Errorf("start control worker: %w", err)
	}

	controllerCtx, cancelController := context.WithCancel(ctx)
	controllerDone := make(chan struct{})
	go func() {
		defer close(controllerDone)
		controller.Run(controllerCtx)
	}()

	return func() {
		cancelController()
		<-controllerDone
		worker.Stop()
		temporalClient.Close()
	}, nil
}

func listenAndServe(ctx context.Context, address string, handler http.Handler) error {
	var listenConfig net.ListenConfig
	listener, err := listenConfig.Listen(ctx, "tcp", address)
	if err != nil {
		return err
	}
	log.Printf("api listening on %s", listener.Addr().String())

	server := &http.Server{
		Handler: handler,
		// Bounds how long a client may take to send its headers, so slow
		// clients cannot hold connections open indefinitely.
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		<-ctx.Done()
		// ctx is already done here; shutdown needs a live context of its own.
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	err = server.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
