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
