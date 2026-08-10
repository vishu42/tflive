# Generic Work Queue Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the two copy-pasted outbox tables and dispatchers with one generic `work_queue` and one controller that routes items to handlers registered by kind, closing the unprotected dual-write in `AssignStackRole`/`RevokeStackRole`.

**Architecture:** Three layers. `internal/queue` holds pure domain types, a handler registry, and a controller — it never parses a payload or touches SQL. `internal/postgres` implements the storage interfaces over one `work_queue` table whose unique partial index provides both coalescing and per-key mutual exclusion. Domain packages own one handler each; `internal/authdispatch` shrinks from a dispatcher loop to a handler wrapping `authz.Authorizer`.

**Tech Stack:** Go, pgx/v5, Postgres, OpenFGA. No new dependencies.

**Design doc:** `docs/superpowers/specs/2026-08-05-generic-work-queue-design.md`

## Global Constraints

- Module path is `github.com/vishu42/tflive`. All internal imports use that prefix.
- Postgres integration tests skip unless `tflive_POSTGRES_TEST_DSN` is set. Use the existing `openMigratedTestPool(t, ctx)` helper in `internal/postgres/store_test.go`; it creates an isolated schema per test and registers cleanup.
- All tests call `t.Parallel()` as the first statement, matching the existing suite.
- Migrations are embedded via `//go:embed migrations/*.sql` and applied in filename order. New migration is `0012_work_queue.sql`.
- Error wrapping uses `fmt.Errorf("verb noun: %w", err)` — lowercase, no trailing punctuation, matching `internal/postgres/repositories.go`.
- Named receivers are spelled out (`func (store *Store)`, `func (adapter *AuthorizationAdapter)`), never single letters.
- SQL keywords are lowercase throughout, matching existing migrations and queries.
- `workflow_outbox`, `internal/dispatch`, and notification delivery are **out of scope**. Do not modify them.

## File Structure

| File | Responsibility |
| --- | --- |
| `internal/queue/queue.go` | `Kind`, `Mode`, `Request`, `Item`, `Status`, `State`, and the `Handler` / `Enqueuer` / `Backend` / `Reader` interfaces. Replaces the draft `internal/queue/main.go`. |
| `internal/queue/registry.go` | `Registry`: kind → handler lookup, key/mode resolution, duplicate rejection. |
| `internal/queue/controller.go` | Poll loop, batch claim, worker pool fan-out, backoff, settle. |
| `internal/queue/memory.go` | In-memory `Backend` for controller tests. |
| `internal/postgres/migrations/0012_work_queue.sql` | Table, three indexes, backfill from `authorization_outbox`. |
| `internal/postgres/workqueue.go` | `Enqueue`, `Claim`, `Complete`, `Reschedule`, `Prune`, `ListByActor`, and the tx-bound enqueuer. |
| `internal/postgres/unitofwork.go` | `InTx` — hands a callback a tx-scoped repo and a tx-bound enqueuer. |
| `internal/authdispatch/handler.go` | `reconcile_stack_grant` handler. Replaces `dispatcher.go`. |
| `internal/openfga/authorization_adapter.go` | Add `ListSubjectGrants` — a `read` filtered by user as well as object. |
| `internal/app/service.go` | `CreateStack`, `AssignStackRole`, `RevokeStackRole` enqueue instead of calling OpenFGA. |
| `internal/api/server.go` | `GET /v1/tenants/{tenant_id}/queue`. |
| `cmd/worker/main.go` | Run the controller instead of `authdispatch.Dispatcher`. |

---

### Task 1: Queue domain types and interfaces

**Files:**
- Create: `internal/queue/queue.go`
- Delete: `internal/queue/main.go`
- Test: `internal/queue/queue_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `Kind string`, `Mode int` (`ModeReconcile`, `ModeJob`), `Request{Kind, Payload, ActorSubject, TenantID}`, `Item{ID int64, Kind Kind, Key string, Payload json.RawMessage, Revision int64, ActorSubject, TenantID string, Attempts int}`, `State string` (`StatePending`, `StateDelivered`), `Status{...}`, and interfaces `Handler`, `Describer`, `Timings`, `Enqueuer`, `Backend`, `Reader`.

The existing `internal/queue/main.go` draft has three type errors to correct: `Revision` must be `int64` (it is a `bigint` used in arithmetic and in the fence comparison), `DeliveredAt` must be `*time.Time` (pending items have none), and `State` needs a terminal success value. Retry is forever, so there is no terminal error state — a failing item is `StatePending` with `Attempts > 1` and a non-empty `LastError`.

- [ ] **Step 1: Write the failing test**

```go
package queue

import (
	"encoding/json"
	"testing"
)

func TestItemRevisionIsNumeric(t *testing.T) {
	t.Parallel()

	item := Item{ID: 7, Revision: 3, Attempts: 2, Payload: json.RawMessage(`{}`)}
	if item.Revision+1 != 4 {
		t.Fatalf("Revision must support arithmetic, got %d", item.Revision)
	}
}

func TestStateConstants(t *testing.T) {
	t.Parallel()

	if StatePending == StateDelivered {
		t.Fatal("StatePending and StateDelivered must differ")
	}
	if StatePending != "pending" || StateDelivered != "delivered" {
		t.Fatalf("unexpected state values: %q %q", StatePending, StateDelivered)
	}
}

