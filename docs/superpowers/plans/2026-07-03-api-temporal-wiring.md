# API Temporal Wiring Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Wire `cmd/megagega-api` to construct the app service with the Temporal workflow dispatcher.

**Architecture:** `internal/config` owns API environment loading, including Temporal address and task queue defaults. `cmd/megagega-api` remains the composition root and gains a private dependency seam so tests can verify Postgres plus Temporal wiring without dialing real services. The real `run(ctx, os.Getenv)` path still uses pgx, Postgres migrations, `temporal.Dial`, and `temporal.NewDispatcher`.

**Tech Stack:** Go 1.24, `pgxpool`, existing `internal/postgres`, existing `internal/temporal`, standard `testing`.

---

## File Structure

- Modify `internal/config/config.go`: extend `APIConfig`, add `DefaultTemporalTaskQueue`, load and validate Temporal settings.
- Modify `internal/config/config_test.go`: cover Temporal config loading, missing address, and default task queue.
- Modify `cmd/megagega-api/main.go`: add private startup dependency seam, dial Temporal, build dispatcher, wire `app.Service.Workflows`.
- Modify `cmd/megagega-api/main_test.go`: test config failure, Temporal dial parameters, task queue wiring, service wiring, Temporal dial errors, and the Postgres integration test with fake Temporal.

### Task 1: Extend API Config With Temporal Settings

**Files:**
- Modify: `internal/config/config_test.go`
- Modify: `internal/config/config.go`

- [ ] **Step 1: Replace config tests with Temporal coverage**

Replace `internal/config/config_test.go` with:

```go
package config

import (
	"errors"
	"testing"
)

func TestLoadAPIConfigReadsAPISettings(t *testing.T) {
	t.Parallel()

	cfg, err := LoadAPIConfig(func(key string) string {
		switch key {
		case "DATABASE_URL":
			return " postgres://user:pass@localhost:5432/db?sslmode=disable "
		case "TEMPORAL_ADDRESS":
			return " localhost:7233 "
		case "TEMPORAL_NAMESPACE":
			return " megagega "
		case "TEMPORAL_TASK_QUEUE":
			return " terraform-runs-dev "
		default:
			return ""
		}
	})
	if err != nil {
		t.Fatalf("LoadAPIConfig returned error: %v", err)
	}

	if cfg.DatabaseURL != "postgres://user:pass@localhost:5432/db?sslmode=disable" {
		t.Fatalf("DatabaseURL = %q", cfg.DatabaseURL)
	}
	if cfg.TemporalAddress != "localhost:7233" {
		t.Fatalf("TemporalAddress = %q", cfg.TemporalAddress)
	}
	if cfg.TemporalNamespace != "megagega" {
		t.Fatalf("TemporalNamespace = %q", cfg.TemporalNamespace)
	}
	if cfg.TemporalTaskQueue != "terraform-runs-dev" {
		t.Fatalf("TemporalTaskQueue = %q", cfg.TemporalTaskQueue)
	}
}

func TestLoadAPIConfigDefaultsTemporalTaskQueue(t *testing.T) {
	t.Parallel()

	cfg, err := LoadAPIConfig(func(key string) string {
		switch key {
		case "DATABASE_URL":
			return "postgres://user:pass@localhost:5432/db?sslmode=disable"
		case "TEMPORAL_ADDRESS":
			return "localhost:7233"
		default:
			return ""
		}
	})
	if err != nil {
		t.Fatalf("LoadAPIConfig returned error: %v", err)
	}

	if cfg.TemporalTaskQueue != DefaultTemporalTaskQueue {
		t.Fatalf("TemporalTaskQueue = %q, want %q", cfg.TemporalTaskQueue, DefaultTemporalTaskQueue)
	}
	if cfg.TemporalNamespace != "" {
		t.Fatalf("TemporalNamespace = %q, want empty", cfg.TemporalNamespace)
	}
}

func TestLoadAPIConfigRequiresDatabaseURL(t *testing.T) {
	t.Parallel()

	_, err := LoadAPIConfig(func(key string) string {
		if key == "TEMPORAL_ADDRESS" {
			return "localhost:7233"
		}
		return ""
	})
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("error = %v, want ErrInvalidConfig", err)
	}
}

func TestLoadAPIConfigRequiresTemporalAddress(t *testing.T) {
	t.Parallel()

	_, err := LoadAPIConfig(func(key string) string {
		if key == "DATABASE_URL" {
			return "postgres://user:pass@localhost:5432/db?sslmode=disable"
		}
		return ""
	})
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("error = %v, want ErrInvalidConfig", err)
	}
}
```

- [ ] **Step 2: Run config tests to verify they fail**

Run:

```bash
go test ./internal/config
```

Expected: FAIL because `APIConfig` does not have Temporal fields and `DefaultTemporalTaskQueue` is undefined.

- [ ] **Step 3: Replace config implementation**

Replace `internal/config/config.go` with:

```go
package config

import (
	"errors"
	"fmt"
	"strings"
)

const DefaultTemporalTaskQueue = "terraform-runs"

var ErrInvalidConfig = errors.New("invalid config")

type APIConfig struct {
	DatabaseURL       string
	TemporalAddress   string
	TemporalNamespace string
	TemporalTaskQueue string
}

func LoadAPIConfig(getenv func(string) string) (APIConfig, error) {
	cfg := APIConfig{
		DatabaseURL:       strings.TrimSpace(getenv("DATABASE_URL")),
		TemporalAddress:   strings.TrimSpace(getenv("TEMPORAL_ADDRESS")),
		TemporalNamespace: strings.TrimSpace(getenv("TEMPORAL_NAMESPACE")),
		TemporalTaskQueue: strings.TrimSpace(getenv("TEMPORAL_TASK_QUEUE")),
	}
	if cfg.TemporalTaskQueue == "" {
		cfg.TemporalTaskQueue = DefaultTemporalTaskQueue
	}

	if cfg.DatabaseURL == "" {
		return APIConfig{}, fmt.Errorf("%w: DATABASE_URL is required", ErrInvalidConfig)
	}
	if cfg.TemporalAddress == "" {
		return APIConfig{}, fmt.Errorf("%w: TEMPORAL_ADDRESS is required", ErrInvalidConfig)
	}

	return cfg, nil
}
```

- [ ] **Step 4: Run config tests to verify they pass**

Run:

```bash
go test ./internal/config
```

Expected: PASS.

- [ ] **Step 5: Commit the config slice**

Run:

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat: load api temporal config"
```

Expected: commit succeeds with only config files staged.

### Task 2: Add API Startup Dependency Seam And Temporal Wiring

**Files:**
- Modify: `cmd/megagega-api/main_test.go`
- Modify: `cmd/megagega-api/main.go`

- [ ] **Step 1: Replace API command tests with wiring coverage**

Replace `cmd/megagega-api/main_test.go` with:

```go
package main

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/vishu42/megagega/internal/app"
	"github.com/vishu42/megagega/internal/config"
	"github.com/vishu42/megagega/internal/traits"
	"github.com/vishu42/megagega/internal/temporal"
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
	err := runWithDependencies(context.Background(), apiTestEnv, deps.apiDependencies)
	if err != nil {
		t.Fatalf("runWithDependencies returned error: %v", err)
	}

	if deps.pool.databaseURL != "postgres://user:pass@localhost:5432/db?sslmode=disable" {
		t.Fatalf("database URL = %q", deps.pool.databaseURL)
	}
	if !deps.pool.pinged {
		t.Fatal("postgres pool was not pinged")
	}
	if !deps.migrated {
		t.Fatal("postgres migrations were not run")
	}
	if deps.temporalConfig.Address != "localhost:7233" {
		t.Fatalf("temporal address = %q", deps.temporalConfig.Address)
	}
	if deps.temporalConfig.Namespace != "megagega" {
		t.Fatalf("temporal namespace = %q", deps.temporalConfig.Namespace)
	}
	if deps.dispatcherTaskQueue != "terraform-runs-dev" {
		t.Fatalf("dispatcher task queue = %q", deps.dispatcherTaskQueue)
	}
	if deps.service.Workflows != deps.dispatcher {
		t.Fatalf("service Workflows was not wired to dispatcher")
	}
	if deps.service.StackTemplates != deps.store {
		t.Fatalf("service StackTemplates was not wired to store")
	}
	if deps.service.TemplateRuns != deps.store {
		t.Fatalf("service TemplateRuns was not wired to store")
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

	dialErr := errors.New("temporal unavailable")
	deps := newRecordingAPIDependencies(t)
	deps.dialErr = dialErr

	err := runWithDependencies(context.Background(), apiTestEnv, deps.apiDependencies)
	if !errors.Is(err, dialErr) {
		t.Fatalf("error = %v, want wrapped dial error", err)
	}
	if !strings.Contains(err.Error(), "dial temporal") {
		t.Fatalf("error = %q, want dial temporal context", err.Error())
	}
}

func TestRunWrapsWireServiceFailure(t *testing.T) {
	t.Parallel()

	wireErr := errors.New("service wiring failed")
	deps := newRecordingAPIDependencies(t)
	deps.serviceErr = wireErr

	err := runWithDependencies(context.Background(), apiTestEnv, deps.apiDependencies)
	if !errors.Is(err, wireErr) {
		t.Fatalf("error = %v, want wrapped wire service error", err)
	}
	if !strings.Contains(err.Error(), "wire service") {
		t.Fatalf("error = %q, want wire service context", err.Error())
	}
}

