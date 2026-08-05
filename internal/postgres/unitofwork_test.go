package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/vishu42/tflive/internal/app"
	"github.com/vishu42/tflive/internal/queue"
	"github.com/vishu42/tflive/internal/traits"
)

var _ app.UnitOfWork = (*Store)(nil)

func TestInTxCommitsDomainWriteAndIntentTogether(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, pool := newQueueStore(t, ctx)

	stack := traits.Stack{
		ID:        traits.StackID("stack_intx_ok"),
		TenantID:  traits.TenantID("t1"),
		Name:      "intx",
		Slug:      "intx",
		CreatedBy: traits.UserID("user:me"),
		CreatedAt: time.Now().UTC(),
	}

	err := store.InTx(ctx, func(repo app.TxRepo, enqueuer queue.Enqueuer) error {
		if err := repo.CreateStack(ctx, stack); err != nil {
			return err
		}
		return enqueuer.Enqueue(ctx, queue.Request{
			Kind:         "reconcile",
			Payload:      json.RawMessage(`{"key":"stack:stack_intx_ok/user:me"}`),
			ActorSubject: "user:me",
			TenantID:     "t1",
		})
	})
	if err != nil {
		t.Fatalf("InTx returned error: %v", err)
	}

	var stacks, intents int
	if err := pool.QueryRow(ctx, `select
		(select count(*) from stacks where id = $1),
		(select count(*) from work_queue where ordering_key = $2)
	`, string(stack.ID), "stack:stack_intx_ok/user:me").Scan(&stacks, &intents); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if stacks != 1 || intents != 1 {
		t.Fatalf("stacks = %d, intents = %d, want 1 and 1", stacks, intents)
	}
}

func TestInTxRollsBackBothOnError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, pool := newQueueStore(t, ctx)

	stack := traits.Stack{
		ID:        traits.StackID("stack_intx_rollback"),
		TenantID:  traits.TenantID("t1"),
		Name:      "rollback",
		Slug:      "rollback",
		CreatedBy: traits.UserID("user:me"),
		CreatedAt: time.Now().UTC(),
	}
	sentinel := errors.New("caller failed after enqueue")

	err := store.InTx(ctx, func(repo app.TxRepo, enqueuer queue.Enqueuer) error {
		if err := repo.CreateStack(ctx, stack); err != nil {
			return err
		}
		if err := enqueuer.Enqueue(ctx, queue.Request{
			Kind:     "reconcile",
			Payload:  json.RawMessage(`{"key":"stack:stack_intx_rollback/user:me"}`),
			TenantID: "t1",
		}); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("InTx error = %v, want the caller's sentinel", err)
	}

	var stacks, intents int
	if err := pool.QueryRow(ctx, `select
		(select count(*) from stacks where id = $1),
		(select count(*) from work_queue where ordering_key = $2)
	`, string(stack.ID), "stack:stack_intx_rollback/user:me").Scan(&stacks, &intents); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if stacks != 0 || intents != 0 {
		t.Fatalf("stacks = %d, intents = %d, want 0 and 0 — rollback must leave no orphan intent", stacks, intents)
	}
}

func TestInTxWritesAuditEventTransactionally(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, pool := newQueueStore(t, ctx)

	err := store.InTx(ctx, func(repo app.TxRepo, enqueuer queue.Enqueuer) error {
		if err := repo.AppendAuditEvent(ctx, traits.SecurityAuditEvent{
			ActorSubject: "user:me",
			Action:       traits.AuditActionGrant,
			TargetUser:   "user:them",
			TenantID:     traits.TenantID("t1"),
			StackID:      traits.StackID("stack_audit"),
			NewRole:      "operator",
			Outcome:      traits.AuditOutcomeSuccess,
		}); err != nil {
			return err
		}
		return enqueuer.Enqueue(ctx, queue.Request{
			Kind:     "reconcile",
			Payload:  json.RawMessage(`{"key":"stack:stack_audit/user:them"}`),
			TenantID: "t1",
		})
	})
	if err != nil {
		t.Fatalf("InTx returned error: %v", err)
	}

	var audits, intents int
	if err := pool.QueryRow(ctx, `select
		(select count(*) from security_audit_log where stack_id = $1),
		(select count(*) from work_queue where ordering_key = $2)
	`, "stack_audit", "stack:stack_audit/user:them").Scan(&audits, &intents); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if audits != 1 || intents != 1 {
		t.Fatalf("audits = %d, intents = %d, want 1 and 1", audits, intents)
	}
}
