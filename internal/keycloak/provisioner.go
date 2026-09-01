package keycloak

import (
	"context"
	"fmt"
)

var platformRealmManagementRoles = []string{
	"manage-users",
	"query-users",
	"view-realm",
	"view-users",
}

type RealmSpec struct {
	Name                string
	Enabled             bool
	AccessTokenLifespan int
	SSLRequired         string
	RegistrationAllowed bool
}

type RoleSpec struct {
	Name        string
	Description string
	Composite   bool
}

type ClientSpec struct {
	ClientID                     string
	Name                         string
	Secret                       string
	Enabled                      bool
	Protocol                     string
	BearerOnly                   bool
	PublicClient                 bool
	StandardFlowEnabled          bool
	ImplicitFlowEnabled          bool
	DirectAccessGrantsEnabled    bool
	ServiceAccountsEnabled       bool
	AuthorizationServicesEnabled bool
	FullScopeAllowed             bool
	RedirectURIs                 []string
	WebOrigins                   []string
	Attributes                   map[string]string
}

type UserSpec struct {
	Username      string
	Password      string
	Email         string
	FirstName     string
	LastName      string
	Enabled       bool
	EmailVerified bool
}

type ResourceRef struct {
	ID   string
	Name string
}

// Result contains only non-sensitive identifiers suitable for operational
// logs after a successful provisioning run.
type Result struct {
	Realm                 string
	APIClientID           string
	PlatformAdminUsername string
}

type provisionBackend interface {
	EnsureRealm(context.Context, RealmSpec) error
	EnsureRole(context.Context, string, RoleSpec) (ResourceRef, error)
	EnsureClient(context.Context, string, ClientSpec) (ResourceRef, error)
	LookupClient(context.Context, string, string) (ResourceRef, error)
	EnsureUser(context.Context, string, UserSpec) (ResourceRef, error)
	ClientRole(context.Context, string, ResourceRef, string) (ResourceRef, error)
	EnsureClientRoleMapping(context.Context, string, ResourceRef, ResourceRef, []ResourceRef) error
}

func provisionWithBackend(ctx context.Context, cfg Config, backend provisionBackend) (Result, error) {
	sslRequired := "external"
	if cfg.Environment == "development" {
		sslRequired = "none"
	}
	realmSpec := RealmSpec{
		Name:    cfg.Realm,
		Enabled: true,
		// Five minutes. It does not govern how long a tflive session lasts —
		// that is tflive's own, set by TFLIVE_SESSION_ABSOLUTE_TTL and
		// TFLIVE_SESSION_IDLE_TTL — and the ID token it bounds authenticates
		// nothing at tflive after the callback, since the middleware works off
		// the session store. So this is hygiene rather than a control: the one
		// token we keep is short-lived by default, and nothing is lost by that.
		// RP-Initiated Logout 1.0 says the OP SHOULD honour an expired
		// id_token_hint, and a session running up to eight hours means ours is
		// expired at logout whatever this value says.
		AccessTokenLifespan: 300,
		SSLRequired:         sslRequired,
		RegistrationAllowed: false,
	}
	if err := backend.EnsureRealm(ctx, realmSpec); err != nil {
		return Result{}, fmt.Errorf("ensure realm %s: %w", cfg.Realm, err)
	}

	apiAttributes := disabledGrantAttributes()
	apiAttributes["pkce.code.challenge.method"] = "S256"
	apiAttributes["post.logout.redirect.uris"] = cfg.PostLogoutRedirectURI
	// Keycloak posts the logout notification here when a session it owns ends.
	// session.required makes it include sid, in both the ID token and the
	// logout token, which is what lets one device be signed out instead of
	// every session the user has.
	apiAttributes["backchannel.logout.url"] = cfg.BackchannelLogoutURI
	apiAttributes["backchannel.logout.session.required"] = "true"
	apiAttributes["backchannel.logout.revoke.offline.tokens"] = "false"
	if _, err := backend.EnsureClient(ctx, cfg.Realm, ClientSpec{
		ClientID:                     cfg.APIClientID,
		Name:                         "tflive API",
		Secret:                       cfg.APIClientSecret,
		Enabled:                      true,
		Protocol:                     "openid-connect",
		BearerOnly:                   false,
		PublicClient:                 false,
		StandardFlowEnabled:          true,
		ImplicitFlowEnabled:          false,
		DirectAccessGrantsEnabled:    false,
		ServiceAccountsEnabled:       false,
		AuthorizationServicesEnabled: false,
		FullScopeAllowed:             false,
		// No WebOrigins: the browser reaches the API through the same origin
		// that serves the SPA, so no CORS is involved anywhere.
		RedirectURIs: []string{cfg.CallbackURI},
		Attributes:   apiAttributes,
	}); err != nil {
		return Result{}, fmt.Errorf("ensure API client %s: %w", cfg.APIClientID, err)
	}

	platformUser, err := backend.EnsureUser(ctx, cfg.Realm, UserSpec{
		Username:      cfg.PlatformAdminUsername,
		Password:      cfg.PlatformAdminPassword,
		Email:         cfg.PlatformAdminEmail,
		FirstName:     cfg.PlatformAdminFirstName,
		LastName:      cfg.PlatformAdminLastName,
		Enabled:       true,
		EmailVerified: true,
	})
	if err != nil {
		return Result{}, fmt.Errorf("ensure bootstrap platform administrator: %w", err)
	}
	// No global realm roles are assigned. Keycloak is identity-only: the
	// platform tiers that platform-admin and stack-creator used to carry are
	// OpenFGA tuples on platform:tflive now, seeded by #212. Leaving the roles
	// here would be worse than redundant -- a stale claim nothing reads looks
	// like access that was granted.

	realmManagement, err := backend.LookupClient(ctx, cfg.Realm, "realm-management")
	if err != nil {
		return Result{}, fmt.Errorf("lookup required client realm-management: %w", err)
	}
	adminRoles := make([]ResourceRef, 0, len(platformRealmManagementRoles))
	for _, roleName := range platformRealmManagementRoles {
		role, err := backend.ClientRole(ctx, cfg.Realm, realmManagement, roleName)
		if err != nil {
			return Result{}, fmt.Errorf("lookup realm-management role %s: %w", roleName, err)
		}
		adminRoles = append(adminRoles, role)
	}
	if err := backend.EnsureClientRoleMapping(ctx, cfg.Realm, platformUser, realmManagement, adminRoles); err != nil {
		return Result{}, fmt.Errorf("assign least-privilege realm administration roles: %w", err)
	}

	// No directory reader service account. Grant display names and the user
	// search box read a local projection of what each ID token asserted at
	// sign-in, so nothing here needs query-users or view-users on the realm --
	// which is exactly the elevated permission a customer's security team
	// would have had to approve on their own IdP.

	return Result{
		Realm:                 cfg.Realm,
		APIClientID:           cfg.APIClientID,
		PlatformAdminUsername: cfg.PlatformAdminUsername,
	}, nil
}

func disabledGrantAttributes() map[string]string {
	return map[string]string{
		"oauth2.device.authorization.grant.enabled": "false",
		"oidc.ciba.grant.enabled":                   "false",
		"standard.token.exchange.enabled":           "false",
	}
}
