# Run Status Is Lifecycle, Progress Is a Step: Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reduce `template_runs.status` to 7 lifecycle states. Track progress in a separate `step` column, written when each step starts. Record the stack-template side effects as explicit run events.

**Architecture:**
- The workflow makes three kinds of control-plane write:
  - `RecordTemplateRunStatus` for lifecycle, now guarded and idempotent.
  - `RecordTemplateRunStep` for progress.
  - `RecordTemplateRunEvent` for `applied`, `destroying` and `destroyed`.
- The rollout is additive first:
  - Task 1 adds the vocabulary, the column and the new store writes.
  - Task 2 adds the activities.
  - Task 3 switches the workflow over.
  - Task 4 removes the old statuses and guards transitions.
  - Task 5 moves the web client over.

**Tech Stack:** Go, Temporal Go SDK (`testsuite`), Postgres via pgx (migrations are embedded SQL under `internal/postgres/migrations/`), React, TypeScript and Vitest.

**Spec:** `docs/superpowers/specs/2026-09-23-run-status-and-step-design.md`

## Global Constraints

- **Status values** (exact): `queued`, `running`, `waiting_approval`, `approved`, `completed`, `failed`, `canceled`.
- **Step values** (exact): `waiting_for_executor`, `preparing_workspace`, `fetching_source`, `restoring_plan`, `initializing`, `selecting_workspace`, `planning`, `saving_plan`, `applying`. `''` means no step yet.
- **Event values** (exact): `applied`, `destroying`, `destroyed`.
- **A step is recorded before its work starts, never after.** It is never cleared, and teardown is never a step.
- **Only the run workflow records steps and events,** through its unexported `recordStep` and `recordEvent`. There are three reasons:
  - Executor activities have no database, and whatever they return is untrusted.
  - A step spans several activities, which only the workflow sees.
  - Writes from the workflow happen in a fixed order in its history.

  No activity calls `RecordTemplateRunStep` or `RecordTemplateRunEvent` on the store except the control activity that is that write. Progress within a step (for example, apply counts) goes in heartbeat details, not in new step values.
- **tflive is pre-production.** A migration closes out unfinished rows instead of mapping them; there is no backfill and no backward compatibility.
- **Store tests skip silently without a database.** Run every `internal/postgres` test with `tflive_POSTGRES_TEST_DSN='postgres://tflive:tflive@localhost:55432/tflive_test?sslmode=disable'` after `docker compose up -d postgres`. A "PASS" without the DSN proves nothing.
- **Formatting:** `gofmt -w` every Go file you touch.
- **Commits:** subjects use a lowercase prefix (`feat:`, `refactor:`, `test:`, `docs:`) and end with the trailer `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.

## Review Focus

1. **Retried status writes.** Temporal can redeliver `RecordTemplateRunStatus(completed)` after the database already committed it. The retry must succeed as a no-op; it must not fail the run and then fail again recording `failed`. This is pinned in Task 4, Step 1 (`TestRecordTemplateRunStatusIsIdempotent`).
2. **Retried apply claim.** A `BeginApply` retried after its update committed must still report `Claimed: true`. Otherwise the apply workflow exits silently and leaves the run `running` forever, which blocks the stack template through the in-flight index. This is pinned in Task 1 (`TestBeginTemplateApplyClaimIsIdempotent`).
3. **Logs while a run is running.** `RunDetailScreen` keys its log queries on `run.status`. Once status stays `running` for the whole run, logs would never refetch mid-run. The key must include the step. This is pinned in Task 5 (`runProgressTag` test).
4. **A failed run keeps its step.** "Plan failed while fetching source" needs the step to survive the terminal write. Nothing in the terminal SQL may touch `step`. This is pinned in Task 4, Step 1 (`TestRecordTemplateRunStatusKeepsTheStep`).
5. **Rows already in the database when 0027 runs.** A dev database holding `plan_started` or `locked` must migrate, with those runs closed out as `failed`. A waiting or approved run must keep its state. This is pinned in Task 4 (`TestLifecycleStatusMigrationClosesRunsMidProgress`).

---

## File Structure

| File | Responsibility | Tasks |
|---|---|---|
| `internal/domain/template_run.go` | status, step and event vocabulary; `TemplateRun.Step` | 1, 4 |
| `internal/domain/workflow.go` | activity names and inputs | 1, 4 |
| `internal/postgres/migrations/0026_run_steps.sql` | step column; allows `running` | 1 |
| `internal/postgres/migrations/0027_run_status_is_lifecycle.sql` | narrows status to 7 values | 4 |
| `internal/postgres/run_progress.go` (new) | `RecordTemplateRunStep`, `RecordTemplateRunEvent` | 1 |
| `internal/postgres/run_progress_test.go` (new) | tests for the above | 1 |
| `internal/postgres/repositories.go` | status write (guarded in Task 4); run reads include `step`; stack-template helpers | 1, 4 |
| `internal/postgres/saved_plans.go` | `BeginTemplateApply` claims into `running` | 1 |
| `internal/activities/control.go` | two new control activities | 2 |
| `cmd/api/main.go` | registers them | 2 |
| `internal/workflows/template_run.go` | records steps and events; no progress statuses | 3 |
| `web/src/api/types.ts`, `web/src/features/runs/runStatusLabel.ts`, `web/src/shared/statusTone.ts`, `web/src/features/runs/RunDetailScreen.tsx` | client | 5 |
| `docs/openapi.yaml`, `docs/architecture.md`, `docs/apply-run-sequence.md` | docs | 1, 4 |

---

### Task 1: Step and event vocabulary, the step column, and the new store writes

**Files:**
- Modify: `internal/domain/template_run.go`, `internal/domain/workflow.go`, `internal/domain/domain_test.go`
- Create: `internal/postgres/migrations/0026_run_steps.sql`, `internal/postgres/run_progress.go`, `internal/postgres/run_progress_test.go`
- Modify: `internal/postgres/repositories.go` (the reads at the `GetTemplateRun` and `ListTemplateRuns` selects, and the stack-template helpers at lines ~1472-1560), `internal/postgres/saved_plans.go:183-198`, `internal/postgres/saved_plans_test.go`, `internal/postgres/store_test.go`
- Modify: `docs/openapi.yaml` (the `TemplateRun` schema, ~line 1884)

**Interfaces:**
- **Produces (domain):**
  - `TemplateRunRunning TemplateRunStatus = "running"`
  - `type TemplateRunStep string`, with constants `TemplateRunStepWaitingForExecutor`, `TemplateRunStepPreparingWorkspace`, `TemplateRunStepFetchingSource`, `TemplateRunStepRestoringPlan`, `TemplateRunStepInitializing`, `TemplateRunStepSelectingWorkspace`, `TemplateRunStepPlanning`, `TemplateRunStepSavingPlan`, `TemplateRunStepApplying`
  - `AllTemplateRunSteps`, and `(TemplateRunStep) Valid() bool`
  - `type TemplateRunEvent string`, with constants `TemplateRunApplied`, `TemplateRunDestroying`, `TemplateRunDestroyed`
  - `TemplateRun.Step TemplateRunStep` (JSON `step`)
  - `RecordTemplateRunStepActivityName = "RecordTemplateRunStep"` and `RecordTemplateRunEventActivityName = "RecordTemplateRunEvent"`
  - `TemplateRunStepActivityInput{RunID, TenantID, Step}`
  - `TemplateRunEventActivityInput{RunID, TenantID, StackTemplateID, Operation, Event, Summary *PlanSummary}`
- **Produces (postgres):**
  - `(*Store) RecordTemplateRunStep(ctx, domain.TemplateRunStepActivityInput) error`
  - `(*Store) RecordTemplateRunEvent(ctx, domain.TemplateRunEventActivityInput) error`
  - `BeginTemplateApply` now claims into `running`, and reports `true` for a run it already claimed.

- [ ] **Step 1: Add the domain vocabulary**

In `internal/domain/template_run.go`, add `TemplateRunRunning` to the status constants, directly after `TemplateRunQueued`:

```go
	TemplateRunQueued            TemplateRunStatus = "queued"
	// TemplateRunRunning is a run a workflow is working on: planning, or,
	// once claimed for its apply, applying. What it is doing right now is its
	// Step.
	TemplateRunRunning           TemplateRunStatus = "running"
```

Add `TemplateRunRunning,` to `AllTemplateRunStatuses`, directly after `TemplateRunQueued,`.

After the `Terminal` method, add:

```go
// TemplateRunStep is what a running run is doing right now, for the people
// watching it. It is not a status: nothing branches on it. It is recorded when
// a step starts, never when one finishes, and kept when the run ends, so a
// failed run still says where it failed. The zero value is a run that has not
// started a step.
type TemplateRunStep string

