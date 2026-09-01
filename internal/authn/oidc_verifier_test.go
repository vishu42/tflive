package authn

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jws"
	"github.com/lestrrat-go/jwx/v3/jwt"
)

type oidcTestServer struct {
	t *testing.T

	server          *httptest.Server
	issuer          string
	issuerPathToken string

	mu                    sync.Mutex
	keys                  map[string]*rsa.PrivateKey
	published             []string
	discoveryIssuer       string
	unavailableBody       string
	discoveryBody         string
	jwksBody              string
	authorizationEndpoint string
	tokenEndpoint         string
	endSessionEndpoint    string
	omitEndpoints         bool
	discoveryRequests     int
	jwksRequests          int
	clock                 time.Time
}

func newOIDCTestServer(t *testing.T) *oidcTestServer {
	t.Helper()

	s := &oidcTestServer{
		t:               t,
		issuerPathToken: "issuer-private-path",
		keys:            make(map[string]*rsa.PrivateKey),
	}
	s.server = httptest.NewServer(http.HandlerFunc(s.serveHTTP))
	s.issuer = s.server.URL + "/" + s.issuerPathToken
	s.discoveryIssuer = s.issuer
	s.authorizationEndpoint = s.server.URL + "/authorize"
	s.tokenEndpoint = s.server.URL + "/token"
	s.endSessionEndpoint = s.server.URL + "/logout"
	t.Cleanup(s.server.Close)

	return s
}

func (s *oidcTestServer) addRSAKey(t *testing.T, keyID string) {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatalf("rsa.GenerateKey() error = %v", err)
	}

	s.mu.Lock()
	s.keys[keyID] = key
	s.mu.Unlock()
}

func (s *oidcTestServer) publish(keyIDs ...string) {
	s.t.Helper()

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, keyID := range keyIDs {
		if _, ok := s.keys[keyID]; !ok {
			s.t.Fatalf("unknown key ID %q", keyID)
		}
	}
	s.published = append([]string(nil), keyIDs...)
}

func (s *oidcTestServer) config(now time.Time) OIDCVerifierConfig {
	s.t.Helper()
	s.clock = now

	issuerURL, err := url.Parse(s.issuer)
	if err != nil {
		s.t.Fatalf("url.Parse() error = %v", err)
	}
	return OIDCVerifierConfig{
		IssuerURL:  issuerURL,
		Audience:   "test-audience",
		HTTPClient: s.server.Client(),
		Clock: func() time.Time {
			return now
		},
	}
}

func (s *oidcTestServer) setDiscoveryIssuer(issuer string) {
	s.mu.Lock()
	s.discoveryIssuer = issuer
	s.mu.Unlock()
}

func (s *oidcTestServer) setUnavailable(body string) {
	s.mu.Lock()
	s.unavailableBody = body
	s.mu.Unlock()
}

func (s *oidcTestServer) setDiscoveryBody(body string) {
	s.mu.Lock()
	s.discoveryBody = body
	s.mu.Unlock()
}

func (s *oidcTestServer) setJWKSBody(body string) {
	s.mu.Lock()
	s.jwksBody = body
	s.mu.Unlock()
}

func (s *oidcTestServer) requestCounts() (discovery, jwks int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.discoveryRequests, s.jwksRequests
}

func (s *oidcTestServer) serveHTTP(writer http.ResponseWriter, request *http.Request) {
	switch request.URL.Path {
	case "/" + s.issuerPathToken + "/.well-known/openid-configuration":
		s.serveDiscovery(writer)
	case "/jwks":
		s.serveJWKS(writer)
	default:
		http.NotFound(writer, request)
	}
}

func (s *oidcTestServer) serveDiscovery(writer http.ResponseWriter) {
	s.mu.Lock()
	s.discoveryRequests++
	unavailableBody := s.unavailableBody
	discoveryBody := s.discoveryBody
	issuer := s.discoveryIssuer
	authorizationEndpoint := s.authorizationEndpoint
	tokenEndpoint := s.tokenEndpoint
	endSessionEndpoint := s.endSessionEndpoint
	omitEndpoints := s.omitEndpoints
	s.mu.Unlock()

	if unavailableBody != "" {
		http.Error(writer, unavailableBody, http.StatusServiceUnavailable)
		return
	}
	if discoveryBody != "" {
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte(discoveryBody))
		return
	}

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
}

