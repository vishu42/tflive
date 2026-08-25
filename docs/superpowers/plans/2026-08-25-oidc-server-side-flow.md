# Server-Side OIDC Flow Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the tflive API the OIDC confidential client, so the browser never handles a token and any OIDC provider works without a paid custom authorization server.

**Architecture:** The API gains `/v1/auth/login`, `/v1/auth/callback`, and `/v1/auth/logout`. Login redirects the browser to the IdP with PKCE, sealing `state`/`nonce`/`code_verifier`/`return_to` in a short-lived AEAD cookie. The callback exchanges the code server-to-server, verifies the **ID token** (whose `aud` is the client ID), and sets it as an httpOnly session cookie. The authn middleware accepts the token from the `Authorization` header or that cookie. There is no refresh token: session length is the ID token's lifetime, and the SPA re-authenticates through the IdP's still-live SSO session.

**Tech Stack:** Go 1.25, `golang.org/x/oauth2` (new direct dependency, grant mechanics only), `lestrrat-go/jwx/v3` (existing, verification), React 18 + Vite + Vitest, Keycloak fixture provisioned by `cmd/keycloak-provisioner`.

**Spec:** `docs/superpowers/specs/2026-08-25-oidc-server-side-flow-design.md`

## Global Constraints

- Go module floor is `go 1.25.0`, toolchain `go1.25.14`. Do not raise either.
- Format every Go file you touch: `gofmt -w <files>`.
- **tflive is pre-production with disposable state.** Never write a backward-compatibility shim, a config alias, or a data migration for old values. A stale env var name must fail startup loudly.
- Only one new Go dependency is authorised: `golang.org/x/oauth2`. Do not add `coreos/go-oidc` — our own discovery and verifier are a settled decision (`docs/superpowers/specs/2026-08-20-iam-openfga-surface-analysis.md`).
- Cookie names are exactly `tflive_session` and `tflive_auth_tx`.
- Both cookies are `HttpOnly` and `SameSite=Lax`. **Never `SameSite=Strict`** — the IdP callback is a cross-site top-level GET and `Strict` withholds the cookie, breaking every login.
- `Secure` is set on both cookies only when the runtime mode is `production`.
- Requested scope is exactly `openid profile email`. **Never add `offline_access`** — no refresh token is wanted.
- All authentication failures render one identical, detail-free response. Never report which check failed.
- Backend tests: `go test ./...` from the repo root. Frontend tests: `npm test` from `web/`.
- Commit subjects are short, imperative, lowercase-prefixed: `feat:`, `fix:`, `test:`, `refactor:`, `docs:`, `chore:`.

## File Structure

**New files**

| Path | Responsibility |
|---|---|
| `internal/secrets/cipher.go` | AES-GCM seal/open, moved out of `internal/credentials` so `internal/authn` can use it without depending on Terraform credential storage |
| `internal/secrets/cipher_test.go` | Round trip, wrong key, invalid key |
| `internal/authn/endpoints.go` | `Endpoints` struct and the `OIDCVerifier.Endpoints()` accessor |
| `internal/authn/session.go` | Cookie names, transaction sealing, `SafeReturnTo`, cookie builders |
| `internal/authn/session_test.go` | Seal round trip, tamper detection, `SafeReturnTo` table |
| `internal/authn/flow.go` | `Flow`: authorization URL, code exchange, end-session URL |
| `internal/authn/flow_test.go` | URL shape, exchange against a stub token endpoint |
| `internal/api/auth.go` | The three HTTP handlers |
| `internal/api/auth_test.go` | Handler tests against a stub IdP |

**Modified files**

| Path | Change |
|---|---|
| `internal/authn/oidc_provider.go` | `discoveryDocument` grows three fields; endpoints validated at construction |
| `internal/authn/verifier.go` | `VerifiedToken` gains `Nonce` and `ExpiresAt` |
| `internal/authn/oidc_verifier.go` | `validatedToken` populates them |
| `internal/authn/principal.go` | `Principal` gains `ExpiresAt` |
| `internal/authn/middleware.go` | Cookie fallback after the header |
| `internal/api/server.go` | Auth routes, `WithAuth` option, extended public path list |
| `internal/auth/me.go` | `MeResponse` gains `sessionExpiresAt` |
| `internal/config/auth.go` | `OIDCConfig` reshaped; `PublicURL`, `SessionEncryptionKey` added |
| `internal/config/config.go` | `CredentialEncryptionKey` on `APIConfig` and `WorkerConfig` |
| `internal/postgres/store.go` | Cipher injected instead of read from `os.Getenv` |
| `internal/keycloak/provisioner.go`, `config.go` | One confidential client; audience scope and `ExampleAccessToken` deleted |
| `cmd/api/main.go`, `cmd/worker/main.go` | Wiring |
| `web/src/auth/*`, `web/src/api/client.ts`, `web/src/app/router.tsx` | SPA teardown and `SessionProvider` |
| `.env.example`, `docker-compose.app.yaml`, `docs/authentication.md`, `README.md` | Config and docs |

**Deleted files**

`internal/credentials/crypto.go`, `internal/credentials/crypto_test.go`, `web/src/auth/oidcConfig.ts`, `web/src/auth/userManager.ts`, `web/src/auth/userManager.test.ts`, `web/src/auth/CallbackPage.tsx`, `web/src/auth/CallbackPage.test.tsx`.

---

### Task 1: Move the AES-GCM cipher to `internal/secrets`

`internal/authn` needs authenticated encryption for the transaction cookie. The implementation already exists in `internal/credentials`, but that package is about Terraform credential storage — the wrong dependency for identity code. `internal/secrets` exists today as a bare `doc.go` describing exactly this boundary. Move it there and delete `internal/credentials`, whose only importer is `internal/postgres`.

**Files:**
- Create: `internal/secrets/cipher.go`
- Create: `internal/secrets/cipher_test.go`
- Delete: `internal/credentials/crypto.go`, `internal/credentials/crypto_test.go`
- Modify: `internal/postgres/store.go:1-40`

**Interfaces:**
- Consumes: nothing
- Produces: `secrets.NewCipher(rawKey string) (*secrets.Cipher, error)`, `(*Cipher).Encrypt(string) (string, error)`, `(*Cipher).Decrypt(string) (string, error)`, `secrets.ErrInvalidKey`

- [ ] **Step 1: Write the failing test**

Create `internal/secrets/cipher_test.go`:

```go
package secrets

import "testing"

func TestCipherEncryptDecryptRoundTrip(t *testing.T) {
	// Verify that encryption is reversible while the stored representation is not plaintext.
	cipher, err := NewCipher("01234567890123456789012345678901")
	if err != nil {
		t.Fatalf("NewCipher returned error: %v", err)
	}

	ciphertext, err := cipher.Encrypt("provider-secret")
	if err != nil {
		t.Fatalf("Encrypt returned error: %v", err)
	}
	if ciphertext == "provider-secret" {
		t.Fatal("ciphertext contains plaintext")
	}

	plaintext, err := cipher.Decrypt(ciphertext)
	if err != nil {
		t.Fatalf("Decrypt returned error: %v", err)
	}
	if plaintext != "provider-secret" {
		t.Fatalf("plaintext = %q, want provider-secret", plaintext)
	}
}

func TestCipherRejectsWrongKey(t *testing.T) {
	// Verify that authenticated encryption rejects ciphertext opened with another key.
	cipher, err := NewCipher("01234567890123456789012345678901")
	if err != nil {
		t.Fatalf("NewCipher returned error: %v", err)
	}
	ciphertext, err := cipher.Encrypt("provider-secret")
	if err != nil {
		t.Fatalf("Encrypt returned error: %v", err)
	}

	other, err := NewCipher("abcdefghijklmnopqrstuvwxyz123456")
	if err != nil {
		t.Fatalf("NewCipher returned error: %v", err)
	}
	if _, err := other.Decrypt(ciphertext); err == nil {
		t.Fatal("Decrypt succeeded with the wrong key")
	}
}

func TestNewCipherRejectsInvalidKey(t *testing.T) {
	// Verify that cipher construction fails closed for unsupported key material.
	if _, err := NewCipher("too-short"); err == nil {
		t.Fatal("NewCipher accepted an invalid key")
	}
}

func TestCipherRejectsTamperedCiphertext(t *testing.T) {
	// AEAD is the only reason a sealed cookie can be trusted, so prove it detects edits.
	cipher, err := NewCipher("01234567890123456789012345678901")
	if err != nil {
		t.Fatalf("NewCipher returned error: %v", err)
	}
	ciphertext, err := cipher.Encrypt("provider-secret")
	if err != nil {
		t.Fatalf("Encrypt returned error: %v", err)
	}

	tampered := []byte(ciphertext)
	tampered[len(tampered)-1] ^= 'A'
	if _, err := cipher.Decrypt(string(tampered)); err == nil {
		t.Fatal("Decrypt accepted tampered ciphertext")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/secrets/...`
Expected: FAIL — `undefined: NewCipher`

- [ ] **Step 3: Create the cipher**

Create `internal/secrets/cipher.go`. This is `internal/credentials/crypto.go` with the package renamed and the error strings generalised away from "credential":

```go
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
)

var ErrInvalidKey = errors.New("encryption key must be 32 bytes")

type Cipher struct {
	aead cipher.AEAD
}

// NewCipher constructs an AES-GCM cipher from a 32-byte raw, base64, or hex key.
func NewCipher(rawKey string) (*Cipher, error) {
	key, err := decodeKey(rawKey)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create AEAD: %w", err)
	}
	return &Cipher{aead: aead}, nil
}

// Encrypt seals a value and returns nonce-prefixed, base64 ciphertext.
func (cipher *Cipher) Encrypt(plaintext string) (string, error) {
	nonce := make([]byte, cipher.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}
	ciphertext := cipher.aead.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.RawURLEncoding.EncodeToString(ciphertext), nil
}

// Decrypt authenticates and opens nonce-prefixed, base64 ciphertext.
func (cipher *Cipher) Decrypt(encoded string) (string, error) {
	ciphertext, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("decode ciphertext: %w", err)
	}
	nonceSize := cipher.aead.NonceSize()
	if len(ciphertext) < nonceSize {
		return "", errors.New("ciphertext is too short")
	}
	plaintext, err := cipher.aead.Open(nil, ciphertext[:nonceSize], ciphertext[nonceSize:], nil)
	if err != nil {
		return "", fmt.Errorf("decrypt: %w", err)
	}
	return string(plaintext), nil
}

// decodeKey accepts the supported key encodings and requires exactly 32 bytes.
func decodeKey(raw string) ([]byte, error) {
	if decoded, err := base64.RawStdEncoding.DecodeString(raw); err == nil && len(decoded) == 32 {
		return decoded, nil
	}
	if decoded, err := hex.DecodeString(raw); err == nil && len(decoded) == 32 {
		return decoded, nil
	}
	if len(raw) == 32 {
		return []byte(raw), nil
	}
	return nil, ErrInvalidKey
}
```

**Note the one deliberate behaviour change:** the encoding moves from `base64.RawStdEncoding` to `base64.RawURLEncoding`, because this value now goes into a cookie and `+` and `/` are not safe there. Existing encrypted credentials in a local database will fail to decode — that is acceptable and expected, because tflive is pre-production with disposable state. Do not write a fallback decoder.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/secrets/...`
Expected: PASS, 4 tests

- [ ] **Step 5: Point the store at the new package and delete the old one**

In `internal/postgres/store.go`, change the import from `"github.com/vishu42/tflive/internal/credentials"` to `"github.com/vishu42/tflive/internal/secrets"`, and change both occurrences of `*credentials.Cipher` to `*secrets.Cipher` and `credentials.NewCipher` to `secrets.NewCipher`. Leave the `os.Getenv` call alone for now — Task 2 removes it.

Then: `rm -r internal/credentials`

- [ ] **Step 6: Verify the whole build**

Run: `gofmt -l internal/secrets internal/postgres && go build ./... && go test ./internal/secrets/... ./internal/postgres/...`
Expected: no gofmt output, build succeeds, tests pass

- [ ] **Step 7: Commit**

```bash
git add internal/secrets internal/postgres/store.go
git add -A internal/credentials
git commit -m "refactor(secrets): move the AES-GCM cipher out of internal/credentials"
```

---

### Task 2: Configuration surface

Five changes: `OIDC_AUDIENCE` becomes `OIDC_CLIENT_ID`, a client secret arrives, the API learns its own public origin, a session sealing key arrives, and `CREDENTIAL_ENCRYPTION_KEY` stops being read by a bare `os.Getenv` outside `internal/config`.

**Files:**
- Modify: `internal/config/auth.go`
- Modify: `internal/config/config.go:18-40,62-130`
- Modify: `internal/config/auth_test.go`
- Modify: `internal/postgres/store.go:30-45`
- Modify: `cmd/api/main.go:95-105,125-140`
- Modify: `cmd/worker/main.go:125-135`
- Modify: `cmd/api/main_test.go:370-380`

**Interfaces:**
- Consumes: `secrets.NewCipher` (Task 1)
- Produces:
  - `config.OIDCConfig{IssuerURL *url.URL; ClientID string; ClientSecret Secret}`
  - `config.SecurityConfig.PublicURL *url.URL`
  - `config.SecurityConfig.SessionEncryptionKey Secret`
  - `config.APIConfig.CredentialEncryptionKey Secret` and `config.WorkerConfig.CredentialEncryptionKey Secret`
  - `postgres.WithCredentialCipher(*secrets.Cipher) postgres.Option`

- [ ] **Step 1: Write the failing tests**

Append to `internal/config/auth_test.go`:

```go
func TestLoadSecurityConfigRequiresOIDCClientCredentials(t *testing.T) {
	for _, name := range []string{"OIDC_CLIENT_ID", "OIDC_CLIENT_SECRET", "TFLIVE_PUBLIC_URL", "SESSION_ENCRYPTION_KEY"} {
		t.Run(name, func(t *testing.T) {
			env := validSecurityEnv()
			delete(env, name)
			if _, err := loadSecurityConfig(envLookup(env)); err == nil {
				t.Fatalf("loadSecurityConfig accepted a missing %s", name)
			}
		})
	}
}

