# Authentication and Authorization

This document defines the Keycloak realm and identity resources provisioned for
tflive and the OpenFGA model used for per-stack authorization. The broader
trust model and authorization invariants remain in the
[authentication and authorization security architecture](superpowers/specs/2026-07-14-authn-authz-security-architecture-design.md).

## Local Realm

Docker Compose runs Keycloak 26.6.3 at `http://keycloak.localhost:8082` and executes the
`keycloak-provision` one-shot service after Keycloak reports healthy. The
service reconciles named resources through the Keycloak Admin REST API and
exits non-zero if any operation fails.

The local issuer is:

```text
http://keycloak.localhost:8082/realms/tflive
```

The realm has a one-hour access-token lifespan, is enabled, does not permit
self-registration, and uses Keycloak's `external` SSL policy. Local loopback
HTTP exists only for development; production uses one canonical HTTPS issuer.
This lifespan no longer bounds a browser session: it governs only the ID
token's freshness during the sign-in round trip itself. See "Browser Session"
below for the session tflive owns after that, deliberately independent of it.

## OIDC Clients and Claims

| Resource | Configuration |
|---|---|
| `tflive-api` | Confidential OpenID Connect client; Authorization Code flow with PKCE S256; implicit, password, device, CIBA, service-account, and standard token-exchange grants disabled |

There is one client, not two. The API is the only OIDC client and the only
party that ever talks to Keycloak: `/v1/auth/login`, `/v1/auth/callback`, and
`/v1/auth/logout` on the API run the entire authorization-code exchange
server-side, holding the client secret, and the browser receives nothing but
an httpOnly session cookie (see "Browser Session" below). An earlier revision
of this design split a public `tflive-web` browser client from a bearer-only
`tflive-api` audience client; both the second client and the client scope and
mapper it needed are gone along with it.

The client's one registered redirect URI is derived, never configured
separately:

```text
Redirect URIs:
  <TFLIVE_PUBLIC_URL>/v1/auth/callback
```

`WebOrigins` is empty and stays empty: the browser only ever calls the API's
own origin, so no CORS configuration exists anywhere in `internal/api`.

The API verifies **ID tokens**, not access tokens, and checks `aud` against
`OIDC_CLIENT_ID`. An ID token's `aud` is the client ID by construction, which
is the whole reason ID tokens replaced access tokens here: forcing a resource
identifier into an access token's `aud` used to require a dedicated client
scope and protocol mapper on Keycloak, and on Okta a custom authorization
server, which is a paid add-on. Verifying the ID token instead means the
audience is already correct and nothing needs to mint it.

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
`http://keycloak.localhost:8082/admin/tflive/console/` to find and manage tflive users
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
| `KEYCLOAK_API_CLIENT_ID` | No | The confidential client's ID; defaults to `tflive-api` |
| `TFLIVE_PUBLIC_URL` | No | Origin the browser reaches; the provisioner derives the client's redirect URI (`<TFLIVE_PUBLIC_URL>/v1/auth/callback`) and post-logout redirect URI from it |
| `OIDC_CLIENT_SECRET` | Yes | Secret registered on the confidential client; must match what the API is configured with |
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
| `VITE_TFLIVE_TENANT_ID` | No | Frontend build-time tenant context; must exactly match `TFLIVE_TENANT_ID`; local development falls back to `tenant_123` |
| `OIDC_ISSUER_URL` | No | Required exact Keycloak issuer URL |
| `OIDC_CLIENT_ID` | No | Required OAuth client ID; also the ID token audience the verifier checks against |
| `OIDC_CLIENT_SECRET` | Yes | Required; the API is a confidential client and authenticates as one when it exchanges a code |
| `TFLIVE_PUBLIC_URL` | No | Required; the origin the browser reaches. The API derives its own OIDC redirect URI (`<TFLIVE_PUBLIC_URL>/v1/auth/callback`) and post-logout redirect URI from it — never from `Host` or `X-Forwarded-Proto`, which an attacker can set |
| `SESSION_ENCRYPTION_KEY` | Yes | Required 32-byte key (raw, base64, or hex) that seals the short-lived login transaction cookie (`state`, `nonce`, PKCE verifier, `return_to`) |
| `TFLIVE_SESSION_ABSOLUTE_TTL` | No | Optional hard cap on a session from sign-in, never extended; defaults to `8h` |
| `TFLIVE_SESSION_IDLE_TTL` | No | Optional idle bound, sliding on activity; defaults to `1h`; must not exceed `TFLIVE_SESSION_ABSOLUTE_TTL` |
| `OPENFGA_API_URL` | No | Required OpenFGA API base URL |
| `OPENFGA_STORE_ID` | No | Required exact store ID emitted by bootstrap |
| `OPENFGA_MODEL_ID` | No | Required exact immutable model ID emitted by bootstrap |
| `OPENFGA_API_TOKEN` | Yes | Optional for local development and required in production |
| `OPENFGA_HTTP_TIMEOUT` | No | Positive per-request deadline; defaults to `10s` |

