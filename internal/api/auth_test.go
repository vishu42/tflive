package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"testing"

	"github.com/vishu42/tflive/internal/authn"
	"github.com/vishu42/tflive/internal/secrets"
)

type stubFlow struct {
	authorizationURL string
	authorizationErr error
	idToken          string
	exchangeErr      error
	endSessionURL    string

	gotState        string
	gotNonce        string
	gotCodeVerifier string
	gotCode         string
	gotIDTokenHint  string
}

func (f *stubFlow) AuthorizationURL(state, nonce, codeVerifier string) (string, error) {
	f.gotState, f.gotNonce, f.gotCodeVerifier = state, nonce, codeVerifier
	return f.authorizationURL, f.authorizationErr
}

func (f *stubFlow) Exchange(_ context.Context, code, codeVerifier string) (string, error) {
	f.gotCode, f.gotCodeVerifier = code, codeVerifier
	return f.idToken, f.exchangeErr
}

func (f *stubFlow) EndSessionURL(idTokenHint, _ string) string {
	f.gotIDTokenHint = idTokenHint
	return f.endSessionURL
}

type stubVerifier struct {
	token authn.VerifiedToken
	err   error
}

func (v stubVerifier) Verify(context.Context, string) (authn.VerifiedToken, error) {
	return v.token, v.err
}

func newAuthTestServer(t *testing.T, flow *stubFlow, verifier authn.Verifier) *Server {
	t.Helper()
	sealer, err := secrets.NewCipher("01234567890123456789012345678901")
	if err != nil {
		t.Fatalf("NewCipher returned error: %v", err)
	}
	return NewServer(nil, "tenant_123", WithAuth(AuthConfig{
		Flow:          flow,
		Verifier:      verifier,
		Sealer:        sealer,
		PublicURL:     "http://localhost:5173",
		SecureCookies: false,
	}))
}

func cookieByName(response *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, cookie := range (&http.Response{Header: response.Header()}).Cookies() {
		if cookie.Name == name {
			return cookie
		}
	}
	return nil
}

func TestAuthLoginRedirectsAndSetsTransactionCookie(t *testing.T) {
	flow := &stubFlow{authorizationURL: "https://idp.test/authorize?state=x"}
	server := newAuthTestServer(t, flow, stubVerifier{})

	request := httptest.NewRequest(http.MethodGet, "/v1/auth/login?return_to=/stacks/abc", nil)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)

	if response.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", response.Code)
	}
	if location := response.Header().Get("Location"); location != flow.authorizationURL {
		t.Fatalf("Location = %q", location)
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q", response.Header().Get("Cache-Control"))
	}

	cookie := cookieByName(response, authn.TransactionCookieName)
	if cookie == nil {
		t.Fatal("transaction cookie is missing")
	}
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("transaction cookie = %#v", cookie)
	}
	if strings.Contains(cookie.Value, flow.gotState) || strings.Contains(cookie.Value, "/stacks/abc") {
		t.Fatal("transaction cookie is not sealed")
	}
	if flow.gotState == "" || flow.gotNonce == "" || flow.gotCodeVerifier == "" {
		t.Fatalf("flow parameters = %q/%q/%q", flow.gotState, flow.gotNonce, flow.gotCodeVerifier)
	}
	if flow.gotState == flow.gotNonce {
		t.Fatal("state and nonce are the same value")
	}
}

func TestAuthLoginRejectsOffOriginReturnTo(t *testing.T) {
	flow := &stubFlow{authorizationURL: "https://idp.test/authorize"}
	server := newAuthTestServer(t, flow, stubVerifier{})
	sealer, _ := secrets.NewCipher("01234567890123456789012345678901")

	request := httptest.NewRequest(http.MethodGet, "/v1/auth/login?return_to=https://evil.test/steal", nil)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)

	cookie := cookieByName(response, authn.TransactionCookieName)
	if cookie == nil {
		t.Fatal("transaction cookie is missing")
	}
	transaction, err := authn.OpenTransaction(sealer, cookie.Value)
	if err != nil {
		t.Fatalf("OpenTransaction returned error: %v", err)
	}
	if transaction.ReturnTo != "/" {
		t.Fatalf("ReturnTo = %q, want /", transaction.ReturnTo)
	}
}

func callbackRequest(t *testing.T, sealer *secrets.Cipher, transaction authn.Transaction, query url.Values) *http.Request {
	t.Helper()
	sealed, err := authn.SealTransaction(sealer, transaction)
	if err != nil {
		t.Fatalf("SealTransaction returned error: %v", err)
	}
	request := httptest.NewRequest(http.MethodGet, "/v1/auth/callback?"+query.Encode(), nil)
	request.AddCookie(&http.Cookie{Name: authn.TransactionCookieName, Value: sealed})
	return request
}

