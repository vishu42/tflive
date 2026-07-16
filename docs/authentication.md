# Authentication and Authorization

This document defines the Keycloak realm and identity resources provisioned for
tflive and the OpenFGA model used for per-stack authorization. The broader
trust model and authorization invariants remain in the
[authentication and authorization security architecture](superpowers/specs/2026-07-14-authn-authz-security-architecture-design.md).

## Local Realm

Docker Compose runs Keycloak 26.6.3 at `http://localhost:8082` and executes the
`keycloak-provision` one-shot service after Keycloak reports healthy. The
service reconciles named resources through the Keycloak Admin REST API and
exits non-zero if any operation or effective-token postcondition fails.

The local issuer is:

```text
http://localhost:8082/realms/tflive
```

The realm has a 300-second access-token lifespan, is enabled, does not permit
self-registration, and uses Keycloak's `external` SSL policy. Local loopback
HTTP exists only for development; production uses one canonical HTTPS issuer.

## OIDC Clients and Claims

| Resource | Configuration |
|---|---|
| `tflive-web` | Public OpenID Connect client; Authorization Code flow enabled; PKCE S256 required; implicit, password, device, CIBA, service-account, and standard token-exchange grants disabled |
| `tflive-api` | Bearer-only OpenID Connect client used as the API audience |
| `tflive-api-audience` | Default client scope on `tflive-web` with a hardcoded `tflive-api` access-token audience mapper |
| `roles` | Built-in default client scope explicitly linked to `tflive-web`; realm roles remain in `realm_access.roles` |

The local browser allowlist contains exact entries only:

```text
Redirect URIs:
  http://localhost:5173/
  http://127.0.0.1:5173/

Web origins:
  http://localhost:5173
  http://127.0.0.1:5173
```

Wildcard redirects and origins are rejected by provisioner configuration
validation. Plain HTTP browser endpoints are accepted only for loopback hosts.
User information, queries, and fragments are also rejected in configured URLs.

An access token for the initial platform administrator is verified during every
provisioning run to contain:

```json
{
  "aud": ["tflive-api"],
  "realm_access": {
    "roles": ["platform-admin"]
  }
}
```

Keycloak can serialize a single audience as either a string or an array. tflive
provisioning accepts both representations when enforcing the postcondition.

## Global Roles

| Role | Meaning |
|---|---|
| `platform-admin` | Administer tflive and bypass ordinary stack checks, but never authentication, tenant validation, audit requirements, last-owner protection, dependency fail-closed behavior, or self-approval prevention |
| `stack-creator` | Create a stack and become its initial OpenFGA owner |

These are realm roles and appear in `realm_access.roles`. Per-stack roles never
belong in Keycloak; AUTH-004 provisions those relationships in OpenFGA.

## Administrator Boundary

Two different identities serve different purposes:

1. The master-realm bootstrap administrator is supplied to Keycloak itself and
   is used by the one-shot provisioner. Its credentials are not shared for
   daily platform administration.
2. The initial tflive platform administrator is a user inside the `tflive`
   realm. It receives the `platform-admin` realm role and only these
   `realm-management` client roles: `query-users`, `view-users`,
   `manage-users`, and `view-realm`.

The tflive administrator can use the dedicated console at
`http://localhost:8082/admin/tflive/console/` to find and manage tflive users
and assign the fixed global roles. It does not receive the broad `realm-admin`
composite and cannot administer the master realm.

Keycloak 26's default user profile requires email, first name, and last name for
normal realm users. Provisioning reconciles those attributes and marks the
trusted bootstrap email as verified so the initial administrator is immediately
usable. The password is set only when the user is first created; later reruns do
not overwrite a password rotated by a deployment administrator.

## Keycloak Provisioner Configuration

The provisioner requires these values. Local-only examples live in
`.env.example`; production supplies them through its secret/config delivery
system and must not reuse the examples.

| Variable | Sensitive | Purpose |
|---|---:|---|
| `KEYCLOAK_ADMIN_URL` | No | Admin API base URL; Compose fixes it to `http://keycloak:8080` |
| `KEYCLOAK_ADMIN_REALM` | No | Bootstrap administrator realm; defaults to `master` |
| `KEYCLOAK_ADMIN_USERNAME` | Yes | Master bootstrap administrator username |
| `KEYCLOAK_ADMIN_PASSWORD` | Yes | Master bootstrap administrator password |
| `KEYCLOAK_REALM` | No | Product realm; defaults to `tflive` |
| `KEYCLOAK_WEB_CLIENT_ID` | No | Browser client; defaults to `tflive-web` |
| `KEYCLOAK_API_CLIENT_ID` | No | API audience client; defaults to `tflive-api` |
| `KEYCLOAK_WEB_REDIRECT_URIS` | No | Comma-separated exact redirects |
| `KEYCLOAK_WEB_ORIGINS` | No | Comma-separated exact browser origins |
| `KEYCLOAK_PLATFORM_ADMIN_USERNAME` | Yes | Initial tflive platform administrator username |
| `KEYCLOAK_PLATFORM_ADMIN_PASSWORD` | Yes | Initial password, used only when creating the user |
| `KEYCLOAK_PLATFORM_ADMIN_EMAIL` | No | Required trusted bootstrap profile email |
| `KEYCLOAK_PLATFORM_ADMIN_FIRST_NAME` | No | Required bootstrap profile first name |
| `KEYCLOAK_PLATFORM_ADMIN_LAST_NAME` | No | Required bootstrap profile last name |
| `KEYCLOAK_HTTP_TIMEOUT` | No | Per-client HTTP timeout; defaults to 10 seconds |

Configured passwords and in-memory admin tokens are redacted from surfaced
errors and never written to successful logs.