func TestModeConstantsAreDistinct(t *testing.T) {
	t.Parallel()

	if ModeReconcile == ModeJob {
		t.Fatal("ModeReconcile and ModeJob must differ")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/queue/ -run 'TestItemRevision|TestState|TestMode' -v`
Expected: FAIL — compile error, `StatePending` undefined and `Revision` is a string.

- [ ] **Step 3: Write the implementation**

Delete `internal/queue/main.go`, create `internal/queue/queue.go`:

```go
// Package queue delivers durable work intents to external systems.
//
// Callers enqueue a Request inside their own transaction. A Controller later
// claims it and routes it to the Handler registered for its Kind. The queue
// never parses a payload; kinds own their own schemas.
package queue

import (
	"context"
	"encoding/json"
	"time"
)

// Kind names a category of work. Each Kind has exactly one registered Handler.
type Kind string

// Mode decides how repeated intents for the same key behave.
type Mode int

const (
	// ModeReconcile coalesces: the payload is desired state, so repeated
	// intents for one key collapse into the newest. Handlers are inherently
	// replay safe because re-applying desired state is a no-op.
	ModeReconcile Mode = iota
	// ModeJob keeps every distinct item: the payload is the work itself and
	// collapsing would lose it. Re-enqueueing an identical key is a no-op.
	ModeJob
)

// State is the lifecycle position reported to the caller who queued an item.
// There is no terminal failure state: retry is forever with capped backoff, so
// a failing item stays StatePending with a non-empty LastError.
type State string

const (
	StatePending   State = "pending"
	StateDelivered State = "delivered"
)

// Request is what callers enqueue. The resource key is derived by the Handler,
// never supplied by the caller, so Mode and key shape cannot disagree.
type Request struct {
	Kind         Kind
	Payload      json.RawMessage
	ActorSubject string
	TenantID     string
}

// Item is a claimed row handed to a Handler.
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

// Status is one row of the caller's own queue history.
type Status struct {
	Kind        Kind
	State       State
	Summary     string
	Attempts    int
	LastError   string
	CreatedAt   time.Time
	DeliveredAt *time.Time
}

// Handler is implemented by whichever package owns the target external system.
//
// Deliver MUST be idempotent. The queue guarantees at-least-once delivery: a
// crash between the external call and the completing commit replays the item,
// and a lease that expires while a slow worker is still running lets a second
// worker deliver concurrently.
//
// Key derivation is a frozen contract. Keys are persisted, so changing the
// derivation splits one resource across two key formats and disables the
// mutual exclusion the unique index provides. To change it, either drain the
// queue with producers stopped, or introduce a new Kind and let the old one
// drain on the old format.
type Handler interface {
	Kind() Kind
	Mode() Mode
	Key(payload json.RawMessage) (string, error)
	Deliver(ctx context.Context, item Item) error
}

// Describer renders a payload for the queue read API so that layer never
// parses kind-specific JSON. Optional; the Kind name is used when absent.
type Describer interface {
	Describe(payload json.RawMessage) string
}

// Timings lets a Handler override controller defaults. Optional.
type Timings interface {
	Lease() time.Duration
	MaxBackoff() time.Duration
}

// Enqueuer is implemented by the store. Implementations may be bound to an
// open transaction — that is the entire reason this is an outbox rather than a
// message broker, and why a broker cannot implement it.
type Enqueuer interface {
	Enqueue(ctx context.Context, requests ...Request) error
}

// Backend is the delivery seam: lease, settle, prune.
type Backend interface {
	Claim(ctx context.Context, now, leaseUntil time.Time, limit int, kinds []Kind) ([]Item, error)
	// Complete reports false when the revision moved while the item was in
	// flight, meaning a newer intent arrived and the item must run again.
	Complete(ctx context.Context, id, revision int64) (bool, error)
	Reschedule(ctx context.Context, id int64, availableAt time.Time, lastErr string) error
	Prune(ctx context.Context, before time.Time) (int64, error)
}

// Reader serves the queue read API.
type Reader interface {
	ListByActor(ctx context.Context, tenantID, actorSubject string, limit int) ([]Status, error)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/queue/ -v`
Expected: PASS, 3 tests.

- [ ] **Step 5: Commit**

```bash
git add internal/queue/
git commit -m "feat(queue): add domain types and interfaces"
```

---

### Task 2: Handler registry

**Files:**
- Create: `internal/queue/registry.go`
- Test: `internal/queue/registry_test.go`

**Interfaces:**
- Consumes: `Handler`, `Kind`, `Mode`, `Request` from Task 1.
- Produces: `Resolved{Kind Kind, Key string, Mode Mode, Payload json.RawMessage, ActorSubject, TenantID string}`, `NewRegistry(handlers ...Handler) (*Registry, error)`, `(*Registry).Handler(Kind) (Handler, bool)`, `(*Registry).Resolve(Request) (Resolved, error)`, `(*Registry).Kinds() []Kind`, and sentinel `ErrUnknownKind`.

`Resolve` is what turns a caller's `Request` into the columns a row needs. The store calls it so callers never see a key.

- [ ] **Step 1: Write the failing test**

```go
package queue

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

type stubHandler struct {
	kind Kind
	mode Mode
	key  string
	err  error
}

func (h stubHandler) Kind() Kind { return h.kind }
func (h stubHandler) Mode() Mode { return h.mode }
func (h stubHandler) Key(json.RawMessage) (string, error) {
	if h.err != nil {
		return "", h.err
	}
	return h.key, nil
}
func (h stubHandler) Deliver(context.Context, Item) error { return nil }

func TestNewRegistryRejectsDuplicateKinds(t *testing.T) {
	t.Parallel()

	_, err := NewRegistry(
		stubHandler{kind: "a", key: "k"},
		stubHandler{kind: "a", key: "k"},
	)
	if err == nil {
		t.Fatal("NewRegistry accepted a duplicate kind")
	}
}

func TestNewRegistryRejectsEmptyKind(t *testing.T) {
	t.Parallel()

	if _, err := NewRegistry(stubHandler{kind: "", key: "k"}); err == nil {
		t.Fatal("NewRegistry accepted an empty kind")
	}
}

func TestResolveDerivesKeyAndMode(t *testing.T) {
	t.Parallel()

	registry, err := NewRegistry(stubHandler{kind: "grant", mode: ModeReconcile, key: "stack:a/user:x"})
	if err != nil {
		t.Fatalf("NewRegistry returned error: %v", err)
	}

	resolved, err := registry.Resolve(Request{
		Kind:         "grant",
		Payload:      json.RawMessage(`{"role":"owner"}`),
		ActorSubject: "user:x",
		TenantID:     "tenant_1",
	})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if resolved.Key != "stack:a/user:x" {
		t.Fatalf("Key = %q, want stack:a/user:x", resolved.Key)
	}
	if resolved.Mode != ModeReconcile {
		t.Fatalf("Mode = %v, want ModeReconcile", resolved.Mode)
	}
	if resolved.TenantID != "tenant_1" {
		t.Fatalf("TenantID = %q, want tenant_1", resolved.TenantID)
	}
}

func TestResolveUnknownKind(t *testing.T) {
	t.Parallel()

	registry, err := NewRegistry(stubHandler{kind: "grant", key: "k"})
	if err != nil {
		t.Fatalf("NewRegistry returned error: %v", err)
	}
	if _, err := registry.Resolve(Request{Kind: "nope"}); !errors.Is(err, ErrUnknownKind) {
		t.Fatalf("Resolve error = %v, want ErrUnknownKind", err)
	}
}

func TestResolveRejectsEmptyKey(t *testing.T) {
	t.Parallel()

	registry, err := NewRegistry(stubHandler{kind: "grant", key: ""})
	if err != nil {
		t.Fatalf("NewRegistry returned error: %v", err)
	}
	if _, err := registry.Resolve(Request{Kind: "grant"}); err == nil {
		t.Fatal("Resolve accepted an empty derived key")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/queue/ -run TestRegistry -v`
Expected: FAIL — `NewRegistry` undefined.

- [ ] **Step 3: Write the implementation**

```go
package queue

import (
	"encoding/json"
	"errors"
	"fmt"
)

// ErrUnknownKind reports a Kind with no registered Handler.
var ErrUnknownKind = errors.New("queue: unknown kind")

// Resolved is a Request with the handler-derived fields filled in. It maps
// one-to-one onto the columns of a work_queue row.
type Resolved struct {
	Kind         Kind
	Key          string
	Mode         Mode
	Payload      json.RawMessage
	ActorSubject string
	TenantID     string
}

// Registry maps a Kind to its Handler.
type Registry struct {
	handlers map[Kind]Handler
}

// NewRegistry indexes handlers by kind and rejects duplicates at construction
// so a misconfigured binary fails at startup rather than at delivery time.
func NewRegistry(handlers ...Handler) (*Registry, error) {
	indexed := make(map[Kind]Handler, len(handlers))
	for _, handler := range handlers {
		kind := handler.Kind()
		if kind == "" {
			return nil, fmt.Errorf("queue: handler has an empty kind")
		}
		if _, duplicate := indexed[kind]; duplicate {
			return nil, fmt.Errorf("queue: duplicate handler for kind %q", kind)
		}
		indexed[kind] = handler
	}
	return &Registry{handlers: indexed}, nil
}

// Handler returns the handler registered for kind.
func (registry *Registry) Handler(kind Kind) (Handler, bool) {
	handler, ok := registry.handlers[kind]
	return handler, ok
}

// Kinds returns every registered kind. Order is unspecified.
func (registry *Registry) Kinds() []Kind {
	kinds := make([]Kind, 0, len(registry.handlers))
	for kind := range registry.handlers {
		kinds = append(kinds, kind)
	}
	return kinds
}

// Resolve derives the resource key and mode for a request.
func (registry *Registry) Resolve(request Request) (Resolved, error) {
	handler, ok := registry.handlers[request.Kind]
	if !ok {
		return Resolved{}, fmt.Errorf("%w: %q", ErrUnknownKind, request.Kind)
	}
	key, err := handler.Key(request.Payload)
	if err != nil {
		return Resolved{}, fmt.Errorf("derive %s key: %w", request.Kind, err)
	}
	if key == "" {
		return Resolved{}, fmt.Errorf("queue: handler for kind %q derived an empty key", request.Kind)
	}
	return Resolved{
		Kind:         request.Kind,
		Key:          key,
		Mode:         handler.Mode(),
		Payload:      request.Payload,
		ActorSubject: request.ActorSubject,
		TenantID:     request.TenantID,
	}, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/queue/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/queue/registry.go internal/queue/registry_test.go
git commit -m "feat(queue): add handler registry with key and mode resolution"
```

---

### Task 3: In-memory backend

**Files:**
- Create: `internal/queue/memory.go`
- Test: `internal/queue/memory_test.go`

**Interfaces:**
- Consumes: `Backend`, `Item`, `Kind` from Task 1.
- Produces: `NewMemoryBackend() *MemoryBackend`, `(*MemoryBackend).Add(Item)`, and the `Backend` methods. Task 4 uses this to test the controller without a database.

Semantics deliberately mirror the SQL: claim respects `availableAt` and lease, complete is fenced on revision, reschedule clears the lease.

- [ ] **Step 1: Write the failing test**

```go
package queue

import (
	"context"
	"testing"
	"time"
)

func TestMemoryBackendClaimRespectsAvailableAt(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC)
	backend := NewMemoryBackend()
	backend.Add(Item{ID: 1, Kind: "a", Revision: 1})
	backend.AddAt(Item{ID: 2, Kind: "a", Revision: 1}, now.Add(time.Minute))

	claimed, err := backend.Claim(context.Background(), now, now.Add(30*time.Second), 10, nil)
	if err != nil {
		t.Fatalf("Claim returned error: %v", err)
	}
	if len(claimed) != 1 || claimed[0].ID != 1 {
		t.Fatalf("Claim returned %+v, want only item 1", claimed)
	}
}

func TestMemoryBackendClaimSkipsLeasedItems(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC)
	backend := NewMemoryBackend()
	backend.Add(Item{ID: 1, Kind: "a", Revision: 1})

	if _, err := backend.Claim(context.Background(), now, now.Add(30*time.Second), 10, nil); err != nil {
		t.Fatalf("first Claim returned error: %v", err)
	}
	claimed, err := backend.Claim(context.Background(), now.Add(time.Second), now.Add(31*time.Second), 10, nil)
	if err != nil {
		t.Fatalf("second Claim returned error: %v", err)
	}
	if len(claimed) != 0 {
		t.Fatalf("Claim returned %d leased items, want 0", len(claimed))
	}
}

func TestMemoryBackendClaimReclaimsExpiredLease(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC)
	backend := NewMemoryBackend()
	backend.Add(Item{ID: 1, Kind: "a", Revision: 1})

	if _, err := backend.Claim(context.Background(), now, now.Add(30*time.Second), 10, nil); err != nil {
		t.Fatalf("first Claim returned error: %v", err)
	}
	claimed, err := backend.Claim(context.Background(), now.Add(31*time.Second), now.Add(61*time.Second), 10, nil)
	if err != nil {
		t.Fatalf("second Claim returned error: %v", err)
	}
	if len(claimed) != 1 || claimed[0].Attempts != 2 {
		t.Fatalf("Claim returned %+v, want item 1 with Attempts 2", claimed)
	}
}

func TestMemoryBackendClaimFiltersKinds(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC)
	backend := NewMemoryBackend()
	backend.Add(Item{ID: 1, Kind: "a", Revision: 1})
	backend.Add(Item{ID: 2, Kind: "b", Revision: 1})

	claimed, err := backend.Claim(context.Background(), now, now.Add(30*time.Second), 10, []Kind{"b"})
	if err != nil {
		t.Fatalf("Claim returned error: %v", err)
	}
	if len(claimed) != 1 || claimed[0].Kind != "b" {
		t.Fatalf("Claim returned %+v, want only kind b", claimed)
	}
}

func TestMemoryBackendCompleteRejectsStaleRevision(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC)
	backend := NewMemoryBackend()
	backend.Add(Item{ID: 1, Kind: "a", Revision: 1})
	if _, err := backend.Claim(context.Background(), now, now.Add(30*time.Second), 10, nil); err != nil {
		t.Fatalf("Claim returned error: %v", err)
	}
	backend.Bump(1)

	completed, err := backend.Complete(context.Background(), 1, 1)
	if err != nil {
		t.Fatalf("Complete returned error: %v", err)
	}
	if completed {
		t.Fatal("Complete accepted a stale revision")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/queue/ -run TestMemoryBackend -v`
Expected: FAIL — `NewMemoryBackend` undefined.

- [ ] **Step 3: Write the implementation**

```go
package queue

import (
	"context"
	"sort"
	"sync"
	"time"
)

// MemoryBackend is an in-memory Backend for tests. Its semantics mirror the
// Postgres implementation: claims respect availableAt and the lease, Complete
// is fenced on revision, and Reschedule clears the lease.
type MemoryBackend struct {
	mutex sync.Mutex
	rows  map[int64]*memoryRow
}

type memoryRow struct {
	item         Item
	availableAt  time.Time
	claimedUntil time.Time
	processedAt  time.Time
	lastError    string
}

func NewMemoryBackend() *MemoryBackend {
	return &MemoryBackend{rows: map[int64]*memoryRow{}}
}

// Add makes an item immediately claimable.
func (backend *MemoryBackend) Add(item Item) {
	backend.AddAt(item, time.Time{})
}

// AddAt makes an item claimable no earlier than availableAt.
func (backend *MemoryBackend) AddAt(item Item, availableAt time.Time) {
	backend.mutex.Lock()
	defer backend.mutex.Unlock()
	backend.rows[item.ID] = &memoryRow{item: item, availableAt: availableAt}
}

// Bump simulates a newer intent coalescing onto an in-flight row.
func (backend *MemoryBackend) Bump(id int64) {
	backend.mutex.Lock()
	defer backend.mutex.Unlock()
	if row, ok := backend.rows[id]; ok {
		row.item.Revision++
	}
}

// Pending reports how many rows have not been completed.
func (backend *MemoryBackend) Pending() int {
	backend.mutex.Lock()
	defer backend.mutex.Unlock()
	pending := 0
	for _, row := range backend.rows {
		if row.processedAt.IsZero() {
			pending++
		}
	}
	return pending
}

// LastError returns the recorded failure text for a row.
func (backend *MemoryBackend) LastError(id int64) string {
	backend.mutex.Lock()
	defer backend.mutex.Unlock()
	if row, ok := backend.rows[id]; ok {
		return row.lastError
	}
	return ""
}

func (backend *MemoryBackend) Claim(_ context.Context, now, leaseUntil time.Time, limit int, kinds []Kind) ([]Item, error) {
	backend.mutex.Lock()
	defer backend.mutex.Unlock()

	allowed := map[Kind]struct{}{}
	for _, kind := range kinds {
		allowed[kind] = struct{}{}
	}

	var eligible []*memoryRow
	for _, row := range backend.rows {
		if !row.processedAt.IsZero() {
			continue
		}
		if row.availableAt.After(now) {
			continue
		}
		if !row.claimedUntil.IsZero() && row.claimedUntil.After(now) {
			continue
		}
		if len(allowed) > 0 {
			if _, ok := allowed[row.item.Kind]; !ok {
				continue
			}
		}
		eligible = append(eligible, row)
	}
	sort.Slice(eligible, func(i, j int) bool {
		if !eligible[i].availableAt.Equal(eligible[j].availableAt) {
			return eligible[i].availableAt.Before(eligible[j].availableAt)
		}
		return eligible[i].item.ID < eligible[j].item.ID
	})
	if limit > 0 && len(eligible) > limit {
		eligible = eligible[:limit]
	}

	claimed := make([]Item, 0, len(eligible))
	for _, row := range eligible {
		row.claimedUntil = leaseUntil
		row.item.Attempts++
		claimed = append(claimed, row.item)
	}
	return claimed, nil
}

func (backend *MemoryBackend) Complete(_ context.Context, id, revision int64) (bool, error) {
	backend.mutex.Lock()
	defer backend.mutex.Unlock()

	row, ok := backend.rows[id]
	if !ok || !row.processedAt.IsZero() || row.item.Revision != revision {
		return false, nil
	}
	row.processedAt = time.Now()
	row.claimedUntil = time.Time{}
	row.lastError = ""
	return true, nil
}

func (backend *MemoryBackend) Reschedule(_ context.Context, id int64, availableAt time.Time, lastErr string) error {
	backend.mutex.Lock()
	defer backend.mutex.Unlock()

	if row, ok := backend.rows[id]; ok {
		row.claimedUntil = time.Time{}
		row.availableAt = availableAt
		row.lastError = lastErr
	}
	return nil
}

func (backend *MemoryBackend) Prune(_ context.Context, before time.Time) (int64, error) {
	backend.mutex.Lock()
	defer backend.mutex.Unlock()

	var pruned int64
	for id, row := range backend.rows {
		if !row.processedAt.IsZero() && row.processedAt.Before(before) {
			delete(backend.rows, id)
			pruned++
		}
	}
	return pruned, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/queue/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/queue/memory.go internal/queue/memory_test.go
git commit -m "feat(queue): add in-memory backend for controller tests"
```

---

### Task 4: Controller

**Files:**
- Create: `internal/queue/controller.go`
- Test: `internal/queue/controller_test.go`

**Interfaces:**
- Consumes: `Registry` (Task 2), `Backend` (Task 1), `MemoryBackend` (Task 3).
- Produces: `Options{PollInterval, Lease, BaseBackoff, MaxBackoff, PruneAfter, PruneInterval time.Duration, BatchSize, Workers int}`, `NewController(Backend, *Registry, Options) *Controller`, `(*Controller).DispatchOnce(ctx context.Context, now time.Time) (int, error)`, `(*Controller).Run(ctx context.Context)`.

Behaviour required by the design:
- Unknown kind is **rescheduled, never failed** — during a rolling deploy the API can produce kinds the worker does not know yet, and failing them would drop work permanently.
- Backoff is `BaseBackoff * 2^(attempts-1)`, jittered, capped at `MaxBackoff` or the handler's `MaxBackoff()`. Jitter prevents a thundering herd when a downed target recovers and every stuck item becomes available at once.
- `Complete` returning false means a newer intent arrived mid-flight: reschedule immediately rather than completing, so the fresher payload runs.

- [ ] **Step 1: Write the failing test**

```go
package queue

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
)

type recordingHandler struct {
	stubHandler
	mutex     sync.Mutex
	delivered []Item
	deliverEr error
}

func (h *recordingHandler) Deliver(_ context.Context, item Item) error {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	h.delivered = append(h.delivered, item)
	return h.deliverEr
}

func (h *recordingHandler) count() int {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	return len(h.delivered)
}

func testOptions() Options {
	return Options{
		PollInterval: time.Millisecond,
		Lease:        30 * time.Second,
		BaseBackoff:  time.Second,
		MaxBackoff:   5 * time.Minute,
		BatchSize:    10,
		Workers:      2,
	}
}

func TestDispatchOnceCompletesDeliveredItem(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC)
	handler := &recordingHandler{stubHandler: stubHandler{kind: "a", key: "k"}}
	registry, err := NewRegistry(handler)
	if err != nil {
		t.Fatalf("NewRegistry returned error: %v", err)
	}
	backend := NewMemoryBackend()
	backend.Add(Item{ID: 1, Kind: "a", Revision: 1, Payload: json.RawMessage(`{}`)})

	controller := NewController(backend, registry, testOptions())
	processed, err := controller.DispatchOnce(context.Background(), now)
	if err != nil {
		t.Fatalf("DispatchOnce returned error: %v", err)
	}
	if processed != 1 {
		t.Fatalf("processed = %d, want 1", processed)
	}
	if handler.count() != 1 {
		t.Fatalf("handler delivered %d items, want 1", handler.count())
	}
	if backend.Pending() != 0 {
		t.Fatalf("Pending = %d, want 0", backend.Pending())
	}
}

func TestDispatchOnceReschedulesFailedItem(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC)
	handler := &recordingHandler{
		stubHandler: stubHandler{kind: "a", key: "k"},
		deliverEr:   errors.New("openfga unavailable"),
	}
	registry, err := NewRegistry(handler)
	if err != nil {
		t.Fatalf("NewRegistry returned error: %v", err)
	}
	backend := NewMemoryBackend()
	backend.Add(Item{ID: 1, Kind: "a", Revision: 1, Payload: json.RawMessage(`{}`)})

	controller := NewController(backend, registry, testOptions())
	if _, err := controller.DispatchOnce(context.Background(), now); err != nil {
		t.Fatalf("DispatchOnce returned error: %v", err)
	}
	if backend.Pending() != 1 {
		t.Fatalf("Pending = %d, want 1", backend.Pending())
	}
	if backend.LastError(1) == "" {
		t.Fatal("LastError was not recorded")
	}
}

func TestDispatchOnceReschedulesUnknownKind(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC)
	registry, err := NewRegistry(stubHandler{kind: "a", key: "k"})
	if err != nil {
		t.Fatalf("NewRegistry returned error: %v", err)
	}
	backend := NewMemoryBackend()
	backend.Add(Item{ID: 1, Kind: "unregistered", Revision: 1, Payload: json.RawMessage(`{}`)})

	controller := NewController(backend, registry, testOptions())
	if _, err := controller.DispatchOnce(context.Background(), now); err != nil {
		t.Fatalf("DispatchOnce returned error: %v", err)
	}
	if backend.Pending() != 1 {
		t.Fatalf("Pending = %d, want 1 — an unknown kind must never be dropped", backend.Pending())
	}
}

