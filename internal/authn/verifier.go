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
	RealmRoles        []string
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