func TestLoadSecurityConfigRejectsRetiredOIDCAudience(t *testing.T) {
	// OIDC_AUDIENCE changed meaning from a resource identifier to a client ID.
	// Silently accepting the old name would validate a value nobody re-checked.
	env := validSecurityEnv()
	delete(env, "OIDC_CLIENT_ID")
	env["OIDC_AUDIENCE"] = "tflive-api"
	if _, err := loadSecurityConfig(envLookup(env)); err == nil {
		t.Fatal("loadSecurityConfig accepted the retired OIDC_AUDIENCE")
	}
}

func TestLoadSecurityConfigReadsPublicURLAndSessionKey(t *testing.T) {
	env := validSecurityEnv()
	cfg, err := loadSecurityConfig(envLookup(env))
	if err != nil {
		t.Fatalf("loadSecurityConfig returned error: %v", err)
	}
	if cfg.PublicURL == nil || cfg.PublicURL.String() != "http://localhost:5173" {
		t.Fatalf("PublicURL = %v", cfg.PublicURL)
	}
	if cfg.OIDC.ClientID != "tflive-api" {
		t.Fatalf("ClientID = %q", cfg.OIDC.ClientID)
	}
	if cfg.OIDC.ClientSecret.Value() != "oidc-client-secret" {
		t.Fatalf("ClientSecret = %q", cfg.OIDC.ClientSecret.Value())
	}
	if cfg.SessionEncryptionKey.Value() != "01234567890123456789012345678901" {
		t.Fatalf("SessionEncryptionKey = %q", cfg.SessionEncryptionKey.Value())
	}
}

func TestSecurityConfigStringRedactsSecrets(t *testing.T) {
	cfg, err := loadSecurityConfig(envLookup(validSecurityEnv()))
	if err != nil {
		t.Fatalf("loadSecurityConfig returned error: %v", err)
	}
	rendered := cfg.String()
	for _, secret := range []string{"oidc-client-secret", "01234567890123456789012345678901"} {
		if strings.Contains(rendered, secret) {
			t.Fatalf("SecurityConfig.String() leaked %q: %s", secret, rendered)
		}
	}
}
```

These tests use `strings.Contains`, so ensure `"strings"` is imported. Add these two helpers to the same file if the existing test file has no equivalent (check first — reuse whatever env-map helper `auth_test.go` already defines rather than adding a duplicate):

```go
func validSecurityEnv() map[string]string {
	return map[string]string{
		"TFLIVE_ENVIRONMENT":     "development",
		"TFLIVE_TENANT_ID":       "tenant_123",
		"TFLIVE_PUBLIC_URL":      "http://localhost:5173",
		"OIDC_ISSUER_URL":        "http://localhost:8082/realms/tflive",
		"OIDC_CLIENT_ID":         "tflive-api",
		"OIDC_CLIENT_SECRET":     "oidc-client-secret",
		"SESSION_ENCRYPTION_KEY": "01234567890123456789012345678901",
		"OPENFGA_API_URL":        "http://localhost:8080",
		"OPENFGA_STORE_ID":       "store",
		"OPENFGA_MODEL_ID":       "model",
	}
}

func envLookup(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/config/...`
Expected: FAIL — `cfg.PublicURL undefined`, `cfg.OIDC.ClientID undefined`

- [ ] **Step 3: Reshape the config structs and loader**

In `internal/config/auth.go`:

```go
type OIDCConfig struct {
	IssuerURL    *url.URL
	ClientID     string
	ClientSecret Secret
}
```

Add to `SecurityConfig`, after `OIDC`:

```go
	PublicURL            *url.URL
	SessionEncryptionKey Secret
```

Replace the `OIDC_AUDIENCE` block in `loadSecurityConfig` with:

```go
	if strings.TrimSpace(getenv("OIDC_AUDIENCE")) != "" {
		return SecurityConfig{}, authConfigError("OIDC_AUDIENCE is retired: set OIDC_CLIENT_ID to the OAuth client ID instead")
	}
	clientID := strings.TrimSpace(getenv("OIDC_CLIENT_ID"))
	if clientID == "" {
		return SecurityConfig{}, authConfigError("OIDC_CLIENT_ID is required")
	}
	if !safeOpaqueValue(clientID) {
		return SecurityConfig{}, authConfigError("OIDC_CLIENT_ID must not contain whitespace or control characters")
	}
	clientSecret := newSecret(strings.TrimSpace(getenv("OIDC_CLIENT_SECRET")))
	if clientSecret.Empty() {
		return SecurityConfig{}, authConfigError("OIDC_CLIENT_SECRET is required")
	}

	publicURL, err := parseConfigURL("TFLIVE_PUBLIC_URL", getenv("TFLIVE_PUBLIC_URL"))
	if err != nil {
		return SecurityConfig{}, err
	}
	publicURL.Path = strings.TrimRight(publicURL.Path, "/")

	sessionKey := newSecret(strings.TrimSpace(getenv("SESSION_ENCRYPTION_KEY")))
	if sessionKey.Empty() {
		return SecurityConfig{}, authConfigError("SESSION_ENCRYPTION_KEY is required")
	}
	if _, err := secrets.NewCipher(sessionKey.Value()); err != nil {
		return SecurityConfig{}, authConfigError("SESSION_ENCRYPTION_KEY must be a 32-byte raw, base64, or hex key")
	}
```

Import `"github.com/vishu42/tflive/internal/secrets"`. Add `publicURL.Scheme != "https"` to the existing production HTTPS block alongside the issuer check. Populate the new fields in the returned struct, and update the `String()` format so it renders `ClientID`, `ClientSecret`, `PublicURL`, and `SessionEncryptionKey` — the `Secret` type already redacts, so pass the values through `%s`, never `%q` on `.Value()`.

- [ ] **Step 4: Route the credential key through config**

In `internal/config/config.go`, add `CredentialEncryptionKey Secret` to both `APIConfig` and `WorkerConfig`, and set it in both loaders:

```go
	cfg.CredentialEncryptionKey = newSecret(strings.TrimSpace(getenv("CREDENTIAL_ENCRYPTION_KEY")))
```

In `internal/postgres/store.go`, delete the `os` import and the `os.Getenv` block, and replace `NewStore`'s body with:

```go
// NewStore creates a repository store. The credential cipher is injected by the
// caller from internal/config; the store reads no environment of its own.
func NewStore(pool *pgxpool.Pool, options ...Option) *Store {
	store := &Store{pool: pool}
	for _, option := range options {
		option(store)
	}
	return store
}

// WithCredentialCipher supplies the process-wide credential encryption cipher.
// Without it, Encrypt and Decrypt return app.ErrCredentialEncryptionUnavailable.
func WithCredentialCipher(cipher *secrets.Cipher) Option {
	return func(store *Store) { store.credentialCipher = cipher }
}
```

In `cmd/api/main.go` and `cmd/worker/main.go`, build the cipher once and pass it. In `cmd/api/main.go`, change `newStore`'s signature in `apiDependencies` to accept the cipher:

```go
	newStore func(postgresPool, *queue.SpecRegistry, *secrets.Cipher) (appRepositories, error)
```

and in `defaultAPIDependencies`:

```go
		newStore: func(pool postgresPool, specs *queue.SpecRegistry, cipher *secrets.Cipher) (appRepositories, error) {
			pgxPool, ok := pool.(*pgxpool.Pool)
			if !ok {
				return nil, fmt.Errorf("unexpected postgres pool type %T", pool)
			}
			return postgres.NewStore(pgxPool, postgres.WithQueueSpecs(specs), postgres.WithCredentialCipher(cipher)), nil
		},
```

and in `runWithDependencies`, before the `newStore` call:

```go
	var credentialCipher *secrets.Cipher
	if !cfg.CredentialEncryptionKey.Empty() {
		credentialCipher, err = secrets.NewCipher(cfg.CredentialEncryptionKey.Value())
		if err != nil {
			return fmt.Errorf("create credential cipher: %w", err)
		}
	}
	store, err := deps.newStore(pool, specs, credentialCipher)
```

Apply the same shape in `cmd/worker/main.go`. Update `cmd/api/main_test.go`'s env fixture: replace `"OIDC_AUDIENCE": "tflive-api"` with the four new variables from `validSecurityEnv()` above, and update any `newStore` test double to the three-argument signature.

- [ ] **Step 5: Run the tests**

Run: `gofmt -l internal/config internal/postgres cmd && go test ./internal/config/... ./internal/postgres/... ./cmd/...`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/config internal/postgres cmd
git commit -m "feat(config): replace OIDC_AUDIENCE with client credentials, public URL, and session key"
```

---

### Task 3: Expose the IdP's endpoints from discovery

`discoveryDocument` parses two of the ~40 fields the provider returns, because verification is all it has ever needed. The flow needs three more. They are read through the verifier rather than a second fetch, so there is one discovery document, one TTL, and one hardened HTTP client.

**Files:**
- Create: `internal/authn/endpoints.go`
- Modify: `internal/authn/oidc_provider.go:33-36,57-75,300-330`
- Modify: `internal/authn/oidc_verifier_test.go:145-175`

**Interfaces:**
- Consumes: nothing
- Produces: `authn.Endpoints{Authorization, Token, EndSession string}` and `(*authn.OIDCVerifier).Endpoints() Endpoints`

- [ ] **Step 1: Write the failing test**

First extend the test server so discovery can carry the new fields. In `internal/authn/oidc_verifier_test.go`, add three fields to `oidcTestServer` next to `discoveryBody`:

```go
	authorizationEndpoint string
	tokenEndpoint         string
	endSessionEndpoint    string
	omitEndpoints         bool
```

In `newOIDCTestServer`, after `s.discoveryIssuer = s.issuer`, add:

```go
	s.authorizationEndpoint = s.server.URL + "/authorize"
	s.tokenEndpoint = s.server.URL + "/token"
	s.endSessionEndpoint = s.server.URL + "/logout"
```

Replace the anonymous struct in `serveDiscovery` with one carrying all five fields, reading them under the same lock as the others, and omitting the endpoint fields entirely when `omitEndpoints` is set:

```go
	type document struct {
		Issuer                string `json:"issuer"`
		JWKSURI               string `json:"jwks_uri"`
		AuthorizationEndpoint string `json:"authorization_endpoint,omitempty"`
		TokenEndpoint         string `json:"token_endpoint,omitempty"`
		EndSessionEndpoint    string `json:"end_session_endpoint,omitempty"`
	}
	doc := document{Issuer: issuer, JWKSURI: s.server.URL + "/jwks"}
	if !omitEndpoints {
		doc.AuthorizationEndpoint = authorizationEndpoint
		doc.TokenEndpoint = tokenEndpoint
		doc.EndSessionEndpoint = endSessionEndpoint
	}
	writer.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(writer).Encode(doc); err != nil {
		s.t.Errorf("Encode discovery document: %v", err)
	}
```

Then add the tests:

```go
func TestOIDCVerifierExposesDiscoveredEndpoints(t *testing.T) {
	server := newOIDCTestServer(t)
	server.addRSAKey(t, "kid-1")
	server.publish("kid-1")

	verifier, err := NewOIDCVerifier(context.Background(), server.config(time.Now()))
	if err != nil {
		t.Fatalf("NewOIDCVerifier returned error: %v", err)
	}
	t.Cleanup(func() { _ = verifier.Close(context.Background()) })

	endpoints := verifier.Endpoints()
	if endpoints.Authorization != server.authorizationEndpoint {
		t.Fatalf("Authorization = %q, want %q", endpoints.Authorization, server.authorizationEndpoint)
	}
	if endpoints.Token != server.tokenEndpoint {
		t.Fatalf("Token = %q, want %q", endpoints.Token, server.tokenEndpoint)
	}
	if endpoints.EndSession != server.endSessionEndpoint {
		t.Fatalf("EndSession = %q, want %q", endpoints.EndSession, server.endSessionEndpoint)
	}
}

func TestOIDCVerifierRejectsDiscoveryMissingFlowEndpoints(t *testing.T) {
	// Without an authorization or token endpoint the API cannot run the flow at
	// all, so failing at construction beats failing on the first login.
	server := newOIDCTestServer(t)
	server.addRSAKey(t, "kid-1")
	server.publish("kid-1")
	server.omitEndpoints = true

	if _, err := NewOIDCVerifier(context.Background(), server.config(time.Now())); !errors.Is(err, ErrVerifierUnavailable) {
		t.Fatalf("NewOIDCVerifier error = %v, want ErrVerifierUnavailable", err)
	}
}

func TestOIDCVerifierAcceptsProviderWithoutEndSessionEndpoint(t *testing.T) {
	// end_session_endpoint is optional; logout degrades to clearing our cookie.
	server := newOIDCTestServer(t)
	server.addRSAKey(t, "kid-1")
	server.publish("kid-1")
	server.endSessionEndpoint = ""

	verifier, err := NewOIDCVerifier(context.Background(), server.config(time.Now()))
	if err != nil {
		t.Fatalf("NewOIDCVerifier returned error: %v", err)
	}
	t.Cleanup(func() { _ = verifier.Close(context.Background()) })

	if endpoints := verifier.Endpoints(); endpoints.EndSession != "" {
		t.Fatalf("EndSession = %q, want empty", endpoints.EndSession)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/authn/ -run TestOIDCVerifier.*Endpoint -v`
Expected: FAIL — `verifier.Endpoints undefined`

- [ ] **Step 3: Widen discovery and add the accessor**

In `internal/authn/oidc_provider.go`:

```go
type discoveryDocument struct {
	Issuer                string `json:"issuer"`
	JWKSURI               string `json:"jwks_uri"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	EndSessionEndpoint    string `json:"end_session_endpoint"`
}

// validFlowEndpoints reports whether discovery advertises the endpoints the
// authorization-code flow needs. end_session_endpoint is optional: a provider
// without one degrades to local logout.
func validFlowEndpoints(discovery discoveryDocument) bool {
	for _, raw := range []string{discovery.AuthorizationEndpoint, discovery.TokenEndpoint} {
		parsed, err := url.Parse(raw)
		if err != nil || !validProviderURL(parsed) || parsed.User != nil {
			return false
		}
	}
	if discovery.EndSessionEndpoint == "" {
		return true
	}
	parsed, err := url.Parse(discovery.EndSessionEndpoint)
	return err == nil && validProviderURL(parsed) && parsed.User == nil
}
```

In `NewOIDCVerifier`, immediately after the existing `discovery.Issuer != cfg.IssuerURL.String()` check:

```go
	if !validFlowEndpoints(discovery) {
		return nil, ErrVerifierUnavailable
	}
