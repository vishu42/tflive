# Template Destroy Interruption Lifecycle Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move a stack template from `destroying` to `failed` when a destroy run is canceled or fails after destruction starts.

**Architecture:** Keep the workflow’s terminal run statuses unchanged. Extend the repository’s transactional status side effects so terminal failed/canceled destroy statuses conditionally compensate `stack_templates.lifecycle` only when it is currently `destroying`.

**Tech Stack:** Go, PostgreSQL, Temporal workflow tests, Testify/mock helpers.

## Global Constraints

- Do not restore an interrupted destroy to `active`; Terraform may have partially changed infrastructure.
- Do not mark a template `destroyed` unless the Terraform destroy activity succeeded.
- Preserve the existing run status distinction: `failed` for activity errors and `canceled` for user cancellation.
- Keep tenant, run, stack-template, and operation predicates on every repository update.
- Use `gofmt` and run focused Go tests before the full relevant package tests.

---

### Task 1: Add the repository lifecycle compensation

**Files:**
- Modify: `internal/postgres/repositories.go:1437-1473,1538-1593`
- Test: `internal/postgres/store_test.go` near the existing destroy lifecycle tests at lines 2444-2568

**Interfaces:**
- Consumes: `traits.TemplateRunStatusActivityInput`, `TemplateRunStatus.Terminal`, and the existing `recordStackTemplateLifecycle` helper.
- Produces: transactional behavior where terminal failed/canceled destroy statuses set a currently `destroying` stack template to `failed`.

- [ ] **Step 1: Write failing repository tests**

Add tests that seed an active stack template and a destroy run already at
`destroy_started`, then call `RecordTemplateRunStatus` with `failed` and
`canceled`. Assert the run has the requested terminal status and the stack
template lifecycle is `failed`. Add a control case with an active template and
a terminal failed destroy status; assert its lifecycle remains `active`.

- [ ] **Step 2: Run the focused repository tests and verify the failure**

Run:

```bash
rtk go test ./internal/postgres -run 'TestRecordTemplateRunStatus.*Destroy|TestRecordTemplateRunStatus.*Failure' -count=1
```

Expected: the new interruption tests fail because the lifecycle remains
`destroying` or `active` instead of becoming `failed`.

- [ ] **Step 3: Implement the minimal transaction change**

Extend the special-side-effect predicate used by `RecordTemplateRunStatus` to
include terminal failed/canceled destroy inputs. Add
`recordInterruptedDestroyLifecycle`, which reads the tenant-owned
`stack_templates.lifecycle` row with `FOR UPDATE`: it changes `destroying` to
`failed`, treats an already `failed` row as an idempotent no-op, and leaves
`active` or `destroyed` unchanged. A missing stack template still returns
`ErrNotFound`. Call this helper in the same transaction after
`recordTemplateRunStatus`, while preserving the existing `destroy_started`,
`destroyed`, and apply side effects.

- [ ] **Step 4: Run the focused repository tests and verify they pass**

Run the same focused command. Expected: all new and existing matching tests
pass.

- [ ] **Step 5: Commit the repository change**

```bash
git add internal/postgres/repositories.go internal/postgres/store_test.go
git commit -m "fix: reconcile interrupted destroy lifecycle"
```

### Task 2: Document the interrupted destroy lifecycle

**Files:**
- Modify: `docs/architecture.md` in the run lifecycle and StackTemplate lifecycle sections

**Interfaces:**
- Consumes: the repository compensation implemented in Task 1 and the existing
  workflow cancellation behavior.
- Produces: documentation that distinguishes a successful destroy from an
  interrupted destroy and makes the no-rollback behavior explicit.

- [ ] **Step 1: Review the existing workflow coverage**

Confirm the existing destroy workflow tests still cover approval, successful
destroy, and cancellation before Terraform starts. The new persistence tests in
Task 1 cover the lifecycle compensation after the workflow records a terminal
interruption.

- [ ] **Step 2: Update the lifecycle documentation**

Document the destroy interruption path as
`destroying → failed`, while retaining `destroyed` for successful Terraform
completion and explaining that no infrastructure rollback is implied.

- [ ] **Step 3: Run focused and package-level tests**

Run:

```bash
rtk go test ./internal/workflows ./internal/postgres -count=1
```

Expected: all available tests pass without warnings; PostgreSQL integration
tests may be skipped when `tflive_POSTGRES_TEST_DSN` is not configured.

- [ ] **Step 4: Commit the documentation**

```bash
git add docs/architecture.md
git commit -m "docs: describe interrupted destroy lifecycle"
```
