# AUTH-016: Keycloak User-Directory Reader Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a least-privilege Keycloak user-directory reader that uses a dedicated service credential to search realm users, returning only safe display attributes for access management.

**Architecture:** A new `DirectoryClient` in `internal/keycloak/` authenticates with a dedicated `tflive-directory-reader` confidential client via client-credentials grant. It calls the Keycloak Admin REST API user-search endpoint with `enabled=true` filtering. The app layer defines a `UserDirectory` interface, and the API exposes a `GET /v1/tenants/{tenant_id}/users/search` endpoint gated by `platform-admin` role. The existing provisioner is extended to create the directory reader client and assign minimal realm-management roles.

**Tech Stack:** Go, Keycloak Admin REST API, OAuth2 client-credentials grant, `httptest` for mocking

## Global Constraints

- Secrets (`ClientSecret`) use plain `string` in `keycloak` package; redacted via `redactSecrets()` in error messages
- Config `Secret` type in `internal/config` with `String()`/`GoString()` returning `[REDACTED]`
- Error wrapping via `authConfigError()` (config) or `fmt.Errorf` with `%w` (elsewhere)
- Bounded request bodies: `maxSuccessBody = 1 << 20`, `maxErrorBody = 4 << 10`
- HTTP timeouts default to 10s via `defaultHTTPTimeout`
- Test pattern: `httptest.NewServer` with `http.HandlerFunc` switch-dispatch
- All `/v1` routes protected by `RequireAuthentication` middleware; health endpoints public

---

## File Structure

| File | Action | Responsibility |
|---|---|---|
| `internal/keycloak/directory.go` | Create | `DirectoryClient` — authenticates with client-credentials, searches users |
| `internal/keycloak/directory_test.go` | Create | Tests for `DirectoryClient` with httptest mock Keycloak |
| `internal/keycloak/config.go` | Modify | Add `DirectoryReaderClientSecret` field to `Config` |
| `internal/keycloak/provisioner.go` | Modify | Create directory reader client and assign roles |
| `internal/keycloak/provisioner_test.go` | Modify | Tests for directory reader provisioning |
| `internal/config/auth.go` | Modify | Add `DirectoryReaderClientID`, `DirectoryReaderClientSecret` to `SecurityConfig` |
| `internal/config/auth_test.go` | Modify | Tests for directory reader config validation |
| `internal/app/directory.go` | Create | `DirectoryUser` type and `UserDirectory` interface |
| `internal/app/directory_test.go` | Create | Tests for `SearchUsers` authorization gating |
| `internal/app/service.go` | Modify | Add `UserDirectory` field and `SearchUsers` method |
| `internal/api/server.go` | Modify | Add `handleSearchUsers` handler and route |
| `internal/api/server_test.go` | Modify | Tests for search users endpoint |
| `cmd/api/main.go` | Modify | Wire `DirectoryClient` into `app.Service` |
| `cmd/keycloak-provisioner/main.go` | Modify | Log directory reader client ID on success |

---

## Task 1: Directory Client Core

**Files:**
- Create: `internal/keycloak/directory.go`
- Create: `internal/keycloak/directory_test.go`

**Interfaces:**
- Produces: `keycloak.DirectoryClient`, `keycloak.DirectoryUser`, `keycloak.DirectoryClientConfig`, `DirectoryClient.Authenticate(ctx)`, `DirectoryClient.SearchUsers(ctx, query, first, max) ([]DirectoryUser, error)`

- [ ] **Step 1: Create directory client with config, auth, and search**

Create `internal/keycloak/directory.go` with `DirectoryClientConfig`, `DirectoryUser`, and `DirectoryClient` types. The client authenticates via client-credentials grant and searches users via `GET /admin/realms/{realm}/users?q=...&first=...&max=...&enabled=true`. Reuses `readBounded`, `readBoundedWithTruncation`, and `redactSecrets` from `client.go`.

Key implementation details:
- `DirectoryClientConfig`: `AdminURL *url.URL`, `Realm string`, `ClientID string`, `ClientSecret string`, `HTTPTimeout time.Duration`
- `DirectoryUser`: `ID`, `Username`, `Email`, `FirstName`, `LastName` (JSON-tagged)
- `DirectoryClient`: stores `httpClient`, `adminURL`, `realm`, `clientID`, `clientSecret`, `secrets []string`, `accessToken`
- `NewDirectoryClient(cfg)`: creates client with `http.Client` using `cfg.HTTPTimeout` and `CheckRedirect` returning `http.ErrUseLastResponse` (same as existing `Client`)
- `Authenticate(ctx)`: POST to `{adminURL}/realms/{realm}/protocol/openid-connect/token` with form `client_id`, `client_secret`, `grant_type=client_credentials`. Stores `access_token` in memory.
- `SearchUsers(ctx, query, first, max)`: GET `{adminURL}/admin/realms/{realm}/users` with query params `q`, `first`, `max`, `enabled=true`. Uses `Bearer` token. Returns `[]DirectoryUser`.
- `buildURL(segments...)`: helper to construct admin API URLs (path-escape each segment)
- Error messages include `redactSecrets()` using `c.secrets`

