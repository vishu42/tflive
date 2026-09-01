package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vishu42/tflive/internal/app"
	"github.com/vishu42/tflive/internal/authn"
	"github.com/vishu42/tflive/internal/authz"
	"github.com/vishu42/tflive/internal/config"
	"github.com/vishu42/tflive/internal/openfga"
	"github.com/vishu42/tflive/internal/queue"
	"github.com/vishu42/tflive/internal/secrets"
	"github.com/vishu42/tflive/internal/traits"
)

func TestRunRequiresDatabaseURL(t *testing.T) {
	t.Parallel()

	values := apiTestValues()
	delete(values, "DATABASE_URL")
	err := run(context.Background(), apiTestGetenv(values))
	if !errors.Is(err, config.ErrInvalidConfig) || err == nil || !strings.Contains(err.Error(), "DATABASE_URL is required") {
		t.Fatalf("error = %v, want DATABASE_URL ErrInvalidConfig", err)
	}
}

func TestRunRequiresTemporalAddress(t *testing.T) {
	t.Parallel()

	values := apiTestValues()
	delete(values, "TEMPORAL_ADDRESS")
	err := run(context.Background(), apiTestGetenv(values))
	if !errors.Is(err, config.ErrInvalidConfig) || err == nil || !strings.Contains(err.Error(), "TEMPORAL_ADDRESS is required") {
		t.Fatalf("error = %v, want TEMPORAL_ADDRESS ErrInvalidConfig", err)
	}
}

func TestRunRejectsSecurityConfigBeforeDependencies(t *testing.T) {
	t.Parallel()

	values := apiTestValues()
	delete(values, "TFLIVE_TENANT_ID")
	postgresCalled := false
	deps := apiDependencies{
		newPostgresPool: func(context.Context, string) (postgresPool, error) {
			postgresCalled = true
			return nil, nil
		},
	}

	err := runWithDependencies(context.Background(), apiTestGetenv(values), deps)
	if !errors.Is(err, config.ErrInvalidConfig) || err == nil || !strings.Contains(err.Error(), "TFLIVE_TENANT_ID is required") {
		t.Fatalf("error = %v, want tenant ErrInvalidConfig", err)
	}
	if postgresCalled {
		t.Fatal("Postgres initialization ran after invalid security configuration")
	}
}

func TestWriteStartupErrorDoesNotLeakSecuritySecrets(t *testing.T) {
	t.Parallel()

	values := apiTestValues()
	values["TFLIVE_ENVIRONMENT"] = "production"
	values["OIDC_ISSUER_URL"] = "https://client:oidc-client-secret-sentinel@id.example.com/realms/tflive"
	values["OPENFGA_API_URL"] = "https://openfga.example.com"
	values["OPENFGA_API_TOKEN"] = "openfga-api-token-sentinel"
	values["KEYCLOAK_BOOTSTRAP_ADMIN_PASSWORD"] = "bootstrap-password-sentinel"

	err := runWithDependencies(context.Background(), apiTestGetenv(values), apiDependencies{})
	if !errors.Is(err, config.ErrInvalidConfig) {
		t.Fatalf("error = %v, want ErrInvalidConfig", err)
	}
	var output bytes.Buffer
	writeStartupError(&output, err)
	for _, secret := range []string{
		"oidc-client-secret-sentinel",
		"openfga-api-token-sentinel",
		"bootstrap-password-sentinel",
	} {
		if strings.Contains(output.String(), secret) {
			t.Fatalf("startup log leaked %q: %s", secret, output.String())
		}
	}
	if !strings.Contains(output.String(), "OIDC_ISSUER_URL must not include user information") {
		t.Fatalf("startup log = %q", output.String())
	}
}

