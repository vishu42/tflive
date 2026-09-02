package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vishu42/tflive/internal/authn"
	"github.com/vishu42/tflive/internal/authz"
	"github.com/vishu42/tflive/internal/bootstrap"
)

func cookieByName(response *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == name {
			return cookie
		}
	}
	return nil
}

// localOnlyAPIValues is the deployment #211 exists to make possible: local
// accounts, no IdP configuration at all.
func localOnlyAPIValues() map[string]string {
	values := apiTestValues()
	delete(values, "OIDC_ISSUER_URL")
	delete(values, "OIDC_CLIENT_ID")
	delete(values, "OIDC_CLIENT_SECRET")
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

func TestRunServesLocalLoginWithNoOIDC(t *testing.T) {
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

// There is no configuration that removes the password route. Root is a local
// account seeded at every boot and unable to be locked out (#212), so a
// deployment without this route would hold the highest-privileged identity in
// the model in a table it cannot sign in from.
func TestRunAlwaysServesLocalLoginEvenWithOIDCConfigured(t *testing.T) {
	t.Parallel()

	values := apiTestValues()
	values["TFLIVE_LOCAL_AUTH_ENABLED"] = "false"

	deps := newRecordingAPIDependencies(t)
	if err := runWithDependencies(context.Background(), apiTestGetenv(values), deps.apiDependencies); err != nil {
		t.Fatalf("runWithDependencies returned error: %v", err)
	}

	request := httptest.NewRequest(http.MethodPost, "/v1/auth/login", strings.NewReader(`{"username":"root","password":"wrong"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	deps.serverHandler.ServeHTTP(response, request)

	// 401 means the route exists and reached the authenticator. A 405 would
	// mean the setting had removed it.
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body = %s", response.Code, response.Body.String())
	}

	methods := httptest.NewRecorder()
	deps.serverHandler.ServeHTTP(methods, httptest.NewRequest(http.MethodGet, "/v1/auth/methods", nil))
	if !strings.Contains(methods.Body.String(), `"local":true`) {
		t.Fatalf("methods = %s, want local enabled", methods.Body.String())
	}
}

// Both methods at once is the configuration a customer running Okta and
// holding a local root account is in.
func TestRunServesBothMethodsTogether(t *testing.T) {
	t.Parallel()

	deps := newRecordingAPIDependencies(t)
	if err := runWithDependencies(context.Background(), apiTestGetenv(apiTestValues()), deps.apiDependencies); err != nil {
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

// #212: a fresh install is administrable without an IdP. Seeding runs at every
// boot, so the account and its tuple exist before the server serves.
func TestRunSeedsTheRootAccountAndTuple(t *testing.T) {
	t.Parallel()

	deps := newRecordingAPIDependencies(t)
	if err := runWithDependencies(context.Background(), apiTestGetenv(localOnlyAPIValues()), deps.apiDependencies); err != nil {
		t.Fatalf("runWithDependencies returned error: %v", err)
	}

	if len(deps.store.ensuredAccounts) != 1 {
		t.Fatalf("seeded %d accounts, want 1", len(deps.store.ensuredAccounts))
	}
	account := deps.store.ensuredAccounts[0]
	if account.Subject != bootstrap.DefaultRootSubject {
		t.Fatalf("Subject = %q, want %q", account.Subject, bootstrap.DefaultRootSubject)
	}
	if !authn.VerifyPassword(account.PasswordHash, "root-local-only") {
		t.Fatal("the seeded hash does not verify against the configured root password")
	}

	if len(deps.authorizer.written) != 1 {
		t.Fatalf("wrote %d tuples, want the root tuple", len(deps.authorizer.written))
	}
	if deps.authorizer.written[0].Relation() != authz.RelationRoot {
		t.Fatalf("relation = %q, want root", deps.authorizer.written[0].Relation())
	}
}

// Fail closed: an install that cannot be administered must not serve. Every
// route would answer 403 and nothing would say why.
func TestRunRefusesToStartWhenRootSeedingFails(t *testing.T) {
	t.Parallel()

	deps := newRecordingAPIDependencies(t)
	deps.store.ensureAccountErr = errors.New("connection refused")

	err := runWithDependencies(context.Background(), apiTestGetenv(localOnlyAPIValues()), deps.apiDependencies)
	if err == nil {
		t.Fatal("runWithDependencies served despite failing to seed root")
	}
	if !strings.Contains(err.Error(), "root") {
		t.Fatalf("error = %v, want it to name the root seeding failure", err)
	}
}

// Root is seeded whether or not an IdP is configured. The zero-admins problem
// is not local-specific: granting admin requires already being an admin.
func TestRunSeedsRootWithOIDCConfigured(t *testing.T) {
	t.Parallel()

	deps := newRecordingAPIDependencies(t)
	if err := runWithDependencies(context.Background(), apiTestGetenv(apiTestValues()), deps.apiDependencies); err != nil {
		t.Fatalf("runWithDependencies returned error: %v", err)
	}
	if len(deps.store.ensuredAccounts) != 1 {
		t.Fatalf("seeded %d accounts, want 1", len(deps.store.ensuredAccounts))
	}
}

// The seeded account can actually sign in. This is the end-to-end fact #212 is
// for: docker compose up, then sign in as root.
func TestRunLetsTheSeededRootSignIn(t *testing.T) {
	t.Parallel()

	deps := newRecordingAPIDependencies(t)
	if err := runWithDependencies(context.Background(), apiTestGetenv(localOnlyAPIValues()), deps.apiDependencies); err != nil {
		t.Fatalf("runWithDependencies returned error: %v", err)
	}

	request := httptest.NewRequest(http.MethodPost, "/v1/auth/login", strings.NewReader(`{"username":"root","password":"root-local-only"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	deps.serverHandler.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body = %s", response.Code, response.Body.String())
	}
	if cookieByName(response, authn.SessionCookieName) == nil {
		t.Fatal("signing in as the seeded root set no session cookie")
	}
}
