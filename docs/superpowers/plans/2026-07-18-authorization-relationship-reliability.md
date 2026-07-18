# Authorization Relationship Reliability Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make OpenFGA stack-role mutations durably retryable, beginning with atomic stack creation and owner-grant recovery.

**Architecture:** Add an OpenFGA-specific `authorization_outbox` table and an `internal/authdispatch` dispatcher. Stack creation persists its stack row and canonical owner-grant intent in one Postgres transaction, then preserves current synchronous confirmation behavior and marks the intent complete. The worker retries incomplete intents using the existing OpenFGA authorization port; no shared application-wide outbox abstraction is introduced.

**Tech Stack:** Go, pgx v5/PostgreSQL, existing provider-neutral `internal/authz` port, existing OpenFGA adapter, Go standard-library tests.

## Global Constraints

- `workflow_outbox` remains specific to Temporal workflow starts; do not generalize or merge it.
- Only OpenFGA direct roles (`owner`, `operator`, `approver`, `viewer`) may enter authorization delivery.
- All OpenFGA mutations use the existing idempotent adapter and request confirmation.
- A successful stack-create response requires confirmed owner delivery; an immediate OpenFGA failure remains a stable `503` with a durable pending intent.
- Never persist or log access tokens, OpenFGA request bodies, or unsanitized dependency errors.
- Use Postgres transactions, deterministic outbox IDs, `FOR UPDATE SKIP LOCKED`, leases, and retry scheduling.

---

## File Structure

- Create: `internal/postgres/migrations/0008_authorization_outbox.sql` - durable authorization relationship intent table and pending-work index.
- Create: `internal/authdispatch/dispatcher.go` - narrow outbox interface and polling dispatcher for confirmed grant/revoke delivery.
- Create: `internal/authdispatch/dispatcher_test.go` - dispatcher success, retry, and idle behavior.
- Modify: `internal/app/service.go` - atomic stack-and-owner-intent dependency and completed-intent handling in `CreateStack`.
- Modify: `internal/app/stack_authorization_test.go` - application behavior for durable owner intent and immediate delivery failure.
- Modify: `internal/postgres/repositories.go` - transactionally create stacks plus owner intents and implement claim, complete, and retry operations.
- Modify: `internal/postgres/store_test.go` - migration, transaction, lease, retry, and completion integration tests.
- Modify: `internal/config/auth.go` and `internal/config/config.go` - load validated OpenFGA delivery configuration for the worker without requiring OIDC configuration.
- Modify: `internal/config/auth_test.go` and `internal/config/config_test.go` - worker OpenFGA configuration validation tests.
- Modify: `cmd/worker/main.go` and `cmd/worker/main_test.go` - wire and lifecycle-manage the authorization dispatcher beside the Temporal dispatcher.
- Modify: `docs/authentication.md` and `docs/sprint/authn_and_authz/README.md` - document recovery and mark AUTH-012 complete after verification.

### Task 1: Define Durable Authorization Delivery

**Files:**
- Create: `internal/postgres/migrations/0008_authorization_outbox.sql`
- Create: `internal/authdispatch/dispatcher.go`
- Create: `internal/authdispatch/dispatcher_test.go`
- Modify: `internal/postgres/store_test.go:43-114`

**Interfaces:**
- Consumes: `authz.Grant`, `authz.Mutation`, and `authz.Authorizer` from `internal/authz/authorization.go`.
- Produces: `authdispatch.Entry`, `authdispatch.Outbox`, and `authdispatch.Dispatcher` for Postgres, the API service, and the worker.

- [ ] **Step 1: Write the failing dispatcher tests**

