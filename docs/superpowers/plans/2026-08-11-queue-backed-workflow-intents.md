# Queue-Backed Workflow Intents Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move template registration, template-run start, approval, and cancellation delivery behind the durable `work_queue`, retire the duplicate `workflow_outbox` implementation, and make the API independent of Temporal.

**Architecture:** Domain writes and event payloads are committed together through `UnitOfWork`; queue handlers own only the external Temporal call (with cancellation reconciliation as its explicit exception). The API registers pure queue specs for enqueueing, while the worker registers the same specs plus four handlers and runs one queue controller.

**Tech Stack:** Go, PostgreSQL/pgx, Temporal SDK, the repository’s generic `internal/queue` controller, and pgx-backed migration tests.

## Global Constraints

- Every new kind is `queue.ModeJob`.
- Persisted resource keys are frozen contracts: use `run:<tenant>/<run>` and `registration:<tenant>/<registration>` exactly.
- New workflow payload structs have no JSON tags, so JSON uses verbatim Go field names.
- `UnitOfWork.InTx` must contain the domain write and its queue enqueue; any returned error rolls both back.
- Queue handlers must be idempotent and must pass the delivery context into Temporal and reconciliation calls.
- `PutTemplateRunLog` remains object-store-first and receives a comment documenting Temporal activity retry as its convergence mechanism.
- The API must not dial Temporal or construct a Temporal dispatcher.
- The worker must run one queue delivery loop; the old `internal/dispatch` loop is deleted.

---

### Task 1: Define the four workflow-intent contracts and handlers

**Files:**
- Create: `internal/app/start_template_run_handler.go`
- Create: `internal/app/start_template_sync_handler.go`
- Create: `internal/app/signal_run_approval_handler.go`
- Create: `internal/app/signal_run_cancellation_handler.go`
- Create: `internal/app/workflow_intent_handlers_test.go`
- Create: `internal/app/queue_specs.go`
- Create: `internal/app/queue_specs_test.go`

**Interfaces:**
- Consumes: `app.WorkflowDispatcher`, `app.TemplateRunCancellationReconciler`, `queue.Item`, and the four existing traits input/signal types.
- Produces: `KindStartTemplateRun`, `KindStartTemplateSync`, `KindSignalRunApproval`, `KindSignalRunCancellation`; four exported payload/spec values; `NewStartTemplateRunHandler`, `NewStartTemplateSyncHandler`, `NewSignalRunApprovalHandler`, `NewSignalRunCancellationHandler`; `TemplateRunCancellationReconciler`; and `QueueSpecs() []queue.Spec`.

- [ ] **Step 1: Write failing handler tests.** Use a recording fake implementing `WorkflowDispatcher` and a cancellation reconciler fake. Assert each handler decodes its payload and invokes exactly its matching method, dispatcher errors are returned, and cancellation treats `serviceerror.NotFound` as reconciliation success while returning other errors. Assert key derivation returns the frozen `run:` or `registration:` key and rejects malformed payloads.

```go
func TestSignalRunCancellationHandlerReconcilesClosedWorkflow(t *testing.T) {
	workflowErr := serviceerror.NewNotFound("closed")
	dispatcher := &recordingWorkflowDispatcher{cancelErr: workflowErr}
	reconciler := &recordingCancellationReconciler{}
	handler := NewSignalRunCancellationHandler(dispatcher, reconciler)
	payload, err := json.Marshal(SignalRunCancellationPayload{
		TenantID: "tenant-1", RunID: "run-1",
		Signal: traits.CancelSignal{RequestedBy: "user-1", Reason: "operator requested"},
	})
	if err != nil { t.Fatal(err) }

	followUps, err := handler.Deliver(context.Background(), queue.Item{Payload: payload})
	if err != nil { t.Fatalf("Deliver returned error: %v", err) }
	if len(followUps) != 0 { t.Fatalf("follow-ups = %v, want none", followUps) }
	if reconciler.tenantID != "tenant-1" || reconciler.runID != "run-1" {
		t.Fatalf("reconcile target = %s/%s", reconciler.tenantID, reconciler.runID)
	}
}
```

- [ ] **Step 2: Run the focused handler tests and verify the expected red failure.**

Run: `go test ./internal/app -run 'Test(StartTemplate|SignalRun|QueueSpecs)'`

