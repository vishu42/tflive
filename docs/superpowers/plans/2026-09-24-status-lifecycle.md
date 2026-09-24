# Status Lifecycle Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give every status write in `internal/workflows` one home per workflow, a `lifecycle` method, the way `ExecuteStep` is the one home of a step, and pin that with a test.

**Architecture:** `run.lifecycle(kind, work)` validates the operation, starts the run (writes running for `runPlan`, claims through `BeginApply` for `runApply`), runs `work`, marks the run failed on any error, and settles the end (`FinishPlan` then completed or nothing for a plan, completed for an apply). `registration.lifecycle(work)` does the same for a template sync. The status write is a closure local to each `lifecycle`, and `setStatus`, `fail`, `claimApply`, `settlePlan` and `settle` stop being methods, so no other code has a status method to call. A test parses the package's source and fails if any status-writing activity is named outside its `lifecycle`.

**Tech Stack:** Go, the Temporal Go SDK (`workflow`, `testsuite`), `go/parser` and `go/ast` for the guard test.

**Spec:** None written. The design was agreed in conversation on 2026-09-24 and is restated in Architecture above. The workflow bodies it asks for are:

```go
func TemplatePlanWorkflow(ctx workflow.Context, input domain.TemplateRunWorkflowInput) error {
	r := newRun(ctx, input)
	return r.lifecycle(runPlan, func() (domain.RunTerraformActivityOutput, error) { ... r.plan(ctx) ... })
}

func TemplateApplyWorkflow(ctx workflow.Context, input domain.TemplateRunWorkflowInput) error {
	r := newRun(ctx, input)
	return r.lifecycle(runApply, func() (domain.RunTerraformActivityOutput, error) { ... r.apply ... })
}
```

## Global Constraints

- **Do not commit.** The user commits their own work. Leave every change uncommitted in the working tree; there are no commit steps.
- No `phase` type, parameter or word in new identifiers. The kinds are `runPlan` and `runApply`; the method is `lifecycle`.
- The order of every status, step and event write stays exactly as today. The existing 47 tests in `internal/workflows` pin it and must pass unmodified.
- Workflow tests in this package use the standard `testing` package with `t.Fatalf`, not testify's `assert`/`require`. New tests follow suit and call `t.Parallel()`.
- Format with `gofmt -w` on every Go file you touch. Run `go vet ./internal/workflows/`.
- The repository is pre-production: no backward-compatibility concerns.

## Review Focus

1. **A lost apply claim ends quietly.** When `BeginApply` reports `Claimed: false`, `lifecycle` returns nil and writes neither failed nor completed. Already pinned by `TestTemplateApplyWorkflowStopsWhenItLosesTheClaim` (only the claim activity starts).
2. **A rejected operation fails without ever running.** Validation runs before the start, so the only status write is `failed`. Already pinned by `TestTemplatePlanWorkflowRejectsUnsupportedOperation` and `TestTemplateWorkflowsRejectRunsTheyDoNotRun`.
3. **A run whose failure cannot be recorded surfaces both errors.** The sync has this pinned (`TestTemplateSyncWorkflowKeepsTheSyncErrorWhenItsFailureCannotBeRecorded`); the run does not. Task 1 adds `TestTemplatePlanWorkflowKeepsItsErrorWhenItsFailureCannotBeRecorded`.
4. **A plan waiting for approval writes no completed.** `FinishPlan` writes `waiting_approval` itself; `lifecycle` must write nothing after it. Already pinned by `TestTemplatePlanWorkflowSavesAPlanWithChangesAndEnds`.
5. **The guard test goes vacuous if an activity is renamed.** If `domain.FinishPlanActivityName` were renamed, a guard keyed on the old name would pass forever. The guard in Task 1 fails when any name it guards appears nowhere.

---

### Task 1: `run.lifecycle` and the guard test

**Files:**
- Create: `internal/workflows/lifecycle_test.go`
- Modify: `internal/workflows/template_run.go` (workflow bodies at lines 13-18 and 76-136; `validateOperation` unchanged; delete `setStatus`, `fail`, `claimApply`, `settlePlan` at the end of the file)
- Test: `internal/workflows/template_run_workflow_test.go` (one new test appended)

