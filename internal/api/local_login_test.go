package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vishu42/tflive/internal/app"
	"github.com/vishu42/tflive/internal/authn"
	"github.com/vishu42/tflive/internal/secrets"
)

type stubLocalAuthenticator struct {
	identity authn.Identity
	err      error

	gotUsername string
	gotPassword string
	calls       int
}

func (s *stubLocalAuthenticator) Authenticate(_ context.Context, username, password string) (authn.Identity, error) {
	s.calls++
	s.gotUsername, s.gotPassword = username, password
	return s.identity, s.err
}

func testIdentity() authn.Identity {
	return authn.Identity{
		Subject:           "local_root",
		DisplayName:       "Root",
		PreferredUsername: "root",
		Email:             "root@tflive.local",
	}
}

// newLocalLoginServer builds a server with local auth wired and no OIDC flow,
// which is the IdP-less deployment this issue exists to make possible.
func newLocalLoginServer(
	t *testing.T,
	authenticator LocalAuthenticator,
	sessions authn.SessionStore,
	users app.UserRepository,
) *Server {
	t.Helper()

	sealer, err := secrets.NewCipher("01234567890123456789012345678901")
	if err != nil {
		t.Fatalf("NewCipher returned error: %v", err)
	}
	return NewServer(app.NewService(app.Service{Users: users}), "tenant_123", WithAuth(AuthConfig{
		LocalAuthenticator: authenticator,
		Sealer:             sealer,
		PublicURL:          "http://localhost:5173",
		Sessions:           sessions,
		SessionAbsoluteTTL: authn.DefaultSessionAbsoluteTTL,
		SessionIdleTTL:     authn.DefaultSessionIdleTTL,
	}))
}

func newLocalLoginRequest(body string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "/v1/auth/login", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	return request
}

func TestLocalLoginSetsASessionCookieForCorrectCredentials(t *testing.T) {
	sessions := newFakeSessionStore()
	server := newLocalLoginServer(t, &stubLocalAuthenticator{identity: testIdentity()}, sessions, &apiFakeUserRepository{})

	response := httptest.NewRecorder()
	server.ServeHTTP(response, newLocalLoginRequest(`{"username":"root","password":"hunter2"}`))

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body = %s", response.Code, response.Body.String())
	}
	cookie := cookieByName(response, authn.SessionCookieName)
	if cookie == nil || cookie.Value == "" {
		t.Fatal("no session cookie was set")
	}
	if !cookie.HttpOnly {
		t.Fatal("session cookie is not HttpOnly")
	}
	if len(sessions.created) != 1 {
		t.Fatalf("created %d sessions, want 1", len(sessions.created))
	}
	session := sessions.created[0]
	if session.Subject != "local_root" || session.PreferredUsername != "root" || session.Email != "root@tflive.local" {
		t.Fatalf("session identity = %+v, want the authenticated identity", session)
	}
	if session.IDHash != authn.HashSessionID(cookie.Value) {
		t.Fatal("the session row does not name the cookie that was set")
	}
}

// A local session has no IdP behind it, so it carries no ID token and no IdP
// session id. Storing anything there would make logout offer an
// id_token_hint for a provider that never issued one.
func TestLocalLoginCreatesASessionWithNoIdPToken(t *testing.T) {
	sessions := newFakeSessionStore()
	server := newLocalLoginServer(t, &stubLocalAuthenticator{identity: testIdentity()}, sessions, &apiFakeUserRepository{})

	server.ServeHTTP(httptest.NewRecorder(), newLocalLoginRequest(`{"username":"root","password":"hunter2"}`))

	session := sessions.created[0]
	if session.IDToken != "" {
		t.Fatalf("IDToken = %q, want empty for a local session", session.IDToken)
	}
	if session.IDPSessionID != "" {
		t.Fatalf("IDPSessionID = %q, want empty for a local session", session.IDPSessionID)
	}
}

