package authn

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type reapingStore struct {
	fakeSessionStore
	mu      sync.Mutex
	cutoffs []time.Time
	err     error
	swept   chan struct{}
}

func (store *reapingStore) DeleteSessionsExpiredBefore(_ context.Context, cutoff time.Time) (int, error) {
	store.mu.Lock()
	store.cutoffs = append(store.cutoffs, cutoff)
	store.mu.Unlock()
	if store.swept != nil {
		store.swept <- struct{}{}
	}
	return 1, store.err
}

func (store *reapingStore) sweeps() []time.Time {
	store.mu.Lock()
	defer store.mu.Unlock()
	return append([]time.Time(nil), store.cutoffs...)
}

// TestReapSessionsSweepsBeforeWaiting pins the sweep-then-wait order. A process
// restarted more often than the interval would otherwise never sweep at all,
// and the rows it should have deleted each hold an encrypted ID token.
func TestReapSessionsSweepsBeforeWaiting(t *testing.T) {
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	store := &reapingStore{swept: make(chan struct{}, 1)}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		ReapSessions(ctx, store, time.Hour, func() time.Time { return now })
		close(done)
	}()

	select {
	case <-store.swept:
	case <-time.After(time.Second):
		t.Fatal("no sweep before the first tick — a short-lived process would never reap")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("ReapSessions did not return when its context was cancelled")
	}

	sweeps := store.sweeps()
	if len(sweeps) == 0 || !sweeps[0].Equal(now) {
		t.Fatalf("first sweep cutoff = %v, want %v — rows are dead against the clock, not the interval", sweeps, now)
	}
}

// TestReapSessionsSurvivesAFailedSweep keeps a database blip from silently
// ending the only thing that removes session rows.
func TestReapSessionsSurvivesAFailedSweep(t *testing.T) {
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	store := &reapingStore{err: errors.New("connection refused"), swept: make(chan struct{}, 4)}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		ReapSessions(ctx, store, time.Millisecond, func() time.Time { return now })
		close(done)
	}()

	for range 2 {
		select {
		case <-store.swept:
		case <-time.After(time.Second):
			t.Fatal("the loop stopped after a failed sweep")
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("ReapSessions did not return when its context was cancelled")
	}
}

// TestReapSessionsIgnoresAMissingStore covers the server built without
// WithAuth: there is nothing to reap, and a nil interface would panic in a
// goroutine, which takes the whole process down rather than one request.
func TestReapSessionsIgnoresAMissingStore(t *testing.T) {
	done := make(chan struct{})
	go func() {
		ReapSessions(context.Background(), nil, time.Hour, nil)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("ReapSessions blocked on a nil store")
	}
}