Expected: FAIL because the new handler constructors, payloads, and specs do not exist yet.

- [ ] **Step 3: Implement the four handler files.** Each handler must unmarshal its event payload, call the dispatcher directly, and return no follow-ups. `SignalRunCancellationHandler` must move `isWorkflowClosedError` out of `service.go`, return success after `ReconcileTemplateRunCancellation` for `serviceerror.NotFound`, and propagate every other dispatcher/reconciler error. Key functions must parse the payload and format the exact frozen keys.

- [ ] **Step 4: Add the shared spec list and make its test assert all seven kinds.** `QueueSpecs` returns the four new specs plus `GrantStackOwnerSpec`, `MarkStackReadySpec`, and `authz.StackGrantSpec`; the test compares the set of kinds and verifies every spec has `ModeJob` or its existing declared mode and a non-nil key.

- [ ] **Step 5: Run the focused tests and format the new files.**

Run: `gofmt -w internal/app/start_template_run_handler.go internal/app/start_template_sync_handler.go internal/app/signal_run_approval_handler.go internal/app/signal_run_cancellation_handler.go internal/app/queue_specs.go internal/app/workflow_intent_handlers_test.go internal/app/queue_specs_test.go && go test ./internal/app -run 'Test(StartTemplate|SignalRun|QueueSpecs)'`

Expected: PASS once the environment has a functioning Go standard library; otherwise retain the exact toolchain error separately from code failures.

- [ ] **Step 6: Commit the handler contract.**

```bash
git add internal/app docs/superpowers/plans/2026-08-11-queue-backed-workflow-intents.md
git commit -m "feat: add queued workflow intent handlers"
```

### Task 2: Extend transaction-scoped repositories and make service writes atomic

**Files:**
- Modify: `internal/app/service.go`
- Modify: `internal/postgres/unitofwork.go`
- Modify: `internal/postgres/repositories.go`
- Modify: `internal/app/service_test.go`
- Modify: `internal/postgres/store_test.go`
- Modify: `internal/api/server_test.go`
- Modify: `cmd/api/main_test.go`

**Interfaces:**
- Consumes: the four queue kinds/payloads from Task 1 and existing `UnitOfWork.InTx`.
- Produces: `TxRepo.CreateTemplateRun`, `TxRepo.CreateTemplateRegistration`, `TxRepo.ApproveTemplateRun`, and `TxRepo.RequestTemplateRunCancellation`, each callable by a transaction-bound repository.

- [ ] **Step 1: Write failing service tests for transactional intent pairing.** Extend the existing recording unit-of-work fake to capture repository and enqueuer calls. Add one test per use case proving the callback performs the domain write and enqueue, plus rollback-oriented tests proving a domain error prevents enqueue. Update approval/cancellation expectations so no workflow dispatcher method is called by the service.

```go
func TestRegisterTemplatePersistsAndEnqueuesInOneTransaction(t *testing.T) {
	work := &recordingUnitOfWork{}
	service := testService(t, work)
	registration, err := service.RegisterTemplate(testActorContext(), RegisterTemplateCommand{
		TenantID: "tenant-1", RepoOwner: "org", RepoName: "repo", SourceRef: "main", RootPath: ".",
	})
	if err != nil { t.Fatal(err) }
	if work.inTxCalls != 1 || work.createdRegistration.ID != registration.ID {
		t.Fatalf("transaction calls=%d registration=%+v", work.inTxCalls, work.createdRegistration)
	}
	if work.enqueued[0].Kind != KindStartTemplateSync { t.Fatalf("kind = %q", work.enqueued[0].Kind) }
}
```

- [ ] **Step 2: Run the focused service tests to verify they fail for the missing transaction behavior.**

Run: `go test ./internal/app -run 'Test(RegisterTemplate|StartTemplateRun|ApproveRun|CancelRun|CreateStack)'`

Expected: FAIL because the current methods call standalone repositories and Temporal directly.

- [ ] **Step 3: Extract transaction-safe repository bodies.** Add free functions over the existing `pgxExecutor`/`pgx.Tx` seam for template registration, template run insertion, approval, and cancellation. The standalone methods open/commit their own transaction where they currently do; the new `txRepo` methods call the same functions without opening another transaction. Preserve `ErrRunNotApprovable` and `ErrRunNotCancelable` on zero-row updates.

