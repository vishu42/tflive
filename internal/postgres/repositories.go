package postgres

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/vishu42/tflive/internal/app"
	"github.com/vishu42/tflive/internal/authdispatch"
	"github.com/vishu42/tflive/internal/authz"
	"github.com/vishu42/tflive/internal/dispatch"
	"github.com/vishu42/tflive/internal/traits"
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
			template_revision_id,
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
		registration.TemplateRevisionID,
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
			template_revision_id,
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
		&registration.TemplateRevisionID,
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
				template_revision_id = $2,
				resolved_commit_sha = $3,
				error_summary = $4,
				completed_at = coalesce(completed_at, now())
			where tenant_id = $5
				and id = $6
		`,
			input.Status,
			input.TemplateRevisionID,
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
				template_revision_id = $2,
				resolved_commit_sha = $3,
				error_summary = $4
			where tenant_id = $5
				and id = $6
		`,
			input.Status,
			input.TemplateRevisionID,
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

func (store *Store) UpsertTemplateRevisionWithVariables(ctx context.Context, templateRevision traits.TemplateRevision, variables []traits.TemplateVariable) (traits.TemplateRevision, error) {
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return traits.TemplateRevision{}, fmt.Errorf("begin upsert template revision: %w", err)
	}
	defer tx.Rollback(ctx)

	tagsJSON, err := json.Marshal(templateRevision.Tags)
	if err != nil {
		return traits.TemplateRevision{}, fmt.Errorf("marshal template revision tags: %w", err)
	}
	if tagsJSON == nil {
		tagsJSON = []byte("[]")
	}

	sourceTemplateID, err := upsertSourceTemplate(ctx, tx, templateRevision)
	if err != nil {
		return traits.TemplateRevision{}, err
	}
	templateRevision.SourceTemplateID = sourceTemplateID

	inserted, insertedNew, err := insertTemplateRevision(ctx, tx, templateRevision, tagsJSON)
	if err != nil {
		return traits.TemplateRevision{}, err
	}
	if err := recordLatestTemplateRevision(ctx, tx, inserted); err != nil {
		return traits.TemplateRevision{}, err
	}
	if !insertedNew {
		if err := tx.Commit(ctx); err != nil {
			return traits.TemplateRevision{}, fmt.Errorf("commit reused template revision: %w", err)
		}
		return inserted, nil
	}

	for _, variable := range variables {
		if _, err := tx.Exec(ctx, `
			insert into template_variables (
				template_revision_id,
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
			return traits.TemplateRevision{}, fmt.Errorf("insert template revision variable %q: %w", variable.Name, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return traits.TemplateRevision{}, fmt.Errorf("commit upsert template revision: %w", err)
	}
	return inserted, nil
}

func (store *Store) GetTemplateRevisionVariables(ctx context.Context, tenantID traits.TenantID, templateRevisionID traits.TemplateRevisionID) ([]traits.TemplateVariable, error) {
	var exists bool
	if err := store.pool.QueryRow(ctx, `
		select exists (
			select 1
			from template_revisions
			where tenant_id = $1
				and id = $2
		)
	`, tenantID, templateRevisionID).Scan(&exists); err != nil {
		return nil, fmt.Errorf("check template revision existence: %w", err)
	}
	if !exists {
		return nil, app.ErrNotFound
	}

	rows, err := store.pool.Query(ctx, `
		select
			template_revision_id,
			name,
			type_expression,
			description,
			required,
			has_default,
			sensitive,
			has_validation
		from template_variables
		where template_revision_id = $1
		order by name
	`, templateRevisionID)
	if err != nil {
		return nil, fmt.Errorf("get template revision variables: %w", err)
	}
	defer rows.Close()

	var variables []traits.TemplateVariable
	for rows.Next() {
		var variable traits.TemplateVariable
		if err := rows.Scan(
			&variable.TemplateRevisionID,
			&variable.Name,
			&variable.TypeExpression,
			&variable.Description,
			&variable.Required,
			&variable.HasDefault,
			&variable.Sensitive,
			&variable.HasValidation,
		); err != nil {
			return nil, fmt.Errorf("scan template revision variable: %w", err)
		}
		variables = append(variables, variable)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate template revision variables: %w", err)
	}
	return variables, nil
}

func (store *Store) ListTemplateRevisions(ctx context.Context, tenantID traits.TenantID) ([]traits.TemplateRevision, error) {
	rows, err := store.pool.Query(ctx, `
		select
			id,
			tenant_id,
			source_template_id,
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
		from template_revisions
		where tenant_id = $1
		order by created_at desc, id desc
	`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list template revisions: %w", err)
	}
	defer rows.Close()

	var templateRevisions []traits.TemplateRevision
	for rows.Next() {
		templateRevision, err := scanTemplateRevision(rows)
		if err != nil {
			return nil, err
		}
		templateRevisions = append(templateRevisions, templateRevision)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate template revisions: %w", err)
	}
	return templateRevisions, nil
}

func (store *Store) CreateStackWithOwnerIntent(ctx context.Context, stack traits.Stack, grant authz.Grant) error {
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin create stack: %w", err)
	}
	defer tx.Rollback(ctx)
	if err := insertStack(ctx, tx, stack); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		insert into authorization_outbox (id, operation, subject, stack, role)
		values ($1, 'grant', $2, $3, $4)
	`, authorizationOutboxID("grant", grant), grant.Subject().String(), grant.Stack().String(), grant.Role().String())
	if err != nil {
		return fmt.Errorf("enqueue stack owner relationship: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit create stack: %w", err)
	}
	return nil
}