**Interfaces:**
- Consumes: `newRun(ctx, input) *run`, `(*run).validateOperation(applying bool) error`, `(*run).inSession(time.Duration, func(workflow.Context) error) error`, `(*run).plan(workflow.Context) (domain.RunTerraformActivityOutput, error)`, `(*run).apply(workflow.Context) error`, `(*run).event(domain.TemplateRunEvent, *domain.PlanSummary) error` — all existing, unchanged.
- Produces: `type runKind int` with constants `runPlan`, `runApply`; `func (r *run) lifecycle(kind runKind, work func() (domain.RunTerraformActivityOutput, error)) (err error)`; in the test file, `var statusWriters map[string]string` and `func funcName(*ast.FuncDecl) string`, which Task 2 extends.

- [ ] **Step 1: Write the guard test**

Create `internal/workflows/lifecycle_test.go`:

```go
package workflows

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// statusWriters are the activities that write a status, each with the one
// function allowed to schedule it. BeginApply and FinishPlan are here because
// they move a run's status as part of their own work.
var statusWriters = map[string]string{
	"RecordTemplateRunStatusActivityName": "(*run).lifecycle",
	"BeginApplyActivityName":              "(*run).lifecycle",
	"FinishPlanActivityName":              "(*run).lifecycle",
}

// TestOnlyLifecycleWritesStatus pins that a workflow's status is written in one
// place: every activity that writes it is named only in its lifecycle, so
// reading lifecycle is reading every status change. It also fails when a
// guarded name appears nowhere, so a renamed activity cannot leave it passing
// while guarding nothing.
func TestOnlyLifecycleWritesStatus(t *testing.T) {
	t.Parallel()

	names, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	seen := map[string]bool{}
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			owner := "package scope"
			if fn, ok := decl.(*ast.FuncDecl); ok {
				owner = funcName(fn)
			}
			ast.Inspect(decl, func(node ast.Node) bool {
				sel, ok := node.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				want, ok := statusWriters[sel.Sel.Name]
				if !ok {
					return true
				}
				seen[sel.Sel.Name] = true
				if owner != want {
					t.Errorf("%s: %s names %s; only %s may write status", fset.Position(sel.Pos()), owner, sel.Sel.Name, want)
				}
				return true
			})
		}
	}
	for name := range statusWriters {
		if !seen[name] {
			t.Errorf("nothing names %s: the guard is out of date", name)
		}
	}
}

// funcName names a function as the guard reports it: (*run).lifecycle for a
// method, lifecycle for a function.
func funcName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return fn.Name.Name
	}
	switch recv := fn.Recv.List[0].Type.(type) {
	case *ast.StarExpr:
		if ident, ok := recv.X.(*ast.Ident); ok {
			return "(*" + ident.Name + ")." + fn.Name.Name
		}
	case *ast.Ident:
		return recv.Name + "." + fn.Name.Name
	}
	return fn.Name.Name
}
```

- [ ] **Step 2: Add the Review Focus 3 test**

Append to `internal/workflows/template_run_workflow_test.go`:

```go
// A run whose failure cannot be recorded returns both errors: the one that
// failed it, still matchable, and the one that kept its status from saying so.
func TestTemplatePlanWorkflowKeepsItsErrorWhenItsFailureCannotBeRecorded(t *testing.T) {
	t.Parallel()

	env := newTemplateRunWorkflowTestEnvironment(t)
	env.OnActivity(domain.RecordTemplateRunStatusActivityName, mock.Anything, mock.Anything).
		Return(func(_ context.Context, input domain.TemplateRunStatusActivityInput) error {
			if input.Status == domain.TemplateRunFailed {
				return errors.New("database unavailable")
			}
			return nil
		})

	env.ExecuteWorkflow(TemplatePlanWorkflow, templateRunWorkflowInput(domain.OperationType("migrate")))

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow did not complete")
	}
	err := env.GetWorkflowError()
	if err == nil {
		t.Fatal("workflow error is nil, want the validation error")
	}
	for _, want := range []string{"unsupported template run operation", "also failed to persist failure status", "database unavailable"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("workflow error = %q, want it to mention %q", err, want)
		}
	}
}
```

