package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/vishu42/tflive/internal/domain"
)

// A running run records the step it starts, and a new step replaces the last.
func TestRecordTemplateRunStepReplacesTheStep(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)
	seedTemplateRun(t, ctx, pool, templateRunAt("stack_template_123", "run_123", domain.TemplateRunRunning))

	for _, step := range []domain.TemplateRunStep{domain.TemplateRunStepFetchingSource, domain.TemplateRunStepInitializing} {
		if err := store.RecordTemplateRunStep(ctx, domain.TemplateRunStepActivityInput{
			TenantID: "tenant_123", RunID: "run_123", Step: step,
		}); err != nil {
			t.Fatalf("RecordTemplateRunStep(%q) returned error: %v", step, err)
		}
	}

	run, err := store.GetTemplateRun(ctx, "tenant_123", "run_123")
	if err != nil {
		t.Fatal(err)
	}
	if run.Step != domain.TemplateRunStepInitializing {
		t.Fatalf("step = %q, want %q", run.Step, domain.TemplateRunStepInitializing)
	}
}

// Every step in domain.AllTemplateRunSteps must round-trip through the
// step column: 0026's check constraint is a copy of that list, and this
// keeps the two from drifting apart unnoticed.
func TestRecordTemplateRunStepAcceptsEveryDomainStep(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)
	seedTemplateRun(t, ctx, pool, templateRunAt("stack_template_123", "run_123", domain.TemplateRunRunning))

	for _, step := range domain.AllTemplateRunSteps {
		if err := store.RecordTemplateRunStep(ctx, domain.TemplateRunStepActivityInput{
			TenantID: "tenant_123", RunID: "run_123", Step: step,
		}); err != nil {
			t.Fatalf("RecordTemplateRunStep(%q) returned error: %v", step, err)
		}
		run, err := store.GetTemplateRun(ctx, "tenant_123", "run_123")
		if err != nil {
			t.Fatal(err)
		}
		if run.Step != step {
			t.Fatalf("step = %q, want %q", run.Step, step)
		}
	}
}

// Only a running run has a step to start: a queued, waiting or finished one
// is not found, and neither is another tenant's.
func TestRecordTemplateRunStepNeedsARunningRunOfTheTenant(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name   string
		status domain.TemplateRunStatus
		tenant domain.TenantID
	}{
		{name: "queued", status: domain.TemplateRunQueued, tenant: "tenant_123"},
		{name: "waiting", status: domain.TemplateRunWaitingApproval, tenant: "tenant_123"},
		{name: "completed", status: domain.TemplateRunCompleted, tenant: "tenant_123"},
		{name: "other tenant", status: domain.TemplateRunRunning, tenant: "tenant_456"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			pool := openMigratedTestPool(t, ctx)
			store := NewStore(pool)
			seedTemplateRun(t, ctx, pool, templateRunAt("stack_template_123", "run_123", testCase.status))

			err := store.RecordTemplateRunStep(ctx, domain.TemplateRunStepActivityInput{
				TenantID: testCase.tenant, RunID: "run_123", Step: domain.TemplateRunStepPlanning,
			})
			if !errors.Is(err, ErrNotFound) {
				t.Fatalf("error = %v, want ErrNotFound", err)
			}
		})
	}
}

func TestRecordTemplateRunStepRejectsAnUnknownStep(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)
	seedTemplateRun(t, ctx, pool, templateRunAt("stack_template_123", "run_123", domain.TemplateRunRunning))

	for _, step := range []domain.TemplateRunStep{"", "cloning"} {
		if err := store.RecordTemplateRunStep(ctx, domain.TemplateRunStepActivityInput{
			TenantID: "tenant_123", RunID: "run_123", Step: step,
		}); err == nil {
			t.Fatalf("RecordTemplateRunStep(%q) returned nil, want an error", step)
		}
	}
}

