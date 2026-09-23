package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/vishu42/tflive/internal/app"
	"github.com/vishu42/tflive/internal/domain"
)

func (store *Store) CreateTemplateRegistration(ctx context.Context, registration domain.TemplateRegistration) error {
	return createTemplateRegistration(ctx, store.pool, registration)
}

func createTemplateRegistration(ctx context.Context, exec pgxExecutor, registration domain.TemplateRegistration) error {
	_, err := exec.Exec(ctx, `
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

// CreateCredential stores one already-encrypted credential under exactly one scope owner.
func (store *Store) CreateCredential(ctx context.Context, credential domain.CredentialSet) error {
	_, err := store.pool.Exec(ctx, `
		insert into scoped_credentials (id, tenant_id, stack_id, stack_template_id, name, ciphertext, created_at)
		values ($1, $2, nullif($3, ''), nullif($4, ''), $5, $6, $7)
	`, credential.ID, credential.TenantID, credential.StackID, credential.StackTemplateID, credential.Name, credential.Ciphertext, credential.CreatedAt)
	if duplicateConstraint(err, "scoped_credentials_stack_name_idx") || duplicateConstraint(err, "scoped_credentials_template_name_idx") {
		return app.ErrDuplicateCredentialName
	}
	if err != nil {
		return fmt.Errorf("create credential: %w", err)
	}
	return nil
}

// ListCredentials returns encrypted credential records owned directly by one scope owner.
func (store *Store) ListCredentials(ctx context.Context, tenantID domain.TenantID, scope domain.CredentialScope, ownerID string) ([]domain.CredentialSet, error) {
	column := "stack_id"
	if scope == domain.CredentialScopeStackTemplate {
		column = "stack_template_id"
	}
	if scope != domain.CredentialScopeStack && scope != domain.CredentialScopeStackTemplate {
		return nil, app.ErrInvalidCommand
	}
	rows, err := store.pool.Query(ctx, fmt.Sprintf(`
		select id, tenant_id, stack_id, stack_template_id, name, ciphertext, created_at
		from scoped_credentials
		where tenant_id = $1 and %s = $2
		order by name
	`, column), tenantID, ownerID)
	if err != nil {
		return nil, fmt.Errorf("list credentials: %w", err)
	}
	defer rows.Close()
	return scanCredentials(rows)
}

// DeleteCredential removes one tenant-owned credential record by ID.
func (store *Store) DeleteCredential(ctx context.Context, tenantID domain.TenantID, id domain.CredentialSetID) error {
	result, err := store.pool.Exec(ctx, `delete from scoped_credentials where tenant_id = $1 and id = $2`, tenantID, id)
	if err != nil {
		return fmt.Errorf("delete credential: %w", err)
	}
	if result.RowsAffected() == 0 {
		return app.ErrNotFound
	}
	return nil
}

// ListCredentialsForStackTemplate returns stack credentials plus template credentials for runtime inheritance.
func (store *Store) ListCredentialsForStackTemplate(ctx context.Context, tenantID domain.TenantID, stackTemplateID domain.StackTemplateID) ([]domain.CredentialSet, error) {
	rows, err := store.pool.Query(ctx, `
		select c.id, c.tenant_id, c.stack_id, c.stack_template_id, c.name, c.ciphertext, c.created_at
		from scoped_credentials c
		join stack_templates st on st.id = $2
		where c.tenant_id = $1 and (c.stack_id = st.stack_id or c.stack_template_id = st.id)
		order by c.stack_id is not null desc, c.name
	`, tenantID, stackTemplateID)
	if err != nil {
		return nil, fmt.Errorf("list runtime credentials: %w", err)
	}
	defer rows.Close()
	return scanCredentials(rows)
}

type credentialRows interface {
	Next() bool
	Scan(...any) error
	Err() error
}

// scanCredentials converts nullable scope-owner columns into credential domain records.
func scanCredentials(rows credentialRows) ([]domain.CredentialSet, error) {
	credentials := make([]domain.CredentialSet, 0)
	for rows.Next() {
		var credential domain.CredentialSet
		var stackID sql.NullString
		var stackTemplateID sql.NullString
		if err := rows.Scan(&credential.ID, &credential.TenantID, &stackID, &stackTemplateID, &credential.Name, &credential.Ciphertext, &credential.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan credential: %w", err)
		}
		if stackID.Valid {
			credential.StackID = domain.StackID(stackID.String)
		}
		if stackTemplateID.Valid {
			credential.StackTemplateID = domain.StackTemplateID(stackTemplateID.String)
		}
		credentials = append(credentials, credential)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate credentials: %w", err)
	}
	return credentials, nil
}

func (store *Store) GetTemplateRegistration(ctx context.Context, tenantID domain.TenantID, id domain.TemplateRegistrationID) (domain.TemplateRegistration, error) {
	var registration domain.TemplateRegistration
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
		return domain.TemplateRegistration{}, app.ErrNotFound
	}
	if err != nil {
		return domain.TemplateRegistration{}, fmt.Errorf("get template registration: %w", err)
	}
	if completedAt.Valid {
		registration.CompletedAt = completedAt.Time
	}
	return registration, nil
}

func (store *Store) RecordTemplateRegistrationStatus(ctx context.Context, input domain.TemplateRegistrationStatusActivityInput) error {
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

// UpsertTemplateRevisionWithVariables records a template revision, deduplicating against
// an existing revision with the same identity. The source template is resolved or created
// first so the revision can reference it; variables are only written when this call actually
// inserts a new revision, since a reused revision already has its variables recorded.
func (store *Store) UpsertTemplateRevisionWithVariables(ctx context.Context, templateRevision domain.TemplateRevision, variables []domain.TemplateVariable) (domain.TemplateRevision, error) {
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return domain.TemplateRevision{}, fmt.Errorf("begin upsert template revision: %w", err)
	}
	defer tx.Rollback(ctx)

	tagsJSON, err := json.Marshal(templateRevision.Tags)
	if err != nil {
		return domain.TemplateRevision{}, fmt.Errorf("marshal template revision tags: %w", err)
	}
	if tagsJSON == nil {
		// Store an empty JSON array rather than a null tags column.
		tagsJSON = []byte("[]")
	}

	sourceTemplateID, err := upsertSourceTemplate(ctx, tx, templateRevision)
	if err != nil {
		return domain.TemplateRevision{}, err
	}
	templateRevision.SourceTemplateID = sourceTemplateID

	inserted, insertedNew, err := insertTemplateRevision(ctx, tx, templateRevision, tagsJSON)
	if err != nil {
		return domain.TemplateRevision{}, err
	}
	if err := recordLatestTemplateRevision(ctx, tx, inserted); err != nil {
		return domain.TemplateRevision{}, err
	}
	if !insertedNew {
		// Revision already existed under this identity, so its variables were already
		// recorded on the original insert — skip re-inserting them.
		if err := tx.Commit(ctx); err != nil {
			return domain.TemplateRevision{}, fmt.Errorf("commit reused template revision: %w", err)
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
			return domain.TemplateRevision{}, fmt.Errorf("insert template revision variable %q: %w", variable.Name, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return domain.TemplateRevision{}, fmt.Errorf("commit upsert template revision: %w", err)
	}
	return inserted, nil
}

func (store *Store) GetTemplateRevisionVariables(ctx context.Context, tenantID domain.TenantID, templateRevisionID domain.TemplateRevisionID) ([]domain.TemplateVariable, error) {
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

	var variables []domain.TemplateVariable
	for rows.Next() {
		var variable domain.TemplateVariable
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

func (store *Store) ListTemplateRevisions(ctx context.Context, tenantID domain.TenantID) ([]domain.TemplateRevision, error) {
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

	var templateRevisions []domain.TemplateRevision
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

func (store *Store) CreateStack(ctx context.Context, stack domain.Stack) error {
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

func insertStack(ctx context.Context, tx pgx.Tx, stack domain.Stack) error {
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

	// A caller that sets no status gets ready, the only one there is.
	status := stack.Status
	if status == "" {
		status = domain.StackStatusReady
	}

	_, err = tx.Exec(ctx, `
		insert into stacks (
			id,
			tenant_id,
			name,
			slug,
			status,
			tags_json,
			default_credential_ids_json,
			created_by,
			created_at
		) values ($1, $2, $3, $4, $5, $6::jsonb, $7::jsonb, $8, $9)
	`,
		stack.ID,
		stack.TenantID,
		stack.Name,
		stack.Slug,
		string(status),
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

func (store *Store) GetStack(ctx context.Context, tenantID domain.TenantID, stackID domain.StackID) (domain.Stack, error) {
	stack, err := store.getStack(ctx, tenantID, stackID)
	if err != nil {
		return domain.Stack{}, err
	}
	return stack, nil
}

func (store *Store) ListStacks(ctx context.Context, tenantID domain.TenantID) ([]domain.Stack, error) {
	rows, err := store.pool.Query(ctx, `
		select
			id,
			tenant_id,
			name,
			slug,
			status,
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

	var stacks []domain.Stack
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

func (store *Store) ListStacksPage(ctx context.Context, tenantID domain.TenantID, after *app.StackPageCursor, limit int) ([]domain.Stack, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("stack page limit must be positive")
	}
	var afterCreatedAt any
	var afterID domain.StackID
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
			status,
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

	stacks := make([]domain.Stack, 0, limit)
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

func (store *Store) GetStackWithTemplates(ctx context.Context, tenantID domain.TenantID, stackID domain.StackID) (app.StackView, error) {
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
			workspace_name,
			installed_config_json,
			desired_config_json,
			last_applied_run_id,
			last_applied_config_json,
			last_applied_at,
			pending_plan_run_id,
			pending_plan_template_revision_id,
			pending_plan_config_json,
			pending_plan_at,
			created_by,
			lifecycle
		from stack_templates
		where tenant_id = $1
			and stack_id = $2
			and lifecycle != $3
		order by id
	`, tenantID, stackID, domain.StackTemplateDestroyed)
	if err != nil {
		return app.StackView{}, fmt.Errorf("get stack templates: %w", err)
	}
	defer rows.Close()

	// The repository returns views with their label fields unset; resolving
	// those needs revision metadata, which is the service's job, not storage's.
	var templates []app.StackTemplateView
	for rows.Next() {
		stackTemplate, err := scanStackTemplate(rows)
		if err != nil {
			return app.StackView{}, err
		}
		templates = append(templates, app.StackTemplateView{StackTemplate: stackTemplate})
	}
	if err := rows.Err(); err != nil {
		return app.StackView{}, fmt.Errorf("iterate stack templates: %w", err)
	}

	return app.StackView{Stack: stack, Templates: templates}, nil
}

func (store *Store) CreateStackTemplate(ctx context.Context, stackTemplate domain.StackTemplate) error {
	configJSON := stackTemplate.InstalledConfigJSON
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
			workspace_name,
			installed_config_json,
			desired_config_json,
			last_applied_run_id,
			last_applied_config_json,
			last_applied_at,
			pending_plan_run_id,
			pending_plan_template_revision_id,
			pending_plan_config_json,
			pending_plan_at,
			created_by,
			lifecycle
		)
		select $1, $2, $3, $4, $5, $6, $7, $8, $9::jsonb, $10::jsonb, $11, $12::jsonb, $13, $14, $15, $16::jsonb, $17, $18, $19
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
		stackTemplate.WorkspaceName,
		configJSON,
		desiredConfigJSON,
		stackTemplate.LastAppliedRunID,
		nullJSON(stackTemplate.LastAppliedConfigJSON),
		nullTime(stackTemplate.LastAppliedAt),
		stackTemplate.PendingPlanRunID,
		stackTemplate.PendingPlanTemplateRevisionID,
		nullJSON(stackTemplate.PendingPlanConfigJSON),
		nullTime(stackTemplate.PendingPlanAt),
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

func (store *Store) GetTemplateRevision(ctx context.Context, tenantID domain.TenantID, templateRevisionID domain.TemplateRevisionID) (domain.TemplateRevision, error) {
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
		return domain.TemplateRevision{}, app.ErrNotFound
	}
	if err != nil {
		return domain.TemplateRevision{}, fmt.Errorf("get template revision: %w", err)
	}
	return templateRevision, nil
}

func (store *Store) GetStackTemplate(ctx context.Context, tenantID domain.TenantID, id domain.StackTemplateID) (domain.StackTemplate, error) {
	row := store.pool.QueryRow(ctx, `
		select
			id,
			tenant_id,
			stack_id,
			component_key,
			source_template_id,
			desired_template_revision_id,
			last_applied_template_revision_id,
			workspace_name,
			installed_config_json,
			desired_config_json,
			last_applied_run_id,
			last_applied_config_json,
			last_applied_at,
			pending_plan_run_id,
			pending_plan_template_revision_id,
			pending_plan_config_json,
			pending_plan_at,
			created_by,
			lifecycle
		from stack_templates
		where tenant_id = $1
			and id = $2
	`, tenantID, id)
	stackTemplate, scanErr := scanStackTemplate(row)
	if errors.Is(scanErr, pgx.ErrNoRows) {
		return domain.StackTemplate{}, app.ErrNotFound
	}
	if scanErr != nil {
		return domain.StackTemplate{}, fmt.Errorf("get stack template: %w", scanErr)
	}
	return stackTemplate, nil
}

func (store *Store) UpdateStackTemplateConfig(ctx context.Context, tenantID domain.TenantID, id domain.StackTemplateID, configJSON json.RawMessage) (domain.StackTemplate, error) {
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
			workspace_name,
			installed_config_json,
			desired_config_json,
			last_applied_run_id,
			last_applied_config_json,
			last_applied_at,
			pending_plan_run_id,
			pending_plan_template_revision_id,
			pending_plan_config_json,
			pending_plan_at,
			created_by,
			lifecycle
	`, defaultJSON(configJSON), tenantID, id)
	stackTemplate, err := scanStackTemplate(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.StackTemplate{}, app.ErrNotFound
	}
	if err != nil {
		return domain.StackTemplate{}, fmt.Errorf("update stack template config: %w", err)
	}
	return stackTemplate, nil
}

func (store *Store) UpdateStackTemplateDesiredRevision(ctx context.Context, tenantID domain.TenantID, id domain.StackTemplateID, templateRevisionID domain.TemplateRevisionID, configJSON json.RawMessage) (domain.StackTemplate, error) {
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
			workspace_name,
			installed_config_json,
			desired_config_json,
			last_applied_run_id,
			last_applied_config_json,
			last_applied_at,
			pending_plan_run_id,
			pending_plan_template_revision_id,
			pending_plan_config_json,
			pending_plan_at,
			created_by,
			lifecycle
	`, templateRevisionID, defaultJSON(configJSON), tenantID, id)
	stackTemplate, err := scanStackTemplate(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.StackTemplate{}, app.ErrNotFound
	}
	if err != nil {
		return domain.StackTemplate{}, fmt.Errorf("update stack template desired revision: %w", err)
	}
	return stackTemplate, nil
}

func (store *Store) CreateTemplateRun(ctx context.Context, run domain.TemplateRun) (int, error) {
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin create template run: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	runNumber, err := createTemplateRun(ctx, tx, run)
	if err != nil {
		return 0, err
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit template run: %w", err)
	}

	return runNumber, nil
}

// createTemplateRun inserts run and returns the run number it was assigned. The
// number is max + 1 within the stack template, computed by the insert itself;
// see migration 0022 for why that needs no counter.
func createTemplateRun(ctx context.Context, exec pgxExecutor, run domain.TemplateRun) (int, error) {
	var runNumber int
	err := exec.QueryRow(ctx, `
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
			error_summary,
			auto_approve,
			run_number
		) values (
			$1, $2, $3, $4, $5, $6, $7, $8,
			$9, $10::jsonb, $11, $12, $13, $14, $15, $16, $17, $18,
			(
				select coalesce(max(run_number), 0) + 1
				from template_runs
				where tenant_id = $2
					and stack_template_id = $3
			)
		)
		returning run_number
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
		run.AutoApprove,
	).Scan(&runNumber)
	// The gate against concurrent runs on one stack template: the insert is the
	// check, so there is no window between deciding and writing. See migration
	// 0021 for why it lives here rather than in StartTemplateRun.
	//
	// A losing concurrent insert computed the same run number as the winner, so
	// it can trip the run number index before the in-flight one; which index
	// Postgres checks first is not something to rely on. Either means another
	// run got there first.
	if duplicateConstraint(err, "template_runs_in_flight_idx") || duplicateConstraint(err, "template_runs_run_number_idx") {
		return 0, app.ErrTemplateRunInFlight
	}
	if err != nil {
		return 0, fmt.Errorf("create template run: %w", err)
	}

	return runNumber, nil
}