func (store *Store) CreateStack(ctx context.Context, stack traits.Stack) error {
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin create stack: %w", err)
	}
	defer tx.Rollback(ctx)
	if err := insertStack(ctx, tx, stack); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit create stack: %w", err)
	}
	return nil
}

func insertStack(ctx context.Context, tx pgx.Tx, stack traits.Stack) error {
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

	_, err = tx.Exec(ctx, `
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

func authorizationOutboxID(operation string, grant authz.Grant) string {
	return operation + "/" + grant.Subject().String() + "/" + grant.Stack().String() + "/" + grant.Role().String()
}

func (store *Store) GetStack(ctx context.Context, tenantID traits.TenantID, stackID traits.StackID) (traits.Stack, error) {
	stack, err := store.getStack(ctx, tenantID, stackID)
	if err != nil {
		return traits.Stack{}, err
	}
	return stack, nil
}

func (store *Store) ListStacks(ctx context.Context, tenantID traits.TenantID) ([]traits.Stack, error) {
	rows, err := store.pool.Query(ctx, `
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
		order by created_at desc, id desc
	`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list stacks: %w", err)
	}
	defer rows.Close()

	var stacks []traits.Stack
	for rows.Next() {
		stack, err := scanStack(rows)
		if err != nil {
			return nil, err
		}
		stacks = append(stacks, stack)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate stacks: %w", err)
	}
	return stacks, nil
}

func (store *Store) ListStacksPage(ctx context.Context, tenantID traits.TenantID, after *app.StackPageCursor, limit int) ([]traits.Stack, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("stack page limit must be positive")
	}
	var afterCreatedAt any
	var afterID traits.StackID
	if after != nil {
		afterCreatedAt = after.CreatedAt
		afterID = after.ID
	}
	rows, err := store.pool.Query(ctx, `
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
			and ($2::timestamptz is null or (created_at, id) < ($2, $3))
		order by created_at desc, id desc
		limit $4
	`, tenantID, afterCreatedAt, afterID, limit)
	if err != nil {
		return nil, fmt.Errorf("list stack page: %w", err)
	}
	defer rows.Close()

	stacks := make([]traits.Stack, 0, limit)
	for rows.Next() {
		stack, err := scanStack(rows)
		if err != nil {
			return nil, err
		}
		stacks = append(stacks, stack)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate stack page: %w", err)
	}
	return stacks, nil
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
			component_key,
			source_template_id,
			desired_template_revision_id,
			last_applied_template_revision_id,
			selected_ref,
			workspace_name,
			config_json,
			desired_config_json,
			last_applied_run_id,
			last_applied_ref,
			last_applied_at,
			created_by,
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
	desiredConfigJSON := stackTemplate.DesiredConfigJSON
	if len(desiredConfigJSON) == 0 {
		desiredConfigJSON = configJSON
	}
	componentKey := strings.TrimSpace(stackTemplate.ComponentKey)
	if componentKey == "" {
		componentKey = string(stackTemplate.ID)
	}
	desiredTemplateRevisionID := stackTemplate.DesiredTemplateRevisionID

	result, err := store.pool.Exec(ctx, `
		insert into stack_templates (
			id,
			tenant_id,
			stack_id,
			component_key,
			source_template_id,
			desired_template_revision_id,
			last_applied_template_revision_id,
			selected_ref,
			workspace_name,
			config_json,
			desired_config_json,
			last_applied_run_id,
			last_applied_ref,
			last_applied_at,
			created_by,
			lifecycle
		)
		select $1, $2, $3, $4, $5, $6, $7, $8, $9, $10::jsonb, $11::jsonb, $12, $13, $14, $15, $16
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
		componentKey,
		stackTemplate.SourceTemplateID,
		desiredTemplateRevisionID,
		stackTemplate.LastAppliedTemplateRevisionID,
		stackTemplate.SelectedRef,
		stackTemplate.WorkspaceName,
		configJSON,
		desiredConfigJSON,
		stackTemplate.LastAppliedRunID,
		stackTemplate.LastAppliedRef,
		nullTime(stackTemplate.LastAppliedAt),
		stackTemplate.CreatedBy,
		stackTemplate.Lifecycle,
	)
	if err != nil {
		if duplicateConstraint(err, "stack_templates_active_component_key_idx") {
			return app.ErrDuplicateStackTemplateComponentKey
		}
		return fmt.Errorf("create stack template: %w", err)
	}
	if result.RowsAffected() == 0 {
		return app.ErrNotFound
	}
	return nil
}

