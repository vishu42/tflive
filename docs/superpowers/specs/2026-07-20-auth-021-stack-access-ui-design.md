# AUTH-021: Stack Access UI Design

## Overview

Allow stack owners and platform administrators to manage fixed stack roles (owner, operator, approver, viewer) from the tflive UI. Built on the reserved `/stacks/:stackId/access` route scaffold, gated by `canManageAccess`.

## Dependencies

- **AUTH-017** (backend stack-access APIs) — closed; adds 3 missing HTTP endpoints + service methods as part of this ticket
- **AUTH-020** (permission-aware UI) — closed; `RequireCapability`, `useStackCapabilities`, and error boundaries already in place

## Backend API Contract

Three new endpoints, all gated behind `canManageAccess` (OpenFGA check via existing `requireStackAccess` helper):

| Method | Path | Description |
|---|---|---|
| `GET` | `/v1/tenants/{tenant_id}/stacks/{stack_id}/grants` | List all direct grants with resolved user profiles |
| `PUT` | `/v1/tenants/{tenant_id}/stacks/{stack_id}/grants` | Assign or replace one fixed role per user |
| `DELETE` | `/v1/tenants/{tenant_id}/stacks/{stack_id}/grants/{user_sub}` | Revoke a user's role |

### Requests and Responses

**GET grants:**

```json
{
  "grants": [
    {
      "userSub": "abc-123",
      "role": "owner",
      "displayName": "Alice Smith",
      "email": "alice@example.com"
    }
  ]
}
```

**PUT grant (request):**

```json
{
  "userSub": "abc-123",
  "role": "owner"
}
```

**PUT grant (response):** 200 with the grant object (same shape as grant item above).

**DELETE grant:** 204 no content.

### Error Codes

| HTTP | Code | When |
|---|---|---|
| 400 | `invalid_user_sub` | Target user not found in Keycloak directory |
| 400 | `invalid_role` | Role is not one of owner/operator/approver/viewer |
| 403 | `access_denied` | Caller lacks canManageAccess on the stack |
| 404 | `stack_not_found` | Stack does not exist or caller cannot see it |
| 409 | `last_owner` | Attempt to remove or demote the last remaining owner |
| 503 | `authorization_unavailable` | OpenFGA is unreachable or times out |

### Authorization

All three endpoints call `requireStackAccess(principal, stackID, PermissionManageAccess)` before any operation. List further enriches results with Keycloak directory lookups. Mutations use `AuthorizationOutbox` for reliable cross-datastore writes, matching the `CreateStack` ownership grant pattern.

### Last-Owner Protection

Before revoking or reassigning, the service checks that at least one other user holds the `owner` role on the stack. If the target is the sole owner, the operation is rejected with `409 last_owner`. Platform administrators can bypass this via a documented recovery operation (out of scope for this ticket).

### Audit

All mutations create audit records via the existing `SecurityAuditRecorder`:
- Assign: `audit.action.grant` with target user, role
- Replace: `audit.action.grant` with old and new roles
- Revoke: `audit.action.revoke` with target user, former role

## Backend Service Methods

Three new methods on `app.Service`, following the existing command pattern:

### ListStackGrants

```go
type ListStackGrantsCommand struct {
    TenantID traits.TenantID
    StackID  traits.StackID
}
type ListStackGrantsResult struct {
    Grants []GrantView
}
type GrantView struct {
    UserSub     string
    Role        string
    DisplayName string
    Email       string
}
```

Flow: authorize → `Authorizer.ListGrants()` → resolve user profiles via `SearchUsers` → return.

### AssignStackRole

```go
type AssignStackRoleCommand struct {
    TenantID traits.TenantID
    StackID  traits.StackID
    UserSub  string
    Role     string
}
```

Flow: authorize → validate role enum → validate user exists in Keycloak → check last-owner guard (only if user is currently an owner being demoted/replaced) → `Outbox.Claim()` → `Authorizer.WriteRelationships()` (atomically removes any existing role for this user, writes new one; if new role equals current role, no-op and return) → `Outbox.Complete()` → audit (skip audit for idempotent no-op) → return grant.

### RevokeStackRole

```go
type RevokeStackRoleCommand struct {
    TenantID traits.TenantID
    StackID  traits.StackID
    UserSub  string
}
```

