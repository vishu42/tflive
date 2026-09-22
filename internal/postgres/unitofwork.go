package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/vishu42/tflive/internal/app"
	"github.com/vishu42/tflive/internal/authorization"
	"github.com/vishu42/tflive/internal/domain"
	"github.com/vishu42/tflive/internal/queue"
)

// txRepo is the transaction-scoped subset of Store handed to an InTx callback.
type txRepo struct {
	tx pgx.Tx
}

func (repo *txRepo) CreateStack(ctx context.Context, stack domain.Stack) error {
	return insertStack(ctx, repo.tx, stack)
}

func (repo *txRepo) AppendAuditEvent(ctx context.Context, event domain.SecurityAuditEvent) error {
	return appendAuditEvent(ctx, repo.tx, event)
}

func (repo *txRepo) CreateTemplateRun(ctx context.Context, run domain.TemplateRun) (int, error) {
	return createTemplateRun(ctx, repo.tx, run)
}

func (repo *txRepo) CreateTemplateRegistration(ctx context.Context, registration domain.TemplateRegistration) error {
	return createTemplateRegistration(ctx, repo.tx, registration)
}

func (repo *txRepo) ApproveTemplateRun(ctx context.Context, approval domain.TemplateRunApproval) error {
	return approveTemplateRun(ctx, repo.tx, approval)
}

func (repo *txRepo) DiscardTemplateRun(ctx context.Context, discard domain.TemplateRunDiscard) (bool, error) {
	return discardTemplateRun(ctx, repo.tx, discard)
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
// repository, a transaction-bound enqueuer, and a context carrying the
// transaction itself. Returning an error rolls back everything written under
// any of the three.
//
// The context is what extends the unit of work beyond this package. An
// authorization write made under it lands in this same transaction, so a stack
// row and the grant that makes it reachable commit together or not at all.
//
// fn takes a context rather than closing over the caller's on purpose: closing
// over it would compile, pass, and silently leave the tuple committing
// separately, which is the bug this signature exists to prevent.
func (store *Store) InTx(ctx context.Context, fn func(context.Context, app.TxRepo, queue.Enqueuer) error) error {
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin unit of work: %w", err)
	}
	defer tx.Rollback(ctx)

	if err := fn(authorization.WithTx(ctx, tx), &txRepo{tx: tx}, &txEnqueuer{tx: tx, specs: store.queueSpecs}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit unit of work: %w", err)
	}
	return nil
}