`TFLIVE_TENANT_ID` is the authoritative security boundary. Every authenticated
tenant-scoped route compares its `{tenant_id}` path value with that configured
tenant before decoding a body or accessing application services, repositories,
logs, artifacts, or authorization data. Missing, malformed, and mismatched
tenant paths return `404` without disclosing whether a referenced resource
exists.

The React application reads `VITE_TFLIVE_TENANT_ID` as non-editable build-time
context. Deployments must set it to the same value as `TFLIVE_TENANT_ID`; a
mismatch is safe but prevents tenant-scoped requests from succeeding. Changing
this value requires rebuilding and redeploying the frontend bundle.

Development permits the documented loopback HTTP issuer, local HTTP OpenFGA
endpoint, and tokenless OpenFGA service. Production must be selected explicitly
with `TFLIVE_ENVIRONMENT=production`; it requires HTTPS for both external
dependencies and a non-empty OpenFGA bearer token. Unknown modes, malformed
tenant IDs, unsafe URLs or identifiers, and non-positive timeouts stop startup.

The OpenFGA store and model IDs are never discovered at API startup. Copy the
exact assignments printed by the serialized bootstrap command into runtime
configuration before starting the API. Keycloak bootstrap passwords and
provisioner administrator tokens are not API runtime credentials.

`OIDC_AUDIENCE` is retired: it named a resource identifier that used to be
forced into an access token's `aud`, and that concept does not exist for the
ID token the API verifies now. Setting it is a hard startup error rather than
a silently ignored or aliased variable — this codebase carries no
compatibility shims, and a config file that still names the old variable
should fail loudly rather than start with a meaning it no longer has. Set
`OIDC_CLIENT_ID` instead.

## API Token Verification

The API accepts compact **ID tokens** — not access tokens, and not opaque
tokens — for the configured issuer and `OIDC_CLIENT_ID`. Signature, issuer,
audience, expiry (with a small acceptable clock skew), and subject checks
complete before identity data is returned. During the login callback the
handler additionally compares the token's `nonce` claim against the one it
sealed into the transaction cookie; PKCE plus a confidential client already
closes code injection, so this is defence in depth rather than the
load-bearing control.

Keycloak discovery and JWKS signing keys are cached. A new or replaced signing
key triggers one bounded refresh, so routine key rotation does not require an
API restart. A fresh cached key continues to work through a short Keycloak
outage. If the verifier cannot fetch required public keys, it fails closed and
exposes no token or provider-response detail.

AUTH-007 middleware owns HTTP status mapping and credential parsing.

## API Request Authentication

Every `/v1` route accepts a credential two ways: an `Authorization: Bearer
<id-token>` header, or the `tflive_session` cookie the browser was handed at
login. The middleware tries the header first and falls back to the cookie —
never the reverse — so a CLI or service-to-service caller presenting its own
token on a request that happens to also carry a browser cookie is never
silently overridden by that stale cookie. Both paths converge on the same
`authn.Principal`, but they no longer share one verification step: the Bearer
path still calls `Verifier.Verify` against the IdP's live signing keys, exactly
as before, while the cookie path looks up the session row by the cookie's
SHA-256 hash and checks `Session.IsLive` against tflive's own bounds — see
"Browser Session" below. The IdP is not consulted on the cookie path at all.

