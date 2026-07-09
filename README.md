# Megagega Terraform Platform

Megagega is a Terraform orchestration platform for composing infrastructure stacks from reusable Terraform templates. The MVP focuses on fast, UI-triggered Terraform operations, durable workflow orchestration, metadata persistence, live logs, and a clear isolation model.

This document captures the current system design and product requirements.

## Goals

- Let users register Terraform templates from GitHub repositories.
- Let users create stacks and add templates to those stacks.
- Let users configure non-sensitive Terraform variables from the UI.
- Run Terraform `plan`, `apply`, and `destroy` from the UI.
- Use a Go workflow engine to orchestrate long-running operations.
- Start every Terraform run in a fresh empty working directory.
- Persist product metadata, activity history, run state, logs, and artifacts.
- Use a stable Terraform workspace per installed stack template.
- Keep the MVP fast by running Terraform in worker activity processes, not Kubernetes Jobs.

## Non-Goals For MVP

- Managed Terraform backend provisioning.
- Automatic data sharing between templates in a stack.
- Secret Terraform variable collection in the UI.
- Full RBAC or approval policy engine.
- Multi-provider Git integrations beyond GitHub.
- Kubernetes Job based Terraform execution.
- Persisting binary Terraform plan files for exact approved-plan apply.

## Architecture

The MVP uses Temporal OSS as the durable workflow engine, Postgres as the application product database, S3-compatible object storage for logs/artifacts, and Vault or another secret store for execution credentials. Temporal also has its own persistence database for workflow history, timers, task queues, signals, and retry state.

The architecture also defines a pluggable `EventBus` interface for system events, live log fanout, and future pub/sub needs. The MVP can start with a no-op, in-memory, or simple local implementation, then swap in Redis, NATS JetStream, Kafka, or another broker without changing API or workflow semantics.

The API does not call workers directly. The API creates product records in Postgres, starts or signals Temporal workflows, and serves UI state/log endpoints. Worker pods poll Temporal task queues and run Terraform activities.

```text
 +------------+        +-------------+        +----------------+
 |            |        |             |        |                |
 |    UI      +------->+ API Server  +------->+ Temporal       |
 |            |        |             |        | Server         |
 +-----+------+        +------+------+        +-------+--------+
       ^                      |                       ^
       | state/log APIs       |                       |
       |                      v                       |
       |              +-------+------+                |
       |              | App Postgres |                |
       +--------------+ metadata     |                |
                      +--------------+                |
                                                      |
                                      +---------------+---------------+
                                      |                               |
                                      v                               v
                              +-------+--------+              +-------+-------+
                              | Temporal Task  |              | Worker Pods   |
                              | Queue          |              | terraform-runs|
                              +-------+--------+              +-------+-------+
                                      ^                               |
                                      | workers poll Temporal         |
                                      | server APIs                   |
                                      +-------------------------------+
                                                      |
                                                      v
                                            +---------+---------+
                                            | LocalProcess      |
                                            | Terraform Runner  |
                                            +---------+---------+
                                                      |
                   +----------------+-----------------+------------------+
                   |                |                                    |
                   v                v                                    v
          +--------+------+  +------+-------+                    +-------+-------+
          | GitHub App    |  | Vault/Secret |                    | Log Sink      |
          | installation  |  | Store        |                    | stream/store  |
          | tokens        |  +--------------+                    +-------+-------+
          +---------------+                                             |
                                               +------------------------+----------------+
                                               |                                         |
                                               v                                         v
                                      +--------+-------+                         +-------+--------+
                                      | Object Storage |                         | EventBus      |
                                      | logs/artifacts |                         | Interface     |
                                      +----------------+                         +-------+--------+
                                                                                         |
                                                                                         v
                                                                                +--------+-------+
                                                                                | API live log  |
                                                                                | stream/events |
                                                                                +----------------+

 Temporal Server persists workflow and queue state to:

 +----------------------+
 | Temporal Persistence |
 | DB                   |
 +----------------------+
```

## Local UI Development

Run the backend API and the Vite UI as separate processes.

```text
API: http://localhost:8081
UI:  http://localhost:5173
```

Start backend dependencies:

```bash
docker compose up app-postgres temporal-postgres temporal temporal-ui minio minio-init
```

The local MinIO API is available at `http://localhost:9000`, and the console is
available at `http://localhost:9001`. Credentials and the bucket name are loaded
from `.env`.

