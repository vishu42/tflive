# Template Run Workflow Outbox Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Atomically persist template runs with durable workflow-start intent and dispatch that intent to Temporal from the existing worker.

**Architecture:** Postgres stores a generic workflow outbox row in the same transaction as the template run. A focused `internal/dispatch` loop claims rows with leases and invokes Temporal using deterministic workflow IDs; the existing worker hosts that loop.

**Tech Stack:** Go 1.24, pgx v5, PostgreSQL, Temporal Go SDK v1.45, standard `testing` package.

## Global Constraints

- Do not introduce Kafka, SQS, or another message broker.
- Do not hold a Postgres transaction open during a Temporal RPC.
- Keep workflow starts retry-safe through deterministic workflow IDs and reject-duplicate reuse.
- Scope the first outbox event to `start_template_run`.

---

### Task 1: Persist Run and Outbox Atomically

**Files:**
- Create: `internal/postgres/migrations/0007_workflow_outbox.sql`
- Modify: `internal/postgres/repositories.go`
- Test: `internal/postgres/store_test.go`

**Interfaces:**
- Consumes: `Store.pool`, `traits.TemplateRun`.
- Produces: `CreateTemplateRun(context.Context, traits.TemplateRun) error` with atomic outbox semantics.

- [ ] **Step 1: Write a failing integration test**

Extend `TestCreateTemplateRunPersistsRunFields` to seed the referenced immutable template revision, call `CreateTemplateRun`, and assert a pending `workflow_outbox` row with ID `template-run/tenant_123/run_123`, event type `start_template_run`, and aggregate ID `run_123`.

- [ ] **Step 2: Run the focused test and confirm RED**

Run: `go test ./internal/postgres -run TestCreateTemplateRunPersistsRunFields -count=1`

Expected: failure because `workflow_outbox` does not exist.

- [ ] **Step 3: Add the migration and transactional insert**

Create `workflow_outbox` with lease, retry, processing, and error columns plus this partial index:

```sql
create index workflow_outbox_pending_idx
on workflow_outbox (available_at, id)
where processed_at is null;
```

Change `CreateTemplateRun` to begin a pgx transaction, insert the run, insert the deterministic outbox row, and commit. Roll back on every failure.

- [ ] **Step 4: Run the focused integration test and confirm GREEN**

Run: `go test ./internal/postgres -run TestCreateTemplateRunPersistsRunFields -count=1`

Expected: PASS.

### Task 2: Add Outbox Claim, Complete, and Retry Operations

**Files:**
- Create: `internal/dispatch/dispatcher.go`
- Modify: `internal/postgres/repositories.go`
- Test: `internal/postgres/store_test.go`

**Interfaces:**
- Produces `dispatch.Entry { ID string; Input traits.TemplateRunWorkflowInput }`.
- Produces `dispatch.Outbox` with `ClaimTemplateRun`, `CompleteTemplateRun`, and `RetryTemplateRun` methods.

- [ ] **Step 1: Write failing Postgres integration tests**

Add tests that create a run/outbox record, claim it, compare every workflow input field, complete it, and verify it cannot be claimed again. Add a retry test that records `last_error`, clears the lease, and defers availability.

- [ ] **Step 2: Run the claim/retry tests and confirm RED**

Run: `go test ./internal/postgres -run 'Test(Claim|Complete|Retry)TemplateRunWorkflow' -count=1`

Expected: compile failure because the outbox API does not exist.

- [ ] **Step 3: Implement the narrow outbox port and Postgres adapter**

Claim with one CTE using `FOR UPDATE SKIP LOCKED`, update the lease and attempts, then join the claimed run to `template_revisions`. Complete by setting `processed_at`; retry by setting `available_at`, clearing the lease, and recording the error.

- [ ] **Step 4: Run the focused tests and confirm GREEN**

Run: `go test ./internal/postgres -run 'Test(Claim|Complete|Retry)TemplateRunWorkflow' -count=1`

Expected: PASS.

### Task 3: Dispatch Outbox Entries to Temporal

**Files:**
- Create: `internal/dispatch/dispatcher_test.go`
- Modify: `internal/dispatch/dispatcher.go`
- Modify: `internal/temporal/dispatcher.go`
- Test: `internal/temporal/dispatcher_test.go`

**Interfaces:**
- Consumes: `dispatch.Outbox` and `WorkflowStarter.StartTemplateRun`.
- Produces: `NewDispatcher(outbox, workflows)` and `Run(context.Context)`.