func TestRunMigratesRealPostgresWhenDSNIsSet(t *testing.T) {
	t.Parallel()

	dsn := os.Getenv("MEGAGEGA_POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("MEGAGEGA_POSTGRES_TEST_DSN is not set")
	}

	deps := defaultAPIDependencies()
	fakeTemporalClient := &recordingTemporalClient{}
	deps.dialTemporal = func(_ context.Context, _ temporal.Config) (client.Client, error) {
		return fakeTemporalClient, nil
	}
	deps.newDispatcher = func(_ client.Client, _ string) app.WorkflowDispatcher {
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
	if !fakeTemporalClient.closed {
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
				t.Fatalf("migrate pool = %#v, want recording pool", pool)
			}
			deps.migrated = true
			return nil
		},
		newStore: func(pool postgresPool) (appRepositories, error) {
			if pool != deps.pool {
				t.Fatalf("store pool = %#v, want recording pool", pool)
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
				t.Fatalf("dispatcher temporal client = %#v, want recording temporal client", temporalClient)
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
```

- [ ] **Step 2: Run API command tests to verify they fail**

Run:

```bash
go test ./cmd/megagega-api
```

Expected: FAIL because `runWithDependencies`, `apiDependencies`, `postgresPool`, and `appRepositories` are undefined, and `run` does not require Temporal config yet.

- [ ] **Step 3: Replace API command startup implementation**

Replace `cmd/megagega-api/main.go` with:

```go
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/vishu42/megagega/internal/app"
	"github.com/vishu42/megagega/internal/config"
	"github.com/vishu42/megagega/internal/postgres"
	"github.com/vishu42/megagega/internal/temporal"
	"go.temporal.io/sdk/client"
)

func main() {
	if err := run(context.Background(), os.Getenv); err != nil {
		log.Fatal(err)
	}
}

type postgresPool interface {
	Ping(context.Context) error
	Close()
}

type appRepositories interface {
	app.StackTemplateRepository
	app.TemplateRunRepository
}

type apiDependencies struct {
	newPostgresPool func(context.Context, string) (postgresPool, error)
	migratePostgres func(context.Context, postgresPool) error
	newStore         func(postgresPool) (appRepositories, error)
	dialTemporal    func(context.Context, temporal.Config) (client.Client, error)
	newDispatcher   func(client.Client, string) app.WorkflowDispatcher
	newService      func(app.Service) (*app.Service, error)
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
		newStore: func(pool postgresPool) (appRepositories, error) {
			pgxPool, ok := pool.(*pgxpool.Pool)
			if !ok {
				return nil, fmt.Errorf("unexpected postgres pool type %T", pool)
			}
			return postgres.NewStore(pgxPool), nil
		},
		dialTemporal: temporal.Dial,
		newDispatcher: func(temporalClient client.Client, taskQueue string) app.WorkflowDispatcher {
			return temporal.NewDispatcher(temporalClient, taskQueue)
		},
		newService: func(service app.Service) (*app.Service, error) {
			return app.NewService(service), nil
		},
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

	store, err := deps.newStore(pool)
	if err != nil {
		return fmt.Errorf("wire service: %w", err)
	}

	temporalClient, err := deps.dialTemporal(ctx, temporal.Config{
		Address:   cfg.TemporalAddress,
		Namespace: cfg.TemporalNamespace,
	})
	if err != nil {
		return fmt.Errorf("dial temporal: %w", err)
	}
	defer temporalClient.Close()

	dispatcher := deps.newDispatcher(temporalClient, cfg.TemporalTaskQueue)
	if _, err := deps.newService(app.Service{
		StackTemplates: store,
		TemplateRuns:   store,
		Workflows:      dispatcher,
	}); err != nil {
		return fmt.Errorf("wire service: %w", err)
	}

	return nil
}
```

- [ ] **Step 4: Run API command tests to verify they pass**

Run:

```bash
go test ./cmd/megagega-api
```

Expected: PASS, with `TestRunMigratesRealPostgresWhenDSNIsSet` skipped when `MEGAGEGA_POSTGRES_TEST_DSN` is absent.

- [ ] **Step 5: Commit the API startup wiring slice**

Run:

```bash
git add cmd/megagega-api/main.go cmd/megagega-api/main_test.go
git commit -m "feat: wire api temporal dispatcher"
```

Expected: commit succeeds with only API command files staged.

### Task 3: Verify Whole Repository

**Files:**
- Modify: Go files touched by gofmt if formatting changes.

- [ ] **Step 1: Format touched Go packages**

Run:

```bash
gofmt -w internal/config cmd/megagega-api
```

Expected: no output.

- [ ] **Step 2: Run focused tests**

Run:

```bash
go test ./internal/config ./cmd/megagega-api
```

Expected: PASS.

- [ ] **Step 3: Run full test suite**

Run:

```bash
go test ./...
```

Expected: PASS. Postgres integration tests skip unless `MEGAGEGA_POSTGRES_TEST_DSN` is set.

- [ ] **Step 4: Check module tidiness**

Run:

```bash
go mod tidy -diff
```

Expected: no output and exit code 0.

- [ ] **Step 5: Check git status**

Run:

```bash
git status --short
```

Expected: no unstaged changes from this Temporal API wiring implementation except pre-existing untracked files unrelated to this work.
