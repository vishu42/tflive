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

// IsLive reports whether a session may still authenticate a request.
// Revocation is unconditional and so is checked first: a revoked session is
// dead whatever the clock says, and back-channel logout depends on that being
// true the instant the row is marked.
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