- [ ] **Step 2: Write directory client tests**

Create `internal/keycloak/directory_test.go` with httptest mock server:

| Test | Description |
|---|---|
| `TestDirectoryClientAuthenticatesAndSearches` | Full flow: auth → search. Validates token request form data, Bearer header, `enabled=true` query param, correct response parsing |
| `TestDirectoryClientSearchReturnsEmptyResults` | Empty array from Keycloak → empty slice |
| `TestDirectoryClientSearchForwardsPagination` | Verifies `first` and `max` params forwarded correctly |
| `TestDirectoryClientAuthFailure` | 401 from token endpoint → error |
| `TestDirectoryClientSearchBeforeAuth` | Search without authenticate → error |
| `TestDirectoryClientKeycloakServerError` | 500 from users endpoint → error |
| `TestDirectoryClientMalformedJSON` | Non-JSON response → error |
| `TestDirectoryClientTimeout` | Slow server + short timeout → error |
| `TestDirectoryClientRedactsSecrets` | Secret in error body appears as `[REDACTED]` |

Helper: `newDirectoryClientForServer(t, serverURL)` creates a `DirectoryClient` pointing at test server.
Helper: `mustParseURL(t, raw)` parses URL or fails test.
Uses existing `writeTestJSON` helper from `client_test.go`.

- [ ] **Step 3: Run tests**

Run: `go test ./internal/keycloak/ -run TestDirectory -v`
Expected: All PASS

- [ ] **Step 4: Commit**

```bash
git add internal/keycloak/directory.go internal/keycloak/directory_test.go
git commit -m "feat(keycloak): add least-privilege directory reader client"
```

---

## Task 2: Keycloak Provisioner — Directory Reader Client

**Files:**
- Modify: `internal/keycloak/provisioner.go`
- Modify: `internal/keycloak/provisioner_test.go`
- Modify: `internal/keycloak/config.go`

**Interfaces:**
- Consumes: `keycloak.provisionBackend` interface (existing)
- Produces: `Result.DirectoryReaderClientID`, `Result.DirectoryReaderClientSecret`

- [ ] **Step 1: Add directory reader constants and extend Result**

In `internal/keycloak/provisioner.go`:
- Add constant `directoryReaderClientID = "tflive-directory-reader"`
- Add `var directoryReaderRealmManagementRoles = []string{"query-users", "view-users", "view-realm"}`
- Extend `Result` with `DirectoryReaderClientID string` and `DirectoryReaderClientSecret string`

- [ ] **Step 2: Add directory reader provisioning**

In `internal/keycloak/provisioner.go`, after the platform admin role mapping block (after `EnsureClientRoleMapping` for platform admin), add:
- `EnsureClient` for `tflive-directory-reader` with `ServiceAccountsEnabled: true`, `PublicClient: false`, `BearerOnly: false`, `FullScopeAllowed: false`, `Attributes: disabledGrantAttributes()`
- Loop over `directoryReaderRealmManagementRoles`, calling `ClientRole` for each
- `EnsureClientRoleMapping` to assign roles to the directory reader service account

- [ ] **Step 3: Update return value**

Update the `return Result{...}` at the end of `provisionWithBackend()` to include `DirectoryReaderClientID` and `DirectoryReaderClientSecret`.

- [ ] **Step 4: Add config field**

In `internal/keycloak/config.go`, add `DirectoryReaderClientSecret string` field to `Config` struct.

- [ ] **Step 5: Update provisioner tests**

In `internal/keycloak/provisioner_test.go`, add assertions to `TestProvisionWithBackendIsRepeatableAndUsesApprovedDesiredState`:
- Verify directory reader client was created with `ServiceAccountsEnabled: true`
- Verify `Result.DirectoryReaderClientID` equals `tflive-directory-reader`
- Verify client role mapping calls include directory reader roles

- [ ] **Step 6: Run tests**

Run: `go test ./internal/keycloak/ -run TestProvision -v`
Expected: All PASS

- [ ] **Step 7: Commit**

