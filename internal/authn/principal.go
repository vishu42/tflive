package authn

import (
	"context"
	"time"

	"github.com/vishu42/tflive/internal/strval"
)

// Principal is the normalized identity made available to handlers. It is built
// from the session row, whose claims were copied from an ID token verified once
// at the callback. It carries identity only: authorization is answered by
// OpenFGA, so no role claim from the token reaches an access decision.
// ExpiresAt is identity metadata carried through for presentation — it is
// not an authorization input.
type Principal struct {
	Subject           string
	Name              string
	PreferredUsername string
	Email             string
	ExpiresAt         time.Time
}

// DisplayName applies the same fallback as VerifiedToken.DisplayName, over the
// claims copied onto the session row at sign-in. Both exist because the same
// person is labelled in two places — the app header from this, a grants list
// from the projection — and a provider that omits `name` should not make one of
// them blank while the other reads correctly.
func (principal Principal) DisplayName() string {
	return strval.FirstNonEmpty(principal.Name, principal.PreferredUsername, principal.Email, principal.Subject)
}

type principalContextKey struct{}

func ContextWithPrincipal(ctx context.Context, principal Principal) context.Context {
	return context.WithValue(ctx, principalContextKey{}, principal)
}

func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	principal, ok := ctx.Value(principalContextKey{}).(Principal)
	if !ok {
		return Principal{}, false
	}
	return principal, true
}