// The projection is what makes a user grantable and searchable. A local
// sign-in must reach it through the same path a federated one does.
func TestLocalLoginProjectsTheSignedInUser(t *testing.T) {
	users := &apiFakeUserRepository{}
	server := newLocalLoginServer(t, &stubLocalAuthenticator{identity: testIdentity()}, newFakeSessionStore(), users)

	server.ServeHTTP(httptest.NewRecorder(), newLocalLoginRequest(`{"username":"root","password":"hunter2"}`))

	if len(users.users) != 1 {
		t.Fatalf("projected %d users, want 1", len(users.users))
	}
	if users.users[0] != (app.UserProfile{Sub: "local_root", DisplayName: "Root", Email: "root@tflive.local"}) {
		t.Fatalf("projected %+v, want the authenticated identity", users.users[0])
	}
}

func TestLocalLoginRejectsInvalidCredentials(t *testing.T) {
	sessions := newFakeSessionStore()
	authenticator := &stubLocalAuthenticator{err: authn.ErrInvalidCredentials}
	server := newLocalLoginServer(t, authenticator, sessions, &apiFakeUserRepository{})

	response := httptest.NewRecorder()
	server.ServeHTTP(response, newLocalLoginRequest(`{"username":"root","password":"wrong"}`))

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", response.Code)
	}
	if cookieByName(response, authn.SessionCookieName) != nil {
		t.Fatal("a session cookie was set for invalid credentials")
	}
	if len(sessions.created) != 0 {
		t.Fatalf("created %d sessions for invalid credentials, want 0", len(sessions.created))
	}
}

// A store outage is not a wrong password. Reporting it as 401 tells the user
// to retype a correct password and buries the outage in the 401 rate.
func TestLocalLoginReportsAnAuthenticatorFailureAsServerError(t *testing.T) {
	authenticator := &stubLocalAuthenticator{err: errors.New("connection refused")}
	server := newLocalLoginServer(t, authenticator, newFakeSessionStore(), &apiFakeUserRepository{})

	response := httptest.NewRecorder()
	server.ServeHTTP(response, newLocalLoginRequest(`{"username":"root","password":"hunter2"}`))

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", response.Code)
	}
}

// The response body must not name which half was wrong, and must not echo the
// submitted username back into the page.
func TestLocalLoginFailureDisclosesNothing(t *testing.T) {
	authenticator := &stubLocalAuthenticator{err: authn.ErrInvalidCredentials}
	server := newLocalLoginServer(t, authenticator, newFakeSessionStore(), &apiFakeUserRepository{})

	response := httptest.NewRecorder()
	server.ServeHTTP(response, newLocalLoginRequest(`{"username":"root","password":"wrong"}`))

	body := response.Body.String()
	for _, leak := range []string{"root", "password", "username", "exist"} {
		if strings.Contains(strings.ToLower(body), leak) {
			t.Fatalf("failure body %q mentions %q", body, leak)
		}
	}
}

func TestLocalLoginRejectsAMalformedBody(t *testing.T) {
	authenticator := &stubLocalAuthenticator{identity: testIdentity()}
	server := newLocalLoginServer(t, authenticator, newFakeSessionStore(), &apiFakeUserRepository{})

	response := httptest.NewRecorder()
	server.ServeHTTP(response, newLocalLoginRequest(`{"username":`))

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.Code)
	}
	if authenticator.calls != 0 {
		t.Fatal("a malformed body reached the authenticator")
	}
}

