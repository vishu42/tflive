# Plan / apply flow — analysis

Status: decided and built on `feat/saved-plan-flow`, 2026-09-22. The analysis
below is as written before the decisions; the decisions are at the end.

## The proposal

Make runs behave like the Terraform CLI:

| User action | Workflow | Terraform | Approval |
|---|---|---|---|
| Plan | plan workflow | `plan` — logs kept, plan not uploaded | none; the run ends at the plan |
| Apply | plan workflow, then apply workflow | `plan -out`, plan uploaded; then `apply -auto-approve tfplan` | row shows Approve, which starts the apply workflow |
| Apply + auto-approve | apply workflow only | `apply -auto-approve`, no saved plan | none |

## How today differs

- There is one "plan" operation that always ends in either an apply or a
  wait for approval. A finished plan with changes becomes `waiting_approval`
  and sets the stack template's `pending_plan_*` (`FinishTemplatePlan`,
  `internal/postgres/saved_plans.go:100`). So every plan offers Approve.
- Auto-approve is a flag on that operation. The plan workflow plans, uploads,
  records the approval as the plan finishes (plus an `approval_granted`
  audit event), then calls `applyPhase` itself. That runs setup twice (fetch,
  init, select) in two sessions.
- `TemplateApplyWorkflow` only knows how to apply a saved plan. It claims the
  run by moving it `approved` → `locked` (`BeginTemplateApply`).
- `OperationDestroy` is the same shape with `plan_destroy`.

## What the change touches

1. **Operation model** (`domain.OperationType`). `plan` currently means "plan
   and then apply". It would become `plan` (speculative), `apply` and
   `destroy`, with `AutoApprove` meaningful only on `apply`/`destroy`. Since
   we're pre-production, the values can change outright.
2. **Plan workflow.** It stops applying. The `PlanOutcomeApproved` branch and
   the auto-approve handling in `FinishTemplatePlan` go away. The plan
   workflow never calls `applyPhase`, so the double-setup cost disappears.
   The name `TemplatePlanWithApplyWorkflow` becomes wrong again; it goes back
   to `TemplatePlanWorkflow`. The proposal says `TerraformPlanWorkflow` /
   `TerraformApplyWorkflow`: is that a rename from `Template*` too?
3. **Apply workflow gets two entry modes.**
   - From an approval: the claim is `approved` → `locked`, then download the
     plan and `apply tfplan`. Same as today.
   - Direct (auto-approve): the run starts `queued`, so the claim must accept
     `queued` → `locked`, or direct runs skip the claim. No plan to restore,
     and a different terraform command.
4. **Runner.** It passes `TF_VAR_*` only to plans, on purpose: a saved plan
   carries its variables, and "applying it must not be able to change them"
   (`TestLocalProcessRunnerSetsTerraformVariablesOnlyForPlans`). A direct
   apply needs the variables. Following the `plan_destroy` precedent, that
   means new commands, e.g. `apply_direct` / `destroy_direct` (`tofu apply
   -auto-approve`, `tofu apply -destroy -auto-approve`), each with its own
   status row and log name. It must not become a flag on `apply`, or the
   saved-plan apply could accidentally get variables.
5. **The speculative plan still needs `-out` locally.** The summary counts
   (`plan_add/change/destroy`) come from `show -json` on the plan file
   (`runner.plan`). "No upload" means skipping `SealPlanKey` + `UploadPlan`,
   not dropping `-out`. The file dies with the workspace.
6. **`FinishTemplatePlan` becomes per operation.**
   - `plan`: record the summary and complete. It must **not** set
     `pending_plan_*`, since there is nothing to approve.
   - `apply`/`destroy` with changes: `waiting_approval` + `pending_plan_*`,
     as today.
   - No changes: today a no-change `plan` run records `last_applied_*`
     (LiveState flips to `matches`). Should a speculative plan still do that?
     It's true that infra equals config at that moment, but no apply
     happened. The CLI-faithful answer is no.
7. **UI** (`web/src/features/runs/TemplateRunActions.tsx`). The header has
   one Plan button with an Auto Apply checkbox. It would become Plan, plus
   Apply with the checkbox next to it. The row's Approve already renders only
   for `waiting_approval`, which a speculative plan never reaches, so the row
   may need no change. The Destroy panel (`TemplateDestroyPanel.tsx`) mirrors
   Apply.
8. **Authorization.** Today starting any run takes `can_operate`, and
   auto-approve also takes `can_approve`. With a separate speculative plan,
   a plan-only permission becomes possible (viewers who can see what would
   change). No need to do that now, but the operation split is what makes it
   cheap later.
9. **Audit.** Auto-approve's `approval_granted` event is written in
   `FinishTemplatePlan` today. With no plan step, it moves to
   `StartTemplateRun`, where the approve check already happens.
10. **What a direct apply gives up**, knowingly, as the CLI does:
    - No saved plan, so no "what was reviewed is what was applied"
      guarantee. Nobody reviewed anything.
    - No `plan_started`/`plan_finished` and no plan summary on the run.
      Counts could come from `apply -json` (`change_summary`) or from parsing
      "Apply complete! Resources: …".
    - No "no changes" outcome. The apply just completes. Either the UI
      tolerates a run with no counts, or the runner parses them.
11. **Cancel.** `CancelTemplateRun` signals the plan workflow first and falls
    back to the apply workflow on NotFound. A direct apply has only an apply
    workflow, so that path should just work. It needs one test.
    `cancelTemplateRunBeforeApply` (waiting/approved) doesn't apply to
    direct runs.
12. **Concurrency.** A speculative plan still counts as "in flight"
    (`requireNoRunInFlight` blocks desired edits and other runs). That matches
    the CLI, where plan takes the state lock. Keep it.
13. **Logs.** `RunPhase` fits as is. A speculative plan logs `plan-init`,
    `plan-workspace`, `plan`. A direct apply logs `apply-init`,
    `apply-workspace`, `apply`. A saved-plan apply logs all six.
14. **Staleness gate.** `ApproveRun` requires `PendingPlanRunID == run` and
    `PlanMatches`. That's unchanged, and it applies only to `apply`/`destroy`
    runs.

## Decisions needed

- Operation values and whether destroy follows Apply's shape (saved plan +
  approve, or direct with auto-approve). The proposal doesn't mention
  destroy.
- Does a no-change speculative plan update `last_applied_*`? (#6)
- Direct apply: parse counts from the apply output, or show none? (#10)
- Workflow names: `Template*` or `Terraform*`? (#2)

## Decisions (2026-09-22)

- Destroy keeps the saved-plan-and-approve shape only; it has no
  auto-approve. The API refuses `auto_approve` on anything but `apply`.
- A plan run with no changes still records the template as applied.
- An auto-approved apply records counts parsed from its "Apply complete!"
  line.
- Workflows keep the `Template*` prefix. The plan workflow is
  `TemplatePlanWorkflow` again, since it no longer applies.
- The row's approve control reads **Approve**, because the header's
  **Apply** now starts an apply run.

## Suggested order

1. Operation split + speculative plan (plan workflow stops uploading, stops
   setting pending plan, stops applying).
2. `apply` operation = today's approval flow, now reached only by an
   Apply-with-plan run.
3. Direct apply: new commands, the runner passing variables, the `queued`
   claim, and the audit moving to start.
4. UI buttons, Destroy panel.
