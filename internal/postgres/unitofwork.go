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

// txEnqueuer enqueues inside the caller's transaction. This is the entire
// reason the queue is an outbox rather than a message broker: the intent and
// the domain write commit or roll back together, so a crash can never leave
// state written with its intent lost.
type txEnqueuer struct {
	tx       pgx.Tx
	registry *queue.Registry
}

func (enqueuer *txEnqueuer) Enqueue(ctx context.Context, requests ...queue.Request) error {
	return enqueueRequests(ctx, enqueuer.tx, enqueuer.registry, requests...)
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

	if err := fn(&txRepo{tx: tx}, &txEnqueuer{tx: tx, registry: store.queueRegistry}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit unit of work: %w", err)
	}
	return nil
}
