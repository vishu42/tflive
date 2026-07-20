package auth

import (
	"github.com/vishu42/tflive/internal/authn"
	"github.com/vishu42/tflive/internal/traits"
)

// MeResponse is the identity envelope returned by GET /v1/me.
type MeResponse struct {
	Sub                string             `json:"sub"`
	DisplayName        string             `json:"displayName"`
	Email              string             `json:"email,omitempty"`
	GlobalCapabilities GlobalCapabilities `json:"globalCapabilities"`
	TenantID           string             `json:"tenantID"`
}

// GlobalCapabilities encodes coarse-grained permissions derived
// from the principal's Keycloak realm roles.
type GlobalCapabilities struct {
	IsPlatformAdmin bool `json:"isPlatformAdmin"`
	CanCreateStack  bool `json:"canCreateStack"`
}

// MeFromPrincipal maps the authenticated principal to a MeResponse,
// deriving global capabilities from realm roles.
func MeFromPrincipal(principal authn.Principal, tenantID traits.TenantID) MeResponse {
	return MeResponse{
		Sub:                principal.Subject,
		DisplayName:        principal.Name,
		Email:              principal.Email,
		GlobalCapabilities: capabilitiesFromRoles(principal.RealmRoles),
		TenantID:           string(tenantID),
	}
}

func capabilitiesFromRoles(roles []string) GlobalCapabilities {
	var caps GlobalCapabilities
	for _, role := range roles {
		if role == "platform-admin" {
			caps.IsPlatformAdmin = true
		}
		if role == "stack-creator" {
			caps.CanCreateStack = true
		}
	}
	return caps
}
