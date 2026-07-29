# Template Destroy Interruption Lifecycle Design

**Date:** 2026-07-29
**Status:** Approved

## Goal

Prevent a stack template from remaining in the non-runnable `destroying`
lifecycle when a destroy run is canceled or fails after Terraform destruction
has started.

## Root Cause

`TemplateRunWorkflow.destroy` records `destroy_started` before it schedules the
Terraform destroy activity. `RecordTemplateRunStatus` treats that status as a
special transition and updates `stack_templates.lifecycle` to `destroying` in
the same transaction as the run-status update. Later failure and cancellation
paths only write terminal status to `template_runs`, so the related
`stack_templates` row is never compensated.

## Decision

When a terminal `failed` or `canceled` status is recorded for a destroy run,
the repository will update the related stack template from `destroying` to
`failed` in the same transaction. The conditional lifecycle predicate makes
this idempotent and prevents failures before `destroy_started` from changing an
otherwise active template.

The run status remains truthful: Terraform errors produce `failed`, while user
cancellation produces `canceled`. The stack-template lifecycle is `failed` in
both cases because the infrastructure may have been partially destroyed and
cannot safely be treated as active. Successful destroys continue to transition
to `destroyed`.

`orphaned` is reserved for a future reconciliation flow that can distinguish
metadata loss from a partially completed Terraform operation.

## Data Flow

```text
destroy_started
    └─ stack_templates.lifecycle = destroying

destroy activity fails or is canceled
    └─ terminal run status is recorded
       └─ if lifecycle is still destroying:
          stack_templates.lifecycle = failed

destroy activity succeeds
    └─ destroyed → stack_templates.lifecycle = destroyed
```

The repository owns this compensation rather than the workflow so both the
normal workflow path and any terminal reconciliation path get the same behavior
without requiring the workflow to remember whether the start transition was
already persisted.

## Testing

- Repository integration tests prove failed and canceled destroy runs move a
  `destroying` stack template to `failed`.
- A repository control test proves a terminal destroy status recorded while the
  template is still `active` does not mark it failed.
- Existing workflow tests continue to cover cancellation before Terraform
  destroy starts; repository integration tests cover the terminal interruption
  transition that the workflow delegates to persistence.
- Existing successful destroy and pre-destroy cancellation tests remain green.

## Scope Boundaries

- No Terraform rollback is attempted.
- No new lifecycle value or recovery endpoint is added.
- No change is made to successful destroy behavior.
