package authn

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jws"
)

func TestVerifyLogoutTokenAcceptsAWellFormedToken(t *testing.T) {
	verifier := newTestVerifier(t)
	raw := signLogoutToken(t, map[string]any{
		"sub":    "user-1",
		"sid":    "idp-sid-1",
		"events": map[string]any{backchannelLogoutEvent: map[string]any{}},
	})

	got, err := verifier.VerifyLogoutToken(context.Background(), raw)
	if err != nil {
		t.Fatalf("VerifyLogoutToken: %v", err)
	}
	if got.Subject != "user-1" || got.SessionID != "idp-sid-1" {
		t.Fatalf("got %+v, want sub=user-1 sid=idp-sid-1", got)
	}
}

func TestVerifyLogoutTokenRejections(t *testing.T) {
	verifier := newTestVerifier(t)
	validEvents := map[string]any{backchannelLogoutEvent: map[string]any{}}

	tests := map[string]map[string]any{
		"no events claim": {
			"sub": "user-1", "sid": "idp-sid-1",
		},
		"wrong event": {
			"sub": "user-1", "sid": "idp-sid-1",
			"events": map[string]any{"http://example.test/other": map[string]any{}},
		},
		"neither sub nor sid": {
			"events": validEvents,
		},
		"carries a nonce": {
			// OIDC Back-Channel Logout 1.0 §2.4 forbids nonce. Its presence
			// means an ID token is being replayed as a logout token, which
			// would let anyone holding one revoke sessions.
			"sub": "user-1", "sid": "idp-sid-1", "events": validEvents, "nonce": "n-1",
		},
		"stale iat": {
			"sub": "user-1", "sid": "idp-sid-1", "events": validEvents,
			"iat": time.Now().Add(-10 * time.Minute).Unix(),
		},
	}

	for name, claims := range tests {
		t.Run(name, func(t *testing.T) {
			raw := signLogoutToken(t, claims)
			if _, err := verifier.VerifyLogoutToken(context.Background(), raw); err == nil {
				t.Fatal("want an error, got none")
			}
		})
	}
}

func TestVerifyLogoutTokenRejectsAForeignSignature(t *testing.T) {
	verifier := newTestVerifier(t)
	raw := signLogoutTokenWithForeignKey(t, map[string]any{
		"sub": "user-1", "sid": "idp-sid-1",
		"events": map[string]any{backchannelLogoutEvent: map[string]any{}},
	})
	if _, err := verifier.VerifyLogoutToken(context.Background(), raw); err == nil {
		t.Fatal("a token signed by an unknown key was accepted")
	}
}

// logoutFixture pairs the key-serving test server behind a *OIDCVerifier with
// a second key that server never publishes, so tests can sign a token the
// verifier's JWKS cannot validate.
type logoutFixture struct {
	server     *oidcTestServer
	keyID      string
	foreignKey *rsa.PrivateKey
}

// activeLogoutFixture backs signLogoutToken and signLogoutTokenWithForeignKey,
// which the brief's test bodies call with no reference to the verifier or
// server they must sign against. Tests in this file run sequentially (none
// call t.Parallel), so a single package-level slot set by newTestVerifier and
// cleared via t.Cleanup is safe.
var activeLogoutFixture *logoutFixture

// newTestVerifier builds an *OIDCVerifier backed by a fresh test IdP with one
// published RSA key, following the same construction oidc_verifier_test.go
// uses for the ID-token verifier tests.
func newTestVerifier(t *testing.T) *OIDCVerifier {
	t.Helper()

	s := newOIDCTestServer(t)
	s.addRSAKey(t, "key-a")
	s.publish("key-a")
	v, err := NewOIDCVerifier(context.Background(), s.config(time.Now()))
	if err != nil {
		t.Fatalf("NewOIDCVerifier() error = %v", err)
	}
	t.Cleanup(func() { _ = v.Close(context.Background()) })

	foreignKey, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatalf("rsa.GenerateKey() error = %v", err)
	}

	previous := activeLogoutFixture
	activeLogoutFixture = &logoutFixture{server: s, keyID: "key-a", foreignKey: foreignKey}
	t.Cleanup(func() { activeLogoutFixture = previous })

	return v
}

// signLogoutToken signs claims as a compact JWS using the key the active
// verifier's JWKS publishes, filling in iss, aud, and a fresh iat unless the
// caller's claims map overrides them.
func signLogoutToken(t *testing.T, claims map[string]any) string {
	t.Helper()
	fixture := requireLogoutFixture(t)

	fixture.server.mu.Lock()
	key := fixture.server.keys[fixture.keyID]
	fixture.server.mu.Unlock()
	if key == nil {
		t.Fatalf("unknown key ID %q", fixture.keyID)
	}

	return signLogoutClaims(t, fixture.keyID, key, mergeLogoutClaims(fixture.server, claims))
}

// signLogoutTokenWithForeignKey signs claims with a key that is not in the
// verifier's JWKS, for exercising signature rejection.
func signLogoutTokenWithForeignKey(t *testing.T, claims map[string]any) string {
	t.Helper()
	fixture := requireLogoutFixture(t)

	return signLogoutClaims(t, "foreign-key", fixture.foreignKey, mergeLogoutClaims(fixture.server, claims))
}

func requireLogoutFixture(t *testing.T) *logoutFixture {
	t.Helper()
	if activeLogoutFixture == nil {
		t.Fatal("signLogoutToken called without newTestVerifier")
	}
	return activeLogoutFixture
}

// mergeLogoutClaims fills in iss, aud, and a fresh iat unless overrides
// already sets them; a logout token carries none of the ID-token claims
// accessToken (in oidc_verifier_test.go) defaults, so it is built directly
// here rather than reusing that helper.
func mergeLogoutClaims(s *oidcTestServer, overrides map[string]any) map[string]any {
	s.mu.Lock()
	issuer := s.issuer
	s.mu.Unlock()

	claims := map[string]any{
		"iss": issuer,
		"aud": []string{"test-audience"},
		"iat": time.Now().Unix(),
	}
	for name, value := range overrides {
		claims[name] = value
	}
	return claims
}

// signLogoutClaims signs a claims set as a compact JWS, mirroring the
// construction oidcTestServer.signWith uses in oidc_verifier_test.go.
func signLogoutClaims(t *testing.T, keyID string, key any, claims map[string]any) string {
	t.Helper()

	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	protected := jws.NewHeaders()
	if err := protected.Set("kid", keyID); err != nil {
		t.Fatalf("protected.Set() error = %v", err)
	}
	raw, err := jws.Sign(payload, jws.WithKey(jwa.RS256(), key, jws.WithProtectedHeaders(protected)))
	if err != nil {
		t.Fatalf("jws.Sign() error = %v", err)
	}
	return string(raw)
}
