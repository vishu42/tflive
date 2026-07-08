# Template Revision Upgrades Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add stable source templates, immutable revision pointers, editable desired config, and in-place stack-template upgrades without losing repeated installs.

**Architecture:** Keep the existing `templates` table and Go `TemplateID` type as the compatibility representation of template revisions. Add `source_templates` and additive pointer columns first, then update registration, install, run creation, and apply completion to write/read the new model. API endpoints for config edit and upgrade sit behind the existing `internal/app` service boundary.

**Tech Stack:** Go `net/http`, pgx/Postgres migrations, existing app service/repository ports, existing Temporal workflow dispatch, React/Vite only if the backend slice leaves time for UI affordances.

## Global Constraints

- Preserve the ability to install the same source template multiple times in one stack.
- Do not auto-upgrade production stack templates.
- Run creation must snapshot desired revision and desired config.
- Registration must create or reuse a source template and create or reuse one immutable revision per resolved SHA.
- The first implementation keeps the existing `templates` table name and `TemplateID` Go type.

---

## File Structure

- `internal/traits/traits.go` owns source-template traits and new revision/config fields on `Template`, `StackTemplate`, and `TemplateRun`.
- `internal/postgres/migrations/0006_template_sources_and_revision_pointers.sql` adds source templates, source/revision pointers, component keys, and run snapshots.
- `internal/postgres/repositories.go` owns source-template upsert, template-revision persistence, stack-template persistence, run snapshot persistence, and last-applied revision updates.
- `internal/postgres/store_test.go` owns migration and repository coverage.
- `internal/activities/template_sync.go` owns deterministic source-template IDs and passes source IDs into persisted templates.
- `internal/app/service.go` owns install config validation, desired config edits, upgrades, and run snapshots.
- `internal/app/service_test.go` owns service-level behavior tests.
- `internal/api/server.go` owns HTTP endpoints and response shape updates.
- `internal/api/server_test.go` owns HTTP contract tests.
- `web/src/App.tsx`, `web/src/api/client.ts`, and `web/src/api/types.ts` can expose config save and upgrade later, after backend behavior passes.

---

### Task 1: Add Source Template And Revision Pointer Persistence

**Files:**
- Modify: `internal/traits/traits.go`
- Create: `internal/postgres/migrations/0006_template_sources_and_revision_pointers.sql`
- Modify: `internal/postgres/repositories.go`
- Modify: `internal/postgres/store_test.go`
- Modify: `internal/activities/template_sync.go`

**Interfaces:**
- Consumes: existing `TemplateRepository.UpsertTemplateWithVariables(ctx, template, variables)`.
- Produces: `traits.SourceTemplate`, `traits.Template.SourceTemplateID`, `traits.StackTemplate.SourceTemplateID`, `traits.StackTemplate.DesiredTemplateID`, `traits.StackTemplate.LastAppliedTemplateID`, `traits.StackTemplate.ComponentKey`, `traits.StackTemplate.DesiredConfigJSON`, `traits.TemplateRun.TemplateID`, `traits.TemplateRun.SourceTemplateID`, and `traits.TemplateRun.ConfigJSON`.

- [ ] **Step 1: Write failing migration/repository tests**

Add a test to `internal/postgres/store_test.go` that inserts a source-backed template, installs it twice with distinct component keys, and verifies both installs survive:

```go
func TestCreateStackTemplateAllowsRepeatedSourceWithDistinctComponentKeys(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newMigratedStore(t, ctx)
	tenantID := traits.TenantID("tenant_123")
	stack := seedStack(t, ctx, store, tenantID)
	template := seedTemplate(t, ctx, store, traits.Template{
		ID:                traits.TemplateID("template_rev_1"),
		TenantID:          tenantID,
		SourceTemplateID:  traits.SourceTemplateID("source_template_vpc"),
		RepoOwner:         "acme",
		RepoName:          "infra",
		SourceRef:         "main",
		ResolvedCommitSHA: "sha-1",
		RootPath:          "vpc",
		Name:              "vpc",
		Status:            traits.TemplateActive,
	})

	first := traits.StackTemplate{
		ID:                traits.StackTemplateID("stack_template_primary"),
		TenantID:          tenantID,
		StackID:           stack.ID,
		TemplateID:        template.ID,
		SourceTemplateID:  template.SourceTemplateID,
		DesiredTemplateID: template.ID,
		ComponentKey:      "primary-vpc",
		WorkspaceName:     "meg_acme_primary",
		ConfigJSON:        json.RawMessage(`{"region":"us-east-1"}`),
		DesiredConfigJSON: json.RawMessage(`{"region":"us-east-1"}`),
		CreatedBy:         traits.UserID("user_123"),
		Lifecycle:         traits.StackTemplateActive,
	}
	second := first
	second.ID = traits.StackTemplateID("stack_template_shared")
	second.ComponentKey = "shared-vpc"
	second.WorkspaceName = "meg_acme_shared"

	if err := store.CreateStackTemplate(ctx, first); err != nil {
		t.Fatalf("CreateStackTemplate first returned error: %v", err)
	}
	if err := store.CreateStackTemplate(ctx, second); err != nil {
		t.Fatalf("CreateStackTemplate second returned error: %v", err)
	}

	view, err := store.GetStackWithTemplates(ctx, tenantID, stack.ID)
	if err != nil {
		t.Fatalf("GetStackWithTemplates returned error: %v", err)
	}
	if len(view.Templates) != 2 {
		t.Fatalf("template count = %d, want 2", len(view.Templates))
	}
}
```

