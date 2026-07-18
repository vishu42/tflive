# Authorization Relationship Reliability Design

**Issue:** AUTH-012 / GitHub issue #14  
**Goal:** Reliably deliver desired OpenFGA stack-role relationships after product-state commits, beginning with initial stack ownership.

## Scope

This change prevents a newly persisted stack from being permanently left without an owner when OpenFGA delivery fails. It introduces durable, observable, retry-safe relationship delivery for direct stack-role grants and revocations.

It does not create a shared application-wide outbox framework. `workflow_outbox` remains specific to Temporal workflow starts. AUTH-012 adds a separate, OpenFGA-specific `authorization_outbox`, reusing the proven transactional-outbox mechanics without coupling unrelated external integrations. It does not add stack-access HTTP endpoints; AUTH-017 will reuse this delivery mechanism when it adds role management.

## Data Model

`authorization_outbox` records one desired direct OpenFGA tuple mutation. Each row contains:

- `id`: deterministic mutation identity, including operation, canonical subject, canonical stack, and direct role;
- `operation`: `grant` or `revoke`;
- `subject`, `stack`, and `role`: validated canonical tuple components;
- `available_at`: earliest retry time;
- `claimed_until`: a short worker lease;
- `attempts`: delivery-attempt count;
- `processed_at`: set only after OpenFGA confirms the desired tuple state;
- `last_error`: a sanitized operational error;
- `created_at`: audit and operational timestamp.

The deterministic identity makes a repeated enqueue of the same desired operation idempotent. A grant and revoke of the same tuple have different identities. Future role-replacement semantics are owned by AUTH-017.

## Creation Flow

`app.Service.CreateStack` continues to validate the authenticated creator and authorized global role. Its persistence dependency changes so the Postgres implementation creates the stack and its initial `owner` grant intent in the same database transaction.

After that transaction commits, the service immediately attempts delivery through the authorization dispatcher using the existing OpenFGA adapter with confirmation enabled:

1. A confirmed owner tuple marks the outbox entry processed and returns the new stack with `201`.
2. An OpenFGA timeout, outage, malformed response, or unconfirmed write returns the existing stable authorization `503` response. The stack and pending owner intent remain committed.
3. The background dispatcher retries the pending intent until confirmation succeeds.

This preserves current API semantics: successful creation never reports an unconfirmed owner relationship. A persisted stack with a pending owner intent is inaccessible through future OpenFGA-enforced endpoints until delivery completes.

## Delivery And Recovery

A dedicated authorization dispatcher is wired into the worker process alongside the existing Temporal workflow dispatcher. It claims at most one eligible outbox row using `FOR UPDATE SKIP LOCKED`, increments its attempt count, and sets a short lease. Multiple workers therefore cannot concurrently deliver the same entry.

For each claimed entry, the dispatcher constructs a validated `authz.Grant` and invokes `WriteRelationships` or `DeleteRelationships` with confirmation enabled. On success it records `processed_at`. On a retryable failure it clears the lease, records a sanitized error, and moves `available_at` forward using bounded backoff.

A crash after OpenFGA accepts a mutation but before Postgres records completion is safe. Once the lease expires, the same deterministic operation is retried; the existing adapter's desired-state confirmation treats an already-applied grant or revoke as successful.

Locally invalid persisted entries are terminal: they remain visible with their error and are not endlessly retried. All other adapter dependency and confirmation failures are retryable. Operators can query incomplete entries, including their attempt count and sanitized last error, and run the dispatcher or reconciliation command to retry them. No credentials, access tokens, or OpenFGA request bodies are stored.

## Interfaces

The application layer receives narrow interfaces for atomically persisting an initial owner intent and for claiming, completing, and retrying authorization-outbox entries. The Postgres store implements those interfaces. The dispatcher depends only on that outbox interface and `authz.Authorizer`, keeping OpenFGA SDK details in the existing adapter.

The existing `authz.Authorizer` contract remains unchanged. Its idempotent write/delete and higher-consistency confirmation behavior is the delivery guarantee used by the dispatcher.

## Failure Behavior

| Condition | Result |
|---|---|
| Stack transaction fails | Neither stack nor owner intent persists. |
| Owner intent cannot be inserted | The stack transaction rolls back; no stack persists. |
| Immediate OpenFGA delivery succeeds | Outbox entry is completed and API returns `201`. |
| Immediate OpenFGA delivery fails | Stack and pending intent persist; API returns stable `503`; background recovery retries. |
| Worker or process crashes after OpenFGA acceptance | Lease expires and idempotent confirmed delivery retries safely. |
| Concurrent dispatchers | Row lease and `SKIP LOCKED` permit only one active delivery attempt. |
| Invalid durable entry | Dispatcher records terminal failure for operator reconciliation; it does not loop indefinitely. |

## Testing

- Postgres tests prove stack and initial owner intent are committed atomically and roll back together.
- Dispatcher tests cover confirmed grant and revoke delivery, retry scheduling, sanitized errors, completion, and idle operation.
- Tests prove duplicate delivery after an uncertain result is safe through confirmed desired-state semantics.
- Postgres concurrency tests cover lease claims and expired-lease recovery.
- Application and API tests preserve `201` after confirmed owner assignment and stable `503` with durable recovery after an immediate failure.
- Worker wiring tests prove the authorization dispatcher starts and stops with the existing worker.
- Documentation describes the pending-owner state and operator reconciliation process.
- Focused tests run during development, followed by `go test ./...`.
