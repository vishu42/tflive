package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/vishu42/tflive/internal/app"
	"github.com/vishu42/tflive/internal/dispatch"
	"github.com/vishu42/tflive/internal/traits"
)

const (
	requesterSubject = traits.UserID("6fdb4b4c-2a8f-4cf7-945f-38f67f6a0e91")
	approverSubject  = traits.UserID("cb4afba6-d18d-496f-80ce-8a50b94f09be")
)

var (
	_ app.StackRepository                    = (*Store)(nil)
	_ app.StackTemplateRepository            = (*Store)(nil)
	_ app.StackTemplateInstaller             = (*Store)(nil)
	_ app.TemplateRevisionMetadataRepository = (*Store)(nil)
	_ app.TemplateRunRepository              = (*Store)(nil)
	_ app.TemplateRegistrationRepository     = (*Store)(nil)
	_ app.TemplateRevisionRepository         = (*Store)(nil)
	_ dispatch.Outbox                        = (*Store)(nil)
	_ interface {
		RecordTemplateRunStatus(context.Context, traits.TemplateRunStatusActivityInput) error
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
		"workflow_outbox",
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

	var sourceTemplateID traits.SourceTemplateID
	var desiredTemplateRevisionID traits.TemplateRevisionID
	var lastAppliedTemplateRevisionID traits.TemplateRevisionID
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
	if desiredTemplateRevisionID != traits.TemplateRevisionID("template_rev_1") {
		t.Fatalf("desired template revision ID = %q, want template_rev_1", desiredTemplateRevisionID)
	}
	if lastAppliedTemplateRevisionID != traits.TemplateRevisionID("template_rev_1") {
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

	registration := traits.TemplateRegistration{
		ID:          traits.TemplateRegistrationID("template_registration_123"),
		TenantID:    traits.TenantID("tenant_123"),
		RepoOwner:   "acme",
		RepoName:    "infra-templates",
		SourceRef:   "v0.0.1",
		RootPath:    "modules/vpc",
		Status:      traits.TemplateRegistrationPending,
		RequestedBy: traits.UserID("user_123"),
		RequestedAt: requestedAt,
	}

	if err := store.CreateTemplateRegistration(ctx, registration); err != nil {
		t.Fatalf("CreateTemplateRegistration returned error: %v", err)
	}

	got, err := store.GetTemplateRegistration(ctx, traits.TenantID("tenant_123"), traits.TemplateRegistrationID("template_registration_123"))
	if err != nil {
		t.Fatalf("GetTemplateRegistration returned error: %v", err)
	}

	if got != registration {
		t.Fatalf("registration = %#v, want %#v", got, registration)
	}

	_, err = store.GetTemplateRegistration(ctx, traits.TenantID("tenant_456"), traits.TemplateRegistrationID("template_registration_123"))
	if !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("other tenant error = %v, want app.ErrNotFound", err)
	}
}

func TestRecordTemplateRegistrationStatusUpdatesTerminalFields(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)
	registration := traits.TemplateRegistration{
		ID:          traits.TemplateRegistrationID("template_registration_123"),
		TenantID:    traits.TenantID("tenant_123"),
		RepoOwner:   "acme",
		RepoName:    "infra-templates",
		SourceRef:   "v0.0.1",
		RootPath:    "modules/vpc",
		Status:      traits.TemplateRegistrationPending,
		RequestedBy: traits.UserID("user_123"),
		RequestedAt: time.Date(2026, 7, 6, 11, 30, 0, 0, time.UTC),
	}
	if err := store.CreateTemplateRegistration(ctx, registration); err != nil {
		t.Fatalf("CreateTemplateRegistration returned error: %v", err)
	}

	err := store.RecordTemplateRegistrationStatus(ctx, traits.TemplateRegistrationStatusActivityInput{
		RegistrationID:     traits.TemplateRegistrationID("template_registration_123"),
		TenantID:           traits.TenantID("tenant_123"),
		Status:             traits.TemplateRegistrationCompleted,
		TemplateRevisionID: traits.TemplateRevisionID("template_123"),
		ResolvedCommitSHA:  "abc123",
	})
	if err != nil {
		t.Fatalf("RecordTemplateRegistrationStatus returned error: %v", err)
	}

	got, err := store.GetTemplateRegistration(ctx, traits.TenantID("tenant_123"), traits.TemplateRegistrationID("template_registration_123"))
	if err != nil {
		t.Fatalf("GetTemplateRegistration returned error: %v", err)
	}
	if got.Status != traits.TemplateRegistrationCompleted {
		t.Fatalf("status = %q, want completed", got.Status)
	}
	if got.TemplateRevisionID != traits.TemplateRevisionID("template_123") {
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
	template := traits.TemplateRevision{
		ID:                traits.TemplateRevisionID("template_123"),
		TenantID:          traits.TenantID("tenant_123"),
		RepoOwner:         "acme",
		RepoName:          "infra-templates",
		SourceRef:         "v0.0.1",
		ResolvedCommitSHA: "abc123",
		RootPath:          "modules/vpc",
		Name:              "vpc",
		Description:       "Creates a VPC",
		Tags:              []string{"network", "aws"},
		Status:            traits.TemplateRevisionActive,
		CreatedAt:         createdAt,
	}
	variables := []traits.TemplateVariable{
		{
			TemplateRevisionID: traits.TemplateRevisionID("template_123"),
			Name:               "region",
			TypeExpression:     "string",
			Description:        "AWS region",
			Required:           true,
		},
		{
			TemplateRevisionID: traits.TemplateRevisionID("template_123"),
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
	if created.ID != traits.TemplateRevisionID("template_123") {
		t.Fatalf("created template revision ID = %q, want template_123", created.ID)
	}

	reused, err := store.UpsertTemplateRevisionWithVariables(ctx, traits.TemplateRevision{
		ID:                traits.TemplateRevisionID("template_456"),
		TenantID:          traits.TenantID("tenant_123"),
		RepoOwner:         "acme",
		RepoName:          "infra-templates",
		SourceRef:         "v0.0.1",
		ResolvedCommitSHA: "abc123",
		RootPath:          "modules/vpc",
		Name:              "vpc moved tag",
		Status:            traits.TemplateRevisionActive,
	}, nil)
	if err != nil {
		t.Fatalf("second UpsertTemplateRevisionWithVariables returned error: %v", err)
	}
	if reused.ID != traits.TemplateRevisionID("template_123") {
		t.Fatalf("reused template revision ID = %q, want template_123", reused.ID)
	}

	gotVariables, err := store.GetTemplateRevisionVariables(ctx, traits.TenantID("tenant_123"), traits.TemplateRevisionID("template_123"))
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
	_, err := store.UpsertTemplateRevisionWithVariables(ctx, traits.TemplateRevision{
		ID:                traits.TemplateRevisionID("template_123"),
		TenantID:          traits.TenantID("tenant_123"),
		RepoOwner:         "acme",
		RepoName:          "infra-templates",
		SourceRef:         "v0.0.1",
		ResolvedCommitSHA: "abc123",
		RootPath:          "modules/vpc",
		Name:              "vpc",
		Status:            traits.TemplateRevisionActive,
	}, nil)
	if err != nil {
		t.Fatalf("UpsertTemplateRevisionWithVariables returned error: %v", err)
	}

	_, err = store.GetTemplateRevisionVariables(ctx, traits.TenantID("tenant_456"), traits.TemplateRevisionID("template_123"))
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

	stack := traits.Stack{
		ID:                   traits.StackID("stack_123"),
		TenantID:             traits.TenantID("tenant_123"),
		Name:                 "Acme Prod",
		Slug:                 "acme-prod",
		Tags:                 map[string]string{"env": "prod"},
		DefaultCredentialIDs: []traits.CredentialSetID{traits.CredentialSetID("credential_123")},
		CreatedBy:            traits.UserID("user_123"),
		CreatedAt:            createdAt,
	}

	if err := store.CreateStack(ctx, stack); err != nil {
		t.Fatalf("CreateStack returned error: %v", err)
	}

	got, err := store.GetStack(ctx, traits.TenantID("tenant_123"), traits.StackID("stack_123"))
	if err != nil {
		t.Fatalf("GetStack returned error: %v", err)
	}

	if got.ID != stack.ID || got.TenantID != stack.TenantID || got.Name != stack.Name || got.Slug != stack.Slug || got.CreatedBy != stack.CreatedBy {
		t.Fatalf("stack = %#v, want %#v", got, stack)
	}
	if got.Tags["env"] != "prod" {
		t.Fatalf("tags = %#v", got.Tags)
	}
	if len(got.DefaultCredentialIDs) != 1 || got.DefaultCredentialIDs[0] != traits.CredentialSetID("credential_123") {
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

	first := traits.Stack{
		ID:        traits.StackID("stack_123"),
		TenantID:  traits.TenantID("tenant_123"),
		Name:      "Acme Prod",
		Slug:      "acme-prod",
		CreatedBy: traits.UserID("user_123"),
		CreatedAt: time.Now().UTC(),
	}
	if err := store.CreateStack(ctx, first); err != nil {
		t.Fatalf("CreateStack first returned error: %v", err)
	}

	second := first
	second.ID = traits.StackID("stack_456")
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
	older := traits.Stack{
		ID:        traits.StackID("stack_older"),
		TenantID:  traits.TenantID("tenant_123"),
		Name:      "Older Stack",
		Slug:      "older-stack",
		CreatedBy: traits.UserID("user_123"),
		CreatedAt: time.Date(2026, 7, 6, 10, 0, 0, 0, time.UTC),
	}
	newer := traits.Stack{
		ID:        traits.StackID("stack_newer"),
		TenantID:  traits.TenantID("tenant_123"),
		Name:      "Newer Stack",
		Slug:      "newer-stack",
		CreatedBy: traits.UserID("user_123"),
		CreatedAt: time.Date(2026, 7, 6, 11, 0, 0, 0, time.UTC),
	}
	otherTenant := traits.Stack{
		ID:        traits.StackID("stack_other"),
		TenantID:  traits.TenantID("tenant_456"),
		Name:      "Other Stack",
		Slug:      "other-stack",
		CreatedBy: traits.UserID("user_456"),
		CreatedAt: time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC),
	}
	for _, stack := range []traits.Stack{older, newer, otherTenant} {
		if err := store.CreateStack(ctx, stack); err != nil {
			t.Fatalf("CreateStack(%s) returned error: %v", stack.ID, err)
		}
	}

	stacks, err := store.ListStacks(ctx, traits.TenantID("tenant_123"))
	if err != nil {
		t.Fatalf("ListStacks returned error: %v", err)
	}

	if len(stacks) != 2 {
		t.Fatalf("len(stacks) = %d, want 2: %#v", len(stacks), stacks)
	}
	if stacks[0].ID != traits.StackID("stack_newer") || stacks[1].ID != traits.StackID("stack_older") {
		t.Fatalf("stack order = %#v", stacks)
	}
}

func TestListStacksPageReturnsTenantStacksWithStableKeyset(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)
	older := traits.Stack{
		ID:        traits.StackID("stack_older"),
		TenantID:  traits.TenantID("tenant_123"),
		Name:      "Older Stack",
		Slug:      "older-stack",
		CreatedBy: traits.UserID("user_123"),
		CreatedAt: time.Date(2026, 7, 6, 10, 0, 0, 0, time.UTC),
	}
	newer := traits.Stack{
		ID:        traits.StackID("stack_newer"),
		TenantID:  traits.TenantID("tenant_123"),
		Name:      "Newer Stack",
		Slug:      "newer-stack",
		CreatedBy: traits.UserID("user_123"),
		CreatedAt: time.Date(2026, 7, 6, 11, 0, 0, 0, time.UTC),
	}
	otherTenant := traits.Stack{
		ID:        traits.StackID("stack_other"),
		TenantID:  traits.TenantID("tenant_456"),
		Name:      "Other Stack",
		Slug:      "other-stack",
		CreatedBy: traits.UserID("user_456"),
		CreatedAt: time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC),
	}
	for _, stack := range []traits.Stack{older, newer, otherTenant} {
		if err := store.CreateStack(ctx, stack); err != nil {
			t.Fatalf("CreateStack(%s) returned error: %v", stack.ID, err)
		}
	}

	stacks, err := store.ListStacksPage(ctx, traits.TenantID("tenant_123"), nil, 1)
	if err != nil {
		t.Fatalf("ListStacksPage first page returned error: %v", err)
	}
	if len(stacks) != 1 || stacks[0].ID != newer.ID {
		t.Fatalf("first page = %#v, want newer stack", stacks)
	}
	cursor := &app.StackPageCursor{CreatedAt: stacks[0].CreatedAt, ID: stacks[0].ID}
	stacks, err = store.ListStacksPage(ctx, traits.TenantID("tenant_123"), cursor, 1)
	if err != nil {
		t.Fatalf("ListStacksPage second page returned error: %v", err)
	}
	if len(stacks) != 1 || stacks[0].ID != older.ID {
		t.Fatalf("second page = %#v, want older stack", stacks)
	}
	cursor = &app.StackPageCursor{CreatedAt: stacks[0].CreatedAt, ID: stacks[0].ID}
	stacks, err = store.ListStacksPage(ctx, traits.TenantID("tenant_123"), cursor, 1)
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
	if _, err := store.ListStacksPage(ctx, traits.TenantID("tenant_123"), nil, 0); err == nil {
		t.Fatal("ListStacksPage() error = nil, want invalid limit error")
	}
}

func TestGetTemplateRevisionReturnsTenantScopedRevision(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)
	createdAt := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)
	template := traits.TemplateRevision{
		ID:                traits.TemplateRevisionID("template_123"),
		TenantID:          traits.TenantID("tenant_123"),
		RepoOwner:         "acme",
		RepoName:          "infra-templates",
		SourceRef:         "main",
		ResolvedCommitSHA: "abc123",
		RootPath:          ".",
		Name:              "vpc",
		Tags:              []string{"network"},
		Status:            traits.TemplateRevisionActive,
		CreatedAt:         createdAt,
	}
	if _, err := store.UpsertTemplateRevisionWithVariables(ctx, template, nil); err != nil {
		t.Fatalf("UpsertTemplateRevisionWithVariables returned error: %v", err)
	}

	got, err := store.GetTemplateRevision(ctx, traits.TenantID("tenant_123"), traits.TemplateRevisionID("template_123"))
	if err != nil {
		t.Fatalf("GetTemplateRevision returned error: %v", err)
	}
	if got.ID != traits.TemplateRevisionID("template_123") || got.Status != traits.TemplateRevisionActive || got.Tags[0] != "network" {
		t.Fatalf("template = %#v", got)
	}

	_, err = store.GetTemplateRevision(ctx, traits.TenantID("tenant_456"), traits.TemplateRevisionID("template_123"))
	if !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("other tenant error = %v, want app.ErrNotFound", err)
	}
}

