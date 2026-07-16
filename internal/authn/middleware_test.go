package authn

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
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

func TestRequireAuthentication(t *testing.T) {
	valid := VerifiedToken{Subject: "user-123", Name: "Ada", PreferredUsername: "ada", Email: "ada@example.test", RealmRoles: []string{"stack-creator", "ignored", "platform-admin", "stack-creator"}}

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
			handler := RequireAuthentication(&verifier, "/healthz")(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				called = true
				if request.URL.Path == "/v1/stacks" {
					principal, ok := PrincipalFromContext(request.Context())
					if !ok || principal.Subject != valid.Subject || principal.Name != valid.Name || principal.PreferredUsername != valid.PreferredUsername || principal.Email != valid.Email || !slices.Equal(principal.RealmRoles, []string{"platform-admin", "stack-creator"}) {
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

func TestNormalizedGlobalRoles(t *testing.T) {
	got := normalizedGlobalRoles([]string{"stack-creator", "ignored", "platform-admin", "stack-creator"})
	if !slices.Equal(got, []string{"platform-admin", "stack-creator"}) {
		t.Fatalf("roles = %#v", got)
	}
}

func TestPrincipalFromContextCopiesRoles(t *testing.T) {
	ctx := ContextWithPrincipal(context.Background(), Principal{Subject: "user-123", RealmRoles: []string{"admin"}})
	principal, ok := PrincipalFromContext(ctx)
	if !ok {
		t.Fatal("principal is missing")
	}
	principal.RealmRoles[0] = "mutated"
	second, ok := PrincipalFromContext(ctx)
	if !ok || second.RealmRoles[0] != "admin" {
		t.Fatalf("principal after mutation = %#v, ok = %t", second, ok)
	}
}

func TestRequireAuthenticationDoesNotLeakVerifierErrors(t *testing.T) {
	secret := "token-or-provider-detail"
	verifier := &middlewareVerifier{err: errors.New(secret)}
	handler := RequireAuthentication(verifier)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
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
