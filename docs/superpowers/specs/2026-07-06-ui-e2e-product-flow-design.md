# UI E2E Product Flow Design

## Purpose

Add the first real product UI for tflive and the missing backend stack APIs required to test the product end to end from the browser. The UI should prove the main MVP loop: register a Terraform template, inspect inferred variables, create a stack, install the template with variable values, run `plan`, run `apply`, approve the apply, and inspect run status and logs.

This is intentionally a workflow console, not a full dashboard. It should make the core product loop usable without manual database seeding while leaving room for stack lists, template catalogs, live log streaming, auth, and richer dashboards later.

## Scope

This slice includes:

- A separate Vite and React frontend app under `web/`.
- Local development proxying from the frontend to the Go API.
- Backend APIs to create and read stacks and install registered templates into stacks.
- Postgres persistence for `stacks`.
- Reuse of existing `templates`, `template_variables`, `stack_templates`, `template_runs`, approvals, and log metadata.
- UI polling for registration status, run status, and logs.
- A single browser workflow that exercises registration through apply approval.

This slice does not include:

- Server-Sent Events or WebSockets.
- Full stack, template, or run list pages.
- Real authentication or authorization beyond tenant IDs and actor fields already passed by the UI.
- GitHub App connection UI.
- Secret variable collection.
- Managed backend provisioning.
- A formal design system package.

## Architecture

The Go API remains the only backend boundary used by the UI. The browser never reads Postgres, Temporal, object storage, or local log files directly.

Local development runs as two processes:

```text
API: http://localhost:8081
UI:  http://localhost:5173
```

The Vite dev server proxies `/v1/*` and `/healthz` to the Go API. This keeps browser calls same-origin in development and avoids introducing CORS as part of this slice.

The frontend uses small typed API helper functions and React component state. It does not add Redux, a router, a query library, or a global state framework in this first slice. Polling behavior lives behind small hooks/helpers so the transport can later change to SSE without rewriting product components.

The backend adds stack use cases in `internal/app`, Postgres repository methods in `internal/postgres`, and HTTP handlers in `internal/api`. These follow the existing service/repository boundary used by template registration and template runs.

## Product Flow

The first UI walks through one stack lifecycle:

```text
1. Register template
2. Poll registration until complete
3. Show inferred variables
4. Create stack
5. Install template into stack with variable values
6. Run plan
7. Review plan status and logs
8. Start apply
9. Approve apply when the run waits for approval
10. Watch final apply status and logs
```

The UI keeps a current workflow state in memory. The user can complete the happy path in one session. Backend records remain durable, and the first slice exposes created IDs in a compact details area so a developer can recover state manually after refresh. Dedicated list and resume pages are deferred.

The apply path uses the existing run and approval API model:

```text
POST /v1/tenants/{tenant_id}/stack-templates/{stack_template_id}/runs
poll GET /v1/tenants/{tenant_id}/template-runs/{run_id}
when status is waiting_approval, show Approve
POST /v1/tenants/{tenant_id}/template-runs/{run_id}/approval
continue polling until terminal
```

## Data Model

Add a `stacks` table:

```text
stacks
- id text primary key
- tenant_id text not null
- name text not null
- slug text not null
- tags_json jsonb not null default '{}'::jsonb
- default_credential_ids_json jsonb not null default '[]'::jsonb
- created_by text not null
- created_at timestamptz not null
```

The unique user-facing stack identity is:

```text
tenant_id, slug
```

The existing `stack_templates` table remains the installed-template table. It stores the selected template version, stable workspace name, user-provided non-secret configuration, last apply metadata, and lifecycle.

The existing `template_variables` table is used during install validation. Secret variables are already rejected during template registration for the MVP, so install only accepts non-sensitive config values.

## API Design

Add:

```text
POST /v1/tenants/{tenant_id}/stacks
GET  /v1/tenants/{tenant_id}/stacks/{stack_id}
POST /v1/tenants/{tenant_id}/stacks/{stack_id}/templates
```

`POST /stacks` creates a stack.

Request:

```json
{
  "name": "Acme Prod",
  "slug": "acme-prod",
  "tags": {
    "env": "prod"
  },
  "default_credential_ids": [],
  "actor": "user_123"
}
```

If `slug` is omitted, the app derives one from the name. The derived or provided slug must be unique per tenant.

Response:

```text
201 Created
```

Body is the created stack. API handlers should use explicit JSON response structs rather than returning `traits.Stack` directly, because the current trait type does not define the browser-facing JSON shape.

`GET /stacks/{stack_id}` returns the stack and its installed templates. This endpoint is intentionally UI-shaped enough to continue the workflow without requiring list endpoints.

Response shape:

```json
{
  "stack": {
    "id": "stack_123",
    "tenant_id": "tenant_123",
    "name": "Acme Prod",
    "slug": "acme-prod",
    "tags": {
      "env": "prod"
    },
    "default_credential_ids": []
  },
  "templates": [
    {
      "id": "stack_template_123",
      "stack_id": "stack_123",
      "template_id": "template_123",
      "selected_ref": "main",
      "workspace_name": "meg_acme_prod_template_123",
      "config": {
        "region": "us-east-1"
      },
      "lifecycle": "active"
    }
  ]
}
```

