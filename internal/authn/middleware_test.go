package authn

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type middlewareVerifier struct {
	token VerifiedToken
	err   error
	raw   string
}

func (verifier *middlewareVerifier) Verify(_ context.Context, raw string) (VerifiedToken, error) {
	verifier.raw = raw
	return verifier.token, verifier.err
}

// rejectingVerifier fails every token. It stands in for the IdP verifier in
// tests that authenticate through the cookie, so a 200 proves the cookie path
// never consulted it.
type rejectingVerifier struct{}

func (rejectingVerifier) Verify(context.Context, string) (VerifiedToken, error) {
	return VerifiedToken{}, ErrInvalidToken
}

// acceptingVerifier returns a VerifiedToken for the given subject, whatever
// token it is handed.
type acceptingVerifier struct {
	subject string
}

func (verifier acceptingVerifier) Verify(context.Context, string) (VerifiedToken, error) {
	return VerifiedToken{Subject: verifier.subject}, nil
}

// fakeSessionStore is a SessionStore over an in-memory map. Only SessionByHash
// and TouchSession are exercised by the middleware; touched counts calls to
// TouchSession so tests can assert the touch interval is honored.
type fakeSessionStore struct {
	byHash  map[string]Session
	touched int
}

func (store *fakeSessionStore) CreateSession(_ context.Context, session Session) error {
	store.byHash[session.IDHash] = session
	return nil
}

func (store *fakeSessionStore) SessionByHash(_ context.Context, idHash string) (Session, error) {
	session, ok := store.byHash[idHash]
	if !ok {
		return Session{}, ErrSessionNotFound
	}
	return session, nil
}

func (store *fakeSessionStore) TouchSession(_ context.Context, idHash string, seenAt time.Time) error {
	store.touched++
	if session, ok := store.byHash[idHash]; ok {
		session.LastSeenAt = seenAt
		store.byHash[idHash] = session
	}
	return nil
}

func (store *fakeSessionStore) RevokeSession(_ context.Context, idHash string, at time.Time) error {
	if session, ok := store.byHash[idHash]; ok {
		session.RevokedAt = at
		store.byHash[idHash] = session
	}
	return nil
}

func (store *fakeSessionStore) RevokeSessionsByIDPSessionID(_ context.Context, _ string, _ time.Time) (int, error) {
	return 0, nil
}

func (store *fakeSessionStore) RevokeSessionsBySubject(_ context.Context, _ string, _ time.Time) (int, error) {
	return 0, nil
}

// serveAuthenticated builds the middleware around sessions with a
// rejectingVerifier, issues a request carrying raw as the session cookie, and
// asserts the response is 200.
func serveAuthenticated(t *testing.T, sessions SessionStore, now time.Time, raw string) {
	t.Helper()
	handler := RequireAuthentication(rejectingVerifier{}, sessions, time.Hour, func() time.Time { return now })(
		http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
			response.WriteHeader(http.StatusOK)
		}),
	)
	request := httptest.NewRequest(http.MethodGet, "/v1/me", nil)
	request.AddCookie(&http.Cookie{Name: SessionCookieName, Value: raw})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
}

func TestRequireAuthentication(t *testing.T) {
	expiresAt := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	valid := VerifiedToken{Subject: "user-123", Name: "Ada", PreferredUsername: "ada", Email: "ada@example.test", ExpiresAt: expiresAt}

	for _, test := range []struct {
		name          string
		path          string
		authorization string
		verifier      middlewareVerifier
		status        int
		called        bool
	}{
		{name: "public health", path: "/healthz", status: http.StatusOK, called: true},
		{name: "missing token", path: "/v1/stacks", status: http.StatusUnauthorized},
		{name: "invalid scheme", path: "/v1/stacks", authorization: "Basic ignored", status: http.StatusUnauthorized},
		{name: "invalid token", path: "/v1/stacks", authorization: "Bearer invalid", verifier: middlewareVerifier{err: ErrInvalidToken}, status: http.StatusUnauthorized},
		{name: "unavailable verifier", path: "/v1/stacks", authorization: "Bearer unavailable", verifier: middlewareVerifier{err: ErrVerifierUnavailable}, status: http.StatusUnauthorized},
		{name: "valid token", path: "/v1/stacks", authorization: "Bearer valid", verifier: middlewareVerifier{token: valid}, status: http.StatusOK, called: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			verifier := test.verifier
			called := false
			sessions := &fakeSessionStore{byHash: map[string]Session{}}
			handler := RequireAuthentication(&verifier, sessions, time.Hour, nil, "/healthz")(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				called = true
				if request.URL.Path == "/v1/stacks" {
					principal, ok := PrincipalFromContext(request.Context())
					if !ok || principal.Subject != valid.Subject || principal.Name != valid.Name || principal.PreferredUsername != valid.PreferredUsername || principal.Email != valid.Email || !principal.ExpiresAt.Equal(valid.ExpiresAt) {
						t.Fatalf("principal = %#v, ok = %t", principal, ok)
					}
				}
				response.WriteHeader(http.StatusOK)
			}))
			request := httptest.NewRequest(http.MethodGet, test.path, nil)
			request.Header.Set("Authorization", test.authorization)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d", response.Code, test.status)
			}
			if called != test.called {
				t.Fatalf("handler called = %t, want %t", called, test.called)
			}
			if test.status == http.StatusUnauthorized && response.Body.String() != `{"code":"unauthorized"}` {
				t.Fatalf("body = %q", response.Body.String())
			}
		})
	}
}

