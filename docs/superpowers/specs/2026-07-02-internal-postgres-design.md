# internal/postgres Design

## Purpose

Build the first useful Postgres adapter slice for the current app service. The package will implement the persistence interfaces already defined in `internal/app` and avoid designing the full product database before app use cases need it.

## Scope

This slice includes:

- A `Store` type backed by `pgxpool.Pool`.
- Concrete implementations of `app.StackTemplateRepository` and `app.TemplateRunRepository`.
- Package-owned SQL migrations for the tables needed by those interfaces.
- A migration helper that applies embedded migrations.
- Repository tests for tenant scoping, row mapping, approval eligibility, cancellation eligibility, and migration application.

This slice does not include the broader README persistence model yet. Tables such as templates, stacks, credential sets, activity events, log metadata, and stack runs will be added when app interfaces require them.

## Package Shape

`internal/postgres` will expose a small adapter API:

```go
type Store struct {
    pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store
func Migrate(ctx context.Context, pool *pgxpool.Pool) error
```

Repository methods will live on `Store`:

- `GetStackTemplate(ctx, tenantID, id)`
- `CreateTemplateRun(ctx, run)`
- `ApproveTemplateRun(ctx, approval)`
- `RequestTemplateRunCancellation(ctx, cancellation)`

`Store` may depend on `internal/app` because it is an adapter implementing app-owned ports. `internal/app` will not depend on `internal/postgres`.

## Database Tables

The first migration will create only the tables needed by the current app service:

- `stack_templates`
- `template_runs`
- `template_run_approvals`

`stack_templates` stores the fields required to hydrate `traits.StackTemplate`, including tenant scoping, selected ref, workspace name, config JSON, last applied run metadata, and lifecycle.

`template_runs` stores the fields required to persist `traits.TemplateRun`, including operation, selected ref, resolved commit SHA, workspace name, backend metadata, lifecycle status, trigger actor, timestamps, error summary, and cancellation request metadata.

`template_run_approvals` stores one approval decision per run with tenant ID, approver, and timestamp. A unique constraint on run ID prevents multiple approval rows for the same run.

Check constraints will cover known operation, stack-template lifecycle, and run-status values from `internal/traits`. Foreign keys will be intentionally limited in this first slice because parent tables such as `tenants`, `stacks`, and `templates` are outside scope.

## Repository Behavior

`GetStackTemplate` reads one tenant-owned stack template and maps the row into `traits.StackTemplate`. Missing rows return `postgres.ErrNotFound`.

`CreateTemplateRun` inserts the run produced by the app service. It does not start workflows or make other side effects.

`ApproveTemplateRun` runs in a transaction. It updates only a tenant-owned run currently in `waiting_approval`, changes the run status to `approved`, and inserts the approval record. If no eligible row is updated, it returns `app.ErrRunNotApprovable`.

`RequestTemplateRunCancellation` runs in a transaction. It updates only a tenant-owned, non-terminal run, changes the run status to `cancel_requested`, and stores cancellation actor, reason, and timestamp. If no eligible row is updated, it returns `app.ErrRunNotCancelable`.

## Error Handling

Expected domain rejections use app-level errors:

- `app.ErrRunNotApprovable`
- `app.ErrRunNotCancelable`

Missing reads use `postgres.ErrNotFound`. Unexpected database failures are wrapped with operation context so callers and logs can identify the failing database action.

## Testing

Tests will follow TDD. Integration tests will be gated by `tflive_POSTGRES_TEST_DSN`; when the variable is absent, tests that require a real Postgres instance will skip. Compile-time interface assertions will always run.

The test suite will cover:

- Migrations apply cleanly.
- `Store` satisfies current app repository interfaces.
- `GetStackTemplate` enforces tenant scoping and maps JSON and timestamps correctly.
- `CreateTemplateRun` persists queued run fields.
- Approval only succeeds for `waiting_approval` runs.
- Cancellation only succeeds for non-terminal runs.

## Dependencies

Use `github.com/jackc/pgx/v5` and `pgxpool` for Postgres access. No SQL generation tool is introduced in this slice.