`/healthz` and the four `/v1/auth/*` routes below remain public. Missing,
malformed, invalid, or temporarily unverifiable credentials receive `401`
with the stable JSON body `{"code":"unauthorized"}`; tokens, claims, and
verifier details are never written to logs or responses.

After verification, the request context contains an `authn.Principal` with
the immutable subject and safe display claims — `Name`, `PreferredUsername`,
`Email`, and `ExpiresAt` (the ID token's `exp` on the Bearer path, tflive's own
idle/absolute bound on the cookie path). It carries no role claim: OpenFGA is
the sole authorization source, so nothing from the token feeds an access
decision. Handlers and application services obtain it with
`authn.PrincipalFromContext` rather than parsing HTTP headers or tokens.

## Browser Session

The browser never speaks OIDC. Three API routes run the entire
authorization-code flow on its behalf, plus one more the IdP calls directly:

| Route | Purpose |
|---|---|
| `GET /v1/auth/login` | Starts the flow: generates `state`, `nonce`, and a PKCE verifier, seals them into the transaction cookie, and redirects to the IdP |
| `GET /v1/auth/callback` | Redeems the code on the back channel, verifies the resulting ID token, creates a session row, and hands the browser the session cookie |
| `POST /v1/auth/logout` | Revokes the session row, clears the session cookie, and redirects to the IdP's RP-initiated logout |
| `POST /v1/auth/backchannel-logout` | Unauthenticated; ends sessions on the IdP's own instruction — see "Back-Channel Logout" below |

Two cookies carry the interactive flow:

| | `tflive_session` | `tflive_auth_tx` |
|---|---|---|
| Contents | an opaque 43-character reference to a session row — not a token | sealed `{state, nonce, code_verifier, return_to}` |
| Path | `/` | `/v1/auth` |
| Max-Age | none (session cookie) | 600s |
| `HttpOnly` | yes | yes |
| `SameSite` | `Lax` | `Lax` |
| `Secure` | production only | production only |

Both are `SameSite=Lax`, not `Strict`. The IdP's callback to
`/v1/auth/callback` is a cross-site top-level GET — the browser is navigating
back from `keycloak.localhost`, not from tflive's own origin — and `Strict`
would withhold the transaction cookie on exactly that request, breaking every
login. It would look like a random state-mismatch failure rather than an
obviously misconfigured cookie.

`Lax` still stops CSRF on every mutating route in this API, because `Lax`
withholds the cookie from a cross-site request unless it is a top-level GET
navigation. A forged cross-site form or script can `POST`, `PATCH`, or
`DELETE` all it wants; the browser will not attach `tflive_session` to any of
it, so the request arrives unauthenticated. This holds only because every
mutating route in the API is `POST`, `PATCH`, or `DELETE` — a mutating `GET`
would defeat it, so there is not one, and there is no separate CSRF token.

`tflive_session` needs no encryption: it carries no claims to protect, only 32
bytes of CSPRNG output rendered as base64. The database never stores that
value, only its SHA-256 hash (`id_hash`), so a leaked row of the `sessions`
table yields no usable cookie, and a tampered cookie value simply hashes to no
row — indistinguishable from an expired session. The transaction cookie
**is** sealed with AEAD (`SESSION_ENCRYPTION_KEY`), because `state` is only
meaningful if the browser cannot forge it — an attacker who can set a cookie
can set a query parameter too, and unsealed state would let login-CSRF back
in.

### Session Lifetime

A session is tflive's own record, not the IdP's. Before this design the
session cookie held the raw ID token, so how long a sign-in lasted was decided
by the provider's token lifespan and whether silent renewal was governed by
its SSO idle timeout. tflive is BYO-IdP and configures neither on a
deployment's provider, so a row in the `sessions` table
(`internal/postgres/migrations/0018_sessions.sql`) is a session tflive issues,
expires, and revokes on its own terms. `internal/authn.Session` is the Go
type; `internal/authn.SessionStore` is the persistence interface the cookie
path of `RequireAuthentication` depends on.

