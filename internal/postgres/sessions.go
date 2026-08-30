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