- [ ] **Step 4: Change the four service methods to single `Work.InTx` callbacks.** Marshal the exact payloads from the immutable domain values and enqueue them with the appropriate `Kind`, actor subject, and tenant. `StartTemplateRun` must carry the complete `traits.TemplateRunWorkflowInput`; `RegisterTemplate` must carry the complete `traits.TemplateSyncWorkflowInput`; approval and cancellation payloads must carry tenant, run, and signal event data. Move successful `CreateStack` audit append into its existing transaction and remove only the successful approval audit’s best-effort separate write; failed-access audit calls remain best effort.

- [ ] **Step 5: Run focused service tests and refactor fakes until green.**

Run: `go test ./internal/app -run 'Test(RegisterTemplate|StartTemplateRun|ApproveRun|CancelRun|CreateStack)'`

Expected: PASS with no direct calls to `service.Workflows` from the four service methods.

- [ ] **Step 6: Add store transaction tests for the four tx-scoped writes.** Reuse the existing pgx test database helpers to prove each txRepo write commits with the enclosing transaction and rolls back when the callback returns an error. Update the template-run persistence test to assert no `workflow_outbox` row is written.

- [ ] **Step 7: Run the store tests and format changed Go files.**

Run: `gofmt -w internal/app/service.go internal/postgres/unitofwork.go internal/postgres/repositories.go internal/app/service_test.go internal/postgres/store_test.go internal/api/server_test.go cmd/api/main_test.go && go test ./internal/postgres -run 'Test(CreateTemplateRun|ApproveTemplateRun|RequestTemplateRunCancellation|UnitOfWork|TemplateRun)'`

- [ ] **Step 8: Commit the atomic service/repository changes.**

```bash
git add internal/app/service.go internal/postgres/unitofwork.go internal/postgres/repositories.go internal/app/service_test.go internal/postgres/store_test.go internal/api/server_test.go cmd/api/main_test.go
git commit -m "feat: enqueue workflow intents with domain writes"
```

### Task 3: Retire the duplicate workflow outbox and document log convergence

**Files:**
- Create: `internal/postgres/migrations/0014_retire_workflow_outbox.sql`
- Modify: `internal/postgres/repositories.go`
- Modify: `internal/postgres/store_test.go`
- Modify: `internal/artifacts/store.go`
- Delete: `internal/dispatch/dispatcher.go`
- Delete: `internal/dispatch/dispatcher_test.go`

**Interfaces:**
- Consumes: the `work_queue` schema from migration 0012 and the complete `traits.TemplateRunWorkflowInput` payload contract.
- Produces: one migration that backfills pending workflow starts and drops `workflow_outbox`; no `ClaimTemplateRun`, `CompleteTemplateRun`, `RetryTemplateRun`, or `dispatch.Outbox` symbols.

- [ ] **Step 1: Write the migration test before the migration.** Apply migrations 0001 through 0013 with the existing `applyMigration` helper, seed the two `template_runs` rows, their `template_revisions`, and the matching pending/processed `workflow_outbox` rows, then apply 0014. Query `work_queue`, decode the pending payload into `traits.TemplateRunWorkflowInput`, assert `RunID`, `TenantID`, `StackTemplateID`, `Operation`, `SelectedRef`, `WorkspaceName`, `RepoOwner`, `RepoName`, `RootPath`, and `ConfigJSON`, assert the resource key is `run:tenant-1/run-pending`, assert the processed row was not backfilled, and assert `to_regclass('workflow_outbox')` is null.