```

In `refreshDiscoveryIfDue`, after the existing issuer mismatch check:

```go
	if !validFlowEndpoints(discovery) {
		return errors.New("OIDC discovery is missing flow endpoints")
	}
```

Create `internal/authn/endpoints.go`:

```go
package authn

// Endpoints are the provider URLs the authorization-code flow needs, read from
// the same discovery document the verifier validates tokens against. Serving
// them from the verifier means one fetch, one TTL, and no possibility of the
// flow and the verifier disagreeing about which provider they are talking to.
type Endpoints struct {
	Authorization string
	Token         string
	// EndSession is empty when the provider does not advertise one.
	EndSession string
}

// Endpoints returns the currently discovered provider endpoints.
func (v *OIDCVerifier) Endpoints() Endpoints {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return Endpoints{
		Authorization: v.discovery.AuthorizationEndpoint,
		Token:         v.discovery.TokenEndpoint,
		EndSession:    v.discovery.EndSessionEndpoint,
	}
}
```

- [ ] **Step 4: Run the tests**

Run: `gofmt -l internal/authn && go test ./internal/authn/...`
Expected: PASS — including every pre-existing verifier test, because the test server now serves the required endpoints by default

- [ ] **Step 5: Commit**

```bash
git add internal/authn
git commit -m "feat(authn): expose the IdP's authorization, token, and end-session endpoints"
```

---

### Task 4: Carry `nonce` and `exp` off the verified token

The callback compares the ID token's `nonce` against the one it sealed, and `/v1/me` reports when the session ends. Both values are already inside the token the verifier parses.

**Files:**
- Modify: `internal/authn/verifier.go:26-33`
- Modify: `internal/authn/oidc_verifier.go:110-160`
- Modify: `internal/authn/principal.go`
- Modify: `internal/authn/oidc_verifier_test.go`

**Interfaces:**
- Consumes: nothing
- Produces: `authn.VerifiedToken.Nonce string`, `authn.VerifiedToken.ExpiresAt time.Time`, `authn.Principal.ExpiresAt time.Time`

- [ ] **Step 1: Write the failing test**

Add to `internal/authn/oidc_verifier_test.go`. Match the token-minting helper the file already uses for `TestOIDCVerifierVerifiesValidAccessTokenAndExtractsIdentity` — read that test and copy its construction, adding a `nonce` claim:

```go
func TestOIDCVerifierExtractsNonceAndExpiry(t *testing.T) {
	server := newOIDCTestServer(t)
	server.addRSAKey(t, "kid-1")
	server.publish("kid-1")
	now := time.Now()
	expiry := now.Add(time.Hour).Truncate(time.Second)

	verifier, err := NewOIDCVerifier(context.Background(), server.config(now))
	if err != nil {
		t.Fatalf("NewOIDCVerifier returned error: %v", err)
	}
	t.Cleanup(func() { _ = verifier.Close(context.Background()) })

	raw := server.signedToken(t, "kid-1", map[string]any{
		"iss":   server.issuer,
		"aud":   "test-audience",
		"sub":   "user-123",
		"exp":   expiry.Unix(),
		"nonce": "nonce-value",
	})

	verified, err := verifier.Verify(context.Background(), raw)
	if err != nil {
		t.Fatalf("Verify returned error: %v", err)
	}
	if verified.Nonce != "nonce-value" {
		t.Fatalf("Nonce = %q, want nonce-value", verified.Nonce)
	}
	if !verified.ExpiresAt.Equal(expiry) {
		t.Fatalf("ExpiresAt = %v, want %v", verified.ExpiresAt, expiry)
	}
}

func TestOIDCVerifierAcceptsTokenWithoutNonce(t *testing.T) {
	// nonce is optional in the code flow. An absent claim is a valid token, not
	// a malformed one; the callback is what decides whether it needed to match.
	server := newOIDCTestServer(t)
	server.addRSAKey(t, "kid-1")
	server.publish("kid-1")
	now := time.Now()

	verifier, err := NewOIDCVerifier(context.Background(), server.config(now))
	if err != nil {
		t.Fatalf("NewOIDCVerifier returned error: %v", err)
	}
	t.Cleanup(func() { _ = verifier.Close(context.Background()) })

	raw := server.signedToken(t, "kid-1", map[string]any{
		"iss": server.issuer,
		"aud": "test-audience",
		"sub": "user-123",
		"exp": now.Add(time.Hour).Unix(),
	})

	verified, err := verifier.Verify(context.Background(), raw)
	if err != nil {
		t.Fatalf("Verify returned error: %v", err)
	}
	if verified.Nonce != "" {
		t.Fatalf("Nonce = %q, want empty", verified.Nonce)
	}
}
```

If the test file has no `signedToken` helper with that signature, add one modelled on however the existing tests build and sign a JWT with `jwt.NewBuilder` and `jwt.Sign`, taking a claims map so callers can set `nonce`.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/authn/ -run "TestOIDCVerifier(ExtractsNonce|AcceptsTokenWithout)" -v`
Expected: FAIL — `verified.Nonce undefined`

- [ ] **Step 3: Add the fields**

In `internal/authn/verifier.go`:

```go
type VerifiedToken struct {
	Subject           string
	Name              string
	PreferredUsername string
	Email             string
	// Nonce is the token's nonce claim, empty when absent. It is compared
	// against the login transaction only at the callback; the middleware
	// ignores it.
	Nonce string
	// ExpiresAt is the token's exp claim. It is presentation only: the API
	// enforces expiry during verification, and this is what the SPA uses to
	// re-authenticate before it is interrupted.
	ExpiresAt time.Time
}
```

In `internal/authn/oidc_verifier.go`, inside `validatedToken`, after the `email` block:

```go
	nonce, ok := optionalStringClaim(token, "nonce")
	if !ok {
		return VerifiedToken{}, ErrInvalidToken
	}
	expiresAt, ok := token.Expiration()
	if !ok {
		return VerifiedToken{}, ErrInvalidToken
	}
	return VerifiedToken{
		Subject:           subject,
		Name:              name,
		PreferredUsername: preferredUsername,
		Email:             email,
		Nonce:             nonce,
		ExpiresAt:         expiresAt,
	}, nil
```

In `internal/authn/principal.go`, add `ExpiresAt time.Time` to `Principal` (importing `"time"`) and set it in `principalFromVerifiedToken`. Extend the doc comment: expiry is identity metadata, not an authorization input.

- [ ] **Step 4: Run the tests**

Run: `gofmt -l internal/authn && go test ./internal/authn/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/authn
git commit -m "feat(authn): carry nonce and expiry off the verified token"
```

---

### Task 5: Session and transaction cookies

Two cookies with opposite trust models. `tflive_session` holds the raw ID token and needs no encryption — it is a signed JWT, and tampering is caught by verification. `tflive_auth_tx` holds `state`, `nonce`, `code_verifier`, and `return_to`, and **must** be sealed: `state` only defends against login-CSRF if the browser cannot forge it.

**Files:**
- Create: `internal/authn/session.go`
- Create: `internal/authn/session_test.go`

**Interfaces:**
- Consumes: `secrets.NewCipher` (Task 1)
- Produces:
  - `authn.SessionCookieName`, `authn.TransactionCookieName` (string constants)
  - `authn.Transaction{State, Nonce, CodeVerifier, ReturnTo string}`
  - `authn.SealTransaction(*secrets.Cipher, Transaction) (string, error)`
  - `authn.OpenTransaction(*secrets.Cipher, string) (Transaction, error)`
  - `authn.SafeReturnTo(string) string`
  - `authn.SessionCookie(value string, secure bool) *http.Cookie`
  - `authn.TransactionCookie(value string, secure bool) *http.Cookie`
  - `authn.ClearedSessionCookie(secure bool) *http.Cookie`
  - `authn.ClearedTransactionCookie(secure bool) *http.Cookie`

- [ ] **Step 1: Write the failing tests**

Create `internal/authn/session_test.go`:

```go
package authn

import (
	"net/http"
	"testing"

	"github.com/vishu42/tflive/internal/secrets"
)

func testCipher(t *testing.T) *secrets.Cipher {
	t.Helper()
	cipher, err := secrets.NewCipher("01234567890123456789012345678901")
	if err != nil {
		t.Fatalf("NewCipher returned error: %v", err)
	}
	return cipher
}

func TestTransactionSealRoundTrip(t *testing.T) {
	cipher := testCipher(t)
	want := Transaction{State: "state-1", Nonce: "nonce-1", CodeVerifier: "verifier-1", ReturnTo: "/stacks/abc"}

	sealed, err := SealTransaction(cipher, want)
	if err != nil {
		t.Fatalf("SealTransaction returned error: %v", err)
	}
	for _, secret := range []string{want.State, want.Nonce, want.CodeVerifier, want.ReturnTo} {
		if contains(sealed, secret) {
			t.Fatalf("sealed transaction leaked %q: %s", secret, sealed)
		}
	}

	got, err := OpenTransaction(cipher, sealed)
	if err != nil {
		t.Fatalf("OpenTransaction returned error: %v", err)
	}
	if got != want {
		t.Fatalf("transaction = %#v, want %#v", got, want)
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

func TestOpenTransactionRejectsTampering(t *testing.T) {
	// A forgeable transaction cookie makes state worthless: an attacker who can
	// set it can set the query parameter too, and login-CSRF follows.
	cipher := testCipher(t)
	sealed, err := SealTransaction(cipher, Transaction{State: "state-1"})
	if err != nil {
		t.Fatalf("SealTransaction returned error: %v", err)
	}
	tampered := []byte(sealed)
	tampered[len(tampered)-1] ^= 'A'
	if _, err := OpenTransaction(cipher, string(tampered)); err == nil {
		t.Fatal("OpenTransaction accepted a tampered value")
	}
}

func TestOpenTransactionRejectsAnotherKey(t *testing.T) {
	sealed, err := SealTransaction(testCipher(t), Transaction{State: "state-1"})
	if err != nil {
		t.Fatalf("SealTransaction returned error: %v", err)
	}
	other, err := secrets.NewCipher("abcdefghijklmnopqrstuvwxyz123456")
	if err != nil {
		t.Fatalf("NewCipher returned error: %v", err)
	}
	if _, err := OpenTransaction(other, sealed); err == nil {
		t.Fatal("OpenTransaction accepted a value sealed with another key")
	}
}

func TestSafeReturnTo(t *testing.T) {
	for _, test := range []struct{ name, in, want string }{
		{name: "empty", in: "", want: "/"},
		{name: "path", in: "/stacks/abc", want: "/stacks/abc"},
		{name: "path with query", in: "/stacks?tab=runs", want: "/stacks?tab=runs"},
		{name: "protocol relative", in: "//evil.test/steal", want: "/"},
		{name: "backslash relative", in: `/\evil.test`, want: "/"},
		{name: "absolute url", in: "https://evil.test/steal", want: "/"},
		{name: "scheme relative uppercase", in: "//EVIL.test", want: "/"},
		{name: "relative", in: "stacks", want: "/"},
		{name: "parent traversal", in: "/../../etc", want: "/"},
		{name: "control character", in: "/stacks\n/evil", want: "/"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := SafeReturnTo(test.in); got != test.want {
				t.Fatalf("SafeReturnTo(%q) = %q, want %q", test.in, got, test.want)
			}
		})
	}
}

func TestCookieAttributes(t *testing.T) {
	session := SessionCookie("token", true)
	if session.Name != SessionCookieName || !session.HttpOnly || !session.Secure ||
		session.SameSite != http.SameSiteLaxMode || session.Path != "/" {
		t.Fatalf("session cookie = %#v", session)
	}
	if session.MaxAge != 0 {
		t.Fatalf("session cookie MaxAge = %d, want 0 so it dies with the browser session", session.MaxAge)
	}

	transaction := TransactionCookie("sealed", false)
	if transaction.Name != TransactionCookieName || !transaction.HttpOnly || transaction.Secure ||
		transaction.SameSite != http.SameSiteLaxMode || transaction.Path != "/v1/auth" || transaction.MaxAge != 600 {
		t.Fatalf("transaction cookie = %#v", transaction)
	}

	if cleared := ClearedSessionCookie(true); cleared.Value != "" || cleared.MaxAge != -1 {
		t.Fatalf("cleared session cookie = %#v", cleared)
	}
	if cleared := ClearedTransactionCookie(true); cleared.Value != "" || cleared.MaxAge != -1 || cleared.Path != "/v1/auth" {
		t.Fatalf("cleared transaction cookie = %#v", cleared)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/authn/ -run "TestTransaction|TestOpenTransaction|TestSafeReturnTo|TestCookieAttributes" -v`
Expected: FAIL — `undefined: Transaction`

- [ ] **Step 3: Implement**

Create `internal/authn/session.go`:

