package keycloak

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("url.Parse(%q) error = %v", raw, err)
	}
	return u
}

func newDirectoryClientForServer(t *testing.T, serverURL string) *DirectoryClient {
	t.Helper()
	return NewDirectoryClient(DirectoryClientConfig{
		AdminURL:    mustParseURL(t, serverURL),
		Realm:       "test-realm",
		ClientID:    "test-client",
		ClientSecret: "test-secret",
		HTTPTimeout: 5 * time.Second,
	})
}

func TestDirectoryClientAuthenticatesAndSearches(t *testing.T) {
	t.Parallel()

	var gotTokenRequest bool
	var gotBearerHeader string
	var gotQueryParams url.Values

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/realms/test-realm/protocol/openid-connect/token":
			if r.Method != http.MethodPost {
				t.Errorf("token method = %s, want POST", r.Method)
			}
			if err := r.ParseForm(); err != nil {
				t.Fatalf("ParseForm() error = %v", err)
			}
			if got := r.FormValue("client_id"); got != "test-client" {
				t.Errorf("client_id = %q, want test-client", got)
			}
			if got := r.FormValue("client_secret"); got != "test-secret" {
				t.Errorf("client_secret = %q, want test-secret", got)
			}
			if got := r.FormValue("grant_type"); got != "client_credentials" {
				t.Errorf("grant_type = %q, want client_credentials", got)
			}
			gotTokenRequest = true
			writeTestJSON(t, w, http.StatusOK, map[string]any{
				"access_token": "dir-access-token",
				"expires_in":   300,
			})
		case "/admin/realms/test-realm/users":
			gotBearerHeader = r.Header.Get("Authorization")
			gotQueryParams = r.URL.Query()
			writeTestJSON(t, w, http.StatusOK, []DirectoryUser{
				{ID: "u1", Username: "alice", Email: "alice@example.com", FirstName: "Alice", LastName: "Smith"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := newDirectoryClientForServer(t, server.URL)

	if err := client.Authenticate(context.Background()); err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if !gotTokenRequest {
		t.Fatal("token endpoint was not called")
	}

	users, err := client.SearchUsers(context.Background(), "alice", 0, 10)
	if err != nil {
		t.Fatalf("SearchUsers() error = %v", err)
	}
	if gotBearerHeader != "Bearer dir-access-token" {
		t.Errorf("Authorization = %q, want Bearer dir-access-token", gotBearerHeader)
	}
	if gotQueryParams.Get("q") != "alice" {
		t.Errorf("q = %q, want alice", gotQueryParams.Get("q"))
	}
	if gotQueryParams.Get("first") != "0" {
		t.Errorf("first = %q, want 0", gotQueryParams.Get("first"))
	}
	if gotQueryParams.Get("max") != "10" {
		t.Errorf("max = %q, want 10", gotQueryParams.Get("max"))
	}
	if gotQueryParams.Get("enabled") != "true" {
		t.Errorf("enabled = %q, want true", gotQueryParams.Get("enabled"))
	}
	if len(users) != 1 || users[0].Username != "alice" {
		t.Fatalf("users = %+v, want [alice]", users)
	}
}

func TestDirectoryClientSearchReturnsEmptyResults(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/realms/test-realm/protocol/openid-connect/token":
			writeTestJSON(t, w, http.StatusOK, map[string]any{"access_token": "tok"})
		case "/admin/realms/test-realm/users":
			writeTestJSON(t, w, http.StatusOK, []DirectoryUser{})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := newDirectoryClientForServer(t, server.URL)
	if err := client.Authenticate(context.Background()); err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}

	users, err := client.SearchUsers(context.Background(), "nobody", 0, 10)
	if err != nil {
		t.Fatalf("SearchUsers() error = %v", err)
	}
	if len(users) != 0 {
		t.Fatalf("users = %+v, want empty", users)
	}
}

func TestDirectoryClientSearchForwardsPagination(t *testing.T) {
	t.Parallel()

	var gotQueryParams url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/realms/test-realm/protocol/openid-connect/token":
			writeTestJSON(t, w, http.StatusOK, map[string]any{"access_token": "tok"})
		case "/admin/realms/test-realm/users":
			gotQueryParams = r.URL.Query()
			writeTestJSON(t, w, http.StatusOK, []DirectoryUser{})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := newDirectoryClientForServer(t, server.URL)
	if err := client.Authenticate(context.Background()); err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}

	if _, err := client.SearchUsers(context.Background(), "", 20, 5); err != nil {
		t.Fatalf("SearchUsers() error = %v", err)
	}
	if gotQueryParams.Get("first") != "20" {
		t.Errorf("first = %q, want 20", gotQueryParams.Get("first"))
	}
	if gotQueryParams.Get("max") != "5" {
		t.Errorf("max = %q, want 5", gotQueryParams.Get("max"))
	}
}

func TestDirectoryClientAuthFailure(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":"unauthorized"}`)
	}))
	defer server.Close()

	client := newDirectoryClientForServer(t, server.URL)
	err := client.Authenticate(context.Background())
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("Authenticate() error = %v, want 401 error", err)
	}
}