func (store *Store) GetTemplateRun(ctx context.Context, tenantID domain.TenantID, runID domain.TemplateRunID) (domain.TemplateRun, error) {
	var run domain.TemplateRun
	var startedAt sql.NullTime
	var completedAt sql.NullTime
	var planAdd, planChange, planDestroy *int

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
			step,
			trigger_actor,
			started_at,
			completed_at,
			error_summary,
			run_number,
			auto_approve,
			plan_add,
			plan_change,
			plan_destroy
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
		&run.Step,
		&run.TriggerActor,
		&startedAt,
		&completedAt,
		&run.ErrorSummary,
		&run.RunNumber,
		&run.AutoApprove,
		&planAdd,
		&planChange,
		&planDestroy,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.TemplateRun{}, app.ErrNotFound
	}
	if err != nil {
		return domain.TemplateRun{}, fmt.Errorf("get template run: %w", err)
	}

	if startedAt.Valid {
		run.StartedAt = startedAt.Time
	}
	if completedAt.Valid {
		run.CompletedAt = completedAt.Time
	}
	run.PlanSummary = planSummary(planAdd, planChange, planDestroy)

	return run, nil
}

// planSummary reassembles a run's plan counts, which are written together:
// either all three are set or none is.
func planSummary(add, change, destroy *int) *domain.PlanSummary {
	if add == nil || change == nil || destroy == nil {
		return nil
	}
	return &domain.PlanSummary{Add: *add, Change: *change, Destroy: *destroy}
}

