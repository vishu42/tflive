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
	"github.com/vishu42/tflive/internal/config"
	"github.com/vishu42/tflive/internal/keycloak"
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

	verifier, err := deps.newVerifier(ctx, authn.OIDCVerifierConfig{
		IssuerURL: cfg.Security.OIDC.IssuerURL,
		Audience:  cfg.Security.OIDC.ClientID,
	})
	if err != nil {
		return fmt.Errorf("create token verifier: %w", err)
	}
	defer verifier.Close(context.Background())

	sessionSealer, err := secrets.NewCipher(cfg.Security.SessionEncryptionKey.Value())
	if err != nil {
		return fmt.Errorf("create session sealer: %w", err)
	}
	publicURL := strings.TrimRight(cfg.Security.PublicURL.String(), "/")
	flow, err := authn.NewFlow(authn.FlowConfig{
		ClientID:     cfg.Security.OIDC.ClientID,
		ClientSecret: cfg.Security.OIDC.ClientSecret.Value(),
		RedirectURI:  publicURL + "/v1/auth/callback",
		Endpoints:    verifier,
		HTTPClient:   &http.Client{Timeout: 10 * time.Second},
	})
	if err != nil {
		return fmt.Errorf("create oidc flow: %w", err)
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
	var directoryClient app.UserDirectory
	if !cfg.Security.DirectoryReaderClientSecret.Empty() {
		client := keycloak.NewDirectoryClient(keycloak.DirectoryClientConfig{
			AdminURL:     cfg.Security.KeycloakAdminURL,
			Realm:        cfg.Security.KeycloakRealm,
			ClientID:     cfg.Security.DirectoryReaderClientID,
			ClientSecret: cfg.Security.DirectoryReaderClientSecret.Value(),
			HTTPTimeout:  cfg.Security.DirectoryReaderHTTPTimeout,
			Debug:        cfg.Debug,
		})
		directoryClient = &directoryClientAdapter{client: client, debug: cfg.Debug}
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
		UserDirectory:            directoryClient,
	})
	if err != nil {
		return fmt.Errorf("wire service: %w", err)
	}

	sessions, err := sessionStore(store)
	if err != nil {
		return fmt.Errorf("wire session store: %w", err)
	}

	handler := api.NewAuthenticatedServer(service, cfg.Security.TenantID, cfg.Debug,
		api.WithQueueReader(store),
		api.WithAuth(api.AuthConfig{
			Flow:                flow,
			Verifier:            verifier,
			LogoutTokenVerifier: verifier,
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

type directoryClientAdapter struct {
	client *keycloak.DirectoryClient
	debug  bool
}

func (a *directoryClientAdapter) SearchUsers(ctx context.Context, query string, first, max int) ([]app.DirectoryUser, error) {
	if a.debug {
		log.Printf("[DEBUG] directoryClientAdapter.SearchUsers query=%q first=%d max=%d", query, first, max)
	}
	if err := a.client.Authenticate(ctx); err != nil {
		if a.debug {
			log.Printf("[DEBUG] directoryClientAdapter.Authenticate failed: %v", err)
		}
		return nil, fmt.Errorf("authenticate directory client: %w", err)
	}
	if a.debug {
		log.Printf("[DEBUG] directoryClientAdapter.Authenticate succeeded")
	}
	users, err := a.client.SearchUsers(ctx, query, first, max)
	if err != nil {
		if a.debug {
			log.Printf("[DEBUG] directoryClientAdapter.SearchUsers failed: %v", err)
		}
		return nil, err
	}
	result := make([]app.DirectoryUser, len(users))
	for i, u := range users {
		result[i] = app.DirectoryUser{
			ID:        u.ID,
			Username:  u.Username,
			Email:     u.Email,
			FirstName: u.FirstName,
			LastName:  u.LastName,
		}
	}
	if a.debug {
		log.Printf("[DEBUG] directoryClientAdapter.SearchUsers returned %d users", len(result))
	}
	return result, nil
}

func (a *directoryClientAdapter) GetUser(ctx context.Context, userID string) (*app.DirectoryUser, error) {
	if a.debug {
		log.Printf("[DEBUG] directoryClientAdapter.GetUser userID=%s", userID)
	}
	if err := a.client.Authenticate(ctx); err != nil {
		if a.debug {
			log.Printf("[DEBUG] directoryClientAdapter.Authenticate failed: %v", err)
		}
		return nil, fmt.Errorf("authenticate directory client: %w", err)
	}
	if a.debug {
		log.Printf("[DEBUG] directoryClientAdapter.Authenticate succeeded")
	}
	user, err := a.client.GetUser(ctx, userID)
	if err != nil {
		if a.debug {
			log.Printf("[DEBUG] directoryClientAdapter.GetUser failed: %v", err)
		}
		return nil, err
	}
	result := &app.DirectoryUser{
		ID:        user.ID,
		Username:  user.Username,
		Email:     user.Email,
		FirstName: user.FirstName,
		LastName:  user.LastName,
	}
	if a.debug {
		log.Printf("[DEBUG] directoryClientAdapter.GetUser returned user id=%s username=%s", result.ID, result.Username)
	}
	return result, nil
}