func (s *oidcTestServer) serveJWKS(writer http.ResponseWriter) {
	s.mu.Lock()
	s.jwksRequests++
	unavailableBody := s.unavailableBody
	jwksBody := s.jwksBody
	published := append([]string(nil), s.published...)
	keys := make(map[string]*rsa.PrivateKey, len(s.keys))
	for keyID, key := range s.keys {
		keys[keyID] = key
	}
	s.mu.Unlock()

	if unavailableBody != "" {
		http.Error(writer, unavailableBody, http.StatusServiceUnavailable)
		return
	}
	if jwksBody != "" {
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte(jwksBody))
		return
	}

	jwks := struct {
		Keys []map[string]string `json:"keys"`
	}{
		Keys: make([]map[string]string, 0, len(published)),
	}
	for _, keyID := range published {
		jwks.Keys = append(jwks.Keys, rsaJWK(keyID, &keys[keyID].PublicKey))
	}
	writer.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(writer).Encode(jwks); err != nil {
		s.t.Errorf("Encode JWKS: %v", err)
	}
}

func rsaJWK(keyID string, key *rsa.PublicKey) map[string]string {
	return map[string]string{
		"alg": "RS256",
		"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		"kid": keyID,
		"kty": "RSA",
		"n":   base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
		"use": "sig",
	}
}