```go
func TestDispatchOnceConfirmsAndCompletesGrant(t *testing.T) {
    now := time.Date(2026, 7, 18, 10, 0, 0, 0, time.UTC)
    entry := Entry{ID: "grant/user:user_123/stack:stack_123/owner", Operation: Grant, Subject: "user:user_123", Stack: "stack:stack_123", Role: "owner"}
    outbox := &memoryOutbox{entry: entry}
    dispatcher := NewDispatcher(outbox, recordingAuthorizer{}, Options{LeaseDuration: 30 * time.Second, RetryDelay: 5 * time.Second})

    dispatched, err := dispatcher.DispatchOnce(context.Background(), now)

    if err != nil || !dispatched || outbox.completedID != entry.ID {
        t.Fatalf("DispatchOnce() = %v, %v; completed = %q", dispatched, err, outbox.completedID)
    }
}

func TestDispatchOnceRetriesAuthorizationFailure(t *testing.T) {
    now := time.Date(2026, 7, 18, 10, 0, 0, 0, time.UTC)
    outbox := &memoryOutbox{entry: Entry{ID: "grant/user:user_123/stack:stack_123/owner", Operation: Grant, Subject: "user:user_123", Stack: "stack:stack_123", Role: "owner"}}
    dispatcher := NewDispatcher(outbox, recordingAuthorizer{writeErr: authz.ErrUnavailable}, Options{RetryDelay: 5 * time.Second})

    _, err := dispatcher.DispatchOnce(context.Background(), now)

    if !errors.Is(err, authz.ErrUnavailable) || outbox.retryAt != now.Add(5*time.Second) {
        t.Fatalf("DispatchOnce() error = %v, retryAt = %v", err, outbox.retryAt)
    }
}
```

- [ ] **Step 2: Run the dispatcher tests to verify they fail**

Run: `go test ./internal/authdispatch -run 'TestDispatchOnce' -count=1`

Expected: FAIL because package `internal/authdispatch` does not exist.

- [ ] **Step 3: Add the authorization-outbox migration**

```sql
create table authorization_outbox (
    id text primary key,
    operation text not null check (operation in ('grant', 'revoke')),
    subject text not null,
    stack text not null,
    role text not null check (role in ('owner', 'operator', 'approver', 'viewer')),
    available_at timestamptz not null default now(),
    claimed_until timestamptz,
    attempts integer not null default 0 check (attempts >= 0),
    processed_at timestamptz,
    failed_at timestamptz,
    last_error text not null default '',
    created_at timestamptz not null default now()
);

create index authorization_outbox_pending_idx
    on authorization_outbox (available_at, id)
    where processed_at is null;
```

- [ ] **Step 4: Implement the typed dispatcher**

```go
package authdispatch

type Operation string

const (
    Grant  Operation = "grant"
    Revoke Operation = "revoke"
)

type Entry struct {
    ID        string
    Operation Operation
    Subject   string
    Stack     string
    Role      string
}

type Outbox interface {
    ClaimAuthorizationRelationship(context.Context, time.Time, time.Time) (Entry, bool, error)
    CompleteAuthorizationRelationship(context.Context, string) error
    RetryAuthorizationRelationship(context.Context, string, time.Time, string) error
    FailAuthorizationRelationship(context.Context, string, string) error
}

func (dispatcher *Dispatcher) DispatchOnce(ctx context.Context, now time.Time) (bool, error) {
    entry, found, err := dispatcher.outbox.ClaimAuthorizationRelationship(ctx, now, now.Add(dispatcher.leaseDuration))
    if err != nil || !found { return found, err }
    grant, err := grantFromEntry(entry)
    if err != nil { return true, dispatcher.outbox.FailAuthorizationRelationship(ctx, entry.ID, "invalid authorization relationship intent") }
    mutation, err := authz.NewMutation([]authz.Grant{grant}, true)
    if err != nil { return true, dispatcher.outbox.FailAuthorizationRelationship(ctx, entry.ID, "invalid authorization relationship intent") }
    if entry.Operation == Grant { err = dispatcher.authorizer.WriteRelationships(ctx, mutation) } else { err = dispatcher.authorizer.DeleteRelationships(ctx, mutation) }
    if err != nil { return true, dispatcher.retry(ctx, entry.ID, now, err) }
    return true, dispatcher.outbox.CompleteAuthorizationRelationship(ctx, entry.ID)
}
```

Implement `Run` with the same ticker/cancellation behavior as `dispatch.Dispatcher`. In `retry`, persist only a bounded, context-free error classification such as `authorization unavailable`, `authorization timeout`, `authorization write unconfirmed`, or `authorization delivery failed`; never use `err.Error()` directly.