func (store *Store) ListTemplateRuns(ctx context.Context, tenantID domain.TenantID, stackTemplateID domain.StackTemplateID) ([]domain.TemplateRun, error) {
	rows, err := store.pool.Query(ctx, `
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
			step,
			trigger_actor,
			started_at,
			completed_at,
			error_summary,
			run_number,
			auto_approve,
			plan_add,
			plan_change,
			plan_destroy
		from template_runs
		where tenant_id = $1
			and stack_template_id = $2
		order by started_at desc nulls last, id desc
	`, tenantID, stackTemplateID)
	if err != nil {
		return nil, fmt.Errorf("list template runs: %w", err)
	}
	defer rows.Close()

	runs := []domain.TemplateRun{}
	for rows.Next() {
		var run domain.TemplateRun
		var startedAt sql.NullTime
		var completedAt sql.NullTime
		var planAdd, planChange, planDestroy *int
		if err := rows.Scan(
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
			&run.Step,
			&run.TriggerActor,
			&startedAt,
			&completedAt,
			&run.ErrorSummary,
			&run.RunNumber,
			&run.AutoApprove,
			&planAdd,
			&planChange,
			&planDestroy,
		); err != nil {
			return nil, fmt.Errorf("scan template run: %w", err)
		}
		if startedAt.Valid {
			run.StartedAt = startedAt.Time
		}
		if completedAt.Valid {
			run.CompletedAt = completedAt.Time
		}
		run.PlanSummary = planSummary(planAdd, planChange, planDestroy)
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list template runs: %w", err)
	}

	return runs, nil
}