// Safety rule 1: disabled means the route does not exist. With no
// authenticator wired there is nothing to fall through to — no verifier that
// might accept the credentials some other way, and no handler to reach.
//
// The status is 405 rather than 404 because GET /v1/auth/login is registered
// for the SSO redirect, so the path exists and only the method does not. That
// distinction discloses nothing: /v1/auth/methods states which methods are
// live, because the sign-in screen has to know.
func TestLocalLoginIsNotRegisteredWithoutAnAuthenticator(t *testing.T) {
	sealer, err := secrets.NewCipher("01234567890123456789012345678901")
	if err != nil {
		t.Fatalf("NewCipher returned error: %v", err)
	}
	server := NewServer(app.NewService(app.Service{Users: &apiFakeUserRepository{}}), "tenant_123", WithAuth(AuthConfig{
		Flow:               &stubFlow{},
		Verifier:           stubVerifier{},
		Sealer:             sealer,
		PublicURL:          "http://localhost:5173",
		Sessions:           newFakeSessionStore(),
		SessionAbsoluteTTL: authn.DefaultSessionAbsoluteTTL,
		SessionIdleTTL:     authn.DefaultSessionIdleTTL,
	}))

	response := httptest.NewRecorder()
	server.ServeHTTP(response, newLocalLoginRequest(`{"username":"root","password":"hunter2"}`))

	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405 when local auth is not configured", response.Code)
	}
	if cookieByName(response, authn.SessionCookieName) != nil {
		t.Fatal("a session cookie was set with local auth disabled")
	}
}

// The credentials are passed through untouched: folding and trimming are the
// authenticator's job, and doing them in two places invites them to disagree.
func TestLocalLoginPassesCredentialsThrough(t *testing.T) {
	authenticator := &stubLocalAuthenticator{identity: testIdentity()}
	server := newLocalLoginServer(t, authenticator, newFakeSessionStore(), &apiFakeUserRepository{})

	server.ServeHTTP(httptest.NewRecorder(), newLocalLoginRequest(`{"username":"  ROOT ","password":" hunter2 "}`))

	if authenticator.gotUsername != "  ROOT " {
		t.Fatalf("username = %q, want it passed through unchanged", authenticator.gotUsername)
	}
	if authenticator.gotPassword != " hunter2 " {
		t.Fatalf("password = %q, want it passed through unchanged", authenticator.gotPassword)
	}
}

// A failed projection fails the sign-in, exactly as it does at the OIDC
// callback: a session whose user is invisible to search and cannot be granted
// a role is worse than no session.
func TestLocalLoginFailsWhenTheProjectionFails(t *testing.T) {
	sessions := newFakeSessionStore()
	users := &apiFakeUserRepository{upsertErr: errors.New("connection refused")}
	server := newLocalLoginServer(t, &stubLocalAuthenticator{identity: testIdentity()}, sessions, users)

	response := httptest.NewRecorder()
	server.ServeHTTP(response, newLocalLoginRequest(`{"username":"root","password":"hunter2"}`))

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", response.Code)
	}
	if len(sessions.created) != 0 {
		t.Fatal("a session was created despite a failed projection")
	}
	if cookieByName(response, authn.SessionCookieName) != nil {
		t.Fatal("a session cookie was set despite a failed projection")
	}
}

func TestLocalLoginResponseIsNotCached(t *testing.T) {
	server := newLocalLoginServer(t, &stubLocalAuthenticator{identity: testIdentity()}, newFakeSessionStore(), &apiFakeUserRepository{})

	response := httptest.NewRecorder()
	server.ServeHTTP(response, newLocalLoginRequest(`{"username":"root","password":"hunter2"}`))

	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", response.Header().Get("Cache-Control"))
	}
}

// A body large enough to exhaust memory must be refused before it is decoded.
func TestLocalLoginRejectsAnOversizedBody(t *testing.T) {
	authenticator := &stubLocalAuthenticator{identity: testIdentity()}
	server := newLocalLoginServer(t, authenticator, newFakeSessionStore(), &apiFakeUserRepository{})

	oversized := `{"username":"root","password":"` + strings.Repeat("a", 1<<20) + `"}`
	response := httptest.NewRecorder()
	server.ServeHTTP(response, newLocalLoginRequest(oversized))

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.Code)
	}
	if authenticator.calls != 0 {
		t.Fatal("an oversized body reached the authenticator")
	}
}