```go
package authn

import (
	"encoding/json"
	"net/http"
	"net/url"
	"path"
	"strings"
	"unicode"

	"github.com/vishu42/tflive/internal/secrets"
)

const (
	// SessionCookieName holds the IdP's raw ID token. It is deliberately not
	// encrypted: it is a signed JWT whose contents are the user's own claims,
	// and tampering is caught by verification.
	SessionCookieName = "tflive_session"
	// TransactionCookieName holds the in-flight login, sealed. state is only
	// meaningful if the browser cannot forge it.
	TransactionCookieName = "tflive_auth_tx"
	// transactionMaxAge bounds how long a login may sit half-finished.
	transactionMaxAge = 600
	// transactionCookiePath scopes the transaction cookie to the routes that
	// read it, so it is not attached to every API call.
	transactionCookiePath = "/v1/auth"
)

// Transaction is the state carried between the login redirect and the callback.
type Transaction struct {
	State        string `json:"state"`
	Nonce        string `json:"nonce"`
	CodeVerifier string `json:"code_verifier"`
	ReturnTo     string `json:"return_to"`
}

// SealTransaction encrypts a transaction for storage in a cookie.
func SealTransaction(cipher *secrets.Cipher, transaction Transaction) (string, error) {
	encoded, err := json.Marshal(transaction)
	if err != nil {
		return "", err
	}
	return cipher.Encrypt(string(encoded))
}

// OpenTransaction authenticates and decodes a sealed transaction. Any failure
// means the value was forged, truncated, or sealed under a different key.
func OpenTransaction(cipher *secrets.Cipher, sealed string) (Transaction, error) {
	plaintext, err := cipher.Decrypt(sealed)
	if err != nil {
		return Transaction{}, err
	}
	var transaction Transaction
	if err := json.Unmarshal([]byte(plaintext), &transaction); err != nil {
		return Transaction{}, err
	}
	return transaction, nil
}

// SafeReturnTo reduces a caller-supplied post-login destination to a same-origin
// path, falling back to "/". It is the open-redirect guard on /v1/auth/login,
// which is the one place the flow accepts untrusted input.
func SafeReturnTo(raw string) string {
	if raw == "" || !strings.HasPrefix(raw, "/") {
		return "/"
	}
	// "//host" and "/\host" are both read as protocol-relative URLs by browsers.
	if strings.HasPrefix(raw, "//") || strings.HasPrefix(raw, `/\`) {
		return "/"
	}
	if strings.ContainsFunc(raw, func(r rune) bool { return unicode.IsControl(r) }) {
		return "/"
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || parsed.User != nil {
		return "/"
	}
	if parsed.Path != path.Clean(parsed.Path) {
		return "/"
	}
	return raw
}

// SessionCookie carries the ID token for the life of the browser session.
func SessionCookie(value string, secure bool) *http.Cookie {
	return &http.Cookie{
		Name:     SessionCookieName,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		// Lax, never Strict: the IdP's callback is a cross-site top-level GET,
		// and Strict would withhold the cookie and break every login.
		SameSite: http.SameSiteLaxMode,
	}
}

// TransactionCookie carries one in-flight login.
func TransactionCookie(value string, secure bool) *http.Cookie {
	return &http.Cookie{
		Name:     TransactionCookieName,
		Value:    value,
		Path:     transactionCookiePath,
		MaxAge:   transactionMaxAge,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	}
}

// ClearedSessionCookie expires the session cookie.
func ClearedSessionCookie(secure bool) *http.Cookie {
	cookie := SessionCookie("", secure)
	cookie.MaxAge = -1
	return cookie
}

// ClearedTransactionCookie expires the transaction cookie.
func ClearedTransactionCookie(secure bool) *http.Cookie {
	cookie := TransactionCookie("", secure)
	cookie.MaxAge = -1
	return cookie
}
```

- [ ] **Step 4: Run the tests**

Run: `gofmt -l internal/authn && go test ./internal/authn/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/authn/session.go internal/authn/session_test.go
git commit -m "feat(authn): add sealed login transaction and session cookies"
```

---

### Task 6: The OIDC client flow

The grant mechanics: build the authorization URL, and redeem the code at the token endpoint with the client secret. `golang.org/x/oauth2` carries this — it also supplies PKCE helpers, so no code-challenge hashing is hand-rolled. Verification is deliberately *not* here: the handler calls the same `Verifier.Verify` the middleware uses, so there is one place a token becomes an identity.

**Files:**
- Create: `internal/authn/flow.go`
- Create: `internal/authn/flow_test.go`
- Modify: `go.mod`, `go.sum`

**Interfaces:**
- Consumes: `authn.Endpoints` (Task 3)
- Produces:
  - `authn.EndpointSource` interface with `Endpoints() Endpoints`
  - `authn.FlowConfig{ClientID, ClientSecret, RedirectURI string; Scopes []string; Endpoints EndpointSource; HTTPClient *http.Client}`
  - `authn.NewFlow(FlowConfig) (*Flow, error)`
  - `(*Flow).AuthorizationURL(state, nonce, codeVerifier string) (string, error)`
  - `(*Flow).Exchange(ctx context.Context, code, codeVerifier string) (rawIDToken string, err error)`
  - `(*Flow).EndSessionURL(idTokenHint, postLogoutRedirectURI string) string`
  - `authn.GenerateVerifier() string`
  - `authn.ErrFlowMisconfigured`

- [ ] **Step 1: Add the dependency**

Run: `go get golang.org/x/oauth2@latest`

Confirm `go.mod` lists it under the first `require` block (direct), and that nothing else was added beyond `golang.org/x/oauth2` and its own indirect needs.

- [ ] **Step 2: Write the failing tests**

Create `internal/authn/flow_test.go`:

```go
package authn

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

type staticEndpoints struct{ endpoints Endpoints }

func (s staticEndpoints) Endpoints() Endpoints { return s.endpoints }

func newTestFlow(t *testing.T, endpoints Endpoints) *Flow {
	t.Helper()
	flow, err := NewFlow(FlowConfig{
		ClientID:     "tflive-api",
		ClientSecret: "client-secret",
		RedirectURI:  "http://localhost:5173/v1/auth/callback",
		Endpoints:    staticEndpoints{endpoints: endpoints},
	})
	if err != nil {
		t.Fatalf("NewFlow returned error: %v", err)
	}
	return flow
}

func TestAuthorizationURLCarriesFlowParameters(t *testing.T) {
	flow := newTestFlow(t, Endpoints{
		Authorization: "https://idp.test/authorize",
		Token:         "https://idp.test/token",
	})

	raw, err := flow.AuthorizationURL("state-1", "nonce-1", "verifier-1-verifier-1-verifier-1-verifier")
	if err != nil {
		t.Fatalf("AuthorizationURL returned error: %v", err)
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("url.Parse returned error: %v", err)
	}
	if parsed.Host != "idp.test" || parsed.Path != "/authorize" {
		t.Fatalf("authorization URL = %q", raw)
	}
	query := parsed.Query()
	for name, want := range map[string]string{
		"response_type":         "code",
		"client_id":             "tflive-api",
		"redirect_uri":          "http://localhost:5173/v1/auth/callback",
		"scope":                 "openid profile email",
		"state":                 "state-1",
		"nonce":                 "nonce-1",
		"code_challenge_method": "S256",
	} {
		if got := query.Get(name); got != want {
			t.Fatalf("%s = %q, want %q", name, got, want)
		}
	}
	if query.Get("code_challenge") == "" {
		t.Fatal("code_challenge is missing")
	}
	// The challenge is a hash; the verifier must never reach the front channel.
	if query.Get("code_challenge") == "verifier-1-verifier-1-verifier-1-verifier" {
		t.Fatal("code_challenge is the raw verifier")
	}
	if query.Has("client_secret") {
		t.Fatal("authorization URL carries the client secret")
	}
	if query.Has("offline_access") || query.Get("scope") == "openid profile email offline_access" {
		t.Fatal("authorization URL requests offline_access")
	}
}

func TestExchangeSendsClientCredentialsAndReturnsIDToken(t *testing.T) {
	var gotForm url.Values
	var gotUser, gotPassword string
	var gotBasic bool

	idp := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if err := request.ParseForm(); err != nil {
			t.Errorf("ParseForm returned error: %v", err)
		}
		gotForm = request.PostForm
		gotUser, gotPassword, gotBasic = request.BasicAuth()
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"access_token": "access-token",
			"id_token":     "raw.id.token",
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	}))
	t.Cleanup(idp.Close)

	flow := newTestFlow(t, Endpoints{Authorization: idp.URL + "/authorize", Token: idp.URL + "/token"})

	rawIDToken, err := flow.Exchange(context.Background(), "code-1", "verifier-1-verifier-1-verifier-1-verifier")
	if err != nil {
		t.Fatalf("Exchange returned error: %v", err)
	}
	if rawIDToken != "raw.id.token" {
		t.Fatalf("id token = %q", rawIDToken)
	}
	if !gotBasic || gotUser != "tflive-api" || gotPassword != "client-secret" {
		t.Fatalf("client authentication = %q/%q basic=%t", gotUser, gotPassword, gotBasic)
	}
	if gotForm.Get("grant_type") != "authorization_code" {
		t.Fatalf("grant_type = %q", gotForm.Get("grant_type"))
	}
	if gotForm.Get("code") != "code-1" {
		t.Fatalf("code = %q", gotForm.Get("code"))
	}
	if gotForm.Get("code_verifier") != "verifier-1-verifier-1-verifier-1-verifier" {
		t.Fatalf("code_verifier = %q", gotForm.Get("code_verifier"))
	}
	// RFC 6749 4.1.3: the redirect URI is repeated and must match, binding the
	// code to the URI it was issued for.
	if gotForm.Get("redirect_uri") != "http://localhost:5173/v1/auth/callback" {
		t.Fatalf("redirect_uri = %q", gotForm.Get("redirect_uri"))
	}
}

func TestExchangeFailsWhenResponseHasNoIDToken(t *testing.T) {
	idp := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"access_token":"access-token","token_type":"Bearer"}`))
	}))
	t.Cleanup(idp.Close)

	flow := newTestFlow(t, Endpoints{Authorization: idp.URL + "/authorize", Token: idp.URL + "/token"})
	if _, err := flow.Exchange(context.Background(), "code-1", "verifier"); err == nil {
		t.Fatal("Exchange accepted a response with no id_token")
	}
}

func TestEndSessionURL(t *testing.T) {
	flow := newTestFlow(t, Endpoints{
		Authorization: "https://idp.test/authorize",
		Token:         "https://idp.test/token",
		EndSession:    "https://idp.test/logout",
	})

	raw := flow.EndSessionURL("raw.id.token", "http://localhost:5173/")
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("url.Parse returned error: %v", err)
	}
	query := parsed.Query()
	if query.Get("id_token_hint") != "raw.id.token" {
		t.Fatalf("id_token_hint = %q", query.Get("id_token_hint"))
	}
	if query.Get("post_logout_redirect_uri") != "http://localhost:5173/" {
		t.Fatalf("post_logout_redirect_uri = %q", query.Get("post_logout_redirect_uri"))
	}
}

func TestEndSessionURLIsEmptyWithoutProviderSupport(t *testing.T) {
	flow := newTestFlow(t, Endpoints{Authorization: "https://idp.test/authorize", Token: "https://idp.test/token"})
	if raw := flow.EndSessionURL("raw.id.token", "http://localhost:5173/"); raw != "" {
		t.Fatalf("EndSessionURL = %q, want empty", raw)
	}
}

func TestNewFlowRejectsIncompleteConfig(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*FlowConfig)
	}{
		{name: "no client id", mutate: func(c *FlowConfig) { c.ClientID = "" }},
		{name: "no client secret", mutate: func(c *FlowConfig) { c.ClientSecret = "" }},
		{name: "relative redirect", mutate: func(c *FlowConfig) { c.RedirectURI = "/v1/auth/callback" }},
		{name: "no endpoints", mutate: func(c *FlowConfig) { c.Endpoints = nil }},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := FlowConfig{
				ClientID:     "tflive-api",
				ClientSecret: "client-secret",
				RedirectURI:  "http://localhost:5173/v1/auth/callback",
				Endpoints:    staticEndpoints{},
			}
			test.mutate(&cfg)
			if _, err := NewFlow(cfg); err == nil {
				t.Fatal("NewFlow accepted an incomplete config")
			}
		})
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/authn/ -run "TestAuthorizationURL|TestExchange|TestEndSession|TestNewFlow" -v`
Expected: FAIL — `undefined: NewFlow`

- [ ] **Step 4: Implement the flow**

Create `internal/authn/flow.go`:

```go
package authn

import (
	"context"
	"errors"
	"net/http"
	"net/url"

	"golang.org/x/oauth2"
)

// ErrFlowMisconfigured means the flow cannot be run as configured. It is
// deliberately opaque: the browser is the wrong place to learn why.
var ErrFlowMisconfigured = errors.New("oidc flow is misconfigured")

// EndpointSource supplies provider endpoints. *OIDCVerifier implements it, so
// the flow and the verifier always read the same discovery document.
type EndpointSource interface {
	Endpoints() Endpoints
}

type FlowConfig struct {
	ClientID     string
	ClientSecret string
	RedirectURI  string
	// Scopes defaults to openid, profile, email. offline_access is never
	// requested: tflive holds no refresh token.
	Scopes     []string
	Endpoints  EndpointSource
	HTTPClient *http.Client
}

// Flow runs the authorization-code grant. It never verifies a token: the
// caller passes the returned ID token to the same Verifier the middleware
// uses, so there is one place where a token becomes an identity.
type Flow struct {
	cfg FlowConfig
}

func NewFlow(cfg FlowConfig) (*Flow, error) {
	if cfg.ClientID == "" || cfg.ClientSecret == "" || cfg.Endpoints == nil {
		return nil, ErrFlowMisconfigured
	}
	parsed, err := url.Parse(cfg.RedirectURI)
	if err != nil || !parsed.IsAbs() || parsed.Hostname() == "" {
		return nil, ErrFlowMisconfigured
	}
	if len(cfg.Scopes) == 0 {
		cfg.Scopes = []string{"openid", "profile", "email"}
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: defaultHTTPTimeout}
	}
	return &Flow{cfg: cfg}, nil
}

// GenerateVerifier returns a fresh PKCE code verifier.
func GenerateVerifier() string { return oauth2.GenerateVerifier() }

func (f *Flow) oauth2Config() (*oauth2.Config, error) {
	endpoints := f.cfg.Endpoints.Endpoints()
	if endpoints.Authorization == "" || endpoints.Token == "" {
		return nil, ErrFlowMisconfigured
	}
	return &oauth2.Config{
		ClientID:     f.cfg.ClientID,
		ClientSecret: f.cfg.ClientSecret,
		RedirectURL:  f.cfg.RedirectURI,
		Scopes:       f.cfg.Scopes,
		Endpoint: oauth2.Endpoint{
			AuthURL:  endpoints.Authorization,
			TokenURL: endpoints.Token,
			// client_secret_basic. Keeping the secret in a header rather than
			// the form body keeps it out of proxy access logs.
			AuthStyle: oauth2.AuthStyleInHeader,
		},
	}, nil
}

// AuthorizationURL builds the front-channel redirect. Everything in it is
// public by construction: the browser can read it. The code challenge is a
// SHA-256 hash, so the verifier never leaves this process.
func (f *Flow) AuthorizationURL(state, nonce, codeVerifier string) (string, error) {
	config, err := f.oauth2Config()
	if err != nil {
		return "", err
	}
	return config.AuthCodeURL(
		state,
		oauth2.SetAuthURLParam("nonce", nonce),
		oauth2.S256ChallengeOption(codeVerifier),
	), nil
}

// Exchange redeems the authorization code on the back channel and returns the
// raw ID token. The access token in the response is discarded: there is no
// resource server to call and no userinfo request, so it is a credential with
// nowhere to go.
func (f *Flow) Exchange(ctx context.Context, code, codeVerifier string) (string, error) {
	config, err := f.oauth2Config()
	if err != nil {
		return "", err
	}
	ctx = context.WithValue(ctx, oauth2.HTTPClient, f.cfg.HTTPClient)
	token, err := config.Exchange(ctx, code, oauth2.VerifierOption(codeVerifier))
	if err != nil {
		return "", err
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		return "", errors.New("token response carries no id_token")
	}
	return rawIDToken, nil
}