const (
	TemplateRunStepWaitingForExecutor TemplateRunStep = "waiting_for_executor"
	TemplateRunStepPreparingWorkspace TemplateRunStep = "preparing_workspace"
	TemplateRunStepFetchingSource     TemplateRunStep = "fetching_source"
	TemplateRunStepRestoringPlan      TemplateRunStep = "restoring_plan"
	TemplateRunStepInitializing       TemplateRunStep = "initializing"
	TemplateRunStepSelectingWorkspace TemplateRunStep = "selecting_workspace"
	TemplateRunStepPlanning           TemplateRunStep = "planning"
	TemplateRunStepSavingPlan         TemplateRunStep = "saving_plan"
	TemplateRunStepApplying           TemplateRunStep = "applying"
)

// AllTemplateRunSteps is every step a run may record, in the order a run
// takes them. The step check constraint in the migrations lists the same
// values.
var AllTemplateRunSteps = []TemplateRunStep{
	TemplateRunStepWaitingForExecutor,
	TemplateRunStepPreparingWorkspace,
	TemplateRunStepFetchingSource,
	TemplateRunStepRestoringPlan,
	TemplateRunStepInitializing,
	TemplateRunStepSelectingWorkspace,
	TemplateRunStepPlanning,
	TemplateRunStepSavingPlan,
	TemplateRunStepApplying,
}

// Valid reports whether the step is one a run may record. The zero value is
// not: it is what a run has before it records one.
func (step TemplateRunStep) Valid() bool {
	return slices.Contains(AllTemplateRunSteps, step)
}

// TemplateRunEvent is something a run did to its stack template. The workflow
// records it as it happens, rather than the store inferring it from a status.
type TemplateRunEvent string

const (
	// TemplateRunApplied is an apply run's apply succeeding: what it applied
	// is now what is live.
	TemplateRunApplied TemplateRunEvent = "applied"
	// TemplateRunDestroying is a destroy run about to destroy. From here, a
	// failure leaves its stack template failed.
	TemplateRunDestroying TemplateRunEvent = "destroying"
	// TemplateRunDestroyed is a destroy run's destroy succeeding, or its plan
	// finding nothing left to destroy.
	TemplateRunDestroyed TemplateRunEvent = "destroyed"
)
```

In `TemplateRun`, add the field directly after `Status`:

```go
	Status             TemplateRunStatus  `json:"status"`
	// Step is what the run is doing, or, once it ended, what it was doing
	// last. Empty until the run starts its first step.
	Step TemplateRunStep `json:"step"`
```

In `internal/domain/workflow.go`, add the two names to the activity-name block, directly after `RecordTemplateRunLogActivityName`:

```go
	RecordTemplateRunStepActivityName            = "RecordTemplateRunStep"
	RecordTemplateRunEventActivityName           = "RecordTemplateRunEvent"
```

Then add these after `TemplateRunStatusActivityInput`:

```go
// TemplateRunStepActivityInput records the step a running run has started.
type TemplateRunStepActivityInput struct {
	RunID    TemplateRunID
	TenantID TenantID
	Step     TemplateRunStep
}

// TemplateRunEventActivityInput records something a running run did to its
// stack template.
type TemplateRunEventActivityInput struct {
	RunID           TemplateRunID
	TenantID        TenantID
	StackTemplateID StackTemplateID
	Operation       OperationType
	Event           TemplateRunEvent
	// Summary, when set, records the run's change counts with the event. An
	// auto-approved apply has no plan to count, so it records what the apply
	// itself reported with TemplateRunApplied.
	Summary *PlanSummary
}
```

- [ ] **Step 2: Write the domain test for steps**

Append to `internal/domain/domain_test.go`:

```go
func TestTemplateRunStepValid(t *testing.T) {
	t.Parallel()

	for _, step := range AllTemplateRunSteps {
		if !step.Valid() {
			t.Errorf("%q is listed but not valid", step)
		}
	}
	for _, step := range []TemplateRunStep{"", "cloning", "plan_started"} {
		if step.Valid() {
			t.Errorf("%q is valid, want invalid", step)
		}
	}
}
```

Run: `go test ./internal/domain/ -run 'TestTemplateRunStepValid|TestTemplateRunStatusValid' -count=1`
Expected: PASS. `TestTemplateRunStatusValid` reads the constants back out of the source, so it confirms that `TemplateRunRunning` was added to the slice.

- [ ] **Step 3: Write the migration**

Create `internal/postgres/migrations/0026_run_steps.sql`:

```sql
-- A run's step is what it is doing right now, for the people watching it:
-- fetching source, initializing, planning. It is not its status. Status is the
-- run's lifecycle, and decides what may happen to the run next; nothing
-- branches on the step. It is written when a step starts and kept when the
-- run ends, so a failed run still says where it failed. '' is a run that has
-- not started a step.
--
-- The list must stay equal to domain.AllTemplateRunSteps.
alter table template_runs
	add column step text not null default ''
	constraint template_runs_step_check check (
		step in (
			'',
			'waiting_for_executor',
			'preparing_workspace',
			'fetching_source',
			'restoring_plan',
			'initializing',
			'selecting_workspace',
			'planning',
			'saving_plan',
			'applying'
		)
	);

-- running is the state a run is in while a workflow works on it. It joins the
-- old statuses here; 0027 removes those once nothing writes them.
alter table template_runs
	drop constraint template_runs_status_check;

alter table template_runs
	add constraint template_runs_status_check check (
		status in (
			'queued',
			'running',
			'locked',
			'workspace_prepared',
			'source_fetched',
			'workspace_selected',
			'waiting_approval',
			'approved',
			'canceled',
			'lock_released',
			'completed',
			'failed',
			'init_started',
			'init_finished',
			'plan_started',
			'plan_finished',
			'apply_started',
			'apply_finished',
			'destroy_started',
			'destroy_finished'
		)
	);
```

- [ ] **Step 4: Write the failing store tests**

Create `internal/postgres/run_progress_test.go`:

```go
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
```

`templateRunAt(stackTemplateID, runID, status)` already exists in `store_test.go:1738`. It builds a `tenant_123` plan run. The event tests override `Operation` where they need to.

Append to `internal/postgres/saved_plans_test.go`:

```go
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
```

- [ ] **Step 5: Run the tests to verify they fail**

Run: `tflive_POSTGRES_TEST_DSN='postgres://tflive:tflive@localhost:55432/tflive_test?sslmode=disable' go test ./internal/postgres/ -run 'TestRecordTemplateRunStep|TestRecordTemplateRunEvent|TestBeginTemplateApplyClaimIsIdempotent' -count=1`
Expected: a build failure, because `store.RecordTemplateRunStep` and `store.RecordTemplateRunEvent` are undefined.

- [ ] **Step 6: Refactor the stack-template helpers to take plain identifiers**

In `internal/postgres/repositories.go`, change the three helpers so that both the status path and the event path can call them. Replace `recordStackTemplateLastApplied`, `recordStackTemplateLifecycle` and `recordInterruptedDestroyLifecycle` with:

```go
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
```

Update the four call sites in `RecordTemplateRunStatus` (repositories.go ~1330-1348):

```go
		case recordsStackTemplateLastApplied(input):
			if err := recordStackTemplateLastApplied(ctx, tx, input.TenantID, input.StackTemplateID, input.RunID, input.Status); err != nil {
				return err
			}
		case recordsStackTemplateDestroying(input):
			if err := recordStackTemplateLifecycle(ctx, tx, input.TenantID, input.StackTemplateID, domain.StackTemplateDestroying); err != nil {
				return err
			}
		case recordsStackTemplateDestroyed(input):
			if err := recordStackTemplateLifecycle(ctx, tx, input.TenantID, input.StackTemplateID, domain.StackTemplateDestroyed); err != nil {
				return err
			}
		case recordsStackTemplateDestroyInterrupted(input):
			if err := recordInterruptedDestroyLifecycle(ctx, tx, input.TenantID, input.StackTemplateID); err != nil {
				return err
			}
```

- [ ] **Step 7: Implement the step and event writes**

Create `internal/postgres/run_progress.go`:

```go
package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/vishu42/tflive/internal/domain"
)

