# OpenFGA Authorization Port and Adapter Design

**Issue:** AUTH-010 / GitHub issue #12  
**Goal:** Add a provider-neutral authorization port and an OpenFGA-backed adapter without enforcing permissions on product endpoints yet.

## Scope

This change establishes the foundation consumed by later authorization tickets. It provides provider-independent operations for authorization checks, batch checks, accessible-stack lookup, grant reads, and relationship writes and deletes. It must not expose OpenFGA SDK or wire-format types outside the adapter.

This ticket does not wire endpoint enforcement, platform-administrator bypasses, stack-creation ownership, durable relationship intents, audit records, or access-management HTTP endpoints. Those belong to AUTH-011 through AUTH-017.

## Architecture

Create an `internal/authz` package that owns the application-facing authorization contract. Its types are deliberately small and stable:

- `Subject` represents a validated Keycloak subject as the canonical OpenFGA identifier `user:<sub>`.
- `Stack` represents a validated stack identifier as `stack:<id>`.
- `Role` permits only the four direct writable relations: `owner`, `operator`, `approver`, and `viewer`.
- `Permission` represents the four derived relations: `can_view`, `can_operate`, `can_approve`, and `can_manage_access`.
- `Grant` is a validated direct role assignment returned by a grants read.
- The port exposes `Check`, `BatchCheck`, `ListAccessibleStacks`, `ListGrants`, `WriteRelationships`, and `DeleteRelationships` using only these application types.

`internal/openfga` implements the port. It owns all OpenFGA-specific paths, JSON structures, pagination tokens, tuple serialization, response validation, and consistency settings. The adapter receives the explicit runtime store and model IDs from the existing OpenFGA configuration and never discovers either at request time.

The existing OpenFGA provisioning client remains independent. Shared bounded HTTP and redaction helpers may be extracted only when that does not change existing provisioning behavior or expose provider types to `internal/authz`.

## Identifier and Relation Rules

The only constructors for OpenFGA subject and stack identifiers are defined in `internal/authz`.

- `SubjectFromKeycloakSub` accepts one non-empty Keycloak `sub` and produces exactly `user:<sub>`.
- `StackFromID` accepts one non-empty application stack ID and produces exactly `stack:<id>`.
- Inputs that are blank, contain control characters, or already use a type prefix are invalid. The constructors never double-prefix an identifier.
- Relationship mutations accept direct roles only. Derived permissions can be checked but can never become write or delete targets.
- Returned objects, users, relations, and tuples are parsed and validated before becoming port values. A syntactically successful but malformed OpenFGA response is an adapter failure.

## Operation Behavior

All calls carry the configured authorization model ID where the OpenFGA API supports it. The adapter applies the existing configured request deadline and respects an earlier caller deadline.

`Check` returns an explicit decision. An OpenFGA response that says the relation is absent is a normal denial, not an error. `BatchCheck` returns an outcome for every requested check and does not silently convert an individual backend failure into a denial or allow.

`ListAccessibleStacks` uses OpenFGA `ListObjects` with a subject and permission and returns only validated `stack:<id>` objects. It follows bounded pagination and rejects malformed, repeated, or non-stack results. `ListGrants` reads only direct-role tuples for one stack and returns validated grants.

`WriteRelationships` and `DeleteRelationships` accept a set of validated direct-role tuples and issue OpenFGA tuple writes/deletes. Repeating a desired write or delete must be safe: a successful repeat produces the same authorization state and does not expose a provider conflict as a semantic application error.

Write and delete requests can ask for confirmation. When requested, the adapter follows the mutation with the appropriate check using OpenFGA's higher-consistency preference. It reports success only when that check confirms the desired state before the bounded deadline. AUTH-012 will add durable desired-state intents and retry/reconciliation; this ticket only makes individual delivery and confirmation safe to retry.

## Failure Model

The port exposes typed outcomes that distinguish these cases internally:

| Condition | Port outcome | Required behavior |
|---|---|---|
| OpenFGA explicitly denies a check | denied decision | Normal authorization result; never treated as dependency failure. |
| Caller deadline or OpenFGA request deadline expires | timeout error | Fail closed. |
| Transport failure, OpenFGA `429`, or OpenFGA `5xx` | unavailable error | Fail closed. |
| Invalid JSON, wrong content type, incomplete response, invalid tuple, repeated pagination token, or unexpected successful response | malformed-response error | Fail closed. |
| Invalid caller-supplied ID, role, permission, or mutation | invalid-input error | Do not issue a provider request. |
| A requested mutation cannot be confirmed before deadline | unconfirmed-write error | Fail closed and allow a safe retry. |

OpenFGA errors, bearer tokens, and raw upstream response bodies must not leak through the port. A reusable API-facing mapper may classify timeout, unavailable, malformed-response, and unconfirmed-write errors as stable `503` outcomes (`authorization_unavailable` or, for unconfirmed mutations, `authorization_write_unconfirmed`). It must not be used to begin endpoint permission enforcement in this ticket.

No positive authorization-decision cache is introduced. A confirmed revocation is therefore observed by the next later check, subject only to OpenFGA consistency behavior.

## Testing

Use HTTP-level adapter tests with a controllable OpenFGA test server. Tests must cover:

- canonical ID construction and rejection of blank, unsafe, and already-prefixed values;
- allowed and denied checks, with denial distinct from all error classes;
- batch-check result handling and configured model IDs;
- accessible-stack listing and direct-grant reads, including pagination and malformed returned values;
- writes and deletes, including retry-safe/idempotent behavior;
- higher-consistency confirmation requests for grants and revocations;
- deadline exhaustion, unavailable upstream responses, malformed JSON, wrong media type, incomplete valid-JSON responses, and unexpected success responses;
- fail-closed behavior: no dependency or protocol failure returns an allow decision.

Run the focused authorization and OpenFGA test packages as the red/green loop, then run the complete Go test suite before handoff.

## Documentation and Backlog

Document any new runtime behavior in `docs/authentication.md`, including that checks use the configured immutable model ID, relationship confirmations can request higher consistency, and authorization dependency failures fail closed. Update AUTH-010 in `docs/sprint/authn_and_authz/README.md` from `Not Started` to its completed status only when all acceptance criteria and tests are satisfied.
