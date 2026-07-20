package app

import (
	"context"

	"github.com/vishu42/tflive/internal/authn"
)

// GetMeCommand asks the app to return the authenticated user's identity.
type GetMeCommand struct{}

// MeResponse contains safe identity attributes for the authenticated user.
type MeResponse struct {
	Subject            string             `json:"subject"`
	Name               string             `json:"name"`
	PreferredUsername  string             `json:"preferred_username"`
	Email              string             `json:"email"`
	GlobalCapabilities GlobalCapabilities `json:"global_capabilities"`
}

// GlobalCapabilities derives high-level permissions from realm roles.
type GlobalCapabilities struct {
	CanCreateStacks bool `json:"can_create_stacks"`
	IsPlatformAdmin bool `json:"is_platform_admin"`
}

// GetMe returns the authenticated user's safe identity and global capabilities.
func (service *Service) GetMe(ctx context.Context) (MeResponse, error) {
	principal, err := requirePrincipal(ctx)
	if err != nil {
		return MeResponse{}, err
	}
	return MeResponse{
		Subject:            principal.Subject,
		Name:               principal.Name,
		PreferredUsername:  principal.PreferredUsername,
		Email:              principal.Email,
		GlobalCapabilities: deriveGlobalCapabilities(principal),
	}, nil
}

func deriveGlobalCapabilities(principal authn.Principal) GlobalCapabilities {
	return GlobalCapabilities{
		CanCreateStacks: hasRealmRole(principal, "stack-creator") || isPlatformAdmin(principal),
		IsPlatformAdmin: isPlatformAdmin(principal),
	}
}
