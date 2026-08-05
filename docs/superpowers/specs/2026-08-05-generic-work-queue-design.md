# Generic Work Queue Design

Date: 2026-08-05

## Problem

Writes that must land in both Postgres and an external system are currently
handled three different ways.

`CreateStack` is correct: it writes the stack row and an `authorization_outbox`
row in one transaction, attempts delivery inline, and marks the intent complete
([`internal/postgres/repositories.go:430`][create-stack],
[`internal/app/service.go:551`][create-stack-service]).

`AssignStackRole` and `RevokeStackRole` are not. They call OpenFGA directly with
no durable intent behind them ([`internal/app/service.go:1248`][assign]). The
assign path deletes the old role tuple and then writes the new one as two
separate calls, so a crash between them leaves the user with **no** role rather
than the old one, permanently, with nothing in the database to recover from.

Template runs use a third mechanism, `workflow_outbox`, with its own dispatcher
that is a near-copy of the authorization one ([`internal/dispatch/dispatcher.go`][dispatch],
[`internal/authdispatch/dispatcher.go`][authdispatch]).

The pattern has already been copy-pasted once. This design replaces it with a
single generic queue that any package can enqueue into, and one controller that
routes items to registered handlers.

[create-stack]: ../../../internal/postgres/repositories.go
[create-stack-service]: ../../../internal/app/service.go
[assign]: ../../../internal/app/service.go
[dispatch]: ../../../internal/dispatch/dispatcher.go
[authdispatch]: ../../../internal/authdispatch/dispatcher.go

## Scope

In scope:

- A generic `work_queue` table and an `internal/queue` package.
- Migrating authorization writes onto it, including the currently-unprotected
  assign and revoke paths.
- A `notify_user` kind reserved for downstream notifications.
- A read endpoint letting a user see the items they queued.

Out of scope, deliberately:

- Migrating `workflow_outbox`. It works today. Move it once the generic shape
  has run in production; it is already Job-shaped and the migration is mostly
  renaming.
- Building notification delivery. `internal/events` is an empty stub and there
  is no email, websocket, or webhook infrastructure in the repository. This
  design defines the *slot* a notification handler plugs into. What actually
  sends the message needs its own design.
- Drift detection between Postgres and OpenFGA.

## Decisions taken, and what was rejected

**OpenFGA remains the source of truth for grants.** An earlier draft added a
`stack_grants` table so Postgres held desired state. It was rejected: the
argument for it rested on the last-owner invariant, and that invariant turns out
not to be safety-critical. `can_manage_access` derives only from `owner`
([`openfga/authorization-model.json`](../../../openfga/authorization-model.json)),
so a stack with zero owners loses only the ability to change its own access —
view, operate, and approve still work for whoever holds those roles, and
`isPlatformAdmin` short-circuits before the OpenFGA check
([`internal/app/authorization.go:68`](../../../internal/app/authorization.go)),
so a platform admin can always reassign an owner. The failure mode is a support
ticket, not data loss.

The queue is therefore **a durable retry buffer that guarantees accepted intents
eventually land**, not a system of record.

Revisit if grants ever need to be *queried* as product data — "every stack where
user X is an owner", joins against user metadata, access reports. OpenFGA's read
API is paginated and does not join. Adding `stack_grants` later is additive:
backfill from OpenFGA and flip the read path. It is not a rewrite of the queue.

**No inline delivery attempt.** The API enqueues and returns; the controller is
the only writer to external systems. This differs from what `CreateStack` does
today and makes grant changes eventually consistent. Notifications are the
feedback path. Consequence: the API returns before the grant exists in OpenFGA,
and `ListStackGrants` reads OpenFGA so it will briefly not reflect a change the
caller just made. See Open Questions.

**Retry forever with capped backoff.** No dead-lettering. Coalescing (below)
makes this safe: there is at most one row per key, it always carries the latest
intent, and a permanently-failing key blocks only itself. The `failed_at` column
from `authorization_outbox` is dropped — with payloads this small, "unparseable
payload" barely exists as a failure mode.

