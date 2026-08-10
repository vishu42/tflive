package queue

import (
	"context"
	"fmt"
	"log"
	"math"
	"math/rand"
	"runtime/debug"
	"sync"
	"time"
)

const (
	defaultPollInterval  = time.Second
	defaultLease         = 30 * time.Second
	defaultBaseBackoff   = time.Second
	defaultMaxBackoff    = 5 * time.Minute
	defaultWorkers       = 4
	defaultPruneAfter    = 24 * time.Hour
	defaultPruneInterval = 10 * time.Minute
	// leaseSafetyDivisor reserves a fraction of the lease for everything that is
	// not the handler: dispatching the batch, settling the row afterwards, and
	// clock skew between this process and the database.
	leaseSafetyDivisor = 6
)

// Options tunes the controller. Zero values fall back to defaults.
//
// Three of these are bound together by one invariant, and it is the invariant
// that keeps the queue's per-key mutual exclusion true:
//
//	ItemTimeout × ceil(BatchSize / Workers) < Lease
//
// A batch is claimed at one instant and every row in it is leased from that
// instant, but only Workers of them run at a time. If the batch outlives the
// lease, another worker can claim rows this one is still delivering — two
// concurrent deliveries for one key, which the unique index was supposed to
// make impossible. The defaults satisfy the invariant, and a configuration that
// violates it is clamped and logged rather than silently unsafe.
type Options struct {
	PollInterval time.Duration
	Lease        time.Duration
	// ItemTimeout bounds a single Deliver call. It must be shorter than the
	// lease so that work is cancelled before the row becomes claimable again.
	ItemTimeout   time.Duration
	BaseBackoff   time.Duration
	MaxBackoff    time.Duration
	PruneAfter    time.Duration
	PruneInterval time.Duration
	// BatchSize defaults to Workers. Claiming more rows than the pool can run
	// leases and counts an attempt against rows that have not been tried yet,
	// so a crash makes them wait out a lease they never used.
	BatchSize int
	Workers   int
	// Kinds optionally restricts this controller to a subset of kinds, so a
	// hot kind can be sharded onto its own controller over the same table.
	Kinds []Kind
}

// safeItemTimeout is the longest a single delivery may take without the batch
// outliving its lease.
func safeItemTimeout(lease time.Duration, batchSize, workers int) time.Duration {
	rounds := (batchSize + workers - 1) / workers
	if rounds < 1 {
		rounds = 1
	}
	return (lease - lease/leaseSafetyDivisor) / time.Duration(rounds)
}

func (options Options) withDefaults() Options {
	if options.PollInterval <= 0 {
		options.PollInterval = defaultPollInterval
	}
	if options.Lease <= 0 {
		options.Lease = defaultLease
	}
	if options.BaseBackoff <= 0 {
		options.BaseBackoff = defaultBaseBackoff
	}
	if options.MaxBackoff <= 0 {
		options.MaxBackoff = defaultMaxBackoff
	}
	if options.PruneAfter <= 0 {
		options.PruneAfter = defaultPruneAfter
	}
	if options.PruneInterval <= 0 {
		options.PruneInterval = defaultPruneInterval
	}
	if options.Workers <= 0 {
		options.Workers = defaultWorkers
	}
	// Claim only what the pool can actually run. The controller reclaims as soon
	// as a batch finishes, so a smaller batch costs extra claim queries, not
	// throughput.
	if options.BatchSize <= 0 {
		options.BatchSize = options.Workers
	}

	safeTimeout := safeItemTimeout(options.Lease, options.BatchSize, options.Workers)
	if options.ItemTimeout <= 0 {
		options.ItemTimeout = safeTimeout
	} else if options.ItemTimeout > safeTimeout {
		log.Printf("queue: ItemTimeout %v exceeds what a %d-item batch on %d workers can finish within a %v lease; clamping to %v",
			options.ItemTimeout, options.BatchSize, options.Workers, options.Lease, safeTimeout)
		options.ItemTimeout = safeTimeout
	}
	return options
}

// Controller claims batches of work and routes each item to its handler.
type Controller struct {
	backend  Backend
	registry *Registry
	enqueuer Enqueuer
	options  Options

	// beforeComplete is a test seam for simulating a newer intent arriving
	// while an item is in flight.
	beforeComplete func()
}

// NewController builds a controller. The enqueuer receives the follow-up work
// handlers return; a controller whose handlers never chain may pass nil.
func NewController(backend Backend, registry *Registry, enqueuer Enqueuer, options Options) *Controller {
	return &Controller{backend: backend, registry: registry, enqueuer: enqueuer, options: options.withDefaults()}
}

// DispatchOnce claims one batch and processes it, returning how many items were
// handled. An error means the claim itself failed; per-item failures are
// recorded on the row and do not surface here.
func (controller *Controller) DispatchOnce(ctx context.Context) (int, error) {
	items, err := controller.backend.Claim(ctx, controller.options.Lease, controller.options.BatchSize, controller.options.Kinds)
	if err != nil {
		return 0, err
	}
	if len(items) == 0 {
		return 0, nil
	}

	work := make(chan Item)
	var group sync.WaitGroup
	for worker := 0; worker < controller.options.Workers; worker++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for item := range work {
				controller.process(ctx, item)
			}
		}()
	}
	for _, item := range items {
		work <- item
	}
	close(work)
	group.Wait()

	return len(items), nil
}

