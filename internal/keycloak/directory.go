package keycloak

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type DirectoryClientConfig struct {
	AdminURL     *url.URL
	Realm        string
	ClientID     string
	ClientSecret string
	HTTPTimeout  time.Duration
	Debug        bool
}

type DirectoryUser struct {
	ID        string `json:"id"`
	Username  string `json:"username"`
	Email     string `json:"email"`
	FirstName string `json:"firstName"`
	LastName  string `json:"lastName"`
}

type DirectoryClient struct {
	adminURL     *url.URL
	realm        string
	clientID     string
	clientSecret string
	secrets      []string
	httpClient   *http.Client
	accessToken  string
	debug        bool
}

func NewDirectoryClient(cfg DirectoryClientConfig) *DirectoryClient {
	return &DirectoryClient{
		adminURL:     cfg.AdminURL,
		realm:        cfg.Realm,
		clientID:     cfg.ClientID,
		clientSecret: cfg.ClientSecret,
		secrets:      []string{cfg.ClientSecret},
		debug:        cfg.Debug,
		httpClient: &http.Client{
			Timeout: cfg.HTTPTimeout,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

func (c *DirectoryClient) Authenticate(ctx context.Context) error {
	form := url.Values{
		"client_id":     {c.clientID},
		"client_secret": {c.clientSecret},
		"grant_type":    {"client_credentials"},
	}
	endpoint, err := c.buildURL([]string{"realms", c.realm, "protocol", "openid-connect", "token"}, nil)
	if err != nil {
		return fmt.Errorf("build authentication endpoint: %w", err)
	}
	if c.debug {
		log.Printf("[DEBUG] DirectoryClient.Authenticate client_id=%s endpoint=%s", c.clientID, endpoint.String())
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("build authentication request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("authenticate to Keycloak: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := readBounded(resp.Body, maxErrorBody)
		return fmt.Errorf("authenticate to Keycloak: unexpected HTTP %d: %s", resp.StatusCode, redactSecrets(strings.TrimSpace(string(body)), c.secrets))
	}

	body, truncated, err := readBoundedWithTruncation(resp.Body, maxSuccessBody)
	if err != nil {
		return fmt.Errorf("read authentication response: %w", err)
	}
	if truncated {
		return fmt.Errorf("read authentication response: response exceeds %d bytes", maxSuccessBody)
	}
	var tokenResponse struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &tokenResponse); err != nil {
		return fmt.Errorf("decode authentication response: %w", err)
	}
	if strings.TrimSpace(tokenResponse.AccessToken) == "" {
		return fmt.Errorf("decode authentication response: missing access_token")
	}
	c.accessToken = tokenResponse.AccessToken
	return nil
}

func (c *DirectoryClient) SearchUsers(ctx context.Context, query string, first, max int) ([]DirectoryUser, error) {
	if c.accessToken == "" {
		return nil, fmt.Errorf("Keycloak directory search requires authentication")
	}
	queryParams := url.Values{
		"first":   {fmt.Sprintf("%d", first)},
		"max":     {fmt.Sprintf("%d", max)},
		"enabled": {"true"},
	}
	if query != "" {
		queryParams.Set("q", query)
	}
	endpoint, err := c.buildURL([]string{"admin", "realms", c.realm, "users"}, queryParams)
	if err != nil {
		return nil, fmt.Errorf("build search endpoint: %w", err)
	}
	if c.debug {
		log.Printf("[DEBUG] DirectoryClient.SearchUsers realm=%s endpoint=%s", c.realm, endpoint.String())
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build search request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.accessToken)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("search Keycloak users: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := readBounded(resp.Body, maxErrorBody)
		return nil, fmt.Errorf("search Keycloak users: unexpected HTTP %d: %s", resp.StatusCode, redactSecrets(strings.TrimSpace(string(body)), c.secrets))
	}

	body, truncated, err := readBoundedWithTruncation(resp.Body, maxSuccessBody)
	if err != nil {
		return nil, fmt.Errorf("read search response: %w", err)
	}
	if truncated {
		return nil, fmt.Errorf("read search response: response exceeds %d bytes", maxSuccessBody)
	}

	var users []DirectoryUser
	if err := json.Unmarshal(body, &users); err != nil {
		return nil, fmt.Errorf("decode search response: %w", err)
	}
	if c.debug {
		log.Printf("[DEBUG] DirectoryClient.SearchUsers returned %d users", len(users))
	}
	return users, nil
}

func (c *DirectoryClient) buildURL(segments []string, query url.Values) (*url.URL, error) {
	return buildAdminURL(c.adminURL, segments, query)
}
