package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/vishu42/tflive/internal/app"
	"github.com/vishu42/tflive/internal/domain"
	"github.com/vishu42/tflive/internal/queue"
)

const (
	requesterSubject = domain.UserID("6fdb4b4c-2a8f-4cf7-945f-38f67f6a0e91")
	approverSubject  = domain.UserID("cb4afba6-d18d-496f-80ce-8a50b94f09be")
)

var (
	_ app.StackRepository                    = (*Store)(nil)
	_ app.StackTemplateRepository            = (*Store)(nil)
	_ app.StackTemplateInstaller             = (*Store)(nil)
	_ app.TemplateRevisionMetadataRepository = (*Store)(nil)
	_ app.TemplateRunRepository              = (*Store)(nil)
	_ app.TemplateRegistrationRepository     = (*Store)(nil)
	_ app.TemplateRevisionRepository         = (*Store)(nil)
	_ app.AuditRepository                    = (*Store)(nil)
	_ interface {
		RecordTemplateRunStatus(context.Context, domain.TemplateRunStatusActivityInput) error
	} = (*Store)(nil)
)

var testSchemaCounter uint64

func TestMigrateAppliesSchema(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openTestPool(t, ctx)

	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate returned error: %v", err)
	}

	for _, table := range []string{
		"schema_migrations",
		"stacks",
		"stack_templates",
		"template_runs",
		"template_run_approvals",
		"template_run_logs",
		"source_templates",
		"template_revisions",
		"template_registrations",
		"template_variables",
		"work_queue",
		"security_audit_log",
		"users",
	} {
		table := table
		t.Run(table, func(t *testing.T) {
			t.Parallel()

			var exists bool
			err := pool.QueryRow(ctx, `
				select exists (
					select 1
					from information_schema.tables
					where table_schema = current_schema()
						and table_name = $1
				)
			`, table).Scan(&exists)
			if err != nil {
				t.Fatalf("query table existence: %v", err)
			}
			if !exists {
				t.Fatalf("expected table %q to exist", table)
			}
		})
	}
}

func TestWorkflowOutboxMigrationDefinesDurablePendingQueue(t *testing.T) {
	t.Parallel()

	migration, err := migrationFiles.ReadFile("migrations/0007_workflow_outbox.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	sql := strings.ToLower(string(migration))

	for _, snippet := range []string{
		"create table workflow_outbox",
		"event_type text not null",
		"aggregate_id text not null references template_runs",
		"available_at timestamptz not null",
		"claimed_until timestamptz",
		"processed_at timestamptz",
		"where processed_at is null",
		"insert into workflow_outbox",
		"from template_runs",
		"where status = 'queued'",
	} {
		if !strings.Contains(sql, snippet) {
			t.Fatalf("migration 0007 is missing durable outbox snippet %q", snippet)
		}
	}
}

func TestRetireWorkflowOutboxBackfillsPendingStarts(t *testing.T) {
	ctx := context.Background()
	pool := openTestPool(t, ctx)

	if _, err := pool.Exec(ctx, `
		create table schema_migrations (
			version text primary key,
			applied_at timestamptz not null default now()
		)
	`); err != nil {
		t.Fatalf("create schema migrations table: %v", err)
	}
	for _, version := range []string{
		"0001_app_repositories",
		"0002_template_run_logs",
		"0003_template_registration",
		"0004_stacks",
		"0005_stack_templates_created_by",
		"0006_template_sources_and_revision_pointers",
		"0007_workflow_outbox",
		"0008_authorization_outbox",
		"0009_security_audit",
		"0010_scoped_credentials",
		"0011_terraform_command_statuses",
		"0012_work_queue",
		"0013_stack_status",
	} {
		if err := applyMigration(ctx, pool, version, "migrations/"+version+".sql"); err != nil {
			t.Fatalf("apply migration %s: %v", version, err)
		}
	}

	if _, err := pool.Exec(ctx, `
		insert into template_revisions (
			id, tenant_id, repo_owner, repo_name, source_ref,
			resolved_commit_sha, root_path, name, status
		) values
			('revision-pending', 'tenant-1', 'org', 'repo', 'main', 'sha-pending', 'modules/demo', 'Demo', 'active'),
			('revision-processed', 'tenant-1', 'other-org', 'other-repo', 'release', 'sha-processed', 'modules/other', 'Other', 'active')
	`); err != nil {
		t.Fatalf("seed template revisions: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into template_runs (
			id, tenant_id, stack_template_id, template_revision_id,
			operation, selected_ref, workspace_name, config_json, status, trigger_actor
		) values
			('run-pending', 'tenant-1', 'stack-template-pending', 'revision-pending', 'plan', 'main', 'demo-workspace', '{"region":"us-east-1"}', 'queued', 'user-1'),
			('run-processed', 'tenant-1', 'stack-template-processed', 'revision-processed', 'apply', 'release', 'other-workspace', '{"region":"us-west-2"}', 'queued', 'user-1')
	`); err != nil {
		t.Fatalf("seed template runs: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into workflow_outbox (id, event_type, aggregate_id, processed_at)
		values
			('template-run/tenant-1/run-pending', 'start_template_run', 'run-pending', null),
			('template-run/tenant-1/run-processed', 'start_template_run', 'run-processed', now())
	`); err != nil {
		t.Fatalf("seed workflow outbox: %v", err)
	}

	if err := applyMigration(ctx, pool, "0014_retire_workflow_outbox", "migrations/0014_retire_workflow_outbox.sql"); err != nil {
		t.Fatalf("apply migration 0014_retire_workflow_outbox: %v", err)
	}

	var (
		resourceKey string
		actor       string
		tenantID    string
		payload     []byte
	)
	if err := pool.QueryRow(ctx, `
		select resource_key, actor_subject, tenant_id, payload
		from work_queue
		where kind = 'start_template_run'
	`).Scan(&resourceKey, &actor, &tenantID, &payload); err != nil {
		t.Fatalf("read backfilled work queue row: %v", err)
	}

	var input domain.TemplateRunWorkflowInput
	if err := json.Unmarshal(payload, &input); err != nil {
		t.Fatalf("decode work queue payload: %v", err)
	}
	if resourceKey != "run:tenant-1/run-pending" {
		t.Fatalf("resource key = %q, want %q", resourceKey, "run:tenant-1/run-pending")
	}
	if actor != "user-1" {
		t.Fatalf("actor subject = %q, want user-1", actor)
	}
	if tenantID != "tenant-1" {
		t.Fatalf("tenant ID = %q, want tenant-1", tenantID)
	}
	if input.RunID != "run-pending" {
		t.Fatalf("RunID = %q, want run-pending", input.RunID)
	}
	if input.TenantID != "tenant-1" {
		t.Fatalf("TenantID = %q, want tenant-1", input.TenantID)
	}
	if input.StackTemplateID != "stack-template-pending" {
		t.Fatalf("StackTemplateID = %q, want stack-template-pending", input.StackTemplateID)
	}
	if input.Operation != domain.OperationPlan {
		t.Fatalf("Operation = %q, want %q", input.Operation, domain.OperationPlan)
	}
	if input.SelectedRef != "main" {
		t.Fatalf("SelectedRef = %q, want main", input.SelectedRef)
	}
	if input.WorkspaceName != "demo-workspace" {
		t.Fatalf("WorkspaceName = %q, want demo-workspace", input.WorkspaceName)
	}
	if input.RepoOwner != "org" {
		t.Fatalf("RepoOwner = %q, want org", input.RepoOwner)
	}
	if input.RepoName != "repo" {
		t.Fatalf("RepoName = %q, want repo", input.RepoName)
	}
	if input.RootPath != "modules/demo" {
		t.Fatalf("RootPath = %q, want modules/demo", input.RootPath)
	}
	if !sameJSON(t, input.ConfigJSON, []byte(`{"region":"us-east-1"}`)) {
		t.Fatalf("ConfigJSON = %s, want region config", input.ConfigJSON)
	}

	var queuedStarts int
	if err := pool.QueryRow(ctx, `select count(*) from work_queue where kind = 'start_template_run'`).Scan(&queuedStarts); err != nil {
		t.Fatalf("count backfilled work queue rows: %v", err)
	}
	if queuedStarts != 1 {
		t.Fatalf("backfilled work queue rows = %d, want 1", queuedStarts)
	}

	var workflowOutbox *string
	if err := pool.QueryRow(ctx, `select to_regclass('workflow_outbox')::text`).Scan(&workflowOutbox); err != nil {
		t.Fatalf("check workflow outbox relation: %v", err)
	}
	if workflowOutbox != nil {
		t.Fatalf("workflow_outbox still exists as %q", *workflowOutbox)
	}
}

func TestAuthorizationOutboxMigrationDefinesDurablePendingQueue(t *testing.T) {
	t.Parallel()

	migration, err := migrationFiles.ReadFile("migrations/0008_authorization_outbox.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	sql := strings.ToLower(string(migration))

	for _, snippet := range []string{
		"create table authorization_outbox",
		"operation text not null check (operation in ('grant', 'revoke'))",
		"subject text not null",
		"stack text not null",
		"role text not null check (role in ('owner', 'operator', 'approver', 'viewer'))",
		"claimed_until timestamptz",
		"processed_at timestamptz",
		"failed_at timestamptz",
		"where processed_at is null and failed_at is null",
	} {
		if !strings.Contains(sql, snippet) {
			t.Fatalf("migration 0008 is missing durable outbox snippet %q", snippet)
		}
	}
}

func TestSecurityAuditLogMigrationDefinesAppendOnlyTable(t *testing.T) {
	t.Parallel()

	migration, err := migrationFiles.ReadFile("migrations/0009_security_audit.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	sql := strings.ToLower(string(migration))

	for _, snippet := range []string{
		"create table security_audit_log",
		"id              bigserial primary key",
		"recorded_at     timestamptz not null default now()",
		"actor_subject   text not null",
		"action          text not null",
		"target_user     text not null default ''",
		"tenant_id       text not null",
		"stack_id        text not null default ''",
		"old_role        text not null default ''",
		"new_role        text not null default ''",
		"outcome         text not null check (outcome in ('success', 'failure'))",
		"correlation_id  text not null default ''",
		"security_audit_log_actor_idx",
		"security_audit_log_tenant_idx",
		"security_audit_log_stack_idx",
	} {
		if !strings.Contains(sql, snippet) {
			t.Fatalf("migration 0009 is missing snippet %q", snippet)
		}
	}
}

func TestTemplateRevisionTablesUseRevisionNames(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)

	var tableName string
	err := pool.QueryRow(ctx, `
		select table_name
		from information_schema.tables
		where table_schema = current_schema()
			and table_name = 'template_revisions'
	`).Scan(&tableName)
	if err != nil {
		t.Fatalf("template_revisions table lookup: %v", err)
	}
	if tableName != "template_revisions" {
		t.Fatalf("table = %q, want template_revisions", tableName)
	}

	var oldTableExists bool
	err = pool.QueryRow(ctx, `
		select exists (
			select 1
			from information_schema.tables
			where table_schema = current_schema()
				and table_name = 'templates'
		)
	`).Scan(&oldTableExists)
	if err != nil {
		t.Fatalf("templates table lookup: %v", err)
	}
	if oldTableExists {
		t.Fatal("old templates table still exists")
	}
}

func TestTemplateSourceMigrationKeepsLegacyRevisionBackfillSQL(t *testing.T) {
	t.Parallel()

	migration, err := migrationFiles.ReadFile("migrations/0006_template_sources_and_revision_pointers.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	sql := string(migration)

	if strings.Contains(sql, "chr(0)") {
		t.Fatal("migration 0006 must not use chr(0); Postgres text does not permit NUL characters")
	}

	for _, snippet := range []string{
		"alter table templates rename to template_revisions",
		"alter table stack_templates rename column template_id to template_revision_id",
		"source_template_id = tr.source_template_id",
		"desired_template_revision_id = stack_templates.template_revision_id",
		"last_applied_template_revision_id = case",
	} {
		if !strings.Contains(sql, snippet) {
			t.Fatalf("migration 0006 is missing legacy backfill snippet %q", snippet)
		}
	}
}

func TestTemplateSourceMigrationBackfillsLegacyStackTemplateRevisionPointers(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openTestPool(t, ctx)

	_, err := pool.Exec(ctx, `
		create table schema_migrations (
			version text primary key,
			applied_at timestamptz not null default now()
		);
		create table template_revisions (
			id text primary key,
			tenant_id text not null,
			repo_owner text not null,
			repo_name text not null,
			source_ref text not null,
			resolved_commit_sha text not null,
			root_path text not null,
			name text not null,
			description text not null default '',
			tags_json jsonb not null default '[]'::jsonb,
			status text not null,
			created_at timestamptz not null default now()
		);
		create unique index template_revisions_source_identity_idx on template_revisions (
			tenant_id,
			repo_owner,
			repo_name,
			root_path,
			resolved_commit_sha
		);
		create table stack_templates (
			id text primary key,
			tenant_id text not null,
			stack_id text not null,
			template_revision_id text not null,
			selected_ref text not null,
			workspace_name text not null,
			config_json jsonb not null default '{}'::jsonb,
			last_applied_run_id text not null default '',
			last_applied_ref text not null default '',
			last_applied_at timestamptz,
			created_by text not null default '',
			lifecycle text not null
		);
		create table template_runs (
			id text primary key,
			tenant_id text not null,
			stack_template_id text not null,
			operation text not null,
			selected_ref text not null,
			resolved_commit_sha text not null default '',
			workspace_name text not null,
			backend_type text not null default '',
			backend_config_hash text not null default '',
			status text not null,
			trigger_actor text not null,
			started_at timestamptz,
			completed_at timestamptz,
			error_summary text not null default '',
			cancellation_requested_by text not null default '',
			cancellation_reason text not null default '',
			cancellation_requested_at timestamptz
		);
		insert into template_revisions (
			id,
			tenant_id,
			repo_owner,
			repo_name,
			source_ref,
			resolved_commit_sha,
			root_path,
			name,
			status
		) values (
			'template_rev_1',
			'tenant_123',
			'acme',
			'infra',
			'main',
			'sha-1',
			'vpc',
			'vpc',
			'active'
		);
		insert into stack_templates (
			id,
			tenant_id,
			stack_id,
			template_revision_id,
			selected_ref,
			workspace_name,
			config_json,
			last_applied_run_id,
			last_applied_ref,
			lifecycle
		) values (
			'stack_template_123',
			'tenant_123',
			'stack_123',
			'template_rev_1',
			'main',
			'meg_acme_primary',
			'{"region":"us-east-1"}'::jsonb,
			'run_123',
			'main',
			'active'
		);
	`)
	if err != nil {
		t.Fatalf("seed legacy schema: %v", err)
	}

	if err := applyMigration(ctx, pool, "0006_template_sources_and_revision_pointers", "migrations/0006_template_sources_and_revision_pointers.sql"); err != nil {
		t.Fatalf("apply migration 0006: %v", err)
	}

	var sourceTemplateID domain.SourceTemplateID
	var desiredTemplateRevisionID domain.TemplateRevisionID
	var lastAppliedTemplateRevisionID domain.TemplateRevisionID
	var desiredConfigJSON []byte
	var componentKey string
	err = pool.QueryRow(ctx, `
		select
			source_template_id,
			desired_template_revision_id,
			last_applied_template_revision_id,
			desired_config_json,
			component_key
		from stack_templates
		where id = 'stack_template_123'
	`).Scan(
		&sourceTemplateID,
		&desiredTemplateRevisionID,
		&lastAppliedTemplateRevisionID,
		&desiredConfigJSON,
		&componentKey,
	)
	if err != nil {
		t.Fatalf("get migrated stack template: %v", err)
	}
	if sourceTemplateID == "" {
		t.Fatal("source template ID was not backfilled")
	}
	if desiredTemplateRevisionID != domain.TemplateRevisionID("template_rev_1") {
		t.Fatalf("desired template revision ID = %q, want template_rev_1", desiredTemplateRevisionID)
	}
	if lastAppliedTemplateRevisionID != domain.TemplateRevisionID("template_rev_1") {
		t.Fatalf("last applied template revision ID = %q, want template_rev_1", lastAppliedTemplateRevisionID)
	}
	if string(desiredConfigJSON) != `{"region": "us-east-1"}` {
		t.Fatalf("desired config JSON = %s, want region config", desiredConfigJSON)
	}
	if componentKey != "stack_template_123" {
		t.Fatalf("component key = %q, want stack_template_123", componentKey)
	}
}

func TestCreateAndGetTemplateRegistration(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)
	requestedAt := time.Date(2026, 7, 6, 11, 30, 0, 123456000, time.UTC)

	registration := domain.TemplateRegistration{
		ID:          domain.TemplateRegistrationID("template_registration_123"),
		TenantID:    domain.TenantID("tenant_123"),
		RepoOwner:   "acme",
		RepoName:    "infra-templates",
		SourceRef:   "v0.0.1",
		RootPath:    "modules/vpc",
		Status:      domain.TemplateRegistrationPending,
		RequestedBy: domain.UserID("user_123"),
		RequestedAt: requestedAt,
	}

	if err := store.CreateTemplateRegistration(ctx, registration); err != nil {
		t.Fatalf("CreateTemplateRegistration returned error: %v", err)
	}

	got, err := store.GetTemplateRegistration(ctx, domain.TenantID("tenant_123"), domain.TemplateRegistrationID("template_registration_123"))
	if err != nil {
		t.Fatalf("GetTemplateRegistration returned error: %v", err)
	}
	got.RequestedAt = got.RequestedAt.UTC()
	got.CompletedAt = got.CompletedAt.UTC()

	if got != registration {
		t.Fatalf("registration = %#v, want %#v", got, registration)
	}

	_, err = store.GetTemplateRegistration(ctx, domain.TenantID("tenant_456"), domain.TemplateRegistrationID("template_registration_123"))
	if !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("other tenant error = %v, want app.ErrNotFound", err)
	}
}

func TestRecordTemplateRegistrationStatusUpdatesTerminalFields(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)
	registration := domain.TemplateRegistration{
		ID:          domain.TemplateRegistrationID("template_registration_123"),
		TenantID:    domain.TenantID("tenant_123"),
		RepoOwner:   "acme",
		RepoName:    "infra-templates",
		SourceRef:   "v0.0.1",
		RootPath:    "modules/vpc",
		Status:      domain.TemplateRegistrationPending,
		RequestedBy: domain.UserID("user_123"),
		RequestedAt: time.Date(2026, 7, 6, 11, 30, 0, 0, time.UTC),
	}
	if err := store.CreateTemplateRegistration(ctx, registration); err != nil {
		t.Fatalf("CreateTemplateRegistration returned error: %v", err)
	}

	err := store.RecordTemplateRegistrationStatus(ctx, domain.TemplateRegistrationStatusActivityInput{
		RegistrationID:     domain.TemplateRegistrationID("template_registration_123"),
		TenantID:           domain.TenantID("tenant_123"),
		Status:             domain.TemplateRegistrationCompleted,
		TemplateRevisionID: domain.TemplateRevisionID("template_123"),
		ResolvedCommitSHA:  "abc123",
	})
	if err != nil {
		t.Fatalf("RecordTemplateRegistrationStatus returned error: %v", err)
	}

	got, err := store.GetTemplateRegistration(ctx, domain.TenantID("tenant_123"), domain.TemplateRegistrationID("template_registration_123"))
	if err != nil {
		t.Fatalf("GetTemplateRegistration returned error: %v", err)
	}
	if got.Status != domain.TemplateRegistrationCompleted {
		t.Fatalf("status = %q, want completed", got.Status)
	}
	if got.TemplateRevisionID != domain.TemplateRevisionID("template_123") {
		t.Fatalf("template id = %q, want template_123", got.TemplateRevisionID)
	}
	if got.ResolvedCommitSHA != "abc123" {
		t.Fatalf("resolved sha = %q, want abc123", got.ResolvedCommitSHA)
	}
	if got.CompletedAt.IsZero() {
		t.Fatal("completed_at was not set for terminal registration status")
	}
}

