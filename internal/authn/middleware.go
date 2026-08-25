package authn

import (
	"errors"
	"net/http"
	"strings"
)

const invalidCredentialsCode = "unauthorized"

// RequireAuthentication protects every request except paths named in publicPaths.
func RequireAuthentication(verifier Verifier, publicPaths ...string) func(http.Handler) http.Handler {
	public := make(map[string]struct{}, len(publicPaths))
	for _, path := range publicPaths {
		public[path] = struct{}{}
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
			raw, ok := credential(request)
			if !ok {
				writeUnauthorized(response)
				return
			}
			verified, err := verifier.Verify(request.Context(), raw)
			if err != nil {
				if errors.Is(err, ErrInvalidToken) || errors.Is(err, ErrVerifierUnavailable) {
					writeUnauthorized(response)
					return
				}
				writeUnauthorized(response)
				return
			}
			next.ServeHTTP(response, request.WithContext(ContextWithPrincipal(request.Context(), principalFromVerifiedToken(verified))))
		})
	}
}

// credential reads the caller's token from the Authorization header, falling
// back to the session cookie. The header wins so a CLI or service caller is
// never overridden by a stale browser cookie on the same connection.
func credential(request *http.Request) (string, bool) {
	if raw, ok := bearerToken(request.Header.Get("Authorization")); ok {
		return raw, true
	}
	cookie, err := request.Cookie(SessionCookieName)
	if err != nil || cookie.Value == "" {
		return "", false
	}
	return cookie.Value, true
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
