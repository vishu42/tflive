package keycloak

import (
	"context"
	"fmt"
	"strings"
)

const (
	platformAdminDescription = "Administer tflive and bypass ordinary stack checks except security invariants"
	stackCreatorDescription  = "Create tflive stacks and become their initial owner"
	audienceScopeName        = "tflive-api-audience"
	audienceMapperName       = "tflive-api-audience"
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

type ClientScopeSpec struct {
	Name       string
	Protocol   string
	Attributes map[string]string
}

type ProtocolMapperSpec struct {
	Name            string
	Protocol        string
	ProtocolMapper  string
	ConsentRequired bool
	Config          map[string]string
}

type UserSpec struct {
	Username string
	Password string
	Enabled  bool
}

type ResourceRef struct {
	ID   string
	Name string
}

type ExampleAccessToken struct {
	Audience   []string
	RealmRoles []string
}

// Result contains only non-sensitive identifiers suitable for operational
// logs after a successful provisioning run.
type Result struct {
	Realm                 string
	WebClientID           string
	APIClientID           string
	PlatformAdminUsername string
}

type provisionBackend interface {
	EnsureRealm(context.Context, RealmSpec) error
	EnsureRole(context.Context, string, RoleSpec) (ResourceRef, error)
	EnsureClient(context.Context, string, ClientSpec) (ResourceRef, error)
	LookupClient(context.Context, string, string) (ResourceRef, error)
	EnsureClientScope(context.Context, string, ClientScopeSpec) (ResourceRef, error)
	LookupClientScope(context.Context, string, string) (ResourceRef, error)
	EnsureProtocolMapper(context.Context, string, ResourceRef, ProtocolMapperSpec) error
	EnsureDefaultClientScope(context.Context, string, ResourceRef, ResourceRef) error
	EnsureUser(context.Context, string, UserSpec) (ResourceRef, error)
	EnsureRealmRoleMapping(context.Context, string, ResourceRef, []ResourceRef) error
	ClientRole(context.Context, string, ResourceRef, string) (ResourceRef, error)
	EnsureClientRoleMapping(context.Context, string, ResourceRef, ResourceRef, []ResourceRef) error
	ExampleAccessToken(context.Context, string, ResourceRef, ResourceRef) (ExampleAccessToken, error)
}

func provisionWithBackend(ctx context.Context, cfg Config, backend provisionBackend) (Result, error) {
	realmSpec := RealmSpec{
		Name:                cfg.Realm,
		Enabled:             true,
		AccessTokenLifespan: 300,
		SSLRequired:         "external",
		RegistrationAllowed: false,
	}
	if err := backend.EnsureRealm(ctx, realmSpec); err != nil {
		return Result{}, fmt.Errorf("ensure realm %s: %w", cfg.Realm, err)
	}

	platformRole, err := backend.EnsureRole(ctx, cfg.Realm, RoleSpec{
		Name:        "platform-admin",
		Description: platformAdminDescription,
		Composite:   false,
	})
	if err != nil {
		return Result{}, fmt.Errorf("ensure realm role platform-admin: %w", err)
	}
	if _, err := backend.EnsureRole(ctx, cfg.Realm, RoleSpec{
		Name:        "stack-creator",
		Description: stackCreatorDescription,
		Composite:   false,
	}); err != nil {
		return Result{}, fmt.Errorf("ensure realm role stack-creator: %w", err)
	}

	if _, err := backend.EnsureClient(ctx, cfg.Realm, ClientSpec{
		ClientID:                     cfg.APIClientID,
		Name:                         "tflive API",
		Enabled:                      true,
		Protocol:                     "openid-connect",
		BearerOnly:                   true,
		PublicClient:                 false,
		StandardFlowEnabled:          false,
		ImplicitFlowEnabled:          false,
		DirectAccessGrantsEnabled:    false,
		ServiceAccountsEnabled:       false,
		AuthorizationServicesEnabled: false,
		FullScopeAllowed:             false,
		Attributes:                   disabledGrantAttributes(),
	}); err != nil {
		return Result{}, fmt.Errorf("ensure API client %s: %w", cfg.APIClientID, err)
	}

	webAttributes := disabledGrantAttributes()
	webAttributes["pkce.code.challenge.method"] = "S256"
	webAttributes["post.logout.redirect.uris"] = strings.Join(cfg.RedirectURIs, "##")
	webClient, err := backend.EnsureClient(ctx, cfg.Realm, ClientSpec{
		ClientID:                     cfg.WebClientID,
		Name:                         "tflive web",
		Enabled:                      true,
		Protocol:                     "openid-connect",
		BearerOnly:                   false,
		PublicClient:                 true,
		StandardFlowEnabled:          true,
		ImplicitFlowEnabled:          false,
		DirectAccessGrantsEnabled:    false,
		ServiceAccountsEnabled:       false,
		AuthorizationServicesEnabled: false,
		FullScopeAllowed:             true,
		RedirectURIs:                 append([]string(nil), cfg.RedirectURIs...),
		WebOrigins:                   append([]string(nil), cfg.WebOrigins...),
		Attributes:                   webAttributes,
	})
	if err != nil {
		return Result{}, fmt.Errorf("ensure browser client %s: %w", cfg.WebClientID, err)
	}

	audienceScope, err := backend.EnsureClientScope(ctx, cfg.Realm, ClientScopeSpec{
		Name:     audienceScopeName,
		Protocol: "openid-connect",
		Attributes: map[string]string{
			"include.in.token.scope": "false",
		},
	})
	if err != nil {
		return Result{}, fmt.Errorf("ensure client scope %s: %w", audienceScopeName, err)
	}
	if err := backend.EnsureProtocolMapper(ctx, cfg.Realm, audienceScope, ProtocolMapperSpec{
		Name:            audienceMapperName,
		Protocol:        "openid-connect",
		ProtocolMapper:  "oidc-audience-mapper",
		ConsentRequired: false,
		Config: map[string]string{
			"included.client.audience":  cfg.APIClientID,
			"access.token.claim":        "true",
			"introspection.token.claim": "true",
			"id.token.claim":            "false",
			"lightweight.claim":         "false",
		},
	}); err != nil {
		return Result{}, fmt.Errorf("ensure audience mapper %s: %w", audienceMapperName, err)
	}
	if err := backend.EnsureDefaultClientScope(ctx, cfg.Realm, webClient, audienceScope); err != nil {
		return Result{}, fmt.Errorf("link audience scope to browser client: %w", err)
	}
	rolesScope, err := backend.LookupClientScope(ctx, cfg.Realm, "roles")
	if err != nil {
		return Result{}, fmt.Errorf("lookup required client scope roles: %w", err)
	}
	if err := backend.EnsureDefaultClientScope(ctx, cfg.Realm, webClient, rolesScope); err != nil {
		return Result{}, fmt.Errorf("link roles scope to browser client: %w", err)
	}

	platformUser, err := backend.EnsureUser(ctx, cfg.Realm, UserSpec{
		Username: cfg.PlatformAdminUsername,
		Password: cfg.PlatformAdminPassword,
		Enabled:  true,
	})
	if err != nil {
		return Result{}, fmt.Errorf("ensure bootstrap platform administrator: %w", err)
	}
	if err := backend.EnsureRealmRoleMapping(ctx, cfg.Realm, platformUser, []ResourceRef{platformRole}); err != nil {
		return Result{}, fmt.Errorf("assign platform-admin realm role: %w", err)
	}

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

	exampleToken, err := backend.ExampleAccessToken(ctx, cfg.Realm, webClient, platformUser)
	if err != nil {
		return Result{}, fmt.Errorf("generate effective example access token: %w", err)
	}
	if !containsString(exampleToken.Audience, cfg.APIClientID) {
		return Result{}, fmt.Errorf("effective access token is missing audience %s", cfg.APIClientID)
	}
	if !containsString(exampleToken.RealmRoles, "platform-admin") {
		return Result{}, fmt.Errorf("effective access token is missing realm role platform-admin")
	}

	return Result{
		Realm:                 cfg.Realm,
		WebClientID:           cfg.WebClientID,
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

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
