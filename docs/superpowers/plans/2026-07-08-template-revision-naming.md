# Template Revision Naming Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rename the immutable registered template object to `TemplateRevision` everywhere, including database tables, Go types, HTTP JSON, and frontend types.

**Architecture:** This is a clean contract rename with no compatibility layer. `SourceTemplate` remains the stable logical identity, while `TemplateRevision` becomes the resolved-SHA object currently represented by `Template`. `StackTemplate` remains the installed component instance and points to install-time, desired, and last-applied template revision IDs.

**Tech Stack:** Go `net/http`, pgx/Postgres migrations, React/Vite/TypeScript, Vitest.

## Global Constraints

- No backward-compatible aliases, route shims, duplicate JSON fields, or SQL views.
- Existing migrations can be edited directly.
- Preserve repeated installs of the same source template through `component_key`.
- Preserve upgrade behavior: update the desired revision on an existing `stack_templates` row.
- Preserve the uncommitted `StackTemplate` field comments by renaming them along with the fields.

---

## File Structure

- `internal/traits/traits.go` owns all shared type names and JSON field tags.
- `internal/postgres/migrations/0001_app_repositories.sql`, `0003_template_registration.sql`, and `0006_template_sources_and_revision_pointers.sql` own the table and column names.
- `internal/postgres/repositories.go` owns SQL queries and repository method names.
- `internal/postgres/store_test.go` owns schema and persistence contract coverage.
- `internal/app/service.go` owns use-case interfaces, command field names, and service methods.
- `internal/app/service_test.go` owns app behavior and compile-time fake repository contracts.
- `internal/activities/template_sync.go` owns deterministic revision IDs and variable parsing.
- `internal/activities/template_sync_test.go` owns sync activity assertions.
- `internal/api/server.go` owns routes and JSON request/response names.
- `internal/api/server_test.go` owns HTTP contract coverage.
- `cmd/tflive-api/main_test.go` owns end-to-end wiring fakes.
- `web/src/api/types.ts`, `web/src/api/client.ts`, and `web/src/api/client.test.ts` own frontend API contracts.
- `web/src/App.tsx`, `web/src/workflowState.ts`, and `web/src/workflowState.test.ts` own UI state names and labels.

---

### Task 1: Rename Core Traits To TemplateRevision

**Files:**
- Modify: `internal/traits/traits.go`
- Modify: `internal/traits/traits_test.go`

**Interfaces:**
- Produces: `TemplateRevisionID`, `TemplateRevision`, `TemplateRevisionStatus`, and `TemplateVariable.TemplateRevisionID` naming for later layers.
- Consumes: existing `SourceTemplate`, `StackTemplate`, and `TemplateRun` shapes.

- [ ] **Step 1: Update trait tests to use revision names**

In `internal/traits/traits_test.go`, replace revision IDs used in `StackTemplate` fixtures:

```go
stackTemplate := StackTemplate{
	ID:                 StackTemplateID("stack_template_123"),
	StackID:            StackID("stack_123"),
	TemplateRevisionID: TemplateRevisionID("template_rev_123"),
	SelectedRef:        "main",
	WorkspaceName:      "prod-workspace",
	Lifecycle:          StackTemplateActive,
}
```

- [ ] **Step 2: Run the focused failing test**

Run:

```bash
go test ./internal/traits -run TestStackTemplateWorkspaceStable -count=1
```

Expected: FAIL to compile because `TemplateRevisionID` and `StackTemplate.TemplateRevisionID` do not exist.

- [ ] **Step 3: Rename trait types and fields**

In `internal/traits/traits.go`:

- Rename `TemplateID` to `TemplateRevisionID`.
- Rename `TemplateStatus` to `TemplateRevisionStatus`; constants become `TemplateRevisionPendingValidation`, `TemplateRevisionValidating`, `TemplateRevisionActive`, and `TemplateRevisionInvalid`.
- Rename `Template` to `TemplateRevision`.
- Rename `TemplateRegistration.TemplateID` to `TemplateRegistration.TemplateRevisionID` with JSON tag `template_revision_id`.
- Rename `TemplateVariable.TemplateID` to `TemplateVariable.TemplateRevisionID` with JSON tag `template_revision_id`.
- Rename `StackTemplate.TemplateID` to `StackTemplate.TemplateRevisionID` with JSON tag `template_revision_id`.
- Rename `StackTemplate.DesiredTemplateID` to `StackTemplate.DesiredTemplateRevisionID` with JSON tag `desired_template_revision_id`.
- Rename `StackTemplate.LastAppliedTemplateID` to `StackTemplate.LastAppliedTemplateRevisionID` with JSON tag `last_applied_template_revision_id`.
- Rename `TemplateRun.TemplateID` to `TemplateRun.TemplateRevisionID` with JSON tag `template_revision_id`.
- Rename `TemplateSyncActivityOutput.TemplateID` to `TemplateSyncActivityOutput.TemplateRevisionID`.
- Rename `TemplateRegistrationStatusActivityInput.TemplateID` to `TemplateRegistrationStatusActivityInput.TemplateRevisionID`.
- Update comments so `TemplateRevision` describes one resolved source tree.

