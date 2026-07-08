# Template Revision Upgrades Design

## Purpose

Separate the stable identity of a template from immutable resolved revisions, then let each installed stack template upgrade in place. This prevents production stacks from receiving a duplicate install when a downstream module changes and a new SHA is registered.

The key product guarantee is that a `StackTemplate` is the long-lived installed component instance. Installing creates a new component instance; upgrading changes the desired revision for an existing component instance; applying records exactly which desired revision became production.

## Scope

This slice includes:

- A stable source-template identity that threads related revisions together.
- Immutable template revisions that own parsed Terraform variables.
- Stack templates that can track a source template while pointing at desired and last-applied revisions.
- Editable stack-template config before plan and apply.
- Upgrade flow from an existing installed component to a newer revision without creating a duplicate install.
- Run-time snapshots of the selected revision and config so plans and applies remain auditable.
- Preservation of the ability to install the same source template multiple times in one stack.

This slice does not include:

- Scheduled refresh polling for all template sources.
- Automatic production upgrades.
- Destructive migration of existing Terraform workspaces.
- Secret variable support.
- A full template catalog or stack dashboard redesign.

## Current State

`templates` currently stores one registered template for one resolved commit SHA and root path. It also carries source fields such as repo owner, repo name, root path, and source ref.

`template_variables` belongs to `templates.id`.

`stack_templates` currently stores the installed template instance and points at `template_id`. It also owns workspace name, config JSON, lifecycle, and last-applied run metadata.

This works for first install, but not for upgrades. If a mutable source ref resolves to a new SHA, registration creates a new template row. Installing that new template creates another `stack_templates` row and therefore another running component, instead of upgrading the existing production component.

## Target Data Model

### Source Template

`source_templates` stores the stable logical template identity.

Fields:

- `id`
- `tenant_id`
- `repo_owner`
- `repo_name`
- `root_path`
- `source_ref`
- `latest_template_revision_id`
- `created_at`
- `updated_at`

The unique identity is:

```text
tenant_id, repo_owner, repo_name, root_path, source_ref
```

The `source_ref` is part of the source identity because two installs may intentionally track different channels such as `main`, `release`, or `v1`. If the same commit is reachable from multiple refs, those refs can still have separate source-template rows.

### Template Revision

`template_revisions` stores immutable resolved template metadata. This is the renamed role of the current `templates` table.

Fields:

- `id`
- `tenant_id`
- `source_template_id`
- `repo_owner`
- `repo_name`
- `source_ref`
- `resolved_commit_sha`
- `root_path`
- `name`
- `description`
- `tags_json`
- `status`
- `created_at`

The unique identity is:

```text
tenant_id, source_template_id, resolved_commit_sha
```

Each revision owns its variable schema. Variable rows move from belonging to `template_id` to belonging to `template_revision_id`.

### Stack Template

`stack_templates` remains the installed component table. One row means one component instance inside one stack.

Fields added or renamed:

- `component_key`
- `source_template_id`
- `desired_template_revision_id`
- `last_applied_template_revision_id`
- `desired_config_json`

Existing fields retained:

- `id`
- `tenant_id`
- `stack_id`
- `workspace_name`
- `last_applied_run_id`
- `last_applied_at`
- `created_by`
- `lifecycle`

The active installed-instance uniqueness is:

```text
tenant_id, stack_id, component_key
```

where lifecycle is active. There is intentionally no uniqueness on `stack_id, source_template_id`, because a stack must be able to install the same template multiple times with different component keys, workspaces, and configs.

The `source_template_id` can be inferred through `desired_template_revision_id`, but storing it directly makes common queries simple:

- show all installs tracking one source template
- find updates available for existing stack templates
- list repeated installs of one source inside a stack

## Naming Compatibility

The existing API and code use `TemplateID` for what will become `TemplateRevisionID`. To keep the migration manageable, the first implementation may keep Go type names as `TemplateID` while introducing database columns named `source_template_id`, `desired_template_revision_id`, and `last_applied_template_revision_id`.

The product model should still be documented and treated as:

```text
source template -> template revisions -> stack template desired/applied pointers
```

Follow-up cleanup can rename code-level types from `TemplateID` to `TemplateRevisionID` when the API surface is stable.