func TestListTemplateRevisionsReturnsTenantScopedRevisionsNewestFirst(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)
	older := traits.TemplateRevision{
		ID:                traits.TemplateRevisionID("template_older"),
		TenantID:          traits.TenantID("tenant_123"),
		RepoOwner:         "acme",
		RepoName:          "infra-templates",
		SourceRef:         "v0.0.1",
		ResolvedCommitSHA: "abc123",
		RootPath:          ".",
		Name:              "older",
		Status:            traits.TemplateRevisionActive,
		CreatedAt:         time.Date(2026, 7, 6, 10, 0, 0, 0, time.UTC),
	}
	newer := traits.TemplateRevision{
		ID:                traits.TemplateRevisionID("template_newer"),
		TenantID:          traits.TenantID("tenant_123"),
		RepoOwner:         "acme",
		RepoName:          "infra-templates",
		SourceRef:         "v0.0.2",
		ResolvedCommitSHA: "def456",
		RootPath:          ".",
		Name:              "newer",
		Status:            traits.TemplateRevisionActive,
		CreatedAt:         time.Date(2026, 7, 6, 11, 0, 0, 0, time.UTC),
	}
	otherTenant := traits.TemplateRevision{
		ID:                traits.TemplateRevisionID("template_other"),
		TenantID:          traits.TenantID("tenant_456"),
		RepoOwner:         "acme",
		RepoName:          "infra-templates",
		SourceRef:         "v0.0.3",
		ResolvedCommitSHA: "ghi789",
		RootPath:          ".",
		Name:              "other",
		Status:            traits.TemplateRevisionActive,
		CreatedAt:         time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC),
	}
	for _, template := range []traits.TemplateRevision{older, newer, otherTenant} {
		if _, err := store.UpsertTemplateRevisionWithVariables(ctx, template, nil); err != nil {
			t.Fatalf("UpsertTemplateRevisionWithVariables(%s) returned error: %v", template.ID, err)
		}
	}

	templates, err := store.ListTemplateRevisions(ctx, traits.TenantID("tenant_123"))
	if err != nil {
		t.Fatalf("ListTemplateRevisions returned error: %v", err)
	}

	if len(templates) != 2 {
		t.Fatalf("len(templates) = %d, want 2: %#v", len(templates), templates)
	}
	if templates[0].ID != traits.TemplateRevisionID("template_newer") || templates[1].ID != traits.TemplateRevisionID("template_older") {
		t.Fatalf("template order = %#v", templates)
	}
}

