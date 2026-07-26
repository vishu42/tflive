# Implementation Plan: Terraform Destroy Flow

Ref: docs/superpowers/specs/2026-07-26-terraform-destroy-flow-design.md
Issue: https://github.com/vishu42/tflive/issues/126

## Global Constraints

- Go: PascalCase for exported, camelCase for private
- SQL: positional params ($1, $2...), transactions with `defer tx.Rollback()`
- Error wrapping: `fmt.Errorf("action: %w", err)`
- Frontend: TypeScript, React, @tanstack/react-query, vitest + testing-library
- Tests: `t.Parallel()`, table-driven, recording doubles, factory functions
- Follow existing patterns exactly — mirror apply() for destroy()

## Tasks

### Task 1: Backend Command Wiring
Files: `internal/traits/traits.go`, `internal/logsink/filesink.go`, `internal/workflows/template_run.go`, `internal/runner/terraform.go`

- Add `TerraformCommandDestroy TerraformCommandType = "destroy"` to const block in traits.go
- Add `case traits.TerraformCommandDestroy: return "destroy", nil` to PhaseForTerraformCommand in filesink.go
- Replace the destroy() stub in template_run.go with real implementation matching apply() pattern: WaitingApproval → waitForApproval → Approved + DestroyStarted → runTerraform(destroy) → Destroyed → complete()
- Add destroy case in runner/terraform.go Run() switch: `tofu destroy -input=false -auto-approve -no-color`

### Task 2: Postgres Lifecycle + Filtering
File: `internal/postgres/repositories.go`

- Add `recordsStackTemplateDestroying()` and `recordsStackTemplateDestroyed()` helpers
- Add `recordStackTemplateLifecycle()` helper (follows recordStackTemplateLastApplied pattern)
- Update `RecordTemplateRunStatus` to call lifecycle helpers for destroy statuses
- Add `AND lifecycle != 'destroyed'` to GetStackWithTemplates WHERE clause

### Task 3: Frontend Destroy UI
Files: `web/src/features/stacks/stackWorkflow.ts`, `web/src/features/stacks/StackTemplateScreen.tsx`

- Add `canDestroyStackTemplate(t: StackTemplate): boolean` — lifecycle === "active"
- Add `isDestroyingStackTemplate(t: StackTemplate): boolean` — lifecycle === "destroying"
- Add Destroy button (gated by canDestroyStackTemplate) with confirmation dialog
- Show "Destroying…" badge when isDestroyingStackTemplate, disable other actions

### Task 4: Tests
Files: all test files for changed sources

- Update TestTemplateRunWorkflowRecordsDestroyStatuses to assert destroy command dispatched
- Add destroy case to TestPhaseForTerraformCommand
- Add destroy lifecycle tests to store_test.go
- Add destroy case to terraform runner tests
- Update frontend tests for destroy button and badge