Add a companion test for duplicate active component keys:

```go
func TestCreateStackTemplateRejectsDuplicateActiveComponentKey(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newMigratedStore(t, ctx)
	tenantID := traits.TenantID("tenant_123")
	stack := seedStack(t, ctx, store, tenantID)
	template := seedTemplate(t, ctx, store, traits.Template{
		ID:                traits.TemplateID("template_rev_1"),
		TenantID:          tenantID,
		SourceTemplateID:  traits.SourceTemplateID("source_template_vpc"),
		RepoOwner:         "acme",
		RepoName:          "infra",
		SourceRef:         "main",
		ResolvedCommitSHA: "sha-1",
		RootPath:          "vpc",
		Name:              "vpc",
		Status:            traits.TemplateActive,
	})

	stackTemplate := traits.StackTemplate{
		ID:                traits.StackTemplateID("stack_template_primary"),
		TenantID:          tenantID,
		StackID:           stack.ID,
		TemplateID:        template.ID,
		SourceTemplateID:  template.SourceTemplateID,
		DesiredTemplateID: template.ID,
		ComponentKey:      "primary-vpc",
		WorkspaceName:     "meg_acme_primary",
		ConfigJSON:        json.RawMessage(`{}`),
		DesiredConfigJSON: json.RawMessage(`{}`),
		CreatedBy:         traits.UserID("user_123"),
		Lifecycle:         traits.StackTemplateActive,
	}

	if err := store.CreateStackTemplate(ctx, stackTemplate); err != nil {
		t.Fatalf("CreateStackTemplate first returned error: %v", err)
	}
	stackTemplate.ID = traits.StackTemplateID("stack_template_duplicate")
	err := store.CreateStackTemplate(ctx, stackTemplate)
	if !errors.Is(err, app.ErrDuplicateStackTemplateComponentKey) {
		t.Fatalf("error = %v, want ErrDuplicateStackTemplateComponentKey", err)
	}
}
```

- [ ] **Step 2: Run the failing tests**

Run:

```bash
go test ./internal/postgres -run 'TestCreateStackTemplate(AllowsRepeatedSource|RejectsDuplicate)' -count=1
```

Expected: build fails because the new trait fields and duplicate-key app error do not exist.

- [ ] **Step 3: Add traits**

Add to `internal/traits/traits.go`:

```go
type SourceTemplateID string

// SourceTemplate is the stable logical identity tracked by template revisions.
type SourceTemplate struct {
	ID                       SourceTemplateID `json:"id"`
	TenantID                 TenantID         `json:"tenant_id"`
	RepoOwner                string           `json:"repo_owner"`
	RepoName                 string           `json:"repo_name"`
	SourceRef                string           `json:"source_ref"`
	RootPath                 string           `json:"root_path"`
	LatestTemplateRevisionID TemplateID       `json:"latest_template_revision_id"`
	CreatedAt                time.Time        `json:"created_at"`
	UpdatedAt                time.Time        `json:"updated_at"`
}
```

Extend `Template`, `StackTemplate`, and `TemplateRun`:

```go
SourceTemplateID SourceTemplateID `json:"source_template_id"`
```

```go
ComponentKey          string           `json:"component_key"`
SourceTemplateID      SourceTemplateID `json:"source_template_id"`
DesiredTemplateID     TemplateID       `json:"desired_template_id"`
LastAppliedTemplateID TemplateID       `json:"last_applied_template_id"`
DesiredConfigJSON     json.RawMessage  `json:"desired_config_json"`
```

