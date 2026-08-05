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