Start the API with the local environment:

```bash
set -a
source .env
set +a
go run ./cmd/megagega-api
```

Start the worker in another shell:

```bash
set -a
source .env
set +a
go run ./cmd/megagega-worker
```

Start the UI:

```bash
cd web
npm install
npm run dev
```

The Vite dev server proxies `/v1/*` and `/healthz` to the Go API.

## Core Boundaries

The platform has three important boundaries:

```text
Stack         = composition boundary
StackTemplate = Terraform state boundary
TemplateRun   = execution boundary
```

A `Stack` groups templates and stack-level metadata. A `StackTemplate` represents one template installed into one stack and owns a stable Terraform workspace. A `TemplateRun` represents one `plan`, `apply`, or `destroy` execution against one `StackTemplate`.

`StackRun` exists only for coordinated multi-template operations. The main MVP use case is destroying a whole stack in a controlled order.

## Major Components

### UI

The UI supports:

- Connecting a tenant to GitHub.
- Registering templates from GitHub repository, ref, and path.
- Creating stacks.
- Adding tags to stacks as key-value pairs.
- Adding templates to stacks.
- Entering non-sensitive template input values.
- Triggering `plan`, `apply`, and `destroy`.
- Viewing run lifecycle, activity events, and live logs.
- Pressing a dummy approval button for apply operations.

### API Server

The API server is responsible for:

- Validating requests.
- Using mock user identity for MVP.
- Creating and reading product records in Postgres.
- Starting Temporal workflows.
- Sending approval/cancel signals to Temporal workflows.
- Serving UI-facing run, stack, template, and log endpoints.
- Exposing live log streams to the UI.

The API does not execute Terraform and does not communicate directly with worker processes.

### Temporal

Temporal is the execution coordinator. It owns workflow durability, task queues, retries, timers, cancellation, and approval waits.

The MVP uses one shared task queue:

```text
terraform-runs
```

All worker pods poll this queue. Operation type is represented in run metadata:

```text
plan | apply | destroy
```

### Workers

Workers are Go processes that poll Temporal and execute workflow activities.

For MVP, workers use a local process runner:

```text
LocalProcessRunner
```

Each Terraform-compatible activity uses the OpenTofu CLI (`tofu`) and:

- Creates a fresh temporary working directory.
- Clones the template GitHub repository.
- Checks out the selected ref.
- Changes into the template root path.
- Reads `template.yaml` as metadata when needed.
- Runs `tofu init`.
- Selects or creates the assigned Terraform workspace.
- Runs `tofu plan`, `tofu apply`, or `tofu destroy`.
- Streams redacted logs.
- Persists redacted logs.
- Cleans up the temporary working directory.

The runner should be implemented behind an interface so a future `KubernetesJobRunner` or sandboxed runner can be added without changing workflow semantics.

### Runner Security

The MVP local process runner is optimized for fast feedback, but it should be treated as trusted-template execution. OpenTofu/Terraform providers, provisioners, external data sources, and local-exec style behavior may execute code inside the worker pod with access to the run's injected credentials.

MVP worker deployments should use the following guardrails:

- Run worker containers as non-root.
- Avoid host mounts and shared writable volumes.
- Use fresh per-run working directories with cleanup after every run.
- Apply CPU, memory, ephemeral storage, and wall-clock limits.
- Inject only the credential sets required for the target stack or template run.
- Prefer short-lived credentials wherever supported.
- Avoid persisting secret values to Postgres, object storage, Temporal payloads, or logs.
- Redact known secret values before logs are streamed or persisted.
- Restrict worker network egress where practical.
- Separate workers by task queue or deployment if future tenants, templates, or credential sets require stronger trust boundaries.

Stronger isolation, such as a Kubernetes Job per run, sandboxed containers, remote runners, or per-tenant runner pools, remains a deferred design topic. The `LocalProcessRunner` interface keeps that migration path open without changing workflow semantics.

## Go Package Layout

The Go implementation should keep product concepts, application use cases, adapters, and execution orchestration separated by package boundaries.

```text
cmd/
  megagega-api/
  megagega-worker/

internal/
  traits/
  app/
  api/
  auth/
  postgres/
  temporal/
  workflows/
  activities/
  runner/
  terraform/
  githubapp/
  secrets/
  artifacts/
  events/
  logsink/
  locks/
  config/
```