```bash
git add internal/keycloak/provisioner.go internal/keycloak/provisioner_test.go internal/keycloak/config.go
git commit -m "feat(keycloak): extend provisioner with directory reader client"
```

---

## Task 3: App-Layer Directory Interface

**Files:**
- Create: `internal/app/directory.go`

**Interfaces:**
- Produces: `app.DirectoryUser`, `app.UserDirectory` interface, `app.ErrDirectoryUnavailable`

- [ ] **Step 1: Create directory interface**

Create `internal/app/directory.go`:
- `var ErrDirectoryUnavailable = errors.New("directory unavailable")`
- `DirectoryUser` struct with `ID`, `Username`, `Email`, `FirstName`, `LastName` (JSON-tagged)
- `UserDirectory` interface with `SearchUsers(ctx context.Context, query string, first, max int) ([]DirectoryUser, error)`

- [ ] **Step 2: Commit**

```bash
git add internal/app/directory.go
git commit -m "feat(app): add UserDirectory interface and DirectoryUser type"
```

---

## Task 4: App-Layer SearchUsers Use Case

**Files:**
- Modify: `internal/app/service.go`
- Create: `internal/app/directory_test.go`

**Interfaces:**
- Consumes: `app.UserDirectory` interface (from Task 3)
- Produces: `Service.SearchUsers(ctx, SearchUsersCommand) ([]DirectoryUser, error)`

- [ ] **Step 1: Add UserDirectory to Service and implement SearchUsers**

In `internal/app/service.go`:
- Add `UserDirectory UserDirectory` field to `Service` struct
- Add `SearchUsersCommand` struct: `TenantID`, `Query string`, `First int`, `Max int`
- Add `SearchUsers` method: validates command, requires `platform-admin` role, requires `UserDirectory` non-nil, delegates to `UserDirectory.SearchUsers`
- Add `validateSearchUsersCommand`: rejects empty tenant, empty query, negative first, max outside 1..50

- [ ] **Step 2: Write SearchUsers tests**

Create `internal/app/directory_test.go` with fakes:

| Test | Description |
|---|---|
| `TestSearchUsersReturnsResults` | Platform admin gets results from directory |
| `TestSearchUsersRequiresPlatformAdmin` | Non-admin gets `ErrForbidden` |
| `TestSearchUsersRequiresAuthentication` | No principal gets `ErrUnauthenticated` |
| `TestSearchUsersRejectsEmptyQuery` | Empty query gets `ErrInvalidCommand` |
| `TestSearchUsersRejectsInvalidPagination` | Table-driven: negative first, zero max, max>50 |
| `TestSearchUsersDirectoryUnavailable` | Nil directory gets `ErrDirectoryUnavailable` |
| `TestSearchUsersDirectoryError` | Directory error propagated |
| `TestSearchUsersEmptyResults` | Nil from directory → empty slice |

Helpers: `contextWithPlatformAdmin()`, `contextWithOrdinaryUser()`, `fakeUserDirectory`, `fakeErrorDirectory`

- [ ] **Step 3: Run tests**

Run: `go test ./internal/app/ -run TestSearchUsers -v`
Expected: All PASS

- [ ] **Step 4: Commit**

```bash
git add internal/app/service.go internal/app/directory_test.go
git commit -m "feat(app): add SearchUsers use case with platform-admin gate"
```

---

## Task 5: API Endpoint

**Files:**
- Modify: `internal/api/server.go`
- Modify: `internal/api/server_test.go`

**Interfaces:**
- Consumes: `app.Service.SearchUsers(ctx, app.SearchUsersCommand)` (from Task 4)
- Produces: `GET /v1/tenants/{tenant_id}/users/search` route

- [ ] **Step 1: Add route and handler**

In `internal/api/server.go`:
- Add `handleTenantRoute("GET /v1/tenants/{tenant_id}/users/search", server.handleSearchUsers)` in `NewServer`
- Add `handleSearchUsers` method: parses `q`, `first`, `max` from query params, validates, calls `service.SearchUsers`, returns JSON `{ users, first, max }`
- Add `searchUsersResponse` type
- Add `import "strconv"` and `"strings"`
- Add `app.ErrDirectoryUnavailable` case in `writeAppError` → 503 `directory_unavailable`

Query param validation in handler:
- `q` required, non-empty after trim, max 200 chars
- `first` defaults to 0, must be non-negative integer
- `max` defaults to 20, must be integer 1..50

- [ ] **Step 2: Add directory mock to test dependencies**

