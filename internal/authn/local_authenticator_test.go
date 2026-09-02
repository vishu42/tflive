package authn

import (
	"context"
	"errors"
	"testing"
)

type stubLocalAccountStore struct {
	account LocalAccount
	err     error
	queried []string
}

func (store *stubLocalAccountStore) LocalAccountByUsername(_ context.Context, username string) (LocalAccount, error) {
	store.queried = append(store.queried, username)
	if store.err != nil {
		return LocalAccount{}, store.err
	}
	return store.account, nil
}

func testStoredAccount(t *testing.T) LocalAccount {
	t.Helper()

	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	return LocalAccount{
		Subject:      "local_root",
		Username:     "root",
		PasswordHash: hash,
		DisplayName:  "Root",
		Email:        "root@tflive.local",
	}
}

func TestAuthenticateReturnsTheIdentityForCorrectCredentials(t *testing.T) {
	account := testStoredAccount(t)
	authenticator := NewLocalAuthenticator(&stubLocalAccountStore{account: account})

	identity, err := authenticator.Authenticate(context.Background(), "root", "correct horse battery staple")
	if err != nil {
		t.Fatalf("Authenticate returned error: %v", err)
	}
	want := Identity{
		Subject:           "local_root",
		DisplayName:       "Root",
		PreferredUsername: "root",
		Email:             "root@tflive.local",
	}
	if identity != want {
		t.Fatalf("Authenticate = %+v, want %+v", identity, want)
	}
}

func TestAuthenticateRejectsAWrongPassword(t *testing.T) {
	authenticator := NewLocalAuthenticator(&stubLocalAccountStore{account: testStoredAccount(t)})

	_, err := authenticator.Authenticate(context.Background(), "root", "wrong")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Authenticate error = %v, want ErrInvalidCredentials", err)
	}
}

// A wrong password and an unknown username must be the same answer. Anything
// that separates them is an account-enumeration oracle.
func TestAuthenticateReportsAnUnknownUsernameAsInvalidCredentials(t *testing.T) {
	authenticator := NewLocalAuthenticator(&stubLocalAccountStore{err: ErrLocalAccountNotFound})

	_, err := authenticator.Authenticate(context.Background(), "nobody", "whatever")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Authenticate error = %v, want ErrInvalidCredentials", err)
	}
}

// The equal error is not enough on its own: returning it without hashing makes
// the unknown-username path measurably faster, which enumerates accounts just
// as well. The dummy verify is what closes that, so assert it happens.
func TestAuthenticateHashesEvenWhenNoAccountMatches(t *testing.T) {
	authenticator := NewLocalAuthenticator(&stubLocalAccountStore{err: ErrLocalAccountNotFound})
	var verified []string
	authenticator.verify = func(encoded, _ string) bool {
		verified = append(verified, encoded)
		return false
	}

	if _, err := authenticator.Authenticate(context.Background(), "nobody", "whatever"); err == nil {
		t.Fatal("Authenticate accepted an unknown username")
	}
	if len(verified) != 1 || verified[0] != DummyPasswordHash {
		t.Fatalf("verified = %q, want exactly one call with DummyPasswordHash", verified)
	}
}

// A database outage is not a credential decision. Reporting it as invalid
// credentials would tell the user their password is wrong and hide the outage
// from anyone reading a 401 rate.
func TestAuthenticateDistinguishesAStoreFailureFromBadCredentials(t *testing.T) {
	outage := errors.New("connection refused")
	authenticator := NewLocalAuthenticator(&stubLocalAccountStore{err: outage})

	_, err := authenticator.Authenticate(context.Background(), "root", "whatever")
	if errors.Is(err, ErrInvalidCredentials) {
		t.Fatal("a store outage was reported as invalid credentials")
	}
	if !errors.Is(err, outage) {
		t.Fatalf("Authenticate error = %v, want it to wrap the store error", err)
	}
}

func TestAuthenticateFoldsTheUsernameBeforeLookup(t *testing.T) {
	store := &stubLocalAccountStore{account: testStoredAccount(t)}
	authenticator := NewLocalAuthenticator(store)

	if _, err := authenticator.Authenticate(context.Background(), "  ROOT ", "correct horse battery staple"); err != nil {
		t.Fatalf("Authenticate returned error: %v", err)
	}
	if len(store.queried) != 1 || store.queried[0] != "root" {
		t.Fatalf("queried = %q, want exactly one lookup for %q", store.queried, "root")
	}
}

func TestAuthenticateRejectsEmptyCredentialsWithoutAStoreLookup(t *testing.T) {
	for name, credentials := range map[string][2]string{
		"no username": {"", "password"},
		"no password": {"root", ""},
		"neither":     {"", ""},
	} {
		t.Run(name, func(t *testing.T) {
			store := &stubLocalAccountStore{account: testStoredAccount(t)}
			authenticator := NewLocalAuthenticator(store)

			_, err := authenticator.Authenticate(context.Background(), credentials[0], credentials[1])
			if !errors.Is(err, ErrInvalidCredentials) {
				t.Fatalf("Authenticate error = %v, want ErrInvalidCredentials", err)
			}
			if len(store.queried) != 0 {
				t.Fatalf("queried = %q, want no lookup for an empty credential", store.queried)
			}
		})
	}
}
