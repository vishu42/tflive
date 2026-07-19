# AUTH-016: Least-privilege Keycloak User-Directory Reader Design

## Summary

Add a read-only Keycloak user-directory adapter that uses a dedicated service credential with minimal realm permissions (`query-users`, `view-users`, `view-realm`) to search and resolve realm users. Exposes a paginated search endpoint gated by `platform-admin` role, returning only safe display attributes (subject ID, username, email, name) — never credentials or admin tokens.

## Motivation

AUTH-017 (access management APIs) needs to resolve Keycloak users when assigning stack roles, but should not take over user lifecycle management. A dedicated least-privilege credential ensures the directory adapter cannot create, modify, or delete users, even if the adapter code is compromised.

## Design Decisions

| Decision | Choice | Rationale |
|---|---|---|
| Credential model | Separate Keycloak confidential client with client-credentials grant | Matches acceptance criteria ("backend-only service credential" with "minimum required Keycloak permissions") |
| Client permissions | `query-users`, `view-realm`, `view-users` from `realm-management` | Minimum set required to search users by username/email; no write permissions |
| Search gating | `platform-admin` global role | Simple, aligns with who currently performs access management |
| API surface | Standalone `GET /v1/tenants/{tenant_id}/users/search` | Reusable by AUTH-017 and future callers; not coupled to a specific stack |
| Disabled-user filtering | Server-side via Keycloak `enabled=true` query param | Prevents disabled users from appearing as assignment candidates without post-filtering |
| Config location | `SecurityConfig` in `internal/config/auth.go` | Follows existing pattern for auth-related configuration |

## Architecture

```
┌──────────────┐     client-credentials     ┌──────────────┐
│  API Server  │ ────────────────────────── │  Keycloak    │
│              │   GET /admin/realms/.../users │  Admin REST  │
│  Directory   │ ◄───────────────────────── │  API         │
│  Client      │   [{ id, username, ... }]  │              │
└──────┬───────┘                            └──────────────┘
       │
       │ UserDirectory interface
       ▼
┌──────────────┐
│  App Service │
│  SearchUsers │
└──────┬───────┘
       │ platform-admin gate
       ▼
┌──────────────┐
│  API Handler │
│  GET /users/ │
│  search      │
└──────────────┘
```

## New Keycloak Client

The provisioner creates a confidential client `tflive-directory-reader` in the tflive realm:

| Property | Value |
|---|---|
| `clientId` | `tflive-directory-reader` (configurable via `KEYCLOAK_DIRECTORY_READER_CLIENT_ID`) |
| `protocol` | `openid-connect` |
| `serviceAccountsEnabled` | `true` |
| `publicClient` | `false` |
| `bearerOnly` | `false` |
| `directAccessGrantsEnabled` | `false` |
| `standardFlowEnabled` | `false` |
| `fullScopeAllowed` | `false` |

Assigned client roles from `realm-management`:
- `query-users`
- `view-users`
- `view-realm`

These are the same roles already assigned to the platform admin for user management. The directory reader gets only these three — no `manage-users`, no `create-client`, no realm-level admin roles.

## Directory Adapter

New file: `internal/keycloak/directory.go`

```go
package keycloak

// DirectoryClient reads user information from Keycloak using a
// least-privilege service credential. It never exposes credentials
// or administrative tokens.
type DirectoryClient struct {
    httpClient  *http.Client
    adminURL    *url.URL
    realm       string
    clientID    string
    clientSecret Secret
    secrets     []string
    accessToken string
}
```

The adapter returns raw Keycloak user data (unmarshalled from the Admin REST API JSON). The app layer (`internal/app/directory.go`) defines the domain `DirectoryUser` type and the `UserDirectory` interface. The `cmd/api` wiring maps adapter results to app-domain types.

### Authentication

Uses OAuth2 client-credentials grant against the realm token endpoint:
```
POST /realms/{realm}/protocol/openid-connect/token
Content-Type: application/x-www-form-urlencoded

client_id=tflive-directory-reader&client_secret=...&grant_type=client_credentials
```

Token is held in memory only, same pattern as the existing `Client.Authenticate()`.

### User Search

Calls the Keycloak Admin REST API:
```
GET /admin/realms/{realm}/users?q={query}&first={offset}&max={limit}&enabled=true
```

Key parameters:
- `q` — username/email search (Keycloak performs case-insensitive partial match)
- `first` — offset for pagination (0-indexed)
- `max` — page size limit
- `enabled=true` — server-side filter excludes disabled users

Returns `[]DirectoryUser` with only safe display fields.

### Error Handling

- Keycloak 4xx/5xx → wrapped error with HTTP status, secrets redacted
- Auth failure → clear error message indicating credential problem
- Timeout → propagated as context deadline exceeded
- Malformed response → error indicating unexpected Keycloak response

### Configuration

New config struct:

```go
type DirectoryReaderConfig struct {
    ClientID     string
    ClientSecret Secret
    Realm        string
    AdminURL     *url.URL
    HTTPTimeout  time.Duration
}
```

Environment variables:
- `KEYCLOAK_DIRECTORY_READER_CLIENT_ID` (required in production, defaults to `tflive-directory-reader`)
- `KEYCLOAK_DIRECTORY_READER_CLIENT_SECRET` (required in production)
- `KEYCLOAK_DIRECTORY_READER_HTTP_TIMEOUT` (optional, default 10s)