```go
TemplateID       TemplateID       `json:"template_id"`
SourceTemplateID SourceTemplateID `json:"source_template_id"`
ConfigJSON       json.RawMessage  `json:"config_json"`
```

- [ ] **Step 4: Add migration 0006**

Create `internal/postgres/migrations/0006_template_sources_and_revision_pointers.sql`:

```sql
create table source_templates (
	id text primary key,
	tenant_id text not null,
	repo_owner text not null,
	repo_name text not null,
	source_ref text not null,
	root_path text not null,
	latest_template_revision_id text not null default '',
	created_at timestamptz not null default now(),
	updated_at timestamptz not null default now()
);

create unique index source_templates_identity_idx on source_templates (
	tenant_id,
	repo_owner,
	repo_name,
	root_path,
	source_ref
);
create index source_templates_tenant_id_id_idx on source_templates (tenant_id, id);

alter table templates add column source_template_id text not null default '';

insert into source_templates (
	id,
	tenant_id,
	repo_owner,
	repo_name,
	source_ref,
	root_path,
	latest_template_revision_id,
	created_at,
	updated_at
)
select
	'source_template_' || substr(md5(tenant_id || chr(0) || repo_owner || chr(0) || repo_name || chr(0) || root_path || chr(0) || source_ref), 1, 32),
	tenant_id,
	repo_owner,
	repo_name,
	source_ref,
	root_path,
	(select t2.id
	 from templates t2
	 where t2.tenant_id = templates.tenant_id
	   and t2.repo_owner = templates.repo_owner
	   and t2.repo_name = templates.repo_name
	   and t2.root_path = templates.root_path
	   and t2.source_ref = templates.source_ref
	 order by t2.created_at desc, t2.id desc
	 limit 1),
	min(created_at),
	max(created_at)
from templates
group by tenant_id, repo_owner, repo_name, root_path, source_ref;

update templates
set source_template_id = source_templates.id
from source_templates
where templates.tenant_id = source_templates.tenant_id
	and templates.repo_owner = source_templates.repo_owner
	and templates.repo_name = source_templates.repo_name
	and templates.root_path = source_templates.root_path
	and templates.source_ref = source_templates.source_ref;

create index templates_tenant_id_source_template_id_idx on templates (tenant_id, source_template_id);
create unique index templates_source_revision_idx on templates (tenant_id, source_template_id, resolved_commit_sha);

alter table stack_templates add column component_key text not null default '';
alter table stack_templates add column source_template_id text not null default '';
alter table stack_templates add column desired_template_id text not null default '';
alter table stack_templates add column last_applied_template_id text not null default '';
alter table stack_templates add column desired_config_json jsonb not null default '{}'::jsonb;

update stack_templates
set
	component_key = case
		when stack_templates.component_key <> '' then stack_templates.component_key
		else stack_templates.id
	end,
	source_template_id = templates.source_template_id,
	desired_template_id = stack_templates.template_id,
	last_applied_template_id = case
		when stack_templates.last_applied_run_id <> '' then stack_templates.template_id
		else ''
	end,
	desired_config_json = stack_templates.config_json
from templates
where stack_templates.tenant_id = templates.tenant_id
	and stack_templates.template_id = templates.id;

create unique index stack_templates_active_component_key_idx on stack_templates (
	tenant_id,
	stack_id,
	component_key
) where lifecycle = 'active';

alter table template_runs add column template_id text not null default '';
alter table template_runs add column source_template_id text not null default '';
alter table template_runs add column config_json jsonb not null default '{}'::jsonb;
```

- [ ] **Step 5: Implement repository persistence**

Update `insertTemplate`, `selectTemplateByIdentity`, `scanTemplate`, `CreateStackTemplate`, `scanStackTemplate`, `CreateTemplateRun`, `scanTemplateRun`, and `recordStackTemplateLastApplied` to read/write the new columns. On duplicate `stack_templates_active_component_key_idx`, return `app.ErrDuplicateStackTemplateComponentKey`.

- [ ] **Step 6: Add deterministic source IDs in sync**

Add to `internal/activities/template_sync.go`:

```go
func deterministicSourceTemplateID(input traits.TemplateSyncActivityInput, rootPath string) traits.SourceTemplateID {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		string(input.TenantID),
		input.RepoOwner,
		input.RepoName,
		rootPath,
		input.SourceRef,
	}, "\x00")))
	return traits.SourceTemplateID("source_template_" + hex.EncodeToString(sum[:16]))
}
```

Set `SourceTemplateID` on the `traits.Template` created by `SyncTemplate`.

