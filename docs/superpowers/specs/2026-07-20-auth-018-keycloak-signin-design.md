# AUTH-018: Integrate Keycloak sign-in and session handling — Design

## Summary

Replace the frontend mock auth system with real Keycloak OIDC authentication using `oidc-client-ts` with manual React integration. Tokens are kept strictly in memory, the API client injects Bearer tokens, and all auth states (initializing, authenticated, unauthenticated, error) are tested.

## Architecture & Components

```
web/src/auth/
├── oidcConfig.ts          ← NEW: OIDC discovery config, client settings from .env
├── InMemoryStore.ts       ← NEW: oidc-client-ts storage adapter (memory only)
├── userManager.ts         ← NEW: singleton UserManager instance
├── OidcAuthProvider.tsx   ← NEW: replaces MockAuthProvider
├── CallbackPage.tsx       ← NEW: handles /auth/callback redirect
├── AuthContext.ts         ← MODIFY: add error status if needed
├── RequireCapability.tsx  ← unchanged
└── useStackCapabilities.ts ← unchanged

web/src/api/
└── client.ts             ← MODIFY: inject Bearer token, refresh before expiry

web/src/
├── main.tsx              ← MODIFY: OidcAuthProvider instead of MockAuthProvider
└── app/router.tsx        ← MODIFY: wire CallbackPage to /auth/callback

DELETED:
├── MockAuthProvider.tsx
├── mockUsers.ts
```

### OIDC configuration (`oidcConfig.ts`)

- Reads from Vite env vars: `VITE_OIDC_ISSUER`, `VITE_OIDC_CLIENT_ID`, `VITE_OIDC_REDIRECT_URI`
- Derives the remaining OIDC endpoints from the issuer's `.well-known/openid-configuration` or hardcodes standard Keycloak paths
- PKCE S256 enabled, no client secret (public client)

### In-memory storage (`InMemoryStore.ts`)

- Implements oidc-client-ts `UserStore` interface
- Stores the `User` object in a module-level variable
- Never touches `localStorage` or `sessionStorage`

### UserManager singleton (`userManager.ts`)

- Single `UserManager` instance with in-memory store
- Configured for Authorization Code + PKCE S256
- Exposes `getUser()`, `signinRedirect()`, `signinRedirectCallback()`, `signinSilent()`, `signoutRedirect()`, `signoutRedirectCallback()`

### OAuth Provider (`OidcAuthProvider.tsx`)

Four states exposed through `AuthContext`:

| State | Condition | Behavior |
|---|---|---|
| `"initializing"` | App boot, no user yet | Show a loading spinner |
| `"authenticated"` | User loaded, tokens in memory | Render children |
| `"unauthenticated"` | No user, needs login | Trigger redirect to Keycloak |
| `"error"` | Keycloak unreachable or callback failure | Show error screen with retry |

Key considerations:
- On initial mount: check `userManager.getUser()` — if null, `signinRedirect()`
- Expose `login()`, `logout()`, and `me` matching the existing `AuthContextValue` interface
- `me` is derived from the id_token claims (sub, preferred_username, email) plus global roles from access token

### Callback page (`CallbackPage.tsx`)

- Route: `/auth/callback`
- Handles `signinRedirectCallback()` and `signoutRedirectCallback()`
- On success: redirect to original route (or `/stacks` as fallback)
- On error: display error message with retry link

## Data Flow

### Sign-in

1. User visits any route → OidcAuthProvider sees no user → calls `UserManager.signinRedirect()` → browser navigates to Keycloak
2. User authenticates → Keycloak redirects to `/auth/callback?code=...&state=...`
3. CallbackPage calls `UserManager.signinRedirectCallback()` → exchanges code for tokens → navigates back to original route
4. Tokens stored in memory via InMemoryStore

### API calls

