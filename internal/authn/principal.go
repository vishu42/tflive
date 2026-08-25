package authn

import (
	"context"
	"time"
)

// Principal is the normalized identity made available after token
// verification. It carries identity only: authorization is answered by
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

func principalFromVerifiedToken(token VerifiedToken) Principal {
	return Principal{
		Subject:           token.Subject,
		Name:              token.Name,
		PreferredUsername: token.PreferredUsername,
		Email:             token.Email,
		ExpiresAt:         token.ExpiresAt,
	}
}