Package ownership:

- `cmd/megagega-api`: API server boot, config loading, dependency wiring, and HTTP server startup.
- `cmd/megagega-worker`: worker boot, config loading, Temporal worker registration, and activity dependency wiring.
- `internal/traits`: shared product and workflow traits, including IDs, statuses, operation types, validation helpers, entities such as `Tenant`, `Template`, `Stack`, `StackTemplate`, `TemplateRun`, `StackRun`, and `CredentialSet`, plus Temporal workflow payloads, signal names, query names, and constants. Keep this package focused on stable cross-boundary data contracts; concrete behavior and side effects belong in the packages that own them.
- `internal/app`: application use cases such as creating stacks, registering templates, adding templates to stacks, starting runs, approving runs, canceling runs, listing runs, and fetching log metadata. This package owns use-case interfaces for persistence, workflow dispatch, events, locks, artifacts, and secrets; concrete adapters implement those interfaces outside `app`.
- `internal/api`: HTTP handlers, request and response DTOs, routing, SSE endpoints, API validation, and mapping API input into app commands.
- `internal/auth`: mock identity for MVP, tenant/user context extraction, and the future authentication boundary.
- `internal/postgres`: Postgres repositories, transactions, SQL queries, persistence models, and migration helper code.
- `internal/temporal`: Temporal client adapter that implements `app` workflow-dispatch interfaces and is wired in `cmd`. API and app code should depend on interfaces, not on this adapter package directly.
- `internal/workflows`: deterministic Temporal workflow definitions such as `TemplateRunWorkflow`, `TemplateSyncWorkflow`, and future `StackRunWorkflow`.
- `internal/activities`: Temporal activities that perform side effects such as cloning repositories, parsing templates, acquiring locks, running the OpenTofu CLI for Terraform-compatible operations, persisting logs, and writing activity events.
- `internal/runner`: Terraform-compatible runner interface and runner implementations, including the MVP OpenTofu-backed `LocalProcessRunner`.
- `internal/terraform`: Terraform-specific helpers for HCL variable parsing, tfvars rendering, backend metadata extraction, plan summary parsing, and workspace command modeling.
- `internal/githubapp`: GitHub App integration, installation token generation, repository clone helpers, and ref-to-commit resolution.
- `internal/secrets`: secret store boundary, credential set resolution, and Vault or local development adapters.
- `internal/artifacts`: object storage adapter, redacted log artifact writes, plan summary artifacts, and artifact key generation.
- `internal/events`: pluggable `EventBus` interface, event types, and no-op or in-memory implementations.
- `internal/logsink`: log redaction, phase log writers, live log publication, and log metadata creation.
- `internal/locks`: product-level `StackTemplate` lease lock, including acquire, refresh, release, fencing token checks, and stale lock handling.
- `internal/config`: environment and config structs, default values, and config validation.

Temporal workflows should stay thin and deterministic. Workflows decide sequence; activities perform side effects through interfaces.

Workflow code may import `traits` and Temporal SDK packages, but it should not import concrete adapters such as `postgres`, `githubapp`, `secrets`, `artifacts`, or `logsink`.

Package dataflow:

```text
                   shared product and workflow traits
                      +----------------------+
                      | internal/traits      |
                      +----------+-----------+
                                 ^
                                 |
cmd/megagega-api                 |                   cmd/megagega-worker
  config + wiring                |                     config + wiring
        |                        |                           |
        v                        |                           v
+----------------+        +------+-------+          +--------------------+
| internal/api   +------->+ internal/app |          | internal/workflows |
| HTTP/SSE DTOs  |        | use cases    |          | deterministic      |
+-------+--------+        +------+-------+          +---------+----------+
        |                        ^                            |
        |                        | app-owned interfaces       |
        |                        |                            v
        |              +---------+----------+       +--------------------+
        |              | concrete adapters  |       | internal/activities|
        |              +---------+----------+       | side effects       |
        |                        |                  +---------+----------+
        |                        |                            |
        |                        v                            v
        |              postgres, temporal,          runner, terraform,
        |              events, artifacts,           githubapp, secrets,
        |              secrets, locks               artifacts, events,
        |                                             logsink, locks,
        |                                             postgres
        |
        v
internal/auth
tenant/user context
```

## Template Model

Templates are GitHub sourced in MVP.

