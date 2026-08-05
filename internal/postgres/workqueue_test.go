package postgres

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/vishu42/tflive/internal/queue"
)

var _ queue.Backend = (*Store)(nil)
var _ queue.Enqueuer = (*Store)(nil)
var _ queue.Reader = (*Store)(nil)

// keyedHandler derives its ordering key straight from the payload so queue
// tests can exercise both modes without depending on a real domain handler.
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

func newQueueStore(t *testing.T, ctx context.Context) (*Store, *pgxpool.Pool) {
	t.Helper()
	pool := openMigratedTestPool(t, ctx)
	return NewStore(pool, WithQueueRegistry(testRegistry(t))), pool
}

// dbNow reads the database clock. Claim eligibility compares the caller's now
// against available_at, which the database stamps with its own now() at insert
// time, so a Go timestamp captured before enqueueing is always too early.
func dbNow(t *testing.T, ctx context.Context, pool *pgxpool.Pool) time.Time {
	t.Helper()
	var now time.Time
	if err := pool.QueryRow(ctx, `select now()`).Scan(&now); err != nil {
		t.Fatalf("read database clock: %v", err)
	}
	return now.UTC()
}

func TestEnqueueReconcileCoalescesAndBumpsRevision(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, pool := newQueueStore(t, ctx)

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
	store, pool := newQueueStore(t, ctx)

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
	store, _ := newQueueStore(t, ctx)

	if err := store.Enqueue(ctx, queue.Request{Kind: "nope", Payload: json.RawMessage(`{"key":"k"}`)}); err == nil {
		t.Fatal("Enqueue accepted an unknown kind")
	}
}

func TestEnqueueWithoutRegistryFails(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)

	if err := store.Enqueue(ctx, queue.Request{Kind: "reconcile", Payload: json.RawMessage(`{"key":"k"}`)}); err == nil {
		t.Fatal("Enqueue succeeded on a store with no queue registry")
	}
}

func TestClaimLeasesAndSkipsClaimedRows(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, pool := newQueueStore(t, ctx)

	for _, key := range []string{"k1", "k2"} {
		payload := json.RawMessage(`{"key":"` + key + `"}`)
		if err := store.Enqueue(ctx, queue.Request{Kind: "reconcile", Payload: payload, TenantID: "t1"}); err != nil {
			t.Fatalf("Enqueue %s returned error: %v", key, err)
		}
	}

	now := dbNow(t, ctx, pool)
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

func TestClaimReclaimsExpiredLease(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, pool := newQueueStore(t, ctx)

	if err := store.Enqueue(ctx, queue.Request{Kind: "reconcile", Payload: json.RawMessage(`{"key":"k1"}`), TenantID: "t1"}); err != nil {
		t.Fatalf("Enqueue returned error: %v", err)
	}
	now := dbNow(t, ctx, pool)
	if _, err := store.Claim(ctx, now, now.Add(30*time.Second), 10, nil); err != nil {
		t.Fatalf("first Claim returned error: %v", err)
	}

	// A dead worker releases nothing; the lease simply stops matching.
	after := now.Add(31 * time.Second)
	again, err := store.Claim(ctx, after, after.Add(30*time.Second), 10, nil)
	if err != nil {
		t.Fatalf("second Claim returned error: %v", err)
	}
	if len(again) != 1 || again[0].Attempts != 2 {
		t.Fatalf("second Claim = %+v, want the reclaimed row with Attempts 2", again)
	}
}

func TestClaimFiltersByKind(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, pool := newQueueStore(t, ctx)

	if err := store.Enqueue(ctx,
		queue.Request{Kind: "reconcile", Payload: json.RawMessage(`{"key":"k1"}`), TenantID: "t1"},
		queue.Request{Kind: "job", Payload: json.RawMessage(`{"key":"k2"}`), TenantID: "t1"},
	); err != nil {
		t.Fatalf("Enqueue returned error: %v", err)
	}

	now := dbNow(t, ctx, pool)
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
	store, pool := newQueueStore(t, ctx)

	payload := json.RawMessage(`{"key":"stack:a/user:x","role":"viewer"}`)
	if err := store.Enqueue(ctx, queue.Request{Kind: "reconcile", Payload: payload, TenantID: "t1"}); err != nil {
		t.Fatalf("Enqueue returned error: %v", err)
	}
	now := dbNow(t, ctx, pool)
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

func TestCompleteSucceedsOnCurrentRevision(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, pool := newQueueStore(t, ctx)

	if err := store.Enqueue(ctx, queue.Request{Kind: "reconcile", Payload: json.RawMessage(`{"key":"k1"}`), TenantID: "t1"}); err != nil {
		t.Fatalf("Enqueue returned error: %v", err)
	}
	now := dbNow(t, ctx, pool)
	claimed, err := store.Claim(ctx, now, now.Add(30*time.Second), 10, nil)
	if err != nil {
		t.Fatalf("Claim returned error: %v", err)
	}

	completed, err := store.Complete(ctx, claimed[0].ID, claimed[0].Revision)
	if err != nil {
		t.Fatalf("Complete returned error: %v", err)
	}
	if !completed {
		t.Fatal("Complete rejected the current revision")
	}

	// A completed row leaves the partial index, so the same key is insertable.
	if err := store.Enqueue(ctx, queue.Request{Kind: "reconcile", Payload: json.RawMessage(`{"key":"k1"}`), TenantID: "t1"}); err != nil {
		t.Fatalf("Enqueue after completion returned error: %v", err)
	}
}

func TestRescheduleClearsLeaseAndRecordsError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, pool := newQueueStore(t, ctx)

	if err := store.Enqueue(ctx, queue.Request{Kind: "reconcile", Payload: json.RawMessage(`{"key":"k1"}`), TenantID: "t1"}); err != nil {
		t.Fatalf("Enqueue returned error: %v", err)
	}
	now := dbNow(t, ctx, pool)
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
	store, pool := newQueueStore(t, ctx)

	if err := store.Enqueue(ctx,
		queue.Request{Kind: "reconcile", Payload: json.RawMessage(`{"key":"pending"}`), TenantID: "t1"},
		queue.Request{Kind: "reconcile", Payload: json.RawMessage(`{"key":"old"}`), TenantID: "t1"},
	); err != nil {
		t.Fatalf("Enqueue returned error: %v", err)
	}
	if _, err := pool.Exec(ctx, `update work_queue set processed_at = now() - interval '48 hours' where ordering_key = 'old'`); err != nil {
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
	store, _ := newQueueStore(t, ctx)

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
