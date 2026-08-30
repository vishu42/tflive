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
// Two credential kinds resolve to one Principal. A cookie names an app-owned
// session row, whose lifetime tflive chose; an Authorization header carries an
// IdP token for a CLI or service caller, verified as it always was. The header
// wins so a stale browser cookie on the same connection cannot override it.
func RequireAuthentication(
	verifier Verifier,
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

			principal, ok := authenticate(request, verifier, sessions, idleTTL, clock)
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
	verifier Verifier,
	sessions SessionStore,
	idleTTL time.Duration,
	clock func() time.Time,
) (Principal, bool) {
	if raw, ok := bearerToken(request.Header.Get("Authorization")); ok {
		verified, err := verifier.Verify(request.Context(), raw)
		if err != nil {
			// ErrVerifierUnavailable means the IdP is unreachable — every
			// request fails the same 401 an invalid token would, so without
			// this log an outage is a silent 401 storm.
			if errors.Is(err, ErrVerifierUnavailable) {
				log.Printf("authn middleware: token verifier unavailable: %v", err)
			}
			return Principal{}, false
		}
		return principalFromVerifiedToken(verified), true
	}

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

func bearerToken(header string) (string, bool) {
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
		return "", false
	}
	return parts[1], true
}

func writeUnauthorized(response http.ResponseWriter) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(http.StatusUnauthorized)
	_, _ = response.Write([]byte(`{"code":"` + invalidCredentialsCode + `"}`))
}
