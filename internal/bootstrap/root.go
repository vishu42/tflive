// Package bootstrap holds the reconciliation a fresh install needs before it
// can be administered at all.
package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/vishu42/tflive/internal/authn"
	"github.com/vishu42/tflive/internal/authz"
)

const (
	// DefaultRootSubject is the sub the root account is seeded under, and the
	// string that becomes user:<sub> in its OpenFGA tuple.
	//
	// No colon, deliberately: the natural-looking "local:root" is refused by
	// authz's tuple-token rules, so it would seed an account that signs in and
	// can be granted nothing. It is a constant rather than configuration
	// because a changed sub does not move the old tuple — it strands it, and
	// leaves a second root behind.
	DefaultRootSubject = "local_root"
	// DefaultRootUsername is what root types at the sign-in form.
	DefaultRootUsername = "root"
)

// RootConfig is the root account an install is reconciled against.
type RootConfig struct {
	// Username defaults to DefaultRootUsername.
	Username string
	// Password is required only when the account does not yet exist.
	Password string
	// Subject defaults to DefaultRootSubject.
	Subject string
}

// Accounts is the account store SeedRoot reconciles. *postgres.Store satisfies
// it.
type Accounts interface {
	LocalAccountByUsername(ctx context.Context, username string) (authn.LocalAccount, error)
	EnsureLocalAccount(ctx context.Context, account authn.LocalAccount, now time.Time) (bool, error)
}

// SeedRoot makes a fresh install administrable: a local root account, and the
// {user:<sub>, root, platform:tflive} tuple that gives it authority.
//
// Root is not a bypass and not a special code path. It is an ordinary local
// account plus an ordinary tuple, so every authorization question about it is
// still answered by OpenFGA. What makes it root is the relation, and `root`
// sits outside the grantable set (internal/authz/relations.go), so the grant
// API cannot revoke it — stated honestly rather than offered as a delete the
// next boot would silently undo.
//
// Add-only, and the two halves reconcile independently. A boot that wrote the
// account and then failed before the tuple completes on the next one, and an
// operator who has rotated the root password does not have it reset from
// configuration on every restart — which would look like the rotation worked
// until then.
//
// Every failure stops the boot. Running with no reachable administrator is the
// worse outcome, and it is silent: every route answers 403 and nothing says why.
func SeedRoot(
	ctx context.Context,
	accounts Accounts,
	authorizer authz.Authorizer,
	config RootConfig,
	now func() time.Time,
) error {
	username := authn.FoldUsername(config.Username)
	if username == "" {
		username = DefaultRootUsername
	}
	subject := config.Subject
	if subject == "" {
		subject = DefaultRootSubject
	}

	// Built before anything is written. This is the constraint that would
	// otherwise be discovered at the tuple write, after the account row was
	// already committed.
	rootSubject, err := authz.SubjectFromOIDCSub(subject)
	if err != nil {
		return fmt.Errorf("seed root: %w", err)
	}

	if err := ensureRootAccount(ctx, accounts, config, username, subject, now); err != nil {
		return err
	}
	return ensureRootTuple(ctx, authorizer, rootSubject)
}

// ensureRootAccount creates the account when it is absent and leaves it alone
// when it is not.
//
// The lookup comes first so the password is hashed only when it will be used.
// Hashing costs argon2id's full memory and time, and reconciling runs at every
// boot, so hashing unconditionally would put that cost on every restart to
// produce a value that is then discarded.
func ensureRootAccount(
	ctx context.Context,
	accounts Accounts,
	config RootConfig,
	username, subject string,
	now func() time.Time,
) error {
	_, err := accounts.LocalAccountByUsername(ctx, username)
	switch {
	case err == nil:
		return nil
	case !errors.Is(err, authn.ErrLocalAccountNotFound):
		return fmt.Errorf("seed root: look up root account: %w", err)
	}

	if config.Password == "" {
		return fmt.Errorf("seed root: a root password is required to create the root account")
	}
	hash, err := authn.HashPassword(config.Password)
	if err != nil {
		return fmt.Errorf("seed root: hash root password: %w", err)
	}
	if _, err := accounts.EnsureLocalAccount(ctx, authn.LocalAccount{
		Subject:      subject,
		Username:     username,
		PasswordHash: hash,
		// Root is an operator identity, not a person, and has no mailbox. The
		// projection renders DisplayName, so it reads as "root" in a grants
		// list rather than as a bare sub.
		DisplayName: DefaultRootUsername,
	}, now()); err != nil {
		return fmt.Errorf("seed root: create root account: %w", err)
	}
	return nil
}

// ensureRootTuple writes the root relationship when it is absent.
//
// Checked before written so the reconcile is genuinely add-only rather than a
// write that happens to be idempotent — and so a boot against a store that
// already has it does no write at all.
//
// NewStructuralRelationship rather than NewGrant: `root` is not grantable, and
// that refusal is the only thing standing between the grant API and this
// tuple, so seeding goes through the separate door instead of the refusal
// being relaxed.
func ensureRootTuple(ctx context.Context, authorizer authz.Authorizer, rootSubject authz.Subject) error {
	request := authz.CheckRequest{
		Subject:  rootSubject,
		Relation: authz.RelationRoot,
		Object:   authz.Platform,
	}
	result, err := authorizer.Check(ctx, request)
	if err != nil {
		return fmt.Errorf("seed root: check root relationship: %w", err)
	}
	if result.Allowed {
		return nil
	}

	relationship, err := authz.NewStructuralRelationship(rootSubject, authz.Platform, authz.RelationRoot)
	if err != nil {
		return fmt.Errorf("seed root: build root relationship: %w", err)
	}
	mutation, err := authz.NewMutation([]authz.Grant{relationship}, false)
	if err != nil {
		return fmt.Errorf("seed root: build root mutation: %w", err)
	}
	if err := authorizer.WriteRelationships(ctx, mutation); err != nil {
		return fmt.Errorf("seed root: write root relationship: %w", err)
	}
	return nil
}
