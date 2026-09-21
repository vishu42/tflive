# Saved plans, plan → approve → apply, template pages, run numbers

Issue: #249 (Fasten epic #247). Stacked PRs: #265 (run numbers), #266 (template pages), PR 3 (saved-plan flow).

## Status

| PR | Scope | State |
|---|---|---|
| 1 — #265 | Run numbers | Open, done |
| 2 — #266 | Template list and tabbed pages, UI rework | Open, done |
| 3 | Saved plans, plan → approve → apply, auto-approve | Open, done |

## Context

Today an apply is **two runs that each plan**:
- A `plan` run: init, then plan.
- An `apply` run: gated on `PlanState()==matches`. It does init, plans **again**, then waits for approval while **holding its executor session**, then runs a bare `apply -auto-approve`.

`destroy` runs init first and then waits for approval **without producing any plan**, so nobody reviews what will be destroyed.

The result:
- Session slots are held by idle runs, and there is a 24h session cliff (#249).
- What is approved is not what gets applied.
- The user has to click three times (Plan → Apply → Approve).
- Every change is planned twice.

The template screen also packs the list and the detail into one view, and runs are identified only by random IDs.

After this work:
- A **plan run** saves an encrypted plan (`-out`) and waits for approval. Approving it applies **that saved plan** in a separate workflow and a separate session.
- Destroy works the same way, using `plan -destroy`.
- Users who can both operate and approve can tick auto-approve.
- Templates get their own list page and a tabbed detail page.
- Runs get a number per template (#1, #2, …).

## Decisions

1. **Operations are `plan` and `destroy`.** `apply` is no longer an operation anyone can start. It only happens when a waiting plan is approved, and approval runs `tofu apply -input=false -auto-approve -no-color <planfile>` with no `-var` (the variables are inside the saved plan). The `PlanState` gate moves from `StartTemplateRun` to approval (decision 10).
2. **A waiting plan blocks the stack.** `waiting_approval` stays in `template_runs_in_flight_idx`. To start over, the user discards the plan (the existing cancel), which deletes the saved plan. tofu also rejects a stale plan at apply time ("Saved plan is stale").
3. **A plan with no changes completes immediately** (`-detailed-exitcode` exit 0), without approval, and **records `last_applied` from the run's snapshot**. "No changes" means the infrastructure already equals that snapshot. Without this, `LiveState` would stay `differs` forever after a config edit that produces no diff, since no apply would ever happen to clear it. (Output-only changes return exit 2, so they still go through approval.)
4. **One run row, two workflows.**
   - `TemplatePlanWorkflow` runs one session and ends in `waiting_approval` or `completed`.
   - Approval enqueues `TemplateApplyWorkflow` (workflow ID `templateRunWorkflowID+"/apply"`), which runs a second session.
   - No workflow waits for a person.
5. **Auto-approve** requires `can_approve` in addition to `can_operate`, checked in `StartTemplateRun`, and is recorded as an approval by the trigger actor with an audit event. It needs no new FGA relation.
6. **The saved plan is encrypted with existing primitives.**
   - At plan time the control plane generates a per-run DEK, stores it encrypted with `encryption.Cipher` in Postgres, and seals it to the executor's run key with `runseal.Seal`.
   - The executor encrypts `tfplan` + `.terraform.lock.hcl` (tar.gz, AES-GCM) and uploads the bundle to `artifacts.ObjectStore`.
   - At apply time the control plane seals the same DEK to the new executor's key.
   - When the run reaches a terminal state, `plan_artifact_dek` is **nulled in the same transaction** as the status write. That destroys the key (crypto-shredding), so any bundle left behind can never be read. Deleting the object itself is best effort. Runs are never deleted, so tying the DEK's lifetime to the run row would keep it forever.
7. **Run numbers.**
   - Add `template_runs.run_number int not null` and a unique index on `(tenant_id, stack_template_id, run_number)`.
   - The number is assigned in the insert: `coalesce(max(run_number),0)+1` for the template. This can't race, because the in-flight index already allows only one concurrent run per template; the unique index is a backstop.
   - No counter column. The migration numbers existing runs by `started_at`.
   - The random run ID stays the primary key and keeps its role in Temporal IDs and artifact keys. URLs use the run number.
   - There is no lookup-by-number endpoint: run detail finds the id in the template's runs list, which the Runs tab has already loaded.
8. **Approve sits on the run row and on run detail.** PR 2 already put Cancel and Approve on the run's own row and in the run detail header, shown only when the run can take them and the viewer may. PR 3 renames Approve to "Apply", or "Destroy N" on a destroy run, and Cancel to "Discard" on a waiting plan, and adds a Changes column like "+3 ~1 -0" from `tofu show -json`.
9. **Destroy lives in the template's Settings tab**, in a danger zone next to upgrade, away from the Plan button. Clicking it runs a destroy **plan**, so the irreversible step becomes approving that plan:
   - The Settings button reads "Plan destroy" and is one click: it destroys nothing. It then opens the Runs tab. An "Auto Apply" checkbox is shown only to users with `canApprove`; ticking it makes the click irreversible, so then it takes a second click.
   - Approving a destroy run uses a red "Destroy N" button (the full "Destroy N resources" is its title) with a two-step confirm, on the row and on run detail. Approving is the irreversible action, so the confirm moved there.

10. **State matrix: P becomes "the waiting plan", and the D~P gate moves to approval.**
    - **P** is the snapshot of the run in `waiting_approval`. The in-flight index allows at most one. It is no longer "the latest completed plan run": a plan run now completes only after its apply, and a discarded plan must stop counting as reviewed.
    - The `last_planned_*` columns are renamed `pending_plan_*` and become a pointer to the waiting run: `FinishTemplatePlan` sets it when a plan with changes finishes, and `releaseRunPlan` clears it in the same transaction as every write that makes that run terminal. (Built as a pointer rather than the join planned here: five stack-template queries select these columns, and a pointer maintained transactionally gives the same answer with far less churn.) `recordsStackTemplateLastPlanned` is removed.
    - `PlanState()` keeps its values: `none` means no plan is waiting, `stale` means the waiting plan ≠ D, `matches` means it equals D.
    - The gate moves from `StartTemplateRun` to `ApproveRun` and to `FinishPlan` on the auto-approve path: approval requires `PlanState()==matches`, or it returns `ErrStackTemplatePlanStale` (409 `plan_stale`). `snapshotMatchesDesired` is reused unchanged.
    - **Editing is blocked while a run is in flight.** `UpdateStackTemplateConfig` and `UpgradeStackTemplate` return `ErrTemplateRunInFlight` when the template has a non-terminal run. Today nothing on the server stops them, and a waiting plan makes that window hours long. With edits blocked, the `stale` row is unreachable in normal use. The approval gate stays as a backstop.
    - **A** (last_applied) is now always a reviewed snapshot. Steady state moves from (matches, matches) to (none, matches). The top row stops meaning "applied without review", which becomes impossible.
    - The web stops using `hasFreshPlan`/`planStaleReason` for an Apply button, which no longer exists. The approve button on a waiting row reads `plan_state`.
    - Once PR 3 lands, update the state-matrix page (https://claude.ai/artifact/R4y2Kw4DpdUtMQeYsmk5ao). It already misstates the in-flight gate as client-only, when 0021 enforces it in Postgres.
11. **Stack template bookkeeping is keyed by phase, not by operation.** The predicates in `repositories.go` (~L1440) are all keyed on operation plus status:
    - `last_applied`: `OperationApply && apply_finished`. This would never fire once `apply` is no longer an operation. It becomes `OperationPlan && apply_finished`.
    - `last_planned`: removed (decision 10).
    - Destroy runs keep `destroy_started`/`destroy_finished` as the statuses of their apply phase, even though the command is `apply <planfile>`. The lifecycle predicates (destroying, destroyed, interrupted) then keep working unchanged.
    - **A no-change destroy plan** (nothing left in state) must still record `destroy_finished`. Otherwise the lifecycle never reaches `destroyed`.
12. **Change summary is stored on the run.** The executor computes add/change/destroy counts from `tofu show -json` on the saved plan. Only the counts leave the executor, because the JSON holds sensitive values. `FinishPlan` writes them to `plan_add`, `plan_change` and `plan_destroy` on `template_runs`, and the row button and "Destroy N resources" read them from there.

## PR 1 — run numbers (#265, done)
- Migration `0022_run_numbers.sql` (see decision 7).
- `CreateTemplateRun` in `internal/postgres/repositories.go` assigns the number and returns it.
  - A losing concurrent insert may hit **either** unique index, whichever Postgres checks first, so a violation of the run-number index also maps to `ErrTemplateRunInFlight`.
- `domain.TemplateRun.RunNumber`, `run_number` in the run JSON and in `docs/openapi.yaml`.
- The web shows "Run #N".

## PR 2 — template pages (#266, done)
- Routes, in `web/src/app/router.tsx`:
  - `stacks/:stackId/templates`: the list only, one bordered list whose rows name each template's state.
  - `stacks/:stackId/templates/:stackTemplateId`: tabs `runs` (the index redirects here), `variables`, `credentials` (gated on `canManageAccess`) and `settings`.
  - `…/runs/:runNumber`: run detail, with the Runs tab still highlighted.
  - `templates/new` and `templates/:stackTemplateId/upgrade` replace the old `template/…` routes. The old URLs 404.
- On a template's page its tabs **replace** the stack's, and its state sits at the far end of that tab row (not in the breadcrumb, and not on a run's page).
- Runs tab: a header with Plan and Apply, then one run list. Each row shows state, `#number operation` (the link), who started it and when, and on a live run, Cancel and Approve. The list owns the columns and each row is a CSS subgrid.
- Run detail: a header with state, operation and actions, one row of facts (started, completed, started by, source), then logs.
- Actions a viewer may not take are hidden rather than disabled. Plan and Apply are the exception: they stay visible with a reason.
- Route-mode `RequireCapability` passes its parent's outlet context through, and each tab's content is keyed on the template.

## PR 3 — saved-plan flow + auto-approve (not started)
**Backend**
- Domain and migration: drop `OperationApply` as something that can be started; add `auto_approve`, `plan_artifact_key`, `plan_artifact_dek`, `plan_add`, `plan_change` and `plan_destroy` to `template_runs`. Update the bookkeeping predicates as in decision 11.
- `internal/runner/terraform.go`:
  - plan: `-detailed-exitcode -out=<planfile>`, plus `-destroy` for destroy runs. It returns has-changes and a summary taken from `show -json`.
  - apply: the saved planfile, as in decision 1. `TerraformCommandDestroy` **stays** as a command type and also runs `apply <planfile>`. That keeps `terraformCommandStatusTable` recording `destroy_started`/`destroy_finished` for destroy runs, which the lifecycle predicates need (decision 11).
- `internal/artifacts/store.go`:
  - `PlanKey` → `tenants/{tenant}/runs/{run}/plan/tfplan.tar.gz.enc`, the same layout as `LogKey`.
  - `DeleteObject` for both the filesystem and S3 stores.
  - With `ARTIFACT_STORE_KIND=filesystem`, every executor must share the root, because the apply can land on a different host from the plan. Document this next to the setting in `.env.example`.
- `internal/activities`:
  - Executor activities: `UploadPlan`, `DownloadPlan` (puts the lock file back before init), and `CleanupWorkspace` (nothing deletes run workspaces today).
  - Control activities: `SealPlanKey`, `DeletePlanArtifact`, and `FinishPlan`. `FinishPlan` is one transaction that writes the change counts and then takes one of three branches:
    - **no changes**: `completed` plus `last_applied` (decision 3). For a destroy run it records `destroy_finished` then `completed`, so the lifecycle reaches `destroyed`.
    - **changes**: `waiting_approval`.
    - **changes + auto-approve**: `waiting_approval`, then the same approval write, audit event and start-apply enqueue as `ApproveRun`, through one shared function.
- `internal/workflows/template_run.go`:
  - Split into plan and apply workflows with a shared `withSession(fn)` helper (CreateSession → fn → releaseKey → CleanupWorkspace → CompleteSession) and shared terminal error and cancel handling.
  - The apply phase records only `apply_started → apply_finished`, or `destroy_*` for destroy runs. `approved` is already written by `approveTemplateRun` inside the approval transaction (~L1207), so the workflow must not write it again.
  - The apply session gets a longer `CreationTimeout` than 1 minute. The run was just approved, and failing it because an executor was briefly full would push the user through a whole re-plan.
  - Delete `waitForApproval`.
- `internal/app/service.go`:
  - `AutoApprove` on the start command.
  - `ApproveRun` requires `waiting_approval` and enqueues `KindStartTemplateApply`, which replaces `signal_run_approval_handler.go`.
  - **Cancel before the apply.** A run in `waiting_approval`, or `approved` but not yet claimed by its apply, has no workflow to signal. `CancelRun` first tries `cancelTemplateRunBeforeApply`, a conditional write from those two statuses straight to `canceled` that also drops the plan key; only if that changes nothing does it take the signal path. (Built this way instead of the planned "treat not-found as success": the existing reconciliation of a not-found cancel marks the run `failed`, which is wrong for a discard.)
  - **The apply claims its run.** The apply phase's first step is `BeginApply`, a conditional `approved → locked`. The two conditional writes on the same row mean a cancel and an apply that race have exactly one winner: a lost claim ends the workflow quietly, since the cancel already recorded the run's end.
  - A cancel that arrives while the plan runs is carried out by `FinishTemplatePlan`, which sees `cancel_requested` and cancels.
- `internal/temporal/dispatcher.go`:
  - Add `StartTemplateApply`, and delete `ApproveTemplateRun` (the signal).
  - `CancelTemplateRun` signals the plan workflow and, if that has closed, the apply workflow; at most one is running. If neither is, the NotFound goes back to the handler, which reconciles as before.
  - Register both workflows on the control worker.
- **Deleting the saved plan:**
  - The apply session's `CleanupWorkspace` deletes it, with the workspace, whether the apply succeeded or not.
  - A discarded, failed or crashed plan leaves its bundle behind: there is no deletion job (dropped from the plan). Its key is nulled when the run turns terminal, so the bundle can never be read. A sweeper for these is follow-up work.
  - The session is torn down (release key, cleanup, complete) before the run's final statuses are recorded, so it is held no longer than the Terraform work needs.

**Web**
- The Runs tab header loses Apply. Plan gains the auto-approve checkbox (only with `canApprove`), and the `hasFreshPlan` gating goes away.
- On a waiting run, the row's Approve becomes "Apply" / "Destroy" with its change summary, and run detail's header does the same (decision 8).
- The destroy panel follows decision 9.

**Docs:** update `docs/apply-run-sequence.md`. #249 is closed by this PR, and so is the approval-integrity follow-up it names. #248 is no longer a prerequisite.

## Verification
- PR 1 (done): postgres tests that numbering counts 1, 2, 3 per template and is independent per template and tenant, that get and list return the number, and that the migration numbers existing runs in start order.
- PR 2 (done): `cd web && npm test && npm run build`, plus screenshots at 1440px and a true 390px viewport of each page.
- PR 3:
  - Workflow tests in the Temporal test env:
    - a plan with changes parks at `waiting_approval` with no open session;
    - a no-change plan completes;
    - approval runs apply with `DownloadPlan` before init;
    - discarding a waiting plan deletes the saved plan;
    - auto-approve goes straight to apply.
  - Runner argument tests: exit 2 means has-changes, and apply gets no `-var`.
  - App and API:
    - auto-approve without `can_approve` returns 403;
    - approving a run that isn't waiting returns `ErrRunNotApprovable`;
    - approving when the waiting run's snapshot ≠ desired returns `ErrStackTemplatePlanStale` (set desired directly in the fake repo, since the UI path is blocked);
    - cancel in `approved` before the apply workflow exists ends `canceled`, and so does cancel after it starts.
  - Postgres:
    - `last_applied` is set on `apply_finished` of a plan run, and on a no-change plan's `completed`;
    - `plan_artifact_dek` is null on every terminal status;
    - `PlanState()` is `matches` while a plan waits, and `none` after it is discarded or applied;
    - config save and upgrade return `ErrTemplateRunInFlight` while a plan waits;
    - a no-change destroy ends with lifecycle `destroyed`;
    - discarding a waiting destroy leaves lifecycle `active`.
  - Postgres-backed tests run with `docker compose up -d postgres`. `make differential-test` isn't needed, because nothing here touches the authorization write path.
  - End to end with compose, api, executor and the UI:
    - apply.log applies the saved plan with no second plan;
    - drift between plan and approve fails with "Saved plan is stale";
    - destroy shows its plan before the danger button;
    - the checkbox is hidden for a user with operate-only access.