- [ ] **Step 5: Add the migration assertion and run focused tests**

Add `authorization_outbox` to `TestMigrateAppliesSchema`, and assert its operation constraint, tuple columns, lease columns, processed/failed markers, and partial pending index in a migration-content test.

Run: `go test ./internal/authdispatch ./internal/postgres -run 'TestDispatchOnce|TestMigrateAppliesSchema|TestAuthorizationOutboxMigration' -count=1`

Expected: PASS.

- [ ] **Step 6: Commit the delivery foundation**

```bash
git add internal/authdispatch internal/postgres/migrations/0008_authorization_outbox.sql internal/postgres/store_test.go
git commit -m "feat(authz): add durable relationship dispatcher"
```

### Task 2: Persist Stack Creation With Its Owner Intent

**Files:**
- Modify: `internal/app/service.go:54-60,351-406`
- Modify: `internal/app/stack_authorization_test.go:14-145`
- Modify: `internal/app/service_test.go:81-152`
- Modify: `internal/postgres/repositories.go:328-373`
- Modify: `internal/postgres/store_test.go:520-650`

**Interfaces:**
- Consumes: `authdispatch.Outbox` from Task 1 and the stack/owner `authz.Grant` already constructed by `CreateStack`.
- Produces: `StackRepository.CreateStackWithOwnerIntent(context.Context, traits.Stack, authz.Grant) error`; Postgres persists the stack and outbox row atomically.

- [ ] **Step 1: Write failing application tests for the new repository boundary**

```go
func TestCreateStackPersistsOwnerIntentBeforeDelivery(t *testing.T) {
    stacks := &authorizationStackRepository{}
    service := NewService(Service{Stacks: stacks, Authorizer: &recordingAuthorizer{}, StackIDs: fixedStackIDGenerator{id: "stack_123"}})

    ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: "user_123", RealmRoles: []string{"stack-creator"}})
    _, err := service.CreateStack(ctx, CreateStackCommand{TenantID: "tenant_123", Name: "Acme"})

    if err != nil || stacks.ownerGrant.Role() != authz.RoleOwner {
        t.Fatalf("CreateStack() error = %v, owner grant = %#v", err, stacks.ownerGrant)
    }
}

func TestCreateStackDoesNotDeliverWhenAtomicPersistenceFails(t *testing.T) {
    stacks := &authorizationStackRepository{createErr: errors.New("insert owner intent")}
    authorizer := &recordingAuthorizer{}
    service := NewService(Service{Stacks: stacks, Authorizer: authorizer, StackIDs: fixedStackIDGenerator{id: "stack_123"}})

    ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: "user_123", RealmRoles: []string{"stack-creator"}})
    _, _ = service.CreateStack(ctx, CreateStackCommand{TenantID: "tenant_123", Name: "Acme"})

    if authorizer.calls != 0 { t.Fatal("OpenFGA write occurred after atomic persistence failure") }
}
```

- [ ] **Step 2: Run the application tests to verify they fail**

Run: `go test ./internal/app -run 'TestCreateStack(PersistsOwnerIntent|DoesNotDeliverWhenAtomicPersistenceFails)' -count=1`

Expected: FAIL because `CreateStackWithOwnerIntent` and the recording repository fields do not exist.

- [ ] **Step 3: Replace the stack write boundary and use it in `CreateStack`**

```go
type StackRepository interface {
    CreateStackWithOwnerIntent(context.Context, traits.Stack, authz.Grant) error
    GetStack(context.Context, traits.TenantID, traits.StackID) (traits.Stack, error)
    GetStackWithTemplates(context.Context, traits.TenantID, traits.StackID) (StackView, error)
    ListStacks(context.Context, traits.TenantID) ([]traits.Stack, error)
}

if err := service.Stacks.CreateStackWithOwnerIntent(ctx, stack, grant); err != nil {
    return traits.Stack{}, fmt.Errorf("create stack: %w", err)
}
if err := service.Authorizer.WriteRelationships(ctx, mutation); err != nil {
    return traits.Stack{}, fmt.Errorf("assign stack owner: %w", err)
}
```