## Architecture

```
API ──> InTx { domain write + queue.Enqueue } ──> COMMIT ──> 202
                                                    │
                                     ┌──────────────┘
                                     ▼
                              Controller: claim batch, route on `kind`
                                     │
                                     ▼
                          Handler.Deliver(item)
                            ① external call            (outside any tx)
                            ② BEGIN
                                 enqueue notify item
                                 complete, fenced on revision
                               COMMIT
```

Three layers, and the boundary test is that adding a Keycloak provisioning kind
later touches one new file and zero lines in `internal/queue` or
`internal/postgres`:

- **`internal/queue`** — `Item`, `Kind`, `Mode`, `Registry`, `Controller`, the
  interfaces below, and an in-memory backend for tests. Knows nothing about
  grants, workflows, OpenFGA, or Postgres. Never parses a payload.
- **`internal/postgres`** — implements `Enqueuer`, `Backend`, `Reader` over
  `work_queue`. Knows nothing about kinds.
- **Domain packages** — one `Handler` each.

## Schema

Migration `0012_work_queue.sql`:

```sql
create table work_queue (
    id            bigserial primary key,
    kind          text not null,
    ordering_key  text not null,
    payload       jsonb not null,
    revision      bigint not null default 1,
    actor_subject text not null,
    tenant_id     text not null,
    available_at  timestamptz not null default now(),
    claimed_until timestamptz,
    attempts      integer not null default 0 check (attempts >= 0),
    last_error    text not null default '',
    created_at    timestamptz not null default now(),
    processed_at  timestamptz
);

-- load-bearing: at most one pending row per (kind, key).
-- Provides coalescing AND per-key mutual exclusion.
create unique index work_queue_pending_key_idx
    on work_queue (kind, ordering_key) where processed_at is null;

create index work_queue_ready_idx
    on work_queue (available_at, id) where processed_at is null;

create index work_queue_actor_idx
    on work_queue (tenant_id, actor_subject, created_at desc);
```

The unique partial index does two jobs at once. It collapses repeated intents
for the same key, and it makes concurrent processing of one key structurally
impossible — a second worker cannot claim a row that cannot exist.

## Modes

The deciding question: **if five of these pile up, is collapsing them to one
correct, or is it data loss?**

|                     | `ModeReconcile`         | `ModeJob`              |
| ------------------- | ----------------------- | ---------------------- |
| payload is          | desired state           | the work itself        |
| key identifies      | the resource            | the event (unique)     |
| on conflict         | overwrite, `revision++` | do nothing             |
| five pile up        | one delivery            | five deliveries        |
| collapsing loses    | nothing                 | work                   |
| replay safe         | inherently              | handler's obligation   |
| backlog grows with  | number of resources     | number of events       |

Default to `ModeJob`; it never loses work. Use `ModeReconcile` only when the
desired state can be named as a value. "This user's role on this stack is X" —
yes. "Send this email" — no.

Initial kinds:

```
reconcile_stack_grant     Reconcile   stack:A/user:X
notify_user               Job         notif:9c2
start_template_run        Job         run:7f3a      (migrated later)
```

## The three queries

**Enqueue.** The conflict clause is chosen by the handler's mode.

```sql
-- ModeReconcile: newest desired state wins, fence any in-flight worker
insert into work_queue (kind, ordering_key, payload, actor_subject, tenant_id)
values ($1, $2, $3, $4, $5)
on conflict (kind, ordering_key) where processed_at is null
do update set payload  = excluded.payload,
              revision = work_queue.revision + 1;

-- ModeJob: re-enqueueing identical work is a no-op
insert into work_queue (kind, ordering_key, payload, actor_subject, tenant_id)
values ($1, $2, $3, $4, $5)
on conflict (kind, ordering_key) where processed_at is null
do nothing;
```

A pending row that is backing off keeps its `available_at` when a new intent
coalesces into it. This respects backoff during an outage at the cost of
delaying a fresh intent behind an old failure. Tunable later if it bites.

