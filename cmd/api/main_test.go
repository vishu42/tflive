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
	"github.com/vishu42/tflive/internal/config"
	"github.com/vishu42/tflive/internal/temporal"
	"github.com/vishu42/tflive/internal/traits"
	"go.temporal.io/sdk/client"
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

func TestRunWiresTemporalDispatcher(t *testing.T) {
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
	if deps.temporalConfig.Address != "localhost:7233" {
		t.Fatalf("temporal address = %q, want localhost:7233", deps.temporalConfig.Address)
	}
	if deps.temporalConfig.Namespace != "tflive" {
		t.Fatalf("temporal namespace = %q, want tflive", deps.temporalConfig.Namespace)
	}
	if deps.dispatcherTaskQueue != "terraform-runs-dev" {
		t.Fatalf("dispatcher task queue = %q, want terraform-runs-dev", deps.dispatcherTaskQueue)
	}
	if deps.service.Workflows != deps.dispatcher {
		t.Fatal("service Workflows is not the dispatcher")
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
	if !deps.temporalClient.closed {
		t.Fatal("temporal client was not closed")
	}
	if !deps.pool.closed {
		t.Fatal("postgres pool was not closed")
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
	request.Header.Set("Authorization", "Bearer test-token")
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

func TestRunUsesDefaultTemporalTaskQueue(t *testing.T) {
	t.Parallel()

	deps := newRecordingAPIDependencies(t)
	values := apiTestValues()
	delete(values, "TEMPORAL_TASK_QUEUE")
	err := runWithDependencies(context.Background(), apiTestGetenv(values), deps.apiDependencies)
	if err != nil {
		t.Fatalf("runWithDependencies returned error: %v", err)
	}
	if deps.dispatcherTaskQueue != config.DefaultTemporalTaskQueue {
		t.Fatalf("dispatcher task queue = %q, want %q", deps.dispatcherTaskQueue, config.DefaultTemporalTaskQueue)
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
	if got.IssuerURL == nil || got.IssuerURL.String() != apiTestEnv("OIDC_ISSUER_URL") || got.Audience != apiTestEnv("OIDC_AUDIENCE") {
		t.Fatalf("OIDC verifier config = %#v", got)
	}
	if !verifier.closed {
		t.Fatal("verifier was not closed")
	}
}

func TestRunWrapsTemporalDialFailure(t *testing.T) {
	t.Parallel()

	dialErr := errors.New("dial failed")
	deps := newRecordingAPIDependencies(t)
	deps.dialErr = dialErr

	err := runWithDependencies(context.Background(), apiTestEnv, deps.apiDependencies)
	if !errors.Is(err, dialErr) {
		t.Fatalf("error = %v, want dialErr", err)
	}
	if !strings.Contains(err.Error(), "dial temporal") {
		t.Fatalf("error = %q, want dial temporal", err)
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

func TestRunMigratesRealPostgresWhenDSNIsSet(t *testing.T) {
	t.Parallel()

	dsn := os.Getenv("tflive_POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("tflive_POSTGRES_TEST_DSN is not set")
	}

	temporalClient := &recordingTemporalClient{}
	deps := defaultAPIDependencies()
	deps.dialTemporal = func(context.Context, temporal.Config) (client.Client, error) {
		return temporalClient, nil
	}
	deps.newDispatcher = func(client.Client, string) app.WorkflowDispatcher {
		return recordingWorkflowDispatcher{}
	}

	values := apiTestValues()
	values["DATABASE_URL"] = dsn
	err := runWithDependencies(context.Background(), apiTestGetenv(values), deps)
	if err != nil {
		t.Fatalf("runWithDependencies returned error: %v", err)
	}
	if !temporalClient.closed {
		t.Fatal("temporal client was not closed")
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
		"OIDC_ISSUER_URL":                "http://localhost:8082/realms/tflive",
		"OIDC_AUDIENCE":                  "tflive-api",
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
	temporalClient      *recordingTemporalClient
	dispatcher          recordingWorkflowDispatcher
	temporalConfig      temporal.Config
	dispatcherTaskQueue string
	artifactStoreConfig config.ArtifactStoreConfig
	logReader           recordingTemplateRunLogReader
	service             app.Service
	serverAddress       string
	serverHandler       http.Handler
	migrated            bool
	dialErr             error
	serviceErr          error
	serverErr           error
}

func newRecordingAPIDependencies(t *testing.T) *recordingAPIDependencies {
	t.Helper()

	deps := &recordingAPIDependencies{
		pool:           &recordingPostgresPool{},
		store:          &recordingStore{},
		temporalClient: &recordingTemporalClient{},
		dispatcher:     recordingWorkflowDispatcher{},
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
		newStore: func(pool postgresPool) (appRepositories, error) {
			if pool != deps.pool {
				t.Fatalf("newStore pool = %p, want %p", pool, deps.pool)
			}
			return deps.store, nil
		},
		dialTemporal: func(_ context.Context, cfg temporal.Config) (client.Client, error) {
			deps.temporalConfig = cfg
			if deps.dialErr != nil {
				return nil, deps.dialErr
			}
			return deps.temporalClient, nil
		},
		newDispatcher: func(temporalClient client.Client, taskQueue string) app.WorkflowDispatcher {
			if temporalClient != deps.temporalClient {
				t.Fatalf("newDispatcher temporalClient = %p, want %p", temporalClient, deps.temporalClient)
			}
			deps.dispatcherTaskQueue = taskQueue
			return deps.dispatcher
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
		listenAndServe: func(_ context.Context, address string, handler http.Handler) error {
			deps.serverAddress = address
			deps.serverHandler = handler
			return deps.serverErr
		},
	}
	return deps
}

type testTokenVerifier struct{}

func (testTokenVerifier) Verify(context.Context, string) (authn.VerifiedToken, error) {
	return authn.VerifiedToken{Subject: "test-user"}, nil
}

func (testTokenVerifier) Close(context.Context) error { return nil }

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

type recordingTemporalClient struct {
	client.Client
	closed bool
}

func (temporalClient *recordingTemporalClient) Close() {
	temporalClient.closed = true
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

func (recordingStore) GetTemplateRun(context.Context, traits.TenantID, traits.TemplateRunID) (traits.TemplateRun, error) {
	return traits.TemplateRun{}, nil
}

func (recordingStore) GetTemplateRunLog(context.Context, traits.TenantID, traits.TemplateRunID, string) (traits.TemplateRunLog, error) {
	return traits.TemplateRunLog{}, nil
}

func (recordingStore) ListTemplateRunLogs(context.Context, traits.TenantID, traits.TemplateRunID) ([]traits.TemplateRunLog, error) {
	return nil, nil
}

func (recordingStore) ApproveTemplateRun(context.Context, traits.TemplateRunApproval) error {
	return nil
}

func (recordingStore) RequestTemplateRunCancellation(context.Context, traits.TemplateRunCancellation) error {
	return nil
}

func (recordingStore) CreateTemplateRegistration(context.Context, traits.TemplateRegistration) error {
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

type recordingTemplateRunLogReader struct{}

func (recordingTemplateRunLogReader) ReadTemplateRunLog(context.Context, traits.TemplateRunLog) ([]byte, error) {
	return nil, nil
}

type recordingWorkflowDispatcher struct{}

func (recordingWorkflowDispatcher) StartTemplateRun(context.Context, traits.TemplateRunWorkflowInput) error {
	return nil
}

func (recordingWorkflowDispatcher) StartTemplateSync(context.Context, traits.TemplateSyncWorkflowInput) error {
	return nil
}

func (recordingWorkflowDispatcher) ApproveTemplateRun(context.Context, traits.TenantID, traits.TemplateRunID, traits.ApprovalSignal) error {
	return nil
}

func (recordingWorkflowDispatcher) CancelTemplateRun(context.Context, traits.TenantID, traits.TemplateRunID, traits.CancelSignal) error {
	return nil
}