func TestRunWiresProducerOnlyQueueStore(t *testing.T) {
	t.Parallel()

	deps := newRecordingAPIDependencies(t)
	if err := runWithDependencies(context.Background(), apiTestEnv, deps.apiDependencies); err != nil {
		t.Fatalf("runWithDependencies returned error: %v", err)
	}

	if deps.pool.databaseURL != "postgres://user:pass@localhost:5432/db?sslmode=disable" {
		t.Fatalf("databaseURL = %q", deps.pool.databaseURL)
	}
	if !deps.pool.pinged {
		t.Fatal("postgres pool was not pinged")
	}
	if !deps.migrated {
		t.Fatal("postgres migrations did not run")
	}
	assertAPIQueueSpecs(t, deps.queueSpecs)
	if deps.service.Authorizer != deps.authorizer {
		t.Fatal("service Authorizer is not the configured OpenFGA adapter")
	}
	if deps.openFGAConfig.APIURL.String() != "http://localhost:8080" || deps.openFGAConfig.StoreID != "store-id" || deps.openFGAConfig.ModelID != "model-id" || deps.openFGAConfig.HTTPTimeout != 10*time.Second {
		t.Fatalf("OpenFGA config = %#v", deps.openFGAConfig)
	}
	if deps.service.Stacks != deps.store {
		t.Fatal("service Stacks is not the store")
	}
	if deps.service.StackTemplates != deps.store {
		t.Fatal("service StackTemplates is not the store")
	}
	if deps.service.StackTemplateInstaller != deps.store {
		t.Fatal("service StackTemplateInstaller is not the store")
	}
	if deps.service.TemplateRuns != deps.store {
		t.Fatal("service TemplateRuns is not the store")
	}
	if deps.service.TemplateRegistrations != deps.store {
		t.Fatal("service TemplateRegistrations is not the store")
	}
	if deps.service.TemplateRevisionMetadata != deps.store {
		t.Fatal("service TemplateRevisionMetadata is not the store")
	}
	if deps.service.TemplateRevisions != deps.store {
		t.Fatal("service TemplateRevisions is not the store")
	}
	if deps.service.TemplateRunLogMetadata != deps.store {
		t.Fatal("service TemplateRunLogMetadata is not the store")
	}
	if deps.artifactStoreConfig.Kind != config.ArtifactStoreFilesystem {
		t.Fatalf("artifact store kind = %q, want filesystem", deps.artifactStoreConfig.Kind)
	}
	if deps.artifactStoreConfig.FilesystemRoot != "/var/lib/tflive/artifacts" {
		t.Fatalf("artifact store root = %q, want /var/lib/tflive/artifacts", deps.artifactStoreConfig.FilesystemRoot)
	}
	if deps.service.TemplateRunLogs != deps.logReader {
		t.Fatal("service TemplateRunLogs is not the configured log reader")
	}
	if deps.serverAddress != ":9090" {
		t.Fatalf("server address = %q, want :9090", deps.serverAddress)
	}
	if deps.serverHandler == nil {
		t.Fatal("server handler was not provided")
	}
	if !deps.pool.closed {
		t.Fatal("postgres pool was not closed")
	}
}

func assertAPIQueueSpecs(t *testing.T, registry *queue.SpecRegistry) {
	t.Helper()
	if registry == nil {
		t.Fatal("queue spec registry was not passed to the API store")
	}
	for _, kind := range []queue.Kind{
		app.KindStartTemplateRun,
		app.KindStartTemplateSync,
		app.KindSignalRunApproval,
		app.KindSignalRunCancellation,
		app.KindGrantStackOwner,
		app.KindMarkStackReady,
		authz.StackGrantSpec.Kind,
	} {
		if _, ok := registry.Spec(kind); !ok {
			t.Fatalf("queue spec registry missing %q", kind)
		}
	}
}

func TestRunWiresConfiguredTenantBoundary(t *testing.T) {
	t.Parallel()

	values := apiTestValues()
	values["TFLIVE_TENANT_ID"] = "tenant_configured"
	deps := newRecordingAPIDependencies(t)
	if err := runWithDependencies(context.Background(), apiTestGetenv(values), deps.apiDependencies); err != nil {
		t.Fatalf("runWithDependencies returned error: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/v1/tenants/tenant_other/stacks", nil)
	request.AddCookie(&http.Cookie{Name: authn.SessionCookieName, Value: recordingSessionRaw})
	response := httptest.NewRecorder()
	deps.serverHandler.ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusNotFound, response.Body.String())
	}
	var body struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Error != "not_found" || body.Message != "resource not found" {
		t.Fatalf("body = %#v", body)
	}
}

