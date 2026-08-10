package queue

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

type recordingHandler struct {
	stubHandler
	mutex     sync.Mutex
	delivered []Item
	followUps []Request
	deliverEr error
}

func (h *recordingHandler) Deliver(_ context.Context, item Item) ([]Request, error) {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	h.delivered = append(h.delivered, item)
	if h.deliverEr != nil {
		return nil, h.deliverEr
	}
	return h.followUps, nil
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

	handler := &recordingHandler{stubHandler: stubHandler{kind: "a", key: "k"}}
	registry, err := NewRegistry(handler)
	if err != nil {
		t.Fatalf("NewRegistry returned error: %v", err)
	}
	backend := NewMemoryBackend()
	backend.Add(Item{ID: 1, Kind: "a", Revision: 1, Payload: json.RawMessage(`{}`)})

	controller := NewController(backend, registry, nil, testOptions())
	processed, err := controller.DispatchOnce(context.Background())
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

	controller := NewController(backend, registry, nil, testOptions())
	if _, err := controller.DispatchOnce(context.Background()); err != nil {
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

	registry, err := NewRegistry(stubHandler{kind: "a", key: "k"})
	if err != nil {
		t.Fatalf("NewRegistry returned error: %v", err)
	}
	backend := NewMemoryBackend()
	backend.Add(Item{ID: 1, Kind: "unregistered", Revision: 1, Payload: json.RawMessage(`{}`)})

	controller := NewController(backend, registry, nil, testOptions())
	if _, err := controller.DispatchOnce(context.Background()); err != nil {
		t.Fatalf("DispatchOnce returned error: %v", err)
	}
	if backend.Pending() != 1 {
		t.Fatalf("Pending = %d, want 1 — an unknown kind must never be dropped", backend.Pending())
	}
}

func TestDispatchOnceReschedulesWhenRevisionMoved(t *testing.T) {
	t.Parallel()

	handler := &recordingHandler{stubHandler: stubHandler{kind: "a", key: "k"}}
	registry, err := NewRegistry(handler)
	if err != nil {
		t.Fatalf("NewRegistry returned error: %v", err)
	}
	backend := NewMemoryBackend()
	backend.Add(Item{ID: 1, Kind: "a", Revision: 1, Payload: json.RawMessage(`{}`)})

	controller := NewController(backend, registry, nil, testOptions())
	controller.beforeComplete = func() { backend.Bump(1) }

	if _, err := controller.DispatchOnce(context.Background()); err != nil {
		t.Fatalf("DispatchOnce returned error: %v", err)
	}
	if backend.Pending() != 1 {
		t.Fatalf("Pending = %d, want 1 — a newer intent must re-run", backend.Pending())
	}
}

func TestBackoffGrowsAndCaps(t *testing.T) {
	t.Parallel()

	options := testOptions()
	controller := NewController(NewMemoryBackend(), &Registry{}, nil, options)

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

func TestDispatchOnceEnqueuesFollowUpWork(t *testing.T) {
	t.Parallel()

	handler := &recordingHandler{
		stubHandler: stubHandler{kind: "a", key: "k"},
		followUps:   []Request{{Kind: "b", Payload: json.RawMessage(`{"next":true}`)}},
	}
	registry, err := NewRegistry(handler, stubHandler{kind: "b", mode: ModeJob, key: "k2"})
	if err != nil {
		t.Fatalf("NewRegistry returned error: %v", err)
	}
	backend := NewMemoryBackend(WithSpecs(registry.Specs()))
	backend.Add(Item{ID: 1, Kind: "a", Revision: 1, Payload: json.RawMessage(`{}`)})

	controller := NewController(backend, registry, backend, testOptions())
	if _, err := controller.DispatchOnce(context.Background()); err != nil {
		t.Fatalf("DispatchOnce returned error: %v", err)
	}

	items := backend.Items()
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2 — the follow-up must be enqueued", len(items))
	}
	if items[1].Kind != "b" || items[1].Key != "k2" {
		t.Fatalf("follow-up = %+v, want kind b keyed k2", items[1])
	}
	if backend.Pending() != 1 {
		t.Fatalf("Pending = %d, want 1 — only the follow-up stays pending", backend.Pending())
	}
}

func TestDispatchOnceSkipsFollowUpsWhenDeliveryFails(t *testing.T) {
	t.Parallel()

	handler := &recordingHandler{
		stubHandler: stubHandler{kind: "a", key: "k"},
		followUps:   []Request{{Kind: "b", Payload: json.RawMessage(`{}`)}},
		deliverEr:   errors.New("openfga unavailable"),
	}
	registry, err := NewRegistry(handler, stubHandler{kind: "b", mode: ModeJob, key: "k2"})
	if err != nil {
		t.Fatalf("NewRegistry returned error: %v", err)
	}
	backend := NewMemoryBackend(WithSpecs(registry.Specs()))
	backend.Add(Item{ID: 1, Kind: "a", Revision: 1, Payload: json.RawMessage(`{}`)})

	controller := NewController(backend, registry, backend, testOptions())
	if _, err := controller.DispatchOnce(context.Background()); err != nil {
		t.Fatalf("DispatchOnce returned error: %v", err)
	}

	if items := backend.Items(); len(items) != 1 {
		t.Fatalf("items = %d, want 1 — a failed delivery must announce nothing", len(items))
	}
}

func TestDispatchOnceReschedulesWhenFollowUpEnqueueFails(t *testing.T) {
	t.Parallel()

	handler := &recordingHandler{
		stubHandler: stubHandler{kind: "a", key: "k"},
		followUps:   []Request{{Kind: "b", Payload: json.RawMessage(`{}`)}},
	}
	registry, err := NewRegistry(handler)
	if err != nil {
		t.Fatalf("NewRegistry returned error: %v", err)
	}
	backend := NewMemoryBackend(WithSpecs(registry.Specs()))
	backend.Add(Item{ID: 1, Kind: "a", Revision: 1, Payload: json.RawMessage(`{}`)})

	controller := NewController(backend, registry, backend, testOptions())
	if _, err := controller.DispatchOnce(context.Background()); err != nil {
		t.Fatalf("DispatchOnce returned error: %v", err)
	}

	if backend.Pending() != 1 {
		t.Fatalf("Pending = %d, want 1 — an unenqueueable chain must retry, not complete", backend.Pending())
	}
	if backend.LastError(1) == "" {
		t.Fatal("LastError was not recorded for the failed follow-up enqueue")
	}
}

type slowHandler struct {
	stubHandler
	release chan struct{}
	entered chan struct{}
}

func (h *slowHandler) Deliver(context.Context, Item) ([]Request, error) {
	h.entered <- struct{}{}
	<-h.release
	return nil, nil
}

func TestDispatchOnceRunsKindsConcurrently(t *testing.T) {
	t.Parallel()

	slow := &slowHandler{
		stubHandler: stubHandler{kind: "slow", key: "k"},
		release:     make(chan struct{}),
		entered:     make(chan struct{}, 1),
	}
	fast := &recordingHandler{stubHandler: stubHandler{kind: "fast", key: "k"}}
	registry, err := NewRegistry(slow, fast)
	if err != nil {
		t.Fatalf("NewRegistry returned error: %v", err)
	}
	backend := NewMemoryBackend()
	backend.Add(Item{ID: 1, Kind: "slow", Revision: 1, Payload: json.RawMessage(`{}`)})
	backend.Add(Item{ID: 2, Kind: "fast", Revision: 1, Payload: json.RawMessage(`{}`)})

	controller := NewController(backend, registry, nil, testOptions())
	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := controller.DispatchOnce(context.Background()); err != nil {
			t.Errorf("DispatchOnce returned error: %v", err)
		}
	}()

	<-slow.entered
	deadline := time.After(2 * time.Second)
	for fast.count() == 0 {
		select {
		case <-deadline:
			t.Fatal("a slow handler starved another kind in the same batch")
		case <-time.After(time.Millisecond):
		}
	}
	close(slow.release)
	<-done
}

