package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/vishu42/megagega/internal/app"
	"github.com/vishu42/megagega/internal/traits"
)

func (store *Store) CreateTemplateRegistration(ctx context.Context, registration traits.TemplateRegistration) error {
	_, err := store.pool.Exec(ctx, `
		insert into template_registrations (
			id,
			tenant_id,
			repo_owner,
			repo_name,
			source_ref,
			root_path,
			status,
			template_id,
			resolved_commit_sha,
			requested_by,
			requested_at,
			completed_at,
			error_summary
		) values (
			$1, $2, $3, $4, $5, $6, $7,
			$8, $9, $10, $11, $12, $13
		)
	`,
		registration.ID,
		registration.TenantID,
		registration.RepoOwner,
		registration.RepoName,
		registration.SourceRef,
		registration.RootPath,
		registration.Status,
		registration.TemplateID,
		registration.ResolvedCommitSHA,
		registration.RequestedBy,
		registration.RequestedAt,
		nullTime(registration.CompletedAt),
		registration.ErrorSummary,
	)
	if err != nil {
		return fmt.Errorf("create template registration: %w", err)
	}
	return nil
}

func (store *Store) GetTemplateRegistration(ctx context.Context, tenantID traits.TenantID, id traits.TemplateRegistrationID) (traits.TemplateRegistration, error) {
	var registration traits.TemplateRegistration
	var completedAt sql.NullTime
	err := store.pool.QueryRow(ctx, `
		select
			id,
			tenant_id,
			repo_owner,
			repo_name,
			source_ref,
			root_path,
			status,
			template_id,
			resolved_commit_sha,
			requested_by,
			requested_at,
			completed_at,
			error_summary
		from template_registrations
		where tenant_id = $1
			and id = $2
	`, tenantID, id).Scan(
		&registration.ID,
		&registration.TenantID,
		&registration.RepoOwner,
		&registration.RepoName,
		&registration.SourceRef,
		&registration.RootPath,
		&registration.Status,
		&registration.TemplateID,
		&registration.ResolvedCommitSHA,
		&registration.RequestedBy,
		&registration.RequestedAt,
		&completedAt,
		&registration.ErrorSummary,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return traits.TemplateRegistration{}, app.ErrNotFound
	}
	if err != nil {
		return traits.TemplateRegistration{}, fmt.Errorf("get template registration: %w", err)
	}
	if completedAt.Valid {
		registration.CompletedAt = completedAt.Time
	}
	return registration, nil
}

func (store *Store) RecordTemplateRegistrationStatus(ctx context.Context, input traits.TemplateRegistrationStatusActivityInput) error {
	var err error
	if terminalTemplateRegistrationStatus(input.Status) {
		_, err = store.pool.Exec(ctx, `
			update template_registrations
			set
				status = $1,
				template_id = $2,
				resolved_commit_sha = $3,
				error_summary = $4,
				completed_at = coalesce(completed_at, now())
			where tenant_id = $5
				and id = $6
		`,
			input.Status,
			input.TemplateID,
			input.ResolvedCommitSHA,
			input.ErrorSummary,
			input.TenantID,
			input.RegistrationID,
		)
	} else {
		_, err = store.pool.Exec(ctx, `
			update template_registrations
			set
				status = $1,
				template_id = $2,
				resolved_commit_sha = $3,
				error_summary = $4
			where tenant_id = $5
				and id = $6
		`,
			input.Status,
			input.TemplateID,
			input.ResolvedCommitSHA,
			input.ErrorSummary,
			input.TenantID,
			input.RegistrationID,
		)
	}
	if err != nil {
		return fmt.Errorf("record template registration status: %w", err)
	}

	registration, err := store.GetTemplateRegistration(ctx, input.TenantID, input.RegistrationID)
	if err != nil {
		return err
	}
	if registration.ID == "" {
		return app.ErrNotFound
	}
	return nil
}

