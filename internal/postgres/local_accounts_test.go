package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/vishu42/tflive/internal/authn"
)

func newLocalAccountTestStore(t *testing.T, ctx context.Context) *Store {
	t.Helper()
	return NewStore(openMigratedTestPool(t, ctx))
}

func testLocalAccount(t *testing.T) authn.LocalAccount {
	t.Helper()

	hash, err := authn.HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	return authn.LocalAccount{
		Subject:      "local_root",
		Username:     "root",
		PasswordHash: hash,
		DisplayName:  "Root",
		Email:        "root@tflive.local",
	}
}

func TestLocalAccountByUsernameReturnsTheStoredAccount(t *testing.T) {
	ctx := context.Background()
	store := newLocalAccountTestStore(t, ctx)
	account := testLocalAccount(t)

	created, err := store.EnsureLocalAccount(ctx, account, time.Now().UTC())
	if err != nil {
		t.Fatalf("EnsureLocalAccount returned error: %v", err)
	}
	if !created {
		t.Fatal("EnsureLocalAccount reported no insert on an empty table")
	}

	found, err := store.LocalAccountByUsername(ctx, "root")
	if err != nil {
		t.Fatalf("LocalAccountByUsername returned error: %v", err)
	}
	if found != account {
		t.Fatalf("LocalAccountByUsername = %+v, want %+v", found, account)
	}
}

func TestLocalAccountByUsernameReportsAMissingAccount(t *testing.T) {
	ctx := context.Background()
	store := newLocalAccountTestStore(t, ctx)

	_, err := store.LocalAccountByUsername(ctx, "nobody")
	if !errors.Is(err, authn.ErrLocalAccountNotFound) {
		t.Fatalf("LocalAccountByUsername error = %v, want ErrLocalAccountNotFound", err)
	}
}

// Usernames are matched case-insensitively, so someone who registered "root"
// can sign in typing "Root". Storing the fold rather than comparing with ilike
// keeps the unique constraint and the lookup agreeing on what a duplicate is.
func TestLocalAccountByUsernameIsCaseInsensitive(t *testing.T) {
	ctx := context.Background()
	store := newLocalAccountTestStore(t, ctx)
	account := testLocalAccount(t)
	account.Username = "Root"

	if _, err := store.EnsureLocalAccount(ctx, account, time.Now().UTC()); err != nil {
		t.Fatalf("EnsureLocalAccount returned error: %v", err)
	}

	found, err := store.LocalAccountByUsername(ctx, "ROOT")
	if err != nil {
		t.Fatalf("LocalAccountByUsername returned error: %v", err)
	}
	if found.Username != "root" {
		t.Fatalf("Username = %q, want the folded %q", found.Username, "root")
	}
}

// #212 reconciles the root account at every boot, add-only. A second Ensure
// must not overwrite a password the operator has since changed, and must say
// it wrote nothing.
func TestEnsureLocalAccountDoesNotOverwriteAnExistingAccount(t *testing.T) {
	ctx := context.Background()
	store := newLocalAccountTestStore(t, ctx)
	account := testLocalAccount(t)
	now := time.Now().UTC()

	if _, err := store.EnsureLocalAccount(ctx, account, now); err != nil {
		t.Fatalf("EnsureLocalAccount returned error: %v", err)
	}

	rotated := account
	rotated.PasswordHash = authn.DummyPasswordHash
	rotated.DisplayName = "Someone Else"
	created, err := store.EnsureLocalAccount(ctx, rotated, now.Add(time.Hour))
	if err != nil {
		t.Fatalf("second EnsureLocalAccount returned error: %v", err)
	}
	if created {
		t.Fatal("EnsureLocalAccount reported an insert over an existing account")
	}

	found, err := store.LocalAccountByUsername(ctx, "root")
	if err != nil {
		t.Fatalf("LocalAccountByUsername returned error: %v", err)
	}
	if found != account {
		t.Fatalf("account was overwritten: got %+v, want %+v", found, account)
	}
}

// The sub is what becomes user:<sub> in an OpenFGA tuple, so a second account
// may not claim one that is taken, even under a different username.
func TestEnsureLocalAccountRejectsADuplicateSubject(t *testing.T) {
	ctx := context.Background()
	store := newLocalAccountTestStore(t, ctx)
	account := testLocalAccount(t)
	now := time.Now().UTC()

	if _, err := store.EnsureLocalAccount(ctx, account, now); err != nil {
		t.Fatalf("EnsureLocalAccount returned error: %v", err)
	}

	other := account
	other.Username = "someone"
	created, err := store.EnsureLocalAccount(ctx, other, now)
	if err == nil && created {
		t.Fatal("EnsureLocalAccount inserted a second account on a taken subject")
	}
}

func TestLocalAccountBySubjectReturnsTheStoredAccount(t *testing.T) {
	ctx := context.Background()
	store := newLocalAccountTestStore(t, ctx)
	account := testLocalAccount(t)

	if _, err := store.EnsureLocalAccount(ctx, account, time.Now().UTC()); err != nil {
		t.Fatalf("EnsureLocalAccount returned error: %v", err)
	}

	found, err := store.LocalAccountBySubject(ctx, account.Subject)
	if err != nil {
		t.Fatalf("LocalAccountBySubject returned error: %v", err)
	}
	if found != account {
		t.Fatalf("LocalAccountBySubject = %+v, want %+v", found, account)
	}
}

// The lookup root seeding reconciles by. A renamed root still has to be found,
// which is exactly what looking it up by username stopped doing.
func TestLocalAccountBySubjectFindsARenamedAccount(t *testing.T) {
	ctx := context.Background()
	store := newLocalAccountTestStore(t, ctx)
	account := testLocalAccount(t)
	account.Username = "administrator"

	if _, err := store.EnsureLocalAccount(ctx, account, time.Now().UTC()); err != nil {
		t.Fatalf("EnsureLocalAccount returned error: %v", err)
	}

	found, err := store.LocalAccountBySubject(ctx, account.Subject)
	if err != nil {
		t.Fatalf("LocalAccountBySubject returned error: %v", err)
	}
	if found.Username != "administrator" {
		t.Fatalf("Username = %q, want administrator", found.Username)
	}
}

func TestLocalAccountBySubjectReportsAMissingAccount(t *testing.T) {
	ctx := context.Background()
	store := newLocalAccountTestStore(t, ctx)

	_, err := store.LocalAccountBySubject(ctx, "local_nobody")
	if !errors.Is(err, authn.ErrLocalAccountNotFound) {
		t.Fatalf("LocalAccountBySubject error = %v, want ErrLocalAccountNotFound", err)
	}
}
