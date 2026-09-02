package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vishu42/tflive/internal/authn"
)

// localOnlyAPIValues is the deployment #211 exists to make possible: local
// accounts, no IdP configuration at all.
func localOnlyAPIValues() map[string]string {
	values := apiTestValues()
	delete(values, "OIDC_ISSUER_URL")
	delete(values, "OIDC_CLIENT_ID")
	delete(values, "OIDC_CLIENT_SECRET")
	values["TFLIVE_LOCAL_AUTH_ENABLED"] = "true"
	return values
}

// Without an issuer there is no provider to discover, and constructing a
// verifier would reach for a well-known document that is not there. It is not
// merely unnecessary — it is the boot failure that makes local-only impossible.
func TestRunBuildsNoVerifierWithoutOIDC(t *testing.T) {
	t.Parallel()

	deps := newRecordingAPIDependencies(t)
	verifierBuilt := false
	deps.newVerifier = func(context.Context, authn.OIDCVerifierConfig) (tokenVerifier, error) {
		verifierBuilt = true
		return testTokenVerifier{}, nil
	}

	if err := runWithDependencies(context.Background(), apiTestGetenv(localOnlyAPIValues()), deps.apiDependencies); err != nil {
		t.Fatalf("runWithDependencies returned error: %v", err)
	}
	if verifierBuilt {
		t.Fatal("a token verifier was constructed with no OIDC configured")
	}
}

func TestRunServesLocalLoginWhenLocalAuthIsEnabled(t *testing.T) {
	t.Parallel()

	deps := newRecordingAPIDependencies(t)
	if err := runWithDependencies(context.Background(), apiTestGetenv(localOnlyAPIValues()), deps.apiDependencies); err != nil {
		t.Fatalf("runWithDependencies returned error: %v", err)
	}

	request := httptest.NewRequest(http.MethodPost, "/v1/auth/login", strings.NewReader(`{"username":"root","password":"wrong"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	deps.serverHandler.ServeHTTP(response, request)

	// 401, not 404 or 405: the route exists and reached the authenticator,
	// which is the whole point. The credentials are wrong because the test
	// seeds no account.
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body = %s", response.Code, response.Body.String())
	}
}

// The methods endpoint is what the sign-in screen reads, so it must reflect
// the deployment rather than a compile-time default.
func TestRunReportsLocalOnlyAuthMethods(t *testing.T) {
	t.Parallel()

	deps := newRecordingAPIDependencies(t)
	if err := runWithDependencies(context.Background(), apiTestGetenv(localOnlyAPIValues()), deps.apiDependencies); err != nil {
		t.Fatalf("runWithDependencies returned error: %v", err)
	}

	response := httptest.NewRecorder()
	deps.serverHandler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/auth/methods", nil))

	body := response.Body.String()
	if !strings.Contains(body, `"local":true`) || !strings.Contains(body, `"oidc":false`) {
		t.Fatalf("methods = %s, want local only", body)
	}
}

// The default deployment is unchanged: OIDC configured, local auth off, and
// no local login route to reach.
func TestRunLeavesLocalLoginOffByDefault(t *testing.T) {
	t.Parallel()

	deps := newRecordingAPIDependencies(t)
	if err := runWithDependencies(context.Background(), apiTestGetenv(apiTestValues()), deps.apiDependencies); err != nil {
		t.Fatalf("runWithDependencies returned error: %v", err)
	}

	request := httptest.NewRequest(http.MethodPost, "/v1/auth/login", strings.NewReader(`{"username":"root","password":"x"}`))
	response := httptest.NewRecorder()
	deps.serverHandler.ServeHTTP(response, request)

	if response.Code == http.StatusUnauthorized || response.Code == http.StatusNoContent {
		t.Fatalf("status = %d, want the local login route to be absent", response.Code)
	}

	methods := httptest.NewRecorder()
	deps.serverHandler.ServeHTTP(methods, httptest.NewRequest(http.MethodGet, "/v1/auth/methods", nil))
	if !strings.Contains(methods.Body.String(), `"local":false`) {
		t.Fatalf("methods = %s, want local disabled", methods.Body.String())
	}
}

// Both methods at once is the configuration a customer running Okta and
// holding a local root account is in.
func TestRunServesBothMethodsTogether(t *testing.T) {
	t.Parallel()

	values := apiTestValues()
	values["TFLIVE_LOCAL_AUTH_ENABLED"] = "true"

	deps := newRecordingAPIDependencies(t)
	if err := runWithDependencies(context.Background(), apiTestGetenv(values), deps.apiDependencies); err != nil {
		t.Fatalf("runWithDependencies returned error: %v", err)
	}

	response := httptest.NewRecorder()
	deps.serverHandler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/auth/methods", nil))

	body := response.Body.String()
	if !strings.Contains(body, `"local":true`) || !strings.Contains(body, `"oidc":true`) {
		t.Fatalf("methods = %s, want both", body)
	}

	// The SSO redirect still works alongside it.
	login := httptest.NewRecorder()
	deps.serverHandler.ServeHTTP(login, httptest.NewRequest(http.MethodGet, "/v1/auth/login", nil))
	if login.Code != http.StatusFound {
		t.Fatalf("SSO login status = %d, want 302", login.Code)
	}
}
