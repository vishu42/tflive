package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vishu42/tflive/internal/authn"
)

func postLogoutToken(t *testing.T, server *Server, body string) *httptest.ResponseRecorder {
	t.Helper()

	request := httptest.NewRequest(http.MethodPost, "/v1/auth/backchannel-logout", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	return recorder
}

func TestBackchannelLogoutRevokesBySessionID(t *testing.T) {
	sessions := newFakeSessionStore()
	server := newAuthTestServer(t, &stubFlow{}, stubVerifier{},
		withSessions(sessions),
		withLogoutTokenVerifier(fakeLogoutVerifier{token: authn.LogoutToken{Subject: "user-1", SessionID: "idp-sid-1"}}),
	)

	recorder := postLogoutToken(t, server, "logout_token=anything")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	if sessions.revokedBySID["idp-sid-1"] != 1 {
		t.Fatal("no session was revoked for the sid")
	}
	if len(sessions.revokedBySubject) != 0 {
		t.Fatal("fell back to subject even though a sid was present")
	}
}

func TestBackchannelLogoutFallsBackToSubject(t *testing.T) {
	sessions := newFakeSessionStore()
	server := newAuthTestServer(t, &stubFlow{}, stubVerifier{},
		withSessions(sessions),
		withLogoutTokenVerifier(fakeLogoutVerifier{token: authn.LogoutToken{Subject: "user-1"}}),
	)

	recorder := postLogoutToken(t, server, "logout_token=anything")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	if sessions.revokedBySubject["user-1"] != 1 {
		t.Fatal("no session was revoked for the subject")
	}
}

func TestBackchannelLogoutRejectsABadToken(t *testing.T) {
	sessions := newFakeSessionStore()
	server := newAuthTestServer(t, &stubFlow{}, stubVerifier{},
		withSessions(sessions),
		withLogoutTokenVerifier(fakeLogoutVerifier{err: authn.ErrInvalidLogoutToken}),
	)

	recorder := postLogoutToken(t, server, "logout_token=forged")

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
	if len(sessions.revokedBySID)+len(sessions.revokedBySubject) != 0 {
		t.Fatal("a rejected token still revoked sessions")
	}
}

func TestBackchannelLogoutIsUnauthenticatedAndUncached(t *testing.T) {
	sessions := newFakeSessionStore()
	server := newAuthTestServer(t, &stubFlow{}, stubVerifier{},
		withSessions(sessions),
		withLogoutTokenVerifier(fakeLogoutVerifier{token: authn.LogoutToken{SessionID: "idp-sid-1"}}),
	)

	// No cookie, no Authorization header: the IdP has neither.
	recorder := postLogoutToken(t, server, "logout_token=anything")

	if recorder.Code == http.StatusUnauthorized {
		t.Fatal("the endpoint requires authentication; the IdP cannot provide any")
	}
	if recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", recorder.Header().Get("Cache-Control"))
	}
}
