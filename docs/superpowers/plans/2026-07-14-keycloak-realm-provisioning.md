# Keycloak Realm Provisioning Implementation Plan

> **For Codex:** Execute this plan with test-driven development and verify every
> acceptance criterion against a live Keycloak 26.6.3 container before closing
> AUTH-003.

**Goal:** Provision and continuously reconcile the local `tflive` Keycloak
realm, OIDC clients, token claims, fixed global roles, and initial platform
administrator without duplicating named resources.

**Architecture:** Add a small Go one-shot provisioner that authenticates to the
Keycloak Admin REST API with bootstrap credentials supplied at runtime. It
creates missing resources, updates fields owned by tflive on existing
resources, assigns roles idempotently, and verifies the effective example
access token before returning success. Docker Compose builds and runs the
provisioner only after Keycloak is healthy. Keycloak startup realm import is not
the authority because it skips an already-existing realm and cannot reconcile
later desired-state changes.

**Tech stack:** Go 1.24 standard library, Keycloak Admin REST API, Docker
Compose, Node.js contract tests, Go `httptest` tests.

**Issue:** [AUTH-003](https://github.com/vishu42/tflive/issues/5)

## Approved Design Decisions

- Manage one realm named `tflive` with a maximum access-token lifespan of 300
  seconds.
- Provision `tflive-web` as a public OpenID Connect client. Enable only the
  standard Authorization Code flow and require PKCE S256. Disable implicit,
  resource-owner password, device, CIBA, and service-account flows.
- Accept only exact redirect URIs and web origins. Reject wildcard entries,
  URL fragments, URL user information, and non-loopback plain HTTP browser
  endpoints. Local development permits exactly `http://localhost:5173/` and
  `http://127.0.0.1:5173/` with their matching origins.
- Provision `tflive-api` as a bearer-only client. Add a default client scope
  with an explicit hardcoded audience mapper for `tflive-api`.
- Keep global realm roles in Keycloak's standard `realm_access.roles` access
  token claim and explicitly link the built-in `roles` client scope.
- Provision the fixed realm roles `platform-admin` and `stack-creator` with
  descriptions matching the approved security architecture.
- Source the initial platform administrator username and password only from
  runtime environment values. Set the password only when creating the user;
  reruns do not overwrite an operator-rotated password.
- Keep the tflive realm administrator distinct from the master-realm bootstrap
  administrator. Assign `platform-admin` plus the least-privilege
  `realm-management` roles `query-users`, `view-users`, `manage-users`, and
  `view-realm`. Do not grant the broad `realm-admin` composite.
- Treat an example access token generated for the browser client and bootstrap
  platform user as a postcondition: its audience must include `tflive-api` and
  `realm_access.roles` must include `platform-admin`.
- Never log passwords, access tokens, refresh tokens, or credential-bearing
  request bodies. Redact configured secret values from all surfaced errors.

## Task 1: Add typed, security-conscious provisioner configuration

**Files:**

- Create: `internal/keycloak/config.go`
- Create: `internal/keycloak/config_test.go`

1. Write failing table-driven tests for a complete local configuration,
   required admin/platform secrets, distinct admin identities, exact URI list
   parsing, wildcard rejection, malformed URLs, and insecure non-loopback HTTP
   browser endpoints.
2. Implement `LoadConfig(getenv func(string) string) (Config, error)` with
   defaults only for non-secret identifiers and timeouts.
3. Keep the following values required at runtime:
   `KEYCLOAK_ADMIN_URL`, `KEYCLOAK_ADMIN_USERNAME`,
   `KEYCLOAK_ADMIN_PASSWORD`, `KEYCLOAK_WEB_REDIRECT_URIS`,
   `KEYCLOAK_WEB_ORIGINS`, `KEYCLOAK_PLATFORM_ADMIN_USERNAME`, and
   `KEYCLOAK_PLATFORM_ADMIN_PASSWORD`.
4. Default the admin realm to `master`, product realm to `tflive`, browser
   client to `tflive-web`, API client to `tflive-api`, and timeout to ten
   seconds.
5. Run `go test ./internal/keycloak -run TestLoadConfig -count=1`.

## Task 2: Implement a bounded, secret-redacting Keycloak Admin API client

**Files:**

- Create: `internal/keycloak/client.go`
- Create: `internal/keycloak/client_test.go`

1. Write failing HTTP tests for master-realm password authentication, bearer
   authorization, JSON request/response handling, accepted status codes,
   bounded error bodies, URL escaping, and redaction when a hostile server
   echoes configured secrets.
2. Implement the client using only `net/http`, `net/url`, and `encoding/json`.
3. Never include authorization headers, form bodies, raw tokens, or passwords
   in errors. Replace any configured secret value that appears in a response
   error with `[REDACTED]`.
4. Limit response bodies and apply the configured HTTP timeout.
5. Run `go test ./internal/keycloak -run 'TestClient|TestRedact' -count=1`.

## Task 3: Reconcile realm, roles, clients, scopes, and mappings

**Files:**

- Create: `internal/keycloak/provisioner.go`
- Create: `internal/keycloak/provisioner_test.go`

1. Write a stateful fake Admin API test that runs provisioning twice and
   proves exactly one realm, two owned clients, two fixed roles, one audience
   scope, one audience mapper, and one platform user exist.
2. Add drift to owned browser-client fields between runs and prove the second
   run repairs public-client, flow, PKCE, redirect, and origin settings without
   duplicating the client.
3. Add failure tests for duplicate lookup results, missing built-in `roles` or
   `realm-management` resources, failed role assignment, and an effective token
   missing either the API audience or `platform-admin`.
4. Implement create-or-update reconciliation keyed only by immutable Keycloak
   resource identifiers: realm name, client ID, role name, client-scope name,
   protocol-mapper name, and exact username.
5. Preserve fields not owned by tflive when updating existing resource
   representations.
6. Set the initial password only on user creation, then idempotently apply the
   realm and realm-management role mappings.
7. Generate an example access token through
   `/clients/{uuid}/evaluate-scopes/generate-example-access-token` and enforce
   the audience/role postconditions.
8. Run `go test ./internal/keycloak -count=1`.

## Task 4: Add the one-shot command and container

**Files:**

- Create: `cmd/keycloak-provisioner/main.go`
- Create: `cmd/keycloak-provisioner/main_test.go`
- Create: `Dockerfile.keycloak-provisioner`
- Modify: `docker-compose.yaml`
- Modify: `.env.example`
- Modify: `scripts/verify-auth-compose.mjs`

1. Write failing command tests for configuration failure, authentication/API
   failure, successful provisioning, and cancellation.
2. Implement a signal-aware command that loads environment configuration,
   runs provisioning once, logs only non-sensitive resource identifiers, and
   exits non-zero on any failed postcondition.
3. Add a pinned multi-stage image that compiles only the provisioner and runs
   it as an unprivileged user with CA certificates available.
4. Add `keycloak-provision` to Compose with a `service_healthy` dependency on
   Keycloak, `restart: "no"`, required secret substitutions, exact local URI
   settings, and no published port.
5. Extend the Compose contract test to require the provisioner build,
   dependency, secret mappings, and one-shot behavior.
6. Run:

   ```sh
   node scripts/verify-auth-compose.mjs
   docker compose --env-file .env.example config --quiet
   go test ./cmd/keycloak-provisioner ./internal/keycloak -count=1
   ```

## Task 5: Document operation and update tracking

**Files:**

- Create: `docs/authentication.md`
- Modify: `README.md`
- Modify: `docs/sprint/authn_and_authz/README.md`

1. Document the local issuer, exact client settings, token claims, role
   meanings, five-minute access-token lifetime, secret inputs, and the
   distinction between master bootstrap and tflive realm administration.
2. Document safe reruns and explain that existing platform-user passwords are
   not overwritten.
3. Update clean-checkout startup to include `keycloak-provision` and show how
   to inspect its successful exit.
4. Mark AUTH-003 `In Progress` during implementation and `Done` only after live
   and regression verification.

## Task 6: Verify against live Keycloak and the full repository

1. Build and run the provisioner against the clean local Compose stack.
2. Run the provisioner a second time and require another successful exit.
3. Query Keycloak Admin REST representations to prove resource counts,
   exact redirect/origin settings, PKCE S256, disabled unsafe flows, role
   assignments, and effective example-token audience/role claims.
4. Prove the tflive realm administrator can authenticate to its dedicated
   realm Admin Console/API and cannot administer the master realm.
5. Run fresh final verification:

   ```sh
   go test ./...
   npm test
   npm run build
   node scripts/verify-auth-compose.mjs
   docker compose --env-file .env.example config --quiet
   git diff --check
   ```

6. Commit intentionally, fast-forward the verified branch into `main`, rerun
   the regression suite from `main`, push, attach the evidence to issue #5, and
   close it as completed.
