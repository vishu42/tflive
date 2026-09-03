package authn

import (
	"context"
	"errors"
	"fmt"
)

// ErrInvalidCredentials is the single answer to every failed local sign-in.
// It deliberately does not say whether the username exists.
var ErrInvalidCredentials = errors.New("invalid credentials")

// Identity is what a completed authentication asserts, independent of how it
// was proved. A local sign-in constructs one; the OIDC path arrives at the same
// facts by verifying an ID token. It is the input to the session row and to the
// users projection, and it carries no authorization: what this principal may do
// is answered by OpenFGA.
type Identity struct {
	Subject           string
	DisplayName       string
	PreferredUsername string
	Email             string
}

// LocalAuthenticator checks a username and password against the accounts
// tflive owns.
//
// It issues nothing. The caller takes the returned Identity through the same
// project-then-mint-a-session path the OIDC callback uses, which is what keeps
// local and federated sign-ins indistinguishable everywhere downstream.
type LocalAuthenticator struct {
	accounts LocalAccountStore
	// verify is VerifyPassword in production. It is a field so that the
	// equalisation below can be asserted in a test without measuring wall
	// clock, which would be flaky in CI and silently stop testing anything the
	// first time it was made lenient enough to pass.
	verify func(encoded, password string) bool
}

func NewLocalAuthenticator(accounts LocalAccountStore) *LocalAuthenticator {
	return &LocalAuthenticator{accounts: accounts, verify: VerifyPassword}
}

// Authenticate returns the identity behind a correct username and password.
//
// An unknown username is verified against DummyPasswordHash rather than
// returning early. Both paths therefore cost one argon2id run, so response time
// does not disclose which usernames exist — the equal error above would
// otherwise be undone by an unequal latency.
func (authenticator *LocalAuthenticator) Authenticate(ctx context.Context, username, password string) (Identity, error) {
	// Empty input is refused before the lookup. It reveals nothing about which
	// accounts exist, because the answer does not depend on any stored state.
	if FoldUsername(username) == "" || password == "" {
		return Identity{}, ErrInvalidCredentials
	}

	account, err := authenticator.accounts.LocalAccountByUsername(ctx, FoldUsername(username))
	switch {
	case errors.Is(err, ErrLocalAccountNotFound):
		authenticator.verify(DummyPasswordHash, password)
		return Identity{}, ErrInvalidCredentials
	case err != nil:
		// Not a credential decision. The caller renders this as a server error,
		// so an outage is not reported to the user as a wrong password and does
		// not hide inside the 401 rate.
		return Identity{}, fmt.Errorf("local authentication: %w", err)
	}

	if !authenticator.verify(account.PasswordHash, password) {
		return Identity{}, ErrInvalidCredentials
	}
	return Identity{
		Subject:           account.Subject,
		DisplayName:       account.DisplayName,
		PreferredUsername: account.Username,
		Email:             account.Email,
	}, nil
}
