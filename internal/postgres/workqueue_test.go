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

// keyedSpec derives its resource key straight from the payload so queue tests
// can exercise both modes without depending on a real domain kind.
func keyedSpec(kind queue.Kind, mode queue.Mode) queue.Spec {
	return queue.Spec{
		Kind: kind,
		Mode: mode,
		Key: func(payload json.RawMessage) (string, error) {
			var parsed struct {
				Key string `json:"key"`
			}
			if err := json.Unmarshal(payload, &parsed); err != nil {
				return "", err
			}
			return parsed.Key, nil
		},
	}
}

func testSpecs(t *testing.T) *queue.SpecRegistry {
	t.Helper()
	specs, err := queue.NewSpecRegistry(
		keyedSpec("reconcile", queue.ModeReconcile),
		keyedSpec("job", queue.ModeJob),
	)
	if err != nil {
		t.Fatalf("NewSpecRegistry returned error: %v", err)
	}
	return specs
}

func newQueueStore(t *testing.T, ctx context.Context) (*Store, *pgxpool.Pool) {
	t.Helper()
	pool := openMigratedTestPool(t, ctx)
	return NewStore(pool, WithQueueSpecs(testSpecs(t))), pool
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
	store, _ := newQueueStore(t, ctx)

	for _, key := range []string{"k1", "k2"} {
		payload := json.RawMessage(`{"key":"` + key + `"}`)
		if err := store.Enqueue(ctx, queue.Request{Kind: "reconcile", Payload: payload, TenantID: "t1"}); err != nil {
			t.Fatalf("Enqueue %s returned error: %v", key, err)
		}
	}

	first, err := store.Claim(ctx, 30*time.Second, 1, nil)
	if err != nil {
		t.Fatalf("first Claim returned error: %v", err)
	}
	if len(first) != 1 || first[0].Attempts != 1 {
		t.Fatalf("first Claim = %+v, want 1 item with Attempts 1", first)
	}

	second, err := store.Claim(ctx, 30*time.Second, 10, nil)
	if err != nil {
		t.Fatalf("second Claim returned error: %v", err)
	}
	if len(second) != 1 || second[0].Key == first[0].Key {
		t.Fatalf("second Claim = %+v, want the other key", second)
	}

	third, err := store.Claim(ctx, 30*time.Second, 10, nil)
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
	store, _ := newQueueStore(t, ctx)

	if err := store.Enqueue(ctx, queue.Request{Kind: "reconcile", Payload: json.RawMessage(`{"key":"k1"}`), TenantID: "t1"}); err != nil {
		t.Fatalf("Enqueue returned error: %v", err)
	}
	// A zero lease has already expired by the time the next statement reads the
	// database clock, so expiry is exercised without waiting for one.
	if _, err := store.Claim(ctx, 0, 10, nil); err != nil {
		t.Fatalf("first Claim returned error: %v", err)
	}

	// A dead worker releases nothing; the lease simply stops matching.
	again, err := store.Claim(ctx, 30*time.Second, 10, nil)
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
	store, _ := newQueueStore(t, ctx)

	if err := store.Enqueue(ctx,
		queue.Request{Kind: "reconcile", Payload: json.RawMessage(`{"key":"k1"}`), TenantID: "t1"},
		queue.Request{Kind: "job", Payload: json.RawMessage(`{"key":"k2"}`), TenantID: "t1"},
	); err != nil {
		t.Fatalf("Enqueue returned error: %v", err)
	}

	claimed, err := store.Claim(ctx, 30*time.Second, 10, []queue.Kind{"job"})
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
	claimed, err := store.Claim(ctx, 30*time.Second, 10, nil)
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
	store, _ := newQueueStore(t, ctx)

	if err := store.Enqueue(ctx, queue.Request{Kind: "reconcile", Payload: json.RawMessage(`{"key":"k1"}`), TenantID: "t1"}); err != nil {
		t.Fatalf("Enqueue returned error: %v", err)
	}
	claimed, err := store.Claim(ctx, 30*time.Second, 10, nil)
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
	claimed, err := store.Claim(ctx, 30*time.Second, 10, nil)
	if err != nil {
		t.Fatalf("Claim returned error: %v", err)
	}

	if err := store.Reschedule(ctx, claimed[0].ID, 0, "openfga unavailable"); err != nil {
		t.Fatalf("Reschedule returned error: %v", err)
	}

	again, err := store.Claim(ctx, 30*time.Second, 10, nil)
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
	if _, err := pool.Exec(ctx, `update work_queue set processed_at = now() - interval '48 hours' where resource_key = 'old'`); err != nil {
		t.Fatalf("age the completed row: %v", err)
	}

	pruned, err := store.Prune(ctx, 24*time.Hour)
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

func TestPruneSkipsWhenAnotherWorkerHoldsTheLock(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, pool := newQueueStore(t, ctx)

	if err := store.Enqueue(ctx, queue.Request{Kind: "reconcile", Payload: json.RawMessage(`{"key":"old"}`), TenantID: "t1"}); err != nil {
		t.Fatalf("Enqueue returned error: %v", err)
	}
	if _, err := pool.Exec(ctx, `update work_queue set processed_at = now() - interval '48 hours'`); err != nil {
		t.Fatalf("age the completed row: %v", err)
	}

	// Stand in for another worker's prune round already in progress.
	holder, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin holder transaction: %v", err)
	}
	if _, err := holder.Exec(ctx, `select pg_advisory_xact_lock(`+pruneLockKeyExpr+`)`); err != nil {
		t.Fatalf("take prune lock: %v", err)
	}

	pruned, err := store.Prune(ctx, 24*time.Hour)
	if err != nil {
		t.Fatalf("Prune returned error: %v — losing the race is not a failure", err)
	}
	if pruned != 0 {
		t.Fatalf("pruned = %d, want 0 while another worker holds the lock", pruned)
	}

	if err := holder.Rollback(ctx); err != nil {
		t.Fatalf("release prune lock: %v", err)
	}

	pruned, err = store.Prune(ctx, 24*time.Hour)
	if err != nil {
		t.Fatalf("second Prune returned error: %v", err)
	}
	if pruned != 1 {
		t.Fatalf("pruned = %d, want 1 once the lock is free", pruned)
	}
}
