package githubapp

import (
	"bytes"
	"context"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jwt"
)

const (
	defaultAPIBaseURL  = "https://api.github.com"
	defaultHTTPTimeout = 15 * time.Second
	// GitHub rejects an App JWT whose exp is more than ten minutes past its iat.
	// Nine leaves room for the request itself without courting the boundary.
	appJWTLifetime = 9 * time.Minute
	// The iat is backdated so a host clock running slightly fast does not
	// produce a token GitHub considers issued in the future.
	appJWTBackdate = time.Minute
	// Enough of an error body to diagnose, bounded so a hostile or broken
	// response cannot flood a log.
	maxErrorBody = 512
)

// Token is a minted installation access token and the moment it stops working.
//
// ExpiresAt comes from GitHub rather than being computed locally: the documented
// lifetime is an hour, but a value read from the response stays correct if that
// ever changes.
type Token struct {
	Value     string
	ExpiresAt time.Time
}

// Client calls the GitHub App endpoints tflive needs: which installation covers
// a repository, and a token for it.
type Client struct {
	baseURL    string
	appID      string
	privateKey *rsa.PrivateKey
	http       *http.Client
	now        func() time.Time
}

type ClientOption func(*Client)

// WithBaseURL points the client at another API root. Tests use it; production
// does not.
func WithBaseURL(baseURL string) ClientOption {
	return func(client *Client) {
		if baseURL != "" {
			client.baseURL = baseURL
		}
	}
}

// WithClock replaces the time source, so JWT bounds can be asserted exactly.
func WithClock(now func() time.Time) ClientOption {
	return func(client *Client) {
		if now != nil {
			client.now = now
		}
	}
}

func NewClient(appID string, privateKey *rsa.PrivateKey, options ...ClientOption) *Client {
	client := &Client{
		baseURL:    defaultAPIBaseURL,
		appID:      appID,
		privateKey: privateKey,
		http:       &http.Client{Timeout: defaultHTTPTimeout},
		now:        time.Now,
	}
	for _, option := range options {
		option(client)
	}
	return client
}

// appJWT signs the App's identity assertion. It authenticates the App itself,
// not any installation, and opens only the endpoints below -- it can read no
// repository content.
func (client *Client) appJWT() (string, error) {
	now := client.now()
	token, err := jwt.NewBuilder().
		Issuer(client.appID).
		IssuedAt(now.Add(-appJWTBackdate)).
		Expiration(now.Add(appJWTLifetime)).
		Build()
	if err != nil {
		return "", fmt.Errorf("build app jwt: %w", err)
	}
	signed, err := jwt.Sign(token, jwt.WithKey(jwa.RS256(), client.privateKey))
	if err != nil {
		return "", fmt.Errorf("sign app jwt: %w", err)
	}
	return string(signed), nil
}

// InstallationForRepo returns the installation covering owner/repo.
//
// Resolving per repository is what makes multiple organisations work without
// any stored state: one App installed on many accounts yields a different
// installation here for each, and each mints its own token.
func (client *Client) InstallationForRepo(ctx context.Context, owner string, repo string) (int64, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/installation", client.baseURL, owner, repo)
	response, err := client.do(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()

	if response.StatusCode == http.StatusNotFound {
		return 0, fmt.Errorf("%w: %s/%s", ErrAppNotInstalled, owner, repo)
	}
	if response.StatusCode != http.StatusOK {
		return 0, client.statusError("resolve installation", response)
	}

	var payload struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, maxErrorBody<<4)).Decode(&payload); err != nil {
		return 0, fmt.Errorf("decode installation: %w", err)
	}
	if payload.ID == 0 {
		return 0, fmt.Errorf("resolve installation: response carried no id")
	}
	return payload.ID, nil
}

// MintToken exchanges the App JWT for an installation token scoped to one
// repository with read-only access to its contents.
//
// The narrowing is deliberate: an installation may cover hundreds of
// repositories with write access, and this token is used for exactly one clone.
func (client *Client) MintToken(ctx context.Context, installationID int64, repo string) (Token, error) {
	url := fmt.Sprintf("%s/app/installations/%d/access_tokens", client.baseURL, installationID)
	body, err := json.Marshal(map[string]any{
		"repositories": []string{repo},
		"permissions":  map[string]string{"contents": "read"},
	})
	if err != nil {
		return Token{}, fmt.Errorf("encode token request: %w", err)
	}

	response, err := client.do(ctx, http.MethodPost, url, body)
	if err != nil {
		return Token{}, err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusCreated && response.StatusCode != http.StatusOK {
		return Token{}, client.statusError("mint installation token", response)
	}

	var payload struct {
		Token     string `json:"token"`
		ExpiresAt string `json:"expires_at"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, maxErrorBody<<4)).Decode(&payload); err != nil {
		return Token{}, fmt.Errorf("decode installation token: %w", err)
	}
	if payload.Token == "" {
		return Token{}, fmt.Errorf("mint installation token: response carried no token")
	}
	expiresAt, err := time.Parse(time.RFC3339, payload.ExpiresAt)
	if err != nil {
		return Token{}, fmt.Errorf("parse installation token expiry: %w", err)
	}
	return Token{Value: payload.Token, ExpiresAt: expiresAt.UTC()}, nil
}

func (client *Client) do(ctx context.Context, method string, url string, body []byte) (*http.Response, error) {
	assertion, err := client.appJWT()
	if err != nil {
		return nil, err
	}

	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return nil, fmt.Errorf("build github request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+assertion)
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}

	response, err := client.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf("github request failed: %w", err)
	}
	return response, nil
}

// statusError renders a bounded slice of the failure body. GitHub echoes little
// of the request back, but the bound means a hostile or broken response cannot
// flood a log through an error string.
func (client *Client) statusError(action string, response *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(response.Body, maxErrorBody))
	return fmt.Errorf("%s: unexpected status %s: %s", action, response.Status, bytes.TrimSpace(body))
}
