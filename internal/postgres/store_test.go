package postgres

import (
	"context"
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
	_ app.StackTemplateRepository = (*Store)(nil)
	_ app.TemplateRunRepository   = (*Store)(nil)
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

	for _, table := range []string{"schema_migrations", "stack_templates", "template_runs", "template_run_approvals"} {
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
