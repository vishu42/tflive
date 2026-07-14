# Authentication and Authorization Security Architecture

**Status:** Approved

**Date:** 2026-07-14

**Issue:** [AUTH-001](https://github.com/vishu42/tflive/issues/3)

**Scope:** Authentication and authorization foundation for the configured single tflive tenant

## Purpose

This document is the authoritative security design for the authentication and
authorization sprint. It defines the trust boundaries, identity propagation,
role semantics, authorization decisions, failure contracts, audit guarantees,
and threat mitigations that the implementation tickets must preserve.

Keycloak authenticates people and administers global roles. OpenFGA stores
per-stack relationships and derives stack permissions. The tflive API is the
only product enforcement point. Browser visibility is a usability aid and is
never an authorization control.

High availability, cross-region operation, disaster-recovery automation,
enterprise identity federation, SCIM, mandatory MFA policy, custom roles, and
multi-tenant realm design remain outside this sprint.

## Security Invariants

The following invariants apply to every implementation ticket:

1. Only `GET /healthz` is public. Every `/v1` route requires a valid Keycloak
   access token.
2. The API identifies a caller only by the validated token's immutable `sub`
   claim. Mutable names and email addresses are display data, not security keys.
3. Browser request data never selects the actor. Actor-like fields are rejected
   or ignored by decoding rules and never override the authenticated principal.
4. The configured tenant is the only accepted tenant. A path value cannot
   select another tenant or influence the authorization namespace.
5. The API authorizes every operation before product data, logs, artifacts,
   credentials, or workflow signals are exposed or mutated.
6. Stack children inherit the authorization decision of their owning stack.
7. Authentication and authorization dependency failures fail closed.
8. A requester cannot approve their own run, regardless of global or stack
   role.
9. Bearer tokens, passwords, client secrets, and usable production defaults are
   never persisted or logged.
10. Temporal workflows and workers receive immutable actor subject IDs, never
    bearer tokens.

## Deployment and Trust Boundaries

Production runs Keycloak, OpenFGA, the API, workers, Temporal, Postgres, and
object storage as platform-operated services in the same Kubernetes
environment. Namespace isolation and network policies restrict service access.

```text
Untrusted browser
    |
    | HTTPS: OIDC authorization code + PKCE, API bearer token
    v
Platform ingress
    |---------------------------> Keycloak browser endpoints
    |
    v
tflive API ---------------> Keycloak discovery/JWKS
    |  |  |
    |  |  +---------------> OpenFGA checks and relationship writes
    |  +------------------> Postgres product state, audit, durable intents
    +---------------------> Temporal commands and signals
                                   |
                                   v
                              tflive workers
                                   |
                                   v
                         logs, artifacts, providers
```

Keycloak uses one canonical HTTPS issuer URL. Browser redirects and API issuer
validation use that exact issuer. The API may reach the canonical endpoint
through cluster routing, but discovery metadata and the `iss` claim must remain
identical. Keycloak administration endpoints are restricted to platform
operators and backend service accounts. OpenFGA has no public ingress and is
reachable only by authorized backend workloads.

Production service-to-service traffic is encrypted. Workload credentials and
client secrets come from Kubernetes-backed secret delivery, are mounted or
injected at runtime, and are never embedded in images or committed files.
Development may use explicitly documented local HTTP endpoints and local-only
credentials; production mode rejects those settings.

### Component Responsibilities

| Component | Trusted responsibility | Must not do |
|---|---|---|
| Browser | Run the OIDC code flow, retain tokens in memory, present server-provided capabilities | Select actor or tenant identity, persist tokens, enforce security alone |
| Keycloak | Authenticate users, issue access tokens, hold global roles, administer user lifecycle | Store stack roles or decide stack access |
| API | Validate tokens, construct principals, enforce tenant and permission rules, create audits, authorize commands | Trust browser identities, send tokens downstream, fail open |
| OpenFGA | Store direct stack relations and derive stack permissions | Hold global roles, accept browser traffic, store product or audit state |
| Postgres | Store product state, immutable actor subjects, append-only audits, and durable relationship intents | Store bearer tokens, passwords, or Keycloak administrative tokens |
| Temporal | Durably execute previously authorized commands and record immutable actor subjects | Authenticate browser requests or reinterpret API permissions |
| Workers | Execute workflow activities using narrowly scoped runtime credentials | Receive browser tokens or expose authorization data |
| Object storage | Store redacted logs and artifacts under tenant/run keys | Decide access independently of the API |

The authorization decision for starting a long-running command is made when the
API accepts that command. A later role revocation blocks new commands but does
not silently rewrite an already-running workflow. Approval and cancellation are
new commands and require a fresh authorization decision when requested.

## Authentication and Principal Propagation

The React application is a public OIDC client using Authorization Code with
PKCE S256. Access and refresh tokens stay in memory. Refresh failure clears the
principal and starts a new sign-in flow rather than retrying indefinitely.

The API validates all of the following before constructing a principal:

- signature using an allowed asymmetric algorithm;
- exact issuer;
- API audience;
- expiry and not-before time;
- token type and required subject claim.

Discovery and JWKS responses are cached with bounded lifetimes. An unknown key
identifier triggers one immediate JWKS refresh. Key rotation does not require
an API restart. Cached keys may be used until their valid cache lifetime ends;
the API never accepts an unverifiable token because Keycloak is unavailable.

The request principal contains:

```text
subject: immutable Keycloak sub
display_name: optional presentation claim
email: optional presentation claim
global_roles: normalized set of recognized tflive global roles
```

Unknown roles are ignored. Tokens and unneeded claims are discarded after
verification. Application services obtain the principal from request context
or an explicit application command context; handlers and services never parse
authorization headers independently.

Actor columns already present in Postgres continue to store string values, but
new values are always the authenticated `sub`. Temporal payloads and signals
carry the same `sub`. No access token crosses the API boundary.

## Tenant Boundary

tflive has one configured tenant identifier. Startup validation rejects an
empty or malformed tenant value. Every tenant-scoped handler compares
`{tenant_id}` with the configured tenant before repository access, OpenFGA
calls, object-key construction, or workflow dispatch.

A missing route value, malformed tenant value, or mismatch returns the protected
not-found contract. The response does not reveal whether a resource exists in
the configured tenant. The frontend obtains the tenant from `/v1/me` and never
offers an editable tenant selector.

Tenant checks remain mandatory even though the deployment is single-tenant.
This prevents URL-controlled namespace confusion and keeps product, object
storage, and workflow identifiers consistently scoped.

## Role and Permission Model

### Global Roles

| Role | Source | Meaning |
|---|---|---|
| `platform-admin` | Validated Keycloak token | Administer tflive and bypass ordinary stack checks, except the constraints listed below |
| `stack-creator` | Validated Keycloak token | Create a stack and become its initial owner |

Global roles are evaluated by the API after token verification. They are not
copied into OpenFGA. A `platform-admin` bypass does not bypass authentication,
configured-tenant validation, input validation, audit requirements,
last-owner protection, dependency fail-closed behavior, or self-approval
prevention.

Production Keycloak access tokens have a maximum five-minute lifetime. Global
role changes therefore converge no later than the current access-token expiry.
The API does not cache global-role decisions beyond one request.

### Stack Roles and Derived Permissions

OpenFGA uses canonical identifiers:

```text
user:<keycloak-sub>
stack:<stack-id>
```

The versioned model defines only direct role relations and derived permissions:

```text
owner, operator, approver, viewer
can_view          = owner or operator or approver or viewer
can_operate       = owner or operator
can_approve       = owner or approver
can_manage_access = owner
```

Derived permission relations are never direct write targets. The API enforces
at most one direct role for a user on a stack. Assigning a different role is an
idempotent replacement that removes the old relation and establishes the new
one as one logical mutation.

Owners and platform administrators may list and manage grants. Target users
must resolve to enabled users in the tflive Keycloak realm. The backend
directory credential may search and read safe user attributes only; it cannot
create users, change passwords, delete users, or assign global roles.

Removing the last owner is rejected. A platform administrator recovery flow
must first establish and confirm a replacement owner. There is no ownerless
active stack state.

### Stack Creation

Stack creation requires `stack-creator` or `platform-admin`. The creator's
subject becomes the initial OpenFGA owner. A newly committed stack remains in a
non-usable provisioning state and is excluded from ordinary reads and lists
until the owner relation is confirmed with higher consistency. Failed
relationship delivery is retryable and visible to operators.

### Resource Inheritance

Installed templates, template revisions selected by a stack, runs, approvals,
logs, artifacts, and referenced credentials resolve to an owning stack. The
API performs that resolution in a tenant-scoped repository operation and then
checks the required stack permission. A child identifier never grants access
by itself.

Stack list endpoints use OpenFGA `ListObjects` or an equivalent non-leaking
authorized-object query. They do not load every stack and filter in browser
code. The first implementation does not cache positive OpenFGA decisions, so a
confirmed revocation affects the next request.

### Command Permission Semantics

| Operation | Required permission |
|---|---|
| Create stack | `stack-creator` or `platform-admin` |
| List/read stack and inherited resources | `can_view` or `platform-admin` |
| Edit stack, install/upgrade template, start/cancel run | `can_operate` or `platform-admin` |
| Approve eligible run | `can_approve` or `platform-admin`, plus self-approval check |
| List/search/manage stack access | `can_manage_access` or `platform-admin` |
| User/global-role administration | Keycloak Admin Console only |

The complete route-to-permission matrix is delivered by AUTH-013, but it may
only refine route coverage; it may not change the semantics above without a
new recorded architecture decision.

## Self-Approval Prevention

The subject that requests or triggers an apply is stored immutably with the run.
At approval time, the API loads that subject and compares it with the current
authenticated approver's `sub` before persisting or signaling approval.

Equality rejects the approval with `403 self_approval_forbidden`. The rule
applies to owners and platform administrators. A rejected attempt does not
signal Temporal and produces a security audit record. Successful approval
persists and signals the authenticated approver subject.

## Reliable Relationship Mutations

Postgres and OpenFGA do not share a transaction. tflive therefore uses a durable
desired-state and reconciliation pattern rather than pretending that a
cross-datastore transaction is atomic.

For stack ownership, grants, role replacements, and revocations:

1. The API requires an idempotency key and validates current authority.
2. One Postgres transaction writes the desired grant state, append-only audit
   record, and idempotent relationship intent.
3. A reconciler performs the OpenFGA write/delete using the configured model
   ID. Repeated delivery is safe.
4. The request path attempts immediate reconciliation and a higher-consistency
   confirmation.
5. Success is returned only after confirmation. If confirmation misses the
   bounded deadline, the API returns `503 authorization_write_unconfirmed`;
   the durable intent remains retryable, and a retry with the same idempotency
   key returns the eventual state rather than creating another mutation.
6. Permanent failures are surfaced through operator-visible status, metrics,
   and reconciliation tooling. They are never silently discarded.

An access mutation and its audit record are atomic in Postgres. If the audit
write cannot commit, the access mutation is rejected. Denied requests remain
denied if audit persistence fails; the API emits an operational error and a
bounded fallback signal, but never turns an audit failure into authorization.

## Stable Failure Contract

Errors use a stable machine-readable envelope:

```json
{
  "error": {
    "code": "forbidden",
    "message": "request is not permitted",
    "request_id": "req_..."
  }
}
```

Messages do not include token contents, claims, internal dependency responses,
or protected resource facts.

| Condition | HTTP | Code | Disclosure rule |
|---|---:|---|---|
| Missing, malformed, expired, wrong-issuer, wrong-audience, or invalid token | 401 | `unauthenticated` | One generic response; no verification detail |
| Keycloak/JWKS unavailable and the presented token cannot be verified from a valid cache | 503 | `authentication_unavailable` | Fail closed; no token detail |
| Inaccessible resource read or mismatched tenant path | 404 | `not_found` | Same response as an absent resource |
| Caller can view a resource but lacks a requested mutation permission | 403 | `forbidden` | Does not reveal policy internals |
| Self-approval attempt | 403 | `self_approval_forbidden` | Stable explicit business-security rule |
| OpenFGA unavailable, timed out, or malformed during a required decision | 503 | `authorization_unavailable` | Fail closed; denial and outage remain distinguishable internally |
| Relationship mutation not confirmed before deadline | 503 | `authorization_write_unconfirmed` | Safe to retry with the same idempotency key |
| Last-owner removal | 409 | `last_owner_required` | Returned only to callers already allowed to manage access |
| Audit transaction unavailable for an access mutation | 503 | `audit_unavailable` | Mutation is not committed |

List endpoints omit inaccessible objects and return an empty list when none are
accessible. They never return a per-object denial list.

## Audit Model

Security audit records are append-only through normal application APIs and
contain:

```text
timestamp
authenticated actor subject
action
target user subject, when applicable
configured tenant
stack and resource identifiers, when applicable
old role and new role, when applicable
outcome and stable reason code
request/correlation ID
idempotency key, when applicable
```

Grant, role replacement, revocation, failed access-management attempts,
last-owner rejection, relationship-reconciliation failure, and successful and
rejected approvals are audited. Audit records never contain access tokens,
passwords, client secrets, authorization headers, or raw Keycloak
administrative responses.

## Threat Model

| Threat | Attack | Required mitigation |
|---|---|---|
| Credential theft | Steal tokens from browser storage, logs, or transport | In-memory browser tokens, PKCE S256, HTTPS, short access-token lifetime, redaction, no downstream token propagation |
| Actor spoofing | Submit `actor`, `approved_by`, or similar fields | Remove identity fields from browser schemas; derive every actor from validated `sub`; reject legacy overrides |
| Tenant swapping | Change `{tenant_id}` to access another namespace | Compare with configured tenant before all repository/auth calls; protected `404`; no editable frontend tenant |
| Privilege escalation | Forge roles, write derived relations, assign multiple stack roles, or abuse admin bypass | Validate signed token roles, ignore unknown roles, write direct relations only, atomic role replacement, explicit bypass limits, route-level enforcement |
| Stale global-role revocation | Continue using an old token after Keycloak role removal | Five-minute maximum access-token lifetime, no cross-request global-role cache, safe refresh and logout |
| Stale stack-role revocation | Continue using a cached OpenFGA allow | No positive decision cache initially, higher-consistency mutation confirmation, per-request checks |
| Confused deputy | Use an authorized workflow, child ID, or service to act on another stack | Tenant-scoped ownership resolution, explicit principal/stack command context, immutable workflow inputs, no worker-facing browser authority |
| Resource enumeration | Distinguish protected objects through status or timing | Protected `404`, authorization-filtered lists, uniform error bodies, check tenant before resource lookup |
| Partial relationship write | Product state and OpenFGA diverge | Durable idempotent intents, provisioning state, immediate confirmation, retry/reconciliation, operator visibility |
| Authorization outage | Dependency error is mistaken for allow or deny | Typed adapter outcomes, bounded deadlines, fail closed with `503`, no frontend fallback |
| Self-approval | Requester approves their own privileged operation | Immutable requester subject, mandatory equality check after authorization, audit rejection, no Temporal signal |
| Secret leakage | Sensitive values appear in logs, errors, payloads, or examples | Structured redaction, secret-backed configuration, production-default rejection, regression tests |
| Directory overreach | tflive service account changes Keycloak users or global roles | Least-privilege read/search service account; lifecycle and global-role changes stay in Admin Console |

## Security Logging and Observability

Application logs use request IDs and stable error codes. They may include the
authenticated subject and resource identifiers when operationally necessary,
but never credentials or raw tokens. Authentication failures are logged without
token contents. Authorization denials, dependency failures, reconciliation
backlog age, and dead-lettered relationship intents have metrics suitable for
alerting.

Health endpoints report process liveness only and remain public. Dependency
readiness belongs on separately configured internal probes and does not expose
Keycloak, OpenFGA, or database details to unauthenticated callers.

## Verification Obligations

Implementation is not complete until automated tests demonstrate:

- OIDC signature, issuer, audience, expiry, not-before, algorithm, JWKS cache,
  and key-rotation behavior;
- authentication middleware coverage for every `/v1` route and the public
  health exception;
- configured-tenant matching and non-disclosure on mismatches;
- every global and stack role's allowed and denied permissions;
- stack-child inheritance and authorization-filtered lists;
- browser actor-spoof attempts cannot affect stored or signaled identities;
- platform-admin bypass limits and self-approval rejection;
- last-owner protection and mutually exclusive role replacement;
- audit success and fail-closed access-mutation behavior;
- OpenFGA timeout, unavailable, malformed, deny, and higher-consistency paths;
- relationship-write failures before and after the Postgres commit;
- token, password, authorization-header, and client-secret redaction;
- end-to-end sign-in, refresh, revocation, access management, and approval
  journeys.

AUTH-013 owns the exhaustive endpoint permission matrix. AUTH-022 and AUTH-023
own production-faithful backend, frontend, and end-to-end coverage. AUTH-026 is
the final release gate.

## Decision Log

| ID | Decision | Rationale |
|---|---|---|
| SEC-001 | Keycloak and OpenFGA are private platform-operated services in the same Kubernetes environment as tflive | Keeps the initial operational and network trust model explicit and bounded |
| SEC-002 | Keycloak `sub` is the only authorization identity | It is immutable within the realm and avoids mutable-name ambiguity |
| SEC-003 | Keycloak owns identity/global roles; OpenFGA owns stack relations; the API owns enforcement | Gives each system one authority and prevents policy duplication |
| SEC-004 | tflive accepts one configured tenant and treats URL tenant values as assertions to validate | Prevents browser-selected tenancy while preserving existing routes |
| SEC-005 | Platform administrators bypass ordinary stack checks but never self-approval, tenant, audit, validation, or fail-closed controls | Preserves recovery power without creating an unbounded superuser path |
| SEC-006 | Inaccessible reads use protected `404`; visible-resource mutation denials use `403`; dependency failures use `503` | Prevents enumeration while keeping operational failures distinguishable |
| SEC-007 | Access mutations and their audits commit atomically in Postgres and fail closed on audit failure | An unrecorded privilege change is not acceptable |
| SEC-008 | OpenFGA relationship writes use durable idempotent desired-state intents and higher-consistency confirmation | Provides recoverable cross-datastore behavior without false atomicity |
| SEC-009 | Active stacks may never be ownerless; new stacks remain provisioning until initial ownership is confirmed | Prevents irrecoverable or publicly usable orphan stacks |
| SEC-010 | Authorization is evaluated when each command is accepted; later revocation blocks new commands but does not rewrite running workflows | Keeps Temporal execution deterministic while enforcing current authority on new actions |
| SEC-011 | The initial implementation does not cache positive OpenFGA decisions | Makes confirmed stack-role revocation effective on the next request |
| SEC-012 | Production access tokens have a maximum five-minute lifetime | Bounds stale global-role authority without requiring token introspection on every request |

Changes to these decisions require an explicit follow-up design record linked
from this document and the affected GitHub issue.
