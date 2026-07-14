# Template Registration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build async public GitHub template registration with immutable template rows, registration attempts, Terraform variable inference, and no sensitive variable support.

**Architecture:** The API creates a pending `TemplateRegistration` and dispatches `TemplateSyncWorkflow`. The worker runs side-effecting activities that clone a public repo/ref, resolve the commit SHA, parse metadata and variables, persist or reuse an immutable `Template`, persist variables, and update the registration attempt status.

**Tech Stack:** Go 1.24, net/http, pgx/Postgres migrations, Temporal Go SDK, `gopkg.in/yaml.v3`, HashiCorp HCL parser, local `git` subprocesses behind an injectable interface.

---

### Task 1: Shared Traits And App Ports

**Files:**
- Modify: `internal/traits/traits.go`
- Modify: `internal/traits/traits_test.go`
- Modify: `internal/app/service.go`
- Modify: `internal/app/service_test.go`

- [ ] **Step 1: Write failing traits and app tests**

Add tests for `TemplateRegistrationStatus.Valid`, registration workflow/activity constants, `RegisterTemplate`, `GetTemplateRegistration`, and `GetTemplateVariables`.

Run:

```bash
go test ./internal/traits ./internal/app
```

Expected: FAIL because the registration status type, registration entity, repository ports, service fields, and service methods do not exist yet.

- [ ] **Step 2: Add trait types**

Add `TemplateRegistrationID`, `TemplateRegistrationStatus`, `TemplateRegistration`, JSON tags on template-facing structs, `TemplateSyncActivityInput`, `TemplateSyncActivityOutput`, and `TemplateRegistrationStatusActivityInput`.

- [ ] **Step 3: Add app ports and service methods**

Add app repository ports:

```go
type TemplateRegistrationRepository interface {
	CreateTemplateRegistration(context.Context, traits.TemplateRegistration) error
	GetTemplateRegistration(context.Context, traits.TenantID, traits.TemplateRegistrationID) (traits.TemplateRegistration, error)
	RecordTemplateRegistrationStatus(context.Context, traits.TemplateRegistrationStatusActivityInput) error
}

type TemplateRepository interface {
	UpsertTemplateWithVariables(context.Context, traits.Template, []traits.TemplateVariable) (traits.Template, error)
	GetTemplateVariables(context.Context, traits.TenantID, traits.TemplateID) ([]traits.TemplateVariable, error)
}
```

Extend `WorkflowDispatcher` with `StartTemplateSync`.

Add `RegisterTemplate`, `GetTemplateRegistration`, and `GetTemplateVariables`.

- [ ] **Step 4: Run tests**

Run:

```bash
go test ./internal/traits ./internal/app
```

Expected: PASS.

### Task 2: API Registration Endpoints

**Files:**
- Modify: `internal/api/server.go`
- Modify: `internal/api/server_test.go`
- Modify: `cmd/tflive-api/main.go`
- Modify: `cmd/tflive-api/main_test.go`

- [ ] **Step 1: Write failing API tests**

Add tests for:

- `POST /v1/tenants/{tenant_id}/templates`
- invalid JSON and invalid request mapping
- `GET /v1/tenants/{tenant_id}/template-registrations/{registration_id}`
- `GET /v1/tenants/{tenant_id}/templates/{template_id}/variables`
- API composition root wiring the Postgres store into new service fields

Run:

```bash
go test ./internal/api ./cmd/tflive-api
```

Expected: FAIL because routes and wiring do not exist.

- [ ] **Step 2: Add handlers and DTOs**

Add request struct:

```go
type registerTemplateRequest struct {
	RepoOwner   string `json:"repo_owner"`
	RepoName    string `json:"repo_name"`
	SourceRef   string `json:"source_ref"`
	RootPath    string `json:"root_path"`
	RequestedBy string `json:"requested_by"`
}
```

Map API fields into `app.RegisterTemplateCommand`.

- [ ] **Step 3: Update API wiring**

Extend `appRepositories` with `TemplateRegistrationRepository` and `TemplateRepository`, then wire `TemplateRegistrations: store` and `Templates: store`.

- [ ] **Step 4: Run tests**

Run:

```bash
go test ./internal/api ./cmd/tflive-api
```

Expected: PASS.

### Task 3: Postgres Schema And Repositories

**Files:**
- Add: `internal/postgres/migrations/0003_template_registration.sql`
- Modify: `internal/postgres/repositories.go`
- Modify: `internal/postgres/store_test.go`

- [ ] **Step 1: Write failing Postgres tests**

Add tests for new tables, interface assertions, creating/reading registrations, recording registration statuses, upserting/reusing immutable templates by resolved SHA, inserting variables for new templates, and tenant-scoped variable reads.

Run:

```bash
go test ./internal/postgres
```

Expected: FAIL because tables and repository methods do not exist.

- [ ] **Step 2: Add migration**

Create `templates`, `template_registrations`, and `template_variables` with check constraints for known statuses and a unique index on:

```text
tenant_id, repo_owner, repo_name, root_path, resolved_commit_sha
```

- [ ] **Step 3: Add repository methods**

Implement:

