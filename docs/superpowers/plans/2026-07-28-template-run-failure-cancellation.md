# Template Run Failure and Cancellation Recovery Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox syntax for tracking.

**Goal:** Persist activity failures and closed-workflow cancellation as terminal run outcomes so logical locks clear and the UI re-enables actions.

**Architecture:** Temporal remains the source of detailed activity history; Postgres stores the durable run outcome. Add a workflow failure finalizer, extend status persistence with an error summary, and add atomic reconciliation when cancellation cannot signal a live workflow. No terminate operation.

**Tech Stack:** Go, Temporal Go SDK, PostgreSQL/pgx, React, TanStack Query, Vitest.

## Global Constraints

- Final unexplained failures use terminal status failed.
- completed, failed, and canceled remain the only terminal statuses.
- lock_released is intermediate; final failed means the logical lock is clear.
- Cancel signals the workflow; the workflow cancels the activity context.
- Terminate is out of scope.
- Error summaries must not expose credentials.

---

### Task 1: Persist failure summaries and reconcile closed cancellation

**Files:**
- Modify: internal/traits/traits.go around TemplateRunStatusActivityInput
- Modify: internal/postgres/repositories.go around RecordTemplateRunStatus
- Test: internal/postgres/store_test.go

**Interfaces:**
- Add ErrorSummary string to TemplateRunStatusActivityInput.
- Add ReconcileTemplateRunCancellation(ctx, tenantID, runID, errorSummary) error to the repository interface and Store.

- [ ] Write failing repository tests for terminal failure persistence and closed-cancellation reconciliation.
- [ ] Run: rtk go test ./internal/postgres -run 'TestRecordTemplateRunStatus|TestReconcileTemplateRunCancellation'. Expect failure because the field and method do not exist.
- [ ] Implement terminal persistence so failed writes error_summary and completed_at. Add an atomic SQL update restricted to status = cancel_requested that sets failed, error_summary, and completed_at. Return ErrRunNotCancelable when no row is updated.
- [ ] Run the focused tests again; expect PASS.
- [ ] Commit: git add internal/traits/traits.go internal/postgres/repositories.go internal/postgres/store_test.go && git commit -m "feat: persist template run failure summaries"

### Task 2: Finalize failed template-run workflows

**Files:**
- Modify: internal/workflows/template_run.go around TemplateRunWorkflow and recordStatus
- Test: internal/workflows/template_run_workflow_test.go

**Interfaces:**
- Add a workflow helper recordFailure(rootErr error) error.
- Pass ErrorSummary through the status activity input.

- [ ] Write failing workflow tests for plan/apply/destroy activity failures. Mock the target activity error, capture status inputs, and assert the final input has status failed and an error summary containing the root error.
- [ ] Add a test where failure-status persistence fails and assert the original activity error remains the root workflow error.
- [ ] Run: rtk go test ./internal/workflows -run 'TestTemplateRunWorkflow.*Failure|TestTemplateRunWorkflow.*ActivityError'. Expect failure because errors currently return without recording failed.
- [ ] Implement TemplateRunWorkflow cleanup: preserve errTemplateRunCanceled; otherwise call recordFailure before returning the original error. Do not use the canceled activity context for cleanup. Keep failed as the final status rather than lock_released.
- [ ] Run: rtk go test ./internal/workflows. Expect PASS, including existing success, retry, approval, and cancellation tests.
- [ ] Commit: git add internal/workflows/template_run.go internal/workflows/template_run_workflow_test.go && git commit -m "fix: finalize failed template runs"

### Task 3: Reconcile cancellation when Temporal is closed

**Files:**
- Modify: internal/app/service.go in CancelRun and TemplateRunRepository
- Modify: internal/temporal/dispatcher.go only if closed-workflow errors need classification
- Test: internal/app/service_test.go and internal/temporal/dispatcher_test.go

**Interfaces:**
- Consume ReconcileTemplateRunCancellation from Task 1.
- Keep live-workflow cancellation on the existing signal path.

- [ ] Write a service test where the repository records cancel_requested and the dispatcher returns a narrowly classified Temporal not-found/closed error. Assert reconciliation is called and CancelRun returns nil.
- [ ] Add tests proving timeouts, unavailable Temporal, and unknown dispatcher errors are still returned rather than guessed as terminal.
- [ ] Run: rtk go test ./internal/app -run 'TestCancelRun.*Closed|TestCancelRun.*Reconcile|TestCancelRun.*NotCancelable'. Expect failure because every signal error currently escapes.
- [ ] Implement closed-workflow classification and reconciliation with the stable summary "workflow closed before cancellation was processed". Keep reconciliation conditional on cancel_requested so repeated calls are safe.
- [ ] Run: rtk go test ./internal/app ./internal/temporal. Expect PASS.
- [ ] Commit: git add internal/app/service.go internal/app/service_test.go internal/temporal/dispatcher.go internal/temporal/dispatcher_test.go && git commit -m "fix: reconcile cancellation after closed workflows"

### Task 4: Verify API/UI recovery behavior

**Files:**
- Modify: internal/api/server_test.go only if response coverage is needed
- Test/Modify: web/src/features/runs/RunDetailScreen.test.tsx
- Test/Modify: web/src/features/runs/RunsListRow.test.tsx
- Test/Modify: web/src/api/queries.test.tsx

**Interfaces:**
- Consume terminal failed runs and stable cancellation responses from Tasks 1–3.
- Do not add a Terminate control or change capability gates.

- [ ] Add a failed-run fixture with error_summary and assert RunDetailScreen renders it.
- [ ] Add a failed latest-run fixture and assert RunsListRow enables Plan.
- [ ] Assert cancellation success invalidates the run-history query and no Terminate control is rendered.
- [ ] Run: rtk npm test -- --run web/src/features/runs/RunDetailScreen.test.tsx web/src/features/runs/RunsListRow.test.tsx web/src/api/queries.test.tsx. Expect new assertions to fail before fixture/wiring updates.
- [ ] Reuse existing error rendering and terminal-status helpers; update only fixtures and mutation expectations needed for the new backend response.
- [ ] Run: rtk npm test -- --run web/src/features/runs/RunDetailScreen.test.tsx web/src/features/runs/RunsListRow.test.tsx web/src/api/queries.test.tsx && rtk npm run build. Expect PASS.
- [ ] Commit: git add internal/api/server_test.go web/src/features/runs/RunDetailScreen.test.tsx web/src/features/runs/RunsListRow.test.tsx web/src/api/queries.test.tsx && git commit -m "test: verify template run recovery states"

### Task 5: Full verification

**Files:**
- Test only: existing Go and frontend suites

- [ ] Run: rtk go test ./internal/activities ./internal/workflows ./internal/postgres ./internal/app ./internal/api ./internal/temporal.
- [ ] Run: rtk npm test -- --run && rtk npm run build.
- [ ] Run: rtk git diff --check and rtk git status --short. Confirm only intended files changed.
- [ ] Commit any verified test-only correction with a specific file list and message.