Production mode requires non-empty `ClientID` and `ClientSecret`.

## App-Layer Interface

New file: `internal/app/directory.go`

```go
package app

// DirectoryUser is the safe public representation of a Keycloak user
// returned by the user directory. It contains only display attributes
// needed for access management — never credentials or tokens.
type DirectoryUser struct {
    ID        string `json:"id"`
    Username  string `json:"username"`
    Email     string `json:"email"`
    FirstName string `json:"firstName"`
    LastName  string `json:"lastName"`
}

// UserDirectory searches existing Keycloak users for access management.
// It returns only safe display attributes and never exposes credentials
// or administrative tokens.
type UserDirectory interface {
    SearchUsers(ctx context.Context, query string, first, max int) ([]DirectoryUser, error)
}
```

## API Endpoint

`GET /v1/tenants/{tenant_id}/users/search?q={query}&first=0&max=20`

### Query Parameters

| Parameter | Required | Default | Constraints | Description |
|---|---|---|---|---|
| `q` | yes | — | Non-empty, max 200 chars | Username or email search term |
| `first` | no | `0` | `>= 0` | Offset for pagination (0-indexed) |
| `max` | no | `20` | `1..50` | Page size (bounded) |

### Authorization

- Requires authenticated request (handled by existing `RequireAuthentication` middleware)
- Requires `platform-admin` global role
- Non-admin callers receive `403 Forbidden`

### Response

```json
{
  "users": [
    {
      "id": "uuid",
      "username": "jdoe",
      "email": "jdoe@example.com",
      "firstName": "Jane",
      "lastName": "Doe"
    }
  ],
  "first": 0,
  "max": 20
}
```

### Error Responses

| Status | Code | Condition |
|---|---|---|
| `401` | `unauthorized` | Missing or invalid bearer token |
| `403` | `forbidden` | Caller lacks `platform-admin` role |
| `400` | `invalid_request` | Missing `q`, invalid `first`/`max` values |
| `503` | `directory_unavailable` | Keycloak unreachable or returned error |

### Empty Query Behavior

An empty `q` parameter returns `400 invalid_request`. This prevents accidental full-realm dumps and enforces intentional search.

## Provisioner Changes

Extend `internal/keycloak/provisioner.go` to:

1. Create the `tflive-directory-reader` confidential client with service accounts enabled
2. Look up `query-users`, `view-users`, `view-realm` from the `realm-management` client
3. Assign these client roles to the directory reader's service account
4. Return the client secret in `Result` (for deployment configuration)

The provisioner pattern already handles client creation and role assignment — this follows the same `EnsureClient` / `ClientRole` / `EnsureClientRoleMapping` flow used for the platform admin.

## Server Wiring

In `cmd/api/main.go`, the directory reader is initialized from config and injected into `app.Service`:

```go
directoryClient := keycloak.NewDirectoryClient(directoryConfig)
service := app.NewService(app.Service{
    // ... existing fields ...
    UserDirectory: directoryClient,
})
```

The `UserDirectory` field is optional — `SearchUsers` returns a clear error if the directory is not configured.

## Tests

### Directory Adapter Tests (`internal/keycloak/directory_test.go`)

Using `httptest` to mock the Keycloak Admin REST API:

| Test Case | Description |
|---|---|
| Successful search | Returns users matching query with correct display attributes |
| Pagination | Verifies `first` and `max` params are forwarded; multiple pages work |
| Disabled users excluded | Confirms `enabled=true` is sent; disabled users absent from response |
| Empty results | Query matches no users; returns empty array |
| Auth failure | Invalid credentials → clear error |
| Keycloak 5xx | Server error → error with status, secrets redacted |
| Timeout | Context deadline exceeded → propagated |
| Malformed JSON | Unexpected response body → error indicating bad response |
| Client-credentials grant | Verifies correct token request format |

### Config Tests (`internal/config/auth_test.go`)

| Test Case | Description |
|---|---|
| Valid config | All directory reader fields present |
| Missing client ID in production | Returns validation error |
| Missing client secret in production | Returns validation error |
| Development mode allows empty | No error when fields missing in development |
| Invalid HTTP timeout | Returns validation error |

### API Tests (`internal/api/server_test.go`)

| Test Case | Description |
|---|---|
| Platform admin allowed | Returns search results |
| Non-admin forbidden | Returns 403 |
| Missing q parameter | Returns 400 |
| Invalid first/max | Returns 400 |
| Directory unavailable | Returns 503 |
| Unauthenticated | Returns 401 |

## Sprint Backlog Update

Update `docs/sprint/authn_and_authz/README.md`:
- Set AUTH-016 status to `In Progress` (then `Done` on completion)
- AUTH-017 dependency on AUTH-016 remains unchanged

## Security Considerations

- The directory reader credential is a client secret, not a user password — lower risk if leaked
- The adapter never stores the access token beyond the request lifetime
- Secrets are redacted in all error messages via existing `redactSecrets`
- The `enabled=true` server-side filter prevents disabled users from appearing as assignment candidates
- The `max` parameter is bounded to 50 to prevent large-realm enumeration
- The empty `q` rejection prevents wildcard searches that could dump the entire user directory
- No user credentials (passwords, MFA state, federated identities) are ever returned
