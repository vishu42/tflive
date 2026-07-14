# Template Run Workflow Outbox Design

## Goal

Remove the unreliable Postgres-to-Temporal dual write when starting template runs without adding another message broker.

## Architecture

`StartTemplateRun` persists the queued `template_runs` row through `TemplateRunRepository.CreateTemplateRun` and returns without calling Temporal. The Postgres implementation creates the run and a `workflow_outbox` record in one database transaction. The outbox stores a durable key (`run_id`); dispatch input is reconstructed from the persisted run and its immutable template revision.

The existing `tflive-worker` process runs a lightweight outbox dispatcher alongside the Temporal worker. It claims one available outbox record with a short lease, calls Temporal with the existing deterministic workflow ID, and marks the record processed. Failed starts release the record with a retry time and error. A crash after Temporal accepts the workflow but before Postgres records completion is safe because repeated starts use the same workflow ID and reject duplicate workflow-ID reuse.

## Components

- `internal/postgres/migrations/0007_workflow_outbox.sql` creates the durable outbox and its pending-work index.
- `internal/postgres` creates run and outbox atomically and implements claim, completion, and retry operations.
- `internal/dispatch` owns the outbox port and dispatch loop. It depends only on application traits and a narrow workflow starter interface.
- `cmd/tflive-worker` wires the Postgres store and Temporal dispatcher into the dispatch loop and stops it when the Temporal worker exits.
- `internal/app.Service.StartTemplateRun` no longer calls Temporal directly.

## Data Model

Each `workflow_outbox` row contains:

- `id`: deterministic `template-run/<tenant-id>/<run-id>` identity.
- `event_type`: currently `start_template_run`.
- `aggregate_id`: the template run ID.
- `available_at`: earliest retry time.
- `claimed_until`: short lease used by one dispatcher instance.
- `attempts`: claim count.
- `processed_at`: non-null after Temporal accepts the workflow.
- `last_error`: last dispatch failure for operations and debugging.

The payload is not duplicated. Claiming joins `template_runs` with `template_revisions`, ensuring dispatch always uses durable inputs.

## Failure Semantics

- A failed database transaction persists neither run nor outbox row.
- A worker outage leaves pending rows durable in Postgres.
- A Temporal outage records the failure and retries later.
- A dispatcher crash after Temporal accepts the start leaves the lease to expire; the retry targets the same workflow ID.
- Multiple workers use `FOR UPDATE SKIP LOCKED` and leases to avoid concurrently claiming the same row.

## Scope

This change covers starting template-run workflows. Template registration starts, approvals, and cancellation signals retain their current behavior and can move to the same outbox pattern separately.

## Verification

- Application tests prove run creation no longer directly invokes Temporal.
- Postgres integration tests prove run and outbox creation, input reconstruction, claiming, completion, and retry.
- Dispatcher unit tests prove success, retry, and idle behavior.
- Worker wiring tests prove the dispatcher starts and stops with the worker process.
- `go test ./...` remains green.