func TestDirectoryClientSearchBeforeAuth(t *testing.T) {
	t.Parallel()

	client := newDirectoryClientForServer(t, "http://localhost:1")
	_, err := client.SearchUsers(context.Background(), "query", 0, 10)
	if err == nil || !strings.Contains(err.Error(), "authentication") {
		t.Fatalf("SearchUsers() error = %v, want authentication error", err)
	}
}

func TestDirectoryClientKeycloakServerError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/realms/test-realm/protocol/openid-connect/token":
			writeTestJSON(t, w, http.StatusOK, map[string]any{"access_token": "tok"})
		default:
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"error":"internal"}`)
		}
	}))
	defer server.Close()

	client := newDirectoryClientForServer(t, server.URL)
	if err := client.Authenticate(context.Background()); err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}

	_, err := client.SearchUsers(context.Background(), "q", 0, 10)
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("SearchUsers() error = %v, want 500 error", err)
	}
}

func TestDirectoryClientMalformedJSON(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/realms/test-realm/protocol/openid-connect/token":
			writeTestJSON(t, w, http.StatusOK, map[string]any{"access_token": "tok"})
		default:
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{`)
		}
	}))
	defer server.Close()

	client := newDirectoryClientForServer(t, server.URL)
	if err := client.Authenticate(context.Background()); err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}

	_, err := client.SearchUsers(context.Background(), "q", 0, 10)
	if err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("SearchUsers() error = %v, want decode error", err)
	}
}

func TestDirectoryClientTimeout(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		switch r.URL.Path {
		case "/realms/test-realm/protocol/openid-connect/token":
			writeTestJSON(t, w, http.StatusOK, map[string]any{"access_token": "tok"})
		default:
			writeTestJSON(t, w, http.StatusOK, []DirectoryUser{})
		}
	}))
	defer server.Close()

	cfg := DirectoryClientConfig{
		AdminURL:     mustParseURL(t, server.URL),
		Realm:        "test-realm",
		ClientID:     "test-client",
		ClientSecret: "test-secret",
		HTTPTimeout:  1 * time.Millisecond,
	}
	client := NewDirectoryClient(cfg)

	err := client.Authenticate(context.Background())
	if err == nil {
		t.Fatal("Authenticate() error = nil, want timeout error")
	}
}

func TestDirectoryClientRedactsSecrets(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1024))
		_ = body
		_, _ = io.WriteString(w, "my-secret-value in error response")
	}))
	defer server.Close()

	cfg := DirectoryClientConfig{
		AdminURL:      mustParseURL(t, server.URL),
		Realm:         "test-realm",
		ClientID:      "test-client",
		ClientSecret:  "my-secret-value",
		HTTPTimeout:   5 * time.Second,
	}
	client := NewDirectoryClient(cfg)

	err := client.Authenticate(context.Background())
	if err == nil {
		t.Fatal("Authenticate() error = nil, want error")
	}
	if strings.Contains(err.Error(), "my-secret-value") {
		t.Fatalf("error leaked secret: %s", err.Error())
	}
	if !strings.Contains(err.Error(), "[REDACTED]") {
		t.Fatalf("error = %q, want [REDACTED]", err.Error())
	}
}