Flow: authorize → list current grants → check last-owner guard (reject if sole owner) → `Outbox.Claim()` → `Authorizer.DeleteRelationships()` → `Outbox.Complete()` → audit → return.

## Frontend Design

### Layout

Two-column grid using the existing `.workflow-grid` CSS:

```
┌──────────────────────────────┐
│  Stack Name > Access tab     │
├──────────────┬───────────────┤
│  Grants      │  Assign Role  │
│  (left panel)│  (right panel)│
└──────────────┴───────────────┘
```

**Left panel — Grants:** Lists all current grants. Each row shows user display name, email, role badge (colored tag), and a revoke button. Revoke triggers inline confirmation then shows an undo banner for 5 seconds.

**Right panel — Assign Role:** Search input (debounced, minimum 2 chars) queries Keycloak directory. Results dropdown shows matching users; already-assigned users appear grayed out with current role. Click a user to select, pick a role from a dropdown, click Assign (or Replace if user already has a grant).

### States

| State | Left Panel | Right Panel |
|---|---|---|
| **Loading** | `<Loader2 size={16} className="spin" /> Loading grants...` | Search input visible, no results |
| **Empty** | "No users have been assigned access yet. Use the panel on the right to add the first grant." | Default search state |
| **Error (grants)** | `<div className="alert">` with retry button | Search still works |
| **Error (403)** | Route gate completely blocks the page; `AccessDenied` shown | — |
| **Error (409)** | Inline alert on the revoked row: "Cannot remove the last owner..." | — |
| **Error (503)** | `useQueryErrorBoundary` → `ServiceUnavailable` page | — |
| **Conflict** | After mutation, if list changed concurrently: "Data has changed. Refresh to see latest." banner | — |
| **Success** | Grant list refreshes; changed row gets a brief visual highlight | Selected user card updates, role select resets |

### Confirmation Pattern

**Revoke:** Click "Revoke" on a grant row → row enters confirm state showing "Remove access for [name]? [Confirm] [Cancel]" inline. Clicking Confirm triggers the mutation. Cancel returns to normal row state.

**Undo:** After successful revoke, a fixed-position bar appears at the bottom: "Removed [name]'s [role] access. [Undo]" — auto-dismisses after 5 seconds. Undo calls the assign mutation with the previous role.

### Keyboard Accessibility

Tab order: search input → search results (arrow keys navigate) → role dropdown → assign button → (left panel) grant rows. Escape clears search and closes dropdown. Enter confirms selections. All interactive elements have visible focus styles (existing browser defaults).

### Component Tree

```
StackAccessScreen (new)
├── useStackGrantsQuery
├── useSearchUsersQuery (debounced, enabled: q.length >= 2)
├── useAssignRoleMutation
├── useRevokeRoleMutation
│
├── <section className="workflow-grid">
│   ├── <GrantsPanel>
│   │   ├── loading state
│   │   ├── empty state
│   │   ├── error state
│   │   └── <GrantRow> per grant
│   │       ├── displayName + email
│   │       ├── <RoleBadge role={...}>
│   │       ├── revoke button → inline confirm
│   │       └── undo banner (5s timeout)
│   │
│   └── <AssignRolePanel>
│       ├── <input> search (debounced)
│       ├── <SearchResults> dropdown
│       ├── selected user card (when selected)
│       ├── <select> role picker
│       ├── <button> Assign/Replace
│       └── inline error/validation
```

### API Client Functions

```typescript
// web/src/api/client.ts
export function listStackGrants(tenantID: string, stackID: string): Promise<{ grants: GrantView[] }>;
export function assignStackRole(tenantID: string, stackID: string, body: { userSub: string; role: string }): Promise<GrantView>;
export function revokeStackRole(tenantID: string, stackID: string, userSub: string): Promise<void>;
export function searchUsers(tenantID: string, query: string, first: number, max: number): Promise<{ users: DirectoryUser[] }>;
```

### New Types

```typescript
// web/src/api/types.ts (additions)
export interface GrantView {
  userSub: string;
  role: string;
  displayName: string;
  email: string;
}

export interface DirectoryUser {
  id: string;
  username: string;
  email: string;
  firstName: string;
  lastName: string;
}
```

### TanStack Query Hooks

```typescript
// web/src/api/queries.ts (additions)
export function useStackGrantsQuery(tenantID: string, stackID: string);
export function useSearchUsersQuery(tenantID: string, query: string);
export function useAssignStackRoleMutation(tenantID: string, stackID: string);
export function useRevokeStackRoleMutation(tenantID: string, stackID: string);
```