func TestDispatchOnceReschedulesWhenRevisionMoved(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC)
	handler := &recordingHandler{stubHandler: stubHandler{kind: "a", key: "k"}}
	registry, err := NewRegistry(handler)
	if err != nil {
		t.Fatalf("NewRegistry returned error: %v", err)
	}
	backend := NewMemoryBackend()
	backend.Add(Item{ID: 1, Kind: "a", Revision: 1, Payload: json.RawMessage(`{}`)})

	controller := NewController(backend, registry, testOptions())
	controller.beforeComplete = func() { backend.Bump(1) }

	if _, err := controller.DispatchOnce(context.Background(), now); err != nil {
		t.Fatalf("DispatchOnce returned error: %v", err)
	}
	if backend.Pending() != 1 {
		t.Fatalf("Pending = %d, want 1 — a newer intent must re-run", backend.Pending())
	}
}

func TestBackoffGrowsAndCaps(t *testing.T) {
	t.Parallel()

	options := testOptions()
	controller := NewController(NewMemoryBackend(), &Registry{}, options)

	first := controller.backoff(1, options.MaxBackoff)
	fourth := controller.backoff(4, options.MaxBackoff)
	huge := controller.backoff(40, options.MaxBackoff)

	if fourth <= first {
		t.Fatalf("backoff did not grow: attempt 1 = %v, attempt 4 = %v", first, fourth)
	}
	if huge > options.MaxBackoff {
		t.Fatalf("backoff %v exceeded cap %v", huge, options.MaxBackoff)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/queue/ -run 'TestDispatchOnce|TestBackoff' -v`
Expected: FAIL — `NewController` undefined.

- [ ] **Step 3: Write the implementation**

```go
package queue

import (
	"context"
	"log"
	"math"
	"math/rand"
	"sync"
	"time"
)

const (
	defaultPollInterval  = time.Second
	defaultLease         = 30 * time.Second
	defaultBaseBackoff   = time.Second
	defaultMaxBackoff    = 5 * time.Minute
	defaultBatchSize     = 20
	defaultWorkers       = 4
	defaultPruneAfter    = 24 * time.Hour
	defaultPruneInterval = 10 * time.Minute
)

// Options tunes the controller. Zero values fall back to defaults.
type Options struct {
	PollInterval  time.Duration
	Lease         time.Duration
	BaseBackoff   time.Duration
	MaxBackoff    time.Duration
	PruneAfter    time.Duration
	PruneInterval time.Duration
	BatchSize     int
	Workers       int
	Kinds         []Kind // optional allowlist, for sharding controllers by kind
}

func (options Options) withDefaults() Options {
	if options.PollInterval <= 0 {
		options.PollInterval = defaultPollInterval
	}
	if options.Lease <= 0 {
		options.Lease = defaultLease
	}
	if options.BaseBackoff <= 0 {
		options.BaseBackoff = defaultBaseBackoff
	}
	if options.MaxBackoff <= 0 {
		options.MaxBackoff = defaultMaxBackoff
	}
	if options.PruneAfter <= 0 {
		options.PruneAfter = defaultPruneAfter
	}
	if options.PruneInterval <= 0 {
		options.PruneInterval = defaultPruneInterval
	}
	if options.BatchSize <= 0 {
		options.BatchSize = defaultBatchSize
	}
	if options.Workers <= 0 {
		options.Workers = defaultWorkers
	}
	return options
}

// Controller claims batches of work and routes each item to its handler.
type Controller struct {
	backend  Backend
	registry *Registry
	options  Options

	// beforeComplete is a test seam for simulating a newer intent arriving
	// while an item is in flight.
	beforeComplete func()
}

func NewController(backend Backend, registry *Registry, options Options) *Controller {
	return &Controller{backend: backend, registry: registry, options: options.withDefaults()}
}

// DispatchOnce claims one batch and processes it, returning how many items were
// handled. An error means the claim itself failed; per-item failures are
// recorded on the row and do not surface here.
func (controller *Controller) DispatchOnce(ctx context.Context, now time.Time) (int, error) {
	items, err := controller.backend.Claim(ctx, now, now.Add(controller.options.Lease), controller.options.BatchSize, controller.options.Kinds)
	if err != nil {
		return 0, err
	}
	if len(items) == 0 {
		return 0, nil
	}

	work := make(chan Item)
	var group sync.WaitGroup
	for worker := 0; worker < controller.options.Workers; worker++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for item := range work {
				controller.process(ctx, item, now)
			}
		}()
	}
	for _, item := range items {
		work <- item
	}
	close(work)
	group.Wait()

	return len(items), nil
}

func (controller *Controller) process(ctx context.Context, item Item, now time.Time) {
	handler, ok := controller.registry.Handler(item.Kind)
	if !ok {
		// Never fail an unknown kind. During a rolling deploy the producer can
		// be ahead of this worker; dropping the item would lose work forever.
		controller.reschedule(ctx, item, now, defaultMaxBackoff, "no handler registered for kind "+string(item.Kind))
		return
	}

	maxBackoff := controller.options.MaxBackoff
	if timings, ok := handler.(Timings); ok && timings.MaxBackoff() > 0 {
		maxBackoff = timings.MaxBackoff()
	}

	if err := handler.Deliver(ctx, item); err != nil {
		controller.reschedule(ctx, item, now, maxBackoff, err.Error())
		return
	}

	if controller.beforeComplete != nil {
		controller.beforeComplete()
	}

	completed, err := controller.backend.Complete(ctx, item.ID, item.Revision)
	if err != nil {
		log.Printf("queue: complete %s item %d: %v", item.Kind, item.ID, err)
		return
	}
	if !completed {
		// A newer intent coalesced onto this row while it was in flight. Run
		// again immediately against the fresher payload.
		if err := controller.backend.Reschedule(ctx, item.ID, now, ""); err != nil {
			log.Printf("queue: reschedule superseded %s item %d: %v", item.Kind, item.ID, err)
		}
	}
}

func (controller *Controller) reschedule(ctx context.Context, item Item, now time.Time, maxBackoff time.Duration, lastErr string) {
	availableAt := now.Add(controller.backoff(item.Attempts, maxBackoff))
	if err := controller.backend.Reschedule(ctx, item.ID, availableAt, lastErr); err != nil {
		log.Printf("queue: reschedule %s item %d: %v", item.Kind, item.ID, err)
	}
}

// backoff grows exponentially from BaseBackoff and is jittered so that every
// item stuck behind one outage does not become available on the same tick.
func (controller *Controller) backoff(attempts int, maxBackoff time.Duration) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	if maxBackoff <= 0 {
		maxBackoff = defaultMaxBackoff
	}
	base := controller.options.BaseBackoff
	if base <= 0 {
		base = defaultBaseBackoff
	}

	exponent := float64(attempts - 1)
	if exponent > 32 {
		exponent = 32
	}
	delay := time.Duration(float64(base) * math.Pow(2, exponent))
	if delay <= 0 || delay > maxBackoff {
		delay = maxBackoff
	}
	jitter := time.Duration(rand.Int63n(int64(delay)/4 + 1))
	if delay+jitter > maxBackoff {
		return maxBackoff
	}
	return delay + jitter
}

