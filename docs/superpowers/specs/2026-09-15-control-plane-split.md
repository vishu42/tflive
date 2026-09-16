# Control plane split: api owns control, executor only executes

Status: implemented on `feat/control-plane-split` (steps 1–5). Run sandboxing
and Temporal access control are out of scope and follow this work (see "Not in
this change").

## Problem

The control plane is split across two binaries, and the half in `cmd/worker`
runs next to tenant Terraform.

`tflive-worker` today holds:

| Code | Plane |
|---|---|
| Queue controller: `work_queue` row → `ExecuteWorkflow` / `SignalWorkflow` | control |
| `TemplateRunWorkflow`, `TemplateSyncWorkflow` | control |
| `RecordTemplateRunStatus`, `RecordTemplateRegistrationStatus`, `SyncTemplate`'s revision upsert, log metadata row | control |
| Credential read + decrypt (`CREDENTIAL_ENCRYPTION_KEY`) | control |
| GitHub App installation tokens (`GITHUB_APP_PRIVATE_KEY`) | control |
| Postgres migrations (also run by the API, unlocked) | control |
| `PrepareWorkspace`, `FetchSource`, `RunTerraform` | **data** |

Only the last row needs to sit next to tofu. Everything else landed in the
worker because it was the process with a Temporal client, and because the
queue used to carry authorization work that has since moved into the API's
transactions (`cmd/worker/main.go:77`).

The cost: a `local-exec` in any template can read `DATABASE_URL` (passed via
`os.Environ()`, `internal/runner/executor.go:34`) and, through
`/proc/1/environ`, the encryption and GitHub App keys that `scrubConsumedSecret`
claims to remove (`CGO_ENABLED=0` means `os.Unsetenv` only edits Go's copy).
That is every tenant's database rows and every tenant's decrypted credentials.

## Target

```
CONTROL PLANE                                                 DATA PLANE
───────────────────────────────────────────────               ────────────────────────────────
tflive-api                                                    tflive-executor
 ├─ HTTP                                                       └─ Temporal worker, queue "execution"
 ├─ queue loop (work_queue → Temporal)        ──start/signal─▶     (sessions enabled)
 └─ Temporal worker, queue "control"          ◀─poll/respond─      ├─ PrepareWorkspace  (+ run keypair)
     ├─ TemplateRunWorkflow, TemplateSyncWorkflow                   ├─ FetchSource       (opens sealed token)
     ├─ RecordTemplateRunStatus, RecordTemplateRunLog               ├─ RunTerraform      (opens sealed creds)
     ├─ SealRunCredentials, SealSourceToken                         └─ ReleaseRunKey     (drops the keypair)
     └─ RecordTemplateRegistrationStatus, SyncTemplate
          │                                     temporal-server
          ▼                                        ▲    ▲
       Postgres (app)                              │    └── executor polls "execution"
                                                   └────── api polls "control", starts, signals
```

Every connection to Temporal is outbound from a tflive process. The executor
connects to Temporal and the artifact store, nothing else: no `DATABASE_URL`,
no keys, no route to the API.

## How a run flows

1. `POST .../runs` commits `template_runs` + a `start_template_run` queue row
   (unchanged).
2. The API's queue loop claims the row and starts `TemplateRunWorkflow` on
   `control`.
3. The API's Temporal worker runs the workflow. Control activities (status
   writes, sealing, log metadata) are scheduled on `control`; the session and
   the three execution activities on `execution`.
4. The executor polls `execution`, runs git and tofu, and responds. It never
   learns that statuses exist.
5. Approval and cancel keep today's path: queue row → API queue loop →
   `SignalWorkflow`.

## Decisions

### D1. Task queue names are constants, environments separate by namespace

`domain.ControlTaskQueue = "control"`, `domain.ExecutionTaskQueue = "execution"`.
`TEMPORAL_TASK_QUEUE` is removed; dev/test isolation uses `TEMPORAL_NAMESPACE`,
which already exists. **Why:** the workflow must name the execution queue in its
activity options; a queue name that comes from env on one side and a constant on
the other fails silently (tasks sit on a queue nobody polls).

### D2. Status and log metadata are control activities

The executor reports results only by completing its activity. `RunTerraform`
returns `{Phase, ObjectKey, SizeBytes}`; the workflow then calls a new
`RecordTemplateRunLog` control activity. `artifacts.RecordedLogStore` stops
writing metadata from the executor.

**Rejected:** executor calls the API (new route and credential on the data
plane, reimplements Temporal's retries, can write out of order with the
workflow); API polls Temporal (lag, per-run load, status is not queryable
state in Temporal).

### D3. Credentials travel sealed to a per-run key

```
execution  PrepareWorkspace   → X25519 keypair, private key kept in memory by run ID
                                → returns {WorkspacePath, PublicKey}
control    SealSourceToken    → installation token for owner/repo, box.SealAnonymous
execution  FetchSource        → opens token, clones
control    SealRunCredentials → reads + decrypts credential rows, seals the env map
execution  RunTerraform       → opens env, runs tofu          (seal again before every command)
execution  ReleaseRunKey      → executor drops the private key (runseal also expires
                                  unreleased keys after 25h, past the 24h session)
```

Temporal history holds only the public key and ciphertext. Sealing again before
each `RunTerraform` keeps today's behavior of reading credentials per command,
which matters because apply can start up to 24h after plan. Sessions already pin
every execution activity to one host, so the host that made the key is the host
that opens the box; if it dies the session fails, as it does today.
`golang.org/x/crypto/nacl/box` is already in the module graph.

**Rejected:** run-scoped token + executor fetches from the API (new route; the
token passes through history where other runs can use it while live); Temporal
payload codec (every executor holds the key that decrypts all history);
plaintext activity results (secrets in history forever).

**Limit:** this protects credentials in Temporal history and in transit. It
does not stop one run reading another run's tofu environment on a shared
executor. That is run sandboxing, not this change.

Nor does it stop a compromised executor from obtaining any tenant's
credentials. A `local-exec` can reach Temporal, which has no access control:
it can poll `control` and answer a workflow task by scheduling
`SealRunCredentials` for another tenant against a public key it holds, or start
a `TemplateRunWorkflow` naming another tenant's stack template. Removing
`DATABASE_URL` and the keys from the executor raises the bar from reading an
environment variable to speaking the Temporal protocol; it does not close the
cross-tenant exposure described in Problem. That needs "Temporal access
control" below.

### D4. Template sync moves wholly to control

`SyncTemplate` clones and parses HCL with `hclparse`; it runs no tenant code.
Keeping it on `control` avoids a sealed-token path for a workflow that has no
session. `Dockerfile.api` needs `apk add git`. Revisit if clone size or parser
exposure becomes a concern.

### D5. The API is the only migrator

The executor has no database, so this falls out. It also removes today's
unlocked `postgres.Migrate` race between api and worker at startup.

### D6. Logs: executor keeps artifact-store write access for now

Filesystem store (compose) stays a shared volume; S3 keeps bucket credentials on
the executor. This leaves cross-tenant log read on the data plane, which is
accepted until sandboxing. Follow-up: control activity mints a presigned PUT per
phase key; `internal/artifacts` has no presign support yet.

## Sequence

Each step is a mergeable PR that leaves runs working. No backfill or version
gating: pre-production, in-flight runs are disposable.

| # | Change | Estimate |
|---|---|---|
| 1 | Queue constants (D1). Workflows route control vs execution activities; the existing worker polls **both** queues. No behavior change. | 2–3 h |
| 2 | API dials Temporal, runs the queue loop and a worker on `control` with both workflows and all control activities (D4, D5). Worker stops polling `control`, drops the queue loop and migrations. `apk add git` in `Dockerfile.api`. | 0.5 day |
| 3 | Log metadata split (D2): `RunTerraform` returns log refs, `RecordTemplateRunLog` on control. | 2–3 h |
| 4 | Sealed credentials and source token (D3). Executor stops reading credentials and minting tokens. | 1 day |
| 5 | Strip the data plane: remove store, cipher, GitHub App key, `DATABASE_URL` from worker config; rename `cmd/worker` → `cmd/executor`, `Dockerfile.worker` → `Dockerfile.executor`; trim compose env (also drop `SESSION_ENCRYPTION_KEY`, `OIDC_CLIENT_SECRET`, which the worker never reads). | 0.5 day |

About 3 days total. Verification after step 5: `docker compose exec executor env`
shows no `DATABASE_URL` or keys, and a plan + approve + apply run completes.

## Not in this change

- **Run sandboxing.** Runs share an executor; one run can read another's tofu
  environment via `/proc/<pid>/environ`, reach Temporal and private networks,
  and exhaust host resources. Plan: per-run gVisor sandbox. Separate doc.
- **Temporal access control.** The executor can still signal any workflow or
  poll `control`. Needs Temporal mTLS plus a server authorizer restricting the
  executor's identity to `execution`. Pairs with sandboxing.
- **`RunTerraform` 10-minute `StartToCloseTimeout`** with `MaximumAttempts: 1`
  kills long applies mid-flight (`internal/workflows/template_run.go:369`).
- **No activity heartbeats**, so a cancel signal never reaches a running tofu;
  the run is recorded canceled while apply continues.

The last two are independent bugs; fix them before or alongside step 1.
