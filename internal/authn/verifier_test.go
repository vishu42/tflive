package authn

import (
	"net/http"
	"testing"
	"time"
)

func TestVerifierContractUsesSafeStableErrorsAndDefaults(t *testing.T) {
	t.Parallel()

	if got := ErrInvalidToken.Error(); got != "invalid access token" {
		t.Fatalf("ErrInvalidToken = %q", got)
	}
	if got := ErrVerifierUnavailable.Error(); got != "token verifier unavailable" {
		t.Fatalf("ErrVerifierUnavailable = %q", got)
	}
	cfg := OIDCVerifierConfig{}
	cfg = cfg.withDefaults()
	if cfg.DiscoveryTTL != 15*time.Minute || cfg.JWKSMinRefreshInterval != time.Minute ||
		cfg.JWKSMaxRefreshInterval != 15*time.Minute || cfg.RefreshCooldown != 5*time.Second {
		t.Fatalf("unexpected cache defaults: %#v", cfg)
	}
}

func TestOIDCVerifierConfigWithDefaultsClonesSuppliedHTTPClient(t *testing.T) {
	t.Parallel()

	client := &http.Client{Timeout: 3 * time.Second}
	cfg := OIDCVerifierConfig{HTTPClient: client}.withDefaults()

	if cfg.HTTPClient == client {
		t.Fatal("HTTPClient was not cloned")
	}
	if cfg.HTTPClient.Timeout != client.Timeout {
		t.Fatalf("HTTPClient timeout = %s, want %s", cfg.HTTPClient.Timeout, client.Timeout)
	}
}