- [ ] **Step 7: Run repository tests**

Run:

```bash
go test ./internal/postgres -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit**

Run:

```bash
git add internal/traits/traits.go internal/postgres/migrations/0006_template_sources_and_revision_pointers.sql internal/postgres/repositories.go internal/postgres/store_test.go internal/activities/template_sync.go
git commit -m "feat: add template source revision pointers"
```

---

### Task 2: Make Install And Run Creation Use Desired Revision Snapshots

**Files:**
- Modify: `internal/app/service.go`
- Modify: `internal/app/service_test.go`
- Modify: `internal/postgres/repositories.go`
- Modify: `internal/postgres/store_test.go`

**Interfaces:**
- Consumes: `StackTemplate.DesiredTemplateID`, `StackTemplate.DesiredConfigJSON`, and `TemplateRun.TemplateID`.
- Produces: install writes source/desired pointers, run creation snapshots desired revision and config, apply completion writes `LastAppliedTemplateID`.

- [ ] **Step 1: Write app tests**

Add a test asserting `AddTemplateToStack` sets source and desired pointers:

```go
func TestAddTemplateToStackPersistsDesiredRevisionFields(t *testing.T) {
	t.Parallel()

	templates := &recordingTemplateRepository{
		template: traits.Template{
			ID:               traits.TemplateID("template_rev_1"),
			TenantID:         traits.TenantID("tenant_123"),
			SourceTemplateID: traits.SourceTemplateID("source_template_vpc"),
			SourceRef:        "main",
			Status:           traits.TemplateActive,
		},
	}
	installer := &recordingStackTemplateInstaller{}
	service := NewService(Service{
		Stacks: &recordingStackRepository{
			stack: traits.Stack{ID: traits.StackID("stack_123"), TenantID: traits.TenantID("tenant_123"), Slug: "acme-prod"},
		},
		TemplateMetadata:       templates,
		Templates:              templates,
		StackTemplateInstaller: installer,
		StackTemplateIDs:       fixedStackTemplateIDGenerator{id: traits.StackTemplateID("stack_template_a1b2c3d4")},
	})

	_, err := service.AddTemplateToStack(context.Background(), AddTemplateToStackCommand{
		TenantID:     traits.TenantID("tenant_123"),
		StackID:      traits.StackID("stack_123"),
		TemplateID:   traits.TemplateID("template_rev_1"),
		ComponentKey: "primary-vpc",
		SelectedRef:  "main",
		ConfigJSON:   json.RawMessage(`{"region":"us-east-1"}`),
		Actor:        traits.UserID("user_123"),
	})
	if err != nil {
		t.Fatalf("AddTemplateToStack returned error: %v", err)
	}
	if installer.created.SourceTemplateID != traits.SourceTemplateID("source_template_vpc") {
		t.Fatalf("source template ID = %q", installer.created.SourceTemplateID)
	}
	if installer.created.DesiredTemplateID != traits.TemplateID("template_rev_1") {
		t.Fatalf("desired template ID = %q", installer.created.DesiredTemplateID)
	}
	if string(installer.created.DesiredConfigJSON) != `{"region":"us-east-1"}` {
		t.Fatalf("desired config = %s", installer.created.DesiredConfigJSON)
	}
}
```

- [ ] **Step 2: Implement install field population**

Add `ComponentKey string` to `AddTemplateToStackCommand`, require or derive it, and populate:

```go
ComponentKey:          componentKey(command.ComponentKey, stackTemplate.ID),
SourceTemplateID:      template.SourceTemplateID,
DesiredTemplateID:     command.TemplateID,
DesiredConfigJSON:     configJSON,
LastAppliedTemplateID: "",
```

- [ ] **Step 3: Snapshot run desired state**

In `StartTemplateRun`, load metadata with `stackTemplate.DesiredTemplateID`, validate `DesiredConfigJSON`, and create `TemplateRun` with:

```go
TemplateID:       stackTemplate.DesiredTemplateID,
SourceTemplateID: stackTemplate.SourceTemplateID,
ConfigJSON:       stackTemplate.DesiredConfigJSON,
ResolvedCommitSHA: template.ResolvedCommitSHA,
```

- [ ] **Step 4: Run app tests**

Run:

```bash
go test ./internal/app -run 'TestAddTemplateToStack|TestStartTemplateRun' -count=1
```

Expected: PASS.

- [ ] **Step 5: Run repository tests**

Run:

```bash
go test ./internal/postgres -run 'TestCreateTemplateRun|TestRecordTemplateRunStatusUpdatesStackTemplateLastApplied' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

