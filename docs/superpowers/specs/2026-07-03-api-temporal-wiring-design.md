# API Temporal Wiring Design

## Goal

Wire the API command to the Temporal dispatch adapter so API startup composes a service with real Postgres persistence and real workflow dispatch.

## Scope

This slice includes:

- Extending `internal/config.APIConfig` with Temporal API dispatch settings.
- Loading Temporal environment variables for API startup.
- Dialing Temporal from `cmd/megagega-api`.
- Constructing `temporal.NewDispatcher` with the configured task queue.
- Passing the dispatcher into `app.NewService`.
- Unit tests for config loading and startup wiring seams.

This slice does not add HTTP routes, production run ID generation, workflow implementations, worker registration, worker command wiring, or activity implementations. The API process will continue to bootstrap dependencies and return successfully; long-running server behavior remains a later slice.

## Configuration

`internal/config.APIConfig` will expose:

```go
type APIConfig struct {
	DatabaseURL       string
	TemporalAddress   string
	TemporalNamespace string
	TemporalTaskQueue string
}
```

`LoadAPIConfig(getenv func(string) string)` reads:

- `DATABASE_URL`, required.
- `TEMPORAL_ADDRESS`, required.
- `TEMPORAL_NAMESPACE`, optional.
- `TEMPORAL_TASK_QUEUE`, optional.

When `TEMPORAL_TASK_QUEUE` is empty, the loader defaults it to:

```text
terraform-runs
```

`TEMPORAL_NAMESPACE` remains empty when omitted. The Temporal adapter already defaults an empty namespace to Temporal's `default` namespace, so config loading does not duplicate that behavior.

Missing required values return an error wrapping `config.ErrInvalidConfig`, matching the existing database URL validation pattern.

## API Startup Wiring

`cmd/megagega-api` remains the composition root. Startup will:

1. Load `APIConfig`.
2. Create and ping a Postgres pool.
3. Run embedded Postgres migrations.
4. Create the Postgres store.
5. Dial Temporal with `temporal.Dial(ctx, temporal.Config{Address: cfg.TemporalAddress, Namespace: cfg.TemporalNamespace})`.
6. Defer `temporalClient.Close()`.
7. Build `temporal.NewDispatcher(temporalClient, cfg.TemporalTaskQueue)`.
8. Construct `app.NewService` with:

```go
app.Service{
	StackTemplates: store,
	TemplateRuns:   store,
	Workflows:      dispatcher,
}
```

The API startup helper still returns after successful composition. This mirrors the current Postgres wiring and keeps actual HTTP serving for a future slice.

## Test Seam

Startup tests should not require a live Temporal server. `cmd/megagega-api` will introduce a small unexported dependency seam for tests:

```go
type apiDependencies struct {
	newPostgresPool func(context.Context, string) (postgresPool, error)
	migratePostgres func(context.Context, postgresPool) error
	newStore        func(postgresPool) appRepositories
	dialTemporal    func(context.Context, temporal.Config) (temporalClient, error)
	newDispatcher   func(temporalClient, string) app.WorkflowDispatcher
	newService      func(app.Service) *app.Service
}
```

The exact helper names can vary, but the intent is fixed: production `run(ctx, getenv)` uses real pgx, Postgres migrations, `temporal.Dial`, and `temporal.NewDispatcher`; unit tests can inject fakes to verify startup sequencing and service wiring without network access.

The seam should stay inside `cmd/megagega-api`. It is not a new public API and should not spread into `internal/app`.

## Errors

Startup wraps failures with the phase that failed:

- `load api config`
- `create postgres pool`
- `ping postgres`
- `migrate postgres`
- `dial temporal`
- `wire service`

Temporal dial errors are wrapped by both the Temporal adapter and API startup. That produces context such as:

```text
dial temporal: dial temporal: ...
```

This is acceptable for the first wiring slice because it preserves both boundaries. If duplicated wording becomes noisy in real logs, a later cleanup can adjust one layer after observing actual error output.

## Testing

Config unit tests cover:

- Loading `DATABASE_URL`, `TEMPORAL_ADDRESS`, `TEMPORAL_NAMESPACE`, and `TEMPORAL_TASK_QUEUE`.
- Defaulting empty `TEMPORAL_TASK_QUEUE` to `terraform-runs`.
- Missing `DATABASE_URL` fails with `config.ErrInvalidConfig`.
- Missing `TEMPORAL_ADDRESS` fails with `config.ErrInvalidConfig`.

API command unit tests cover:

- Missing Temporal address fails during config loading.
- Startup passes the configured Temporal address and namespace into the Temporal dialer.
- Startup passes the configured or default task queue into the dispatcher constructor.
- Startup wires the dispatcher into `app.NewService`.
- Temporal dial failure is wrapped with `dial temporal`.

The existing Postgres integration test should use the startup dependency seam with a fake Temporal dialer. It should continue to require only `MEGAGEGA_POSTGRES_TEST_DSN` and should not require a live Temporal server.

## Dependencies

No new third-party dependency is introduced in this slice. It uses the existing `internal/temporal` package and the Temporal Go SDK already present in `go.mod`.