In `internal/api/server_test.go`:
- Add `userDirectory` field to `apiTestDependencies`
- Add `fakeUserDirectory` struct (same pattern as app tests)
- Wire `UserDirectory: deps.userDirectory` in `deps.service()`

- [ ] **Step 3: Write API handler tests**

Add to `internal/api/server_test.go`:

| Test | Description |
|---|---|
| `TestSearchUsersPlatformAdminAllowed` | Returns 200 with user results |
| `TestSearchUsersNonAdminForbidden` | Returns 403 |
| `TestSearchUsersMissingQuery` | Returns 400 |
| `TestSearchUsersInvalidPagination` | Table-driven: negative first, max=0, max=51, non-numeric |
| `TestSearchUsersDirectoryUnavailable` | Returns 503 |
| `TestSearchUsersUnauthenticated` | Returns 401 (via middleware) |

- [ ] **Step 4: Run tests**

Run: `go test ./internal/api/ -run TestSearchUsers -v`
Expected: All PASS

- [ ] **Step 5: Commit**

```bash
git add internal/api/server.go internal/api/server_test.go
git commit -m "feat(api): add user search endpoint for access management"
```

---

## Task 6: Server Wiring and Config

**Files:**
- Modify: `cmd/api/main.go`
- Modify: `cmd/keycloak-provisioner/main.go`
- Modify: `internal/config/auth.go`
- Modify: `internal/config/auth_test.go`

**Interfaces:**
- Consumes: `keycloak.NewDirectoryClient(keycloak.DirectoryClientConfig)` (from Task 1)
- Produces: Wired `DirectoryClient` in API server startup

- [ ] **Step 1: Add directory reader config to SecurityConfig**

In `internal/config/auth.go`:
- Add to `SecurityConfig`: `DirectoryReaderClientID string`, `DirectoryReaderClientSecret Secret`, `DirectoryReaderHTTPTimeout time.Duration`
- In `loadSecurityConfig`: read `KEYCLOAK_DIRECTORY_READER_CLIENT_ID` (default empty), `KEYCLOAK_DIRECTORY_READER_CLIENT_SECRET`, `KEYCLOAK_DIRECTORY_READER_HTTP_TIMEOUT` (default 10s)
- Production mode: require non-empty `DirectoryReaderClientID` and non-empty `DirectoryReaderClientSecret`
- Add `String()`/`GoString()` methods redacting the secret

- [ ] **Step 2: Write config tests**

In `internal/config/auth_test.go`:
- Valid development config: directory reader fields optional
- Valid production config: directory reader fields required
- Missing `DirectoryReaderClientID` in production → error
- Missing `DirectoryReaderClientSecret` in production → error
- `DirectoryReaderClientSecret.String()` returns `[REDACTED]`

- [ ] **Step 3: Wire DirectoryClient in cmd/api/main.go**

In `cmd/api/main.go`:
- If `cfg.Security.DirectoryReaderClientSecret` is non-empty, create `keycloak.NewDirectoryClient` with config from `cfg.Security`
- Pass to `app.Service{..., UserDirectory: directoryClient}`
- If empty, leave `UserDirectory` nil (returns `ErrDirectoryUnavailable`)

- [ ] **Step 4: Update provisioner logging**

In `cmd/keycloak-provisioner/main.go`:
- Log `result.DirectoryReaderClientID` on success (non-sensitive)

- [ ] **Step 5: Run all tests**

Run: `go test ./...`
Expected: All PASS (existing 638 + new tests)

- [ ] **Step 6: Commit**

```bash
git add cmd/api/main.go cmd/keycloak-provisioner/main.go internal/config/auth.go internal/config/auth_test.go
git commit -m "feat: wire directory reader client and config into API server"
```

---

## Task 7: Sprint Backlog Update

**Files:**
- Modify: `docs/sprint/authn_and_authz/README.md`

- [ ] **Step 1: Update AUTH-016 status to Done**

In `docs/sprint/authn_and_authz/README.md`, change AUTH-016 row status from `Not Started` to `Done`.

- [ ] **Step 2: Commit**

```bash
git add docs/sprint/authn_and_authz/README.md
git commit -m "docs: mark AUTH-016 as Done in sprint backlog"
```

---

## Task 8: Final Verification

- [ ] **Step 1: Run full test suite**

Run: `go test ./...`
Expected: All PASS

- [ ] **Step 2: Run vet and lint**

Run: `go vet ./...` and `staticcheck ./...` (if configured)
Expected: No issues

- [ ] **Step 3: Verify no secrets committed**

Run: `git log --all --oneline` and inspect for any committed secrets or tokens.
Expected: No secrets in any commit.
