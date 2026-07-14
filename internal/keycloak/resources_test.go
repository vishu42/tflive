package keycloak

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEnsureRealmCreatesOnceThenUpdatesOwnedFields(t *testing.T) {
	t.Parallel()

	var realm map[string]any
	creates := 0
	updates := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("Authorization = %q", got)
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/admin/realms/tflive":
			if realm == nil {
				http.NotFound(w, r)
				return
			}
			realm["operatorSetting"] = "preserved"
			writeTestJSON(t, w, http.StatusOK, realm)
		case r.Method == http.MethodPost && r.URL.Path == "/admin/realms":
			creates++
			decodeTestJSON(t, r, &realm)
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodPut && r.URL.Path == "/admin/realms/tflive":
			updates++
			decodeTestJSON(t, r, &realm)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := authenticatedClientForServer(t, server.URL)
	spec := RealmSpec{Name: "tflive", Enabled: true, AccessTokenLifespan: 300, SSLRequired: "external"}
	for run := 1; run <= 2; run++ {
		if err := client.EnsureRealm(context.Background(), spec); err != nil {
			t.Fatalf("EnsureRealm() run %d error = %v", run, err)
		}
	}
	if creates != 1 || updates != 2 {
		t.Fatalf("creates = %d, updates = %d", creates, updates)
	}
	if realm["operatorSetting"] != "preserved" || realm["accessTokenLifespan"] != float64(300) || realm["enabled"] != true {
		t.Fatalf("realm = %#v", realm)
	}
}

func TestEnsureClientCreatesOnceRepairsDriftAndPreservesUnknownFields(t *testing.T) {
	t.Parallel()

	var clientResource map[string]any
	creates := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/admin/realms/tflive/clients" && clientResource == nil:
			writeTestJSON(t, w, http.StatusOK, []any{})
		case r.Method == http.MethodPost && r.URL.Path == "/admin/realms/tflive/clients":
			creates++
			decodeTestJSON(t, r, &clientResource)
			clientResource["id"] = "web-uuid"
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodGet && r.URL.Path == "/admin/realms/tflive/clients":
			writeTestJSON(t, w, http.StatusOK, []any{map[string]any{"id": "web-uuid", "clientId": "tflive-web"}})
		case r.Method == http.MethodGet && r.URL.Path == "/admin/realms/tflive/clients/web-uuid":
			clientResource["operatorSetting"] = "preserved"
			clientResource["publicClient"] = false
			clientResource["redirectUris"] = []string{"http://drift.invalid/*"}
			writeTestJSON(t, w, http.StatusOK, clientResource)
		case r.Method == http.MethodPut && r.URL.Path == "/admin/realms/tflive/clients/web-uuid":
			decodeTestJSON(t, r, &clientResource)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := authenticatedClientForServer(t, server.URL)
	spec := ClientSpec{
		ClientID:            "tflive-web",
		Name:                "tflive web",
		Enabled:             true,
		Protocol:            "openid-connect",
		PublicClient:        true,
		StandardFlowEnabled: true,
		RedirectURIs:        []string{"http://localhost:5173/"},
		WebOrigins:          []string{"http://localhost:5173"},
		Attributes:          map[string]string{"pkce.code.challenge.method": "S256"},
	}
	for run := 1; run <= 2; run++ {
		ref, err := client.EnsureClient(context.Background(), "tflive", spec)
		if err != nil {
			t.Fatalf("EnsureClient() run %d error = %v", run, err)
		}
		if ref.ID != "web-uuid" || ref.Name != "tflive-web" {
			t.Fatalf("ref = %#v", ref)
		}
	}
	if creates != 1 {
		t.Fatalf("creates = %d, want 1", creates)
	}
	if clientResource["operatorSetting"] != "preserved" || clientResource["publicClient"] != true {
		t.Fatalf("client = %#v", clientResource)
	}
	redirects, ok := clientResource["redirectUris"].([]any)
	if !ok || len(redirects) != 1 || redirects[0] != "http://localhost:5173/" {
		t.Fatalf("redirectUris = %#v", clientResource["redirectUris"])
	}
}

