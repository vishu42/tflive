package main

import (
	"context"
	"errors"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/vishu42/tflive/internal/activities"
	"github.com/vishu42/tflive/internal/config"
	"github.com/vishu42/tflive/internal/domain"
	"github.com/vishu42/tflive/internal/encryption"
	"github.com/vishu42/tflive/internal/temporal"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	temporalworker "go.temporal.io/sdk/worker"
)

// scrubConsumedSecret is what stands between a parsed GitHub App private key
// or credential encryption key and every Terraform subprocess this worker
// starts (runner.CommandExecutor inherits the full process environment). This
// test exercises the two real variable names directly rather than driving the
// whole of runWithDependencies, since standing up Postgres and Temporal just
// to observe two Unsetenv calls would make the test about wiring, not about
// the guarantee that matters: the value is gone from the environment
// afterward.
func TestScrubConsumedSecretRemovesTheVariableFromTheEnvironment(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"GITHUB_APP_PRIVATE_KEY", "CREDENTIAL_ENCRYPTION_KEY"} {
		t.Run(name, func(t *testing.T) {
			original, hadOriginal := os.LookupEnv(name)
			if err := os.Setenv(name, "super-secret-value"); err != nil {
				t.Fatalf("setenv %s: %v", name, err)
			}
			defer func() {
				if hadOriginal {
					os.Setenv(name, original)
				} else {
					os.Unsetenv(name)
				}
			}()

			scrubConsumedSecret(name)

			if value := os.Getenv(name); value != "" {
				t.Fatalf("Getenv(%q) = %q, want empty after scrubbing", name, value)
			}
		})
	}
}

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
	if deps.temporalConfig.Address != "localhost:7233" {
		t.Fatalf("temporal address = %q, want localhost:7233", deps.temporalConfig.Address)
	}
	if deps.temporalConfig.Namespace != "tflive" {
		t.Fatalf("temporal namespace = %q, want tflive", deps.temporalConfig.Namespace)
	}
	// The API owns the control queue now; this process must never poll it.
	if deps.workerTaskQueue != domain.ExecutionTaskQueue {
		t.Fatalf("worker task queue = %q, want %q", deps.workerTaskQueue, domain.ExecutionTaskQueue)
	}
	if !deps.workerOptions.EnableSessionWorker {
		t.Fatal("session worker was not enabled")
	}
	for _, name := range []string{domain.PrepareWorkspaceActivityName, domain.FetchSourceActivityName, domain.RunTerraformActivityName} {
		if !deps.worker.registeredActivities[name] {
			t.Fatalf("activity %q was not registered", name)
		}
	}
	if len(deps.worker.registeredActivities) != 3 {
		t.Fatalf("registered activities = %v, want only the three execution activities", deps.worker.registeredActivities)
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
	if !deps.worker.ran {
		t.Fatal("worker was not run")
	}
	if !deps.temporalClient.closed {
		t.Fatal("temporal client was not closed")
	}
	if !deps.pool.closed {
		t.Fatal("postgres pool was not closed")
	}
}

func TestDefaultWorkerDependenciesRegisterOnlyExecutionActivities(t *testing.T) {
	t.Parallel()

	worker := &recordingTemporalWorker{}
	deps := defaultWorkerDependencies()

	deps.registerActivities(worker, &recordingWorkerStore{}, t.TempDir(), recordingWorkerLogStore{}, nil)

	want := map[string]bool{
		domain.PrepareWorkspaceActivityName: true,
		domain.FetchSourceActivityName:      true,
		domain.RunTerraformActivityName:     true,
	}
	if !reflect.DeepEqual(worker.registeredActivities, want) {
		t.Fatalf("registered activities = %v, want %v", worker.registeredActivities, want)
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
	credentialCipher     *encryption.Cipher
	temporalConfig       temporal.Config
	workerTaskQueue      string
	workerOptions        temporalworker.Options
	artifactStoreConfig  config.ArtifactStoreConfig
	activityStoreIsWired bool
	activityRunRoot      string
	activityLogStore     activities.TemplateRunLogStore
	logStore             recordingWorkerLogStore
	dialErr              error
}

func newRecordingWorkerDependencies(t *testing.T) *recordingWorkerDependencies {
	t.Helper()

	deps := &recordingWorkerDependencies{
		temporalClient: &recordingWorkerTemporalClient{},
		worker:         &recordingTemporalWorker{},
		pool:           &recordingWorkerPostgresPool{},
		store:          &recordingWorkerStore{},
	}
	deps.workerDependencies = workerDependencies{
		newPostgresPool: func(_ context.Context, databaseURL string) (postgresPool, error) {
			deps.pool.databaseURL = databaseURL
			return deps.pool, nil
		},
		newStore: func(pool postgresPool, cipher *encryption.Cipher) (workerStore, error) {
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
		registerActivities: func(worker temporalWorker, recorder workerStore, runRoot string, logStore activities.TemplateRunLogStore, gitHubTokens activities.GitHubTokenSource) {
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
				func(context.Context, domain.PrepareWorkspaceActivityInput) (domain.PrepareWorkspaceActivityOutput, error) {
					return domain.PrepareWorkspaceActivityOutput{}, nil
				},
				activity.RegisterOptions{
					Name: domain.PrepareWorkspaceActivityName,
				},
			)
			worker.RegisterActivityWithOptions(
				func(context.Context, domain.FetchSourceActivityInput) (domain.FetchSourceActivityOutput, error) {
					return domain.FetchSourceActivityOutput{}, nil
				},
				activity.RegisterOptions{
					Name: domain.FetchSourceActivityName,
				},
			)
			worker.RegisterActivityWithOptions(
				func(context.Context, domain.RunTerraformActivityInput) error {
					return nil
				},
				activity.RegisterOptions{
					Name: domain.RunTerraformActivityName,
				},
			)
		},
		newLogStore: func(cfg config.ArtifactStoreConfig) (activities.TemplateRunLogStore, error) {
			deps.artifactStoreConfig = cfg
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

func (store *recordingWorkerStore) RecordTemplateRunStatus(context.Context, domain.TemplateRunStatusActivityInput) error {
	return nil
}

func (store *recordingWorkerStore) RecordTemplateRunLog(context.Context, domain.TemplateRunLog) error {
	return nil
}

type recordingWorkerLogStore struct{}

func (recordingWorkerLogStore) PutTemplateRunLog(context.Context, domain.TenantID, domain.TemplateRunID, string, io.Reader) (domain.TemplateRunLog, error) {
	return domain.TemplateRunLog{}, nil
}

type recordingWorkerTemporalClient struct {
	client.Client
	closed bool
}

func (temporalClient *recordingWorkerTemporalClient) Close() {
	temporalClient.closed = true
}

type recordingTemporalWorker struct {
	registeredActivity        uintptr
	registeredActivityOptions activity.RegisterOptions
	registeredActivities      map[string]bool
	ran                       bool
	runErr                    error
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
