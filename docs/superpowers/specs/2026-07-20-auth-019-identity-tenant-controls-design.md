# AUTH-019: Replace placeholder identity and tenant controls — Design

## Summary

Drive the application identity, global capabilities, and tenant context from the server's authenticated state via a new `GET /v1/me` endpoint. This eliminates client-side derivation of capabilities from OIDC token claims and ensures the frontend obtains identity from a single, server-verified source.

## Architecture & Components

```
internal/
├── auth/
│   └── me.go                ← NEW: MeResponse type, role→capability mapping
└── api/
    ├── server.go            ← MODIFY: add GET /v1/me handler + route
    └── server_test.go       ← MODIFY: add /v1/me tests

web/src/
├── auth/
│   ├── useMeQuery.ts        ← NEW: TanStack Query hook for GET /v1/me
│   ├── useMeQuery.test.ts   ← NEW: hook tests
│   ├── OidcAuthProvider.tsx ← MODIFY: call useMeQuery, remove convertUserToMe
│   ├── __mocks__/
│   │   └── OidcAuthProvider.tsx ← MODIFY: provide /v1/me-shaped mock data
│   ├── types.ts             ← MODIFY: add tenantID to Me
│   └── convertUser.ts       ← DELETED
├── api/
│   ├── queryKeys.ts         ← MODIFY: add queryKeys.me
│   ├── client.test.ts       ← MODIFY: actor override proof test
│   └── client.ts            ← REVIEW ONLY: confirm no actor fields in bodies
└── app/
    └── AppShell.tsx          ← unchanged (already shows identity + logout)
```

### Backend: `GET /v1/me` (`internal/auth/me.go` + `internal/api/server.go`)

**MeResponse type:**
```go
type MeResponse struct {
    Sub                string              `json:"sub"`
    DisplayName        string              `json:"displayName"`
    Email              string              `json:"email,omitempty"`
    GlobalCapabilities GlobalCapabilities  `json:"globalCapabilities"`
    TenantID           string              `json:"tenantID"`
}

type GlobalCapabilities struct {
    IsPlatformAdmin bool `json:"isPlatformAdmin"`
    CanCreateStack  bool `json:"canCreateStack"`
}
```

**Handler (`handleMe`):**
1. Extract `Principal` from request context (set by auth middleware)
2. If no principal: return 401 (should never happen behind `RequireAuthentication`)
3. Map `Principal` fields:
   - `Sub` ← `principal.Subject`
   - `DisplayName` ← `principal.Name`
   - `Email` ← `principal.Email`
   - `GlobalCapabilities.IsPlatformAdmin` ← `"platform-admin"` role in `principal.RealmRoles`
   - `GlobalCapabilities.CanCreateStack` ← `"stack-creator"` role in `principal.RealmRoles`
4. `TenantID` ← `server.tenantID` (configured at startup, matches what `handleTenantRoute` enforces)
5. Return 200 JSON

**Route registration** in `NewServer()`:
```go
server.mux.HandleFunc("GET /v1/me", server.handleMe)
```

Registered directly (not via `handleTenantRoute`) — it's not tenant-scoped. The path starts with `/v1/` so the auth middleware (`RequireAuthentication`) already protects it.

**Reuses existing logic:**
- Principal extraction: `authn.PrincipalFromContext(ctx)` (same as `app.authenticatedActor`)
- Role mapping: same `slices.Contains` on `RealmRoles` as `app.isPlatformAdmin()` and `app.canCreateStack()`
- Tenant ID: `server.tenantID` (same string used by `handleTenantRoute` requirement checks)

### Frontend: `useMeQuery` hook (`web/src/auth/useMeQuery.ts`)

```typescript
export function useMeQuery() {
  return useQuery({
    queryKey: queryKeys.me,
    queryFn: () => fetchWithAuth("/v1/me").then(parseJSON),
    staleTime: Infinity,  // identity doesn't change mid-session
    retry: false,         // don't retry on failure (let OidcAuthProvider handle)
  });
}
```

- Uses the existing `fetchWithAuth` from `client.ts` (which injects Bearer token and handles 401)
- `staleTime: Infinity` — Me data is constant for a session
- `retry: false` — failures mean the auth state is invalid, let the provider handle reauth

**Query key:** `["me"]` added to `web/src/api/queryKeys.ts`

### OidcAuthProvider integration

**Current flow:**
1. `getUserManager().getUser()` resolves a valid OIDC `User`
2. `convertUserToMe(user)` derives `Me` from token claims
3. Provider renders `<Outlet>`

**New flow:**
1. `getUserManager().getUser()` resolves a valid OIDC `User` → provider knows the user is authenticated
2. `useMeQuery()` fires `GET /v1/me` with the bearer token from the OIDC user
3. On success: provider sets `me` from the server response, renders `<Outlet>`
4. On failure (401/error): treat as unauthenticated, redirect to login

The `useMeQuery` hook uses TanStack Query's `enabled` option gated on a state flag (`meQueryEnabled`) set to `true` after `getUserManager().getUser()` resolves with a valid user. This prevents the query from firing before a token is available. `fetchWithAuth` (called inside the query function) retrieves the access token from the user manager each time it sends a request.

**Loading states:**
- `status: "loading"` while either `getUser()` or `useMeQuery` is pending
- Provider renders nothing (null) until both resolve

**Error handling:**
- If `getUser()` fails → `status: "unauthenticated"` → redirect to sign-in
- If `useMeQuery` fails with 401 → `status: "unauthenticated"` → redirect to sign-in
- If `useMeQuery` fails with non-401 → `status: "error"` → show error with retry button

