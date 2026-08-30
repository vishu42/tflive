package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"

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

// authTestOption adjusts the AuthConfig a test server is built with.
type authTestOption func(*AuthConfig)

func withSessions(store authn.SessionStore) authTestOption {
	return func(cfg *AuthConfig) { cfg.Sessions = store }
}

func withClock(now time.Time) authTestOption {
	return func(cfg *AuthConfig) { cfg.Clock = func() time.Time { return now } }
}

// fakeSessionStore is an in-memory authn.SessionStore.
type fakeSessionStore struct {
	created          []authn.Session
	byHash           map[string]authn.Session
	touched          int
	revoked          map[string]int
	revokedBySID     map[string]int
	revokedBySubject map[string]int
}

func newFakeSessionStore() *fakeSessionStore {
	return &fakeSessionStore{
		byHash:           map[string]authn.Session{},
		revoked:          map[string]int{},
		revokedBySID:     map[string]int{},
		revokedBySubject: map[string]int{},
	}
}

func (f *fakeSessionStore) CreateSession(_ context.Context, session authn.Session) error {
	f.created = append(f.created, session)
	f.byHash[session.IDHash] = session
	return nil
}

func (f *fakeSessionStore) SessionByHash(_ context.Context, idHash string) (authn.Session, error) {
	session, ok := f.byHash[idHash]
	if !ok {
		return authn.Session{}, authn.ErrSessionNotFound
	}
	return session, nil
}

func (f *fakeSessionStore) TouchSession(_ context.Context, idHash string, seenAt time.Time) error {
	f.touched++
	if session, ok := f.byHash[idHash]; ok {
		session.LastSeenAt = seenAt
		f.byHash[idHash] = session
	}
	return nil
}

func (f *fakeSessionStore) RevokeSession(_ context.Context, idHash string, at time.Time) error {
	f.revoked[idHash]++
	if session, ok := f.byHash[idHash]; ok {
		session.RevokedAt = at
		f.byHash[idHash] = session
	}
	return nil
}

func (f *fakeSessionStore) RevokeSessionsByIDPSessionID(_ context.Context, idpSessionID string, at time.Time) (int, error) {
	f.revokedBySID[idpSessionID]++
	count := 0
	for hash, session := range f.byHash {
		if session.IDPSessionID == idpSessionID && session.RevokedAt.IsZero() {
			session.RevokedAt = at
			f.byHash[hash] = session
			count++
		}
	}
	return count, nil
}

func (f *fakeSessionStore) RevokeSessionsBySubject(_ context.Context, subject string, at time.Time) (int, error) {
	f.revokedBySubject[subject]++
	count := 0
	for hash, session := range f.byHash {
		if session.Subject == subject && session.RevokedAt.IsZero() {
			session.RevokedAt = at
			f.byHash[hash] = session
			count++
		}
	}
	return count, nil
}

func newAuthTestServer(t *testing.T, flow *stubFlow, verifier authn.Verifier, options ...authTestOption) *Server {
	t.Helper()
	sealer, err := secrets.NewCipher("01234567890123456789012345678901")
	if err != nil {
		t.Fatalf("NewCipher returned error: %v", err)
	}
	cfg := AuthConfig{
		Flow:               flow,
		Verifier:           verifier,
		Sealer:             sealer,
		PublicURL:          "http://localhost:5173",
		SecureCookies:      false,
		SessionAbsoluteTTL: authn.DefaultSessionAbsoluteTTL,
		SessionIdleTTL:     authn.DefaultSessionIdleTTL,
	}
	for _, option := range options {
		option(&cfg)
	}
	return NewServer(nil, "tenant_123", WithAuth(cfg))
}

