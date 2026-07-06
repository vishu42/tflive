# Template Registration Design

## Purpose

Add real Terraform template registration for public GitHub repositories. Registration accepts a repo, ref, and root path; resolves the ref to an immutable commit SHA; clones the source; reads template metadata; infers Terraform variables; and persists the result for later stack installation.

The key product guarantee is that an existing stack template never silently changes because a mutable tag moved. Template rows are immutable at the resolved commit SHA boundary, and each registration request is tracked separately as an async registration attempt.

## Scope

This slice includes:

- An API entry point to request template registration for a public GitHub repository.
- App use case, repository ports, and workflow dispatch for template registration.
- Postgres tables for registration attempts, immutable templates, and inferred template variables.
- Temporal dispatch and worker registration for `TemplateSyncWorkflow`.
- Worker-side activities to clone a public repo/ref, resolve the commit SHA, read `template.yaml`, parse Terraform variables, and persist the synced template metadata.
- Validation that rejects sensitive Terraform variables because secret template input values are not supported in the MVP.

This slice does not include:

- GitHub App authentication, installation tokens, or private repository access.
- Stack upgrade flows from one template version to another.
- Template deletion, archival, or refresh scheduling.
- UI work.
- Managed Terraform backends or execution credential handling.

## Data Model

`template_registrations` records every registration request and sync result. It is the API-visible async unit.

Fields:

- `id`
- `tenant_id`
- `repo_owner`
- `repo_name`
- `source_ref`
- `root_path`
- `status`
- `template_id`
- `resolved_commit_sha`
- `requested_by`
- `requested_at`
- `completed_at`
- `error_summary`

Statuses:

- `pending`
- `running`
- `completed`
- `invalid`
- `failed`

`templates` stores immutable registered template metadata. One row represents one resolved source tree at one root path.

Fields:

- `id`
- `tenant_id`
- `repo_owner`
- `repo_name`
- `source_ref`
- `resolved_commit_sha`
- `root_path`
- `name`
- `description`
- `tags`
- `status`
- `created_at`

The unique identity is:

```text
tenant_id, repo_owner, repo_name, root_path, resolved_commit_sha
```

The `source_ref` remains useful display/audit data, but it is not the immutable identity. If `v0.0.1` moves to a new commit, the next registration creates a new template row. If `v0.0.1` still resolves to the same commit, the registration links to the existing template row.

`template_variables` stores inferred non-secret Terraform input metadata for one immutable template.

Fields:

- `template_id`
- `name`
- `type_expression`
- `description`
- `required`
- `has_default`
- `sensitive`
- `has_validation`

The primary key is `template_id, name`.

## API Behavior

Add:

```text
POST /v1/tenants/{tenant_id}/templates
```

Request body:

```json
{
  "repo_owner": "hashicorp",
  "repo_name": "example-template",
  "source_ref": "v0.0.1",
  "root_path": "modules/vpc",
  "requested_by": "user_123"
}
```

Response:

```text
202 Accepted
```

Body is the created `TemplateRegistration` record with `pending` status.

Add read endpoints for the async result:

```text
GET /v1/tenants/{tenant_id}/template-registrations/{registration_id}
GET /v1/tenants/{tenant_id}/templates/{template_id}/variables
```

The registration read endpoint lets callers poll for completion. The variables endpoint exposes the inferred schema once a template is active.

## App Flow

`RegisterTemplate` validates tenant ID, repo owner, repo name, source ref, root path, and actor. It creates a `TemplateRegistration` in `pending` status and dispatches `TemplateSyncWorkflow` with the registration ID and source fields.

The service returns after workflow dispatch. It does not clone repositories or parse Terraform files inside the API process.

Like the existing run dispatch path, this first slice accepts the risk that the registration row can be persisted while workflow dispatch fails. The error is returned to the caller if dispatch fails; an outbox can be added later.

## Workflow And Activities

`TemplateSyncWorkflow` is deterministic and thin:

1. Record registration status `running`.
2. Execute `SyncTemplate` activity.
3. On success, record `completed` with the resulting template ID and resolved SHA.
4. On validation failure, record `invalid` with an error summary.
5. On unexpected infrastructure failure, record `failed` with an error summary.

`SyncTemplate` activity owns side effects:

1. Create a temporary workspace.
2. Clone the public GitHub repository at the requested ref.
3. Resolve the checked-out `HEAD` commit SHA.
4. Validate that `root_path` is a safe relative path and exists.
5. Read optional `template.yaml` from `root_path`.
6. Parse Terraform variable blocks from root module `.tf` files.
7. Reject the registration if any variable is `sensitive = true`.
8. Persist or reuse an immutable template row for the resolved SHA.
9. Replace variable rows for the immutable template row only when inserting a new template row.
10. Clean up the temporary workspace.

Public repository clone URLs use:

```text
https://github.com/{repo_owner}/{repo_name}.git
```

No credentials are injected.

## Parsing Rules

`template.yaml` is metadata-only and optional. If absent, the template name falls back to the repository name or root path basename. Supported fields are:

- `name`
- `description`
- `tags`

Terraform variables are inferred from root module `.tf` files under `root_path`. Parsing must use HCL/Terraform parser libraries, not regex.

For each variable:

- `name` comes from the variable block label.
- `type_expression` is the source expression for `type`, or empty if omitted.
- `description` is the string value of `description`, or empty if omitted.
- `required` is true when no `default` attribute exists.
- `has_default` is true when `default` exists.
- `sensitive` is true only when `sensitive = true`.
- `has_validation` is true when one or more `validation` blocks exist.

If any variable is sensitive, registration is marked `invalid`. The MVP does not support secret template input values.

## Error Handling

Expected user-correctable failures mark the registration `invalid`:

- Repository or ref cannot be cloned.
- Root path is unsafe or missing.
- `template.yaml` is malformed.
- Terraform files cannot be parsed.
- Any variable is sensitive.

Unexpected persistence or process failures mark the registration `failed` when the workflow can record the failure. Errors are wrapped with operation context in app, workflow, activity, and repository code.

API validation failures return `400`. Missing registration/template reads return `404`. Registration dispatch failures return `500` after the pending row has been attempted.

## Testing

App tests cover:

- Successful registration creates a pending registration and dispatches the workflow.
- Invalid commands are rejected.
- Workflow dispatch errors are wrapped and returned.
- Registration and variable reads are tenant-scoped.

API tests cover:

- `POST /templates` request mapping and `202` response.
- Invalid JSON and invalid app command mapping.
- Registration read endpoint.
- Template variables endpoint.

Temporal tests cover:

- Dispatcher starts `TemplateSyncWorkflow` with deterministic workflow ID `template-sync/{tenant_id}/{registration_id}`.
- Worker command registers `TemplateSyncWorkflow` and sync activities.
- Workflow records running, completed, invalid, and failed states.

Activity tests cover:

- Public clone command construction through an injectable git runner.
- Ref resolution to commit SHA.
- Safe root path validation.
- `template.yaml` metadata parsing.
- Terraform variable inference.
- Sensitive variables make registration invalid.
- Existing template by resolved SHA is reused.

Postgres tests cover:

- Migrations create `template_registrations`, `templates`, and `template_variables`.
- Store satisfies new app repository ports.
- Creating and reading registration attempts.
- Upserting or reusing immutable templates by resolved commit SHA.
- Replacing variables for a new template row.
- Tenant scoping for reads.

`go test ./...` remains the final verification command.