- [ ] **Step 3: Run the new tests to see where they stand**

Run: `go test ./internal/workflows/ -run 'TestOnlyLifecycleWritesStatus|TestTemplatePlanWorkflowKeepsItsErrorWhenItsFailureCannotBeRecorded' -v`

Expected: `TestOnlyLifecycleWritesStatus` FAILS, with errors naming `(*run).setStatus`, `(*run).claimApply` and `(*run).settlePlan` as the owners of the status activities. `TestTemplatePlanWorkflowKeepsItsErrorWhenItsFailureCannotBeRecorded` PASSES: it pins behaviour the current `fail` already has, which `lifecycle` must keep.

- [ ] **Step 4: Replace the workflow bodies and add `runKind`**

In `internal/workflows/template_run.go`, replace the file's opening comment (lines 13-18):

```go
// A template run is two workflows, one per phase: TemplatePlanWorkflow and
// TemplateApplyWorkflow. Each hands its work to lifecycle, which alone moves
// the run along its status, and does that work in an executor session, as the
// run's steps.
//
// Both are written as methods on run (below), which holds the control plane,
// and hands the executor session to the methods that work in it.
```

Add, just above `TemplatePlanWorkflow`:

```go
// runKind is which of a run's two workflows is carrying it: the plan, or the
// apply of a plan someone approved (or of an auto-approved run).
type runKind int

const (
	runPlan runKind = iota
	runApply
)
```

Replace the body of `TemplatePlanWorkflow` (keep its doc comment, but change its last line from "Any error marks the run failed before the workflow returns it." to "Its status is lifecycle's."):

```go
func TemplatePlanWorkflow(ctx workflow.Context, input domain.TemplateRunWorkflowInput) error {
	r := newRun(ctx, input)
	return r.lifecycle(runPlan, func() (domain.RunTerraformActivityOutput, error) {
		var planned domain.RunTerraformActivityOutput
		err := r.inSession(planSessionCreationTimeout, func(ctx workflow.Context) (err error) {
			planned, err = r.plan(ctx)
			return err
		})
		return planned, err
	})
}
```

Replace the body of `TemplateApplyWorkflow` (doc comment unchanged):

```go
func TemplateApplyWorkflow(ctx workflow.Context, input domain.TemplateRunWorkflowInput) error {
	r := newRun(ctx, input)
	return r.lifecycle(runApply, func() (domain.RunTerraformActivityOutput, error) {
		return domain.RunTerraformActivityOutput{}, r.inSession(applySessionCreationTimeout, r.apply)
	})
}
```

- [ ] **Step 5: Add `lifecycle` and delete the four status methods**

Delete `setStatus`, `fail`, `claimApply` and `settlePlan` from `internal/workflows/template_run.go`, with their doc comments. In their place, add:

