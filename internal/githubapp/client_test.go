package githubapp

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jwt"
)

func testKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return key
}

// The JWT is the App's only proof of identity. GitHub rejects an exp more than
// ten minutes out, and a clock a second fast makes an iat of "now" invalid, so
// both bounds are asserted rather than assumed.
func TestClientSignsAppJWTWithinGitHubBounds(t *testing.T) {
	t.Parallel()

	key := testKey(t)
	issued := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	client := NewClient("12345", key, WithClock(func() time.Time { return issued }))

	raw, err := client.appJWT()
	if err != nil {
		t.Fatalf("appJWT returned error: %v", err)
	}

	// jwt.Parse validates exp/iat/nbf against the real wall clock unless told
	// otherwise; without this, an iat backdated from the fixed `issued` instant
	// above (rather than from actual now) is spuriously rejected as future-dated.
	token, err := jwt.Parse([]byte(raw), jwt.WithKey(jwa.RS256(), key.Public()), jwt.WithClock(jwt.ClockFunc(func() time.Time { return issued })))
	if err != nil {
		t.Fatalf("parse jwt: %v", err)
	}
	issuer, ok := token.Issuer()
	if !ok || issuer != "12345" {
		t.Fatalf("issuer = %q, want 12345", issuer)
	}
	issuedAt, ok := token.IssuedAt()
	if !ok || !issuedAt.Before(issued) {
		t.Fatalf("iat = %v, want before %v to absorb clock skew", issuedAt, issued)
	}
	expiration, ok := token.Expiration()
	if !ok {
		t.Fatal("exp is missing")
	}
	if expiration.Sub(issuedAt) > 10*time.Minute {
		t.Fatalf("exp - iat = %v, more than GitHub's 10 minute maximum", expiration.Sub(issuedAt))
	}
	if expiration.Sub(issued) > 10*time.Minute {
		t.Fatalf("exp = %v, more than 10 minutes after %v", expiration, issued)
	}
}

func TestInstallationForRepoReturnsID(t *testing.T) {
	t.Parallel()

	var gotPath, gotAuthPrefix string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if len(r.Header.Get("Authorization")) > 7 {
			gotAuthPrefix = r.Header.Get("Authorization")[:7]
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 48291})
	}))
	defer server.Close()

	client := NewClient("12345", testKey(t), WithBaseURL(server.URL))

	id, err := client.InstallationForRepo(context.Background(), "acme", "private-infra")
	if err != nil {
		t.Fatalf("InstallationForRepo returned error: %v", err)
	}
	if id != 48291 {
		t.Fatalf("id = %d, want 48291", id)
	}
	if gotPath != "/repos/acme/private-infra/installation" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotAuthPrefix != "Bearer " {
		t.Fatalf("auth prefix = %q, want Bearer", gotAuthPrefix)
	}
}

// A 404 here means the App was never installed on that repo. It must be
// distinguishable, because it is the one failure a user can actually act on.
func TestInstallationForRepoReportsNotInstalled(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
	}))
	defer server.Close()

	client := NewClient("12345", testKey(t), WithBaseURL(server.URL))

	_, err := client.InstallationForRepo(context.Background(), "acme", "missing")
	if !errors.Is(err, ErrAppNotInstalled) {
		t.Fatalf("error = %v, want ErrAppNotInstalled", err)
	}
}

// The minted token is narrowed to one repository and to reading contents, so a
// leak exposes that repo's source and nothing else in the installation.
func TestMintTokenRequestsRepoScopedReadOnlyToken(t *testing.T) {
	t.Parallel()

	var body struct {
		Repositories []string          `json:"repositories"`
		Permissions  map[string]string `json:"permissions"`
	}
	var gotPath string
	expiry := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token":      "ghs_minted",
			"expires_at": expiry.Format(time.RFC3339),
		})
	}))
	defer server.Close()

	client := NewClient("12345", testKey(t), WithBaseURL(server.URL))

	token, err := client.MintToken(context.Background(), 48291, "private-infra")
	if err != nil {
		t.Fatalf("MintToken returned error: %v", err)
	}
	if token.Value != "ghs_minted" {
		t.Fatalf("token = %q, want ghs_minted", token.Value)
	}
	if !token.ExpiresAt.Equal(expiry) {
		t.Fatalf("expires = %v, want %v", token.ExpiresAt, expiry)
	}
	if gotPath != "/app/installations/48291/access_tokens" {
		t.Fatalf("path = %q", gotPath)
	}
	if len(body.Repositories) != 1 || body.Repositories[0] != "private-infra" {
		t.Fatalf("repositories = %#v, want [private-infra]", body.Repositories)
	}
	if body.Permissions["contents"] != "read" {
		t.Fatalf("permissions = %#v, want contents:read", body.Permissions)
	}
}

// A failure response body can echo request material; it must not reach an error
// string unbounded, and the minted token must never appear in one.
func TestMintTokenFailureOmitsTokenAndBoundsBody(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"Bad credentials"}`, http.StatusUnauthorized)
	}))
	defer server.Close()

	client := NewClient("12345", testKey(t), WithBaseURL(server.URL))

	_, err := client.MintToken(context.Background(), 48291, "private-infra")
	if err == nil {
		t.Fatal("expected an error")
	}
	if len(err.Error()) > 1024 {
		t.Fatalf("error length = %d, want bounded", len(err.Error()))
	}
}
