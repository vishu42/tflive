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
//
// idTokenHint is the whole ID token, not a reference to one. Without it the OP
// cannot tell the request came from us rather than from anything else able to
// navigate the browser, so Keycloak stops to ask the user to confirm.
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