// The error body is the same JSON shape every other API error uses, so the
// SPA has one error path rather than a special case for this route.
func TestLocalLoginErrorBodyIsTheStandardShape(t *testing.T) {
	authenticator := &stubLocalAuthenticator{err: authn.ErrInvalidCredentials}
	server := newLocalLoginServer(t, authenticator, newFakeSessionStore(), &apiFakeUserRepository{})

	response := httptest.NewRecorder()
	server.ServeHTTP(response, newLocalLoginRequest(`{"username":"root","password":"wrong"}`))

	var body errorResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if body.Error != "unauthorized" {
		t.Fatalf("error code = %q, want unauthorized", body.Error)
	}
}

// The sign-in screen renders the methods that exist rather than discovering
// them by failing (#213), so it needs to be told which ones are live before it
// has a session.
func TestAuthMethodsReportsLocalOnlyWhenNoFlowIsWired(t *testing.T) {
	server := newLocalLoginServer(t, &stubLocalAuthenticator{}, newFakeSessionStore(), &apiFakeUserRepository{})

	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/auth/methods", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", response.Code, response.Body.String())
	}
	var body authMethodsResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !body.Local || body.OIDC {
		t.Fatalf("methods = %+v, want local only", body)
	}
}

func TestAuthMethodsReportsOIDCOnlyWhenNoLocalAuthenticatorIsWired(t *testing.T) {
	server := newAuthTestServer(t, &stubFlow{}, stubVerifier{})

	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/auth/methods", nil))

	var body authMethodsResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Local || !body.OIDC {
		t.Fatalf("methods = %+v, want oidc only", body)
	}
}

func TestAuthMethodsReportsBothWhenBothAreWired(t *testing.T) {
	server := newAuthTestServer(t, &stubFlow{}, stubVerifier{}, withLocalAuthenticator(&stubLocalAuthenticator{}))

	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/auth/methods", nil))

	var body authMethodsResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !body.Local || !body.OIDC {
		t.Fatalf("methods = %+v, want both", body)
	}
}

// It is read before any session exists, so the middleware must let it through.
func TestAuthMethodsIsReachableWithoutASession(t *testing.T) {
	server := newLocalLoginServer(t, &stubLocalAuthenticator{}, newFakeSessionStore(), &apiFakeUserRepository{})
	authenticated := NewAuthenticatedServer(
		app.NewService(app.Service{Users: &apiFakeUserRepository{}}),
		"tenant_123",
		false,
		WithAuth(server.auth),
	)

	response := httptest.NewRecorder()
	authenticated.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/auth/methods", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 without a session", response.Code)
	}
}

func TestAuthMethodsIsNotCached(t *testing.T) {
	server := newLocalLoginServer(t, &stubLocalAuthenticator{}, newFakeSessionStore(), &apiFakeUserRepository{})

	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/auth/methods", nil))

	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", response.Header().Get("Cache-Control"))
	}
}

// Ending a session is not an OIDC operation. While every auth route was gated
// on a Flow, a deployment with local accounts and no IdP had no logout route at
// all, so a local session could be started and never ended.
func TestLogoutRevokesALocalSessionWithNoFlowWired(t *testing.T) {
	sessions := newFakeSessionStore()
	server := newLocalLoginServer(t, &stubLocalAuthenticator{identity: testIdentity()}, sessions, &apiFakeUserRepository{})

	signIn := httptest.NewRecorder()
	server.ServeHTTP(signIn, newLocalLoginRequest(`{"username":"root","password":"hunter2"}`))
	cookie := cookieByName(signIn, authn.SessionCookieName)
	if cookie == nil {
		t.Fatal("sign-in set no session cookie")
	}

	request := httptest.NewRequest(http.MethodPost, "/v1/auth/logout", nil)
	request.AddCookie(&http.Cookie{Name: authn.SessionCookieName, Value: cookie.Value})
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)

	if response.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303; body = %s", response.Code, response.Body.String())
	}
	if sessions.revoked[authn.HashSessionID(cookie.Value)] != 1 {
		t.Fatal("the session row was not revoked")
	}
	cleared := cookieByName(response, authn.SessionCookieName)
	if cleared == nil || cleared.MaxAge >= 0 {
		t.Fatal("the session cookie was not cleared")
	}
}