```text
Template
  tenant_id
  github_installation_id
  repo_owner
  repo_name
  default_ref
  root_path
  metadata from template.yaml
  inferred Terraform variables
```

Template source is identified by:

```text
repo_owner/repo_name
ref
path
```

`template.yaml` is intentionally metadata-only:

```yaml
name: vpc
description: Creates a VPC
tags:
  - network
  - aws
```

It does not define input schema, secret schema, dependency schema, or backend config.

## Template Registration Workflow

Template registration and refresh run as background Temporal workflows.

```text
TemplateSyncWorkflow
  -> generate GitHub App installation token
  -> clone repo/ref into fresh temp dir
  -> validate root path exists
  -> read template.yaml
  -> parse Terraform root module files
  -> extract variable metadata
  -> persist template metadata and inferred variables
  -> mark template active or invalid
```

The UI can show states such as:

```text
pending_validation
validating
active
invalid
```

Terraform variable parsing should use HashiCorp HCL/Terraform parsing libraries, not regex.

Inferred variable metadata includes:

- name
- type expression
- description
- required status
- default presence
- sensitive flag
- validation presence

A variable is considered required when the Terraform variable block has no `default`.

## Stack Model

A stack is a logical infrastructure composition.

```text
Stack
  tenant_id
  name
  slug
  tags
  default credential sets
```

Stack tags are key-value pairs.

```text
environment = production
team        = payments
region      = us-east-1
```

## StackTemplate Model

A `StackTemplate` is one installed template inside one stack.

```text
StackTemplate
  stack_id
  template_id
  selected_ref
  workspace_name
  config_json
  last_applied_run_id
  last_applied_ref
  last_applied_at
  lifecycle state
```

The `StackTemplate` is the Terraform state boundary.

Each `StackTemplate` gets one stable, unique Terraform workspace name when it is created. That workspace remains attached to the `StackTemplate` for its full lifecycle.

Example:

```text
Stack: prod-payments
  vpc      -> workspace mtp_acme_prod_payments_vpc_a13f9c
  eks      -> workspace mtp_acme_prod_payments_eks_b47d21
  postgres -> workspace mtp_acme_prod_payments_postgres_91aa0e
```

The workspace name is:

- generated by the platform
- unique per `StackTemplate`
- stable across all runs
- not user-editable after creation
- included in run metadata and audit logs
- injected into every Terraform run

MVP requires workspace-compatible templates. The runner fails early if it cannot select or create the assigned Terraform workspace.

## Terraform Backend

MVP does not manage Terraform backend infrastructure.

Templates must bring their own backend configuration. The platform does not provision state buckets, state tables, encryption keys, or backend config files.

The platform still enforces workspace selection:

```text
tofu init
tofu workspace select <workspace> || tofu workspace new <workspace>
```

The isolation contract is:

```text
backend owned by user/template
workspace owned by platform
```

MVP templates should use workspace-compatible remote backends. Backend state locking is required where the selected backend supports it, because the platform lock prevents concurrent Megagega runs but does not replace Terraform's backend-level state lock.

Unsupported or risky backend configurations should fail validation or receive an explicit warning before execution. Examples include:

- local backends
- backends that do not support workspaces
- remote backends without state locking when locking is available
- backend configuration that changes unexpectedly between runs

Each run should record backend identity metadata, such as backend type and a normalized backend configuration hash when it can be safely derived without storing secrets. If backend identity changes between runs for the same `StackTemplate`, the system should emit an activity event and surface a warning in the UI.

## Inputs And Secrets

Template input schema is not declared in `template.yaml`.

The platform infers Terraform variables from root module `.tf` files and lets users enter non-sensitive values from the UI.

Non-sensitive values are stored per `StackTemplate`:

```text
StackTemplate.config_json
```

At runtime, these values are rendered into Terraform variable input, such as:

```text
terraform.tfvars.json
```

The platform does not collect secret Terraform input values in MVP. Templates should fetch secret material directly from Vault using Terraform providers/data sources.

Runner secrets are execution credentials, not template input values. Examples:

- Vault credentials
- cloud provider credentials
- GitHub App installation token

These are resolved at runtime from the secret store and injected only into the worker process environment. Known secret values are redacted from logs before streaming or persistence.

## Credential Sets

Credential sets represent execution credentials needed by Terraform.

