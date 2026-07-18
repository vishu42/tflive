# Endpoint Permission Matrix Design

**Issue:** AUTH-013 / GitHub issue #15  
**Goal:** Enforce global and stack-scoped authorization consistently on every existing API endpoint without disclosing inaccessible resource existence.

## Scope

This change applies the existing `internal/authz.Authorizer` port to the HTTP API's current application use cases. It uses the configured OpenFGA model for stack decisions and the authenticated Keycloak principal's normalized global roles for tenant-global template catalog decisions.

It does not add access-management endpoints, audit records, self-approval protection, frontend permission presentation, or a tenant/platform object to the OpenFGA model. Those concerns remain with AUTH-014 through AUTH-020 and the documented future global-authorization follow-up.

## Permission Matrix

| Route | Authorization rule |
|---|---|
| `GET /healthz` | Public health probe. |
| `POST /v1/tenants/{tenant_id}/template-revisions` | Global `platform-admin` or `stack-creator`. |
| `GET /v1/tenants/{tenant_id}/template-revisions` | Global `platform-admin` or `stack-creator`. |
| `GET /v1/tenants/{tenant_id}/template-registrations/{registration_id}` | Global `platform-admin` or `stack-creator`. |
| `GET /v1/tenants/{tenant_id}/template-revisions/{template_revision_id}/variables` | Global `platform-admin` or `stack-creator`. |
| `POST /v1/tenants/{tenant_id}/stacks` | Existing global `platform-admin` or `stack-creator` rule. |
| `GET /v1/tenants/{tenant_id}/stacks` | `ListAccessibleStacks(can_view)`, except `platform-admin` lists all tenant stacks. |
| `GET /v1/tenants/{tenant_id}/stacks/{stack_id}` | `can_view`. |
| `POST /v1/tenants/{tenant_id}/stacks/{stack_id}/templates` | `can_operate`. |
| `PATCH /v1/tenants/{tenant_id}/stack-templates/{stack_template_id}/config` | Resolve stack template to its stack, then `can_operate`. |
| `POST /v1/tenants/{tenant_id}/stack-templates/{stack_template_id}/upgrade` | Resolve stack template to its stack, then `can_operate`. |
| `POST /v1/tenants/{tenant_id}/stack-templates/{stack_template_id}/runs` | Resolve stack template to its stack, then `can_operate`. |
| `GET /v1/tenants/{tenant_id}/template-runs/{run_id}` | Resolve run through its stack template to its stack, then `can_view`. |
| `GET /v1/tenants/{tenant_id}/template-runs/{run_id}/logs` | Resolve run through its stack template to its stack, then `can_view`. |
| `GET /v1/tenants/{tenant_id}/template-runs/{run_id}/logs/{phase}` | Resolve run through its stack template to its stack, then `can_view`. |
| `POST /v1/tenants/{tenant_id}/template-runs/{run_id}/approval` | Resolve run through its stack template to its stack, then `can_approve`. |
| `POST /v1/tenants/{tenant_id}/template-runs/{run_id}/cancellation` | Resolve run through its stack template to its stack, then `can_operate`. |

`can_manage_access` has no current HTTP route. AUTH-017 will use it for grant read and mutation endpoints.

## Enforcement Design

Authorization belongs in `internal/app`, before a use case performs its protected action. This keeps the policy boundary independent of HTTP and prevents future callers from bypassing enforcement. The API remains responsible only for decoding commands and mapping stable application errors.

The service adds narrow helpers that:

- require the authenticated principal and construct its canonical `authz.Subject`;
- recognize `platform-admin` as a stack-permission bypass, except for later self-approval enforcement owned by AUTH-015;
- check an OpenFGA derived permission for a validated stack identifier;
- map an explicit stack-permission denial to protected not-found for reads and stable forbidden for mutations; and
- return underlying authorization dependency failures unchanged so the existing `authz.HTTPStatus` mapping emits stable `503` responses.

The service performs global catalog authorization with the existing normalized principal roles. A principal with neither `platform-admin` nor `stack-creator` receives the stable forbidden response before catalog repositories or workflows are called.

## Resource Resolution And Non-Disclosure

For a direct stack request, the service authorizes the supplied stack identifier before fetching the stack. A denied OpenFGA decision returns protected `404` without a repository call.

For a stack template or run request, the service first retrieves the minimal owning relationship needed to identify its stack. It then checks the owning stack permission before reading protected details or performing a mutation. If the relationship lookup cannot find the resource, or the later OpenFGA decision denies access, the externally observable result is the same protected `404` response. Resource lookup failures that are internal or authorization dependency failures retain their existing stable error behavior.

Run logs and log artifacts reuse the run-to-stack authorization path. Credential identifiers are returned only as part of an authorized stack response and are never exposed through a separate existing route.

## List Behavior

For non-administrators, `ListStacks` constructs the caller subject and invokes `ListAccessibleStacks` with `can_view`. It passes only the returned stack IDs to a repository query scoped to the configured tenant. The repository must not fall back to the tenant-wide list when OpenFGA returns no objects.

`platform-admin` is the explicit global bypass and uses the existing tenant-wide stack listing. Any OpenFGA timeout, unavailable response, or malformed response fails closed and returns the existing stable authorization `503`, never an empty or complete list.

## Error Behavior

| Condition | HTTP response |
|---|---|
| Missing or invalid token | Existing `401` authentication response. |
| Global catalog caller lacks the required global role | Stable `403` forbidden response. |
| Stack-scoped mutation denied | Stable `403` forbidden response. |
| Stack-scoped read or inherited-resource lookup denied | Stable protected `404` not-found response. |
| Resource genuinely absent | Existing `404` not-found response. |
| OpenFGA timeout, outage, or malformed response | Existing stable authorization `503` response. |

All decisions are server-side. Frontend visibility is not part of authorization enforcement.

## Testing

Application and route-level API tests will cover:

- the complete route matrix, including global catalog access for platform administrators, stack creators, and ordinary users;
- stack list filtering through `ListAccessibleStacks(can_view)`, empty results, platform-administrator bypass, and authorization dependency failures;
- direct stack checks for owner, operator, approver, viewer, and unassigned principals;
- inherited stack-template, run, log, log-artifact, approval, and cancellation decisions;
- denied reads returning protected `404`, denied mutations returning `403`, and no mutation occurring after denial;
- OpenFGA dependency failure returning stable `503` and never allowing a protected operation; and
- existing tenant-boundary and authentication middleware behavior continuing unchanged.

Focused `internal/app` and `internal/api` tests will run during development, followed by `go test ./...`. `docs/authentication.md` will document this matrix and protected-resource behavior. AUTH-013 moves to `Done` in the sprint backlog only after all acceptance criteria and verification pass.
