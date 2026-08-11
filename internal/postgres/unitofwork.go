package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/vishu42/tflive/internal/app"
	"github.com/vishu42/tflive/internal/queue"
	"github.com/vishu42/tflive/internal/traits"
)

// txRepo is the transaction-scoped subset of Store handed to an InTx callback.
type txRepo struct {
	tx pgx.Tx
}

func (repo *txRepo) CreateStack(ctx context.Context, stack traits.Stack) error {
	return insertStack(ctx, repo.tx, stack)
}

func (repo *txRepo) AppendAuditEvent(ctx context.Context, event traits.SecurityAuditEvent) error {
	return appendAuditEvent(ctx, repo.tx, event)
}

func (repo *txRepo) CreateTemplateRun(ctx context.Context, run traits.TemplateRun) error {
	return createTemplateRun(ctx, repo.tx, run)
}

func (repo *txRepo) CreateTemplateRegistration(ctx context.Context, registration traits.TemplateRegistration) error {
	return createTemplateRegistration(ctx, repo.tx, registration)
}

func (repo *txRepo) ApproveTemplateRun(ctx context.Context, approval traits.TemplateRunApproval) error {
	return approveTemplateRun(ctx, repo.tx, approval)
}

func (repo *txRepo) RequestTemplateRunCancellation(ctx context.Context, cancellation traits.TemplateRunCancellation) error {
	return requestTemplateRunCancellation(ctx, repo.tx, cancellation)
}

// txEnqueuer enqueues inside the caller's transaction. This is the entire
// reason the queue is an outbox rather than a message broker: the intent and
// the domain write commit or roll back together, so a crash can never leave
// state written with its intent lost.
type txEnqueuer struct {
	tx    pgx.Tx
	specs *queue.SpecRegistry
}

func (enqueuer *txEnqueuer) Enqueue(ctx context.Context, requests ...queue.Request) error {
	return enqueueRequests(ctx, enqueuer.tx, enqueuer.specs, requests...)
}

// InTx runs fn inside one transaction, giving it a transaction-scoped
// repository and a transaction-bound enqueuer. Returning an error rolls back
// both the domain write and the queued intent.
func (store *Store) InTx(ctx context.Context, fn func(app.TxRepo, queue.Enqueuer) error) error {
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin unit of work: %w", err)
	}
	defer tx.Rollback(ctx)

	if err := fn(&txRepo{tx: tx}, &txEnqueuer{tx: tx, specs: store.queueSpecs}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit unit of work: %w", err)
	}
	return nil
}