func TestUpsertTemplateRevisionWithVariablesCreatesAndReusesImmutableRevision(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)
	createdAt := time.Date(2026, 7, 6, 12, 0, 0, 123456000, time.UTC)
	template := domain.TemplateRevision{
		ID:                domain.TemplateRevisionID("template_123"),
		TenantID:          domain.TenantID("tenant_123"),
		RepoOwner:         "acme",
		RepoName:          "infra-templates",
		SourceRef:         "v0.0.1",
		ResolvedCommitSHA: "abc123",
		RootPath:          "modules/vpc",
		SourceTemplateID:  domain.SourceTemplateID("source_template_vpc"),
		Name:              "vpc",
		Description:       "Creates a VPC",
		Tags:              []string{"network", "aws"},
		Status:            domain.TemplateRevisionActive,
		CreatedAt:         createdAt,
	}
	variables := []domain.TemplateVariable{
		{
			TemplateRevisionID: domain.TemplateRevisionID("template_123"),
			Name:               "region",
			TypeExpression:     "string",
			Description:        "AWS region",
			Required:           true,
		},
		{
			TemplateRevisionID: domain.TemplateRevisionID("template_123"),
			Name:               "cidr",
			TypeExpression:     "string",
			HasDefault:         true,
			HasValidation:      true,
		},
	}

	created, err := store.UpsertTemplateRevisionWithVariables(ctx, template, variables)
	if err != nil {
		t.Fatalf("UpsertTemplateRevisionWithVariables returned error: %v", err)
	}
	if created.ID != domain.TemplateRevisionID("template_123") {
		t.Fatalf("created template revision ID = %q, want template_123", created.ID)
	}

	reused, err := store.UpsertTemplateRevisionWithVariables(ctx, domain.TemplateRevision{
		ID:                domain.TemplateRevisionID("template_456"),
		TenantID:          domain.TenantID("tenant_123"),
		RepoOwner:         "acme",
		RepoName:          "infra-templates",
		SourceRef:         "v0.0.1",
		ResolvedCommitSHA: "abc123",
		RootPath:          "modules/vpc",
		SourceTemplateID:  domain.SourceTemplateID("source_template_vpc"),
		Name:              "vpc moved tag",
		Status:            domain.TemplateRevisionActive,
	}, nil)
	if err != nil {
		t.Fatalf("second UpsertTemplateRevisionWithVariables returned error: %v", err)
	}
	if reused.ID != domain.TemplateRevisionID("template_123") {
		t.Fatalf("reused template revision ID = %q, want template_123", reused.ID)
	}

	gotVariables, err := store.GetTemplateRevisionVariables(ctx, domain.TenantID("tenant_123"), domain.TemplateRevisionID("template_123"))
	if err != nil {
		t.Fatalf("GetTemplateRevisionVariables returned error: %v", err)
	}
	if len(gotVariables) != 2 {
		t.Fatalf("len(gotVariables) = %d, want 2", len(gotVariables))
	}
	if gotVariables[0].Name != "cidr" || gotVariables[1].Name != "region" {
		t.Fatalf("variables ordered by name = %#v", gotVariables)
	}
}

func TestGetTemplateRevisionVariablesReturnsNotFoundForOtherTenant(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)
	_, err := store.UpsertTemplateRevisionWithVariables(ctx, domain.TemplateRevision{
		ID:                domain.TemplateRevisionID("template_123"),
		TenantID:          domain.TenantID("tenant_123"),
		RepoOwner:         "acme",
		RepoName:          "infra-templates",
		SourceRef:         "v0.0.1",
		ResolvedCommitSHA: "abc123",
		RootPath:          "modules/vpc",
		SourceTemplateID:  domain.SourceTemplateID("source_template_vpc"),
		Name:              "vpc",
		Status:            domain.TemplateRevisionActive,
	}, nil)
	if err != nil {
		t.Fatalf("UpsertTemplateRevisionWithVariables returned error: %v", err)
	}

	_, err = store.GetTemplateRevisionVariables(ctx, domain.TenantID("tenant_456"), domain.TemplateRevisionID("template_123"))
	if !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("error = %v, want app.ErrNotFound", err)
	}
}

func TestCreateAndGetStack(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)
	createdAt := time.Date(2026, 7, 6, 13, 30, 0, 123456000, time.UTC)

	stack := domain.Stack{
		ID:                   domain.StackID("stack_123"),
		TenantID:             domain.TenantID("tenant_123"),
		Name:                 "Acme Prod",
		Slug:                 "acme-prod",
		Tags:                 map[string]string{"env": "prod"},
		DefaultCredentialIDs: []domain.CredentialSetID{domain.CredentialSetID("credential_123")},
		CreatedBy:            domain.UserID("user_123"),
		CreatedAt:            createdAt,
	}

	if err := store.CreateStack(ctx, stack); err != nil {
		t.Fatalf("CreateStack returned error: %v", err)
	}

	got, err := store.GetStack(ctx, domain.TenantID("tenant_123"), domain.StackID("stack_123"))
	if err != nil {
		t.Fatalf("GetStack returned error: %v", err)
	}

	if got.ID != stack.ID || got.TenantID != stack.TenantID || got.Name != stack.Name || got.Slug != stack.Slug || got.CreatedBy != stack.CreatedBy {
		t.Fatalf("stack = %#v, want %#v", got, stack)
	}
	if got.Tags["env"] != "prod" {
		t.Fatalf("tags = %#v", got.Tags)
	}
	if len(got.DefaultCredentialIDs) != 1 || got.DefaultCredentialIDs[0] != domain.CredentialSetID("credential_123") {
		t.Fatalf("default credential IDs = %#v", got.DefaultCredentialIDs)
	}
	if !got.CreatedAt.Equal(createdAt) {
		t.Fatalf("created at = %v, want %v", got.CreatedAt, createdAt)
	}
}

func TestCreateStackReturnsDuplicateSlugConflict(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)

	first := domain.Stack{
		ID:        domain.StackID("stack_123"),
		TenantID:  domain.TenantID("tenant_123"),
		Name:      "Acme Prod",
		Slug:      "acme-prod",
		CreatedBy: domain.UserID("user_123"),
		CreatedAt: time.Now().UTC(),
	}
	if err := store.CreateStack(ctx, first); err != nil {
		t.Fatalf("CreateStack first returned error: %v", err)
	}

	second := first
	second.ID = domain.StackID("stack_456")
	err := store.CreateStack(ctx, second)
	if !errors.Is(err, app.ErrDuplicateStackSlug) {
		t.Fatalf("error = %v, want app.ErrDuplicateStackSlug", err)
	}
}

func TestListStacksReturnsTenantScopedStacksNewestFirst(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)
	older := domain.Stack{
		ID:        domain.StackID("stack_a"),
		TenantID:  domain.TenantID("tenant_123"),
		Name:      "Older Stack",
		Slug:      "older-stack",
		CreatedBy: domain.UserID("user_123"),
		CreatedAt: time.Date(2026, 7, 6, 10, 0, 0, 0, time.UTC),
	}
	newer := domain.Stack{
		ID:        domain.StackID("stack_b"),
		TenantID:  domain.TenantID("tenant_123"),
		Name:      "Newer Stack",
		Slug:      "newer-stack",
		CreatedBy: domain.UserID("user_123"),
		CreatedAt: time.Date(2026, 7, 6, 10, 0, 0, 0, time.UTC),
	}
	otherTenant := domain.Stack{
		ID:        domain.StackID("stack_other"),
		TenantID:  domain.TenantID("tenant_456"),
		Name:      "Other Stack",
		Slug:      "other-stack",
		CreatedBy: domain.UserID("user_456"),
		CreatedAt: time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC),
	}
	for _, stack := range []domain.Stack{older, newer, otherTenant} {
		if err := store.CreateStack(ctx, stack); err != nil {
			t.Fatalf("CreateStack(%s) returned error: %v", stack.ID, err)
		}
	}

	stacks, err := store.ListStacks(ctx, domain.TenantID("tenant_123"))
	if err != nil {
		t.Fatalf("ListStacks returned error: %v", err)
	}

	if len(stacks) != 2 {
		t.Fatalf("len(stacks) = %d, want 2: %#v", len(stacks), stacks)
	}
	if stacks[0].ID != newer.ID || stacks[1].ID != older.ID {
		t.Fatalf("stack order = %#v", stacks)
	}
}

func TestListStacksPageReturnsTenantStacksWithStableKeyset(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)
	older := domain.Stack{
		ID:        domain.StackID("stack_a"),
		TenantID:  domain.TenantID("tenant_123"),
		Name:      "Older Stack",
		Slug:      "older-stack",
		CreatedBy: domain.UserID("user_123"),
		CreatedAt: time.Date(2026, 7, 6, 10, 0, 0, 0, time.UTC),
	}
	newer := domain.Stack{
		ID:        domain.StackID("stack_b"),
		TenantID:  domain.TenantID("tenant_123"),
		Name:      "Newer Stack",
		Slug:      "newer-stack",
		CreatedBy: domain.UserID("user_123"),
		CreatedAt: time.Date(2026, 7, 6, 10, 0, 0, 0, time.UTC),
	}
	otherTenant := domain.Stack{
		ID:        domain.StackID("stack_other"),
		TenantID:  domain.TenantID("tenant_456"),
		Name:      "Other Stack",
		Slug:      "other-stack",
		CreatedBy: domain.UserID("user_456"),
		CreatedAt: time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC),
	}
	for _, stack := range []domain.Stack{older, newer, otherTenant} {
		if err := store.CreateStack(ctx, stack); err != nil {
			t.Fatalf("CreateStack(%s) returned error: %v", stack.ID, err)
		}
	}

	stacks, err := store.ListStacksPage(ctx, domain.TenantID("tenant_123"), nil, 1)
	if err != nil {
		t.Fatalf("ListStacksPage first page returned error: %v", err)
	}
	if len(stacks) != 1 || stacks[0].ID != newer.ID {
		t.Fatalf("first page = %#v, want newer stack", stacks)
	}
	cursor := &app.StackPageCursor{CreatedAt: stacks[0].CreatedAt, ID: stacks[0].ID}
	stacks, err = store.ListStacksPage(ctx, domain.TenantID("tenant_123"), cursor, 1)
	if err != nil {
		t.Fatalf("ListStacksPage second page returned error: %v", err)
	}
	if len(stacks) != 1 || stacks[0].ID != older.ID {
		t.Fatalf("second page = %#v, want older stack", stacks)
	}
	cursor = &app.StackPageCursor{CreatedAt: stacks[0].CreatedAt, ID: stacks[0].ID}
	stacks, err = store.ListStacksPage(ctx, domain.TenantID("tenant_123"), cursor, 1)
	if err != nil {
		t.Fatalf("ListStacksPage final page returned error: %v", err)
	}
	if len(stacks) != 0 {
		t.Fatalf("final page = %#v, want empty", stacks)
	}
}

func TestListStacksPageRejectsNonPositiveLimit(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)
	if _, err := store.ListStacksPage(ctx, domain.TenantID("tenant_123"), nil, 0); err == nil {
		t.Fatal("ListStacksPage() error = nil, want invalid limit error")
	}
}

func TestGetTemplateRevisionReturnsTenantScopedRevision(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)
	createdAt := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)
	template := domain.TemplateRevision{
		ID:                domain.TemplateRevisionID("template_123"),
		TenantID:          domain.TenantID("tenant_123"),
		RepoOwner:         "acme",
		RepoName:          "infra-templates",
		SourceRef:         "main",
		ResolvedCommitSHA: "abc123",
		RootPath:          ".",
		SourceTemplateID:  domain.SourceTemplateID("source_template_vpc"),
		Name:              "vpc",
		Tags:              []string{"network"},
		Status:            domain.TemplateRevisionActive,
		CreatedAt:         createdAt,
	}
	if _, err := store.UpsertTemplateRevisionWithVariables(ctx, template, nil); err != nil {
		t.Fatalf("UpsertTemplateRevisionWithVariables returned error: %v", err)
	}

	got, err := store.GetTemplateRevision(ctx, domain.TenantID("tenant_123"), domain.TemplateRevisionID("template_123"))
	if err != nil {
		t.Fatalf("GetTemplateRevision returned error: %v", err)
	}
	if got.ID != domain.TemplateRevisionID("template_123") || got.Status != domain.TemplateRevisionActive || got.Tags[0] != "network" {
		t.Fatalf("template = %#v", got)
	}

	_, err = store.GetTemplateRevision(ctx, domain.TenantID("tenant_456"), domain.TemplateRevisionID("template_123"))
	if !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("other tenant error = %v, want app.ErrNotFound", err)
	}
}