// The principal is all value types now, so a reader cannot reach through it to
// mutate what the next reader sees. It used to carry a role slice that could be.
func TestPrincipalFromContextIsNotMutableByAReader(t *testing.T) {
	ctx := ContextWithPrincipal(context.Background(), Principal{Subject: "user-123", Name: "Ada"})
	principal, ok := PrincipalFromContext(ctx)
	if !ok {
		t.Fatal("principal is missing")
	}
	principal.Name = "mutated"
	second, ok := PrincipalFromContext(ctx)
	if !ok || second.Name != "Ada" {
		t.Fatalf("principal after mutation = %#v, ok = %t", second, ok)
	}
}

func TestRequireAuthenticationDoesNotLeakVerifierErrors(t *testing.T) {
	secret := "token-or-provider-detail"
	verifier := &middlewareVerifier{err: errors.New(secret)}
	sessions := &fakeSessionStore{byHash: map[string]Session{}}
	handler := RequireAuthentication(verifier, sessions, time.Hour, nil)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("protected handler was called")
	}))
	request := httptest.NewRequest(http.MethodGet, "/v1/stacks", nil)
	request.Header.Set("Authorization", "Bearer opaque")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized || response.Body.String() != `{"code":"unauthorized"}` {
		t.Fatalf("response = %d %q", response.Code, response.Body.String())
	}
}

func TestRequireAuthenticationAcceptsSessionCookie(t *testing.T) {
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	validSession := Session{
		IDHash:            HashSessionID("cookie-token"),
		Subject:           "cookie-user",
		LastSeenAt:        now,
		AbsoluteExpiresAt: now.Add(time.Hour),
	}

	// hasCookie is separate from cookie's zero value on purpose: "empty cookie"
	// needs to send a cookie whose value is "" (the shape a real client sends
	// after logout clears it), which collapses into "neither" if injection is
	// keyed on cookie != "" instead of a dedicated flag.
	for _, test := range []struct {
		name          string
		authorization string
		cookie        string
		hasCookie     bool
		status        int
		wantSubject   string
	}{
		{name: "cookie only", cookie: "cookie-token", hasCookie: true, status: http.StatusOK, wantSubject: "cookie-user"},
		{name: "header only", authorization: "Bearer header-token", status: http.StatusOK, wantSubject: "header-user"},
		{name: "header wins over cookie", authorization: "Bearer header-token", cookie: "cookie-token", hasCookie: true, status: http.StatusOK, wantSubject: "header-user"},
		{name: "empty cookie", cookie: "", hasCookie: true, status: http.StatusUnauthorized},
		{name: "neither", status: http.StatusUnauthorized},
		{name: "malformed header falls back to cookie", authorization: "Basic ignored", cookie: "cookie-token", hasCookie: true, status: http.StatusOK, wantSubject: "cookie-user"},
	} {
		t.Run(test.name, func(t *testing.T) {
			verifier := &middlewareVerifier{token: VerifiedToken{Subject: "header-user"}}
			sessions := &fakeSessionStore{byHash: map[string]Session{validSession.IDHash: validSession}}
			var got Principal
			handler := RequireAuthentication(verifier, sessions, time.Hour, func() time.Time { return now }, "/healthz")(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				got, _ = PrincipalFromContext(request.Context())
				response.WriteHeader(http.StatusOK)
			}))
			request := httptest.NewRequest(http.MethodGet, "/v1/stacks", nil)
			if test.authorization != "" {
				request.Header.Set("Authorization", test.authorization)
			}
			if test.hasCookie {
				request.AddCookie(&http.Cookie{Name: SessionCookieName, Value: test.cookie})
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)

			if response.Code != test.status {
				t.Fatalf("status = %d, want %d", response.Code, test.status)
			}
			if test.status == http.StatusOK && got.Subject != test.wantSubject {
				t.Fatalf("subject = %q, want %q", got.Subject, test.wantSubject)
			}
		})
	}
}

func TestRequireAuthenticationIgnoresOtherCookies(t *testing.T) {
	verifier := &middlewareVerifier{token: VerifiedToken{Subject: "user-123"}}
	sessions := &fakeSessionStore{byHash: map[string]Session{}}
	handler := RequireAuthentication(verifier, sessions, time.Hour, nil)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("protected handler was called")
	}))
	request := httptest.NewRequest(http.MethodGet, "/v1/stacks", nil)
	request.AddCookie(&http.Cookie{Name: "some_other_cookie", Value: "value"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", response.Code)
	}
}