Run:

```bash
git add internal/app/service.go internal/app/service_test.go internal/postgres/repositories.go internal/postgres/store_test.go
git commit -m "feat: snapshot desired template runs"
```

---

### Task 3: Add Config Edit And Upgrade Use Cases

**Files:**
- Modify: `internal/app/service.go`
- Modify: `internal/app/service_test.go`
- Modify: `internal/postgres/repositories.go`
- Modify: `internal/postgres/store_test.go`

**Interfaces:**
- Produces: `UpdateStackTemplateConfig(ctx, command)`, `UpgradeStackTemplate(ctx, command)`, repository methods `UpdateStackTemplateConfig` and `UpdateStackTemplateDesiredRevision`.

- [ ] **Step 1: Add app tests**

Add tests for config edit validation, upgrade target source mismatch, upgrade carry-forward, and new required variable failure.

- [ ] **Step 2: Add service commands**

Add:

```go
type UpdateStackTemplateConfigCommand struct {
	TenantID        traits.TenantID
	StackTemplateID traits.StackTemplateID
	ConfigJSON      json.RawMessage
	Actor           traits.UserID
}

type UpgradeStackTemplateCommand struct {
	TenantID         traits.TenantID
	StackTemplateID  traits.StackTemplateID
	TargetTemplateID traits.TemplateID
	ConfigJSON       json.RawMessage
	Actor            traits.UserID
}
```

- [ ] **Step 3: Implement config edit**

Load stack template, validate `ConfigJSON` against variables for `DesiredTemplateID`, update `DesiredConfigJSON`, and return the updated stack template.

- [ ] **Step 4: Implement upgrade**

Load stack template, target template, and target variables. Reject source mismatches. If config is empty, carry forward matching values from current desired config, then validate. Update desired revision and desired config.

- [ ] **Step 5: Run tests**

Run:

```bash
go test ./internal/app ./internal/postgres -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

Run:

```bash
git add internal/app/service.go internal/app/service_test.go internal/postgres/repositories.go internal/postgres/store_test.go
git commit -m "feat: edit and upgrade stack templates"
```

---

### Task 4: Expose Config Edit And Upgrade APIs

**Files:**
- Modify: `internal/api/server.go`
- Modify: `internal/api/server_test.go`
- Modify: `web/src/api/types.ts`
- Modify: `web/src/api/client.ts`

**Interfaces:**
- Consumes: app service config edit and upgrade methods.
- Produces: `PATCH /v1/tenants/{tenant_id}/stack-templates/{stack_template_id}/config` and `POST /v1/tenants/{tenant_id}/stack-templates/{stack_template_id}/upgrade`.

- [ ] **Step 1: Add API tests**

Add server tests for successful config edit, upgrade to explicit revision, missing required variable, source mismatch, and not found.

- [ ] **Step 2: Add routes**

Register:

```text
PATCH /v1/tenants/{tenant_id}/stack-templates/{stack_template_id}/config
POST  /v1/tenants/{tenant_id}/stack-templates/{stack_template_id}/upgrade
```

- [ ] **Step 3: Add handlers**

Decode JSON request bodies into explicit structs, call app methods, and return the same stack-template response shape used by install/read endpoints.

- [ ] **Step 4: Add frontend API helpers**

Add typed helpers:

```ts
export function updateStackTemplateConfig(tenantID: string, stackTemplateID: string, request: UpdateStackTemplateConfigRequest): Promise<StackTemplateResponse>
export function upgradeStackTemplate(tenantID: string, stackTemplateID: string, request: UpgradeStackTemplateRequest): Promise<StackTemplateResponse>
```

- [ ] **Step 5: Run tests**

Run:

```bash
go test ./internal/api -count=1
cd web && npm test
```

Expected: PASS.

- [ ] **Step 6: Commit**

Run:

```bash
git add internal/api/server.go internal/api/server_test.go web/src/api/types.ts web/src/api/client.ts
git commit -m "feat: expose stack template upgrades"
```

---

### Task 5: Final Verification

**Files:**
- No source edits unless verification exposes a bug.

- [ ] **Step 1: Run all Go tests**

Run:

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 2: Run frontend tests**

Run:

```bash
cd web && npm test
```

Expected: PASS.

- [ ] **Step 3: Build frontend**

Run:

```bash
cd web && npm run build
```

Expected: PASS.

- [ ] **Step 4: Check status**

Run:

```bash
git status --short
```

Expected: clean working tree.