func TestListTemplateRevisionsReturnsTenantScopedRevisionsNewestFirst(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)
	older := domain.TemplateRevision{
		ID:                domain.TemplateRevisionID("template_older"),
		TenantID:          domain.TenantID("tenant_123"),
		RepoOwner:         "acme",
		RepoName:          "infra-templates",
		SourceRef:         "v0.0.1",
		ResolvedCommitSHA: "abc123",
		RootPath:          ".",
		SourceTemplateID:  domain.SourceTemplateID("source_template_older"),
		Name:              "older",
		Status:            domain.TemplateRevisionActive,
		CreatedAt:         time.Date(2026, 7, 6, 10, 0, 0, 0, time.UTC),
	}
	newer := domain.TemplateRevision{
		ID:                domain.TemplateRevisionID("template_newer"),
		TenantID:          domain.TenantID("tenant_123"),
		RepoOwner:         "acme",
		RepoName:          "infra-templates",
		SourceRef:         "v0.0.2",
		ResolvedCommitSHA: "def456",
		RootPath:          ".",
		SourceTemplateID:  domain.SourceTemplateID("source_template_newer"),
		Name:              "newer",
		Status:            domain.TemplateRevisionActive,
		CreatedAt:         time.Date(2026, 7, 6, 11, 0, 0, 0, time.UTC),
	}
	otherTenant := domain.TemplateRevision{
		ID:                domain.TemplateRevisionID("template_other"),
		TenantID:          domain.TenantID("tenant_456"),
		RepoOwner:         "acme",
		RepoName:          "infra-templates",
		SourceRef:         "v0.0.3",
		ResolvedCommitSHA: "ghi789",
		RootPath:          ".",
		SourceTemplateID:  domain.SourceTemplateID("source_template_other"),
		Name:              "other",
		Status:            domain.TemplateRevisionActive,
		CreatedAt:         time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC),
	}
	for _, template := range []domain.TemplateRevision{older, newer, otherTenant} {
		if _, err := store.UpsertTemplateRevisionWithVariables(ctx, template, nil); err != nil {
			t.Fatalf("UpsertTemplateRevisionWithVariables(%s) returned error: %v", template.ID, err)
		}
	}

	templates, err := store.ListTemplateRevisions(ctx, domain.TenantID("tenant_123"))
	if err != nil {
		t.Fatalf("ListTemplateRevisions returned error: %v", err)
	}

	if len(templates) != 2 {
		t.Fatalf("len(templates) = %d, want 2: %#v", len(templates), templates)
	}
	if templates[0].ID != domain.TemplateRevisionID("template_newer") || templates[1].ID != domain.TemplateRevisionID("template_older") {
		t.Fatalf("template order = %#v", templates)
	}
}

func TestCreateStackTemplateAndGetStackWithTemplates(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)

	stack := domain.Stack{
		ID:        domain.StackID("stack_123"),
		TenantID:  domain.TenantID("tenant_123"),
		Name:      "Acme Prod",
		Slug:      "acme-prod",
		CreatedBy: domain.UserID("user_123"),
		CreatedAt: time.Now().UTC(),
	}
	if err := store.CreateStack(ctx, stack); err != nil {
		t.Fatalf("CreateStack returned error: %v", err)
	}

	stackTemplate := domain.StackTemplate{
		ID:                        domain.StackTemplateID("stack_template_123"),
		TenantID:                  domain.TenantID("tenant_123"),
		StackID:                   domain.StackID("stack_123"),
		DesiredTemplateRevisionID: domain.TemplateRevisionID("template_123"),
		WorkspaceName:             "meg_acme_prod_late_123",
		InstalledConfigJSON:       json.RawMessage(`{"region":"us-east-1"}`),
		CreatedBy:                 domain.UserID("installer_123"),
		Lifecycle:                 domain.StackTemplateActive,
	}
	if err := store.CreateStackTemplate(ctx, stackTemplate); err != nil {
		t.Fatalf("CreateStackTemplate returned error: %v", err)
	}

	view, err := store.GetStackWithTemplates(ctx, domain.TenantID("tenant_123"), domain.StackID("stack_123"))
	if err != nil {
		t.Fatalf("GetStackWithTemplates returned error: %v", err)
	}
	if view.Stack.ID != domain.StackID("stack_123") {
		t.Fatalf("view stack ID = %q, want stack_123", view.Stack.ID)
	}
	if len(view.Templates) != 1 {
		t.Fatalf("len(view.Templates) = %d, want 1", len(view.Templates))
	}
	if view.Templates[0].TenantID != domain.TenantID("tenant_123") {
		t.Fatalf("template tenant ID = %q, want tenant_123", view.Templates[0].TenantID)
	}
	if view.Templates[0].CreatedBy != domain.UserID("installer_123") {
		t.Fatalf("template created by = %q, want installer_123", view.Templates[0].CreatedBy)
	}
	if string(view.Templates[0].InstalledConfigJSON) != `{"region": "us-east-1"}` {
		t.Fatalf("template config = %s", view.Templates[0].InstalledConfigJSON)
	}
}

func TestCreateStackTemplateAllowsRepeatedSourceWithDistinctComponentKeys(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)
	tenantID := domain.TenantID("tenant_123")
	sourceTemplateID := domain.SourceTemplateID("source_template_vpc")

	stack := domain.Stack{
		ID:        domain.StackID("stack_123"),
		TenantID:  tenantID,
		Name:      "Acme Prod",
		Slug:      "acme-prod",
		CreatedBy: domain.UserID("user_123"),
		CreatedAt: time.Now().UTC(),
	}
	if err := store.CreateStack(ctx, stack); err != nil {
		t.Fatalf("CreateStack returned error: %v", err)
	}

	template := domain.TemplateRevision{
		ID:                domain.TemplateRevisionID("template_rev_1"),
		TenantID:          tenantID,
		SourceTemplateID:  sourceTemplateID,
		RepoOwner:         "acme",
		RepoName:          "infra",
		SourceRef:         "main",
		ResolvedCommitSHA: "sha-1",
		RootPath:          "vpc",
		Name:              "vpc",
		Status:            domain.TemplateRevisionActive,
		CreatedAt:         time.Now().UTC(),
	}
	if _, err := store.UpsertTemplateRevisionWithVariables(ctx, template, nil); err != nil {
		t.Fatalf("UpsertTemplateRevisionWithVariables returned error: %v", err)
	}

	first := domain.StackTemplate{
		ID:                        domain.StackTemplateID("stack_template_primary"),
		TenantID:                  tenantID,
		StackID:                   stack.ID,
		SourceTemplateID:          sourceTemplateID,
		DesiredTemplateRevisionID: template.ID,
		ComponentKey:              "primary-vpc",
		WorkspaceName:             "meg_acme_primary",
		InstalledConfigJSON:       json.RawMessage(`{"region":"us-east-1"}`),
		DesiredConfigJSON:         json.RawMessage(`{"region":"us-east-1"}`),
		CreatedBy:                 domain.UserID("user_123"),
		Lifecycle:                 domain.StackTemplateActive,
	}
	second := first
	second.ID = domain.StackTemplateID("stack_template_shared")
	second.ComponentKey = "shared-vpc"
	second.WorkspaceName = "meg_acme_shared"

	if err := store.CreateStackTemplate(ctx, first); err != nil {
		t.Fatalf("CreateStackTemplate first returned error: %v", err)
	}
	if err := store.CreateStackTemplate(ctx, second); err != nil {
		t.Fatalf("CreateStackTemplate second returned error: %v", err)
	}

	view, err := store.GetStackWithTemplates(ctx, tenantID, stack.ID)
	if err != nil {
		t.Fatalf("GetStackWithTemplates returned error: %v", err)
	}
	if len(view.Templates) != 2 {
		t.Fatalf("template count = %d, want 2", len(view.Templates))
	}
	if view.Templates[0].SourceTemplateID != sourceTemplateID || view.Templates[1].SourceTemplateID != sourceTemplateID {
		t.Fatalf("source template IDs = %#v", view.Templates)
	}
}

func TestCreateStackTemplateRejectsDuplicateActiveComponentKey(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)
	tenantID := domain.TenantID("tenant_123")
	sourceTemplateID := domain.SourceTemplateID("source_template_vpc")

	stack := domain.Stack{
		ID:        domain.StackID("stack_123"),
		TenantID:  tenantID,
		Name:      "Acme Prod",
		Slug:      "acme-prod",
		CreatedBy: domain.UserID("user_123"),
		CreatedAt: time.Now().UTC(),
	}
	if err := store.CreateStack(ctx, stack); err != nil {
		t.Fatalf("CreateStack returned error: %v", err)
	}

	template := domain.TemplateRevision{
		ID:                domain.TemplateRevisionID("template_rev_1"),
		TenantID:          tenantID,
		SourceTemplateID:  sourceTemplateID,
		RepoOwner:         "acme",
		RepoName:          "infra",
		SourceRef:         "main",
		ResolvedCommitSHA: "sha-1",
		RootPath:          "vpc",
		Name:              "vpc",
		Status:            domain.TemplateRevisionActive,
		CreatedAt:         time.Now().UTC(),
	}
	if _, err := store.UpsertTemplateRevisionWithVariables(ctx, template, nil); err != nil {
		t.Fatalf("UpsertTemplateRevisionWithVariables returned error: %v", err)
	}

	stackTemplate := domain.StackTemplate{
		ID:                        domain.StackTemplateID("stack_template_primary"),
		TenantID:                  tenantID,
		StackID:                   stack.ID,
		SourceTemplateID:          sourceTemplateID,
		DesiredTemplateRevisionID: template.ID,
		ComponentKey:              "primary-vpc",
		WorkspaceName:             "meg_acme_primary",
		InstalledConfigJSON:       json.RawMessage(`{}`),
		DesiredConfigJSON:         json.RawMessage(`{}`),
		CreatedBy:                 domain.UserID("user_123"),
		Lifecycle:                 domain.StackTemplateActive,
	}

	if err := store.CreateStackTemplate(ctx, stackTemplate); err != nil {
		t.Fatalf("CreateStackTemplate first returned error: %v", err)
	}
	stackTemplate.ID = domain.StackTemplateID("stack_template_duplicate")
	err := store.CreateStackTemplate(ctx, stackTemplate)
	if !errors.Is(err, app.ErrDuplicateStackTemplateComponentKey) {
		t.Fatalf("error = %v, want ErrDuplicateStackTemplateComponentKey", err)
	}
}

func TestUpdateStackTemplateConfigPersistsDesiredConfig(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)
	tenantID := domain.TenantID("tenant_123")
	stack := domain.Stack{
		ID:        domain.StackID("stack_123"),
		TenantID:  tenantID,
		Name:      "Acme Prod",
		Slug:      "acme-prod",
		CreatedBy: domain.UserID("user_123"),
		CreatedAt: time.Now().UTC(),
	}
	if err := store.CreateStack(ctx, stack); err != nil {
		t.Fatalf("CreateStack returned error: %v", err)
	}
	stackTemplate := domain.StackTemplate{
		ID:                        domain.StackTemplateID("stack_template_123"),
		TenantID:                  tenantID,
		StackID:                   stack.ID,
		SourceTemplateID:          domain.SourceTemplateID("source_template_vpc"),
		DesiredTemplateRevisionID: domain.TemplateRevisionID("template_rev_1"),
		ComponentKey:              "primary-vpc",
		WorkspaceName:             "meg_acme_primary",
		InstalledConfigJSON:       json.RawMessage(`{"region":"us-east-1"}`),
		DesiredConfigJSON:         json.RawMessage(`{"region":"us-east-1"}`),
		CreatedBy:                 domain.UserID("user_123"),
		Lifecycle:                 domain.StackTemplateActive,
	}
	if err := store.CreateStackTemplate(ctx, stackTemplate); err != nil {
		t.Fatalf("CreateStackTemplate returned error: %v", err)
	}

	updated, err := store.UpdateStackTemplateConfig(ctx, tenantID, stackTemplate.ID, json.RawMessage(`{"region":"us-west-2"}`))
	if err != nil {
		t.Fatalf("UpdateStackTemplateConfig returned error: %v", err)
	}
	if !sameJSON(t, updated.DesiredConfigJSON, []byte(`{"region":"us-west-2"}`)) {
		t.Fatalf("updated desired config = %s", updated.DesiredConfigJSON)
	}
	if !sameJSON(t, updated.InstalledConfigJSON, []byte(`{"region":"us-east-1"}`)) {
		t.Fatalf("legacy config changed = %s", updated.InstalledConfigJSON)
	}
}

func TestUpdateStackTemplateDesiredRevisionPersistsRevisionAndConfig(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)
	tenantID := domain.TenantID("tenant_123")
	stack := domain.Stack{
		ID:        domain.StackID("stack_123"),
		TenantID:  tenantID,
		Name:      "Acme Prod",
		Slug:      "acme-prod",
		CreatedBy: domain.UserID("user_123"),
		CreatedAt: time.Now().UTC(),
	}
	if err := store.CreateStack(ctx, stack); err != nil {
		t.Fatalf("CreateStack returned error: %v", err)
	}
	stackTemplate := domain.StackTemplate{
		ID:                        domain.StackTemplateID("stack_template_123"),
		TenantID:                  tenantID,
		StackID:                   stack.ID,
		SourceTemplateID:          domain.SourceTemplateID("source_template_vpc"),
		DesiredTemplateRevisionID: domain.TemplateRevisionID("template_rev_1"),
		ComponentKey:              "primary-vpc",
		WorkspaceName:             "meg_acme_primary",
		InstalledConfigJSON:       json.RawMessage(`{"region":"us-east-1"}`),
		DesiredConfigJSON:         json.RawMessage(`{"region":"us-east-1"}`),
		CreatedBy:                 domain.UserID("user_123"),
		Lifecycle:                 domain.StackTemplateActive,
	}
	if err := store.CreateStackTemplate(ctx, stackTemplate); err != nil {
		t.Fatalf("CreateStackTemplate returned error: %v", err)
	}

	updated, err := store.UpdateStackTemplateDesiredRevision(ctx, tenantID, stackTemplate.ID, domain.TemplateRevisionID("template_rev_2"), json.RawMessage(`{"region":"us-west-2"}`))
	if err != nil {
		t.Fatalf("UpdateStackTemplateDesiredRevision returned error: %v", err)
	}
	if updated.DesiredTemplateRevisionID != domain.TemplateRevisionID("template_rev_2") {
		t.Fatalf("updated desired template revision ID = %q, want template_rev_2", updated.DesiredTemplateRevisionID)
	}
	if !sameJSON(t, updated.DesiredConfigJSON, []byte(`{"region":"us-west-2"}`)) {
		t.Fatalf("updated desired config = %s", updated.DesiredConfigJSON)
	}
	if updated.LastAppliedTemplateRevisionID != "" {
		t.Fatalf("last applied template revision ID = %q, want empty", updated.LastAppliedTemplateRevisionID)
	}
}

func TestGetStackTemplateReturnsTenantScopedRecord(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)
	lastAppliedAt := time.Date(2026, 7, 2, 8, 30, 0, 123456000, time.UTC)

	_, err := pool.Exec(ctx, `
		insert into stack_templates (
			id,
			tenant_id,
			stack_id,
			component_key,
			source_template_id,
			desired_template_revision_id,
			workspace_name,
			installed_config_json,
			desired_config_json,
			last_applied_run_id,
			last_applied_at,
			lifecycle
		) values ($1, $2, $3, $4, $5, $6, $7, $8::jsonb, $9::jsonb, $10, $11, $12)
	`,
		"stack_template_123",
		"tenant_123",
		"stack_123",
		"primary-vpc",
		"source_template_vpc",
		"template_123",
		"mtp_acme_prod_vpc_a13f9c",
		`{"region":"us-east-1"}`,
		`{"region":"us-east-1"}`,
		"run_123",
		lastAppliedAt,
		"active",
	)
	if err != nil {
		t.Fatalf("seed stack template: %v", err)
	}

	stackTemplate, err := store.GetStackTemplate(ctx, domain.TenantID("tenant_123"), domain.StackTemplateID("stack_template_123"))
	if err != nil {
		t.Fatalf("GetStackTemplate returned error: %v", err)
	}

	if stackTemplate.ID != domain.StackTemplateID("stack_template_123") {
		t.Fatalf("ID = %q, want stack_template_123", stackTemplate.ID)
	}
	if stackTemplate.StackID != domain.StackID("stack_123") {
		t.Fatalf("StackID = %q, want stack_123", stackTemplate.StackID)
	}
	if stackTemplate.DesiredTemplateRevisionID != domain.TemplateRevisionID("template_123") {
		t.Fatalf("DesiredTemplateRevisionID = %q, want template_123", stackTemplate.DesiredTemplateRevisionID)
	}
	if stackTemplate.WorkspaceName != "mtp_acme_prod_vpc_a13f9c" {
		t.Fatalf("WorkspaceName = %q, want mtp_acme_prod_vpc_a13f9c", stackTemplate.WorkspaceName)
	}
	if string(stackTemplate.InstalledConfigJSON) != `{"region": "us-east-1"}` {
		t.Fatalf("InstalledConfigJSON = %s, want region config", stackTemplate.InstalledConfigJSON)
	}
	if stackTemplate.LastAppliedRunID != domain.TemplateRunID("run_123") {
		t.Fatalf("LastAppliedRunID = %q, want run_123", stackTemplate.LastAppliedRunID)
	}
	if !stackTemplate.LastAppliedAt.Equal(lastAppliedAt) {
		t.Fatalf("LastAppliedAt = %v, want %v", stackTemplate.LastAppliedAt, lastAppliedAt)
	}
	if stackTemplate.Lifecycle != domain.StackTemplateActive {
		t.Fatalf("Lifecycle = %q, want active", stackTemplate.Lifecycle)
	}
}

