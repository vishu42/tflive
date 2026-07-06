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

func (store *Store) GetTemplateRun(ctx context.Context, tenantID traits.TenantID, runID traits.TemplateRunID) (traits.TemplateRun, error) {
	var run traits.TemplateRun
	var startedAt sql.NullTime
	var completedAt sql.NullTime

	err := store.pool.QueryRow(ctx, `
		select
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
		from template_runs
		where tenant_id = $1
			and id = $2
	`, tenantID, runID).Scan(
		&run.ID,
		&run.TenantID,
		&run.StackTemplateID,
		&run.Operation,
		&run.SelectedRef,
		&run.ResolvedCommitSHA,
		&run.WorkspaceName,
		&run.BackendType,
		&run.BackendConfigHash,
		&run.Status,
		&run.TriggerActor,
		&startedAt,
		&completedAt,
		&run.ErrorSummary,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return traits.TemplateRun{}, app.ErrNotFound
	}
	if err != nil {
		return traits.TemplateRun{}, fmt.Errorf("get template run: %w", err)
	}

	if startedAt.Valid {
		run.StartedAt = startedAt.Time
	}
	if completedAt.Valid {
		run.CompletedAt = completedAt.Time
	}

	return run, nil
}

func (store *Store) RecordTemplateRunLog(ctx context.Context, log traits.TemplateRunLog) error {
	result, err := store.pool.Exec(ctx, `
		insert into template_run_logs (
			tenant_id,
			run_id,
			phase,
			object_key,
			content_type,
			size_bytes,
			uploaded_at
		)
		select $1, $2, $3, $4, $5, $6, $7
		where exists (
			select 1
			from template_runs
			where tenant_id = $1
				and id = $2
		)
		on conflict (tenant_id, run_id, phase) do update set
			object_key = excluded.object_key,
			content_type = excluded.content_type,
			size_bytes = excluded.size_bytes,
			uploaded_at = excluded.uploaded_at
	`,
		log.TenantID,
		log.RunID,
		log.Phase,
		log.ObjectKey,
		log.ContentType,
		log.SizeBytes,
		log.UploadedAt,
	)
	if err != nil {
		return fmt.Errorf("record template run log: %w", err)
	}
	if result.RowsAffected() == 0 {
		return app.ErrNotFound
	}
	return nil
}

func (store *Store) GetTemplateRunLog(ctx context.Context, tenantID traits.TenantID, runID traits.TemplateRunID, phase string) (traits.TemplateRunLog, error) {
	var log traits.TemplateRunLog
	err := store.pool.QueryRow(ctx, `
		select
			tenant_id,
			run_id,
			phase,
			object_key,
			content_type,
			size_bytes,
			uploaded_at
		from template_run_logs
		where tenant_id = $1
			and run_id = $2
			and phase = $3
	`, tenantID, runID, phase).Scan(
		&log.TenantID,
		&log.RunID,
		&log.Phase,
		&log.ObjectKey,
		&log.ContentType,
		&log.SizeBytes,
		&log.UploadedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return traits.TemplateRunLog{}, app.ErrNotFound
	}
	if err != nil {
		return traits.TemplateRunLog{}, fmt.Errorf("get template run log: %w", err)
	}
	return log, nil
}

func (store *Store) ListTemplateRunLogs(ctx context.Context, tenantID traits.TenantID, runID traits.TemplateRunID) ([]traits.TemplateRunLog, error) {
	rows, err := store.pool.Query(ctx, `
		select
			tenant_id,
			run_id,
			phase,
			object_key,
			content_type,
			size_bytes,
			uploaded_at
		from template_run_logs
		where tenant_id = $1
			and run_id = $2
		order by phase
	`, tenantID, runID)
	if err != nil {
		return nil, fmt.Errorf("list template run logs: %w", err)
	}
	defer rows.Close()

	var logs []traits.TemplateRunLog
	for rows.Next() {
		var log traits.TemplateRunLog
		if err := rows.Scan(
			&log.TenantID,
			&log.RunID,
			&log.Phase,
			&log.ObjectKey,
			&log.ContentType,
			&log.SizeBytes,
			&log.UploadedAt,
		); err != nil {
			return nil, fmt.Errorf("scan template run log: %w", err)
		}
		logs = append(logs, log)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate template run logs: %w", err)
	}

	return logs, nil
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