// runCallback seals a transaction under a known state and the given nonce,
// issues the callback request carrying it, and returns the recorder. Tests
// that need a different transaction still build one with callbackRequest.
func runCallback(t *testing.T, server *Server, nonce string) *httptest.ResponseRecorder {
	t.Helper()
	sealer, err := secrets.NewCipher("01234567890123456789012345678901")
	if err != nil {
		t.Fatalf("NewCipher returned error: %v", err)
	}
	transaction := authn.Transaction{State: "state-1", Nonce: nonce, CodeVerifier: "verifier-1", ReturnTo: "/stacks/abc"}
	request := callbackRequest(t, sealer, transaction, url.Values{"code": {"code-1"}, "state": {"state-1"}})
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	return response
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
	sessions := newFakeSessionStore()
	flow := &stubFlow{idToken: "raw.id.token"}
	verifier := stubVerifier{token: authn.VerifiedToken{Subject: "user-123", Nonce: "nonce-1"}}
	server := newAuthTestServer(t, flow, verifier, withSessions(sessions))

	response := runCallback(t, server, "nonce-1")

	if response.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302; body = %s", response.Code, response.Body.String())
	}
	if location := response.Header().Get("Location"); location != "/stacks/abc" {
		t.Fatalf("Location = %q", location)
	}
	// The cookie is now an opaque reference to the session row, not the ID
	// token itself.
	session := cookieByName(response, authn.SessionCookieName)
	if session == nil || session.Value == "" || session.Value == "raw.id.token" || !session.HttpOnly {
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

func TestCallbackCreatesSessionAndSetsOpaqueCookie(t *testing.T) {
	sessions := newFakeSessionStore()
	flow := &stubFlow{idToken: "header.payload.signature"}
	verifier := stubVerifier{token: authn.VerifiedToken{
		Subject: "user-1", Name: "Ada Lovelace", Email: "ada@example.test",
		SessionID: "idp-sid-1", Nonce: "nonce-1",
	}}
	server := newAuthTestServer(t, flow, verifier, withSessions(sessions))

	response := runCallback(t, server, "nonce-1")

	cookie := cookieByName(response, authn.SessionCookieName)
	if cookie == nil {
		t.Fatal("no session cookie was set")
	}
	if strings.Count(cookie.Value, ".") == 2 {
		t.Fatalf("cookie value %q still looks like a JWT; it must be an opaque reference", cookie.Value)
	}
	if len(sessions.created) != 1 {
		t.Fatalf("created %d sessions, want 1", len(sessions.created))
	}

	created := sessions.created[0]
	if created.IDHash != authn.HashSessionID(cookie.Value) {
		t.Fatal("the stored hash does not match the cookie the browser was given")
	}
	if created.IDToken != "header.payload.signature" {
		t.Fatal("the ID token was not kept; logout needs it for id_token_hint")
	}
	if created.IDPSessionID != "idp-sid-1" {
		t.Fatalf("IDPSessionID = %q, want idp-sid-1", created.IDPSessionID)
	}
	if created.AbsoluteExpiresAt.Sub(created.CreatedAt) != authn.DefaultSessionAbsoluteTTL {
		t.Fatalf("absolute window = %v, want 8h", created.AbsoluteExpiresAt.Sub(created.CreatedAt))
	}
}

func TestCallbackSessionLifetimeIgnoresTokenExpiry(t *testing.T) {
	// The whole point of the change: a 60-second ID token must still produce a
	// full-length session.
	sessions := newFakeSessionStore()
	flow := &stubFlow{idToken: "header.payload.signature"}
	verifier := stubVerifier{token: authn.VerifiedToken{
		Subject: "user-1", Nonce: "nonce-1",
		ExpiresAt: time.Now().Add(time.Minute),
	}}
	server := newAuthTestServer(t, flow, verifier, withSessions(sessions))

	runCallback(t, server, "nonce-1")

	created := sessions.created[0]
	if created.AbsoluteExpiresAt.Sub(created.CreatedAt) != authn.DefaultSessionAbsoluteTTL {
		t.Fatalf("absolute window = %v, want 8h regardless of the token's exp", created.AbsoluteExpiresAt.Sub(created.CreatedAt))
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
	raw := "session-token"
	sessions := newFakeSessionStore()
	sessions.byHash[authn.HashSessionID(raw)] = authn.Session{
		IDHash:  authn.HashSessionID(raw),
		Subject: "user-1",
		IDToken: "raw.id.token",
	}
	flow := &stubFlow{endSessionURL: "https://idp.test/logout?id_token_hint=raw.id.token"}
	server := newAuthTestServer(t, flow, stubVerifier{}, withSessions(sessions))

	request := httptest.NewRequest(http.MethodPost, "/v1/auth/logout", nil)
	request.AddCookie(&http.Cookie{Name: authn.SessionCookieName, Value: raw})
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
	raw := "session-token"
	sessions := newFakeSessionStore()
	sessions.byHash[authn.HashSessionID(raw)] = authn.Session{
		IDHash:  authn.HashSessionID(raw),
		Subject: "user-1",
		IDToken: "raw.id.token",
	}
	server := newAuthTestServer(t, &stubFlow{}, stubVerifier{}, withSessions(sessions))

	request := httptest.NewRequest(http.MethodPost, "/v1/auth/logout", nil)
	request.AddCookie(&http.Cookie{Name: authn.SessionCookieName, Value: raw})
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)

	if response.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", response.Code)
	}
	if location := response.Header().Get("Location"); location != "http://localhost:5173/" {
		t.Fatalf("Location = %q", location)
	}
}

func TestLogoutRevokesTheSessionAndUsesTheStoredIDToken(t *testing.T) {
	raw := "session-token"
	sessions := newFakeSessionStore()
	sessions.byHash[authn.HashSessionID(raw)] = authn.Session{
		IDHash:  authn.HashSessionID(raw),
		Subject: "user-1",
		IDToken: "stored.id.token",
	}
	flow := &stubFlow{endSessionURL: "https://idp.test/logout"}
	server := newAuthTestServer(t, flow, stubVerifier{}, withSessions(sessions))

	request := httptest.NewRequest(http.MethodPost, "/v1/auth/logout", nil)
	request.AddCookie(&http.Cookie{Name: authn.SessionCookieName, Value: raw})
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", recorder.Code)
	}
	if sessions.revoked[authn.HashSessionID(raw)] != 1 {
		t.Fatal("logout did not revoke the session row; a copied cookie would still work")
	}
	// stubFlow records what it was handed rather than building a URL.
	if flow.gotIDTokenHint != "stored.id.token" {
		t.Fatalf("id_token_hint = %q, want the ID token stored on the session", flow.gotIDTokenHint)
	}
}

func TestLogoutWithoutASessionStillRedirects(t *testing.T) {
	sessions := newFakeSessionStore()
	server := newAuthTestServer(t, &stubFlow{}, stubVerifier{}, withSessions(sessions))

	request := httptest.NewRequest(http.MethodPost, "/v1/auth/logout", nil)
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303 even with no session", recorder.Code)
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