func TestGetStackTemplateReturnsNotFoundForOtherTenant(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)

	_, err := pool.Exec(ctx, `
		insert into stack_templates (
			id,
			tenant_id,
			stack_id,
			desired_template_revision_id,
			workspace_name,
			lifecycle
		) values ($1, $2, $3, $4, $5, $6)
	`,
		"stack_template_123",
		"tenant_123",
		"stack_123",
		"template_rev_1",
		"mtp_acme_prod_vpc_a13f9c",
		"active",
	)
	if err != nil {
		t.Fatalf("seed stack template: %v", err)
	}

	_, err = store.GetStackTemplate(ctx, domain.TenantID("tenant_456"), domain.StackTemplateID("stack_template_123"))
	if !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("error = %v, want app.ErrNotFound", err)
	}
}

func TestCreateTemplateRunPersistsRunFields(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)
	startedAt := time.Date(2026, 7, 2, 9, 30, 0, 123456000, time.UTC)
	completedAt := time.Date(2026, 7, 2, 9, 45, 0, 123456000, time.UTC)
	_, err := pool.Exec(ctx, `
		insert into template_revisions (
			id, tenant_id, repo_owner, repo_name, source_ref,
			resolved_commit_sha, root_path, name, status
		) values ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, "template_rev_2", "tenant_123", "acme", "infra-templates", "main", "abc123", "modules/vpc", "VPC", "active")
	if err != nil {
		t.Fatalf("seed template revision: %v", err)
	}

	run := domain.TemplateRun{
		ID:                 domain.TemplateRunID("run_123"),
		TenantID:           domain.TenantID("tenant_123"),
		StackTemplateID:    domain.StackTemplateID("stack_template_123"),
		TemplateRevisionID: domain.TemplateRevisionID("template_rev_2"),
		SourceTemplateID:   domain.SourceTemplateID("source_template_vpc"),
		Operation:          domain.OperationApply,
		SelectedRef:        "main",
		ResolvedCommitSHA:  "abc123",
		WorkspaceName:      "mtp_acme_prod_vpc_a13f9c",
		ConfigJSON:         json.RawMessage(`{"region":"us-east-1"}`),
		BackendType:        "s3",
		BackendConfigHash:  "backend_hash_123",
		Status:             domain.TemplateRunQueued,
		TriggerActor:       requesterSubject,
		StartedAt:          startedAt,
		CompletedAt:        completedAt,
		ErrorSummary:       "previous error summary",
	}

	if err := store.CreateTemplateRun(ctx, run); err != nil {
		t.Fatalf("CreateTemplateRun returned error: %v", err)
	}

	var got domain.TemplateRun
	err = pool.QueryRow(ctx, `
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
		where id = $1
	`, run.ID).Scan(
		&got.ID,
		&got.TenantID,
		&got.StackTemplateID,
		&got.TemplateRevisionID,
		&got.SourceTemplateID,
		&got.Operation,
		&got.SelectedRef,
		&got.ResolvedCommitSHA,
		&got.WorkspaceName,
		&got.ConfigJSON,
		&got.BackendType,
		&got.BackendConfigHash,
		&got.Status,
		&got.TriggerActor,
		&got.StartedAt,
		&got.CompletedAt,
		&got.ErrorSummary,
	)
	if err != nil {
		t.Fatalf("read inserted template run: %v", err)
	}

	if got.ID != run.ID ||
		got.TenantID != run.TenantID ||
		got.StackTemplateID != run.StackTemplateID ||
		got.TemplateRevisionID != run.TemplateRevisionID ||
		got.SourceTemplateID != run.SourceTemplateID ||
		got.Operation != run.Operation ||
		got.SelectedRef != run.SelectedRef ||
		got.ResolvedCommitSHA != run.ResolvedCommitSHA ||
		got.WorkspaceName != run.WorkspaceName ||
		!sameJSON(t, got.ConfigJSON, run.ConfigJSON) ||
		got.BackendType != run.BackendType ||
		got.BackendConfigHash != run.BackendConfigHash ||
		got.Status != run.Status ||
		got.TriggerActor != run.TriggerActor ||
		got.ErrorSummary != run.ErrorSummary {
		t.Fatalf("inserted run = %#v, want %#v", got, run)
	}
	if !got.StartedAt.Equal(run.StartedAt) {
		t.Fatalf("StartedAt = %v, want %v", got.StartedAt, run.StartedAt)
	}
	if !got.CompletedAt.Equal(run.CompletedAt) {
		t.Fatalf("CompletedAt = %v, want %v", got.CompletedAt, run.CompletedAt)
	}

	var queuedStarts int
	if err := pool.QueryRow(ctx, `select count(*) from work_queue where kind = 'start_template_run'`).Scan(&queuedStarts); err != nil {
		t.Fatalf("count work queue rows: %v", err)
	}
	if queuedStarts != 0 {
		t.Fatalf("work queue rows = %d, want none from standalone CreateTemplateRun", queuedStarts)
	}
}

// The gate against concurrent runs on one stack template, which is a partial
// unique index rather than a check in the service, so that the check and the
// write are the same operation and nothing can slip between them.
//
// Walking every status is the point: the index predicate names the three
// terminal statuses in SQL and nothing ties that list to Terminal(). A status
// added on one side and not the other shows up here as a run that was let
// through while one was still going, or refused while none was.
func TestCreateTemplateRunAllowsOneNonTerminalRunPerStackTemplate(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)

	for _, status := range domain.AllTemplateRunStatuses {
		status := status
		t.Run(string(status), func(t *testing.T) {
			t.Parallel()

			// Scoped to this subtest so the parallel cases cannot collide.
			stackTemplateID := domain.StackTemplateID("stack_template_" + string(status))
			seedTemplateRun(t, ctx, pool, templateRunAt(stackTemplateID, domain.TemplateRunID("run_first_"+status), status))

			err := store.CreateTemplateRun(ctx, templateRunAt(stackTemplateID, domain.TemplateRunID("run_second_"+status), domain.TemplateRunQueued))

			if status.Terminal() {
				if err != nil {
					t.Fatalf("CreateTemplateRun after a %q run returned error: %v", status, err)
				}
				return
			}
			if !errors.Is(err, app.ErrTemplateRunInFlight) {
				t.Fatalf("CreateTemplateRun during a %q run: error = %v, want app.ErrTemplateRunInFlight", status, err)
			}
		})
	}
}

// The gate is per stack template, and history is unbounded: neither a run on a
// neighbouring template nor a shelf of finished runs may block a new one.
func TestCreateTemplateRunScopesTheInFlightGate(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)

	seedTemplateRun(t, ctx, pool, templateRunAt("stack_template_a", "run_a_active", domain.TemplateRunApplyStarted))
	// A different stack template, and the same template under a different
	// tenant: the index keys on the pair, so neither is the same slot.
	if err := store.CreateTemplateRun(ctx, templateRunAt("stack_template_b", "run_b_active", domain.TemplateRunQueued)); err != nil {
		t.Fatalf("CreateTemplateRun on a second stack template returned error: %v", err)
	}
	otherTenant := templateRunAt("stack_template_a", "run_other_tenant", domain.TemplateRunQueued)
	otherTenant.TenantID = domain.TenantID("tenant_456")
	if err := store.CreateTemplateRun(ctx, otherTenant); err != nil {
		t.Fatalf("CreateTemplateRun for a second tenant returned error: %v", err)
	}

	// Finished runs leave the index, so they accumulate without ever filling
	// the slot - and the slot reopens once the active run reaches a terminal
	// status through the ordinary status-recording path. Failure is the case
	// worth proving: it is the one #157 used to leave stuck non-terminal, which
	// under this index would wedge the stack template permanently.
	seedTemplateRun(t, ctx, pool, templateRunAt("stack_template_c", "run_c_1", domain.TemplateRunCompleted))
	seedTemplateRun(t, ctx, pool, templateRunAt("stack_template_c", "run_c_2", domain.TemplateRunFailed))
	seedTemplateRun(t, ctx, pool, templateRunAt("stack_template_c", "run_c_3", domain.TemplateRunCanceled))
	if err := store.CreateTemplateRun(ctx, templateRunAt("stack_template_c", "run_c_4", domain.TemplateRunQueued)); err != nil {
		t.Fatalf("CreateTemplateRun after three finished runs returned error: %v", err)
	}
	if err := store.RecordTemplateRunStatus(ctx, domain.TemplateRunStatusActivityInput{
		RunID:           domain.TemplateRunID("run_c_4"),
		TenantID:        domain.TenantID("tenant_123"),
		StackTemplateID: domain.StackTemplateID("stack_template_c"),
		Operation:       domain.OperationPlan,
		Status:          domain.TemplateRunFailed,
	}); err != nil {
		t.Fatalf("RecordTemplateRunStatus returned error: %v", err)
	}
	if err := store.CreateTemplateRun(ctx, templateRunAt("stack_template_c", "run_c_5", domain.TemplateRunQueued)); err != nil {
		t.Fatalf("CreateTemplateRun after the active run failed returned error: %v", err)
	}
}

// The reason the gate is an index and not a check in StartTemplateRun. A
// read-then-insert lets both requests see an empty stack template and both
// write; here the check and the write are one statement, so the second insert
// is refused however close together they arrive.
func TestCreateTemplateRunAdmitsOneOfManyConcurrentRuns(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)

	const racers = 8
	start := make(chan struct{})
	errs := make(chan error, racers)
	var waiting sync.WaitGroup
	waiting.Add(racers)
	for index := range racers {
		go func() {
			defer waiting.Done()
			<-start
			errs <- store.CreateTemplateRun(ctx, templateRunAt(
				"stack_template_race",
				domain.TemplateRunID(fmt.Sprintf("run_race_%d", index)),
				domain.TemplateRunQueued,
			))
		}()
	}
	close(start)
	waiting.Wait()
	close(errs)

	admitted := 0
	for err := range errs {
		switch {
		case err == nil:
			admitted++
		case errors.Is(err, app.ErrTemplateRunInFlight):
		default:
			t.Fatalf("CreateTemplateRun returned an unexpected error: %v", err)
		}
	}
	if admitted != 1 {
		t.Fatalf("admitted %d of %d concurrent runs, want exactly 1", admitted, racers)
	}
}

func templateRunAt(stackTemplateID domain.StackTemplateID, runID domain.TemplateRunID, status domain.TemplateRunStatus) domain.TemplateRun {
	return domain.TemplateRun{
		ID:              runID,
		TenantID:        domain.TenantID("tenant_123"),
		StackTemplateID: stackTemplateID,
		Operation:       domain.OperationPlan,
		SelectedRef:     "main",
		WorkspaceName:   "workspace",
		ConfigJSON:      json.RawMessage(`{}`),
		Status:          status,
		TriggerActor:    domain.UserID("user_123"),
		StartedAt:       time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC),
	}
}

func TestUnitOfWorkTemplateWritesRollbackTogether(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)
	_, err := pool.Exec(ctx, `insert into template_revisions (id, tenant_id, repo_owner, repo_name, source_ref, resolved_commit_sha, root_path, name, status) values ('revision_123', 'tenant_123', 'acme', 'infra', 'main', 'sha_123', 'modules/vpc', 'VPC', 'active')`)
	if err != nil {
		t.Fatalf("seed template revision: %v", err)
	}
	// One stack template each: all three runs here are non-terminal, and
	// template_runs_in_flight_idx allows only one of those per stack template.
	// What this test is about is that four unrelated writes roll back together,
	// so which template each run belongs to does not matter to it.
	seedTemplateRun(t, ctx, pool, domain.TemplateRun{ID: "run_approval", TenantID: "tenant_123", StackTemplateID: "stack_template_approval", Operation: domain.OperationApply, SelectedRef: "main", WorkspaceName: "workspace", Status: domain.TemplateRunWaitingApproval, TriggerActor: "user_123"})
	seedTemplateRun(t, ctx, pool, domain.TemplateRun{ID: "run_cancel", TenantID: "tenant_123", StackTemplateID: "stack_template_cancel", Operation: domain.OperationApply, SelectedRef: "main", WorkspaceName: "workspace", Status: domain.TemplateRunQueued, TriggerActor: "user_123"})
	sentinel := errors.New("rollback")

	err = store.InTx(ctx, func(repo app.TxRepo, _ queue.Enqueuer) error {
		if err := repo.CreateTemplateRegistration(ctx, domain.TemplateRegistration{ID: "registration_123", TenantID: "tenant_123", RepoOwner: "acme", RepoName: "infra", SourceRef: "main", RootPath: "modules/vpc", Status: domain.TemplateRegistrationPending, RequestedBy: "user_123", RequestedAt: time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC)}); err != nil {
			return err
		}
		if err := repo.CreateTemplateRun(ctx, domain.TemplateRun{ID: "run_created", TenantID: "tenant_123", StackTemplateID: "stack_template_123", TemplateRevisionID: "revision_123", Operation: domain.OperationPlan, SelectedRef: "main", WorkspaceName: "workspace", ConfigJSON: json.RawMessage(`{}`), Status: domain.TemplateRunQueued, TriggerActor: "user_123"}); err != nil {
			return err
		}
		if err := repo.ApproveTemplateRun(ctx, domain.TemplateRunApproval{TenantID: "tenant_123", RunID: "run_approval", ApprovedBy: "user_123", ApprovedAt: time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC)}); err != nil {
			return err
		}
		if err := repo.RequestTemplateRunCancellation(ctx, domain.TemplateRunCancellation{TenantID: "tenant_123", RunID: "run_cancel", RequestedBy: "user_123", Reason: "superseded", RequestedAt: time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC)}); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("InTx error = %v, want rollback sentinel", err)
	}

	var registrations, createdRuns, approvals int
	var approvalStatus, cancellationStatus domain.TemplateRunStatus
	if err := pool.QueryRow(ctx, `select (select count(*) from template_registrations where id = 'registration_123'), (select count(*) from template_runs where id = 'run_created'), (select count(*) from template_run_approvals where run_id = 'run_approval'), (select status from template_runs where id = 'run_approval'), (select status from template_runs where id = 'run_cancel')`).Scan(&registrations, &createdRuns, &approvals, &approvalStatus, &cancellationStatus); err != nil {
		t.Fatalf("read transaction state: %v", err)
	}
	if registrations != 0 || createdRuns != 0 || approvals != 0 || approvalStatus != domain.TemplateRunWaitingApproval || cancellationStatus != domain.TemplateRunQueued {
		t.Fatalf("registrations=%d createdRuns=%d approvals=%d approvalStatus=%q cancellationStatus=%q", registrations, createdRuns, approvals, approvalStatus, cancellationStatus)
	}
}

func TestGetTemplateRunReturnsTenantScopedRecord(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)
	startedAt := time.Date(2026, 7, 5, 9, 30, 0, 123456000, time.UTC)
	completedAt := time.Date(2026, 7, 5, 9, 45, 0, 123456000, time.UTC)
	seedTemplateRun(t, ctx, pool, domain.TemplateRun{
		ID:                domain.TemplateRunID("run_123"),
		TenantID:          domain.TenantID("tenant_123"),
		StackTemplateID:   domain.StackTemplateID("stack_template_123"),
		Operation:         domain.OperationPlan,
		SelectedRef:       "main",
		ResolvedCommitSHA: "abc123",
		WorkspaceName:     "mtp_acme_prod_vpc_a13f9c",
		BackendType:       "s3",
		BackendConfigHash: "backend_hash_123",
		Status:            domain.TemplateRunCompleted,
		TriggerActor:      domain.UserID("user_123"),
		StartedAt:         startedAt,
		CompletedAt:       completedAt,
		ErrorSummary:      "previous error summary",
	})

	run, err := store.GetTemplateRun(ctx, domain.TenantID("tenant_123"), domain.TemplateRunID("run_123"))
	if err != nil {
		t.Fatalf("GetTemplateRun returned error: %v", err)
	}

	want := domain.TemplateRun{
		ID:                domain.TemplateRunID("run_123"),
		TenantID:          domain.TenantID("tenant_123"),
		StackTemplateID:   domain.StackTemplateID("stack_template_123"),
		Operation:         domain.OperationPlan,
		SelectedRef:       "main",
		ResolvedCommitSHA: "abc123",
		WorkspaceName:     "mtp_acme_prod_vpc_a13f9c",
		BackendType:       "s3",
		BackendConfigHash: "backend_hash_123",
		Status:            domain.TemplateRunCompleted,
		TriggerActor:      domain.UserID("user_123"),
		StartedAt:         startedAt,
		CompletedAt:       completedAt,
		ConfigJSON:        json.RawMessage(`{}`),
		ErrorSummary:      "previous error summary",
	}
	run.StartedAt = run.StartedAt.UTC()
	run.CompletedAt = run.CompletedAt.UTC()
	if !reflect.DeepEqual(run, want) {
		t.Fatalf("run = %#v, want %#v", run, want)
	}
}

func TestGetTemplateRunReturnsNotFoundForOtherTenant(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)
	seedTemplateRun(t, ctx, pool, domain.TemplateRun{
		ID:              domain.TemplateRunID("run_123"),
		TenantID:        domain.TenantID("tenant_123"),
		StackTemplateID: domain.StackTemplateID("stack_template_123"),
		Operation:       domain.OperationPlan,
		SelectedRef:     "main",
		WorkspaceName:   "mtp_acme_prod_vpc_a13f9c",
		Status:          domain.TemplateRunQueued,
		TriggerActor:    domain.UserID("user_123"),
	})

	_, err := store.GetTemplateRun(ctx, domain.TenantID("tenant_456"), domain.TemplateRunID("run_123"))
	if !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("error = %v, want app.ErrNotFound", err)
	}
}

func TestRecordTemplateRunLogUpsertsTenantScopedMetadata(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)
	uploadedAt := time.Date(2026, 7, 6, 10, 15, 0, 123456000, time.UTC)
	seedTemplateRun(t, ctx, pool, domain.TemplateRun{
		ID:              domain.TemplateRunID("run_123"),
		TenantID:        domain.TenantID("tenant_123"),
		StackTemplateID: domain.StackTemplateID("stack_template_123"),
		Operation:       domain.OperationPlan,
		SelectedRef:     "main",
		WorkspaceName:   "mtp_acme_prod_vpc_a13f9c",
		Status:          domain.TemplateRunQueued,
		TriggerActor:    domain.UserID("user_123"),
	})

	log := domain.TemplateRunLog{
		TenantID:    domain.TenantID("tenant_123"),
		RunID:       domain.TemplateRunID("run_123"),
		Phase:       "plan",
		ObjectKey:   "tenants/tenant_123/runs/run_123/logs/plan.log",
		ContentType: "text/plain; charset=utf-8",
		SizeBytes:   18,
		UploadedAt:  uploadedAt,
	}
	if err := store.RecordTemplateRunLog(ctx, log); err != nil {
		t.Fatalf("RecordTemplateRunLog returned error: %v", err)
	}

	log.SizeBytes = 24
	log.UploadedAt = uploadedAt.Add(time.Minute)
	if err := store.RecordTemplateRunLog(ctx, log); err != nil {
		t.Fatalf("RecordTemplateRunLog upsert returned error: %v", err)
	}

	got, err := store.GetTemplateRunLog(ctx, domain.TenantID("tenant_123"), domain.TemplateRunID("run_123"), "plan")
	if err != nil {
		t.Fatalf("GetTemplateRunLog returned error: %v", err)
	}
	got.UploadedAt = got.UploadedAt.UTC()

	if got != log {
		t.Fatalf("log = %#v, want %#v", got, log)
	}
}

func TestListTemplateRunLogsReturnsTenantScopedMetadataOrderedByPhase(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)
	uploadedAt := time.Date(2026, 7, 6, 10, 15, 0, 123456000, time.UTC)
	seedTemplateRun(t, ctx, pool, domain.TemplateRun{
		ID:              domain.TemplateRunID("run_123"),
		TenantID:        domain.TenantID("tenant_123"),
		StackTemplateID: domain.StackTemplateID("stack_template_123"),
		Operation:       domain.OperationPlan,
		SelectedRef:     "main",
		WorkspaceName:   "mtp_acme_prod_vpc_a13f9c",
		Status:          domain.TemplateRunQueued,
		TriggerActor:    domain.UserID("user_123"),
	})
	seedTemplateRun(t, ctx, pool, domain.TemplateRun{
		ID:              domain.TemplateRunID("run_456"),
		TenantID:        domain.TenantID("tenant_456"),
		StackTemplateID: domain.StackTemplateID("stack_template_456"),
		Operation:       domain.OperationPlan,
		SelectedRef:     "main",
		WorkspaceName:   "mtp_other_prod_vpc_f47ac",
		Status:          domain.TemplateRunQueued,
		TriggerActor:    domain.UserID("user_456"),
	})
	for _, log := range []domain.TemplateRunLog{
		{
			TenantID:    domain.TenantID("tenant_123"),
			RunID:       domain.TemplateRunID("run_123"),
			Phase:       "plan",
			ObjectKey:   "tenants/tenant_123/runs/run_123/logs/plan.log",
			ContentType: "text/plain; charset=utf-8",
			SizeBytes:   18,
			UploadedAt:  uploadedAt,
		},
		{
			TenantID:    domain.TenantID("tenant_123"),
			RunID:       domain.TemplateRunID("run_123"),
			Phase:       "init",
			ObjectKey:   "tenants/tenant_123/runs/run_123/logs/init.log",
			ContentType: "text/plain; charset=utf-8",
			SizeBytes:   12,
			UploadedAt:  uploadedAt.Add(-time.Minute),
		},
		{
			TenantID:    domain.TenantID("tenant_456"),
			RunID:       domain.TemplateRunID("run_456"),
			Phase:       "plan",
			ObjectKey:   "tenants/tenant_456/runs/run_456/logs/plan.log",
			ContentType: "text/plain; charset=utf-8",
			SizeBytes:   17,
			UploadedAt:  uploadedAt,
		},
	} {
		if err := store.RecordTemplateRunLog(ctx, log); err != nil {
			t.Fatalf("RecordTemplateRunLog returned error: %v", err)
		}
	}

	logs, err := store.ListTemplateRunLogs(ctx, domain.TenantID("tenant_123"), domain.TemplateRunID("run_123"))
	if err != nil {
		t.Fatalf("ListTemplateRunLogs returned error: %v", err)
	}

	if len(logs) != 2 {
		t.Fatalf("len(logs) = %d, want 2", len(logs))
	}
	if logs[0].Phase != "init" || logs[1].Phase != "plan" {
		t.Fatalf("phases = %q, %q; want init, plan", logs[0].Phase, logs[1].Phase)
	}
}

func TestRecordTemplateRunLogReturnsNotFoundForOtherTenant(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)
	seedTemplateRun(t, ctx, pool, domain.TemplateRun{
		ID:              domain.TemplateRunID("run_123"),
		TenantID:        domain.TenantID("tenant_123"),
		StackTemplateID: domain.StackTemplateID("stack_template_123"),
		Operation:       domain.OperationPlan,
		SelectedRef:     "main",
		WorkspaceName:   "mtp_acme_prod_vpc_a13f9c",
		Status:          domain.TemplateRunQueued,
		TriggerActor:    domain.UserID("user_123"),
	})

	err := store.RecordTemplateRunLog(ctx, domain.TemplateRunLog{
		TenantID:    domain.TenantID("tenant_456"),
		RunID:       domain.TemplateRunID("run_123"),
		Phase:       "plan",
		ObjectKey:   "tenants/tenant_456/runs/run_123/logs/plan.log",
		ContentType: "text/plain; charset=utf-8",
		SizeBytes:   18,
		UploadedAt:  time.Date(2026, 7, 6, 10, 15, 0, 0, time.UTC),
	})
	if !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("error = %v, want app.ErrNotFound", err)
	}
}

func TestListTemplateRunsReturnsMostRecentFirstScopedToStackTemplate(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)

	seedTemplateRun(t, ctx, pool, domain.TemplateRun{
		ID:              domain.TemplateRunID("run_older"),
		TenantID:        domain.TenantID("tenant_123"),
		StackTemplateID: domain.StackTemplateID("stack_template_123"),
		Operation:       domain.OperationPlan,
		SelectedRef:     "main",
		WorkspaceName:   "mtp_acme_prod_vpc_a13f9c",
		Status:          domain.TemplateRunCompleted,
		TriggerActor:    domain.UserID("user_123"),
		StartedAt:       time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC),
	})
	seedTemplateRun(t, ctx, pool, domain.TemplateRun{
		ID:              domain.TemplateRunID("run_newer"),
		TenantID:        domain.TenantID("tenant_123"),
		StackTemplateID: domain.StackTemplateID("stack_template_123"),
		Operation:       domain.OperationApply,
		SelectedRef:     "main",
		WorkspaceName:   "mtp_acme_prod_vpc_a13f9c",
		Status:          domain.TemplateRunWaitingApproval,
		TriggerActor:    domain.UserID("user_456"),
		StartedAt:       time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC),
	})
	seedTemplateRun(t, ctx, pool, domain.TemplateRun{
		ID:              domain.TemplateRunID("run_other_stack_template"),
		TenantID:        domain.TenantID("tenant_123"),
		StackTemplateID: domain.StackTemplateID("stack_template_456"),
		Operation:       domain.OperationPlan,
		SelectedRef:     "main",
		WorkspaceName:   "mtp_acme_prod_vpc_other",
		Status:          domain.TemplateRunCompleted,
		TriggerActor:    domain.UserID("user_123"),
		StartedAt:       time.Date(2026, 7, 20, 11, 0, 0, 0, time.UTC),
	})

	runs, err := store.ListTemplateRuns(ctx, domain.TenantID("tenant_123"), domain.StackTemplateID("stack_template_123"))
	if err != nil {
		t.Fatalf("ListTemplateRuns returned error: %v", err)
	}

	if len(runs) != 2 {
		t.Fatalf("len(runs) = %d, want 2", len(runs))
	}
	if runs[0].ID != domain.TemplateRunID("run_newer") || runs[1].ID != domain.TemplateRunID("run_older") {
		t.Fatalf("run order = %q, %q; want run_newer, run_older", runs[0].ID, runs[1].ID)
	}
	if runs[0].Status != domain.TemplateRunWaitingApproval {
		t.Fatalf("newest run status = %q, want waiting_approval", runs[0].Status)
	}
	if runs[0].TriggerActor != domain.UserID("user_456") {
		t.Fatalf("newest run trigger actor = %q, want user_456", runs[0].TriggerActor)
	}
}

func TestListTemplateRunsReturnsEmptySliceWhenNoneExist(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)

	runs, err := store.ListTemplateRuns(ctx, domain.TenantID("tenant_123"), domain.StackTemplateID("stack_template_nonexistent"))
	if err != nil {
		t.Fatalf("ListTemplateRuns returned error: %v", err)
	}
	if runs == nil {
		t.Fatal("runs = nil, want empty slice")
	}
	if len(runs) != 0 {
		t.Fatalf("len(runs) = %d, want 0", len(runs))
	}
}

func TestApproveTemplateRunApprovesWaitingRun(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)
	approvedAt := time.Date(2026, 7, 2, 10, 15, 0, 123456000, time.UTC)
	seedTemplateRun(t, ctx, pool, domain.TemplateRun{
		ID:              domain.TemplateRunID("run_123"),
		TenantID:        domain.TenantID("tenant_123"),
		StackTemplateID: domain.StackTemplateID("stack_template_123"),
		Operation:       domain.OperationApply,
		SelectedRef:     "main",
		WorkspaceName:   "mtp_acme_prod_vpc_a13f9c",
		Status:          domain.TemplateRunWaitingApproval,
		TriggerActor:    requesterSubject,
	})

	err := store.ApproveTemplateRun(ctx, domain.TemplateRunApproval{
		RunID:      domain.TemplateRunID("run_123"),
		TenantID:   domain.TenantID("tenant_123"),
		ApprovedBy: approverSubject,
		ApprovedAt: approvedAt,
	})
	if err != nil {
		t.Fatalf("ApproveTemplateRun returned error: %v", err)
	}

	var status domain.TemplateRunStatus
	if err := pool.QueryRow(ctx, "select status from template_runs where id = $1", "run_123").Scan(&status); err != nil {
		t.Fatalf("read approved run status: %v", err)
	}
	if status != domain.TemplateRunApproved {
		t.Fatalf("status = %q, want %q", status, domain.TemplateRunApproved)
	}

	var approvedBy domain.UserID
	var gotApprovedAt time.Time
	if err := pool.QueryRow(ctx, `
		select approved_by, approved_at
		from template_run_approvals
		where run_id = $1
	`, "run_123").Scan(&approvedBy, &gotApprovedAt); err != nil {
		t.Fatalf("read approval row: %v", err)
	}
	if approvedBy != approverSubject {
		t.Fatalf("approved_by = %q, want %q", approvedBy, approverSubject)
	}
	if !gotApprovedAt.Equal(approvedAt) {
		t.Fatalf("approved_at = %v, want %v", gotApprovedAt, approvedAt)
	}
}

func TestApproveTemplateRunRejectsNonWaitingRun(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)
	seedTemplateRun(t, ctx, pool, domain.TemplateRun{
		ID:              domain.TemplateRunID("run_123"),
		TenantID:        domain.TenantID("tenant_123"),
		StackTemplateID: domain.StackTemplateID("stack_template_123"),
		Operation:       domain.OperationApply,
		SelectedRef:     "main",
		WorkspaceName:   "mtp_acme_prod_vpc_a13f9c",
		Status:          domain.TemplateRunQueued,
		TriggerActor:    domain.UserID("user_123"),
	})

	err := store.ApproveTemplateRun(ctx, domain.TemplateRunApproval{
		RunID:      domain.TemplateRunID("run_123"),
		TenantID:   domain.TenantID("tenant_123"),
		ApprovedBy: domain.UserID("user_456"),
		ApprovedAt: time.Date(2026, 7, 2, 10, 15, 0, 0, time.UTC),
	})
	if !errors.Is(err, app.ErrRunNotApprovable) {
		t.Fatalf("error = %v, want ErrRunNotApprovable", err)
	}
}

func TestRequestTemplateRunCancellationMarksCancelableRun(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)
	requestedAt := time.Date(2026, 7, 2, 10, 45, 0, 123456000, time.UTC)
	seedTemplateRun(t, ctx, pool, domain.TemplateRun{
		ID:              domain.TemplateRunID("run_123"),
		TenantID:        domain.TenantID("tenant_123"),
		StackTemplateID: domain.StackTemplateID("stack_template_123"),
		Operation:       domain.OperationApply,
		SelectedRef:     "main",
		WorkspaceName:   "mtp_acme_prod_vpc_a13f9c",
		Status:          domain.TemplateRunApplyStarted,
		TriggerActor:    requesterSubject,
	})

	err := store.RequestTemplateRunCancellation(ctx, domain.TemplateRunCancellation{
		RunID:       domain.TemplateRunID("run_123"),
		TenantID:    domain.TenantID("tenant_123"),
		RequestedBy: requesterSubject,
		Reason:      "superseded by newer run",
		RequestedAt: requestedAt,
	})
	if err != nil {
		t.Fatalf("RequestTemplateRunCancellation returned error: %v", err)
	}

	var status domain.TemplateRunStatus
	var requestedBy domain.UserID
	var reason string
	var gotRequestedAt time.Time
	if err := pool.QueryRow(ctx, `
		select
			status,
			cancellation_requested_by,
			cancellation_reason,
			cancellation_requested_at
		from template_runs
		where id = $1
	`, "run_123").Scan(&status, &requestedBy, &reason, &gotRequestedAt); err != nil {
		t.Fatalf("read cancellation metadata: %v", err)
	}
	if status != domain.TemplateRunCancelRequested {
		t.Fatalf("status = %q, want %q", status, domain.TemplateRunCancelRequested)
	}
	if requestedBy != requesterSubject {
		t.Fatalf("requested_by = %q, want %q", requestedBy, requesterSubject)
	}
	if reason != "superseded by newer run" {
		t.Fatalf("reason = %q, want superseded by newer run", reason)
	}
	if !gotRequestedAt.Equal(requestedAt) {
		t.Fatalf("requested_at = %v, want %v", gotRequestedAt, requestedAt)
	}
}

func TestRequestTemplateRunCancellationRejectsTerminalRun(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)
	seedTemplateRun(t, ctx, pool, domain.TemplateRun{
		ID:              domain.TemplateRunID("run_123"),
		TenantID:        domain.TenantID("tenant_123"),
		StackTemplateID: domain.StackTemplateID("stack_template_123"),
		Operation:       domain.OperationApply,
		SelectedRef:     "main",
		WorkspaceName:   "mtp_acme_prod_vpc_a13f9c",
		Status:          domain.TemplateRunCompleted,
		TriggerActor:    domain.UserID("user_123"),
	})

	err := store.RequestTemplateRunCancellation(ctx, domain.TemplateRunCancellation{
		RunID:       domain.TemplateRunID("run_123"),
		TenantID:    domain.TenantID("tenant_123"),
		RequestedBy: domain.UserID("user_456"),
		Reason:      "too late",
		RequestedAt: time.Date(2026, 7, 2, 10, 45, 0, 0, time.UTC),
	})
	if !errors.Is(err, app.ErrRunNotCancelable) {
		t.Fatalf("error = %v, want ErrRunNotCancelable", err)
	}
}

func TestRecordTemplateRunStatusUpdatesTenantScopedRun(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)
	seedTemplateRun(t, ctx, pool, domain.TemplateRun{
		ID:              domain.TemplateRunID("run_123"),
		TenantID:        domain.TenantID("tenant_123"),
		StackTemplateID: domain.StackTemplateID("stack_template_123"),
		Operation:       domain.OperationPlan,
		SelectedRef:     "main",
		WorkspaceName:   "mtp_acme_prod_vpc_a13f9c",
		Status:          domain.TemplateRunQueued,
		TriggerActor:    domain.UserID("user_123"),
	})
	seedTemplateRun(t, ctx, pool, domain.TemplateRun{
		ID:              domain.TemplateRunID("run_456"),
		TenantID:        domain.TenantID("tenant_456"),
		StackTemplateID: domain.StackTemplateID("stack_template_456"),
		Operation:       domain.OperationPlan,
		SelectedRef:     "main",
		WorkspaceName:   "mtp_other_prod_vpc_f47ac",
		Status:          domain.TemplateRunQueued,
		TriggerActor:    domain.UserID("user_456"),
	})

	err := store.RecordTemplateRunStatus(ctx, domain.TemplateRunStatusActivityInput{
		RunID:           domain.TemplateRunID("run_123"),
		TenantID:        domain.TenantID("tenant_123"),
		StackTemplateID: domain.StackTemplateID("stack_template_123"),
		Operation:       domain.OperationPlan,
		Status:          domain.TemplateRunPlanStarted,
	})
	if err != nil {
		t.Fatalf("RecordTemplateRunStatus returned error: %v", err)
	}

	var status domain.TemplateRunStatus
	if err := pool.QueryRow(ctx, "select status from template_runs where id = $1", "run_123").Scan(&status); err != nil {
		t.Fatalf("read updated run status: %v", err)
	}
	if status != domain.TemplateRunPlanStarted {
		t.Fatalf("status = %q, want %q", status, domain.TemplateRunPlanStarted)
	}

	if err := pool.QueryRow(ctx, "select status from template_runs where id = $1", "run_456").Scan(&status); err != nil {
		t.Fatalf("read other tenant run status: %v", err)
	}
	if status != domain.TemplateRunQueued {
		t.Fatalf("other tenant status = %q, want %q", status, domain.TemplateRunQueued)
	}
}

func TestRecordTemplateRunStatusSetsCompletedAtForTerminalStatus(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)
	// A completed plan also denormalises its snapshot onto the stack template,
	// so the template has to exist for the status write to land.
	seedStackWithTemplate(t, ctx, store)
	seedTemplateRun(t, ctx, pool, domain.TemplateRun{
		ID:                 domain.TemplateRunID("run_123"),
		TenantID:           domain.TenantID("tenant_123"),
		StackTemplateID:    domain.StackTemplateID("stack_template_123"),
		TemplateRevisionID: domain.TemplateRevisionID("template_rev_2"),
		Operation:          domain.OperationPlan,
		SelectedRef:        "main",
		WorkspaceName:      "mtp_acme_prod_vpc_a13f9c",
		Status:             domain.TemplateRunLockReleased,
		TriggerActor:       domain.UserID("user_123"),
	})

	err := store.RecordTemplateRunStatus(ctx, domain.TemplateRunStatusActivityInput{
		RunID:           domain.TemplateRunID("run_123"),
		TenantID:        domain.TenantID("tenant_123"),
		StackTemplateID: domain.StackTemplateID("stack_template_123"),
		Operation:       domain.OperationPlan,
		Status:          domain.TemplateRunCompleted,
	})
	if err != nil {
		t.Fatalf("RecordTemplateRunStatus returned error: %v", err)
	}

	var status domain.TemplateRunStatus
	var completedAt time.Time
	if err := pool.QueryRow(ctx, `
		select status, completed_at
		from template_runs
		where id = $1
	`, "run_123").Scan(&status, &completedAt); err != nil {
		t.Fatalf("read terminal run status: %v", err)
	}
	if status != domain.TemplateRunCompleted {
		t.Fatalf("status = %q, want %q", status, domain.TemplateRunCompleted)
	}
	if completedAt.IsZero() {
		t.Fatal("completed_at was not set")
	}
}

func TestRecordTemplateRunStatusPersistsFailureSummary(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)
	seedTemplateRun(t, ctx, pool, domain.TemplateRun{
		ID:              domain.TemplateRunID("run_failure_summary"),
		TenantID:        domain.TenantID("tenant_123"),
		StackTemplateID: domain.StackTemplateID("stack_template_123"),
		Operation:       domain.OperationPlan,
		SelectedRef:     "main",
		WorkspaceName:   "workspace",
		Status:          domain.TemplateRunInitFinished,
		TriggerActor:    requesterSubject,
	})

	err := store.RecordTemplateRunStatus(ctx, domain.TemplateRunStatusActivityInput{
		RunID:           domain.TemplateRunID("run_failure_summary"),
		TenantID:        domain.TenantID("tenant_123"),
		StackTemplateID: domain.StackTemplateID("stack_template_123"),
		Operation:       domain.OperationPlan,
		Status:          domain.TemplateRunFailed,
		ErrorSummary:    "run terraform: activity failed",
	})
	if err != nil {
		t.Fatalf("RecordTemplateRunStatus returned error: %v", err)
	}

	var status domain.TemplateRunStatus
	var errorSummary string
	var completedAt time.Time
	if err := pool.QueryRow(ctx, `
		select status, error_summary, completed_at
		from template_runs
		where id = $1
	`, "run_failure_summary").Scan(&status, &errorSummary, &completedAt); err != nil {
		t.Fatalf("read failed run: %v", err)
	}
	if status != domain.TemplateRunFailed {
		t.Fatalf("status = %q, want %q", status, domain.TemplateRunFailed)
	}
	if errorSummary != "run terraform: activity failed" {
		t.Fatalf("error_summary = %q", errorSummary)
	}
	if completedAt.IsZero() {
		t.Fatal("completed_at was not set")
	}
}

func TestReconcileTemplateRunCancellationMarksRunFailed(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)
	seedTemplateRun(t, ctx, pool, domain.TemplateRun{
		ID:              domain.TemplateRunID("run_reconcile"),
		TenantID:        domain.TenantID("tenant_123"),
		StackTemplateID: domain.StackTemplateID("stack_template_123"),
		Operation:       domain.OperationApply,
		SelectedRef:     "main",
		WorkspaceName:   "workspace",
		Status:          domain.TemplateRunCancelRequested,
		TriggerActor:    requesterSubject,
	})

	err := store.ReconcileTemplateRunCancellation(ctx, domain.TenantID("tenant_123"), domain.TemplateRunID("run_reconcile"), "workflow closed before cancellation was processed")
	if err != nil {
		t.Fatalf("ReconcileTemplateRunCancellation returned error: %v", err)
	}

	var status domain.TemplateRunStatus
	var errorSummary string
	var completedAt time.Time
	if err := pool.QueryRow(ctx, `
		select status, error_summary, completed_at
		from template_runs
		where id = $1
	`, "run_reconcile").Scan(&status, &errorSummary, &completedAt); err != nil {
		t.Fatalf("read reconciled run: %v", err)
	}
	if status != domain.TemplateRunFailed {
		t.Fatalf("status = %q, want %q", status, domain.TemplateRunFailed)
	}
	if errorSummary != "workflow closed before cancellation was processed" {
		t.Fatalf("error_summary = %q", errorSummary)
	}
	if completedAt.IsZero() {
		t.Fatal("completed_at was not set")
	}
}

func TestReconcileTemplateRunCancellationRejectsTerminalRun(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)
	seedTemplateRun(t, ctx, pool, domain.TemplateRun{
		ID:              domain.TemplateRunID("run_reconcile_terminal"),
		TenantID:        domain.TenantID("tenant_123"),
		StackTemplateID: domain.StackTemplateID("stack_template_123"),
		Operation:       domain.OperationApply,
		SelectedRef:     "main",
		WorkspaceName:   "workspace",
		Status:          domain.TemplateRunCompleted,
		TriggerActor:    requesterSubject,
	})

	err := store.ReconcileTemplateRunCancellation(ctx, domain.TenantID("tenant_123"), domain.TemplateRunID("run_reconcile_terminal"), "should not overwrite terminal run")
	if !errors.Is(err, app.ErrRunNotCancelable) {
		t.Fatalf("error = %v, want ErrRunNotCancelable", err)
	}
}

func TestRecordTemplateRunStatusUpdatesStackTemplateLastAppliedForSuccessfulApply(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)
	stack := domain.Stack{
		ID:        domain.StackID("stack_123"),
		TenantID:  domain.TenantID("tenant_123"),
		Name:      "Acme Prod",
		Slug:      "acme-prod",
		CreatedBy: domain.UserID("user_123"),
		CreatedAt: time.Now().UTC(),
	}
	if err := store.CreateStack(ctx, stack); err != nil {
		t.Fatalf("CreateStack returned error: %v", err)
	}
	stackTemplate := domain.StackTemplate{
		ID:                        domain.StackTemplateID("stack_template_123"),
		TenantID:                  domain.TenantID("tenant_123"),
		StackID:                   domain.StackID("stack_123"),
		SourceTemplateID:          domain.SourceTemplateID("source_template_vpc"),
		DesiredTemplateRevisionID: domain.TemplateRevisionID("template_rev_2"),
		WorkspaceName:             "mtp_acme_prod_vpc_a13f9c",
		InstalledConfigJSON:       json.RawMessage(`{"region":"us-east-1"}`),
		DesiredConfigJSON:         json.RawMessage(`{"region":"us-east-1"}`),
		CreatedBy:                 domain.UserID("installer_123"),
		Lifecycle:                 domain.StackTemplateActive,
	}
	if err := store.CreateStackTemplate(ctx, stackTemplate); err != nil {
		t.Fatalf("CreateStackTemplate returned error: %v", err)
	}
	seedTemplateRun(t, ctx, pool, domain.TemplateRun{
		ID:                 domain.TemplateRunID("run_123"),
		TenantID:           domain.TenantID("tenant_123"),
		StackTemplateID:    domain.StackTemplateID("stack_template_123"),
		TemplateRevisionID: domain.TemplateRevisionID("template_rev_2"),
		SourceTemplateID:   domain.SourceTemplateID("source_template_vpc"),
		Operation:          domain.OperationApply,
		SelectedRef:        "release-2026-07-08",
		WorkspaceName:      "mtp_acme_prod_vpc_a13f9c",
		Status:             domain.TemplateRunApplyStarted,
		TriggerActor:       domain.UserID("user_123"),
	})

	err := store.RecordTemplateRunStatus(ctx, domain.TemplateRunStatusActivityInput{
		RunID:           domain.TemplateRunID("run_123"),
		TenantID:        domain.TenantID("tenant_123"),
		StackTemplateID: domain.StackTemplateID("stack_template_123"),
		Operation:       domain.OperationApply,
		Status:          domain.TemplateRunApplyFinished,
	})
	if err != nil {
		t.Fatalf("RecordTemplateRunStatus returned error: %v", err)
	}

	var lastAppliedRunID domain.TemplateRunID
	var lastAppliedTemplateRevisionID domain.TemplateRevisionID
	var lastAppliedAt time.Time
	if err := pool.QueryRow(ctx, `
		select last_applied_run_id, last_applied_template_revision_id, last_applied_at
		from stack_templates
		where tenant_id = $1
			and id = $2
	`, "tenant_123", "stack_template_123").Scan(
		&lastAppliedRunID,
		&lastAppliedTemplateRevisionID,
		&lastAppliedAt,
	); err != nil {
		t.Fatalf("read stack template last applied fields: %v", err)
	}
	if lastAppliedRunID != domain.TemplateRunID("run_123") {
		t.Fatalf("LastAppliedRunID = %q, want run_123", lastAppliedRunID)
	}
	// The applied revision is the durable fact. The run's ref is kept on the run
	// itself; copying it onto the component gave it a ref it does not own.
	if lastAppliedTemplateRevisionID != domain.TemplateRevisionID("template_rev_2") {
		t.Fatalf("LastAppliedTemplateRevisionID = %q, want template_rev_2", lastAppliedTemplateRevisionID)
	}
	if lastAppliedAt.IsZero() {
		t.Fatal("LastAppliedAt was not set")
	}
}

func TestRecordTemplateRunStatusReturnsNotFoundForOtherTenant(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)
	seedTemplateRun(t, ctx, pool, domain.TemplateRun{
		ID:              domain.TemplateRunID("run_123"),
		TenantID:        domain.TenantID("tenant_123"),
		StackTemplateID: domain.StackTemplateID("stack_template_123"),
		Operation:       domain.OperationPlan,
		SelectedRef:     "main",
		WorkspaceName:   "mtp_acme_prod_vpc_a13f9c",
		Status:          domain.TemplateRunQueued,
		TriggerActor:    domain.UserID("user_123"),
	})

	err := store.RecordTemplateRunStatus(ctx, domain.TemplateRunStatusActivityInput{
		RunID:           domain.TemplateRunID("run_123"),
		TenantID:        domain.TenantID("tenant_456"),
		StackTemplateID: domain.StackTemplateID("stack_template_123"),
		Operation:       domain.OperationPlan,
		Status:          domain.TemplateRunPlanStarted,
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}
}

func TestRecordTemplateRunStatusUpdatesStackTemplateLastPlannedForCompletedPlan(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)
	seedStackWithTemplate(t, ctx, store)
	seedTemplateRun(t, ctx, pool, domain.TemplateRun{
		ID:                 domain.TemplateRunID("run_plan_1"),
		TenantID:           domain.TenantID("tenant_123"),
		StackTemplateID:    domain.StackTemplateID("stack_template_123"),
		TemplateRevisionID: domain.TemplateRevisionID("template_rev_2"),
		SourceTemplateID:   domain.SourceTemplateID("source_template_vpc"),
		Operation:          domain.OperationPlan,
		SelectedRef:        "main",
		WorkspaceName:      "mtp_acme_prod_vpc_a13f9c",
		ConfigJSON:         json.RawMessage(`{"region":"us-east-1"}`),
		Status:             domain.TemplateRunPlanFinished,
		TriggerActor:       domain.UserID("user_123"),
	})

	if err := store.RecordTemplateRunStatus(ctx, domain.TemplateRunStatusActivityInput{
		RunID:           domain.TemplateRunID("run_plan_1"),
		TenantID:        domain.TenantID("tenant_123"),
		StackTemplateID: domain.StackTemplateID("stack_template_123"),
		Operation:       domain.OperationPlan,
		Status:          domain.TemplateRunCompleted,
	}); err != nil {
		t.Fatalf("RecordTemplateRunStatus returned error: %v", err)
	}

	stackTemplate, err := store.GetStackTemplate(ctx, domain.TenantID("tenant_123"), domain.StackTemplateID("stack_template_123"))
	if err != nil {
		t.Fatalf("GetStackTemplate returned error: %v", err)
	}
	if stackTemplate.LastPlannedRunID != domain.TemplateRunID("run_plan_1") {
		t.Fatalf("LastPlannedRunID = %q, want run_plan_1", stackTemplate.LastPlannedRunID)
	}
	if stackTemplate.LastPlannedTemplateRevisionID != domain.TemplateRevisionID("template_rev_2") {
		t.Fatalf("LastPlannedTemplateRevisionID = %q, want template_rev_2", stackTemplate.LastPlannedTemplateRevisionID)
	}
	if stackTemplate.LastPlannedAt.IsZero() {
		t.Fatal("LastPlannedAt was not set")
	}
	// The point of recording it: the plan snapshot equals desired, so an apply
	// would run exactly what this plan showed. Note the config comes back with
	// jsonb's own key spacing, which is why the comparison parses rather than
	// comparing bytes.
	if stackTemplate.PlanState() != domain.PlanMatches {
		t.Fatalf("PlanState() = %q, want matches (planned config %s, desired %s)", stackTemplate.PlanState(), stackTemplate.LastPlannedConfigJSON, stackTemplate.DesiredConfig())
	}
	if stackTemplate.LiveState() != domain.LiveNever {
		t.Fatalf("LiveState() = %q, want never", stackTemplate.LiveState())
	}

	// Saving config moves desired without touching runs; the recorded plan is
	// what makes that visible instead of leaving apply enabled.
	if _, err := store.UpdateStackTemplateConfig(ctx, domain.TenantID("tenant_123"), domain.StackTemplateID("stack_template_123"), json.RawMessage(`{"region":"eu-west-1"}`)); err != nil {
		t.Fatalf("UpdateStackTemplateConfig returned error: %v", err)
	}
	edited, err := store.GetStackTemplate(ctx, domain.TenantID("tenant_123"), domain.StackTemplateID("stack_template_123"))
	if err != nil {
		t.Fatalf("GetStackTemplate returned error: %v", err)
	}
	if edited.PlanState() != domain.PlanStale {
		t.Fatalf("PlanState() after config edit = %q, want stale", edited.PlanState())
	}
}

func TestRecordTemplateRunStatusRecordsAppliedConfigAlongsideRevision(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)
	seedStackWithTemplate(t, ctx, store)
	seedTemplateRun(t, ctx, pool, domain.TemplateRun{
		ID:                 domain.TemplateRunID("run_apply_1"),
		TenantID:           domain.TenantID("tenant_123"),
		StackTemplateID:    domain.StackTemplateID("stack_template_123"),
		TemplateRevisionID: domain.TemplateRevisionID("template_rev_2"),
		SourceTemplateID:   domain.SourceTemplateID("source_template_vpc"),
		Operation:          domain.OperationApply,
		SelectedRef:        "main",
		WorkspaceName:      "mtp_acme_prod_vpc_a13f9c",
		ConfigJSON:         json.RawMessage(`{"region":"us-east-1"}`),
		Status:             domain.TemplateRunApplyStarted,
		TriggerActor:       domain.UserID("user_123"),
	})

	if err := store.RecordTemplateRunStatus(ctx, domain.TemplateRunStatusActivityInput{
		RunID:           domain.TemplateRunID("run_apply_1"),
		TenantID:        domain.TenantID("tenant_123"),
		StackTemplateID: domain.StackTemplateID("stack_template_123"),
		Operation:       domain.OperationApply,
		Status:          domain.TemplateRunApplyFinished,
	}); err != nil {
		t.Fatalf("RecordTemplateRunStatus returned error: %v", err)
	}

	stackTemplate, err := store.GetStackTemplate(ctx, domain.TenantID("tenant_123"), domain.StackTemplateID("stack_template_123"))
	if err != nil {
		t.Fatalf("GetStackTemplate returned error: %v", err)
	}
	if stackTemplate.LiveState() != domain.LiveMatches {
		t.Fatalf("LiveState() = %q, want matches (applied config %s)", stackTemplate.LiveState(), stackTemplate.LastAppliedConfigJSON)
	}
}

// The columns are nullable so that "never planned" stays distinct from
// "planned with an empty config", which is what an all-optional template looks
// like. A default of '{}' would erase that difference.
func TestStackTemplateSnapshotConfigsStartAbsentRatherThanEmpty(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)
	seedStackWithTemplate(t, ctx, store)

	var plannedConfig, appliedConfig *string
	if err := pool.QueryRow(ctx, `
		select last_planned_config_json, last_applied_config_json
		from stack_templates
		where tenant_id = $1
			and id = $2
	`, "tenant_123", "stack_template_123").Scan(&plannedConfig, &appliedConfig); err != nil {
		t.Fatalf("read snapshot configs: %v", err)
	}
	if plannedConfig != nil || appliedConfig != nil {
		t.Fatalf("snapshot configs = %v / %v, want both null", plannedConfig, appliedConfig)
	}

	stackTemplate, err := store.GetStackTemplate(ctx, domain.TenantID("tenant_123"), domain.StackTemplateID("stack_template_123"))
	if err != nil {
		t.Fatalf("GetStackTemplate returned error: %v", err)
	}
	if stackTemplate.PlanState() != domain.PlanNone || stackTemplate.LiveState() != domain.LiveNever {
		t.Fatalf("states = %q / %q, want none / never", stackTemplate.PlanState(), stackTemplate.LiveState())
	}
}

// The backfill is what makes the new columns true for templates that already
// exist. Without it every installed template would read as "never planned" and
// apply would be blocked until someone re-planned.
func TestPlannedStateMigrationBackfillsFromExistingRuns(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openTestPool(t, ctx)
	migrateThrough(t, ctx, pool, "0014_retire_workflow_outbox")

	_, err := pool.Exec(ctx, `
		insert into stack_templates (
			id, tenant_id, stack_id, component_key, source_template_id,
			desired_template_revision_id, last_applied_template_revision_id,
			selected_ref, workspace_name, config_json, desired_config_json,
			last_applied_run_id, last_applied_ref, lifecycle
		) values (
			'stack_template_123', 'tenant_123', 'stack_123', 'primary', 'source_vpc',
			'template_rev_2', 'template_rev_2',
			'main', 'workspace', '{"region":"us-east-1"}'::jsonb, '{"region":"us-east-1"}'::jsonb,
			'run_apply_1', 'main', 'active'
		);
		insert into template_runs (
			id, tenant_id, stack_template_id, template_revision_id, source_template_id,
			operation, selected_ref, workspace_name, config_json, status, trigger_actor,
			started_at, completed_at
		) values
			('run_apply_1', 'tenant_123', 'stack_template_123', 'template_rev_2', 'source_vpc',
			 'apply', 'main', 'workspace', '{"region":"us-east-1"}'::jsonb, 'completed', 'user_123',
			 now() - interval '2 hours', now() - interval '2 hours'),
			-- Two completed plans: the backfill must take the newer one, ordered
			-- the way ListTemplateRuns orders runs.
			('run_plan_old', 'tenant_123', 'stack_template_123', 'template_rev_1', 'source_vpc',
			 'plan', 'main', 'workspace', '{"region":"ap-south-1"}'::jsonb, 'completed', 'user_123',
			 now() - interval '3 hours', now() - interval '3 hours'),
			('run_plan_new', 'tenant_123', 'stack_template_123', 'template_rev_2', 'source_vpc',
			 'plan', 'main', 'workspace', '{"region":"us-east-1"}'::jsonb, 'completed', 'user_123',
			 now() - interval '1 hour', now() - interval '1 hour'),
			-- A failed plan is not a reviewable one.
			('run_plan_failed', 'tenant_123', 'stack_template_123', 'template_rev_2', 'source_vpc',
			 'plan', 'main', 'workspace', '{"region":"nowhere"}'::jsonb, 'failed', 'user_123',
			 now(), now());
	`)
	if err != nil {
		t.Fatalf("seed pre-migration rows: %v", err)
	}

	// Migrate to head rather than to 0015 alone: the assertions are about the
	// 0015 backfill, but the store reads the current schema.
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate to head: %v", err)
	}

	store := NewStore(pool)
	stackTemplate, err := store.GetStackTemplate(ctx, domain.TenantID("tenant_123"), domain.StackTemplateID("stack_template_123"))
	if err != nil {
		t.Fatalf("GetStackTemplate returned error: %v", err)
	}
	if stackTemplate.LastPlannedRunID != domain.TemplateRunID("run_plan_new") {
		t.Fatalf("LastPlannedRunID = %q, want run_plan_new", stackTemplate.LastPlannedRunID)
	}
	if stackTemplate.LastPlannedAt.IsZero() {
		t.Fatal("LastPlannedAt was not backfilled")
	}
	// Both snapshots equal desired, so the template lands in steady state
	// rather than being told to re-plan something it already applied.
	if stackTemplate.PlanState() != domain.PlanMatches {
		t.Fatalf("PlanState() = %q, want matches", stackTemplate.PlanState())
	}
	if stackTemplate.LiveState() != domain.LiveMatches {
		t.Fatalf("LiveState() = %q, want matches", stackTemplate.LiveState())
	}
}

// A template that never ran keeps both snapshots absent through the migration.
func TestPlannedStateMigrationLeavesUnplannedTemplatesAbsent(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openTestPool(t, ctx)
	migrateThrough(t, ctx, pool, "0014_retire_workflow_outbox")

	if _, err := pool.Exec(ctx, `
		insert into stack_templates (
			id, tenant_id, stack_id, component_key, source_template_id,
			desired_template_revision_id, last_applied_template_revision_id,
			selected_ref, workspace_name, config_json, desired_config_json,
			last_applied_run_id, last_applied_ref, lifecycle
		) values (
			'stack_template_456', 'tenant_123', 'stack_123', 'primary', 'source_vpc',
			'template_rev_2', '',
			'main', 'workspace', '{}'::jsonb, '{}'::jsonb,
			'', '', 'active'
		)
	`); err != nil {
		t.Fatalf("seed pre-migration rows: %v", err)
	}

	// Migrate to head rather than to 0015 alone: the assertions are about the
	// 0015 backfill, but the store reads the current schema.
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate to head: %v", err)
	}

	stackTemplate, err := NewStore(pool).GetStackTemplate(ctx, domain.TenantID("tenant_123"), domain.StackTemplateID("stack_template_456"))
	if err != nil {
		t.Fatalf("GetStackTemplate returned error: %v", err)
	}
	// An all-optional template's config is '{}' on both sides, so absence has
	// to be carried by the run pointers rather than by the config being empty.
	if stackTemplate.LastPlannedConfigJSON != nil || stackTemplate.LastAppliedConfigJSON != nil {
		t.Fatalf("snapshot configs = %s / %s, want both absent", stackTemplate.LastPlannedConfigJSON, stackTemplate.LastAppliedConfigJSON)
	}
	if stackTemplate.PlanState() != domain.PlanNone || stackTemplate.LiveState() != domain.LiveNever {
		t.Fatalf("states = %q / %q, want none / never", stackTemplate.PlanState(), stackTemplate.LiveState())
	}
}

// The ref columns are gone from the component. A ref belongs to the revision,
// which is immutable per commit, so it cannot go stale there; on the component
// it was a copy that nothing updated when the desired revision changed.
func TestStackTemplatesNoLongerCarryRefColumns(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)

	for _, column := range []string{"selected_ref", "last_applied_ref"} {
		var exists bool
		if err := pool.QueryRow(ctx, `
			select exists (
				select 1
				from information_schema.columns
				where table_schema = current_schema()
					and table_name = 'stack_templates'
					and column_name = $1
			)
		`, column).Scan(&exists); err != nil {
			t.Fatalf("check stack_templates.%s: %v", column, err)
		}
		if exists {
			t.Fatalf("stack_templates.%s still exists", column)
		}
	}

	// The run keeps its own ref: there it is provenance of what was started,
	// frozen at start, not a claim about the present.
	var runRefExists bool
	if err := pool.QueryRow(ctx, `
		select exists (
			select 1
			from information_schema.columns
			where table_schema = current_schema()
				and table_name = 'template_runs'
				and column_name = 'selected_ref'
		)
	`).Scan(&runRefExists); err != nil {
		t.Fatalf("check template_runs.selected_ref: %v", err)
	}
	if !runRefExists {
		t.Fatal("template_runs.selected_ref was dropped; runs must keep their own ref")
	}
}

// config_json meant two things: the config captured at install, and a fallback
// for desired state. The fallback was unreachable for persisted rows, so the
// column is named for the one meaning it has left.
func TestStackTemplateInstallConfigIsNamedForWhatItHolds(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)

	columns := map[string]bool{}
	rows, err := pool.Query(ctx, `
		select column_name
		from information_schema.columns
		where table_schema = current_schema()
			and table_name = 'stack_templates'
	`)
	if err != nil {
		t.Fatalf("read stack_templates columns: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan column name: %v", err)
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate columns: %v", err)
	}

	if !columns["installed_config_json"] {
		t.Fatal("stack_templates.installed_config_json is missing")
	}
	if columns["config_json"] {
		t.Fatal("stack_templates.config_json still exists; the rename did not apply")
	}
	// Desired state keeps its own column, and it is what runs read.
	if !columns["desired_config_json"] {
		t.Fatal("stack_templates.desired_config_json is missing")
	}
}

func openMigratedTestPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()

	pool := openTestPool(t, ctx)
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate test schema: %v", err)
	}

	return pool
}

// seedStackWithTemplate creates the stack and one active stack template on it,
// which is the minimum a run needs in order to record its snapshot back onto
// the template.
func seedStackWithTemplate(t *testing.T, ctx context.Context, store *Store) {
	t.Helper()

	if err := store.CreateStack(ctx, domain.Stack{
		ID:        domain.StackID("stack_123"),
		TenantID:  domain.TenantID("tenant_123"),
		Name:      "Acme Prod",
		Slug:      "acme-prod",
		CreatedBy: domain.UserID("user_123"),
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("CreateStack returned error: %v", err)
	}
	if err := store.CreateStackTemplate(ctx, domain.StackTemplate{
		ID:                        domain.StackTemplateID("stack_template_123"),
		TenantID:                  domain.TenantID("tenant_123"),
		StackID:                   domain.StackID("stack_123"),
		SourceTemplateID:          domain.SourceTemplateID("source_template_vpc"),
		DesiredTemplateRevisionID: domain.TemplateRevisionID("template_rev_2"),
		WorkspaceName:             "mtp_acme_prod_vpc_a13f9c",
		InstalledConfigJSON:       json.RawMessage(`{"region":"us-east-1"}`),
		DesiredConfigJSON:         json.RawMessage(`{"region":"us-east-1"}`),
		CreatedBy:                 domain.UserID("installer_123"),
		Lifecycle:                 domain.StackTemplateActive,
	}); err != nil {
		t.Fatalf("CreateStackTemplate returned error: %v", err)
	}
}

func seedTemplateRun(t *testing.T, ctx context.Context, pool *pgxpool.Pool, run domain.TemplateRun) {
	t.Helper()

	_, err := pool.Exec(ctx, `
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
		t.Fatalf("seed template run: %v", err)
	}
}