type cappedHandler struct {
	stubHandler
	maxBackoff time.Duration
}

func (h cappedHandler) Deliver(context.Context, Item) ([]Request, error) {
	return nil, errors.New("still failing")
}

func (h cappedHandler) MaxBackoff() time.Duration { return h.maxBackoff }

func TestDispatchOnceHonoursHandlerMaxBackoff(t *testing.T) {
	t.Parallel()

	handler := cappedHandler{
		stubHandler: stubHandler{kind: "a", key: "k"},
		maxBackoff:  2 * time.Second,
	}
	registry, err := NewRegistry(handler)
	if err != nil {
		t.Fatalf("NewRegistry returned error: %v", err)
	}

	clock := &testClock{now: time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)}
	backend := NewMemoryBackend(WithClock(clock.Now))
	// Attempts is high enough that the default cap would schedule minutes out.
	backend.Add(Item{ID: 1, Kind: "a", Revision: 1, Attempts: 20, Payload: json.RawMessage(`{}`)})

	controller := NewController(backend, registry, nil, testOptions())
	if _, err := controller.DispatchOnce(context.Background()); err != nil {
		t.Fatalf("DispatchOnce returned error: %v", err)
	}

	clock.advance(handler.maxBackoff + time.Second)
	claimed, err := backend.Claim(context.Background(), time.Minute, 10, nil)
	if err != nil {
		t.Fatalf("Claim returned error: %v", err)
	}
	if len(claimed) == 0 {
		t.Fatal("item was not claimable within the handler's MaxBackoff")
	}
}

