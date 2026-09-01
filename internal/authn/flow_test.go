package authn

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

type staticEndpoints struct{ endpoints Endpoints }

func (s staticEndpoints) Endpoints() Endpoints { return s.endpoints }

func newTestFlow(t *testing.T, endpoints Endpoints) *Flow {
	t.Helper()
	flow, err := NewFlow(FlowConfig{
		ClientID:     "tflive-api",
		ClientSecret: "client-secret",
		RedirectURI:  "http://localhost:5173/v1/auth/callback",
		Endpoints:    staticEndpoints{endpoints: endpoints},
	})
	if err != nil {
		t.Fatalf("NewFlow returned error: %v", err)
	}
	return flow
}

func TestAuthorizationURLCarriesFlowParameters(t *testing.T) {
	flow := newTestFlow(t, Endpoints{
		Authorization: "https://idp.test/authorize",
		Token:         "https://idp.test/token",
	})

	raw, err := flow.AuthorizationURL("state-1", "nonce-1", "verifier-1-verifier-1-verifier-1-verifier")
	if err != nil {
		t.Fatalf("AuthorizationURL returned error: %v", err)
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("url.Parse returned error: %v", err)
	}
	if parsed.Host != "idp.test" || parsed.Path != "/authorize" {
		t.Fatalf("authorization URL = %q", raw)
	}
	query := parsed.Query()
	for name, want := range map[string]string{
		"response_type":         "code",
		"client_id":             "tflive-api",
		"redirect_uri":          "http://localhost:5173/v1/auth/callback",
		"scope":                 "openid profile email",
		"state":                 "state-1",
		"nonce":                 "nonce-1",
		"code_challenge_method": "S256",
	} {
		if got := query.Get(name); got != want {
			t.Fatalf("%s = %q, want %q", name, got, want)
		}
	}
	if query.Get("code_challenge") == "" {
		t.Fatal("code_challenge is missing")
	}
	// The challenge is a hash; the verifier must never reach the front channel.
	if query.Get("code_challenge") == "verifier-1-verifier-1-verifier-1-verifier" {
		t.Fatal("code_challenge is the raw verifier")
	}
	if query.Has("client_secret") {
		t.Fatal("authorization URL carries the client secret")
	}
	if query.Has("offline_access") || query.Get("scope") == "openid profile email offline_access" {
		t.Fatal("authorization URL requests offline_access")
	}
}

func TestExchangeSendsClientCredentialsAndReturnsIDToken(t *testing.T) {
	var gotForm url.Values
	var gotUser, gotPassword string
	var gotBasic bool

	idp := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if err := request.ParseForm(); err != nil {
			t.Errorf("ParseForm returned error: %v", err)
		}
		gotForm = request.PostForm
		gotUser, gotPassword, gotBasic = request.BasicAuth()
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"access_token": "access-token",
			"id_token":     "raw.id.token",
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	}))
	t.Cleanup(idp.Close)

	flow := newTestFlow(t, Endpoints{Authorization: idp.URL + "/authorize", Token: idp.URL + "/token"})

	rawIDToken, err := flow.Exchange(context.Background(), "code-1", "verifier-1-verifier-1-verifier-1-verifier")
	if err != nil {
		t.Fatalf("Exchange returned error: %v", err)
	}
	if rawIDToken != "raw.id.token" {
		t.Fatalf("id token = %q", rawIDToken)
	}
	if !gotBasic || gotUser != "tflive-api" || gotPassword != "client-secret" {
		t.Fatalf("client authentication = %q/%q basic=%t", gotUser, gotPassword, gotBasic)
	}
	if gotForm.Get("grant_type") != "authorization_code" {
		t.Fatalf("grant_type = %q", gotForm.Get("grant_type"))
	}
	if gotForm.Get("code") != "code-1" {
		t.Fatalf("code = %q", gotForm.Get("code"))
	}
	if gotForm.Get("code_verifier") != "verifier-1-verifier-1-verifier-1-verifier" {
		t.Fatalf("code_verifier = %q", gotForm.Get("code_verifier"))
	}
	// RFC 6749 4.1.3: the redirect URI is repeated and must match, binding the
	// code to the URI it was issued for.
	if gotForm.Get("redirect_uri") != "http://localhost:5173/v1/auth/callback" {
		t.Fatalf("redirect_uri = %q", gotForm.Get("redirect_uri"))
	}
}

func TestExchangeFailsWhenResponseHasNoIDToken(t *testing.T) {
	idp := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"access_token":"access-token","token_type":"Bearer"}`))
	}))
	t.Cleanup(idp.Close)

	flow := newTestFlow(t, Endpoints{Authorization: idp.URL + "/authorize", Token: idp.URL + "/token"})
	if _, err := flow.Exchange(context.Background(), "code-1", "verifier"); err == nil {
		t.Fatal("Exchange accepted a response with no id_token")
	}
}

func TestEndSessionURL(t *testing.T) {
	flow := newTestFlow(t, Endpoints{
		Authorization: "https://idp.test/authorize",
		Token:         "https://idp.test/token",
		EndSession:    "https://idp.test/logout",
	})

	raw := flow.EndSessionURL("raw.id.token", "http://localhost:5173/")
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("url.Parse returned error: %v", err)
	}
	query := parsed.Query()
	if query.Get("id_token_hint") != "raw.id.token" {
		t.Fatalf("id_token_hint = %q", query.Get("id_token_hint"))
	}
	if query.Get("post_logout_redirect_uri") != "http://localhost:5173/" {
		t.Fatalf("post_logout_redirect_uri = %q", query.Get("post_logout_redirect_uri"))
	}
}

func TestEndSessionURLIsEmptyWithoutProviderSupport(t *testing.T) {
	flow := newTestFlow(t, Endpoints{Authorization: "https://idp.test/authorize", Token: "https://idp.test/token"})
	if raw := flow.EndSessionURL("raw.id.token", "http://localhost:5173/"); raw != "" {
		t.Fatalf("EndSessionURL = %q, want empty", raw)
	}
}

func TestNewFlowRejectsIncompleteConfig(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*FlowConfig)
	}{
		{name: "no client id", mutate: func(c *FlowConfig) { c.ClientID = "" }},
		{name: "no client secret", mutate: func(c *FlowConfig) { c.ClientSecret = "" }},
		{name: "relative redirect", mutate: func(c *FlowConfig) { c.RedirectURI = "/v1/auth/callback" }},
		{name: "no endpoints", mutate: func(c *FlowConfig) { c.Endpoints = nil }},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := FlowConfig{
				ClientID:     "tflive-api",
				ClientSecret: "client-secret",
				RedirectURI:  "http://localhost:5173/v1/auth/callback",
				Endpoints:    staticEndpoints{},
			}
			test.mutate(&cfg)
			if _, err := NewFlow(cfg); err == nil {
				t.Fatal("NewFlow accepted an incomplete config")
			}
		})
	}
}
