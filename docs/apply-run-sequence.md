# Apply run sequence

How an apply run moves between the API, the control worker, Temporal, the
executors and Postgres, from the Apply click until the run reads `completed`.
An apply run plans first and saves the plan; approving it applies exactly that
saved plan, in a second workflow that usually lands on a different executor.
A destroy run goes the same way with a plan to destroy. A plan run stops after
the plan, and an auto-approved apply run skips it (both below). The design is in
[the saved-plan flow spec](superpowers/specs/2026-09-21-saved-plan-flow-design.md).
Background on why the processes are split this way is in
[the control plane split spec](superpowers/specs/2026-09-15-control-plane-split.md).

## Who connects to whom

Every arrow points from the process that opens the connection. Temporal never
opens a connection to anything.

```mermaid
flowchart LR
    UI[UI]
    subgraph api["cmd/api (control plane)"]
        HTTP[HTTP handlers]
        QL[Queue loop]
        CW["Control worker<br/>polls 'control'"]
    end
    PG[(App Postgres)]
    T[Temporal server]
    subgraph exec["cmd/executor (data plane)"]
        EX["Execution worker<br/>polls 'execution'"]
        TOFU[tofu]
    end
    AS[(Artifact store)]
    CLOUD[Cloud APIs and state backends]

    UI --> HTTP
    HTTP --> PG
    QL -->|claim intents| PG
    QL -->|start workflows| T
    CW -->|poll, respond| T
    CW -->|status, logs, credentials| PG
    EX -->|poll, respond| T
    EX -->|upload logs, saved plans| AS
    HTTP -->|read logs| AS
    EX --> TOFU
    TOFU --> CLOUD
```

The executor has no arrow to Postgres or to the API. Everything it needs arrives
through Temporal, and everything it produces goes back the same way.

## Reading the diagrams

- **Nothing calls the executor or the control worker.** Both keep a poll open
  against Temporal. A dashed arrow from Temporal is the server answering that
  poll, not a push.
- **Workflow code never touches Postgres.** It schedules an activity on the
  `control` queue; the control worker runs that activity and writes the row.
- **The API and the control worker are one process** (`cmd/api`). They are
  drawn apart because they play different roles: HTTP and the queue loop on one
  side, polling `control` on the other.
- **Every step is a round trip.** The workflow replies to a workflow task with a
  command ("schedule this activity"), Temporal hands that activity to a poller,
  the poller reports the result, and Temporal queues the next workflow task.

## Part 1: from Apply to tofu applying the saved plan

```mermaid
sequenceDiagram
    autonumber
    participant UI
    participant API as API (HTTP + queue loop)
    participant PG as Postgres
    participant T as Temporal server
    participant CW as Control worker
    participant EX1 as Executor A
    participant EX2 as Executor B
    participant AS as Artifact store

    UI->>API: POST /runs {operation: apply}
    API->>PG: INSERT run (queued) + work_queue row, one transaction
    API-->>UI: 201 queued

    Note over API: queue loop claims the row
    API->>T: StartWorkflow TemplatePlanWorkflow on "control"
    T-->>CW: workflow task
    CW->>T: create session on "execution"
    T-->>EX1: session created, pinned to executor A

    Note over CW,EX1: PrepareWorkspace (run key A), SealSourceToken, FetchSource, init, select workspace, then plan -out=tfplan -detailed-exitcode. Exit 2 means changes, and show -json counts them.
    CW->>PG: SealPlanKey creates the run's plan key (stored encrypted), sealed to key A
    EX1->>AS: UploadPlan: tfplan + lock file, AES-GCM under the plan key
    Note over CW,EX1: ReleaseRunKey, CleanupWorkspace, session completed. Executor A is free.
    CW->>PG: FinishPlan: counts, waiting_approval, template's pending plan
    Note over CW: TemplatePlanWorkflow completes. Nothing waits on a person.

    UI->>API: POST /approval
    API->>PG: approved + audit + work_queue row, one transaction
    Note over API: queue loop claims the row
    API->>T: StartWorkflow TemplateApplyWorkflow ("…/apply")
    T-->>CW: workflow task
    CW->>PG: BeginApply: approved to running, or stop if discarded meanwhile
    CW->>T: create session on "execution"
    T-->>EX2: session created, usually another executor

    Note over CW,EX2: PrepareWorkspace (run key B), FetchSource at the same commit
    CW->>PG: SealPlanKey reads the same plan key, sealed to key B
    EX2->>AS: DownloadPlan: open, put tfplan and lock file back
    Note over CW,EX2: init installs the providers the plan was made with, select workspace, SealRunCredentials
    CW->>T: schedule RunTerraform(apply) on the session queue
    T-->>EX2: activity task {sealed env}
    Note over EX2: tofu apply tfplan, upload apply.log
```

Credentials, the source token and the plan key cross Temporal only as
ciphertext sealed to a key the executor generated in `PrepareWorkspace`. The
plan phase and the apply phase each generate their own, so the control plane
seals everything twice, once to each.

A plan run (`{operation: plan}`) takes the same plan phase but keeps nothing:
no `SealPlanKey`, no `UploadPlan`. `FinishPlan` records its counts without
making it the template's pending plan, and the run completes. Nothing waits for
approval.