Update every in-memory `StackRepository` test double to implement `CreateStackWithOwnerIntent`; make it record the supplied grant before returning its configured error. Keep validation and canonical identifier construction before persistence.

- [ ] **Step 4: Implement the Postgres transaction**

Refactor the existing insert SQL into an unexported `insertStack(ctx, tx, stack)` helper. Implement:

```go
func (store *Store) CreateStackWithOwnerIntent(ctx context.Context, stack traits.Stack, grant authz.Grant) error {
    tx, err := store.pool.Begin(ctx)
    if err != nil { return fmt.Errorf("begin create stack: %w", err) }
    defer tx.Rollback(ctx)
    if err := insertStack(ctx, tx, stack); err != nil { return err }
    _, err = tx.Exec(ctx, `
        insert into authorization_outbox (id, operation, subject, stack, role)
        values ($1, 'grant', $2, $3, $4)
        on conflict (id) do nothing
    `, authorizationOutboxID(authdispatch.Grant, grant), grant.Subject().String(), grant.Stack().String(), grant.Role().String())
    if err != nil { return fmt.Errorf("enqueue stack owner relationship: %w", err) }
    if err := tx.Commit(ctx); err != nil { return fmt.Errorf("commit create stack: %w", err) }
    return nil
}
```

Use `authorizationOutboxID` to return exactly `grant/<subject>/<stack>/<role>`. Preserve `ErrDuplicateStackSlug` mapping from the old method. Do not leave a public standalone `CreateStack` persistence method after all callers move.

- [ ] **Step 5: Add Postgres integration tests for atomicity and deterministic intent**

Test a successful create by querying both `stacks` and `authorization_outbox`, asserting `grant/user:user_123/stack:stack_123/owner`, canonical tuple values, `attempts = 0`, and null `processed_at`. Force owner-intent insertion failure with an invalid role in a direct repository call and assert no stack row exists. Preserve the duplicate-slug test and assert no second outbox row appears.

Run: `go test ./internal/app ./internal/postgres -run 'TestCreateStack|TestCreateStackWithOwnerIntent' -count=1`

Expected: PASS.

- [ ] **Step 6: Commit atomic owner intent persistence**

```bash
git add internal/app/service.go internal/app/service_test.go internal/app/stack_authorization_test.go internal/postgres/repositories.go internal/postgres/store_test.go
git commit -m "feat(authz): persist stack owner intent atomically"
```

### Task 3: Complete And Retry Postgres Authorization Intents

**Files:**
- Modify: `internal/postgres/repositories.go:741-830`
- Modify: `internal/postgres/store_test.go:1269-1319`

**Interfaces:**
- Consumes: `authdispatch.Outbox` defined in Task 1.
- Produces: `Store.ClaimAuthorizationRelationship`, `Store.CompleteAuthorizationRelationship`, and `Store.RetryAuthorizationRelationship`.

- [ ] **Step 1: Write failing Postgres outbox lifecycle tests**

```go
func TestAuthorizationOutboxClaimsRetriesAndCompletesOwnerIntent(t *testing.T) {
    store, pool := migratedStore(t)
    seedAuthorizationIntent(t, pool, "grant/user:user_123/stack:stack_123/owner")
    now := time.Date(2026, 7, 18, 11, 0, 0, 0, time.UTC)

    entry, found, err := store.ClaimAuthorizationRelationship(context.Background(), now, now.Add(30*time.Second))
    if err != nil || !found || entry.Operation != authdispatch.Grant { t.Fatalf("claim = %#v, %v, %v", entry, found, err) }
    if err := store.RetryAuthorizationRelationship(context.Background(), entry.ID, now.Add(time.Minute), "authorization unavailable"); err != nil { t.Fatal(err) }
    if _, found, _ := store.ClaimAuthorizationRelationship(context.Background(), now, now.Add(30*time.Second)); found { t.Fatal("claimed before retry time") }
    if err := store.CompleteAuthorizationRelationship(context.Background(), entry.ID); err != nil { t.Fatal(err) }
}
```

- [ ] **Step 2: Run the lifecycle test to verify it fails**