**Claim.** Batched, plain indexed scan, no correlated subquery. `kinds` is
optional and lets a second controller shard by kind later.

```sql
with candidate as (
    select id from work_queue
    where processed_at is null
      and available_at <= $1
      and (claimed_until is null or claimed_until <= $1)
      and ($4::text[] is null or kind = any($4))
    order by available_at, id
    for update skip locked
    limit $3
), claimed as (
    update work_queue q
       set claimed_until = $2, attempts = attempts + 1
      from candidate
     where q.id = candidate.id
 returning q.id, q.kind, q.ordering_key, q.payload, q.revision,
           q.actor_subject, q.tenant_id, q.attempts
) select * from claimed;
```

**Complete.** Fenced on revision.

```sql
update work_queue
   set processed_at = now(), claimed_until = null, last_error = ''
 where id = $1 and revision = $2 and processed_at is null;
```

Zero rows affected means a newer intent arrived while this one was in flight.
Do **not** complete: clear `claimed_until`, set `available_at = now()`, and let
it run again against the fresher payload. Without this fence the newer intent is
silently lost.

## Ordering keys

The key is derived by the handler, stored in the column, and used by the index.
It is not computed in SQL — that would put payload structure into the queue's
schema and destroy the genericity.

Grammar: `type:id` segments, `/`-separated, most significant first. Build them
from the existing canonical formatters (`authz.Stack.String()`,
`authz.Subject.String()`) rather than `fmt.Sprintf`, so the key format cannot
drift from the identity it names.

**Key derivation is a frozen contract.** Because keys are persisted, changing
the derivation splits one resource across two key formats, and the unique index
stops excluding concurrent workers — the race this design exists to prevent
comes back. To change a derivation: either drain the queue with producers
stopped, or introduce a new kind name (`reconcile_stack_grant_v2`) and let the
old kind drain on the old format. This must be stated in the `Handler` doc
comment.

Mode and key shape must agree. A Job with a repeating key silently swallows
work; a Reconcile with a unique key turns off mutual exclusion. Because the
handler owns both `Mode()` and `Key()`, they are declared in the same file and
cannot be set independently by a caller.

## Package interfaces

```go
package queue

type Kind string

type Mode int

const (
    ModeReconcile Mode = iota // coalesce by key; payload is desired state
    ModeJob                   // distinct work; payload is immutable
)

// Request is what callers enqueue. The key is derived, not supplied.
type Request struct {
    Kind         Kind
    Payload      json.RawMessage
    ActorSubject string
    TenantID     string
}

// Item is a claimed row handed to a handler.
type Item struct {
    ID           int64
    Kind         Kind
    Key          string
    Payload      json.RawMessage
    Revision     int64
    ActorSubject string
    TenantID     string
    Attempts     int
}

// Handler is implemented by whichever package owns the target system.
//
// Deliver MUST be idempotent: the queue guarantees at-least-once delivery, and
// a crash between the external call and the completing commit replays the item.
//
// Key derivation is a frozen contract. See "Ordering keys" in the design doc
// before changing it.
type Handler interface {
    Kind() Kind
    Mode() Mode
    Key(payload json.RawMessage) (string, error)
    Deliver(ctx context.Context, item Item) error
}

// Optional, discovered by type assertion; defaults apply when unimplemented.
type Describer interface {
    Describe(payload json.RawMessage) string // human-readable, for the UI
}

type Timings interface {
    Lease() time.Duration
    MaxBackoff() time.Duration
}

// Enqueuer is implemented by the store. Implementations may be bound to an
// open transaction — that is the point, and it is why this cannot be a message
// broker.
type Enqueuer interface {
    Enqueue(ctx context.Context, requests ...Request) error
}

type Backend interface {
    Claim(ctx context.Context, now, leaseUntil time.Time, limit int, kinds []Kind) ([]Item, error)
    Complete(ctx context.Context, id, revision int64) (bool, error) // false = revision moved
    Reschedule(ctx context.Context, id int64, availableAt time.Time, lastErr string) error
    Prune(ctx context.Context, before time.Time) (int64, error)
}

// Status is one row of the caller's own queue history.
type Status struct {
    Kind        Kind
    State       State // Pending | Delivered
    Summary     string // from Describer, or the kind name
    Attempts    int
    LastError   string
    CreatedAt   time.Time
    DeliveredAt *time.Time
}

type Reader interface {
    ListByActor(ctx context.Context, tenantID, actorSubject string, limit int) ([]Status, error)
}
```

