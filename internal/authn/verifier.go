package authn

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"
)

var (
	ErrInvalidToken        = errors.New("invalid access token")
	ErrVerifierUnavailable = errors.New("token verifier unavailable")
)

const (
	defaultDiscoveryTTL           = 15 * time.Minute
	defaultJWKSMinRefreshInterval = time.Minute
	defaultJWKSMaxRefreshInterval = 15 * time.Minute
	defaultRefreshCooldown        = 5 * time.Second
	defaultHTTPTimeout            = 10 * time.Second
	maxTokenBytes                 = 16 << 10
	maxProviderResponseBytes      = 1 << 20
	clockSkew                     = 30 * time.Second
)

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
	// SessionID is the token's sid claim, empty when the provider omits it.
	// It is the key a back-channel logout arrives on, so it is copied into the
	// session record at sign-in.
	SessionID string
}

// DisplayName reduces the token's display claims to the one string a UI can
// show, falling back until something is non-empty.
//
// Every claim but sub is optional: a provider is not required to send name,
// preferred_username, or email, and optionalStringClaim deliberately reports an
// absent claim as empty rather than rejecting the token. Falling back to the
// subject last means the answer is never blank — a raw sub is a poor label, but
// it identifies the right person, which an empty string does not.
func (token VerifiedToken) DisplayName() string {
	return firstNonEmpty(token.Name, token.PreferredUsername, token.Email, token.Subject)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

type Verifier interface {
	Verify(context.Context, string) (VerifiedToken, error)
}

type OIDCVerifierConfig struct {
	IssuerURL              *url.URL
	Audience               string
	HTTPClient             *http.Client
	Clock                  func() time.Time
	DiscoveryTTL           time.Duration
	JWKSMinRefreshInterval time.Duration
	JWKSMaxRefreshInterval time.Duration
	RefreshCooldown        time.Duration
}

func (c OIDCVerifierConfig) withDefaults() OIDCVerifierConfig {
	if c.Clock == nil {
		c.Clock = time.Now
	}
	if c.DiscoveryTTL == 0 {
		c.DiscoveryTTL = defaultDiscoveryTTL
	}
	if c.JWKSMinRefreshInterval == 0 {
		c.JWKSMinRefreshInterval = defaultJWKSMinRefreshInterval
	}
	if c.JWKSMaxRefreshInterval == 0 {
		c.JWKSMaxRefreshInterval = defaultJWKSMaxRefreshInterval
	}
	if c.RefreshCooldown == 0 {
		c.RefreshCooldown = defaultRefreshCooldown
	}
	if c.HTTPClient == nil {
		c.HTTPClient = &http.Client{Timeout: defaultHTTPTimeout}
	} else {
		client := *c.HTTPClient
		c.HTTPClient = &client
		if c.HTTPClient.Timeout == 0 {
			c.HTTPClient.Timeout = defaultHTTPTimeout
		}
	}

	return c
}