`handleAuthCallback` copies the verified ID token's claims onto the row once,
at sign-in — subject, name, preferred username, email, and the `sid` claim
when the provider sends one. Every later request authenticates against that
row; the ID token is never re-verified or re-read after the callback. That is
the whole point: session length becomes tflive's to choose instead of a
consequence of whatever access-token lifespan or SSO idle timeout a customer's
IdP happens to run.

Two independent bounds decide whether a session is live (`Session.IsLive`):

| Bound | Config | Default | Behavior |
|---|---|---|---|
| Absolute | `TFLIVE_SESSION_ABSOLUTE_TTL` | `8h` | Set once at sign-in and never extended — a hard cap from `CreatedAt`, not slid by activity |
| Idle | `TFLIVE_SESSION_IDLE_TTL` | `1h` | Slides on activity, but the row is written back at most once every 5 minutes (`SessionTouchInterval`), not on every request |

The session's effective expiry is the earlier of the two (`Session.ExpiresAt`),
which is what `/v1/me` reports as `sessionExpiresAt` so the SPA can
re-authenticate proactively rather than being surprised by a `401` mid-action.
`TFLIVE_SESSION_IDLE_TTL` must not exceed `TFLIVE_SESSION_ABSOLUTE_TTL` — the
API refuses to start otherwise, since an unreachable idle bound is a
configuration mistake, not a permissive setting. Revocation (`RevokedAt`) is
checked first in `IsLive` and is unconditional: a revoked session is dead
regardless of either bound, which is what lets back-channel logout end a
session immediately instead of waiting on a TTL.

Because claims are copied once, an IdP-side change — a renamed user, a
disabled account, a role change — is not observed by tflive until the session
ends. Without back-channel logout that staleness window is bounded by the 8h
absolute cap; with it, the window closes as soon as the notification arrives
(see below). That trade is deliberate: session length a BYO-IdP deployment
never has to negotiate with its provider, at the cost of display claims that
are a snapshot rather than live.

The row also keeps the raw ID token, encrypted at rest, solely so
`handleAuthLogout` can pass it to the IdP as `id_token_hint` during
RP-initiated logout — without it, Keycloak shows a logout confirmation page
instead of signing out silently. It is encrypted with
`CREDENTIAL_ENCRYPTION_KEY`, the same cipher that protects stored Terraform
credentials, not `SESSION_ENCRYPTION_KEY`, which seals only the transaction
cookie above. `CREDENTIAL_ENCRYPTION_KEY` is optional at API config load, but
sign-in now depends on it in practice: session creation fails if no credential
cipher is configured, so a deployment that never touches Terraform credentials
still needs this key set for login to work.

There is still no refresh token — that part of the design is unchanged.
Storing and rotating one was evaluated and rejected: correct handling needs a
transactional store with row locking to survive concurrent requests racing a
single-use refresh token, the new cookie has nowhere reliable to ride out on a
streaming log response, and `offline_access` means three different things
across Keycloak, Okta, and Google. The full reasoning, including the ArgoCD
comparison that shaped it, is in the [design
doc](superpowers/specs/2026-08-25-oidc-server-side-flow-design.md). What has
changed is what "expired" means: it is no longer the IdP's ID token `exp`, so
when a tflive session does end, the same silent-redirect trip through
`/v1/auth/login` picks up the user's still-live IdP SSO session and returns
with a fresh code — no password, no MFA prompt — exactly as before.

### Back-Channel Logout