// TestRunGatesSecureCookiesOnRuntimeMode asserts the one cookie attribute
// whose regression is invisible in dev and serious in production: a session
// cookie sent over plaintext. AuthConfig.SecureCookies is unexported outside
// internal/api, so this observes it the way a browser would — through the
// Secure flag on the Set-Cookie the login route actually issues — rather than
// reaching into the server's internals.
func TestRunGatesSecureCookiesOnRuntimeMode(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name       string
		production bool
	}{
		{name: "development", production: false},
		{name: "production", production: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			values := apiTestValues()
			if test.production {
				values["TFLIVE_ENVIRONMENT"] = "production"
				values["OIDC_ISSUER_URL"] = "https://id.example.com/realms/tflive"
				values["TFLIVE_PUBLIC_URL"] = "https://app.example.com"
				values["OPENFGA_API_URL"] = "https://openfga.example.com"
				values["OPENFGA_API_TOKEN"] = "openfga-token"
			}

			deps := newRecordingAPIDependencies(t)
			if err := runWithDependencies(context.Background(), apiTestGetenv(values), deps.apiDependencies); err != nil {
				t.Fatalf("runWithDependencies returned error: %v", err)
			}

			request := httptest.NewRequest(http.MethodGet, "/v1/auth/login", nil)
			response := httptest.NewRecorder()
			deps.serverHandler.ServeHTTP(response, request)

			var transactionCookie *http.Cookie
			for _, cookie := range response.Result().Cookies() {
				if cookie.Name == authn.TransactionCookieName {
					transactionCookie = cookie
				}
			}
			if transactionCookie == nil {
				t.Fatalf("no %s cookie in response; headers = %v", authn.TransactionCookieName, response.Header())
			}
			if transactionCookie.Secure != test.production {
				t.Fatalf("transaction cookie Secure = %v, want %v for production=%v", transactionCookie.Secure, test.production, test.production)
			}
		})
	}
}

func TestRunConstructsAndClosesOIDCVerifier(t *testing.T) {
	deps := newRecordingAPIDependencies(t)
	verifier := &recordingTokenVerifier{}
	var got authn.OIDCVerifierConfig
	deps.newVerifier = func(_ context.Context, cfg authn.OIDCVerifierConfig) (tokenVerifier, error) {
		got = cfg
		return verifier, nil
	}

	if err := runWithDependencies(context.Background(), apiTestEnv, deps.apiDependencies); err != nil {
		t.Fatalf("runWithDependencies() error = %v", err)
	}
	if got.IssuerURL == nil || got.IssuerURL.String() != apiTestEnv("OIDC_ISSUER_URL") || got.Audience != apiTestEnv("OIDC_CLIENT_ID") {
		t.Fatalf("OIDC verifier config = %#v", got)
	}
	if !verifier.closed {
		t.Fatal("verifier was not closed")
	}
}

func TestRunWrapsWireServiceFailure(t *testing.T) {
	t.Parallel()

	wireErr := errors.New("wire failed")
	deps := newRecordingAPIDependencies(t)
	deps.serviceErr = wireErr

	err := runWithDependencies(context.Background(), apiTestEnv, deps.apiDependencies)
	if !errors.Is(err, wireErr) {
		t.Fatalf("error = %v, want wireErr", err)
	}
	if !strings.Contains(err.Error(), "wire service") {
		t.Fatalf("error = %q, want wire service", err)
	}
}

