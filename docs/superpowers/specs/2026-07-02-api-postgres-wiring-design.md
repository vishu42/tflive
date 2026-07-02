# API Postgres Wiring Design

## Goal

Wire the API command to the Postgres persistence slice so startup can validate database configuration, connect to Postgres, run embedded migrations, and construct the app service with real repository adapters.

## Scope

This slice does not add HTTP routes, Temporal dispatch, or production run ID generation. Those adapters do not exist yet. The API process will bootstrap the database-backed service and then return successfully, leaving long-running server behavior for a later slice.

## Configuration

`internal/config` exposes `APIConfig` with a required `DatabaseURL` field. `LoadAPIConfig(getenv func(string) string)` reads `DATABASE_URL`, validates it is present, and returns a named validation error when it is missing.

The loader accepts a `getenv` function so command startup can be tested without mutating process-wide environment state.

## API Startup Wiring

`cmd/megagega-api` owns startup composition:

- Load `APIConfig`.
- Parse and create a `pgxpool.Pool`.
- Ping the pool to fail fast on bad connectivity.
- Run `postgres.Migrate(ctx, pool)`.
- Build `postgres.NewStore(pool)`.
- Construct `app.NewService` with the store assigned to both `StackTemplates` and `TemplateRuns`.

The pool is closed by the startup helper before it returns. This is acceptable until the API process becomes a long-running HTTP server; the future server slice will keep the pool alive for the process lifetime.

## Errors

Startup wraps each failure with the phase that failed: load config, create Postgres pool, ping Postgres, migrate Postgres, or wire service. Config validation exposes `config.ErrInvalidConfig` for tests and callers.

## Testing

Unit tests cover:

- `LoadAPIConfig` returns `DATABASE_URL`.
- Missing `DATABASE_URL` fails with `config.ErrInvalidConfig`.
- API startup fails early when the database URL is missing.

Integration tests cover:

- With `MEGAGEGA_POSTGRES_TEST_DSN`, API startup connects to the real Postgres container and applies migrations.