// EndSessionURL builds the RP-initiated logout URL, or returns empty when the
// provider does not advertise one. Without it, logging out and back in
// silently returns the same user, because the IdP's own session still stands.
func (f *Flow) EndSessionURL(idTokenHint, postLogoutRedirectURI string) string {
	endpoint := f.cfg.Endpoints.Endpoints().EndSession
	if endpoint == "" {
		return ""
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return ""
	}
	query := parsed.Query()
	query.Set("id_token_hint", idTokenHint)
	query.Set("post_logout_redirect_uri", postLogoutRedirectURI)
	query.Set("client_id", f.cfg.ClientID)
	parsed.RawQuery = query.Encode()
	return parsed.String()
}
```

- [ ] **Step 5: Run the tests**

Run: `gofmt -l internal/authn && go test ./internal/authn/...`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/authn/flow.go internal/authn/flow_test.go go.mod go.sum
git commit -m "feat(authn): add the OIDC authorization-code flow client"
```

---

### Task 7: Accept the session cookie in the middleware

Header first, then cookie. Header precedence keeps a future CLI working and makes the order explicit rather than accidental. Both paths feed the same `verifier.Verify` and produce the same `Principal`.

**Files:**
- Modify: `internal/authn/middleware.go:29-33,45-52`
- Modify: `internal/authn/middleware_test.go`

**Interfaces:**
- Consumes: `authn.SessionCookieName` (Task 5)
- Produces: no new exported symbols

- [ ] **Step 1: Write the failing tests**

Append to `internal/authn/middleware_test.go`:

```go
func TestRequireAuthenticationAcceptsSessionCookie(t *testing.T) {
	valid := VerifiedToken{Subject: "user-123", Name: "Ada"}

	for _, test := range []struct {
		name          string
		authorization string
		cookie        string
		status        int
		wantRaw       string
	}{
		{name: "cookie only", cookie: "cookie-token", status: http.StatusOK, wantRaw: "cookie-token"},
		{name: "header only", authorization: "Bearer header-token", status: http.StatusOK, wantRaw: "header-token"},
		{name: "header wins over cookie", authorization: "Bearer header-token", cookie: "cookie-token", status: http.StatusOK, wantRaw: "header-token"},
		{name: "empty cookie", cookie: "", status: http.StatusUnauthorized},
		{name: "neither", status: http.StatusUnauthorized},
		{name: "malformed header falls back to cookie", authorization: "Basic ignored", cookie: "cookie-token", status: http.StatusOK, wantRaw: "cookie-token"},
	} {
		t.Run(test.name, func(t *testing.T) {
			verifier := &middlewareVerifier{token: valid}
			handler := RequireAuthentication(verifier, "/healthz")(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				response.WriteHeader(http.StatusOK)
			}))
			request := httptest.NewRequest(http.MethodGet, "/v1/stacks", nil)
			if test.authorization != "" {
				request.Header.Set("Authorization", test.authorization)
			}
			if test.cookie != "" {
				request.AddCookie(&http.Cookie{Name: SessionCookieName, Value: test.cookie})
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)

			if response.Code != test.status {
				t.Fatalf("status = %d, want %d", response.Code, test.status)
			}
			if verifier.raw != test.wantRaw {
				t.Fatalf("verified raw = %q, want %q", verifier.raw, test.wantRaw)
			}
		})
	}
}

func TestRequireAuthenticationIgnoresOtherCookies(t *testing.T) {
	verifier := &middlewareVerifier{token: VerifiedToken{Subject: "user-123"}}
	handler := RequireAuthentication(verifier)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("protected handler was called")
	}))
	request := httptest.NewRequest(http.MethodGet, "/v1/stacks", nil)
	request.AddCookie(&http.Cookie{Name: "some_other_cookie", Value: "value"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", response.Code)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/authn/ -run TestRequireAuthentication -v`
Expected: FAIL — the cookie-only case returns 401

- [ ] **Step 3: Implement**

In `internal/authn/middleware.go`, replace the `raw, ok := bearerToken(...)` line with `raw, ok := credential(request)` and add:

```go
// credential reads the caller's token from the Authorization header, falling
// back to the session cookie. The header wins so a CLI or service caller is
// never overridden by a stale browser cookie on the same connection.
func credential(request *http.Request) (string, bool) {
	if raw, ok := bearerToken(request.Header.Get("Authorization")); ok {
		return raw, true
	}
	cookie, err := request.Cookie(SessionCookieName)
	if err != nil || cookie.Value == "" {
		return "", false
	}
	return cookie.Value, true
}
```

- [ ] **Step 4: Run the tests**

Run: `gofmt -l internal/authn && go test ./internal/authn/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/authn/middleware.go internal/authn/middleware_test.go
git commit -m "feat(authn): accept the session cookie as well as a bearer header"
```

---

### Task 8: The three auth routes

The handlers, the server option that carries their dependencies, the extended public path list, and the `cmd/api` wiring that constructs the flow. These land together because none of them is testable alone.

**Files:**
- Create: `internal/api/auth.go`
- Create: `internal/api/auth_test.go`
- Modify: `internal/api/server.go:22-50,125-131`
- Modify: `cmd/api/main.go:125-215`

**Interfaces:**
- Consumes: `authn.Flow` (Task 6), `authn.Transaction`/cookies (Task 5), `authn.Verifier`, `secrets.Cipher` (Task 1), `config.SecurityConfig` (Task 2)
- Produces:
  - `api.AuthFlow` interface: `AuthorizationURL(state, nonce, codeVerifier string) (string, error)`, `Exchange(ctx context.Context, code, codeVerifier string) (string, error)`, `EndSessionURL(idTokenHint, postLogoutRedirectURI string) string`
  - `api.AuthConfig{Flow AuthFlow; Verifier authn.Verifier; Sealer *secrets.Cipher; PublicURL string; SecureCookies bool}`
  - `api.WithAuth(AuthConfig) ServerOption`

- [ ] **Step 1: Write the failing tests**

Create `internal/api/auth_test.go`:

```go
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/vishu42/tflive/internal/authn"
	"github.com/vishu42/tflive/internal/secrets"
)

type stubFlow struct {
	authorizationURL string
	authorizationErr error
	idToken          string
	exchangeErr      error
	endSessionURL    string

	gotState        string
	gotNonce        string
	gotCodeVerifier string
	gotCode         string
	gotIDTokenHint  string
}

func (f *stubFlow) AuthorizationURL(state, nonce, codeVerifier string) (string, error) {
	f.gotState, f.gotNonce, f.gotCodeVerifier = state, nonce, codeVerifier
	return f.authorizationURL, f.authorizationErr
}

func (f *stubFlow) Exchange(_ context.Context, code, codeVerifier string) (string, error) {
	f.gotCode, f.gotCodeVerifier = code, codeVerifier
	return f.idToken, f.exchangeErr
}

func (f *stubFlow) EndSessionURL(idTokenHint, _ string) string {
	f.gotIDTokenHint = idTokenHint
	return f.endSessionURL
}

type stubVerifier struct {
	token authn.VerifiedToken
	err   error
}

func (v stubVerifier) Verify(context.Context, string) (authn.VerifiedToken, error) {
	return v.token, v.err
}

func newAuthTestServer(t *testing.T, flow *stubFlow, verifier authn.Verifier) *Server {
	t.Helper()
	sealer, err := secrets.NewCipher("01234567890123456789012345678901")
	if err != nil {
		t.Fatalf("NewCipher returned error: %v", err)
	}
	return NewServer(nil, "tenant_123", WithAuth(AuthConfig{
		Flow:          flow,
		Verifier:      verifier,
		Sealer:        sealer,
		PublicURL:     "http://localhost:5173",
		SecureCookies: false,
	}))
}

func cookieByName(response *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, cookie := range (&http.Response{Header: response.Header()}).Cookies() {
		if cookie.Name == name {
			return cookie
		}
	}
	return nil
}

func TestAuthLoginRedirectsAndSetsTransactionCookie(t *testing.T) {
	flow := &stubFlow{authorizationURL: "https://idp.test/authorize?state=x"}
	server := newAuthTestServer(t, flow, stubVerifier{})

	request := httptest.NewRequest(http.MethodGet, "/v1/auth/login?return_to=/stacks/abc", nil)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)

	if response.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", response.Code)
	}
	if location := response.Header().Get("Location"); location != flow.authorizationURL {
		t.Fatalf("Location = %q", location)
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q", response.Header().Get("Cache-Control"))
	}

	cookie := cookieByName(response, authn.TransactionCookieName)
	if cookie == nil {
		t.Fatal("transaction cookie is missing")
	}
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("transaction cookie = %#v", cookie)
	}
	if strings.Contains(cookie.Value, flow.gotState) || strings.Contains(cookie.Value, "/stacks/abc") {
		t.Fatal("transaction cookie is not sealed")
	}
	if flow.gotState == "" || flow.gotNonce == "" || flow.gotCodeVerifier == "" {
		t.Fatalf("flow parameters = %q/%q/%q", flow.gotState, flow.gotNonce, flow.gotCodeVerifier)
	}
	if flow.gotState == flow.gotNonce {
		t.Fatal("state and nonce are the same value")
	}
}

func TestAuthLoginRejectsOffOriginReturnTo(t *testing.T) {
	flow := &stubFlow{authorizationURL: "https://idp.test/authorize"}
	server := newAuthTestServer(t, flow, stubVerifier{})
	sealer, _ := secrets.NewCipher("01234567890123456789012345678901")

	request := httptest.NewRequest(http.MethodGet, "/v1/auth/login?return_to=https://evil.test/steal", nil)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)

	cookie := cookieByName(response, authn.TransactionCookieName)
	if cookie == nil {
		t.Fatal("transaction cookie is missing")
	}
	transaction, err := authn.OpenTransaction(sealer, cookie.Value)
	if err != nil {
		t.Fatalf("OpenTransaction returned error: %v", err)
	}
	if transaction.ReturnTo != "/" {
		t.Fatalf("ReturnTo = %q, want /", transaction.ReturnTo)
	}
}

func callbackRequest(t *testing.T, sealer *secrets.Cipher, transaction authn.Transaction, query url.Values) *http.Request {
	t.Helper()
	sealed, err := authn.SealTransaction(sealer, transaction)
	if err != nil {
		t.Fatalf("SealTransaction returned error: %v", err)
	}
	request := httptest.NewRequest(http.MethodGet, "/v1/auth/callback?"+query.Encode(), nil)
	request.AddCookie(&http.Cookie{Name: authn.TransactionCookieName, Value: sealed})
	return request
}

func TestAuthCallbackSetsSessionCookieAndRedirects(t *testing.T) {
	flow := &stubFlow{idToken: "raw.id.token"}
	verifier := stubVerifier{token: authn.VerifiedToken{Subject: "user-123", Nonce: "nonce-1"}}
	server := newAuthTestServer(t, flow, verifier)
	sealer, _ := secrets.NewCipher("01234567890123456789012345678901")

	transaction := authn.Transaction{State: "state-1", Nonce: "nonce-1", CodeVerifier: "verifier-1", ReturnTo: "/stacks/abc"}
	request := callbackRequest(t, sealer, transaction, url.Values{"code": {"code-1"}, "state": {"state-1"}})
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)

	if response.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302; body = %s", response.Code, response.Body.String())
	}
	if location := response.Header().Get("Location"); location != "/stacks/abc" {
		t.Fatalf("Location = %q", location)
	}
	session := cookieByName(response, authn.SessionCookieName)
	if session == nil || session.Value != "raw.id.token" || !session.HttpOnly {
		t.Fatalf("session cookie = %#v", session)
	}
	cleared := cookieByName(response, authn.TransactionCookieName)
	if cleared == nil || cleared.MaxAge != -1 {
		t.Fatalf("transaction cookie = %#v, want cleared", cleared)
	}
	if flow.gotCode != "code-1" || flow.gotCodeVerifier != "verifier-1" {
		t.Fatalf("exchange args = %q/%q", flow.gotCode, flow.gotCodeVerifier)
	}
	if body := response.Body.String(); strings.Contains(body, "raw.id.token") {
		t.Fatal("callback response body carries the token")
	}
}

func TestAuthCallbackFailuresAreIndistinguishable(t *testing.T) {
	sealer, _ := secrets.NewCipher("01234567890123456789012345678901")
	good := authn.Transaction{State: "state-1", Nonce: "nonce-1", CodeVerifier: "verifier-1", ReturnTo: "/stacks"}

	var bodies []string
	for _, test := range []struct {
		name     string
		flow     *stubFlow
		verifier stubVerifier
		build    func(*testing.T) *http.Request
	}{
		{
			name: "idp error",
			flow: &stubFlow{}, verifier: stubVerifier{},
			build: func(t *testing.T) *http.Request {
				return callbackRequest(t, sealer, good, url.Values{"error": {"access_denied"}, "state": {"state-1"}})
			},
		},
		{
			name: "no transaction cookie",
			flow: &stubFlow{}, verifier: stubVerifier{},
			build: func(t *testing.T) *http.Request {
				return httptest.NewRequest(http.MethodGet, "/v1/auth/callback?code=code-1&state=state-1", nil)
			},
		},
		{
			name: "state mismatch",
			flow: &stubFlow{idToken: "raw.id.token"}, verifier: stubVerifier{},
			build: func(t *testing.T) *http.Request {
				return callbackRequest(t, sealer, good, url.Values{"code": {"code-1"}, "state": {"other-state"}})
			},
		},
		{
			name: "exchange fails",
			flow: &stubFlow{exchangeErr: context.Canceled}, verifier: stubVerifier{},
			build: func(t *testing.T) *http.Request {
				return callbackRequest(t, sealer, good, url.Values{"code": {"code-1"}, "state": {"state-1"}})
			},
		},
		{
			name: "verification fails",
			flow: &stubFlow{idToken: "raw.id.token"}, verifier: stubVerifier{err: authn.ErrInvalidToken},
			build: func(t *testing.T) *http.Request {
				return callbackRequest(t, sealer, good, url.Values{"code": {"code-1"}, "state": {"state-1"}})
			},
		},
		{
			name: "nonce mismatch",
			flow: &stubFlow{idToken: "raw.id.token"},
			verifier: stubVerifier{token: authn.VerifiedToken{Subject: "user-123", Nonce: "other-nonce"}},
			build: func(t *testing.T) *http.Request {
				return callbackRequest(t, sealer, good, url.Values{"code": {"code-1"}, "state": {"state-1"}})
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := newAuthTestServer(t, test.flow, test.verifier)
			response := httptest.NewRecorder()
			server.ServeHTTP(response, test.build(t))

			if response.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", response.Code)
			}
			if cookieByName(response, authn.SessionCookieName) != nil {
				t.Fatal("a failed callback set a session cookie")
			}
			bodies = append(bodies, response.Body.String())
		})
	}

	// Distinguishable failures are an oracle: they tell an attacker which check
	// they tripped.
	for i := 1; i < len(bodies); i++ {
		if bodies[i] != bodies[0] {
			t.Fatalf("failure bodies differ:\n%q\n%q", bodies[0], bodies[i])
		}
	}
}

func TestAuthLogoutClearsCookieAndReturnsIdPLogoutURL(t *testing.T) {
	flow := &stubFlow{endSessionURL: "https://idp.test/logout?id_token_hint=raw.id.token"}
	server := newAuthTestServer(t, flow, stubVerifier{})

	request := httptest.NewRequest(http.MethodPost, "/v1/auth/logout", nil)
	request.AddCookie(&http.Cookie{Name: authn.SessionCookieName, Value: "raw.id.token"})
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	cleared := cookieByName(response, authn.SessionCookieName)
	if cleared == nil || cleared.Value != "" || cleared.MaxAge != -1 {
		t.Fatalf("session cookie = %#v, want cleared", cleared)
	}
	if flow.gotIDTokenHint != "raw.id.token" {
		t.Fatalf("id_token_hint = %q", flow.gotIDTokenHint)
	}
	var body struct {
		LogoutURL *string `json:"logoutURL"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.LogoutURL == nil || *body.LogoutURL != flow.endSessionURL {
		t.Fatalf("logoutURL = %v", body.LogoutURL)
	}
}

