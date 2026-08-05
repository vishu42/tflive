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

// Request is what callers enqueue. The ordering key is derived by the Handler,
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