// testClock is a manually advanced clock, standing in for the database clock
// the Postgres backend reads.
type testClock struct {
	mutex sync.Mutex
	now   time.Time
}

func (c *testClock) Now() time.Time {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.now
}

func (c *testClock) advance(d time.Duration) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.now = c.now.Add(d)
}

type panickingHandler struct {
	stubHandler
	panicWith any
}

func (h panickingHandler) Deliver(context.Context, Item) ([]Request, error) {
	panic(h.panicWith)
}

func TestDispatchOnceReschedulesPanickingHandler(t *testing.T) {
	t.Parallel()

	handler := panickingHandler{
		stubHandler: stubHandler{kind: "a", key: "k"},
		panicWith:   "nil map write",
	}
	registry, err := NewRegistry(handler)
	if err != nil {
		t.Fatalf("NewRegistry returned error: %v", err)
	}
	backend := NewMemoryBackend()
	backend.Add(Item{ID: 1, Kind: "a", Revision: 1, Payload: json.RawMessage(`{}`)})

	controller := NewController(backend, registry, nil, testOptions())
	// Reaching the next line at all is the assertion that matters: an
	// unrecovered panic here would take down the test binary.
	if _, err := controller.DispatchOnce(context.Background()); err != nil {
		t.Fatalf("DispatchOnce returned error: %v", err)
	}

	if backend.Pending() != 1 {
		t.Fatalf("Pending = %d, want 1 — a panicking handler must retry, not complete", backend.Pending())
	}
	if lastErr := backend.LastError(1); !strings.Contains(lastErr, "panicked") {
		t.Fatalf("LastError = %q, want it to record the panic", lastErr)
	}
}

