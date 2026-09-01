package main

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/vishu42/tflive/internal/activities"
	"github.com/vishu42/tflive/internal/app"
	"github.com/vishu42/tflive/internal/artifacts"
	"github.com/vishu42/tflive/internal/authz"
	"github.com/vishu42/tflive/internal/config"
	"github.com/vishu42/tflive/internal/queue"
	"github.com/vishu42/tflive/internal/secrets"
	"github.com/vishu42/tflive/internal/temporal"
	"github.com/vishu42/tflive/internal/traits"
	"github.com/vishu42/tflive/internal/workflows"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	temporalworker "go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

func TestRunRequiresTemporalAddress(t *testing.T) {
	t.Parallel()

	err := run(context.Background(), func(string) string {
		return ""
	})
	if !errors.Is(err, config.ErrInvalidConfig) {
		t.Fatalf("error = %v, want ErrInvalidConfig", err)
	}
}

func TestRunRequiresDatabaseURL(t *testing.T) {
	t.Parallel()

	err := run(context.Background(), func(key string) string {
		if key == "TEMPORAL_ADDRESS" {
			return "localhost:7233"
		}
		return ""
	})
	if !errors.Is(err, config.ErrInvalidConfig) {
		t.Fatalf("error = %v, want ErrInvalidConfig", err)
	}
}