## Registration Flow

Template registration creates or reuses a source template and creates or reuses a revision.

Flow:

1. User registers repo owner, repo name, source ref, and root path.
2. Sync clones the source ref and resolves `HEAD` to a commit SHA.
3. Sync creates or reads `source_templates` by tenant, repo owner, repo name, root path, and source ref.
4. Sync creates or reads `template_revisions` by source template and resolved commit SHA.
5. If the revision is new, sync parses variables and persists them for that revision.
6. Sync updates `source_templates.latest_template_revision_id` to the resolved revision.
7. The registration record points at the created or reused revision.

If a ref still resolves to the same SHA, registration reuses the existing revision. If the ref moves, registration creates a new revision under the same source template.

## Install Flow

Installing always creates a new stack-template component instance.

Request fields:

- `template_revision_id`
- `component_key`
- `config`
- `actor`

Service behavior:

1. Load the requested template revision.
2. Read its `source_template_id`.
3. Validate config against that revision's variables.
4. Create `stack_templates` with:

```text
source_template_id = revision.source_template_id
desired_template_revision_id = requested revision
last_applied_template_revision_id = empty
desired_config_json = validated config
component_key = requested or generated key
workspace_name = stable workspace for this stack_template_id
```

This preserves repeated installs because `component_key` distinguishes component instances, not template source.

## Config Edit Flow

Config edits update desired config for an existing stack template before plan or apply.

Request:

```text
PATCH /v1/tenants/{tenant_id}/stack-templates/{stack_template_id}/config
```

Body:

```json
{
  "config": {
    "region": "us-east-1"
  },
  "actor": "user_123"
}
```

Service behavior:

1. Load the stack template.
2. Load variables for `desired_template_revision_id`.
3. Validate config against the desired revision's variables.
4. Update `desired_config_json`.

This does not change production by itself. Production changes only after an apply completes successfully.

## Upgrade Flow

Upgrading changes the desired revision for an existing stack template.

Request:

```text
POST /v1/tenants/{tenant_id}/stack-templates/{stack_template_id}/upgrade
```

Body:

```json
{
  "target_template_revision_id": "template_456",
  "config": {
    "region": "us-east-1"
  },
  "actor": "user_123"
}
```

If the target revision is omitted, the service uses `source_templates.latest_template_revision_id`.

Service behavior:

1. Load the stack template.
2. Load the target revision.
3. Ensure the target revision belongs to the same `source_template_id` as the stack template.
4. Validate supplied config against target revision variables.
5. If config is omitted, attempt a name-based carry-forward from current desired config:
   - keep values whose variable names still exist
   - drop values for removed variables
   - fail with a validation error if a new required variable has no default and no carried value
6. Set `desired_template_revision_id` to the target revision.
7. Set `desired_config_json` to the validated target config.

This operation does not touch `last_applied_template_revision_id`. Until apply succeeds, production still reflects the old applied revision.

## Run Snapshot Flow

Starting a run snapshots desired state at that moment.

`template_runs` should store:

- `stack_template_id`
- `template_revision_id`
- `source_template_id`
- `config_json`
- `selected_ref`
- `resolved_commit_sha`
- `workspace_name`
- `operation`
- status and audit fields

Plan/apply behavior:

1. Load the stack template.
2. Read `desired_template_revision_id` and `desired_config_json`.
3. Load the desired revision for source metadata and resolved SHA.
4. Create a template run with revision and config snapshots.
5. Execute Terraform from the snapshot, not from mutable stack-template fields.

If a user edits config or upgrades the stack template while a run is active, the existing run remains honest and reproducible. A later run picks up the new desired state.

## Apply Completion

When an apply run reaches successful terminal completion, update the installed component:

```text
last_applied_run_id = run.id
last_applied_template_revision_id = run.template_revision_id
last_applied_at = now()
```

The stack template is up to date when:

```text
desired_template_revision_id = last_applied_template_revision_id
and desired_config_json matches the last successful apply snapshot
```

The first implementation may not store a separate last-applied config hash on `stack_templates`; run history still provides the applied config snapshot. A later slice can add `last_applied_config_hash` for faster drift/status queries.

## Update Detection

A stack template has an update available when:

```text
source_templates.latest_template_revision_id != stack_templates.desired_template_revision_id
```

A stack template has a staged but unapplied upgrade when:

```text
desired_template_revision_id != last_applied_template_revision_id
```

For never-applied stack templates, `last_applied_template_revision_id` is empty and the UI should show the component as installed but not applied.

## API Summary

Keep existing registration endpoints, but responses should eventually expose both source and revision IDs:

```text
POST /v1/tenants/{tenant_id}/templates
GET  /v1/tenants/{tenant_id}/template-registrations/{registration_id}
GET  /v1/tenants/{tenant_id}/templates/{template_revision_id}/variables
```

Add:

```text
PATCH /v1/tenants/{tenant_id}/stack-templates/{stack_template_id}/config
POST  /v1/tenants/{tenant_id}/stack-templates/{stack_template_id}/upgrade
```

Existing run creation remains:

```text
POST /v1/tenants/{tenant_id}/stack-templates/{stack_template_id}/runs
```

but it must snapshot the desired revision and config into `template_runs`.

Install should accept a component key:

```text
POST /v1/tenants/{tenant_id}/stacks/{stack_id}/templates
```

Body:

```json
{
  "template_revision_id": "template_123",
  "component_key": "primary-vpc",
  "config": {
    "region": "us-east-1"
  },
  "actor": "user_123"
}
```

## Error Handling

Return validation errors when:

- config includes unknown variables for the selected revision
- config omits required variables without defaults
- an upgrade target belongs to a different source template
- an install uses a duplicate active component key in the same stack
- a stack template is destroyed or otherwise not upgradeable

Return not-found errors when:

- stack template does not belong to the tenant
- source template or revision does not belong to the tenant
- latest revision is requested but the source template has no latest revision

Concurrent updates should use a transaction. If two upgrades race, the last committed desired revision wins unless a later optimistic-locking field is added. Run snapshots prevent that race from corrupting already-started runs.

## Migration Strategy

Use additive migrations first.

1. Create `source_templates`.
2. Backfill one source template per existing unique tenant, repo owner, repo name, root path, and source ref in `templates`.
3. Add `source_template_id` to `templates` and backfill it.
4. Add source and desired/applied revision columns to `stack_templates`.
5. Backfill stack templates from existing `template_id`:

```text
source_template_id = templates.source_template_id
desired_template_revision_id = stack_templates.template_id
last_applied_template_revision_id = stack_templates.template_id when last_applied_run_id is not empty
```

6. Add `component_key` to `stack_templates` using a deterministic value based on existing ID or template name.
7. Add run snapshot columns to `template_runs`.
8. Keep old columns during the transition.
9. Update reads and writes to use new columns.
10. Drop or rename old columns only after all code paths are migrated.

The first implementation can keep the `templates` table name and treat it as template revisions. A table rename is optional and should be deferred unless it materially reduces confusion.

## Testing

App service tests should cover:

- registration creates or reuses a source template and revision
- install stores source, desired revision, component key, and desired config
- duplicate component key in one active stack is rejected
- same source template can be installed multiple times with different component keys
- config edit validates against desired revision variables
- upgrade rejects revisions from a different source template
- upgrade carries forward compatible variable values
- upgrade requires values for new required variables
- run creation snapshots desired revision and config
- successful apply records the run revision as last applied

Postgres tests should cover:

- migration creates and backfills source-template relationships
- active component-key uniqueness
- update available query semantics
- run snapshot persistence
- last-applied revision update after successful apply

API tests should cover:

- config edit endpoint request/response and errors
- upgrade endpoint request/response and errors
- install accepts component key and repeated installs with distinct keys
- run creation returns snapshot metadata

Frontend tests, if implemented in this slice, should cover:

- variable form edits can be saved before plan/apply
- update-available state appears when latest revision differs from desired revision
- repeated installs display separate component keys

## Open Follow-Ups

- Add scheduled source refreshes after manual registration and upgrade are stable.
- Add richer compatibility summaries that compare old and new variable schemas before upgrade.
- Add last-applied config hash on stack templates for faster "desired differs from applied" display.
- Rename Go types from `TemplateID` to `TemplateRevisionID` after API compatibility questions settle.