## API Runtime Security Configuration

The API validates its authentication and authorization configuration before it
connects to Postgres or Temporal or starts its HTTP listener.

| Variable | Sensitive | Purpose |
|---|---:|---|
| `TFLIVE_ENVIRONMENT` | No | Optional runtime mode; empty defaults to `development`; valid values are `development` and `production` |
| `TFLIVE_TENANT_ID` | No | Required single configured tenant identifier |
| `OIDC_ISSUER_URL` | No | Required exact Keycloak issuer URL |
| `OIDC_AUDIENCE` | No | Required access-token audience; local value is `tflive-api` |
| `OPENFGA_API_URL` | No | Required OpenFGA API base URL |
| `OPENFGA_STORE_ID` | No | Required exact store ID emitted by bootstrap |
| `OPENFGA_MODEL_ID` | No | Required exact immutable model ID emitted by bootstrap |
| `OPENFGA_API_TOKEN` | Yes | Optional for local development and required in production |
| `OPENFGA_HTTP_TIMEOUT` | No | Positive per-request deadline; defaults to `10s` |

Development permits the documented loopback HTTP issuer, local HTTP OpenFGA
endpoint, and tokenless OpenFGA service. Production must be selected explicitly
with `TFLIVE_ENVIRONMENT=production`; it requires HTTPS for both external
dependencies and a non-empty OpenFGA bearer token. Unknown modes, malformed
tenant IDs, unsafe URLs or identifiers, and non-positive timeouts stop startup.

The OpenFGA store and model IDs are never discovered at API startup. Copy the
exact assignments printed by the serialized bootstrap command into runtime
configuration before starting the API. Keycloak bootstrap passwords and
provisioner administrator tokens are not API runtime credentials.

## API Access-Token Verification

The API accepts compact bearer access tokens for the configured issuer and
`OIDC_AUDIENCE`. It does not accept ID tokens or opaque tokens. Signature,
issuer, audience, expiry, optional not-before (when present), subject, and
bearer type checks complete before identity data is returned.

Keycloak discovery and JWKS signing keys are cached. A new or replaced signing
key triggers one bounded refresh, so routine key rotation does not require an
API restart. A fresh cached key continues to work through a short Keycloak
outage. If the verifier cannot fetch required public keys, it fails closed and
exposes no token or provider-response detail.

AUTH-007 middleware owns HTTP status mapping and bearer-header parsing.

## Operation and Reruns

Start or reconcile the realm with:

```bash
docker compose --env-file .env up --build keycloak-provision
```

A successful run exits `0` after checking the effective access token. Re-run
the same command after configuration changes. The provisioner looks up realms,
clients, roles, scopes, mappers, and users by their immutable names, creates
missing resources, and repairs fields owned by tflive without discarding
unrelated representation fields managed by a deployment administrator.

To prove idempotence locally:

```bash
docker compose --env-file .env up --build --force-recreate keycloak-provision
```

Duplicate exact client IDs, usernames, client-scope names, or mapper names fail
the run instead of making an arbitrary choice.

## OpenFGA Stack Authorization

The model defines `user` and `stack` types. Only the four direct stack roles can
be assigned to a user; the permission relations are derived and cannot be
assigned directly.

### Role and Permission Matrix

| Direct stack role | `can_view` | `can_operate` | `can_approve` | `can_manage_access` | Meaning |
|---|---:|---:|---:|---:|---|
| `owner` | Allowed | Allowed | Allowed | Allowed | View, operate, approve, and manage access |
| `operator` | Allowed | Allowed | Denied | Denied | View and operate |
| `approver` | Allowed | Denied | Allowed | Denied | View and approve |
| `viewer` | Allowed | Denied | Denied | Denied | View only |

The derived relations are exactly:

- `can_view = owner or operator or approver or viewer`
- `can_operate = owner or operator`
- `can_approve = owner or approver`
- `can_manage_access = owner`

`operator` always means the per-stack OpenFGA role in this repository. The
person or pipeline that configures and deploys the services is the deployment
administrator.

### Provisioning and Verification

On a clean checkout, initialize OpenFGA with the two-phase workflow:

```bash
docker compose --env-file .env.example up -d openfga-postgres openfga-migrate openfga
docker compose --env-file .env.example run --rm openfga-provision bootstrap
# Copy OPENFGA_STORE_ID and OPENFGA_MODEL_ID from stdout into .env.
docker compose run --rm openfga-provision verify
```

The provisioner's standard output contains only these two assignments:

```text
OPENFGA_STORE_ID=<store ID>
OPENFGA_MODEL_ID=<authorization model ID>
```

The deployment administrator copies both assignment lines into environment
configuration as text; the bootstrap output must not be executed or evaluated
directly.
Bootstrap discovers only the uniquely named `tflive` store and reuses exactly
one semantic match for the repository model. Duplicate `tflive` store names or
duplicate semantic model matches fail closed rather than selecting an arbitrary
resource.

Bootstrap must be serialized because OpenFGA store names are not unique. If a
run fails after creating only the store, or after creating the model but before
the IDs are recorded, rerun the same bootstrap command: it safely reuses the
unique completed resource and finishes the missing work. A model definition
change creates a new immutable model ID; the deployment administrator must
explicitly update `OPENFGA_MODEL_ID` in environment configuration.

Verify fetches the exact `OPENFGA_STORE_ID` and `OPENFGA_MODEL_ID`, compares the
exact stored model with the repository model, and never writes or otherwise
mutates OpenFGA. It never discovers or substitutes a latest model. The API will
later use the same explicit IDs, so verification and runtime authorization
remain pinned to the environment configuration.
