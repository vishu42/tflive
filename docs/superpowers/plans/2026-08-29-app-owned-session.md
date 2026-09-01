# App-Owned Session Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make tflive's session lifetime tflive's own, so a BYO-IdP customer's token and SSO-idle settings stop deciding how long a sign-in lasts.

**Architecture:** The OIDC round trip becomes an authentication event rather than the session itself. The callback records a `sessions` row in Postgres and hands the browser an opaque 32-byte reference; the middleware's cookie path resolves that row instead of verifying an ID token. Revocation, which a stateless cookie cannot offer, arrives through an OIDC Back-Channel Logout endpoint.

**Tech Stack:** Go 1.25, pgx/v5, `github.com/lestrrat-go/jwx/v3/jwt`, `golang.org/x/oauth2`, Postgres, React + TypeScript + vitest, Keycloak 26.6.3.

**Spec:** `docs/superpowers/specs/2026-08-29-app-owned-session-design.md`

## Global Constraints

- **Pre-production.** No users, disposable state. Do not write backfills, compatibility shims, or dual-read paths. A migration may drop and recreate.
- **Branch:** `feat/oidc-server-side-flow`. Do not merge to `main` as part of this plan.
- **The Bearer path is untouched.** `credential()` accepting `Authorization: Bearer` and verifying via `Verifier.Verify` stays exactly as it is. Only the cookie path changes.
- **No refresh tokens, no `offline_access`.** Out of scope by design; do not add the scope.
- **No Redis, no new infrastructure.** Sessions live in the Postgres tflive already runs.
- **The raw session token is never persisted.** Only its SHA-256 hash reaches the database.
- **Session TTL defaults:** absolute 8h, idle 1h, touch interval 5 min.
- **Go tests** run with `go test ./...`. Postgres-backed tests skip unless `tflive_POSTGRES_TEST_DSN` is set; use the existing `openTestPool(t, ctx)` helper in `internal/postgres/store_test.go`.
- **Web tests** run from `web/` with `npx vitest run`.

---

## File Structure

| File | Responsibility |
|---|---|
| `internal/authn/session_store.go` | *new* — `Session` record, id generation and hashing, expiry arithmetic, `SessionStore` interface. Pure domain, no database. |
| `internal/authn/session_store_test.go` | *new* — expiry rules and hashing. |
| `internal/postgres/migrations/0018_sessions.sql` | *new* — `sessions` table. |
| `internal/postgres/sessions.go` | *new* — `Store` methods implementing `authn.SessionStore`. |
| `internal/postgres/sessions_test.go` | *new* — round-trip, touch, revoke-by-sid/subject. |
| `internal/config/auth.go` | `SecurityConfig` gains the two TTLs. |
| `internal/authn/oidc_verifier.go` | `VerifiedToken` gains `SessionID` (`sid`). |
| `internal/authn/session.go` | `SessionCookie` carries an opaque reference. |
| `internal/authn/middleware.go` | cookie path resolves through the session store. |
| `internal/api/auth.go` | callback creates a session; logout revokes it; `warnOnOversizedSession` deleted. |
| `internal/authn/logout_token.go` | *new* — back-channel logout token verification. |
| `internal/api/backchannel_logout.go` | *new* — the endpoint. |
| `internal/keycloak/provisioner.go` | registers `backchannel.logout.url`. |
| `internal/auth/me.go` | `SessionExpiresAt` reports the session's expiry. |
| `web/src/auth/SessionProvider.tsx` | deferral deadline. |
| `docs/authentication.md`, `.env.example`, `README.md` | follow. |

---

### Task 1: Session record and lifetime rules

Pure domain logic with no database, so the expiry rules are testable without Postgres.

**Files:**
- Create: `internal/authn/session_store.go`
- Test: `internal/authn/session_store_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `type Session struct` with fields `IDHash, Subject, Name, PreferredUsername, Email, IDPSessionID, IDToken string`, `CreatedAt, LastSeenAt, AbsoluteExpiresAt time.Time`, `RevokedAt time.Time`
  - `func NewSessionID() (string, error)`
  - `func HashSessionID(raw string) string`
  - `func (s Session) ExpiresAt(idleTTL time.Duration) time.Time`
  - `func (s Session) IsLive(now time.Time, idleTTL time.Duration) bool`
  - `type SessionStore interface` with `CreateSession`, `SessionByHash`, `TouchSession`, `RevokeSession`, `RevokeSessionsByIDPSessionID`, `RevokeSessionsBySubject`
  - `var ErrSessionNotFound = errors.New("session not found")`
  - `const DefaultSessionAbsoluteTTL = 8 * time.Hour`, `DefaultSessionIdleTTL = time.Hour`, `SessionTouchInterval = 5 * time.Minute`

- [ ] **Step 1: Write the failing test**

Create `internal/authn/session_store_test.go`:

```go
package authn

import (
	"strings"
	"testing"
	"time"
)

func TestNewSessionIDIsUnpredictableAndURLSafe(t *testing.T) {
	first, err := NewSessionID()
	if err != nil {
		t.Fatalf("NewSessionID: %v", err)
	}
	second, err := NewSessionID()
	if err != nil {
		t.Fatalf("NewSessionID: %v", err)
	}
	if first == second {
		t.Fatal("two session IDs are identical, so they are not random")
	}
	// 32 bytes base64url-encoded without padding.
	if len(first) != 43 {
		t.Fatalf("session ID length = %d, want 43", len(first))
	}
	if strings.ContainsAny(first, "+/=") {
		t.Fatalf("session ID %q is not URL-safe", first)
	}
}

func TestHashSessionIDIsStableAndNotTheInput(t *testing.T) {
	raw, err := NewSessionID()
	if err != nil {
		t.Fatalf("NewSessionID: %v", err)
	}
	hash := HashSessionID(raw)
	if hash == raw {
		t.Fatal("hash equals the raw ID, so the database would hold a usable cookie")
	}
	if hash != HashSessionID(raw) {
		t.Fatal("hash is not stable, so a session could never be looked up twice")
	}
}

func TestExpiresAtIsTheEarlierBound(t *testing.T) {
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	idleTTL := time.Hour

	t.Run("idle bound is earlier", func(t *testing.T) {
		session := Session{
			LastSeenAt:        now,
			AbsoluteExpiresAt: now.Add(8 * time.Hour),
		}
		if got, want := session.ExpiresAt(idleTTL), now.Add(time.Hour); !got.Equal(want) {
			t.Fatalf("ExpiresAt = %v, want %v", got, want)
		}
	})

	t.Run("absolute bound is earlier", func(t *testing.T) {
		session := Session{
			LastSeenAt:        now,
			AbsoluteExpiresAt: now.Add(10 * time.Minute),
		}
		if got, want := session.ExpiresAt(idleTTL), now.Add(10*time.Minute); !got.Equal(want) {
			t.Fatalf("ExpiresAt = %v, want %v", got, want)
		}
	})
}