func TestCreateStackTemplateAndGetStackWithTemplates(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)

	stack := traits.Stack{
		ID:        traits.StackID("stack_123"),
		TenantID:  traits.TenantID("tenant_123"),
		Name:      "Acme Prod",
		Slug:      "acme-prod",
		CreatedBy: traits.UserID("user_123"),
		CreatedAt: time.Now().UTC(),
	}
	if err := store.CreateStack(ctx, stack); err != nil {
		t.Fatalf("CreateStack returned error: %v", err)
	}

	stackTemplate := traits.StackTemplate{
		ID:                        traits.StackTemplateID("stack_template_123"),
		TenantID:                  traits.TenantID("tenant_123"),
		StackID:                   traits.StackID("stack_123"),
		DesiredTemplateRevisionID: traits.TemplateRevisionID("template_123"),
		SelectedRef:               "main",
		WorkspaceName:             "meg_acme_prod_late_123",
		ConfigJSON:                json.RawMessage(`{"region":"us-east-1"}`),
		CreatedBy:                 traits.UserID("installer_123"),
		Lifecycle:                 traits.StackTemplateActive,
	}
	if err := store.CreateStackTemplate(ctx, stackTemplate); err != nil {
		t.Fatalf("CreateStackTemplate returned error: %v", err)
	}

	view, err := store.GetStackWithTemplates(ctx, traits.TenantID("tenant_123"), traits.StackID("stack_123"))
	if err != nil {
		t.Fatalf("GetStackWithTemplates returned error: %v", err)
	}
	if view.Stack.ID != traits.StackID("stack_123") {
		t.Fatalf("view stack ID = %q, want stack_123", view.Stack.ID)
	}
	if len(view.Templates) != 1 {
		t.Fatalf("len(view.Templates) = %d, want 1", len(view.Templates))
	}
	if view.Templates[0].TenantID != traits.TenantID("tenant_123") {
		t.Fatalf("template tenant ID = %q, want tenant_123", view.Templates[0].TenantID)
	}
	if view.Templates[0].CreatedBy != traits.UserID("installer_123") {
		t.Fatalf("template created by = %q, want installer_123", view.Templates[0].CreatedBy)
	}
	if string(view.Templates[0].ConfigJSON) != `{"region": "us-east-1"}` {
		t.Fatalf("template config = %s", view.Templates[0].ConfigJSON)
	}
}

