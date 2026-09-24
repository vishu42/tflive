package githubapp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func tokenServer(t *testing.T, mints *int64, expiry time.Time) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/installation") {
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 48291})
			return
		}
		atomic.AddInt64(mints, 1)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token":      "ghs_minted",
			"expires_at": expiry.Format(time.RFC3339),
		})
	}))
}

// A run and a registration against the same repo minutes apart should not each
// pay two API calls against the App's hourly budget.
func TestTokenSourceCachesUntilNearExpiry(t *testing.T) {
	t.Parallel()

	var mints int64
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	server := tokenServer(t, &mints, now.Add(time.Hour))
	defer server.Close()

	clock := now
	source := NewTokenSource(
		NewClient("12345", testKey(t), WithBaseURL(server.URL)),
		WithTokenSourceClock(func() time.Time { return clock }),
	)

	for range 3 {
		token, err := source.Token(context.Background(), "acme", "private-infra")
		if err != nil {
			t.Fatalf("Token returned error: %v", err)
		}
		if token != "ghs_minted" {
			t.Fatalf("token = %q, want ghs_minted", token)
		}
		clock = clock.Add(10 * time.Minute)
	}
	if got := atomic.LoadInt64(&mints); got != 1 {
		t.Fatalf("mints = %d, want 1", got)
	}
}

// A token handed out moments before it expires is a clone that fails halfway.
// The margin is what stops that.
func TestTokenSourceRemintsInsideTheExpiryMargin(t *testing.T) {
	t.Parallel()

	var mints int64
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	server := tokenServer(t, &mints, now.Add(time.Hour))
	defer server.Close()

	clock := now
	source := NewTokenSource(
		NewClient("12345", testKey(t), WithBaseURL(server.URL)),
		WithTokenSourceClock(func() time.Time { return clock }),
	)

	if _, err := source.Token(context.Background(), "acme", "private-infra"); err != nil {
		t.Fatalf("Token returned error: %v", err)
	}
	// 58 minutes in, the cached token has two minutes left -- inside the margin.
	clock = clock.Add(58 * time.Minute)
	if _, err := source.Token(context.Background(), "acme", "private-infra"); err != nil {
		t.Fatalf("Token returned error: %v", err)
	}
	if got := atomic.LoadInt64(&mints); got != 2 {
		t.Fatalf("mints = %d, want 2", got)
	}
}

// Tokens are scoped to one repository, so one repo's token can never serve
// another even within the same installation.
func TestTokenSourceKeysCacheByRepository(t *testing.T) {
	t.Parallel()

	var mints int64
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	server := tokenServer(t, &mints, now.Add(time.Hour))
	defer server.Close()

	source := NewTokenSource(
		NewClient("12345", testKey(t), WithBaseURL(server.URL)),
		WithTokenSourceClock(func() time.Time { return now }),
	)

	if _, err := source.Token(context.Background(), "acme", "repo-a"); err != nil {
		t.Fatalf("Token returned error: %v", err)
	}
	if _, err := source.Token(context.Background(), "acme", "repo-b"); err != nil {
		t.Fatalf("Token returned error: %v", err)
	}
	if got := atomic.LoadInt64(&mints); got != 2 {
		t.Fatalf("mints = %d, want 2", got)
	}
}

// An unconfigured deployment must clone public repositories exactly as before,
// so the disabled source is a success returning no credential, not an error.
func TestNilTokenSourceReturnsNoCredential(t *testing.T) {
	t.Parallel()

	var source *TokenSource

	token, err := source.Token(context.Background(), "acme", "public")
	if err != nil {
		t.Fatalf("Token returned error: %v", err)
	}
	if token != "" {
		t.Fatalf("token = %q, want empty", token)
	}
}

func TestTokenSourcePropagatesNotInstalled(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
	}))
	defer server.Close()

	source := NewTokenSource(NewClient("12345", testKey(t), WithBaseURL(server.URL)))

	_, err := source.Token(context.Background(), "acme", "missing")
	if !errors.Is(err, ErrAppNotInstalled) {
		t.Fatalf("error = %v, want ErrAppNotInstalled", err)
	}
}

// The API stores a nil *TokenSource in an interface when no App is
// configured. A nil receiver guard is what keeps that from panicking on the
// first clone of a public repository.
func TestNilTokenSourceThroughInterfaceIsSafe(t *testing.T) {
	t.Parallel()

	var source *TokenSource
	var iface interface {
		Token(ctx context.Context, owner string, repo string) (string, error)
	} = source

	token, err := iface.Token(context.Background(), "acme", "public")
	if err != nil {
		t.Fatalf("Token returned error: %v", err)
	}
	if token != "" {
		t.Fatalf("token = %q, want empty", token)
	}
}