// sameJSON compares two JSON documents by value. Postgres reformats jsonb on
// the way out — a space after every colon, object keys reordered — so comparing
// the bytes it returns against the literal that went in fails for reasons that
// have nothing to do with the value stored.
func sameJSON(t *testing.T, got, want []byte) bool {
	t.Helper()

	var gotValue, wantValue any
	if err := json.Unmarshal(got, &gotValue); err != nil {
		t.Fatalf("decode got JSON %s: %v", got, err)
	}
	if err := json.Unmarshal(want, &wantValue); err != nil {
		t.Fatalf("decode want JSON %s: %v", want, err)
	}
	return reflect.DeepEqual(gotValue, wantValue)
}

func openTestPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()

	dsn := os.Getenv("tflive_POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("tflive_POSTGRES_TEST_DSN is not set")
	}

	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("open admin postgres pool: %v", err)
	}
	t.Cleanup(admin.Close)

	schema := fmt.Sprintf("tflive_test_%d_%d", time.Now().UnixNano(), atomic.AddUint64(&testSchemaCounter, 1))
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "create schema "+quotedSchema); err != nil {
		t.Fatalf("create test schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), "drop schema "+quotedSchema+" cascade")
	})

	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse postgres test dsn: %v", err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatalf("open postgres test pool: %v", err)
	}
	t.Cleanup(pool.Close)

	return pool
}

