# Template Revision Naming Design

## Context

The current revision-upgrade design introduced stable `source_templates` plus immutable revision rows, but the immutable revision object still uses the older `templates` table, `Template` Go type, and `template_id` API fields. That makes names like `DesiredTemplateID` ambiguous: the value is not a stable template identity, it is the desired immutable revision.

The product does not need backward compatibility yet, so this change should rename the model cleanly across storage, Go, HTTP JSON, and frontend types.

## Goals

- Make the stable source identity and immutable revision identity obvious from names.
- Replace `templates` with `template_revisions` in the database.
- Replace `Template` domain naming with `TemplateRevision` where the object represents one resolved SHA/root path.
- Use `template_revision_id` in public JSON wherever the value identifies a revision.
- Preserve the existing install behavior: the same source template can be installed multiple times in one stack via distinct `component_key` values.
- Preserve the existing upgrade behavior: upgrading changes the desired revision of one existing `stack_templates` row.

## Non-Goals

- No backward-compatible aliases, views, route shims, or duplicate JSON fields.
- No migration path for production data. Existing migrations can be edited directly because the database is not yet compatibility-bound.
- No UI workflow redesign beyond renaming API helpers/types/state to match the new contract.

## Domain Model

`SourceTemplate` remains the stable logical template identity. It represents a repo owner, repo name, source ref, and root path thread. Multiple immutable revisions hang off one source template.

`TemplateRevision` replaces `Template`. It represents one resolved source tree at one commit SHA and root path. Its ID type becomes `TemplateRevisionID`.

`TemplateVariable` belongs to a revision, so its field becomes `TemplateRevisionID`.

`StackTemplate` remains the installed component instance. Its revision-related fields become:

- `TemplateRevisionID`: the install-time revision for this component.
- `DesiredTemplateRevisionID`: the revision to snapshot into the next run.
- `LastAppliedTemplateRevisionID`: the revision from the last successful apply.

`TemplateRun` snapshots the revision used by that run. Its revision field becomes `TemplateRevisionID`.

## Database

The schema should use `template_revisions` as the immutable revision table.

Tables and columns:

- `source_templates.latest_template_revision_id` remains unchanged.
- `template_revisions.id` replaces `templates.id`.
- `template_variables.template_revision_id` replaces `template_variables.template_id`.
- `template_registrations.template_revision_id` replaces `template_registrations.template_id`.
- `stack_templates.template_revision_id` replaces `stack_templates.template_id`.
- `stack_templates.desired_template_revision_id` replaces `stack_templates.desired_template_id`.
- `stack_templates.last_applied_template_revision_id` replaces `stack_templates.last_applied_template_id`.
- `template_runs.template_revision_id` replaces `template_runs.template_id`.

Indexes and constraints should use revision names, for example `template_revisions_source_revision_idx` and foreign keys to `template_revisions(id)`.

## App And Repository Interfaces

Repository names should match the revision concept:

- `TemplateRepository` becomes `TemplateRevisionRepository`.
- `TemplateMetadataRepository` becomes `TemplateRevisionMetadataRepository`.
- `UpsertTemplateWithVariables` becomes `UpsertTemplateRevisionWithVariables`.
- `ListTemplates` becomes `ListTemplateRevisions`.
- `GetTemplate` becomes `GetTemplateRevision`.
- `GetTemplateVariables` becomes `GetTemplateRevisionVariables`.

Service command fields should use revision names:

- `AddTemplateToStackCommand.TemplateRevisionID`
- `UpgradeStackTemplateCommand.TargetTemplateRevisionID`
- `GetTemplateRevisionVariablesCommand.TemplateRevisionID`

The stack install use case can continue to be named `AddTemplateToStack` because the operation installs a template component into a stack. Its input ID is now clearly a revision ID.

## HTTP API

Routes that expose revision resources should be renamed:

- `POST /v1/tenants/{tenant_id}/template-revisions`
- `GET /v1/tenants/{tenant_id}/template-revisions`
- `GET /v1/tenants/{tenant_id}/template-revisions/{template_revision_id}/variables`

The stack install route stays:

- `POST /v1/tenants/{tenant_id}/stacks/{stack_id}/templates`

JSON fields that identify revisions should be renamed:

- Registration response: `template_revision_id`
- Install request: `template_revision_id`
- Stack template response: `template_revision_id`, `desired_template_revision_id`, `last_applied_template_revision_id`
- Template run response: `template_revision_id`
- Template variable response: `template_revision_id`
- Upgrade request: `target_template_revision_id`

No `template_id` compatibility fields should remain in these API shapes.

## Frontend

Frontend API types and helpers should mirror the public API:

- `Template` becomes `TemplateRevision`.
- `listTemplates` becomes `listTemplateRevisions`.
- `getTemplateVariables` becomes `getTemplateRevisionVariables`.
- Install requests send `template_revision_id`.
- UI state that stores selectable registered revisions should use names like `templateRevisions` and `selectedTemplateRevisionID`.

Visible UI copy can still say “template” where it refers to the user-facing concept of installing a template. Technical labels that display IDs should say “revision” when showing revision IDs.

## Testing

Add or update tests at each boundary:

- Postgres migration/repository tests should assert the `template_revisions` table exists and that revision IDs persist through install, run creation, and last-applied updates.
- App service tests should assert commands and snapshots use `TemplateRevisionID` fields.
- API tests should assert new routes and JSON field names, and should no longer send or expect `template_id`.
- Frontend client tests should assert calls use `/template-revisions` and `template_revision_id`.
- Full verification should run `go test ./...`, `npm test`, and `npm run build`.

## Rollout

Because backward compatibility is not required, implement this as a direct rename across migrations and code. The final repository should not contain old `templates` table references, `TemplateID` type usage for revision IDs, or public JSON fields named `template_id` where a revision is meant.