func TestRunWiresTemporalWorker(t *testing.T) {
	t.Parallel()

	deps := newRecordingWorkerDependencies(t)
	if err := runWithDependencies(context.Background(), workerTestEnv, deps.workerDependencies); err != nil {
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
	if deps.workerTaskQueue != "terraform-runs-dev" {
		t.Fatalf("worker task queue = %q, want terraform-runs-dev", deps.workerTaskQueue)
	}
	if !deps.workerOptions.EnableSessionWorker {
		t.Fatal("session worker was not enabled")
	}
	if deps.worker.registeredWorkflow != reflect.ValueOf(workflows.TemplateRunWorkflow).Pointer() {
		t.Fatal("TemplateRunWorkflow was not registered")
	}
	if deps.worker.registeredWorkflowOptions.Name != traits.TemplateRunWorkflowName {
		t.Fatalf("workflow registration name = %q, want %q", deps.worker.registeredWorkflowOptions.Name, traits.TemplateRunWorkflowName)
	}
	if deps.worker.registeredActivityOptions.Name != traits.RecordTemplateRunStatusActivityName {
		t.Fatalf("activity registration name = %q, want %q", deps.worker.registeredActivityOptions.Name, traits.RecordTemplateRunStatusActivityName)
	}
	if !deps.worker.registeredActivities[traits.PrepareWorkspaceActivityName] {
		t.Fatalf("activity %q was not registered", traits.PrepareWorkspaceActivityName)
	}
	if !deps.worker.registeredActivities[traits.FetchSourceActivityName] {
		t.Fatalf("activity %q was not registered", traits.FetchSourceActivityName)
	}
	if !deps.worker.registeredActivities[traits.RunTerraformActivityName] {
		t.Fatalf("activity %q was not registered", traits.RunTerraformActivityName)
	}
	if !deps.activityStoreIsWired {
		t.Fatal("activity was not wired with the Postgres store")
	}
	if deps.activityRunRoot != "/tmp/tflive-worker-test" {
		t.Fatalf("activity run root = %q, want /tmp/tflive-worker-test", deps.activityRunRoot)
	}
	if deps.artifactStoreConfig.Kind != config.ArtifactStoreFilesystem {
		t.Fatalf("artifact store kind = %q, want filesystem", deps.artifactStoreConfig.Kind)
	}
	if deps.artifactStoreConfig.FilesystemRoot != "/tmp/tflive-worker-artifacts" {
		t.Fatalf("artifact store root = %q, want /tmp/tflive-worker-artifacts", deps.artifactStoreConfig.FilesystemRoot)
	}
	if deps.activityLogStore != deps.logStore {
		t.Fatal("activity log store was not wired")
	}
	if deps.logMetadataRecorder != deps.store {
		t.Fatal("log metadata recorder was not wired with the Postgres store")
	}
	if !deps.worker.ran {
		t.Fatal("worker was not run")
	}
	if !deps.queueController.ran {
		t.Fatal("queue controller was not run")
	}
	if !deps.queueController.stopped {
		t.Fatal("queue controller was not stopped with the worker")
	}
	if !deps.temporalClient.closed {
		t.Fatal("temporal client was not closed")
	}
	if !deps.pool.closed {
		t.Fatal("postgres pool was not closed")
	}
}

func TestRunUsesDefaultTemporalTaskQueue(t *testing.T) {
	t.Parallel()

	deps := newRecordingWorkerDependencies(t)
	err := runWithDependencies(context.Background(), func(key string) string {
		switch key {
		case "DATABASE_URL":
			return "postgres://user:pass@localhost:5432/db?sslmode=disable"
		case "TEMPORAL_ADDRESS":
			return "localhost:7233"
		case "OPENFGA_API_URL":
			return "http://localhost:8080"
		case "OPENFGA_STORE_ID":
			return "store_123"
		case "OPENFGA_MODEL_ID":
			return "model_123"
		default:
			return ""
		}
	}, deps.workerDependencies)
	if err != nil {
		t.Fatalf("runWithDependencies returned error: %v", err)
	}
	if deps.workerTaskQueue != config.DefaultTemporalTaskQueue {
		t.Fatalf("worker task queue = %q, want %q", deps.workerTaskQueue, config.DefaultTemporalTaskQueue)
	}
}

func TestNewQueueRegistryRegistersAllHandlers(t *testing.T) {
	registry, err := newQueueRegistry(&recordingWorkerStore{}, &recordingWorkerAuthorizer{}, recordingWorkflowDispatcher{})
	if err != nil {
		t.Fatalf("newQueueRegistry returned error: %v", err)
	}

	want := map[queue.Kind]bool{
		app.KindStartTemplateRun:      true,
		app.KindStartTemplateSync:     true,
		app.KindSignalRunApproval:     true,
		app.KindSignalRunCancellation: true,
		app.KindGrantStackOwner:       true,
		app.KindMarkStackReady:        true,
		authz.StackGrantSpec.Kind:     true,
	}
	got := registry.Kinds()
	if len(got) != len(want) {
		t.Fatalf("registered handler count = %d, want %d (%v)", len(got), len(want), got)
	}
	for _, kind := range got {
		if !want[kind] {
			t.Fatalf("unexpected registered handler %q", kind)
		}
	}
}

func TestDefaultWorkerDependenciesRegisterTerraformActivities(t *testing.T) {
	t.Parallel()

	worker := &recordingTemporalWorker{}
	deps := defaultWorkerDependencies()

	deps.registerActivities(worker, &recordingWorkerStore{}, t.TempDir(), recordingWorkerLogStore{})

	if !worker.registeredActivities[traits.PrepareWorkspaceActivityName] {
		t.Fatalf("activity %q was not registered", traits.PrepareWorkspaceActivityName)
	}
	if !worker.registeredActivities[traits.FetchSourceActivityName] {
		t.Fatalf("activity %q was not registered", traits.FetchSourceActivityName)
	}
	if !worker.registeredActivities[traits.RunTerraformActivityName] {
		t.Fatalf("activity %q was not registered", traits.RunTerraformActivityName)
	}
	if !worker.registeredActivities[traits.RecordTemplateRunStatusActivityName] {
		t.Fatalf("activity %q was not registered", traits.RecordTemplateRunStatusActivityName)
	}
}

func TestDefaultWorkerDependenciesRegisterTemplateSyncWorkflow(t *testing.T) {
	t.Parallel()

	worker := &recordingTemporalWorker{}
	deps := defaultWorkerDependencies()

	deps.registerWorkflow(worker)

	if !worker.registeredWorkflows[traits.TemplateRunWorkflowName] {
		t.Fatalf("workflow %q was not registered", traits.TemplateRunWorkflowName)
	}
	if !worker.registeredWorkflows[traits.TemplateSyncWorkflowName] {
		t.Fatalf("workflow %q was not registered", traits.TemplateSyncWorkflowName)
	}
}

func TestDefaultWorkerDependenciesRegisterTemplateSyncActivities(t *testing.T) {
	t.Parallel()

	worker := &recordingTemporalWorker{}
	deps := defaultWorkerDependencies()

	deps.registerActivities(worker, &recordingWorkerStore{}, t.TempDir(), recordingWorkerLogStore{})

	if !worker.registeredActivities[traits.RecordTemplateRegistrationStatusActivityName] {
		t.Fatalf("activity %q was not registered", traits.RecordTemplateRegistrationStatusActivityName)
	}
	if !worker.registeredActivities[traits.SyncTemplateActivityName] {
		t.Fatalf("activity %q was not registered", traits.SyncTemplateActivityName)
	}
}

func TestRunWrapsTemporalDialFailure(t *testing.T) {
	t.Parallel()

	dialErr := errors.New("dial failed")
	deps := newRecordingWorkerDependencies(t)
	deps.dialErr = dialErr

	err := runWithDependencies(context.Background(), workerTestEnv, deps.workerDependencies)
	if !errors.Is(err, dialErr) {
		t.Fatalf("error = %v, want dialErr", err)
	}
	if !strings.Contains(err.Error(), "dial temporal") {
		t.Fatalf("error = %q, want dial temporal", err)
	}
}

func TestRunWrapsWorkerRunFailure(t *testing.T) {
	t.Parallel()

	runErr := errors.New("worker failed")
	deps := newRecordingWorkerDependencies(t)
	deps.worker.runErr = runErr

	err := runWithDependencies(context.Background(), workerTestEnv, deps.workerDependencies)
	if !errors.Is(err, runErr) {
		t.Fatalf("error = %v, want runErr", err)
	}
	if !strings.Contains(err.Error(), "run worker") {
		t.Fatalf("error = %q, want run worker", err)
	}
}

func workerTestEnv(key string) string {
	switch key {
	case "DATABASE_URL":
		return "postgres://user:pass@localhost:5432/db?sslmode=disable"
	case "TEMPORAL_ADDRESS":
		return "localhost:7233"
	case "TEMPORAL_NAMESPACE":
		return "tflive"
	case "TEMPORAL_TASK_QUEUE":
		return "terraform-runs-dev"
	case "WORKER_RUN_ROOT":
		return "/tmp/tflive-worker-test"
	case "ARTIFACT_STORE_KIND":
		return "filesystem"
	case "ARTIFACT_STORE_FILESYSTEM_ROOT":
		return "/tmp/tflive-worker-artifacts"
	case "OPENFGA_API_URL":
		return "http://localhost:8080"
	case "OPENFGA_STORE_ID":
		return "store_123"
	case "OPENFGA_MODEL_ID":
		return "model_123"
	default:
		return ""
	}
}

type recordingWorkerDependencies struct {
	workerDependencies
	temporalClient       *recordingWorkerTemporalClient
	worker               *recordingTemporalWorker
	pool                 *recordingWorkerPostgresPool
	store                *recordingWorkerStore
	credentialCipher     *secrets.Cipher
	temporalConfig       temporal.Config
	workerTaskQueue      string
	workerOptions        temporalworker.Options
	artifactStoreConfig  config.ArtifactStoreConfig
	migrated             bool
	activityStoreIsWired bool
	activityRunRoot      string
	activityLogStore     activities.TemplateRunLogStore
	logStore             recordingWorkerLogStore
	logMetadataRecorder  artifacts.LogMetadataRecorder
	dialErr              error
	dispatcher           recordingWorkflowDispatcher
	queueController      *recordingQueueController
}

func newRecordingWorkerDependencies(t *testing.T) *recordingWorkerDependencies {
	t.Helper()

	deps := &recordingWorkerDependencies{
		temporalClient:  &recordingWorkerTemporalClient{},
		worker:          &recordingTemporalWorker{},
		pool:            &recordingWorkerPostgresPool{},
		store:           &recordingWorkerStore{},
		queueController: &recordingQueueController{},
	}
	deps.workerDependencies = workerDependencies{
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
		newStore: func(pool postgresPool, cipher *secrets.Cipher) (workerStore, error) {
			if pool != deps.pool {
				t.Fatalf("newStore pool = %p, want %p", pool, deps.pool)
			}
			deps.credentialCipher = cipher
			return deps.store, nil
		},
		dialTemporal: func(_ context.Context, cfg temporal.Config) (client.Client, error) {
			deps.temporalConfig = cfg
			if deps.dialErr != nil {
				return nil, deps.dialErr
			}
			return deps.temporalClient, nil
		},
		newWorker: func(temporalClient client.Client, taskQueue string, options temporalworker.Options) temporalWorker {
			if temporalClient != deps.temporalClient {
				t.Fatalf("newWorker temporalClient = %p, want %p", temporalClient, deps.temporalClient)
			}
			deps.workerTaskQueue = taskQueue
			deps.workerOptions = options
			return deps.worker
		},
		newDispatcher: func(temporalClient client.Client, taskQueue string) app.WorkflowDispatcher {
			if temporalClient != deps.temporalClient {
				t.Fatalf("newDispatcher temporalClient = %p, want %p", temporalClient, deps.temporalClient)
			}
			if taskQueue != "terraform-runs-dev" && taskQueue != config.DefaultTemporalTaskQueue {
				t.Fatalf("newDispatcher task queue = %q", taskQueue)
			}
			return deps.dispatcher
		},
		newAuthorizationAdapter: func(config.OpenFGAConfig) (workerAuthorizer, error) { return &recordingWorkerAuthorizer{}, nil },
		newQueueController: func(store workerStore, _ workerAuthorizer, dispatcher app.WorkflowDispatcher) (queueController, error) {
			if store != workerStore(deps.store) {
				t.Fatalf("newQueueController store = %p, want %p", store, deps.store)
			}
			if dispatcher != deps.dispatcher {
				t.Fatalf("newQueueController dispatcher = %p, want %p", dispatcher, deps.dispatcher)
			}
			return deps.queueController, nil
		},
		registerWorkflow: func(worker temporalWorker) {
			if worker != deps.worker {
				t.Fatalf("registerWorkflow worker = %p, want %p", worker, deps.worker)
			}
			worker.RegisterWorkflowWithOptions(workflows.TemplateRunWorkflow, workflow.RegisterOptions{
				Name: traits.TemplateRunWorkflowName,
			})
		},
		registerActivities: func(worker temporalWorker, recorder workerStore, runRoot string, logStore activities.TemplateRunLogStore) {
			if worker != deps.worker {
				t.Fatalf("registerActivities worker = %p, want %p", worker, deps.worker)
			}
			if recorder != workerStore(deps.store) {
				t.Fatalf("activity recorder = %p, want store %p", recorder, deps.store)
			}
			deps.activityStoreIsWired = true
			deps.activityRunRoot = runRoot
			deps.activityLogStore = logStore
			worker.RegisterActivityWithOptions(
				func(context.Context, traits.PrepareWorkspaceActivityInput) (traits.PrepareWorkspaceActivityOutput, error) {
					return traits.PrepareWorkspaceActivityOutput{}, nil
				},
				activity.RegisterOptions{
					Name: traits.PrepareWorkspaceActivityName,
				},
			)
			worker.RegisterActivityWithOptions(
				func(context.Context, traits.FetchSourceActivityInput) (traits.FetchSourceActivityOutput, error) {
					return traits.FetchSourceActivityOutput{}, nil
				},
				activity.RegisterOptions{
					Name: traits.FetchSourceActivityName,
				},
			)
			worker.RegisterActivityWithOptions(
				func(context.Context, traits.RunTerraformActivityInput) error {
					return nil
				},
				activity.RegisterOptions{
					Name: traits.RunTerraformActivityName,
				},
			)
			worker.RegisterActivityWithOptions(
				func(context.Context, traits.TemplateRunStatusActivityInput) error {
					return nil
				},
				activity.RegisterOptions{
					Name: traits.RecordTemplateRunStatusActivityName,
				},
			)
		},
		newLogStore: func(cfg config.ArtifactStoreConfig, recorder artifacts.LogMetadataRecorder) (activities.TemplateRunLogStore, error) {
			deps.artifactStoreConfig = cfg
			deps.logMetadataRecorder = recorder
			return deps.logStore, nil
		},
		interruptCh: func() <-chan interface{} {
			ch := make(chan interface{})
			close(ch)
			return ch
		},
	}
	return deps
}

type recordingWorkerPostgresPool struct {
	databaseURL string
	pinged      bool
	closed      bool
}

func (pool *recordingWorkerPostgresPool) Ping(context.Context) error {
	pool.pinged = true
	return nil
}

func (pool *recordingWorkerPostgresPool) Close() {
	pool.closed = true
}

type recordingWorkerStore struct{}

func (store *recordingWorkerStore) RecordTemplateRunStatus(context.Context, traits.TemplateRunStatusActivityInput) error {
	return nil
}

func (store *recordingWorkerStore) RecordTemplateRegistrationStatus(context.Context, traits.TemplateRegistrationStatusActivityInput) error {
	return nil
}

func (store *recordingWorkerStore) UpsertTemplateRevisionWithVariables(context.Context, traits.TemplateRevision, []traits.TemplateVariable) (traits.TemplateRevision, error) {
	return traits.TemplateRevision{}, nil
}

func (store *recordingWorkerStore) RecordTemplateRunLog(context.Context, traits.TemplateRunLog) error {
	return nil
}

func (store *recordingWorkerStore) Enqueue(context.Context, ...queue.Request) error {
	return nil
}

func (store *recordingWorkerStore) ReconcileTemplateRunCancellation(context.Context, traits.TenantID, traits.TemplateRunID, string) error {
	return nil
}

func (store *recordingWorkerStore) MarkStackReady(context.Context, traits.TenantID, traits.StackID) error {
	return nil
}

type recordingWorkerAuthorizer struct{}

func (*recordingWorkerAuthorizer) Check(context.Context, authz.CheckRequest) (authz.CheckResult, error) {
	return authz.CheckResult{}, nil
}
func (*recordingWorkerAuthorizer) BatchCheck(context.Context, authz.BatchCheckRequest) (authz.BatchCheckResult, error) {
	return authz.BatchCheckResult{}, nil
}
func (*recordingWorkerAuthorizer) ListGrants(context.Context, authz.ListGrantsRequest) (authz.ListGrantsResult, error) {
	return authz.ListGrantsResult{}, nil
}
func (*recordingWorkerAuthorizer) WriteRelationships(context.Context, authz.Mutation) error {
	return nil
}
func (*recordingWorkerAuthorizer) DeleteRelationships(context.Context, authz.Mutation) error {
	return nil
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

type recordingQueueController struct {
	ran     bool
	stopped bool
}

func (dispatcher *recordingQueueController) Run(ctx context.Context) {
	dispatcher.ran = true
	<-ctx.Done()
	dispatcher.stopped = true
}

type recordingWorkerLogStore struct{}

func (recordingWorkerLogStore) PutTemplateRunLog(context.Context, traits.TenantID, traits.TemplateRunID, string, io.Reader) error {
	return nil
}

type recordingWorkerTemporalClient struct {
	client.Client
	closed bool
}

func (temporalClient *recordingWorkerTemporalClient) Close() {
	temporalClient.closed = true
}

type recordingTemporalWorker struct {
	registeredWorkflow        uintptr
	registeredWorkflowOptions workflow.RegisterOptions
	registeredWorkflows       map[string]bool
	registeredActivity        uintptr
	registeredActivityOptions activity.RegisterOptions
	registeredActivities      map[string]bool
	ran                       bool
	runErr                    error
}

func (worker *recordingTemporalWorker) RegisterWorkflowWithOptions(workflowFn interface{}, options workflow.RegisterOptions) {
	if worker.registeredWorkflows == nil {
		worker.registeredWorkflows = make(map[string]bool)
	}
	worker.registeredWorkflow = reflect.ValueOf(workflowFn).Pointer()
	worker.registeredWorkflowOptions = options
	worker.registeredWorkflows[options.Name] = true
}

func (worker *recordingTemporalWorker) RegisterActivityWithOptions(activityFn interface{}, options activity.RegisterOptions) {
	if worker.registeredActivities == nil {
		worker.registeredActivities = make(map[string]bool)
	}
	worker.registeredActivity = reflect.ValueOf(activityFn).Pointer()
	worker.registeredActivityOptions = options
	worker.registeredActivities[options.Name] = true
}

func (worker *recordingTemporalWorker) Run(<-chan interface{}) error {
	worker.ran = true
	return worker.runErr
}

// recordingWorkerStore must satisfy queue.Backend now that the worker runs a
// queue controller instead of the authorization dispatcher.
func (store *recordingWorkerStore) Claim(context.Context, time.Duration, int, []queue.Kind) ([]queue.Item, error) {
	return nil, nil
}

func (store *recordingWorkerStore) Complete(context.Context, int64, int64) (bool, error) {
	return true, nil
}

func (store *recordingWorkerStore) Reschedule(context.Context, int64, time.Duration, string) error {
	return nil
}

func (store *recordingWorkerStore) Prune(context.Context, time.Duration) (int64, error) {
	return 0, nil
}

func (authorizer *recordingWorkerAuthorizer) ListSubjectGrants(context.Context, authz.ListSubjectGrantsRequest) (authz.ListGrantsResult, error) {
	return authz.ListGrantsResult{}, nil
}