```go
func TestRetireWorkflowOutboxBackfillsPendingStarts(t *testing.T) {
	ctx := context.Background()
	pool := openTestPool(t, ctx)
	for _, version := range []string{
		"0001_app_repositories", "0002_template_run_logs", "0003_template_registration",
		"0004_stacks", "0005_stack_templates_created_by", "0006_template_sources_and_revision_pointers",
		"0007_workflow_outbox", "0008_authorization_outbox", "0009_security_audit",
		"0010_scoped_credentials", "0011_terraform_command_statuses", "0012_work_queue", "0013_stack_status",
	} {
		if err := applyMigration(ctx, pool, version, "migrations/"+version+".sql"); err != nil { t.Fatal(err) }
	}
	_, err := pool.Exec(ctx, `insert into template_revisions
		(id, tenant_id, repo_owner, repo_name, source_ref, resolved_commit_sha, root_path, name, status)
		values ('revision-1', 'tenant-1', 'org', 'repo', 'main', 'sha-1', '.', 'Demo', 'active'),
		       ('revision-2', 'tenant-1', 'org', 'repo', 'main', 'sha-2', '.', 'Demo 2', 'active')`)
	if err != nil { t.Fatal(err) }
	_, err = pool.Exec(ctx, `insert into template_runs
		(id, tenant_id, stack_template_id, template_revision_id, operation, selected_ref, workspace_name, config_json, status, trigger_actor)
		values ('run-pending', 'tenant-1', 'stack-template-1', 'revision-1', 'plan', 'main', 'demo', '{"x":1}', 'queued', 'user-1'),
		       ('run-processed', 'tenant-1', 'stack-template-2', 'revision-2', 'apply', 'release', 'demo-2', '{"x":2}', 'queued', 'user-1')`)
	if err != nil { t.Fatal(err) }
	_, err = pool.Exec(ctx, `insert into workflow_outbox (id, event_type, aggregate_id)
		values ('template-run/tenant-1/run-pending', 'start_template_run', 'run-pending'),
		       ('template-run/tenant-1/run-processed', 'start_template_run', 'run-processed')`)
	if err != nil { t.Fatal(err) }
	_, err = pool.Exec(ctx, `update workflow_outbox set processed_at = now() where aggregate_id = 'run-processed'`)
	if err != nil { t.Fatal(err) }
	if err := applyMigration(ctx, pool, "0014_retire_workflow_outbox", "migrations/0014_retire_workflow_outbox.sql"); err != nil { t.Fatal(err) }

	var payload []byte
	var key string
	if err := pool.QueryRow(ctx, `select resource_key, payload from work_queue`).Scan(&key, &payload); err != nil { t.Fatal(err) }
	var input traits.TemplateRunWorkflowInput
	if err := json.Unmarshal(payload, &input); err != nil { t.Fatal(err) }
	if key != "run:tenant-1/run-pending" || input.RunID != "run-pending" || input.RepoOwner != "org" || input.ConfigJSON.String() != `{"x":1}` {
		t.Fatalf("backfilled intent = key %q input %+v", key, input)
	}
	var table *string
	if err := pool.QueryRow(ctx, `select to_regclass('workflow_outbox')::text`).Scan(&table); err != nil { t.Fatal(err) }
	if table != nil { t.Fatalf("workflow_outbox still exists: %q", *table) }
}
```

- [ ] **Step 2: Run the migration test and verify the expected red failure.**

Run: `go test ./internal/postgres -run TestRetireWorkflowOutboxBackfillsPendingStarts`

Expected: FAIL because migration 0014 and the new backfill do not exist.

- [ ] **Step 3: Implement migration 0014.** Insert only unprocessed `workflow_outbox` rows by joining `template_runs` to `template_revisions` exactly as the existing start path does. Build JSON with `jsonb_build_object` using the verbatim Go field names (`RunID`, `TenantID`, `StackTemplateID`, `Operation`, `SelectedRef`, `WorkspaceName`, `RepoOwner`, `RepoName`, `RootPath`, `ConfigJSON`), set the frozen run key, use `on conflict ... do nothing`, then drop `workflow_outbox`.

- [ ] **Step 4: Delete the legacy repository methods and orphan helper.** Remove the `internal/dispatch` import, `authorizationOutboxID`, and the three workflow-outbox claim/complete/retry methods. Remove their old tests and update migration fixture expectations that previously required `workflow_outbox` to exist.

- [ ] **Step 5: Add the explicit `PutTemplateRunLog` comment.** State that object persistence and metadata are intentionally ordered object-first, that the deterministic key plus upsert make retries idempotent, and that the surrounding Temporal activity retry is the convergence mechanism; do not enqueue metadata.

- [ ] **Step 6: Run migration and repository tests, then format.**

Run: `gofmt -w internal/postgres/repositories.go internal/postgres/store_test.go internal/artifacts/store.go && go test ./internal/postgres ./internal/artifacts -run 'Test(RetireWorkflowOutbox|WorkQueue|CreateTemplateRun|PutTemplateRunLog)'`

- [ ] **Step 7: Commit the outbox retirement.**

```bash
git add internal/postgres/migrations/0014_retire_workflow_outbox.sql internal/postgres/repositories.go internal/postgres/store_test.go internal/artifacts/store.go internal/dispatch
git commit -m "refactor: retire workflow outbox"
```