func (store *Store) UpsertTemplateWithVariables(ctx context.Context, template traits.Template, variables []traits.TemplateVariable) (traits.Template, error) {
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return traits.Template{}, fmt.Errorf("begin upsert template: %w", err)
	}
	defer tx.Rollback(ctx)

	tagsJSON, err := json.Marshal(template.Tags)
	if err != nil {
		return traits.Template{}, fmt.Errorf("marshal template tags: %w", err)
	}
	if tagsJSON == nil {
		tagsJSON = []byte("[]")
	}

	inserted, insertedNew, err := insertTemplate(ctx, tx, template, tagsJSON)
	if err != nil {
		return traits.Template{}, err
	}
	if !insertedNew {
		if err := tx.Commit(ctx); err != nil {
			return traits.Template{}, fmt.Errorf("commit reused template: %w", err)
		}
		return inserted, nil
	}

	for _, variable := range variables {
		if _, err := tx.Exec(ctx, `
			insert into template_variables (
				template_id,
				name,
				type_expression,
				description,
				required,
				has_default,
				sensitive,
				has_validation
			) values ($1, $2, $3, $4, $5, $6, $7, $8)
		`,
			inserted.ID,
			variable.Name,
			variable.TypeExpression,
			variable.Description,
			variable.Required,
			variable.HasDefault,
			variable.Sensitive,
			variable.HasValidation,
		); err != nil {
			return traits.Template{}, fmt.Errorf("insert template variable %q: %w", variable.Name, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return traits.Template{}, fmt.Errorf("commit upsert template: %w", err)
	}
	return inserted, nil
}

func (store *Store) GetTemplateVariables(ctx context.Context, tenantID traits.TenantID, templateID traits.TemplateID) ([]traits.TemplateVariable, error) {
	var exists bool
	if err := store.pool.QueryRow(ctx, `
		select exists (
			select 1
			from templates
			where tenant_id = $1
				and id = $2
		)
	`, tenantID, templateID).Scan(&exists); err != nil {
		return nil, fmt.Errorf("check template existence: %w", err)
	}
	if !exists {
		return nil, app.ErrNotFound
	}

	rows, err := store.pool.Query(ctx, `
		select
			template_id,
			name,
			type_expression,
			description,
			required,
			has_default,
			sensitive,
			has_validation
		from template_variables
		where template_id = $1
		order by name
	`, templateID)
	if err != nil {
		return nil, fmt.Errorf("get template variables: %w", err)
	}
	defer rows.Close()

	var variables []traits.TemplateVariable
	for rows.Next() {
		var variable traits.TemplateVariable
		if err := rows.Scan(
			&variable.TemplateID,
			&variable.Name,
			&variable.TypeExpression,
			&variable.Description,
			&variable.Required,
			&variable.HasDefault,
			&variable.Sensitive,
			&variable.HasValidation,
		); err != nil {
			return nil, fmt.Errorf("scan template variable: %w", err)
		}
		variables = append(variables, variable)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate template variables: %w", err)
	}
	return variables, nil
}

func (store *Store) CreateStack(ctx context.Context, stack traits.Stack) error {
	tagsJSON, err := json.Marshal(stack.Tags)
	if err != nil {
		return fmt.Errorf("marshal stack tags: %w", err)
	}
	if tagsJSON == nil {
		tagsJSON = []byte("{}")
	}

	credentialIDsJSON, err := json.Marshal(stack.DefaultCredentialIDs)
	if err != nil {
		return fmt.Errorf("marshal default credential IDs: %w", err)
	}
	if credentialIDsJSON == nil {
		credentialIDsJSON = []byte("[]")
	}

	_, err = store.pool.Exec(ctx, `
		insert into stacks (
			id,
			tenant_id,
			name,
			slug,
			tags_json,
			default_credential_ids_json,
			created_by,
			created_at
		) values ($1, $2, $3, $4, $5::jsonb, $6::jsonb, $7, $8)
	`,
		stack.ID,
		stack.TenantID,
		stack.Name,
		stack.Slug,
		tagsJSON,
		credentialIDsJSON,
		stack.CreatedBy,
		stack.CreatedAt,
	)
	if duplicateConstraint(err, "stacks_tenant_id_slug_idx") {
		return app.ErrDuplicateStackSlug
	}
	if err != nil {
		return fmt.Errorf("create stack: %w", err)
	}
	return nil
}

func (store *Store) GetStack(ctx context.Context, tenantID traits.TenantID, stackID traits.StackID) (traits.Stack, error) {
	stack, err := store.getStack(ctx, tenantID, stackID)
	if err != nil {
		return traits.Stack{}, err
	}
	return stack, nil
}

func (store *Store) GetStackWithTemplates(ctx context.Context, tenantID traits.TenantID, stackID traits.StackID) (app.StackView, error) {
	stack, err := store.getStack(ctx, tenantID, stackID)
	if err != nil {
		return app.StackView{}, err
	}

	rows, err := store.pool.Query(ctx, `
		select
			id,
			tenant_id,
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
			and stack_id = $2
		order by id
	`, tenantID, stackID)
	if err != nil {
		return app.StackView{}, fmt.Errorf("get stack templates: %w", err)
	}
	defer rows.Close()

	var templates []traits.StackTemplate
	for rows.Next() {
		stackTemplate, err := scanStackTemplate(rows)
		if err != nil {
			return app.StackView{}, err
		}
		templates = append(templates, stackTemplate)
	}
	if err := rows.Err(); err != nil {
		return app.StackView{}, fmt.Errorf("iterate stack templates: %w", err)
	}

	return app.StackView{Stack: stack, Templates: templates}, nil
}

func (store *Store) CreateStackTemplate(ctx context.Context, stackTemplate traits.StackTemplate) error {
	configJSON := stackTemplate.ConfigJSON
	if len(configJSON) == 0 {
		configJSON = json.RawMessage(`{}`)
	}

	result, err := store.pool.Exec(ctx, `
		insert into stack_templates (
			id,
			tenant_id,
			stack_id,
			template_id,
			selected_ref,
			workspace_name,
			config_json,
			last_applied_run_id,
			last_applied_ref,
			last_applied_at,
			lifecycle
		)
		select $1, $2, $3, $4, $5, $6, $7::jsonb, $8, $9, $10, $11
		where exists (
			select 1
			from stacks
			where tenant_id = $2
				and id = $3
		)
	`,
		stackTemplate.ID,
		stackTemplate.TenantID,
		stackTemplate.StackID,
		stackTemplate.TemplateID,
		stackTemplate.SelectedRef,
		stackTemplate.WorkspaceName,
		configJSON,
		stackTemplate.LastAppliedRunID,
		stackTemplate.LastAppliedRef,
		nullTime(stackTemplate.LastAppliedAt),
		stackTemplate.Lifecycle,
	)
	if err != nil {
		return fmt.Errorf("create stack template: %w", err)
	}
	if result.RowsAffected() == 0 {
		return app.ErrNotFound
	}
	return nil
}

func (store *Store) GetTemplate(ctx context.Context, tenantID traits.TenantID, templateID traits.TemplateID) (traits.Template, error) {
	var template traits.Template
	var tagsJSON []byte
	err := store.pool.QueryRow(ctx, `
		select
			id,
			tenant_id,
			repo_owner,
			repo_name,
			source_ref,
			resolved_commit_sha,
			root_path,
			name,
			description,
			tags_json,
			status,
			created_at
		from templates
		where tenant_id = $1
			and id = $2
	`, tenantID, templateID).Scan(
		&template.ID,
		&template.TenantID,
		&template.RepoOwner,
		&template.RepoName,
		&template.SourceRef,
		&template.ResolvedCommitSHA,
		&template.RootPath,
		&template.Name,
		&template.Description,
		&tagsJSON,
		&template.Status,
		&template.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return traits.Template{}, app.ErrNotFound
	}
	if err != nil {
		return traits.Template{}, fmt.Errorf("get template: %w", err)
	}
	if err := json.Unmarshal(tagsJSON, &template.Tags); err != nil {
		return traits.Template{}, fmt.Errorf("unmarshal template tags: %w", err)
	}
	return template, nil
}

func (store *Store) GetStackTemplate(ctx context.Context, tenantID traits.TenantID, id traits.StackTemplateID) (traits.StackTemplate, error) {
	row := store.pool.QueryRow(ctx, `
		select
			id,
			tenant_id,
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
	`, tenantID, id)
	stackTemplate, scanErr := scanStackTemplate(row)
	if errors.Is(scanErr, pgx.ErrNoRows) {
		return traits.StackTemplate{}, ErrNotFound
	}
	if scanErr != nil {
		return traits.StackTemplate{}, fmt.Errorf("get stack template: %w", scanErr)
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

type stackTemplateScanner interface {
	Scan(dest ...any) error
}

func scanStackTemplate(scanner stackTemplateScanner) (traits.StackTemplate, error) {
	var stackTemplate traits.StackTemplate
	var configJSON []byte
	var lastAppliedAt sql.NullTime

	if err := scanner.Scan(
		&stackTemplate.ID,
		&stackTemplate.TenantID,
		&stackTemplate.StackID,
		&stackTemplate.TemplateID,
		&stackTemplate.SelectedRef,
		&stackTemplate.WorkspaceName,
		&configJSON,
		&stackTemplate.LastAppliedRunID,
		&stackTemplate.LastAppliedRef,
		&lastAppliedAt,
		&stackTemplate.Lifecycle,
	); err != nil {
		return traits.StackTemplate{}, fmt.Errorf("scan stack template: %w", err)
	}
	stackTemplate.ConfigJSON = configJSON
	if lastAppliedAt.Valid {
		stackTemplate.LastAppliedAt = lastAppliedAt.Time
	}
	return stackTemplate, nil
}

func (store *Store) getStack(ctx context.Context, tenantID traits.TenantID, stackID traits.StackID) (traits.Stack, error) {
	var stack traits.Stack
	var tagsJSON []byte
	var credentialIDsJSON []byte

	err := store.pool.QueryRow(ctx, `
		select
			id,
			tenant_id,
			name,
			slug,
			tags_json,
			default_credential_ids_json,
			created_by,
			created_at
		from stacks
		where tenant_id = $1
			and id = $2
	`, tenantID, stackID).Scan(
		&stack.ID,
		&stack.TenantID,
		&stack.Name,
		&stack.Slug,
		&tagsJSON,
		&credentialIDsJSON,
		&stack.CreatedBy,
		&stack.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return traits.Stack{}, app.ErrNotFound
	}
	if err != nil {
		return traits.Stack{}, fmt.Errorf("get stack: %w", err)
	}
	if err := json.Unmarshal(tagsJSON, &stack.Tags); err != nil {
		return traits.Stack{}, fmt.Errorf("unmarshal stack tags: %w", err)
	}
	if err := json.Unmarshal(credentialIDsJSON, &stack.DefaultCredentialIDs); err != nil {
		return traits.Stack{}, fmt.Errorf("unmarshal stack credential IDs: %w", err)
	}
	return stack, nil
}

func duplicateConstraint(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == constraint
}

func insertTemplate(ctx context.Context, tx pgx.Tx, template traits.Template, tagsJSON []byte) (traits.Template, bool, error) {
	var inserted traits.Template
	var insertedTagsJSON []byte
	err := tx.QueryRow(ctx, `
		insert into templates (
			id,
			tenant_id,
			repo_owner,
			repo_name,
			source_ref,
			resolved_commit_sha,
			root_path,
			name,
			description,
			tags_json,
			status,
			created_at
		) values (
			$1, $2, $3, $4, $5, $6, $7,
			$8, $9, $10::jsonb, $11, coalesce($12::timestamptz, now())
		)
		on conflict (tenant_id, repo_owner, repo_name, root_path, resolved_commit_sha) do nothing
		returning
			id,
			tenant_id,
			repo_owner,
			repo_name,
			source_ref,
			resolved_commit_sha,
			root_path,
			name,
			description,
			tags_json,
			status,
			created_at
	`,
		template.ID,
		template.TenantID,
		template.RepoOwner,
		template.RepoName,
		template.SourceRef,
		template.ResolvedCommitSHA,
		template.RootPath,
		template.Name,
		template.Description,
		tagsJSON,
		template.Status,
		nullTime(template.CreatedAt),
	).Scan(
		&inserted.ID,
		&inserted.TenantID,
		&inserted.RepoOwner,
		&inserted.RepoName,
		&inserted.SourceRef,
		&inserted.ResolvedCommitSHA,
		&inserted.RootPath,
		&inserted.Name,
		&inserted.Description,
		&insertedTagsJSON,
		&inserted.Status,
		&inserted.CreatedAt,
	)
	if err == nil {
		if err := json.Unmarshal(insertedTagsJSON, &inserted.Tags); err != nil {
			return traits.Template{}, false, fmt.Errorf("unmarshal inserted template tags: %w", err)
		}
		return inserted, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return traits.Template{}, false, fmt.Errorf("insert template: %w", err)
	}

	selected, err := selectTemplateByIdentity(ctx, tx, template)
	if err != nil {
		return traits.Template{}, false, err
	}
	return selected, false, nil
}

func selectTemplateByIdentity(ctx context.Context, tx pgx.Tx, template traits.Template) (traits.Template, error) {
	var selected traits.Template
	var tagsJSON []byte
	err := tx.QueryRow(ctx, `
		select
			id,
			tenant_id,
			repo_owner,
			repo_name,
			source_ref,
			resolved_commit_sha,
			root_path,
			name,
			description,
			tags_json,
			status,
			created_at
		from templates
		where tenant_id = $1
			and repo_owner = $2
			and repo_name = $3
			and root_path = $4
			and resolved_commit_sha = $5
	`,
		template.TenantID,
		template.RepoOwner,
		template.RepoName,
		template.RootPath,
		template.ResolvedCommitSHA,
	).Scan(
		&selected.ID,
		&selected.TenantID,
		&selected.RepoOwner,
		&selected.RepoName,
		&selected.SourceRef,
		&selected.ResolvedCommitSHA,
		&selected.RootPath,
		&selected.Name,
		&selected.Description,
		&tagsJSON,
		&selected.Status,
		&selected.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return traits.Template{}, app.ErrNotFound
	}
	if err != nil {
		return traits.Template{}, fmt.Errorf("select template by identity: %w", err)
	}
	if err := json.Unmarshal(tagsJSON, &selected.Tags); err != nil {
		return traits.Template{}, fmt.Errorf("unmarshal selected template tags: %w", err)
	}
	return selected, nil
}

func terminalTemplateRegistrationStatus(status traits.TemplateRegistrationStatus) bool {
	switch status {
	case traits.TemplateRegistrationCompleted, traits.TemplateRegistrationInvalid, traits.TemplateRegistrationFailed:
		return true
	default:
		return false
	}
}

func nullTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}

	return value
}