### Query Keys

```typescript
// web/src/api/queryKeys.ts (additions)
stackGrants: (tenantID: string, stackID: string) => ["stackGrants", tenantID, stackID];
userSearch: (tenantID: string, query: string) => ["userSearch", tenantID, query];
```

Mutations invalidate `stackGrants` on success.

### CSS Additions to styles.css

- `.grant-row` — list item with user info, role badge, actions
- `.role-badge` — small colored tag: owner blue, operator green, approver amber, viewer gray
- `.role-badge.owner` `.role-badge.operator` `.role-badge.approver` `.role-badge.viewer`
- `.search-dropdown` — absolutely positioned dropdown below search input
- `.selected-user-card` — bordered info card for selected user
- `.confirm-row` — inline confirm/cancel button pair
- `.undo-banner` — fixed-position bottom bar, fades after 5s

### Route Wiring

In `web/src/app/router.tsx`, replace:

```tsx
{ index: true, element: <RoutePlaceholder title="Access" /> }
```

With:

```tsx
{ index: true, element: <StackAccessScreen /> }
```

## Files to Create/Modify

### Backend (Go)

| File | Action |
|---|---|
| `internal/app/service.go` | Add `ListStackGrants`, `AssignStackRole`, `RevokeStackRole` methods |
| `internal/api/server.go` | Add 3 routes + handlers + request/response DTOs |
| `internal/api/server_test.go` | Add handler integration tests |
| `internal/app/service_test.go` | Add service unit tests |

### Frontend (TypeScript/React)

| File | Action |
|---|---|
| `web/src/api/client.ts` | Add `listStackGrants`, `assignStackRole`, `revokeStackRole`, `searchUsers` |
| `web/src/api/types.ts` | Add `GrantView`, `DirectoryUser` |
| `web/src/api/queries.ts` | Add `useStackGrantsQuery`, `useSearchUsersQuery`, `useAssignStackRoleMutation`, `useRevokeStackRoleMutation` |
| `web/src/api/queryKeys.ts` | Add `stackGrants`, `userSearch` keys |
| `web/src/features/stacks/StackAccessScreen.tsx` | Create main component |
| `web/src/features/stacks/StackAccessScreen.test.tsx` | Create tests |
| `web/src/app/router.tsx` | Wire `StackAccessScreen` to `/stacks/:stackId/access` |
| `web/src/styles.css` | Add grant list, role badge, search dropdown, undo banner styles |

## Testing

### Backend

Handler tests for each endpoint covering:
- Authorized owner/admin can list, assign, revoke
- Unauthorized user gets 403
- Invalid user sub returns 400
- Invalid role returns 400  
- Last-owner protection returns 409 with explanatory message
- Re-assigning same role to same user is idempotent (200, no duplicate)
- Replacing a role (operator→viewer) writes new relationship, removes old
- OpenFGA unavailable returns 503
- Keycloak user search unavailable during validation returns 400/503 depending on context

### Frontend

Component tests covering:
- Grants list renders grant rows with role badges and revoke buttons
- Empty state shows guidance text
- Loading shows spinner
- Search input triggers debounced query, displays results
- Selecting a user and role then clicking assign triggers mutation
- Successful assign refreshes grant list
- Revoke shows inline confirm state, confirm triggers mutation, undo appears
- API error (403/409/503) shows appropriate feedback
- Tab order follows keyboard accessibility requirements
- Escape clears search input and dropdown

## Acceptance Criteria Mapping

| AC | How Satisfied |
|---|---|
| View current grants + search users | Left panel (grants) + right panel (search + directory API) |
| Assign, replace, revoke owner/operator/approver/viewer | PUT for assign/replace, DELETE for revoke, all 4 roles supported |
| Destructive changes require confirmation + last-owner errors | Inline confirm on revoke, 409 with explanatory alert |
| Unauthorized users cannot open or infer data | Route gate via `RequireCapability canManageAccess`, 404-style hiding for non-viewable stacks |
| Loading, empty, validation, conflict, dependency-failure, success states | All documented above per panel |
| Keyboard accessible | Tab order, arrow key navigation in dropdown, escape, enter |
| Frontend tests for each role-management flow | Tests for grant list, search, assign, revoke, confirm, undo |
