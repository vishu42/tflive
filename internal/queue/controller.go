package queue

import (
	"context"
	"log"
	"math"
	"math/rand"
	"sync"
	"time"
)

const (
	defaultPollInterval  = time.Second
	defaultLease         = 30 * time.Second
	defaultBaseBackoff   = time.Second
	defaultMaxBackoff    = 5 * time.Minute
	defaultBatchSize     = 20
	defaultWorkers       = 4
	defaultPruneAfter    = 24 * time.Hour
	defaultPruneInterval = 10 * time.Minute
)

// Options tunes the controller. Zero values fall back to defaults.
type Options struct {
	PollInterval  time.Duration
	Lease         time.Duration
	BaseBackoff   time.Duration
	MaxBackoff    time.Duration
	PruneAfter    time.Duration
	PruneInterval time.Duration
	BatchSize     int
	Workers       int
	// Kinds optionally restricts this controller to a subset of kinds, so a
	// hot kind can be sharded onto its own controller over the same table.
	Kinds []Kind
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
	if options.BatchSize <= 0 {
		options.BatchSize = defaultBatchSize
	}
	if options.Workers <= 0 {
		options.Workers = defaultWorkers
	}
	return options
}

// Controller claims batches of work and routes each item to its handler.
type Controller struct {
	backend  Backend
	registry *Registry
	options  Options

	// beforeComplete is a test seam for simulating a newer intent arriving
	// while an item is in flight.
	beforeComplete func()
}

func NewController(backend Backend, registry *Registry, options Options) *Controller {
	return &Controller{backend: backend, registry: registry, options: options.withDefaults()}
}

// DispatchOnce claims one batch and processes it, returning how many items were
// handled. An error means the claim itself failed; per-item failures are
// recorded on the row and do not surface here.
func (controller *Controller) DispatchOnce(ctx context.Context, now time.Time) (int, error) {
	items, err := controller.backend.Claim(ctx, now, now.Add(controller.options.Lease), controller.options.BatchSize, controller.options.Kinds)
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
				controller.process(ctx, item, now)
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

func (controller *Controller) process(ctx context.Context, item Item, now time.Time) {
	handler, ok := controller.registry.Handler(item.Kind)
	if !ok {
		// Never fail an unknown kind. During a rolling deploy the producer can
		// be ahead of this worker; dropping the item would lose work forever.
		controller.reschedule(ctx, item, now, defaultMaxBackoff, "no handler registered for kind "+string(item.Kind))
		return
	}

	maxBackoff := controller.options.MaxBackoff
	if timings, ok := handler.(Timings); ok && timings.MaxBackoff() > 0 {
		maxBackoff = timings.MaxBackoff()
	}

	if err := handler.Deliver(ctx, item); err != nil {
		controller.reschedule(ctx, item, now, maxBackoff, err.Error())
		return
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
		if err := controller.backend.Reschedule(ctx, item.ID, now, ""); err != nil {
			log.Printf("queue: reschedule superseded %s item %d: %v", item.Kind, item.ID, err)
		}
	}
}

func (controller *Controller) reschedule(ctx context.Context, item Item, now time.Time, maxBackoff time.Duration, lastErr string) {
	availableAt := now.Add(controller.backoff(item.Attempts, maxBackoff))
	if err := controller.backend.Reschedule(ctx, item.ID, availableAt, lastErr); err != nil {
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
	ticker := time.NewTicker(controller.options.PollInterval)
	defer ticker.Stop()
	pruneTicker := time.NewTicker(controller.options.PruneInterval)
	defer pruneTicker.Stop()

	for {
		processed, err := controller.DispatchOnce(ctx, time.Now())
		if err != nil && ctx.Err() == nil {
			log.Printf("queue: dispatch failed: %v", err)
		}
		if processed > 0 && err == nil {
			continue
		}

		select {
		case <-ctx.Done():
			return
		case <-pruneTicker.C:
			if _, err := controller.backend.Prune(ctx, time.Now().Add(-controller.options.PruneAfter)); err != nil && ctx.Err() == nil {
				log.Printf("queue: prune failed: %v", err)
			}
		case <-ticker.C:
		}
	}
}
