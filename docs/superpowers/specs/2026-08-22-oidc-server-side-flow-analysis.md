# #216 — Server-side OIDC flow: analysis and sequencing decision

Pre-design analysis for [#216](https://github.com/vishu42/tflive/issues/216), under
[Epic #210](https://github.com/vishu42/tflive/issues/210). Every claim below was read out of
the tree at b11e592, not recalled.

**Outcome: #216 is parked behind #145.** The analysis set out to design #216 and instead found
a regression that moves it. The short version is in [Decision](#decision) at the end; the
findings that led there are worth keeping regardless of order, because they still describe
what #216 has to do when it comes up.

## What the issue already settles

Not re-litigated here: the API becomes the confidential client, it verifies the **ID token**
so `aud` is the client ID, the cookie is httpOnly + `SameSite=lax`, and the token is accepted
from cookie *or* `Authorization` header. The [library boundary](2026-08-20-iam-openfga-surface-analysis.md)
is settled too — our discovery, our verifier, `x/oauth2` for grant mechanics only.

## Verified starting state

| Fact | Where |
|---|---|
| Middleware is header-only; no cookie path, no login routes | `internal/authn/middleware.go:29` |
| One middleware guards everything, `/healthz` the only public path | `internal/api/server.go:125` |
| `discoveryDocument` parses 2 of the fields it fetches | `internal/authn/oidc_provider.go:33` |
| Verifier pins `discovery.Issuer == cfg.IssuerURL` | `internal/authn/oidc_provider.go:65` |
| `typ: Bearer` check already gone (#195 landed on this branch) | `internal/authn/oidc_verifier.go` |
| AES-GCM cipher exists and is reusable | `internal/credentials/crypto.go:22` |
| ...but its key is read via bare `os.Getenv`, outside `internal/config` | `internal/postgres/store.go:35` |
| Single origin both envs — no CORS anywhere in `internal/api` | `web/vite.config.ts`, `deploy/web/nginx.conf` |
| SPA holds the access token and attaches it per request | `web/src/api/client.ts:236` |

## Findings the issue does not mention

### 1. Moving to ID tokens drops every global role — a live break, not a risk

`validatedToken` sources `platform-admin` and `stack-creator` from `realm_access.roles`
(`oidc_verifier.go`, `realmRoles`), and `normalizedGlobalRoles` keeps only those two
(`principal.go:47`). Keycloak's realm-roles mapper writes `realm_access` to the **access**
token only by default, and the fixture provisions exactly one mapper — the audience mapper,
with `"id.token.claim": "false"` hardcoded (`internal/keycloak/provisioner.go:219`). No
roles mapper is provisioned at all.

So the moment the verifier reads an ID token, `realmRoles` returns empty, `RealmRoles` is
empty, and **every user loses `isPlatformAdmin` and `canCreateStack`** — silently, because an
absent `realm_access` is a valid token, not a malformed one.

The blast radius is worse than "roles are missing". Three paths return `ErrForbidden`
outright: stack creation (`internal/app/service.go:542`), the template catalog
(`internal/app/authorization.go:50`), and user search, which the grants UI depends on
(`internal/app/service.go:1188`). Stack-scoped checks are gentler — `authorizeStack` falls
through to an OpenFGA `Check` for non-admins — but the owner tuple is written as a
consequence of stack creation (`internal/app/service.go:571`), which is itself one of the
closed paths. On a fresh deployment there are no stacks, no tuples, and no way to create
either. The app is bricked, not degraded.

Three ways out were considered: provision a Keycloak roles mapper with
`id.token.claim: true` (smallest diff, but adds coupling in the quarter meant to shed it, and
no other IdP emits `realm_access`); read a configurable claim (portable, but invents contract
#198 is meant to settle); or land #145 first so nothing reads the claim at all. See
[Decision](#decision) — the third won.

### 2. `end_session_endpoint` makes it four new discovery fields, not two

The issue says `discoveryDocument` grows from two fields to four
(`authorization_endpoint`, `token_endpoint`). Logout is currently RP-initiated from the SPA
via `signoutRedirect` (`web/src/auth/OidcAuthProvider.tsx`). Preserving that server-side needs
`end_session_endpoint` as well. If logout only clears our cookie and leaves the IdP session
standing, "log out then log in" silently re-authenticates as the same user — which on a shared
browser is a real problem, and is also the demo path.

### 3. The flow needs somewhere to keep `state`, PKCE verifier, and return-to

Between `/v1/auth/login` and `/v1/auth/callback` the server holds three things it did not
hold before. A short-TTL encrypted cookie keeps this stateless and needs no table; a
server-side store is the alternative. Note this cookie **must** be `SameSite=lax` — the
callback arrives as a cross-site top-level GET from the IdP, and `strict` would withhold it
and break every login.

### 4. `SameSite=lax` may already be sufficient, and the reasoning is worth recording

`lax` withholds cookies from all cross-site POST/PATCH/DELETE, which is every mutating route
we have. Cross-site top-level **GET** navigations do carry it, but our GET routes do not
mutate. So lax alone plausibly closes CSRF here — the epic flags it as "new surface", and the
honest answer looks like "covered, for a reason worth writing down" rather than "needs a
token". Worth an explicit decision either way instead of reflexively adding double-submit.

### 5. Refresh is the biggest single chunk, and it is separable

Server-side encrypted refresh storage pulls in a migration, a session table, a key config
item, rotation, and revocation-on-logout. Everything else in #216 — login, callback, cookie,
cookie-or-header extraction, the SPA teardown — is independent of it. Splitting it out would
make #216 landable much sooner; the cost is that ID tokens are short-lived (~5 min on several
IdPs), so without refresh a session dies fast and the user gets bounced to the IdP. Given
this is pre-production with no users, that may be an acceptable interim; it is a scope call,
not a technical one.

### 6. Cookie contents interact with #211

ArgoCD stores the IdP's ID token as the cookie and routes on `iss` — which is exactly what
#211 assumes when it adds `iss: tflive` self-signed tokens. Minting our own session token
instead would collapse both paths into one, but it changes #211's shape. Following ArgoCD
keeps the epic's sequencing intact and is the default unless you want to revisit #211.

### 7. Config surface, and an existing inconsistency to either follow or fix

New: client secret, redirect URI. Changed: `OIDC_AUDIENCE` now means the client ID
(`internal/config/auth.go`). Any refresh encryption key is a third. Note
`CREDENTIAL_ENCRYPTION_KEY` is read by bare `os.Getenv` in `internal/postgres/store.go:35`,
bypassing `internal/config` and its validation entirely — a new key config item should go
through `internal/config`, which means either leaving that inconsistency in place or fixing
it while we are here.

### 8. Fixture and dev loop

Two Keycloak clients collapse to one confidential client — `tflive-api` is currently
`BearerOnly: true, StandardFlowEnabled: false` (`provisioner.go:160`) and must gain the
standard flow and a secret; `tflive-web` goes away. Redirect URI becomes
`http://localhost:5173/v1/auth/callback`, which resolves through the Vite proxy in dev and
nginx in prod with no new routing. This shrinks #197.

### 9. Web teardown

`oidc-client-ts`, `userManager.ts`, `oidcConfig.ts`, and the token plumbing in
`client.ts` all go; `login()` becomes a navigation to `/v1/auth/login`, and `fetchWithAuth`'s
silent-renew retry ladder disappears. `OidcAuthProvider.tsx` shrinks to a `/v1/me` gate.
Tests to rewrite: `AuthContext.test.tsx`, `CallbackPage.test.tsx`, `userManager.test.ts`.

## Decision

**Do #151 before #216.**

Finding 1 is not incidental to #216 — it is the seam between the two epics, and #151 already
names it: "The two meet at exactly one point — #145 removing `RealmRoles` from
`authn.Principal`." #145 exists to do precisely what Finding 1 needs done. Landing it first
means nothing reads `realm_access.roles` by the time the verifier switches to ID tokens, so
the regression cannot occur, and no Keycloak roles mapper is added-then-deleted.

The bootstrap gap relocates rather than disappearing: under #151 it is answered by #153 (the
`root` relation) and #212 (seeding the account and tuple at boot), which is the intended
solution rather than a fixture patch.

**Cost.** #145 is five issues deep — #214 → #208 + #209 → #141 → #145 — and does not
cherry-pick. `CheckRequest` takes a `Stack`, not an object
(`internal/app/authorization.go:74`), so `platform` cannot be expressed until #214 lands, and
#141 must define the singleton before #145 can ask OpenFGA whether a caller is an admin. #151
also warns that changing the model before #208/#209 means retro-testing it. Against that,
tflive is pre-production with disposable state, so the schedule pressure that would normally
favour the interim mapper is not real.

**Next:** #214, which gates the rest of #151 and carries no identity entanglement.

## Decisions carried into #216 when it comes up

Settled here, not revisited later:

- **Refresh and logout scope stay open.** Whether refresh ships inside #216 (finding 5) and
  whether logout ends the IdP session or only ours (finding 2) are unresolved, and should be
  settled when #216 is designed rather than now.
- **State handling:** short-TTL encrypted cookie, `SameSite=lax` — `strict` withholds the
  cookie on the IdP's cross-site callback and breaks every login (finding 3).
- **CSRF:** document `lax` as sufficient, with the reasoning in finding 4; no double-submit
  token unless a mutating GET appears.
- **Cookie contents:** the IdP's ID token, routed on `iss`, per ArgoCD — keeps #211's shape
  intact (finding 6).
- **Any new key goes through `internal/config`**, not the bare `os.Getenv` that
  `CREDENTIAL_ENCRYPTION_KEY` uses today (finding 7).

## Issue edits made from these findings

- **#151** — opening premise and "Provider constraint" section were written around JWT access
  tokens with `aud: tflive-api`, which #216 supersedes; couplings list reduced from three to
  one (#195 closed, #155 moved to #210).
- **#153** — root was "configured via environment (e.g. Keycloak subject)"; it is a seeded
  local account needing no IdP. Model/seeding split with #212 made explicit.
- **#141** — bootstrap bullet said seed `admin`; #212 seeds `root` deliberately so the grant
  API cannot target it. Stale paths corrected to `openfga/authorization-model.json`,
  `cmd/openfga-provisioner/`, `cmd/keycloak-provisioner/`.

Not a bug, recorded to stop it being re-raised: **#153 and #212 are not duplicates**, and
`platform#admin` versus `platform#root` is a deliberate two-relation design — `root` sits
outside the grantable set so the grant API rejects it, whereas a seeded `admin` would look
revocable and be silently restored by add-only reconcile.