### Me type update (`web/src/auth/types.ts`)

Add optional `tenantID` field:
```typescript
export interface Me {
  sub: string;
  displayName: string;
  email?: string;
  tenantID?: string;  // NEW: from /v1/me
  globalCapabilities: {
    isPlatformAdmin: boolean;
    canCreateStack: boolean;
  };
}
```

Frontend can now use `me.tenantID` for API calls. The `VITE_TFLIVE_TENANT_ID` env var remains as a build-time fallback (`resolveTenantID` in `config.ts` unchanged for now).

### Mock auth provider update

`__mocks__/OidcAuthProvider.tsx` currently hardcodes:
```typescript
me: { sub: "test", displayName: "Test", globalCapabilities: { isPlatformAdmin: false, canCreateStack: true } }
```

Update to include `tenantID`:
```typescript
me: { sub: "test", displayName: "Test", email: "test@example.com", tenantID: "tenant_123", globalCapabilities: { isPlatformAdmin: false, canCreateStack: true } }
```

### Deleted: `convertUser.ts`

This file mapped OIDC token claims (`realm_access.roles`, `preferred_username`) to `Me`. It is no longer needed because:
- Global capabilities come from the server's `/v1/me` response
- The server, not the token, is the authoritative source

### 401 handling (unchanged)

Current behavior is sufficient:
1. `fetchWithAuth` does silent token refresh + 1 retry on 401, then falls through to `signinRedirect`
2. `useQueryErrorBoundary` calls `logout()` (signout redirect) on any 401 from react-query

No changes needed per design decision.

### Placeholder verification (no source changes needed)

Audit result: no `"user_123"` hardcodings exist in production source. All occurrences are in test mock data. No source-level actor controls exist in the UI. The acceptance criterion "Placeholder or editable user_123 and actor controls are removed from production UI paths" is already satisfied by prior work.

## Test plan

### Backend tests (`internal/api/server_test.go`)

| Test | Description |
|------|-------------|
| `GET /v1/me` with valid token | Returns 200, correct sub/displayName/email, correct globalCapabilities based on realm roles, correct tenantID |
| `GET /v1/me` with platform-admin role | `isPlatformAdmin: true` |
| `GET /v1/me` with stack-creator role | `canCreateStack: true` |
| `GET /v1/me` with both roles | Both flags true |
| `GET /v1/me` with neither role | Both flags false |
| `GET /v1/me` without auth header | Returns 401 (middleware rejects) |
| `GET /v1/me` with invalid token | Returns 401 (verifier rejects) |

### Frontend tests

| File | Test | Description |
|------|------|-------------|
| `useMeQuery.test.ts` | Success response | Hook returns MeResponse data when `/v1/me` returns 200 |
| `useMeQuery.test.ts` | 401 response | Hook returns error with 401 status |
| `useMeQuery.test.ts` | Loading state | Hook shows loading while fetch is pending |
| `OidcAuthProvider.test.tsx` | Authenticated flow | Provider fetches /v1/me after getUser, renders children with me data |
| `OidcAuthProvider.test.tsx` | /v1/me failure | Provider redirects to login when /v1/me returns 401 |
| `client.test.ts` | No actor overrides | All API request builder functions exclude identity fields from request bodies |

### Actor override proof test (`client.test.ts`)

This test verifies the acceptance criterion "Frontend tests prove that request bodies cannot include actor overrides."

**Method:** Export the request-building functions (or capture their fetch calls) and assert that no JSON request body sent by the API client contains any of these fields: `actor`, `actor_id`, `sub`, `user_id`, `on_behalf_of`.

The `requested_by` field on `TemplateRegistration` is excluded — it's a business concept (who requested the template registration), not an actor override.

## Dependencies and ordering

1. Backend `GET /v1/me` handler — has no internal dependencies, can be implemented first
2. Frontend `useMeQuery` hook — depends on backend endpoint existing
3. OidcAuthProvider integration — depends on `useMeQuery` hook
4. Mock auth provider update — depends on types update
5. `convertUser.ts` deletion — last, after OidcAuthProvider no longer imports it
6. Actor override test — independent, can be done at any point
7. Backend tests — alongside the handler

## Files summary

| File | Action |
|------|--------|
| `internal/auth/me.go` | **CREATE** — MeResponse type, role→capability mapping function |
| `internal/api/server.go` | **MODIFY** — add `handleMe` handler + `GET /v1/me` route |
| `internal/api/server_test.go` | **MODIFY** — add /v1/me integration tests |
| `web/src/auth/useMeQuery.ts` | **CREATE** — TanStack Query hook |
| `web/src/auth/useMeQuery.test.ts` | **CREATE** — hook tests |
| `web/src/api/queryKeys.ts` | **MODIFY** — add `me` query key |
| `web/src/auth/types.ts` | **MODIFY** — add `tenantID` to `Me` |
| `web/src/auth/OidcAuthProvider.tsx` | **MODIFY** — replace convertUserToMe with useMeQuery |
| `web/src/auth/__mocks__/OidcAuthProvider.tsx` | **MODIFY** — update mock me data |
| `web/src/auth/convertUser.ts` | **DELETE** — no longer needed |
| `web/src/auth/convertUser.test.ts` | **DELETE** — no longer needed |
| `web/src/api/client.test.ts` | **MODIFY** — add actor override proof test |
