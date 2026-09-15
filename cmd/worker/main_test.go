package main

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/vishu42/tflive/internal/activities"
	"github.com/vishu42/tflive/internal/config"
	"github.com/vishu42/tflive/internal/domain"
	"github.com/vishu42/tflive/internal/runseal"
	"github.com/vishu42/tflive/internal/temporal"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	temporalworker "go.temporal.io/sdk/worker"
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

func TestRunWiresTemporalWorker(t *testing.T) {
	t.Parallel()

	deps := newRecordingWorkerDependencies(t)
	if err := runWithDependencies(context.Background(), workerTestEnv, deps.workerDependencies); err != nil {
		t.Fatalf("runWithDependencies returned error: %v", err)
	}

	if deps.temporalConfig.Address != "localhost:7233" {
		t.Fatalf("temporal address = %q, want localhost:7233", deps.temporalConfig.Address)
	}
	if deps.temporalConfig.Namespace != "tflive" {
		t.Fatalf("temporal namespace = %q, want tflive", deps.temporalConfig.Namespace)
	}
	// The API owns the control queue; this process must never poll it.
	if deps.workerTaskQueue != domain.ExecutionTaskQueue {
		t.Fatalf("worker task queue = %q, want %q", deps.workerTaskQueue, domain.ExecutionTaskQueue)
	}
	if !deps.workerOptions.EnableSessionWorker {
		t.Fatal("session worker was not enabled")
	}
	if deps.activityKeys == nil {
		t.Fatal("activities were not given a key ring")
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
}

func TestDefaultWorkerDependenciesRegisterOnlyExecutionActivities(t *testing.T) {
	t.Parallel()

	worker := &recordingTemporalWorker{}
	deps := defaultWorkerDependencies()

	deps.registerActivities(worker, t.TempDir(), recordingWorkerLogStore{}, runseal.NewKeyRing())

	want := map[string]bool{
		domain.PrepareWorkspaceActivityName: true,
		domain.FetchSourceActivityName:      true,
		domain.RunTerraformActivityName:     true,
		domain.ReleaseRunKeyActivityName:    true,
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
	default:
		return ""
	}
}

type recordingWorkerDependencies struct {
	workerDependencies
	temporalClient      *recordingWorkerTemporalClient
	worker              *recordingTemporalWorker
	temporalConfig      temporal.Config
	workerTaskQueue     string
	workerOptions       temporalworker.Options
	artifactStoreConfig config.ArtifactStoreConfig
	activityRunRoot     string
	activityLogStore    activities.TemplateRunLogStore
	activityKeys        *runseal.KeyRing
	logStore            recordingWorkerLogStore
	dialErr             error
}

func newRecordingWorkerDependencies(t *testing.T) *recordingWorkerDependencies {
	t.Helper()

	deps := &recordingWorkerDependencies{
		temporalClient: &recordingWorkerTemporalClient{},
		worker:         &recordingTemporalWorker{},
	}
	deps.workerDependencies = workerDependencies{
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
		registerActivities: func(worker temporalWorker, runRoot string, logStore activities.TemplateRunLogStore, keys *runseal.KeyRing) {
			if worker != deps.worker {
				t.Fatalf("registerActivities worker = %p, want %p", worker, deps.worker)
			}
			deps.activityRunRoot = runRoot
			deps.activityLogStore = logStore
			deps.activityKeys = keys
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
	registeredActivities map[string]bool
	ran                  bool
	runErr               error
}

func (worker *recordingTemporalWorker) RegisterActivityWithOptions(_ interface{}, options activity.RegisterOptions) {
	if worker.registeredActivities == nil {
		worker.registeredActivities = make(map[string]bool)
	}
	worker.registeredActivities[options.Name] = true
}

func (worker *recordingTemporalWorker) Run(<-chan interface{}) error {
	worker.ran = true
	return worker.runErr
}
