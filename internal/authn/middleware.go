package authn

import (
	"errors"
	"log"
	"net/http"
	"strings"
	"time"
)

const invalidCredentialsCode = "unauthorized"

// RequireAuthentication protects every request except paths named in
// publicPaths.
//
// The session cookie is the only credential. It names an app-owned session row
// whose lifetime tflive chose, and identity comes off that row rather than off
// a token presented per request — the ID token behind it was verified once, at
// the callback, and its claims copied onto the row there.
//
// An Authorization header is deliberately not a credential here. Accepting an
// IdP token as a bearer credential made the ID token a key to every /v1 route,
// which mattered because RP-initiated logout necessarily puts that token in a
// URL, where it reaches browser history and every access log in between. A
// signed token is also not revocable: logout, back-channel logout, and an admin
// disabling an account all mark a session row, and none of them can reach a
// copy of a JWT somebody already holds. A caller that needs non-browser access
// wants a credential tflive issues and can revoke, not this.
func RequireAuthentication(
	sessions SessionStore,
	idleTTL time.Duration,
	clock func() time.Time,
	publicPaths ...string,
) func(http.Handler) http.Handler {
	public := make(map[string]struct{}, len(publicPaths))
	for _, path := range publicPaths {
		public[path] = struct{}{}
	}
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			if _, ok := public[request.URL.Path]; ok {
				next.ServeHTTP(response, request)
				return
			}
			if !strings.HasPrefix(request.URL.Path, "/v1/") {
				next.ServeHTTP(response, request)
				return
			}

			principal, ok := authenticate(request, sessions, idleTTL, clock)
			if !ok {
				writeUnauthorized(response)
				return
			}
			next.ServeHTTP(response, request.WithContext(ContextWithPrincipal(request.Context(), principal)))
		})
	}
}

func authenticate(
	request *http.Request,
	sessions SessionStore,
	idleTTL time.Duration,
	clock func() time.Time,
) (Principal, bool) {
	cookie, err := request.Cookie(SessionCookieName)
	if err != nil || cookie.Value == "" {
		return Principal{}, false
	}

	// A server built without WithAuth has no session store. Cookies cannot
	// authenticate against nothing, and a nil interface would panic rather
	// than 401, so treat it as no credential.
	if sessions == nil {
		return Principal{}, false
	}

	idHash := HashSessionID(cookie.Value)
	session, err := sessions.SessionByHash(request.Context(), idHash)
	if err != nil {
		if !errors.Is(err, ErrSessionNotFound) {
			log.Printf("authn middleware: session lookup failed: %v", err)
		}
		return Principal{}, false
	}

	now := clock()
	if !session.IsLive(now, idleTTL) {
		return Principal{}, false
	}
	// Slide the idle bound, but only once per interval: writing on every
	// request would put a database write in front of every read.
	if now.Sub(session.LastSeenAt) >= SessionTouchInterval {
		if err := sessions.TouchSession(request.Context(), idHash, now); err != nil {
			// A failed touch shortens this session, it does not break it, so
			// the request proceeds.
			log.Printf("authn middleware: failed to touch session: %v", err)
		} else {
			// Report the bound this request just wrote, not the one it read.
			// /v1/me is how the browser learns when to re-authenticate, and a
			// pre-touch answer names a moment this very request has already
			// moved: an idle session would keep being told it ends at the
			// instant the client is asking about, and the client would give
			// up and re-authenticate a session that is not ending.
			session.LastSeenAt = now
		}
	}

	return Principal{
		Subject:           session.Subject,
		Name:              session.Name,
		PreferredUsername: session.PreferredUsername,
		Email:             session.Email,
		ExpiresAt:         session.ExpiresAt(idleTTL),
	}, true
}

func writeUnauthorized(response http.ResponseWriter) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(http.StatusUnauthorized)
	_, _ = response.Write([]byte(`{"code":"` + invalidCredentialsCode + `"}`))
}