- [ ] **Step 1: Write failing dispatcher tests**

Test idle claims, successful start followed by completion, failed start followed by retry, and context cancellation. Test that Temporal start options use reject-duplicate workflow-ID reuse.

- [ ] **Step 2: Run focused tests and confirm RED**

Run: `go test ./internal/dispatch ./internal/temporal -count=1`

Expected: compile/test failure because dispatch behavior and reuse policy are missing.

- [ ] **Step 3: Implement the dispatch loop**

Use a one-second idle poll, a five-second retry delay, and a thirty-second claim lease. Drain immediately while work exists; wait only when no row is available or an error occurs. Log dispatch failures without terminating the Temporal worker process.

- [ ] **Step 4: Run focused tests and confirm GREEN**

Run: `go test ./internal/dispatch ./internal/temporal -count=1`

Expected: PASS.

### Task 4: Move API Dispatch to the Worker

**Files:**
- Modify: `internal/app/service.go`
- Test: `internal/app/service_test.go`
- Modify: `cmd/megagega-worker/main.go`
- Test: `cmd/megagega-worker/main_test.go`

**Interfaces:**
- Consumes: `dispatch.NewDispatcher`, `workerStore` implementing `dispatch.Outbox`.
- Produces: API persistence-only behavior and worker-hosted dispatch lifecycle.

- [ ] **Step 1: Write failing application and wiring tests**

Change the application test to require that `StartTemplateRun` persists the run and does not call `WorkflowDispatcher.StartTemplateRun`. Add worker dependency recording that proves the outbox dispatcher starts before the Temporal worker and is canceled after it exits.

- [ ] **Step 2: Run focused tests and confirm RED**

Run: `go test ./internal/app ./cmd/megagega-worker -count=1`

Expected: failure because the API still calls Temporal and the worker does not start the dispatcher.

- [ ] **Step 3: Implement persistence-only API behavior and worker wiring**

Remove the direct Temporal start from `Service.StartTemplateRun`. Extend `workerStore` with `dispatch.Outbox`, construct a Temporal dispatcher and outbox dispatcher from the existing client/store, run it in a cancelable goroutine, and cancel/wait after `worker.Run` exits.

- [ ] **Step 4: Run focused tests and confirm GREEN**

Run: `go test ./internal/app ./cmd/megagega-worker -count=1`

Expected: PASS.

### Task 5: Full Verification

**Files:**
- Verify all files changed by Tasks 1-4.

**Interfaces:**
- Consumes: completed implementation.
- Produces: verified build and test evidence.

- [ ] **Step 1: Format Go sources**

Run: `gofmt -w internal/dispatch/*.go internal/postgres/*.go internal/app/*.go internal/temporal/*.go cmd/megagega-worker/*.go`

- [ ] **Step 2: Run the full test suite**

Run: `go test ./...`

Expected: all Go tests pass.

- [ ] **Step 3: Run static analysis**

Run: `go vet ./...`

Expected: exit code 0 with no findings.

- [ ] **Step 4: Inspect the final diff**

Run: `git diff --check` and `git status --short`.

Expected: no whitespace errors and only scoped files changed.

### Task 6: Update Architecture Documentation

**Files:**
- Modify: `README.md`

**Interfaces:**
- Consumes: the implemented API, Postgres outbox, worker dispatcher, and Temporal idempotency behavior.
- Produces: an accurate operator and contributor description of the run-start path.

- [x] **Step 1: Correct the architecture and component responsibilities**

Update the architecture overview and diagram so template-run starts flow from the API into an atomic Postgres run/outbox transaction, then through the worker-hosted dispatcher into Temporal. Preserve direct API-to-Temporal approval and cancellation signaling.

- [x] **Step 2: Document the dispatch package and reliability contract**

Add `internal/dispatch` to the package layout and ownership list. Describe leasing with `FOR UPDATE SKIP LOCKED`, retry scheduling, deterministic Temporal workflow IDs, duplicate rejection/use-existing behavior, and migration backfill for existing queued runs.

- [x] **Step 3: Verify documentation consistency**

Run: `rg -n "starts Temporal workflows|API Server.*Temporal|internal/dispatch|workflow_outbox" README.md`

Expected: no claim that template-run creation directly starts Temporal; the dispatch package and outbox are documented.

- [x] **Step 4: Check the final diff**

Run: `git diff --check README.md`

Expected: exit code 0 with no whitespace errors.