func (store *Store) GetTemplateRevision(ctx context.Context, tenantID traits.TenantID, templateRevisionID traits.TemplateRevisionID) (traits.TemplateRevision, error) {
	row := store.pool.QueryRow(ctx, `
		select
			id,
			tenant_id,
			source_template_id,
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
		from template_revisions
		where tenant_id = $1
			and id = $2
	`, tenantID, templateRevisionID)
	templateRevision, err := scanTemplateRevision(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return traits.TemplateRevision{}, app.ErrNotFound
	}
	if err != nil {
		return traits.TemplateRevision{}, fmt.Errorf("get template revision: %w", err)
	}
	return templateRevision, nil
}

func (store *Store) GetStackTemplate(ctx context.Context, tenantID traits.TenantID, id traits.StackTemplateID) (traits.StackTemplate, error) {
	row := store.pool.QueryRow(ctx, `
		select
			id,
			tenant_id,
			stack_id,
			component_key,
			source_template_id,
			desired_template_revision_id,
			last_applied_template_revision_id,
			selected_ref,
			workspace_name,
			config_json,
			desired_config_json,
			last_applied_run_id,
			last_applied_ref,
			last_applied_at,
			created_by,
			lifecycle
		from stack_templates
		where tenant_id = $1
			and id = $2
	`, tenantID, id)
	stackTemplate, scanErr := scanStackTemplate(row)
	if errors.Is(scanErr, pgx.ErrNoRows) {
		return traits.StackTemplate{}, app.ErrNotFound
	}
	if scanErr != nil {
		return traits.StackTemplate{}, fmt.Errorf("get stack template: %w", scanErr)
	}
	return stackTemplate, nil
}

