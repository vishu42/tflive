package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/vishu42/tflive/internal/authn"
)

// LocalAccountByUsername reads one account by its folded username.
func (store *Store) LocalAccountByUsername(ctx context.Context, username string) (authn.LocalAccount, error) {
	var account authn.LocalAccount
	err := store.pool.QueryRow(ctx, `
		select sub, username, password_hash, display_name, email
		from local_accounts
		where username = $1
	`, authn.FoldUsername(username)).Scan(
		&account.Subject,
		&account.Username,
		&account.PasswordHash,
		&account.DisplayName,
		&account.Email,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return authn.LocalAccount{}, authn.ErrLocalAccountNotFound
	}
	if err != nil {
		return authn.LocalAccount{}, fmt.Errorf("local account by username: %w", err)
	}
	return account, nil
}

// LocalAccountBySubject reads one account by its sub.
//
// The counterpart to LocalAccountByUsername, for the callers whose subject is
// the stable identity and whose username is not. Root is the one that matters:
// its sub is fixed by bootstrap, its username is configurable, so asking "does
// root exist" by username answers a different question after a rename.
func (store *Store) LocalAccountBySubject(ctx context.Context, subject string) (authn.LocalAccount, error) {
	var account authn.LocalAccount
	err := store.pool.QueryRow(ctx, `
		select sub, username, password_hash, display_name, email
		from local_accounts
		where sub = $1
	`, subject).Scan(
		&account.Subject,
		&account.Username,
		&account.PasswordHash,
		&account.DisplayName,
		&account.Email,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return authn.LocalAccount{}, authn.ErrLocalAccountNotFound
	}
	if err != nil {
		return authn.LocalAccount{}, fmt.Errorf("local account by subject: %w", err)
	}
	return account, nil
}

// EnsureLocalAccount inserts the account if its username is free, and reports
// whether it did.
//
// Add-only, deliberately. #212 reconciles the root account at every boot, and
// an upsert there would silently reset a password the operator had rotated —
// and would do it on every restart, so the rotation would look like it worked
// until the next one. Changing an existing account is a separate, deliberate
// operation.
//
// A taken sub is a conflict too, not just a taken username: the sub is what
// becomes an OpenFGA subject, so two accounts sharing one would share every
// grant. The primary key refuses it, and that refusal surfaces as an error
// rather than as created=false, because unlike a username collision it means
// the caller asked for something incoherent rather than something already done.
func (store *Store) EnsureLocalAccount(ctx context.Context, account authn.LocalAccount, now time.Time) (bool, error) {
	tag, err := store.pool.Exec(ctx, `
		insert into local_accounts (
			sub, username, password_hash, display_name, email, created_at, updated_at
		) values ($1, $2, $3, $4, $5, $6, $6)
		on conflict (username) do nothing
	`,
		account.Subject,
		authn.FoldUsername(account.Username),
		account.PasswordHash,
		account.DisplayName,
		account.Email,
		now,
	)
	if err != nil {
		return false, fmt.Errorf("ensure local account: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}
