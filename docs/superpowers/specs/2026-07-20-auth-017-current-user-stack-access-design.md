# AUTH-017: Current-User and Stack-Access APIs Design

## Overview

Expose the authenticated user identity, effective capabilities per stack, and idempotent stack-role management endpoints. This completes the access-management surface for the auth sprint.

## New API Endpoints

| Method | Path | Purpose | Authorization |
|--------|------|---------|---------------|
| `GET` | `/v1/me` | Authenticated user identity + global capabilities | Any authenticated user |
| `GET` | `/v1/tenants/{tenant_id}/stacks/{stack_id}/grants` | List current role assignments on a stack | `can_manage_access` or platform-admin |
| `PUT` | `/v1/tenants/{tenant_id}/stacks/{stack_id}/grants` | Assign or replace one fixed role per user (idempotent) | `can_manage_access` or platform-admin |
| `DELETE` | `/v1/tenants/{tenant_id}/stacks/{stack_id}/grants/{user_id}` | Revoke a user's role on a stack | `can_manage_access` or platform-admin |

## Response Shapes

### GET /v1/me

```json
{
  "subject": "6fdb4b4c-2a8f-4cf7-945f-38f67f6a0e91",
  "name": "Alice Smith",
  "preferred_username": "alice",
  "email": "alice@example.com",
  "global_capabilities": {
    "can_create_stacks": true,
    "is_platform_admin": true
  }
}
```

`global_capabilities` is derived from normalized realm roles:
- `stack-creator` → `can_create_stacks: true`
- `platform-admin` → `is_platform_admin: true`

No raw tokens or internal Keycloak role names are exposed.

### Stack Response with Effective Capabilities

The existing `stackResponse` and `stackViewResponse` gain an `effective_capabilities` field:

```json
{
  "id": "stack_123",
  "...existing fields...",
  "effective_capabilities": {
    "can_view": true,
    "can_operate": false,
    "can_approve": true,
    "can_manage_access": true
  }
}
```

For list endpoints, capabilities are computed via `BatchCheck` across all stacks. For single stack reads, 4 parallel `Check` calls. Platform admins receive all `true` without authorization service calls.

### GET /grants Response

```json
{
  "grants": [
    {"user_id": "user-123", "role": "owner"},
    {"user_id": "user-456", "role": "operator"}
  ]
}
```

### PUT /grants Request

```json
{
  "user_id": "user-456",
  "role": "operator"
}
```

Valid roles: `owner`, `operator`, `approver`, `viewer`.

## Grant Management Flow

### PUT /grants (assign/replace)

1. Authenticate principal, extract `Subject`.
2. Authorize `can_manage_access` on the stack (or platform-admin bypass).
3. Validate `user_id` is non-empty and `role` is a valid fixed role.
4. Resolve target user via `UserDirectory.GetUserByID` to confirm they exist and are enabled.
5. Read current grants via `Authorizer.ListGrants`.
6. **Last-owner protection**: If target currently has `owner` role and new role differs, and they are the last owner → reject with `409 last_owner_protected`.
7. Build mutation:
   - No existing role → single grant write (confirm: true).
   - Different existing role → delete old + write new (confirm: true).
   - Same role already assigned → idempotent no-op, return 200 with current grants.
8. Write relationships via `Authorizer.WriteRelationships` (and `DeleteRelationships` if replacing).
9. Append audit record.
10. Return updated grants list.

### DELETE /grants/{user_id} (revoke)

1. Same authentication and authorization as PUT.
2. Validate `user_id` is non-empty.
3. Read current grants.
4. **Last-owner protection**: If target is an `owner` and is the last remaining owner → reject with `409 last_owner_protected`.
5. Delete relationship via `Authorizer.DeleteRelationships` (confirm: true).
6. Append audit record.
7. Return 204 No Content.

### Last-Owner Protection

Count owners from `ListGrants`. If removing or changing the last owner leaves zero owners, reject. The documented platform-administrator recovery operation (force bypass) is out of scope for this ticket and will be addressed separately.

## Service Layer

New commands and methods on `app.Service`:

```go
type GetMeCommand struct{}

type MeResponse struct {
    Subject           string            `json:"subject"`
    Name              string            `json:"name"`
    PreferredUsername string            `json:"preferred_username"`
    Email             string            `json:"email"`
    GlobalCapabilities GlobalCapabilities `json:"global_capabilities"`
}

type GlobalCapabilities struct {
    CanCreateStacks bool `json:"can_create_stacks"`
    IsPlatformAdmin bool `json:"is_platform_admin"`
}

type ListStackGrantsCommand struct {
    TenantID traits.TenantID
    StackID  traits.StackID
}

type GrantEntry struct {
    UserID string `json:"user_id"`
    Role   string `json:"role"`
}

type PutStackGrantCommand struct {
    TenantID traits.TenantID
    StackID  traits.StackID
    UserID   string
    Role     string
}

type RevokeStackGrantCommand struct {
    TenantID traits.TenantID
    StackID  traits.StackID
    UserID   string
}
```

New dependency on `Service`:

```go
type UserLookup interface {
    GetUserByID(ctx context.Context, id string) (DirectoryUser, error)
}
```

This extends the existing `UserDirectory` interface with a `GetUserByID` method. The Keycloak `DirectoryClient` implementation will call `GET /admin/realms/{realm}/users/{id}`.

## Error Codes

| Status | Code | When |
|--------|------|------|
| 400 | `invalid_request` | Missing/invalid user_id, role, or tenant |
| 403 | `forbidden` | Caller lacks `can_manage_access` |
| 404 | `not_found` | Stack does not exist |
| 409 | `last_owner_protected` | Would remove the last stack owner |
| 409 | `conflict` | Target user not found or disabled in directory |
| 503 | `authorization_unavailable` | OpenFGA dependency failure |
| 503 | `directory_unavailable` | Keycloak directory dependency failure |

## Testing Strategy

### Unit Tests (app layer)

- `TestGetMeReturnsIdentityAndCapabilities`
- `TestGetMeRequiresAuthentication`
- `TestListStackGrantsRequiresManageAccess`
- `TestListStackGrantsReturnsGrants`
- `TestPutStackGrantAssignsNewRole`
- `TestPutStackGrantReplacesExistingRole`
- `TestPutStackGrantIdempotentForSameRole`
- `TestPutStackGrantValidatesTargetUser`
- `TestPutStackGrantRejectsLastOwnerRemoval`
- `TestPutStackGrantRejectsNonAdminWithoutPermission`
- `TestPutStackGrantCreatesAuditRecord`
- `TestPutStackGrantDependencyFailureFailsClosed`
- `TestRevokeStackGrantRemovesRole`
- `TestRevokeStackGrantRejectsLastOwnerRemoval`
- `TestRevokeStackGrantCreatesAuditRecord`
- `TestRevokeStackGrantDependencyFailureFailsClosed`

### API Layer Tests

- `TestGetMeReturnsAuthenticatedUser`
- `TestGetMeUnauthenticated`
- `TestListGrantsRequiresAuthorization`
- `TestPutGrantCallsService`
- `TestPutGrantRejectsInvalidBody`
- `TestDeleteGrantCallsService`
- `TestTenantBoundaryEnforcedOnGrantRoutes`
- `TestGrantRoutesRequireAuthentication`

### Authorization Matrix Tests

- Owner can list/assign/revoke grants
- Operator cannot access grant endpoints
- Viewer cannot access grant endpoints
- Approver cannot access grant endpoints
- Platform admin bypasses stack authorization
- Unauthenticated callers get 401

## Files to Create/Modify

### New Files
- `internal/app/current_user.go` — `GetMe`, `ListStackGrants`, `PutStackGrant`, `RevokeStackGrant` commands and methods
- `internal/app/current_user_test.go` — Unit tests for grant management logic
- `internal/api/current_user.go` — HTTP handlers for `/v1/me`, grant CRUD endpoints
- `internal/app/user_lookup.go` — `UserLookup` interface extending `UserDirectory`

### Modified Files
- `internal/api/server.go` — Register new routes, update `stackResponse`/`stackViewResponse` with capabilities
- `internal/api/server_test.go` — Add API tests for new endpoints
- `internal/app/service.go` — Add `UserLookup` field to `Service` struct
- `internal/keycloak/directory.go` — Add `GetUserByID` method
- `internal/app/directory.go` — Extend `UserDirectory` interface

## Implementation Order

1. Add `GetUserByID` to `UserDirectory` interface and Keycloak adapter
2. Add `UserLookup` to `Service` struct
3. Implement `GetMe` in app layer + API handler
4. Add `effective_capabilities` to stack responses (service + API)
5. Implement `ListStackGrants` in app layer + API handler
6. Implement `PutStackGrant` with last-owner protection + API handler
7. Implement `RevokeStackGrant` with last-owner protection + API handler
8. Add audit records for all mutations
9. Write comprehensive tests
10. Update sprint backlog status in `docs/sprint/authn_and_authz/README.md`