func TestCreateStackTemplateAllowsRepeatedSourceWithDistinctComponentKeys(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)
	tenantID := traits.TenantID("tenant_123")
	sourceTemplateID := traits.SourceTemplateID("source_template_vpc")

	stack := traits.Stack{
		ID:        traits.StackID("stack_123"),
		TenantID:  tenantID,
		Name:      "Acme Prod",
		Slug:      "acme-prod",
		CreatedBy: traits.UserID("user_123"),
		CreatedAt: time.Now().UTC(),
	}
	if err := store.CreateStack(ctx, stack); err != nil {
		t.Fatalf("CreateStack returned error: %v", err)
	}

	template := traits.TemplateRevision{
		ID:                traits.TemplateRevisionID("template_rev_1"),
		TenantID:          tenantID,
		SourceTemplateID:  sourceTemplateID,
		RepoOwner:         "acme",
		RepoName:          "infra",
		SourceRef:         "main",
		ResolvedCommitSHA: "sha-1",
		RootPath:          "vpc",
		Name:              "vpc",
		Status:            traits.TemplateRevisionActive,
		CreatedAt:         time.Now().UTC(),
	}
	if _, err := store.UpsertTemplateRevisionWithVariables(ctx, template, nil); err != nil {
		t.Fatalf("UpsertTemplateRevisionWithVariables returned error: %v", err)
	}

	first := traits.StackTemplate{
		ID:                        traits.StackTemplateID("stack_template_primary"),
		TenantID:                  tenantID,
		StackID:                   stack.ID,
		SourceTemplateID:          sourceTemplateID,
		DesiredTemplateRevisionID: template.ID,
		ComponentKey:              "primary-vpc",
		SelectedRef:               "main",
		WorkspaceName:             "meg_acme_primary",
		ConfigJSON:                json.RawMessage(`{"region":"us-east-1"}`),
		DesiredConfigJSON:         json.RawMessage(`{"region":"us-east-1"}`),
		CreatedBy:                 traits.UserID("user_123"),
		Lifecycle:                 traits.StackTemplateActive,
	}
	second := first
	second.ID = traits.StackTemplateID("stack_template_shared")
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
	tenantID := traits.TenantID("tenant_123")
	sourceTemplateID := traits.SourceTemplateID("source_template_vpc")

	stack := traits.Stack{
		ID:        traits.StackID("stack_123"),
		TenantID:  tenantID,
		Name:      "Acme Prod",
		Slug:      "acme-prod",
		CreatedBy: traits.UserID("user_123"),
		CreatedAt: time.Now().UTC(),
	}
	if err := store.CreateStack(ctx, stack); err != nil {
		t.Fatalf("CreateStack returned error: %v", err)
	}

	template := traits.TemplateRevision{
		ID:                traits.TemplateRevisionID("template_rev_1"),
		TenantID:          tenantID,
		SourceTemplateID:  sourceTemplateID,
		RepoOwner:         "acme",
		RepoName:          "infra",
		SourceRef:         "main",
		ResolvedCommitSHA: "sha-1",
		RootPath:          "vpc",
		Name:              "vpc",
		Status:            traits.TemplateRevisionActive,
		CreatedAt:         time.Now().UTC(),
	}
	if _, err := store.UpsertTemplateRevisionWithVariables(ctx, template, nil); err != nil {
		t.Fatalf("UpsertTemplateRevisionWithVariables returned error: %v", err)
	}

	stackTemplate := traits.StackTemplate{
		ID:                        traits.StackTemplateID("stack_template_primary"),
		TenantID:                  tenantID,
		StackID:                   stack.ID,
		SourceTemplateID:          sourceTemplateID,
		DesiredTemplateRevisionID: template.ID,
		ComponentKey:              "primary-vpc",
		SelectedRef:               "main",
		WorkspaceName:             "meg_acme_primary",
		ConfigJSON:                json.RawMessage(`{}`),
		DesiredConfigJSON:         json.RawMessage(`{}`),
		CreatedBy:                 traits.UserID("user_123"),
		Lifecycle:                 traits.StackTemplateActive,
	}

	if err := store.CreateStackTemplate(ctx, stackTemplate); err != nil {
		t.Fatalf("CreateStackTemplate first returned error: %v", err)
	}
	stackTemplate.ID = traits.StackTemplateID("stack_template_duplicate")
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
	tenantID := traits.TenantID("tenant_123")
	stack := traits.Stack{
		ID:        traits.StackID("stack_123"),
		TenantID:  tenantID,
		Name:      "Acme Prod",
		Slug:      "acme-prod",
		CreatedBy: traits.UserID("user_123"),
		CreatedAt: time.Now().UTC(),
	}
	if err := store.CreateStack(ctx, stack); err != nil {
		t.Fatalf("CreateStack returned error: %v", err)
	}
	stackTemplate := traits.StackTemplate{
		ID:                        traits.StackTemplateID("stack_template_123"),
		TenantID:                  tenantID,
		StackID:                   stack.ID,
		SourceTemplateID:          traits.SourceTemplateID("source_template_vpc"),
		DesiredTemplateRevisionID: traits.TemplateRevisionID("template_rev_1"),
		ComponentKey:              "primary-vpc",
		SelectedRef:               "main",
		WorkspaceName:             "meg_acme_primary",
		ConfigJSON:                json.RawMessage(`{"region":"us-east-1"}`),
		DesiredConfigJSON:         json.RawMessage(`{"region":"us-east-1"}`),
		CreatedBy:                 traits.UserID("user_123"),
		Lifecycle:                 traits.StackTemplateActive,
	}
	if err := store.CreateStackTemplate(ctx, stackTemplate); err != nil {
		t.Fatalf("CreateStackTemplate returned error: %v", err)
	}

	updated, err := store.UpdateStackTemplateConfig(ctx, tenantID, stackTemplate.ID, json.RawMessage(`{"region":"us-west-2"}`))
	if err != nil {
		t.Fatalf("UpdateStackTemplateConfig returned error: %v", err)
	}
	if string(updated.DesiredConfigJSON) != `{"region":"us-west-2"}` {
		t.Fatalf("updated desired config = %s", updated.DesiredConfigJSON)
	}
	if string(updated.ConfigJSON) != `{"region":"us-east-1"}` {
		t.Fatalf("legacy config changed = %s", updated.ConfigJSON)
	}
}