- [ ] **Step 4: Run the focused traits test**

Run:

```bash
go test ./internal/traits -count=1
```

Expected: PASS for the traits package.

- [ ] **Step 5: Commit**

Run:

```bash
git add internal/traits/traits.go internal/traits/traits_test.go
git commit -m "refactor: rename template traits to revisions"
```

---

### Task 2: Rename Migrations And Postgres Repository

**Files:**
- Modify: `internal/postgres/migrations/0001_app_repositories.sql`
- Modify: `internal/postgres/migrations/0003_template_registration.sql`
- Modify: `internal/postgres/migrations/0006_template_sources_and_revision_pointers.sql`
- Modify: `internal/postgres/repositories.go`
- Modify: `internal/postgres/store_test.go`

**Interfaces:**
- Consumes: `traits.TemplateRevision`, `traits.TemplateRevisionID`, and renamed `StackTemplate` fields from Task 1.
- Produces: SQL table `template_revisions`, columns `template_revision_id`, `desired_template_revision_id`, and `last_applied_template_revision_id`, plus repository methods with revision names.

- [ ] **Step 1: Update repository tests for schema names**

In `internal/postgres/store_test.go`, rename fixtures and assertions:

- `traits.Template{...}` becomes `traits.TemplateRevision{...}`.
- `traits.TemplateID("template_rev_1")` becomes `traits.TemplateRevisionID("template_rev_1")`.
- `TemplateID:` struct fields become `TemplateRevisionID:`.
- `DesiredTemplateID:` becomes `DesiredTemplateRevisionID:`.
- `LastAppliedTemplateID:` becomes `LastAppliedTemplateRevisionID:`.

Add a focused schema assertion to an existing migration test or create:

```go
func TestTemplateRevisionTablesUseRevisionNames(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)

	var tableName string
	err := pool.QueryRow(ctx, `
		select table_name
		from information_schema.tables
		where table_schema = 'public'
			and table_name = 'template_revisions'
	`).Scan(&tableName)
	if err != nil {
		t.Fatalf("template_revisions table lookup: %v", err)
	}
	if tableName != "template_revisions" {
		t.Fatalf("table = %q, want template_revisions", tableName)
	}
}
```

- [ ] **Step 2: Run failing Postgres tests**

Run:

```bash
go test ./internal/postgres -run 'TestTemplateRevisionTablesUseRevisionNames|TestCreateStackTemplateAllowsRepeatedSourceWithDistinctComponentKeys' -count=1
```

Expected: FAIL to compile or fail schema lookup because repository and migrations still use `templates` and `template_id`.

- [ ] **Step 3: Rename migrations directly**

Update migrations:

- In `0003_template_registration.sql`, create `template_revisions` instead of `templates`.
- Rename indexes and check constraints to `template_revisions_*`.
- Rename `template_registrations.template_id` to `template_revision_id`.
- Rename `template_variables.template_id` to `template_revision_id` and reference `template_revisions(id)`.
- In `0001_app_repositories.sql`, rename `stack_templates.template_id` to `template_revision_id`.
- In `0006_template_sources_and_revision_pointers.sql`, replace all `templates` table references with `template_revisions`.
- In `0006_template_sources_and_revision_pointers.sql`, rename stack-template columns to `desired_template_revision_id` and `last_applied_template_revision_id`.
- In `0006_template_sources_and_revision_pointers.sql`, rename `template_runs.template_id` to `template_revision_id`.

- [ ] **Step 4: Rename Postgres repository methods and SQL**

In `internal/postgres/repositories.go`:

- Rename `UpsertTemplateWithVariables` to `UpsertTemplateRevisionWithVariables`.
- Rename `ListTemplates` to `ListTemplateRevisions`.
- Rename `GetTemplate` to `GetTemplateRevision`.
- Rename `GetTemplateVariables` to `GetTemplateRevisionVariables`.
- Rename helpers `insertTemplate`, `scanTemplate`, `selectTemplateByIdentity`, and `deterministicTemplateID` to revision names.
- Update SQL table names and columns to the migration names.
- Update error strings from `"get template variables"` to `"get template revision variables"` where useful.

- [ ] **Step 5: Run Postgres tests**

Run:

```bash
go test ./internal/postgres -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

Run:

```bash
git add internal/postgres/migrations/0001_app_repositories.sql internal/postgres/migrations/0003_template_registration.sql internal/postgres/migrations/0006_template_sources_and_revision_pointers.sql internal/postgres/repositories.go internal/postgres/store_test.go
git commit -m "refactor: rename template revision storage"
```

---

### Task 3: Rename App Service And Activities

**Files:**
- Modify: `internal/app/service.go`
- Modify: `internal/app/service_test.go`
- Modify: `internal/activities/template_sync.go`
- Modify: `internal/activities/template_sync_test.go`
- Modify: `cmd/tflive-api/main_test.go`

**Interfaces:**
- Consumes: repository methods from Task 2.
- Produces: app commands and activity outputs using `TemplateRevisionID` names.

- [ ] **Step 1: Update app and activity tests**

In `internal/app/service_test.go`, `internal/activities/template_sync_test.go`, and `cmd/tflive-api/main_test.go`:

- Rename fake repository methods to `GetTemplateRevision`, `ListTemplateRevisions`, `GetTemplateRevisionVariables`, and `UpsertTemplateRevisionWithVariables`.
- Rename command fields:
  - `AddTemplateToStackCommand.TemplateID` to `TemplateRevisionID`
  - `UpgradeStackTemplateCommand.TargetTemplateID` to `TargetTemplateRevisionID`
  - `GetTemplateVariablesCommand` to `GetTemplateRevisionVariablesCommand`
- Rename expected run fields from `TemplateID` to `TemplateRevisionID`.

- [ ] **Step 2: Run failing app/activity tests**

Run:

```bash
go test ./internal/app ./internal/activities ./cmd/tflive-api -count=1
```

Expected: FAIL to compile because service/activity production code still uses old names.

- [ ] **Step 3: Rename service interfaces and commands**

In `internal/app/service.go`:

- Rename `TemplateRepository` to `TemplateRevisionRepository`.
- Rename `TemplateMetadataRepository` to `TemplateRevisionMetadataRepository`.
- Rename service fields `TemplateMetadata` and `Templates` to `TemplateRevisionMetadata` and `TemplateRevisions`.
- Rename list/get variable use cases to revision names.
- Update service methods to fetch and validate using `TemplateRevisionID`, `DesiredTemplateRevisionID`, and `TargetTemplateRevisionID`.
- Rename status references to `traits.TemplateRevisionActive` and `traits.TemplateRevisionInvalid`.

- [ ] **Step 4: Rename template sync activity**

In `internal/activities/template_sync.go`:

- Build a `traits.TemplateRevision`.
- Call `UpsertTemplateRevisionWithVariables`.
- Return `TemplateRevisionID` in `TemplateSyncActivityOutput`.
- Rename `deterministicTemplateID` to `deterministicTemplateRevisionID`.
- Rename variable parsing parameters to `templateRevisionID`.

- [ ] **Step 5: Run app/activity tests**

Run:

```bash
go test ./internal/app ./internal/activities ./cmd/tflive-api -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

Run:

```bash
git add internal/app/service.go internal/app/service_test.go internal/activities/template_sync.go internal/activities/template_sync_test.go cmd/tflive-api/main_test.go
git commit -m "refactor: rename template revision app contracts"
```

---

### Task 4: Rename HTTP API Contract

**Files:**
- Modify: `internal/api/server.go`
- Modify: `internal/api/server_test.go`

**Interfaces:**
- Consumes: app service revision commands from Task 3.
- Produces: HTTP routes and JSON fields using `template_revision_id`.

- [ ] **Step 1: Update API tests**

In `internal/api/server_test.go`:

- Change registration/list/variables routes to `/template-revisions`.
- Change install request JSON from `template_id` to `template_revision_id`.
- Change expected response fields from `TemplateID`, `DesiredTemplateID`, and `LastAppliedTemplateID` to revision names.
- Change fake repository methods to the revision names from Task 3.

- [ ] **Step 2: Run failing API tests**

Run:

```bash
go test ./internal/api -count=1
```

Expected: FAIL to compile or fail route assertions because server code still exposes old routes and fields.

- [ ] **Step 3: Rename server routes and DTOs**

In `internal/api/server.go`:

- Rename routes:
  - `POST /v1/tenants/{tenant_id}/templates` to `POST /v1/tenants/{tenant_id}/template-revisions`
  - `GET /v1/tenants/{tenant_id}/templates` to `GET /v1/tenants/{tenant_id}/template-revisions`
  - `GET /v1/tenants/{tenant_id}/templates/{template_id}/variables` to `GET /v1/tenants/{tenant_id}/template-revisions/{template_revision_id}/variables`
