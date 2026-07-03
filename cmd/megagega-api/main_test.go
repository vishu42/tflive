package main

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/vishu42/megagega/internal/app"
	"github.com/vishu42/megagega/internal/config"
	"github.com/vishu42/megagega/internal/temporal"
	"github.com/vishu42/megagega/internal/traits"
	"go.temporal.io/sdk/client"
)

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

func TestRunRequiresTemporalAddress(t *testing.T) {
	t.Parallel()

	err := run(context.Background(), func(key string) string {
		if key == "DATABASE_URL" {
			return "postgres://user:pass@localhost:5432/db?sslmode=disable"
		}
		return ""
	})
	if !errors.Is(err, config.ErrInvalidConfig) {
		t.Fatalf("error = %v, want ErrInvalidConfig", err)
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
	if deps.temporalConfig.Namespace != "megagega" {
		t.Fatalf("temporal namespace = %q, want megagega", deps.temporalConfig.Namespace)
	}
	if deps.dispatcherTaskQueue != "terraform-runs-dev" {
		t.Fatalf("dispatcher task queue = %q, want terraform-runs-dev", deps.dispatcherTaskQueue)
	}
	if deps.service.Workflows != deps.dispatcher {
		t.Fatal("service Workflows is not the dispatcher")
	}
	if deps.service.StackTemplates != deps.store {
		t.Fatal("service StackTemplates is not the store")
	}
	if deps.service.TemplateRuns != deps.store {
		t.Fatal("service TemplateRuns is not the store")
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

	deps := newRecordingAPIDependencies(t)
	err := runWithDependencies(context.Background(), func(key string) string {
		switch key {
		case "DATABASE_URL":
			return "postgres://user:pass@localhost:5432/db?sslmode=disable"
		case "TEMPORAL_ADDRESS":
			return "localhost:7233"
		default:
			return ""
		}
	}, deps.apiDependencies)
	if err != nil {
		t.Fatalf("runWithDependencies returned error: %v", err)
	}
	if deps.dispatcherTaskQueue != config.DefaultTemporalTaskQueue {
		t.Fatalf("dispatcher task queue = %q, want %q", deps.dispatcherTaskQueue, config.DefaultTemporalTaskQueue)
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

	dsn := os.Getenv("MEGAGEGA_POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("MEGAGEGA_POSTGRES_TEST_DSN is not set")
	}

	temporalClient := &recordingTemporalClient{}
	deps := defaultAPIDependencies()
	deps.dialTemporal = func(context.Context, temporal.Config) (client.Client, error) {
		return temporalClient, nil
	}
	deps.newDispatcher = func(client.Client, string) app.WorkflowDispatcher {
		return recordingWorkflowDispatcher{}
	}

	err := runWithDependencies(context.Background(), func(key string) string {
		switch key {
		case "DATABASE_URL":
			return dsn
		case "TEMPORAL_ADDRESS":
			return "localhost:7233"
		default:
			return ""
		}
	}, deps)
	if err != nil {
		t.Fatalf("runWithDependencies returned error: %v", err)
	}
	if !temporalClient.closed {
		t.Fatal("temporal client was not closed")
	}
}

func apiTestEnv(key string) string {
	switch key {
	case "DATABASE_URL":
		return "postgres://user:pass@localhost:5432/db?sslmode=disable"
	case "TEMPORAL_ADDRESS":
		return "localhost:7233"
	case "TEMPORAL_NAMESPACE":
		return "megagega"
	case "TEMPORAL_TASK_QUEUE":
		return "terraform-runs-dev"
	default:
		return ""
	}
}

type recordingAPIDependencies struct {
	apiDependencies
	pool                *recordingPostgresPool
	store               recordingStore
	temporalClient      *recordingTemporalClient
	dispatcher          recordingWorkflowDispatcher
	temporalConfig      temporal.Config
	dispatcherTaskQueue string
	service             app.Service
	migrated            bool
	dialErr             error
	serviceErr          error
}

func newRecordingAPIDependencies(t *testing.T) *recordingAPIDependencies {
	t.Helper()

	deps := &recordingAPIDependencies{
		pool:           &recordingPostgresPool{},
		store:          recordingStore{},
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
		newService: func(service app.Service) (*app.Service, error) {
			deps.service = service
			if deps.serviceErr != nil {
				return nil, deps.serviceErr
			}
			return app.NewService(service), nil
		},
	}
	return deps
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

func (recordingStore) GetStackTemplate(context.Context, traits.TenantID, traits.StackTemplateID) (traits.StackTemplate, error) {
	return traits.StackTemplate{}, nil
}

func (recordingStore) CreateTemplateRun(context.Context, traits.TemplateRun) error {
	return nil
}

func (recordingStore) ApproveTemplateRun(context.Context, traits.TemplateRunApproval) error {
	return nil
}

func (recordingStore) RequestTemplateRunCancellation(context.Context, traits.TemplateRunCancellation) error {
	return nil
}

type recordingWorkflowDispatcher struct{}

func (recordingWorkflowDispatcher) StartTemplateRun(context.Context, traits.TemplateRunWorkflowInput) error {
	return nil
}

func (recordingWorkflowDispatcher) ApproveTemplateRun(context.Context, traits.TenantID, traits.TemplateRunID, traits.ApprovalSignal) error {
	return nil
}

func (recordingWorkflowDispatcher) CancelTemplateRun(context.Context, traits.TenantID, traits.TemplateRunID, traits.CancelSignal) error {
	return nil
}
