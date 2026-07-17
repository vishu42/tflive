# Configured Single-Tenant Boundary Design

**Issue:** AUTH-009 / GitHub issue #11

**Status:** Approved

## Purpose

Prevent authenticated callers from selecting an arbitrary tenant through an
HTTP path. Every tenant-scoped request must match the single tenant already
validated from `TFLIVE_TENANT_ID` before an API handler can access application
services, repositories, log artifacts, or future authorization data.

The React application must use deployment configuration for tenant context
rather than presenting tenant identity as an editable user choice.

## Root Cause

The API currently registers sixteen routes containing `{tenant_id}`. Each
handler converts `request.PathValue("tenant_id")` directly to
`traits.TenantID` and passes it to an application service. The API server does
not receive `cfg.Security.TenantID`, so the validated runtime tenant never
participates in request routing or handler execution.

The repository layer correctly scopes reads and writes by the tenant it is
given. That behavior becomes a vulnerability at the HTTP boundary because an
authenticated caller can choose which tenant value the repository receives.
Existing handler tests demonstrate this data flow by asserting that arbitrary
path tenant values are forwarded unchanged.

The frontend compounds the problem by keeping `tenantID` in editable React
state and rendering it as an input. Every API client call then builds its URL
from that browser-controlled value.

## Scope

The boundary applies to every existing tenant-scoped route:

- template registration and template revision list, detail, and variables;
- stack creation, list, detail, template installation, configuration, and
  upgrade;
- template run creation and detail;
- run log metadata and artifact-backed log body reads; and
- run approval and cancellation.

Health remains tenantless and public. Authentication remains responsible for
protecting `/v1`; authorization policy, `/v1/me`, and server-derived frontend
identity remain owned by later AUTH tickets.

## Backend Architecture

### Required server configuration

`NewServer` and `NewAuthenticatedServer` require a configured
`traits.TenantID`. The `Server` stores that value. Requiring it in both
constructors keeps authenticated production servers and unauthenticated unit
test servers on the same tenant boundary.

`cmd/api` passes `cfg.Security.TenantID` to `NewAuthenticatedServer`. The
runtime configuration loader remains the source of tenant syntax validation;
the server does not define a second configuration parser.

### Tenant-scoped route registration

All sixteen tenant routes are registered through one focused
`handleTenantRoute` helper rather than directly through `http.ServeMux`. The
helper wraps the route handler with the configured-tenant check. Health is
registered directly because it has no tenant scope.

The guard executes after `ServeMux` has matched the route, so
`request.PathValue("tenant_id")` is available, and before the existing handler
decodes a body or calls an application service. Existing handlers may continue
to pass the matched tenant to services after the guard succeeds.

This registration pattern centralizes the invariant and makes a future
tenant-scoped route visibly different from a public or tenantless route.

### Request order

Authenticated production requests flow through these boundaries:

1. authentication middleware verifies the bearer token and attaches the
   normalized principal;
2. `http.ServeMux` matches the method and path;
3. the tenant guard compares `{tenant_id}` exactly with the configured tenant;
4. the existing API handler decodes and validates request data; and
5. the application service accesses repositories, workflows, logs, or future
   authorization adapters.

Authentication intentionally remains outside the tenant guard. An
unauthenticated request receives the existing `401` response without learning
whether a tenant path is configured. An authenticated request with the correct
tenant proceeds normally.

## Failure Behavior

An authenticated request whose captured tenant does not exactly equal the
configured tenant receives the existing generic `404/not_found` JSON response.
The response does not distinguish a well-formed tenant belonging to another
deployment from a malformed captured tenant and does not depend on whether any
referenced resource exists.

The guard returns before body decoding, application-service invocation,
repository access, workflow dispatch, log reads, or future authorization
checks. Malformed request bodies therefore cannot bypass or obscure the tenant
decision.

Paths with a missing tenant segment do not match a tenant route and retain
`http.ServeMux`'s `404` behavior. Encoded separators and other malformed values
that are captured by a route are rejected by the same exact comparison. The
configured tenant is already guaranteed to satisfy the runtime validator, so a
captured value can only pass by being the configured valid value.

## Frontend Configuration

A focused frontend configuration module reads
`VITE_TFLIVE_TENANT_ID`, trims it, and validates the same tenant syntax used by
the backend: 1 through 128 ASCII characters, an ASCII alphanumeric first
character, and only ASCII alphanumerics, underscore, or hyphen thereafter.

`tenant_123` remains only as the documented local-development fallback. A
production build with a missing or malformed value fails closed with a clear
configuration error before issuing API requests. Deployment documentation
requires `VITE_TFLIVE_TENANT_ID` to equal backend `TFLIVE_TENANT_ID`.

`App` imports one configured tenant constant instead of storing tenant in
React state. The Tenant control becomes read-only context in the application
header. Existing API client functions continue accepting a tenant argument and
constructing the same URLs; application call sites pass the configured
constant. Keeping the client boundary explicit avoids an unrelated client
refactor and leaves room for AUTH-019 to replace build-time context with
server-derived authenticated state.

A frontend/backend configuration mismatch is an availability/configuration
error, not a boundary escape: the backend rejects every mismatched URL before
resource access.

## Testing Strategy

Development follows red-green-refactor cycles.

### API boundary tests

- Add a table covering every tenant-scoped method and route shape with a
  mismatched but well-formed tenant.
- Assert every cross-tenant request returns the same generic `404/not_found`
  response.
- Use empty or malformed mutation bodies to prove tenant rejection occurs
  before decoding.
- Assert repository, workflow, and log-reader spies remain untouched for every
  rejected request.
- Add focused matching, missing-segment, malformed, encoded-separator, and
  cross-tenant cases.
- Retain existing successful route tests as matching-tenant coverage after
  updating server construction with the configured tenant.

### Authentication and startup tests

- Verify a missing credential still returns `401` before tenant evaluation.
- Verify a valid credential with the configured tenant reaches the handler.
- Verify a valid credential with another tenant receives `404` and does not
  reach the service.
- Extend `cmd/api` wiring coverage to prove `cfg.Security.TenantID` reaches the
  authenticated server.

### Frontend tests

- Unit test frontend tenant resolution for explicit, trimmed, missing
  development, missing production, and malformed values.
- Render the application shell and assert the configured tenant is visible but
  no editable Tenant input exists.
- Retain API-client URL tests to prove the configured value is encoded into all
  existing tenant paths.
- Run the frontend test suite and production Vite build.

## Documentation and Completion

`.env.example` documents `VITE_TFLIVE_TENANT_ID=tenant_123` beside
`TFLIVE_TENANT_ID=tenant_123`. Authentication operations documentation explains
that the values must match, that the backend value is authoritative, and that
authenticated tenant mismatches intentionally return `404`.

After focused and full backend tests, frontend tests, and the frontend
production build pass, AUTH-009 changes from `Not Started` to `Done` in
`docs/sprint/authn_and_authz/README.md`. No database migration, authorization
model change, new API endpoint, or secret-handling change is required.