// Run drains the queue until ctx is cancelled, pruning completed rows
// periodically.
func (controller *Controller) Run(ctx context.Context) {
	ticker := time.NewTicker(controller.options.PollInterval)
	defer ticker.Stop()
	pruneTicker := time.NewTicker(controller.options.PruneInterval)
	defer pruneTicker.Stop()

	for {
		processed, err := controller.DispatchOnce(ctx, time.Now())
		if err != nil && ctx.Err() == nil {
			log.Printf("queue: dispatch failed: %v", err)
		}
		if processed > 0 && err == nil {
			continue
		}

		select {
		case <-ctx.Done():
			return
		case <-pruneTicker.C:
			if _, err := controller.backend.Prune(ctx, time.Now().Add(-controller.options.PruneAfter)); err != nil && ctx.Err() == nil {
				log.Printf("queue: prune failed: %v", err)
			}
		case <-ticker.C:
		}
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/queue/ -race -v`
Expected: PASS, no data races.

- [ ] **Step 5: Commit**

```bash
git add internal/queue/controller.go internal/queue/controller_test.go
git commit -m "feat(queue): add controller with backoff, worker pool and revision fence"
```

---

### Task 5: Migration

**Files:**
- Create: `internal/postgres/migrations/0012_work_queue.sql`
- Test: `internal/postgres/store_test.go` (append)

**Interfaces:**
- Consumes: nothing.
- Produces: table `work_queue` with columns `id bigserial`, `kind text`, `resource_key text`, `payload jsonb`, `revision bigint`, `actor_subject text`, `tenant_id text`, `available_at timestamptz`, `claimed_until timestamptz`, `attempts integer`, `last_error text`, `created_at timestamptz`, `processed_at timestamptz`; unique partial index `work_queue_pending_key_idx`; indexes `work_queue_ready_idx`, `work_queue_actor_idx`.

The backfill mirrors the precedent set by `0007_workflow_outbox.sql`, which seeds itself with an `insert ... select`. `authorization_outbox` stores `stack` as `stack:<id>` and `subject` as `user:<sub>`; the payload stores the raw ids and the resource key stores the prefixed forms, matching what `Key()` derives in Task 9. `authorization_outbox` has no tenant column, so backfilled rows carry an empty `tenant_id` — they are transient and disappear once delivered.

- [ ] **Step 1: Write the failing test**

Append to `internal/postgres/store_test.go`:

```go
func TestWorkQueueMigrationDefinesCoalescingQueue(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)

	columns := map[string]string{}
	rows, err := pool.Query(ctx, `
		select column_name, data_type
		from information_schema.columns
		where table_name = 'work_queue' and table_schema = current_schema()
	`)
	if err != nil {
		t.Fatalf("query work_queue columns: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name, dataType string
		if err := rows.Scan(&name, &dataType); err != nil {
			t.Fatalf("scan column: %v", err)
		}
		columns[name] = dataType
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate columns: %v", err)
	}

	for _, name := range []string{
		"id", "kind", "resource_key", "payload", "revision", "actor_subject",
		"tenant_id", "available_at", "claimed_until", "attempts", "last_error",
		"created_at", "processed_at",
	} {
		if _, ok := columns[name]; !ok {
			t.Fatalf("work_queue is missing column %q", name)
		}
	}
	if columns["payload"] != "jsonb" {
		t.Fatalf("payload data_type = %q, want jsonb", columns["payload"])
	}
	if columns["revision"] != "bigint" {
		t.Fatalf("revision data_type = %q, want bigint", columns["revision"])
	}

	var indexDef string
	if err := pool.QueryRow(ctx, `
		select indexdef from pg_indexes
		where schemaname = current_schema() and indexname = 'work_queue_pending_key_idx'
	`).Scan(&indexDef); err != nil {
		t.Fatalf("query work_queue_pending_key_idx: %v", err)
	}
	if !strings.Contains(indexDef, "UNIQUE") {
		t.Fatalf("work_queue_pending_key_idx must be unique, got %q", indexDef)
	}
	if !strings.Contains(indexDef, "processed_at IS NULL") {
		t.Fatalf("work_queue_pending_key_idx must be partial on processed_at is null, got %q", indexDef)
	}
}

func TestWorkQueuePendingKeyIndexBlocksDuplicatePendingRows(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)

	insert := `insert into work_queue (kind, resource_key, payload, actor_subject, tenant_id) values ($1, $2, '{}'::jsonb, '', '')`
	if _, err := pool.Exec(ctx, insert, "k", "stack:a/user:x"); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if _, err := pool.Exec(ctx, insert, "k", "stack:a/user:x"); err == nil {
		t.Fatal("second pending insert for the same key was accepted")
	}

	if _, err := pool.Exec(ctx, `update work_queue set processed_at = now() where resource_key = $1`, "stack:a/user:x"); err != nil {
		t.Fatalf("complete first row: %v", err)
	}
	if _, err := pool.Exec(ctx, insert, "k", "stack:a/user:x"); err != nil {
		t.Fatalf("insert after completion must succeed, got: %v", err)
	}
}

func TestWorkQueueMigrationBackfillsPendingAuthorizationOutbox(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openTestPool(t, ctx)

	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := pool.Exec(ctx, `delete from work_queue`); err != nil {
		t.Fatalf("clear work_queue: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into authorization_outbox (id, operation, subject, stack, role)
		values ('grant/user:x/stack:a/owner', 'grant', 'user:x', 'stack:a', 'owner'),
		       ('revoke/user:y/stack:a/viewer', 'revoke', 'user:y', 'stack:a', 'viewer'),
		       ('done/user:z/stack:a/viewer', 'grant', 'user:z', 'stack:a', 'viewer')
	`); err != nil {
		t.Fatalf("seed authorization_outbox: %v", err)
	}
	if _, err := pool.Exec(ctx, `update authorization_outbox set processed_at = now() where id = 'done/user:z/stack:a/viewer'`); err != nil {
		t.Fatalf("mark processed: %v", err)
	}

	if _, err := pool.Exec(ctx, `delete from schema_migrations where version = '0012_work_queue'`); err != nil {
		t.Fatalf("reset migration version: %v", err)
	}
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("re-run migration: %v", err)
	}

	type backfilled struct {
		key  string
		role string
	}
	rows, err := pool.Query(ctx, `select resource_key, payload->>'role' from work_queue order by resource_key`)
	if err != nil {
		t.Fatalf("query backfilled rows: %v", err)
	}
	defer rows.Close()
	var got []backfilled
	for rows.Next() {
		var row backfilled
		if err := rows.Scan(&row.key, &row.role); err != nil {
			t.Fatalf("scan backfilled row: %v", err)
		}
		got = append(got, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate backfilled rows: %v", err)
	}

	want := []backfilled{
		{key: "stack:a/user:x", role: "owner"},
		{key: "stack:a/user:y", role: ""},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("backfilled rows = %+v, want %+v", got, want)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `tflive_POSTGRES_TEST_DSN=$tflive_POSTGRES_TEST_DSN go test ./internal/postgres/ -run TestWorkQueue -v`
Expected: FAIL — `relation "work_queue" does not exist`.

- [ ] **Step 3: Write the implementation**

Create `internal/postgres/migrations/0012_work_queue.sql`:

```sql
create table work_queue (
	id            bigserial primary key,
	kind          text not null,
	resource_key  text not null,
	payload       jsonb not null,
	revision      bigint not null default 1,
	actor_subject text not null default '',
	tenant_id     text not null default '',
	available_at  timestamptz not null default now(),
	claimed_until timestamptz,
	attempts      integer not null default 0 check (attempts >= 0),
	last_error    text not null default '',
	created_at    timestamptz not null default now(),
	processed_at  timestamptz
);

-- Load-bearing: at most one pending row per (kind, resource_key). This gives
-- coalescing for reconcile kinds and per-key mutual exclusion for every kind,
-- because a second worker cannot claim a row that structurally cannot exist.
create unique index work_queue_pending_key_idx
	on work_queue (kind, resource_key)
	where processed_at is null;

create index work_queue_ready_idx
	on work_queue (available_at, id)
	where processed_at is null;

create index work_queue_actor_idx
	on work_queue (tenant_id, actor_subject, created_at desc);

-- Backfill undelivered authorization intents. authorization_outbox stores
-- prefixed identifiers; the payload carries raw ids and the resource key
-- carries the prefixed forms, matching the handler's key derivation.
insert into work_queue (kind, resource_key, payload, actor_subject, tenant_id)
select
	'reconcile_stack_grant',
	stack || '/' || subject,
	jsonb_build_object(
		'stack_id', replace(stack, 'stack:', ''),
		'subject', replace(subject, 'user:', ''),
		'role', case when operation = 'grant' then role else '' end
	),
	'',
	''
from authorization_outbox
where processed_at is null and failed_at is null
on conflict (kind, resource_key) where processed_at is null do nothing;
```

- [ ] **Step 4: Run test to verify it passes**

Run: `tflive_POSTGRES_TEST_DSN=$tflive_POSTGRES_TEST_DSN go test ./internal/postgres/ -run TestWorkQueue -v`
Expected: PASS, 3 tests.

- [ ] **Step 5: Commit**

```bash
git add internal/postgres/migrations/0012_work_queue.sql internal/postgres/store_test.go
git commit -m "feat(postgres): add work_queue table with coalescing index"
```

---

### Task 6: Postgres enqueue

**Files:**
- Create: `internal/postgres/workqueue.go`
- Modify: `internal/postgres/repositories.go` — `NewStore` signature
- Test: `internal/postgres/workqueue_test.go`

**Interfaces:**
- Consumes: `queue.Registry`, `queue.Request`, `queue.Resolved`, `queue.ModeReconcile`, `queue.ModeJob`.
- Produces: `NewStore(pool *pgxpool.Pool, registry *queue.Registry) *Store` (registry may be nil for callers that never enqueue), `(*Store).Enqueue(ctx context.Context, requests ...queue.Request) error`, and unexported `enqueueRequests(ctx context.Context, exec pgxExecutor, registry *queue.Registry, requests ...queue.Request) error` reused by the tx-bound enqueuer in Task 8.

`NewStore` currently takes only a pool. Every existing call site must be updated to pass a registry (or `nil`). Find them with `grep -rn "NewStore(" --include='*.go' .`.

- [ ] **Step 1: Write the failing test**

```go
package postgres

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/vishu42/tflive/internal/queue"
)

type keyedHandler struct {
	kind queue.Kind
	mode queue.Mode
}

func (h keyedHandler) Kind() queue.Kind { return h.kind }
func (h keyedHandler) Mode() queue.Mode { return h.mode }
func (h keyedHandler) Key(payload json.RawMessage) (string, error) {
	var parsed struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(payload, &parsed); err != nil {
		return "", err
	}
	return parsed.Key, nil
}
func (h keyedHandler) Deliver(context.Context, queue.Item) error { return nil }

func testRegistry(t *testing.T) *queue.Registry {
	t.Helper()
	registry, err := queue.NewRegistry(
		keyedHandler{kind: "reconcile", mode: queue.ModeReconcile},
		keyedHandler{kind: "job", mode: queue.ModeJob},
	)
	if err != nil {
		t.Fatalf("NewRegistry returned error: %v", err)
	}
	return registry
}

func TestEnqueueReconcileCoalescesAndBumpsRevision(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool, testRegistry(t))

	for _, role := range []string{"viewer", "operator", "owner"} {
		payload := json.RawMessage(`{"key":"stack:a/user:x","role":"` + role + `"}`)
		if err := store.Enqueue(ctx, queue.Request{Kind: "reconcile", Payload: payload, TenantID: "t1", ActorSubject: "user:x"}); err != nil {
			t.Fatalf("Enqueue %s returned error: %v", role, err)
		}
	}

	var count int
	var revision int64
	var role string
	if err := pool.QueryRow(ctx, `
		select count(*) over (), revision, payload->>'role'
		from work_queue where processed_at is null
	`).Scan(&count, &revision, &role); err != nil {
		t.Fatalf("query coalesced row: %v", err)
	}
	if count != 1 {
		t.Fatalf("pending rows = %d, want 1", count)
	}
	if revision != 3 {
		t.Fatalf("revision = %d, want 3", revision)
	}
	if role != "owner" {
		t.Fatalf("payload role = %q, want owner", role)
	}
}

func TestEnqueueJobIgnoresDuplicateKey(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool, testRegistry(t))

	payload := json.RawMessage(`{"key":"run:7f3a"}`)
	for i := 0; i < 3; i++ {
		if err := store.Enqueue(ctx, queue.Request{Kind: "job", Payload: payload, TenantID: "t1"}); err != nil {
			t.Fatalf("Enqueue attempt %d returned error: %v", i, err)
		}
	}

	var count int
	var revision int64
	if err := pool.QueryRow(ctx, `select count(*) over (), revision from work_queue where processed_at is null`).Scan(&count, &revision); err != nil {
		t.Fatalf("query job row: %v", err)
	}
	if count != 1 {
		t.Fatalf("pending rows = %d, want 1", count)
	}
	if revision != 1 {
		t.Fatalf("revision = %d, want 1 — ModeJob must not bump revision", revision)
	}
}

func TestEnqueueRejectsUnknownKind(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool, testRegistry(t))

	err := store.Enqueue(ctx, queue.Request{Kind: "nope", Payload: json.RawMessage(`{"key":"k"}`)})
	if err == nil {
		t.Fatal("Enqueue accepted an unknown kind")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `tflive_POSTGRES_TEST_DSN=$tflive_POSTGRES_TEST_DSN go test ./internal/postgres/ -run TestEnqueue -v`
Expected: FAIL — compile error, `NewStore` takes 1 argument.

- [ ] **Step 3: Write the implementation**

Create `internal/postgres/workqueue.go`:

```go
package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/vishu42/tflive/internal/queue"
)

// pgxExecutor is satisfied by both *pgxpool.Pool and pgx.Tx, so enqueue works
// standalone or inside a caller's transaction.
type pgxExecutor interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

const enqueueReconcileSQL = `
	insert into work_queue (kind, resource_key, payload, actor_subject, tenant_id)
	values ($1, $2, $3, $4, $5)
	on conflict (kind, resource_key) where processed_at is null
	do update set payload  = excluded.payload,
	              revision = work_queue.revision + 1
`

const enqueueJobSQL = `
	insert into work_queue (kind, resource_key, payload, actor_subject, tenant_id)
	values ($1, $2, $3, $4, $5)
	on conflict (kind, resource_key) where processed_at is null
	do nothing
`

// Enqueue writes work intents. Use the transaction-bound enqueuer from InTx
// when the intent must commit atomically with a domain write.
func (store *Store) Enqueue(ctx context.Context, requests ...queue.Request) error {
	return enqueueRequests(ctx, store.pool, store.registry, requests...)
}

func enqueueRequests(ctx context.Context, exec pgxExecutor, registry *queue.Registry, requests ...queue.Request) error {
	if len(requests) == 0 {
		return nil
	}
	if registry == nil {
		return fmt.Errorf("enqueue work: no queue registry configured")
	}
	for _, request := range requests {
		resolved, err := registry.Resolve(request)
		if err != nil {
			return fmt.Errorf("resolve work request: %w", err)
		}
		statement := enqueueReconcileSQL
		if resolved.Mode == queue.ModeJob {
			statement = enqueueJobSQL
		}
		if _, err := exec.Exec(ctx, statement,
			string(resolved.Kind),
			resolved.Key,
			[]byte(resolved.Payload),
			resolved.ActorSubject,
			resolved.TenantID,
		); err != nil {
			return fmt.Errorf("enqueue %s work: %w", resolved.Kind, err)
		}
	}
	return nil
}
```

In `internal/postgres/repositories.go`, add the registry field and parameter:

```go
type Store struct {
	pool     *pgxpool.Pool
	registry *queue.Registry
}

// NewStore returns a Store. registry may be nil for callers that never enqueue.
func NewStore(pool *pgxpool.Pool, registry *queue.Registry) *Store {
	return &Store{pool: pool, registry: registry}
}
```

Update every existing `NewStore(` call site to pass a registry or `nil`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go build ./... && tflive_POSTGRES_TEST_DSN=$tflive_POSTGRES_TEST_DSN go test ./internal/postgres/ -run TestEnqueue -v`
Expected: PASS, 3 tests, everything still compiles.

- [ ] **Step 5: Commit**

```bash
git add internal/postgres/
git commit -m "feat(postgres): add mode-aware work queue enqueue"
```

---

### Task 7: Postgres backend and reader

**Files:**
- Modify: `internal/postgres/workqueue.go`
- Test: `internal/postgres/workqueue_test.go` (append)

**Interfaces:**
- Consumes: `queue.Item`, `queue.Kind`, `queue.Status`, `queue.State`.
- Produces: `(*Store).Claim`, `(*Store).Complete`, `(*Store).Reschedule`, `(*Store).Prune`, `(*Store).ListByActor` — satisfying `queue.Backend` and `queue.Reader`.

- [ ] **Step 1: Write the failing test**

Append to `internal/postgres/workqueue_test.go`:

```go
func TestClaimLeasesAndSkipsClaimedRows(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool, testRegistry(t))
	now := time.Now().UTC()

	for _, key := range []string{"k1", "k2"} {
		payload := json.RawMessage(`{"key":"` + key + `"}`)
		if err := store.Enqueue(ctx, queue.Request{Kind: "reconcile", Payload: payload, TenantID: "t1"}); err != nil {
			t.Fatalf("Enqueue %s returned error: %v", key, err)
		}
	}

	first, err := store.Claim(ctx, now, now.Add(30*time.Second), 1, nil)
	if err != nil {
		t.Fatalf("first Claim returned error: %v", err)
	}
	if len(first) != 1 || first[0].Attempts != 1 {
		t.Fatalf("first Claim = %+v, want 1 item with Attempts 1", first)
	}

	second, err := store.Claim(ctx, now, now.Add(30*time.Second), 10, nil)
	if err != nil {
		t.Fatalf("second Claim returned error: %v", err)
	}
	if len(second) != 1 || second[0].Key == first[0].Key {
		t.Fatalf("second Claim = %+v, want the other key", second)
	}

	third, err := store.Claim(ctx, now, now.Add(30*time.Second), 10, nil)
	if err != nil {
		t.Fatalf("third Claim returned error: %v", err)
	}
	if len(third) != 0 {
		t.Fatalf("third Claim = %+v, want none — both rows are leased", third)
	}
}

func TestClaimFiltersByKind(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool, testRegistry(t))
	now := time.Now().UTC()

	if err := store.Enqueue(ctx,
		queue.Request{Kind: "reconcile", Payload: json.RawMessage(`{"key":"k1"}`), TenantID: "t1"},
		queue.Request{Kind: "job", Payload: json.RawMessage(`{"key":"k2"}`), TenantID: "t1"},
	); err != nil {
		t.Fatalf("Enqueue returned error: %v", err)
	}

	claimed, err := store.Claim(ctx, now, now.Add(30*time.Second), 10, []queue.Kind{"job"})
	if err != nil {
		t.Fatalf("Claim returned error: %v", err)
	}
	if len(claimed) != 1 || claimed[0].Kind != "job" {
		t.Fatalf("Claim = %+v, want only kind job", claimed)
	}
}

