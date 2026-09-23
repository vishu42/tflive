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
	seedPlanRun(t, ctx, pool, "run_123", domain.OperationApply, domain.TemplateRunRunning)

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
	seedPlanRun(t, ctx, pool, "run_123", domain.OperationApply, domain.TemplateRunRunning)

	outcome, err := store.FinishTemplatePlan(ctx, domain.FinishPlanActivityInput{
		TenantID: "tenant_123", RunID: "run_123", StackTemplateID: "stack_template_123", Operation: domain.OperationApply,
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

// A plan run with changes records what it would do and completes. It saved
// no plan, so it is nobody's pending plan and there is nothing to approve.
func TestFinishTemplatePlanCompletesAPlanRunWithChanges(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := savedPlanStore(t, pool)
	seedStackWithTemplate(t, ctx, store)
	seedPlanRun(t, ctx, pool, "run_123", domain.OperationPlan, domain.TemplateRunRunning)

	outcome, err := store.FinishTemplatePlan(ctx, domain.FinishPlanActivityInput{
		TenantID: "tenant_123", RunID: "run_123", StackTemplateID: "stack_template_123", Operation: domain.OperationPlan,
		HasChanges: true, Summary: domain.PlanSummary{Add: 1, Destroy: 2},
	})
	if err != nil {
		t.Fatalf("FinishTemplatePlan returned error: %v", err)
	}
	if outcome != domain.PlanOutcomePlanned {
		t.Fatalf("outcome = %q, want planned", outcome)
	}
	run, err := store.GetTemplateRun(ctx, "tenant_123", "run_123")
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != domain.TemplateRunRunning {
		t.Fatalf("status = %q, want running", run.Status)
	}
	if run.PlanSummary == nil || *run.PlanSummary != (domain.PlanSummary{Add: 1, Destroy: 2}) {
		t.Fatalf("plan summary = %#v", run.PlanSummary)
	}
	stackTemplate, err := store.GetStackTemplate(ctx, "tenant_123", "stack_template_123")
	if err != nil {
		t.Fatal(err)
	}
	if stackTemplate.PendingPlanRunID != "" || stackTemplate.LastAppliedRunID != "" {
		t.Fatalf("pending plan = %q, last applied = %q; want neither", stackTemplate.PendingPlanRunID, stackTemplate.LastAppliedRunID)
	}
}

// A plan with no changes means the infrastructure already is the run's
// snapshot, so that snapshot becomes what is live, and nothing waits. That
// holds for a plan run as much as for an apply run.
func TestFinishTemplatePlanWithoutChangesRecordsTheSnapshotAsLive(t *testing.T) {
	t.Parallel()

	for _, operation := range []domain.OperationType{domain.OperationPlan, domain.OperationApply} {
		t.Run(string(operation), func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			pool := openMigratedTestPool(t, ctx)
			store := savedPlanStore(t, pool)
			seedStackWithTemplate(t, ctx, store)
			seedPlanRun(t, ctx, pool, "run_123", operation, domain.TemplateRunRunning)

			outcome, err := store.FinishTemplatePlan(ctx, domain.FinishPlanActivityInput{
				TenantID: "tenant_123", RunID: "run_123", StackTemplateID: "stack_template_123", Operation: operation,
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
		})
	}
}

// The apply claims its run by moving it from approved to locked, and a
// discarded run cannot be claimed. Discarding before the claim takes the same
// row the other way, so exactly one of them wins.
func TestApplyClaimAndDiscardExcludeEachOther(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := savedPlanStore(t, pool)
	seedStackWithTemplate(t, ctx, store)

	// Claimed first: there is no plan left to discard.
	seedPlanRun(t, ctx, pool, "run_claimed", domain.OperationApply, domain.TemplateRunApproved)
	claimed, err := store.BeginTemplateApply(ctx, "tenant_123", "run_claimed", false)
	if err != nil || !claimed {
		t.Fatalf("BeginTemplateApply = %v, %v; want claimed", claimed, err)
	}
	if runStatus(t, ctx, pool, "run_claimed") != domain.TemplateRunRunning {
		t.Fatalf("status = %q, want running", runStatus(t, ctx, pool, "run_claimed"))
	}
	discarded, err := discardTemplateRun(ctx, pool, domain.TemplateRunDiscard{TenantID: "tenant_123", RunID: "run_claimed", RequestedBy: "user_123"})
	if err != nil || discarded {
		t.Fatalf("discard after claim = %v, %v; want nothing discarded", discarded, err)
	}
	if _, err := pool.Exec(ctx, `update template_runs set status = 'completed' where id = 'run_claimed'`); err != nil {
		t.Fatal(err)
	}

	// Discarded first: the claim loses.
	seedPlanRun(t, ctx, pool, "run_canceled", domain.OperationApply, domain.TemplateRunApproved)
	if _, err := store.CreatePlanKey(ctx, "tenant_123", "run_canceled"); err != nil {
		t.Fatal(err)
	}
	discarded, err = discardTemplateRun(ctx, pool, domain.TemplateRunDiscard{TenantID: "tenant_123", RunID: "run_canceled", RequestedBy: "user_123", Reason: "changed my mind"})
	if err != nil || !discarded {
		t.Fatalf("discard before claim = %v, %v; want discarded", discarded, err)
	}
	claimed, err = store.BeginTemplateApply(ctx, "tenant_123", "run_canceled", false)
	if err != nil || claimed {
		t.Fatalf("BeginTemplateApply after discard = %v, %v; want not claimed", claimed, err)
	}
	if runStatus(t, ctx, pool, "run_canceled") != domain.TemplateRunCanceled {
		t.Fatalf("status = %q, want canceled", runStatus(t, ctx, pool, "run_canceled"))
	}
	if !planKeyIsNull(t, ctx, pool, "run_canceled") {
		t.Fatal("discarded run kept its plan key")
	}
}

// seedAutoApprovedRun seeds an auto-approved apply run of the fixture stack
// template in status.
func seedAutoApprovedRun(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id domain.TemplateRunID, status domain.TemplateRunStatus) {
	t.Helper()
	seedTemplateRun(t, ctx, pool, domain.TemplateRun{
		ID:                 id,
		TenantID:           "tenant_123",
		StackTemplateID:    "stack_template_123",
		TemplateRevisionID: "template_rev_2",
		SourceTemplateID:   "source_template_vpc",
		Operation:          domain.OperationApply,
		SelectedRef:        "main",
		WorkspaceName:      "mtp_acme_prod_vpc_a13f9c",
		ConfigJSON:         json.RawMessage(`{"region":"us-east-1"}`),
		Status:             status,
		TriggerActor:       "user_123",
		AutoApprove:        true,
	})
}

// An auto-approved apply run has no plan to approve, so its claim takes it
// straight from queued. Each kind of claim takes only its own kind of run.
func TestAutoApprovedApplyIsClaimedFromQueued(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := savedPlanStore(t, pool)
	seedStackWithTemplate(t, ctx, store)

	seedAutoApprovedRun(t, ctx, pool, "run_auto", domain.TemplateRunQueued)
	if claimed, err := store.BeginTemplateApply(ctx, "tenant_123", "run_auto", false); err != nil || claimed {
		t.Fatalf("approved claim of an auto-approved run = %v, %v; want not claimed", claimed, err)
	}
	if claimed, err := store.BeginTemplateApply(ctx, "tenant_123", "run_auto", true); err != nil || !claimed {
		t.Fatalf("BeginTemplateApply = %v, %v; want claimed", claimed, err)
	}
	if runStatus(t, ctx, pool, "run_auto") != domain.TemplateRunRunning {
		t.Fatalf("status = %q, want running", runStatus(t, ctx, pool, "run_auto"))
	}
	if _, err := pool.Exec(ctx, `update template_runs set status = 'completed' where id = 'run_auto'`); err != nil {
		t.Fatal(err)
	}

	seedPlanRun(t, ctx, pool, "run_approved", domain.OperationApply, domain.TemplateRunApproved)
	if claimed, err := store.BeginTemplateApply(ctx, "tenant_123", "run_approved", true); err != nil || claimed {
		t.Fatalf("auto-approved claim of an approved run = %v, %v; want not claimed", claimed, err)
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
	seedPlanRun(t, ctx, pool, "run_123", domain.OperationApply, domain.TemplateRunRunning)
	if _, err := store.CreatePlanKey(ctx, "tenant_123", "run_123"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.FinishTemplatePlan(ctx, domain.FinishPlanActivityInput{
		TenantID: "tenant_123", RunID: "run_123", StackTemplateID: "stack_template_123", Operation: domain.OperationApply, HasChanges: true,
	}); err != nil {
		t.Fatal(err)
	}

	if err := store.RecordTemplateRunStatus(ctx, domain.TemplateRunStatusActivityInput{
		TenantID: "tenant_123", RunID: "run_123", StackTemplateID: "stack_template_123", Operation: domain.OperationApply, Status: domain.TemplateRunFailed,
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

// Runs 0027 finds mid-progress were in flight under a workflow that wrote the
// old statuses; they are closed out, as 0021 and 0023 did. Runs in a
// lifecycle state keep it. A closed-out run that held a saved plan must not
// stay reviewable: releaseRunPlan does the same two things (drop the plan
// key, clear the template's pending plan) for every other path that makes a
// run terminal, and 0027's close-out is such a path.
func TestLifecycleStatusMigrationClosesRunsMidProgress(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openTestPool(t, ctx)
	migrateThrough(t, ctx, pool, "0026_run_steps")
	if _, err := pool.Exec(ctx, `
		insert into template_runs (
			id, tenant_id, stack_template_id, template_revision_id,
			operation, selected_ref, workspace_name, config_json, status, trigger_actor, run_number
		) values
			('run_planning', 'tenant_123', 'stack_template_a', 'rev', 'apply', 'main', 'ws', '{}', 'plan_started', 'user_123', 3),
			('run_waiting', 'tenant_123', 'stack_template_b', 'rev', 'apply', 'main', 'ws', '{}', 'waiting_approval', 'user_123', 1),
			('run_done', 'tenant_123', 'stack_template_a', 'rev', 'plan', 'main', 'ws', '{}', 'completed', 'user_123', 1),
			('run_locked', 'tenant_123', 'stack_template_c', 'rev', 'apply', 'main', 'ws', '{}', 'locked', 'user_123', 1)
	`); err != nil {
		t.Fatalf("seed pre-migration runs: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into template_runs (
			id, tenant_id, stack_template_id, template_revision_id,
			operation, selected_ref, workspace_name, config_json, status, trigger_actor, run_number,
			plan_artifact_dek
		) values (
			'run_apply_started', 'tenant_123', 'stack_template_d', 'rev', 'apply', 'main', 'ws', '{}', 'apply_started', 'user_123', 1,
			'sealed-plan-key'
		)
	`); err != nil {
		t.Fatalf("seed pre-migration run with a saved plan: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into stack_templates (
			id, tenant_id, stack_id, workspace_name, lifecycle,
			pending_plan_run_id, pending_plan_template_revision_id, pending_plan_config_json, pending_plan_at
		) values (
			'stack_template_d', 'tenant_123', 'stack_d', 'ws', 'active',
			'run_apply_started', 'rev', '{}', now()
		)
	`); err != nil {
		t.Fatalf("seed pre-migration stack template with a pending plan: %v", err)
	}

	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate to head: %v", err)
	}
	for runID, want := range map[string]domain.TemplateRunStatus{
		"run_planning":      domain.TemplateRunFailed,
		"run_locked":        domain.TemplateRunFailed,
		"run_waiting":       domain.TemplateRunWaitingApproval,
		"run_done":          domain.TemplateRunCompleted,
		"run_apply_started": domain.TemplateRunFailed,
	} {
		if got := runStatus(t, ctx, pool, domain.TemplateRunID(runID)); got != want {
			t.Errorf("%s status = %q, want %q", runID, got, want)
		}
	}
	if !planKeyIsNull(t, ctx, pool, "run_apply_started") {
		t.Error("closed-out run kept its plan key")
	}
	var pendingPlanRunID string
	if err := pool.QueryRow(ctx, `
		select pending_plan_run_id from stack_templates where tenant_id = 'tenant_123' and id = 'stack_template_d'
	`).Scan(&pendingPlanRunID); err != nil {
		t.Fatalf("read pending plan run id: %v", err)
	}
	if pendingPlanRunID != "" {
		t.Errorf("pending plan run id = %q, want none: a closed-out run must stop counting as reviewed", pendingPlanRunID)
	}
}

// A claim whose acknowledgement was lost is retried. The run is already
// running under this claim (nothing else moves an approved run to running),
// so the retry must report the claim, not a lost race: reporting false would
// end the apply workflow and leave the run running forever.
func TestBeginTemplateApplyClaimIsIdempotent(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := savedPlanStore(t, pool)
	seedStackWithTemplate(t, ctx, store)
	seedPlanRun(t, ctx, pool, "run_123", domain.OperationApply, domain.TemplateRunApproved)

	for attempt := 1; attempt <= 2; attempt++ {
		claimed, err := store.BeginTemplateApply(ctx, "tenant_123", "run_123", false)
		if err != nil {
			t.Fatal(err)
		}
		if !claimed {
			t.Fatalf("attempt %d: claimed = false, want true", attempt)
		}
	}
	if got := runStatus(t, ctx, pool, "run_123"); got != domain.TemplateRunRunning {
		t.Fatalf("status = %q, want running", got)
	}
}