func TestDispatchOncePanicDoesNotStopTheBatch(t *testing.T) {
	t.Parallel()

	panicking := panickingHandler{
		stubHandler: stubHandler{kind: "panics", key: "k"},
		panicWith:   errors.New("boom"),
	}
	healthy := &recordingHandler{stubHandler: stubHandler{kind: "healthy", key: "k"}}
	registry, err := NewRegistry(panicking, healthy)
	if err != nil {
		t.Fatalf("NewRegistry returned error: %v", err)
	}
	backend := NewMemoryBackend()
	backend.Add(Item{ID: 1, Kind: "panics", Revision: 1, Payload: json.RawMessage(`{}`)})
	backend.Add(Item{ID: 2, Kind: "healthy", Revision: 1, Payload: json.RawMessage(`{}`)})

	controller := NewController(backend, registry, nil, testOptions())
	if _, err := controller.DispatchOnce(context.Background()); err != nil {
		t.Fatalf("DispatchOnce returned error: %v", err)
	}

	if healthy.count() != 1 {
		t.Fatalf("healthy handler delivered %d items, want 1 — one bad kind must not stop the batch", healthy.count())
	}
	if backend.Pending() != 1 {
		t.Fatalf("Pending = %d, want 1 — only the panicking item stays", backend.Pending())
	}
}

type blockingHandler struct {
	stubHandler
	entered chan struct{}
}

func (h *blockingHandler) Deliver(ctx context.Context, _ Item) ([]Request, error) {
	close(h.entered)
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestDispatchOnceBoundsDeliveryByItemTimeout(t *testing.T) {
	t.Parallel()

	handler := &blockingHandler{
		stubHandler: stubHandler{kind: "a", key: "k"},
		entered:     make(chan struct{}),
	}
	registry, err := NewRegistry(handler)
	if err != nil {
		t.Fatalf("NewRegistry returned error: %v", err)
	}
	backend := NewMemoryBackend()
	backend.Add(Item{ID: 1, Kind: "a", Revision: 1, Payload: json.RawMessage(`{}`)})

	options := testOptions()
	options.ItemTimeout = 20 * time.Millisecond
	controller := NewController(backend, registry, nil, options)

	// A handler that never returns on its own must still be given up on, or it
	// would hold the item past its lease and let a second worker claim it.
	if _, err := controller.DispatchOnce(context.Background()); err != nil {
		t.Fatalf("DispatchOnce returned error: %v", err)
	}

	if backend.Pending() != 1 {
		t.Fatalf("Pending = %d, want 1", backend.Pending())
	}
	if lastErr := backend.LastError(1); !strings.Contains(lastErr, "deadline exceeded") {
		t.Fatalf("LastError = %q, want the deadline recorded", lastErr)
	}
}

func TestOptionsKeepBatchWithinLease(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		options         Options
		wantBatchSize   int
		wantItemTimeout time.Duration
	}{
		{
			name:            "batch defaults to the worker count",
			options:         Options{Lease: 30 * time.Second, Workers: 4},
			wantBatchSize:   4,
			wantItemTimeout: 25 * time.Second,
		},
		{
			name:            "a wider batch shrinks the per-item budget",
			options:         Options{Lease: 30 * time.Second, Workers: 4, BatchSize: 20},
			wantBatchSize:   20,
			wantItemTimeout: 5 * time.Second,
		},
		{
			name:            "an over-long timeout is clamped to what the lease allows",
			options:         Options{Lease: 30 * time.Second, Workers: 4, ItemTimeout: time.Minute},
			wantBatchSize:   4,
			wantItemTimeout: 25 * time.Second,
		},
		{
			name:            "a shorter timeout is left alone",
			options:         Options{Lease: 30 * time.Second, Workers: 4, ItemTimeout: time.Second},
			wantBatchSize:   4,
			wantItemTimeout: time.Second,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			options := test.options.withDefaults()
			if options.BatchSize != test.wantBatchSize {
				t.Fatalf("BatchSize = %d, want %d", options.BatchSize, test.wantBatchSize)
			}
			if options.ItemTimeout != test.wantItemTimeout {
				t.Fatalf("ItemTimeout = %v, want %v", options.ItemTimeout, test.wantItemTimeout)
			}

			rounds := (options.BatchSize + options.Workers - 1) / options.Workers
			if worst := options.ItemTimeout * time.Duration(rounds); worst >= options.Lease {
				t.Fatalf("a full batch can take %v, which is not shorter than the %v lease", worst, options.Lease)
			}
		})
	}
}