func TestOIDCVerifierVerifiesValidAccessTokenAndExtractsIdentity(t *testing.T) {
	s := newOIDCTestServer(t)
	s.addRSAKey(t, "key-a")
	s.publish("key-a")
	now := time.Now()
	v, err := NewOIDCVerifier(context.Background(), s.config(now))
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close(context.Background())

	raw := s.sign(t, "key-a", func(tok jwt.Token) {
		_ = tok.Set("name", "Ada Lovelace")
		_ = tok.Set("preferred_username", "ada")
		_ = tok.Set("email", "ada@example.test")
		_ = tok.Set("realm_access", map[string]any{"roles": []string{"platform-admin"}})
	})
	got, err := v.Verify(context.Background(), raw)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	want := VerifiedToken{
		Subject:           "user-123",
		Name:              "Ada Lovelace",
		PreferredUsername: "ada",
		Email:             "ada@example.test",
		ExpiresAt:         now.Add(time.Hour).Truncate(time.Second).UTC(),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Verify() = %#v, want %#v", got, want)
	}
}

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

	raw := server.sign(t, "kid-1", func(tok jwt.Token) {
		_ = tok.Set(jwt.ExpirationKey, expiry)
		_ = tok.Set("nonce", "nonce-value")
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

	raw := server.sign(t, "kid-1", nil)

	verified, err := verifier.Verify(context.Background(), raw)
	if err != nil {
		t.Fatalf("Verify returned error: %v", err)
	}
	if verified.Nonce != "" {
		t.Fatalf("Nonce = %q, want empty", verified.Nonce)
	}
}

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

func TestOIDCVerifierErrorsAreExactlySafeCategories(t *testing.T) {
	s := newOIDCTestServer(t)
	s.addRSAKey(t, "key-a")
	s.publish("key-a")
	v, err := NewOIDCVerifier(context.Background(), s.config(time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close(context.Background())

	_, err = v.Verify(context.Background(), "not-a-jwt")
	if !errors.Is(err, ErrInvalidToken) || err.Error() != "invalid access token" {
		t.Fatalf("invalid error = %v", err)
	}

	s.addRSAKey(t, "key-b")
	s.setUnavailable("provider-detail")
	_, err = v.Verify(context.Background(), s.sign(t, "key-b", nil))
	if !errors.Is(err, ErrVerifierUnavailable) || err.Error() != "token verifier unavailable" {
		t.Fatalf("unavailable error = %v", err)
	}
}

func TestOIDCVerifierVerifiesStringAudience(t *testing.T) {
	s := newOIDCTestServer(t)
	s.addRSAKey(t, "key-a")
	s.publish("key-a")
	v, err := NewOIDCVerifier(context.Background(), s.config(time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close(context.Background())

	raw := s.sign(t, "key-a", func(tok jwt.Token) {
		_ = tok.Set(jwt.AudienceKey, "test-audience")
	})
	got, err := v.Verify(context.Background(), raw)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if got.Subject != "user-123" {
		t.Fatalf("Verify().Subject = %q, want user-123", got.Subject)
	}
}

// OIDC Core 1.0 §3.1.3.7 steps 4-5: azp must equal our client ID whenever it
// is present, and is required once aud carries more than one value. A
// single-audience token need not carry it, so an absent azp there is fine.
func TestOIDCVerifierAcceptsSingleAudienceTokenWithoutAzp(t *testing.T) {
	s := newOIDCTestServer(t)
	s.addRSAKey(t, "key-a")
	s.publish("key-a")
	v, err := NewOIDCVerifier(context.Background(), s.config(time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close(context.Background())

	raw := s.sign(t, "key-a", func(tok jwt.Token) {
		_ = tok.Set(jwt.AudienceKey, []string{"test-audience"})
	})
	got, err := v.Verify(context.Background(), raw)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if got.Subject != "user-123" {
		t.Fatalf("Verify().Subject = %q, want user-123", got.Subject)
	}
}

func TestOIDCVerifierAcceptsMultiAudienceTokenWithMatchingAzp(t *testing.T) {
	s := newOIDCTestServer(t)
	s.addRSAKey(t, "key-a")
	s.publish("key-a")
	v, err := NewOIDCVerifier(context.Background(), s.config(time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close(context.Background())

	raw := s.sign(t, "key-a", func(tok jwt.Token) {
		_ = tok.Set(jwt.AudienceKey, []string{"test-audience", "other-client"})
		_ = tok.Set("azp", "test-audience")
	})
	got, err := v.Verify(context.Background(), raw)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if got.Subject != "user-123" {
		t.Fatalf("Verify().Subject = %q, want user-123", got.Subject)
	}
}

// The exploitable path: an ID token minted by the same issuer for a different
// client, with our client ID pushed into aud by a stray audience mapper. If
// azp names that other client, this must not authenticate as us.
func TestOIDCVerifierRejectsMultiAudienceTokenWithWrongAzp(t *testing.T) {
	s := newOIDCTestServer(t)
	s.addRSAKey(t, "key-a")
	s.publish("key-a")
	v, err := NewOIDCVerifier(context.Background(), s.config(time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close(context.Background())

	raw := s.sign(t, "key-a", func(tok jwt.Token) {
		_ = tok.Set(jwt.AudienceKey, []string{"test-audience", "other-client"})
		_ = tok.Set("azp", "other-client")
	})
	_, err = v.Verify(context.Background(), raw)
	if !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("Verify() error = %v, want ErrInvalidToken", err)
	}
}

func TestOIDCVerifierRejectsMultiAudienceTokenWithAbsentAzp(t *testing.T) {
	s := newOIDCTestServer(t)
	s.addRSAKey(t, "key-a")
	s.publish("key-a")
	v, err := NewOIDCVerifier(context.Background(), s.config(time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close(context.Background())

	raw := s.sign(t, "key-a", func(tok jwt.Token) {
		_ = tok.Set(jwt.AudienceKey, []string{"test-audience", "other-client"})
	})
	_, err = v.Verify(context.Background(), raw)
	if !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("Verify() error = %v, want ErrInvalidToken", err)
	}
}

// The substitution azp actually guards against: another client in the same
// realm holds an ID token minted for itself, and a stray audience mapper has
// replaced our audience into it, leaving aud a single entry that names us.
// Only azp still names the client the token was issued to, so it must be
// consulted even when aud carries one value.
func TestOIDCVerifierRejectsSingleAudienceTokenWithWrongAzp(t *testing.T) {
	s := newOIDCTestServer(t)
	s.addRSAKey(t, "key-a")
	s.publish("key-a")
	v, err := NewOIDCVerifier(context.Background(), s.config(time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close(context.Background())

	raw := s.sign(t, "key-a", func(tok jwt.Token) {
		_ = tok.Set(jwt.AudienceKey, []string{"test-audience"})
		_ = tok.Set("azp", "other-client")
	})
	_, err = v.Verify(context.Background(), raw)
	if !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("Verify() error = %v, want ErrInvalidToken", err)
	}
}

func TestOIDCVerifierAcceptsSingleAudienceTokenWithMatchingAzp(t *testing.T) {
	s := newOIDCTestServer(t)
	s.addRSAKey(t, "key-a")
	s.publish("key-a")
	v, err := NewOIDCVerifier(context.Background(), s.config(time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close(context.Background())

	raw := s.sign(t, "key-a", func(tok jwt.Token) {
		_ = tok.Set(jwt.AudienceKey, []string{"test-audience"})
		_ = tok.Set("azp", "test-audience")
	})
	got, err := v.Verify(context.Background(), raw)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if got.Subject != "user-123" {
		t.Fatalf("Verify().Subject = %q, want user-123", got.Subject)
	}
}

func TestOIDCVerifierRejectsNonStringAzp(t *testing.T) {
	s := newOIDCTestServer(t)
	s.addRSAKey(t, "key-a")
	s.publish("key-a")
	v, err := NewOIDCVerifier(context.Background(), s.config(time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close(context.Background())

	raw := s.sign(t, "key-a", func(tok jwt.Token) {
		_ = tok.Set(jwt.AudienceKey, []string{"test-audience"})
		_ = tok.Set("azp", []string{"test-audience"})
	})
	_, err = v.Verify(context.Background(), raw)
	if !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("Verify() error = %v, want ErrInvalidToken", err)
	}
}

func TestOIDCVerifierUsesCachedKeyWithoutRepeatedJWKSFetch(t *testing.T) {
	s := newOIDCTestServer(t)
	s.addRSAKey(t, "key-a")
	s.publish("key-a")
	v, err := NewOIDCVerifier(context.Background(), s.config(time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close(context.Background())

	raw := s.sign(t, "key-a", nil)
	for range 2 {
		if _, err := v.Verify(context.Background(), raw); err != nil {
			t.Fatal(err)
		}
	}
	_, jwks := s.requestCounts()
	if jwks != 1 {
		t.Fatalf("JWKS requests = %d, want 1", jwks)
	}
}

func TestOIDCVerifierRefreshesJWKSForRotatedKey(t *testing.T) {
	s := newOIDCTestServer(t)
	s.addRSAKey(t, "key-a")
	s.publish("key-a")
	v, err := NewOIDCVerifier(context.Background(), s.config(time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close(context.Background())

	s.addRSAKey(t, "key-b")
	s.publish("key-a", "key-b")
	if _, err := v.Verify(context.Background(), s.sign(t, "key-b", nil)); err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	_, jwks := s.requestCounts()
	if jwks != 2 {
		t.Fatalf("JWKS requests = %d, want 2", jwks)
	}
}

func TestOIDCVerifierRefreshesJWKSAfterSameKIDSignatureRotation(t *testing.T) {
	s := newOIDCTestServer(t)
	s.addRSAKey(t, "key-a")
	s.publish("key-a")
	v, err := NewOIDCVerifier(context.Background(), s.config(time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close(context.Background())

	s.addRSAKey(t, "key-a")
	s.publish("key-a")
	if _, err := v.Verify(context.Background(), s.sign(t, "key-a", nil)); err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	_, jwks := s.requestCounts()
	if jwks != 2 {
		t.Fatalf("JWKS requests = %d, want 2", jwks)
	}
}

func TestOIDCVerifierCoordinatesConcurrentUnknownKIDRefresh(t *testing.T) {
	s := newOIDCTestServer(t)
	s.addRSAKey(t, "key-a")
	s.publish("key-a")
	v, err := NewOIDCVerifier(context.Background(), s.config(time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close(context.Background())

	s.addRSAKey(t, "key-b")
	s.publish("key-a", "key-b")
	raw := s.sign(t, "key-b", nil)
	start, errs := make(chan struct{}), make(chan error, 16)
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := v.Verify(context.Background(), raw)
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("Verify() error = %v", err)
		}
	}

	_, jwks := s.requestCounts()
	if jwks != 2 {
		t.Fatalf("JWKS requests = %d, want 2", jwks)
	}
}

func TestOIDCVerifierUsesFreshCachedKeyDuringProviderOutage(t *testing.T) {
	s := newOIDCTestServer(t)
	s.addRSAKey(t, "key-a")
	s.publish("key-a")
	now := time.Now().UTC().Truncate(time.Second)
	cfg := s.config(now)
	cfg.JWKSMinRefreshInterval = 10 * time.Second
	cfg.JWKSMaxRefreshInterval = 20 * time.Second
	cfg.Clock = func() time.Time {
		return now
	}
	v, err := NewOIDCVerifier(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close(context.Background())

	now = now.Add(9 * time.Second)
	s.setUnavailable("keycloak-down")
	if _, err := v.Verify(context.Background(), s.sign(t, "key-a", nil)); err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	_, jwks := s.requestCounts()
	if jwks != 1 {
		t.Fatalf("JWKS requests = %d, want 1", jwks)
	}
}

func TestOIDCVerifierFailsClosedAfterJWKSFreshnessExpiresDuringProviderOutage(t *testing.T) {
	s := newOIDCTestServer(t)
	s.addRSAKey(t, "key-a")
	s.publish("key-a")
	cfg := s.config(time.Now())
	cfg.Clock = time.Now
	cfg.JWKSMinRefreshInterval = time.Second
	cfg.JWKSMaxRefreshInterval = time.Second
	v, err := NewOIDCVerifier(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close(context.Background())

	time.Sleep(2 * time.Second)
	_, jwks := s.requestCounts()
	if jwks != 1 {
		t.Fatalf("automatic JWKS requests = %d, want 1", jwks)
	}
	s.setUnavailable("keycloak-down")
	_, err = v.Verify(context.Background(), s.sign(t, "key-a", nil))
	if err != ErrVerifierUnavailable {
		t.Fatalf("Verify() error = %v, want ErrVerifierUnavailable", err)
	}
	_, jwks = s.requestCounts()
	if jwks != 2 {
		t.Fatalf("JWKS requests = %d, want 2", jwks)
	}
}

func TestOIDCVerifierRejectsExpiredCachedKeyDuringProviderOutage(t *testing.T) {
	s := newOIDCTestServer(t)
	s.addRSAKey(t, "key-a")
	s.publish("key-a")
	now := time.Now().UTC().Truncate(time.Second)
	cfg := s.config(now)
	cfg.JWKSMinRefreshInterval = 10 * time.Second
	cfg.JWKSMaxRefreshInterval = 20 * time.Second
	cfg.Clock = func() time.Time {
		return now
	}
	v, err := NewOIDCVerifier(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close(context.Background())

	now = now.Add(11 * time.Second)
	s.setUnavailable("keycloak-down")
	_, err = v.Verify(context.Background(), s.sign(t, "key-a", nil))
	if err != ErrVerifierUnavailable {
		t.Fatalf("Verify() error = %v, want ErrVerifierUnavailable", err)
	}
	_, jwks := s.requestCounts()
	if jwks != 2 {
		t.Fatalf("JWKS requests = %d, want 2", jwks)
	}
}

func TestOIDCVerifierReturnsUnavailableWhenNoUsableKeyCanBeFetched(t *testing.T) {
	s := newOIDCTestServer(t)
	s.addRSAKey(t, "key-a")
	s.publish("key-a")
	v, err := NewOIDCVerifier(context.Background(), s.config(time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close(context.Background())

	s.addRSAKey(t, "key-b")
	s.setUnavailable("keycloak-down")
	_, err = v.Verify(context.Background(), s.sign(t, "key-b", nil))
	if !errors.Is(err, ErrVerifierUnavailable) {
		t.Fatalf("Verify() error = %v", err)
	}
}

func TestOIDCVerifierValidatedTokenAllowsMissingNotBefore(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	tok := jwt.New()
	_ = tok.Set(jwt.IssuerKey, "https://issuer.example")
	_ = tok.Set(jwt.AudienceKey, []string{"test-audience"})
	_ = tok.Set(jwt.ExpirationKey, now.Add(time.Hour))
	_ = tok.Set(jwt.SubjectKey, "user-123")
	payload, err := json.Marshal(tok)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	v := &OIDCVerifier{
		cfg: OIDCVerifierConfig{
			Audience: "test-audience",
			Clock: func() time.Time {
				return now
			},
		},
		discovery: discoveryDocument{Issuer: "https://issuer.example"},
	}
	got, err := v.validatedToken(payload)
	if err != nil {
		t.Fatalf("validatedToken() error = %v", err)
	}
	if got.Subject != "user-123" {
		t.Fatalf("validatedToken().Subject = %q, want user-123", got.Subject)
	}
}

func TestOIDCVerifierRejectsInvalidTokens(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)

	for _, test := range []struct {
		name    string
		prepare func(t *testing.T, s *oidcTestServer)
		raw     func(t *testing.T, s *oidcTestServer) string
	}{
		{
			name: "empty input",
			raw: func(*testing.T, *oidcTestServer) string {
				return ""
			},
		},
		{
			name: "input larger than 16 KiB",
			raw: func(t *testing.T, s *oidcTestServer) string {
				return s.sign(t, "key-a", func(tok jwt.Token) {
					_ = tok.Set("padding", strings.Repeat("x", maxTokenBytes))
				})
			},
		},
		{
			name: "not three segments",
			raw: func(*testing.T, *oidcTestServer) string {
				return "not.a.compact.jws"
			},
		},
		{
			name: "missing kid",
			raw: func(t *testing.T, s *oidcTestServer) string {
				return s.signWithoutKeyID(t)
			},
		},
		{
			name: "none algorithm",
			raw: func(t *testing.T, s *oidcTestServer) string {
				return s.compactWithHeader(t, map[string]any{"alg": "none", "kid": "key-a"}, nil)
			},
		},
		{
			name: "HS256 algorithm",
			raw: func(t *testing.T, s *oidcTestServer) string {
				return s.signWith(t, "key-a", jwa.HS256(), []byte("test-hmac-key"), nil, true)
			},
		},
		{
			name: "unsupported asymmetric algorithm",
			raw: func(t *testing.T, s *oidcTestServer) string {
				return s.compactWithHeader(t, map[string]any{"alg": "ES256K", "kid": "key-a"}, nil)
			},
		},
		{
			name: "JWK use mismatch",
			prepare: func(t *testing.T, s *oidcTestServer) {
				s.setJWK(t, "key-a", func(key map[string]string) {
					key["use"] = "enc"
				})
			},
			raw: func(t *testing.T, s *oidcTestServer) string {
				return s.sign(t, "key-a", nil)
			},
		},
		{
			name: "JWK algorithm mismatch",
			prepare: func(t *testing.T, s *oidcTestServer) {
				s.setJWK(t, "key-a", func(key map[string]string) {
					key["alg"] = "RS512"
				})
			},
			raw: func(t *testing.T, s *oidcTestServer) string {
				return s.sign(t, "key-a", nil)
			},
		},
		{
			name: "changed signature",
			raw: func(t *testing.T, s *oidcTestServer) string {
				return tamperSignature(t, s.sign(t, "key-a", nil))
			},
		},
		{
			name: "expired expiration",
			raw: func(t *testing.T, s *oidcTestServer) string {
				return s.sign(t, "key-a", func(tok jwt.Token) {
					_ = tok.Set(jwt.ExpirationKey, now.Add(-clockSkew-time.Second))
				})
			},
		},
		{
			name: "future not before",
			raw: func(t *testing.T, s *oidcTestServer) string {
				return s.sign(t, "key-a", func(tok jwt.Token) {
					_ = tok.Set(jwt.NotBeforeKey, now.Add(clockSkew+time.Second))
				})
			},
		},
		{
			name: "wrong issuer",
			raw: func(t *testing.T, s *oidcTestServer) string {
				return s.sign(t, "key-a", func(tok jwt.Token) {
					_ = tok.Set(jwt.IssuerKey, "https://other-issuer.example")
				})
			},
		},
		{
			name: "wrong audience",
			raw: func(t *testing.T, s *oidcTestServer) string {
				return s.sign(t, "key-a", func(tok jwt.Token) {
					_ = tok.Set(jwt.AudienceKey, []string{"other-audience"})
				})
			},
		},
		{
			name: "missing subject",
			raw: func(t *testing.T, s *oidcTestServer) string {
				return s.sign(t, "key-a", func(tok jwt.Token) {
					_ = tok.Remove(jwt.SubjectKey)
				})
			},
		},
		{
			name: "empty subject",
			raw: func(t *testing.T, s *oidcTestServer) string {
				return s.sign(t, "key-a", func(tok jwt.Token) {
					_ = tok.Set(jwt.SubjectKey, "")
				})
			},
		},
		{
			name: "non-string presentation claim",
			raw: func(t *testing.T, s *oidcTestServer) string {
				return s.sign(t, "key-a", func(tok jwt.Token) {
					_ = tok.Set("name", []string{"Ada Lovelace"})
				})
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := newOIDCTestServer(t)
			s.addRSAKey(t, "key-a")
			s.publish("key-a")
			if test.prepare != nil {
				test.prepare(t, s)
			}
			v, err := NewOIDCVerifier(context.Background(), s.config(now))
			if err != nil {
				t.Fatalf("NewOIDCVerifier() error = %v", err)
			}
			defer v.Close(context.Background())

			_, err = v.Verify(context.Background(), test.raw(t, s))
			if !errors.Is(err, ErrInvalidToken) {
				t.Fatalf("Verify() error = %v, want ErrInvalidToken", err)
			}
		})
	}
}

func TestOIDCVerifierRedactsTokenAndProviderDetails(t *testing.T) {
	const (
		rawTokenFragment = "raw-token-private-fragment"
		claimString      = "claim-private-fragment"
		fixtureResponse  = "fixture-failure-response"
	)

	s := newOIDCTestServer(t)
	s.addRSAKey(t, "key-a")
	s.publish("key-a")
	v, err := NewOIDCVerifier(context.Background(), s.config(time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close(context.Background())
	s.setUnavailable(fixtureResponse)

	for _, raw := range []string{
		rawTokenFragment + ".invalid",
		s.sign(t, "key-a", func(tok jwt.Token) {
			_ = tok.Set(jwt.IssuerKey, claimString)
		}),
	} {
		_, err := v.Verify(context.Background(), raw)
		if !errors.Is(err, ErrInvalidToken) {
			t.Fatalf("Verify() error = %v, want ErrInvalidToken", err)
		}
		for _, secret := range []string{rawTokenFragment, claimString, fixtureResponse} {
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("Verify() leaked %q in error %q", secret, err)
			}
		}
	}
	s.addRSAKey(t, "key-b")
	_, err = v.Verify(context.Background(), s.sign(t, "key-b", nil))
	if err != ErrVerifierUnavailable {
		t.Fatalf("Verify() error = %v, want ErrVerifierUnavailable", err)
	}
	if strings.Contains(err.Error(), fixtureResponse) {
		t.Fatalf("Verify() leaked %q in error %q", fixtureResponse, err)
	}
	_, jwks := s.requestCounts()
	if jwks != 2 {
		t.Fatalf("JWKS requests = %d, want 2", jwks)
	}
}

func TestVerifyCarriesSessionIDClaim(t *testing.T) {
	verified := verifyTokenWithClaims(t, map[string]any{"sid": "idp-sid-1"})
	if verified.SessionID != "idp-sid-1" {
		t.Fatalf("SessionID = %q, want %q", verified.SessionID, "idp-sid-1")
	}
}

func TestVerifyToleratesAbsentSessionIDClaim(t *testing.T) {
	verified := verifyTokenWithClaims(t, nil)
	if verified.SessionID != "" {
		t.Fatalf("SessionID = %q, want empty when sid is absent", verified.SessionID)
	}
}

// verifyTokenWithClaims mints a token carrying the given extra claims, signs
// it, and runs it through a fresh verifier.
func verifyTokenWithClaims(t *testing.T, claims map[string]any) VerifiedToken {
	t.Helper()

	s := newOIDCTestServer(t)
	s.addRSAKey(t, "key-a")
	s.publish("key-a")
	v, err := NewOIDCVerifier(context.Background(), s.config(time.Now()))
	if err != nil {
		t.Fatalf("NewOIDCVerifier() error = %v", err)
	}
	t.Cleanup(func() { _ = v.Close(context.Background()) })

	raw := s.sign(t, "key-a", func(tok jwt.Token) {
		for name, value := range claims {
			_ = tok.Set(name, value)
		}
	})
	verified, err := v.Verify(context.Background(), raw)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	return verified
}

func (s *oidcTestServer) sign(t *testing.T, keyID string, mutate func(jwt.Token)) string {
	t.Helper()

	s.mu.Lock()
	key := s.keys[keyID]
	s.mu.Unlock()
	if key == nil {
		t.Fatalf("unknown key ID %q", keyID)
	}
	return s.signWith(t, keyID, jwa.RS256(), key, mutate, true)
}

func (s *oidcTestServer) signWithoutKeyID(t *testing.T) string {
	t.Helper()

	s.mu.Lock()
	key := s.keys["key-a"]
	s.mu.Unlock()
	if key == nil {
		t.Fatal("missing key-a")
	}
	return s.signWith(t, "", jwa.RS256(), key, nil, false)
}

func (s *oidcTestServer) signWith(t *testing.T, keyID string, algorithm jwa.SignatureAlgorithm, key any, mutate func(jwt.Token), includeKeyID bool) string {
	t.Helper()

	payload, err := json.Marshal(s.accessToken(mutate))
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	protected := jws.NewHeaders()
	if includeKeyID {
		if err := protected.Set("kid", keyID); err != nil {
			t.Fatalf("protected.Set() error = %v", err)
		}
	}
	raw, err := jws.Sign(payload, jws.WithKey(algorithm, key, jws.WithProtectedHeaders(protected)))
	if err != nil {
		t.Fatalf("jws.Sign() error = %v", err)
	}
	return string(raw)
}

func (s *oidcTestServer) compactWithHeader(t *testing.T, header map[string]any, mutate func(jwt.Token)) string {
	t.Helper()

	encodedHeader, err := json.Marshal(header)
	if err != nil {
		t.Fatalf("json.Marshal(header) error = %v", err)
	}
	payload, err := json.Marshal(s.accessToken(mutate))
	if err != nil {
		t.Fatalf("json.Marshal(payload) error = %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(encodedHeader) + "." + base64.RawURLEncoding.EncodeToString(payload) + "."
}

func (s *oidcTestServer) accessToken(mutate func(jwt.Token)) jwt.Token {
	s.mu.Lock()
	now := s.clock
	issuer := s.issuer
	s.mu.Unlock()
	if now.IsZero() {
		now = time.Now()
	}

	tok := jwt.New()
	_ = tok.Set(jwt.IssuerKey, issuer)
	_ = tok.Set(jwt.AudienceKey, []string{"test-audience"})
	_ = tok.Set(jwt.ExpirationKey, now.Add(time.Hour))
	_ = tok.Set(jwt.NotBeforeKey, now.Add(-time.Minute))
	_ = tok.Set(jwt.SubjectKey, "user-123")
	if mutate != nil {
		mutate(tok)
	}
	return tok
}

func (s *oidcTestServer) setJWK(t *testing.T, keyID string, mutate func(map[string]string)) {
	t.Helper()

	s.mu.Lock()
	key := s.keys[keyID]
	s.mu.Unlock()
	if key == nil {
		t.Fatalf("unknown key ID %q", keyID)
	}
	serialized := rsaJWK(keyID, &key.PublicKey)
	mutate(serialized)
	body, err := json.Marshal(struct {
		Keys []map[string]string `json:"keys"`
	}{Keys: []map[string]string{serialized}})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	s.setJWKSBody(string(body))
}

func tamperSignature(t *testing.T, raw string) string {
	t.Helper()

	segments := strings.Split(raw, ".")
	if len(segments) != 3 || len(segments[2]) == 0 {
		t.Fatalf("unexpected compact JWS %q", raw)
	}
	if segments[2][0] == 'A' {
		segments[2] = "B" + segments[2][1:]
	} else {
		segments[2] = "A" + segments[2][1:]
	}
	return strings.Join(segments, ".")
}

func TestOIDCVerifierAcceptsProviderTokenShapes(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(jwt.Token)
		want   VerifiedToken
	}{
		{
			name: "Okta shaped token without typ or realm_access",
			mutate: func(tok jwt.Token) {
				_ = tok.Set("name", "Ada Lovelace")
				_ = tok.Set("preferred_username", "ada")
				_ = tok.Set("email", "ada@example.test")
			},
			want: VerifiedToken{Subject: "user-123", Name: "Ada Lovelace", PreferredUsername: "ada", Email: "ada@example.test"},
		},
		{
			// realm_access is malformed on purpose: authorization is OpenFGA's,
			// so the claim is not read and cannot fail verification.
			name: "Keycloak shaped token with a malformed realm_access",
			mutate: func(tok jwt.Token) {
				_ = tok.Set("name", "Ada Lovelace")
				_ = tok.Set("preferred_username", "ada")
				_ = tok.Set("email", "ada@example.test")
				_ = tok.Set("realm_access", map[string]any{"roles": []any{"platform-admin", 1}})
			},
			want: VerifiedToken{Subject: "user-123", Name: "Ada Lovelace", PreferredUsername: "ada", Email: "ada@example.test"},
		},
		{
			name: "Keycloak shaped token with typ Bearer and realm_access",
			mutate: func(tok jwt.Token) {
				_ = tok.Set("typ", "Bearer")
				_ = tok.Set("name", "Ada Lovelace")
				_ = tok.Set("preferred_username", "ada")
				_ = tok.Set("email", "ada@example.test")
				_ = tok.Set("realm_access", map[string]any{"roles": []string{"platform-admin"}})
			},
			want: VerifiedToken{Subject: "user-123", Name: "Ada Lovelace", PreferredUsername: "ada", Email: "ada@example.test"},
		},
		{
			name: "Keycloak shaped ID token with typ ID",
			mutate: func(tok jwt.Token) {
				_ = tok.Set("typ", "ID")
				_ = tok.Set("name", "Ada Lovelace")
				_ = tok.Set("preferred_username", "ada")
				_ = tok.Set("email", "ada@example.test")
			},
			want: VerifiedToken{Subject: "user-123", Name: "Ada Lovelace", PreferredUsername: "ada", Email: "ada@example.test"},
		},
		{
			name:   "minimal token carrying only the subject",
			mutate: nil,
			want:   VerifiedToken{Subject: "user-123"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := newOIDCTestServer(t)
			s.addRSAKey(t, "key-a")
			s.publish("key-a")
			now := time.Now()
			v, err := NewOIDCVerifier(context.Background(), s.config(now))
			if err != nil {
				t.Fatalf("NewOIDCVerifier() error = %v", err)
			}
			defer v.Close(context.Background())

			got, err := v.Verify(context.Background(), s.sign(t, "key-a", test.mutate))
			if err != nil {
				t.Fatalf("Verify() error = %v", err)
			}
			want := test.want
			want.ExpiresAt = now.Add(time.Hour).Truncate(time.Second).UTC()
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("Verify() = %#v, want %#v", got, want)
			}
		})
	}
}
