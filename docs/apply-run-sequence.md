# Apply run sequence

How an apply run moves between the API, the control worker, Temporal, the
executor and Postgres, from the request until the run reads `completed`.
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
    QL -->|start workflow, signal| T
    CW -->|poll, respond| T
    CW -->|status, logs, credentials| PG
    EX -->|poll, respond| T
    EX -->|upload logs| AS
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

## Part 1: from the apply request to tofu running

```mermaid
sequenceDiagram
    autonumber
    participant UI
    participant API as API (HTTP + queue loop)
    participant PG as Postgres
    participant T as Temporal server
    participant CW as Control worker
    participant EX as Executor

    UI->>API: POST /runs {operation: apply}
    API->>PG: INSERT run (queued) + work_queue row, one transaction
    API-->>UI: 201 queued

    Note over API: queue loop claims the row
    API->>T: StartWorkflow TemplateRunWorkflow on "control"
    T-->>CW: workflow task
    CW->>T: create session on "execution"
    T-->>EX: session created, pinned to this executor

    Note over CW,EX: PrepareWorkspace returns the run public key, then SealSourceToken, FetchSource, init, select workspace, plan. Each is a round trip, and each status write goes CW to PG.

    CW->>PG: status waiting_approval (via RecordTemplateRunStatus)
    Note over CW: workflow parks on the approval signal

    UI->>API: POST /approval
    API->>PG: approval row + work_queue row, one transaction
    Note over API: queue loop claims the row
    API->>T: SignalWorkflow approval
    T-->>CW: workflow task (signal arrived)

    CW->>T: schedule SealRunCredentials on "control"
    T-->>CW: activity task
    CW->>PG: read credential rows
    Note over CW: decrypt, seal to run public key
    CW->>T: activity completed {sealed env}
    T-->>CW: workflow task

    CW->>T: schedule RunTerraform(apply) on the session queue
    T-->>EX: activity task {sealed env}
    Note over EX: open with private key, run tofu apply, upload apply.log
```

Credentials cross Temporal only as ciphertext sealed to a key the executor
generated in `PrepareWorkspace`. They are sealed again before every Terraform
command, because an apply can start up to a day after its plan.

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
    CW->>T: schedule RecordTemplateRunStatus(apply_finished)
    T-->>CW: activity task
    CW->>PG: status apply_finished + stack template last applied, one transaction
    CW->>T: activity completed

    loop lock_released, then completed
        T-->>CW: workflow task
        CW->>T: schedule RecordTemplateRunStatus
        T-->>CW: activity task
        CW->>PG: UPDATE template_runs status
        CW->>T: activity completed
    end

    T-->>CW: workflow task
    CW->>T: schedule ReleaseRunKey on the session queue
    T-->>EX: activity task
    Note over EX: forget the run private key
    EX->>T: activity completed

    T-->>CW: workflow task
    CW->>T: complete session
    T-->>EX: session completion task
    EX->>T: done, session slot freed

    T-->>CW: workflow task
    CW->>T: CompleteWorkflowExecution
    Note over T: workflow closed

    UI->>API: GET /template-runs/{id}
    API->>PG: SELECT run
    PG-->>API: status completed
    API-->>UI: completed
```

The executor's part ends at step 1, apart from releasing its key and closing
the session. Every write after that is a workflow step on the control worker,
which is what keeps them ordered: the log row cannot land after `completed`, and
a cancel that arrives during the apply cannot be overwritten by a late
`apply_finished`. Writing `apply_finished` also records the stack template's
last applied revision in the same transaction
(`recordsStackTemplateLastApplied`, `internal/postgres/repositories.go`).

## When the control worker is down

The executor does not depend on the control worker to finish an apply. If no
control worker is polling when step 1 happens, the result waits in Temporal's
history and Postgres still says `apply_started`. Nothing is lost: the workflow
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
    PG-->>UI: apply_started, stale but not wrong

    Note over CW: control worker restarts
    CW->>T: poll "control"
    T-->>CW: workflow task (full history)
    Note over CW: replay history, resume after RunTerraform
    CW->>T: schedule RecordTemplateRunLog
    Note over CW,PG: continues exactly as Part 2, from step 3
    CW->>PG: log row, apply_finished, lock_released, completed

    UI->>PG: GET run (through the API)
    PG-->>UI: completed
```

The executor finished at step 1, which is why the worker that picks the run
back up needs nothing from it except the log metadata already in history. The
session stays open on that executor until the session completes (steps 22 to 24
of Part 2).

## Where this lives in code

| Step | Code |
|---|---|
| Queue loop, control worker wiring | `cmd/api/main.go` (`startControlPlane`, `registerControl`) |
| Workflow | `internal/workflows/template_run.go` |
| Control activities | `internal/activities/control.go` |
| Execution activities | `internal/activities/template_run.go` |
| Executor wiring | `cmd/executor/main.go` |
| Sealing | `internal/runseal` |
| Queue names | `domain.ControlTaskQueue`, `domain.ExecutionTaskQueue` |
