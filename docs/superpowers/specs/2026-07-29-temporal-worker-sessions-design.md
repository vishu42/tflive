# Temporal Worker Session Affinity Design

## Goal

Allow multiple `tflive` worker replicas to share the Temporal task queue while keeping every filesystem-dependent activity for one `TemplateRun` on the same worker replica.

## Context

The application already persists logs and artifacts in S3. A Terraform run also uses a worker-local workspace created under `WORKER_RUN_ROOT`: source is cloned there, OpenTofu initializes the directory, and later activities reuse the same path. Temporal task queues do not guarantee that sequential activities run on the same worker.

## Decision

Use Temporal Go SDK Sessions. The worker will enable session processing, and `TemplateRunWorkflow` will create one session for the run. Workspace-dependent activities will execute with the session context; status and failure-recording activities will continue using the normal workflow context so they remain available after session completion or failure.

The session spans the complete run, including approval waits, because apply and destroy prepare or reuse local workspace state before waiting for approval. Session creation will wait up to one minute, and each session will have a 24-hour execution limit.

Session affinity is not durable storage or automatic crash recovery. If the worker owning a session dies, the session fails and the run must be surfaced as failed; a future recovery enhancement can recreate the workspace/session from durable inputs.

## Files and Boundaries

- `cmd/worker/main.go`: enable session workers in the Temporal worker options.
- `internal/workflows/template_run.go`: create and complete the run session; route workspace activities through its context while preserving normal status persistence.
- `cmd/worker/main_test.go`: verify worker construction enables sessions.
- `internal/workflows/template_run_workflow_test.go`: verify workspace activities use session task routing and existing status behavior remains intact.
- `docs/architecture.md`: document session affinity, local workspace ownership, and session-worker failure semantics.

## Testing

Use the Temporal workflow test environment with session workers enabled. Record activity task queues for workspace and status activities and assert workspace activities share a session-specific queue distinct from normal status activity routing. Preserve existing approval, cancellation, retry, and failure-persistence tests.