func (store *Store) UpdateStackTemplateConfig(ctx context.Context, tenantID traits.TenantID, id traits.StackTemplateID, configJSON json.RawMessage) (traits.StackTemplate, error) {
	row := store.pool.QueryRow(ctx, `
		update stack_templates
		set desired_config_json = $1::jsonb
		where tenant_id = $2
			and id = $3
		returning
			id,
			tenant_id,
			stack_id,
			component_key,
			source_template_id,
			desired_template_revision_id,
			last_applied_template_revision_id,
			selected_ref,
			workspace_name,
			config_json,
			desired_config_json,
			last_applied_run_id,
			last_applied_ref,
			last_applied_at,
			created_by,
			lifecycle
	`, defaultJSON(configJSON), tenantID, id)
	stackTemplate, err := scanStackTemplate(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return traits.StackTemplate{}, app.ErrNotFound
	}
	if err != nil {
		return traits.StackTemplate{}, fmt.Errorf("update stack template config: %w", err)
	}
	return stackTemplate, nil
}

func (store *Store) UpdateStackTemplateDesiredRevision(ctx context.Context, tenantID traits.TenantID, id traits.StackTemplateID, templateRevisionID traits.TemplateRevisionID, configJSON json.RawMessage) (traits.StackTemplate, error) {
	row := store.pool.QueryRow(ctx, `
		update stack_templates
		set
			desired_template_revision_id = $1,
			desired_config_json = $2::jsonb
		where tenant_id = $3
			and id = $4
		returning
			id,
			tenant_id,
			stack_id,
			component_key,
			source_template_id,
			desired_template_revision_id,
			last_applied_template_revision_id,
			selected_ref,
			workspace_name,
			config_json,
			desired_config_json,
			last_applied_run_id,
			last_applied_ref,
			last_applied_at,
			created_by,
			lifecycle
	`, templateRevisionID, defaultJSON(configJSON), tenantID, id)
	stackTemplate, err := scanStackTemplate(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return traits.StackTemplate{}, app.ErrNotFound
	}
	if err != nil {
		return traits.StackTemplate{}, fmt.Errorf("update stack template desired revision: %w", err)
	}
	return stackTemplate, nil
}

func (store *Store) CreateTemplateRun(ctx context.Context, run traits.TemplateRun) error {
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin create template run: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, `
		insert into template_runs (
			id,
			tenant_id,
			stack_template_id,
			template_revision_id,
			source_template_id,
			operation,
			selected_ref,
			resolved_commit_sha,
			workspace_name,
			config_json,
			backend_type,
			backend_config_hash,
			status,
			trigger_actor,
			started_at,
			completed_at,
			error_summary
		) values (
			$1, $2, $3, $4, $5, $6, $7, $8,
			$9, $10::jsonb, $11, $12, $13, $14, $15, $16, $17
		)
	`,
		run.ID,
		run.TenantID,
		run.StackTemplateID,
		run.TemplateRevisionID,
		run.SourceTemplateID,
		run.Operation,
		run.SelectedRef,
		run.ResolvedCommitSHA,
		run.WorkspaceName,
		defaultJSON(run.ConfigJSON),
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

	_, err = tx.Exec(ctx, `
		insert into workflow_outbox (
			id,
			event_type,
			aggregate_id
		) values ($1, 'start_template_run', $2)
	`, fmt.Sprintf("template-run/%s/%s", run.TenantID, run.ID), run.ID)
	if err != nil {
		return fmt.Errorf("enqueue template run workflow: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit template run: %w", err)
	}

	return nil
}

func (store *Store) ClaimTemplateRun(ctx context.Context, availableAt, claimedUntil time.Time) (dispatch.Entry, bool, error) {
	var entry dispatch.Entry
	err := store.pool.QueryRow(ctx, `
		with candidate as (
			select id
			from workflow_outbox
			where event_type = 'start_template_run'
				and processed_at is null
				and available_at <= $1
				and (claimed_until is null or claimed_until <= $1)
			order by available_at, id
			for update skip locked
			limit 1
		), claimed as (
			update workflow_outbox outbox
			set
				claimed_until = $2,
				attempts = attempts + 1
			from candidate
			where outbox.id = candidate.id
			returning outbox.id, outbox.aggregate_id
		)
		select
			claimed.id,
			run.id,
			run.tenant_id,
			run.stack_template_id,
			run.operation,
			run.selected_ref,
			run.workspace_name,
			revision.repo_owner,
			revision.repo_name,
			revision.root_path,
			run.config_json
		from claimed
		join template_runs run on run.id = claimed.aggregate_id
		join template_revisions revision on revision.id = run.template_revision_id
	`, availableAt, claimedUntil).Scan(
		&entry.ID,
		&entry.Input.RunID,
		&entry.Input.TenantID,
		&entry.Input.StackTemplateID,
		&entry.Input.Operation,
		&entry.Input.SelectedRef,
		&entry.Input.WorkspaceName,
		&entry.Input.RepoOwner,
		&entry.Input.RepoName,
		&entry.Input.RootPath,
		&entry.Input.ConfigJSON,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return dispatch.Entry{}, false, nil
	}
	if err != nil {
		return dispatch.Entry{}, false, fmt.Errorf("claim template run outbox entry: %w", err)
	}
	return entry, true, nil
}

