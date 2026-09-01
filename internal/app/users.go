package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/vishu42/tflive/internal/authz"
)

// ErrUserNotProvisioned means the target subject has never signed in, so there
// is no projected identity to attach a role to.
//
// It is its own sentinel rather than an ErrInvalidCommand, because this is the
// user-visible face of a deliberate design limitation rather than a malformed
// request, and the message reaches a person: the API renders it verbatim.
var ErrUserNotProvisioned = errors.New("user has not signed in to tflive yet")

// UserProfile is what a verified token asserted about a person. It is display
// data with one key: Sub is the OIDC sub claim and the only stable identifier,
// while DisplayName and Email are mutable and never an authorization input.
//
// The JSON names match MeResponse (internal/auth/me.go), which describes the
// same three things about the caller.
type UserProfile struct {
	Sub         string `json:"sub"`
	DisplayName string `json:"displayName"`
	Email       string `json:"email"`
}

// UserRepository reads and writes the local identity projection.
//
// It is not a directory. Nothing here creates, disables, or deletes a person:
// UpsertUser records what a token already asserted at the moment it was
// verified, and the two reads serve the grants UI. A subject absent from the
// projection has simply never signed in.
type UserRepository interface {
	// UpsertUser projects one verified identity. It is called on every
	// sign-in, so it refreshes a changed name or email naturally.
	UpsertUser(ctx context.Context, profile UserProfile, seenAt time.Time) error
	// SearchUsers matches display name or email, case-insensitively.
	SearchUsers(ctx context.Context, query string, first, max int) ([]UserProfile, error)
	// UsersBySubs resolves many subjects in one round trip, keyed by sub.
	// Subjects with no projected row are absent from the map rather than
	// present and empty, so the caller can tell the difference.
	UsersBySubs(ctx context.Context, subs []string) (map[string]UserProfile, error)
}

// requireUserRepository guards every entry point that reads or writes the
// projection. The grants and search routes are registered unconditionally,
// so a Service wired without a UserRepository is reachable; without this it
// nil-panics on the first request instead of answering. ErrUnavailable rather
// than ErrInvalidCommand, because nothing about the request is wrong.
func (service *Service) requireUserRepository() error {
	if service.Users == nil {
		return fmt.Errorf("%w: user repository not configured", authz.ErrUnavailable)
	}
	return nil
}