func TestEnsureClientRejectsDuplicateExactClientIDs(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeTestJSON(t, w, http.StatusOK, []any{
			map[string]any{"id": "one", "clientId": "tflive-web"},
			map[string]any{"id": "two", "clientId": "tflive-web"},
		})
	}))
	defer server.Close()

	client := authenticatedClientForServer(t, server.URL)
	_, err := client.EnsureClient(context.Background(), "tflive", ClientSpec{ClientID: "tflive-web"})
	if err == nil || !strings.Contains(err.Error(), "multiple clients with clientId tflive-web") {
		t.Fatalf("EnsureClient() error = %v", err)
	}
}

func TestEnsureUserSetsPasswordOnlyWhenCreatingUser(t *testing.T) {
	t.Parallel()

	var user map[string]any
	creates := 0
	updates := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/admin/realms/tflive/users" && user == nil:
			writeTestJSON(t, w, http.StatusOK, []any{})
		case r.Method == http.MethodPost && r.URL.Path == "/admin/realms/tflive/users":
			creates++
			decodeTestJSON(t, r, &user)
			credentials, ok := user["credentials"].([]any)
			if !ok || len(credentials) != 1 || credentials[0].(map[string]any)["value"] != "platform-local-only-secret" {
				t.Fatalf("create credentials = %#v", user["credentials"])
			}
			user["id"] = "user-uuid"
			delete(user, "credentials")
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodGet && r.URL.Path == "/admin/realms/tflive/users":
			writeTestJSON(t, w, http.StatusOK, []any{map[string]any{"id": "user-uuid", "username": "tflive-platform-admin"}})
		case r.Method == http.MethodGet && r.URL.Path == "/admin/realms/tflive/users/user-uuid":
			user["operatorSetting"] = "preserved"
			writeTestJSON(t, w, http.StatusOK, user)
		case r.Method == http.MethodPut && r.URL.Path == "/admin/realms/tflive/users/user-uuid":
			updates++
			decodeTestJSON(t, r, &user)
			if _, ok := user["credentials"]; ok {
				t.Fatal("user update must not contain credentials")
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := authenticatedClientForServer(t, server.URL)
	spec := UserSpec{Username: "tflive-platform-admin", Password: "platform-local-only-secret", Enabled: true}
	for run := 1; run <= 2; run++ {
		if _, err := client.EnsureUser(context.Background(), "tflive", spec); err != nil {
			t.Fatalf("EnsureUser() run %d error = %v", run, err)
		}
	}
	if creates != 1 || updates != 2 {
		t.Fatalf("creates = %d, updates = %d", creates, updates)
	}
	if user["operatorSetting"] != "preserved" || user["enabled"] != true {
		t.Fatalf("user = %#v", user)
	}
}

func TestExampleAccessTokenParsesStringAndArrayAudiences(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		aud  any
		want []string
	}{
		{name: "string", aud: "tflive-api", want: []string{"tflive-api"}},
		{name: "array", aud: []string{"account", "tflive-api"}, want: []string{"account", "tflive-api"}},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				writeTestJSON(t, w, http.StatusOK, map[string]any{
					"aud":          tt.aud,
					"realm_access": map[string]any{"roles": []string{"platform-admin"}},
				})
			}))
			defer server.Close()

			client := authenticatedClientForServer(t, server.URL)
			token, err := client.ExampleAccessToken(
				context.Background(), "tflive",
				ResourceRef{ID: "web-uuid", Name: "tflive-web"},
				ResourceRef{ID: "user-uuid", Name: "tflive-platform-admin"},
			)
			if err != nil {
				t.Fatalf("ExampleAccessToken() error = %v", err)
			}
			if !equalStrings(token.Audience, tt.want) || !equalStrings(token.RealmRoles, []string{"platform-admin"}) {
				t.Fatalf("token = %#v", token)
			}
		})
	}
}

func authenticatedClientForServer(t *testing.T, serverURL string) *Client {
	t.Helper()
	client := NewClient(configForServer(t, serverURL))
	client.accessToken = "test-token"
	return client
}

func decodeTestJSON(t *testing.T, r *http.Request, target any) {
	t.Helper()
	if err := json.NewDecoder(r.Body).Decode(target); err != nil {
		t.Fatalf("decode request %s %s: %v", r.Method, r.URL.Path, err)
	}
}

func testResourceID(resource map[string]any) string {
	id, _ := resource["id"].(string)
	return id
}

func unexpectedRequest(r *http.Request) error {
	return fmt.Errorf("unexpected request %s %s", r.Method, r.URL.String())
}