```text
CredentialSet
  tenant_id
  name
  provider_type
  secret_store_ref
```

Stacks inherit credential sets by default:

```text
Stack default credential sets
  -> inherited by all StackTemplates
```

Future versions may allow `StackTemplate` level credential overrides or additions.

GitHub source access is not modeled as a stack credential. GitHub source access uses the tenant's GitHub App installation.

## GitHub Integration

MVP supports GitHub only.

Tenants connect GitHub by installing a GitHub App. The platform stores installation metadata, not long-lived Git credentials.

```text
GitHubIntegration
  tenant_id
  installation_id
  account_login
  account_type
  status
```

During template registration or execution, the worker generates a short-lived GitHub App installation token and clones the repository over HTTPS.

Selected refs are resolved to immutable commit SHAs during template sync and every run. A `TemplateRun` stores both the user-selected ref and the resolved commit SHA. For apply operations, the post-approval apply uses the same resolved commit SHA that produced the displayed plan unless the user starts a new run.

## TemplateRun Workflow

`TemplateRunWorkflow` is the main execution workflow.

Plan-only flow:

```text
queued
locked
workspace_prepared
source_fetched
init
workspace_selected
planned
lock_released
completed
```

Apply flow:

```text
queued
locked
workspace_prepared
source_fetched
init
workspace_selected
planned
waiting_approval
approved
apply_started
applied
lock_released
completed
```

Destroy flow:

```text
queued
locked
workspace_prepared
source_fetched
init
workspace_selected
destroy_started
destroyed
lock_released
completed
```

Failed runs transition to:

```text
failed
lock_released
```

Canceled runs transition to:

```text
cancel_requested
canceling
canceled
lock_released
```

The workflow should acquire a product-level lock before running Terraform so only one active run can target a `StackTemplate` at a time.

Cancellation is cooperative and must leave the product lock, run status, and logs in a consistent state:

- The API records the cancel request actor and timestamp, then sends a cancellation signal to Temporal.
- If the run is queued or waiting for approval, the workflow can mark it canceled without starting more Terraform work.
- If a Terraform subprocess is active, the worker first sends a graceful interrupt, waits for a bounded shutdown period, then terminates the process if needed.
- Long-running activities heartbeat cancellation progress so Temporal can observe worker liveness.
- Partial logs are flushed to object storage before the run reaches `canceled`.
- The product-level lock is released only during cancellation finalization.
- Canceling an apply or destroy does not imply infrastructure rollback; the next run should use Terraform state and backend locking to determine the current infrastructure state.

## Approval

MVP approval is intentionally simple.

- Apply runs perform a plan first.
- Workflow enters `waiting_approval`.
- UI shows plan logs and summary.
- Any mock authenticated user in the tenant can approve.
- API records the approval actor and timestamp.
- API sends an approval signal to Temporal.
- Workflow resumes and runs apply.

Apply may re-run planning after approval. MVP does not persist binary `tfplan` files for exact approved-plan apply.

The UI should communicate that approval is based on the displayed plan, but apply will re-evaluate before execution. The re-evaluation must use the same resolved commit SHA as the approved plan.

The system should persist a redacted plan summary, preferably generated from Terraform JSON output, as a run artifact. This is not an exact binary `tfplan` approval artifact, but it gives the UI and audit trail a structured summary of the approved intent.

## Logs

Logs are both live streamed and persisted.

```text
Terraform stdout/stderr
  -> redaction
  -> live log stream
  -> object storage
```

The API exposes a live stream endpoint, likely using Server-Sent Events:

```text
GET /runs/{run_id}/logs/stream
```

Object storage keys should be tenant and run scoped:

```text
tenants/{tenant_id}/runs/{run_id}/logs/{phase}.log
```

Example phases:

```text
clone.log
init.log
workspace.log
plan.log
apply.log
destroy.log
```

Postgres stores log metadata, not large log bodies.

## Activity Logs

Activity logs capture user and system actions.

Examples:

- template registered
- template validation failed
- stack created
- template added to stack
- run queued
- lock acquired
- plan completed
- approval submitted
- apply completed
- destroy completed
- lock released

Activity records include actor metadata:

```text
actor_user_id
actor_display_name
actor_email
tenant_id
timestamp
resource_type
resource_id
action
metadata
```

Auth is mocked in MVP, but actor fields are real so the system can adopt proper auth later.

## Persistence

