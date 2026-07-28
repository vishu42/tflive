# Stack Environment Tab Design

## Goal

Add a dedicated Environment tab to each stack so users with access-management permission can create and delete stack-scoped credentials without navigating through the Template tab.

## Current Context

The workspace already contains the credential API client methods, React Query hooks, metadata types, and a reusable write-only `CredentialsPanel`. `StackTemplateScreen` currently renders both Stack credentials and StackTemplate credentials together. The stack detail shell currently exposes Overview, Template, Runs, and Access tabs.

## Design

Add a nested `/stacks/:stackId/environment` route and an `EnvironmentScreen` that renders the existing `CredentialsPanel` for Stack-scoped credentials only. Add an Environment `NavLink` to `StackDetailShell`, guarded by `canManageAccess` in the same way as Access.

Remove the Stack-scoped panel and its now-unused query/mutation wiring from `StackTemplateScreen`. Keep the StackTemplate-scoped credential panel in the Template tab, so template-specific overrides remain next to the template configuration they affect.

The Environment screen will use the existing `useStackCredentialsQuery`, `useCreateStackCredentialMutation`, and `useDeleteStackCredentialMutation` hooks. The API remains write-only for secret values: the list displays credential names and configured status, while the form clears the secret from local state after a successful create. No credential values are added to URLs, query keys, rendered markup, or API responses.

Update the panel copy to explain the naming convention relevant to Terraform: names beginning with `TF_VAR_` populate Terraform input variables, while provider credentials should use their provider-defined environment names. This is guidance only; the backend will continue to preserve names exactly as entered.

## Authorization and Error Handling

The route and navigation link are visible only to users with `canManageAccess`. The existing API authorization remains authoritative for create, list, and delete operations. Loading, mutation, and request errors use the existing panel and query error patterns. Stack-template credential behavior is unchanged.

## Testing

Add or update frontend tests to verify:

1. The stack shell renders the Environment tab for an authorized user and omits it for an unauthorized user.
2. The router maps `/stacks/:stackId/environment` to the Environment screen.
3. The Environment screen loads and renders Stack credential metadata, submits a new credential through the existing mutation, and deletes a credential through the existing mutation.
4. The Template screen continues to render template-scoped credentials and no longer renders the Stack-scoped panel.
5. Credential values remain write-only in the rendered UI.

No backend or database schema changes are required for this reorganization.

## Scope Boundaries

- No automatic `TF_VAR_` prefixing is added.
- No support for editing an existing secret is added; users delete and recreate it.
- No change is made to StackTemplate credential inheritance or runtime resolution.
- Existing unrelated working-tree changes are preserved.