func TestCompleteIsFencedOnRevision(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool, testRegistry(t))
	now := time.Now().UTC()

	payload := json.RawMessage(`{"key":"stack:a/user:x","role":"viewer"}`)
	if err := store.Enqueue(ctx, queue.Request{Kind: "reconcile", Payload: payload, TenantID: "t1"}); err != nil {
		t.Fatalf("Enqueue returned error: %v", err)
	}
	claimed, err := store.Claim(ctx, now, now.Add(30*time.Second), 10, nil)
	if err != nil {
		t.Fatalf("Claim returned error: %v", err)
	}

	newer := json.RawMessage(`{"key":"stack:a/user:x","role":"owner"}`)
	if err := store.Enqueue(ctx, queue.Request{Kind: "reconcile", Payload: newer, TenantID: "t1"}); err != nil {
		t.Fatalf("second Enqueue returned error: %v", err)
	}

	completed, err := store.Complete(ctx, claimed[0].ID, claimed[0].Revision)
	if err != nil {
		t.Fatalf("Complete returned error: %v", err)
	}
	if completed {
		t.Fatal("Complete accepted a stale revision — the newer intent would be lost")
	}

	var processed *time.Time
	if err := pool.QueryRow(ctx, `select processed_at from work_queue where id = $1`, claimed[0].ID).Scan(&processed); err != nil {
		t.Fatalf("query processed_at: %v", err)
	}
	if processed != nil {
		t.Fatal("row was completed despite the revision fence")
	}
}

func TestRescheduleClearsLeaseAndRecordsError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool, testRegistry(t))
	now := time.Now().UTC()

	if err := store.Enqueue(ctx, queue.Request{Kind: "reconcile", Payload: json.RawMessage(`{"key":"k1"}`), TenantID: "t1"}); err != nil {
		t.Fatalf("Enqueue returned error: %v", err)
	}
	claimed, err := store.Claim(ctx, now, now.Add(30*time.Second), 10, nil)
	if err != nil {
		t.Fatalf("Claim returned error: %v", err)
	}

	if err := store.Reschedule(ctx, claimed[0].ID, now.Add(-time.Second), "openfga unavailable"); err != nil {
		t.Fatalf("Reschedule returned error: %v", err)
	}

	again, err := store.Claim(ctx, now, now.Add(30*time.Second), 10, nil)
	if err != nil {
		t.Fatalf("second Claim returned error: %v", err)
	}
	if len(again) != 1 || again[0].Attempts != 2 {
		t.Fatalf("second Claim = %+v, want the rescheduled row with Attempts 2", again)
	}

	var lastError string
	if err := pool.QueryRow(ctx, `select last_error from work_queue where id = $1`, claimed[0].ID).Scan(&lastError); err != nil {
		t.Fatalf("query last_error: %v", err)
	}
	if lastError != "openfga unavailable" {
		t.Fatalf("last_error = %q, want openfga unavailable", lastError)
	}
}

func TestPruneRemovesOnlyOldCompletedRows(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool, testRegistry(t))

	if err := store.Enqueue(ctx,
		queue.Request{Kind: "reconcile", Payload: json.RawMessage(`{"key":"pending"}`), TenantID: "t1"},
		queue.Request{Kind: "reconcile", Payload: json.RawMessage(`{"key":"old"}`), TenantID: "t1"},
	); err != nil {
		t.Fatalf("Enqueue returned error: %v", err)
	}
	if _, err := pool.Exec(ctx, `update work_queue set processed_at = now() - interval '48 hours' where resource_key = 'old'`); err != nil {
		t.Fatalf("age the completed row: %v", err)
	}

	pruned, err := store.Prune(ctx, time.Now().Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("Prune returned error: %v", err)
	}
	if pruned != 1 {
		t.Fatalf("pruned = %d, want 1", pruned)
	}

	var remaining int
	if err := pool.QueryRow(ctx, `select count(*) from work_queue`).Scan(&remaining); err != nil {
		t.Fatalf("count remaining: %v", err)
	}
	if remaining != 1 {
		t.Fatalf("remaining = %d, want 1 — the pending row must survive", remaining)
	}
}

func TestListByActorReturnsOnlyCallerItems(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool, testRegistry(t))

	if err := store.Enqueue(ctx,
		queue.Request{Kind: "reconcile", Payload: json.RawMessage(`{"key":"mine"}`), TenantID: "t1", ActorSubject: "user:me"},
		queue.Request{Kind: "reconcile", Payload: json.RawMessage(`{"key":"theirs"}`), TenantID: "t1", ActorSubject: "user:other"},
		queue.Request{Kind: "reconcile", Payload: json.RawMessage(`{"key":"othertenant"}`), TenantID: "t2", ActorSubject: "user:me"},
	); err != nil {
		t.Fatalf("Enqueue returned error: %v", err)
	}

	statuses, err := store.ListByActor(ctx, "t1", "user:me", 50)
	if err != nil {
		t.Fatalf("ListByActor returned error: %v", err)
	}
	if len(statuses) != 1 {
		t.Fatalf("ListByActor returned %d rows, want 1", len(statuses))
	}
	if statuses[0].State != queue.StatePending {
		t.Fatalf("State = %q, want pending", statuses[0].State)
	}
	if statuses[0].DeliveredAt != nil {
		t.Fatal("DeliveredAt must be nil while pending")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `tflive_POSTGRES_TEST_DSN=$tflive_POSTGRES_TEST_DSN go test ./internal/postgres/ -run 'TestClaim|TestComplete|TestReschedule|TestPrune|TestListByActor' -v`
Expected: FAIL — `store.Claim` undefined.

- [ ] **Step 3: Write the implementation**

Append to `internal/postgres/workqueue.go`:

```go
const claimWorkSQL = `
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
	 returning q.id, q.kind, q.resource_key, q.payload, q.revision,
	           q.actor_subject, q.tenant_id, q.attempts
	) select id, kind, resource_key, payload, revision, actor_subject, tenant_id, attempts
	    from claimed
`

// Claim leases up to limit ready rows. Rows already locked by another worker
// are skipped rather than waited on, so claims scale across workers. A nil
// kinds slice claims every kind.
func (store *Store) Claim(ctx context.Context, now, leaseUntil time.Time, limit int, kinds []queue.Kind) ([]queue.Item, error) {
	var kindFilter []string
	if len(kinds) > 0 {
		kindFilter = make([]string, len(kinds))
		for index, kind := range kinds {
			kindFilter[index] = string(kind)
		}
	}

	rows, err := store.pool.Query(ctx, claimWorkSQL, now, leaseUntil, limit, kindFilter)
	if err != nil {
		return nil, fmt.Errorf("claim work: %w", err)
	}
	defer rows.Close()

	var items []queue.Item
	for rows.Next() {
		var item queue.Item
		var kind string
		var payload []byte
		if err := rows.Scan(&item.ID, &kind, &item.Key, &payload, &item.Revision, &item.ActorSubject, &item.TenantID, &item.Attempts); err != nil {
			return nil, fmt.Errorf("scan claimed work: %w", err)
		}
		item.Kind = queue.Kind(kind)
		item.Payload = append(json.RawMessage(nil), payload...)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate claimed work: %w", err)
	}
	return items, nil
}

// Complete marks an item delivered. It reports false when the revision moved
// while the item was in flight, meaning a newer intent coalesced onto the row
// and must run instead.
func (store *Store) Complete(ctx context.Context, id, revision int64) (bool, error) {
	tag, err := store.pool.Exec(ctx, `
		update work_queue
		   set processed_at = now(), claimed_until = null, last_error = ''
		 where id = $1 and revision = $2 and processed_at is null
	`, id, revision)
	if err != nil {
		return false, fmt.Errorf("complete work: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// Reschedule returns an item to the pool, clearing its lease.
func (store *Store) Reschedule(ctx context.Context, id int64, availableAt time.Time, lastErr string) error {
	if _, err := store.pool.Exec(ctx, `
		update work_queue
		   set claimed_until = null, available_at = $2, last_error = $3
		 where id = $1 and processed_at is null
	`, id, availableAt, lastErr); err != nil {
		return fmt.Errorf("reschedule work: %w", err)
	}
	return nil
}

// Prune deletes rows completed before the retention cutoff.
func (store *Store) Prune(ctx context.Context, before time.Time) (int64, error) {
	tag, err := store.pool.Exec(ctx, `delete from work_queue where processed_at is not null and processed_at < $1`, before)
	if err != nil {
		return 0, fmt.Errorf("prune work: %w", err)
	}
	return tag.RowsAffected(), nil
}

// ListByActor returns the items a caller queued. There is no permission
// concept beyond this: a caller only ever sees their own items.
func (store *Store) ListByActor(ctx context.Context, tenantID, actorSubject string, limit int) ([]queue.Status, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := store.pool.Query(ctx, `
		select kind, attempts, last_error, created_at, processed_at
		  from work_queue
		 where tenant_id = $1 and actor_subject = $2
		 order by created_at desc
		 limit $3
	`, tenantID, actorSubject, limit)
	if err != nil {
		return nil, fmt.Errorf("list work by actor: %w", err)
	}
	defer rows.Close()

	statuses := []queue.Status{}
	for rows.Next() {
		var status queue.Status
		var kind string
		var processedAt *time.Time
		if err := rows.Scan(&kind, &status.Attempts, &status.LastError, &status.CreatedAt, &processedAt); err != nil {
			return nil, fmt.Errorf("scan work status: %w", err)
		}
		status.Kind = queue.Kind(kind)
		status.State = queue.StatePending
		if processedAt != nil {
			status.State = queue.StateDelivered
			status.DeliveredAt = processedAt
		}
		statuses = append(statuses, status)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate work statuses: %w", err)
	}
	return statuses, nil
}
```

Add `"encoding/json"` and `"time"` to the file's imports.

- [ ] **Step 4: Run test to verify it passes**

Run: `tflive_POSTGRES_TEST_DSN=$tflive_POSTGRES_TEST_DSN go test ./internal/postgres/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/postgres/workqueue.go internal/postgres/workqueue_test.go
git commit -m "feat(postgres): add work queue backend and actor reader"
```

---

### Task 8: Unit of work

**Files:**
- Create: `internal/postgres/unitofwork.go`
- Test: `internal/postgres/unitofwork_test.go`

**Interfaces:**
- Consumes: `enqueueRequests` (Task 6), `insertStack` and `appendAuditEvent` helpers.
- Produces: `app.TxRepo` interface (declared in `internal/app/service.go` in Task 10) satisfied by `*txRepo`; `(*Store).InTx(ctx context.Context, fn func(app.TxRepo, queue.Enqueuer) error) error`.

The existing `AppendAuditEvent` opens its own transaction. Extract its body into `appendAuditEvent(ctx context.Context, exec pgxExecutor, event traits.SecurityAuditEvent) error` and have both the public method and the tx-scoped repo call it, so the SQL exists once.

- [ ] **Step 1: Write the failing test**

