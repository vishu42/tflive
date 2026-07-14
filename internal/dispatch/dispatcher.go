package dispatch

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/vishu42/tflive/internal/traits"
)

const (
	defaultPollInterval  = time.Second
	defaultLeaseDuration = 30 * time.Second
	defaultRetryDelay    = 5 * time.Second
)

type Entry struct {
	ID    string
	Input traits.TemplateRunWorkflowInput
}

type Outbox interface {
	ClaimTemplateRun(context.Context, time.Time, time.Time) (Entry, bool, error)
	CompleteTemplateRun(context.Context, string) error
	RetryTemplateRun(context.Context, string, time.Time, string) error
}

type WorkflowStarter interface {
	StartTemplateRun(context.Context, traits.TemplateRunWorkflowInput) error
}

type Options struct {
	PollInterval  time.Duration
	LeaseDuration time.Duration
	RetryDelay    time.Duration
}

type Dispatcher struct {
	outbox        Outbox
	workflows     WorkflowStarter
	pollInterval  time.Duration
	leaseDuration time.Duration
	retryDelay    time.Duration
}

func NewDispatcher(outbox Outbox, workflows WorkflowStarter, options Options) *Dispatcher {
	if options.PollInterval <= 0 {
		options.PollInterval = defaultPollInterval
	}
	if options.LeaseDuration <= 0 {
		options.LeaseDuration = defaultLeaseDuration
	}
	if options.RetryDelay <= 0 {
		options.RetryDelay = defaultRetryDelay
	}
	return &Dispatcher{
		outbox:        outbox,
		workflows:     workflows,
		pollInterval:  options.PollInterval,
		leaseDuration: options.LeaseDuration,
		retryDelay:    options.RetryDelay,
	}
}

// DispatchOnce leases and processes at most one pending workflow start.
func (dispatcher *Dispatcher) DispatchOnce(ctx context.Context, now time.Time) (bool, error) {
	entry, found, err := dispatcher.outbox.ClaimTemplateRun(ctx, now, now.Add(dispatcher.leaseDuration))
	if err != nil {
		return false, fmt.Errorf("claim template run outbox entry: %w", err)
	}
	if !found {
		return false, nil
	}

	if err := dispatcher.workflows.StartTemplateRun(ctx, entry.Input); err != nil {
		if retryErr := dispatcher.outbox.RetryTemplateRun(ctx, entry.ID, now.Add(dispatcher.retryDelay), err.Error()); retryErr != nil {
			return true, fmt.Errorf("start template run workflow: %w (record retry: %v)", err, retryErr)
		}
		return true, fmt.Errorf("start template run workflow: %w", err)
	}

	if err := dispatcher.outbox.CompleteTemplateRun(ctx, entry.ID); err != nil {
		return true, fmt.Errorf("complete template run outbox entry: %w", err)
	}
	return true, nil
}

// Run continuously drains the outbox until its context is cancelled.
func (dispatcher *Dispatcher) Run(ctx context.Context) {
	ticker := time.NewTicker(dispatcher.pollInterval)
	defer ticker.Stop()

	for {
		dispatched, err := dispatcher.DispatchOnce(ctx, time.Now())
		if err != nil && ctx.Err() == nil {
			log.Printf("workflow outbox dispatch failed: %v", err)
		}
		if dispatched && err == nil {
			continue
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
