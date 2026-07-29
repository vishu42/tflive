# Temporal Worker Session Affinity Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Keep all filesystem-dependent activities for one `TemplateRun` on the same Temporal worker replica while allowing multiple replicas to share the task queue.

**Architecture:** Enable Temporal Go SDK session workers in every `cmd/tflive-worker` process. Create one session per `TemplateRunWorkflow`, route workspace activities through the session context, and keep status/failure persistence on the normal workflow context. A session spans approval waits because apply and destroy reuse workspace state after approval; a worker crash still fails the session and is reported as a failed run.

**Tech Stack:** Go 1.24, Temporal Go SDK v1.45.0, Temporal workflow test suite, standard `testing` package.

## Global Constraints

- Keep the existing shared `terraform-runs` task queue and Postgres outbox dispatcher behavior.
- Do not introduce a shared filesystem requirement for worker replicas.
- Keep logs and artifacts in the existing configured artifact store.
- Use a 1-minute session creation timeout and a 24-hour session execution timeout.
- Preserve existing approval, cancellation, retry, status, and failure-persistence behavior.
- Do not claim automatic recovery after the worker owning a session crashes.

---

### Task 1: Enable Session Workers in Worker Wiring

**Files:**
- Modify: `cmd/worker/main.go:28-110,209`
- Test: `cmd/worker/main_test.go:51-128,291-417,530-539`

**Interfaces:**
- Consumes: existing `workerDependencies.newWorker` injection seam and `temporalworker.New`.
- Produces: worker construction that passes `worker.Options{EnableSessionWorker: true}` to every worker replica.

- [ ] **Step 1: Extend the worker factory seam with Temporal options**

Change the private factory signature from:

```go
newWorker func(client.Client, string) temporalWorker
```

to:

```go
newWorker func(client.Client, string, temporalworker.Options) temporalWorker
```

Pass the options from `runWithDependencies` and update the recording dependency factory to capture them.

- [ ] **Step 2: Write the failing worker-wiring assertion**

In `TestRunWiresTemporalWorker`, assert that the captured options enable sessions:

```go
if !deps.workerOptions.EnableSessionWorker {
	 t.Fatal("session worker was not enabled")
}
```

- [ ] **Step 3: Run the focused worker test and verify it fails**

Run: `go test ./cmd/worker -run TestRunWiresTemporalWorker -count=1`

Expected: FAIL because the default worker factory still receives zero-valued options.

- [ ] **Step 4: Implement the minimal session-worker wiring**

Update the default factory to pass the received options to `temporalworker.New`, and invoke it with:

```go
temporalworker.Options{EnableSessionWorker: true}
```

- [ ] **Step 5: Run the focused worker tests and verify they pass**

Run: `go test ./cmd/worker -count=1`

Expected: PASS.

- [ ] **Step 6: Commit the worker wiring**

```bash
git add cmd/worker/main.go cmd/worker/main_test.go
git commit -m "feat: enable temporal session workers"
```

### Task 2: Route TemplateRun Workspace Activities Through a Session

**Files:**
- Modify: `internal/workflows/template_run.go:47-184,301-352`
- Test: `internal/workflows/template_run_workflow_test.go:1-437`

**Interfaces:**
- Consumes: `workflow.CreateSession`, `workflow.CompleteSession`, existing `templateRunWorkflow` activity helpers, and the Temporal workflow test environment.
- Produces: `TemplateRunWorkflow` with a session context for `PrepareWorkspace`, `FetchSource`, and every `RunTerraform` activity, while `recordStatus` and failure persistence use the base context.

- [ ] **Step 1: Add a workflow regression test that records activity task queues**

Create a focused plan workflow test with session workers enabled. Capture `activity.GetInfo(ctx).TaskQueue` from the workspace activities and from `RecordTemplateRunStatus`. Assert that all workspace activities use the same queue and that it differs from the normal status queue. Register the existing activity names and return deterministic workspace paths:

```go
env.SetWorkerOptions(worker.Options{EnableSessionWorker: true})
// PrepareWorkspace -> WorkspacePath: "run/workspace"
// FetchSource -> TerraformPath: "run/workspace/source"
// RunTerraform -> nil
// RecordTemplateRunStatus -> capture the normal task queue
```

