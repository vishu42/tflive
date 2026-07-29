# Template Run Failure and Cancellation Recovery Design

**Date:** 2026-07-28  
**Status:** Draft

## Goal

Ensure every template run reaches a durable, truthful terminal state when an
activity fails or cancellation is requested after the Temporal workflow has
already closed. A stranded non-terminal run must not keep Plan, Apply, or
cancellation controls disabled indefinitely.

Terminate is intentionally out of scope for this change.

## Current failure

`template_runs.status` is the persisted logical lock. The workflow records
`locked` during workspace preparation and later records `lock_released` and a
terminal status on successful completion or normal cancellation.

Activity failures currently bubble out of `TemplateRunWorkflow` without a
failure-status transition. The workflow can therefore close with an activity
error while Postgres still contains a non-terminal status. The Cancel service
updates that row to `cancel_requested` before signaling Temporal. If the
workflow is already closed, the signal cannot be consumed and the row remains
stuck at `cancel_requested`.

The UI derives `activeRun` from any non-terminal run status, so these stranded
states disable Plan and Apply.

## Design

### 1. Durable activity failure capture

When the workflow receives a terminal activity failure, it must attempt to
persist:

1. `failed` as the run's terminal status;
2. the sanitized underlying error in `error_summary`; and
3. `completed_at` through the existing terminal-status persistence path.

Because `lock_released` is itself non-terminal in the current status model, the
failure path should not leave that status as the final value. A terminal
`failed` status is the durable indication that the logical lock is no longer
held. The final persisted state must be terminal and must not depend on the
activity that originally failed.

Error summaries must preserve useful operational context such as activity or
phase and wrapped error text, while continuing to exclude credential values.
If failure-status persistence itself fails, the workflow should return the
original failure and log/record the cleanup failure for diagnosis; cleanup
errors must not overwrite the root activity error.

The normal success and cancellation sequences remain unchanged.

### 2. Workflow-based cancellation

The existing Cancel action continues to signal the workflow. The workflow
receives the signal and cancels the activity context with
`workflow.WithCancel`. Activities are not signaled directly.

Cancellation handling must be idempotent:

- A live workflow consumes the signal and records
  `cancel_requested → canceling → lock_released → canceled`.
- A workflow that is already closed must not leave the database at
  `cancel_requested`.
- Repeating Cancel after reconciliation must return a stable terminal-state
  response rather than attempting to mutate the run indefinitely.

When the Temporal signal reports that the workflow is missing or already
closed, the service should reconcile the persisted run to a terminal state.
Because the workflow did not complete its normal cancellation path, this
state should be `failed` with an explanatory `error_summary`, unless durable
evidence proves the activity was canceled before the workflow closed. The
reconciliation must set `completed_at` and use a terminal status as the
durable indication that the logical lock is clear.

### 3. UI behavior

No new Terminate button is added.

The UI continues to treat `completed`, `failed`, and `canceled` as terminal.
Once the backend records one of these states, polling naturally re-enables
Plan/Apply according to the existing capability and run-history rules.

The run detail view should display `error_summary` for failed runs. Existing
error rendering can be reused; no new status category is required.

## Data flow

```text
activity error
    ↓
Temporal activity retries exhaust
    ↓
workflow failure handler
    ↓
persist failed + error_summary + completed_at
    ↓
logical lock released; UI sees terminal run
```

For cancellation:

```text
Cancel API
    ├─ live workflow → signal → cancel activity context → canceled
    └─ closed workflow → reconcile run → failed/terminal + lock released
```

## Testing

### Workflow tests

- A plan/apply/destroy activity failure persists terminal `failed`, which
  releases the logical lock.
- The original activity error is retained when failure cleanup succeeds.
- A cleanup failure does not replace the original failure.
- Normal cancellation during Terraform still ends in `canceled`.
- Retry exhaustion produces one terminal failure rather than a stranded
  non-terminal status.

### Service/repository tests

- Terminal failure persistence stores `error_summary` and `completed_at`, with
  `failed` as the final status rather than `lock_released`.
- Cancellation against a live workflow preserves the existing signal path.
- Cancellation against a closed workflow reconciles the run and is safe to
  repeat.
- A terminal run cannot be canceled again.

### API/UI tests

- Cancel returns a stable response for a run whose workflow is already gone.
- A failed run displays its error summary.
- Plan and Apply become enabled after polling observes `failed`.
- No Terminate control is rendered.

## Scope boundaries

- No terminate signal or emergency kill workflow.
- No separate lock table or lease implementation.
- No duplication of Temporal's complete activity event history in Postgres.
- No changes to Terraform execution semantics or cloud-resource rollback.
