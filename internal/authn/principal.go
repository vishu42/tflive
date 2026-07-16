package authn

import (
	"context"
	"slices"
)

var globalRoles = map[string]struct{}{
	"platform-admin": {},
	"stack-creator":  {},
}

// Principal is the normalized identity made available after token verification.
// Roles are copied before the value is stored in a request context.
type Principal struct {
	Subject           string
	Name              string
	PreferredUsername string
	Email             string
	RealmRoles        []string
}

type principalContextKey struct{}

func ContextWithPrincipal(ctx context.Context, principal Principal) context.Context {
	principal.RealmRoles = append([]string(nil), principal.RealmRoles...)
	return context.WithValue(ctx, principalContextKey{}, principal)
}

func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	principal, ok := ctx.Value(principalContextKey{}).(Principal)
	if !ok {
		return Principal{}, false
	}
	principal.RealmRoles = append([]string(nil), principal.RealmRoles...)
	return principal, true
}

func principalFromVerifiedToken(token VerifiedToken) Principal {
	return Principal{
		Subject:           token.Subject,
		Name:              token.Name,
		PreferredUsername: token.PreferredUsername,
		Email:             token.Email,
		RealmRoles:        normalizedGlobalRoles(token.RealmRoles),
	}
}

func normalizedGlobalRoles(roles []string) []string {
	seen := make(map[string]struct{}, len(roles))
	for _, role := range roles {
		if _, ok := globalRoles[role]; ok {
			seen[role] = struct{}{}
		}
	}
	result := make([]string, 0, len(seen))
	for role := range seen {
		result = append(result, role)
	}
	slices.Sort(result)
	return result
}