func (store *Store) RecordTemplateRunLog(ctx context.Context, log domain.TemplateRunLog) error {
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

func (store *Store) GetTemplateRunLog(ctx context.Context, tenantID domain.TenantID, runID domain.TemplateRunID, phase string) (domain.TemplateRunLog, error) {
	var log domain.TemplateRunLog
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
		return domain.TemplateRunLog{}, app.ErrNotFound
	}
	if err != nil {
		return domain.TemplateRunLog{}, fmt.Errorf("get template run log: %w", err)
	}
	return log, nil
}

func (store *Store) ListTemplateRunLogs(ctx context.Context, tenantID domain.TenantID, runID domain.TemplateRunID) ([]domain.TemplateRunLog, error) {
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
		order by uploaded_at, phase
	`, tenantID, runID)
	if err != nil {
		return nil, fmt.Errorf("list template run logs: %w", err)
	}
	defer rows.Close()

	var logs []domain.TemplateRunLog
	for rows.Next() {
		var log domain.TemplateRunLog
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

func (store *Store) ApproveTemplateRun(ctx context.Context, approval domain.TemplateRunApproval) error {
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin approve template run: %w", err)
	}
	defer tx.Rollback(ctx)

	if err := approveTemplateRun(ctx, tx, approval); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit approve template run: %w", err)
	}

	return nil
}

func approveTemplateRun(ctx context.Context, exec pgxExecutor, approval domain.TemplateRunApproval) error {
	commandTag, err := exec.Exec(ctx, `
		update template_runs
		set status = $1
		where tenant_id = $2
			and id = $3
			and status = $4
	`,
		domain.TemplateRunApproved,
		approval.TenantID,
		approval.RunID,
		domain.TemplateRunWaitingApproval,
	)
	if err != nil {
		return fmt.Errorf("approve template run status: %w", err)
	}
	if commandTag.RowsAffected() == 0 {
		return app.ErrRunNotApprovable
	}

	if _, err := exec.Exec(ctx, `
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

	return nil
}