func TestAuthLogoutWithoutProviderSupportReturnsNull(t *testing.T) {
	server := newAuthTestServer(t, &stubFlow{}, stubVerifier{})

	request := httptest.NewRequest(http.MethodPost, "/v1/auth/logout", nil)
	request.AddCookie(&http.Cookie{Name: authn.SessionCookieName, Value: "raw.id.token"})
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)

	var body struct {
		LogoutURL *string `json:"logoutURL"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.LogoutURL != nil {
		t.Fatalf("logoutURL = %v, want null", *body.LogoutURL)
	}
}

func TestAuthRoutesArePublic(t *testing.T) {
	// The middleware must let the login routes through: a user with no session
	// cannot obtain one from behind an authentication gate.
	flow := &stubFlow{authorizationURL: "https://idp.test/authorize"}
	sealer, _ := secrets.NewCipher("01234567890123456789012345678901")
	server := NewAuthenticatedServer(nil, stubVerifier{err: authn.ErrInvalidToken}, "tenant_123", false,
		WithAuth(AuthConfig{Flow: flow, Verifier: stubVerifier{}, Sealer: sealer, PublicURL: "http://localhost:5173"}))

	request := httptest.NewRequest(http.MethodGet, "/v1/auth/login", nil)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)

	if response.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302 — /v1/auth/login is behind the auth gate", response.Code)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/api/ -run TestAuth -v`
Expected: FAIL — `undefined: WithAuth`

- [ ] **Step 3: Add the server option**

In `internal/api/server.go`, add to the imports `"context"` and `"github.com/vishu42/tflive/internal/secrets"`, add the field `auth AuthConfig` to `Server`, and add:

```go
// AuthFlow is the subset of the OIDC flow the handlers use. *authn.Flow
// satisfies it; the interface exists so handler tests need no live IdP.
type AuthFlow interface {
	AuthorizationURL(state, nonce, codeVerifier string) (string, error)
	Exchange(ctx context.Context, code, codeVerifier string) (string, error)
	EndSessionURL(idTokenHint, postLogoutRedirectURI string) string
}

// AuthConfig carries what the browser login routes need.
type AuthConfig struct {
	Flow     AuthFlow
	Verifier authn.Verifier
	Sealer   *secrets.Cipher
	// PublicURL is the origin the browser reaches, with no trailing slash. It
	// is configured rather than derived from Host or X-Forwarded-Proto, which
	// a caller can spoof.
	PublicURL     string
	SecureCookies bool
}

// WithAuth enables the browser login routes.
func WithAuth(cfg AuthConfig) ServerOption {
	return func(server *Server) { server.auth = cfg }
}
```

In `NewServer`, after the option loop and before the health route:

```go
	// Browser login routes. Registered only when auth is configured, so an
	// unauthenticated test server does not expose a half-wired flow.
	if server.auth.Flow != nil {
		server.mux.HandleFunc("GET /v1/auth/login", server.handleAuthLogin)
		server.mux.HandleFunc("GET /v1/auth/callback", server.handleAuthCallback)
		server.mux.HandleFunc("POST /v1/auth/logout", server.handleAuthLogout)
	}
```

In `NewAuthenticatedServer`, extend the public path list:

```go
	server.handler = authn.RequireAuthentication(verifier,
		"/healthz", "/v1/auth/login", "/v1/auth/callback", "/v1/auth/logout",
	)(server.mux)
```

- [ ] **Step 4: Write the handlers**

Create `internal/api/auth.go`:

```go
package api

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"log"
	"net/http"

	"github.com/vishu42/tflive/internal/authn"
)

// authFailureBody is the single response every authentication failure renders.
// Distinguishing "bad state" from "expired code" from "nonce mismatch" would
// tell an attacker which check they tripped.
const authFailureBody = `<!doctype html><meta charset="utf-8"><title>Sign-in failed</title>` +
	`<p>Sign-in could not be completed. <a href="/v1/auth/login">Try again</a>.</p>`

type logoutResponse struct {
	LogoutURL *string `json:"logoutURL"`
}

// handleAuthLogin starts the flow. It makes no network call: the authorization
// endpoint comes from discovery the verifier already cached.
func (server *Server) handleAuthLogin(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "no-store")

	state, stateErr := randomToken()
	nonce, nonceErr := randomToken()
	if stateErr != nil || nonceErr != nil {
		server.writeAuthFailure(response)
		return
	}
	transaction := authn.Transaction{
		State:        state,
		Nonce:        nonce,
		CodeVerifier: authn.GenerateVerifier(),
		ReturnTo:     authn.SafeReturnTo(request.URL.Query().Get("return_to")),
	}

	sealed, err := authn.SealTransaction(server.auth.Sealer, transaction)
	if err != nil {
		server.writeAuthFailure(response)
		return
	}
	authorizationURL, err := server.auth.Flow.AuthorizationURL(transaction.State, transaction.Nonce, transaction.CodeVerifier)
	if err != nil {
		server.writeAuthFailure(response)
		return
	}

	http.SetCookie(response, authn.TransactionCookie(sealed, server.auth.SecureCookies))
	http.Redirect(response, request, authorizationURL, http.StatusFound)
}

// handleAuthCallback finishes the flow: redeem the code on the back channel,
// verify the ID token, and hand the browser a session cookie. It redirects
// rather than rendering, so the authorization code leaves the address bar and
// the browser history.
func (server *Server) handleAuthCallback(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "no-store")
	http.SetCookie(response, authn.ClearedTransactionCookie(server.auth.SecureCookies))

	query := request.URL.Query()
	if query.Get("error") != "" {
		server.writeAuthFailure(response)
		return
	}
	cookie, err := request.Cookie(authn.TransactionCookieName)
	if err != nil || cookie.Value == "" {
		server.writeAuthFailure(response)
		return
	}
	transaction, err := authn.OpenTransaction(server.auth.Sealer, cookie.Value)
	if err != nil {
		server.writeAuthFailure(response)
		return
	}
	if subtle.ConstantTimeCompare([]byte(transaction.State), []byte(query.Get("state"))) != 1 {
		server.writeAuthFailure(response)
		return
	}
	code := query.Get("code")
	if code == "" {
		server.writeAuthFailure(response)
		return
	}

	rawIDToken, err := server.auth.Flow.Exchange(request.Context(), code, transaction.CodeVerifier)
	if err != nil {
		server.writeAuthFailure(response)
		return
	}
	verified, err := server.auth.Verifier.Verify(request.Context(), rawIDToken)
	if err != nil {
		server.writeAuthFailure(response)
		return
	}
	if subtle.ConstantTimeCompare([]byte(verified.Nonce), []byte(transaction.Nonce)) != 1 {
		server.writeAuthFailure(response)
		return
	}

	server.warnOnOversizedSession(rawIDToken)
	http.SetCookie(response, authn.SessionCookie(rawIDToken, server.auth.SecureCookies))
	http.Redirect(response, request, authn.SafeReturnTo(transaction.ReturnTo), http.StatusFound)
}

// handleAuthLogout clears our session and reports where to end the IdP's.
// Without the second half, logging out and back in silently returns the same
// user, because the provider's SSO session still stands.
func (server *Server) handleAuthLogout(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "no-store")

	var idTokenHint string
	if cookie, err := request.Cookie(authn.SessionCookieName); err == nil {
		idTokenHint = cookie.Value
	}
	http.SetCookie(response, authn.ClearedSessionCookie(server.auth.SecureCookies))

	body := logoutResponse{}
	if idTokenHint != "" {
		if logoutURL := server.auth.Flow.EndSessionURL(idTokenHint, server.auth.PublicURL+"/"); logoutURL != "" {
			body.LogoutURL = &logoutURL
		}
	}
	writeJSON(response, http.StatusOK, body)
}

func (server *Server) writeAuthFailure(response http.ResponseWriter) {
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.WriteHeader(http.StatusUnauthorized)
	_, _ = response.Write([]byte(authFailureBody))
}

// warnOnOversizedSession logs when an ID token approaches the 4096-byte cookie
// limit. Past it, browsers drop the cookie silently and the user simply never
// appears logged in. A provider that stuffs group or role claims into the ID
// token is the realistic way to get there.
func (server *Server) warnOnOversizedSession(rawIDToken string) {
	const warnThresholdBytes = 3072
	if len(rawIDToken) > warnThresholdBytes {
		log.Printf("id token is %d bytes, approaching the 4096-byte cookie limit; sessions will break silently past it", len(rawIDToken))
	}
}

func randomToken() (string, error) {
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}
```

- [ ] **Step 5: Run the handler tests**

Run: `gofmt -l internal/api && go test ./internal/api/...`
Expected: PASS, including the pre-existing `server_test.go`

- [ ] **Step 6: Wire it in `cmd/api`**

In `runWithDependencies`, after the verifier is constructed:

```go
	sessionSealer, err := secrets.NewCipher(cfg.Security.SessionEncryptionKey.Value())
	if err != nil {
		return fmt.Errorf("create session sealer: %w", err)
	}
	publicURL := strings.TrimRight(cfg.Security.PublicURL.String(), "/")
	flow, err := authn.NewFlow(authn.FlowConfig{
		ClientID:     cfg.Security.OIDC.ClientID,
		ClientSecret: cfg.Security.OIDC.ClientSecret.Value(),
		RedirectURI:  publicURL + "/v1/auth/callback",
		Endpoints:    verifier,
		HTTPClient:   &http.Client{Timeout: 10 * time.Second},
	})
	if err != nil {
		return fmt.Errorf("create oidc flow: %w", err)
	}
```

`deps.newVerifier` returns the `tokenVerifier` interface, which does not expose `Endpoints()`. Add it to that interface so the flow can consume the verifier directly:

```go
type tokenVerifier interface {
	authn.Verifier
	authn.EndpointSource
	Close(context.Context) error
}
```

Then update the two test doubles in `cmd/api/main_test.go` (`testTokenVerifier` and `recordingTokenVerifier`) with:

```go
func (testTokenVerifier) Endpoints() authn.Endpoints {
	return authn.Endpoints{Authorization: "https://idp.test/authorize", Token: "https://idp.test/token"}
}
```

and the same method on `recordingTokenVerifier`.

Finally, pass the option to the server:

```go
	handler := api.NewAuthenticatedServer(service, verifier, cfg.Security.TenantID, cfg.Debug,
		api.WithQueueReader(store),
		api.WithAuth(api.AuthConfig{
			Flow:          flow,
			Verifier:      verifier,
			Sealer:        sessionSealer,
			PublicURL:     publicURL,
			SecureCookies: cfg.Security.Mode == config.RuntimeProduction,
		}),
	)
```

Add `"strings"` to `cmd/api/main.go`'s imports if absent.

- [ ] **Step 7: Verify the whole backend**

Run: `gofmt -l cmd internal && go build ./... && go test ./...`
Expected: PASS

- [ ] **Step 8: Commit**

```bash
git add internal/api cmd/api
git commit -m "feat(api): add server-side login, callback, and logout routes"
```

---

### Task 9: Report session expiry from `/v1/me`

The SPA needs to know when the session ends so it can re-authenticate at a moment of its choosing rather than being interrupted by a 401.

**Files:**
- Modify: `internal/auth/me.go`
- Modify: `internal/api/server.go` (`handleMe` needs no change if `MeFromPrincipal` reads the principal)
- Modify: `internal/api/server_test.go` (the `/v1/me` assertions)

**Interfaces:**
- Consumes: `authn.Principal.ExpiresAt` (Task 4)
- Produces: `auth.MeResponse.SessionExpiresAt string` with JSON name `sessionExpiresAt`

- [ ] **Step 1: Write the failing test**

Add to `internal/auth/me_test.go` (create the file if it does not exist):

```go
package auth

import (
	"testing"
	"time"

	"github.com/vishu42/tflive/internal/authn"
)

func TestMeFromPrincipalReportsSessionExpiry(t *testing.T) {
	expiry := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	me := MeFromPrincipal(
		authn.Principal{Subject: "user-123", Name: "Ada", ExpiresAt: expiry},
		"tenant_123",
		GlobalCapabilities{},
	)
	if me.SessionExpiresAt != "2026-08-25T12:00:00Z" {
		t.Fatalf("SessionExpiresAt = %q", me.SessionExpiresAt)
	}
}

func TestMeFromPrincipalOmitsZeroExpiry(t *testing.T) {
	me := MeFromPrincipal(authn.Principal{Subject: "user-123"}, "tenant_123", GlobalCapabilities{})
	if me.SessionExpiresAt != "" {
		t.Fatalf("SessionExpiresAt = %q, want empty", me.SessionExpiresAt)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/auth/...`
Expected: FAIL — `me.SessionExpiresAt undefined`

- [ ] **Step 3: Implement**

In `internal/auth/me.go`, add to `MeResponse` after `TenantID`:

```go
	// SessionExpiresAt is when the ID token backing this session expires, in
	// RFC 3339. It lets the web client re-authenticate at a quiet moment
	// instead of being interrupted by a 401. It is not a control: the API
	// rejects an expired token regardless of what the browser believes.
	SessionExpiresAt string `json:"sessionExpiresAt,omitempty"`
```

and in `MeFromPrincipal`:

```go
	sessionExpiresAt := ""
	if !principal.ExpiresAt.IsZero() {
		sessionExpiresAt = principal.ExpiresAt.UTC().Format(time.RFC3339)
	}
```

setting it on the returned struct. Import `"time"`.

- [ ] **Step 4: Run the tests**

Run: `gofmt -l internal/auth && go test ./internal/auth/... ./internal/api/...`
Expected: PASS. If `server_test.go` asserts an exact `/v1/me` JSON body, update it to include `sessionExpiresAt` — `omitempty` means a principal with no expiry produces no field, so most existing assertions should be unaffected.

- [ ] **Step 5: Commit**

```bash
git add internal/auth internal/api
git commit -m "feat(api): report session expiry from /v1/me"
```

---

### Task 10: Collapse the Keycloak fixture to one confidential client

`tflive-web` (public, PKCE, browser) and `tflive-api` (bearer-only) become a single confidential client that does both jobs. The audience mapper that forced `aud: tflive-api` into the access token goes with them — an ID token's `aud` *is* the client ID, which is the point of the whole issue. This shrinks #197.

**Files:**
- Modify: `internal/keycloak/config.go:11-16,21-39,58-72,120-145`
- Modify: `internal/keycloak/provisioner.go:1-30,128-215,285-305`
- Modify: `internal/keycloak/provisioner_test.go:60-115,193-260`
- Modify: `internal/keycloak/config_test.go`

**Interfaces:**
- Consumes: `TFLIVE_PUBLIC_URL` (Task 2)
- Produces: `keycloak.Config{APIClientID, APIClientSecret string; CallbackURI, PostLogoutRedirectURI string}` — `WebClientID`, `RedirectURIs`, and `WebOrigins` are removed

- [ ] **Step 1: Write the failing tests**

In `internal/keycloak/provisioner_test.go`, replace the `tflive-web` assertions with assertions on the single client. Read lines 60–115 for the existing style and follow it:

```go
func TestProvisionCreatesOneConfidentialClient(t *testing.T) {
	backend := newFakeProvisionBackend()
	cfg := validProvisionConfig()

	if _, err := provisionWithBackend(context.Background(), cfg, backend); err != nil {
		t.Fatalf("provisionWithBackend returned error: %v", err)
	}

	if _, exists := backend.clients["tflive-web"]; exists {
		t.Fatal("the public browser client still exists")
	}

	api := backend.clients[cfg.APIClientID]
	if api.PublicClient || api.BearerOnly {
		t.Fatalf("api client = %#v, want confidential and not bearer-only", api)
	}
	if !api.StandardFlowEnabled {
		t.Fatal("api client cannot run the authorization-code flow")
	}
	if api.Secret != cfg.APIClientSecret {
		t.Fatalf("api client secret = %q", api.Secret)
	}
	if len(api.RedirectURIs) != 1 || api.RedirectURIs[0] != cfg.CallbackURI {
		t.Fatalf("redirect URIs = %v, want [%s]", api.RedirectURIs, cfg.CallbackURI)
	}
	if len(api.WebOrigins) != 0 {
		t.Fatalf("web origins = %v, want none — the browser never calls the API cross-origin", api.WebOrigins)
	}
	if api.Attributes["post.logout.redirect.uris"] != cfg.PostLogoutRedirectURI {
		t.Fatalf("post-logout redirect = %q", api.Attributes["post.logout.redirect.uris"])
	}
	if api.Attributes["pkce.code.challenge.method"] != "S256" {
		t.Fatal("PKCE is not enforced on the confidential client")
	}
}

func TestProvisionNoLongerCreatesTheAudienceScope(t *testing.T) {
	// The audience mapper existed to force a resource identifier into an access
	// token's aud. An ID token's aud is the client ID by construction.
	backend := newFakeProvisionBackend()
	if _, err := provisionWithBackend(context.Background(), validProvisionConfig(), backend); err != nil {
		t.Fatalf("provisionWithBackend returned error: %v", err)
	}
	if _, exists := backend.clientScopes[audienceScopeName]; exists {
		t.Fatal("the audience client scope still exists")
	}
}
```

Add a `validProvisionConfig()` helper if the test file has no equivalent, returning a `Config` with `APIClientID: "tflive-api"`, `APIClientSecret: "oidc-client-secret"`, `CallbackURI: "http://localhost:5173/v1/auth/callback"`, `PostLogoutRedirectURI: "http://localhost:5173/"`, and whatever the existing tests already set for realm, admin, and platform fields. If `fakeProvisionBackend` does not record client scopes in a map, add one following how it records `clients`.

In `internal/keycloak/config_test.go`, replace any `KEYCLOAK_WEB_REDIRECT_URIS` / `KEYCLOAK_WEB_ORIGINS` cases with:

```go
func TestLoadConfigDerivesCallbackFromPublicURL(t *testing.T) {
	env := validKeycloakEnv()
	cfg, err := LoadConfig(envLookup(env))
	if err != nil {
		t.Fatalf("LoadConfig returned error: %v", err)
	}
	if cfg.CallbackURI != "http://localhost:5173/v1/auth/callback" {
		t.Fatalf("CallbackURI = %q", cfg.CallbackURI)
	}
	if cfg.PostLogoutRedirectURI != "http://localhost:5173/" {
		t.Fatalf("PostLogoutRedirectURI = %q", cfg.PostLogoutRedirectURI)
	}
}

func TestLoadConfigRequiresPublicURLAndClientSecret(t *testing.T) {
	for _, name := range []string{"TFLIVE_PUBLIC_URL", "OIDC_CLIENT_SECRET"} {
		t.Run(name, func(t *testing.T) {
			env := validKeycloakEnv()
			delete(env, name)
			if _, err := LoadConfig(envLookup(env)); err == nil {
				t.Fatalf("LoadConfig accepted a missing %s", name)
			}
		})
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/keycloak/...`
Expected: FAIL — `cfg.CallbackURI undefined`

- [ ] **Step 3: Reshape the config**

In `internal/keycloak/config.go`, delete the `defaultWebClient` constant, and on `Config` replace `WebClientID`, `RedirectURIs`, and `WebOrigins` with:

```go
	APIClientSecret       string
	CallbackURI           string
	PostLogoutRedirectURI string
```

In `LoadConfig`, delete the `KEYCLOAK_WEB_REDIRECT_URIS` and `KEYCLOAK_WEB_ORIGINS` blocks and the two `parseBrowserURLs` calls, and add:

```go
	publicURLRaw, err := required(getenv, "TFLIVE_PUBLIC_URL")
	if err != nil {
		return Config{}, err
	}
	publicURL, err := parseAdminURL(publicURLRaw)
	if err != nil {
		return Config{}, fmt.Errorf("invalid Keycloak config: TFLIVE_PUBLIC_URL %w", err)
	}
	apiClientSecret, err := required(getenv, "OIDC_CLIENT_SECRET")
	if err != nil {
		return Config{}, err
	}
```

and set on the returned struct:

```go
		APIClientSecret:       apiClientSecret,
		CallbackURI:           publicURL.String() + "/v1/auth/callback",
		PostLogoutRedirectURI: publicURL.String() + "/",
```

`parseAdminURL` already strips a trailing slash and rejects userinfo, queries, and fragments, so reusing it keeps one URL policy in the package. If `parseBrowserURLs` now has no callers, delete it and its tests.

- [ ] **Step 4: Rewrite the provisioning**

In `internal/keycloak/provisioner.go`:

Replace the two `EnsureClient` blocks (the bearer-only API client and the public web client) with one:

```go
	apiAttributes := disabledGrantAttributes()
	apiAttributes["pkce.code.challenge.method"] = "S256"
	apiAttributes["post.logout.redirect.uris"] = cfg.PostLogoutRedirectURI
	if _, err := backend.EnsureClient(ctx, cfg.Realm, ClientSpec{
		ClientID:                     cfg.APIClientID,
		Name:                         "tflive API",
		Secret:                       cfg.APIClientSecret,
		Enabled:                      true,
		Protocol:                     "openid-connect",
		BearerOnly:                   false,
		PublicClient:                 false,
		StandardFlowEnabled:          true,
		ImplicitFlowEnabled:          false,
		DirectAccessGrantsEnabled:    false,
		ServiceAccountsEnabled:       false,
		AuthorizationServicesEnabled: false,
		FullScopeAllowed:             false,
		// No WebOrigins: the browser reaches the API through the same origin
		// that serves the SPA, so no CORS is involved anywhere.
		RedirectURIs: []string{cfg.CallbackURI},
		Attributes:   apiAttributes,
	}); err != nil {
		return Result{}, fmt.Errorf("ensure API client %s: %w", cfg.APIClientID, err)
	}
```

Delete, in full:
- the `EnsureClientScope` call for `audienceScopeName` and the `EnsureProtocolMapper` call for `audienceMapperName`
- both `EnsureDefaultClientScope` calls (the audience scope and the `roles` scope) and the `LookupClientScope(ctx, cfg.Realm, "roles")` that fed the second — nothing has read realm roles since #145
- the `audienceScopeName` and `audienceMapperName` constants
- the `exampleToken` block and its audience assertion at the end of `provisionWithBackend`
- `ExampleAccessToken` from the `provisionBackend` interface, the `ExampleAccessToken` struct, and its implementation in `resources.go` and the fake backend
- `containsString` if it now has no callers
- `WebClientID` from `Result`

Change the realm spec's token lifespan and comment it:

```go
	realmSpec := RealmSpec{
		Name:    cfg.Realm,
		Enabled: true,
		// One hour. This is the whole session: tflive holds no refresh token,
		// so expiry means a round trip through Keycloak's still-live SSO
		// session. Five minutes made that a constant interruption; eight hours
		// would make the re-authentication path one nobody notices breaking.
		AccessTokenLifespan: 3600,
		SSLRequired:         sslRequired,
		RegistrationAllowed: false,
	}
```

Update `cmd/keycloak-provisioner` if it logs `Result.WebClientID`.

- [ ] **Step 5: Run the tests**

Run: `gofmt -l internal/keycloak cmd && go build ./... && go test ./internal/keycloak/... ./cmd/...`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/keycloak cmd/keycloak-provisioner
git commit -m "feat(keycloak): collapse the two clients into one confidential client"
```

---

### Task 11: Web — delete the browser OIDC client

The SPA stops being an OIDC client. `oidc-client-ts`, the user manager, the callback route, the bearer header, and the silent-renew ladder all go. What replaces them is a `/v1/me` gate plus a timer that re-authenticates before expiry rather than after a 401.

**Files:**
- Create: `web/src/auth/SessionProvider.tsx`
- Create: `web/src/auth/SessionProvider.test.tsx`
- Create: `web/src/auth/__mocks__/SessionProvider.tsx`
- Delete: `web/src/auth/oidcConfig.ts`, `userManager.ts`, `userManager.test.ts`, `CallbackPage.tsx`, `CallbackPage.test.tsx`, `OidcAuthProvider.tsx`, `OidcAuthProvider.test.tsx`, `__mocks__/OidcAuthProvider.tsx`
- Modify: `web/src/api/client.ts:1-2,230-275`
- Modify: `web/src/auth/types.ts`
- Modify: `web/src/app/router.tsx:8-9,50,101`
- Modify: `web/package.json`

**Interfaces:**
- Consumes: `GET /v1/me` with `sessionExpiresAt` (Task 9), `GET /v1/auth/login`, `POST /v1/auth/logout` (Task 8)
- Produces: `SessionProvider` default export; `AuthContextValue` unchanged, so no consumer moves

- [ ] **Step 1: Write the failing tests**

Create `web/src/auth/SessionProvider.test.tsx`:

```tsx
// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import { ApiRequestError } from "../api/client";
import SessionProvider from "./SessionProvider";

const getMe = vi.fn();
const logout = vi.fn();

vi.mock("../api/client", async () => {
  const actual = await vi.importActual<typeof import("../api/client")>("../api/client");
  return { ...actual, getMe: () => getMe(), logout: () => logout() };
});

function renderProvider(children: ReactNode) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={["/stacks"]}>
        <Routes>
          <Route element={<SessionProvider />}>
            <Route path="/stacks" element={children} />
          </Route>
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>
  );
}