func (store *Store) CompleteTemplateRun(ctx context.Context, id string) error {
	_, err := store.pool.Exec(ctx, `
		update workflow_outbox
		set
			processed_at = now(),
			claimed_until = null,
			last_error = ''
		where id = $1
			and processed_at is null
	`, id)
	if err != nil {
		return fmt.Errorf("complete template run outbox entry: %w", err)
	}
	return nil
}

func (store *Store) RetryTemplateRun(ctx context.Context, id string, availableAt time.Time, lastError string) error {
	_, err := store.pool.Exec(ctx, `
		update workflow_outbox
		set
			available_at = $2,
			claimed_until = null,
			last_error = $3
		where id = $1
			and processed_at is null
	`, id, availableAt, lastError)
	if err != nil {
		return fmt.Errorf("retry template run outbox entry: %w", err)
	}
	return nil
}

func (store *Store) ClaimAuthorizationRelationship(ctx context.Context, availableAt, claimedUntil time.Time) (authdispatch.Entry, bool, error) {
	var entry authdispatch.Entry
	err := store.pool.QueryRow(ctx, `
		with candidate as (
			select id from authorization_outbox
			where processed_at is null and failed_at is null and available_at <= $1
				and (claimed_until is null or claimed_until <= $1)
			order by available_at, id for update skip locked limit 1
		), claimed as (
			update authorization_outbox outbox set claimed_until = $2, attempts = attempts + 1
			from candidate where outbox.id = candidate.id
			returning outbox.id, outbox.operation, outbox.subject, outbox.stack, outbox.role
		) select id, operation, subject, stack, role from claimed
	`, availableAt, claimedUntil).Scan(&entry.ID, &entry.Operation, &entry.Subject, &entry.Stack, &entry.Role)
	if errors.Is(err, pgx.ErrNoRows) {
		return authdispatch.Entry{}, false, nil
	}
	if err != nil {
		return authdispatch.Entry{}, false, fmt.Errorf("claim authorization relationship: %w", err)
	}
	return entry, true, nil
}

func (store *Store) CompleteAuthorizationRelationship(ctx context.Context, id string) error {
	_, err := store.pool.Exec(ctx, `update authorization_outbox set processed_at = now(), claimed_until = null, last_error = '' where id = $1 and processed_at is null and failed_at is null`, id)
	if err != nil {
		return fmt.Errorf("complete authorization relationship: %w", err)
	}
	return nil
}

func (store *Store) RetryAuthorizationRelationship(ctx context.Context, id string, availableAt time.Time, lastError string) error {
	_, err := store.pool.Exec(ctx, `update authorization_outbox set available_at = $2, claimed_until = null, last_error = $3 where id = $1 and processed_at is null and failed_at is null`, id, availableAt, lastError)
	if err != nil {
		return fmt.Errorf("retry authorization relationship: %w", err)
	}
	return nil
}