func TestAppendAuditEventSuccess(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)

	event := domain.SecurityAuditEvent{
		ActorSubject:  "user:6fdb4b4c-2a8f-4cf7-945f-38f67f6a0e91",
		Action:        domain.AuditActionGrant,
		TargetUser:    "user:target-subject",
		TenantID:      domain.TenantID("tenant_123"),
		StackID:       domain.StackID("stack_123"),
		OldRole:       "",
		NewRole:       "owner",
		Outcome:       domain.AuditOutcomeSuccess,
		CorrelationID: "req_abc123",
	}

	if err := store.AppendAuditEvent(ctx, event); err != nil {
		t.Fatalf("AppendAuditEvent returned error: %v", err)
	}

	var got struct {
		ID            int64
		ActorSubject  string
		Action        string
		TargetUser    string
		TenantID      string
		StackID       string
		OldRole       string
		NewRole       string
		Outcome       string
		CorrelationID string
		RecordedAt    time.Time
	}
	err := pool.QueryRow(ctx, `
		SELECT id, actor_subject, action, target_user, tenant_id, stack_id,
		       old_role, new_role, outcome, correlation_id, recorded_at
		FROM security_audit_log
		ORDER BY id DESC LIMIT 1
	`).Scan(
		&got.ID, &got.ActorSubject, &got.Action, &got.TargetUser,
		&got.TenantID, &got.StackID, &got.OldRole, &got.NewRole,
		&got.Outcome, &got.CorrelationID, &got.RecordedAt,
	)
	if err != nil {
		t.Fatalf("read audit event: %v", err)
	}

	if got.ActorSubject != event.ActorSubject {
		t.Fatalf("actor_subject = %q, want %q", got.ActorSubject, event.ActorSubject)
	}
	if got.Action != "grant" {
		t.Fatalf("action = %q, want grant", got.Action)
	}
	if got.TargetUser != "user:target-subject" {
		t.Fatalf("target_user = %q, want user:target-subject", got.TargetUser)
	}
	if got.TenantID != "tenant_123" {
		t.Fatalf("tenant_id = %q, want tenant_123", got.TenantID)
	}
	if got.StackID != "stack_123" {
		t.Fatalf("stack_id = %q, want stack_123", got.StackID)
	}
	if got.OldRole != "" {
		t.Fatalf("old_role = %q, want empty", got.OldRole)
	}
	if got.NewRole != "owner" {
		t.Fatalf("new_role = %q, want owner", got.NewRole)
	}
	if got.Outcome != "success" {
		t.Fatalf("outcome = %q, want success", got.Outcome)
	}
	if got.CorrelationID != "req_abc123" {
		t.Fatalf("correlation_id = %q, want req_abc123", got.CorrelationID)
	}
	if got.RecordedAt.IsZero() {
		t.Fatal("recorded_at is zero, want populated by database default")
	}
	if got.ID <= 0 {
		t.Fatalf("id = %d, want positive", got.ID)
	}
}