`Registry` maps `Kind` to `Handler` and rejects duplicate registrations at
construction. `postgres.Store` holds the registry so `Enqueue` can resolve key
and mode from the payload; callers pass a `Request` and never see a key.

`Controller` claims a batch and fans out to a bounded worker pool, so one slow
handler cannot starve other kinds.

**An item whose kind has no registered handler must be rescheduled, never
failed.** During a rolling deploy the API can produce kinds the worker does not
yet know; failing them would drop work permanently. Retry-forever makes this
self-healing.

Backoff: `1s * 2^(attempts-1)`, jittered, capped at 5 minutes or the handler's
`MaxBackoff()`. Prune deletes rows with `processed_at < now() - 24h`, run from
the controller every 10 minutes.

## Transaction seam

Enqueueing must join the caller's transaction — that is the entire reason this
is an outbox and not a message broker. No unit-of-work helper exists today; all
28 `Store` methods open their own transaction.

Add one, scoped to exactly what the three call sites need. It grows only when a
new dual-write point appears.

```go
type TxRepo interface {
    CreateStack(ctx context.Context, stack traits.Stack) error
    AppendAuditEvent(ctx context.Context, event traits.SecurityAuditEvent) error
}

type UnitOfWork interface {
    InTx(ctx context.Context, fn func(TxRepo, queue.Enqueuer) error) error
}
```

## Call sites

**`CreateStack`.** Behavior unchanged; plumbing simplifies. The
`stackOwnerIntentRepository` type assertion and `CreateStackWithOwnerIntent`
both disappear. Owner grant becomes a `reconcile_stack_grant` request enqueued
in the same transaction as the stack insert.

**`AssignStackRole`.** The fix. Today it fires two unprotected OpenFGA calls.
After: one `InTx` writing the audit event and a single
`reconcile_stack_grant` request whose payload is the *desired* role. The
old-role deletion disappears entirely — the handler converges, so there is no
delete-then-write window to crash inside.

**`RevokeStackRole`.** Same shape, payload carries an empty role.

Payload for `reconcile_stack_grant`:

```json
{"stack_id": "stack_abc", "subject": "user:xyz", "role": "owner"}
{"stack_id": "stack_abc", "subject": "user:xyz", "role": ""}
```

An empty role means "no access". Key: `stack:stack_abc/user:xyz`.

The last-owner guard keeps reading OpenFGA and stays best-effort, as it is
today. It is now reading state that can lag by the queue's delivery latency.
Accepted per the decision above.

## Handler: reconcile_stack_grant

Lives in `internal/authdispatch`, which drops its dispatcher loop and shrinks to
a handler wrapping `authz.Authorizer`.

Converge: read the subject's current tuples for that stack, compute the delta,
apply it. This needs a small addition to the OpenFGA adapter — a `read` filtered
by user as well as object, since `ListGrants` filters by object only
([`internal/openfga/authorization_adapter.go:170`](../../../internal/openfga/authorization_adapter.go)).

Naturally idempotent: re-applying the same desired state is a no-op.

## Notifications

A notification is work with a different kind, not a second table. `notify_user`,
`ModeJob`, same table, same controller. If notification volume ever starves
grants, run a second controller with a kind allowlist over the same table.

The notification must be enqueued **in the same transaction that completes the
item**, and **after** the external call returns:

```
① call OpenFGA                     external, no transaction
② BEGIN
     enqueue notify_user request
     complete, fenced on revision
   COMMIT
```

Enqueueing before the external call would notify users about work that then
failed. Enqueueing in a separate transaction would reintroduce a dual write one
level down.