// RecordTemplateRunStep records the step a running run has started. Only a
// running run has a step to start, so any other run is not found.
func (store *Store) RecordTemplateRunStep(ctx context.Context, input domain.TemplateRunStepActivityInput) error {
	if !input.Step.Valid() {
		return fmt.Errorf("record template run step: unknown step %q", input.Step)
	}
	commandTag, err := store.pool.Exec(ctx, `
		update template_runs
		set step = $1
		where tenant_id = $2
			and id = $3
			and status = $4
	`, input.Step, input.TenantID, input.RunID, domain.TemplateRunRunning)
	if err != nil {
		return fmt.Errorf("record template run step: %w", err)
	}
	if commandTag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// templateRunEventOperations is the one kind of run each event belongs to.
var templateRunEventOperations = map[domain.TemplateRunEvent]domain.OperationType{
	domain.TemplateRunApplied:    domain.OperationApply,
	domain.TemplateRunDestroying: domain.OperationDestroy,
	domain.TemplateRunDestroyed:  domain.OperationDestroy,
}

// RecordTemplateRunEvent records something a running run did to its stack
// template, together with any counts the event carries, in one transaction
// with the run row locked. A run that is not running, or is not this stack
// template's run of this operation, is not found.
func (store *Store) RecordTemplateRunEvent(ctx context.Context, input domain.TemplateRunEventActivityInput) error {
	operation, ok := templateRunEventOperations[input.Event]
	if !ok {
		return fmt.Errorf("record template run event: unknown event %q", input.Event)
	}
	if input.Operation != operation {
		return fmt.Errorf("record template run event: %q is not an event of a %s run", input.Event, input.Operation)
	}

	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin record template run event: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var runID domain.TemplateRunID
	err = tx.QueryRow(ctx, `
		select id
		from template_runs
		where tenant_id = $1
			and id = $2
			and stack_template_id = $3
			and operation = $4
			and status = $5
		for update
	`, input.TenantID, input.RunID, input.StackTemplateID, input.Operation, domain.TemplateRunRunning).Scan(&runID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("read template run for event: %w", err)
	}

	if input.Summary != nil {
		if _, err := tx.Exec(ctx, `
			update template_runs
			set plan_add = $1, plan_change = $2, plan_destroy = $3
			where tenant_id = $4 and id = $5
		`, input.Summary.Add, input.Summary.Change, input.Summary.Destroy, input.TenantID, input.RunID); err != nil {
			return fmt.Errorf("record template run event counts: %w", err)
		}
	}

	switch input.Event {
	case domain.TemplateRunApplied:
		err = recordStackTemplateLastApplied(ctx, tx, input.TenantID, input.StackTemplateID, input.RunID, domain.TemplateRunRunning)
	case domain.TemplateRunDestroying:
		err = recordStackTemplateLifecycle(ctx, tx, input.TenantID, input.StackTemplateID, domain.StackTemplateDestroying)
	case domain.TemplateRunDestroyed:
		err = recordStackTemplateLifecycle(ctx, tx, input.TenantID, input.StackTemplateID, domain.StackTemplateDestroyed)
	}
	if err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit record template run event: %w", err)
	}
	return nil
}
```

- [ ] **Step 8: Read the step back, and claim into running**

In `internal/postgres/repositories.go`, in both `GetTemplateRun` and `ListTemplateRuns`:
- Add `step,` to the select list directly after `status,`.
- Add `&run.Step,` to the `Scan` arguments directly after `&run.Status,`.

In `internal/postgres/saved_plans.go`, replace `BeginTemplateApply` with:

```go
// BeginTemplateApply claims a run for its apply phase by moving it to running:
// from approved, or, for an auto-approved apply run that never had a plan to
// approve, from queued. Losing the claim means the plan was discarded first;
// the conditional update is what makes that race safe, because
// discardTemplateRun makes the same kind of update from the other side.
//
// A run already running is this claim retried after its acknowledgement was
// lost. Nothing else moves an approved or auto-approved run to running, and
// the plan workflow of an approved run has already ended, so it reports the
// claim rather than a lost race.
func (store *Store) BeginTemplateApply(ctx context.Context, tenantID domain.TenantID, runID domain.TemplateRunID, autoApprove bool) (bool, error) {
	from := domain.TemplateRunApproved
	if autoApprove {
		from = domain.TemplateRunQueued
	}
	commandTag, err := store.pool.Exec(ctx, `
		update template_runs
		set status = $1
		where tenant_id = $2 and id = $3 and status in ($4, $1) and auto_approve = $5
	`, domain.TemplateRunRunning, tenantID, runID, from, autoApprove)
	if err != nil {
		return false, fmt.Errorf("claim run for apply: %w", err)
	}
	return commandTag.RowsAffected() == 1, nil
}
```

Update the two existing claim assertions in `saved_plans_test.go` (lines ~234 and ~303) from `domain.TemplateRunLocked` to `domain.TemplateRunRunning`.

Update the `BeginTemplateApply` doc comment in `internal/activities/control.go` (`PlanRecorder`) so it no longer says "locked".

- [ ] **Step 9: Document the field in the API**

In `docs/openapi.yaml`, in the `TemplateRun` schema, add `step` directly after `status`:

```yaml
        status: { $ref: "#/components/schemas/TemplateRunStatus" }
        step: { $ref: "#/components/schemas/TemplateRunStep" }
```

Add `step` to that schema's `required` list, directly after `status`. Then add the new schema directly after `TemplateRunStatus`:

```yaml
    TemplateRunStep:
      type: string
      description: |
        What a running run is doing right now, or, once it ended, what it was
        doing last, so a failed run says where it failed. Empty until the run
        starts its first step. It is for people: nothing about what a run may
        do next depends on it.
      enum:
        - ""
        - waiting_for_executor
        - preparing_workspace
        - fetching_source
        - restoring_plan
        - initializing
        - selecting_workspace
        - planning
        - saving_plan
        - applying
```

- [ ] **Step 10: Run the tests to verify they pass**

Run: `gofmt -w internal/domain internal/postgres internal/activities && go build ./... && tflive_POSTGRES_TEST_DSN='postgres://tflive:tflive@localhost:55432/tflive_test?sslmode=disable' go test ./internal/postgres/ ./internal/domain/ -count=1`
Expected: PASS. That includes the in-flight index test, which walks `AllTemplateRunStatuses`, so `running` is now covered too.

- [ ] **Step 11: Commit**

```bash
git add internal/domain internal/postgres internal/activities/control.go docs/openapi.yaml
git commit -m "feat: record a run's step and its stack template events

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Control activities for steps and events

**Files:**
- Modify: `internal/activities/control.go`, `internal/activities/template_run.go:24-27`, `internal/activities/template_run_test.go` (`controlStoreStub`, ~line 970)
- Modify: `cmd/api/main.go:262-290`, `cmd/api/main_test.go` (activity-name set ~line 258; `recordingStore` ~line 1000)

**Interfaces:**
- **Consumes:** Task 1's store methods and activity inputs.
- **Produces:**
  - `(*ControlActivities) RecordTemplateRunStep(ctx, domain.TemplateRunStepActivityInput) error`
  - `(*ControlActivities) RecordTemplateRunEvent(ctx, domain.TemplateRunEventActivityInput) error`
  - Both are registered on the control worker under `domain.RecordTemplateRunStepActivityName` and `domain.RecordTemplateRunEventActivityName`.

- [ ] **Step 1: Write the failing tests**

Append to `internal/activities/template_run_test.go`:

```go
func TestRecordTemplateRunStepDelegatesToTheStore(t *testing.T) {
	t.Parallel()

	store := &controlStoreStub{}
	input := domain.TemplateRunStepActivityInput{TenantID: "tenant_123", RunID: "run_123", Step: domain.TemplateRunStepFetchingSource}

	if err := NewControlActivities(store, nil).RecordTemplateRunStep(context.Background(), input); err != nil {
		t.Fatalf("RecordTemplateRunStep returned error: %v", err)
	}
	if store.step != input {
		t.Fatalf("recorded step = %#v, want %#v", store.step, input)
	}
}

func TestRecordTemplateRunEventDelegatesToTheStore(t *testing.T) {
	t.Parallel()

	storeErr := errors.New("database unavailable")
	store := &controlStoreStub{eventErr: storeErr}
	input := domain.TemplateRunEventActivityInput{
		TenantID: "tenant_123", RunID: "run_123", StackTemplateID: "stack_template_123",
		Operation: domain.OperationApply, Event: domain.TemplateRunApplied,
	}

	err := NewControlActivities(store, nil).RecordTemplateRunEvent(context.Background(), input)
	if !errors.Is(err, storeErr) || !strings.Contains(err.Error(), "record template run event") {
		t.Fatalf("error = %v, want the store's error with event context", err)
	}
	if !reflect.DeepEqual(store.event, input) {
		t.Fatalf("recorded event = %#v, want %#v", store.event, input)
	}
}
```

Add the fields and methods to `controlStoreStub`:

```go
	step     domain.TemplateRunStepActivityInput
	event    domain.TemplateRunEventActivityInput
	eventErr error
```

```go
func (store *controlStoreStub) RecordTemplateRunStep(_ context.Context, input domain.TemplateRunStepActivityInput) error {
	store.step = input
	return nil
}

func (store *controlStoreStub) RecordTemplateRunEvent(_ context.Context, input domain.TemplateRunEventActivityInput) error {
	store.event = input
	return store.eventErr
}
```

In `cmd/api/main_test.go`:
- Add `domain.RecordTemplateRunStepActivityName: true,` and `domain.RecordTemplateRunEventActivityName: true,` to the expected activity-name set beside `domain.BeginApplyActivityName` (~line 258).
- Add these methods to `recordingStore`:

```go
func (recordingStore) RecordTemplateRunStep(context.Context, domain.TemplateRunStepActivityInput) error {
	return nil
}

func (recordingStore) RecordTemplateRunEvent(context.Context, domain.TemplateRunEventActivityInput) error {
	return nil
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/activities/ ./cmd/api/ -count=1`
Expected: a build failure, because `RecordTemplateRunStep` and `RecordTemplateRunEvent` are undefined on `*ControlActivities`.

- [ ] **Step 3: Implement**

In `internal/activities/template_run.go`, extend `StatusRecorder`:

