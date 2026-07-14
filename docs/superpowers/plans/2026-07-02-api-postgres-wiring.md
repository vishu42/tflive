# API Postgres Wiring Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `cmd/tflive-api` bootstrap the Postgres store from `DATABASE_URL`.

**Architecture:** `internal/config` owns environment loading and validation. `cmd/tflive-api` owns process composition: config, pgx pool, migrations, store construction, and `app.NewService` wiring. Startup remains short-lived until the HTTP server slice exists.

**Tech Stack:** Go 1.24, standard `testing`, `errors`, `os`, `pgxpool`, existing `internal/postgres` migrations.

---

### Task 1: API Config Loader

**Files:**
- Create: `internal/config/config.go`
- Create: `internal/config/config_test.go`

- [ ] **Step 1: Write failing tests**

```go
func TestLoadAPIConfigReadsDatabaseURL(t *testing.T) {
	t.Parallel()

	cfg, err := LoadAPIConfig(func(key string) string {
		if key == "DATABASE_URL" {
			return "postgres://user:pass@localhost:5432/db?sslmode=disable"
		}
		return ""
	})
	if err != nil {
		t.Fatalf("LoadAPIConfig returned error: %v", err)
	}
	if cfg.DatabaseURL != "postgres://user:pass@localhost:5432/db?sslmode=disable" {
		t.Fatalf("DatabaseURL = %q", cfg.DatabaseURL)
	}
}

func TestLoadAPIConfigRequiresDatabaseURL(t *testing.T) {
	t.Parallel()

	_, err := LoadAPIConfig(func(string) string { return "" })
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("error = %v, want ErrInvalidConfig", err)
	}
}
```

- [ ] **Step 2: Verify red**

Run: `go test ./internal/config`

Expected: FAIL because `LoadAPIConfig`, `APIConfig`, and `ErrInvalidConfig` are undefined.

- [ ] **Step 3: Implement config loader**

```go
var ErrInvalidConfig = errors.New("invalid config")

type APIConfig struct {
	DatabaseURL string
}

func LoadAPIConfig(getenv func(string) string) (APIConfig, error) {
	cfg := APIConfig{DatabaseURL: strings.TrimSpace(getenv("DATABASE_URL"))}
	if cfg.DatabaseURL == "" {
		return APIConfig{}, fmt.Errorf("%w: DATABASE_URL is required", ErrInvalidConfig)
	}
	return cfg, nil
}
```

- [ ] **Step 4: Verify green**

Run: `go test ./internal/config`

Expected: PASS.

### Task 2: API Startup Wiring

**Files:**
- Modify: `cmd/tflive-api/main.go`
- Create: `cmd/tflive-api/main_test.go`

- [ ] **Step 1: Write missing-config startup test**

```go
func TestRunRequiresDatabaseURL(t *testing.T) {
	t.Parallel()

	err := run(context.Background(), func(string) string { return "" })
	if !errors.Is(err, config.ErrInvalidConfig) {
		t.Fatalf("error = %v, want ErrInvalidConfig", err)
	}
}
```

- [ ] **Step 2: Verify red**

Run: `go test ./cmd/tflive-api`

Expected: FAIL because `run` is undefined or does not return the expected error.

- [ ] **Step 3: Implement startup helper**

```go
func main() {
	if err := run(context.Background(), os.Getenv); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, getenv func(string) string) error {
	cfg, err := config.LoadAPIConfig(getenv)
	if err != nil {
		return fmt.Errorf("load api config: %w", err)
	}

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("create postgres pool: %w", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("ping postgres: %w", err)
	}
	if err := postgres.Migrate(ctx, pool); err != nil {
		return fmt.Errorf("migrate postgres: %w", err)
	}

	store := postgres.NewStore(pool)
	_ = app.NewService(app.Service{
		StackTemplates: store,
		TemplateRuns:   store,
	})

	return nil
}
```

- [ ] **Step 4: Verify green**

Run: `go test ./cmd/tflive-api`

Expected: PASS.

### Task 3: Real Postgres Startup Integration

**Files:**
- Modify: `cmd/tflive-api/main_test.go`

- [ ] **Step 1: Write real DB startup test**

```go
func TestRunMigratesRealPostgresWhenDSNIsSet(t *testing.T) {
	t.Parallel()

	dsn := os.Getenv("tflive_POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("tflive_POSTGRES_TEST_DSN is not set")
	}

	err := run(context.Background(), func(key string) string {
		if key == "DATABASE_URL" {
			return dsn
		}
		return ""
	})
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}
}
```

- [ ] **Step 2: Verify red/green according to environment**

Run without DSN: `go test ./cmd/tflive-api`

Expected: PASS with integration test skipped.

Run with DSN: `tflive_POSTGRES_TEST_DSN='postgres://tflive:tflive@localhost:55432/tflive_test?sslmode=disable' go test -count=1 ./cmd/tflive-api`

Expected: PASS against the local Postgres container.

### Task 4: Full Verification And Commit

**Files:**
- All changed files.

- [ ] **Step 1: Run all tests**

Run: `go test ./...`

Expected: PASS. Postgres tests skip unless the DSN env var is provided.

- [ ] **Step 2: Run all tests with real Postgres**

Run: `tflive_POSTGRES_TEST_DSN='postgres://tflive:tflive@localhost:55432/tflive_test?sslmode=disable' go test -count=1 ./...`

Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add docs/superpowers/specs/2026-07-02-api-postgres-wiring-design.md docs/superpowers/plans/2026-07-02-api-postgres-wiring.md internal/config/config.go internal/config/config_test.go cmd/tflive-api/main.go cmd/tflive-api/main_test.go
git commit -m "Wire API startup to Postgres"
```
