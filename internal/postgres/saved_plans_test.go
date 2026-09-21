package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/vishu42/tflive/internal/domain"
	"github.com/vishu42/tflive/internal/encryption"
)

func savedPlanStore(t *testing.T, pool *pgxpool.Pool) *Store {
	t.Helper()
	cipher, err := encryption.NewCipher("717cb4d0fd1db07a30442806c2987599580f6d7c6e63b9bddf509bc183a086d3")
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	return NewStore(pool, WithCredentialCipher(cipher))
}

// seedPlanRun seeds a plan run of the fixture stack template in status,
// against the template's desired snapshot.
func seedPlanRun(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id domain.TemplateRunID, operation domain.OperationType, status domain.TemplateRunStatus) {
	t.Helper()
	seedTemplateRun(t, ctx, pool, domain.TemplateRun{
		ID:                 id,
		TenantID:           "tenant_123",
		StackTemplateID:    "stack_template_123",
		TemplateRevisionID: "template_rev_2",
		SourceTemplateID:   "source_template_vpc",
		Operation:          operation,
		SelectedRef:        "main",
		WorkspaceName:      "mtp_acme_prod_vpc_a13f9c",
		ConfigJSON:         json.RawMessage(`{"region":"us-east-1"}`),
		Status:             status,
		TriggerActor:       "user_123",
	})
}

func runStatus(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id domain.TemplateRunID) domain.TemplateRunStatus {
	t.Helper()
	var status domain.TemplateRunStatus
	if err := pool.QueryRow(ctx, `select status from template_runs where id = $1`, id).Scan(&status); err != nil {
		t.Fatalf("read run status: %v", err)
	}
	return status
}

func planKeyIsNull(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id domain.TemplateRunID) bool {
	t.Helper()
	var key *string
	if err := pool.QueryRow(ctx, `select plan_artifact_dek from template_runs where id = $1`, id).Scan(&key); err != nil {
		t.Fatalf("read plan key: %v", err)
	}
	return key == nil
}