- [ ] **Step 2: Run the new workflow test and verify it fails**

Run: `go test ./internal/workflows -run TestTemplateRunWorkflowUsesSessionForWorkspaceActivities -count=1`

Expected: FAIL because the current workflow schedules all activities on the ordinary task queue.

- [ ] **Step 3: Add separate base and session workflow contexts**

Keep `templateRunWorkflow.ctx` as the base context for signals, status updates, and failure persistence. Add `sessionCtx workflow.Context` for workspace-dependent activities.

At the beginning of `TemplateRunWorkflow`, create the session after applying the existing baseline activity options:

```go
sessionCtx, err := workflow.CreateSession(ctx, &workflow.SessionOptions{
	CreationTimeout:  time.Minute,
	ExecutionTimeout: 24 * time.Hour,
})
```

If creation fails, record the failure using the base context and return. After `run.execute()` returns, call `workflow.CompleteSession(sessionCtx)` before recording any failure so status persistence is not canceled by session completion.

- [ ] **Step 4: Route only filesystem-dependent activities through the session**

Use `run.sessionCtx` in `prepareLocalWorkspace`, `fetchSource`, and `runTerraform`. Keep `waitForApproval`, `recordStatusWithSummary`, `cancel`, `complete`, and `recordFailure` on `run.ctx`. In `runTerraform`, derive activity cancellation from `run.sessionCtx`, but continue selecting on signals from `run.ctx` and retrieve the future result with `run.ctx` so cancellation and session failure are surfaced to the workflow.

- [ ] **Step 5: Run the focused workflow tests and verify they pass**

Run: `go test ./internal/workflows -count=1`

Expected: PASS, including the new session-routing test and all existing approval, cancellation, retry, and failure tests.

- [ ] **Step 6: Commit the workflow session change**

```bash
git add internal/workflows/template_run.go internal/workflows/template_run_workflow_test.go
git commit -m "feat: pin template run activities to worker sessions"
```

### Task 3: Document Replica and Failure Semantics

**Files:**
- Modify: `docs/architecture.md:159-180,786-810`
- Modify: `.env.example:149-157`

**Interfaces:**
- Consumes: the implemented Temporal session behavior and existing S3 artifact-store configuration.
- Produces: operator-facing documentation that explains local workspace affinity, session duration, and worker-crash limitations.

- [ ] **Step 1: Update the worker architecture section**

Document that workspace-dependent activities in one `TemplateRun` use a Temporal Session and remain on one worker replica, while status activities and logs remain durable through Postgres and the artifact store.

- [ ] **Step 2: Update the scaling section**

State that replicas can share the task queue and independently own different run sessions. State that a session-owning worker crash fails the session/run and does not automatically reconstruct a local workspace.

- [ ] **Step 3: Clarify `WORKER_RUN_ROOT`**

Describe it as a per-worker local workspace root. Explain that no shared mount is required while the session owner remains alive, but durable recovery requires rehydrating the workspace from durable inputs.

- [ ] **Step 4: Review the documentation diff**

Run: `git diff --check` and inspect the changed paragraphs for consistency with the code and design spec.

- [ ] **Step 5: Commit the documentation**

```bash
git add docs/architecture.md .env.example
git commit -m "docs: explain temporal worker session affinity"
```

### Task 4: Full Verification

**Files:**
- Verify all changed files and the complete branch history.

**Interfaces:**
- Consumes: completed implementation, tests, and documentation.
- Produces: evidence-backed branch handoff with baseline failures called out separately.

- [ ] **Step 1: Format changed Go files**

Run: `gofmt -w cmd/worker/main.go cmd/worker/main_test.go internal/workflows/template_run.go internal/workflows/template_run_workflow_test.go`

- [ ] **Step 2: Run focused verification**

Run: `go test ./cmd/worker ./internal/workflows -count=1`

Expected: PASS.

- [ ] **Step 3: Run the full Go test suite**

Run: `go test ./...`

Expected: the feature tests pass; report the five pre-existing API `/me` failures observed on the clean baseline if they remain.

- [ ] **Step 4: Run static checks and diff validation**

Run: `go vet ./...` and `git diff --check`.

- [ ] **Step 5: Inspect branch state**

Run: `git status --short` and `git log --oneline --decorate -6`.