func TestCookieAuthenticatesFromTheSessionStore(t *testing.T) {
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	raw := "session-token"
	sessions := &fakeSessionStore{byHash: map[string]Session{
		HashSessionID(raw): {
			IDHash:            HashSessionID(raw),
			Subject:           "user-1",
			Name:              "Ada Lovelace",
			Email:             "ada@example.test",
			LastSeenAt:        now,
			AbsoluteExpiresAt: now.Add(8 * time.Hour),
		},
	}}

	var got Principal
	handler := RequireAuthentication(rejectingVerifier{}, sessions, time.Hour, func() time.Time { return now })(
		http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
			got, _ = PrincipalFromContext(request.Context())
		}),
	)

	request := httptest.NewRequest(http.MethodGet, "/v1/me", nil)
	request.AddCookie(&http.Cookie{Name: SessionCookieName, Value: raw})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	if got.Subject != "user-1" {
		t.Fatalf("Subject = %q, want user-1", got.Subject)
	}
	// rejectingVerifier fails every token; reaching 200 proves the cookie path
	// never consulted it.
}

// A server built without WithAuth (most of internal/api's own tests, plus any
// future caller that forgets it) has a nil SessionStore. A cookie must 401
// against that, not panic a nil interface mid-request.
func TestNilSessionStoreRejectsCookieInsteadOfPanicking(t *testing.T) {
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	handler := RequireAuthentication(rejectingVerifier{}, nil, time.Hour, func() time.Time { return now })(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Fatal("handler ran with no session store")
		}),
	)
	request := httptest.NewRequest(http.MethodGet, "/v1/me", nil)
	request.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "session-token"})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", recorder.Code)
	}
}

func TestExpiredAndRevokedSessionsAre401(t *testing.T) {
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	tests := map[string]Session{
		"idle expired": {
			LastSeenAt:        now.Add(-2 * time.Hour),
			AbsoluteExpiresAt: now.Add(6 * time.Hour),
		},
		"absolute expired": {
			LastSeenAt:        now,
			AbsoluteExpiresAt: now.Add(-time.Minute),
		},
		"revoked": {
			LastSeenAt:        now,
			AbsoluteExpiresAt: now.Add(8 * time.Hour),
			RevokedAt:         now.Add(-time.Minute),
		},
	}

	for name, session := range tests {
		t.Run(name, func(t *testing.T) {
			raw := "session-token"
			session.IDHash = HashSessionID(raw)
			sessions := &fakeSessionStore{byHash: map[string]Session{session.IDHash: session}}

			handler := RequireAuthentication(rejectingVerifier{}, sessions, time.Hour, func() time.Time { return now })(
				http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
					t.Fatal("handler ran for a dead session")
				}),
			)
			request := httptest.NewRequest(http.MethodGet, "/v1/me", nil)
			request.AddCookie(&http.Cookie{Name: SessionCookieName, Value: raw})
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)

			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", recorder.Code)
			}
		})
	}
}

func TestTouchOnlyAfterTheTouchInterval(t *testing.T) {
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	raw := "session-token"
	base := Session{
		IDHash:            HashSessionID(raw),
		Subject:           "user-1",
		AbsoluteExpiresAt: now.Add(8 * time.Hour),
	}

	t.Run("fresh session is not written back", func(t *testing.T) {
		session := base
		session.LastSeenAt = now.Add(-time.Minute)
		sessions := &fakeSessionStore{byHash: map[string]Session{session.IDHash: session}}
		serveAuthenticated(t, sessions, now, raw)
		if sessions.touched != 0 {
			t.Fatalf("touched %d times, want 0 — a write per request is what the interval avoids", sessions.touched)
		}
	})

	t.Run("stale session is written back", func(t *testing.T) {
		session := base
		session.LastSeenAt = now.Add(-SessionTouchInterval - time.Second)
		sessions := &fakeSessionStore{byHash: map[string]Session{session.IDHash: session}}
		serveAuthenticated(t, sessions, now, raw)
		if sessions.touched != 1 {
			t.Fatalf("touched %d times, want 1", sessions.touched)
		}
	})
}

func TestBearerPathStillUsesTheVerifier(t *testing.T) {
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	sessions := &fakeSessionStore{byHash: map[string]Session{}}

	var got Principal
	handler := RequireAuthentication(acceptingVerifier{subject: "cli-user"}, sessions, time.Hour, func() time.Time { return now })(
		http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
			got, _ = PrincipalFromContext(request.Context())
		}),
	)
	request := httptest.NewRequest(http.MethodGet, "/v1/me", nil)
	request.Header.Set("Authorization", "Bearer some-token")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK || got.Subject != "cli-user" {
		t.Fatalf("status = %d, subject = %q; the Bearer path must still verify tokens", recorder.Code, got.Subject)
	}
}
