package authn

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"
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