// An apply run's apply succeeding makes what it ran the stack template's live
// state: run, revision and config.
func TestRecordTemplateRunEventAppliedRecordsLastApplied(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)
	seedStackWithTemplate(t, ctx, store)
	seedTemplateRun(t, ctx, pool, domain.TemplateRun{
		ID:                 "run_apply_1",
		TenantID:           "tenant_123",
		StackTemplateID:    "stack_template_123",
		TemplateRevisionID: "template_rev_2",
		SourceTemplateID:   "source_template_vpc",
		Operation:          domain.OperationApply,
		SelectedRef:        "main",
		WorkspaceName:      "mtp_acme_prod_vpc_a13f9c",
		ConfigJSON:         json.RawMessage(`{"region":"us-east-1"}`),
		Status:             domain.TemplateRunRunning,
		TriggerActor:       "user_123",
	})

	if err := store.RecordTemplateRunEvent(ctx, domain.TemplateRunEventActivityInput{
		TenantID: "tenant_123", RunID: "run_apply_1", StackTemplateID: "stack_template_123",
		Operation: domain.OperationApply, Event: domain.TemplateRunApplied,
	}); err != nil {
		t.Fatalf("RecordTemplateRunEvent returned error: %v", err)
	}

	stackTemplate, err := store.GetStackTemplate(ctx, "tenant_123", "stack_template_123")
	if err != nil {
		t.Fatal(err)
	}
	if stackTemplate.LastAppliedRunID != "run_apply_1" {
		t.Fatalf("last applied run = %q, want run_apply_1", stackTemplate.LastAppliedRunID)
	}
	if stackTemplate.LastAppliedTemplateRevisionID != "template_rev_2" {
		t.Fatalf("last applied revision = %q, want template_rev_2", stackTemplate.LastAppliedTemplateRevisionID)
	}
	if stackTemplate.LiveState() != domain.LiveMatches {
		t.Fatalf("LiveState() = %q, want matches (applied config %s)", stackTemplate.LiveState(), stackTemplate.LastAppliedConfigJSON)
	}
}

// An auto-approved apply has no plan to count, so the counts it reports come
// with applied; an event without counts leaves the plan's alone.
func TestRecordTemplateRunEventRecordsTheCountsItCarries(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)
	seedStackWithTemplate(t, ctx, store)
	seedTemplateRun(t, ctx, pool, domain.TemplateRun{
		ID: "run_123", TenantID: "tenant_123", StackTemplateID: "stack_template_123",
		Operation: domain.OperationApply, SelectedRef: "main", WorkspaceName: "ws",
		Status: domain.TemplateRunRunning, TriggerActor: "user_123", AutoApprove: true,
	})
	event := domain.TemplateRunEventActivityInput{
		TenantID: "tenant_123", RunID: "run_123", StackTemplateID: "stack_template_123",
		Operation: domain.OperationApply, Event: domain.TemplateRunApplied,
	}

	if err := store.RecordTemplateRunEvent(ctx, event); err != nil {
		t.Fatal(err)
	}
	run, err := store.GetTemplateRun(ctx, "tenant_123", "run_123")
	if err != nil {
		t.Fatal(err)
	}
	if run.PlanSummary != nil {
		t.Fatalf("plan summary = %#v, want none from an event without counts", run.PlanSummary)
	}

	event.Summary = &domain.PlanSummary{Add: 2, Change: 1}
	if err := store.RecordTemplateRunEvent(ctx, event); err != nil {
		t.Fatal(err)
	}
	run, err = store.GetTemplateRun(ctx, "tenant_123", "run_123")
	if err != nil {
		t.Fatal(err)
	}
	if run.PlanSummary == nil || *run.PlanSummary != (domain.PlanSummary{Add: 2, Change: 1}) {
		t.Fatalf("plan summary = %#v, want the counts the event carried", run.PlanSummary)
	}
}

