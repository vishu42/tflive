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
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/vishu42/tflive/internal/api"
	"github.com/vishu42/tflive/internal/app"
	"github.com/vishu42/tflive/internal/artifacts"
	"github.com/vishu42/tflive/internal/authn"
	"github.com/vishu42/tflive/internal/authorizer"
	"github.com/vishu42/tflive/internal/authz"
	"github.com/vishu42/tflive/internal/bootstrap"
	"github.com/vishu42/tflive/internal/config"
	"github.com/vishu42/tflive/internal/openfga"
	"github.com/vishu42/tflive/internal/postgres"
	"github.com/vishu42/tflive/internal/queue"
	"github.com/vishu42/tflive/internal/secrets"
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

type tokenVerifier interface {
	authn.Verifier
	authn.EndpointSource
	VerifyLogoutToken(context.Context, string) (authn.LogoutToken, error)
	Close(context.Context) error
}

type apiDependencies struct {
	newPostgresPool func(context.Context, string) (postgresPool, error)
	migratePostgres func(context.Context, postgresPool) error
	newStore        func(postgresPool, *queue.SpecRegistry, *secrets.Cipher, *secrets.Cipher) (appRepositories, error)
	newLogReader    func(config.ArtifactStoreConfig) (app.TemplateRunLogReader, error)
	newService      func(app.Service) (*app.Service, error)
	newVerifier     func(context.Context, authn.OIDCVerifierConfig) (tokenVerifier, error)
	newAuthorizer   func(openfga.Config) (authz.Authorizer, error)
	listenAndServe  func(context.Context, string, http.Handler) error
}

func credentialRepository(store appRepositories) app.CredentialRepository {
	repository, _ := store.(app.CredentialRepository)
	return repository
}

func credentialEncryptor(store appRepositories) app.CredentialEncryptor {
	encryptor, _ := store.(app.CredentialEncryptor)
	return encryptor
}

// sessionStore requires the wired store to also satisfy authn.SessionStore.
// A silently swallowed assertion failure here would boot the API clean and
// only surface at the first login callback, as a nil-pointer panic instead of
// a startup error naming the actual defect.
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

func sessionStore(store appRepositories) (authn.SessionStore, error) {
	sessions, ok := store.(authn.SessionStore)
	if !ok {
		return nil, fmt.Errorf("store %T does not implement authn.SessionStore", store)
	}
	return sessions, nil
}

func main() {
	if err := run(context.Background(), os.Getenv); err != nil {
		writeStartupError(os.Stderr, err)
		os.Exit(1)
	}
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
		newStore: func(pool postgresPool, specs *queue.SpecRegistry, credentialCipher *secrets.Cipher, sessionCipher *secrets.Cipher) (appRepositories, error) {
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
		newAuthorizer:  func(cfg openfga.Config) (authz.Authorizer, error) { return authorizer.New(cfg) },
		listenAndServe: listenAndServe,
	}
}

func run(ctx context.Context, getenv func(string) string) error {
	return runWithDependencies(ctx, getenv, defaultAPIDependencies())
}

func runWithDependencies(ctx context.Context, getenv func(string) string, deps apiDependencies) error {
	cfg, err := config.LoadAPIConfig(getenv)
	if err != nil {
		return fmt.Errorf("load api config: %w", err)
	}

	sessionSealer, err := secrets.NewCipher(cfg.Security.SessionEncryptionKey.Value())
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
		defer oidcVerifier.Close(context.Background())
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

	authorizer, err := deps.newAuthorizer(openfga.Config{
		APIURL: cfg.Security.OpenFGA.APIURL, StoreID: cfg.Security.OpenFGA.StoreID,
		ModelID: cfg.Security.OpenFGA.ModelID, APIToken: cfg.Security.OpenFGA.APIToken.Value(),
		HTTPTimeout: cfg.Security.OpenFGA.RequestTimeout,
	})
	if err != nil {
		return fmt.Errorf("create authorization adapter: %w", err)
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

	specs, err := queue.NewSpecRegistry(app.QueueSpecs()...)
	if err != nil {
		return fmt.Errorf("build queue specs: %w", err)
	}

	var credentialCipher *secrets.Cipher
	if !cfg.CredentialEncryptionKey.Empty() {
		credentialCipher, err = secrets.NewCipher(cfg.CredentialEncryptionKey.Value())
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
		Authorizer:               authorizer,
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
	if err := bootstrap.SeedRoot(ctx, rootAccounts, authorizer, bootstrap.RootConfig{
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

	if err := deps.listenAndServe(ctx, cfg.HTTPAddress, handler); err != nil {
		return fmt.Errorf("listen and serve api: %w", err)
	}

	return nil
}

func listenAndServe(ctx context.Context, address string, handler http.Handler) error {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	log.Printf("api listening on %s", listener.Addr().String())

	server := &http.Server{
		Handler: handler,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	err = server.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