func TestUpdateStackTemplateDesiredRevisionPersistsRevisionAndConfig(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)
	tenantID := traits.TenantID("tenant_123")
	stack := traits.Stack{
		ID:        traits.StackID("stack_123"),
		TenantID:  tenantID,
		Name:      "Acme Prod",
		Slug:      "acme-prod",
		CreatedBy: traits.UserID("user_123"),
		CreatedAt: time.Now().UTC(),
	}
	if err := store.CreateStack(ctx, stack); err != nil {
		t.Fatalf("CreateStack returned error: %v", err)
	}
	stackTemplate := traits.StackTemplate{
		ID:                        traits.StackTemplateID("stack_template_123"),
		TenantID:                  tenantID,
		StackID:                   stack.ID,
		SourceTemplateID:          traits.SourceTemplateID("source_template_vpc"),
		DesiredTemplateRevisionID: traits.TemplateRevisionID("template_rev_1"),
		ComponentKey:              "primary-vpc",
		SelectedRef:               "main",
		WorkspaceName:             "meg_acme_primary",
		ConfigJSON:                json.RawMessage(`{"region":"us-east-1"}`),
		DesiredConfigJSON:         json.RawMessage(`{"region":"us-east-1"}`),
		CreatedBy:                 traits.UserID("user_123"),
		Lifecycle:                 traits.StackTemplateActive,
	}
	if err := store.CreateStackTemplate(ctx, stackTemplate); err != nil {
		t.Fatalf("CreateStackTemplate returned error: %v", err)
	}

	updated, err := store.UpdateStackTemplateDesiredRevision(ctx, tenantID, stackTemplate.ID, traits.TemplateRevisionID("template_rev_2"), json.RawMessage(`{"region":"us-west-2"}`))
	if err != nil {
		t.Fatalf("UpdateStackTemplateDesiredRevision returned error: %v", err)
	}
	if updated.DesiredTemplateRevisionID != traits.TemplateRevisionID("template_rev_2") {
		t.Fatalf("updated desired template revision ID = %q, want template_rev_2", updated.DesiredTemplateRevisionID)
	}
	if string(updated.DesiredConfigJSON) != `{"region":"us-west-2"}` {
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
			selected_ref,
			workspace_name,
			config_json,
			desired_config_json,
			last_applied_run_id,
			last_applied_ref,
			last_applied_at,
			lifecycle
		) values ($1, $2, $3, $4, $5, $6, $7, $8, $9::jsonb, $10::jsonb, $11, $12, $13, $14)
	`,
		"stack_template_123",
		"tenant_123",
		"stack_123",
		"primary-vpc",
		"source_template_vpc",
		"template_123",
		"main",
		"mtp_acme_prod_vpc_a13f9c",
		`{"region":"us-east-1"}`,
		`{"region":"us-east-1"}`,
		"run_123",
		"main",
		lastAppliedAt,
		"active",
	)
	if err != nil {
		t.Fatalf("seed stack template: %v", err)
	}

	stackTemplate, err := store.GetStackTemplate(ctx, traits.TenantID("tenant_123"), traits.StackTemplateID("stack_template_123"))
	if err != nil {
		t.Fatalf("GetStackTemplate returned error: %v", err)
	}

	if stackTemplate.ID != traits.StackTemplateID("stack_template_123") {
		t.Fatalf("ID = %q, want stack_template_123", stackTemplate.ID)
	}
	if stackTemplate.StackID != traits.StackID("stack_123") {
		t.Fatalf("StackID = %q, want stack_123", stackTemplate.StackID)
	}
	if stackTemplate.DesiredTemplateRevisionID != traits.TemplateRevisionID("template_123") {
		t.Fatalf("DesiredTemplateRevisionID = %q, want template_123", stackTemplate.DesiredTemplateRevisionID)
	}
	if stackTemplate.SelectedRef != "main" {
		t.Fatalf("SelectedRef = %q, want main", stackTemplate.SelectedRef)
	}
	if stackTemplate.WorkspaceName != "mtp_acme_prod_vpc_a13f9c" {
		t.Fatalf("WorkspaceName = %q, want mtp_acme_prod_vpc_a13f9c", stackTemplate.WorkspaceName)
	}
	if string(stackTemplate.ConfigJSON) != `{"region": "us-east-1"}` {
		t.Fatalf("ConfigJSON = %s, want region config", stackTemplate.ConfigJSON)
	}
	if stackTemplate.LastAppliedRunID != traits.TemplateRunID("run_123") {
		t.Fatalf("LastAppliedRunID = %q, want run_123", stackTemplate.LastAppliedRunID)
	}
	if stackTemplate.LastAppliedRef != "main" {
		t.Fatalf("LastAppliedRef = %q, want main", stackTemplate.LastAppliedRef)
	}
	if !stackTemplate.LastAppliedAt.Equal(lastAppliedAt) {
		t.Fatalf("LastAppliedAt = %v, want %v", stackTemplate.LastAppliedAt, lastAppliedAt)
	}
	if stackTemplate.Lifecycle != traits.StackTemplateActive {
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
			template_revision_id,
			selected_ref,
			workspace_name,
			lifecycle
		) values ($1, $2, $3, $4, $5, $6, $7)
	`,
		"stack_template_123",
		"tenant_123",
		"stack_123",
		"template_123",
		"main",
		"mtp_acme_prod_vpc_a13f9c",
		"active",
	)
	if err != nil {
		t.Fatalf("seed stack template: %v", err)
	}

	_, err = store.GetStackTemplate(ctx, traits.TenantID("tenant_456"), traits.StackTemplateID("stack_template_123"))
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

	run := traits.TemplateRun{
		ID:                 traits.TemplateRunID("run_123"),
		TenantID:           traits.TenantID("tenant_123"),
		StackTemplateID:    traits.StackTemplateID("stack_template_123"),
		TemplateRevisionID: traits.TemplateRevisionID("template_rev_2"),
		SourceTemplateID:   traits.SourceTemplateID("source_template_vpc"),
		Operation:          traits.OperationApply,
		SelectedRef:        "main",
		ResolvedCommitSHA:  "abc123",
		WorkspaceName:      "mtp_acme_prod_vpc_a13f9c",
		ConfigJSON:         json.RawMessage(`{"region":"us-east-1"}`),
		BackendType:        "s3",
		BackendConfigHash:  "backend_hash_123",
		Status:             traits.TemplateRunQueued,
		TriggerActor:       requesterSubject,
		StartedAt:          startedAt,
		CompletedAt:        completedAt,
		ErrorSummary:       "previous error summary",
	}

	if err := store.CreateTemplateRun(ctx, run); err != nil {
		t.Fatalf("CreateTemplateRun returned error: %v", err)
	}

	var got traits.TemplateRun
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
		string(got.ConfigJSON) != string(run.ConfigJSON) ||
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

	var outboxID string
	var eventType string
	var aggregateID traits.TemplateRunID
	err = pool.QueryRow(ctx, `
		select id, event_type, aggregate_id
		from workflow_outbox
		where aggregate_id = $1
	`, run.ID).Scan(&outboxID, &eventType, &aggregateID)
	if err != nil {
		t.Fatalf("read workflow outbox: %v", err)
	}
	if outboxID != "template-run/tenant_123/run_123" {
		t.Fatalf("outbox ID = %q, want template-run/tenant_123/run_123", outboxID)
	}
	if eventType != "start_template_run" {
		t.Fatalf("event type = %q, want start_template_run", eventType)
	}
	if aggregateID != run.ID {
		t.Fatalf("aggregate ID = %q, want %q", aggregateID, run.ID)
	}

	claimAt := time.Now().Add(time.Minute)
	entry, found, err := store.ClaimTemplateRun(ctx, claimAt, claimAt.Add(30*time.Second))
	if err != nil {
		t.Fatalf("ClaimTemplateRun returned error: %v", err)
	}
	if !found {
		t.Fatal("ClaimTemplateRun found = false, want true")
	}
	if entry.ID != outboxID || entry.Input.RunID != run.ID || entry.Input.RepoOwner != "acme" || entry.Input.RootPath != "modules/vpc" {
		t.Fatalf("claimed entry = %#v", entry)
	}

	retryAt := claimAt.Add(time.Minute)
	if err := store.RetryTemplateRun(ctx, entry.ID, retryAt, "temporal unavailable"); err != nil {
		t.Fatalf("RetryTemplateRun returned error: %v", err)
	}
	if _, found, err := store.ClaimTemplateRun(ctx, retryAt.Add(-time.Second), retryAt); err != nil || found {
		t.Fatalf("claim before retry: found = %v, error = %v", found, err)
	}
	entry, found, err = store.ClaimTemplateRun(ctx, retryAt, retryAt.Add(30*time.Second))
	if err != nil || !found {
		t.Fatalf("claim at retry: found = %v, error = %v", found, err)
	}
	if err := store.CompleteTemplateRun(ctx, entry.ID); err != nil {
		t.Fatalf("CompleteTemplateRun returned error: %v", err)
	}
	if _, found, err := store.ClaimTemplateRun(ctx, retryAt.Add(time.Minute), retryAt.Add(2*time.Minute)); err != nil || found {
		t.Fatalf("claim completed entry: found = %v, error = %v", found, err)
	}
}

