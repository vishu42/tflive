package keycloak

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

const (
	maxSuccessBody = 1 << 20
	maxErrorBody   = 4 << 10
)

// Client is a narrowly scoped Keycloak Admin REST API client.
type Client struct {
	baseURL     *url.URL
	adminRealm  string
	username    string
	password    string
	secrets     []string
	httpClient  *http.Client
	accessToken string
}

// NewClient creates a client from already validated configuration.
func NewClient(cfg Config) *Client {
	return &Client{
		baseURL:    cfg.AdminURL,
		adminRealm: cfg.AdminRealm,
		username:   cfg.AdminUsername,
		password:   cfg.AdminPassword,
		secrets:    []string{cfg.AdminPassword, cfg.PlatformAdminPassword},
		httpClient: &http.Client{
			Timeout: cfg.HTTPTimeout,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// Authenticate obtains a short-lived master-realm admin access token. The
// token is held only in memory for this provisioning run.
func (c *Client) Authenticate(ctx context.Context) error {
	form := url.Values{
		"client_id":  {"admin-cli"},
		"grant_type": {"password"},
		"username":   {c.username},
		"password":   {c.password},
	}
	endpoint, err := c.endpoint([]string{"realms", c.adminRealm, "protocol", "openid-connect", "token"}, nil)
	if err != nil {
		return fmt.Errorf("build authentication endpoint: %w", err)
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

func (c *Client) doJSON(
	ctx context.Context,
	method string,
	segments []string,
	query url.Values,
	body any,
	expectedStatuses []int,
	out any,
) error {
	_, err := c.doJSONStatus(ctx, method, segments, query, body, expectedStatuses, out)
	return err
}

func (c *Client) doJSONStatus(
	ctx context.Context,
	method string,
	segments []string,
	query url.Values,
	body any,
	expectedStatuses []int,
	out any,
) (int, error) {
	if c.accessToken == "" {
		return 0, fmt.Errorf("Keycloak Admin API request requires authentication")
	}
	endpoint, err := c.endpoint(segments, query)
	if err != nil {
		return 0, fmt.Errorf("build Keycloak Admin API endpoint: %w", err)
	}

	var bodyReader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return 0, fmt.Errorf("encode Keycloak Admin API request: %w", err)
		}
		bodyReader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint.String(), bodyReader)
	if err != nil {
		return 0, fmt.Errorf("build Keycloak Admin API request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.accessToken)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("Keycloak Admin API %s %s: %w", method, endpoint.EscapedPath(), err)
	}
	defer resp.Body.Close()
	if !containsStatus(expectedStatuses, resp.StatusCode) {
		responseBody, _ := readBounded(resp.Body, maxErrorBody)
		return resp.StatusCode, fmt.Errorf(
			"Keycloak Admin API %s %s: unexpected HTTP %d: %s",
			method,
			endpoint.EscapedPath(),
			resp.StatusCode,
			redactSecrets(strings.TrimSpace(string(responseBody)), c.secrets),
		)
	}
	if out == nil || resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusCreated || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxSuccessBody))
		return resp.StatusCode, nil
	}

	responseBody, truncated, err := readBoundedWithTruncation(resp.Body, maxSuccessBody)
	if err != nil {
		return resp.StatusCode, fmt.Errorf("read response from %s %s: %w", method, endpoint.EscapedPath(), err)
	}
	if truncated {
		return resp.StatusCode, fmt.Errorf("read response from %s %s: response exceeds %d bytes", method, endpoint.EscapedPath(), maxSuccessBody)
	}
	if err := json.Unmarshal(responseBody, out); err != nil {
		return resp.StatusCode, fmt.Errorf("decode response from %s %s: %w", method, endpoint.EscapedPath(), err)
	}
	return resp.StatusCode, nil
}

func (c *Client) endpoint(segments []string, query url.Values) (*url.URL, error) {
	escaped := make([]string, len(segments))
	for i, segment := range segments {
		if segment == "" {
			return nil, fmt.Errorf("path segment %d is empty", i)
		}
		escaped[i] = url.PathEscape(segment)
	}
	raw := strings.TrimRight(c.baseURL.String(), "/") + "/" + strings.Join(escaped, "/")
	endpoint, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if query != nil {
		endpoint.RawQuery = query.Encode()
	}
	return endpoint, nil
}

func containsStatus(expected []int, actual int) bool {
	for _, status := range expected {
		if status == actual {
			return true
		}
	}
	return false
}

func readBounded(reader io.Reader, limit int64) ([]byte, error) {
	body, _, err := readBoundedWithTruncation(reader, limit)
	return body, err
}

func readBoundedWithTruncation(reader io.Reader, limit int64) ([]byte, bool, error) {
	body, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, false, err
	}
	if int64(len(body)) > limit {
		return body[:limit], true, nil
	}
	return body, false, nil
}

func redactSecrets(message string, secrets []string) string {
	ordered := make([]string, 0, len(secrets))
	for _, secret := range secrets {
		if secret != "" {
			ordered = append(ordered, secret)
		}
	}
	sort.SliceStable(ordered, func(i, j int) bool { return len(ordered[i]) > len(ordered[j]) })
	for _, secret := range ordered {
		message = strings.ReplaceAll(message, secret, "[REDACTED]")
	}
	return message
}