### Task 4: Unify binary wiring and run one queue controller

**Files:**
- Modify: `cmd/api/main.go`
- Modify: `cmd/api/main_test.go`
- Modify: `cmd/worker/main.go`
- Modify: `cmd/worker/main_test.go`
- Modify: `internal/app/queue_specs.go`
- Modify: `internal/dispatch` references in worker tests and interfaces

**Interfaces:**
- Consumes: `app.QueueSpecs`, the four handler constructors, `queue.NewRegistry`, and `temporal.NewDispatcher`.
- Produces: API dependencies without `dialTemporal`/`newDispatcher`, worker dependencies without `dispatch.Outbox`/`newOutboxDispatcher`/`newWorkflowStarter`, and a single queue controller serving all seven kinds.

- [ ] **Step 1: Write failing wiring tests.** Update API dependency tests to assert startup never invokes a Temporal dialer and that the store gets all `app.QueueSpecs`. Update worker wiring tests to assert the worker builds a single controller with the four dispatcher-backed handlers plus the existing three, starts one delivery loop, and no longer requests an outbox dispatcher.

- [ ] **Step 2: Run API and worker wiring tests to see the expected red failures.**

Run: `go test ./cmd/api ./cmd/worker -run 'Test(Default|Run|Worker|Queue)'`

Expected: FAIL because the current dependency structs still require Temporal in the API and two dispatch loops in the worker.

- [ ] **Step 3: Simplify API wiring.** Remove the Temporal SDK/client imports, `dialTemporal`, `newDispatcher`, the Temporal dial/close block, and `Workflows: dispatcher`. Build the store with `queue.NewSpecRegistry(app.QueueSpecs()...)` and construct the service without a Temporal dependency.

- [ ] **Step 4: Simplify worker wiring.** Remove `internal/dispatch`, its worker-store methods, outbox dependency factories, and the second goroutine. Keep one Temporal dispatcher created from the worker’s Temporal client, construct the four new handlers with it, add them to the same registry as the stack/OpenFGA handlers, and run one `queue.Controller` loop.

- [ ] **Step 5: Run wiring tests and the package tests.**

Run: `gofmt -w cmd/api/main.go cmd/api/main_test.go cmd/worker/main.go cmd/worker/main_test.go internal/app/queue_specs.go && go test ./cmd/api ./cmd/worker ./internal/queue ./internal/temporal`

- [ ] **Step 6: Commit the binary wiring changes.**

```bash
git add cmd/api cmd/worker internal/app/queue_specs.go
git commit -m "refactor: run workflow intents through work queue"
```

### Task 5: Full verification and requirement review

**Files:**
- Modify only files required by verification fixes; do not broaden scope.

- [ ] **Step 1: Re-read the design and check every requirement against the diff.** Confirm four new kinds, frozen keys, full start/sync payloads, direct dispatcher handler dependencies, cancellation `NotFound` reconciliation, tx-scoped writes, transactional CreateStack audit, migration backfill/drop, deleted legacy dispatch path, shared specs, single worker loop, and unchanged template-run log behavior.

- [ ] **Step 2: Run formatting and static source checks.**

Run: `gofmt -w $(rg --files cmd internal -g '*.go')`

Run: `rg -n "workflow_outbox|ClaimTemplateRun|CompleteTemplateRun|RetryTemplateRun|internal/dispatch|dialTemporal|newDispatcher|service\.Workflows\.(StartTemplateSync|ApproveTemplateRun|CancelTemplateRun)" cmd internal`

Expected: only historical migration/spec documentation references remain where intentional; no live Go wiring or repository symbols remain.

- [ ] **Step 3: Run focused tests.**

Run: `go test ./internal/app ./internal/postgres ./internal/queue ./internal/temporal ./cmd/api ./cmd/worker`

- [ ] **Step 4: Run the full backend suite.**

Run: `go test ./...`

Expected: exit 0 with all packages passing. If the Go environment still reports missing standard-library packages, report that as an environment blocker with the exact output and separately report any code-level failures.

- [ ] **Step 5: Inspect the final diff and status.**

Run: `git diff --check && git status --short && git diff --stat`

Expected: no whitespace errors, only scoped implementation changes, and no accidental credential/config changes.