```go
// lifecycle carries a run through its status, and is the only code that writes
// it: every status write is here, and so is every activity that moves the
// status as part of its own work, BeginApply and FinishPlan.
//
// A run whose operation its workflow does not run fails without ever running.
// A plan starts running here. An apply claims its run instead, moving it from
// approved (or, auto-approved, queued) to running; a lost claim ends quietly,
// since the discard that won it already recorded the run's end.
//
// A finished plan is settled: a plan with nothing to change, or a plan run,
// completes, and an apply or destroy run with changes waits for approval,
// which FinishPlan records. A destroy with nothing left to destroy records that
// it is destroyed before it completes, which is what moves its stack template.
// A finished apply completes.
//
// Any error marks the run failed before lifecycle returns it. If recording the
// failure also fails, the run's persisted status will not match reality, so
// both errors are surfaced: the original wrapped with %w to stay matchable by
// callers, the persistence error appended as context.
func (r *run) lifecycle(kind runKind, work func() (domain.RunTerraformActivityOutput, error)) (err error) {
	setStatus := func(status domain.TemplateRunStatus, errorSummary string) error {
		return workflow.ExecuteActivity(
			r.ctx,
			domain.RecordTemplateRunStatusActivityName,
			domain.TemplateRunStatusActivityInput{
				RunID:           r.input.RunID,
				TenantID:        r.input.TenantID,
				StackTemplateID: r.input.StackTemplateID,
				Operation:       r.input.Operation,
				Status:          status,
				ErrorSummary:    errorSummary,
			},
		).Get(r.ctx, nil)
	}
	defer func() {
		if err == nil {
			return
		}
		if failureErr := setStatus(domain.TemplateRunFailed, fmt.Sprintf("template run activity failed: %v", err)); failureErr != nil {
			err = fmt.Errorf("%w (also failed to persist failure status: %v)", err, failureErr)
		}
	}()

	if err := r.validateOperation(kind == runApply); err != nil {
		return err
	}
	switch kind {
	case runPlan:
		if err := setStatus(domain.TemplateRunRunning, ""); err != nil {
			return err
		}
	case runApply:
		var claim domain.BeginApplyActivityOutput
		if err := workflow.ExecuteActivity(r.ctx, domain.BeginApplyActivityName, domain.BeginApplyActivityInput{
			TenantID:    r.input.TenantID,
			RunID:       r.input.RunID,
			AutoApprove: r.input.AutoApprove,
		}).Get(r.ctx, &claim); err != nil {
			return err
		}
		if !claim.Claimed {
			return nil
		}
	}

	output, err := work()
	if err != nil {
		return err
	}
	if kind == runApply {
		return setStatus(domain.TemplateRunCompleted, "")
	}

	var outcome domain.PlanOutcome
	if err := workflow.ExecuteActivity(r.ctx, domain.FinishPlanActivityName, domain.FinishPlanActivityInput{
		TenantID:        r.input.TenantID,
		RunID:           r.input.RunID,
		StackTemplateID: r.input.StackTemplateID,
		Operation:       r.input.Operation,
		HasChanges:      output.HasChanges,
		Summary:         output.Summary,
	}).Get(r.ctx, &outcome); err != nil {
		return err
	}
	switch outcome {
	case domain.PlanOutcomeNoChanges:
		if r.input.Operation == domain.OperationDestroy {
			if err := r.event(domain.TemplateRunDestroyed, nil); err != nil {
				return err
			}
		}
		return setStatus(domain.TemplateRunCompleted, "")
	case domain.PlanOutcomePlanned:
		return setStatus(domain.TemplateRunCompleted, "")
	case domain.PlanOutcomeWaiting:
		return nil
	default:
		return fmt.Errorf("unknown plan outcome %q", outcome)
	}
}
```

Note on `output, err := work()`: `err` is the named result, declared in the function's own scope, so `:=` declares only `output` and assigns `err`. The deferred failure write therefore sees it. The earlier `if err := ...` forms are scoped to their `if` and return their error explicitly, which also sets the named result.

- [ ] **Step 6: Fix `validateOperation`'s doc comment**

Its comment says "Returning the error lets fail record the single Failed status". Change "lets fail record" to "lets lifecycle record".

- [ ] **Step 7: Format, vet and run the whole package**

Run: `gofmt -w internal/workflows/template_run.go internal/workflows/lifecycle_test.go internal/workflows/template_run_workflow_test.go && go vet ./internal/workflows/ && go test ./internal/workflows/ -count=1 -v 2>&1 | grep -E '^\s*--- (PASS|FAIL)' | awk '{print $2}' | sort | uniq -c`

Expected: `49 PASS:` and no `FAIL:` line (the 47 existing tests, unmodified, plus the two new ones).

Then confirm the chokepoint by hand: `grep -n 'RecordTemplateRunStatusActivityName\|BeginApplyActivityName\|FinishPlanActivityName' internal/workflows/*.go | grep -v _test`. Every line must fall inside `lifecycle`.

---

### Task 2: `registration.lifecycle`

**Files:**
- Modify: `internal/workflows/template_sync.go` (`TemplateSyncWorkflow` and its doc comment; delete `fail`, `settle`, `setStatus`)
- Modify: `internal/workflows/lifecycle_test.go` (one entry added to `statusWriters`)