func (store *Store) AppendAuditEvent(ctx context.Context, event domain.SecurityAuditEvent) error {
	return appendAuditEvent(ctx, store.pool, event)
}

// appendAuditEvent holds the insert so both the standalone method and the
// transaction-scoped repository share one copy of the SQL.
func appendAuditEvent(ctx context.Context, exec pgxExecutor, event domain.SecurityAuditEvent) error {
	_, err := exec.Exec(ctx,
		`INSERT INTO security_audit_log
			(actor_subject, action, target_user, tenant_id, stack_id, old_role, new_role, outcome, correlation_id)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		event.ActorSubject, event.Action, event.TargetUser,
		event.TenantID, event.StackID, event.OldRole, event.NewRole,
		event.Outcome, event.CorrelationID,
	)
	return err
}

// ErrTemplateRunTransition is a status write the run's current state does not
// allow.
var ErrTemplateRunTransition = errors.New("postgres: template run cannot make that transition")

// workflowStatusSources is every status the workflow records, with the
// statuses a run may be in when it does. Waiting, approved and canceled are
// written by the approval flow, never here.
var workflowStatusSources = map[domain.TemplateRunStatus][]domain.TemplateRunStatus{
	domain.TemplateRunRunning:   {domain.TemplateRunQueued},
	domain.TemplateRunCompleted: {domain.TemplateRunRunning},
	domain.TemplateRunFailed: {
		domain.TemplateRunQueued,
		domain.TemplateRunRunning,
		domain.TemplateRunWaitingApproval,
		domain.TemplateRunApproved,
	},
}

// RecordTemplateRunStatus moves a run along its lifecycle, with the run row
// locked. A run already in the status is this write retried after its
// acknowledgement was lost, so it succeeds and changes nothing.
//
// Becoming terminal sets completed_at, drops the run's saved plan, and, for a
// destroy that failed after it began destroying, leaves the stack template
// failed. The step is never touched: a failed run keeps the one it failed on.
func (store *Store) RecordTemplateRunStatus(ctx context.Context, input domain.TemplateRunStatusActivityInput) error {
	sources, ok := workflowStatusSources[input.Status]
	if !ok {
		return fmt.Errorf("record template run status: the workflow does not record %q", input.Status)
	}

	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin record template run status: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var current domain.TemplateRunStatus
	err = tx.QueryRow(ctx, `
		select status
		from template_runs
		where tenant_id = $1
			and id = $2
			and stack_template_id = $3
			and operation = $4
		for update
	`, input.TenantID, input.RunID, input.StackTemplateID, input.Operation).Scan(&current)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("read template run status: %w", err)
	}
	if current == input.Status {
		return nil
	}
	if !slices.Contains(sources, current) {
		return fmt.Errorf("%w: %q cannot become %q", ErrTemplateRunTransition, current, input.Status)
	}

	if !input.Status.Terminal() {
		if _, err := tx.Exec(ctx, `
			update template_runs set status = $1 where tenant_id = $2 and id = $3
		`, input.Status, input.TenantID, input.RunID); err != nil {
			return fmt.Errorf("record template run status: %w", err)
		}
		return commitTemplateRunStatus(ctx, tx)
	}

	if _, err := tx.Exec(ctx, `
		update template_runs
		set
			status = $1,
			error_summary = case when $2 <> '' then $2 else error_summary end,
			completed_at = coalesce(completed_at, now())
		where tenant_id = $3 and id = $4
	`, input.Status, input.ErrorSummary, input.TenantID, input.RunID); err != nil {
		return fmt.Errorf("record template run status: %w", err)
	}
	if input.Status == domain.TemplateRunFailed && input.Operation == domain.OperationDestroy {
		if err := recordInterruptedDestroyLifecycle(ctx, tx, input.TenantID, input.StackTemplateID); err != nil {
			return err
		}
	}
	if err := releaseRunPlan(ctx, tx, input.TenantID, input.RunID); err != nil {
		return err
	}
	return commitTemplateRunStatus(ctx, tx)
}

func commitTemplateRunStatus(ctx context.Context, tx pgx.Tx) error {
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit record template run status: %w", err)
	}
	return nil
}

// releaseRunPlan is what a run becoming terminal does to its saved plan. Its
// key is dropped, so the plan file left in the artifact store can never be
// opened again, whoever holds the file; and the plan stops being the stack
// template's pending plan, so a discarded or failed plan no longer counts as
// reviewed. Every path that makes a run terminal calls it in the same
// transaction.
func releaseRunPlan(ctx context.Context, exec pgxExecutor, tenantID domain.TenantID, runID domain.TemplateRunID) error {
	if _, err := exec.Exec(ctx, `
		update template_runs
		set plan_artifact_dek = null
		where tenant_id = $1 and id = $2
	`, tenantID, runID); err != nil {
		return fmt.Errorf("drop plan key: %w", err)
	}
	if _, err := exec.Exec(ctx, `
		update stack_templates
		set
			pending_plan_run_id = '',
			pending_plan_template_revision_id = '',
			pending_plan_config_json = null,
			pending_plan_at = null
		where tenant_id = $1 and pending_plan_run_id = $2
	`, tenantID, runID); err != nil {
		return fmt.Errorf("clear pending plan: %w", err)
	}
	return nil
}

type stackTemplateLastAppliedWriter interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
}

type stackTemplateLifecycleWriter interface {
	stackTemplateLastAppliedWriter
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// recordStackTemplateLastApplied makes the run the stack template's live
// state. runStatus is the status the run must be in, so a run that has
// already moved on cannot become live.
func recordStackTemplateLastApplied(ctx context.Context, writer stackTemplateLastAppliedWriter, tenantID domain.TenantID, stackTemplateID domain.StackTemplateID, runID domain.TemplateRunID, runStatus domain.TemplateRunStatus) error {
	commandTag, err := writer.Exec(ctx, `
		update stack_templates
		set
			last_applied_run_id = template_runs.id,
			last_applied_template_revision_id = template_runs.template_revision_id,
			last_applied_config_json = template_runs.config_json,
			last_applied_at = now()
		from template_runs
		where stack_templates.tenant_id = $1
			and stack_templates.id = $2
			and template_runs.tenant_id = stack_templates.tenant_id
			and template_runs.id = $3
			and template_runs.stack_template_id = stack_templates.id
			and template_runs.operation = $4
			and template_runs.status = $5
	`, tenantID, stackTemplateID, runID, domain.OperationApply, runStatus)
	if err != nil {
		return fmt.Errorf("record stack template last applied: %w", err)
	}
	if commandTag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func recordStackTemplateLifecycle(ctx context.Context, writer stackTemplateLastAppliedWriter, tenantID domain.TenantID, stackTemplateID domain.StackTemplateID, lifecycle domain.StackTemplateLifecycle) error {
	commandTag, err := writer.Exec(ctx, `
		update stack_templates
		set lifecycle = $1
		where tenant_id = $2
			and id = $3
	`, lifecycle, tenantID, stackTemplateID)
	if err != nil {
		return fmt.Errorf("record stack template lifecycle: %w", err)
	}
	if commandTag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func recordInterruptedDestroyLifecycle(ctx context.Context, writer stackTemplateLifecycleWriter, tenantID domain.TenantID, stackTemplateID domain.StackTemplateID) error {
	var lifecycle domain.StackTemplateLifecycle
	err := writer.QueryRow(ctx, `
		select lifecycle
		from stack_templates
		where tenant_id = $1 and id = $2
		for update
	`, tenantID, stackTemplateID).Scan(&lifecycle)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("read interrupted destroy stack template lifecycle: %w", err)
	}
	if lifecycle != domain.StackTemplateDestroying {
		return nil
	}
	return recordStackTemplateLifecycle(ctx, writer, tenantID, stackTemplateID, domain.StackTemplateFailed)
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

func scanStackTemplate(scanner stackTemplateScanner) (domain.StackTemplate, error) {
	var stackTemplate domain.StackTemplate
	var configJSON []byte
	var desiredConfigJSON []byte
	var lastAppliedConfigJSON []byte
	var lastPlannedConfigJSON []byte
	var lastAppliedAt sql.NullTime
	var lastPlannedAt sql.NullTime

	if err := scanner.Scan(
		&stackTemplate.ID,
		&stackTemplate.TenantID,
		&stackTemplate.StackID,
		&stackTemplate.ComponentKey,
		&stackTemplate.SourceTemplateID,
		&stackTemplate.DesiredTemplateRevisionID,
		&stackTemplate.LastAppliedTemplateRevisionID,
		&stackTemplate.WorkspaceName,
		&configJSON,
		&desiredConfigJSON,
		&stackTemplate.LastAppliedRunID,
		&lastAppliedConfigJSON,
		&lastAppliedAt,
		&stackTemplate.PendingPlanRunID,
		&stackTemplate.PendingPlanTemplateRevisionID,
		&lastPlannedConfigJSON,
		&lastPlannedAt,
		&stackTemplate.CreatedBy,
		&stackTemplate.Lifecycle,
	); err != nil {
		return domain.StackTemplate{}, fmt.Errorf("scan stack template: %w", err)
	}
	stackTemplate.InstalledConfigJSON = configJSON
	stackTemplate.DesiredConfigJSON = desiredConfigJSON
	// A nil scan target stays nil rather than becoming an empty RawMessage, so
	// the state comparisons can tell "never recorded" from "recorded as '{}'".
	if lastAppliedConfigJSON != nil {
		stackTemplate.LastAppliedConfigJSON = lastAppliedConfigJSON
	}
	if lastPlannedConfigJSON != nil {
		stackTemplate.PendingPlanConfigJSON = lastPlannedConfigJSON
	}
	if lastAppliedAt.Valid {
		stackTemplate.LastAppliedAt = lastAppliedAt.Time
	}
	if lastPlannedAt.Valid {
		stackTemplate.PendingPlanAt = lastPlannedAt.Time
	}
	return stackTemplate, nil
}

func scanTemplateRevision(scanner templateScanner) (domain.TemplateRevision, error) {
	var templateRevision domain.TemplateRevision
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
		return domain.TemplateRevision{}, err
	}
	if err := json.Unmarshal(tagsJSON, &templateRevision.Tags); err != nil {
		return domain.TemplateRevision{}, fmt.Errorf("unmarshal template revision tags: %w", err)
	}
	return templateRevision, nil
}

func scanStack(scanner stackScanner) (domain.Stack, error) {
	var stack domain.Stack
	var tagsJSON []byte
	var credentialIDsJSON []byte

	var status string

	if err := scanner.Scan(
		&stack.ID,
		&stack.TenantID,
		&stack.Name,
		&stack.Slug,
		&status,
		&tagsJSON,
		&credentialIDsJSON,
		&stack.CreatedBy,
		&stack.CreatedAt,
	); err != nil {
		return domain.Stack{}, err
	}
	stack.Status = domain.StackStatus(status)
	if err := json.Unmarshal(tagsJSON, &stack.Tags); err != nil {
		return domain.Stack{}, fmt.Errorf("unmarshal stack tags: %w", err)
	}
	if err := json.Unmarshal(credentialIDsJSON, &stack.DefaultCredentialIDs); err != nil {
		return domain.Stack{}, fmt.Errorf("unmarshal stack credential IDs: %w", err)
	}
	return stack, nil
}

func (store *Store) getStack(ctx context.Context, tenantID domain.TenantID, stackID domain.StackID) (domain.Stack, error) {
	row := store.pool.QueryRow(ctx, `
		select
			id,
			tenant_id,
			name,
			slug,
			status,
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
		return domain.Stack{}, app.ErrNotFound
	}
	if err != nil {
		return domain.Stack{}, fmt.Errorf("get stack: %w", err)
	}
	return stack, nil
}