Postgres stores product metadata and queryable state:

- tenants
- mock users
- GitHub integrations
- templates
- template versions or sync attempts
- inferred template variables
- stacks
- stack tags
- stack templates
- workspace names
- credential set references
- template runs
- stack runs
- approvals
- activity events
- log metadata

Run records should capture the selected template ref, resolved commit SHA, operation type, trigger source, trigger actor, workspace name, backend identity metadata, lifecycle status, started/completed timestamps, and error summary.

Object storage stores:

- redacted run logs
- future artifacts

Secret store stores:

- cloud provider execution credentials
- Vault execution credentials
- other runtime credential material

Temporal stores:

- workflow execution history
- timers
- signals
- task queue state
- retry/cancellation state

Postgres remains the product source of truth for the UI.

## Scaling

Workers scale horizontally as Kubernetes deployments.

Each worker pod has local concurrency limits, for example:

```text
max concurrent Terraform activities per pod = 2
```

Total execution capacity is:

```text
worker replicas * per-pod Terraform concurrency
```

If all workers are busy, new tasks remain queued in Temporal until a worker has capacity.

HPA can be added using metrics such as:

- Temporal task queue backlog
- schedule-to-start latency
- active Terraform activity count
- CPU and memory as secondary signals

## Deferred Design Topics

### Data Sharing Within A Stack

Automatic output-to-input wiring is deferred.

Future design should answer:

- how outputs from one `StackTemplate` are persisted
- how secret outputs are classified
- how secret outputs are stored
- how downstream templates reference upstream outputs
- whether dependencies are explicit or inferred from references

### Stronger Runner Isolation

MVP uses local subprocess execution inside worker pods for speed.

Future options:

- Kubernetes Job per `TemplateRun`
- sandboxed containers
- remote execution workers
- per-tenant runner pools

### Authorization

MVP uses mock identity and dummy approval.

Future versions should add:

- real authentication
- tenant membership enforcement
- RBAC
- approval policies
- separate permissions for plan, apply, destroy, and template registration

### Managed Backends

MVP requires templates to bring their own Terraform backend.

Future versions may support managed backend profiles for S3, GCS, AzureRM, or Terraform Cloud.

### Log Retention And Access Rules

The MVP persists redacted logs and log metadata, but detailed retention and access policy design is deferred.

Future design should answer:

- how long run logs are retained
- how object storage log encryption is configured
- how tenant isolation is enforced for log reads
- how max log size and truncation are handled
- how redaction failures are handled
- whether discovered-after-the-fact secrets require log rewriting or deletion

### Variable Input Limits

The MVP infers Terraform variables and supports non-sensitive UI-entered values, but advanced variable editing rules are deferred.

Future design should answer:

- how complex types such as `object`, `map`, `list`, `set`, and `tuple` are edited
- whether unsupported complex values require raw JSON input
- how nullable values are represented
- how Terraform validation metadata is surfaced
- how sensitive variables are displayed as unsupported in the UI

### Observability

The MVP identifies useful scaling metrics, but a full observability contract is deferred.

Future design should answer:

- which structured log fields are required across API, worker, and workflow code
- which run and phase duration metrics are emitted
- how cancellation, failure, and approval wait time are measured
- whether traces connect API requests, Temporal workflows, activities, and log streams

### StackTemplate Lifecycle States

`StackTemplate` has a lifecycle state, but the full lifecycle state machine is deferred.

Future design should answer:

- which states exist for active, destroying, destroyed, failed, and orphaned templates
- whether destroyed templates retain workspace names and history
- how deletion differs from infrastructure destroy
- how lifecycle state affects allowed plan, apply, and destroy operations

### Drift And Refresh Behavior

The MVP supports user-triggered plan, apply, and destroy. Dedicated drift detection and refresh behavior are deferred.

Future design should answer:

- whether normal plan runs are enough for MVP drift visibility
- whether latest plan status should be stored on `StackTemplate`
- whether scheduled drift checks are needed
- how drift findings appear in activity history and the UI

## MVP Summary

The recommended MVP is the minimal local platform architecture:

```text
Temporal OSS + Go SDK
Postgres
S3-compatible object storage
Vault/secret store
GitHub App integration
Go API server
Go Temporal workers
LocalProcessRunner
```

It prioritizes fast feedback and a clean product model while leaving room for stronger runner isolation and richer governance later.