An auto-approved apply run (`{operation: apply, auto_approve: true}`) has no
plan phase. `StartTemplateRun` records the approval's audit event and queues
`TemplateApplyWorkflow` directly. `BeginApply` claims the run from `queued`,
the apply phase records its setup steps the way a plan phase would, skips
`DownloadPlan`, and runs `tofu apply -auto-approve` with the run's variables.
The counts come from the apply's "Apply complete!" line and are recorded with
the `applied` event. A destroy is never auto-approved.

A plan with no changes stops at `FinishPlan`, which records the run's snapshot
as live, and the run completes. That includes a plan run.

Discarding a plan waiting for approval, or approved but not yet claimed, is a
single write that ends the run `canceled`: no workflow is running then. The
claim and the discard make conditional updates on the same row, so exactly one
wins. Once `BeginApply` has claimed the run there is nothing left to discard,
and a run planning or applying cannot be stopped.

## Part 2: after `RunTerraform(apply)` succeeds

```mermaid
sequenceDiagram
    autonumber
    participant EX as Executor
    participant T as Temporal server
    participant CW as Control worker
    participant PG as Postgres
    participant API as API (HTTP)
    participant UI

    Note over EX: tofu apply exited 0
    EX->>T: activity completed {Log: apply.log metadata}
    Note over T: append ActivityTaskCompleted to history, queue workflow task on "control"

    T-->>CW: workflow task
    Note over CW: workflow resumes after RunTerraform
    CW->>T: schedule RecordTemplateRunLog
    T-->>CW: activity task
    CW->>PG: UPSERT template_run_logs (apply)
    CW->>T: activity completed

    T-->>CW: workflow task
    CW->>T: schedule RecordTemplateRunEvent(applied)
    T-->>CW: activity task
    CW->>PG: stack template last applied, with the run row locked
    CW->>T: activity completed

    T-->>CW: workflow task
    CW->>T: schedule ReleaseRunKey on the session queue
    T-->>EX: activity task
    Note over EX: forget the run private key
    EX->>T: activity completed

    T-->>CW: workflow task
    CW->>T: schedule CleanupWorkspace {DeletePlan} on the session queue
    T-->>EX: activity task
    Note over EX: delete the run workspace and the saved plan
    EX->>T: activity completed

    T-->>CW: workflow task
    CW->>T: complete session
    T-->>EX: session completion task
    EX->>T: done, session slot freed

    T-->>CW: workflow task
    CW->>T: schedule RecordTemplateRunStatus(completed)
    T-->>CW: activity task
    CW->>PG: UPDATE template_runs status (completed also drops the plan key)
    CW->>T: activity completed

    T-->>CW: workflow task
    CW->>T: CompleteWorkflowExecution
    Note over T: workflow closed

    UI->>API: GET /template-runs/{id}
    API->>PG: SELECT run
    PG-->>API: status completed
    API-->>UI: completed
```

The executor's part ends at step 1, apart from releasing its key, cleaning up
and closing the session, which now happen before the final status so the
session is never held a moment longer than the Terraform work needs. Every write after that is a workflow step on the control worker,
which is what keeps them ordered: the log row cannot land after `completed`.
Recording the `applied` event is what records the stack template's last
applied revision (`RecordTemplateRunEvent`, `internal/postgres/run_progress.go`).

## When the control worker is down

The executor does not depend on the control worker to finish an apply. If no
control worker is polling when step 1 happens, the result waits in Temporal's
history and Postgres still says `running` (step `applying`). Nothing is lost: the workflow
resumes at step 2 as soon as a control worker polls again. The UI lags reality
for that long, because it only ever reads Postgres.

```mermaid
sequenceDiagram
    autonumber
    participant EX as Executor
    participant T as Temporal server
    participant CW as Control worker
    participant PG as Postgres
    participant UI as UI (via API)

    Note over CW: control worker is down
    EX->>T: activity completed {Log: apply.log metadata}
    Note over T: append ActivityTaskCompleted, queue workflow task on "control". No poller, so it waits.

    UI->>PG: GET run (through the API)
    PG-->>UI: running (step applying), stale but not wrong

    Note over CW: control worker restarts
    CW->>T: poll "control"
    T-->>CW: workflow task (full history)
    Note over CW: replay history, resume after RunTerraform
    CW->>T: schedule RecordTemplateRunLog
    Note over CW,PG: continues exactly as Part 2, from step 3
    CW->>PG: log row, applied, completed

    UI->>PG: GET run (through the API)
    PG-->>UI: completed
```

The executor finished at step 1, which is why the worker that picks the run
back up needs nothing from it except the log metadata already in history. The
session stays open on that executor until the teardown after the `applied`
event completes it.

## Where this lives in code

| Step | Code |
|---|---|
| Queue loop, control worker wiring | `cmd/api/main.go` (`startControlPlane`, `registerControl`) |
| Workflows (plan phase, apply phase) | `internal/workflows/template_run.go` |
| Saved plan bundle and encryption | `internal/planbundle` |
| Plan key, finish plan, apply claim | `internal/postgres/saved_plans.go` |
| Control activities | `internal/activities/control.go` |
| Execution activities | `internal/activities/template_run.go` |
| Executor wiring | `cmd/executor/main.go` |
| Sealing | `internal/runseal` |
| Queue names | `domain.ControlTaskQueue`, `domain.ExecutionTaskQueue` |