func TestIsLive(t *testing.T) {
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	idleTTL := time.Hour
	live := Session{LastSeenAt: now, AbsoluteExpiresAt: now.Add(8 * time.Hour)}

	tests := []struct {
		name    string
		session Session
		at      time.Time
		want    bool
	}{
		{"fresh", live, now, true},
		{"just inside the idle bound", live, now.Add(59 * time.Minute), true},
		{"past the idle bound", live, now.Add(61 * time.Minute), false},
		{
			name:    "past the absolute bound despite recent activity",
			session: Session{LastSeenAt: now.Add(8 * time.Hour), AbsoluteExpiresAt: now.Add(8 * time.Hour)},
			at:      now.Add(8*time.Hour + time.Second),
			want:    false,
		},
		{
			name:    "revoked",
			session: Session{LastSeenAt: now, AbsoluteExpiresAt: now.Add(8 * time.Hour), RevokedAt: now},
			at:      now,
			want:    false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.session.IsLive(test.at, idleTTL); got != test.want {
				t.Fatalf("IsLive = %v, want %v", got, test.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/authn/ -run 'TestNewSessionID|TestHashSessionID|TestExpiresAt|TestIsLive' -v`
Expected: FAIL — build error, `undefined: NewSessionID`.

- [ ] **Step 3: Write minimal implementation**

Create `internal/authn/session_store.go`:

```go
package authn

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"
)

// Session is one signed-in browser, owned by tflive rather than by the IdP.
// The claims are copied at sign-in: after that, requests authenticate against
// this record and the ID token is not re-read, so session lifetime is ours to
// choose rather than a consequence of the provider's token lifespan.
//
// There is deliberately no field for the raw session ID. The value handed to
// the browser exists only in the callback handler and the cookie; the record
// can hold nothing but its hash, so a database leak yields no usable cookies.
type Session struct {
	IDHash            string
	Subject           string
	Name              string
	PreferredUsername string
	Email             string
	// IDPSessionID is the ID token's sid claim, empty when the provider omits
	// it. It is the key a back-channel logout arrives on.
	IDPSessionID string
	// IDToken is the raw ID token, kept for id_token_hint at RP-initiated
	// logout. The repository encrypts it at rest.
	IDToken           string
	CreatedAt         time.Time
	LastSeenAt        time.Time
	AbsoluteExpiresAt time.Time
	// RevokedAt is the zero time while the session stands.
	RevokedAt time.Time
}

const (
	// DefaultSessionAbsoluteTTL caps a session from sign-in and cannot be
	// extended. A workday: long enough that nobody is interrupted mid-task.
	DefaultSessionAbsoluteTTL = 8 * time.Hour
	// DefaultSessionIdleTTL ends a session this long after its last request,
	// so an unattended browser does not stay authenticated overnight.
	DefaultSessionIdleTTL = time.Hour
	// SessionTouchInterval is how stale LastSeenAt may get before a request
	// writes it back. Sliding on every request would mean a write per
	// request; this bounds the idle check's accuracy instead.
	SessionTouchInterval = 5 * time.Minute
)

var ErrSessionNotFound = errors.New("session not found")

// NewSessionID returns the opaque value handed to the browser.
func NewSessionID() (string, error) {
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

// HashSessionID reduces a session ID to what the database stores. The input is
// 32 bytes of CSPRNG output, so a plain SHA-256 is enough: there is no
// guessable keyspace for a password-style KDF to defend.
func HashSessionID(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// ExpiresAt is the earlier of the two bounds, which is what the browser is
// told so it can re-authenticate at a quiet moment.
func (s Session) ExpiresAt(idleTTL time.Duration) time.Time {
	idle := s.LastSeenAt.Add(idleTTL)
	if s.AbsoluteExpiresAt.Before(idle) {
		return s.AbsoluteExpiresAt
	}
	return idle
}

func (s Session) IsLive(now time.Time, idleTTL time.Duration) bool {
	if !s.RevokedAt.IsZero() {
		return false
	}
	return now.Before(s.ExpiresAt(idleTTL))
}

// SessionStore is the persistence the cookie path needs. *postgres.Store
// implements it; the interface exists so the middleware and handler tests need
// no database.
type SessionStore interface {
	CreateSession(ctx context.Context, session Session) error
	SessionByHash(ctx context.Context, idHash string) (Session, error)
	TouchSession(ctx context.Context, idHash string, seenAt time.Time) error
	RevokeSession(ctx context.Context, idHash string, at time.Time) error
	RevokeSessionsByIDPSessionID(ctx context.Context, idpSessionID string, at time.Time) (int, error)
	RevokeSessionsBySubject(ctx context.Context, subject string, at time.Time) (int, error)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/authn/ -run 'TestNewSessionID|TestHashSessionID|TestExpiresAt|TestIsLive' -v`
Expected: PASS, all subtests.

- [ ] **Step 5: Commit**

```bash
git add internal/authn/session_store.go internal/authn/session_store_test.go
git commit -m "feat(authn): add the session record and its lifetime rules"
```

---

### Task 2: Postgres sessions table and store methods

**Files:**
- Create: `internal/postgres/migrations/0018_sessions.sql`
- Create: `internal/postgres/sessions.go`
- Test: `internal/postgres/sessions_test.go`

**Interfaces:**
- Consumes: `authn.Session`, `authn.ErrSessionNotFound`, `authn.HashSessionID` from Task 1.
- Produces: `*postgres.Store` satisfying `authn.SessionStore`. `CreateSession` encrypts `Session.IDToken` with the store's credential cipher; `SessionByHash` decrypts it.

- [ ] **Step 1: Write the migration**

Create `internal/postgres/migrations/0018_sessions.sql`:

```sql
-- A session is tflive's own, not the IdP's.
--
-- Before this table the session cookie held the raw ID token, so how long a
-- sign-in lasted was decided by the provider's token lifespan and whether
-- renewal was silent by its SSO idle timeout. tflive is BYO-IdP and sets
-- neither on a customer's provider. A row here is a session we issue, expire,
-- and revoke on our own terms.
--
-- id_hash, not id: the value handed to the browser is 32 bytes of CSPRNG
-- output and only its SHA-256 reaches the database, so a dump of this table
-- yields no usable cookies.
--
-- No tenant_id. A session is an authentication fact about a browser and the
-- deployment serves one configured tenant; scoping it would imply a
-- cross-tenant question that cannot be asked here.
create table sessions (
	id_hash text primary key,
	subject text not null,
	name text not null,
	preferred_username text not null,
	email text not null,
	-- The ID token's sid claim. Empty when the provider omits it, in which
	-- case back-channel logout falls back to matching on subject.
	idp_session_id text not null,
	-- Encrypted at rest. Kept only for id_token_hint at RP-initiated logout,
	-- without which Keycloak shows a confirmation page instead of signing out.
	id_token_ciphertext text not null,
	created_at timestamptz not null,
	last_seen_at timestamptz not null,
	absolute_expires_at timestamptz not null,
	revoked_at timestamptz
);

-- Back-channel logout arrives with a sid or a sub and must find the live
-- sessions for it. Partial: revoked rows are never the target of a revoke.
create index sessions_idp_session_id_idx on sessions (idp_session_id)
where revoked_at is null;

create index sessions_subject_idx on sessions (subject)
where revoked_at is null;

-- Supports deleting rows that can no longer authenticate anyone.
create index sessions_absolute_expires_at_idx on sessions (absolute_expires_at);
```

- [ ] **Step 2: Write the failing test**

Create `internal/postgres/sessions_test.go`:

```go
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
	return NewStore(pool, WithCredentialCipher(cipher))
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
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/postgres/ -run TestCreateAndReadSession -v`
Expected: FAIL — build error, `store.CreateSession undefined`.

(If `tflive_POSTGRES_TEST_DSN` is unset the test skips instead. Set it first — the local stack's value is in `.env`.)

- [ ] **Step 4: Write minimal implementation**

Create `internal/postgres/sessions.go`:

```go
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/vishu42/tflive/internal/authn"
)

func (store *Store) CreateSession(ctx context.Context, session authn.Session) error {
	ciphertext, err := store.credentialCipher.Encrypt(session.IDToken)
	if err != nil {
		return fmt.Errorf("create session: encrypt id token: %w", err)
	}
	_, err = store.pool.Exec(ctx, `
		insert into sessions (
			id_hash,
			subject,
			name,
			preferred_username,
			email,
			idp_session_id,
			id_token_ciphertext,
			created_at,
			last_seen_at,
			absolute_expires_at
		) values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`,
		session.IDHash,
		session.Subject,
		session.Name,
		session.PreferredUsername,
		session.Email,
		session.IDPSessionID,
		ciphertext,
		session.CreatedAt,
		session.LastSeenAt,
		session.AbsoluteExpiresAt,
	)
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

func (store *Store) SessionByHash(ctx context.Context, idHash string) (authn.Session, error) {
	var (
		session    authn.Session
		ciphertext string
		revokedAt  *time.Time
	)
	err := store.pool.QueryRow(ctx, `
		select
			id_hash,
			subject,
			name,
			preferred_username,
			email,
			idp_session_id,
			id_token_ciphertext,
			created_at,
			last_seen_at,
			absolute_expires_at,
			revoked_at
		from sessions
		where id_hash = $1
	`, idHash).Scan(
		&session.IDHash,
		&session.Subject,
		&session.Name,
		&session.PreferredUsername,
		&session.Email,
		&session.IDPSessionID,
		&ciphertext,
		&session.CreatedAt,
		&session.LastSeenAt,
		&session.AbsoluteExpiresAt,
		&revokedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return authn.Session{}, authn.ErrSessionNotFound
	}
	if err != nil {
		return authn.Session{}, fmt.Errorf("session by hash: %w", err)
	}
	if revokedAt != nil {
		session.RevokedAt = *revokedAt
	}
	idToken, err := store.credentialCipher.Decrypt(ciphertext)
	if err != nil {
		return authn.Session{}, fmt.Errorf("session by hash: decrypt id token: %w", err)
	}
	session.IDToken = idToken
	return session, nil
}

// TouchSession moves the idle bound only. The absolute bound is deliberately
// not touched: it is a cap from sign-in, and extending it here would make it
// unreachable for an active session.
func (store *Store) TouchSession(ctx context.Context, idHash string, seenAt time.Time) error {
	_, err := store.pool.Exec(ctx, `
		update sessions set last_seen_at = $2
		where id_hash = $1 and revoked_at is null
	`, idHash, seenAt)
	if err != nil {
		return fmt.Errorf("touch session: %w", err)
	}
	return nil
}

func (store *Store) RevokeSession(ctx context.Context, idHash string, at time.Time) error {
	_, err := store.pool.Exec(ctx, `
		update sessions set revoked_at = $2
		where id_hash = $1 and revoked_at is null
	`, idHash, at)
	if err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}
	return nil
}

func (store *Store) RevokeSessionsByIDPSessionID(ctx context.Context, idpSessionID string, at time.Time) (int, error) {
	// An empty sid would match every session from a provider that omits the
	// claim, so it is never a revocation key.
	if idpSessionID == "" {
		return 0, nil
	}
	tag, err := store.pool.Exec(ctx, `
		update sessions set revoked_at = $2
		where idp_session_id = $1 and revoked_at is null
	`, idpSessionID, at)
	if err != nil {
		return 0, fmt.Errorf("revoke sessions by idp session id: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

func (store *Store) RevokeSessionsBySubject(ctx context.Context, subject string, at time.Time) (int, error) {
	if subject == "" {
		return 0, nil
	}
	tag, err := store.pool.Exec(ctx, `
		update sessions set revoked_at = $2
		where subject = $1 and revoked_at is null
	`, subject, at)
	if err != nil {
		return 0, fmt.Errorf("revoke sessions by subject: %w", err)
	}
	return int(tag.RowsAffected()), nil
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run:
```bash
export tflive_POSTGRES_TEST_DSN='postgres://tflive:tflive@localhost:55432/tflive_test?sslmode=disable'
go test ./internal/postgres/ -run 'Session' -v
```
Expected: PASS for all six session tests.

- [ ] **Step 6: Commit**

```bash
git add internal/postgres/migrations/0018_sessions.sql internal/postgres/sessions.go internal/postgres/sessions_test.go
git commit -m "feat(postgres): store app-owned sessions"
```

---

### Task 3: Carry `sid`, configure the TTLs, and issue a session at the callback

**Files:**
- Modify: `internal/authn/verifier.go` (`VerifiedToken`)
- Modify: `internal/authn/oidc_verifier.go` (read the `sid` claim)
- Modify: `internal/authn/session.go` (`SessionCookie` doc comment)
- Modify: `internal/config/auth.go` (`SecurityConfig` TTLs)
- Modify: `internal/api/server.go` (`AuthConfig`)
- Modify: `internal/api/auth.go` (`handleAuthCallback`)
- Test: `internal/api/auth_test.go`, `internal/config/auth_test.go`, `internal/authn/oidc_verifier_test.go`

**Interfaces:**
- Consumes: `authn.Session`, `authn.NewSessionID`, `authn.HashSessionID`, `authn.SessionStore` (Task 1); `*postgres.Store` (Task 2).
- Produces:
  - `VerifiedToken.SessionID string`
  - `AuthConfig.Sessions authn.SessionStore`, `AuthConfig.SessionAbsoluteTTL`, `AuthConfig.SessionIdleTTL time.Duration`, `AuthConfig.Clock func() time.Time`
  - `SecurityConfig.SessionAbsoluteTTL`, `SecurityConfig.SessionIdleTTL time.Duration`, read from `TFLIVE_SESSION_ABSOLUTE_TTL` / `TFLIVE_SESSION_IDLE_TTL`

- [ ] **Step 1: Write the failing tests**

Add to `internal/authn/oidc_verifier_test.go`:

```go
func TestVerifyCarriesSessionIDClaim(t *testing.T) {
	// Follow the existing helper in this file that mints a signed token; add
	// "sid": "idp-sid-1" to its claims.
	verified := verifyTokenWithClaims(t, map[string]any{"sid": "idp-sid-1"})
	if verified.SessionID != "idp-sid-1" {
		t.Fatalf("SessionID = %q, want %q", verified.SessionID, "idp-sid-1")
	}
}

func TestVerifyToleratesAbsentSessionIDClaim(t *testing.T) {
	verified := verifyTokenWithClaims(t, nil)
	if verified.SessionID != "" {
		t.Fatalf("SessionID = %q, want empty when sid is absent", verified.SessionID)
	}
}
```

> `verifyTokenWithClaims` does not exist yet. Write it as a helper in that test file, modelled on the existing signed-token construction there: it takes extra claims, mints and signs a token, runs it through the test verifier, and fails the test on error.

Add to `internal/config/auth_test.go`:

```go
func TestSessionTTLDefaults(t *testing.T) {
	cfg := loadValidSecurityConfig(t, nil)
	if cfg.SessionAbsoluteTTL != 8*time.Hour {
		t.Fatalf("SessionAbsoluteTTL = %v, want 8h", cfg.SessionAbsoluteTTL)
	}
	if cfg.SessionIdleTTL != time.Hour {
		t.Fatalf("SessionIdleTTL = %v, want 1h", cfg.SessionIdleTTL)
	}
}

func TestSessionTTLOverrides(t *testing.T) {
	cfg := loadValidSecurityConfig(t, map[string]string{
		"TFLIVE_SESSION_ABSOLUTE_TTL": "2h",
		"TFLIVE_SESSION_IDLE_TTL":     "15m",
	})
	if cfg.SessionAbsoluteTTL != 2*time.Hour {
		t.Fatalf("SessionAbsoluteTTL = %v, want 2h", cfg.SessionAbsoluteTTL)
	}
	if cfg.SessionIdleTTL != 15*time.Minute {
		t.Fatalf("SessionIdleTTL = %v, want 15m", cfg.SessionIdleTTL)
	}
}

func TestSessionTTLRejectsNonPositiveAndInverted(t *testing.T) {
	tests := map[string]map[string]string{
		"zero absolute":         {"TFLIVE_SESSION_ABSOLUTE_TTL": "0s"},
		"negative idle":         {"TFLIVE_SESSION_IDLE_TTL": "-1m"},
		"unparseable":           {"TFLIVE_SESSION_IDLE_TTL": "soon"},
		"idle longer than cap":  {"TFLIVE_SESSION_ABSOLUTE_TTL": "1h", "TFLIVE_SESSION_IDLE_TTL": "2h"},
	}
	for name, env := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := loadSecurityConfigWith(t, env); err == nil {
				t.Fatal("want an error, got none")
			}
		})
	}
}
```

> `loadValidSecurityConfig` / `loadSecurityConfigWith` are helpers to write in that file if absent: they build a complete valid env map, apply the overrides, and call `loadSecurityConfig` with a `getenv` closure over the map.

First extend the existing harness in `internal/api/auth_test.go`. It is currently
`newAuthTestServer(t *testing.T, flow *stubFlow, verifier authn.Verifier) *Server`; make the extra
dependencies optional so the existing call sites keep compiling unchanged:

```go
// authTestOption adjusts the AuthConfig a test server is built with.
type authTestOption func(*AuthConfig)

func withSessions(store authn.SessionStore) authTestOption {
	return func(cfg *AuthConfig) { cfg.Sessions = store }
}

func withLogoutTokenVerifier(verifier LogoutTokenVerifier) authTestOption {
	return func(cfg *AuthConfig) { cfg.LogoutTokenVerifier = verifier }
}

func withClock(now time.Time) authTestOption {
	return func(cfg *AuthConfig) { cfg.Clock = func() time.Time { return now } }
}

// fakeSessionStore is an in-memory authn.SessionStore.
type fakeSessionStore struct {
	created          []authn.Session
	byHash           map[string]authn.Session
	touched          int
	revoked          map[string]int
	revokedBySID     map[string]int
	revokedBySubject map[string]int
}

func newFakeSessionStore() *fakeSessionStore {
	return &fakeSessionStore{
		byHash:           map[string]authn.Session{},
		revoked:          map[string]int{},
		revokedBySID:     map[string]int{},
		revokedBySubject: map[string]int{},
	}
}
```

Give `fakeSessionStore` the six `authn.SessionStore` methods: `CreateSession` appends to `created`
and stores into `byHash`; `SessionByHash` returns `authn.ErrSessionNotFound` when absent;
`TouchSession` increments `touched`; the three revoke methods increment their counters and return
`1` when a matching entry exists, `0` otherwise.

Then update `newAuthTestServer` to take `options ...authTestOption`, applying each to the
`AuthConfig` before `NewServer`, and default `SessionAbsoluteTTL`/`SessionIdleTTL` to
`authn.DefaultSessionAbsoluteTTL` / `authn.DefaultSessionIdleTTL` so tests that do not care get
the production values.

Now add the tests:

```go
func TestCallbackCreatesSessionAndSetsOpaqueCookie(t *testing.T) {
	sessions := newFakeSessionStore()
	flow := &stubFlow{idToken: "header.payload.signature"}
	verifier := stubVerifier{token: authn.VerifiedToken{
		Subject: "user-1", Name: "Ada Lovelace", Email: "ada@example.test",
		SessionID: "idp-sid-1", Nonce: "nonce-1",
	}}
	server := newAuthTestServer(t, flow, verifier, withSessions(sessions))

	response := runCallback(t, server, "nonce-1")

	cookie := cookieByName(response, authn.SessionCookieName)
	if cookie == nil {
		t.Fatal("no session cookie was set")
	}
	if strings.Count(cookie.Value, ".") == 2 {
		t.Fatalf("cookie value %q still looks like a JWT; it must be an opaque reference", cookie.Value)
	}
	if len(sessions.created) != 1 {
		t.Fatalf("created %d sessions, want 1", len(sessions.created))
	}

	created := sessions.created[0]
	if created.IDHash != authn.HashSessionID(cookie.Value) {
		t.Fatal("the stored hash does not match the cookie the browser was given")
	}
	if created.IDToken != "header.payload.signature" {
		t.Fatal("the ID token was not kept; logout needs it for id_token_hint")
	}
	if created.IDPSessionID != "idp-sid-1" {
		t.Fatalf("IDPSessionID = %q, want idp-sid-1", created.IDPSessionID)
	}
	if created.AbsoluteExpiresAt.Sub(created.CreatedAt) != authn.DefaultSessionAbsoluteTTL {
		t.Fatalf("absolute window = %v, want 8h", created.AbsoluteExpiresAt.Sub(created.CreatedAt))
	}
}

func TestCallbackSessionLifetimeIgnoresTokenExpiry(t *testing.T) {
	// The whole point of the change: a 60-second ID token must still produce a
	// full-length session.
	sessions := newFakeSessionStore()
	flow := &stubFlow{idToken: "header.payload.signature"}
	verifier := stubVerifier{token: authn.VerifiedToken{
		Subject: "user-1", Nonce: "nonce-1",
		ExpiresAt: time.Now().Add(time.Minute),
	}}
	server := newAuthTestServer(t, flow, verifier, withSessions(sessions))

	runCallback(t, server, "nonce-1")

	created := sessions.created[0]
	if created.AbsoluteExpiresAt.Sub(created.CreatedAt) != authn.DefaultSessionAbsoluteTTL {
		t.Fatalf("absolute window = %v, want 8h regardless of the token's exp", created.AbsoluteExpiresAt.Sub(created.CreatedAt))
	}
}
```

> `runCallback(t, server, nonce)` is a helper to add: it seals an `authn.Transaction` with a known
> state and the given nonce using the same cipher `newAuthTestServer` builds, issues a
> `GET /v1/auth/callback?code=...&state=...` request carrying the transaction cookie, calls
> `server.ServeHTTP`, and returns the recorder. The successful-callback test already in this file
> does all of this inline — factor it out rather than writing a second version.
>
> The field names used above are the ones that exist: `stubFlow.idToken` is what `Exchange`
> returns, and `stubFlow.gotIDTokenHint` records what `EndSessionURL` was handed.

- [ ] **Step 2: Run tests to verify they fail**

Run:
```bash
go test ./internal/authn/ -run TestVerifyCarries -v
go test ./internal/config/ -run TestSessionTTL -v
go test ./internal/api/ -run TestCallbackCreatesSession -v
```
Expected: FAIL — `verified.SessionID undefined`, `cfg.SessionAbsoluteTTL undefined`, `withSessions undefined`.

- [ ] **Step 3: Add `SessionID` to the verified token**

In `internal/authn/verifier.go`, add to `VerifiedToken`:

```go
	// SessionID is the token's sid claim, empty when the provider omits it.
	// It is the key a back-channel logout arrives on, so it is copied into the
	// session record at sign-in.
	SessionID string
```

In `internal/authn/oidc_verifier.go`, alongside the existing `optionalStringClaim` reads for `name` and `preferred_username`:

```go
	sessionID, ok := optionalStringClaim(token, "sid")
	if !ok {
		return VerifiedToken{}, ErrInvalidToken
	}
```

and set `SessionID: sessionID` on the returned `VerifiedToken`.

- [ ] **Step 4: Add the TTL configuration**

In `internal/config/auth.go`, add `SessionAbsoluteTTL` and `SessionIdleTTL time.Duration` to `SecurityConfig`, and in `loadSecurityConfig`, after the `sessionKey` block:

```go
	sessionAbsoluteTTL, err := optionalPositiveDuration(getenv, "TFLIVE_SESSION_ABSOLUTE_TTL", authn.DefaultSessionAbsoluteTTL)
	if err != nil {
		return SecurityConfig{}, err
	}
	sessionIdleTTL, err := optionalPositiveDuration(getenv, "TFLIVE_SESSION_IDLE_TTL", authn.DefaultSessionIdleTTL)
	if err != nil {
		return SecurityConfig{}, err
	}
	// An idle bound past the absolute cap can never be reached, so it is a
	// configuration mistake rather than a permissive setting.
	if sessionIdleTTL > sessionAbsoluteTTL {
		return SecurityConfig{}, authConfigError("TFLIVE_SESSION_IDLE_TTL must not exceed TFLIVE_SESSION_ABSOLUTE_TTL")
	}
```

with the helper:

```go
func optionalPositiveDuration(getenv func(string) string, name string, fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value <= 0 {
		return 0, authConfigError(name + " must be a positive duration")
	}
	return value, nil
}
```

Assign both into the returned `SecurityConfig`, and add them to its `String()` output next to the other non-secret fields.

- [ ] **Step 5: Widen `AuthConfig` and create the session at the callback**

In `internal/api/server.go`, add to `AuthConfig`:

```go
	// Sessions persists the app-owned session the callback creates. The
	// browser is handed a reference to it, never the ID token.
	Sessions           authn.SessionStore
	SessionAbsoluteTTL time.Duration
	SessionIdleTTL     time.Duration
	// Clock is time.Now when nil. Tests set it.
	Clock func() time.Time
```

and a helper on `Server`:

```go
func (server *Server) now() time.Time {
	if server.auth.Clock != nil {
		return server.auth.Clock()
	}
	return time.Now().UTC()
}
```

In `internal/api/auth.go`, replace the tail of `handleAuthCallback` — the `warnOnOversizedSession` and `SetCookie` lines — with:

```go
	sessionID, err := authn.NewSessionID()
	if err != nil {
		log.Printf("auth callback: failed to generate session id: %v", err)
		server.writeAuthFailure(response)
		return
	}
	now := server.now()
	session := authn.Session{
		IDHash:            authn.HashSessionID(sessionID),
		Subject:           verified.Subject,
		Name:              verified.Name,
		PreferredUsername: verified.PreferredUsername,
		Email:             verified.Email,
		IDPSessionID:      verified.SessionID,
		IDToken:           rawIDToken,
		CreatedAt:         now,
		LastSeenAt:        now,
		// The IdP's token lifetime deliberately does not appear here. How long
		// a tflive session lasts is tflive's to decide; the token's exp bounded
		// only the authentication we just completed.
		AbsoluteExpiresAt: now.Add(server.auth.SessionAbsoluteTTL),
	}
	if err := server.auth.Sessions.CreateSession(request.Context(), session); err != nil {
		log.Printf("auth callback: failed to persist session: %v", err)
		server.writeAuthFailure(response)
		return
	}

	http.SetCookie(response, authn.SessionCookie(sessionID, server.auth.SecureCookies))
	http.Redirect(response, request, authn.SafeReturnTo(transaction.ReturnTo), http.StatusFound)
```

Delete `warnOnOversizedSession` entirely — an opaque 43-byte reference cannot approach the cookie limit, so the warning has nothing left to warn about.

Update the `SessionCookieName` comment in `internal/authn/session.go`:

```go
	// SessionCookieName holds an opaque reference to a session row, not a
	// token. Only its SHA-256 reaches the database, so the cookie is useless
	// to anyone who reads the table, and its size does not depend on how many
	// claims the provider puts in an ID token.
	SessionCookieName = "tflive_session"
```

- [ ] **Step 6: Wire it up in `cmd/tflive-api`**

Where `api.WithAuth(api.AuthConfig{...})` is constructed, pass the store and the two TTLs from `SecurityConfig`. Find the call site with:

```bash
grep -rn 'WithAuth(' cmd/ internal/ --include='*.go' | grep -v _test
```

- [ ] **Step 7: Run tests to verify they pass**

Run:
```bash
go build ./...
go test ./internal/authn/ ./internal/config/ ./internal/api/ -v 2>&1 | tail -30
```
Expected: PASS, including the three new test groups.

- [ ] **Step 8: Commit**

```bash
git add internal/authn internal/config internal/api cmd
git commit -m "feat(authn): issue an app-owned session at the callback"
```

---

### Task 4: Authenticate the cookie path from the session store

**Files:**
- Modify: `internal/authn/middleware.go`
- Test: `internal/authn/middleware_test.go`

**Interfaces:**
- Consumes: `SessionStore`, `Session`, `HashSessionID`, `ErrSessionNotFound`, `SessionTouchInterval` (Task 1).
- Produces: `func RequireAuthentication(verifier Verifier, sessions SessionStore, idleTTL time.Duration, clock func() time.Time, publicPaths ...string) func(http.Handler) http.Handler` — note the changed signature; every caller must be updated.

- [ ] **Step 1: Write the failing test**

Add to `internal/authn/middleware_test.go`:

```go
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
```

> Add to this test file: `fakeSessionStore` (a `SessionStore` over a `map[string]Session` with a `touched int` counter), `rejectingVerifier` (returns `ErrInvalidToken` always), `acceptingVerifier` (returns a `VerifiedToken` with the given subject), and `serveAuthenticated(t, sessions, now, raw)` (builds the middleware, issues a cookie request, asserts 200).

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/authn/ -run 'TestCookieAuthenticates|TestExpiredAndRevoked|TestTouchOnly|TestBearerPath' -v`
Expected: FAIL — `too many arguments in call to RequireAuthentication`.

- [ ] **Step 3: Rewrite the middleware**

Replace `RequireAuthentication` and `credential` in `internal/authn/middleware.go`:

```go
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
```

Delete the old `credential` function. Keep `bearerToken` and `writeUnauthorized` unchanged. Add `"time"` to the imports.

- [ ] **Step 4: Update the call site**

In `internal/api/server.go`, the `RequireAuthentication` call near line 162 gains the store, idle TTL, and clock:

```go
	authn.RequireAuthentication(
		server.auth.Verifier,
		server.auth.Sessions,
		server.auth.SessionIdleTTL,
		server.auth.Clock,
		"/healthz", "/v1/auth/login", "/v1/auth/callback", "/v1/auth/logout",
	)
```

- [ ] **Step 5: Run tests to verify they pass**

Run:
```bash
go build ./...
go test ./internal/authn/ ./internal/api/ -v 2>&1 | tail -20
```
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/authn/middleware.go internal/authn/middleware_test.go internal/api/server.go
git commit -m "feat(authn): authenticate the cookie path from the session store"
```

---

### Task 5: Logout revokes server-side, and `/v1/me` reports the session's expiry

**Files:**
- Modify: `internal/api/auth.go` (`handleAuthLogout`)
- Modify: `internal/auth/me.go` (comment only)
- Test: `internal/api/auth_test.go`, `internal/auth/me_test.go`

**Interfaces:**
- Consumes: `SessionStore.RevokeSession`, `Session.IDToken` (Tasks 1–2); `Principal.ExpiresAt` now set from `Session.ExpiresAt` (Task 4).
- Produces: no new symbols.

- [ ] **Step 1: Write the failing test**

Add to `internal/api/auth_test.go`:

```go
func TestLogoutRevokesTheSessionAndUsesTheStoredIDToken(t *testing.T) {
	raw := "session-token"
	sessions := newFakeSessionStore()
	sessions.byHash[authn.HashSessionID(raw)] = authn.Session{
		IDHash:  authn.HashSessionID(raw),
		Subject: "user-1",
		IDToken: "stored.id.token",
	}
	flow := &stubFlow{endSessionURL: "https://idp.test/logout"}
	server := newAuthTestServer(t, flow, stubVerifier{}, withSessions(sessions))

	request := httptest.NewRequest(http.MethodPost, "/v1/auth/logout", nil)
	request.AddCookie(&http.Cookie{Name: authn.SessionCookieName, Value: raw})
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", recorder.Code)
	}
	if sessions.revoked[authn.HashSessionID(raw)] != 1 {
		t.Fatal("logout did not revoke the session row; a copied cookie would still work")
	}
	// stubFlow records what it was handed rather than building a URL.
	if flow.gotIDTokenHint != "stored.id.token" {
		t.Fatalf("id_token_hint = %q, want the ID token stored on the session", flow.gotIDTokenHint)
	}
}

func TestLogoutWithoutASessionStillRedirects(t *testing.T) {
	sessions := newFakeSessionStore()
	server := newAuthTestServer(t, &stubFlow{}, stubVerifier{}, withSessions(sessions))

	request := httptest.NewRequest(http.MethodPost, "/v1/auth/logout", nil)
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303 even with no session", recorder.Code)
	}
}
```

> `fakeSessionStore` already carries the `revoked` counter from Task 3.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/api/ -run TestLogout -v`
Expected: FAIL — the session is never revoked and the redirect carries no `id_token_hint`.

- [ ] **Step 3: Rewrite `handleAuthLogout`**

In `internal/api/auth.go`:

```go
// handleAuthLogout revokes our session and sends the browser on to end the
// IdP's. Without that second half, logging out and back in silently returns
// the same user, because the provider's SSO session still stands.
//
// Revoking the row rather than only clearing the cookie is what makes logout
// real: a copy of the cookie taken beforehand stops working too.
//
// It redirects rather than returning the URL in a body: that URL carries the
// raw ID token as id_token_hint, a body would be readable by any script on the
// origin, and the middleware accepts that token as a bearer credential. A
// Location header on a 303 is not script-readable, and http.Redirect writes no
// body for a POST.
func (server *Server) handleAuthLogout(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "no-store")

	var idTokenHint string
	if cookie, err := request.Cookie(authn.SessionCookieName); err == nil && cookie.Value != "" {
		idHash := authn.HashSessionID(cookie.Value)
		if session, err := server.auth.Sessions.SessionByHash(request.Context(), idHash); err == nil {
			idTokenHint = session.IDToken
		}
		if err := server.auth.Sessions.RevokeSession(request.Context(), idHash, server.now()); err != nil {
			// The cookie is cleared regardless, so the browser is signed out
			// either way; the row outliving it is what this logs.
			log.Printf("auth logout: failed to revoke session: %v", err)
		}
	}
	http.SetCookie(response, authn.ClearedSessionCookie(server.auth.SecureCookies))

	destination := server.auth.PublicURL + "/"
	if idTokenHint != "" {
		if logoutURL := server.auth.Flow.EndSessionURL(idTokenHint, server.auth.PublicURL+"/"); logoutURL != "" {
			destination = logoutURL
		}
	}
	http.Redirect(response, request, destination, http.StatusSeeOther)
}
```

- [ ] **Step 4: Update the `SessionExpiresAt` comment**

In `internal/auth/me.go`, the doc comment on `SessionExpiresAt` describes the ID token. Replace it:

```go
	// SessionExpiresAt is when this session ends: the earlier of its idle and
	// absolute bounds, both of which tflive owns. It lets the web client
	// re-authenticate at a quiet moment instead of being interrupted by a 401.
	// It is not a control: the API rejects an expired session regardless of
	// what the browser believes.
	SessionExpiresAt string `json:"sessionExpiresAt,omitempty"`
```

No code change — `MeFromPrincipal` already reads `principal.ExpiresAt`, which Task 4 now populates from the session.

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/api/ ./internal/auth/ -v 2>&1 | tail -20`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/api/auth.go internal/api/auth_test.go internal/auth/me.go
git commit -m "feat(api): revoke the session row on logout"
```

---

### Task 6: Verify back-channel logout tokens

**Files:**
- Create: `internal/authn/logout_token.go`
- Test: `internal/authn/logout_token_test.go`

**Interfaces:**
- Consumes: the `*OIDCVerifier`'s existing JWKS and discovery state.
- Produces:
  - `type LogoutToken struct { Subject, SessionID string }`
  - `func (v *OIDCVerifier) VerifyLogoutToken(ctx context.Context, raw string) (LogoutToken, error)`
  - `var ErrInvalidLogoutToken = errors.New("invalid logout token")`
  - `const backchannelLogoutEvent = "http://schemas.openid.net/event/backchannel-logout"`

- [ ] **Step 1: Write the failing test**

Create `internal/authn/logout_token_test.go`. Reuse the signing helpers already in `oidc_verifier_test.go` — the same key, issuer, and audience the verifier tests use.

```go
package authn

import (
	"context"
	"testing"
	"time"
)

func TestVerifyLogoutTokenAcceptsAWellFormedToken(t *testing.T) {
	verifier := newTestVerifier(t)
	raw := signLogoutToken(t, map[string]any{
		"sub":    "user-1",
		"sid":    "idp-sid-1",
		"events": map[string]any{backchannelLogoutEvent: map[string]any{}},
	})

	got, err := verifier.VerifyLogoutToken(context.Background(), raw)
	if err != nil {
		t.Fatalf("VerifyLogoutToken: %v", err)
	}
	if got.Subject != "user-1" || got.SessionID != "idp-sid-1" {
		t.Fatalf("got %+v, want sub=user-1 sid=idp-sid-1", got)
	}
}

func TestVerifyLogoutTokenRejections(t *testing.T) {
	verifier := newTestVerifier(t)
	validEvents := map[string]any{backchannelLogoutEvent: map[string]any{}}

	tests := map[string]map[string]any{
		"no events claim": {
			"sub": "user-1", "sid": "idp-sid-1",
		},
		"wrong event": {
			"sub": "user-1", "sid": "idp-sid-1",
			"events": map[string]any{"http://example.test/other": map[string]any{}},
		},
		"neither sub nor sid": {
			"events": validEvents,
		},
		"carries a nonce": {
			// OIDC Back-Channel Logout 1.0 §2.4 forbids nonce. Its presence
			// means an ID token is being replayed as a logout token, which
			// would let anyone holding one revoke sessions.
			"sub": "user-1", "sid": "idp-sid-1", "events": validEvents, "nonce": "n-1",
		},
		"stale iat": {
			"sub": "user-1", "sid": "idp-sid-1", "events": validEvents,
			"iat": time.Now().Add(-10 * time.Minute).Unix(),
		},
	}

	for name, claims := range tests {
		t.Run(name, func(t *testing.T) {
			raw := signLogoutToken(t, claims)
			if _, err := verifier.VerifyLogoutToken(context.Background(), raw); err == nil {
				t.Fatal("want an error, got none")
			}
		})
	}
}

func TestVerifyLogoutTokenRejectsAForeignSignature(t *testing.T) {
	verifier := newTestVerifier(t)
	raw := signLogoutTokenWithForeignKey(t, map[string]any{
		"sub": "user-1", "sid": "idp-sid-1",
		"events": map[string]any{backchannelLogoutEvent: map[string]any{}},
	})
	if _, err := verifier.VerifyLogoutToken(context.Background(), raw); err == nil {
		t.Fatal("a token signed by an unknown key was accepted")
	}
}
```

> `newTestVerifier`, `signLogoutToken`, and `signLogoutTokenWithForeignKey` follow the construction already in `oidc_verifier_test.go`. `signLogoutToken` fills in `iss`, `aud`, and a fresh `iat` unless the claims map overrides them; `signLogoutTokenWithForeignKey` signs with a second key not in the JWKS.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/authn/ -run TestVerifyLogoutToken -v`
Expected: FAIL — `verifier.VerifyLogoutToken undefined`.

- [ ] **Step 3a: Extract the shared signature check**

`Verify` currently does two jobs in one function: verify the signature against the JWKS (with a
refresh-and-retry on an unknown key id), then enforce ID-token claims. A logout token needs the
first half and not the second, so split them — one place trusts the provider's keys, and neither
caller can skip it.

In `internal/authn/oidc_verifier.go`, add:

```go
// verifiedPayload checks a compact JWS against the provider's keys and returns
// the verified payload. It says nothing about the claims inside: ID tokens and
// back-channel logout tokens require different ones, and both need exactly this
// signature check first.
func (v *OIDCVerifier) verifiedPayload(ctx context.Context, raw string) ([]byte, error) {
	if len(raw) == 0 || len(raw) > maxTokenBytes || strings.Count(raw, ".") != 2 {
		return nil, ErrInvalidToken
	}
	header, err := protectedHeader(raw)
	if err != nil {
		return nil, ErrInvalidToken
	}
	algorithm, keyID, ok := allowedHeader(header)
	if !ok {
		return nil, ErrInvalidToken
	}
	key, err := v.keyFor(ctx, keyID, algorithm)
	if err != nil {
		return nil, err
	}
	payload, err := jws.Verify([]byte(raw), jws.WithKey(algorithm, key), jws.WithCompact())
	if err != nil {
		return v.payloadAfterSignatureFailure(ctx, raw, keyID, algorithm)
	}
	return payload, nil
}
```

Rename `retryAfterSignatureFailure` to `payloadAfterSignatureFailure` and change it to return
`([]byte, error)`: every `return VerifiedToken{}, X` becomes `return nil, X`, and its final
`return v.validatedToken(payload)` becomes `return payload, nil`.

`Verify` then becomes:

```go
func (v *OIDCVerifier) Verify(ctx context.Context, raw string) (VerifiedToken, error) {
	payload, err := v.verifiedPayload(ctx, raw)
	if err != nil {
		return VerifiedToken{}, err
	}
	return v.validatedToken(payload)
}
```

Run `go test ./internal/authn/ -v` and confirm the existing verifier tests still pass — this step
is a pure refactor and must change no behaviour.

- [ ] **Step 3b: Write the implementation**

Create `internal/authn/logout_token.go`:

```go
package authn

import (
	"context"
	"errors"
	"time"

	"github.com/lestrrat-go/jwx/v3/jwt"
)

// backchannelLogoutEvent is the event identifier OIDC Back-Channel Logout 1.0
// §2.4 requires in the events claim.
const backchannelLogoutEvent = "http://schemas.openid.net/event/backchannel-logout"

var ErrInvalidLogoutToken = errors.New("invalid logout token")

// LogoutToken is a verified back-channel logout notification. At least one of
// its two fields is set.
type LogoutToken struct {
	Subject   string
	SessionID string
}

// VerifyLogoutToken authenticates a back-channel logout notification from the
// IdP. It reuses the JWKS and issuer the ID-token verifier already maintains,
// so there is one place a provider's signing keys are trusted.
func (v *OIDCVerifier) VerifyLogoutToken(ctx context.Context, raw string) (LogoutToken, error) {
	// verifiedPayload is the shared signature check extracted in Step 3a. It
	// must NOT be v.Verify: that also enforces ID-token claims (exp, name,
	// preferred_username, azp), none of which a logout token carries, so every
	// valid logout token would be rejected — and swallowing that rejection to
	// work around it would swallow signature failures too, since both return
	// ErrInvalidToken.
	payload, err := v.verifiedPayload(ctx, raw)
	if err != nil {
		return LogoutToken{}, err
	}

	v.mu.RLock()
	issuer := v.discovery.Issuer
	v.mu.RUnlock()

	token, err := jwt.Parse(payload,
		// The signature is already verified above; this parses claims from the
		// verified payload.
		jwt.WithVerify(false),
		jwt.WithIssuer(issuer),
		jwt.WithAudience(v.cfg.Audience),
		jwt.WithRequiredClaim("events"),
		jwt.WithRequiredClaim("iat"),
		jwt.WithClock(jwt.ClockFunc(v.cfg.Clock)),
		jwt.WithAcceptableSkew(clockSkew),
	)
	if err != nil {
		return LogoutToken{}, ErrInvalidLogoutToken
	}

	// §2.4: a logout token MUST NOT contain a nonce. Its presence means an ID
	// token is being replayed here, which would let anyone holding one revoke
	// another user's sessions.
	if token.Has("nonce") {
		return LogoutToken{}, ErrInvalidLogoutToken
	}

	// §2.4: iat is required, and a logout notification is only meaningful
	// while it is fresh.
	issuedAt, ok := token.IssuedAt()
	if !ok || v.cfg.Clock().Sub(issuedAt) > logoutTokenMaxAge {
		return LogoutToken{}, ErrInvalidLogoutToken
	}

	var events map[string]any
	if err := token.Get("events", &events); err != nil {
		return LogoutToken{}, ErrInvalidLogoutToken
	}
	if _, ok := events[backchannelLogoutEvent]; !ok {
		return LogoutToken{}, ErrInvalidLogoutToken
	}

	subject, _ := token.Subject()
	sessionID, ok := optionalStringClaim(token, "sid")
	if !ok {
		return LogoutToken{}, ErrInvalidLogoutToken
	}
	// §2.4: at least one of sub and sid must be present, or there is nothing
	// to revoke.
	if subject == "" && sessionID == "" {
		return LogoutToken{}, ErrInvalidLogoutToken
	}

	return LogoutToken{Subject: subject, SessionID: sessionID}, nil
}

// logoutTokenMaxAge bounds replay of a captured logout token.
const logoutTokenMaxAge = 2 * time.Minute
```

> **Why the split in Step 3a matters:** without it there is no way to check a logout token's
> signature except `Verify`, which also demands ID-token claims a logout token does not have. Working
> around that by ignoring `ErrInvalidToken` would ignore signature failures too — they return the
> same error — leaving the endpoint accepting forged logout tokens from anyone who can reach it.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/authn/ -run TestVerifyLogoutToken -v`
Expected: PASS, all subtests.

- [ ] **Step 5: Commit**

```bash
git add internal/authn/logout_token.go internal/authn/logout_token_test.go internal/authn/oidc_verifier.go
git commit -m "feat(authn): verify back-channel logout tokens"
```

---

### Task 7: The back-channel logout endpoint, and the client registration that feeds it

**Files:**
- Create: `internal/api/backchannel_logout.go`
- Modify: `internal/api/server.go` (route + public path)
- Modify: `internal/keycloak/provisioner.go`
- Test: `internal/api/backchannel_logout_test.go`, `internal/keycloak/provisioner_test.go`

**Interfaces:**
- Consumes: `VerifyLogoutToken` (Task 6); `RevokeSessionsByIDPSessionID`, `RevokeSessionsBySubject` (Tasks 1–2).
- Produces: `POST /v1/auth/backchannel-logout`; `AuthConfig.LogoutTokenVerifier` (an interface with `VerifyLogoutToken`, so handler tests need no live IdP).

- [ ] **Step 1: Write the failing test**

Create `internal/api/backchannel_logout_test.go`:

```go
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
```

> Add `fakeLogoutVerifier` (returns a fixed `authn.LogoutToken` or error) and `withLogoutTokenVerifier` to the harness, and extend `fakeSessionStore` with `revokedBySID` / `revokedBySubject` counters.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/api/ -run TestBackchannelLogout -v`
Expected: FAIL — `withLogoutTokenVerifier undefined`, and the route 404s.

- [ ] **Step 3: Write the handler**

Create `internal/api/backchannel_logout.go`:

```go
package api

import (
	"context"
	"log"
	"net/http"

	"github.com/vishu42/tflive/internal/authn"
)

// LogoutTokenVerifier authenticates a back-channel logout notification.
// *authn.OIDCVerifier satisfies it; the interface exists so handler tests need
// no live IdP.
type LogoutTokenVerifier interface {
	VerifyLogoutToken(ctx context.Context, raw string) (authn.LogoutToken, error)
}

// handleBackchannelLogout ends sessions on the IdP's instruction.
//
// It is unauthenticated by necessity: the notification arrives from the
// provider's server, which holds no tflive cookie and no bearer token. The
// logout token is the credential, and it is verified against the same JWKS
// that verifies ID tokens.
//
// Without this endpoint, disabling a user at the IdP would not reach tflive
// until their session hit its own expiry, because tflive stops consulting the
// provider once a session exists.
func (server *Server) handleBackchannelLogout(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "no-store")

	if err := request.ParseForm(); err != nil {
		http.Error(response, "invalid request", http.StatusBadRequest)
		return
	}
	raw := request.PostFormValue("logout_token")
	if raw == "" {
		http.Error(response, "invalid request", http.StatusBadRequest)
		return
	}

	token, err := server.auth.LogoutTokenVerifier.VerifyLogoutToken(request.Context(), raw)
	if err != nil {
		log.Printf("backchannel logout: token rejected: %v", err)
		http.Error(response, "invalid request", http.StatusBadRequest)
		return
	}

	now := server.now()
	// sid identifies one browser session; sub identifies the person. Prefer
	// the narrower one, so a provider that signs one device out does not sign
	// the user out everywhere.
	var revoked int
	if token.SessionID != "" {
		revoked, err = server.auth.Sessions.RevokeSessionsByIDPSessionID(request.Context(), token.SessionID, now)
	} else {
		revoked, err = server.auth.Sessions.RevokeSessionsBySubject(request.Context(), token.Subject, now)
	}
	if err != nil {
		log.Printf("backchannel logout: revoke failed: %v", err)
		http.Error(response, "revocation failed", http.StatusInternalServerError)
		return
	}

	// 200 whether or not anything matched. Whether tflive holds a session for
	// a given sid is not something an unauthenticated caller gets to learn.
	log.Printf("backchannel logout: revoked %d session(s)", revoked)
	response.WriteHeader(http.StatusOK)
}
```

In `internal/api/server.go`, add `LogoutTokenVerifier LogoutTokenVerifier` to `AuthConfig`, register the route beside the other auth routes:

```go
		server.mux.HandleFunc("POST /v1/auth/backchannel-logout", server.handleBackchannelLogout)
```

and add `"/v1/auth/backchannel-logout"` to the public-paths list passed to `RequireAuthentication`.

- [ ] **Step 4: Register the URI in the provisioner**

In `internal/keycloak/provisioner.go`, where the `tflive-api` client is built, add to its attributes:

```go
	// Keycloak posts the logout notification here when a session it owns ends.
	// session.required makes it include sid, in both the ID token and the
	// logout token, which is what lets one device be signed out instead of
	// every session the user has.
	"backchannel.logout.url":                     publicURL + "/v1/auth/backchannel-logout",
	"backchannel.logout.session.required":        "true",
	"backchannel.logout.revoke.offline.tokens":   "false",
```

Add a provisioner test asserting both attributes appear on the client payload, modelled on the existing assertions for redirect URIs in `internal/keycloak/provisioner_test.go`.

- [ ] **Step 5: Run tests to verify they pass**

Run:
```bash
go build ./...
go test ./internal/api/ ./internal/keycloak/ -v 2>&1 | tail -20
```
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/api/backchannel_logout.go internal/api/backchannel_logout_test.go internal/api/server.go internal/keycloak
git commit -m "feat(api): accept OIDC back-channel logout"
```

---

### Task 8: Bound the web client's re-auth deferral

Closes review finding #5. With sessions now lasting hours, proactive re-auth is rare — but the deferral loop still has no deadline, and the 401 path still navigates without checking for unsaved work.

**Files:**
- Modify: `web/src/auth/SessionProvider.tsx`
- Test: `web/src/auth/SessionProvider.test.tsx`

**Interfaces:**
- Consumes: `me.sessionExpiresAt`, now the session's expiry rather than the token's.
- Produces: `REAUTH_DEFER_LIMIT_MS`.

- [ ] **Step 1: Write the failing test**

Add to `web/src/auth/SessionProvider.test.tsx`:

```tsx
it("stops deferring re-authentication once the deferral limit is reached", async () => {
  vi.useFakeTimers();
  const assign = vi.fn();
  vi.stubGlobal("location", { assign, pathname: "/stacks", search: "" });

  // A form that never stops claiming unsaved work.
  const guard = document.createElement("div");
  guard.setAttribute("data-unsaved", "true");
  document.body.append(guard);

  renderSessionProvider({
    me: { ...baseMe, sessionExpiresAt: new Date(Date.now() + 61_000).toISOString() },
  });

  // Reach the re-auth moment, then hold past the deferral limit.
  await vi.advanceTimersByTimeAsync(1_000 + REAUTH_DEFER_LIMIT_MS + REAUTH_RETRY_MS);

  expect(assign).toHaveBeenCalledTimes(1);

  guard.remove();
  vi.useRealTimers();
});

it("does not defer forever while unsaved work is present but the limit has not passed", async () => {
  vi.useFakeTimers();
  const assign = vi.fn();
  vi.stubGlobal("location", { assign, pathname: "/stacks", search: "" });

  const guard = document.createElement("div");
  guard.setAttribute("data-unsaved", "true");
  document.body.append(guard);

  renderSessionProvider({
    me: { ...baseMe, sessionExpiresAt: new Date(Date.now() + 61_000).toISOString() },
  });

  await vi.advanceTimersByTimeAsync(1_000 + REAUTH_RETRY_MS * 2);
  expect(assign).not.toHaveBeenCalled();

  guard.remove();
  vi.useRealTimers();
});
```

> `renderSessionProvider` and `baseMe` follow the harness already in that file. Export `REAUTH_DEFER_LIMIT_MS` and `REAUTH_RETRY_MS` from `SessionProvider.tsx` so the test does not restate the numbers.

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd web && npx vitest run src/auth/SessionProvider.test.tsx`
Expected: FAIL — `REAUTH_DEFER_LIMIT_MS` is not exported, and the first test times out because the deferral never ends.

- [ ] **Step 3: Add the deadline**

In `web/src/auth/SessionProvider.tsx`:

```tsx
// How long re-authentication may be deferred while work is in flight. Past
// this, the session is closer to expiring than the deferral is to helping:
// waiting longer only guarantees the 401 path takes the same navigation with
// no warning at all.
export const REAUTH_DEFER_LIMIT_MS = 120_000;
export const REAUTH_RETRY_MS = 5_000;
```

and in the expiry effect:

```tsx
    let timer: ReturnType<typeof setTimeout>;
    let deferringSince: number | null = null;

    const attempt = () => {
      const busy = pendingMutationsRef.current > 0 || document.querySelector("[data-unsaved='true']");
      if (busy) {
        deferringSince ??= Date.now();
        if (Date.now() - deferringSince < REAUTH_DEFER_LIMIT_MS) {
          timer = setTimeout(attempt, REAUTH_RETRY_MS);
          return;
        }
      }
      login();
    };
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd web && npx vitest run src/auth`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/auth/SessionProvider.tsx web/src/auth/SessionProvider.test.tsx
git commit -m "fix(web): bound how long re-authentication may be deferred"
```

---

### Task 9: Documentation, configuration, and live-stack verification

**Files:**
- Modify: `docs/authentication.md`, `.env.example`, `README.md`

- [ ] **Step 1: Update `.env.example`**

Add after the `SESSION_ENCRYPTION_KEY` block:

```bash
# Optional. How long a signed-in session lasts, and how long it survives
# without a request. tflive owns both: they are deliberately independent of the
# IdP's token lifespan and SSO idle timeout, so sessions behave the same
# whichever provider a deployment brings.
# Defaults: 8h absolute, 1h idle. Idle must not exceed absolute.
# TFLIVE_SESSION_ABSOLUTE_TTL=8h
# TFLIVE_SESSION_IDLE_TTL=1h
```

While here, fix the two drifts found on 2026-08-29: add `TFLIVE_DEBUG` (read at `internal/config/config.go:96`, currently undocumented) and change `OIDC_CLIENT_SECRET`'s placeholder from `replace-me-with-a-local-only-secret` to `tflive-api-local-only`, which is the compose fallback the provisioner and API both default to — the mismatch makes a verbatim copy of this file fail token exchange with `invalid_client`.

- [ ] **Step 2: Rewrite the session section of `docs/authentication.md`**

The sections describing the session cookie as the ID token are now wrong. Replace them with: the session is tflive's own record; the cookie is an opaque reference; the two bounds and their defaults; that the ID token is kept encrypted for `id_token_hint`; that claims are copied at sign-in and are therefore stale until the session ends or back-channel logout arrives; the back-channel logout endpoint and the two Keycloak client attributes a BYO IdP must set; and that a provider without back-channel logout degrades to the absolute cap.

- [ ] **Step 3: Note the BYO-IdP requirement in `README.md`**

One paragraph in the auth section: tflive requires no session or timeout configuration on the IdP. To get immediate revocation, point the provider's back-channel logout at `<TFLIVE_PUBLIC_URL>/v1/auth/backchannel-logout` and enable session-required so `sid` is included. Without it, sessions still end at their own bounds.

- [ ] **Step 4: Run the full suite**

Run:
```bash
go build ./... && go test ./... 2>&1 | tail -20
cd web && npx vitest run 2>&1 | tail -5
```
Expected: all green.

- [ ] **Step 5: Verify on the live stack**

This is the acceptance gate from the spec. Do not mark the plan complete without it.

```bash
docker compose -f docker-compose.yaml -f docker-compose.app.yaml up -d --build
```

1. Sign in at `http://localhost:5173`. `/v1/me` returns an identity.
2. One row exists and the token is not readable:
   ```bash
   docker compose exec -T postgres psql -U tflive -d tflive_test \
     -c "select subject, idp_session_id, absolute_expires_at, revoked_at from sessions;" \
     -c "select left(id_token_ciphertext, 40) from sessions;"
   ```
3. **The point of the change.** Set the realm's `accessTokenLifespan` to 60, wait two minutes, and confirm the session still works — this is what fails today:
   ```bash
   TOKEN=$(curl -s -X POST http://localhost:8082/realms/master/protocol/openid-connect/token \
     -d grant_type=password -d client_id=admin-cli \
     -d username=tflive-admin -d password=tflive-admin-local-only \
     | python3 -c 'import sys,json;print(json.load(sys.stdin)["access_token"])')
   curl -s -X PUT -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
     -d '{"accessTokenLifespan":60}' http://localhost:8082/admin/realms/tflive
   ```
4. Sign out from Keycloak's account console at `http://keycloak.localhost:8082/realms/tflive/account`, then confirm `revoked_at` is set and the next tflive request 401s.
5. Restart the API with `TFLIVE_SESSION_IDLE_TTL=60s` and confirm an idle session dies at 60s.

- [ ] **Step 6: Commit**

```bash
git add docs .env.example README.md
git commit -m "docs: describe the app-owned session and back-channel logout"
```

---

## Self-Review

**Spec coverage.** Every section of the design maps to a task: the session record and lifetime policy to Tasks 1 and 3; server-side records, hashing, and the `sessions` table to Task 2; the cookie path and the untouched Bearer path to Task 4; RP-initiated logout keeping `id_token_hint` to Task 5; back-channel logout verification and endpoint to Tasks 6 and 7; the provisioner registration to Task 7; the frontend deferral to Task 8; documentation and the five live-stack checks to Task 9. `warnOnOversizedSession`'s deletion is in Task 3.

**Known gap, deliberate.** The spec lists no expiry sweep for rows past `absolute_expires_at`. They are dead to `IsLive` and cannot authenticate anyone, so this is disk usage rather than a correctness matter. The index `sessions_absolute_expires_at_idx` exists so a sweep is a one-line `delete` when it is wanted; a scheduled job for a pre-production system with no users is YAGNI.

**Type consistency.** `IDHash` names the hash in the `Session` struct, the migration column, and every repository method. `IDPSessionID` names the `sid` claim on `Session`; the same value is `SessionID` on `VerifiedToken` and `LogoutToken`, matching the claim name at the token boundary and the column name at the storage boundary. `SessionStore` is the interface throughout. `authn.ErrSessionNotFound` is the only not-found error the cookie path treats as ordinary.

**Resolved during the pre-flight scan (2026-08-29).** Two defects were found in this plan and corrected before execution:

1. Task 6 originally called `Verify` for the logout token's signature check and swallowed `ErrInvalidToken`. `Verify` also enforces ID-token claims (`exp`, `name`, `preferred_username`, `azp`) that a logout token does not carry, so every valid logout token would have been rejected — and swallowing that error would have swallowed signature failures, which return the same error, leaving the endpoint open to forged tokens. Task 6 now has Step 3a extracting `verifiedPayload` as the one place the provider's keys are trusted, with both entry points built on it.
2. Task 2's tests called `openTestPool`, which creates an empty schema and applies no migrations, so `sessions` would not have existed. They now call `openMigratedTestPool`.
