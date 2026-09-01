package authn

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// fakeSessionStore is a SessionStore over an in-memory map. Only SessionByHash
// and TouchSession are exercised by the middleware; touched counts calls to
// TouchSession so tests can assert the touch interval is honored.
type fakeSessionStore struct {
	byHash  map[string]Session
	touched int
	// touchErr, when set, fails every TouchSession.
	touchErr error
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
	if store.touchErr != nil {
		return store.touchErr
	}
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

func (store *fakeSessionStore) RevokeSessionsBySubjectWithoutIDPSession(_ context.Context, _ string, _ time.Time) (int, error) {
	return 0, nil
}

func (store *fakeSessionStore) DeleteSessionsExpiredBefore(_ context.Context, _ time.Time) (int, error) {
	return 0, nil
}

// serveAuthenticated builds the middleware around sessions with a
// rejectingVerifier, issues a request carrying raw as the session cookie, and
// asserts the response is 200.
func serveAuthenticated(t *testing.T, sessions SessionStore, now time.Time, raw string) {
	t.Helper()
	handler := RequireAuthentication(sessions, time.Hour, func() time.Time { return now })(
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
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	valid := Session{
		IDHash:            HashSessionID("session-token"),
		Subject:           "user-123",
		Name:              "Ada",
		PreferredUsername: "ada",
		Email:             "ada@example.test",
		LastSeenAt:        now,
		AbsoluteExpiresAt: now.Add(8 * time.Hour),
	}

	for _, test := range []struct {
		name          string
		path          string
		authorization string
		cookie        string
		status        int
		called        bool
	}{
		{name: "public health", path: "/healthz", status: http.StatusOK, called: true},
		{name: "no credential", path: "/v1/stacks", status: http.StatusUnauthorized},
		{name: "unknown cookie value", path: "/v1/stacks", cookie: "not-a-session", status: http.StatusUnauthorized},
		// An Authorization header is not a credential. Accepting one made the
		// ID token — which RP-initiated logout necessarily puts in a URL — a
		// key to every /v1 route, and a signed token cannot be revoked.
		{name: "bearer header is ignored", path: "/v1/stacks", authorization: "Bearer any-token", status: http.StatusUnauthorized},
		{name: "bearer header does not override a cookie", path: "/v1/stacks", authorization: "Bearer any-token", cookie: "session-token", status: http.StatusOK, called: true},
		{name: "session cookie", path: "/v1/stacks", cookie: "session-token", status: http.StatusOK, called: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			called := false
			sessions := &fakeSessionStore{byHash: map[string]Session{valid.IDHash: valid}}
			handler := RequireAuthentication(sessions, time.Hour, func() time.Time { return now }, "/healthz")(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				called = true
				if request.URL.Path == "/v1/stacks" {
					principal, ok := PrincipalFromContext(request.Context())
					if !ok || principal.Subject != valid.Subject || principal.Name != valid.Name || principal.PreferredUsername != valid.PreferredUsername || principal.Email != valid.Email {
						t.Fatalf("principal = %#v, ok = %t", principal, ok)
					}
				}
				response.WriteHeader(http.StatusOK)
			}))
			request := httptest.NewRequest(http.MethodGet, test.path, nil)
			if test.authorization != "" {
				request.Header.Set("Authorization", test.authorization)
			}
			if test.cookie != "" {
				request.AddCookie(&http.Cookie{Name: SessionCookieName, Value: test.cookie})
			}
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
		{name: "header only", authorization: "Bearer header-token", status: http.StatusUnauthorized},
		{name: "header does not displace the cookie", authorization: "Bearer header-token", cookie: "cookie-token", hasCookie: true, status: http.StatusOK, wantSubject: "cookie-user"},
		{name: "empty cookie", cookie: "", hasCookie: true, status: http.StatusUnauthorized},
		{name: "neither", status: http.StatusUnauthorized},
		{name: "malformed header is ignored too", authorization: "Basic ignored", cookie: "cookie-token", hasCookie: true, status: http.StatusOK, wantSubject: "cookie-user"},
	} {
		t.Run(test.name, func(t *testing.T) {
			sessions := &fakeSessionStore{byHash: map[string]Session{validSession.IDHash: validSession}}
			var got Principal
			handler := RequireAuthentication(sessions, time.Hour, func() time.Time { return now }, "/healthz")(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
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
	sessions := &fakeSessionStore{byHash: map[string]Session{}}
	handler := RequireAuthentication(sessions, time.Hour, nil)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
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
	handler := RequireAuthentication(sessions, time.Hour, func() time.Time { return now })(
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
	handler := RequireAuthentication(nil, time.Hour, func() time.Time { return now })(
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

			handler := RequireAuthentication(sessions, time.Hour, func() time.Time { return now })(
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

// TestExpiresAtReflectsTheTouchTheSameRequestWrote pins the bound reported to
// the browser to the one this request just wrote. Reporting the pre-touch
// bound is not merely stale: it is the moment the client is already asking
// about, so an idle client's proactive re-authentication can never find a
// later bound to rearm on and gives up on a session that is not ending.
func TestExpiresAtReflectsTheTouchTheSameRequestWrote(t *testing.T) {
	const idleTTL = time.Hour
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	raw := "session-token"
	session := Session{
		IDHash:            HashSessionID(raw),
		Subject:           "user-1",
		LastSeenAt:        now.Add(-59 * time.Minute),
		AbsoluteExpiresAt: now.Add(8 * time.Hour),
	}
	sessions := &fakeSessionStore{byHash: map[string]Session{session.IDHash: session}}

	var got Principal
	handler := RequireAuthentication(sessions, idleTTL, func() time.Time { return now })(
		http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
			got, _ = PrincipalFromContext(request.Context())
		}),
	)
	request := httptest.NewRequest(http.MethodGet, "/v1/me", nil)
	request.AddCookie(&http.Cookie{Name: SessionCookieName, Value: raw})
	handler.ServeHTTP(httptest.NewRecorder(), request)

	if want := now.Add(idleTTL); !got.ExpiresAt.Equal(want) {
		t.Fatalf("ExpiresAt = %v, want %v — the touch this request wrote must be in the answer it returns", got.ExpiresAt, want)
	}
}

// TestExpiresAtDoesNotClaimAFailedTouch keeps the reported bound honest when
// the write did not land: a failed touch shortens the session, and saying
// otherwise would promise a bound the database does not hold.
func TestExpiresAtDoesNotClaimAFailedTouch(t *testing.T) {
	const idleTTL = time.Hour
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	raw := "session-token"
	lastSeen := now.Add(-59 * time.Minute)
	session := Session{
		IDHash:            HashSessionID(raw),
		Subject:           "user-1",
		LastSeenAt:        lastSeen,
		AbsoluteExpiresAt: now.Add(8 * time.Hour),
	}
	sessions := &fakeSessionStore{byHash: map[string]Session{session.IDHash: session}, touchErr: errors.New("write failed")}

	var got Principal
	handler := RequireAuthentication(sessions, idleTTL, func() time.Time { return now })(
		http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
			got, _ = PrincipalFromContext(request.Context())
		}),
	)
	request := httptest.NewRequest(http.MethodGet, "/v1/me", nil)
	request.AddCookie(&http.Cookie{Name: SessionCookieName, Value: raw})
	handler.ServeHTTP(httptest.NewRecorder(), request)

	if want := lastSeen.Add(idleTTL); !got.ExpiresAt.Equal(want) {
		t.Fatalf("ExpiresAt = %v, want %v — a touch that failed must not be reported as written", got.ExpiresAt, want)
	}
}