// A plan key is made once and read back unchanged, and it is stored encrypted,
// never as the key itself.
func TestPlanKeyIsCreatedOnceAndStoredEncrypted(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := savedPlanStore(t, pool)
	seedStackWithTemplate(t, ctx, store)
	seedPlanRun(t, ctx, pool, "run_123", domain.OperationPlan, domain.TemplateRunPlanStarted)

	if _, err := store.PlanKey(ctx, "tenant_123", "run_123"); err == nil {
		t.Fatal("PlanKey returned a key for a run that has none")
	}
	created, err := store.CreatePlanKey(ctx, "tenant_123", "run_123")
	if err != nil {
		t.Fatalf("CreatePlanKey returned error: %v", err)
	}
	again, err := store.CreatePlanKey(ctx, "tenant_123", "run_123")
	if err != nil {
		t.Fatalf("second CreatePlanKey returned error: %v", err)
	}
	read, err := store.PlanKey(ctx, "tenant_123", "run_123")
	if err != nil {
		t.Fatalf("PlanKey returned error: %v", err)
	}
	if len(created) != 32 || !bytes.Equal(created, again) || !bytes.Equal(created, read) {
		t.Fatalf("keys differ: created %x, again %x, read %x", created, again, read)
	}
	var stored string
	if err := pool.QueryRow(ctx, `select plan_artifact_dek from template_runs where id = 'run_123'`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains([]byte(stored), created) {
		t.Fatal("plan key is stored in the clear")
	}
}

// Finishing a plan with changes stores what it would do and makes it the
// template's pending plan, waiting for approval.
func TestFinishTemplatePlanWithChangesWaitsForApproval(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := savedPlanStore(t, pool)
	seedStackWithTemplate(t, ctx, store)
	seedPlanRun(t, ctx, pool, "run_123", domain.OperationPlan, domain.TemplateRunPlanFinished)

	outcome, err := store.FinishTemplatePlan(ctx, domain.FinishPlanActivityInput{
		TenantID: "tenant_123", RunID: "run_123", StackTemplateID: "stack_template_123", Operation: domain.OperationPlan,
		HasChanges: true, Summary: domain.PlanSummary{Add: 3, Change: 1},
	})
	if err != nil {
		t.Fatalf("FinishTemplatePlan returned error: %v", err)
	}
	if outcome != domain.PlanOutcomeWaiting {
		t.Fatalf("outcome = %q, want waiting", outcome)
	}
	run, err := store.GetTemplateRun(ctx, "tenant_123", "run_123")
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != domain.TemplateRunWaitingApproval {
		t.Fatalf("status = %q, want waiting_approval", run.Status)
	}
	if run.PlanSummary == nil || *run.PlanSummary != (domain.PlanSummary{Add: 3, Change: 1}) {
		t.Fatalf("plan summary = %#v", run.PlanSummary)
	}
	stackTemplate, err := store.GetStackTemplate(ctx, "tenant_123", "stack_template_123")
	if err != nil {
		t.Fatal(err)
	}
	if stackTemplate.PendingPlanRunID != "run_123" || stackTemplate.PlanState() != domain.PlanMatches {
		t.Fatalf("pending plan = %q, plan state = %q; want run_123 matching desired", stackTemplate.PendingPlanRunID, stackTemplate.PlanState())
	}
}

// An auto-approved run is approved as its plan finishes, by the person who
// started it, with the audit record a manual approval would leave.
func TestFinishTemplatePlanApprovesAnAutoApprovedRun(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := savedPlanStore(t, pool)
	seedStackWithTemplate(t, ctx, store)
	seedPlanRun(t, ctx, pool, "run_123", domain.OperationPlan, domain.TemplateRunPlanFinished)

	outcome, err := store.FinishTemplatePlan(ctx, domain.FinishPlanActivityInput{
		TenantID: "tenant_123", RunID: "run_123", StackTemplateID: "stack_template_123", Operation: domain.OperationPlan,
		HasChanges: true, Summary: domain.PlanSummary{Add: 1}, AutoApprove: true,
	})
	if err != nil {
		t.Fatalf("FinishTemplatePlan returned error: %v", err)
	}
	if outcome != domain.PlanOutcomeApproved || runStatus(t, ctx, pool, "run_123") != domain.TemplateRunApproved {
		t.Fatalf("outcome = %q, status = %q; want approved", outcome, runStatus(t, ctx, pool, "run_123"))
	}
	var audits int
	if err := pool.QueryRow(ctx, `
		select count(*) from security_audit_log where actor_subject = 'user_123' and action = $1
	`, domain.AuditActionApprovalGranted).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if audits != 1 {
		t.Fatalf("approval audit events = %d, want 1", audits)
	}
}

// A plan with no changes means the infrastructure already is the run's
// snapshot, so that snapshot becomes what is live, and nothing waits.
func TestFinishTemplatePlanWithoutChangesRecordsTheSnapshotAsLive(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := savedPlanStore(t, pool)
	seedStackWithTemplate(t, ctx, store)
	seedPlanRun(t, ctx, pool, "run_123", domain.OperationPlan, domain.TemplateRunPlanFinished)

	outcome, err := store.FinishTemplatePlan(ctx, domain.FinishPlanActivityInput{
		TenantID: "tenant_123", RunID: "run_123", StackTemplateID: "stack_template_123", Operation: domain.OperationPlan,
	})
	if err != nil {
		t.Fatalf("FinishTemplatePlan returned error: %v", err)
	}
	if outcome != domain.PlanOutcomeNoChanges {
		t.Fatalf("outcome = %q, want no_changes", outcome)
	}
	stackTemplate, err := store.GetStackTemplate(ctx, "tenant_123", "stack_template_123")
	if err != nil {
		t.Fatal(err)
	}
	if stackTemplate.LastAppliedRunID != "run_123" || stackTemplate.LiveState() != domain.LiveMatches {
		t.Fatalf("last applied = %q, live state = %q; want run_123 matching", stackTemplate.LastAppliedRunID, stackTemplate.LiveState())
	}
	if stackTemplate.PendingPlanRunID != "" {
		t.Fatalf("pending plan = %q, want none", stackTemplate.PendingPlanRunID)
	}
}

// A cancel that arrives while the plan runs is carried out when it finishes:
// no workflow step is left to notice it.
func TestFinishTemplatePlanCarriesOutACancelRequestedWhilePlanning(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := savedPlanStore(t, pool)
	seedStackWithTemplate(t, ctx, store)
	seedPlanRun(t, ctx, pool, "run_123", domain.OperationPlan, domain.TemplateRunCancelRequested)

	outcome, err := store.FinishTemplatePlan(ctx, domain.FinishPlanActivityInput{
		TenantID: "tenant_123", RunID: "run_123", StackTemplateID: "stack_template_123", Operation: domain.OperationPlan, HasChanges: true,
	})
	if err != nil {
		t.Fatalf("FinishTemplatePlan returned error: %v", err)
	}
	if outcome != domain.PlanOutcomeCanceled || runStatus(t, ctx, pool, "run_123") != domain.TemplateRunCanceled {
		t.Fatalf("outcome = %q, status = %q; want canceled", outcome, runStatus(t, ctx, pool, "run_123"))
	}
}

// The apply claims its run by moving it from approved to locked, and a canceled
// run cannot be claimed. Canceling before the claim takes the same row the
// other way, so exactly one of them wins.
func TestApplyClaimAndCancelBeforeApplyExcludeEachOther(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := savedPlanStore(t, pool)
	seedStackWithTemplate(t, ctx, store)

	// Claimed first: the cancel falls through to the signal path.
	seedPlanRun(t, ctx, pool, "run_claimed", domain.OperationPlan, domain.TemplateRunApproved)
	claimed, err := store.BeginTemplateApply(ctx, "tenant_123", "run_claimed")
	if err != nil || !claimed {
		t.Fatalf("BeginTemplateApply = %v, %v; want claimed", claimed, err)
	}
	if runStatus(t, ctx, pool, "run_claimed") != domain.TemplateRunLocked {
		t.Fatalf("status = %q, want locked", runStatus(t, ctx, pool, "run_claimed"))
	}
	canceled, err := cancelTemplateRunBeforeApply(ctx, pool, domain.TemplateRunCancellation{TenantID: "tenant_123", RunID: "run_claimed", RequestedBy: "user_123"})
	if err != nil || canceled {
		t.Fatalf("cancel after claim = %v, %v; want not canceled here", canceled, err)
	}
	if _, err := pool.Exec(ctx, `update template_runs set status = 'completed' where id = 'run_claimed'`); err != nil {
		t.Fatal(err)
	}

	// Canceled first: the claim loses.
	seedPlanRun(t, ctx, pool, "run_canceled", domain.OperationPlan, domain.TemplateRunApproved)
	if _, err := store.CreatePlanKey(ctx, "tenant_123", "run_canceled"); err != nil {
		t.Fatal(err)
	}
	canceled, err = cancelTemplateRunBeforeApply(ctx, pool, domain.TemplateRunCancellation{TenantID: "tenant_123", RunID: "run_canceled", RequestedBy: "user_123", Reason: "changed my mind"})
	if err != nil || !canceled {
		t.Fatalf("cancel before claim = %v, %v; want canceled", canceled, err)
	}
	claimed, err = store.BeginTemplateApply(ctx, "tenant_123", "run_canceled")
	if err != nil || claimed {
		t.Fatalf("BeginTemplateApply after cancel = %v, %v; want not claimed", claimed, err)
	}
	if runStatus(t, ctx, pool, "run_canceled") != domain.TemplateRunCanceled {
		t.Fatalf("status = %q, want canceled", runStatus(t, ctx, pool, "run_canceled"))
	}
	if !planKeyIsNull(t, ctx, pool, "run_canceled") {
		t.Fatal("canceled run kept its plan key")
	}
}

// Every way a run becomes terminal drops its plan key and stops it being the
// template's pending plan: a discarded or failed plan is no longer reviewed.
func TestTerminalRunsDropTheirPlanKeyAndPendingPlan(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := savedPlanStore(t, pool)
	seedStackWithTemplate(t, ctx, store)
	seedPlanRun(t, ctx, pool, "run_123", domain.OperationPlan, domain.TemplateRunPlanFinished)
	if _, err := store.CreatePlanKey(ctx, "tenant_123", "run_123"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.FinishTemplatePlan(ctx, domain.FinishPlanActivityInput{
		TenantID: "tenant_123", RunID: "run_123", StackTemplateID: "stack_template_123", Operation: domain.OperationPlan, HasChanges: true,
	}); err != nil {
		t.Fatal(err)
	}

	if err := store.RecordTemplateRunStatus(ctx, domain.TemplateRunStatusActivityInput{
		TenantID: "tenant_123", RunID: "run_123", StackTemplateID: "stack_template_123", Operation: domain.OperationPlan, Status: domain.TemplateRunFailed,
	}); err != nil {
		t.Fatalf("RecordTemplateRunStatus returned error: %v", err)
	}
	if !planKeyIsNull(t, ctx, pool, "run_123") {
		t.Fatal("failed run kept its plan key")
	}
	stackTemplate, err := store.GetStackTemplate(ctx, "tenant_123", "stack_template_123")
	if err != nil {
		t.Fatal(err)
	}
	if stackTemplate.PendingPlanRunID != "" || stackTemplate.PlanState() != domain.PlanNone {
		t.Fatalf("pending plan = %q, plan state = %q; want none", stackTemplate.PendingPlanRunID, stackTemplate.PlanState())
	}
}

// A plan run's apply finishing records its snapshot as live.
func TestApplyFinishedOnAPlanRunRecordsLastApplied(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := savedPlanStore(t, pool)
	seedStackWithTemplate(t, ctx, store)
	seedPlanRun(t, ctx, pool, "run_123", domain.OperationPlan, domain.TemplateRunApplyStarted)

	if err := store.RecordTemplateRunStatus(ctx, domain.TemplateRunStatusActivityInput{
		TenantID: "tenant_123", RunID: "run_123", StackTemplateID: "stack_template_123", Operation: domain.OperationPlan, Status: domain.TemplateRunApplyFinished,
	}); err != nil {
		t.Fatalf("RecordTemplateRunStatus returned error: %v", err)
	}
	stackTemplate, err := store.GetStackTemplate(ctx, "tenant_123", "stack_template_123")
	if err != nil {
		t.Fatal(err)
	}
	if stackTemplate.LastAppliedRunID != "run_123" {
		t.Fatalf("last applied = %q, want run_123", stackTemplate.LastAppliedRunID)
	}
}

// Runs started before saved plans cannot finish under them, so 0023 closes
// them; finished runs keep their history.
func TestSavedPlansMigrationClosesUnfinishedRuns(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openTestPool(t, ctx)
	migrateThrough(t, ctx, pool, "0022_run_numbers")
	if _, err := pool.Exec(ctx, `
		insert into template_runs (
			id, tenant_id, stack_template_id, template_revision_id,
			operation, selected_ref, workspace_name, config_json, status, trigger_actor, run_number
		) values
			('run_waiting', 'tenant_123', 'stack_template_a', 'rev', 'apply', 'main', 'ws', '{}', 'waiting_approval', 'user_123', 2),
			('run_done', 'tenant_123', 'stack_template_a', 'rev', 'plan', 'main', 'ws', '{}', 'completed', 'user_123', 1)
	`); err != nil {
		t.Fatalf("seed pre-migration runs: %v", err)
	}

	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate to head: %v", err)
	}
	if got := runStatus(t, ctx, pool, "run_waiting"); got != domain.TemplateRunFailed {
		t.Fatalf("unfinished run status = %q, want failed", got)
	}
	if got := runStatus(t, ctx, pool, "run_done"); got != domain.TemplateRunCompleted {
		t.Fatalf("finished run status = %q, want completed", got)
	}
}