// runWithDependencies serves until its context is cancelled, so this test has
// to be the thing that stops it. Passing context.Background() meant success had
// no exit: the only ways out were an early error, or the go test deadline ten
// minutes later. Which one you got depended on whether something already held
// the configured port — a fast "address already in use" when the local stack
// was up, a ten-minute hang when it wasn't.
//
// Not parallel, because it redirects the global logger. Bound to port 0 so the
// result no longer depends on what else is running on the machine.
func TestRunMigratesRealPostgresWhenDSNIsSet(t *testing.T) {
	dsn := os.Getenv("tflive_POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("tflive_POSTGRES_TEST_DSN is not set")
	}

	logs := newStartupLogBuffer()
	previousOutput := log.Writer()
	log.SetOutput(logs)
	defer log.SetOutput(previousOutput)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	values := apiTestValues()
	values["DATABASE_URL"] = dsn
	values["HTTP_ADDRESS"] = "127.0.0.1:0"

	errCh := make(chan error, 1)
	go func() {
		errCh <- runWithDependencies(ctx, apiTestGetenv(values), defaultAPIDependencies())
	}()

	// Reaching the listening log means the migration ran and the service wired
	// up, which is everything this test is about.
	select {
	case <-logs.started:
		cancel()
		if err := <-errCh; err != nil {
			t.Fatalf("runWithDependencies returned error: %v", err)
		}
	case err := <-errCh:
		t.Fatalf("runWithDependencies returned before serving: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatalf("timed out waiting for the api to start; logs: %s", logs.String())
	}
}

func TestListenAndServeLogsAfterStarting(t *testing.T) {
	logs := newStartupLogBuffer()
	previousOutput := log.Writer()
	log.SetOutput(logs)
	defer log.SetOutput(previousOutput)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- listenAndServe(ctx, "127.0.0.1:0", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	}()

	select {
	case <-logs.started:
		cancel()
		if err := <-errCh; err != nil {
			t.Fatalf("listenAndServe returned error: %v", err)
		}
	case err := <-errCh:
		if errors.Is(err, os.ErrPermission) {
			t.Skipf("local tcp listen is not permitted: %v", err)
		}
		t.Fatalf("listenAndServe returned before logging: %v", err)
	case <-time.After(time.Second):
		t.Fatalf("log output = %q, want api listening line", logs.String())
	}
}

type startupLogBuffer struct {
	mu      sync.Mutex
	logs    bytes.Buffer
	once    sync.Once
	started chan struct{}
}

func newStartupLogBuffer() *startupLogBuffer {
	return &startupLogBuffer{started: make(chan struct{})}
}

func (buffer *startupLogBuffer) Write(p []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()

	n, err := buffer.logs.Write(p)
	if strings.Contains(string(p), "api listening on") {
		buffer.once.Do(func() {
			close(buffer.started)
		})
	}
	return n, err
}

func (buffer *startupLogBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.logs.String()
}

func apiTestValues() map[string]string {
	return map[string]string{
		"DATABASE_URL":                   "postgres://user:pass@localhost:5432/db?sslmode=disable",
		"HTTP_ADDRESS":                   ":9090",
		"TEMPORAL_ADDRESS":               "localhost:7233",
		"TEMPORAL_NAMESPACE":             "tflive",
		"TEMPORAL_TASK_QUEUE":            "terraform-runs-dev",
		"WORKER_RUN_ROOT":                "/var/lib/tflive/runs",
		"ARTIFACT_STORE_KIND":            "filesystem",
		"ARTIFACT_STORE_FILESYSTEM_ROOT": "/var/lib/tflive/artifacts",
		"TFLIVE_ENVIRONMENT":             "development",
		"TFLIVE_TENANT_ID":               "tenant_123",
		"TFLIVE_PUBLIC_URL":              "http://localhost:5173",
		"OIDC_ISSUER_URL":                "http://localhost:8082/realms/tflive",
		"OIDC_CLIENT_ID":                 "tflive-api",
		"OIDC_CLIENT_SECRET":             "oidc-client-secret",
		"SESSION_ENCRYPTION_KEY":         "01234567890123456789012345678901",
		"OPENFGA_API_URL":                "http://localhost:8080",
		"OPENFGA_STORE_ID":               "store-id",
		"OPENFGA_MODEL_ID":               "model-id",
		"OPENFGA_API_TOKEN":              "",
		"OPENFGA_HTTP_TIMEOUT":           "10s",
	}
}

func apiTestEnv(name string) string {
	return apiTestValues()[name]
}

func apiTestGetenv(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}

type recordingAPIDependencies struct {
	apiDependencies
	pool                *recordingPostgresPool
	store               *recordingStore
	queueSpecs          *queue.SpecRegistry
	credentialCipher    *secrets.Cipher
	sessionCipher       *secrets.Cipher
	artifactStoreConfig config.ArtifactStoreConfig
	logReader           recordingTemplateRunLogReader
	service             app.Service
	serverAddress       string
	serverHandler       http.Handler
	migrated            bool
	serviceErr          error
	serverErr           error
	openFGAConfig       openfga.Config
	authorizer          authz.Authorizer
}

func newRecordingAPIDependencies(t *testing.T) *recordingAPIDependencies {
	t.Helper()

	deps := &recordingAPIDependencies{
		pool:  &recordingPostgresPool{},
		store: &recordingStore{},
	}
	deps.apiDependencies = apiDependencies{
		newPostgresPool: func(_ context.Context, databaseURL string) (postgresPool, error) {
			deps.pool.databaseURL = databaseURL
			return deps.pool, nil
		},
		migratePostgres: func(_ context.Context, pool postgresPool) error {
			if pool != deps.pool {
				t.Fatalf("migratePostgres pool = %p, want %p", pool, deps.pool)
			}
			deps.migrated = true
			return nil
		},
		newStore: func(pool postgresPool, specs *queue.SpecRegistry, credentialCipher *secrets.Cipher, sessionCipher *secrets.Cipher) (appRepositories, error) {
			if pool != deps.pool {
				t.Fatalf("newStore pool = %p, want %p", pool, deps.pool)
			}
			deps.queueSpecs = specs
			deps.credentialCipher = credentialCipher
			deps.sessionCipher = sessionCipher
			return deps.store, nil
		},
		newLogReader: func(cfg config.ArtifactStoreConfig) (app.TemplateRunLogReader, error) {
			deps.artifactStoreConfig = cfg
			return deps.logReader, nil
		},
		newService: func(service app.Service) (*app.Service, error) {
			deps.service = service
			if deps.serviceErr != nil {
				return nil, deps.serviceErr
			}
			return app.NewService(service), nil
		},
		newVerifier: func(context.Context, authn.OIDCVerifierConfig) (tokenVerifier, error) {
			return testTokenVerifier{}, nil
		},
		newAuthorizer: func(cfg openfga.Config) (authz.Authorizer, error) {
			deps.openFGAConfig = cfg
			return deps.authorizer, nil
		},
		listenAndServe: func(_ context.Context, address string, handler http.Handler) error {
			deps.serverAddress = address
			deps.serverHandler = handler
			return deps.serverErr
		},
	}
	deps.authorizer = testAuthorizer{}
	return deps
}

type testTokenVerifier struct{}

type testAuthorizer struct{}

func (testAuthorizer) Check(context.Context, authz.CheckRequest) (authz.CheckResult, error) {
	return authz.CheckResult{}, nil
}
func (testAuthorizer) BatchCheck(context.Context, authz.BatchCheckRequest) (authz.BatchCheckResult, error) {
	return authz.BatchCheckResult{}, nil
}
func (testAuthorizer) ListGrants(context.Context, authz.ListGrantsRequest) (authz.ListGrantsResult, error) {
	return authz.ListGrantsResult{}, nil
}
func (testAuthorizer) WriteRelationships(context.Context, authz.Mutation) error  { return nil }
func (testAuthorizer) DeleteRelationships(context.Context, authz.Mutation) error { return nil }

func (testTokenVerifier) Verify(context.Context, string) (authn.VerifiedToken, error) {
	return authn.VerifiedToken{Subject: "test-user"}, nil
}

func (testTokenVerifier) Close(context.Context) error { return nil }

func (testTokenVerifier) VerifyLogoutToken(context.Context, string) (authn.LogoutToken, error) {
	return authn.LogoutToken{Subject: "test-user"}, nil
}

func (testTokenVerifier) Endpoints() authn.Endpoints {
	return authn.Endpoints{Authorization: "https://idp.test/authorize", Token: "https://idp.test/token"}
}

type recordingTokenVerifier struct {
	closed bool
}

func (verifier *recordingTokenVerifier) Verify(context.Context, string) (authn.VerifiedToken, error) {
	return authn.VerifiedToken{Subject: "test-user"}, nil
}

func (verifier *recordingTokenVerifier) Close(context.Context) error {
	verifier.closed = true
	return nil
}

func (*recordingTokenVerifier) VerifyLogoutToken(context.Context, string) (authn.LogoutToken, error) {
	return authn.LogoutToken{Subject: "test-user"}, nil
}

func (*recordingTokenVerifier) Endpoints() authn.Endpoints {
	return authn.Endpoints{Authorization: "https://idp.test/authorize", Token: "https://idp.test/token"}
}

type recordingPostgresPool struct {
	databaseURL string
	pinged      bool
	closed      bool
}

func (pool *recordingPostgresPool) Ping(context.Context) error {
	pool.pinged = true
	return nil
}

func (pool *recordingPostgresPool) Close() {
	pool.closed = true
}

type recordingStore struct{}

func (recordingStore) CreateStack(context.Context, traits.Stack) error {
	return nil
}

func (recordingStore) GetStack(context.Context, traits.TenantID, traits.StackID) (traits.Stack, error) {
	return traits.Stack{}, nil
}

func (recordingStore) ListStacks(context.Context, traits.TenantID) ([]traits.Stack, error) {
	return nil, nil
}

func (recordingStore) ListStacksPage(context.Context, traits.TenantID, *app.StackPageCursor, int) ([]traits.Stack, error) {
	return nil, nil
}

func (recordingStore) GetStackWithTemplates(context.Context, traits.TenantID, traits.StackID) (app.StackView, error) {
	return app.StackView{}, nil
}

func (recordingStore) GetStackTemplate(context.Context, traits.TenantID, traits.StackTemplateID) (traits.StackTemplate, error) {
	return traits.StackTemplate{}, nil
}

func (recordingStore) UpdateStackTemplateConfig(context.Context, traits.TenantID, traits.StackTemplateID, json.RawMessage) (traits.StackTemplate, error) {
	return traits.StackTemplate{}, nil
}

func (recordingStore) UpdateStackTemplateDesiredRevision(context.Context, traits.TenantID, traits.StackTemplateID, traits.TemplateRevisionID, json.RawMessage) (traits.StackTemplate, error) {
	return traits.StackTemplate{}, nil
}

func (recordingStore) CreateStackTemplate(context.Context, traits.StackTemplate) error {
	return nil
}

func (recordingStore) GetTemplateRevision(context.Context, traits.TenantID, traits.TemplateRevisionID) (traits.TemplateRevision, error) {
	return traits.TemplateRevision{}, nil
}

func (recordingStore) ListTemplateRevisions(context.Context, traits.TenantID) ([]traits.TemplateRevision, error) {
	return nil, nil
}

func (recordingStore) CreateTemplateRun(context.Context, traits.TemplateRun) error {
	return nil
}

func (recordingStore) CreateTemplateRegistration(context.Context, traits.TemplateRegistration) error {
	return nil
}

func (recordingStore) ApproveTemplateRun(context.Context, traits.TemplateRunApproval) error {
	return nil
}

func (recordingStore) RequestTemplateRunCancellation(context.Context, traits.TemplateRunCancellation) error {
	return nil
}

func (recordingStore) GetTemplateRun(context.Context, traits.TenantID, traits.TemplateRunID) (traits.TemplateRun, error) {
	return traits.TemplateRun{}, nil
}

func (recordingStore) ListTemplateRuns(context.Context, traits.TenantID, traits.StackTemplateID) ([]traits.TemplateRun, error) {
	return nil, nil
}

func (recordingStore) GetTemplateRunLog(context.Context, traits.TenantID, traits.TemplateRunID, string) (traits.TemplateRunLog, error) {
	return traits.TemplateRunLog{}, nil
}

func (recordingStore) ListTemplateRunLogs(context.Context, traits.TenantID, traits.TemplateRunID) ([]traits.TemplateRunLog, error) {
	return nil, nil
}

func (recordingStore) ReconcileTemplateRunCancellation(context.Context, traits.TenantID, traits.TemplateRunID, string) error {
	return nil
}

func (recordingStore) GetTemplateRegistration(context.Context, traits.TenantID, traits.TemplateRegistrationID) (traits.TemplateRegistration, error) {
	return traits.TemplateRegistration{}, nil
}

func (recordingStore) RecordTemplateRegistrationStatus(context.Context, traits.TemplateRegistrationStatusActivityInput) error {
	return nil
}

func (recordingStore) UpsertTemplateRevisionWithVariables(context.Context, traits.TemplateRevision, []traits.TemplateVariable) (traits.TemplateRevision, error) {
	return traits.TemplateRevision{}, nil
}

func (recordingStore) GetTemplateRevisionVariables(context.Context, traits.TenantID, traits.TemplateRevisionID) ([]traits.TemplateVariable, error) {
	return nil, nil
}

func (recordingStore) AppendAuditEvent(context.Context, traits.SecurityAuditEvent) error {
	return nil
}

// app.UserRepository, the identity projection the OIDC callback writes through.
func (recordingStore) UpsertUser(context.Context, app.UserProfile, time.Time) error {
	return nil
}

func (recordingStore) SearchUsers(context.Context, string, int, int) ([]app.UserProfile, error) {
	return nil, nil
}

func (recordingStore) UsersBySubs(context.Context, []string) (map[string]app.UserProfile, error) {
	return nil, nil
}

// authn.SessionStore, wired so sessionStore's type assertion in main.go
// succeeds against this fake the way it does against *postgres.Store.
func (recordingStore) CreateSession(context.Context, authn.Session) error {
	return nil
}

// recordingSessionRaw is the cookie value SessionByHash below will honour. The
// session cookie is the only credential the middleware accepts, so a test that
// needs to reach a protected route through the fully wired server has to
// present one.
const recordingSessionRaw = "wired-api-session"

func (recordingStore) SessionByHash(_ context.Context, idHash string) (authn.Session, error) {
	if idHash != authn.HashSessionID(recordingSessionRaw) {
		return authn.Session{}, authn.ErrSessionNotFound
	}
	now := time.Now().UTC()
	return authn.Session{
		IDHash:            idHash,
		Subject:           "user-123",
		LastSeenAt:        now,
		AbsoluteExpiresAt: now.Add(time.Hour),
	}, nil
}

func (recordingStore) TouchSession(context.Context, string, time.Time) error {
	return nil
}

func (recordingStore) RevokeSession(context.Context, string, time.Time) error {
	return nil
}

func (recordingStore) RevokeSessionsByIDPSessionID(context.Context, string, time.Time) (int, error) {
	return 0, nil
}

func (recordingStore) RevokeSessionsBySubject(context.Context, string, time.Time) (int, error) {
	return 0, nil
}

func (recordingStore) RevokeSessionsBySubjectWithoutIDPSession(context.Context, string, time.Time) (int, error) {
	return 0, nil
}

func (recordingStore) DeleteSessionsExpiredBefore(context.Context, time.Time) (int, error) {
	return 0, nil
}

type recordingTemplateRunLogReader struct{}

func (recordingTemplateRunLogReader) ReadTemplateRunLog(context.Context, traits.TemplateRunLog) ([]byte, error) {
	return nil, nil
}

// The API store must satisfy the unit-of-work and queue-reader surfaces the
// service and the queue endpoint depend on.
func (store *recordingStore) InTx(ctx context.Context, fn func(app.TxRepo, queue.Enqueuer) error) error {
	return fn(store, store)
}

func (store *recordingStore) Enqueue(context.Context, ...queue.Request) error { return nil }

func (store *recordingStore) ListByActor(context.Context, string, string, int) ([]queue.Status, error) {
	return nil, nil
}