`POST /v1/auth/backchannel-logout` is unauthenticated by necessity: it is
called by the IdP's own server, which holds no tflive cookie and no bearer
token. The credential is the logout token itself (OIDC Back-Channel Logout
1.0), verified against the same JWKS and issuer that verify ID tokens
(`OIDCVerifier.VerifyLogoutToken`). tflive checks signature, issuer, audience,
`iat` freshness (2-minute maximum age), the required
`http://schemas.openid.net/event/backchannel-logout` event, and rejects any
token carrying `nonce` — its presence would mean an ID token is being replayed
as a logout token, which would let anyone holding one revoke another user's
sessions.

A logout token identifies what to revoke by `sid` or `sub`, and tflive prefers
the narrower one: a `sid` match revokes one browser session
(`RevokeSessionsByIDPSessionID`); a `sub`-only match revokes every session for
that user (`RevokeSessionsBySubject`). The endpoint returns `200` whether or
not anything matched — whether tflive holds a session for a given `sid` is not
something an unauthenticated caller gets to learn.

A BYO-IdP deployment needs **no** session or timeout configuration on its
provider; the 8h/1h bounds above are entirely tflive's own. To get immediate
revocation instead of waiting on those bounds, point the provider's
back-channel logout at:

```text
<TFLIVE_PUBLIC_URL>/v1/auth/backchannel-logout
```

and enable session-required logout so the provider includes `sid` in both the
ID token and the logout token — without it, tflive can only match on `sub`,
so signing one device out signs out every session the user has. On Keycloak,
the provisioner (`internal/keycloak/provisioner.go`) sets this automatically
on the `tflive-api` client:

| Attribute | Value | Effect |
|---|---|---|
| `backchannel.logout.url` | `<TFLIVE_PUBLIC_URL>/v1/auth/backchannel-logout` | Where Keycloak posts the logout token |
| `backchannel.logout.session.required` | `true` | Includes `sid` in the ID token and the logout token |

A provider that never calls this endpoint is not a broken deployment: sessions
still end at their own absolute and idle bounds, exactly as if back-channel
logout did not exist. The endpoint only closes the gap between "the IdP
considers this session over" and "tflive does too."

Logout redirects rather than returning the IdP's logout URL in a JSON body.
That URL carries the raw ID token as `id_token_hint`, and a JSON body would
be readable by any script on the origin — handing an XSS exactly the
credential `HttpOnly` exists to hide, and one the authentication middleware
would accept as a bearer credential. A `Location` header on a `303` is not
script-readable. The route is `POST`, not `GET`, so a cross-site image tag
cannot trigger it — though `SameSite=Lax` would already make such a request
arrive without the session cookie regardless.

## Operation and Reruns

Start or reconcile the realm with:

```bash
docker compose --env-file .env up --build keycloak-provision
```

A successful run exits `0` once every resource is reconciled. Re-run
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