func (store *Store) FailAuthorizationRelationship(ctx context.Context, id, lastError string) error {
	_, err := store.pool.Exec(ctx, `update authorization_outbox set failed_at = now(), claimed_until = null, last_error = $2 where id = $1 and processed_at is null and failed_at is null`, id, lastError)
	if err != nil {
		return fmt.Errorf("fail authorization relationship: %w", err)
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
			template_revision_id,
			source_template_id,
			operation,
			selected_ref,
			resolved_commit_sha,
			workspace_name,
			config_json,
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
		&run.TemplateRevisionID,
		&run.SourceTemplateID,
		&run.Operation,
		&run.SelectedRef,
		&run.ResolvedCommitSHA,
		&run.WorkspaceName,
		&run.ConfigJSON,
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

func (store *Store) AppendAuditEvent(ctx context.Context, event traits.SecurityAuditEvent) error {
	_, err := store.pool.Exec(ctx,
		`INSERT INTO security_audit_log
			(actor_subject, action, target_user, tenant_id, stack_id, old_role, new_role, outcome, correlation_id)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		event.ActorSubject, event.Action, event.TargetUser,
		event.TenantID, event.StackID, event.OldRole, event.NewRole,
		event.Outcome, event.CorrelationID,
	)
	return err
}

func (store *Store) RecordTemplateRunStatus(ctx context.Context, input traits.TemplateRunStatusActivityInput) error {
	if recordsStackTemplateLastApplied(input) {
		tx, err := store.pool.Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin record template run status: %w", err)
		}
		defer func() {
			_ = tx.Rollback(ctx)
		}()

		if err := recordTemplateRunStatus(ctx, tx, input); err != nil {
			return err
		}
		if err := recordStackTemplateLastApplied(ctx, tx, input); err != nil {
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit record template run status: %w", err)
		}
		return nil
	}

	return recordTemplateRunStatus(ctx, store.pool, input)
}

type templateRunStatusWriter interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func recordTemplateRunStatus(ctx context.Context, writer templateRunStatusWriter, input traits.TemplateRunStatusActivityInput) error {
	var updatedRunID traits.TemplateRunID
	var err error

	if input.Status.Terminal() {
		err = writer.QueryRow(ctx, `
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
		err = writer.QueryRow(ctx, `
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

type stackTemplateLastAppliedWriter interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
}

func recordsStackTemplateLastApplied(input traits.TemplateRunStatusActivityInput) bool {
	return input.Operation == traits.OperationApply && input.Status == traits.TemplateRunApplied
}

func recordStackTemplateLastApplied(ctx context.Context, writer stackTemplateLastAppliedWriter, input traits.TemplateRunStatusActivityInput) error {
	commandTag, err := writer.Exec(ctx, `
		update stack_templates
		set
			last_applied_run_id = template_runs.id,
			last_applied_template_revision_id = template_runs.template_revision_id,
			last_applied_ref = template_runs.selected_ref,
			last_applied_at = now()
		from template_runs
		where stack_templates.tenant_id = $1
			and stack_templates.id = $2
			and template_runs.tenant_id = stack_templates.tenant_id
			and template_runs.id = $3
			and template_runs.stack_template_id = stack_templates.id
			and template_runs.operation = $4
			and template_runs.status = $5
	`,
		input.TenantID,
		input.StackTemplateID,
		input.RunID,
		input.Operation,
		input.Status,
	)
	if err != nil {
		return fmt.Errorf("record stack template last applied: %w", err)
	}
	if commandTag.RowsAffected() == 0 {
		return ErrNotFound
	}

	return nil
}

type stackTemplateScanner interface {
	Scan(dest ...any) error
}

type stackScanner interface {
	Scan(dest ...any) error
}

type templateScanner interface {
	Scan(dest ...any) error
}

func scanStackTemplate(scanner stackTemplateScanner) (traits.StackTemplate, error) {
	var stackTemplate traits.StackTemplate
	var configJSON []byte
	var desiredConfigJSON []byte
	var lastAppliedAt sql.NullTime

	if err := scanner.Scan(
		&stackTemplate.ID,
		&stackTemplate.TenantID,
		&stackTemplate.StackID,
		&stackTemplate.ComponentKey,
		&stackTemplate.SourceTemplateID,
		&stackTemplate.DesiredTemplateRevisionID,
		&stackTemplate.LastAppliedTemplateRevisionID,
		&stackTemplate.SelectedRef,
		&stackTemplate.WorkspaceName,
		&configJSON,
		&desiredConfigJSON,
		&stackTemplate.LastAppliedRunID,
		&stackTemplate.LastAppliedRef,
		&lastAppliedAt,
		&stackTemplate.CreatedBy,
		&stackTemplate.Lifecycle,
	); err != nil {
		return traits.StackTemplate{}, fmt.Errorf("scan stack template: %w", err)
	}
	stackTemplate.ConfigJSON = configJSON
	stackTemplate.DesiredConfigJSON = desiredConfigJSON
	if lastAppliedAt.Valid {
		stackTemplate.LastAppliedAt = lastAppliedAt.Time
	}
	return stackTemplate, nil
}

func scanTemplateRevision(scanner templateScanner) (traits.TemplateRevision, error) {
	var templateRevision traits.TemplateRevision
	var tagsJSON []byte

	if err := scanner.Scan(
		&templateRevision.ID,
		&templateRevision.TenantID,
		&templateRevision.SourceTemplateID,
		&templateRevision.RepoOwner,
		&templateRevision.RepoName,
		&templateRevision.SourceRef,
		&templateRevision.ResolvedCommitSHA,
		&templateRevision.RootPath,
		&templateRevision.Name,
		&templateRevision.Description,
		&tagsJSON,
		&templateRevision.Status,
		&templateRevision.CreatedAt,
	); err != nil {
		return traits.TemplateRevision{}, err
	}
	if err := json.Unmarshal(tagsJSON, &templateRevision.Tags); err != nil {
		return traits.TemplateRevision{}, fmt.Errorf("unmarshal template revision tags: %w", err)
	}
	return templateRevision, nil
}

func scanStack(scanner stackScanner) (traits.Stack, error) {
	var stack traits.Stack
	var tagsJSON []byte
	var credentialIDsJSON []byte

	if err := scanner.Scan(
		&stack.ID,
		&stack.TenantID,
		&stack.Name,
		&stack.Slug,
		&tagsJSON,
		&credentialIDsJSON,
		&stack.CreatedBy,
		&stack.CreatedAt,
	); err != nil {
		return traits.Stack{}, err
	}
	if err := json.Unmarshal(tagsJSON, &stack.Tags); err != nil {
		return traits.Stack{}, fmt.Errorf("unmarshal stack tags: %w", err)
	}
	if err := json.Unmarshal(credentialIDsJSON, &stack.DefaultCredentialIDs); err != nil {
		return traits.Stack{}, fmt.Errorf("unmarshal stack credential IDs: %w", err)
	}
	return stack, nil
}

func (store *Store) getStack(ctx context.Context, tenantID traits.TenantID, stackID traits.StackID) (traits.Stack, error) {
	row := store.pool.QueryRow(ctx, `
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
	`, tenantID, stackID)
	stack, err := scanStack(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return traits.Stack{}, app.ErrNotFound
	}
	if err != nil {
		return traits.Stack{}, fmt.Errorf("get stack: %w", err)
	}
	return stack, nil
}

func duplicateConstraint(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == constraint
}

func upsertSourceTemplate(ctx context.Context, tx pgx.Tx, templateRevision traits.TemplateRevision) (traits.SourceTemplateID, error) {
	sourceTemplateID := templateRevision.SourceTemplateID
	if sourceTemplateID == "" {
		sourceTemplateID = deterministicSourceTemplateID(templateRevision)
	}

	var persistedID traits.SourceTemplateID
	err := tx.QueryRow(ctx, `
		insert into source_templates (
			id,
			tenant_id,
			repo_owner,
			repo_name,
			source_ref,
			root_path
		) values ($1, $2, $3, $4, $5, $6)
		on conflict (tenant_id, repo_owner, repo_name, root_path, source_ref) do update set
			updated_at = now()
		returning id
	`,
		sourceTemplateID,
		templateRevision.TenantID,
		templateRevision.RepoOwner,
		templateRevision.RepoName,
		templateRevision.SourceRef,
		templateRevision.RootPath,
	).Scan(&persistedID)
	if err != nil {
		return "", fmt.Errorf("upsert source template: %w", err)
	}

	return persistedID, nil
}

func recordLatestTemplateRevision(ctx context.Context, tx pgx.Tx, templateRevision traits.TemplateRevision) error {
	commandTag, err := tx.Exec(ctx, `
		update source_templates
		set
			latest_template_revision_id = $1,
			updated_at = now()
		where tenant_id = $2
			and id = $3
	`,
		templateRevision.ID,
		templateRevision.TenantID,
		templateRevision.SourceTemplateID,
	)
	if err != nil {
		return fmt.Errorf("record latest template revision: %w", err)
	}
	if commandTag.RowsAffected() == 0 {
		return app.ErrNotFound
	}

	return nil
}

func deterministicSourceTemplateID(templateRevision traits.TemplateRevision) traits.SourceTemplateID {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		string(templateRevision.TenantID),
		templateRevision.RepoOwner,
		templateRevision.RepoName,
		templateRevision.RootPath,
		templateRevision.SourceRef,
	}, "\x00")))
	return traits.SourceTemplateID("source_template_" + hex.EncodeToString(sum[:16]))
}

func defaultJSON(input json.RawMessage) json.RawMessage {
	if len(input) == 0 {
		return json.RawMessage(`{}`)
	}
	return input
}

func insertTemplateRevision(ctx context.Context, tx pgx.Tx, templateRevision traits.TemplateRevision, tagsJSON []byte) (traits.TemplateRevision, bool, error) {
	var inserted traits.TemplateRevision
	var insertedTagsJSON []byte
	err := tx.QueryRow(ctx, `
		insert into template_revisions (
			id,
			tenant_id,
			source_template_id,
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
			$1, $2, $3, $4, $5, $6, $7, $8,
			$9, $10, $11::jsonb, $12, coalesce($13::timestamptz, now())
		)
		on conflict (tenant_id, source_template_id, resolved_commit_sha) do nothing
		returning
			id,
			tenant_id,
			source_template_id,
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
		templateRevision.ID,
		templateRevision.TenantID,
		templateRevision.SourceTemplateID,
		templateRevision.RepoOwner,
		templateRevision.RepoName,
		templateRevision.SourceRef,
		templateRevision.ResolvedCommitSHA,
		templateRevision.RootPath,
		templateRevision.Name,
		templateRevision.Description,
		tagsJSON,
		templateRevision.Status,
		nullTime(templateRevision.CreatedAt),
	).Scan(
		&inserted.ID,
		&inserted.TenantID,
		&inserted.SourceTemplateID,
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
			return traits.TemplateRevision{}, false, fmt.Errorf("unmarshal inserted template revision tags: %w", err)
		}
		return inserted, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return traits.TemplateRevision{}, false, fmt.Errorf("insert template revision: %w", err)
	}

	selected, err := selectTemplateRevisionByIdentity(ctx, tx, templateRevision)
	if err != nil {
		return traits.TemplateRevision{}, false, err
	}
	return selected, false, nil
}

func selectTemplateRevisionByIdentity(ctx context.Context, tx pgx.Tx, templateRevision traits.TemplateRevision) (traits.TemplateRevision, error) {
	var selected traits.TemplateRevision
	var tagsJSON []byte
	err := tx.QueryRow(ctx, `
		select
			id,
			tenant_id,
			source_template_id,
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
		from template_revisions
		where tenant_id = $1
			and source_template_id = $2
			and resolved_commit_sha = $3
	`,
		templateRevision.TenantID,
		templateRevision.SourceTemplateID,
		templateRevision.ResolvedCommitSHA,
	).Scan(
		&selected.ID,
		&selected.TenantID,
		&selected.SourceTemplateID,
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
		return traits.TemplateRevision{}, app.ErrNotFound
	}
	if err != nil {
		return traits.TemplateRevision{}, fmt.Errorf("select template revision by identity: %w", err)
	}
	if err := json.Unmarshal(tagsJSON, &selected.Tags); err != nil {
		return traits.TemplateRevision{}, fmt.Errorf("unmarshal selected template revision tags: %w", err)
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