func TestGetTemplateRunReturnsTenantScopedRecord(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)
	startedAt := time.Date(2026, 7, 5, 9, 30, 0, 123456000, time.UTC)
	completedAt := time.Date(2026, 7, 5, 9, 45, 0, 123456000, time.UTC)
	seedTemplateRun(t, ctx, pool, traits.TemplateRun{
		ID:                traits.TemplateRunID("run_123"),
		TenantID:          traits.TenantID("tenant_123"),
		StackTemplateID:   traits.StackTemplateID("stack_template_123"),
		Operation:         traits.OperationPlan,
		SelectedRef:       "main",
		ResolvedCommitSHA: "abc123",
		WorkspaceName:     "mtp_acme_prod_vpc_a13f9c",
		BackendType:       "s3",
		BackendConfigHash: "backend_hash_123",
		Status:            traits.TemplateRunCompleted,
		TriggerActor:      traits.UserID("user_123"),
		StartedAt:         startedAt,
		CompletedAt:       completedAt,
		ErrorSummary:      "previous error summary",
	})

	run, err := store.GetTemplateRun(ctx, traits.TenantID("tenant_123"), traits.TemplateRunID("run_123"))
	if err != nil {
		t.Fatalf("GetTemplateRun returned error: %v", err)
	}

	want := traits.TemplateRun{
		ID:                traits.TemplateRunID("run_123"),
		TenantID:          traits.TenantID("tenant_123"),
		StackTemplateID:   traits.StackTemplateID("stack_template_123"),
		Operation:         traits.OperationPlan,
		SelectedRef:       "main",
		ResolvedCommitSHA: "abc123",
		WorkspaceName:     "mtp_acme_prod_vpc_a13f9c",
		BackendType:       "s3",
		BackendConfigHash: "backend_hash_123",
		Status:            traits.TemplateRunCompleted,
		TriggerActor:      traits.UserID("user_123"),
		StartedAt:         startedAt,
		CompletedAt:       completedAt,
		ConfigJSON:        json.RawMessage(`{}`),
		ErrorSummary:      "previous error summary",
	}
	if !reflect.DeepEqual(run, want) {
		t.Fatalf("run = %#v, want %#v", run, want)
	}
}