const me = {
  sub: "user-123",
  displayName: "Ada",
  email: "ada@example.test",
  tenantID: "tenant_123",
  globalCapabilities: { isPlatformAdmin: false, canCreateStack: true },
  sessionExpiresAt: new Date(Date.now() + 60 * 60 * 1000).toISOString()
};

describe("SessionProvider", () => {
  let assign: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    assign = vi.fn();
    vi.stubGlobal("location", { ...window.location, assign, pathname: "/stacks", search: "" });
    getMe.mockReset();
    logout.mockReset();
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it("renders children once /v1/me resolves", async () => {
    getMe.mockResolvedValue(me);
    renderProvider(<div data-testid="child">ready</div>);
    expect(await screen.findByTestId("child")).toBeTruthy();
    expect(assign).not.toHaveBeenCalled();
  });

  it("navigates to the login route on a 401", async () => {
    getMe.mockRejectedValue(new ApiRequestError(401, "unauthorized", "unauthorized"));
    renderProvider(<div data-testid="child">ready</div>);
    await waitFor(() => {
      expect(assign).toHaveBeenCalledWith("/v1/auth/login?return_to=%2Fstacks");
    });
  });

  it("re-authenticates sixty seconds before the session expires", async () => {
    getMe.mockResolvedValue({ ...me, sessionExpiresAt: new Date(Date.now() + 120 * 1000).toISOString() });
    renderProvider(<div data-testid="child">ready</div>);
    await screen.findByTestId("child");

    expect(assign).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(61 * 1000);
    expect(assign).toHaveBeenCalledWith("/v1/auth/login?return_to=%2Fstacks");
  });

  it("navigates immediately when the session has already expired", async () => {
    getMe.mockResolvedValue({ ...me, sessionExpiresAt: new Date(Date.now() - 1000).toISOString() });
    renderProvider(<div data-testid="child">ready</div>);
    await waitFor(() => {
      expect(assign).toHaveBeenCalledWith("/v1/auth/login?return_to=%2Fstacks");
    });
  });

  it("renders an error state when /v1/me fails for a non-auth reason", async () => {
    getMe.mockRejectedValue(new ApiRequestError(503, "unavailable", "unavailable"));
    renderProvider(<div data-testid="child">ready</div>);
    expect(await screen.findByTestId("auth-error")).toBeTruthy();
    expect(assign).not.toHaveBeenCalled();
  });
});
```

- [ ] **Step 2: Run tests to verify they fail**

Run (from `web/`): `npm test -- SessionProvider`
Expected: FAIL — cannot resolve `./SessionProvider`

- [ ] **Step 3: Rewrite the API client**

In `web/src/api/client.ts`, delete the `import { getUserManager } from "../auth/userManager";` line, delete `authHeaders`, and replace `fetchWithAuth`:

```ts
function loginURL(): string {
  const returnTo = `${globalThis.location.pathname}${globalThis.location.search}`;
  return `/v1/auth/login?return_to=${encodeURIComponent(returnTo)}`;
}

