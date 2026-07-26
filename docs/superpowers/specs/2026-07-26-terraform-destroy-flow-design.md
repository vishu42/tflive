# Terraform Destroy Flow

Ref: [#126](https://github.com/vishu42/tflive/issues/126)

## Summary

`OperationDestroy` is defined and the workflow has a `destroy()` stub, but it never runs `terraform destroy`. Destroyed templates still appear in the UI. This implements the full destroy flow.

## Current Gaps

- No `TerraformCommandDestroy` constant
- `destroy()` stub in workflow never runs terraform
- `PhaseForTerraformCommand` has no destroy case
- `RecordTemplateRunStatus` doesn't update stack_template lifecycle on destroy events
- `GetStackWithTemplates` doesn't filter destroyed templates
- No `terraform destroy` case in runner
- No frontend destroy button or state

## Destroy Flow

```
WaitingApproval → Approved → DestroyStarted → tofu destroy → Destroyed → LockReleased → Completed
```

Approval required before destructive operation. No plan phase (add as `OperationPlanDestroy` later).

## Lifecycle Transitions

```
active → (destroy run reaches DestroyStarted) → destroying
destroying → (destroy run reaches Destroyed) → destroyed (filtered from UI)
```

## Implementation

### Backend

| File | Change |
|------|--------|
| `internal/traits/traits.go` | Add `TerraformCommandDestroy = "destroy"` |
| `internal/logsink/filesink.go` | Add destroy case returning `"destroy"` |
| `internal/workflows/template_run.go` | Replace destroy() stub with approval → runTerraform → complete |
| `internal/runner/terraform.go` | Add destroy case dispatching `tofu destroy` |
| `internal/postgres/repositories.go` | Lifecycle side-effects in RecordTemplateRunStatus; filter destroyed in GetStackWithTemplates |

### Frontend

| File | Change |
|------|--------|
| `web/src/features/stacks/stackWorkflow.ts` | `canDestroyStackTemplate()`, `isDestroyingStackTemplate()` |
| `web/src/features/stacks/StackTemplateScreen.tsx` | Destroy button, confirmation dialog, "Destroying..." badge |

### Tests

Update existing tests in: `template_run_workflow_test.go`, `store_test.go`, `filesink_test.go`, `terraform_test.go`, `stackWorkflow.test.ts`, `StackTemplateScreen.test.tsx`
