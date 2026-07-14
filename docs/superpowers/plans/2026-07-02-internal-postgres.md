# internal/postgres Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the first pgx-backed Postgres adapter for the current `internal/app` repository interfaces.

**Architecture:** `internal/postgres.Store` wraps a `pgxpool.Pool` and implements the app-owned persistence ports. SQL migrations are embedded in the package and applied by `Migrate`. Integration tests use `tflive_POSTGRES_TEST_DSN` and skip without a live database.

**Tech Stack:** Go 1.24, `github.com/jackc/pgx/v5`, `pgxpool`, embedded SQL migrations, standard `testing`.

---

## File Structure

- Create `internal/postgres/store.go`: `Store`, `NewStore`, and `ErrNotFound`.
- Create `internal/postgres/migrate.go`: embedded migrations and `Migrate`.
- Create `internal/postgres/repositories.go`: app repository method implementations.
- Create `internal/postgres/migrations/0001_app_repositories.sql`: minimal schema for stack templates, template runs, and approvals.
- Create `internal/postgres/store_test.go`: compile-time interface assertions and DSN-gated integration tests.
- Modify `go.mod` and create/modify `go.sum`: add `github.com/jackc/pgx/v5`.

### Task 1: Add Store Shape And Dependency

**Files:**
- Create: `internal/postgres/store_test.go`
- Create: `internal/postgres/store.go`
- Modify: `go.mod`
- Modify: `go.sum`

- [ ] **Step 1: Write the failing compile-time interface test**

Add this to `internal/postgres/store_test.go`:

```go
package postgres

import "github.com/vishu42/tflive/internal/app"

var (
	_ app.StackTemplateRepository = (*Store)(nil)
	_ app.TemplateRunRepository   = (*Store)(nil)
)
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/postgres`

Expected: FAIL because `Store` is undefined.

- [ ] **Step 3: Add pgx dependency**

Run: `go get github.com/jackc/pgx/v5@latest`

Expected: `go.mod` gains `github.com/jackc/pgx/v5`.

- [ ] **Step 4: Add minimal Store implementation**

Create `internal/postgres/store.go`:

```go
package postgres

import (
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("postgres: not found")

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}
```

- [ ] **Step 5: Run test to verify next failure**

Run: `go test ./internal/postgres`

Expected: FAIL because `Store` does not implement the app repository methods yet.

### Task 2: Add Migrations

**Files:**
- Create: `internal/postgres/migrations/0001_app_repositories.sql`
- Create: `internal/postgres/migrate.go`
- Modify: `internal/postgres/store_test.go`

- [ ] **Step 1: Write migration test**

Add DSN-gated helpers and `TestMigrateAppliesSchema` to `internal/postgres/store_test.go`. The test should open `tflive_POSTGRES_TEST_DSN`, create a unique schema, run `Migrate`, and verify all three tables exist.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/postgres`

Expected without DSN: compile failure because `Migrate` is undefined. Expected with DSN: same compile failure.

- [ ] **Step 3: Create migration SQL**

Create `internal/postgres/migrations/0001_app_repositories.sql` with:

- `stack_templates`
- `template_runs`
- `template_run_approvals`
- check constraints for known lifecycle, operation, and run statuses
- indexes for tenant-scoped lookups

- [ ] **Step 4: Implement embedded migration runner**

Create `internal/postgres/migrate.go` with embedded SQL files, a `schema_migrations` table, and transactional application of pending migrations.

- [ ] **Step 5: Run test to verify it passes or skips**

Run: `go test ./internal/postgres`

Expected without DSN: PASS with integration tests skipped. Expected with DSN: PASS and tables exist.

### Task 3: Implement GetStackTemplate

**Files:**
- Modify: `internal/postgres/store_test.go`
- Create or modify: `internal/postgres/repositories.go`

- [ ] **Step 1: Write failing tenant-scoped lookup test**

Add `TestGetStackTemplateReturnsTenantScopedRecord` and `TestGetStackTemplateReturnsNotFoundForOtherTenant`. Seed `stack_templates`, call `store.GetStackTemplate`, and assert field mapping for IDs, selected ref, workspace, config JSON, last applied metadata, and lifecycle.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/postgres`

Expected without DSN: PASS with integration tests skipped. Expected with DSN: FAIL because `GetStackTemplate` is not implemented.

- [ ] **Step 3: Implement GetStackTemplate**

Create `internal/postgres/repositories.go` and query by `tenant_id` plus `id`. Convert `pgx.ErrNoRows` to `ErrNotFound`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/postgres`

Expected: PASS or skip when no DSN is present.

### Task 4: Implement CreateTemplateRun

**Files:**
- Modify: `internal/postgres/store_test.go`
- Modify: `internal/postgres/repositories.go`

- [ ] **Step 1: Write failing insert test**

Add `TestCreateTemplateRunPersistsRunFields`. Create a `traits.TemplateRun`, call `store.CreateTemplateRun`, then read the row back and assert operation, status, trigger actor, selected ref, workspace name, backend metadata, timestamps, and error summary.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/postgres`

Expected with DSN: FAIL because `CreateTemplateRun` is not implemented.

- [ ] **Step 3: Implement CreateTemplateRun**

Insert all `traits.TemplateRun` fields into `template_runs`. Wrap unexpected database errors with `create template run`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/postgres`

Expected: PASS or skip when no DSN is present.

### Task 5: Implement Approval Persistence

**Files:**
- Modify: `internal/postgres/store_test.go`
- Modify: `internal/postgres/repositories.go`

- [ ] **Step 1: Write failing approval tests**

Add `TestApproveTemplateRunApprovesWaitingRun` and `TestApproveTemplateRunRejectsNonWaitingRun`. Assert the success path changes status to `approved` and inserts `template_run_approvals`; assert the rejection returns `app.ErrRunNotApprovable`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/postgres`

Expected with DSN: FAIL because `ApproveTemplateRun` is not implemented.

- [ ] **Step 3: Implement ApproveTemplateRun**

Use a transaction. Update only `status = 'waiting_approval'` rows for the same tenant and run ID. Insert one approval row. Return `app.ErrRunNotApprovable` when the update affects zero rows.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/postgres`

Expected: PASS or skip when no DSN is present.

### Task 6: Implement Cancellation Persistence

**Files:**
- Modify: `internal/postgres/store_test.go`
- Modify: `internal/postgres/repositories.go`

- [ ] **Step 1: Write failing cancellation tests**

Add `TestRequestTemplateRunCancellationMarksCancelableRun` and `TestRequestTemplateRunCancellationRejectsTerminalRun`. Assert the success path changes status to `cancel_requested` and stores cancellation metadata; assert terminal status returns `app.ErrRunNotCancelable`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/postgres`

Expected with DSN: FAIL because `RequestTemplateRunCancellation` is not implemented.

- [ ] **Step 3: Implement RequestTemplateRunCancellation**

Use a transaction or single checked update. Update only rows whose status is not `completed`, `failed`, or `canceled`. Return `app.ErrRunNotCancelable` when no row is eligible.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/postgres`

Expected: PASS or skip when no DSN is present.

### Task 7: Verify Whole Repo

**Files:**
- No new files.

- [ ] **Step 1: Format Go code**

Run: `gofmt -w internal/postgres`

Expected: no output.

- [ ] **Step 2: Run focused tests**

Run: `go test ./internal/postgres`

Expected: PASS or skip DSN-gated tests.

- [ ] **Step 3: Run full suite**

Run: `go test ./...`

Expected: PASS for all packages.
