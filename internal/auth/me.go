package auth

import (
	"time"

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
	// SessionExpiresAt is when this session ends: the earlier of its idle and
	// absolute bounds, both of which tflive owns. It lets the web client
	// re-authenticate at a quiet moment instead of being interrupted by a 401.
	// It is not a control: the API rejects an expired session regardless of
	// what the browser believes.
	SessionExpiresAt string `json:"sessionExpiresAt,omitempty"`
}

// GlobalCapabilities encodes coarse-grained permissions answered by OpenFGA.
// The JSON names predate the move off Keycloak realm roles and are kept: they
// are the web client's contract, and only the source of the answers changed.
type GlobalCapabilities struct {
	IsPlatformAdmin bool `json:"isPlatformAdmin"`
	CanCreateStack  bool `json:"canCreateStack"`
}

// MeFromPrincipal maps the authenticated principal and its resolved platform
// capabilities to a MeResponse. The capabilities are passed in rather than
// derived here, because answering them is an OpenFGA call the app layer owns.
func MeFromPrincipal(principal authn.Principal, tenantID traits.TenantID, capabilities GlobalCapabilities) MeResponse {
	sessionExpiresAt := ""
	if !principal.ExpiresAt.IsZero() {
		sessionExpiresAt = principal.ExpiresAt.UTC().Format(time.RFC3339)
	}

	return MeResponse{
		Sub:                principal.Subject,
		DisplayName:        principal.Name,
		Email:              principal.Email,
		GlobalCapabilities: capabilities,
		TenantID:           string(tenantID),
		SessionExpiresAt:   sessionExpiresAt,
	}
}