```go
// StatusRecorder persists what a run's workflow reports: its lifecycle
// status, the step it is on, and what it did to its stack template.
//
// Only the workflow decides these, and each method is called by exactly one
// control activity: the one that is that write. No other activity records a
// status, step or event on its way through. A step spans several activities,
// which only the workflow sees, and a write made from inside an activity can
// land out of order when that activity is retried.
type StatusRecorder interface {
	// RecordTemplateRunStatus persists one workflow status transition for a run.
	RecordTemplateRunStatus(context.Context, domain.TemplateRunStatusActivityInput) error
	// RecordTemplateRunStep persists the step a running run has started.
	RecordTemplateRunStep(context.Context, domain.TemplateRunStepActivityInput) error
	// RecordTemplateRunEvent persists something a running run did to its
	// stack template.
	RecordTemplateRunEvent(context.Context, domain.TemplateRunEventActivityInput) error
}
```

In `internal/activities/control.go`, add these after `RecordTemplateRunStatus`:

```go
// RecordTemplateRunStep records the step a running run has started.
func (activities *ControlActivities) RecordTemplateRunStep(ctx context.Context, input domain.TemplateRunStepActivityInput) error {
	if err := activities.store.RecordTemplateRunStep(ctx, input); err != nil {
		return fmt.Errorf("record template run step: %w", err)
	}
	return nil
}

// RecordTemplateRunEvent records something a running run did to its stack
// template: an apply that is now live, or a destroy starting or done.
func (activities *ControlActivities) RecordTemplateRunEvent(ctx context.Context, input domain.TemplateRunEventActivityInput) error {
	if err := activities.store.RecordTemplateRunEvent(ctx, input); err != nil {
		return fmt.Errorf("record template run event: %w", err)
	}
	return nil
}
```

In `cmd/api/main.go` `registerControl`, register them after `RecordTemplateRunStatus`:

```go
	worker.RegisterActivityWithOptions(control.RecordTemplateRunStep, activity.RegisterOptions{
		Name: domain.RecordTemplateRunStepActivityName,
	})
	worker.RegisterActivityWithOptions(control.RecordTemplateRunEvent, activity.RegisterOptions{
		Name: domain.RecordTemplateRunEventActivityName,
	})
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `gofmt -w internal/activities cmd/api && go test ./internal/activities/ ./cmd/api/ -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/activities cmd/api
git commit -m "feat: control activities for a run's step and events

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: The workflow records steps and events, not progress statuses

**Files:**
- Modify: `internal/workflows/template_run.go` (`templateRunWorkflow` struct ~72-90, `planPhase` 137-198, `applyPhase` 206-257, `withSession` ~275, `prepareWorkspace` 298-333, `complete` ~469, the status table and `runTerraform` 500-630, and the status helpers 657-691)
- Test: `internal/workflows/template_run_workflow_test.go`