// TestRecordTemplateRunStatusSetsStackTemplateLifecycleToDestroying verifies
// that reaching the destroy_started status on an OperationDestroy run updates
// the associated stack_template row to lifecycle = 'destroying'.
func TestRecordTemplateRunStatusSetsStackTemplateLifecycleToDestroying(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)
	stack := domain.Stack{
		ID:        domain.StackID("stack_destroy_1"),
		TenantID:  domain.TenantID("tenant_destroy_1"),
		Name:      "Destroy Test",
		Slug:      "destroy-test",
		CreatedBy: domain.UserID("user_123"),
		CreatedAt: time.Now().UTC(),
	}
	if err := store.CreateStack(ctx, stack); err != nil {
		t.Fatalf("CreateStack returned error: %v", err)
	}
	stackTemplate := domain.StackTemplate{
		ID:        domain.StackTemplateID("st_destroy_1"),
		TenantID:  domain.TenantID("tenant_destroy_1"),
		StackID:   domain.StackID("stack_destroy_1"),
		Lifecycle: domain.StackTemplateActive,
	}
	if err := store.CreateStackTemplate(ctx, stackTemplate); err != nil {
		t.Fatalf("CreateStackTemplate returned error: %v", err)
	}
	seedTemplateRun(t, ctx, pool, domain.TemplateRun{
		ID:              domain.TemplateRunID("run_destroy_1"),
		TenantID:        domain.TenantID("tenant_destroy_1"),
		StackTemplateID: domain.StackTemplateID("st_destroy_1"),
		Operation:       domain.OperationDestroy,
		SelectedRef:     "main",
		WorkspaceName:   "ws_destroy",
		Status:          domain.TemplateRunLocked,
		TriggerActor:    domain.UserID("user_123"),
	})

	err := store.RecordTemplateRunStatus(ctx, domain.TemplateRunStatusActivityInput{
		RunID:           domain.TemplateRunID("run_destroy_1"),
		TenantID:        domain.TenantID("tenant_destroy_1"),
		StackTemplateID: domain.StackTemplateID("st_destroy_1"),
		Operation:       domain.OperationDestroy,
		Status:          domain.TemplateRunDestroyStarted,
	})
	if err != nil {
		t.Fatalf("RecordTemplateRunStatus returned error: %v", err)
	}

	var lifecycle domain.StackTemplateLifecycle
	if err := pool.QueryRow(ctx, `
		select lifecycle from stack_templates
		where tenant_id = $1 and id = $2
	`, "tenant_destroy_1", "st_destroy_1").Scan(&lifecycle); err != nil {
		t.Fatalf("read stack template lifecycle: %v", err)
	}
	if lifecycle != domain.StackTemplateDestroying {
		t.Fatalf("lifecycle = %q, want %q", lifecycle, domain.StackTemplateDestroying)
	}
}

