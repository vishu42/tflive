# OIDC Access-Token Verification Design

## Goal

Implement AUTH-006 as a replaceable Go verifier for Keycloak access tokens. It
must use OpenID Connect discovery and JWKS public keys to validate a token's
signature, issuer, audience, expiry, and not-before claim without exposing
tokens or sensitive verification details.

## Scope

This ticket adds a verifier package and its unit tests. It does not add HTTP
middleware, protect routes, create request principals, or authorize actions.
Those responsibilities begin with AUTH-007 and later authentication and
authorization tickets.

The verifier consumes the typed issuer URL and audience already validated by
AUTH-005. It does not introduce a client secret, password, token, or new
environment variable.

## Package Boundary

Create `internal/authn` with a small dependency-neutral interface. HTTP
handlers and future middleware depend on this interface rather than the JWKS
or JWT library.

```go
var (
    ErrInvalidToken        = errors.New("invalid access token")
    ErrVerifierUnavailable = errors.New("token verifier unavailable")
)

type VerifiedToken struct {
    Subject           string
    Name              string
    PreferredUsername string
    Email             string
    RealmRoles        []string
}

type Verifier interface {
    Verify(ctx context.Context, rawToken string) (VerifiedToken, error)
}
```

`VerifiedToken` intentionally contains only the identity claims needed by
AUTH-007 to create a request principal. The encoded token, its signature, and
all unrelated claims are discarded as soon as verification completes.

`OIDCVerifier` implements `Verifier`. Its constructor receives the issuer URL,
expected audience, an HTTP client, a clock, and cache controls through an
`OIDCVerifierConfig`. Production uses the documented defaults; tests can inject
short intervals and a deterministic clock without test-only production APIs.

```go
type OIDCVerifierConfig struct {
    IssuerURL               *url.URL
    Audience                string
    HTTPClient              *http.Client
    Clock                   func() time.Time
    DiscoveryTTL            time.Duration
    JWKSMinRefreshInterval  time.Duration
    JWKSMaxRefreshInterval  time.Duration
    RefreshCooldown         time.Duration
}
```

`IssuerURL` and `Audience` are required. Nil or zero optional values select a
hardened HTTP client, `time.Now`, a 15-minute discovery lifetime, one-minute
minimum JWKS refresh interval, 15-minute maximum JWKS refresh interval, and a
five-second refresh cooldown. The verifier clones a supplied HTTP client and
still rejects redirects; callers cannot weaken that rule through configuration.

```go
func NewOIDCVerifier(ctx context.Context, cfg OIDCVerifierConfig) (*OIDCVerifier, error)
func (v *OIDCVerifier) Verify(ctx context.Context, rawToken string) (VerifiedToken, error)
func (v *OIDCVerifier) Close(ctx context.Context) error
```

`Close` releases the background JWKS-cache resources. The `Verifier` interface
does not include `Close`, allowing a future test or alternate verifier to have
no lifecycle requirements.

## Dependency Choice

Use `github.com/lestrrat-go/jwx/v3` for JWS/JWT parsing and cryptographic
verification, plus its JWKS cache. This library provides explicit JWT claim
validation and an in-memory cache with controlled refresh behavior.

The implementation uses OpenID Connect discovery directly rather than
hard-coding a Keycloak certificate endpoint. The discovery request is formed by
appending `/.well-known/openid-configuration` to the configured issuer URL, as
defined by OpenID Connect Discovery. The discovery response must contain an
`issuer` exactly equal to the configured issuer and an absolute HTTP or HTTPS
`jwks_uri`.

Construction synchronously obtains and validates discovery metadata, then
loads an initial JWKS before returning a usable verifier. An unavailable or
malformed provider at this stage makes construction return
`ErrVerifierUnavailable`.

## Verification Flow

For each raw token, the verifier performs these steps in order:

1. Reject an empty or oversized value. The maximum encoded-token size is 16
   KiB.
2. Require compact JWS form with exactly three base64url segments; encrypted
   JWTs and JSON serializations are rejected.
3. Read only the protected header needed to check the untrusted `alg` and
   `kid` values. A non-empty `kid` is mandatory.
4. Reject every algorithm outside this fixed asymmetric allowlist:
   `RS256`, `RS384`, `RS512`, `PS256`, `PS384`, `PS512`, `ES256`, `ES384`,
   `ES512`, and `EdDSA`. `none`, HMAC algorithms, and unknown values are
   rejected before selecting a key or refreshing the cache.
5. Locate the key by `kid`, requiring a compatible key type. If a JWK declares
   a `use` or `alg`, it must be `sig` and match the token's algorithm. Duplicate
   non-empty key IDs make the JWKS unusable.
6. Verify the JWS signature with the selected cached public key.
7. Validate the registered claims and then decode the permitted identity
   claims into `VerifiedToken`.

The verifier applies a fixed 30-second clock skew to time comparisons. It
requires `exp` and rejects an expired token. It validates `nbf` when present
and rejects a token whose not-before time is in the future after skew. It also
requires all of the following:

- `iss` exactly equals the configured issuer;
- `aud` contains the configured audience, whether represented as one string or
  an array;
- `sub` is a non-empty string;
- payload `typ` is exactly `Bearer`.