**Interfaces:**
- **Consumes:** the Task 1 activity names and inputs, and `domain.TemplateRunRunning`.
- **Produces:** status writes are now only `running` (plan phase), `completed` and `failed`. Every other write is a step or an event, in this order:
  - **Plan phase:** `status:running`, `step:waiting_for_executor`, `step:preparing_workspace`, `step:fetching_source`, `step:initializing`, `step:selecting_workspace`, `step:planning`. Then `step:saving_plan` for a plan with changes that is kept, and `event:destroyed` for a destroy plan with no changes. It ends with `status:completed` unless the run waits for approval.
  - **Apply phase, approved run:** after `BeginApply`, `step:waiting_for_executor`, `step:preparing_workspace`, `step:fetching_source`, `step:restoring_plan`, `step:initializing`, `step:selecting_workspace`. Then:
    - apply: `step:applying`, `event:applied`
    - destroy: `event:destroying`, `step:applying`, `event:destroyed`

    It ends with `status:completed`.
  - **Apply phase, auto-approved run:** the same, with no `step:restoring_plan`. It ends `step:applying`, `event:applied` (carrying the apply's counts), `status:completed`.

- [ ] **Step 1: Add test helpers and default registrations**

In `newTemplateRunWorkflowTestEnvironment`, register the two new activities next to the `RecordTemplateRunStatus` default:

```go
	env.RegisterActivityWithOptions(
		func(context.Context, domain.TemplateRunStepActivityInput) error {
			return nil
		},
		activity.RegisterOptions{Name: domain.RecordTemplateRunStepActivityName},
	)
	env.RegisterActivityWithOptions(
		func(context.Context, domain.TemplateRunEventActivityInput) error {
			return nil
		},
		activity.RegisterOptions{Name: domain.RecordTemplateRunEventActivityName},
	)
```

Add this helper after `mockRunTerraform`:

```go
// mockRunWrites mocks the three control-plane writes a run makes (its status,
// its step, and its stack template events) and appends each to writes, in
// order, as "status:<status>", "step:<step>" or "event:<event>".
func mockRunWrites(env *testsuite.TestWorkflowEnvironment, writes *[]string) {
	env.OnActivity(domain.RecordTemplateRunStatusActivityName, mock.Anything, mock.Anything).
		Return(func(_ context.Context, input domain.TemplateRunStatusActivityInput) error {
			*writes = append(*writes, "status:"+string(input.Status))
			return nil
		})
	env.OnActivity(domain.RecordTemplateRunStepActivityName, mock.Anything, mock.Anything).
		Return(func(_ context.Context, input domain.TemplateRunStepActivityInput) error {
			*writes = append(*writes, "step:"+string(input.Step))
			return nil
		})
	env.OnActivity(domain.RecordTemplateRunEventActivityName, mock.Anything, mock.Anything).
		Return(func(_ context.Context, input domain.TemplateRunEventActivityInput) error {
			*writes = append(*writes, "event:"+string(input.Event))
			return nil
		})
}

// setupWrites is what every phase records while it readies its workspace,
// with the plan restored in between for an approved apply.
func setupWrites(restoresPlan bool) []string {
	writes := []string{"step:waiting_for_executor", "step:preparing_workspace", "step:fetching_source"}
	if restoresPlan {
		writes = append(writes, "step:restoring_plan")
	}
	return append(writes, "step:initializing", "step:selecting_workspace")
}
```

- [ ] **Step 2: Rewrite the timeline tests against the new writes**

Make each of the following edits in `template_run_workflow_test.go`. All of them use `mockRunWrites` and `setupWrites` from Step 1.

**`TestTemplatePlanWorkflowRecordsPlanStatuses`** (rename it to `TestTemplatePlanWorkflowRecordsPlanSteps`):
- Delete its `RecordTemplateRunStatus` mock and call `mockRunWrites(env, &events)` in its place.
- Replace `want` with:

```go
	want := []string{
		"status:running",
		"step:waiting_for_executor",
		"step:preparing_workspace",
		"prepare_workspace",
		"step:fetching_source",
		"fetch_source",
		"step:initializing",
		"terraform:" + string(domain.TerraformCommandInit),
		"step:selecting_workspace",
		"terraform:" + string(domain.TerraformCommandSelectWorkspace),
		"step:planning",
		"terraform:" + string(domain.TerraformCommandPlan),
		"status:completed",
	}
```

**`TestTemplatePlanWorkflowRecordsCommandLogsOnControlQueue`:**
- Replace its status mock body so it appends only `"status:"+string(input.Status)` when `input.Status == domain.TemplateRunCompleted`.
- Change the last `want` element from `string(domain.TemplateRunPlanFinished)` to `"status:completed"`.
- Update the comment above the test so it reads "after the command, before the run completes".

**`TestTemplatePlanWorkflowSavesAPlanWithChangesAndEnds`:**
- Replace `var statuses []domain.TemplateRunStatus` and its mock with `var writes []string` and `mockRunWrites(env, &writes)`.
- Replace `wantStatuses` and its assertion with:

```go
	wantWrites := append([]string{"status:running"}, setupWrites(false)...)
	wantWrites = append(wantWrites, "step:planning", "step:saving_plan")
	if !reflect.DeepEqual(writes, wantWrites) {
		t.Fatalf("writes = %#v, want %#v", writes, wantWrites)
	}
```

**`TestTemplateApplyWorkflowAppliesTheSavedPlan`:**
- Replace the `started`/`finished` table fields with `applyWrites []string`:
  - `{name: "apply run", ..., applyWrites: []string{"step:applying", "event:applied"}}`
  - `{name: "destroy run", ..., applyWrites: []string{"event:destroying", "step:applying", "event:destroyed"}}`
- Use `mockRunWrites(env, &writes)` in place of the status mock.
- Replace `wantStatuses` and its assertion with:

```go
			wantWrites := append(setupWrites(true), testCase.applyWrites...)
			wantWrites = append(wantWrites, "status:completed")
			if !reflect.DeepEqual(writes, wantWrites) {
				t.Fatalf("writes = %#v, want %#v", writes, wantWrites)
			}
```

- Replace the comment above the test with: "The apply workflow puts the saved plan back before init, applies it, and deletes it along with the workspace. It records its setup steps again, because they are what it is doing, and runs them in the apply phase so their logs do not replace the plan phase's."

**`TestTemplatePlanWorkflowPlanRunKeepsNoPlan`:** use `mockRunWrites`, then replace the tail check with:

```go
	tail := writes[len(writes)-2:]
	if want := []string{"step:planning", "status:completed"}; !reflect.DeepEqual(tail, want) {
		t.Fatalf("final writes = %#v, want %#v", tail, want)
	}
```

**`TestTemplateApplyWorkflowAppliesAnAutoApprovedRunWithoutAPlan`:**
- Delete `var statuses` and the status mock, and add `var writes []string`.
- Don't call `mockRunWrites` here. Testify uses the first matching `On` registration, so a later event mock would never be reached. Register the three writes inline instead, with the event mock capturing the counts:

```go
	env.OnActivity(domain.RecordTemplateRunStatusActivityName, mock.Anything, mock.Anything).
		Return(func(_ context.Context, input domain.TemplateRunStatusActivityInput) error {
			writes = append(writes, "status:"+string(input.Status))
			return nil
		})
	env.OnActivity(domain.RecordTemplateRunStepActivityName, mock.Anything, mock.Anything).
		Return(func(_ context.Context, input domain.TemplateRunStepActivityInput) error {
			writes = append(writes, "step:"+string(input.Step))
			return nil
		})
	env.OnActivity(domain.RecordTemplateRunEventActivityName, mock.Anything, mock.Anything).
		Return(func(_ context.Context, input domain.TemplateRunEventActivityInput) error {
			writes = append(writes, "event:"+string(input.Event))
			finished = input.Summary
			return nil
		})
```

Replace `wantStatuses` and its assertion with:

```go
	wantWrites := append(setupWrites(false), "step:applying", "event:applied", "status:completed")
	if !reflect.DeepEqual(writes, wantWrites) {
		t.Fatalf("writes = %#v, want %#v", writes, wantWrites)
	}
	if finished == nil || *finished != (domain.PlanSummary{Add: 2, Destroy: 1}) {
		t.Fatalf("applied summary = %#v, want the apply's counts", finished)
	}
```

Update the comment above the test so it reads "records its setup steps, applies without a saved plan, and records the counts the apply reported with applied".

**`TestTemplatePlanWorkflowFinishesADestroyWithNothingToDestroy`:** use `mockRunWrites`, then replace the tail check with:

```go
	tail := writes[len(writes)-3:]
	if want := []string{"step:planning", "event:destroyed", "status:completed"}; !reflect.DeepEqual(tail, want) {
		t.Fatalf("final writes = %#v, want %#v", tail, want)
	}
```

Update its comment: "recording destroyed is what moves the stack template to destroyed."

**`TestTemplatePlanWorkflowRoutesActivitiesByPlane`:** after the status-queue loop, add:

```go
	for _, name := range []string{domain.RecordTemplateRunStepActivityName} {
		for _, queue := range queues[name] {
			if queue != domain.ControlTaskQueue {
				t.Fatalf("%s queues = %#v, want all %q", name, queues[name], domain.ControlTaskQueue)
			}
		}
		if len(queues[name]) == 0 {
			t.Fatalf("no %s ran", name)
		}
	}
```

**`TestTemplatePlanWorkflowRejectsUnsupportedOperation`:** in the comment, change "never claims a lock it would then have to release" to "never starts running".

Don't change any other test. The retry, failure and log-identity tests only check `failed`, which is unchanged.

- [ ] **Step 3: Run the workflow tests to verify they fail**

Run: `go test ./internal/workflows/ -count=1`
Expected: FAIL. The rewritten tests get `status:locked`, `status:workspace_prepared` and so on in place of the step writes.

- [ ] **Step 4: Rewrite the workflow**

In `internal/workflows/template_run.go`, make the following changes.

**The struct:** delete the `setupRecorded` field and its comment.

**`planPhase`:**
- After `validateOperation`, add:

```go
	if err := run.recordStatus(domain.TemplateRunRunning); err != nil {
		return err
	}
```

- Inside the session closure, directly before `sealedPlanKey, err := run.sealPlanKey(true)`, add:

```go
		if err := run.recordStep(domain.TemplateRunStepSavingPlan); err != nil {
			return err
		}
```

- In the `PlanOutcomeNoChanges` branch, replace `run.recordStatus(domain.TemplateRunDestroyFinished)` with `run.recordEvent(domain.TemplateRunDestroyed, nil)`, and update its comment to "recording it is what moves the stack template to destroyed."

**`applyPhase`:**
- Delete the line `run.setupRecorded = !run.input.AutoApprove`.
- Replace the closure's tail (`_, err := run.runTerraform(command); return err`) with `return run.apply(command)`.
- Update the doc comment: "It begins by claiming the run: approved (or, auto-approved, queued) becomes running, or nothing happens because the plan was discarded first."

Add this method after `applyPhase`:

```go
// apply runs the apply phase's command, recording what it does to the stack
// template around it: a destroy marks the template destroying before it starts
// and destroyed once it succeeds, and an apply records what it applied as
// live, with the counts an auto-approved apply reports, since it had no plan
// to count.
func (run *templateRunWorkflow) apply(command domain.TerraformCommandType) error {
	destroy := run.input.Operation == domain.OperationDestroy
	if destroy {
		if err := run.recordEvent(domain.TemplateRunDestroying, nil); err != nil {
			return err
		}
	}
	output, err := run.runTerraform(command)
	if err != nil {
		return err
	}
	if destroy {
		return run.recordEvent(domain.TemplateRunDestroyed, nil)
	}
	var summary *domain.PlanSummary
	if run.input.AutoApprove {
		summary = &output.Summary
	}
	return run.recordEvent(domain.TemplateRunApplied, summary)
}
```

**`withSession`:** make its first statement:

```go
	if err := run.recordStep(domain.TemplateRunStepWaitingForExecutor); err != nil {
		return err
	}
```

Add this sentence to its doc comment: "Waiting for a session is a step of its own: an approved apply can wait minutes for a free executor."

**`prepareWorkspace`:** replace it and delete `recordSetupStatus`:

```go
// prepareWorkspace readies a workspace for Terraform: a directory and a key on
// the executor, the source at the run's commit, then init and workspace
// selection. restorePlan, when set, runs once the source is in place; the
// apply phase uses it to put the saved plan and its lock file back, so init
// installs the providers the plan was made with.
func (run *templateRunWorkflow) prepareWorkspace(restorePlan func() error) error {
	if err := run.recordStep(domain.TemplateRunStepPreparingWorkspace); err != nil {
		return err
	}
	if err := run.prepareLocalWorkspace(); err != nil {
		return err
	}
	if err := run.recordStep(domain.TemplateRunStepFetchingSource); err != nil {
		return err
	}
	if err := run.fetchSource(); err != nil {
		return err
	}
	if restorePlan != nil {
		if err := run.recordStep(domain.TemplateRunStepRestoringPlan); err != nil {
			return err
		}
		if err := restorePlan(); err != nil {
			return err
		}
	}
	if _, err := run.runTerraform(domain.TerraformCommandInit); err != nil {
		return err
	}
	_, err := run.runTerraform(domain.TerraformCommandSelectWorkspace)
	return err
}
```

**`complete`:** replace it with:

```go
func (run *templateRunWorkflow) complete() error {
	return run.recordStatus(domain.TemplateRunCompleted)
}
```

**The status table and `runTerraform`:**
- Delete `terraformCommandStatuses` and `terraformCommandStatusTable`, and replace them with:

```go
// terraformCommandSteps is the step each Terraform command is. Planning a
// destroy is planning, and applying one is applying: the run's operation
// already says it is a destroy.
var terraformCommandSteps = map[domain.TerraformCommandType]domain.TemplateRunStep{
	domain.TerraformCommandInit:             domain.TemplateRunStepInitializing,
	domain.TerraformCommandSelectWorkspace:  domain.TemplateRunStepSelectingWorkspace,
	domain.TerraformCommandPlan:             domain.TemplateRunStepPlanning,
	domain.TerraformCommandPlanDestroy:      domain.TemplateRunStepPlanning,
	domain.TerraformCommandApply:            domain.TemplateRunStepApplying,
	domain.TerraformCommandDestroy:          domain.TemplateRunStepApplying,
	domain.TerraformCommandApplyAutoApprove: domain.TemplateRunStepApplying,
}
```

- In `runTerraform`, replace everything from `statuses := terraformCommandStatusTable[command]` through the `if statuses.before != ""` block with:

```go
	runPhase := domain.RunPhasePlan
	if run.applying {
		runPhase = domain.RunPhaseApply
	}
	if err := run.recordStep(terraformCommandSteps[command]); err != nil {
		return domain.RunTerraformActivityOutput{}, err
	}
```

- Delete the `if statuses.after != ""` block at the end, so the function ends with `return output, nil` after `recordLog`.
- Replace its doc comment with: "runTerraform executes one Terraform command as a step of its own, recording the step before it starts and the command's log after it ends."

**The status helpers:** delete `statusInput` and `recordStatusInput`, and replace `recordStatusWithSummary` with:

```go
func (run *templateRunWorkflow) recordStatusWithSummary(status domain.TemplateRunStatus, errorSummary string) error {
	return workflow.ExecuteActivity(
		run.ctx,
		domain.RecordTemplateRunStatusActivityName,
		domain.TemplateRunStatusActivityInput{
			RunID:           run.input.RunID,
			TenantID:        run.input.TenantID,
			StackTemplateID: run.input.StackTemplateID,
			Operation:       run.input.Operation,
			Status:          status,
			ErrorSummary:    errorSummary,
		},
	).Get(run.ctx, nil)
}

// recordStep records the step the run is starting, for the people watching
// it. It is written before the work, never after, so a slow step shows as
// itself rather than as the step before it.
func (run *templateRunWorkflow) recordStep(step domain.TemplateRunStep) error {
	return workflow.ExecuteActivity(
		run.ctx,
		domain.RecordTemplateRunStepActivityName,
		domain.TemplateRunStepActivityInput{
			RunID:    run.input.RunID,
			TenantID: run.input.TenantID,
			Step:     step,
		},
	).Get(run.ctx, nil)
}

// recordEvent records something the run did to its stack template, with the
// counts it carries, if any.
func (run *templateRunWorkflow) recordEvent(event domain.TemplateRunEvent, summary *domain.PlanSummary) error {
	return workflow.ExecuteActivity(
		run.ctx,
		domain.RecordTemplateRunEventActivityName,
		domain.TemplateRunEventActivityInput{
			RunID:           run.input.RunID,
			TenantID:        run.input.TenantID,
			StackTemplateID: run.input.StackTemplateID,
			Operation:       run.input.Operation,
			Event:           event,
			Summary:         summary,
		},
	).Get(run.ctx, nil)
}
```

Update the comment on `newTemplateRunWorkflow` so it reads "control-plane work (status, step and event writes)".

- [ ] **Step 5: Run the tests to verify they pass**

Run: `gofmt -w internal/workflows && go vet ./internal/workflows/ && go test ./internal/workflows/ -count=1`
Expected: PASS. Then run `rtk proxy grep -n "TemplateRunLocked\|LockReleased\|Started\b\|Finished\b\|setupRecorded" internal/workflows/template_run.go`. Expected: no matches.

- [ ] **Step 6: Commit**

```bash
git add internal/workflows
git commit -m "refactor: the run workflow records steps and events, not progress statuses

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: Status is lifecycle only

**Files:**
- Modify: `internal/domain/template_run.go` (the status constants and `AllTemplateRunStatuses`), `internal/domain/workflow.go` (`TemplateRunStatusActivityInput`)
- Create: `internal/postgres/migrations/0027_run_status_is_lifecycle.sql`
- Modify: `internal/postgres/repositories.go` (`RecordTemplateRunStatus` and `recordTemplateRunStatus` at ~1317-1470; delete the `recordsStackTemplate*` predicates)
- Test: `internal/postgres/store_test.go`, `internal/postgres/saved_plans_test.go`, `internal/activities/template_run_test.go:32`, `internal/app/service_test.go:983,1648`
- Modify: `docs/openapi.yaml` (the `TemplateRunStatus` schema), `docs/architecture.md:588-650`, `docs/apply-run-sequence.md`

**Interfaces:**
- **Consumes:** Task 3. After it, nothing writes the old statuses.
- **Produces:**
  - `AllTemplateRunStatuses` is exactly the 7 values.
  - `TemplateRunStatusActivityInput` loses `Summary`.
  - `(*Store) RecordTemplateRunStatus` accepts only `running`, `completed` and `failed`. It returns `ErrTemplateRunTransition` when the run is in a state it cannot move from, and is a no-op when the run already has that status.

- [ ] **Step 1: Write the failing store tests**

Append to `internal/postgres/run_progress_test.go`:

```go
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
```

Append to `internal/postgres/saved_plans_test.go`:

```go
// Runs 0027 finds mid-progress were in flight under a workflow that wrote the
// old statuses; they are closed out, as 0021 and 0023 did. Runs in a
// lifecycle state keep it.
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

	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate to head: %v", err)
	}
	for runID, want := range map[string]domain.TemplateRunStatus{
		"run_planning": domain.TemplateRunFailed,
		"run_locked":   domain.TemplateRunFailed,
		"run_waiting":  domain.TemplateRunWaitingApproval,
		"run_done":     domain.TemplateRunCompleted,
	} {
		if got := runStatus(t, ctx, pool, domain.TemplateRunID(runID)); got != want {
			t.Errorf("%s status = %q, want %q", runID, got, want)
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `tflive_POSTGRES_TEST_DSN='postgres://tflive:tflive@localhost:55432/tflive_test?sslmode=disable' go test ./internal/postgres/ -run 'TestRecordTemplateRunStatus|TestLifecycleStatusMigration' -count=1`
Expected: a build failure, because `ErrTemplateRunTransition` is undefined.

- [ ] **Step 3: Narrow the domain**

In `internal/domain/template_run.go`, replace the status constant block with:

```go
// TemplateRunStatus is a run's lifecycle state: the one fact about a run that
// decides what may happen to it next. It is not progress. What a running run
// is doing is its Step.
type TemplateRunStatus string

const (
	TemplateRunQueued TemplateRunStatus = "queued"
	// TemplateRunRunning is a run a workflow is working on: planning, or,
	// once claimed for its apply, applying. What it is doing right now is its
	// Step.
	TemplateRunRunning         TemplateRunStatus = "running"
	TemplateRunWaitingApproval TemplateRunStatus = "waiting_approval"
	TemplateRunApproved        TemplateRunStatus = "approved"
	TemplateRunCompleted       TemplateRunStatus = "completed"
	TemplateRunFailed          TemplateRunStatus = "failed"
	TemplateRunCanceled        TemplateRunStatus = "canceled"
)
```

Replace `AllTemplateRunStatuses`'s elements with the 7 values in that order. Keep its comment.

Update the `TerraformCommandApply` and `TerraformCommandDestroy` comment near the top: "They stay two commands because a destroy records the destroying and destroyed events around it."

Update the `TerraformCommandPlanDestroy` and `TerraformCommandApplyAutoApprove` comments to replace "records the same statuses and log as" with "is the same step and records the same log as".

In `internal/domain/workflow.go`, delete the `Summary` field and its comment from `TemplateRunStatusActivityInput`, and change that type's comment to "asks the control plane to move a run along its lifecycle."

- [ ] **Step 4: Write the migration**

Create `internal/postgres/migrations/0027_run_status_is_lifecycle.sql`:

```sql
-- A run's status is its lifecycle state and nothing else. Progress lives in
-- step (0026), and what a run does to its stack template is recorded as an
-- event rather than inferred from a status.
--
-- A run holding one of the old progress statuses was in flight under a
-- workflow that wrote them, and cannot finish under one that does not. tflive
-- is pre-production, so they are closed out, as 0021 and 0023 did.
update template_runs
set
	status = 'failed',
	error_summary = case
		when error_summary = '' then 'closed out by migration 0027: run was in flight when run statuses became lifecycle only'
		else error_summary
	end,
	completed_at = coalesce(completed_at, now())
where status not in ('queued', 'running', 'waiting_approval', 'approved', 'completed', 'failed', 'canceled');

alter table template_runs
	drop constraint template_runs_status_check;

-- Must stay equal to domain.AllTemplateRunStatuses.
alter table template_runs
	add constraint template_runs_status_check check (
		status in (
			'queued',
			'running',
			'waiting_approval',
			'approved',
			'completed',
			'failed',
			'canceled'
		)
	);
```

`template_runs_in_flight_idx` does not change, because its terminal predicate is the same three values.

- [ ] **Step 5: Guard the status write**

In `internal/postgres/repositories.go`:
- Replace `RecordTemplateRunStatus` and `recordTemplateRunStatus`.
- Delete the `templateRunStatusWriter` interface if nothing else uses it.
- Delete the four `recordsStackTemplate*` predicates.

```go
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
```

Add `"slices"` to the imports if it isn't already there.

- [ ] **Step 6: Move the existing tests onto the lifecycle**

Make these edits, then fix whatever else `go vet ./...` reports the same way.

**`internal/postgres/store_test.go`:**
- Delete these tests. Task 1's event tests now cover them:
  - `TestRecordTemplateRunStatusUpdatesStackTemplateLastAppliedForSuccessfulApply`
  - `TestRecordTemplateRunStatusRecordsCountsItCarries`
  - `TestRecordTemplateRunStatusRecordsAppliedConfigAlongsideRevision`
  - `TestRecordTemplateRunStatusSetsStackTemplateLifecycleToDestroying`
  - `TestRecordTemplateRunStatusSetsStackTemplateLifecycleToDestroyed`
  - `TestRecordsStackTemplateDestroyInterrupted`
- In `TestRecordTemplateRunStatusUpdatesTenantScopedRun`, record `domain.TemplateRunRunning` in place of `TemplateRunPlanStarted`, and assert it.
- In `TestRecordTemplateRunStatusReturnsNotFoundForOtherTenant`, record `domain.TemplateRunRunning`.
- In `TestRecordTemplateRunStatusSetsCompletedAtForTerminalStatus` (~2306), seed the run as `TemplateRunRunning`, and record `TemplateRunCompleted` in place of `TemplateRunLockReleased` (~2323). Keep its assertions about `completed_at`.
- In `TestRecordTemplateRunStatusPersistsFailureSummary` (~2355), seed `TemplateRunRunning` in place of `TemplateRunInitFinished` (~2368).
- In `TestCreateTemplateRunScopesTheInFlightGate` (~1592), replace `TemplateRunApplyStarted` with `TemplateRunRunning`.
- In `TestRecordTemplateRunStatusReconcilesInterruptedDestroyLifecycle`:
  - Delete the `"canceled after destroy started"` case, since the workflow never writes canceled.
  - Change every `initialStatus` of `TemplateRunDestroyStarted` or `TemplateRunDestroyFinished` to `TemplateRunRunning`.
  - Change `"late failure after destroy completed"` to `initialStatus: domain.TemplateRunRunning` (lifecycle destroyed, want destroyed).

**`internal/postgres/saved_plans_test.go`:**
- Replace every seeded `TemplateRunPlanStarted`, `TemplateRunPlanFinished` and `TemplateRunApplyStarted` with `TemplateRunRunning`.
- At ~163, the assertion that a plan run with changes keeps its status now expects `TemplateRunRunning`.
- Delete `TestApplyFinishedOnAPlanRunRecordsLastApplied`, which `TestRecordTemplateRunEventAppliedRecordsLastApplied` now covers.

**Other packages:**
- `internal/activities/template_run_test.go:32`: `TemplateRunPlanFinished` → `TemplateRunRunning`.
- `internal/app/service_test.go:983,1648`: `TemplateRunApplyStarted` → `TemplateRunRunning`.

- [ ] **Step 7: Update the docs**

**`docs/openapi.yaml`:** replace the `TemplateRunStatus` schema's description and enum:

```yaml
    TemplateRunStatus:
      type: string
      description: |
        A run's lifecycle state: what may happen to it next. Terminal states
        are `completed`, `failed` and `canceled`, which is how a discarded
        plan ends; anything else means the run is still in flight.

        `waiting_approval` is the one that needs a person — post to the run's
        approval endpoint to release it. What a `running` run is doing is its
        `step`.
      enum:
        - queued
        - running
        - waiting_approval
        - approved
        - completed
        - failed
        - canceled
```

**`docs/architecture.md`:** replace the three status flows and the "Failed runs transition to" block (lines ~588-643) with:

````markdown
A run's `status` is its lifecycle state; what it is doing while `running` is
its `step`, written as each step starts and kept when the run ends.

Plan run:

```text
queued → running → completed
```

Apply or destroy run:

```text
queued → running → waiting_approval → approved → running → completed
```

An auto-approved apply goes `queued → running → completed`. Any non-terminal
run can end `failed`, and a plan waiting for approval can be discarded, which
ends it `canceled`.

Steps, in order: `waiting_for_executor`, `preparing_workspace`,
`fetching_source`, `restoring_plan` (approved apply only), `initializing`,
`selecting_workspace`, then `planning` and `saving_plan` in the plan phase or
`applying` in the apply phase.

What a run does to its stack template is recorded as an event, not a status:
`applied` makes the run the template's live state, and a destroy records
`destroying` before it destroys and `destroyed` once it has.
````

Keep the paragraph that follows, but change "fails after `destroy_started`" to "fails after recording `destroying`".

**`docs/apply-run-sequence.md`:**
- Line ~101: `BeginApply: approved to locked, ...` → `BeginApply: approved to running, ...`
- Line ~130: replace the `apply_finished` mention with "the `applied` event".
- Lines ~165-167:
  - `schedule RecordTemplateRunStatus(apply_finished)` → `schedule RecordTemplateRunEvent(applied)`
  - `status apply_finished + stack template last applied, one transaction` → `stack template last applied, with the run row locked`
- Line ~187: `loop lock_released, then completed` becomes a single `RecordTemplateRunStatus(completed)` block with no `loop`. Keep the inner arrows, and keep "(completed also drops the plan key)".
- Lines ~209-211: "Writing `apply_finished` also records ... (`recordsStackTemplateLastApplied`, ...)" → "Recording the `applied` event is what records the stack template's last applied revision (`RecordTemplateRunEvent`, `internal/postgres/run_progress.go`)."
- Lines ~217, 235, 243, 251: `apply_started` → `running` (step `applying`); `apply_finished, lock_released, completed` → `applied, completed`; "after `apply_finished`" → "after the `applied` event".

- [ ] **Step 8: Run everything**

Run: `gofmt -w internal cmd && go vet ./... && tflive_POSTGRES_TEST_DSN='postgres://tflive:tflive@localhost:55432/tflive_test?sslmode=disable' go test ./... -count=1`
Expected: PASS. Then run `rtk proxy grep -rnE "TemplateRun(Locked|LockReleased|WorkspacePrepared|SourceFetched|WorkspaceSelected|InitStarted|InitFinished|PlanStarted|PlanFinished|ApplyStarted|ApplyFinished|DestroyStarted|DestroyFinished)\b" internal cmd`. Expected: no matches.

- [ ] **Step 9: Commit**

```bash
git add internal cmd docs
git commit -m "refactor: a run's status is its lifecycle only

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: Web client shows status and step

**Files:**
- Modify: `web/src/api/types.ts:15-35,152-178`, `web/src/features/runs/runStatusLabel.ts`, `web/src/shared/statusTone.ts`, `web/src/features/runs/RunDetailScreen.tsx:46-49`, `web/src/dev/StyleGuide.tsx:339-349`, `web/src/styles/primitives.css:562-566`
- Test: `web/src/features/runs/runStatusLabel.test.ts`, `web/src/shared/statusTone.test.ts`, and the `run()` factories and status literals in `RunDetailScreen.test.tsx`, `TemplateRunActions.test.tsx`, `TemplateRunHistory.test.tsx`, `TemplateDestroyPanel.test.tsx`, `features/stacks/StackTemplatePages.test.tsx` and `api/queries.test.tsx`

**Interfaces:**
- **Consumes:** the API `TemplateRun.step` from Task 1 and the 7 statuses from Task 4.
- **Produces:**
  - `type TemplateRunStep`
  - `runStatusLabel(run: Pick<TemplateRun, "operation" | "status" | "step" | "plan_summary" | "auto_approve">): string`
  - `runProgressTag(run: Pick<TemplateRun, "status" | "step">): string`

- [ ] **Step 1: Update the types**

In `web/src/api/types.ts`, replace `TemplateRunStatus` and add `TemplateRunStep`:

```ts
// A run's lifecycle state. What a running run is doing is its step.
export type TemplateRunStatus =
  | "queued"
  | "running"
  | "waiting_approval"
  | "approved"
  | "completed"
  | "failed"
  | "canceled";

// What a run is doing, or, once it ended, what it was doing last. Nothing
// about what the run may do next depends on it.
export type TemplateRunStep =
  | "waiting_for_executor"
  | "preparing_workspace"
  | "fetching_source"
  | "restoring_plan"
  | "initializing"
  | "selecting_workspace"
  | "planning"
  | "saving_plan"
  | "applying";
```

In `interface TemplateRun`, add after `status`:

```ts
  // Empty until the run starts its first step.
  step: TemplateRunStep | "";
```

In each of the five test `run()` factories listed under Files, add `step: "",` after `status: ...`.

- [ ] **Step 2: Write the failing label tests**

Replace `web/src/features/runs/runStatusLabel.test.ts` with:

```ts
import { describe, expect, it } from "vitest";
import type { TemplateRun } from "../../api/types";
import { runProgressTag, runStatusLabel } from "./runStatusLabel";

const counts = { add: 1, change: 0, destroy: 0 };

function label(
  operation: TemplateRun["operation"],
  status: TemplateRun["status"],
  planned: boolean,
  autoApprove = false,
  step: TemplateRun["step"] = ""
): string {
  return runStatusLabel({ operation, status, step, plan_summary: planned ? counts : null, auto_approve: autoApprove });
}

describe("runStatusLabel", () => {
  it.each([
    ["apply", "running", false, "Planning"],
    ["destroy", "queued", false, "Planning destroy"],
    ["apply", "waiting_approval", true, "Planned"],
    ["destroy", "waiting_approval", true, "Destroy planned"],
    ["apply", "approved", true, "Approved"],
    ["destroy", "approved", true, "Destroy approved"],
    ["apply", "running", true, "Applying"],
    ["destroy", "running", true, "Destroying"],
    ["apply", "completed", false, "No changes"],
    ["destroy", "completed", false, "Nothing to destroy"],
    ["apply", "completed", true, "Applied"],
    ["destroy", "completed", true, "Destroyed"],
    ["apply", "failed", false, "Plan failed"],
    ["destroy", "failed", false, "Destroy plan failed"],
    ["apply", "failed", true, "Apply failed"],
    ["destroy", "failed", true, "Destroy failed"],
    ["apply", "canceled", true, "Discarded"],
    ["destroy", "canceled", true, "Destroy discarded"]
  ] as const)("%s %s (planned: %s) reads %s", (operation, status, planned, expected) => {
    expect(label(operation, status, planned)).toBe(expected);
  });

  // A plan run applies nothing, so no label of its says it did, even once
  // its plan has counts.
  it.each([
    ["queued", false, "Planning"],
    ["running", true, "Planning"],
    ["completed", true, "Plan finished"],
    ["completed", false, "No changes"],
    ["failed", true, "Plan failed"]
  ] as const)("plan run %s (planned: %s) reads %s", (status, planned, expected) => {
    expect(label("plan", status, planned)).toBe(expected);
  });

  // An auto-approved apply never plans on its own, so it is applying from the
  // start, before it has any counts.
  it.each([
    ["queued", false, "Applying"],
    ["running", false, "Applying"],
    ["completed", true, "Applied"],
    ["failed", false, "Apply failed"],
    ["canceled", false, "Canceled"]
  ] as const)("auto-approved apply %s (counts: %s) reads %s", (status, planned, expected) => {
    expect(label("apply", status, planned, true)).toBe(expected);
  });

  // A running run says what it is doing; a failed one says what it was doing.
  // Planning and applying add nothing the headline does not already say.
  it.each([
    ["plan", "running", false, false, "fetching_source", "Planning · Fetching source"],
    ["apply", "running", true, false, "waiting_for_executor", "Applying · Waiting for an executor"],
    ["destroy", "running", true, false, "restoring_plan", "Destroying · Restoring saved plan"],
    ["apply", "running", false, true, "initializing", "Applying · Initializing"],
    ["apply", "running", false, false, "planning", "Planning"],
    ["apply", "running", true, false, "applying", "Applying"],
    ["apply", "failed", false, false, "fetching_source", "Plan failed while fetching source"],
    ["destroy", "failed", true, false, "waiting_for_executor", "Destroy failed while waiting for an executor"],
    ["apply", "failed", true, false, "applying", "Apply failed"],
    ["apply", "waiting_approval", true, false, "saving_plan", "Planned"],
    ["apply", "completed", true, false, "applying", "Applied"]
  ] as const)("%s %s (planned: %s, auto: %s) on %s reads %s", (operation, status, planned, autoApprove, step, expected) => {
    expect(label(operation, status, planned, autoApprove, step)).toBe(expected);
  });
});

// Logs are refetched when this tag changes. Status alone stays running for a
// whole run, so the step has to be part of it.
describe("runProgressTag", () => {
  it("changes when the step changes within one status", () => {
    expect(runProgressTag({ status: "running", step: "initializing" })).not.toBe(
      runProgressTag({ status: "running", step: "planning" })
    );
  });

  it("changes when the status changes on the same step", () => {
    expect(runProgressTag({ status: "running", step: "applying" })).not.toBe(
      runProgressTag({ status: "completed", step: "applying" })
    );
  });
});
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `cd web && npx vitest run src/features/runs/runStatusLabel.test.ts`
Expected: FAIL. `runProgressTag` is not exported, and the step cases read without their step.

- [ ] **Step 4: Implement the label and the tag**

In `web/src/features/runs/runStatusLabel.ts`:
- Change the import to `import type { TemplateRun, TemplateRunStep } from "../../api/types";`.
- Rename the existing exported `runStatusLabel` to `function runHeadline` (not exported). Keep its body and its `Pick` type.
- Add:

```ts
const STEP_LABELS: Record<TemplateRunStep, string> = {
  waiting_for_executor: "Waiting for an executor",
  preparing_workspace: "Preparing workspace",
  fetching_source: "Fetching source",
  restoring_plan: "Restoring saved plan",
  initializing: "Initializing",
  selecting_workspace: "Selecting workspace",
  planning: "Planning",
  saving_plan: "Saving plan",
  applying: "Applying"
};

// Steps the headline already names: "Planning · Planning" says nothing.
const HEADLINE_STEPS = new Set<TemplateRunStep>(["planning", "applying"]);

// runStatusLabel says where a run is, in words. The headline comes from the
// status with the operation folded in, so no separate Type is needed:
// "Destroy planned" rather than destroy + waiting_approval. A running run
// adds the step it is on, and a failed one the step it failed on, so a slow
// clone reads as a clone and a failed one says so.
export function runStatusLabel(
  run: Pick<TemplateRun, "operation" | "status" | "step" | "plan_summary" | "auto_approve">
): string {
  const headline = runHeadline(run);
  if (run.step === "" || HEADLINE_STEPS.has(run.step)) {
    return headline;
  }
  const step = STEP_LABELS[run.step];
  if (run.status === "running") {
    return `${headline} · ${step}`;
  }
  if (run.status === "failed") {
    return `${headline} while ${step.charAt(0).toLowerCase()}${step.slice(1)}`;
  }
  return headline;
}

// runProgressTag changes whenever a run moves: a new status, or a new step
// within one. Queries that must refresh as a run progresses, such as its
// logs, key on it.
export function runProgressTag(run: Pick<TemplateRun, "status" | "step">): string {
  return `${run.status}:${run.step}`;
}
```

Move the original top-of-file comment's still-true parts into `runHeadline`'s comment: the notes about discarded runs, plan runs and auto-approved runs.

In `web/src/features/runs/RunDetailScreen.tsx`, import `runProgressTag` beside `runStatusLabel`, then change lines 46 and 49:

```tsx
  const progressTag = run ? runProgressTag(run) : "";
  const logsQuery = useTemplateRunLogsQuery(tenantID, runId, progressTag);
```

```tsx
  const logQuery = useTemplateRunLogQuery(tenantID, runId, selectedPhase, progressTag);
```

Declare `progressTag` once, above both lines.

- [ ] **Step 5: Update the tones**

Replace the top of `web/src/shared/statusTone.ts`, up to `statusTone`'s closing brace, with:

```ts
/**
 * The API exposes three separate status unions (TemplateRunStatus with 7
 * values, TemplateRegistrationStatus with 5, TemplateRevisionStatus with 4).
 * Every status maps onto one of five visual tones.
 */
export type StatusTone = "settled" | "progress" | "waiting" | "failed" | "canceled";

const FAILED = new Set(["failed", "invalid", "error"]);
const CANCELED = new Set(["canceled"]);
const WAITING = new Set(["pending", "pending_validation", "queued", "waiting_approval"]);
const PROGRESS = new Set(["running", "validating"]);

export function statusTone(value: string): StatusTone {
  if (FAILED.has(value)) return "failed";
  if (CANCELED.has(value)) return "canceled";
  if (WAITING.has(value)) return "waiting";
  if (PROGRESS.has(value)) return "progress";

  // Callers pass human phrases such as "not configured" for absent resources.
  if (value.startsWith("not ")) return "waiting";

  // Completed, active, approved, and anything unrecognised are settled.
  return "settled";
}
```

In `web/src/shared/statusTone.test.ts`:
- Delete the tests `"classifies finished pipeline phases as settled"` and `"classifies intermediate pipeline steps as settled"`.
- Delete the lines for `lock_released`, `init_started`, `plan_started`, `apply_started`, `destroy_started` and `locked`.
- Add `expect(statusTone("plan_started")).toBe("settled");` to `"falls back to settled for unrecognised values"`. The old progress strings are no longer special.

- [ ] **Step 6: Replace the old status literals in the other tests and the style guide**

| File | Replace | With |
|---|---|---|
| `TemplateRunActions.test.tsx:197,291` | `"plan_started"`, `"apply_started"` | `"running"` |
| `TemplateDestroyPanel.test.tsx:191` | `"plan_started"` | `"running"` |
| `TemplateRunHistory.test.tsx:118` | `"apply_started"` | `"running"` |
| `RunDetailScreen.test.tsx:323` | `it.each(["plan_started", "plan_finished", "apply_started"] as const)` | `it.each(["queued", "running"] as const)` |
| `api/queries.test.tsx:128-208` | every `"plan_finished"` | `"running"` |
| `dev/StyleGuide.tsx:344` | `value="plan_started"` | `value="running"` |
| `dev/StyleGuide.tsx:348` | `<StatusRow label="Destroy" value="destroy_finished" />` | `<StatusRow label="Approved" value="approved" />` |
| `dev/StyleGuide.tsx:339` note | `"Thirty status values across three API unions"` | `"Sixteen status values across three API unions"` |

Delete the `.status-row[data-status="destroy_finished"]` rule and its comment from `web/src/styles/primitives.css` (lines ~562-566).

- [ ] **Step 7: Run everything**

Run: `cd web && npm test && npm run build`
Expected: all tests PASS, and the build type-checks. `tsc` rejects any old status literal left behind.

- [ ] **Step 8: Check it in the running app**

Start the stack as described in `README.md` (API, executor, `npm run dev`). Start an apply run and watch its detail page. The header should read "Planning · Fetching source", then "Planning · Initializing", and so on. The log tabs should appear as each command's log lands, without a reload.

Take a screenshot for the PR, following the web visual-verification approach used on this project.

- [ ] **Step 9: Commit**

```bash
git add web
git commit -m "feat: runs read their step, and logs refresh as it changes

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```