The handler for `notify_user` is not part of this design — see Scope.

## Read path

`GET /v1/tenants/{tenant_id}/queue` returns items where `actor_subject` equals
the caller and `tenant_id` matches. No new permission concept: you can only ever
see your own items.

Per row: `kind`, `status`, `created_at`, `attempts`, `last_error`, and a
human-readable summary from the handler's `Describe`, so the API layer never
parses kind-specific JSON. Raw payload is not returned.

Because retry is forever, a stuck item stays pending indefinitely. `attempts`
and `last_error` are what distinguish "queued two seconds ago" from "failing for
an hour", so both must be surfaced.

## Migration

1. Create `work_queue`.
2. Backfill unprocessed `authorization_outbox` rows as `reconcile_stack_grant`
   items. `operation = 'grant'` maps to the row's role; `operation = 'revoke'`
   maps to an empty role. Rows with `processed_at` or `failed_at` set are
   skipped.
3. Deploy the controller with the grant handler registered.
4. Switch `CreateStack`, `AssignStackRole`, `RevokeStackRole` to enqueue.
5. Remove `authdispatch.Dispatcher` and the `authorization_outbox` table in a
   follow-up migration, once no rows remain.

`workflow_outbox` and `internal/dispatch` are untouched.

## Testing

Follow the repository's existing table-driven style, tests first.

Unit, no database, against the in-memory backend:

- Registry rejects duplicate kinds; unknown kind reschedules rather than fails.
- Backoff sequence and cap, including a handler-supplied `MaxBackoff`.
- Controller completes on success, reschedules on error, records `last_error`.
- Worker pool bounds concurrency; a slow handler does not block other kinds.
- `Key()` derivation per handler, including malformed payloads.

Integration, against Postgres:

- Coalescing: three reconcile enqueues for one key leave exactly one pending row
  with the newest payload and `revision = 3`.
- Mutual exclusion: two concurrent claim loops never hand the same key to two
  workers simultaneously.
- Revision fence: enqueue during an in-flight claim causes `Complete` to affect
  zero rows and the item to re-run with the newer payload.
- `ModeJob` dedupe: re-enqueueing an identical key is a no-op and does not bump
  revision.
- Rollback leaves no orphan queue row.
- Lease expiry makes an abandoned row claimable again.
- Prune removes processed rows past the retention window and leaves pending ones.

End-to-end: assign a role, kill the process before the controller runs, restart,
assert OpenFGA converges.

## Scaling

Postgres is not the constraint. Queue load is driven by administrative actions,
not user count — role changes for a large user base still average well under one
item per second, with short bursts during bulk onboarding.

What breaks first, in order: single-row polling (addressed here by batch claim),
then OpenFGA's write path, which costs two round trips per item because every
write is followed by a `HIGHER_CONSISTENCY` confirm
([`internal/openfga/authorization_adapter.go:327`](../../../internal/openfga/authorization_adapter.go)).
Postgres can feed that far faster than it can drink.

Revisit at **sustained pending depth above 10k rows** or **p99 time-to-delivery
above 30 seconds**, both readable directly from `work_queue`. If the table is
genuinely outgrown, the move is not to replace it — the transactional-enqueue
argument only strengthens at scale — but to keep it as the write path and add
CDC to fan out to a broker. The `Backend` seam is what makes that possible.

## Open questions

1. **No inline delivery** makes every grant change eventually consistent, so
   `ListStackGrants` can briefly not reflect a change the caller just made. This
   was inferred from the proposed flow rather than confirmed explicitly. If
   read-your-writes matters more than a single code path, add an inline attempt
   before returning, as `CreateStack` does today.
2. **`notify_user` has no delivery mechanism.** The kind and mode are defined
   here; nothing sends anything until notification infrastructure exists.
3. **Backoff and coalescing interaction** — a fresh intent inherits an old
   failure's backoff. Acceptable initially; revisit if grant changes feel
   sluggish after an OpenFGA outage.