async function fetchWithAuth(path: string, init: RequestInit): Promise<Response> {
  const headers = new Headers(init.headers);
  if (!headers.has("content-type")) {
    headers.set("content-type", "application/json");
  }

  // The session cookie is httpOnly: the browser attaches it and this code
  // cannot read it. There is no bearer header and nothing to renew.
  const response = await fetch(path, { method: "GET", ...init, headers, credentials: "same-origin" });

  if (response.status === 401) {
    // A full navigation, never fetch: following the redirect to the IdP as an
    // XHR would hit its origin cross-origin and die on CORS.
    globalThis.location.assign(loginURL());
  }

  return response;
}

export async function logout(): Promise<void> {
  const response = await fetch("/v1/auth/logout", { method: "POST", credentials: "same-origin" });
  const body = response.ok ? ((await response.json()) as { logoutURL: string | null }) : { logoutURL: null };
  globalThis.location.assign(body.logoutURL ?? loginURL());
}
```

Export `loginURL` so `SessionProvider` shares one definition.

- [ ] **Step 4: Write the provider**

Create `web/src/auth/SessionProvider.tsx`:

```tsx
import { useCallback, useEffect, useRef, useState } from "react";
import { Outlet, useLocation } from "react-router-dom";
import { useIsMutating } from "@tanstack/react-query";
import { ApiRequestError, loginURL, logout as postLogout } from "../api/client";
import { AuthContext } from "./AuthContext";
import { useMeQuery } from "./useMeQuery";

// How long before expiry to re-authenticate. Long enough that the round trip
// completes with room to spare, short enough that it is rare.
const REAUTH_LEAD_MS = 60_000;
// How often to retry when re-authentication is deferred because work is in
// flight. Deferral delays re-auth; it never skips it.
const REAUTH_RETRY_MS = 5_000;

export default function SessionProvider() {
  const location = useLocation();
  const [status, setStatus] = useState<"loading" | "error">("loading");
  const { data: me, error: meError, isLoading } = useMeQuery();
  const pendingMutations = useIsMutating();
  const navigated = useRef(false);
  // The live mutation count, read from a ref so the expiry timer below does not
  // restart every time a mutation starts or settles.
  const pendingMutationsRef = useRef(pendingMutations);
  pendingMutationsRef.current = pendingMutations;

  const login = useCallback(() => {
    if (navigated.current) return;
    navigated.current = true;
    globalThis.location.assign(loginURL());
  }, []);

  const logout = useCallback(() => {
    void postLogout();
  }, []);

  useEffect(() => {
    if (!meError) return;
    if (meError instanceof ApiRequestError && meError.status === 401) {
      login();
      return;
    }
    setStatus("error");
  }, [meError, login]);

  // Re-authenticate proactively, at a moment of our choosing. This is a
  // convenience, not a control: the API rejects an expired token whatever the
  // browser believes, so clock skew or a suspended laptop degrades to the 401
  // path rather than to unauthorised access.
  useEffect(() => {
    if (!me?.sessionExpiresAt) return;

    const fireAt = new Date(me.sessionExpiresAt).getTime() - REAUTH_LEAD_MS;
    if (Number.isNaN(fireAt)) return;

    let timer: ReturnType<typeof setTimeout>;
    const attempt = () => {
      if (pendingMutationsRef.current > 0 || document.querySelector("[data-unsaved='true']")) {
        timer = setTimeout(attempt, REAUTH_RETRY_MS);
        return;
      }
      login();
    };
    timer = setTimeout(attempt, Math.max(0, fireAt - Date.now()));
    return () => clearTimeout(timer);
  }, [me?.sessionExpiresAt, login]);

  if (isLoading) return null;

  if (status === "error") {
    return (
      <div data-testid="auth-error">
        <p>Authentication failed. The identity service may be unavailable.</p>
        <button type="button" onClick={login} data-testid="auth-retry-button">
          Retry
        </button>
      </div>
    );
  }

  if (meError || !me) return null;

  return (
    <AuthContext.Provider value={{ me, status: "authenticated", login, logout }}>
      <Outlet />
    </AuthContext.Provider>
  );
}
```

Any screen with a dirty form opts in by rendering `data-unsaved="true"` on a container element. Add it to the stack-template config editor in `web/src/features/stacks/StackTemplateScreen.tsx` where it already tracks unsaved config, and nowhere else for now.

- [ ] **Step 5: Update types, router, and mocks**

In `web/src/auth/types.ts`, add to `Me`:

```ts
  /** RFC 3339. When the session's ID token expires. */
  sessionExpiresAt?: string;
```

In `web/src/app/router.tsx`: replace the `OidcAuthProvider` import with `SessionProvider`, delete the `CallbackPage` import, use `<SessionProvider />` as the `path: "/"` element, and delete the `{ path: "auth/callback", element: <CallbackPage /> }` route. Update the comment above `devRoutes` to name `SessionProvider`.

Create `web/src/auth/__mocks__/SessionProvider.tsx` as a copy of the deleted `__mocks__/OidcAuthProvider.tsx` with the component renamed and `sessionExpiresAt` added to the `me` object. Update any `vi.mock("../auth/OidcAuthProvider")` call sites.

Delete the obsolete files:

```bash
git rm web/src/auth/oidcConfig.ts web/src/auth/userManager.ts web/src/auth/userManager.test.ts \
       web/src/auth/CallbackPage.tsx web/src/auth/CallbackPage.test.tsx \
       web/src/auth/OidcAuthProvider.tsx web/src/auth/OidcAuthProvider.test.tsx \
       web/src/auth/__mocks__/OidcAuthProvider.tsx
```

Then, from `web/`: `npm uninstall oidc-client-ts`

- [ ] **Step 6: Run the frontend checks**

Run (from `web/`): `npm test && npm run build`
Expected: PASS. `npm run build` runs `tsc -b`, which is what catches any remaining reference to a deleted module.

- [ ] **Step 7: Confirm no token handling survives**

Run (from the repo root): `grep -rn "oidc-client-ts\|access_token\|getUserManager\|signinRedirect\|Authorization" web/src`
Expected: no matches. Any hit is a leftover that must be removed before committing.

- [ ] **Step 8: Commit**

```bash
git add web
git commit -m "feat(web): drop the browser OIDC client for a server-side session"
```

---

### Task 12: Configuration files and documentation

The last task makes the stack start. Nothing before this point changed `.env.example` or compose, so `docker compose up` is currently broken — that is expected and is fixed here.

**Files:**
- Modify: `.env.example`
- Modify: `docker-compose.app.yaml:20-40,60-75`
- Modify: `docs/authentication.md`
- Modify: `README.md`

**Interfaces:**
- Consumes: every variable introduced by Tasks 2 and 10
- Produces: a working `docker compose up`

- [ ] **Step 1: Update `.env.example`**

Remove `OIDC_AUDIENCE`, `KEYCLOAK_WEB_REDIRECT_URIS`, `KEYCLOAK_WEB_ORIGINS`, and the three `VITE_OIDC_*` entries. Add, with comments:

```bash
# Origin the browser reaches. The API derives its OIDC redirect URI
# (<TFLIVE_PUBLIC_URL>/v1/auth/callback) and post-logout URI from this, and the
# Keycloak provisioner registers the same value. One source, so they cannot drift.
TFLIVE_PUBLIC_URL=http://localhost:5173

# The OAuth client. The API is a confidential client: it runs the
# authorization-code flow server-side and the browser never holds a token.
# OIDC_CLIENT_ID is the audience the ID token is checked against.
OIDC_CLIENT_ID=tflive-api
OIDC_CLIENT_SECRET=replace-me-with-a-local-only-secret

# Seals the short-lived login transaction cookie (state, nonce, PKCE verifier).
# 32 bytes, raw, base64, or hex.
SESSION_ENCRYPTION_KEY=b6f1d2c4a8e0937516243b8c5d7e9f0a1b2c3d4e5f60718293a4b5c6d7e8f9a0
```

- [ ] **Step 2: Update compose**

In `docker-compose.app.yaml`, in **both** service blocks that currently set `OIDC_AUDIENCE` (lines ~34 and ~67), replace it and add the rest:

```yaml
      OIDC_CLIENT_ID: ${OIDC_CLIENT_ID:-tflive-api}
      OIDC_CLIENT_SECRET: ${OIDC_CLIENT_SECRET:-tflive-api-local-only}
      TFLIVE_PUBLIC_URL: ${TFLIVE_PUBLIC_URL:-http://localhost:5173}
      SESSION_ENCRYPTION_KEY: ${SESSION_ENCRYPTION_KEY:-b6f1d2c4a8e0937516243b8c5d7e9f0a1b2c3d4e5f60718293a4b5c6d7e8f9a0}
```

The Keycloak provisioner service needs `TFLIVE_PUBLIC_URL` and `OIDC_CLIENT_SECRET` too — it registers the redirect URI and the client secret. Remove `KEYCLOAK_WEB_REDIRECT_URIS` and `KEYCLOAK_WEB_ORIGINS` from it. Remove any `VITE_OIDC_*` from the web build service.

- [ ] **Step 3: Rewrite the auth documentation**

In `docs/authentication.md`:

- **"OIDC Clients and Claims"** — one confidential client, not two. The API runs the flow; the browser holds an httpOnly cookie. `aud` is the client ID because the token is an **ID token**, which is what removes the need for a custom authorization server and its paid add-on.
- **"API Access-Token Verification"** — retitle to "API Token Verification" and say it verifies ID tokens.
- **"API Request Authentication"** — the header-then-cookie order and why the header wins.
- **"Keycloak Provisioner Configuration"** — drop `KEYCLOAK_WEB_*`, add `TFLIVE_PUBLIC_URL` and `OIDC_CLIENT_SECRET`.
- **"API Runtime Security Configuration"** — the four new variables, and that `OIDC_AUDIENCE` is retired and fails startup.
- **New section, "Browser Session"** — the cookie table from the design doc, `SameSite=Lax` and why not `Strict`, that CSRF rests on Lax while every mutating route is POST/PATCH/DELETE, and that there is no refresh token: session length is the IdP's ID token lifetime and the SPA re-authenticates through the IdP's SSO session. Link the design doc.

In `README.md`, update the local setup steps to the new variables and remove any instruction to copy a web client ID into `.env`.

- [ ] **Step 4: Verify the stack end to end**

```bash
cp .env.example .env
docker compose up -d
```

Then, in a browser at `http://localhost:5173`:

1. You are redirected to Keycloak. Sign in as `KEYCLOAK_PLATFORM_ADMIN_USERNAME`.
2. You land back on the app, signed in.
3. In devtools → Application → Cookies, `tflive_session` shows `HttpOnly ✓`, `SameSite Lax`, and `tflive_auth_tx` is gone.
4. In the console, `document.cookie` does **not** contain `tflive_session`.
5. Navigate to `http://localhost:5173/stacks` while signed out and confirm you return to `/stacks`, not `/`, after signing in.
6. Sign out, then sign in again. Keycloak must prompt for credentials — if it signs you straight back in, RP-initiated logout is not working.
7. Set `AccessTokenLifespan` to 60 in the Keycloak admin console for the `tflive` realm, sign in, wait, and confirm the re-authentication round trip is silent: no login form, same route, no visible error.

- [ ] **Step 5: Full test suite**

```bash
gofmt -l $(git ls-files '*.go')
go build ./...
go test ./...
cd web && npm test && npm run build
```
Expected: no gofmt output, everything passes.

- [ ] **Step 6: Commit**

```bash
git add .env.example docker-compose.app.yaml docs README.md
git commit -m "docs: describe the server-side OIDC flow and its configuration"
```

---

## Notes for the executor

**Order matters.** Tasks 1–9 are strictly sequential: each consumes symbols the previous one produced. Tasks 10, 11, and 12 depend on 1–9 but not on each other, so they can be worked in parallel by separate agents if you have them.

**The stack is broken between Task 2 and Task 12.** Task 2 makes `OIDC_CLIENT_ID` required and rejects `OIDC_AUDIENCE`, but `.env.example` and compose are not updated until Task 12. That is deliberate — config churn spread across ten commits is harder to review than one. Do not "fix" it early by half-updating compose.

**Three mistakes this design specifically guards against.** If you find yourself writing any of these, stop and re-read the design doc:

1. `SameSite=Strict` on either cookie. It withholds the cookie on the IdP's cross-site callback and breaks every login, and it will look like a random state-mismatch failure.
2. `offline_access` in the scope list, or storing a refresh token. There is no refresh in this design, on purpose.
3. Deriving the redirect URI from `Host` or `X-Forwarded-Proto`. Use `TFLIVE_PUBLIC_URL`.

**When the browser can't reach the IdP.** `OIDC_ISSUER_URL` uses `keycloak.localhost:8082`, a name that resolves from both the host and inside containers. The browser now follows a redirect to the authorization endpoint from that same document, so it must resolve for the browser too. If login fails with a DNS error, that is [#199](https://github.com/vishu42/tflive/issues/199), not this work.
