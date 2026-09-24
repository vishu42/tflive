# Run workflow, jobs and steps: where a write belongs is a matter of type

## Problem

A template run makes four kinds of control-plane write:

| Write | What it records |
|---|---|
| status | lifecycle |
| step | progress |
| event | a stack-template side effect |
| log | a command's log metadata |

It also does executor work in a Temporal session.

Today `internal/workflows/template_run.go` is one struct, `templateRunWorkflow`. Every method can reach every write and both Temporal contexts: the control queue and the executor session. What keeps the code correct is only convention:

- status is written only at the start and end of a phase;
- a step is written before its work;
- executor work writes nothing;
- control writes go to the control queue.

Nothing stops the next edit from breaking any of these. The file has already grown asymmetries: `applyPhase` calls `apply`, but there is no `plan`, and `planPhase` both writes status and runs steps. A reader has to work out from call sites which function may write what.

Rules and comments do not fix this. In Go, privacy stops at the package boundary, so an unexported method cannot restrict other code in the same package. The standard library's answer is to control which values a function is given. An `http.Handler` receives a `ResponseWriter`, never the connection, and a `t.Run` callback receives its own `*testing.T`.

## Decision

Split the run into three types along the control-plane/executor boundary. Each type holds only what its level may use.

| Type | Level | Holds | Can write | Cannot |
|---|---|---|---|---|
| `run` | workflow: the lifecycle | control-queue ctx, input | **status**, and anything below | — |
| `job` | one executor session, as ordered steps | a `recorder` (steps, events, logs; control-queue ctx) and a `*session` | **steps, events, logs** | status: it has no status write |
| `session` | executor work | executor-session ctx, a `sealer`, workspace state | **nothing** | any run-state write |

The types make the rules structural:

- **`job.step(step, work func(*session) error)` is the only way to reach the session.** No executor work runs outside a named step, and every step is recorded before its work starts.
- **Phase code receives a `*job`**, not the run, so it cannot change status.
- **`session` holds no recorder**, so executor work cannot record anything.
- **Each type holds only its own context.** `session` holds only the executor-session context, and `run` and `recorder` hold only the control-queue context. Queue routing follows from the types.
- **`sealer` is the one control-plane dependency executor work has.** It seals secrets to the session's key and can create the run's plan key. It has no status, step or event write.

The workflow functions read as the lifecycle:

- **`TemplatePlanWorkflow`:** `start` (validate; queued → running), `runJob(plan)`, `settlePlan` (FinishPlan; completed, or left waiting), with `finish` recording `failed` on any error.
- **`TemplateApplyWorkflow`:** `claim` (validate; BeginApply), `runJob(apply)`, `complete`.
- **`plan(j *job)` and `apply(j *job)`** are symmetric phase functions. Each prepares the workspace through `prepareWorkspace(j, …)`, then runs its command as a step.

A job is the name for the session-level unit, in the GitHub Actions sense: a workflow has jobs, and a job runs its steps on one runner. The existing word "phase" keeps its meaning, the half of a run (`domain.RunPhase`), and each phase runs one job.

## Constraints

- **Pure restructure.** Temporal activity order, task queues, timeouts, retry policies, payloads and the writes a run makes must not change. The existing workflow tests pin the exact order of every write and are the proof.
- **One package.** Everything stays in `internal/workflows`. Types, not packages, draw the lines.
