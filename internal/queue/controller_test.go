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

	now := time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC)
	handler := &recordingHandler{stubHandler: stubHandler{kind: "a", key: "k"}}
	registry, err := NewRegistry(handler)
	if err != nil {
		t.Fatalf("NewRegistry returned error: %v", err)
	}
	backend := NewMemoryBackend()
	backend.Add(Item{ID: 1, Kind: "a", Revision: 1, Payload: json.RawMessage(`{}`)})

	controller := NewController(backend, registry, nil, testOptions())
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

	controller := NewController(backend, registry, nil, testOptions())
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

	controller := NewController(backend, registry, nil, testOptions())
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

	controller := NewController(backend, registry, nil, testOptions())
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

	now := time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)
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
	if _, err := controller.DispatchOnce(context.Background(), now); err != nil {
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

	now := time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)
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
	if _, err := controller.DispatchOnce(context.Background(), now); err != nil {
		t.Fatalf("DispatchOnce returned error: %v", err)
	}

	if items := backend.Items(); len(items) != 1 {
		t.Fatalf("items = %d, want 1 — a failed delivery must announce nothing", len(items))
	}
}

func TestDispatchOnceReschedulesWhenFollowUpEnqueueFails(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)
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
	if _, err := controller.DispatchOnce(context.Background(), now); err != nil {
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

	now := time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)
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
		if _, err := controller.DispatchOnce(context.Background(), now); err != nil {
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

	now := time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)
	handler := cappedHandler{
		stubHandler: stubHandler{kind: "a", key: "k"},
		maxBackoff:  2 * time.Second,
	}
	registry, err := NewRegistry(handler)
	if err != nil {
		t.Fatalf("NewRegistry returned error: %v", err)
	}
	backend := NewMemoryBackend()
	// Attempts is high enough that the default cap would schedule minutes out.
	backend.Add(Item{ID: 1, Kind: "a", Revision: 1, Attempts: 20, Payload: json.RawMessage(`{}`)})

	controller := NewController(backend, registry, nil, testOptions())
	if _, err := controller.DispatchOnce(context.Background(), now); err != nil {
		t.Fatalf("DispatchOnce returned error: %v", err)
	}

	if _, err := backend.Claim(context.Background(), now.Add(handler.maxBackoff+time.Second), now, 10, nil); err != nil {
		t.Fatalf("Claim returned error: %v", err)
	}
	claimed, err := backend.Claim(context.Background(), now.Add(handler.maxBackoff+time.Second), now, 10, nil)
	if err != nil {
		t.Fatalf("Claim returned error: %v", err)
	}
	if len(claimed) == 0 {
		t.Fatal("item was not claimable within the handler's MaxBackoff")
	}
}
