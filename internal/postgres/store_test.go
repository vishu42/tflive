package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/vishu42/megagega/internal/app"
	"github.com/vishu42/megagega/internal/traits"
)

var (
	_ app.StackRepository                = (*Store)(nil)
	_ app.StackTemplateRepository        = (*Store)(nil)
	_ app.StackTemplateInstaller         = (*Store)(nil)
	_ app.TemplateMetadataRepository     = (*Store)(nil)
	_ app.TemplateRunRepository          = (*Store)(nil)
	_ app.TemplateRegistrationRepository = (*Store)(nil)
	_ app.TemplateRepository             = (*Store)(nil)
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
		"templates",
		"template_registrations",
		"template_variables",
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
		RegistrationID:    traits.TemplateRegistrationID("template_registration_123"),
		TenantID:          traits.TenantID("tenant_123"),
		Status:            traits.TemplateRegistrationCompleted,
		TemplateID:        traits.TemplateID("template_123"),
		ResolvedCommitSHA: "abc123",
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
	if got.TemplateID != traits.TemplateID("template_123") {
		t.Fatalf("template id = %q, want template_123", got.TemplateID)
	}
	if got.ResolvedCommitSHA != "abc123" {
		t.Fatalf("resolved sha = %q, want abc123", got.ResolvedCommitSHA)
	}
	if got.CompletedAt.IsZero() {
		t.Fatal("completed_at was not set for terminal registration status")
	}
}

func TestUpsertTemplateWithVariablesCreatesAndReusesImmutableTemplate(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)
	createdAt := time.Date(2026, 7, 6, 12, 0, 0, 123456000, time.UTC)
	template := traits.Template{
		ID:                traits.TemplateID("template_123"),
		TenantID:          traits.TenantID("tenant_123"),
		RepoOwner:         "acme",
		RepoName:          "infra-templates",
		SourceRef:         "v0.0.1",
		ResolvedCommitSHA: "abc123",
		RootPath:          "modules/vpc",
		Name:              "vpc",
		Description:       "Creates a VPC",
		Tags:              []string{"network", "aws"},
		Status:            traits.TemplateActive,
		CreatedAt:         createdAt,
	}
	variables := []traits.TemplateVariable{
		{
			TemplateID:     traits.TemplateID("template_123"),
			Name:           "region",
			TypeExpression: "string",
			Description:    "AWS region",
			Required:       true,
		},
		{
			TemplateID:     traits.TemplateID("template_123"),
			Name:           "cidr",
			TypeExpression: "string",
			HasDefault:     true,
			HasValidation:  true,
		},
	}

	created, err := store.UpsertTemplateWithVariables(ctx, template, variables)
	if err != nil {
		t.Fatalf("UpsertTemplateWithVariables returned error: %v", err)
	}
	if created.ID != traits.TemplateID("template_123") {
		t.Fatalf("created template ID = %q, want template_123", created.ID)
	}

	reused, err := store.UpsertTemplateWithVariables(ctx, traits.Template{
		ID:                traits.TemplateID("template_456"),
		TenantID:          traits.TenantID("tenant_123"),
		RepoOwner:         "acme",
		RepoName:          "infra-templates",
		SourceRef:         "v0.0.1",
		ResolvedCommitSHA: "abc123",
		RootPath:          "modules/vpc",
		Name:              "vpc moved tag",
		Status:            traits.TemplateActive,
	}, nil)
	if err != nil {
		t.Fatalf("second UpsertTemplateWithVariables returned error: %v", err)
	}
	if reused.ID != traits.TemplateID("template_123") {
		t.Fatalf("reused template ID = %q, want template_123", reused.ID)
	}

	gotVariables, err := store.GetTemplateVariables(ctx, traits.TenantID("tenant_123"), traits.TemplateID("template_123"))
	if err != nil {
		t.Fatalf("GetTemplateVariables returned error: %v", err)
	}
	if len(gotVariables) != 2 {
		t.Fatalf("len(gotVariables) = %d, want 2", len(gotVariables))
	}
	if gotVariables[0].Name != "cidr" || gotVariables[1].Name != "region" {
		t.Fatalf("variables ordered by name = %#v", gotVariables)
	}
}

func TestGetTemplateVariablesReturnsNotFoundForOtherTenant(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)
	_, err := store.UpsertTemplateWithVariables(ctx, traits.Template{
		ID:                traits.TemplateID("template_123"),
		TenantID:          traits.TenantID("tenant_123"),
		RepoOwner:         "acme",
		RepoName:          "infra-templates",
		SourceRef:         "v0.0.1",
		ResolvedCommitSHA: "abc123",
		RootPath:          "modules/vpc",
		Name:              "vpc",
		Status:            traits.TemplateActive,
	}, nil)
	if err != nil {
		t.Fatalf("UpsertTemplateWithVariables returned error: %v", err)
	}

	_, err = store.GetTemplateVariables(ctx, traits.TenantID("tenant_456"), traits.TemplateID("template_123"))
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