Run: `go test ./internal/postgres -run TestAuthorizationOutboxClaimsRetriesAndCompletesOwnerIntent -count=1`

Expected: FAIL because the authorization outbox store methods do not exist.

- [ ] **Step 3: Implement claim, retry, and completion operations**

Use the `workflow_outbox` query shape with `authorization_outbox` columns. Claim only rows where `processed_at is null`, `available_at <= $1`, and the lease is missing or expired; lock the candidate with `FOR UPDATE SKIP LOCKED`; set the supplied lease and increment attempts; scan operation, subject, stack, and role; rebuild the `authz.Grant` using `SubjectFromKeycloakSub(strings.TrimPrefix(subject, "user:"))`, `StackFromID(strings.TrimPrefix(stack, "stack:"))`, and `RoleFromDirectRelation(role)`.

On completion, set `processed_at = now()`, clear the lease, and clear `last_error`. On retry, set `available_at`, clear the lease, and save only the already-sanitized error supplied by `authdispatch`. Return wrapped storage errors and treat no candidate as `(Entry{}, false, nil)`.

- [ ] **Step 4: Add lease-expiry and malformed-entry coverage**

Insert an intent with a future `claimed_until`; prove it is not claimable until that time. Insert a row that bypasses application validation with a malformed canonical subject; claiming returns the raw tuple entry, the dispatcher marks it `failed_at` with `invalid authorization relationship intent`, and the row is no longer eligible for delivery.

Run: `go test ./internal/postgres -run 'TestAuthorizationOutbox' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit durable Postgres delivery operations**

```bash
git add internal/postgres/repositories.go internal/postgres/store_test.go
git commit -m "feat(authz): persist authorization delivery retries"
```

### Task 4: Wire Immediate Completion And Background Recovery

**Files:**
- Modify: `internal/app/service.go:351-406`
- Modify: `internal/app/stack_authorization_test.go:67-82`
- Modify: `internal/config/auth.go:96-182`
- Modify: `internal/config/config.go:29-36,100-129`
- Modify: `internal/config/auth_test.go`
- Modify: `internal/config/config_test.go`
- Modify: `cmd/worker/main.go:36-69,78-145,153-212`
- Modify: `cmd/worker/main_test.go:49-125,236-255,257-463`

**Interfaces:**
- Consumes: `authdispatch.Outbox`, `authdispatch.NewDispatcher`, and `config.OpenFGAConfig`.
- Produces: immediate completion of successful API owner writes and a worker lifecycle that retries incomplete OpenFGA mutations.

- [ ] **Step 1: Write failing API/application and worker wiring tests**

```go
func TestCreateStackCompletesOwnerIntentAfterConfirmedWrite(t *testing.T) {
    outbox := &recordingAuthorizationOutbox{}
    stacks := &authorizationStackRepository{}
    service := NewService(Service{Stacks: stacks, AuthorizationOutbox: outbox, Authorizer: &recordingAuthorizer{}, StackIDs: fixedStackIDGenerator{id: "stack_123"}})
    ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: "user_123", RealmRoles: []string{"stack-creator"}})
    _, err := service.CreateStack(ctx, CreateStackCommand{TenantID: "tenant_123", Name: "Acme"})
    if err != nil || outbox.completedID != "grant/user:user_123/stack:stack_123/owner" { t.Fatalf("error = %v, completed = %q", err, outbox.completedID) }
}