**Interfaces:**
- Consumes: `statusWriters` and `TestOnlyLifecycleWritesStatus` from Task 1; existing `newRegistration(ctx, input) *registration` and `(*registration).sync() (domain.TemplateSyncActivityOutput, error)`, both unchanged.
- Produces: `func (r *registration) lifecycle(work func() (domain.TemplateSyncActivityOutput, error)) (err error)`.

- [ ] **Step 1: Guard the registration's status activity**

In `internal/workflows/lifecycle_test.go`, add to `statusWriters`:

```go
	"RecordTemplateRegistrationStatusActivityName": "(*registration).lifecycle",
```

- [ ] **Step 2: Run the guard to see it fail**

Run: `go test ./internal/workflows/ -run TestOnlyLifecycleWritesStatus -v`

Expected: FAIL, naming `(*registration).setStatus` as the owner of `RecordTemplateRegistrationStatusActivityName`.

- [ ] **Step 3: Replace the workflow body**

In `internal/workflows/template_sync.go`, replace `TemplateSyncWorkflow` and its doc comment:

```go
// TemplateSyncWorkflow syncs a template registration: it syncs the template as
// the sync's one step, and its status is lifecycle's.
func TemplateSyncWorkflow(ctx workflow.Context, input domain.TemplateSyncWorkflowInput) error {
	r := newRegistration(ctx, input)
	return r.lifecycle(r.sync)
}
```

- [ ] **Step 4: Add `lifecycle` and delete the three status methods**

Delete `fail`, `settle` and `setStatus` from `internal/workflows/template_sync.go`, with their doc comments. In their place, add:

```go
// lifecycle carries a registration through its status, and is the only code
// that writes it. It marks the registration running, runs work, and records
// what work found: the revision it produced, or why the template is invalid. A
// sync that reports no status of its own completed.
//
// Any error, including one writing that outcome, marks the registration failed
// before lifecycle returns it. If recording the failure also fails, both
// errors are surfaced: the original wrapped with %w to stay matchable by
// callers, the persistence error appended as context.
func (r *registration) lifecycle(work func() (domain.TemplateSyncActivityOutput, error)) (err error) {
	setStatus := func(status domain.TemplateRegistrationStatusActivityInput) error {
		status.RegistrationID = r.input.RegistrationID
		status.TenantID = r.input.TenantID
		return workflow.ExecuteActivity(
			r.ctx,
			domain.RecordTemplateRegistrationStatusActivityName,
			status,
		).Get(r.ctx, nil)
	}
	defer func() {
		if err == nil {
			return
		}
		if recordErr := setStatus(domain.TemplateRegistrationStatusActivityInput{
			Status:       domain.TemplateRegistrationFailed,
			ErrorSummary: err.Error(),
		}); recordErr != nil {
			err = fmt.Errorf("%w (also failed to persist failure status: %v)", err, recordErr)
		}
	}()

	if err := setStatus(domain.TemplateRegistrationStatusActivityInput{
		Status: domain.TemplateRegistrationRunning,
	}); err != nil {
		return err
	}
	synced, err := work()
	if err != nil {
		return err
	}
	status := synced.Status
	if status == "" {
		status = domain.TemplateRegistrationCompleted
	}
	return setStatus(domain.TemplateRegistrationStatusActivityInput{
		Status:             status,
		TemplateRevisionID: synced.TemplateRevisionID,
		ResolvedCommitSHA:  synced.ResolvedCommitSHA,
		ErrorSummary:       synced.ErrorSummary,
	})
}
```

Also update the `registration` type's doc comment: replace "It holds the control-queue context and the sync's input, and it is the sync's Recorder." with "It holds the control-queue context and the sync's input, and it is the sync's Recorder. Its status is written only by lifecycle."

- [ ] **Step 5: Format, vet and run the whole package**

Run: `gofmt -w internal/workflows/template_sync.go internal/workflows/lifecycle_test.go && go vet ./internal/workflows/ && go build ./... && go test ./internal/workflows/ -count=1 -v 2>&1 | grep -E '^\s*--- (PASS|FAIL)' | awk '{print $2}' | sort | uniq -c`

Expected: `49 PASS:` and no `FAIL:` line. `go build ./...` prints nothing.

Leave everything uncommitted and report the result.
