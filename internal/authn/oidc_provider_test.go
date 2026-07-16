package authn

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestNewOIDCVerifierLoadsDiscoveryAndInitialJWKS(t *testing.T) {
	s := newOIDCTestServer(t)
	s.addRSAKey(t, "key-a")
	s.publish("key-a")

	verifier, err := NewOIDCVerifier(context.Background(), s.config(time.Now()))
	if err != nil {
		t.Fatalf("NewOIDCVerifier() error = %v", err)
	}
	t.Cleanup(func() {
		if err := verifier.Close(context.Background()); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	if discovery, jwks := s.requestCounts(); discovery != 1 || jwks != 1 {
		t.Fatalf("unexpected startup requests: discovery=%d jwks=%d", discovery, jwks)
	}
}

func TestNewOIDCVerifierRejectsIssuerMismatchWithoutProviderDetails(t *testing.T) {
	s := newOIDCTestServer(t)
	s.addRSAKey(t, "key-a")
	s.publish("key-a")
	s.setDiscoveryIssuer(s.issuer + "/unexpected")

	_, err := NewOIDCVerifier(context.Background(), s.config(time.Now()))
	if !errors.Is(err, ErrVerifierUnavailable) {
		t.Fatalf("error = %v", err)
	}
	if got := err.Error(); got != ErrVerifierUnavailable.Error() {
		t.Fatalf("error = %q", got)
	}
	if strings.Contains(err.Error(), "unexpected") {
		t.Fatalf("error leaked discovery data: %q", err)
	}
	if strings.Contains(err.Error(), s.issuerPathToken) {
		t.Fatalf("error leaked issuer URL path: %q", err)
	}
}

func TestNewOIDCVerifierRejectsUnavailableAndOversizedProviderResponses(t *testing.T) {
	for _, body := range []string{"provider-private-detail", strings.Repeat("x", maxProviderResponseBytes+1)} {
		t.Run(strconv.Itoa(len(body)), func(t *testing.T) {
			s := newOIDCTestServer(t)
			s.setUnavailable(body)

			_, err := NewOIDCVerifier(context.Background(), s.config(time.Now()))
			if !errors.Is(err, ErrVerifierUnavailable) {
				t.Fatalf("error = %v", err)
			}
			if got := err.Error(); got != ErrVerifierUnavailable.Error() {
				t.Fatalf("error = %q", got)
			}
			if strings.Contains(err.Error(), body[:min(len(body), 32)]) {
				t.Fatalf("error leaked provider body: %q", err)
			}
			if strings.Contains(err.Error(), s.issuerPathToken) {
				t.Fatalf("error leaked issuer URL path: %q", err)
			}
		})
	}
}

func TestNewOIDCVerifierRejectsOversizedSuccessfulProviderResponses(t *testing.T) {
	for _, response := range []struct {
		name  string
		apply func(*oidcTestServer)
	}{
		{
			name: "discovery",
			apply: func(s *oidcTestServer) {
				s.setDiscoveryBody(strings.Repeat("x", maxProviderResponseBytes+1))
			},
		},
		{
			name: "jwks",
			apply: func(s *oidcTestServer) {
				s.addRSAKey(t, "key-a")
				s.publish("key-a")
				s.setJWKSBody(strings.Repeat("x", maxProviderResponseBytes+1))
			},
		},
	} {
		t.Run(response.name, func(t *testing.T) {
			s := newOIDCTestServer(t)
			response.apply(s)

			_, err := NewOIDCVerifier(context.Background(), s.config(time.Now()))
			if !errors.Is(err, ErrVerifierUnavailable) {
				t.Fatalf("error = %v", err)
			}
			if got := err.Error(); got != ErrVerifierUnavailable.Error() {
				t.Fatalf("error = %q", got)
			}
		})
	}
}

func TestNewOIDCVerifierRejectsMalformedJWKSWithoutHangingOrLeakingDetails(t *testing.T) {
	s := newOIDCTestServer(t)
	s.setJWKSBody(`{"keys":[{"kid":"provider-private-malformed-jwks-detail"}`)

	result := make(chan error, 1)
	go func() {
		_, err := NewOIDCVerifier(context.Background(), s.config(time.Now()))
		result <- err
	}()

	select {
	case err := <-result:
		if !errors.Is(err, ErrVerifierUnavailable) {
			t.Fatalf("error = %v", err)
		}
		if got := err.Error(); got != ErrVerifierUnavailable.Error() {
			t.Fatalf("error = %q", got)
		}
		if strings.Contains(err.Error(), "provider-private-malformed-jwks-detail") {
			t.Fatalf("error leaked provider body: %q", err)
		}
	case <-time.After(time.Second):
		t.Fatal("NewOIDCVerifier() did not return for malformed JWKS")
	}
}

func TestNewOIDCVerifierAllowsProviderResponseAtSizeLimit(t *testing.T) {
	s := newOIDCTestServer(t)
	s.addRSAKey(t, "key-a")
	s.publish("key-a")

	discovery, err := json.Marshal(discoveryDocument{
		Issuer:  s.issuer,
		JWKSURI: s.server.URL + "/jwks",
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	s.setDiscoveryBody(string(discovery) + strings.Repeat(" ", maxProviderResponseBytes-len(discovery)))

	verifier, err := NewOIDCVerifier(context.Background(), s.config(time.Now()))
	if err != nil {
		t.Fatalf("NewOIDCVerifier() error = %v", err)
	}
	t.Cleanup(func() {
		if err := verifier.Close(context.Background()); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
}

func TestNewOIDCVerifierRejectsInvalidConfiguration(t *testing.T) {
	issuer, err := url.Parse("https://issuer.example/issuer-private-path")
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}

	for _, test := range []struct {
		name   string
		mutate func(*OIDCVerifierConfig)
	}{
		{
			name: "nil issuer",
			mutate: func(cfg *OIDCVerifierConfig) {
				cfg.IssuerURL = nil
			},
		},
		{
			name: "empty audience",
			mutate: func(cfg *OIDCVerifierConfig) {
				cfg.Audience = ""
			},
		},
		{
			name: "negative discovery ttl",
			mutate: func(cfg *OIDCVerifierConfig) {
				cfg.DiscoveryTTL = -time.Second
			},
		},
		{
			name: "negative minimum refresh interval",
			mutate: func(cfg *OIDCVerifierConfig) {
				cfg.JWKSMinRefreshInterval = -time.Second
			},
		},
		{
			name: "negative maximum refresh interval",
			mutate: func(cfg *OIDCVerifierConfig) {
				cfg.JWKSMaxRefreshInterval = -time.Second
			},
		},
		{
			name: "negative refresh cooldown",
			mutate: func(cfg *OIDCVerifierConfig) {
				cfg.RefreshCooldown = -time.Second
			},
		},
		{
			name: "minimum interval exceeds maximum interval",
			mutate: func(cfg *OIDCVerifierConfig) {
				cfg.JWKSMinRefreshInterval = 2 * time.Minute
				cfg.JWKSMaxRefreshInterval = time.Minute
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := OIDCVerifierConfig{IssuerURL: issuer, Audience: "audience"}
			test.mutate(&cfg)

			_, err := NewOIDCVerifier(context.Background(), cfg)
			if !errors.Is(err, ErrVerifierUnavailable) {
				t.Fatalf("error = %v", err)
			}
			if got := err.Error(); got != ErrVerifierUnavailable.Error() {
				t.Fatalf("error = %q", got)
			}
		})
	}
}

func TestNewOIDCVerifierRejectsDuplicateKeyIDs(t *testing.T) {
	s := newOIDCTestServer(t)
	s.addRSAKey(t, "key-a")
	s.publish("key-a", "key-a")

	_, err := NewOIDCVerifier(context.Background(), s.config(time.Now()))
	if !errors.Is(err, ErrVerifierUnavailable) {
		t.Fatalf("error = %v", err)
	}
	if got := err.Error(); got != ErrVerifierUnavailable.Error() {
		t.Fatalf("error = %q", got)
	}
}

func TestOIDCVerifierCloseIsIdempotent(t *testing.T) {
	s := newOIDCTestServer(t)
	s.addRSAKey(t, "key-a")
	s.publish("key-a")

	verifier, err := NewOIDCVerifier(context.Background(), s.config(time.Now()))
	if err != nil {
		t.Fatalf("NewOIDCVerifier() error = %v", err)
	}
	if err := verifier.Close(context.Background()); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}
	if err := verifier.Close(context.Background()); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
}
