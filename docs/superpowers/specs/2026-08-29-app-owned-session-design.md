# App-Owned Session — Design

**Status:** proposed
**Supersedes part of:** `2026-08-25-oidc-server-side-flow-design.md` (session representation only;
the authorization-code flow, PKCE, transaction sealing, and confidential-client posture all stand)

## Problem

`tflive_session` holds the raw ID token (`internal/authn/session.go:17`). The session *is* the
token, so two numbers tflive does not own decide how the product behaves:

- **The IdP's token lifespan** sets how long a session lasts.
- **The IdP's SSO idle timeout** decides whether renewal at expiry is silent or a password prompt.

On the Keycloak the provisioner creates, `accessTokenLifespan` is set to 3600s. On a customer's IdP
it is set to nothing, because tflive does not provision a customer's IdP. Measured on the running
local stack, Keycloak's own `master` realm ships `accessTokenLifespan=60` against the `3600` written
into `tflive` — a 60× spread between two realms on one server. Across vendors: Okta ~60 min, Entra
60–90 min and policy-driven, Auth0 different again.

tflive is BYO-IdP. Asking an operator to change `ssoSessionIdleTimeout` so our app's sessions last a
sensible time is asking them to reconfigure their identity provider to accommodate us. That is not a
request we get to make, and it does not scale past the IdPs we happen to have documented.

### This is not a theoretical risk

`2026-08-25-oidc-server-side-flow-design.md` took ArgoCD as its reference and adopted
ID-token-as-session partly on the strength of that precedent. ArgoCD's issue tracker is the
counter-evidence:

- [#12189](https://github.com/argoproj/argo-cd/issues/12189) — logged out of the UI every 5 minutes
  on Keycloak, which is the access-token lifespan.
- [#18940](https://github.com/argoproj/argo-cd/discussions/18940) — `users.session.duration` set to
  15m has no effect on an OIDC session, because that setting governs only ArgoCD's own signed
  tokens. `util/session/sessionmanager.go` delegates SSO expiry entirely to `prov.Verify()`; there
  is no app-side duration that reaches it.
- [#24807](https://github.com/argoproj/argo-cd/issues/24807) — an expired token in a background tab
  produces ~200 req/min of 401s with nothing recovering it.
- [#26764](https://github.com/argoproj/argo-cd/issues/26764) — an open request for OIDC
  Back-Channel Logout so the IdP can invalidate "the corresponding **server-side session**."

ArgoCD's mitigation is refresh tokens: `CheckAndRefreshToken()` and
`GetUpdatedOidcTokenFromCache()` in `util/oidc/oidc.go`, storing via `SetValueInEncryptedCache()`
and requesting `offline_access` when advertised. That cache carries the comment *"if we ever have
replicas of Argo CD, this needs to switch to Redis cache"* — it is in-memory, so a restart or a
second replica loses it, and the flow falls back to a full authorization-code round trip.

The previous design was right to price that machinery as expensive and refuse it. What it missed is
that refusing refresh while keeping ID-token-as-session leaves the coupling in place with no
mitigation at all. #26764 shows ArgoCD converging on a server-side session from the other direction,
years in.

## Approach

Treat the OIDC round trip as an **authentication event**, not as the session.

The IdP answers "who is this, right now." tflive records that answer as a session it owns, with an
expiry it chooses, and hands the browser an opaque reference to it. IdP token lifetimes stop
affecting how long anyone stays signed in, because we stop borrowing the IdP's clock.

The ID token keeps every job it has today except one: it no longer *is* the session.

### Why server-side records rather than a sealed stateless cookie

A sealed cookie carrying claims would decouple lifetime just as well and needs no database. It
cannot be revoked. Back-channel logout exists precisely to revoke, and revoking a stateless bearer
requires server-side state anyway — a revocation list is a session table with the useful columns
removed.

So: one row per session in Postgres, cookie holds an opaque reference. This also has three effects
worth having on their own.

1. **The 4 KB cookie problem disappears at the root.** `warnOnOversizedSession` exists because an
   IdP that stuffs group claims into the ID token silently breaks sign-in
   (`internal/api/auth.go`). An opaque 32-byte reference cannot approach that limit whatever the
   provider emits. The loop guard added in `e837c0a` stays as defence in depth; the cause it guards
   against stops occurring.
2. **No Redis.** tflive already runs Postgres. ArgoCD's scaling note does not apply to a table.
3. **Logout becomes real.** Today clearing the cookie ends the browser session but a copied cookie
   stays valid until the ID token expires — a known accepted limitation of the previous design.
   A revoked row is revoked for every copy.

### Session identifier

The cookie carries 32 bytes from `crypto/rand`, base64url-encoded. The table stores its SHA-256
hash, never the value itself. A database leak then yields no usable session cookies, the same
reasoning applied to password-reset tokens.

Lookup is by hash on a unique index.

### Lifetime policy

Two bounds, both tflive's to choose:

| Bound | Default | Meaning |
|---|---|---|
| Absolute | 8h | Hard cap from sign-in. Not extendable. Re-authentication required past it. |
| Idle | 1h | Expires this long after the last request the session made. |

`TFLIVE_SESSION_ABSOLUTE_TTL` and `TFLIVE_SESSION_IDLE_TTL` override them. The defaults are a
workday and a lunch break: long enough that nobody is interrupted mid-task, short enough that an
unattended browser does not stay authenticated overnight.

Sliding the idle bound on every request would mean a write per request. `last_seen_at` is updated
only when it is more than `sessionTouchInterval` (5 min) stale, so a busy session writes at most
once per 5 minutes and the idle bound is accurate to within that.

### What the session stores, and why the ID token is kept

| Column | Reason |
|---|---|
| `subject`, `name`, `preferred_username`, `email` | Everything `MeResponse` reads. The claims are copied at sign-in; the token is not re-read per request. |
| `idp_session_id` | The ID token's `sid`, the key back-channel logout arrives on. |
| `id_token_ciphertext` | `id_token_hint` for RP-initiated logout, encrypted with `secrets.Cipher`. |

Keeping the ID token server-side is a deliberate choice with an alternative. RP-initiated logout's
`id_token_hint` is RECOMMENDED, not REQUIRED, and `EndSessionURL` already sends `client_id`; we
could store nothing and drop the hint. Keycloak then shows a logout **confirmation page** instead of
logging out directly, which is a visible regression for every user on every logout.

Storing it encrypted is strictly better than the status quo, where the same token sits unencrypted
in a cookie on the user's disk. Taking the UX and the improvement over a purity argument.

### Claims are copied, not re-verified

After sign-in, requests authenticate against the session row. The ID token is not re-verified per
request, so a change at the IdP — a renamed user, a disabled account — is not observed until the
session ends or back-channel logout arrives.

This is the trade being made, and it is bounded on purpose: 8h absolute worst case, immediate when
the IdP supports back-channel logout. The status quo has the same staleness bounded by token
lifetime and *no* revocation path at all, so this is not a regression in either dimension.

### Back-channel logout

`POST /v1/auth/backchannel-logout`, unauthenticated (the IdP has no session), accepting a
`logout_token` per OIDC Back-Channel Logout 1.0 §2.4:

- signature verified against the same JWKS the verifier already maintains
- `iss` matches the discovered issuer; `aud` contains our client ID
- `iat` within `clockSkew` of now
- `events` contains `http://schemas.openid.net/event/backchannel-logout`
- at least one of `sub` / `sid` present
- `nonce` **absent** — §2.4 forbids it, and its presence means an ID token is being replayed as a
  logout token

Matching sessions are revoked: by `idp_session_id` when `sid` is present, otherwise every session
for that `subject`. Always `200` on a well-formed token, `400` otherwise; the response says nothing
about whether any session matched.

The Keycloak provisioner registers `backchannel.logout.url` and
`backchannel.logout.session.required=true` on the `tflive-api` client, the latter being what makes
Keycloak put `sid` in both the ID token and the logout token. For a BYO IdP this is one field in
their client configuration, documented — and unlike an idle-timeout change it is a tflive-specific
integration setting rather than a change to how their IdP treats every other application.

An IdP that does not support back-channel logout degrades to the absolute cap. Nothing breaks.

### The Bearer path is untouched

`credential()` in `internal/authn/middleware.go` accepts an `Authorization: Bearer` token before
falling back to the cookie, for CLI and service callers. That path continues to verify an IdP token
through `Verifier.Verify`. Only the cookie path changes.

Two credential kinds, one `Principal`. The middleware picks a resolver; everything downstream is
unchanged.

## What changes

| Area | Change |
|---|---|
| `internal/postgres/migrations/0018_sessions.sql` | new `sessions` table |
| `internal/authn/session_store.go` | new — record type, hashing, expiry evaluation |
| `internal/postgres/session_repository.go` | new — create, lookup by hash, touch, revoke by id / sid / subject |
| `internal/authn/session.go` | `SessionCookie` carries an opaque reference, not a JWT |
| `internal/authn/middleware.go` | cookie path resolves through the session store |
| `internal/authn/oidc_verifier.go` | `VerifiedToken` gains `SessionID` (the `sid` claim) |
| `internal/api/auth.go` | callback creates a session; logout revokes it; `warnOnOversizedSession` deleted |
| `internal/api/backchannel_logout.go` | new endpoint |
| `internal/auth/me.go` | `SessionExpiresAt` reports the session's expiry |
| `internal/keycloak/provisioner.go` | registers the back-channel logout URI |
| `web/src/auth/SessionProvider.tsx` | deferral deadline; expiry semantics |
| `docs/authentication.md`, `.env.example` | follow |

## Explicitly out of scope

- **Refresh tokens and `offline_access`.** Every objection in
  `2026-08-25-oidc-server-side-flow-design.md` still holds, and an app-owned session removes the
  reason to want them.
- **Front-channel logout.** Back-channel is the reliable one; front-channel depends on third-party
  iframe cookies that browsers increasingly block.
- **Session listing / "sign out everywhere" UI.** The store supports it. Building it is separate.
- **Caching session lookups.** One indexed primary-key read per request, against a database every
  request already touches. Revisit with a measurement, not a hunch.

## Verification

The change is not complete until, on the live compose stack:

1. Sign-in succeeds and `/v1/me` returns an identity.
2. `select` on `sessions` shows one row, `id_token_ciphertext` unreadable without the key.
3. Setting the realm's `accessTokenLifespan` to 60s and waiting 2 minutes leaves the session
   working — the point of the change, and the thing the current design fails.
4. Signing out from Keycloak's account console revokes the tflive session, observed as `revoked_at`
   set and the next request 401ing.
5. `TFLIVE_SESSION_IDLE_TTL=60s` expires an idle session at 60s regardless of token lifetime.