```go
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/vishu42/tflive/internal/app"
	"github.com/vishu42/tflive/internal/queue"
	"github.com/vishu42/tflive/internal/traits"
)

func TestInTxCommitsDomainWriteAndIntentTogether(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool, testRegistry(t))

	stack := traits.Stack{
		ID:        traits.StackID("stack_intx_ok"),
		TenantID:  traits.TenantID("t1"),
		Name:      "intx",
		Slug:      "intx",
		CreatedBy: traits.UserID("user:me"),
		CreatedAt: time.Now().UTC(),
	}

	err := store.InTx(ctx, func(repo app.TxRepo, enqueuer queue.Enqueuer) error {
		if err := repo.CreateStack(ctx, stack); err != nil {
			return err
		}
		return enqueuer.Enqueue(ctx, queue.Request{
			Kind:         "reconcile",
			Payload:      json.RawMessage(`{"key":"stack:stack_intx_ok/user:me"}`),
			ActorSubject: "user:me",
			TenantID:     "t1",
		})
	})
	if err != nil {
		t.Fatalf("InTx returned error: %v", err)
	}

	var stacks, intents int
	if err := pool.QueryRow(ctx, `select
		(select count(*) from stacks where id = $1),
		(select count(*) from work_queue where resource_key = $2)
	`, string(stack.ID), "stack:stack_intx_ok/user:me").Scan(&stacks, &intents); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if stacks != 1 || intents != 1 {
		t.Fatalf("stacks = %d, intents = %d, want 1 and 1", stacks, intents)
	}
}

func TestInTxRollsBackBothOnError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool, testRegistry(t))

	stack := traits.Stack{
		ID:        traits.StackID("stack_intx_rollback"),
		TenantID:  traits.TenantID("t1"),
		Name:      "rollback",
		Slug:      "rollback",
		CreatedBy: traits.UserID("user:me"),
		CreatedAt: time.Now().UTC(),
	}
	sentinel := errors.New("caller failed after enqueue")

	err := store.InTx(ctx, func(repo app.TxRepo, enqueuer queue.Enqueuer) error {
		if err := repo.CreateStack(ctx, stack); err != nil {
			return err
		}
		if err := enqueuer.Enqueue(ctx, queue.Request{
			Kind:    "reconcile",
			Payload: json.RawMessage(`{"key":"stack:stack_intx_rollback/user:me"}`),
			TenantID: "t1",
		}); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("InTx error = %v, want the caller's sentinel", err)
	}

	var stacks, intents int
	if err := pool.QueryRow(ctx, `select
		(select count(*) from stacks where id = $1),
		(select count(*) from work_queue where resource_key = $2)
	`, string(stack.ID), "stack:stack_intx_rollback/user:me").Scan(&stacks, &intents); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if stacks != 0 || intents != 0 {
		t.Fatalf("stacks = %d, intents = %d, want 0 and 0 — rollback must leave no orphan intent", stacks, intents)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `tflive_POSTGRES_TEST_DSN=$tflive_POSTGRES_TEST_DSN go test ./internal/postgres/ -run TestInTx -v`
Expected: FAIL — `store.InTx` undefined.

- [ ] **Step 3: Write the implementation**

```go
package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/vishu42/tflive/internal/app"
	"github.com/vishu42/tflive/internal/queue"
	"github.com/vishu42/tflive/internal/traits"
)

// txRepo is the transaction-scoped subset of Store handed to an InTx callback.
type txRepo struct {
	tx pgx.Tx
}

func (repo *txRepo) CreateStack(ctx context.Context, stack traits.Stack) error {
	return insertStack(ctx, repo.tx, stack)
}

func (repo *txRepo) AppendAuditEvent(ctx context.Context, event traits.SecurityAuditEvent) error {
	return appendAuditEvent(ctx, repo.tx, event)
}

// txEnqueuer enqueues inside the caller's transaction. This is the entire
// reason the queue is an outbox rather than a message broker: the intent and
// the domain write commit or roll back together.
type txEnqueuer struct {
	tx       pgx.Tx
	registry *queue.Registry
}

func (enqueuer *txEnqueuer) Enqueue(ctx context.Context, requests ...queue.Request) error {
	return enqueueRequests(ctx, enqueuer.tx, enqueuer.registry, requests...)
}

// InTx runs fn inside one transaction, giving it a transaction-scoped
// repository and a transaction-bound enqueuer. Returning an error rolls back
// both the domain write and the queued intent.
func (store *Store) InTx(ctx context.Context, fn func(app.TxRepo, queue.Enqueuer) error) error {
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin unit of work: %w", err)
	}
	defer tx.Rollback(ctx)

	if err := fn(&txRepo{tx: tx}, &txEnqueuer{tx: tx, registry: store.registry}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit unit of work: %w", err)
	}
	return nil
}
```

Extract the existing `AppendAuditEvent` body into `appendAuditEvent(ctx context.Context, exec pgxExecutor, event traits.SecurityAuditEvent) error` in `internal/postgres/repositories.go`, and make the public method delegate to it with `store.pool`.

- [ ] **Step 4: Run test to verify it passes**

Run: `tflive_POSTGRES_TEST_DSN=$tflive_POSTGRES_TEST_DSN go test ./internal/postgres/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/postgres/unitofwork.go internal/postgres/unitofwork_test.go internal/postgres/repositories.go
git commit -m "feat(postgres): add unit of work for transactional enqueue"
```

---

### Task 9: Stack grant handler

**Files:**
- Create: `internal/authdispatch/handler.go`
- Delete: `internal/authdispatch/dispatcher.go`, `internal/authdispatch/dispatcher_test.go`
- Modify: `internal/openfga/authorization_adapter.go`
- Test: `internal/authdispatch/handler_test.go`, `internal/openfga/authorization_adapter_test.go` (append)

**Interfaces:**
- Consumes: `queue.Handler`, `authz.Authorizer`, `authz.Grant`, `authz.Mutation`.
- Produces: `authdispatch.KindReconcileStackGrant queue.Kind = "reconcile_stack_grant"`, `authdispatch.GrantPayload{StackID, Subject, Role string}`, `authdispatch.NewStackGrantHandler(authz.SubjectGrantLister) *StackGrantHandler`, and on the authz port `SubjectGrantLister interface { ListSubjectGrants(context.Context, ListSubjectGrantsRequest) (ListGrantsResult, error) }` implemented by `*openfga.AuthorizationAdapter`.

Convergence needs the subject's current tuples on one stack. `ListGrants` filters by object only, so add a read filtered by user as well. The handler then deletes every role the subject holds that is not the desired one, and writes the desired one if absent. Re-applying the same desired state is a no-op, which is what makes the handler idempotent.

- [ ] **Step 1: Write the failing test**

```go
package authdispatch

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/vishu42/tflive/internal/authz"
	"github.com/vishu42/tflive/internal/queue"
)

type fakeAuthorizer struct {
	existing []authz.Grant
	written  []authz.Grant
	deleted  []authz.Grant
	listErr  error
	writeErr error
}

func (f *fakeAuthorizer) ListSubjectGrants(_ context.Context, _ authz.ListSubjectGrantsRequest) (authz.ListGrantsResult, error) {
	if f.listErr != nil {
		return authz.ListGrantsResult{}, f.listErr
	}
	return authz.ListGrantsResult{Grants: f.existing}, nil
}

func (f *fakeAuthorizer) WriteRelationships(_ context.Context, mutation authz.Mutation) error {
	if f.writeErr != nil {
		return f.writeErr
	}
	f.written = append(f.written, mutation.Grants()...)
	return nil
}

func (f *fakeAuthorizer) DeleteRelationships(_ context.Context, mutation authz.Mutation) error {
	if f.writeErr != nil {
		return f.writeErr
	}
	f.deleted = append(f.deleted, mutation.Grants()...)
	return nil
}

func mustGrant(t *testing.T, stackID, subject string, role authz.Role) authz.Grant {
	t.Helper()
	stack, err := authz.StackFromID(stackID)
	if err != nil {
		t.Fatalf("StackFromID(%q): %v", stackID, err)
	}
	sub, err := authz.SubjectFromKeycloakSub(subject)
	if err != nil {
		t.Fatalf("SubjectFromKeycloakSub(%q): %v", subject, err)
	}
	grant, err := authz.NewGrant(sub, stack, role)
	if err != nil {
		t.Fatalf("NewGrant: %v", err)
	}
	return grant
}

func TestKeyUsesCanonicalIdentifiers(t *testing.T) {
	t.Parallel()

	handler := NewStackGrantHandler(&fakeAuthorizer{})
	key, err := handler.Key(json.RawMessage(`{"stack_id":"stack_abc","subject":"6fdb4b4c-2a8f-4cf7-945f-38f67f6a0e91","role":"owner"}`))
	if err != nil {
		t.Fatalf("Key returned error: %v", err)
	}
	if key != "stack:stack_abc/user:6fdb4b4c-2a8f-4cf7-945f-38f67f6a0e91" {
		t.Fatalf("Key = %q, want stack:stack_abc/user:6fdb4b4c-2a8f-4cf7-945f-38f67f6a0e91", key)
	}
}

func TestKeyRejectsMalformedPayload(t *testing.T) {
	t.Parallel()

	handler := NewStackGrantHandler(&fakeAuthorizer{})
	if _, err := handler.Key(json.RawMessage(`{"stack_id":"","subject":""}`)); err == nil {
		t.Fatal("Key accepted an empty identity")
	}
}

func TestModeIsReconcile(t *testing.T) {
	t.Parallel()

	if NewStackGrantHandler(&fakeAuthorizer{}).Mode() != queue.ModeReconcile {
		t.Fatal("stack grant handler must be ModeReconcile")
	}
}

func TestDeliverWritesDesiredRoleWhenAbsent(t *testing.T) {
	t.Parallel()

	authorizer := &fakeAuthorizer{}
	handler := NewStackGrantHandler(authorizer)

	err := handler.Deliver(context.Background(), queue.Item{
		Kind:    KindReconcileStackGrant,
		Payload: json.RawMessage(`{"stack_id":"stack_abc","subject":"6fdb4b4c-2a8f-4cf7-945f-38f67f6a0e91","role":"owner"}`),
	})
	if err != nil {
		t.Fatalf("Deliver returned error: %v", err)
	}
	if len(authorizer.written) != 1 || authorizer.written[0].Role() != authz.RoleOwner {
		t.Fatalf("written = %+v, want one owner grant", authorizer.written)
	}
	if len(authorizer.deleted) != 0 {
		t.Fatalf("deleted = %+v, want none", authorizer.deleted)
	}
}

func TestDeliverReplacesExistingRole(t *testing.T) {
	t.Parallel()

	authorizer := &fakeAuthorizer{
		existing: []authz.Grant{mustGrant(t, "stack_abc", "6fdb4b4c-2a8f-4cf7-945f-38f67f6a0e91", authz.RoleViewer)},
	}
	handler := NewStackGrantHandler(authorizer)

	err := handler.Deliver(context.Background(), queue.Item{
		Payload: json.RawMessage(`{"stack_id":"stack_abc","subject":"6fdb4b4c-2a8f-4cf7-945f-38f67f6a0e91","role":"owner"}`),
	})
	if err != nil {
		t.Fatalf("Deliver returned error: %v", err)
	}
	if len(authorizer.written) != 1 || authorizer.written[0].Role() != authz.RoleOwner {
		t.Fatalf("written = %+v, want one owner grant", authorizer.written)
	}
	if len(authorizer.deleted) != 1 || authorizer.deleted[0].Role() != authz.RoleViewer {
		t.Fatalf("deleted = %+v, want the stale viewer grant", authorizer.deleted)
	}
}

func TestDeliverIsIdempotentWhenAlreadyConverged(t *testing.T) {
	t.Parallel()

	authorizer := &fakeAuthorizer{
		existing: []authz.Grant{mustGrant(t, "stack_abc", "6fdb4b4c-2a8f-4cf7-945f-38f67f6a0e91", authz.RoleOwner)},
	}
	handler := NewStackGrantHandler(authorizer)

	for attempt := 0; attempt < 2; attempt++ {
		if err := handler.Deliver(context.Background(), queue.Item{
			Payload: json.RawMessage(`{"stack_id":"stack_abc","subject":"6fdb4b4c-2a8f-4cf7-945f-38f67f6a0e91","role":"owner"}`),
		}); err != nil {
			t.Fatalf("Deliver attempt %d returned error: %v", attempt, err)
		}
	}
	if len(authorizer.written) != 0 || len(authorizer.deleted) != 0 {
		t.Fatalf("converged state caused writes: written=%+v deleted=%+v", authorizer.written, authorizer.deleted)
	}
}

func TestDeliverEmptyRoleRevokesEverything(t *testing.T) {
	t.Parallel()

	authorizer := &fakeAuthorizer{
		existing: []authz.Grant{mustGrant(t, "stack_abc", "6fdb4b4c-2a8f-4cf7-945f-38f67f6a0e91", authz.RoleOperator)},
	}
	handler := NewStackGrantHandler(authorizer)

	err := handler.Deliver(context.Background(), queue.Item{
		Payload: json.RawMessage(`{"stack_id":"stack_abc","subject":"6fdb4b4c-2a8f-4cf7-945f-38f67f6a0e91","role":""}`),
	})
	if err != nil {
		t.Fatalf("Deliver returned error: %v", err)
	}
	if len(authorizer.written) != 0 {
		t.Fatalf("written = %+v, want none", authorizer.written)
	}
	if len(authorizer.deleted) != 1 || authorizer.deleted[0].Role() != authz.RoleOperator {
		t.Fatalf("deleted = %+v, want the operator grant", authorizer.deleted)
	}
}