The model is authored in OpenFGA's DSL at `openfga/authorization-model.fga`,
which is what changes to permissions are written and reviewed in and the only
form of the model in the repository. `cmd/openfga-provisioner` embeds that file
and transforms it to the API wire format in process; see
[local development](development.md#the-authorization-model).

### Runtime Authorization Behavior

- Stack creation requires the authenticated user to have the Keycloak
  `stack-creator` or `platform-admin` realm role. After Postgres persists the
  stack, the API writes and higher-consistency-confirms an OpenFGA `owner`
  relationship for that user's immutable subject. An ownership-write failure
  returns `503 authorization_unavailable` after persistence. The initial owner
  intent is recorded atomically with the stack in `authorization_outbox`; the
  worker retries confirmed OpenFGA delivery until it succeeds. Operators can
  inspect pending rows using `attempts`, `available_at`, and the sanitized
  `last_error`. The outbox never stores tokens or OpenFGA request bodies.
- The API always sends the explicit configured OpenFGA store and immutable
  model IDs; it never discovers a latest model at runtime.
- Direct role writes and deletes can request higher-consistency confirmation. A
  completed negative higher-consistency confirmation returns
  `authorization_write_unconfirmed` and is retryable safely.
- A confirmation timeout, unavailable service, malformed response, or other
  confirmation dependency failure fails closed as `authorization_unavailable`.
  Explicit OpenFGA denial remains distinct; when an authorization decision is
  required, dependency failures map to `503 authorization_unavailable`.

### Endpoint Permission Matrix

The API enforces permissions in the application layer. Frontend button
visibility is never an authorization boundary.

| Route | Required permission |
|---|---|
| `POST /v1/tenants/{tenant_id}/template-revisions` | Global `platform-admin` or `stack-creator` |
| `GET /v1/tenants/{tenant_id}/template-revisions` | Global `platform-admin` or `stack-creator` |
| `GET /v1/tenants/{tenant_id}/template-registrations/{registration_id}` | Global `platform-admin` or `stack-creator` |
| `GET /v1/tenants/{tenant_id}/template-revisions/{template_revision_id}/variables` | Global `platform-admin` or `stack-creator` |
| `POST /v1/tenants/{tenant_id}/stacks` | Global `platform-admin` or `stack-creator` |
| `GET /v1/tenants/{tenant_id}/stacks` | Complete tenant stack scan with bounded OpenFGA `BatchCheck(can_view)` calls; `platform-admin` bypasses the ordinary stack list check |
| `GET /v1/tenants/{tenant_id}/stacks/{stack_id}` | `can_view` |
| `POST /v1/tenants/{tenant_id}/stacks/{stack_id}/templates` | `can_operate` |
| `PATCH /v1/tenants/{tenant_id}/stack-templates/{stack_template_id}/config` | Owning stack `can_operate` |
| `POST /v1/tenants/{tenant_id}/stack-templates/{stack_template_id}/upgrade` | Owning stack `can_operate` |
| `POST /v1/tenants/{tenant_id}/stack-templates/{stack_template_id}/runs` | Owning stack `can_operate` |
| `GET /v1/tenants/{tenant_id}/template-runs/{run_id}` | Owning stack `can_view` |
| `GET /v1/tenants/{tenant_id}/template-runs/{run_id}/logs` | Owning stack `can_view` |
| `GET /v1/tenants/{tenant_id}/template-runs/{run_id}/logs/{phase}` | Owning stack `can_view` |
| `POST /v1/tenants/{tenant_id}/template-runs/{run_id}/approval` | Owning stack `can_approve` |
| `POST /v1/tenants/{tenant_id}/template-runs/{run_id}/cancellation` | Owning stack `can_operate` |

`can_manage_access` has no current route; the access-management API will use it.
Stack templates, runs, logs, log artifacts, and credential identifiers returned
with a stack inherit the owning stack decision. Non-administrator stack lists
read tenant stacks from Postgres in stable `(created_at, id)` keyset pages of at
most 50 candidates and authorize each page through `BatchCheck(can_view)`. The
API returns only after every page succeeds. A timeout, malformed result, result
count mismatch, or later-page failure discards all earlier results and returns
`503 authorization_unavailable`; partial lists are never returned.

Missing and inaccessible inherited reads both return `404 not_found`. Missing
and inaccessible inherited mutation targets both return `403 forbidden`, so
stack-template and run identifiers cannot be enumerated by comparing statuses.
OpenFGA timeouts, unavailable or malformed responses, and a missing runtime
authorizer fail closed as `503 authorization_unavailable` and never produce an
allowed operation or an unfiltered list.

### Provisioning and Verification

`docker compose up` provisions OpenFGA as part of the infrastructure phase. The
`openfga-provision` one-shot runs `bootstrap`, which reuses a store whose name
already matches and reuses a semantically equal authorization model, creating
either only when absent, so repeated runs converge rather than duplicating.

Bootstrap prints the resolved identifiers to stdout. Copy both into `.env` before
starting the application phase, which refuses to start without them. The
identifiers are therefore always exact and pinned: nothing discovers a store by
name at runtime, and nothing resolves "the latest model".

```bash
docker compose up -d
docker compose logs openfga-provision
# Copy OPENFGA_STORE_ID and OPENFGA_MODEL_ID into .env.
docker compose run --rm openfga-provision verify
```

`verify` reads the exact pair from `.env` and checks it against the live store
and model without mutating either.

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