func TestGetTemplateReturnsTenantScopedTemplate(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)
	createdAt := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)
	template := traits.Template{
		ID:                traits.TemplateID("template_123"),
		TenantID:          traits.TenantID("tenant_123"),
		RepoOwner:         "acme",
		RepoName:          "infra-templates",
		SourceRef:         "main",
		ResolvedCommitSHA: "abc123",
		RootPath:          ".",
		Name:              "vpc",
		Tags:              []string{"network"},
		Status:            traits.TemplateActive,
		CreatedAt:         createdAt,
	}
	if _, err := store.UpsertTemplateWithVariables(ctx, template, nil); err != nil {
		t.Fatalf("UpsertTemplateWithVariables returned error: %v", err)
	}

	got, err := store.GetTemplate(ctx, traits.TenantID("tenant_123"), traits.TemplateID("template_123"))
	if err != nil {
		t.Fatalf("GetTemplate returned error: %v", err)
	}
	if got.ID != traits.TemplateID("template_123") || got.Status != traits.TemplateActive || got.Tags[0] != "network" {
		t.Fatalf("template = %#v", got)
	}

	_, err = store.GetTemplate(ctx, traits.TenantID("tenant_456"), traits.TemplateID("template_123"))
	if !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("other tenant error = %v, want app.ErrNotFound", err)
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
		ID:            traits.StackTemplateID("stack_template_123"),
		TenantID:      traits.TenantID("tenant_123"),
		StackID:       traits.StackID("stack_123"),
		TemplateID:    traits.TemplateID("template_123"),
		SelectedRef:   "main",
		WorkspaceName: "meg_acme_prod_late_123",
		ConfigJSON:    json.RawMessage(`{"region":"us-east-1"}`),
		Lifecycle:     traits.StackTemplateActive,
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
	if string(view.Templates[0].ConfigJSON) != `{"region": "us-east-1"}` {
		t.Fatalf("template config = %s", view.Templates[0].ConfigJSON)
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
			template_id,
			selected_ref,
			workspace_name,
			config_json,
			last_applied_run_id,
			last_applied_ref,
			last_applied_at,
			lifecycle
		) values ($1, $2, $3, $4, $5, $6, $7::jsonb, $8, $9, $10, $11)
	`,
		"stack_template_123",
		"tenant_123",
		"stack_123",
		"template_123",
		"main",
		"mtp_acme_prod_vpc_a13f9c",
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
	if stackTemplate.TemplateID != traits.TemplateID("template_123") {
		t.Fatalf("TemplateID = %q, want template_123", stackTemplate.TemplateID)
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
			template_id,
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
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}
}

func TestCreateTemplateRunPersistsRunFields(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)
	startedAt := time.Date(2026, 7, 2, 9, 30, 0, 123456000, time.UTC)
	completedAt := time.Date(2026, 7, 2, 9, 45, 0, 123456000, time.UTC)

	run := traits.TemplateRun{
		ID:                traits.TemplateRunID("run_123"),
		TenantID:          traits.TenantID("tenant_123"),
		StackTemplateID:   traits.StackTemplateID("stack_template_123"),
		Operation:         traits.OperationApply,
		SelectedRef:       "main",
		ResolvedCommitSHA: "abc123",
		WorkspaceName:     "mtp_acme_prod_vpc_a13f9c",
		BackendType:       "s3",
		BackendConfigHash: "backend_hash_123",
		Status:            traits.TemplateRunQueued,
		TriggerActor:      traits.UserID("user_123"),
		StartedAt:         startedAt,
		CompletedAt:       completedAt,
		ErrorSummary:      "previous error summary",
	}

	if err := store.CreateTemplateRun(ctx, run); err != nil {
		t.Fatalf("CreateTemplateRun returned error: %v", err)
	}

	var got traits.TemplateRun
	err := pool.QueryRow(ctx, `
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
		where id = $1
	`, run.ID).Scan(
		&got.ID,
		&got.TenantID,
		&got.StackTemplateID,
		&got.Operation,
		&got.SelectedRef,
		&got.ResolvedCommitSHA,
		&got.WorkspaceName,
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
		got.Operation != run.Operation ||
		got.SelectedRef != run.SelectedRef ||
		got.ResolvedCommitSHA != run.ResolvedCommitSHA ||
		got.WorkspaceName != run.WorkspaceName ||
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
		ErrorSummary:      "previous error summary",
	}
	if run != want {
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
		TriggerActor:    traits.UserID("user_123"),
	})

	err := store.ApproveTemplateRun(ctx, traits.TemplateRunApproval{
		RunID:      traits.TemplateRunID("run_123"),
		TenantID:   traits.TenantID("tenant_123"),
		ApprovedBy: traits.UserID("user_456"),
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
	if approvedBy != traits.UserID("user_456") {
		t.Fatalf("approved_by = %q, want user_456", approvedBy)
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
		TriggerActor:    traits.UserID("user_123"),
	})

	err := store.RequestTemplateRunCancellation(ctx, traits.TemplateRunCancellation{
		RunID:       traits.TemplateRunID("run_123"),
		TenantID:    traits.TenantID("tenant_123"),
		RequestedBy: traits.UserID("user_456"),
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
	if requestedBy != traits.UserID("user_456") {
		t.Fatalf("requested_by = %q, want user_456", requestedBy)
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
		t.Fatalf("seed template run: %v", err)
	}
}

func openTestPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()

	dsn := os.Getenv("MEGAGEGA_POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("MEGAGEGA_POSTGRES_TEST_DSN is not set")
	}

	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("open admin postgres pool: %v", err)
	}
	t.Cleanup(admin.Close)

	schema := fmt.Sprintf("megagega_test_%d_%d", time.Now().UnixNano(), atomic.AddUint64(&testSchemaCounter, 1))
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