// With no provider behind the session there is no end-session URL to send the
// browser to, so it lands back on the app rather than at an IdP that never
// issued anything.
func TestLogoutRedirectsToTheAppForALocalSession(t *testing.T) {
	sessions := newFakeSessionStore()
	server := newLocalLoginServer(t, &stubLocalAuthenticator{identity: testIdentity()}, sessions, &apiFakeUserRepository{})

	signIn := httptest.NewRecorder()
	server.ServeHTTP(signIn, newLocalLoginRequest(`{"username":"root","password":"hunter2"}`))
	cookie := cookieByName(signIn, authn.SessionCookieName)

	request := httptest.NewRequest(http.MethodPost, "/v1/auth/logout", nil)
	request.AddCookie(&http.Cookie{Name: authn.SessionCookieName, Value: cookie.Value})
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)

	if location := response.Header().Get("Location"); location != "http://localhost:5173/" {
		t.Fatalf("Location = %q, want the app root", location)
	}
}

// A cross-site page must not be able to sign the visitor into an account it
// controls. The session cookie is SameSite=Lax, which permits the cookie the
// response sets, so nothing downstream would notice; the OIDC flow is protected
// by its sealed state cookie and this route has no equivalent transaction.
func TestLocalLoginRejectsCrossSiteRequests(t *testing.T) {
	for name, decorate := range map[string]func(*http.Request){
		"cross-site fetch metadata": func(request *http.Request) {
			request.Header.Set("Sec-Fetch-Site", "cross-site")
		},
		"sibling-origin fetch metadata": func(request *http.Request) {
			request.Header.Set("Sec-Fetch-Site", "same-site")
		},
		"foreign origin": func(request *http.Request) {
			request.Header.Set("Origin", "https://evil.test")
		},
		"opaque origin": func(request *http.Request) {
			request.Header.Set("Origin", "null")
		},
	} {
		t.Run(name, func(t *testing.T) {
			authenticator := &stubLocalAuthenticator{identity: testIdentity()}
			server := newLocalLoginServer(t, authenticator, newFakeSessionStore(), &apiFakeUserRepository{})

			request := newLocalLoginRequest(`{"username":"root","password":"hunter2"}`)
			decorate(request)
			response := httptest.NewRecorder()
			server.ServeHTTP(response, request)

			if response.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403; body = %s", response.Code, response.Body.String())
			}
			if authenticator.calls != 0 {
				t.Fatal("credentials were checked for a cross-site request")
			}
			if cookieByName(response, authn.SessionCookieName) != nil {
				t.Fatal("a cross-site request was given a session cookie")
			}
		})
	}
}

// The app's own page and a non-browser caller both get through: fetch metadata
// naming our own origin, an Origin matching the public URL, and neither header
// at all, which is curl or a script and carries nobody's ambient cookies.
func TestLocalLoginAcceptsSameOriginAndNonBrowserRequests(t *testing.T) {
	for name, decorate := range map[string]func(*http.Request){
		"same-origin fetch metadata": func(request *http.Request) {
			request.Header.Set("Sec-Fetch-Site", "same-origin")
			request.Header.Set("Origin", "http://localhost:5173")
		},
		"typed address": func(request *http.Request) {
			request.Header.Set("Sec-Fetch-Site", "none")
		},
		"matching origin only": func(request *http.Request) {
			request.Header.Set("Origin", "http://localhost:5173")
		},
		"no browser headers": func(*http.Request) {},
	} {
		t.Run(name, func(t *testing.T) {
			server := newLocalLoginServer(t, &stubLocalAuthenticator{identity: testIdentity()}, newFakeSessionStore(), &apiFakeUserRepository{})

			request := newLocalLoginRequest(`{"username":"root","password":"hunter2"}`)
			decorate(request)
			response := httptest.NewRecorder()
			server.ServeHTTP(response, request)

			if response.Code != http.StatusNoContent {
				t.Fatalf("status = %d, want 204; body = %s", response.Code, response.Body.String())
			}
		})
	}
}

