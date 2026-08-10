package queue

import (
	"context"
	"testing"
	"time"
)

// newClockedBackend returns a backend whose clock the test drives, mirroring
// the Postgres backend reading time from the database rather than the caller.
func newClockedBackend() (*MemoryBackend, *testClock) {
	clock := &testClock{now: time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC)}
	return NewMemoryBackend(WithClock(clock.Now)), clock
}

func TestMemoryBackendClaimRespectsAvailableAt(t *testing.T) {
	t.Parallel()

	backend, clock := newClockedBackend()
	backend.Add(Item{ID: 1, Kind: "a", Revision: 1})
	backend.AddAt(Item{ID: 2, Kind: "a", Revision: 1}, clock.Now().Add(time.Minute))

	claimed, err := backend.Claim(context.Background(), 30*time.Second, 10, nil)
	if err != nil {
		t.Fatalf("Claim returned error: %v", err)
	}
	if len(claimed) != 1 || claimed[0].ID != 1 {
		t.Fatalf("Claim returned %+v, want only item 1", claimed)
	}
}

func TestMemoryBackendClaimSkipsLeasedItems(t *testing.T) {
	t.Parallel()

	backend, clock := newClockedBackend()
	backend.Add(Item{ID: 1, Kind: "a", Revision: 1})

	if _, err := backend.Claim(context.Background(), 30*time.Second, 10, nil); err != nil {
		t.Fatalf("first Claim returned error: %v", err)
	}
	clock.advance(time.Second)

	claimed, err := backend.Claim(context.Background(), 30*time.Second, 10, nil)
	if err != nil {
		t.Fatalf("second Claim returned error: %v", err)
	}
	if len(claimed) != 0 {
		t.Fatalf("Claim returned %d leased items, want 0", len(claimed))
	}
}

func TestMemoryBackendClaimReclaimsExpiredLease(t *testing.T) {
	t.Parallel()

	backend, clock := newClockedBackend()
	backend.Add(Item{ID: 1, Kind: "a", Revision: 1})

	if _, err := backend.Claim(context.Background(), 30*time.Second, 10, nil); err != nil {
		t.Fatalf("first Claim returned error: %v", err)
	}
	clock.advance(31 * time.Second)

	claimed, err := backend.Claim(context.Background(), 30*time.Second, 10, nil)
	if err != nil {
		t.Fatalf("second Claim returned error: %v", err)
	}
	if len(claimed) != 1 || claimed[0].Attempts != 2 {
		t.Fatalf("Claim returned %+v, want item 1 with Attempts 2", claimed)
	}
}

func TestMemoryBackendClaimFiltersKinds(t *testing.T) {
	t.Parallel()

	backend, _ := newClockedBackend()
	backend.Add(Item{ID: 1, Kind: "a", Revision: 1})
	backend.Add(Item{ID: 2, Kind: "b", Revision: 1})

	claimed, err := backend.Claim(context.Background(), 30*time.Second, 10, []Kind{"b"})
	if err != nil {
		t.Fatalf("Claim returned error: %v", err)
	}
	if len(claimed) != 1 || claimed[0].Kind != "b" {
		t.Fatalf("Claim returned %+v, want only kind b", claimed)
	}
}

func TestMemoryBackendRescheduleDelaysFromTheBackendClock(t *testing.T) {
	t.Parallel()

	backend, clock := newClockedBackend()
	backend.Add(Item{ID: 1, Kind: "a", Revision: 1})
	if _, err := backend.Claim(context.Background(), 30*time.Second, 10, nil); err != nil {
		t.Fatalf("Claim returned error: %v", err)
	}

	if err := backend.Reschedule(context.Background(), 1, time.Minute, "boom"); err != nil {
		t.Fatalf("Reschedule returned error: %v", err)
	}

	claimed, err := backend.Claim(context.Background(), 30*time.Second, 10, nil)
	if err != nil {
		t.Fatalf("Claim returned error: %v", err)
	}
	if len(claimed) != 0 {
		t.Fatalf("Claim returned %+v, want none until the delay elapses", claimed)
	}

	clock.advance(time.Minute + time.Second)
	claimed, err = backend.Claim(context.Background(), 30*time.Second, 10, nil)
	if err != nil {
		t.Fatalf("Claim returned error: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("Claim returned %+v, want the rescheduled item", claimed)
	}
}

func TestMemoryBackendCompleteRejectsStaleRevision(t *testing.T) {
	t.Parallel()

	backend, _ := newClockedBackend()
	backend.Add(Item{ID: 1, Kind: "a", Revision: 1})
	if _, err := backend.Claim(context.Background(), 30*time.Second, 10, nil); err != nil {
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

func TestMemoryBackendPruneRemovesOnlyOldCompletedRows(t *testing.T) {
	t.Parallel()

	backend, clock := newClockedBackend()
	backend.Add(Item{ID: 1, Kind: "a", Revision: 1})
	backend.Add(Item{ID: 2, Kind: "a", Revision: 1})

	if _, err := backend.Claim(context.Background(), 30*time.Second, 10, nil); err != nil {
		t.Fatalf("Claim returned error: %v", err)
	}
	if _, err := backend.Complete(context.Background(), 1, 1); err != nil {
		t.Fatalf("Complete returned error: %v", err)
	}

	// Too recent to prune yet.
	pruned, err := backend.Prune(context.Background(), time.Hour)
	if err != nil {
		t.Fatalf("Prune returned error: %v", err)
	}
	if pruned != 0 {
		t.Fatalf("pruned = %d, want 0 — the row is inside the retention window", pruned)
	}

	clock.advance(2 * time.Hour)
	pruned, err = backend.Prune(context.Background(), time.Hour)
	if err != nil {
		t.Fatalf("Prune returned error: %v", err)
	}
	if pruned != 1 {
		t.Fatalf("pruned = %d, want 1", pruned)
	}
	if backend.Pending() != 1 {
		t.Fatalf("Pending = %d, want 1 — the uncompleted row must survive", backend.Pending())
	}
}
