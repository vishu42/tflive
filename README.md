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

The MVP uses Temporal OSS as the durable workflow engine, Postgres as the product database, S3-compatible object storage for logs/artifacts, and Vault or another secret store for execution credentials.

The API does not call workers directly. The API creates product records in Postgres, starts or signals Temporal workflows, and serves UI state/log endpoints. Worker pods poll Temporal task queues and run Terraform activities.

```text
 +------------+        +-------------+        +-------------+
 |            |        |             |        |             |
 |    UI      +------->+ API Server  +------->+  Temporal   |
 |            |        |             |        |   Server    |
 +-----+------+        +------+------+        +------+------+ 
       ^                      |                      ^
       | state/log APIs       |                      |
       |                      v                      |
       |              +-------+------+               |
       |              |              |               |
       +--------------+  Postgres    |<--------------+
                      | metadata     | status/activity updates
                      +-------+------+
                              ^
                              |
                              |
                      +-------+--------+
                      | Worker Pods    |
                      | terraform-runs |
                      +-------+--------+
                              |
                +-------------+-------------+
                |                           |
                v                           v
        +-------+------+            +-------+-------+
        | LocalProcess |            | GitHub App    |
        | Terraform    |            | installation  |
        | Runner       |            | tokens        |
        +-------+------+            +---------------+
                |
        +-------+------------------------------------+
        |                                            |
        v                                            v
 +------+-------+                            +-------+-------+
 | Log Sink     +--------------------------->+ Object Storage|
 | stream/store |      redacted logs         | logs/artifacts|
 +------+-------+                            +---------------+
        |
        v
 +------+-------+
 | API live log |
 | stream       |
 +--------------+

 Worker runtime also fetches execution credentials from:

 +---------------+
 | Vault/Secret  |
 | Store         |
 +---------------+
```

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

Each Terraform activity:

- Creates a fresh temporary working directory.
- Clones the template GitHub repository.
- Checks out the selected ref.
- Changes into the template root path.
- Reads `template.yaml` as metadata when needed.
- Runs `terraform init`.
- Selects or creates the assigned Terraform workspace.
- Runs `terraform plan`, `terraform apply`, or `terraform destroy`.
- Streams redacted logs.
- Persists redacted logs.
- Cleans up the temporary working directory.

The runner should be implemented behind an interface so a future `KubernetesJobRunner` or sandboxed runner can be added without changing workflow semantics.

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
terraform init
terraform workspace select <workspace> || terraform workspace new <workspace>
```

The isolation contract is:

```text
backend owned by user/template
workspace owned by platform
```

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

The workflow should acquire a product-level lock before running Terraform so only one active run can target a `StackTemplate` at a time.

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

The UI should communicate that approval is based on the displayed plan, but apply will re-evaluate before execution.

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

Run records should capture the selected template ref, operation type, trigger source, trigger actor, workspace name, lifecycle status, started/completed timestamps, and error summary.

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
