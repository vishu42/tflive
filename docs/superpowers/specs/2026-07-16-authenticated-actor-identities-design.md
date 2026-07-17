# Authenticated Actor Identities Design

**Issue:** AUTH-008 / GitHub issue #10

**Status:** Approved

## Purpose

Remove browser-controlled actor identities from every existing mutation path.
The immutable Keycloak subject (`sub`) attached to the request context by the
authentication middleware becomes the only source for created-by,
requested-by, trigger, cancellation, and approval identities.

This change closes the actor-spoofing boundary without changing the shape or
meaning of existing persisted actor records.

## Scope

The affected operations are:

- register a template revision;
- create a stack;
- install a template into a stack;
- update installed-template configuration;
- stage an installed-template upgrade;
- start a template run;
- approve a template run; and
- cancel a template run.

The React API client and workflow console must stop accepting or sending actor
overrides for those operations. The now-unused editable Actor control is
removed during AUTH-008. AUTH-019 retains ownership of `/v1/me`, authenticated
identity display, tenant locking, logout, and session-state UX.

Tenant selection and authorization policy are not changed by this issue.
AUTH-009 and AUTH-013 retain ownership of those boundaries.

## Architecture

### Identity source

Application services obtain the normalized principal from the operation's
`context.Context` through `authn.PrincipalFromContext`. A focused helper in the
application package returns `traits.UserID(principal.Subject)` and fails when
the principal or subject is absent.

The affected application commands no longer contain `Actor`, `RequestedBy`,
`TriggerActor`, or `ApprovedBy` fields. Removing those fields prevents HTTP and
non-HTTP callers from supplying an alternate identity through a command.

The API handlers continue to pass the authenticated request context to the
application service. They never parse an authorization header or receive an
access token as a command value.

### Request schemas and decoding

The eight request structs contain only operation-specific data:

- template registration keeps repository and source fields;
- stack creation keeps name, slug, tags, and credential IDs;
- template installation keeps revision, component, ref, and config;
- configuration updates keep config;
- upgrades keep target revision and optional config;
- run starts keep operation;
- approval has an empty request shape; and
- cancellation keeps reason.

A shared API decoding helper uses `json.Decoder.DisallowUnknownFields`. The
affected handlers use it so removed identity keys and other undeclared keys
produce `400 Bad Request` instead of being silently ignored. In particular,
requests containing `actor`, `requested_by`, `trigger_actor`, or `approved_by`
cannot reach an application service. Approval accepts the client's canonical
empty JSON object while rejecting an object with legacy identity fields.

Malformed or schema-invalid bodies use the existing stable JSON error response
contract. Error bodies remain generic and do not include request values,
authorization headers, or token material.

### Application behavior

Each affected service operation resolves the actor from context before it
constructs a domain record or workflow signal. Command validation continues to
cover operation data, but no longer validates caller-supplied actor fields.

The resolved subject is assigned as follows:

| Operation | Domain destination |
|---|---|
| Register template | `TemplateRegistration.RequestedBy` |
| Create stack | `Stack.CreatedBy` |
| Install template | `StackTemplate.CreatedBy` |
| Update template config | authenticated context is required; no new actor column is written |
| Upgrade template | authenticated context is required; no new actor column is written |
| Start run | `TemplateRun.TriggerActor` |
| Approve run | `TemplateRunApproval.ApprovedBy` and `ApprovalSignal.ApprovedBy` |
| Cancel run | `TemplateRunCancellation.RequestedBy` and `CancelSignal.RequestedBy` |

Configuration updates and upgrades require an authenticated principal even
though their current persistence methods do not record a new actor value. This
keeps the mutation boundary uniform and prepares those paths for later audit
work without accepting anonymous application-service calls.

### Missing-principal behavior

Authentication middleware protects every `/v1` route before a handler runs.
The application layer nevertheless fails closed if it is called without a
principal or with an empty subject. A dedicated application error represents
that condition, and the API maps it to the existing unauthenticated response
contract. No repository call or workflow signal occurs after this failure.

This guard covers direct application-service callers and makes the security
invariant independently testable below the HTTP middleware.

### Persistence and workflow compatibility

Existing Postgres columns remain text columns with their current names and
constraints. No migration is required. New values are Keycloak subject strings,
which are compatible with those columns. Persistence adapters receive only
`traits.UserID` values and never receive, store, or log access tokens.

Temporal workflow inputs and signals retain their current domain fields so
workflow history remains compatible. `trigger_actor`, `approved_by`, and
`requested_by` carry the authenticated subject supplied by the application
service. Workers never receive browser authority or bearer tokens.

### Frontend behavior

The React API client removes identity properties from all affected request
interfaces. Call sites send only operation data, and the approval client owns
its canonical empty request rather than accepting a caller-provided body.

The workflow console removes its placeholder actor state and editable Actor
input. Response models keep fields such as `created_by`, `requested_by`, and
`trigger_actor` because those are server-produced attribution records, not
browser-controlled inputs.

## Data Flow

1. Authentication middleware verifies the bearer token and stores a normalized
   principal in the request context.
2. Strict request decoding rejects undeclared fields, including legacy actor
   overrides.
3. The handler builds an actor-free application command and passes the request
   context to the service.
4. The service reads the immutable subject from the principal and converts it
   to `traits.UserID`.
5. The service writes that subject into the applicable domain record and, for
   approval or cancellation, the applicable Temporal signal.
6. Postgres and Temporal store only the subject string. The access token remains
   at the authentication boundary.

## Testing Strategy

Development follows red-green-refactor cycles.

### API tests

- Exercise each affected successful mutation with an authenticated principal
  whose subject differs from all submitted domain values.
- Assert that the resulting application/domain record uses the authenticated
  subject.
- Submit each legacy identity key to a representative compatible endpoint and
  assert `400 Bad Request` with no repository mutation or workflow signal.
- Cover a missing principal as a fail-closed path.
- Retain malformed JSON and application-error mapping coverage.

### Application-service tests

- Build contexts containing realistic Keycloak subject strings.
- Assert the subject is copied into created, requested, trigger, cancellation,
  and approval records.
- Assert approval and cancellation signals carry the same subject as their
  persisted records.
- Assert a missing or empty-subject principal fails before persistence or
  workflow dispatch.
- Remove tests that construct commands with browser-controlled actor fields.

### Workflow and persistence tests

- Use subject-shaped values in workflow and Temporal dispatcher signal tests
  and verify signals preserve them unchanged.
- Verify Postgres round-trips subject strings through every existing actor
  column without a schema migration.
- Keep assertions that approval and cancellation state transitions remain
  unchanged.

### Frontend tests

- Update API-client calls to omit all actor fields.
- Assert serialized request bodies contain only operation data.
- Assert approval serializes the canonical empty request and cancellation keeps
  only its reason.
- Run the production TypeScript/Vite build to prove the removed actor state and
  request properties have no remaining consumers.

## Documentation and Completion

No runtime configuration or operational procedure changes. The sprint backlog
entry for AUTH-008 changes from `Not Started` to `Done` after all backend tests,
frontend tests, and the frontend production build pass.

Completion verification includes a repository search confirming that legacy
identity keys remain only in server-produced response/domain models, database
schema compatibility code, explicit spoof-rejection tests, historical design
text, and later backlog requirements—not in browser request schemas or mutation
handler request structs.
