package keycloak

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestClientAuthenticatesAndSendsAuthorizedJSON(t *testing.T) {
	t.Parallel()

	var resourceCalled bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/realms/master/protocol/openid-connect/token":
			if r.Method != http.MethodPost {
				t.Errorf("token method = %s, want POST", r.Method)
			}
			if got := r.Header.Get("Content-Type"); !strings.HasPrefix(got, "application/x-www-form-urlencoded") {
				t.Errorf("token Content-Type = %q", got)
			}
			if err := r.ParseForm(); err != nil {
				t.Fatalf("ParseForm() error = %v", err)
			}
			wantForm := url.Values{
				"client_id":  {"admin-cli"},
				"grant_type": {"password"},
				"username":   {"tflive-admin"},
				"password":   {"master-local-only-secret"},
			}
			if got := r.Form.Encode(); got != wantForm.Encode() {
				t.Errorf("token form = %q, want %q", got, wantForm.Encode())
			}
			writeTestJSON(t, w, http.StatusOK, map[string]any{
				"access_token": "admin-access-token",
				"expires_in":   60,
			})
		case "/admin/realms/tflive/clients/client/id":
			resourceCalled = true
			if got, want := r.RequestURI, "/admin/realms/tflive/clients/client%2Fid?brief=true"; got != want {
				t.Errorf("RequestURI = %q, want %q", got, want)
			}
			if got, want := r.Header.Get("Authorization"), "Bearer admin-access-token"; got != want {
				t.Errorf("Authorization = %q, want %q", got, want)
			}
			if got := r.Header.Get("Content-Type"); got != "application/json" {
				t.Errorf("resource Content-Type = %q, want application/json", got)
			}
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("ReadAll() error = %v", err)
			}
			if got, want := strings.TrimSpace(string(body)), `{"enabled":true}`; got != want {
				t.Errorf("body = %q, want %q", got, want)
			}
			writeTestJSON(t, w, http.StatusOK, map[string]string{"id": "client-uuid"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cfg := configForServer(t, server.URL)
	client := NewClient(cfg)
	if err := client.Authenticate(context.Background()); err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}

	var response struct {
		ID string `json:"id"`
	}
	err := client.doJSON(
		context.Background(),
		http.MethodPut,
		[]string{"admin", "realms", "tflive", "clients", "client/id"},
		url.Values{"brief": {"true"}},
		map[string]bool{"enabled": true},
		[]int{http.StatusOK},
		&response,
	)
	if err != nil {
		t.Fatalf("doJSON() error = %v", err)
	}
	if !resourceCalled || response.ID != "client-uuid" {
		t.Fatalf("resourceCalled = %v, response.ID = %q", resourceCalled, response.ID)
	}
}

func TestClientRedactsSecretsAndBoundsErrorResponses(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, "master-local-only-secret platform-local-only-secret "+strings.Repeat("x", 20_000))
	}))
	defer server.Close()

	client := NewClient(configForServer(t, server.URL))
	err := client.Authenticate(context.Background())
	if err == nil {
		t.Fatal("Authenticate() error = nil, want authentication failure")
	}
	message := err.Error()
	for _, secret := range []string{"master-local-only-secret", "platform-local-only-secret"} {
		if strings.Contains(message, secret) {
			t.Fatalf("error leaked secret %q: %s", secret, message)
		}
	}
	if !strings.Contains(message, "[REDACTED]") {
		t.Fatalf("error = %q, want redaction marker", message)
	}
	if len(message) > 5_000 {
		t.Fatalf("error length = %d, want bounded response", len(message))
	}
}

func TestClientRejectsMissingTokenAndMalformedJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "missing token", body: `{}`, want: "missing access_token"},
		{name: "malformed JSON", body: `{`, want: "decode authentication response"},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, tt.body)
			}))
			defer server.Close()

			client := NewClient(configForServer(t, server.URL))
			err := client.Authenticate(context.Background())
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Authenticate() error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestClientRejectsMalformedJSONResourceResponse(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/token") {
			writeTestJSON(t, w, http.StatusOK, map[string]string{"access_token": "token"})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{`)
	}))
	defer server.Close()

	client := NewClient(configForServer(t, server.URL))
	if err := client.Authenticate(context.Background()); err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	var response map[string]any
	err := client.doJSON(context.Background(), http.MethodGet, []string{"admin", "realms", "tflive"}, nil, nil, []int{http.StatusOK}, &response)
	if err == nil || !strings.Contains(err.Error(), "decode response") {
		t.Fatalf("doJSON() error = %v, want decode response error", err)
	}
}

func TestRedactSecretsUsesLongestValuesFirst(t *testing.T) {
	t.Parallel()

	got := redactSecrets("long-secret and secret", []string{"secret", "long-secret", ""})
	if got != "[REDACTED] and [REDACTED]" {
		t.Fatalf("redactSecrets() = %q", got)
	}
}

func configForServer(t *testing.T, serverURL string) Config {
	t.Helper()
	env := validConfigEnv()
	env["KEYCLOAK_ADMIN_URL"] = serverURL
	cfg, err := LoadConfig(mapEnv(env))
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	return cfg
}

func writeTestJSON(t *testing.T, w http.ResponseWriter, status int, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
}