// A destroy run moves its stack template to destroying before it destroys,
// and to destroyed once it has.
func TestRecordTemplateRunEventMovesADestroyedTemplateThroughItsLifecycle(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)
	if err := store.CreateStack(ctx, domain.Stack{
		ID: "stack_destroy_1", TenantID: "tenant_destroy_1", Name: "Destroy Test", Slug: "destroy-test",
		CreatedBy: "user_123", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateStackTemplate(ctx, domain.StackTemplate{
		ID: "st_destroy_1", TenantID: "tenant_destroy_1", StackID: "stack_destroy_1", Lifecycle: domain.StackTemplateActive,
	}); err != nil {
		t.Fatal(err)
	}
	seedTemplateRun(t, ctx, pool, domain.TemplateRun{
		ID: "run_destroy_1", TenantID: "tenant_destroy_1", StackTemplateID: "st_destroy_1",
		Operation: domain.OperationDestroy, SelectedRef: "main", WorkspaceName: "ws_destroy",
		Status: domain.TemplateRunRunning, TriggerActor: "user_123",
	})

	for _, step := range []struct {
		event domain.TemplateRunEvent
		want  domain.StackTemplateLifecycle
	}{
		{event: domain.TemplateRunDestroying, want: domain.StackTemplateDestroying},
		{event: domain.TemplateRunDestroyed, want: domain.StackTemplateDestroyed},
	} {
		if err := store.RecordTemplateRunEvent(ctx, domain.TemplateRunEventActivityInput{
			TenantID: "tenant_destroy_1", RunID: "run_destroy_1", StackTemplateID: "st_destroy_1",
			Operation: domain.OperationDestroy, Event: step.event,
		}); err != nil {
			t.Fatalf("RecordTemplateRunEvent(%q) returned error: %v", step.event, err)
		}
		var lifecycle domain.StackTemplateLifecycle
		if err := pool.QueryRow(ctx, `
			select lifecycle from stack_templates where tenant_id = $1 and id = $2
		`, "tenant_destroy_1", "st_destroy_1").Scan(&lifecycle); err != nil {
			t.Fatal(err)
		}
		if lifecycle != step.want {
			t.Fatalf("after %q lifecycle = %q, want %q", step.event, lifecycle, step.want)
		}
	}
}

// Temporal redelivers an activity whose acknowledgement was lost. A status
// the run already has is that retry: it succeeds and changes nothing.
func TestRecordTemplateRunStatusIsIdempotent(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)
	run := templateRunAt("stack_template_123", "run_123", domain.TemplateRunRunning)
	seedTemplateRun(t, ctx, pool, run)
	completed := domain.TemplateRunStatusActivityInput{
		TenantID: "tenant_123", RunID: "run_123", StackTemplateID: "stack_template_123",
		Operation: run.Operation, Status: domain.TemplateRunCompleted,
	}

	for attempt := 1; attempt <= 2; attempt++ {
		if err := store.RecordTemplateRunStatus(ctx, completed); err != nil {
			t.Fatalf("attempt %d: RecordTemplateRunStatus returned error: %v", attempt, err)
		}
	}
	if got := runStatus(t, ctx, pool, "run_123"); got != domain.TemplateRunCompleted {
		t.Fatalf("status = %q, want completed", got)
	}
}

// The workflow moves a run only along its lifecycle: a finished run cannot
// start running again, and a queued one cannot complete without running.
func TestRecordTemplateRunStatusRejectsTransitionsOffTheLifecycle(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		from, to domain.TemplateRunStatus
	}{
		{from: domain.TemplateRunCompleted, to: domain.TemplateRunRunning},
		{from: domain.TemplateRunCompleted, to: domain.TemplateRunFailed},
		{from: domain.TemplateRunQueued, to: domain.TemplateRunCompleted},
		{from: domain.TemplateRunCanceled, to: domain.TemplateRunRunning},
	} {
		t.Run(string(testCase.from)+"_to_"+string(testCase.to), func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			pool := openMigratedTestPool(t, ctx)
			store := NewStore(pool)
			run := templateRunAt("stack_template_123", "run_123", testCase.from)
			seedTemplateRun(t, ctx, pool, run)

			err := store.RecordTemplateRunStatus(ctx, domain.TemplateRunStatusActivityInput{
				TenantID: "tenant_123", RunID: "run_123", StackTemplateID: "stack_template_123",
				Operation: run.Operation, Status: testCase.to,
			})
			if !errors.Is(err, ErrTemplateRunTransition) {
				t.Fatalf("error = %v, want ErrTemplateRunTransition", err)
			}
			if got := runStatus(t, ctx, pool, "run_123"); got != testCase.from {
				t.Fatalf("status = %q, want it left %q", got, testCase.from)
			}
		})
	}
}

