package authn

import (
	"context"
	"errors"
	"strings"
)

// ErrLocalAccountNotFound means no local account carries the requested
// username. The login handler must not distinguish it from a wrong password:
// see LocalAuthenticator.
var ErrLocalAccountNotFound = errors.New("local account not found")

// LocalAccount is an account tflive authenticates itself. It is a credential
// record, not an identity assertion — the identity it produces at sign-in is
// projected into users through the same path an OIDC sign-in uses.
type LocalAccount struct {
	// Subject becomes user:<Subject> in an OpenFGA tuple and the sub of the
	// projected users row, so it must satisfy authz's tuple-token rules: no
	// ':', '#', '*', whitespace, or control characters.
	Subject string
	// Username is stored and compared case-folded. FoldUsername is the one
	// place that fold is defined.
	Username string
	// PasswordHash is PHC-encoded argon2id, as written by HashPassword.
	PasswordHash string
	DisplayName  string
	Email        string
}

// LocalAccountStore reads the accounts tflive owns. *postgres.Store implements
// it; the interface is here, next to its consumer, so the authenticator needs
// no database to be tested.
type LocalAccountStore interface {
	LocalAccountByUsername(ctx context.Context, username string) (LocalAccount, error)
}

// FoldUsername normalizes a username for storage and lookup. Both the unique
// constraint and the login lookup are defined in terms of it, so they cannot
// disagree about whether two spellings are the same account.
func FoldUsername(username string) string {
	return strings.ToLower(strings.TrimSpace(username))
}