func TestAuthCallbackSetsSessionCookieAndRedirects(t *testing.T) {
	flow := &stubFlow{idToken: "raw.id.token"}
	verifier := stubVerifier{token: authn.VerifiedToken{Subject: "user-123", Nonce: "nonce-1"}}
	server := newAuthTestServer(t, flow, verifier)
	sealer, _ := secrets.NewCipher("01234567890123456789012345678901")

	transaction := authn.Transaction{State: "state-1", Nonce: "nonce-1", CodeVerifier: "verifier-1", ReturnTo: "/stacks/abc"}
	request := callbackRequest(t, sealer, transaction, url.Values{"code": {"code-1"}, "state": {"state-1"}})
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)

	if response.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302; body = %s", response.Code, response.Body.String())
	}
	if location := response.Header().Get("Location"); location != "/stacks/abc" {
		t.Fatalf("Location = %q", location)
	}
	session := cookieByName(response, authn.SessionCookieName)
	if session == nil || session.Value != "raw.id.token" || !session.HttpOnly {
		t.Fatalf("session cookie = %#v", session)
	}
	cleared := cookieByName(response, authn.TransactionCookieName)
	if cleared == nil || cleared.MaxAge != -1 {
		t.Fatalf("transaction cookie = %#v, want cleared", cleared)
	}
	if flow.gotCode != "code-1" || flow.gotCodeVerifier != "verifier-1" {
		t.Fatalf("exchange args = %q/%q", flow.gotCode, flow.gotCodeVerifier)
	}
	if body := response.Body.String(); strings.Contains(body, "raw.id.token") {
		t.Fatal("callback response body carries the token")
	}
}

func TestAuthCallbackFailuresAreIndistinguishable(t *testing.T) {
	sealer, _ := secrets.NewCipher("01234567890123456789012345678901")
	good := authn.Transaction{State: "state-1", Nonce: "nonce-1", CodeVerifier: "verifier-1", ReturnTo: "/stacks"}

	var bodies []string
	var headers []string
	for _, test := range []struct {
		name     string
		flow     *stubFlow
		verifier stubVerifier
		build    func(*testing.T) *http.Request
	}{
		{
			name: "idp error",
			flow: &stubFlow{}, verifier: stubVerifier{},
			build: func(t *testing.T) *http.Request {
				return callbackRequest(t, sealer, good, url.Values{"error": {"access_denied"}, "state": {"state-1"}})
			},
		},
		{
			name: "no transaction cookie",
			flow: &stubFlow{}, verifier: stubVerifier{},
			build: func(t *testing.T) *http.Request {
				return httptest.NewRequest(http.MethodGet, "/v1/auth/callback?code=code-1&state=state-1", nil)
			},
		},
		{
			name: "state mismatch",
			flow: &stubFlow{idToken: "raw.id.token"}, verifier: stubVerifier{},
			build: func(t *testing.T) *http.Request {
				return callbackRequest(t, sealer, good, url.Values{"code": {"code-1"}, "state": {"other-state"}})
			},
		},
		{
			name: "exchange fails",
			flow: &stubFlow{exchangeErr: context.Canceled}, verifier: stubVerifier{},
			build: func(t *testing.T) *http.Request {
				return callbackRequest(t, sealer, good, url.Values{"code": {"code-1"}, "state": {"state-1"}})
			},
		},
		{
			name: "verification fails",
			flow: &stubFlow{idToken: "raw.id.token"}, verifier: stubVerifier{err: authn.ErrInvalidToken},
			build: func(t *testing.T) *http.Request {
				return callbackRequest(t, sealer, good, url.Values{"code": {"code-1"}, "state": {"state-1"}})
			},
		},
		{
			name:     "nonce mismatch",
			flow:     &stubFlow{idToken: "raw.id.token"},
			verifier: stubVerifier{token: authn.VerifiedToken{Subject: "user-123", Nonce: "other-nonce"}},
			build: func(t *testing.T) *http.Request {
				return callbackRequest(t, sealer, good, url.Values{"code": {"code-1"}, "state": {"state-1"}})
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := newAuthTestServer(t, test.flow, test.verifier)
			response := httptest.NewRecorder()
			server.ServeHTTP(response, test.build(t))

			if response.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", response.Code)
			}
			if cookieByName(response, authn.SessionCookieName) != nil {
				t.Fatal("a failed callback set a session cookie")
			}
			// A failure leaving the transaction cookie live allows replay
			// within its 600-second Max-Age.
			clearedTx := cookieByName(response, authn.TransactionCookieName)
			if clearedTx == nil || clearedTx.MaxAge != -1 {
				t.Fatalf("transaction cookie = %#v, want cleared on failure", clearedTx)
			}
			bodies = append(bodies, response.Body.String())
			headers = append(headers, normalizedHeaderDump(response.Header()))
		})
	}

	// Distinguishable failures are an oracle: they tell an attacker which check
	// they tripped.
	for i := 1; i < len(bodies); i++ {
		if bodies[i] != bodies[0] {
			t.Fatalf("failure bodies differ:\n%q\n%q", bodies[0], bodies[i])
		}
	}
	// Bodies are not the only oracle: a future divergent Set-Cookie,
	// WWW-Authenticate, or X-Reason would leak the same information through a
	// header instead, and only comparing bodies would stay green.
	for i := 1; i < len(headers); i++ {
		if headers[i] != headers[0] {
			t.Fatalf("failure headers differ:\n%s\n---\n%s", headers[0], headers[i])
		}
	}
}