1. `client.ts` reads access token from AuthContext (or from UserManager's events)
2. Injects `Authorization: Bearer <token>` header on every request
3. On 401 response: tries `signinSilent()` once to refresh → retry original request
4. If silent refresh fails: trigger `signinRedirect()` to re-authenticate

### Logout

1. User clicks "Log out" → `UserManager.signoutRedirect()` → Keycloak terminates session → redirects back to app root
2. CallbackPage clears in-memory tokens on signout callback detection

### Page refresh

1. No tokens in memory (by design) → provider detects no user → redirects to Keycloak
2. If Keycloak SSO session exists → instant redirect back with new code → new tokens in memory
3. If Keycloak SSO session expired → user sees Keycloak login page

## Error Handling

| Scenario | Behavior |
|---|---|
| Keycloak unreachable at startup | status: `"error"`, retry button triggers re-attempt |
| Callback error (invalid state/code/network) | Show error message with re-login link |
| Silent refresh fails (network error) | Trigger full redirect to Keycloak |
| Silent refresh fails (session expired) | Trigger full redirect to Keycloak |
| API returns 401 (token expired mid-flight) | Attempt silent refresh once; redirect on failure |
| API returns 403 | Passed through to existing `AccessDenied` component |
| Redirect from Keycloak without expected state param | Discard, redirect to root |

## Modifications to Existing Code

### `web/src/main.tsx`

- Replace `<MockAuthProvider>` with `<OidcAuthProvider>`
- Remove `VITE_TFLIVE_MOCK_USER_ROLE` references

### `web/src/app/router.tsx`

- Replace the placeholder `/auth/callback` route element with `<CallbackPage>`

### `web/src/api/client.ts`

- Accept a `getAccessToken` function as parameter or read from context
- Add `Authorization: Bearer <token>` header to all requests
- Handle 401 with refresh → retry logic

### `web/src/auth/AuthContext.ts`

- Add `"error"` to the status union if not already present
- Ensure `login()` and `logout()` signatures match OIDC provider needs

### `web/src/app/AppShell.tsx`

- Add "Initializing..." and error states to the identity section

## Testing

### New tests
- **OidcAuthProvider**: initialization flow, login redirect trigger, logout redirect trigger, error state
- **CallbackPage**: successful redirect callback, error state with mocked UserManager
- **InMemoryStore**: write/read/remove cycle
- **API client**: Bearer injection, 401 → refresh → retry

### Modified tests
- Update any test that referenced `MockAuthProvider` or `mockUsers`
- Replace with mocked `UserManager` or `AuthContext` provider with test user
- Ensure test setup does not require a running Keycloak instance

### Security testing
- Verify tokens never appear in localStorage or sessionStorage
- Verify access tokens are not logged in console or error messages
- Verify logout clears all in-memory state

## Environment Variables

New Vite-prefixed variables:

| Variable | Default (dev) | Description |
|---|---|---|
| `VITE_OIDC_ISSUER` | `http://localhost:8082/realms/tflive` | OIDC issuer URL |
| `VITE_OIDC_CLIENT_ID` | `tflive-web` | Keycloak client ID |
| `VITE_OIDC_REDIRECT_URI` | `http://localhost:5173/auth/callback` | Redirect URI after login/logout |

Existing variables to keep:
- `VITE_TFLIVE_TENANT_ID` — unchanged, still needed for API calls

Variables to remove:
- `VITE_TFLIVE_MOCK_USER_ROLE` — no longer used

## Dependencies

New npm packages:
- `oidc-client-ts` — OIDC client library for SPAs

Removed:
- None (mock auth was pure React state, no extra packages)

## Acceptance Criteria Mapping

| AC | How it's satisfied |
|---|---|
| Unauthenticated users redirected to Keycloak sign-in | `OidcAuthProvider` triggers `signinRedirect()` when no user found |
| Authorization Code with PKCE S256 | Configured in `UserManager` settings |
| Access/refresh tokens in memory only | `InMemoryStore` — never touches persistent storage |
| API calls send current bearer token | `client.ts` injects `Authorization` header |
| Tokens refreshed before expiry | Silent refresh via `signinSilent()` before API calls |
| Logout clears local state and terminates Keycloak session | `signoutRedirect()` with post-logout redirect URI |
| Initialization, refresh failure, and logout have tested states | Covered in test plan above |