func (controller *Controller) process(ctx context.Context, item Item) {
	handler, ok := controller.registry.Handler(item.Kind)
	if !ok {
		// Never fail an unknown kind. During a rolling deploy the producer can
		// be ahead of this worker; dropping the item would lose work forever.
		controller.reschedule(ctx, item, defaultMaxBackoff, "no handler registered for kind "+string(item.Kind))
		return
	}

	maxBackoff := controller.options.MaxBackoff
	if timings, ok := handler.(Timings); ok && timings.MaxBackoff() > 0 {
		maxBackoff = timings.MaxBackoff()
	}

	// Only the handler runs under the item timeout. Settling the row afterwards
	// uses the outer context, so a delivery that runs out of time can still
	// record why.
	deliverCtx, cancelDeliver := context.WithTimeout(ctx, controller.options.ItemTimeout)
	followUps, err := deliver(deliverCtx, handler, item)
	cancelDeliver()
	if err != nil {
		controller.reschedule(ctx, item, maxBackoff, err.Error())
		return
	}

	// Follow-ups are enqueued outside the completing statement. That is safe
	// because every kind derives its key deterministically and ModeJob ignores
	// a duplicate key: a crash between this enqueue and the complete below
	// replays both without harm.
	if len(followUps) > 0 {
		if controller.enqueuer == nil {
			controller.reschedule(ctx, item, maxBackoff, "no enqueuer configured for follow-up work")
			return
		}
		if err := controller.enqueuer.Enqueue(ctx, followUps...); err != nil {
			controller.reschedule(ctx, item, maxBackoff, err.Error())
			return
		}
	}

	if controller.beforeComplete != nil {
		controller.beforeComplete()
	}

	completed, err := controller.backend.Complete(ctx, item.ID, item.Revision)
	if err != nil {
		log.Printf("queue: complete %s item %d: %v", item.Kind, item.ID, err)
		return
	}
	if !completed {
		// A newer intent coalesced onto this row while it was in flight. Run
		// again immediately against the fresher payload.
		if err := controller.backend.Reschedule(ctx, item.ID, 0, ""); err != nil {
			log.Printf("queue: reschedule superseded %s item %d: %v", item.Kind, item.ID, err)
		}
	}
}

// deliver invokes the handler, turning a panic into an ordinary delivery error.
//
// Handlers are this package's extension point: they live in domain packages and
// call external systems, so their bugs are not ours to prevent. Without this
// recovery a panic on a worker goroutine takes down the entire process — in the
// worker binary that means the Temporal worker and the workflow dispatcher die
// with it, over one bad payload. Recovering costs one deferred call per item
// and turns a crash into a retry.
//
// The panic value goes into last_error, which the caller-facing queue endpoint
// shows; the stack trace goes to the log only.
func deliver(ctx context.Context, handler Handler, item Item) (followUps []Request, err error) {
	defer func() {
		recovery := recover()
		if recovery == nil {
			return
		}
		log.Printf("queue: handler for %s item %d panicked: %v\n%s", item.Kind, item.ID, recovery, debug.Stack())
		followUps, err = nil, fmt.Errorf("handler panicked: %v", recovery)
	}()

	return handler.Deliver(ctx, item)
}

func (controller *Controller) reschedule(ctx context.Context, item Item, maxBackoff time.Duration, lastErr string) {
	if err := controller.backend.Reschedule(ctx, item.ID, controller.backoff(item.Attempts, maxBackoff), lastErr); err != nil {
		log.Printf("queue: reschedule %s item %d: %v", item.Kind, item.ID, err)
	}
}

// backoff grows exponentially from BaseBackoff and is jittered so that every
// item stuck behind one outage does not become available on the same tick.
func (controller *Controller) backoff(attempts int, maxBackoff time.Duration) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	if maxBackoff <= 0 {
		maxBackoff = defaultMaxBackoff
	}
	base := controller.options.BaseBackoff
	if base <= 0 {
		base = defaultBaseBackoff
	}

	exponent := float64(attempts - 1)
	if exponent > 32 {
		exponent = 32
	}
	delay := time.Duration(float64(base) * math.Pow(2, exponent))
	if delay <= 0 || delay > maxBackoff {
		delay = maxBackoff
	}
	jitter := time.Duration(rand.Int63n(int64(delay)/4 + 1))
	if delay+jitter > maxBackoff {
		return maxBackoff
	}
	return delay + jitter
}

// Run drains the queue until ctx is cancelled, pruning completed rows
// periodically.
func (controller *Controller) Run(ctx context.Context) {
	timer := time.NewTimer(controller.jitteredPollInterval())
	defer timer.Stop()
	nextPrune := time.Now().Add(controller.options.PruneInterval)

	for {
		// Pruning is checked on every pass rather than from a channel, because a
		// sustained backlog takes the fast path below and would otherwise starve
		// it for as long as the backlog lasts.
		if time.Now().After(nextPrune) {
			if _, err := controller.backend.Prune(ctx, controller.options.PruneAfter); err != nil && ctx.Err() == nil {
				log.Printf("queue: prune failed: %v", err)
			}
			nextPrune = time.Now().Add(controller.options.PruneInterval)
		}

		processed, err := controller.DispatchOnce(ctx)
		if err != nil && ctx.Err() == nil {
			log.Printf("queue: dispatch failed: %v", err)
		}
		if processed > 0 && err == nil {
			// Work is waiting; drain it without pausing.
			continue
		}

		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(controller.jitteredPollInterval())

		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
	}
}

// jitteredPollInterval spreads idle polls so that several workers restarted
// together, or nudged by the same GC pause, do not settle into one synchronised
// tick that hits the database in a spike.
func (controller *Controller) jitteredPollInterval() time.Duration {
	spread := controller.options.PollInterval / 10
	if spread < time.Millisecond {
		spread = time.Millisecond
	}
	return controller.options.PollInterval + time.Duration(rand.Int63n(int64(spread)))
}
