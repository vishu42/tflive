# Local accounts (#211) — considerations

Issue #211 was written 2026-08-21. Between then and now, #216 landed and then
`2026-08-29-app-owned-session-design.md` landed on top of it. Both changed the
premise the issue reasons from. This document records what is still true, what
is not, and what the issue reduces to once the stale half is removed.

## What the issue assumes

1. `POST /v1/auth/login` returns an **RS256 JWT** to the caller.
2. `Verify` reads `iss` from an unverified parse and **routes** to either a
   local branch (in-process public key) or the OIDC branch.
3. tflive persists an **RSA keypair** across restarts, "if it regenerates on
   restart every session dies".
4. **Server-side sessions are out of scope.**

Assumption 4 is the load-bearing one. The other three follow from it: if the
credential the browser holds is the token, then the token must be signed,
routed on, and verified per request, and the signing key must outlive a restart
because it is what holds sessions up.

## What is actually true now

**The session is a database row; the cookie is an opaque reference to it.**
`internal/postgres/migrations/0018_sessions.sql`, `internal/authn/session.go:26`.
Server-side sessions are not out of scope — they are the only thing in scope.

**The middleware accepts the session cookie and nothing else.**
`internal/authn/middleware.go:16-31` removes the `Authorization: Bearer` path
deliberately, and argues it at length: a signed token is not revocable, and
logout, back-channel logout, and an admin disabling an account all mark a
session row that no copy of a JWT can be reached from. `internal/api/server.go:206-209`
repeats it — "No verifier here."

**A token is verified in exactly one place: the OIDC callback.**
`internal/api/auth.go:104`, reached from `handleAuthCallback` after
`Flow.Exchange`. That is the only call site of `Verifier.Verify` outside tests.

## The finding

**`iss`-routed verification, as specified, has no caller.**

The routing entry point would sit in `Verify`. `Verify` is called once, on the
string that `Flow.Exchange` pulled out of an IdP's token response. A local token
can never arrive there. For the local branch to be reached, the local login
handler would have to mint a JWT and then immediately hand it to the verifier it
just signed it with — a round trip through RSA and back whose only output is the
struct the handler already had.

The pattern is correct **in ArgoCD**, where it was read from, because ArgoCD's
session cookie *is* the JWT. Routing on `iss` is how ArgoCD tells its own
cookie from an IdP's. tflive's cookie is a random 32-byte reference, and the
question "who signed this" is never asked of it, because nothing signed it.

The same collapse takes the other three items with it:

- **No JWT to return.** The browser cannot authenticate with one
  (`middleware.go`), so returning one gives it a credential no route accepts.
- **No persisted RSA keypair.** Its stated purpose — "if it regenerates on
  restart every session dies" — is already handled: sessions survive restarts
  because they are rows in Postgres. There is nothing left for the key to hold up.
- **No widening of the algorithm allowlist**, and so no need for the RS256-over-HS256
  argument, which is sound but answers a question that is no longer asked.

## What #211 reduces to

A second **authentication method** feeding the same session-minting path, not a
second **token issuer**. `handleAuthCallback` ends in four steps —
`RecordSignIn` → `NewSessionID` → `CreateSession` → `SessionCookie`
(`internal/api/auth.go:130-172`). Local login performs argon2id verification and
then those same four steps. Everything downstream is untouched, which was the
issue's goal; it is reached by a shorter route than the one it proposed.

Scope that survives intact:

- `local_accounts` table, argon2id, `sub` free of `:`
  (`safeTupleToken`, `internal/authz/authorization.go:239`, confirmed — it rejects
  `:`, `#`, and `*`).
- Login endpoint, username + password.
- Rate limiting. Nothing in the repo does this today; it is new.
- Safety rule 3 in spirit: **disabled means the route is not registered.** With
  no local branch to fall through to, this becomes ordinary — a 404, with no
  forged-token path to reason about at all.

Safety rules 1 and 2 are moot with routing gone. Nothing is parsed unverified,
and no local issuer string exists for `OIDC_ISSUER_URL` to collide with.

## Gaps the issue does not mention, which the goal requires

**Config makes an IdP mandatory.** `loadSecurityConfig` hard-requires
`OIDC_ISSUER_URL`, `OIDC_CLIENT_ID`, and `OIDC_CLIENT_SECRET`
(`internal/config/auth.go:124-142`). The issue's goal — "a POC, demo, or test
needs no IdP at all" — is unreachable until OIDC config becomes optional when
local auth is on. This is the single largest piece of work the issue omits.

**Logout is gated behind the OIDC flow.** All four auth routes register under
`if server.auth.Flow != nil` (`internal/api/server.go:121-126`). In an
IdP-less deployment `Flow` is nil, so `POST /v1/auth/logout` does not exist and
a local session cannot be ended. The gating has to split: session logout is
unconditional, RP-initiated logout is the part that needs a flow.

**Local accounts need display claims.** The projection writes `email` and
`display_name` (`0019_users.sql`), and the grants list reads them. An account
table of `(sub, username, password_hash)` alone projects a blank-named user.
`display_name` and `email` columns belong in `local_accounts`.

**#213 needs to know which methods are enabled.** "When local auth is disabled
server-side, the form is not rendered" requires an unauthenticated endpoint
stating the enabled methods. It is small, it belongs here rather than in #213,
and the sign-in screen cannot be built without it.

## Consequences for neighbouring issues

- **#212** is unaffected. Root is a seeded row plus an OpenFGA tuple; neither
  cares how the credential is checked.
- **#213** needs rewording: "store the returned JWT the same way an
  OIDC-obtained token is stored" describes the pre-#216 SPA. The browser stores
  nothing now — the response sets a cookie, and the form's success path is a
  refetch of `/v1/me`.
- **#198** gets simpler. A local sign-in constructs a conforming identity rather
  than parsing one, so the contract can be asserted once against both producers.
- **Epic #210** still says requests are accepted "with either the cookie or an
  `Authorization: Bearer` header, so a future CLI still works." #216 removed the
  bearer path on purpose and named the replacement: a credential tflive issues
  and can revoke. That paragraph is stale.

## The one thing genuinely lost

Nothing today wants a stateless tflive-issued token. A future CLI is the usual
candidate, and `middleware.go` has already ruled on it in favour of a revocable
issued credential. Re-adding signing later costs a keypair and a verifier branch
— roughly the code this removes — and would be built against a real caller
rather than a hypothetical one.