func TestDeliverPropagatesListError(t *testing.T) {
	t.Parallel()

	authorizer := &fakeAuthorizer{listErr: errors.New("openfga unavailable")}
	handler := NewStackGrantHandler(authorizer)

	err := handler.Deliver(context.Background(), queue.Item{
		Payload: json.RawMessage(`{"stack_id":"stack_abc","subject":"6fdb4b4c-2a8f-4cf7-945f-38f67f6a0e91","role":"owner"}`),
	})
	if err == nil {
		t.Fatal("Deliver swallowed the list error — the item must be retried")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/authdispatch/ -v`
Expected: FAIL — `NewStackGrantHandler` undefined.

- [ ] **Step 3: Write the implementation**

In `internal/authz/authorization.go`, add the request type and port:

```go
// ListSubjectGrantsRequest asks for one subject's direct roles on one stack.
type ListSubjectGrantsRequest struct {
	Subject Subject
	Stack   Stack
}

func (request ListSubjectGrantsRequest) Valid() bool {
	return request.Subject.Valid() && request.Stack.Valid()
}

// SubjectGrantLister reads the direct roles a subject holds on a stack.
type SubjectGrantLister interface {
	ListSubjectGrants(context.Context, ListSubjectGrantsRequest) (ListGrantsResult, error)
}
```

Add `ListSubjectGrants` to the `Authorizer` interface alongside `ListGrants`.

In `internal/openfga/authorization_adapter.go`, add the filtered read. It reuses the pagination and validation shape of `ListGrants`, adding `user` to the read filter:

```go
// ListSubjectGrants returns the direct roles one subject holds on one stack.
func (adapter *AuthorizationAdapter) ListSubjectGrants(ctx context.Context, request authz.ListSubjectGrantsRequest) (authz.ListGrantsResult, error) {
	if !request.Valid() {
		return authz.ListGrantsResult{}, fmt.Errorf("%w: invalid subject grants request", authz.ErrInvalidInput)
	}

	type readTuple struct {
		Key *tupleKey `json:"key"`
	}
	var response struct {
		Tuples *[]readTuple `json:"tuples"`
	}
	input := struct {
		TupleKey struct {
			User   string `json:"user"`
			Object string `json:"object"`
		} `json:"tuple_key"`
		PageSize int `json:"page_size"`
	}{PageSize: 100}
	input.TupleKey.User = request.Subject.String()
	input.TupleKey.Object = request.Stack.String()

	if err := adapter.client.doJSON(ctx, http.MethodPost, adapter.client.endpoint("stores", adapter.storeID, "read"), nil, input, &response, http.StatusOK); err != nil {
		return authz.ListGrantsResult{}, adapter.classify(err)
	}
	if response.Tuples == nil {
		return authz.ListGrantsResult{}, fmt.Errorf("%w: read response is missing tuples", authz.ErrMalformedResponse)
	}

	result := authz.ListGrantsResult{}
	for _, tuple := range *response.Tuples {
		grant, err := grantFromReadTuple(tuple.Key, request.Stack)
		if err != nil {
			return authz.ListGrantsResult{}, fmt.Errorf("%w: read response contains invalid tuple", authz.ErrMalformedResponse)
		}
		if grant.Subject().String() != request.Subject.String() {
			return authz.ListGrantsResult{}, fmt.Errorf("%w: read response contains another subject", authz.ErrMalformedResponse)
		}
		result.Grants = append(result.Grants, grant)
	}
	return result, nil
}
```

Create `internal/authdispatch/handler.go` and delete `dispatcher.go` plus `dispatcher_test.go`:

```go
// Package authdispatch converges OpenFGA relationships to the desired state
// carried by work queue items.
package authdispatch

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/vishu42/tflive/internal/authz"
	"github.com/vishu42/tflive/internal/queue"
)

// KindReconcileStackGrant names the work kind this handler serves.
const KindReconcileStackGrant queue.Kind = "reconcile_stack_grant"

// GrantPayload is the desired state for one subject on one stack. An empty
// Role means the subject should hold no access at all.
type GrantPayload struct {
	StackID string `json:"stack_id"`
	Subject string `json:"subject"`
	Role    string `json:"role"`
}

// Relationships is the slice of the authorization port this handler needs.
type Relationships interface {
	authz.SubjectGrantLister
	WriteRelationships(context.Context, authz.Mutation) error
	DeleteRelationships(context.Context, authz.Mutation) error
}

// StackGrantHandler converges a subject's roles on a stack to the desired
// state in the payload. It is idempotent: re-applying converged state performs
// no writes at all.
type StackGrantHandler struct {
	relationships Relationships
}

func NewStackGrantHandler(relationships Relationships) *StackGrantHandler {
	return &StackGrantHandler{relationships: relationships}
}

func (handler *StackGrantHandler) Kind() queue.Kind { return KindReconcileStackGrant }

func (handler *StackGrantHandler) Mode() queue.Mode { return queue.ModeReconcile }

// Key derives "stack:<id>/user:<sub>" from the payload identity. It is built
// from the canonical formatters so the key cannot drift from what OpenFGA uses.
//
// This derivation is a frozen contract: keys are persisted, and changing the
// format splits one resource across two keys, disabling mutual exclusion.
func (handler *StackGrantHandler) Key(payload json.RawMessage) (string, error) {
	identity, err := parseGrantPayload(payload)
	if err != nil {
		return "", err
	}
	return identity.stack.String() + "/" + identity.subject.String(), nil
}

func (handler *StackGrantHandler) Deliver(ctx context.Context, item queue.Item) error {
	identity, err := parseGrantPayload(item.Payload)
	if err != nil {
		return err
	}

	current, err := handler.relationships.ListSubjectGrants(ctx, authz.ListSubjectGrantsRequest{
		Subject: identity.subject,
		Stack:   identity.stack,
	})
	if err != nil {
		return fmt.Errorf("read current stack grants: %w", err)
	}

	var stale []authz.Grant
	satisfied := false
	for _, grant := range current.Grants {
		if identity.hasRole && grant.Role() == identity.role {
			satisfied = true
			continue
		}
		stale = append(stale, grant)
	}

	if len(stale) > 0 {
		mutation, err := authz.NewMutation(stale, true)
		if err != nil {
			return fmt.Errorf("build stale grant mutation: %w", err)
		}
		if err := handler.relationships.DeleteRelationships(ctx, mutation); err != nil {
			return fmt.Errorf("remove stale stack grants: %w", err)
		}
	}

	if !identity.hasRole || satisfied {
		return nil
	}

	grant, err := authz.NewGrant(identity.subject, identity.stack, identity.role)
	if err != nil {
		return fmt.Errorf("build desired grant: %w", err)
	}
	mutation, err := authz.NewMutation([]authz.Grant{grant}, true)
	if err != nil {
		return fmt.Errorf("build desired mutation: %w", err)
	}
	if err := handler.relationships.WriteRelationships(ctx, mutation); err != nil {
		return fmt.Errorf("write desired stack grant: %w", err)
	}
	return nil
}

// grantIdentity is the parsed payload. authz.Role is a struct, not a string, so
// the role is parsed once here and compared by value; hasRole distinguishes
// "grant this role" from "revoke everything".
type grantIdentity struct {
	stack   authz.Stack
	subject authz.Subject
	role    authz.Role
	hasRole bool
}

func parseGrantPayload(payload json.RawMessage) (grantIdentity, error) {
	var parsed GrantPayload
	if err := json.Unmarshal(payload, &parsed); err != nil {
		return grantIdentity{}, fmt.Errorf("decode stack grant payload: %w", err)
	}
	stack, err := authz.StackFromID(parsed.StackID)
	if err != nil {
		return grantIdentity{}, fmt.Errorf("parse stack grant stack: %w", err)
	}
	subject, err := authz.SubjectFromKeycloakSub(parsed.Subject)
	if err != nil {
		return grantIdentity{}, fmt.Errorf("parse stack grant subject: %w", err)
	}
	identity := grantIdentity{stack: stack, subject: subject}
	if parsed.Role != "" {
		role, err := authz.RoleFromDirectRelation(parsed.Role)
		if err != nil {
			return grantIdentity{}, fmt.Errorf("parse stack grant role: %w", err)
		}
		identity.role = role
		identity.hasRole = true
	}
	return identity, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/authdispatch/ ./internal/openfga/ ./internal/authz/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/authdispatch/ internal/openfga/ internal/authz/
git commit -m "feat(authdispatch): replace dispatcher with reconciling grant handler"
```

---

### Task 10: Service call sites

**Files:**
- Modify: `internal/app/service.go` — `Service` struct, `CreateStack`, `AssignStackRole`, `RevokeStackRole`
- Test: `internal/app/service_test.go` (append), `internal/app/authorization_test.go` (adjust existing expectations)

**Interfaces:**
- Consumes: `queue.Enqueuer`, `queue.Request`, `authdispatch.KindReconcileStackGrant`, `authdispatch.GrantPayload`.
- Produces: `app.TxRepo` interface, `app.UnitOfWork` interface, `Service.Work UnitOfWork` field.

`CreateStack` loses `stackOwnerIntentRepository` and the `AuthorizationOutbox` completion. `AssignStackRole` loses the delete-then-write pair entirely — the handler converges, so there is no intermediate no-role window to crash inside.

**Before writing these tests, read the neighbouring tests in `internal/app/service_test.go` and copy their service construction and authenticated-context setup verbatim.** The tests below use placeholder names (`newTestServiceWithWork`, `ownerContext`, `creatorContext`) for whatever that file's existing setup is called; substitute the real ones and extend the service builder with a `Work` field. Do not invent a parallel test harness.

- [ ] **Step 1: Write the failing test**

Append to `internal/app/service_test.go`:

```go
type recordingUnitOfWork struct {
	requests []queue.Request
	stacks   []traits.Stack
	audits   []traits.SecurityAuditEvent
	err      error
}

func (u *recordingUnitOfWork) InTx(ctx context.Context, fn func(app.TxRepo, queue.Enqueuer) error) error {
	if u.err != nil {
		return u.err
	}
	return fn(u, u)
}

func (u *recordingUnitOfWork) CreateStack(_ context.Context, stack traits.Stack) error {
	u.stacks = append(u.stacks, stack)
	return nil
}

func (u *recordingUnitOfWork) AppendAuditEvent(_ context.Context, event traits.SecurityAuditEvent) error {
	u.audits = append(u.audits, event)
	return nil
}

func (u *recordingUnitOfWork) Enqueue(_ context.Context, requests ...queue.Request) error {
	u.requests = append(u.requests, requests...)
	return nil
}

func TestAssignStackRoleEnqueuesDesiredRoleWithoutCallingOpenFGA(t *testing.T) {
	t.Parallel()

	work := &recordingUnitOfWork{}
	service := newTestServiceWithWork(t, work) // existing helper, extended to set Service.Work

	if _, err := service.AssignStackRole(ownerContext(t), app.AssignStackRoleCommand{
		TenantID: "tenant_1",
		StackID:  "stack_abc",
		UserSub:  "6fdb4b4c-2a8f-4cf7-945f-38f67f6a0e91",
		Role:     "operator",
	}); err != nil {
		t.Fatalf("AssignStackRole returned error: %v", err)
	}

	if len(work.requests) != 1 {
		t.Fatalf("enqueued %d requests, want 1", len(work.requests))
	}
	if work.requests[0].Kind != authdispatch.KindReconcileStackGrant {
		t.Fatalf("Kind = %q, want %q", work.requests[0].Kind, authdispatch.KindReconcileStackGrant)
	}

	var payload authdispatch.GrantPayload
	if err := json.Unmarshal(work.requests[0].Payload, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload.Role != "operator" || payload.StackID != "stack_abc" {
		t.Fatalf("payload = %+v, want operator on stack_abc", payload)
	}
	if len(work.audits) != 1 {
		t.Fatalf("audit events = %d, want 1 written in the same transaction", len(work.audits))
	}
}

func TestRevokeStackRoleEnqueuesEmptyRole(t *testing.T) {
	t.Parallel()

	work := &recordingUnitOfWork{}
	service := newTestServiceWithWork(t, work)

	if err := service.RevokeStackRole(ownerContext(t), app.RevokeStackRoleCommand{
		TenantID: "tenant_1",
		StackID:  "stack_abc",
		UserSub:  "6fdb4b4c-2a8f-4cf7-945f-38f67f6a0e91",
	}); err != nil {
		t.Fatalf("RevokeStackRole returned error: %v", err)
	}

	if len(work.requests) != 1 {
		t.Fatalf("enqueued %d requests, want 1", len(work.requests))
	}
	var payload authdispatch.GrantPayload
	if err := json.Unmarshal(work.requests[0].Payload, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload.Role != "" {
		t.Fatalf("payload role = %q, want empty — an empty role means no access", payload.Role)
	}
}

func TestCreateStackEnqueuesOwnerGrantInSameTransaction(t *testing.T) {
	t.Parallel()

	work := &recordingUnitOfWork{}
	service := newTestServiceWithWork(t, work)

	stack, err := service.CreateStack(creatorContext(t), app.CreateStackCommand{
		TenantID: "tenant_1",
		Name:     "payments",
	})
	if err != nil {
		t.Fatalf("CreateStack returned error: %v", err)
	}
	if len(work.stacks) != 1 || work.stacks[0].ID != stack.ID {
		t.Fatalf("stacks = %+v, want the created stack", work.stacks)
	}
	if len(work.requests) != 1 {
		t.Fatalf("enqueued %d requests, want 1 owner grant", len(work.requests))
	}
	var payload authdispatch.GrantPayload
	if err := json.Unmarshal(work.requests[0].Payload, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload.Role != "owner" {
		t.Fatalf("payload role = %q, want owner", payload.Role)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app/ -run 'TestAssignStackRoleEnqueues|TestRevokeStackRoleEnqueues|TestCreateStackEnqueues' -v`
Expected: FAIL — `app.TxRepo` and `Service.Work` undefined.

- [ ] **Step 3: Write the implementation**

In `internal/app/service.go` add the seam types and the `Work` field:

```go
// TxRepo is the transaction-scoped repository surface handed to a unit of
// work. It grows only when a new dual-write point appears.
type TxRepo interface {
	CreateStack(ctx context.Context, stack traits.Stack) error
	AppendAuditEvent(ctx context.Context, event traits.SecurityAuditEvent) error
}

// UnitOfWork commits a domain write and a queued intent atomically.
type UnitOfWork interface {
	InTx(ctx context.Context, fn func(TxRepo, queue.Enqueuer) error) error
}
```

Add `Work UnitOfWork` to `Service`. Replace the tail of `CreateStack` (the block from the `stackOwnerIntentRepository` assertion through the `AuthorizationOutbox` completion) with:

```go
	payload, err := json.Marshal(authdispatch.GrantPayload{
		StackID: string(stack.ID),
		Subject: principal.Subject,
		Role:    string(authz.RoleOwner),
	})
	if err != nil {
		return traits.Stack{}, fmt.Errorf("encode owner grant payload: %w", err)
	}
	if err := service.Work.InTx(ctx, func(repo TxRepo, enqueuer queue.Enqueuer) error {
		if err := repo.CreateStack(ctx, stack); err != nil {
			return err
		}
		return enqueuer.Enqueue(ctx, queue.Request{
			Kind:         authdispatch.KindReconcileStackGrant,
			Payload:      payload,
			ActorSubject: principal.Subject,
			TenantID:     string(command.TenantID),
		})
	}); err != nil {
		return traits.Stack{}, fmt.Errorf("create stack: %w", err)
	}
```

Replace the mutation block in `AssignStackRole` (from `grant, err := authz.NewGrant(...)` through the `WriteRelationships` call and the trailing `auditError`) with a single enqueue that also writes the audit event transactionally:

```go
	payload, err := json.Marshal(authdispatch.GrantPayload{
		StackID: string(command.StackID),
		Subject: command.UserSub,
		Role:    command.Role,
	})
	if err != nil {
		return GrantView{}, fmt.Errorf("encode grant payload: %w", err)
	}
	if err := service.Work.InTx(ctx, func(repo TxRepo, enqueuer queue.Enqueuer) error {
		if err := repo.AppendAuditEvent(ctx, traits.SecurityAuditEvent{
			ActorSubject: principal.Subject,
			Action:       traits.AuditActionGrant,
			TargetUser:   command.UserSub,
			TenantID:     command.TenantID,
			StackID:      command.StackID,
			OldRole:      currentRole,
			NewRole:      command.Role,
			Outcome:      traits.AuditOutcomeSuccess,
		}); err != nil {
			return err
		}
		return enqueuer.Enqueue(ctx, queue.Request{
			Kind:         authdispatch.KindReconcileStackGrant,
			Payload:      payload,
			ActorSubject: principal.Subject,
			TenantID:     string(command.TenantID),
		})
	}); err != nil {
		return GrantView{}, fmt.Errorf("assign stack role: %w", err)
	}

	return GrantView{UserSub: command.UserSub, Role: command.Role}, nil
```

Apply the same shape to `RevokeStackRole`, with `Role: ""` in the payload, `Action: traits.AuditActionRevoke`, and `OldRole: targetRole`.

Leave the last-owner guard and its `listGrantsForStack` read untouched — it stays best-effort against OpenFGA, as documented in the design.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/app/ -v`
Expected: PASS. Existing tests that asserted `WriteRelationships` calls from these paths need their expectations moved to the enqueue assertions.

- [ ] **Step 5: Commit**

```bash
git add internal/app/
git commit -m "feat(app): route stack grant changes through the work queue"
```

---

### Task 11: Wiring

**Files:**
- Modify: `cmd/worker/main.go`, `cmd/api/main.go`
- Test: `cmd/worker/main_test.go` (adjust existing dispatcher expectations)

**Interfaces:**
- Consumes: everything above.
- Produces: a worker that runs `queue.Controller` instead of `authdispatch.Dispatcher`.

`internal/dispatch` and the workflow outbox dispatcher stay exactly as they are.

- [ ] **Step 1: Write the failing test**

In `cmd/worker/main_test.go`, replace the `newAuthorizationDispatcher` stub and its assertion with a controller equivalent:

```go
func TestWorkerRunsQueueController(t *testing.T) {
	t.Parallel()

	deps := newTestDeps(t)
	var ran bool
	deps.newQueueController = func(*queue.Registry) outboxDispatcher {
		ran = true
		return &recordingOutboxDispatcher{}
	}

	if err := run(context.Background(), deps); err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	if !ran {
		t.Fatal("queue controller was not constructed")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/worker/ -run TestWorkerRunsQueueController -v`
Expected: FAIL — `newQueueController` undefined.

- [ ] **Step 3: Write the implementation**

In `cmd/worker/main.go`, build the registry, pass it to the store, and swap the dispatcher for the controller:

```go
	grantHandler := authdispatch.NewStackGrantHandler(authorizer)
	registry, err := queue.NewRegistry(grantHandler)
	if err != nil {
		return fmt.Errorf("build queue registry: %w", err)
	}
	store := postgres.NewStore(pool, registry)

	controller := queue.NewController(store, registry, queue.Options{})

	dispatchGroup.Add(1)
	go func() { defer dispatchGroup.Done(); controller.Run(dispatchCtx) }()
```

Remove the `newAuthorizationDispatcher` dependency field and its `authdispatch.Outbox` embedding in the store interface.

In `cmd/api/main.go`, build the same registry (the API enqueues but never delivers), pass it to `postgres.NewStore`, and set `Work: store` on the `app.Service`. Remove the `AuthorizationOutbox` field assignment.

- [ ] **Step 4: Run test to verify it passes**

Run: `go build ./... && go test ./cmd/... -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/
git commit -m "feat(cmd): run the queue controller in the worker"
```

---

### Task 12: Queue read endpoint

**Files:**
- Modify: `internal/api/server.go`
- Test: `internal/api/server_test.go` (append)

**Interfaces:**
- Consumes: `queue.Reader`, `queue.Status`.
- Produces: `GET /v1/tenants/{tenant_id}/queue` returning `{"items":[{"kind","state","summary","attempts","last_error","created_at","delivered_at"}]}`.

Scoping is by `actor_subject` alone — a caller only ever sees items they queued, so no new permission concept is introduced. `attempts` and `last_error` must be surfaced: retry is forever, so a stuck item stays pending indefinitely and those fields are the only way to distinguish "queued two seconds ago" from "failing for an hour".

**As in Task 10, read the neighbouring tests in `internal/api/server_test.go` first and reuse their server construction and authenticated-request helpers.** `newTestServer(t, withQueueReader(...))` and `authenticatedRequest(...)` below are placeholders for whatever that file already provides.

- [ ] **Step 1: Write the failing test**

```go
func TestListQueueReturnsOnlyCallerItems(t *testing.T) {
	t.Parallel()

	reader := &stubQueueReader{statuses: []queue.Status{{
		Kind:      "reconcile_stack_grant",
		State:     queue.StatePending,
		Summary:   "grant operator on stack_abc",
		Attempts:  3,
		LastError: "openfga unavailable",
		CreatedAt: time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC),
	}}}
	server := newTestServer(t, withQueueReader(reader))

	request := authenticatedRequest(t, http.MethodGet, "/v1/tenants/tenant_1/queue", nil)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", response.Code, response.Body.String())
	}

	var body struct {
		Items []struct {
			Kind      string `json:"kind"`
			State     string `json:"state"`
			Attempts  int    `json:"attempts"`
			LastError string `json:"last_error"`
		} `json:"items"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(body.Items))
	}
	if body.Items[0].Attempts != 3 || body.Items[0].LastError != "openfga unavailable" {
		t.Fatalf("item = %+v, want attempts and last_error surfaced", body.Items[0])
	}
	if reader.tenantID != "tenant_1" || reader.actorSubject == "" {
		t.Fatalf("reader called with tenant %q actor %q, want the authenticated caller", reader.tenantID, reader.actorSubject)
	}
}

func TestListQueueRequiresAuthentication(t *testing.T) {
	t.Parallel()

	server := newTestServer(t, withQueueReader(&stubQueueReader{}))
	request := httptest.NewRequest(http.MethodGet, "/v1/tenants/tenant_1/queue", nil)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", response.Code)
	}
}
```

Add the stub alongside the existing test doubles:

```go
type stubQueueReader struct {
	statuses     []queue.Status
	tenantID     string
	actorSubject string
	err          error
}

func (r *stubQueueReader) ListByActor(_ context.Context, tenantID, actorSubject string, _ int) ([]queue.Status, error) {
	r.tenantID = tenantID
	r.actorSubject = actorSubject
	return r.statuses, r.err
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/api/ -run TestListQueue -v`
Expected: FAIL — 404, route not registered.

- [ ] **Step 3: Write the implementation**

Add a `Queue queue.Reader` field to the server's dependencies, register the route next to the other tenant routes, and add the handler:

```go
	server.handleTenantRoute("GET /v1/tenants/{tenant_id}/queue", server.handleListQueue)
```

```go
type queueItemResponse struct {
	Kind        string     `json:"kind"`
	State       string     `json:"state"`
	Summary     string     `json:"summary"`
	Attempts    int        `json:"attempts"`
	LastError   string     `json:"last_error"`
	CreatedAt   time.Time  `json:"created_at"`
	DeliveredAt *time.Time `json:"delivered_at,omitempty"`
}

// handleListQueue returns the work items the caller queued. Scoping is by
// actor alone: a caller only ever sees their own items.
//
// handleTenantRoute takes a plain http.HandlerFunc and the middleware has
// already verified the tenant, so the tenant id is read from the path exactly
// as handleListStackGrants does.
func (server *Server) handleListQueue(response http.ResponseWriter, request *http.Request) {
	principal, ok := authn.PrincipalFromContext(request.Context())
	if !ok || principal.Subject == "" {
		writeError(response, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}
	if server.queue == nil {
		writeError(response, http.StatusServiceUnavailable, "queue_unavailable", "queue is not configured")
		return
	}

	tenantID := request.PathValue("tenant_id")
	statuses, err := server.queue.ListByActor(request.Context(), tenantID, principal.Subject, 50)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "internal_error", "list queue failed")
		return
	}

	items := make([]queueItemResponse, 0, len(statuses))
	for _, status := range statuses {
		summary := status.Summary
		if summary == "" {
			summary = string(status.Kind)
		}
		items = append(items, queueItemResponse{
			Kind:        string(status.Kind),
			State:       string(status.State),
			Summary:     summary,
			Attempts:    status.Attempts,
			LastError:   status.LastError,
			CreatedAt:   status.CreatedAt,
			DeliveredAt: status.DeliveredAt,
		})
	}
	writeJSON(response, http.StatusOK, map[string]any{"items": items})
}
```

Note the exact shapes this file already uses, confirmed against `internal/api/server.go`:
`writeError(response, status, code, message)` and `writeJSON(response, status, body)`
are package-level functions, not methods. `handleTenantRoute(pattern string,
handler http.HandlerFunc)` wraps the handler in `requireConfiguredTenant`, so
handlers take only `(response, request)` and read the tenant with
`request.PathValue("tenant_id")`. Add the reader as an unexported `queue`
field on `Server`, matching the existing unexported `service` field.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/api/ -v`
Expected: PASS.

- [ ] **Step 5: Full suite and commit**

```bash
go build ./...
go vet ./...
tflive_POSTGRES_TEST_DSN=$tflive_POSTGRES_TEST_DSN go test ./... -race
git add internal/api/
git commit -m "feat(api): add queue read endpoint scoped to the caller"
```

---

## Follow-up, not in this plan

**Notification enqueue inside the completing transaction.** The design's
architecture diagram shows a handler enqueueing a `notify_user` item in the
same transaction that completes its own item, so a notification can never
describe work that failed. This plan does not build it, because there is no
notification delivery mechanism to enqueue toward — `internal/events` is an
empty stub. Enqueueing a kind with no registered handler would produce rows
that reschedule forever.

When notifications arrive, `Backend.Complete` must change shape: it currently
takes `(id, revision)` and cannot carry an enqueue into the same transaction.
The likely signature is `Complete(ctx, id, revision int64, follow ...queue.Request) (bool, error)`,
with the Postgres implementation opening one transaction around the completing
`update` and the follow-on inserts. Flagged here so it is a planned change
rather than a surprise.

**Dropping the old outbox.** Once the queue has run in production and
`authorization_outbox` is empty, a follow-up migration drops that table and
removes the remaining `authdispatch.Outbox` interface.

**Migrating `workflow_outbox`.** It becomes a `ModeJob` kind after that. Its
existing id, `'template-run/' || tenant_id || '/' || id`, is already a
unique-per-event key, so the migration is mostly renaming.