func duplicateConstraint(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == constraint
}

func upsertSourceTemplate(ctx context.Context, tx pgx.Tx, templateRevision domain.TemplateRevision) (domain.SourceTemplateID, error) {
	// Identity is minted by the caller (see the deterministic hash in the template sync
	// activity); the store persists it rather than inventing one, so a missing ID is a
	// programming error instead of a silently empty primary key.
	sourceTemplateID := templateRevision.SourceTemplateID
	if sourceTemplateID == "" {
		return "", errors.New("upsert source template: source template ID is required")
	}

	var persistedID domain.SourceTemplateID
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

func recordLatestTemplateRevision(ctx context.Context, tx pgx.Tx, templateRevision domain.TemplateRevision) error {
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

func defaultJSON(input json.RawMessage) json.RawMessage {
	if len(input) == 0 {
		return json.RawMessage(`{}`)
	}
	return input
}

// nullJSON keeps an absent config absent. The snapshot config columns are
// nullable precisely so "never recorded" stays distinguishable from "recorded
// as the empty object", which is what an all-optional template's config is, so
// empty must not be coerced to '{}' the way defaultJSON does.
func nullJSON(input json.RawMessage) any {
	if len(input) == 0 {
		return nil
	}
	return input
}

func insertTemplateRevision(ctx context.Context, tx pgx.Tx, templateRevision domain.TemplateRevision, tagsJSON []byte) (domain.TemplateRevision, bool, error) {
	var inserted domain.TemplateRevision
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
			return domain.TemplateRevision{}, false, fmt.Errorf("unmarshal inserted template revision tags: %w", err)
		}
		return inserted, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return domain.TemplateRevision{}, false, fmt.Errorf("insert template revision: %w", err)
	}

	selected, err := selectTemplateRevisionByIdentity(ctx, tx, templateRevision)
	if err != nil {
		return domain.TemplateRevision{}, false, err
	}
	return selected, false, nil
}

func selectTemplateRevisionByIdentity(ctx context.Context, tx pgx.Tx, templateRevision domain.TemplateRevision) (domain.TemplateRevision, error) {
	var selected domain.TemplateRevision
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
		return domain.TemplateRevision{}, app.ErrNotFound
	}
	if err != nil {
		return domain.TemplateRevision{}, fmt.Errorf("select template revision by identity: %w", err)
	}
	if err := json.Unmarshal(tagsJSON, &selected.Tags); err != nil {
		return domain.TemplateRevision{}, fmt.Errorf("unmarshal selected template revision tags: %w", err)
	}
	return selected, nil
}

func terminalTemplateRegistrationStatus(status domain.TemplateRegistrationStatus) bool {
	switch status {
	case domain.TemplateRegistrationCompleted, domain.TemplateRegistrationInvalid, domain.TemplateRegistrationFailed:
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
