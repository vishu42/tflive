# Queue-Backed Workflow Intents Design

Date: 2026-08-11

## Problem

The generic work queue ([2026-08-05-generic-work-queue-design.md][queue-design])
made stack creation and role changes atomic: the domain row and the delivery
intent commit together, and a handler converges the external system afterwards.
Four write paths never made that transition and still dual-write.

`RegisterTemplate` commits a `template_registrations` row and then calls
Temporal ([`internal/app/service.go:491`][register]). If the start fails, the
registration is stuck `pending` forever — nothing retries it.

`ApproveRun` writes the approval, writes an audit event on a separate
connection, then signals Temporal ([`internal/app/service.go:1419`][approve]).
A persisted, audited approval whose signal is lost leaves the workflow waiting
forever. The code already carries a `TODO` saying so.

`CancelRun` has the same shape with a partial compensation
([`internal/app/service.go:1462`][cancel]). The compensation only fires on
`serviceerror.NotFound`; a timeout or network failure returns an error to the
caller with the cancellation row written and no signal delivered, and nothing
retries.

`StartTemplateRun` is *not* a dual write — it uses `workflow_outbox`
([`internal/postgres/repositories.go:920`][outbox-insert]) — but that table is a
second, single-purpose outbox with its own dispatcher
([`internal/dispatch/dispatcher.go`][dispatch]), its own claim/lease/retry SQL,
and its own worker goroutine. It duplicates what `work_queue` now does
generically. The generic-queue design called out that this pattern "has already
been copy-pasted once"; `workflow_outbox` is the remaining copy.

## Non-Goal: Template Run Log Metadata

`PutTemplateRunLog` writes an object to S3 or the filesystem and then records
its metadata in Postgres ([`internal/artifacts/store.go:57`][log-store]). This
is a genuine dual write and it stays.

The queue cannot fix it. An outbox works by committing the intent in the same
transaction as the domain write; here the first write goes to an object store,
which has no transaction for the enqueue to join. Enqueueing the metadata write
would leave exactly two non-atomic writes — object plus enqueue — while adding a
kind and a handler. A metadata-first two-phase scheme trades two writes for
three and forces readers to filter incomplete rows.

Both halves are already idempotent: the object key is deterministic
([`LogKey`][log-key]) and the metadata insert is `on conflict … do update`. The
whole sequence runs inside a Temporal activity, so Temporal's activity retry is
the convergence mechanism. This design adds a comment recording that, so the
next reader does not "fix" it into something weaker.

## Design

### Four new kinds

Every one is `ModeJob`. Each payload is an event that happened, not desired
state, so collapsing two of them would lose one.

| Kind | Resource key | Replaces |
| --- | --- | --- |
| `start_template_run` | `run:<tenant>/<run>` | `workflow_outbox` |
| `start_template_sync` | `registration:<tenant>/<registration>` | inline `StartTemplateSync` |
| `signal_run_approval` | `run:<tenant>/<run>` | inline approval signal |
| `signal_run_cancellation` | `run:<tenant>/<run>` | inline cancel signal |

Three kinds share the `run:<tenant>/<run>` key shape. That is not a collision:
the unique partial index is on `(kind, resource_key)`, so each kind is only
mutually excluded against itself. A run's start, its approval and its
cancellation are independent intents that may be pending at once. The same
reasoning is already recorded on `GrantStackOwnerSpec`, which shares
`stack:<id>` with `mark_stack_ready`.

Key derivation is a frozen contract, as for every existing kind: keys are
persisted, so changing the format splits one resource across two keys and
disables the mutual exclusion the index provides.

### Handlers depend on the dispatcher, not the service

The existing provisioning handlers adapt over `app.Service` because they need
domain logic that already lives there. These four do not: three are a single
Temporal call. They take `app.WorkflowDispatcher` directly, which keeps them
unit-testable against a fake and avoids constructing a `Service` with mostly nil
fields in the worker.

`SignalRunCancellationHandler` is the exception and takes one extra dependency,
a `TemplateRunCancellationReconciler` exposing
`ReconcileTemplateRunCancellation`. Its `Deliver` signals the workflow and then:

- success — return, nothing to chain;
- workflow closed (`serviceerror.NotFound`) — record the reconciliation and
  return success, because the cancellation can never be delivered and retrying
  forever would be a lie;
- any other error — return it, and the queue retries with backoff.

This is strictly stronger than today. The current code compensates only for the
closed-workflow case and surfaces every other failure to the HTTP caller with
the row already written. Under the queue a transient failure simply retries.
`isWorkflowClosedError` moves from `service.go` to the handler.

Each handler lives in its own file beside the two existing ones
(`start_template_run_handler.go`, `start_template_sync_handler.go`,
`signal_run_approval_handler.go`, `signal_run_cancellation_handler.go`), which
keeps `service.go` from growing further — it is already 2,100 lines.

### Payload carries the full workflow input

`start_template_run` payloads carry a complete `traits.TemplateRunWorkflowInput`
rather than a run ID the handler re-reads. `StartTemplateRun` has already
fetched the revision by the time it enqueues, so every field is in hand, and the
handler needs no repository at all. A run's identity and source are immutable
once created, so the payload cannot drift from the row.

These structs carry no JSON tags, so they marshal with verbatim Go field names
(`RunID`, `TenantID`, `StackTemplateID`, …). The backfill migration must build
the same keys.

### TxRepo grows four methods

`TxRepo` documents itself as "deliberately tiny", growing "only when a new
dual-write point appears". Four appear:

```go
type TxRepo interface {
	CreateStack(context.Context, traits.Stack) error
	AppendAuditEvent(context.Context, traits.SecurityAuditEvent) error
	CreateTemplateRun(context.Context, traits.TemplateRun) error
	CreateTemplateRegistration(context.Context, traits.TemplateRegistration) error
	ApproveTemplateRun(context.Context, traits.TemplateRunApproval) error
	RequestTemplateRunCancellation(context.Context, traits.TemplateRunCancellation) error
}
```

Each corresponding `*Store` method has its body extracted into a free function
over a `pgx.Tx` or `pgxExecutor`, and both the standalone method and the
`txRepo` method call it. This follows `insertStack` and `appendAuditEvent`,
which already work this way, so there is one implementation per write.

`ApproveTemplateRun` and `RequestTemplateRunCancellation` return
`app.ErrRunNotApprovable` and `app.ErrRunNotCancelable` when they match no row.
Returning those from inside `InTx` rolls the transaction back, so an
unapprovable run enqueues no signal. That is the behaviour we want and it comes
for free from the transaction.

### Service methods collapse to one transaction

Each of the four becomes a single `Work.InTx` containing the domain write, any
audit event, and the enqueue:

- `RegisterTemplate` — registration row + `start_template_sync`.
- `StartTemplateRun` — run row + `start_template_run`. The `workflow_outbox`
  insert disappears from `CreateTemplateRun`.
- `ApproveRun` — approval + audit event + `signal_run_approval`. The
  best-effort `auditError` call on this success path goes away.
- `CancelRun` — cancellation + `signal_run_cancellation`. The inline signal and
  inline reconcile move to the handler.

`CreateStack`'s success audit event also moves inside its existing transaction
([`internal/app/service.go:589`][create-stack-audit]), matching
`AssignStackRole`, which already audits transactionally. `auditError` survives
for failed-access events, where best-effort logging is the right behaviour.

### The API stops talking to Temporal

With all four interactions queued, `cmd/api` has no remaining Temporal call.
`dialTemporal`, `newDispatcher` and the `Workflows` field drop out of the API
wiring; `app.Service.Workflows` is populated only in the worker, where the
handlers need it. The API's dependency on the Temporal client — and its startup
failure mode when Temporal is unreachable — both disappear.

### Retiring workflow_outbox

Migration `0014_retire_workflow_outbox.sql`:

1. Backfill every unprocessed `start_template_run` row into `work_queue`,
   joining `template_runs` and `template_revisions` exactly as
   `ClaimTemplateRun` does today, and building the payload with the verbatim
   field names above. `on conflict … do nothing`, as migration 0012 does.
2. `drop table workflow_outbox`.

Dropping departs from migration 0012, which left `authorization_outbox` in
place. That dormant table is now a liability: it still has an orphaned
`authorizationOutboxID` helper referencing it
([`internal/postgres/repositories.go:520`][orphan]) and reads as live code. This
design drops `workflow_outbox` and deletes that dead helper.

Deleted alongside it: the `internal/dispatch` package, `ClaimTemplateRun` /
`CompleteTemplateRun` / `RetryTemplateRun`, the `dispatch.Outbox` member of
`workerStore`, and the worker's second dispatch goroutine. The worker then runs
one delivery loop instead of two.

### One shared spec list

Both binaries build a `SpecRegistry` from a duplicated literal
([`cmd/api/main.go:106`][api-specs], [`cmd/worker/main.go:113`][worker-specs]).
At three kinds the duplication was tolerable; at seven it will drift. A single
`app.QueueSpecs()` returns every spec, including `authz.StackGrantSpec` — `app`
already imports `authz` — and both mains call it.

## Testing

- **Handlers** — a fake `WorkflowDispatcher` per handler: the dispatch happens,
  a dispatcher error propagates so the queue retries, and for cancellation, a
  `NotFound` takes the reconcile branch and reports success while any other
  error propagates.
- **Service** — a fake `UnitOfWork` asserting the domain write and the enqueue
  land in the same `InTx` callback, that a failed domain write enqueues nothing,
  and that no method calls `Workflows` directly any more.
- **Store** — the four new tx-scoped writes commit and roll back with their
  transaction; `CreateTemplateRun` no longer writes `workflow_outbox`.
- **Migration** — a backfill test mirroring the 0012 test at
  [`internal/postgres/store_test.go:2894`][migration-test]: seed unprocessed and
  processed outbox rows, run the migration, assert only the unprocessed ones
  became `work_queue` rows with a payload that round-trips into
  `traits.TemplateRunWorkflowInput`, and assert the table is gone.
- **Existing suites** — `internal/dispatch` tests are deleted with the package;
  worker wiring tests lose the outbox dispatcher.

[queue-design]: 2026-08-05-generic-work-queue-design.md
[register]: ../../../internal/app/service.go
[approve]: ../../../internal/app/service.go
[cancel]: ../../../internal/app/service.go
[create-stack-audit]: ../../../internal/app/service.go
[outbox-insert]: ../../../internal/postgres/repositories.go
[orphan]: ../../../internal/postgres/repositories.go
[dispatch]: ../../../internal/dispatch/dispatcher.go
[log-store]: ../../../internal/artifacts/store.go
[log-key]: ../../../internal/artifacts/store.go
[api-specs]: ../../../cmd/api/main.go
[worker-specs]: ../../../cmd/worker/main.go
[migration-test]: ../../../internal/postgres/store_test.go
