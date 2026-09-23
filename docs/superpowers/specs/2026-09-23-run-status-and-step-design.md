# Run status is lifecycle; progress is a step

## Problem

`template_runs.status` holds 19 values that mix three different things:

- **Lifecycle states** decide what may happen to a run next: `queued`,
  `waiting_approval`, `approved`, `completed`, `failed`, `canceled`.
- **Progress markers** tell a person where the run is:
  - `workspace_prepared`, `source_fetched`
  - `init_started`, `init_finished`, `workspace_selected`
  - `plan_started`, `plan_finished`
  - `apply_started`, `apply_finished`
  - `destroy_started`, `destroy_finished`
- **Leftovers**: `locked` and `lock_released`. They lock and release nothing.
  `locked` is used by `BeginApply` to claim a run.

The setup markers are written only after their step finishes. So during a slow
step the run shows the step before it. A git clone, for example, shows as
`workspace_prepared`, and waiting for an executor shows as `approved`.

Three progress writes also have hidden side effects on `stack_templates`:

| Status write | Side effect |
|---|---|
| `apply_finished` | records the last-applied run |
| `destroy_started` | moves the lifecycle to destroying |
| `destroy_finished` | moves the lifecycle to destroyed |

Every progress tweak needs a status migration. Every new status also has to be
kept in line with `Terminal()`, the in-flight index predicate, and the UI.

## Decision

1. **Status is lifecycle only, 7 values:**
   `queued → running → waiting_approval → approved → running → completed | failed | canceled`.
   - `running` replaces `locked`, and `lock_released` is dropped.
   - The workflow's status writes are guarded transitions. The store checks the
     state a run is moving from. A write to the status the run already has
     succeeds without changing anything, so a retried write is safe.
2. **Step is a separate column.** It shows what a running run is doing right now.
   - It is written when a step starts. There are no "finished" markers.
   - It is kept when the run ends, so a failed run shows where it failed.
   - Nothing branches on it.
   - `''` means the run has not started a step.
   - It is persisted because the API serves runs from Postgres, and a finished
     run must still show its step.
3. **Stack-template side effects become explicit run events** that the workflow
   records: `applied`, `destroying` and `destroyed`. An auto-approved apply
   reports its change counts with `applied`.

## Steps

| Step | Recorded right before | Phase | UI |
|---|---|---|---|
| `waiting_for_executor` | `CreateSession` | both | Waiting for an executor |
| `preparing_workspace` | `PrepareWorkspace` | both | Preparing workspace |
| `fetching_source` | `SealSourceToken` + `FetchSource` | both | Fetching source |
| `restoring_plan` | `SealPlanKey` + `DownloadPlan` | apply (approved) | Restoring saved plan |
| `initializing` | `terraform init` | both | Initializing |
| `selecting_workspace` | `terraform workspace select` | both | Selecting workspace |
| `planning` | plan, plan -destroy | plan | Planning |
| `saving_plan` | `SealPlanKey` + `UploadPlan` | plan (with changes) | Saving plan |
| `applying` | apply, destroy, auto-approved apply | apply | Applying |

- **No separate `destroying` step.** The run's operation already says whether it
  is a destroy.
- **Bookkeeping is folded in.** A sealing activity belongs to the step whose
  executor work it unlocks.
- **Teardown is not a step.** Recording it would overwrite the step a failed run
  needs to show.
- **The apply phase records every setup step again.** With a current-step field
  that is true and harmless, so `setupRecorded` goes away.

## Out of scope

- **Step history.** A table of steps with timestamps can wait until someone
  needs durations.
- **Renaming `canceled`.**