// An HTML form can only post text/plain, urlencoded, or multipart, and
// json.Decoder reads a JSON object straight out of a text/plain form body. A
// cross-origin fetch that does declare JSON is preflighted, so requiring the
// declared type is the half of the defence that does not depend on a header
// the request may simply omit.
func TestLocalLoginRejectsANonJSONContentType(t *testing.T) {
	for name, contentType := range map[string]string{
		"form text/plain": "text/plain",
		"form urlencoded": "application/x-www-form-urlencoded",
		"form multipart":  "multipart/form-data; boundary=x",
		"absent":          "",
		"unparseable":     "application/json;;",
	} {
		t.Run(name, func(t *testing.T) {
			authenticator := &stubLocalAuthenticator{identity: testIdentity()}
			server := newLocalLoginServer(t, authenticator, newFakeSessionStore(), &apiFakeUserRepository{})

			request := httptest.NewRequest(http.MethodPost, "/v1/auth/login", strings.NewReader(`{"username":"root","password":"hunter2"}`))
			if contentType != "" {
				request.Header.Set("Content-Type", contentType)
			}
			response := httptest.NewRecorder()
			server.ServeHTTP(response, request)

			if response.Code != http.StatusUnsupportedMediaType {
				t.Fatalf("status = %d, want 415; body = %s", response.Code, response.Body.String())
			}
			if authenticator.calls != 0 {
				t.Fatal("credentials were checked for a body that was not declared as JSON")
			}
		})
	}
}

// A charset parameter is legitimate on application/json and must not be read as
// a different media type.
func TestLocalLoginAcceptsJSONWithParameters(t *testing.T) {
	server := newLocalLoginServer(t, &stubLocalAuthenticator{identity: testIdentity()}, newFakeSessionStore(), &apiFakeUserRepository{})

	request := httptest.NewRequest(http.MethodPost, "/v1/auth/login", strings.NewReader(`{"username":"root","password":"hunter2"}`))
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body = %s", response.Code, response.Body.String())
	}
}

// Sessions are rows and outlive the configuration that created them. An install
// that had OIDC removed still serves logout for sessions minted while it was
// on, and those carry an ID token -- which used to be the only condition
// checked before dereferencing the now-absent Flow.
func TestLogoutWithAnIDTokenAndNoFlowRedirectsHome(t *testing.T) {
	sessions := newFakeSessionStore()
	server := newLocalLoginServer(t, &stubLocalAuthenticator{identity: testIdentity()}, sessions, &apiFakeUserRepository{})

	// A session left behind by the OIDC configuration that has since been
	// removed: it names an ID token, and there is no Flow to build a hint for.
	sessionID := "leftover-federated-session"
	idHash := authn.HashSessionID(sessionID)
	if err := sessions.CreateSession(context.Background(), authn.Session{
		IDHash:  idHash,
		Subject: "keycloak-subject",
		IDToken: "an.id.token",
	}); err != nil {
		t.Fatalf("CreateSession returned error: %v", err)
	}

	request := httptest.NewRequest(http.MethodPost, "/v1/auth/logout", nil)
	request.AddCookie(&http.Cookie{Name: authn.SessionCookieName, Value: sessionID})
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)

	if response.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303; body = %s", response.Code, response.Body.String())
	}
	if location := response.Header().Get("Location"); location != "http://localhost:5173/" {
		t.Fatalf("Location = %q, want the app root", location)
	}
	if sessions.revoked[idHash] != 1 {
		t.Fatal("the session row was not revoked")
	}
}
