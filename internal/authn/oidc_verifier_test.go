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

	mu                sync.Mutex
	keys              map[string]*rsa.PrivateKey
	published         []string
	discoveryIssuer   string
	unavailableBody   string
	discoveryBody     string
	jwksBody          string
	discoveryRequests int
	jwksRequests      int
	clock             time.Time
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

	writer.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(writer).Encode(struct {
		Issuer  string `json:"issuer"`
		JWKSURI string `json:"jwks_uri"`
	}{
		Issuer:  issuer,
		JWKSURI: s.server.URL + "/jwks",
	}); err != nil {
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
	v, err := NewOIDCVerifier(context.Background(), s.config(time.Now()))
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
	want := VerifiedToken{Subject: "user-123", Name: "Ada Lovelace", PreferredUsername: "ada", Email: "ada@example.test", RealmRoles: []string{"platform-admin"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Verify() = %#v, want %#v", got, want)
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

func TestOIDCVerifierValidatedTokenRejectsMissingNotBefore(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	tok := jwt.New()
	_ = tok.Set(jwt.IssuerKey, "https://issuer.example")
	_ = tok.Set(jwt.AudienceKey, []string{"test-audience"})
	_ = tok.Set(jwt.ExpirationKey, now.Add(time.Hour))
	_ = tok.Set(jwt.SubjectKey, "user-123")
	_ = tok.Set("typ", "Bearer")
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
	_, err = v.validatedToken(payload)
	if err != ErrInvalidToken {
		t.Fatalf("validatedToken() error = %v, want ErrInvalidToken", err)
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
			name: "missing not before",
			raw: func(t *testing.T, s *oidcTestServer) string {
				return s.sign(t, "key-a", func(tok jwt.Token) {
					_ = tok.Remove(jwt.NotBeforeKey)
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
			name: "non-Bearer type",
			raw: func(t *testing.T, s *oidcTestServer) string {
				return s.sign(t, "key-a", func(tok jwt.Token) {
					_ = tok.Set("typ", "ID")
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
		{
			name: "malformed roles",
			raw: func(t *testing.T, s *oidcTestServer) string {
				return s.sign(t, "key-a", func(tok jwt.Token) {
					_ = tok.Set("realm_access", map[string]any{"roles": []any{"platform-admin", 1}})
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
	_ = tok.Set("typ", "Bearer")
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
