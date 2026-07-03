package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/vishu42/megagega/internal/app"
	"github.com/vishu42/megagega/internal/traits"
)

func (store *Store) GetStackTemplate(ctx context.Context, tenantID traits.TenantID, id traits.StackTemplateID) (traits.StackTemplate, error) {
	var stackTemplate traits.StackTemplate
	var configJSON []byte
	var lastAppliedAt sql.NullTime

	err := store.pool.QueryRow(ctx, `
		select
			id,
			stack_id,
			template_id,
			selected_ref,
			workspace_name,
			config_json,
			last_applied_run_id,
			last_applied_ref,
			last_applied_at,
			lifecycle
		from stack_templates
		where tenant_id = $1
			and id = $2
	`, tenantID, id).Scan(
		&stackTemplate.ID,
		&stackTemplate.StackID,
		&stackTemplate.TemplateID,
		&stackTemplate.SelectedRef,
		&stackTemplate.WorkspaceName,
		&configJSON,
		&stackTemplate.LastAppliedRunID,
		&stackTemplate.LastAppliedRef,
		&lastAppliedAt,
		&stackTemplate.Lifecycle,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return traits.StackTemplate{}, ErrNotFound
	}
	if err != nil {
		return traits.StackTemplate{}, fmt.Errorf("get stack template: %w", err)
	}

	stackTemplate.ConfigJSON = configJSON
	if lastAppliedAt.Valid {
		stackTemplate.LastAppliedAt = lastAppliedAt.Time
	}

	return stackTemplate, nil
}

func (store *Store) CreateTemplateRun(ctx context.Context, run traits.TemplateRun) error {
	_, err := store.pool.Exec(ctx, `
		insert into template_runs (
			id,
			tenant_id,
			stack_template_id,
			operation,
			selected_ref,
			resolved_commit_sha,
			workspace_name,
			backend_type,
			backend_config_hash,
			status,
			trigger_actor,
			started_at,
			completed_at,
			error_summary
		) values (
			$1, $2, $3, $4, $5, $6, $7,
			$8, $9, $10, $11, $12, $13, $14
		)
	`,
		run.ID,
		run.TenantID,
		run.StackTemplateID,
		run.Operation,
		run.SelectedRef,
		run.ResolvedCommitSHA,
		run.WorkspaceName,
		run.BackendType,
		run.BackendConfigHash,
		run.Status,
		run.TriggerActor,
		nullTime(run.StartedAt),
		nullTime(run.CompletedAt),
		run.ErrorSummary,
	)
	if err != nil {
		return fmt.Errorf("create template run: %w", err)
	}

	return nil
}

func (store *Store) ApproveTemplateRun(ctx context.Context, approval traits.TemplateRunApproval) error {
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin approve template run: %w", err)
	}
	defer tx.Rollback(ctx)

	commandTag, err := tx.Exec(ctx, `
		update template_runs
		set status = $1
		where tenant_id = $2
			and id = $3
			and status = $4
	`,
		traits.TemplateRunApproved,
		approval.TenantID,
		approval.RunID,
		traits.TemplateRunWaitingApproval,
	)
	if err != nil {
		return fmt.Errorf("approve template run status: %w", err)
	}
	if commandTag.RowsAffected() == 0 {
		return app.ErrRunNotApprovable
	}

	if _, err := tx.Exec(ctx, `
		insert into template_run_approvals (
			run_id,
			tenant_id,
			approved_by,
			approved_at
		) values ($1, $2, $3, $4)
	`,
		approval.RunID,
		approval.TenantID,
		approval.ApprovedBy,
		approval.ApprovedAt,
	); err != nil {
		return fmt.Errorf("insert template run approval: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit approve template run: %w", err)
	}

	return nil
}

func (store *Store) RequestTemplateRunCancellation(ctx context.Context, cancellation traits.TemplateRunCancellation) error {
	// TODO: Revisit cancellation eligibility. This currently allows every
	// non-terminal status, including post-action cleanup states such as applied,
	// destroyed, and lock_released, to move back to cancel_requested.
	commandTag, err := store.pool.Exec(ctx, `
		update template_runs
		set
			status = $1,
			cancellation_requested_by = $2,
			cancellation_reason = $3,
			cancellation_requested_at = $4
		where tenant_id = $5
			and id = $6
			and status not in ($7, $8, $9)
	`,
		traits.TemplateRunCancelRequested,
		cancellation.RequestedBy,
		cancellation.Reason,
		cancellation.RequestedAt,
		cancellation.TenantID,
		cancellation.RunID,
		traits.TemplateRunCompleted,
		traits.TemplateRunFailed,
		traits.TemplateRunCanceled,
	)
	if err != nil {
		return fmt.Errorf("request template run cancellation: %w", err)
	}
	if commandTag.RowsAffected() == 0 {
		return app.ErrRunNotCancelable
	}

	return nil
}

func (store *Store) RecordTemplateRunStatus(ctx context.Context, input traits.TemplateRunStatusActivityInput) error {
	var updatedRunID traits.TemplateRunID
	var err error

	if input.Status.Terminal() {
		err = store.pool.QueryRow(ctx, `
			update template_runs
			set
				status = $1,
				completed_at = coalesce(completed_at, now())
			where tenant_id = $2
				and id = $3
				and stack_template_id = $4
				and operation = $5
			returning id
		`,
			input.Status,
			input.TenantID,
			input.RunID,
			input.StackTemplateID,
			input.Operation,
		).Scan(&updatedRunID)
	} else {
		err = store.pool.QueryRow(ctx, `
			update template_runs
			set status = $1
			where tenant_id = $2
				and id = $3
				and stack_template_id = $4
				and operation = $5
			returning id
		`,
			input.Status,
			input.TenantID,
			input.RunID,
			input.StackTemplateID,
			input.Operation,
		).Scan(&updatedRunID)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("record template run status: %w", err)
	}

	return nil
}

func nullTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}

	return value
}