func TestRunWiresAuthorizationDispatcher(t *testing.T) {
    deps := newRecordingWorkerDependencies(t)
    if err := runWithDependencies(context.Background(), workerTestEnv, deps.workerDependencies); err != nil { t.Fatal(err) }
    if !deps.authorizationDispatcher.ran || !deps.authorizationDispatcher.stopped { t.Fatal("authorization dispatcher lifecycle was not wired") }
}
```

- [ ] **Step 2: Run the focused tests to verify they fail**

Run: `go test ./internal/app ./cmd/worker -run 'TestCreateStackCompletesOwnerIntentAfterConfirmedWrite|TestRunWiresAuthorizationDispatcher' -count=1`

Expected: FAIL because `AuthorizationOutbox` and worker authorization-dispatcher dependencies do not exist.

- [ ] **Step 3: Mark synchronous successes completed without masking OpenFGA failures**

Add `AuthorizationOutbox authdispatch.Outbox` to `app.Service`. After `WriteRelationships` succeeds, call `CompleteAuthorizationRelationship` with the deterministic owner intent ID. If completion fails, return the wrapped database error; the outbox remains pending and the worker safely repeats confirmed delivery. If OpenFGA delivery fails, do not complete the intent and preserve the existing authorization error for API `503` mapping.

- [ ] **Step 4: Add worker OpenFGA-only configuration**

Extract OpenFGA validation from `loadSecurityConfig` into a helper used by API and worker configuration. Add `OpenFGA OpenFGAConfig` to `WorkerConfig`; `LoadWorkerConfig` must validate `OPENFGA_API_URL`, `OPENFGA_STORE_ID`, `OPENFGA_MODEL_ID`, optional positive `OPENFGA_HTTP_TIMEOUT`, and production HTTPS/token requirements without requiring `OIDC_ISSUER_URL`, `OIDC_AUDIENCE`, or `TFLIVE_TENANT_ID`.

Add tests for missing OpenFGA URL, store ID, model ID, invalid timeout, and production missing token. Assert `WorkerConfig` does not contain OIDC or tenant fields.

- [ ] **Step 5: Wire the worker dispatcher and safe shutdown**

Extend `workerStore` with `authdispatch.Outbox`. Add injected `newAuthorizationAdapter(config.OpenFGAConfig) (authz.Authorizer, error)` and `newAuthorizationDispatcher(authdispatch.Outbox, authz.Authorizer) outboxDispatcher` dependencies. Construct `openfga.NewAuthorizationAdapter` from worker OpenFGA configuration, start the authorization dispatcher in its own goroutine with the same `dispatchCtx`, and wait for both dispatchers after the Temporal worker exits.

Update recording worker dependencies and `workerTestEnv` with `OPENFGA_API_URL=http://localhost:8080`, `OPENFGA_STORE_ID=store_123`, and `OPENFGA_MODEL_ID=model_123`. Assert adapter configuration, start, stop, and failure wrapping.

- [ ] **Step 6: Run wiring and service tests**

Run: `go test ./internal/app ./internal/config ./cmd/worker -count=1`

Expected: PASS.

- [ ] **Step 7: Commit API completion and worker recovery wiring**

```bash
git add internal/app internal/config cmd/worker
git commit -m "feat(authz): recover pending OpenFGA relationships"
```

### Task 5: Document And Verify AUTH-012

**Files:**
- Modify: `docs/authentication.md:243-259`
- Modify: `docs/sprint/authn_and_authz/README.md:75`

**Interfaces:**
- Consumes: completed authorization outbox behavior from Tasks 1-4.
- Produces: operator-facing recovery instructions and the completed sprint status.

- [ ] **Step 1: Update authentication documentation**

Replace the AUTH-012 future-tense note with the concrete behavior: stack creation atomically stores an initial owner intent; an immediate OpenFGA failure returns `503` but leaves a pending retry; the worker retries confirmed desired-state operations; and operators inspect `authorization_outbox` fields `attempts`, `available_at`, and `last_error` to reconcile a terminal entry. State that errors are sanitized and the table never stores credentials or tokens.

- [ ] **Step 2: Update sprint status**

Change the AUTH-012 status from `Not Started` to `Done` only after the verification step passes. Keep AUTH-013 and AUTH-017 dependencies unchanged.

- [ ] **Step 3: Run the complete backend suite**

Run: `go test ./...`

Expected: PASS.

- [ ] **Step 4: Inspect the final change set**

Run: `git status --short && git diff --check && git diff HEAD~4..HEAD`

Expected: only authorization-outbox implementation, tests, worker configuration/wiring, and AUTH-012 documentation changes; no secrets or tokens.

- [ ] **Step 5: Commit documentation and status**

```bash
git add docs/authentication.md docs/sprint/authn_and_authz/README.md
git commit -m "docs(authz): document relationship recovery"
```