`name`, `preferred_username`, and `email` are optional strings. An absent value
is represented by an empty string; any non-string supplied value invalidates
the token. `realm_access.roles` is optional and becomes an empty list when
absent. When it is present, `realm_access` must be an object and `roles` must
contain only strings. Role recognition and normalization are deliberately
deferred to AUTH-007.

## Discovery, Caching, and Rotation

The verifier treats discovery metadata and JWKS material as two bounded caches.
It uses an HTTP client with a 10-second request timeout, rejects redirects, and
limits each discovery or JWKS response body to 1 MiB.

- Discovery metadata has a 15-minute lifetime. A successful refresh that moves
  `jwks_uri` atomically replaces the active key cache. A failed later discovery
  refresh does not discard an existing valid key cache.
- JWKS refreshes follow provider cache headers, bounded by a one-minute minimum
  and 15-minute maximum refresh interval. A public key set is only usable while
  it remains inside its bounded lifetime.
- An unknown `kid` or signature mismatch triggers one immediate refresh and
  one retry. Concurrent refresh requests are deduplicated, and new refreshes
  are rate-limited to at most one every five seconds.
- If a valid cached key verifies the token, a transient Keycloak outage does
  not reject it. If verification needs a refresh but discovery or JWKS cannot
  be obtained, the verifier fails closed.

This supports normal Keycloak key rotation—including a new key ID and a
replacement key that retains an ID—without an API restart, while preventing
untrusted token headers from creating unbounded refresh traffic.

## Error Contract and Information Handling

`Verify` returns only the two exported sentinel categories:

| Condition | Returned category |
|---|---|
| Malformed token, bad signature, disallowed algorithm, invalid or missing claim, or a refreshed JWKS with no requested key | `ErrInvalidToken` |
| Initial discovery/JWKS setup failure, expired cache that cannot refresh, or malformed/unavailable discovery/JWKS infrastructure | `ErrVerifierUnavailable` |

Returned error text is exactly the safe category text. It does not wrap or
format the raw token, decoded claims, response bodies, key IDs, endpoint
contents, or cryptographic failure details. The implementation does not log
those values. Future middleware can use `errors.Is` to map invalid credentials
to a stable `401` response and an unusable verifier to a stable `503` response.

## File Layout

| File | Responsibility |
|---|---|
| `internal/authn/verifier.go` | Interface, verified-token type, configuration defaults, and safe errors |
| `internal/authn/oidc_verifier.go` | Token form checks, header/claim validation, and identity extraction |
| `internal/authn/oidc_provider.go` | Discovery retrieval, JWKS cache construction, refresh coordination, and shutdown |
| `internal/authn/oidc_verifier_test.go` | Unit tests for token validity, claims, algorithms, redaction, and key rotation |
| `internal/authn/oidc_provider_test.go` | Unit tests for discovery, cache lifetime, outage, and refresh coordination |
| `docs/authentication.md` | Operational behavior for access-token verification and Keycloak key rotation |
| `docs/sprint/authn_and_authz/README.md` | AUTH-006 status and completed acceptance criteria |

## Test Strategy

Tests use an in-process OIDC discovery and JWKS server plus generated signing
keys. They do not require Docker, Keycloak, a real user, or committed tokens.

The suite proves all AUTH-006 acceptance criteria with these cases:

- a valid asymmetric Keycloak-style access token returns only the documented
  identity fields;
- malformed, oversized, unsigned, HMAC-signed, unknown-algorithm, and
  signature-altered inputs return `ErrInvalidToken`;
- expired, future-not-before, wrong-issuer, wrong-audience, missing-subject,
  wrong-type, and malformed optional-claim inputs return `ErrInvalidToken`;
- repeated valid verification uses cached keys without repeatedly calling the
  JWKS endpoint;
- the same verifier validates a token signed by a newly published key after a
  key rotation;
- concurrent unknown-key requests cause one coordinated refresh, not one
  refresh per request;
- a temporary provider outage still permits a token verifiable by a fresh
  cached key, while an unavailable or expired cache returns
  `ErrVerifierUnavailable`;
- every returned error is checked to ensure it contains neither the raw token,
  decoded claims, provider response body, nor key material.

Run the focused package tests during development with:

```bash
go test ./internal/authn
```

Run the complete Go suite before completion with:

```bash
go test ./...
```

## Documentation and Backlog

`docs/authentication.md` documents the verifier's accepted-token contract,
public-key cache behavior, rotation behavior, and safe dependency-failure
handling. The sprint backlog moves AUTH-006 to `Done` only after the focused
and complete test commands pass and the documented acceptance criteria are
verified.

## Explicitly Out of Scope

- Parsing `Authorization` headers or returning HTTP responses;
- attaching a principal to request context;
- enforcing tenant paths or OpenFGA permissions;
- browser sign-in, refresh-token storage, or Keycloak realm provisioning;
- accepting opaque tokens, ID tokens, or user-supplied issuer URLs.

## References

- [OpenID Connect Discovery 1.0](https://openid.net/specs/openid-connect-discovery-1_0.html)
- [lestrrat-go/jwx v3 JWT package](https://pkg.go.dev/github.com/lestrrat-go/jwx/v3/jwt)
- [lestrrat-go/jwx v3 JWKS cache package](https://pkg.go.dev/github.com/lestrrat-go/jwx/v3/jwk)