`POST /stacks/{stack_id}/templates` installs an immutable registered template into a stack.

Request:

```json
{
  "template_id": "template_123",
  "selected_ref": "main",
  "config": {
    "region": "us-east-1"
  },
  "actor": "user_123"
}
```

Response:

```text
201 Created
```

Body is the created installed stack template using an explicit API response struct.

Install validation:

- The stack belongs to the tenant.
- The template belongs to the tenant.
- The template status is `active`.
- Every required non-sensitive variable has a provided value.
- Unknown config keys are rejected for this first slice.
- Config values are stored as JSON without secret handling.
- The generated workspace name is non-empty and stable for that installed template.
- The installed template starts with lifecycle `active`.

## Backend App Design

Add app ports:

```text
StackRepository
- CreateStack(ctx, stack)
- GetStack(ctx, tenantID, stackID)
- GetStackWithTemplates(ctx, tenantID, stackID)

StackTemplateInstaller
- CreateStackTemplate(ctx, stackTemplate)
```

The concrete Postgres store can implement these alongside the existing repository interfaces.

Add use cases:

```text
CreateStack(command) (Stack, error)
GetStack(command) (StackView, error)
AddTemplateToStack(command) (StackTemplate, error)
```

`StackView` is an app/API view model containing one `Stack` plus its installed `StackTemplate` records. It is used for `GET /stacks/{stack_id}` only; list and dashboard views are deferred.

`CreateStack` validates tenant ID, name, slug, tags, default credential IDs, and actor. It creates a generated stack ID, derives a slug when needed, and persists the record.

`AddTemplateToStack` validates tenant ownership, active template status, template variable requirements, config keys, selected ref, and actor. It generates a stack template ID and workspace name, persists the installed template, and returns it.

Workspace names are generated with this rule:

```text
meg_{normalized_stack_slug}_{short_stack_template_id}
```

`normalized_stack_slug` uses lowercase letters, digits, and underscores. Hyphens and other non-alphanumeric characters become underscores. `short_stack_template_id` is the final 8 characters of the generated stack template ID. If the full workspace name exceeds 90 characters, the normalized slug portion is truncated before persistence.

## Frontend Design

The first app is an operational workflow console. It should feel like a quiet infrastructure tool: dense enough to scan, restrained in color, and centered on actions, state, and logs.

Suggested component split:

```text
App shell
Workflow stepper
TemplateRegistrationForm
RegistrationStatusPanel
VariableConfigForm
StackCreationForm
InstalledTemplateSummary
RunControlPanel
RunStatusTimeline
PhaseLogViewer
RunDecisionActions
```

The screen should avoid a marketing-style landing page. The first viewport is the usable workflow.

The frontend keeps the active records in local state:

```text
tenant_id
actor
registration
template_variables
stack
installed_stack_template
plan_run
apply_run
selected_log_phase
log_body
```

Polling rules:

- Registration polls every 1-2 seconds while status is `pending` or `running`.
- Run status polls every 1-2 seconds while status is non-terminal.
- Log metadata/body polling runs only for the selected run and phase.
- Polling stops on terminal registration and run states.
- Repeated poll failures show a retry state and do not clear the last known good data.

Terminal registration states:

```text
completed
invalid
failed
```

Terminal run states:

```text
completed
failed
canceled
```

The approval button appears only when the current apply run status is `waiting_approval`.

## Error Handling

API validation errors return `400` and should include the existing error response shape:

```json
{
  "error": "invalid_request",
  "message": "..."
}
```

Tenant-scoped missing records return `404`.

Conflicts such as duplicate slugs, inactive templates, missing required variables, unknown variables, non-runnable stack templates, non-approvable runs, and non-cancelable runs return `409`.

The UI displays backend validation messages near the related form or action. Registration `invalid` or `failed` states stop the workflow and show `error_summary`. Run `failed` and `canceled` states stop polling and keep logs visible. Missing log bodies are treated as a waiting/empty log state rather than a fatal workflow error.

## Testing

Backend tests:

- App tests for successful stack creation.
- App tests for slug derivation and duplicate slug conflict.
- App tests for install validation: tenant-owned stack, tenant-owned active template, required variables, unknown variables, and lifecycle.
- API tests for stack create/read/install request mapping and error mapping.
- Postgres tests for tenant-scoped stack reads, stack view reads, and installed template persistence.

Frontend tests use Vitest for non-DOM logic:

- API helper tests for request paths and error handling.
- Polling helper tests for stop-on-terminal behavior.

DOM/component tests are deferred unless the first implementation naturally adds React Testing Library.

Manual smoke test:

1. Start Postgres, Temporal, API, worker, and UI.
2. Register a public Terraform template.
3. Wait for registration completion.
4. Confirm variables render.
5. Create a stack.
6. Install the template with variable values.
7. Start a plan run and inspect status/logs.
8. Start an apply run.
9. Approve the apply when waiting.
10. Confirm terminal status and inspect logs.

## Future Work

- Stack, template, and run list pages.
- Resume workflow from durable IDs after page reload.
- SSE for run status and log updates.
- GitHub App connection and private repositories.
- Secret variable handling.
- Credential set selection and validation.
- Richer run artifact browsing.
- Destroy flow and whole-stack operations.