// TestRecordTemplateRunStatusSetsStackTemplateLifecycleToDestroyed verifies
// that reaching the destroyed status on an OperationDestroy run updates the
// associated stack_template row to lifecycle = 'destroyed', which causes it to
// be excluded from GetStackWithTemplates results.
func TestRecordTemplateRunStatusSetsStackTemplateLifecycleToDestroyed(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)
	stack := domain.Stack{
		ID:        domain.StackID("stack_destroy_2"),
		TenantID:  domain.TenantID("tenant_destroy_2"),
		Name:      "Destroy Done",
		Slug:      "destroy-done",
		CreatedBy: domain.UserID("user_123"),
		CreatedAt: time.Now().UTC(),
	}
	if err := store.CreateStack(ctx, stack); err != nil {
		t.Fatalf("CreateStack returned error: %v", err)
	}
	stackTemplate := domain.StackTemplate{
		ID:        domain.StackTemplateID("st_destroy_2"),
		TenantID:  domain.TenantID("tenant_destroy_2"),
		StackID:   domain.StackID("stack_destroy_2"),
		Lifecycle: domain.StackTemplateDestroying,
	}
	if err := store.CreateStackTemplate(ctx, stackTemplate); err != nil {
		t.Fatalf("CreateStackTemplate returned error: %v", err)
	}
	seedTemplateRun(t, ctx, pool, domain.TemplateRun{
		ID:              domain.TemplateRunID("run_destroy_2"),
		TenantID:        domain.TenantID("tenant_destroy_2"),
		StackTemplateID: domain.StackTemplateID("st_destroy_2"),
		Operation:       domain.OperationDestroy,
		SelectedRef:     "main",
		WorkspaceName:   "ws_destroy_2",
		Status:          domain.TemplateRunDestroyStarted,
		TriggerActor:    domain.UserID("user_123"),
	})

	err := store.RecordTemplateRunStatus(ctx, domain.TemplateRunStatusActivityInput{
		RunID:           domain.TemplateRunID("run_destroy_2"),
		TenantID:        domain.TenantID("tenant_destroy_2"),
		StackTemplateID: domain.StackTemplateID("st_destroy_2"),
		Operation:       domain.OperationDestroy,
		Status:          domain.TemplateRunDestroyFinished,
	})
	if err != nil {
		t.Fatalf("RecordTemplateRunStatus returned error: %v", err)
	}

	var lifecycle domain.StackTemplateLifecycle
	if err := pool.QueryRow(ctx, `
		select lifecycle from stack_templates
		where tenant_id = $1 and id = $2
	`, "tenant_destroy_2", "st_destroy_2").Scan(&lifecycle); err != nil {
		t.Fatalf("read stack template lifecycle: %v", err)
	}
	if lifecycle != domain.StackTemplateDestroyed {
		t.Fatalf("lifecycle = %q, want %q", lifecycle, domain.StackTemplateDestroyed)
	}
}

func TestRecordsStackTemplateDestroyInterrupted(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		operation domain.OperationType
		status    domain.TemplateRunStatus
		want      bool
	}{
		{
			name:      "failed destroy",
			operation: domain.OperationDestroy,
			status:    domain.TemplateRunFailed,
			want:      true,
		},
		{
			name:      "canceled destroy",
			operation: domain.OperationDestroy,
			status:    domain.TemplateRunCanceled,
			want:      true,
		},
		{
			name:      "successful destroy",
			operation: domain.OperationDestroy,
			status:    domain.TemplateRunDestroyFinished,
			want:      false,
		},
		{
			name:      "failed apply",
			operation: domain.OperationApply,
			status:    domain.TemplateRunFailed,
			want:      false,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			input := domain.TemplateRunStatusActivityInput{
				Operation: test.operation,
				Status:    test.status,
			}
			if got := recordsStackTemplateDestroyInterrupted(input); got != test.want {
				t.Fatalf("recordsStackTemplateDestroyInterrupted(%q, %q) = %t, want %t", test.operation, test.status, got, test.want)
			}
		})
	}
}

func TestRecordTemplateRunStatusReconcilesInterruptedDestroyLifecycle(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		initialLifecycle domain.StackTemplateLifecycle
		initialStatus    domain.TemplateRunStatus
		terminalStatus   domain.TemplateRunStatus
		wantLifecycle    domain.StackTemplateLifecycle
	}{
		{
			name:             "failed after destroy started",
			initialLifecycle: domain.StackTemplateDestroying,
			initialStatus:    domain.TemplateRunDestroyStarted,
			terminalStatus:   domain.TemplateRunFailed,
			wantLifecycle:    domain.StackTemplateFailed,
		},
		{
			name:             "canceled after destroy started",
			initialLifecycle: domain.StackTemplateDestroying,
			initialStatus:    domain.TemplateRunDestroyStarted,
			terminalStatus:   domain.TemplateRunCanceled,
			wantLifecycle:    domain.StackTemplateFailed,
		},
		{
			name:             "failed before destroy started",
			initialLifecycle: domain.StackTemplateActive,
			initialStatus:    domain.TemplateRunWaitingApproval,
			terminalStatus:   domain.TemplateRunFailed,
			wantLifecycle:    domain.StackTemplateActive,
		},
		{
			name:             "retries after lifecycle failure",
			initialLifecycle: domain.StackTemplateFailed,
			initialStatus:    domain.TemplateRunDestroyStarted,
			terminalStatus:   domain.TemplateRunFailed,
			wantLifecycle:    domain.StackTemplateFailed,
		},
		{
			name:             "late failure after destroy completed",
			initialLifecycle: domain.StackTemplateDestroyed,
			initialStatus:    domain.TemplateRunDestroyFinished,
			terminalStatus:   domain.TemplateRunFailed,
			wantLifecycle:    domain.StackTemplateDestroyed,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			pool := openMigratedTestPool(t, ctx)
			store := NewStore(pool)
			suffix := strings.ReplaceAll(test.name, " ", "_")
			stackID := domain.StackID("stack_destroy_interrupted_" + suffix)
			stackTemplateID := domain.StackTemplateID("st_destroy_interrupted_" + suffix)
			tenantID := domain.TenantID("tenant_destroy_interrupted_" + suffix)
			runID := domain.TemplateRunID("run_destroy_interrupted_" + suffix)

			if err := store.CreateStack(ctx, domain.Stack{
				ID:        stackID,
				TenantID:  tenantID,
				Name:      "Interrupted Destroy Test",
				Slug:      "interrupted-destroy-" + suffix,
				CreatedBy: domain.UserID("user_123"),
				CreatedAt: time.Now().UTC(),
			}); err != nil {
				t.Fatalf("CreateStack returned error: %v", err)
			}
			if err := store.CreateStackTemplate(ctx, domain.StackTemplate{
				ID:        stackTemplateID,
				TenantID:  tenantID,
				StackID:   stackID,
				Lifecycle: test.initialLifecycle,
			}); err != nil {
				t.Fatalf("CreateStackTemplate returned error: %v", err)
			}
			seedTemplateRun(t, ctx, pool, domain.TemplateRun{
				ID:              runID,
				TenantID:        tenantID,
				StackTemplateID: stackTemplateID,
				Operation:       domain.OperationDestroy,
				SelectedRef:     "main",
				WorkspaceName:   "ws_" + suffix,
				Status:          test.initialStatus,
				TriggerActor:    domain.UserID("user_123"),
			})

			if err := store.RecordTemplateRunStatus(ctx, domain.TemplateRunStatusActivityInput{
				RunID:           runID,
				TenantID:        tenantID,
				StackTemplateID: stackTemplateID,
				Operation:       domain.OperationDestroy,
				Status:          test.terminalStatus,
			}); err != nil {
				t.Fatalf("RecordTemplateRunStatus returned error: %v", err)
			}

			var lifecycle domain.StackTemplateLifecycle
			if err := pool.QueryRow(ctx, `
				select lifecycle from stack_templates
				where tenant_id = $1 and id = $2
			`, tenantID, stackTemplateID).Scan(&lifecycle); err != nil {
				t.Fatalf("read stack template lifecycle: %v", err)
			}
			if lifecycle != test.wantLifecycle {
				t.Fatalf("lifecycle = %q, want %q", lifecycle, test.wantLifecycle)
			}
		})
	}
}

func TestAppendAuditEventEmptyOptionalFields(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)

	event := domain.SecurityAuditEvent{
		ActorSubject: "user:actor",
		Action:       domain.AuditActionFailedAccessAttempt,
		TenantID:     domain.TenantID("tenant_123"),
		Outcome:      domain.AuditOutcomeFailure,
	}

	if err := store.AppendAuditEvent(ctx, event); err != nil {
		t.Fatalf("AppendAuditEvent returned error: %v", err)
	}

	var targetUser, stackID, oldRole, newRole, correlationID string
	err := pool.QueryRow(ctx, `
		SELECT target_user, stack_id, old_role, new_role, correlation_id
		FROM security_audit_log ORDER BY id DESC LIMIT 1
	`).Scan(&targetUser, &stackID, &oldRole, &newRole, &correlationID)
	if err != nil {
		t.Fatalf("read audit event: %v", err)
	}

	if targetUser != "" {
		t.Fatalf("target_user = %q, want empty", targetUser)
	}
	if stackID != "" {
		t.Fatalf("stack_id = %q, want empty", stackID)
	}
	if oldRole != "" {
		t.Fatalf("old_role = %q, want empty", oldRole)
	}
	if newRole != "" {
		t.Fatalf("new_role = %q, want empty", newRole)
	}
	if correlationID != "" {
		t.Fatalf("correlation_id = %q, want empty", correlationID)
	}
}

func TestWorkQueueMigrationDefinesCoalescingQueue(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)

	columns := map[string]string{}
	rows, err := pool.Query(ctx, `
		select column_name, data_type
		from information_schema.columns
		where table_name = 'work_queue' and table_schema = current_schema()
	`)
	if err != nil {
		t.Fatalf("query work_queue columns: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name, dataType string
		if err := rows.Scan(&name, &dataType); err != nil {
			t.Fatalf("scan column: %v", err)
		}
		columns[name] = dataType
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate columns: %v", err)
	}

	for _, name := range []string{
		"id", "kind", "resource_key", "payload", "revision", "actor_subject",
		"tenant_id", "available_at", "claimed_until", "attempts", "last_error",
		"created_at", "processed_at",
	} {
		if _, ok := columns[name]; !ok {
			t.Fatalf("work_queue is missing column %q", name)
		}
	}
	if columns["payload"] != "jsonb" {
		t.Fatalf("payload data_type = %q, want jsonb", columns["payload"])
	}
	if columns["revision"] != "bigint" {
		t.Fatalf("revision data_type = %q, want bigint", columns["revision"])
	}

	var indexDef string
	if err := pool.QueryRow(ctx, `
		select indexdef from pg_indexes
		where schemaname = current_schema() and indexname = 'work_queue_pending_key_idx'
	`).Scan(&indexDef); err != nil {
		t.Fatalf("query work_queue_pending_key_idx: %v", err)
	}
	if !strings.Contains(indexDef, "UNIQUE") {
		t.Fatalf("work_queue_pending_key_idx must be unique, got %q", indexDef)
	}
	if !strings.Contains(indexDef, "processed_at IS NULL") {
		t.Fatalf("work_queue_pending_key_idx must be partial on processed_at is null, got %q", indexDef)
	}
}

func TestWorkQueuePendingKeyIndexBlocksDuplicatePendingRows(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)

	insert := `insert into work_queue (kind, resource_key, payload, actor_subject, tenant_id) values ($1, $2, '{}'::jsonb, '', '')`
	if _, err := pool.Exec(ctx, insert, "k", "stack:a/user:x"); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if _, err := pool.Exec(ctx, insert, "k", "stack:a/user:x"); err == nil {
		t.Fatal("second pending insert for the same key was accepted")
	}

	if _, err := pool.Exec(ctx, `update work_queue set processed_at = now() where resource_key = $1`, "stack:a/user:x"); err != nil {
		t.Fatalf("complete first row: %v", err)
	}
	if _, err := pool.Exec(ctx, insert, "k", "stack:a/user:x"); err != nil {
		t.Fatalf("insert after completion must succeed, got: %v", err)
	}
}

func TestWorkQueueMigrationBackfillsPendingAuthorizationOutbox(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)

	if _, err := pool.Exec(ctx, `
		insert into authorization_outbox (id, operation, subject, stack, role)
		values ('grant/user:x/stack:a/owner', 'grant', 'user:x', 'stack:a', 'owner'),
		       ('revoke/user:y/stack:a/viewer', 'revoke', 'user:y', 'stack:a', 'viewer'),
		       ('done/user:z/stack:a/viewer', 'grant', 'user:z', 'stack:a', 'viewer')
	`); err != nil {
		t.Fatalf("seed authorization_outbox: %v", err)
	}
	if _, err := pool.Exec(ctx, `update authorization_outbox set processed_at = now() where id = 'done/user:z/stack:a/viewer'`); err != nil {
		t.Fatalf("mark processed: %v", err)
	}

	// Replay 0012 against the seeded outbox: drop what it created and forget
	// that it ran, so Migrate re-applies the file including its backfill.
	if _, err := pool.Exec(ctx, `drop table work_queue`); err != nil {
		t.Fatalf("drop work_queue: %v", err)
	}
	if _, err := pool.Exec(ctx, `delete from schema_migrations where version = '0012_work_queue'`); err != nil {
		t.Fatalf("reset migration version: %v", err)
	}
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("re-run migration: %v", err)
	}

	type backfilled struct {
		key  string
		role string
	}
	rows, err := pool.Query(ctx, `select resource_key, payload->>'role' from work_queue order by resource_key`)
	if err != nil {
		t.Fatalf("query backfilled rows: %v", err)
	}
	defer rows.Close()
	var got []backfilled
	for rows.Next() {
		var row backfilled
		if err := rows.Scan(&row.key, &row.role); err != nil {
			t.Fatalf("scan backfilled row: %v", err)
		}
		got = append(got, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate backfilled rows: %v", err)
	}

	want := []backfilled{
		{key: "stack:a/user:x", role: "owner"},
		{key: "stack:a/user:y", role: ""},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("backfilled rows = %+v, want %+v", got, want)
	}
}