// normalizedHeaderDump renders response headers deterministically: sorted
// keys, and sorted values within a key, so every Set-Cookie is included and
// header order (which carries no meaning) cannot cause a false mismatch.
func normalizedHeaderDump(header http.Header) string {
	keys := make([]string, 0, len(header))
	for key := range header {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var dump strings.Builder
	for _, key := range keys {
		values := append([]string(nil), header[key]...)
		sort.Strings(values)
		for _, value := range values {
			dump.WriteString(key)
			dump.WriteString(": ")
			dump.WriteString(value)
			dump.WriteString("\n")
		}
	}
	return dump.String()
}

func TestAuthLogoutClearsCookieAndRedirectsToIdP(t *testing.T) {
	flow := &stubFlow{endSessionURL: "https://idp.test/logout?id_token_hint=raw.id.token"}
	server := newAuthTestServer(t, flow, stubVerifier{})

	request := httptest.NewRequest(http.MethodPost, "/v1/auth/logout", nil)
	request.AddCookie(&http.Cookie{Name: authn.SessionCookieName, Value: "raw.id.token"})
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)

	if response.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", response.Code)
	}
	if location := response.Header().Get("Location"); location != flow.endSessionURL {
		t.Fatalf("Location = %q", location)
	}
	cleared := cookieByName(response, authn.SessionCookieName)
	if cleared == nil || cleared.Value != "" || cleared.MaxAge != -1 {
		t.Fatalf("session cookie = %#v, want cleared", cleared)
	}
	if flow.gotIDTokenHint != "raw.id.token" {
		t.Fatalf("id_token_hint = %q", flow.gotIDTokenHint)
	}
	// The ID token may appear only in the Location header. A response body
	// carrying it would be readable by any script on the origin, which is
	// exactly what HttpOnly exists to prevent.
	if body := response.Body.String(); strings.Contains(body, "raw.id.token") {
		t.Fatal("logout response body carries the ID token")
	}
}

func TestAuthLogoutWithoutProviderSupportRedirectsHome(t *testing.T) {
	server := newAuthTestServer(t, &stubFlow{}, stubVerifier{})

	request := httptest.NewRequest(http.MethodPost, "/v1/auth/logout", nil)
	request.AddCookie(&http.Cookie{Name: authn.SessionCookieName, Value: "raw.id.token"})
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)

	if response.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", response.Code)
	}
	if location := response.Header().Get("Location"); location != "http://localhost:5173/" {
		t.Fatalf("Location = %q", location)
	}
}

func TestAuthRoutesArePublic(t *testing.T) {
	// The middleware must let the login routes through: a user with no session
	// cannot obtain one from behind an authentication gate.
	flow := &stubFlow{authorizationURL: "https://idp.test/authorize"}
	sealer, _ := secrets.NewCipher("01234567890123456789012345678901")
	server := NewAuthenticatedServer(nil, stubVerifier{err: authn.ErrInvalidToken}, "tenant_123", false,
		WithAuth(AuthConfig{Flow: flow, Verifier: stubVerifier{}, Sealer: sealer, PublicURL: "http://localhost:5173"}))

	request := httptest.NewRequest(http.MethodGet, "/v1/auth/login", nil)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)

	if response.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302 — /v1/auth/login is behind the auth gate", response.Code)
	}
}