func TestGetTemplateRunReturnsNotFoundForOtherTenant(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)
	seedTemplateRun(t, ctx, pool, traits.TemplateRun{
		ID:              traits.TemplateRunID("run_123"),
		TenantID:        traits.TenantID("tenant_123"),
		StackTemplateID: traits.StackTemplateID("stack_template_123"),
		Operation:       traits.OperationPlan,
		SelectedRef:     "main",
		WorkspaceName:   "mtp_acme_prod_vpc_a13f9c",
		Status:          traits.TemplateRunQueued,
		TriggerActor:    traits.UserID("user_123"),
	})

	_, err := store.GetTemplateRun(ctx, traits.TenantID("tenant_456"), traits.TemplateRunID("run_123"))
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
	seedTemplateRun(t, ctx, pool, traits.TemplateRun{
		ID:              traits.TemplateRunID("run_123"),
		TenantID:        traits.TenantID("tenant_123"),
		StackTemplateID: traits.StackTemplateID("stack_template_123"),
		Operation:       traits.OperationPlan,
		SelectedRef:     "main",
		WorkspaceName:   "mtp_acme_prod_vpc_a13f9c",
		Status:          traits.TemplateRunQueued,
		TriggerActor:    traits.UserID("user_123"),
	})

	log := traits.TemplateRunLog{
		TenantID:    traits.TenantID("tenant_123"),
		RunID:       traits.TemplateRunID("run_123"),
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

	got, err := store.GetTemplateRunLog(ctx, traits.TenantID("tenant_123"), traits.TemplateRunID("run_123"), "plan")
	if err != nil {
		t.Fatalf("GetTemplateRunLog returned error: %v", err)
	}

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
	seedTemplateRun(t, ctx, pool, traits.TemplateRun{
		ID:              traits.TemplateRunID("run_123"),
		TenantID:        traits.TenantID("tenant_123"),
		StackTemplateID: traits.StackTemplateID("stack_template_123"),
		Operation:       traits.OperationPlan,
		SelectedRef:     "main",
		WorkspaceName:   "mtp_acme_prod_vpc_a13f9c",
		Status:          traits.TemplateRunQueued,
		TriggerActor:    traits.UserID("user_123"),
	})
	seedTemplateRun(t, ctx, pool, traits.TemplateRun{
		ID:              traits.TemplateRunID("run_456"),
		TenantID:        traits.TenantID("tenant_456"),
		StackTemplateID: traits.StackTemplateID("stack_template_456"),
		Operation:       traits.OperationPlan,
		SelectedRef:     "main",
		WorkspaceName:   "mtp_other_prod_vpc_f47ac",
		Status:          traits.TemplateRunQueued,
		TriggerActor:    traits.UserID("user_456"),
	})
	for _, log := range []traits.TemplateRunLog{
		{
			TenantID:    traits.TenantID("tenant_123"),
			RunID:       traits.TemplateRunID("run_123"),
			Phase:       "plan",
			ObjectKey:   "tenants/tenant_123/runs/run_123/logs/plan.log",
			ContentType: "text/plain; charset=utf-8",
			SizeBytes:   18,
			UploadedAt:  uploadedAt,
		},
		{
			TenantID:    traits.TenantID("tenant_123"),
			RunID:       traits.TemplateRunID("run_123"),
			Phase:       "init",
			ObjectKey:   "tenants/tenant_123/runs/run_123/logs/init.log",
			ContentType: "text/plain; charset=utf-8",
			SizeBytes:   12,
			UploadedAt:  uploadedAt.Add(-time.Minute),
		},
		{
			TenantID:    traits.TenantID("tenant_456"),
			RunID:       traits.TemplateRunID("run_456"),
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

	logs, err := store.ListTemplateRunLogs(ctx, traits.TenantID("tenant_123"), traits.TemplateRunID("run_123"))
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
	seedTemplateRun(t, ctx, pool, traits.TemplateRun{
		ID:              traits.TemplateRunID("run_123"),
		TenantID:        traits.TenantID("tenant_123"),
		StackTemplateID: traits.StackTemplateID("stack_template_123"),
		Operation:       traits.OperationPlan,
		SelectedRef:     "main",
		WorkspaceName:   "mtp_acme_prod_vpc_a13f9c",
		Status:          traits.TemplateRunQueued,
		TriggerActor:    traits.UserID("user_123"),
	})

	err := store.RecordTemplateRunLog(ctx, traits.TemplateRunLog{
		TenantID:    traits.TenantID("tenant_456"),
		RunID:       traits.TemplateRunID("run_123"),
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

func TestApproveTemplateRunApprovesWaitingRun(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)
	approvedAt := time.Date(2026, 7, 2, 10, 15, 0, 123456000, time.UTC)
	seedTemplateRun(t, ctx, pool, traits.TemplateRun{
		ID:              traits.TemplateRunID("run_123"),
		TenantID:        traits.TenantID("tenant_123"),
		StackTemplateID: traits.StackTemplateID("stack_template_123"),
		Operation:       traits.OperationApply,
		SelectedRef:     "main",
		WorkspaceName:   "mtp_acme_prod_vpc_a13f9c",
		Status:          traits.TemplateRunWaitingApproval,
		TriggerActor:    requesterSubject,
	})

	err := store.ApproveTemplateRun(ctx, traits.TemplateRunApproval{
		RunID:      traits.TemplateRunID("run_123"),
		TenantID:   traits.TenantID("tenant_123"),
		ApprovedBy: approverSubject,
		ApprovedAt: approvedAt,
	})
	if err != nil {
		t.Fatalf("ApproveTemplateRun returned error: %v", err)
	}

	var status traits.TemplateRunStatus
	if err := pool.QueryRow(ctx, "select status from template_runs where id = $1", "run_123").Scan(&status); err != nil {
		t.Fatalf("read approved run status: %v", err)
	}
	if status != traits.TemplateRunApproved {
		t.Fatalf("status = %q, want %q", status, traits.TemplateRunApproved)
	}

	var approvedBy traits.UserID
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
	seedTemplateRun(t, ctx, pool, traits.TemplateRun{
		ID:              traits.TemplateRunID("run_123"),
		TenantID:        traits.TenantID("tenant_123"),
		StackTemplateID: traits.StackTemplateID("stack_template_123"),
		Operation:       traits.OperationApply,
		SelectedRef:     "main",
		WorkspaceName:   "mtp_acme_prod_vpc_a13f9c",
		Status:          traits.TemplateRunQueued,
		TriggerActor:    traits.UserID("user_123"),
	})

	err := store.ApproveTemplateRun(ctx, traits.TemplateRunApproval{
		RunID:      traits.TemplateRunID("run_123"),
		TenantID:   traits.TenantID("tenant_123"),
		ApprovedBy: traits.UserID("user_456"),
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
	seedTemplateRun(t, ctx, pool, traits.TemplateRun{
		ID:              traits.TemplateRunID("run_123"),
		TenantID:        traits.TenantID("tenant_123"),
		StackTemplateID: traits.StackTemplateID("stack_template_123"),
		Operation:       traits.OperationApply,
		SelectedRef:     "main",
		WorkspaceName:   "mtp_acme_prod_vpc_a13f9c",
		Status:          traits.TemplateRunApplyStarted,
		TriggerActor:    requesterSubject,
	})

	err := store.RequestTemplateRunCancellation(ctx, traits.TemplateRunCancellation{
		RunID:       traits.TemplateRunID("run_123"),
		TenantID:    traits.TenantID("tenant_123"),
		RequestedBy: requesterSubject,
		Reason:      "superseded by newer run",
		RequestedAt: requestedAt,
	})
	if err != nil {
		t.Fatalf("RequestTemplateRunCancellation returned error: %v", err)
	}

	var status traits.TemplateRunStatus
	var requestedBy traits.UserID
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
	if status != traits.TemplateRunCancelRequested {
		t.Fatalf("status = %q, want %q", status, traits.TemplateRunCancelRequested)
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
	seedTemplateRun(t, ctx, pool, traits.TemplateRun{
		ID:              traits.TemplateRunID("run_123"),
		TenantID:        traits.TenantID("tenant_123"),
		StackTemplateID: traits.StackTemplateID("stack_template_123"),
		Operation:       traits.OperationApply,
		SelectedRef:     "main",
		WorkspaceName:   "mtp_acme_prod_vpc_a13f9c",
		Status:          traits.TemplateRunCompleted,
		TriggerActor:    traits.UserID("user_123"),
	})

	err := store.RequestTemplateRunCancellation(ctx, traits.TemplateRunCancellation{
		RunID:       traits.TemplateRunID("run_123"),
		TenantID:    traits.TenantID("tenant_123"),
		RequestedBy: traits.UserID("user_456"),
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
	seedTemplateRun(t, ctx, pool, traits.TemplateRun{
		ID:              traits.TemplateRunID("run_123"),
		TenantID:        traits.TenantID("tenant_123"),
		StackTemplateID: traits.StackTemplateID("stack_template_123"),
		Operation:       traits.OperationPlan,
		SelectedRef:     "main",
		WorkspaceName:   "mtp_acme_prod_vpc_a13f9c",
		Status:          traits.TemplateRunQueued,
		TriggerActor:    traits.UserID("user_123"),
	})
	seedTemplateRun(t, ctx, pool, traits.TemplateRun{
		ID:              traits.TemplateRunID("run_456"),
		TenantID:        traits.TenantID("tenant_456"),
		StackTemplateID: traits.StackTemplateID("stack_template_456"),
		Operation:       traits.OperationPlan,
		SelectedRef:     "main",
		WorkspaceName:   "mtp_other_prod_vpc_f47ac",
		Status:          traits.TemplateRunQueued,
		TriggerActor:    traits.UserID("user_456"),
	})

	err := store.RecordTemplateRunStatus(ctx, traits.TemplateRunStatusActivityInput{
		RunID:           traits.TemplateRunID("run_123"),
		TenantID:        traits.TenantID("tenant_123"),
		StackTemplateID: traits.StackTemplateID("stack_template_123"),
		Operation:       traits.OperationPlan,
		Status:          traits.TemplateRunPlanned,
	})
	if err != nil {
		t.Fatalf("RecordTemplateRunStatus returned error: %v", err)
	}

	var status traits.TemplateRunStatus
	if err := pool.QueryRow(ctx, "select status from template_runs where id = $1", "run_123").Scan(&status); err != nil {
		t.Fatalf("read updated run status: %v", err)
	}
	if status != traits.TemplateRunPlanned {
		t.Fatalf("status = %q, want %q", status, traits.TemplateRunPlanned)
	}

	if err := pool.QueryRow(ctx, "select status from template_runs where id = $1", "run_456").Scan(&status); err != nil {
		t.Fatalf("read other tenant run status: %v", err)
	}
	if status != traits.TemplateRunQueued {
		t.Fatalf("other tenant status = %q, want %q", status, traits.TemplateRunQueued)
	}
}

func TestRecordTemplateRunStatusSetsCompletedAtForTerminalStatus(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)
	seedTemplateRun(t, ctx, pool, traits.TemplateRun{
		ID:              traits.TemplateRunID("run_123"),
		TenantID:        traits.TenantID("tenant_123"),
		StackTemplateID: traits.StackTemplateID("stack_template_123"),
		Operation:       traits.OperationPlan,
		SelectedRef:     "main",
		WorkspaceName:   "mtp_acme_prod_vpc_a13f9c",
		Status:          traits.TemplateRunLockReleased,
		TriggerActor:    traits.UserID("user_123"),
	})

	err := store.RecordTemplateRunStatus(ctx, traits.TemplateRunStatusActivityInput{
		RunID:           traits.TemplateRunID("run_123"),
		TenantID:        traits.TenantID("tenant_123"),
		StackTemplateID: traits.StackTemplateID("stack_template_123"),
		Operation:       traits.OperationPlan,
		Status:          traits.TemplateRunCompleted,
	})
	if err != nil {
		t.Fatalf("RecordTemplateRunStatus returned error: %v", err)
	}

	var status traits.TemplateRunStatus
	var completedAt time.Time
	if err := pool.QueryRow(ctx, `
		select status, completed_at
		from template_runs
		where id = $1
	`, "run_123").Scan(&status, &completedAt); err != nil {
		t.Fatalf("read terminal run status: %v", err)
	}
	if status != traits.TemplateRunCompleted {
		t.Fatalf("status = %q, want %q", status, traits.TemplateRunCompleted)
	}
	if completedAt.IsZero() {
		t.Fatal("completed_at was not set")
	}
}

func TestRecordTemplateRunStatusUpdatesStackTemplateLastAppliedForSuccessfulApply(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)
	stack := traits.Stack{
		ID:        traits.StackID("stack_123"),
		TenantID:  traits.TenantID("tenant_123"),
		Name:      "Acme Prod",
		Slug:      "acme-prod",
		CreatedBy: traits.UserID("user_123"),
		CreatedAt: time.Now().UTC(),
	}
	if err := store.CreateStack(ctx, stack); err != nil {
		t.Fatalf("CreateStack returned error: %v", err)
	}
	stackTemplate := traits.StackTemplate{
		ID:                        traits.StackTemplateID("stack_template_123"),
		TenantID:                  traits.TenantID("tenant_123"),
		StackID:                   traits.StackID("stack_123"),
		SourceTemplateID:          traits.SourceTemplateID("source_template_vpc"),
		DesiredTemplateRevisionID: traits.TemplateRevisionID("template_rev_2"),
		SelectedRef:               "main",
		WorkspaceName:             "mtp_acme_prod_vpc_a13f9c",
		ConfigJSON:                json.RawMessage(`{"region":"us-east-1"}`),
		DesiredConfigJSON:         json.RawMessage(`{"region":"us-east-1"}`),
		CreatedBy:                 traits.UserID("installer_123"),
		Lifecycle:                 traits.StackTemplateActive,
	}
	if err := store.CreateStackTemplate(ctx, stackTemplate); err != nil {
		t.Fatalf("CreateStackTemplate returned error: %v", err)
	}
	seedTemplateRun(t, ctx, pool, traits.TemplateRun{
		ID:                 traits.TemplateRunID("run_123"),
		TenantID:           traits.TenantID("tenant_123"),
		StackTemplateID:    traits.StackTemplateID("stack_template_123"),
		TemplateRevisionID: traits.TemplateRevisionID("template_rev_2"),
		SourceTemplateID:   traits.SourceTemplateID("source_template_vpc"),
		Operation:          traits.OperationApply,
		SelectedRef:        "release-2026-07-08",
		WorkspaceName:      "mtp_acme_prod_vpc_a13f9c",
		Status:             traits.TemplateRunApplyStarted,
		TriggerActor:       traits.UserID("user_123"),
	})

	err := store.RecordTemplateRunStatus(ctx, traits.TemplateRunStatusActivityInput{
		RunID:           traits.TemplateRunID("run_123"),
		TenantID:        traits.TenantID("tenant_123"),
		StackTemplateID: traits.StackTemplateID("stack_template_123"),
		Operation:       traits.OperationApply,
		Status:          traits.TemplateRunApplied,
	})
	if err != nil {
		t.Fatalf("RecordTemplateRunStatus returned error: %v", err)
	}

	var lastAppliedRunID traits.TemplateRunID
	var lastAppliedTemplateRevisionID traits.TemplateRevisionID
	var lastAppliedRef string
	var lastAppliedAt time.Time
	if err := pool.QueryRow(ctx, `
		select last_applied_run_id, last_applied_template_revision_id, last_applied_ref, last_applied_at
		from stack_templates
		where tenant_id = $1
			and id = $2
	`, "tenant_123", "stack_template_123").Scan(
		&lastAppliedRunID,
		&lastAppliedTemplateRevisionID,
		&lastAppliedRef,
		&lastAppliedAt,
	); err != nil {
		t.Fatalf("read stack template last applied fields: %v", err)
	}
	if lastAppliedRunID != traits.TemplateRunID("run_123") {
		t.Fatalf("LastAppliedRunID = %q, want run_123", lastAppliedRunID)
	}
	if lastAppliedTemplateRevisionID != traits.TemplateRevisionID("template_rev_2") {
		t.Fatalf("LastAppliedTemplateRevisionID = %q, want template_rev_2", lastAppliedTemplateRevisionID)
	}
	if lastAppliedRef != "release-2026-07-08" {
		t.Fatalf("LastAppliedRef = %q, want release-2026-07-08", lastAppliedRef)
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
	seedTemplateRun(t, ctx, pool, traits.TemplateRun{
		ID:              traits.TemplateRunID("run_123"),
		TenantID:        traits.TenantID("tenant_123"),
		StackTemplateID: traits.StackTemplateID("stack_template_123"),
		Operation:       traits.OperationPlan,
		SelectedRef:     "main",
		WorkspaceName:   "mtp_acme_prod_vpc_a13f9c",
		Status:          traits.TemplateRunQueued,
		TriggerActor:    traits.UserID("user_123"),
	})

	err := store.RecordTemplateRunStatus(ctx, traits.TemplateRunStatusActivityInput{
		RunID:           traits.TemplateRunID("run_123"),
		TenantID:        traits.TenantID("tenant_456"),
		StackTemplateID: traits.StackTemplateID("stack_template_123"),
		Operation:       traits.OperationPlan,
		Status:          traits.TemplateRunPlanned,
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
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

func seedTemplateRun(t *testing.T, ctx context.Context, pool *pgxpool.Pool, run traits.TemplateRun) {
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