// Waiting, approved and canceled are written by the approval flow, never by
// the workflow's status activity.
func TestRecordTemplateRunStatusRejectsStatusesTheWorkflowDoesNotWrite(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)
	run := templateRunAt("stack_template_123", "run_123", domain.TemplateRunRunning)
	seedTemplateRun(t, ctx, pool, run)

	for _, status := range []domain.TemplateRunStatus{domain.TemplateRunWaitingApproval, domain.TemplateRunApproved, domain.TemplateRunCanceled, domain.TemplateRunQueued} {
		if err := store.RecordTemplateRunStatus(ctx, domain.TemplateRunStatusActivityInput{
			TenantID: "tenant_123", RunID: "run_123", StackTemplateID: "stack_template_123",
			Operation: run.Operation, Status: status,
		}); err == nil {
			t.Fatalf("RecordTemplateRunStatus(%q) returned nil, want an error", status)
		}
	}
}

// A failed run keeps the step it failed on, which is how it says where.
func TestRecordTemplateRunStatusKeepsTheStep(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)
	run := templateRunAt("stack_template_123", "run_123", domain.TemplateRunRunning)
	seedTemplateRun(t, ctx, pool, run)
	if err := store.RecordTemplateRunStep(ctx, domain.TemplateRunStepActivityInput{
		TenantID: "tenant_123", RunID: "run_123", Step: domain.TemplateRunStepFetchingSource,
	}); err != nil {
		t.Fatal(err)
	}

	if err := store.RecordTemplateRunStatus(ctx, domain.TemplateRunStatusActivityInput{
		TenantID: "tenant_123", RunID: "run_123", StackTemplateID: "stack_template_123",
		Operation: run.Operation, Status: domain.TemplateRunFailed, ErrorSummary: "clone failed",
	}); err != nil {
		t.Fatal(err)
	}

	got, err := store.GetTemplateRun(ctx, "tenant_123", "run_123")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.TemplateRunFailed || got.Step != domain.TemplateRunStepFetchingSource {
		t.Fatalf("status, step = %q, %q; want failed, fetching_source", got.Status, got.Step)
	}
}

// An event belongs to one kind of run, and only a running run has events: an
// apply run cannot record destroyed, and a finished run records nothing.
func TestRecordTemplateRunEventRejectsEventsTheRunCannotHave(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name      string
		operation domain.OperationType
		status    domain.TemplateRunStatus
		event     domain.TemplateRunEvent
		notFound  bool
	}{
		{name: "destroyed on an apply run", operation: domain.OperationApply, status: domain.TemplateRunRunning, event: domain.TemplateRunDestroyed},
		{name: "applied on a destroy run", operation: domain.OperationDestroy, status: domain.TemplateRunRunning, event: domain.TemplateRunApplied},
		{name: "unknown event", operation: domain.OperationApply, status: domain.TemplateRunRunning, event: "exploded"},
		{name: "finished run", operation: domain.OperationApply, status: domain.TemplateRunCompleted, event: domain.TemplateRunApplied, notFound: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			pool := openMigratedTestPool(t, ctx)
			store := NewStore(pool)
			seedStackWithTemplate(t, ctx, store)
			run := templateRunAt("stack_template_123", "run_123", testCase.status)
			run.Operation = testCase.operation
			seedTemplateRun(t, ctx, pool, run)

			err := store.RecordTemplateRunEvent(ctx, domain.TemplateRunEventActivityInput{
				TenantID: "tenant_123", RunID: "run_123", StackTemplateID: "stack_template_123",
				Operation: testCase.operation, Event: testCase.event,
			})
			if err == nil {
				t.Fatal("RecordTemplateRunEvent returned nil, want an error")
			}
			if testCase.notFound != errors.Is(err, ErrNotFound) {
				t.Fatalf("error = %v, want ErrNotFound: %t", err, testCase.notFound)
			}
		})
	}
}