- `CreateTemplateRegistration`
- `GetTemplateRegistration`
- `RecordTemplateRegistrationStatus`
- `UpsertTemplateWithVariables`
- `GetTemplateVariables`

Use transactions for template upsert plus variable insert.

- [ ] **Step 4: Run tests**

Run:

```bash
go test ./internal/postgres
```

Expected: PASS, skipping real Postgres integration tests when `tflive_POSTGRES_TEST_DSN` is unset.

### Task 4: Temporal Dispatcher And Workflow

**Files:**
- Modify: `internal/temporal/dispatcher.go`
- Modify: `internal/temporal/dispatcher_test.go`
- Add: `internal/workflows/template_sync.go`
- Add: `internal/workflows/template_sync_workflow_test.go`

- [ ] **Step 1: Write failing tests**

Add tests for:

- `StartTemplateSync` workflow name, task queue, workflow ID, and input
- `templateSyncWorkflowID`
- workflow status sequence for completed registration
- workflow marking invalid when `SyncTemplate` returns invalid output
- workflow marking failed when `SyncTemplate` errors

Run:

```bash
go test ./internal/temporal ./internal/workflows
```

Expected: FAIL because dispatcher and workflow do not exist.

- [ ] **Step 2: Add dispatcher method**

Use deterministic workflow ID:

```text
template-sync/{tenant_id}/{registration_id}
```

- [ ] **Step 3: Add workflow**

Workflow sequence:

1. `RecordTemplateRegistrationStatus(running)`
2. `SyncTemplate`
3. `RecordTemplateRegistrationStatus(completed|invalid|failed)`

For unexpected activity errors, record failed and return the original error so Temporal marks the workflow failed.

- [ ] **Step 4: Run tests**

Run:

```bash
go test ./internal/temporal ./internal/workflows
```

Expected: PASS.

### Task 5: Template Sync Activity And Parsers

**Files:**
- Add: `internal/activities/template_sync.go`
- Add: `internal/activities/template_sync_test.go`
- Modify: `go.mod`
- Modify: `go.sum`

- [ ] **Step 1: Add HCL dependency**

Run:

```bash
go get github.com/hashicorp/hcl/v2@latest
```

Expected: `go.mod` and `go.sum` include HashiCorp HCL packages.

- [ ] **Step 2: Write failing activity tests**

Add tests for:

- status recorder delegation
- public clone URL construction through fake git runner
- resolved commit SHA copied into template row
- unsafe root path rejection
- optional `template.yaml` metadata parsing
- variable inference for required/default/sensitive/validation metadata
- sensitive variables returning invalid output
- existing immutable template row reuse

Run:

```bash
go test ./internal/activities
```

Expected: FAIL because sync activity does not exist.

- [ ] **Step 3: Add activity implementation**

Add `TemplateSyncActivities` with injectable `GitRunner`, temp root, and repository interface. Production git runner shells out to:

```bash
git clone --depth 1 --branch <source_ref> <repo_url> <dest>
git -C <dest> rev-parse HEAD
```

- [ ] **Step 4: Add metadata and variable parsing**

Use `gopkg.in/yaml.v3` for `template.yaml`. Use HashiCorp HCL parser APIs for `.tf` variable blocks. Do not use regex for Terraform parsing.

- [ ] **Step 5: Run tests**

Run:

```bash
go test ./internal/activities
```

Expected: PASS.

### Task 6: Worker Wiring

**Files:**
- Modify: `cmd/tflive-worker/main.go`
- Modify: `cmd/tflive-worker/main_test.go`

- [ ] **Step 1: Write failing worker wiring tests**

Assert that the worker registers `TemplateSyncWorkflow`, `SyncTemplate`, and `RecordTemplateRegistrationStatus`.

Run:

```bash
go test ./cmd/tflive-worker
```

Expected: FAIL because worker wiring only registers template run workflow and activities.

- [ ] **Step 2: Wire workflow and activities**

Register `workflows.TemplateSyncWorkflow` with `traits.TemplateSyncWorkflowName`. Instantiate `activities.NewTemplateSyncActivities(store)` and register:

- `traits.SyncTemplateActivityName`
- `traits.RecordTemplateRegistrationStatusActivityName`

- [ ] **Step 3: Run tests**

Run:

```bash
go test ./cmd/tflive-worker
```

Expected: PASS.

### Task 7: Full Verification

**Files:**
- All touched files

- [ ] **Step 1: Run gofmt**

Run:

```bash
gofmt -w internal/traits/traits.go internal/traits/traits_test.go internal/app/service.go internal/app/service_test.go internal/api/server.go internal/api/server_test.go internal/postgres/repositories.go internal/postgres/store_test.go internal/temporal/dispatcher.go internal/temporal/dispatcher_test.go internal/workflows/template_sync.go internal/workflows/template_sync_workflow_test.go internal/activities/template_sync.go internal/activities/template_sync_test.go cmd/tflive-api/main.go cmd/tflive-api/main_test.go cmd/tflive-worker/main.go cmd/tflive-worker/main_test.go
```

Expected: command exits 0.

- [ ] **Step 2: Run full test suite**

Run:

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 3: Inspect final diff**

Run:

```bash
git diff --stat
git status --short
```

Expected: only template registration implementation files are modified.
