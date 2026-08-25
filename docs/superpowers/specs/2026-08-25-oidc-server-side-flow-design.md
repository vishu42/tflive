# #216 — Server-side OIDC flow: design

Design for [#216](https://github.com/vishu42/tflive/issues/216), under
[Epic #210](https://github.com/vishu42/tflive/issues/210). Successor to
[the analysis](2026-08-22-oidc-server-side-flow-analysis.md), which parked #216 behind #145.

**The block is cleared.** #145 landed at `5dea447` ("make OpenFGA the sole authorization
source"). `authn.Principal` now carries identity only — `Subject`, `Name`,
`PreferredUsername`, `Email` — and nothing reads `realm_access.roles`
(`internal/authn/principal.go`). The regression that finding 1 of the analysis described
cannot occur, so the verifier can switch to ID tokens.

Everything the analysis settled is carried in without restating the reasoning: ID token not
access token, `SameSite=lax` and why `strict` breaks the callback, `lax` as sufficient CSRF
cover, the session cookie holding the IdP's ID token so #211 keeps its shape, and any new key
going through `internal/config`.

## Scope decisions the analysis left open

### There is no refresh — the IdP owns session lifetime

The analysis (finding 5) framed refresh as separable and assumed it would follow. It is
dropped instead, as a design decision rather than a deferral. **Session length equals the
ID token's lifetime, and re-authentication is a round trip through the IdP.**

This works because the user still holds an SSO session at the IdP. When our ID token expires
and the browser navigates to `/v1/auth/login`, Keycloak recognises its own session cookie and
redirects straight back with a fresh code — no form, no password, no MFA prompt. Three
redirects and a few hundred milliseconds. "Log in again" overstates it: the IdP already knows
who they are, and re-authenticating is us asking it to say so again.

That is the right division of responsibility for bring-your-own-IdP. The IdP owns how long a
person stays signed in; we own none of it and store nothing that says otherwise.

What this removes is the entire body of work in analysis finding 5 and everything it drags
behind it: no session table, no migration, no rotation, no `SELECT … FOR UPDATE`, no
revocation call on logout, no session-duration ceiling, no `offline_access`, and no Postgres
dependency in the authentication path. `scope` stays `openid profile email` and the token
response's `refresh_token` is never requested.

**Why refresh is disproportionately expensive**, recorded so this is not reopened casually:

- *Concurrent refresh is a destructive read.* Under rotation the token is single-use, and
  most IdPs treat reuse as theft and invalidate the whole token family. Three parallel
  React Query calls arriving on an expired token means one refresh succeeds and the rest
  poison the session — hard-logging the user out, remotely and irreversibly. Correct
  handling needs a transactional store with row locking; an in-memory mutex fails the moment
  there are two API replicas.
- *The new cookie has to ride out on some request's response.* Refresh happens inside the
  middleware, so `Set-Cookie` lands on whatever call happened to trigger it. Our log
  endpoints stream, and headers cannot be set after bytes are flushed.
- *`offline_access` means three different things.* Okta requires it; Keycloak turns it into
  an offline session that deliberately outlives logout; Google ignores it and wants
  `access_type=offline` with `prompt=consent`, issuing a refresh token only on first consent.
  This is #216's own portability problem reappearing one layer down.
- *Logout stops being cheap.* A 30-day credential server-side has to be revoked at the IdP
  and deleted, with a decision about what to do when that call fails.
- *Testing it needs a fake IdP that models single-use tokens and family invalidation*, or the
  first bullet ships green.

ArgoCD, our reference for the rest of this design, does implement refresh — and it needs
Redis (`util/oidc/oidc.go`, `SetValueInEncryptedCache` keyed by `sub`+`sid`), an interceptor
that mutates the response of unrelated requests via a `renew-token` header
(`server/server.go:1576`), and a cookie splitter across up to 20 cookies
(`util/http/http.go`, `maxCookieLength = 4093`). It still builds a fresh
`oauth2.ReuseTokenSourceWithExpiry` per call, so parallel requests are not deduplicated, and
its rotation write-back is compare-then-write with no CAS. That is a mature project's
version of this feature, and it carries two unsynchronised races.

#### The cost, and the two things that pay it down

The cost is that expiry is a full page navigation: in-memory React state remounts. `return_to`
preserves the route and React Query refetches server state, so for this app it is close to a
flicker — but a dirty form is lost, and an in-flight `POST` that 401s cannot be replayed.

**`/v1/me` returns `sessionExpiresAt`**, and the SPA re-authenticates *proactively* — shortly
before expiry, when nothing is in flight and no form is dirty — rather than waiting to be
surprised by a 401. One field and a timer converts a random mid-action interruption into a
scheduled one. The reactive 401 path stays as the backstop.

**The fixture's `AccessTokenLifespan` rises from 300 to 3600.** Five minutes is unusable; the
eight hours an earlier draft proposed is worse in a different way, because a re-auth path
that fires twice a day is a path nobody notices breaking. An hour keeps it exercised in normal
use and matches what most IdPs default to.

We inherit whatever the IdP is configured for and cannot override it — Google is one hour and
fixed, Entra roughly an hour, Okta and Auth0 configurable, Keycloak five minutes by default
but self-hosted and adjustable. An operator who pins five minutes gets a poor experience and
that is their lever to move, not ours. If a real deployment ever proves that untenable,
refresh becomes a new issue with this document as the record of what it costs.

### Logout ends the IdP session, and ships inside #216

Analysis finding 2: clearing only our cookie means "log out, log in" silently returns the
same user. That is the demo path and a real problem on a shared browser. RP-initiated logout
costs one discovery field and one redirect, so it stays in scope. Providers without an
`end_session_endpoint` degrade to clearing our cookie only.

## Architecture

Three new routes, one changed middleware, one widened discovery document, one new sealed
cookie, and a large deletion in `web/`.

```
browser                    API                          IdP
  |  GET /v1/auth/login      |                            |
  |------------------------->|  seal {state,nonce,        |
  |                          |   verifier,return_to}      |
  |<-- 302 + tx cookie ------|                            |
  |------------------ authorization_endpoint ------------>|
  |<----------------- 302 ?code=&state= ------------------|
  |  GET /v1/auth/callback   |                            |
  |------------------------->|  unseal tx, match state    |
  |                          |--- POST token_endpoint --->|
  |                          |<-- id_token ---------------|
  |                          |  verify, match nonce       |
  |<-- 302 return_to --------|                            |
  |    + session cookie      |                            |
  |    - tx cookie           |                            |
```

### Discovery grows from two fields to five

`discoveryDocument` (`internal/authn/oidc_provider.go`) parses `issuer` and `jwks_uri` today.
It gains `authorization_endpoint`, `token_endpoint`, and `end_session_endpoint`.

The flow reads these **through the verifier**, not from a second fetch. `OIDCVerifier`
already owns discovery, its TTL, its refresh path, and the hardened HTTP client with the
response-size cap and redirect block. A second discovery client would duplicate all of it and
could disagree with the first. So `OIDCVerifier` exposes:

```go
type Endpoints struct {
    Authorization string
    Token         string
    EndSession    string // empty when the provider does not advertise one
}

func (v *OIDCVerifier) Endpoints() Endpoints
```

`authorization_endpoint` and `token_endpoint` are validated at construction with the existing
`validProviderURL` guard, exactly as `jwks_uri` is; a discovery document missing either fails
construction with `ErrVerifierUnavailable`. `end_session_endpoint` is optional and validated
only when present.

### `internal/authn/flow.go` — the OIDC client mechanics

One type, two methods, no HTTP handling:

```go
type Flow struct { /* client id, secret, redirect URI, scopes, endpoints source, http client */ }

func (f *Flow) AuthorizationURL(state, nonce, codeChallenge string) (string, error)
func (f *Flow) Exchange(ctx context.Context, code, codeVerifier string) (rawIDToken string, err error)
```

`Exchange` posts to the token endpoint with `client_secret_basic`, reads `id_token` out of
the response, and returns it raw. It does not verify — verification is the existing
`Verifier.Verify`, called by the handler. Keeping exchange and verification separate means
the handler's happy path uses the same verifier the middleware uses, so there is one place
where a token becomes a principal.

`golang.org/x/oauth2` carries the grant mechanics, per the settled
[library boundary](2026-08-20-iam-openfga-surface-analysis.md): our discovery, our verifier,
`x/oauth2` for grants only. It is a new direct dependency; it is not currently in `go.mod`.

### Nonce

`VerifiedToken` gains a `Nonce` field, populated from the `nonce` claim through the existing
`optionalStringClaim` path. The middleware ignores it. The callback handler compares it to
the nonce it sealed into the transaction cookie and rejects a mismatch.

PKCE plus a confidential client already closes code injection, so nonce is defence in depth
rather than the load-bearing control — but it is five lines and several providers expect it.

### `internal/authn/session.go` — two cookies

| | `tflive_session` | `tflive_auth_tx` |
|---|---|---|
| Contents | the IdP's raw ID token | sealed `{state, nonce, code_verifier, return_to}` |
| Path | `/` | `/v1/auth` |
| Max-Age | none (session cookie) | 600s |
| `HttpOnly` | yes | yes |
| `SameSite` | `Lax` | `Lax` |
| `Secure` | production only | production only |

The session cookie is **not** encrypted. It is a signed JWT: tampering is caught by
`Verify`, and its contents are the user's own claims. This is ArgoCD's choice and it keeps
#211's `iss`-routing intact.

The transaction cookie **is** sealed with AEAD. `state` is only meaningful if the browser
cannot forge it — an attacker who can set the cookie can set the query parameter too, and
login-CSRF follows. AEAD makes the cookie unforgeable and covers `code_verifier` and
`return_to` in the same envelope.

`SameSite=Lax` on the transaction cookie is required, not preferred: the IdP's callback is a
cross-site top-level GET, and `Strict` would withhold the cookie and break every login.

### Where the sealing key comes from

`internal/credentials.Cipher` is the AES-GCM implementation we already have and it is
correct. `internal/authn` must not import `internal/credentials` — Terraform credential
storage is not an identity concern.

**Move the cipher to `internal/secrets`**, which exists today as a `doc.go` describing
exactly this boundary ("secret store boundaries and implementations") and holds nothing else.
`internal/credentials` and `internal/authn` both use it. Call sites to update:
`internal/postgres/store.go` and the `credentials` package itself.

While there, close analysis finding 7: `CREDENTIAL_ENCRYPTION_KEY` is read by a bare
`os.Getenv` in `internal/postgres/store.go:35`, bypassing `internal/config` and its
validation. Both keys route through `internal/config`. Adding a validated
`SESSION_ENCRYPTION_KEY` next to an unvalidated `CREDENTIAL_ENCRYPTION_KEY` would leave the
inconsistency looking deliberate.

This step is separable from the rest of the plan and can be dropped without affecting the
flow, at the cost of duplicating the AES-GCM boilerplate inside `internal/authn`.

### The API needs to know its own public origin

The API sits behind Vite in dev and nginx in prod and cannot see the browser's origin.
Deriving it from `X-Forwarded-Proto` and `Host` makes the redirect URI attacker-influenced.

New required config: **`TFLIVE_PUBLIC_URL`** (`http://localhost:5173` locally). The redirect
URI is `${TFLIVE_PUBLIC_URL}/v1/auth/callback` and the post-logout redirect is
`${TFLIVE_PUBLIC_URL}/`. Both are computed, never configured separately, so they cannot drift
from what is registered with the IdP.

### `return_to` and open redirect

`GET /v1/auth/login?return_to=/stacks/abc` preserves deep links. The value is accepted only
when it begins with a single `/` and not `//`, has no scheme and no host. Anything else falls
back to `/`. It is sealed into the transaction cookie rather than carried through the IdP
round trip, so the IdP never sees it and it cannot be rewritten in flight.

## Routes

All three are added to the public path list in `NewAuthenticatedServer`
(`internal/api/server.go`), alongside `/healthz`. They are not tenant-scoped, so they go on
the mux directly rather than through `handleTenantRoute`.

**`GET /v1/auth/login`** — generates `state`, `nonce`, and a PKCE verifier from
`crypto/rand`; seals them with the validated `return_to`; sets the transaction cookie; 302s
to `AuthorizationURL`. Scope is `openid profile email`, unchanged — `offline_access` is
deliberately absent, because no refresh token is wanted.

**`GET /v1/auth/callback`** — reads and unseals the transaction cookie, compares `state`
against the query parameter in constant time, exchanges the code, verifies the ID token via
`Verifier.Verify`, compares `nonce`, sets the session cookie, clears the transaction cookie,
302s to `return_to`. An `error` parameter from the IdP, an absent or expired transaction
cookie, a state mismatch, a failed exchange, and a failed verification all render the same
HTML error page with no detail — the browser is the wrong place to explain why authentication
failed, and the distinction is an oracle.

**`POST /v1/auth/logout`** — clears the session cookie and returns
`{"logoutURL": "..."}` when the provider advertises an `end_session_endpoint`, built with
`id_token_hint` and `post_logout_redirect_uri`; `{"logoutURL": null}` otherwise. The SPA
navigates to it, or to `/v1/auth/login` when null. POST rather than GET so a cross-site image
tag cannot log the user out; `SameSite=Lax` withholds the cookie from cross-site POST anyway,
so the request would be a no-op regardless.

**`GET /v1/me`** — unchanged in purpose, one field added. `MeResponse`
(`internal/auth/me.go`) gains `sessionExpiresAt`, an RFC 3339 timestamp taken from the
verified token's `exp`. It is the only thing the SPA needs in order to re-authenticate before
it is interrupted, and it is not a secret: it is a claim out of a token the browser already
holds.

This means `VerifiedToken` carries `ExpiresAt` alongside `Nonce`, and `Principal` carries it
through to the handler. `exp` is already required and validated by `validatedToken`, so this
reads a value the verifier has in hand rather than parsing anything new.

## Middleware

`RequireAuthentication` (`internal/authn/middleware.go`) tries `Authorization: Bearer` first,
then the `tflive_session` cookie. Header first keeps a future CLI working and keeps the
existing precedence explicit rather than accidental. Both paths feed the same
`verifier.Verify` and produce the same `Principal`; there is no second code path for
cookie-derived identity.

`/v1/` requests without either credential still return the existing
`401 {"code":"unauthorized"}`. The SPA turns that into a navigation to
`/v1/auth/login`; the API does not redirect XHR itself.

## Configuration

| Variable | Change |
|---|---|
| `OIDC_AUDIENCE` | **retired**, replaced by `OIDC_CLIENT_ID` |
| `OIDC_CLIENT_ID` | new, required — the audience of the ID token |
| `OIDC_CLIENT_SECRET` | new, required — the API is a confidential client |
| `TFLIVE_PUBLIC_URL` | new, required — origin the browser reaches |
| `SESSION_ENCRYPTION_KEY` | new, required — 32 bytes, seals the transaction cookie |
| `CREDENTIAL_ENCRYPTION_KEY` | unchanged value, now read through `internal/config` |
| `VITE_OIDC_ISSUER`, `VITE_OIDC_CLIENT_ID`, `VITE_OIDC_REDIRECT_URI` | **deleted** — the browser no longer speaks OIDC |
| `KEYCLOAK_WEB_REDIRECT_URIS`, `KEYCLOAK_WEB_ORIGINS` | **deleted** — derived from `TFLIVE_PUBLIC_URL` |

`OIDC_AUDIENCE` is renamed rather than redefined. tflive is pre-production with disposable
state, so no compatibility shim: a config file carrying the old name fails to start, which is
the correct outcome when its meaning has changed.

`OIDC_CLIENT_SECRET` and `SESSION_ENCRYPTION_KEY` are `config.Secret`, so they redact in the
`String()`/`GoString()` output that `SecurityConfig` already implements.

## Keycloak fixture

Two clients collapse to one. This shrinks #197.

- **`tflive-web` is deleted.** With it go the public client, PKCE attributes, web origins,
  and post-logout redirect attributes.
- **`tflive-api` becomes the confidential browser client**: `BearerOnly: false`,
  `PublicClient: false`, `StandardFlowEnabled: true`, a secret, and
  `RedirectURIs: ["${TFLIVE_PUBLIC_URL}/v1/auth/callback"]`. `WebOrigins` stays empty — no
  CORS is needed and none is configured anywhere in `internal/api`.
- **The `tflive-api-audience` client scope and its audience mapper are deleted.** They exist
  to force a resource identifier into an access token's `aud`. An ID token's `aud` is the
  client ID by construction — which is the entire point of #216.
- **The `roles` client scope link is deleted.** Nothing has read realm roles since #145.
- **`ExampleAccessToken` is deleted** from the provisioner and its backend interface. It
  asserted `aud` contains `tflive-api`, which is now true by definition and no longer worth a
  round trip.
- **`AccessTokenLifespan` rises from 300 to 3600**, with a comment naming it the session
  length and pointing at the no-refresh decision above.
- `Config.WebClientID` and `Result.WebClientID` are removed; `Config.RedirectURIs` and
  `Config.WebOrigins` are replaced by the derived callback URI.

## Web

Net deletion. `oidc-client-ts` leaves `package.json`.

- **Deleted:** `web/src/auth/oidcConfig.ts`, `userManager.ts`, `userManager.test.ts`,
  `CallbackPage.tsx`, `CallbackPage.test.tsx`, and the `auth/callback` route in
  `web/src/app/router.tsx`.
- **`OidcAuthProvider.tsx` → `SessionProvider.tsx`**: the callback branch, the silent-renew
  branch, the `getUser`/`expired`/`refresh_token` ladder, and the StrictMode double-invoke
  guard all go. What remains is `useMeQuery`, a 401 that navigates to `/v1/auth/login`, and
  the existing error state. `AuthContext`'s shape is unchanged, so consumers do not move.
- **Proactive re-authentication** lives in `SessionProvider`. It reads `sessionExpiresAt` off
  the `/v1/me` response and schedules a navigation to
  `/v1/auth/login?return_to=<current path and query>` sixty seconds before expiry. Two guards
  keep it from interrupting work: it defers while any React Query mutation is in flight
  (`useIsMutating()`), and it defers while the page reports unsaved input. Deferral re-checks
  on a short interval rather than cancelling, so a long-running mutation delays re-auth
  instead of skipping it. `Me` in `web/src/auth/types.ts` gains the field.
- **`client.ts`**: `authHeaders()` and the 401 silent-renew retry ladder are deleted. Requests
  carry `credentials: "same-origin"`. A 401 navigates to
  `/v1/auth/login?return_to=<current path and query>`.
- **`logout()`** posts to `/v1/auth/logout` and navigates to the returned `logoutURL`, or to
  `/v1/auth/login` when it is null.
- **A timer is not a security control.** Nothing in the SPA enforces expiry; the API rejects
  an expired token regardless of what the browser believes. The schedule exists purely to
  pick a convenient moment, so clock skew or a suspended laptop degrades to the reactive 401
  path rather than to unauthorised access.
- **Rewritten tests:** `AuthContext.test.tsx`, `OidcAuthProvider.test.tsx` →
  `SessionProvider.test.tsx`. The `web/src/auth/__mocks__/OidcAuthProvider.tsx` mock, which
  exists so `StyleGuide` can mount outside Keycloak, is renamed alongside it.

## Testing

**`internal/authn` (unit).** Authorization URL shape and PKCE challenge derivation. Seal and
unseal round trip; a tampered transaction cookie fails to open. `return_to` validation table:
`/stacks`, `//evil.test`, `https://evil.test`, `..`, empty. State mismatch, nonce mismatch,
expired transaction cookie, IdP `error` parameter. Middleware: header only, cookie only, both
present with the header winning, neither, malformed cookie value.

**`internal/api` (handler).** The three routes against a stub IdP: a full login round trip
setting the session cookie and landing on `return_to`; a callback with no transaction cookie;
logout with and without an `end_session_endpoint`. `server_test.go` is already the pattern
for this and is 127KB — the auth-route tests go in a new `server_auth_test.go` rather than
growing it further.

**`internal/authn/oidc_provider_test.go`.** Discovery documents missing
`authorization_endpoint` or `token_endpoint` fail construction; a document without
`end_session_endpoint` succeeds and reports an empty `EndSession`.

**`internal/keycloak`.** The existing fake backend asserts the provisioned client set; it
updates to one confidential client with the callback redirect URI, and to the absence of the
audience scope and mapper.

**`web`.** `SessionProvider` renders children on a 200 `/v1/me` and navigates to
`/v1/auth/login` on a 401. `client.ts` navigates on 401 rather than retrying. Proactive
re-auth with fake timers: it fires at `sessionExpiresAt` minus sixty seconds; it defers while
a mutation is in flight and fires once that settles; a `sessionExpiresAt` already in the past
navigates immediately rather than scheduling a negative timeout.

**Manual.** `docker compose up`, log in through Keycloak, confirm `tflive_session` is
httpOnly in devtools and that no token is reachable from `document.cookie` or any JS store.
Drop `AccessTokenLifespan` to 60 temporarily and confirm the warm re-auth round trip is
silent — no login form, route preserved, React Query state refetched.
Log out, log in again, and confirm the IdP prompts rather than silently returning the same
user — that is the check finding 2 exists for.

## Known limits, recorded deliberately

- **Session length is the IdP's ID token lifetime**, and expiry costs a full page
  navigation. This is a decision, not a gap — see the scope section. A dirty form does not
  survive it, which is what proactive re-authentication exists to make rare.
- **Cookie size.** A Keycloak ID token is roughly 1–1.5 KB and fits the 4 KB cookie limit
  comfortably. A provider that stuffs group or role claims into the ID token could exceed it,
  and the failure mode is a silently dropped cookie. Worth a size check that logs when the
  token exceeds 3 KB.
- **CSRF rests on `SameSite=Lax`.** Correct while every mutating route is POST, PATCH, or
  DELETE, which is true today. A mutating GET would break it. No double-submit token; the
  reasoning is analysis finding 4.
- **No server-side revocation.** Clearing the cookie ends the browser session, but a copied
  cookie stays valid until the ID token expires. Same property the SPA's bearer token had, so
  #216 does not regress it — and with no refresh token in play, the exposure stays bounded by
  the ID token's lifetime rather than extending to weeks.

## Documentation

`docs/authentication.md` sections "OIDC Clients and Claims", "API Access-Token Verification",
"API Request Authentication", and "Keycloak Provisioner Configuration" all describe the
browser-as-client model and are rewritten. "API Runtime Security Configuration" gains the
four new variables. `README.md` and `.env.example` follow.

## Knock-on, confirmed against the issue

- **#195** — already closed; the `typ: Bearer` check is gone.
- **#199** — shrinks but does not disappear. The browser still resolves the authorization
  endpoint, so `keycloak.localhost:8082` is still a name both the container and the host must
  resolve. Only the token and JWKS endpoints become API-only.
- **#211 / #213** — converge on `tflive_session` rather than returning a token to the SPA.
- **#197** — shrinks by one client, one client scope, one protocol mapper, and the
  `ExampleAccessToken` verification.
- **#198** — unaffected in shape; the claim contract is now read from the ID token.
