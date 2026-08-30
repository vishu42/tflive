package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/vishu42/tflive/internal/authn"
	"github.com/vishu42/tflive/internal/secrets"
)

func newSessionTestStore(t *testing.T, ctx context.Context) *Store {
	t.Helper()

	// openMigratedTestPool, not openTestPool: the latter creates an empty
	// schema and never applies migrations, so the sessions table would not
	// exist.
	pool := openMigratedTestPool(t, ctx)
	cipher, err := secrets.NewCipher("717cb4d0fd1db07a30442806c2987599580f6d7c6e63b9bddf509bc183a086d3")
	if err != nil {
		t.Fatalf("new cipher: %v", err)
	}
	return NewStore(pool, WithSessionCipher(cipher))
}

func newTestSession(t *testing.T, now time.Time) (raw string, session authn.Session) {
	t.Helper()

	raw, err := authn.NewSessionID()
	if err != nil {
		t.Fatalf("NewSessionID: %v", err)
	}
	return raw, authn.Session{
		IDHash:            authn.HashSessionID(raw),
		Subject:           "user-sub-1",
		Name:              "Ada Lovelace",
		PreferredUsername: "ada",
		Email:             "ada@example.test",
		IDPSessionID:      "idp-sid-1",
		IDToken:           "header.payload.signature",
		CreatedAt:         now,
		LastSeenAt:        now,
		AbsoluteExpiresAt: now.Add(8 * time.Hour),
	}
}

func TestCreateAndReadSession(t *testing.T) {
	ctx := context.Background()
	store := newSessionTestStore(t, ctx)
	now := time.Now().UTC().Truncate(time.Millisecond)

	_, session := newTestSession(t, now)
	if err := store.CreateSession(ctx, session); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	got, err := store.SessionByHash(ctx, session.IDHash)
	if err != nil {
		t.Fatalf("SessionByHash: %v", err)
	}
	if got.Subject != session.Subject || got.Email != session.Email {
		t.Fatalf("identity round trip: got %+v", got)
	}
	if got.IDToken != session.IDToken {
		t.Fatalf("IDToken = %q, want %q — it must decrypt back", got.IDToken, session.IDToken)
	}
	if got.IDPSessionID != session.IDPSessionID {
		t.Fatalf("IDPSessionID = %q, want %q", got.IDPSessionID, session.IDPSessionID)
	}
	if !got.RevokedAt.IsZero() {
		t.Fatalf("RevokedAt = %v, want zero for a new session", got.RevokedAt)
	}
}

func TestCreateSessionWithoutSessionCipherErrors(t *testing.T) {
	ctx := context.Background()
	// No WithSessionCipher: this is the CREDENTIAL_ENCRYPTION_KEY-unset shape
	// that used to break sign-in when sessions were encrypted with the
	// credential cipher. A store with no session cipher configured must fail
	// closed with a clear error, not panic and not store the token in clear.
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)
	now := time.Now().UTC().Truncate(time.Millisecond)

	_, session := newTestSession(t, now)
	err := store.CreateSession(ctx, session)
	if !errors.Is(err, authn.ErrSessionEncryptionUnavailable) {
		t.Fatalf("CreateSession err = %v, want authn.ErrSessionEncryptionUnavailable", err)
	}
}

func TestIDTokenIsNotStoredInClear(t *testing.T) {
	ctx := context.Background()
	store := newSessionTestStore(t, ctx)
	now := time.Now().UTC()

	_, session := newTestSession(t, now)
	if err := store.CreateSession(ctx, session); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	var stored string
	if err := store.pool.QueryRow(ctx,
		`select id_token_ciphertext from sessions where id_hash = $1`, session.IDHash,
	).Scan(&stored); err != nil {
		t.Fatalf("read ciphertext: %v", err)
	}
	if stored == session.IDToken {
		t.Fatal("id_token_ciphertext holds the token verbatim")
	}
}

func TestSessionByHashUnknownIsNotFound(t *testing.T) {
	ctx := context.Background()
	store := newSessionTestStore(t, ctx)

	_, err := store.SessionByHash(ctx, authn.HashSessionID("never-issued"))
	if !errors.Is(err, authn.ErrSessionNotFound) {
		t.Fatalf("err = %v, want authn.ErrSessionNotFound", err)
	}
}

func TestTouchSessionMovesLastSeenAt(t *testing.T) {
	ctx := context.Background()
	store := newSessionTestStore(t, ctx)
	now := time.Now().UTC().Truncate(time.Millisecond)

	_, session := newTestSession(t, now)
	if err := store.CreateSession(ctx, session); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	later := now.Add(10 * time.Minute)
	if err := store.TouchSession(ctx, session.IDHash, later); err != nil {
		t.Fatalf("TouchSession: %v", err)
	}

	got, err := store.SessionByHash(ctx, session.IDHash)
	if err != nil {
		t.Fatalf("SessionByHash: %v", err)
	}
	if !got.LastSeenAt.Equal(later) {
		t.Fatalf("LastSeenAt = %v, want %v", got.LastSeenAt, later)
	}
	if !got.AbsoluteExpiresAt.Equal(session.AbsoluteExpiresAt) {
		t.Fatal("TouchSession moved the absolute bound; it must be unextendable")
	}
}

func TestRevokeSession(t *testing.T) {
	ctx := context.Background()
	store := newSessionTestStore(t, ctx)
	now := time.Now().UTC().Truncate(time.Millisecond)

	_, session := newTestSession(t, now)
	if err := store.CreateSession(ctx, session); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := store.RevokeSession(ctx, session.IDHash, now); err != nil {
		t.Fatalf("RevokeSession: %v", err)
	}

	got, err := store.SessionByHash(ctx, session.IDHash)
	if err != nil {
		t.Fatalf("SessionByHash: %v", err)
	}
	if got.RevokedAt.IsZero() {
		t.Fatal("RevokedAt is zero after RevokeSession")
	}
	if got.IsLive(now, time.Hour) {
		t.Fatal("a revoked session reports itself live")
	}
}

func TestRevokeSessionsByIDPSessionIDAndSubject(t *testing.T) {
	ctx := context.Background()
	store := newSessionTestStore(t, ctx)
	now := time.Now().UTC().Truncate(time.Millisecond)

	_, first := newTestSession(t, now)
	_, second := newTestSession(t, now)
	second.IDHash = authn.HashSessionID("second-session")
	second.IDPSessionID = "idp-sid-2"

	for _, session := range []authn.Session{first, second} {
		if err := store.CreateSession(ctx, session); err != nil {
			t.Fatalf("CreateSession: %v", err)
		}
	}

	revoked, err := store.RevokeSessionsByIDPSessionID(ctx, "idp-sid-1", now)
	if err != nil {
		t.Fatalf("RevokeSessionsByIDPSessionID: %v", err)
	}
	if revoked != 1 {
		t.Fatalf("revoked = %d, want 1 — only the matching sid", revoked)
	}

	// Both rows share a subject; the first is already revoked, so only the
	// second is still revocable.
	revoked, err = store.RevokeSessionsBySubject(ctx, "user-sub-1", now)
	if err != nil {
		t.Fatalf("RevokeSessionsBySubject: %v", err)
	}
	if revoked != 1 {
		t.Fatalf("revoked = %d, want 1 — an already-revoked row must not count again", revoked)
	}
}
