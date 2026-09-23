package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/vishu42/tflive/internal/domain"
)

// RecordTemplateRunStep records the step a running run has started. Only a
// running run has a step to start, so any other run is not found.
func (store *Store) RecordTemplateRunStep(ctx context.Context, input domain.TemplateRunStepActivityInput) error {
	if !input.Step.Valid() {
		return fmt.Errorf("record template run step: unknown step %q", input.Step)
	}
	commandTag, err := store.pool.Exec(ctx, `
		update template_runs
		set step = $1
		where tenant_id = $2
			and id = $3
			and status = $4
	`, input.Step, input.TenantID, input.RunID, domain.TemplateRunRunning)
	if err != nil {
		return fmt.Errorf("record template run step: %w", err)
	}
	if commandTag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// templateRunEventOperations is the one kind of run each event belongs to.
var templateRunEventOperations = map[domain.TemplateRunEvent]domain.OperationType{
	domain.TemplateRunApplied:    domain.OperationApply,
	domain.TemplateRunDestroying: domain.OperationDestroy,
	domain.TemplateRunDestroyed:  domain.OperationDestroy,
}

// RecordTemplateRunEvent records something a running run did to its stack
// template, together with any counts the event carries, in one transaction
// with the run row locked. A run that is not running, or is not this stack
// template's run of this operation, is not found.
func (store *Store) RecordTemplateRunEvent(ctx context.Context, input domain.TemplateRunEventActivityInput) error {
	operation, ok := templateRunEventOperations[input.Event]
	if !ok {
		return fmt.Errorf("record template run event: unknown event %q", input.Event)
	}
	if input.Operation != operation {
		return fmt.Errorf("record template run event: %q is not an event of a %s run", input.Event, input.Operation)
	}

	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin record template run event: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var runID domain.TemplateRunID
	err = tx.QueryRow(ctx, `
		select id
		from template_runs
		where tenant_id = $1
			and id = $2
			and stack_template_id = $3
			and operation = $4
			and status = $5
		for update
	`, input.TenantID, input.RunID, input.StackTemplateID, input.Operation, domain.TemplateRunRunning).Scan(&runID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("read template run for event: %w", err)
	}

	if input.Summary != nil {
		if _, err := tx.Exec(ctx, `
			update template_runs
			set plan_add = $1, plan_change = $2, plan_destroy = $3
			where tenant_id = $4 and id = $5
		`, input.Summary.Add, input.Summary.Change, input.Summary.Destroy, input.TenantID, input.RunID); err != nil {
			return fmt.Errorf("record template run event counts: %w", err)
		}
	}

	switch input.Event {
	case domain.TemplateRunApplied:
		err = recordStackTemplateLastApplied(ctx, tx, input.TenantID, input.StackTemplateID, input.RunID, domain.TemplateRunRunning)
	case domain.TemplateRunDestroying:
		err = recordStackTemplateLifecycle(ctx, tx, input.TenantID, input.StackTemplateID, domain.StackTemplateDestroying)
	case domain.TemplateRunDestroyed:
		err = recordStackTemplateLifecycle(ctx, tx, input.TenantID, input.StackTemplateID, domain.StackTemplateDestroyed)
	}
	if err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit record template run event: %w", err)
	}
	return nil
}
