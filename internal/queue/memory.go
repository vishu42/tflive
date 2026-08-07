package queue

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"
)

// MemoryBackend is an in-memory Backend for tests. Its semantics mirror the
// Postgres implementation: claims respect availableAt and the lease, Complete
// is fenced on revision, Reschedule clears the lease, and Enqueue coalesces or
// dedupes per (kind, ordering key) according to the spec's Mode.
type MemoryBackend struct {
	mutex  sync.Mutex
	rows   map[int64]*memoryRow
	specs  *SpecRegistry
	nextID int64
}

// MemoryOption configures a MemoryBackend at construction.
type MemoryOption func(*MemoryBackend)

// WithSpecs makes the backend usable as an Enqueuer, which a controller needs
// to accept the follow-up work handlers return.
func WithSpecs(specs *SpecRegistry) MemoryOption {
	return func(backend *MemoryBackend) { backend.specs = specs }
}

type memoryRow struct {
	item         Item
	availableAt  time.Time
	claimedUntil time.Time
	processedAt  time.Time
	lastError    string
}

func NewMemoryBackend(options ...MemoryOption) *MemoryBackend {
	backend := &MemoryBackend{rows: map[int64]*memoryRow{}}
	for _, option := range options {
		option(backend)
	}
	return backend
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
	if item.ID > backend.nextID {
		backend.nextID = item.ID
	}
}

// Enqueue mirrors the Postgres upsert: at most one pending row per (kind,
// ordering key), with ModeReconcile overwriting the payload and bumping the
// revision while ModeJob leaves the existing row untouched.
func (backend *MemoryBackend) Enqueue(_ context.Context, requests ...Request) error {
	if backend.specs == nil {
		return errors.New("queue: memory backend has no spec registry")
	}

	backend.mutex.Lock()
	defer backend.mutex.Unlock()

	for _, request := range requests {
		resolved, err := backend.specs.Resolve(request)
		if err != nil {
			return err
		}

		if pending := backend.pendingRow(resolved.Kind, resolved.Key); pending != nil {
			if resolved.Mode == ModeReconcile {
				pending.item.Payload = resolved.Payload
				pending.item.Revision++
			}
			continue
		}

		backend.nextID++
		backend.rows[backend.nextID] = &memoryRow{item: Item{
			ID:           backend.nextID,
			Kind:         resolved.Kind,
			Key:          resolved.Key,
			Payload:      resolved.Payload,
			Revision:     1,
			ActorSubject: resolved.ActorSubject,
			TenantID:     resolved.TenantID,
		}}
	}
	return nil
}

// Items returns every row, completed or not, ordered by id.
func (backend *MemoryBackend) Items() []Item {
	backend.mutex.Lock()
	defer backend.mutex.Unlock()

	items := make([]Item, 0, len(backend.rows))
	for _, row := range backend.rows {
		items = append(items, row.item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items
}

func (backend *MemoryBackend) pendingRow(kind Kind, key string) *memoryRow {
	for _, row := range backend.rows {
		if row.processedAt.IsZero() && row.item.Kind == kind && row.item.Key == key {
			return row
		}
	}
	return nil
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