- Rename handlers to `handleListTemplateRevisions` and `handleGetTemplateRevisionVariables`.
- Rename request field `TemplateID` to `TemplateRevisionID` with JSON `template_revision_id`.
- Rename response fields to `TemplateRevisionID`, `DesiredTemplateRevisionID`, and `LastAppliedTemplateRevisionID`.
- Keep upgrade request JSON `target_template_revision_id`.

- [ ] **Step 4: Run API tests**

Run:

```bash
go test ./internal/api -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

Run:

```bash
git add internal/api/server.go internal/api/server_test.go
git commit -m "refactor: rename template revision API"
```

---

### Task 5: Rename Frontend API And UI State

**Files:**
- Modify: `web/src/api/types.ts`
- Modify: `web/src/api/client.ts`
- Modify: `web/src/api/client.test.ts`
- Modify: `web/src/App.tsx`
- Modify: `web/src/workflowState.ts`
- Modify: `web/src/workflowState.test.ts`

**Interfaces:**
- Consumes: HTTP API contract from Task 4.
- Produces: frontend API helpers and UI state names using template revision naming.

- [ ] **Step 1: Update frontend tests**

In `web/src/api/client.test.ts` and `web/src/workflowState.test.ts`:

- Rename imports and helpers from `Template` to `TemplateRevision`.
- Expect `/template-revisions` routes.
- Send `template_revision_id` in install requests.
- Expect stack template fields `template_revision_id`, `desired_template_revision_id`, and `last_applied_template_revision_id`.

- [ ] **Step 2: Run failing frontend tests**

Run:

```bash
npm test
```

from `web/`.

Expected: FAIL to compile because API client/types still use old names.

- [ ] **Step 3: Rename frontend types and client helpers**

In `web/src/api/types.ts` and `web/src/api/client.ts`:

- Rename `Template` to `TemplateRevision`.
- Rename `listTemplates` to `listTemplateRevisions`.
- Rename `getTemplateVariables` to `getTemplateRevisionVariables`.
- Rename request/response fields to `template_revision_id`, `desired_template_revision_id`, and `last_applied_template_revision_id`.
- Use `/template-revisions` routes.

- [ ] **Step 4: Rename UI state and labels**

In `web/src/App.tsx`, `web/src/workflowState.ts`, and `web/src/workflowState.test.ts`:

- Rename `templates` state to `templateRevisions`.
- Rename `selectedTemplateID` to `selectedTemplateRevisionID`.
- Rename helper functions to `findSelectedTemplateRevision`, `nextSelectedTemplateRevisionID`, and `templateRevisionLabel`.
- Keep visible copy like “Install template” where it describes the user-facing action.
- Show revision IDs using labels that include “revision” where technical IDs are displayed.

- [ ] **Step 5: Run frontend tests and build**

Run:

```bash
npm test
npm run build
```

from `web/`.

Expected: PASS.

- [ ] **Step 6: Commit**

Run:

```bash
git add web/src/api/types.ts web/src/api/client.ts web/src/api/client.test.ts web/src/App.tsx web/src/workflowState.ts web/src/workflowState.test.ts
git commit -m "refactor: rename template revision frontend contract"
```

---

### Task 6: Cleanup, Search, And Final Verification

**Files:**
- Modify only files with stale names found by searches.

**Interfaces:**
- Consumes: all previous tasks.
- Produces: a repo with no stale revision-as-template identifiers in active code.

- [ ] **Step 1: Search for stale active-code names**

Run:

```bash
rg -n "TemplateID|TemplateStatus|type Template struct|template_id|desired_template_id|last_applied_template_id|from templates|insert into templates|/templates" internal cmd web --glob '!web/node_modules/**'
```

Expected: Any hits should be legitimate user-facing copy such as `/stacks/{stack_id}/templates` or names like `StackTemplateID`; all revision identifiers should use revision names.

- [ ] **Step 2: Fix stale active-code hits**

For each stale hit:

- Use `TemplateRevisionID` for revision ID types.
- Use `template_revision_id` for revision JSON/SQL fields.
- Keep `StackTemplateID`, `SourceTemplateID`, and `source_template_id` unchanged.
- Keep `/stacks/{stack_id}/templates` unchanged.

- [ ] **Step 3: Run full Go tests**

Run:

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 4: Run frontend tests and build**

Run:

```bash
npm test
npm run build
```

from `web/`.

Expected: PASS.

- [ ] **Step 5: Check diff and status**

Run:

```bash
git diff --check
git status --short
```

Expected: no whitespace errors; only intentional committed or staged changes remain.

- [ ] **Step 6: Commit final cleanup if needed**

If Step 2 changed files, run:

```bash
git add <changed-files>
git commit -m "refactor: clean up template revision naming"
```
