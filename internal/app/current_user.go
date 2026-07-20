package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/vishu42/tflive/internal/authn"
	"github.com/vishu42/tflive/internal/authz"
	"github.com/vishu42/tflive/internal/traits"
)

var (
	ErrLastOwnerProtected = errors.New("last owner protected")
	ErrTargetUserNotFound = errors.New("target user not found")
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

// ListStackGrantsCommand asks the app to list role assignments on a stack.
type ListStackGrantsCommand struct {
	TenantID traits.TenantID
	StackID  traits.StackID
}

// GrantEntry is one role assignment returned by grant management endpoints.
type GrantEntry struct {
	UserID string `json:"user_id"`
	Role   string `json:"role"`
}

// ListStackGrants returns all direct role assignments on a stack.
func (service *Service) ListStackGrants(ctx context.Context, command ListStackGrantsCommand) ([]GrantEntry, error) {
	if err := validateListStackGrantsCommand(command); err != nil {
		return nil, err
	}
	if err := authorizeStack(ctx, service.Authorizer, command.StackID, authz.PermissionManageAccess, ErrForbidden); err != nil {
		return nil, err
	}

	stack, err := authz.StackFromID(string(command.StackID))
	if err != nil {
		return nil, fmt.Errorf("resolve stack: %w", err)
	}
	result, err := service.Authorizer.ListGrants(ctx, authz.ListGrantsRequest{Stack: stack})
	if err != nil {
		return nil, fmt.Errorf("list grants: %w", err)
	}
	entries := make([]GrantEntry, 0, len(result.Grants))
	for _, grant := range result.Grants {
		entries = append(entries, GrantEntry{
			UserID: subjectID(grant.Subject()),
			Role:   grant.Role().String(),
		})
	}
	return entries, nil
}

// PutStackGrantCommand asks the app to assign or replace a role on a stack.
type PutStackGrantCommand struct {
	TenantID traits.TenantID
	StackID  traits.StackID
	UserID   string
	Role     string
}

// PutStackGrant assigns or replaces a role for a user on a stack. It is
// idempotent when the user already holds the requested role.
func (service *Service) PutStackGrant(ctx context.Context, command PutStackGrantCommand) ([]GrantEntry, error) {
	actor, err := requirePrincipalForGrant(ctx)
	if err != nil {
		return nil, err
	}
	if err := validatePutStackGrantCommand(command); err != nil {
		return nil, err
	}
	if err := authorizeStack(ctx, service.Authorizer, command.StackID, authz.PermissionManageAccess, ErrForbidden); err != nil {
		service.auditError(ctx, traits.SecurityAuditEvent{
			ActorSubject: actor.Subject,
			Action:       traits.AuditActionFailedAccessAttempt,
			TenantID:     command.TenantID,
			StackID:      command.StackID,
			Outcome:      traits.AuditOutcomeFailure,
		})
		return nil, err
	}
	if service.UserDirectory == nil {
		return nil, ErrDirectoryUnavailable
	}
	if _, err := service.UserDirectory.GetUserByID(ctx, command.UserID); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrTargetUserNotFound, err)
	}

	targetSubject, err := authz.SubjectFromKeycloakSub(command.UserID)
	if err != nil {
		return nil, fmt.Errorf("resolve target subject: %w", err)
	}
	stack, err := authz.StackFromID(string(command.StackID))
	if err != nil {
		return nil, fmt.Errorf("resolve stack: %w", err)
	}
	targetRole, err := authz.RoleFromDirectRelation(command.Role)
	if err != nil {
		return nil, fmt.Errorf("resolve role: %w", err)
	}

	// Read current grants to check last-owner and idempotency.
	currentGrants, err := service.Authorizer.ListGrants(ctx, authz.ListGrantsRequest{Stack: stack})
	if err != nil {
		return nil, fmt.Errorf("list current grants: %w", err)
	}

	var existingRole string
	for _, g := range currentGrants.Grants {
		if g.Subject() == targetSubject {
			existingRole = g.Role().String()
			break
		}
	}

	// Idempotent: same role already assigned.
	if existingRole == command.Role {
		return buildGrantEntries(currentGrants.Grants), nil
	}

	// Last-owner protection: if target is currently the last owner and role changes.
	if existingRole == "owner" && command.Role != "owner" {
		ownerCount := 0
		for _, g := range currentGrants.Grants {
			if g.Role() == authz.RoleOwner {
				ownerCount++
			}
		}
		if ownerCount <= 1 {
			return nil, ErrLastOwnerProtected
		}
	}

	// Build mutation.
	newGrant, err := authz.NewGrant(targetSubject, stack, targetRole)
	if err != nil {
		return nil, fmt.Errorf("create grant: %w", err)
	}

	if existingRole != "" {
		// Replace: delete old role, write new role.
		oldRole, err := authz.RoleFromDirectRelation(existingRole)
		if err != nil {
			return nil, fmt.Errorf("resolve old role: %w", err)
		}
		oldGrant, err := authz.NewGrant(targetSubject, stack, oldRole)
		if err != nil {
			return nil, fmt.Errorf("create old grant: %w", err)
		}
		deleteMutation, err := authz.NewMutation([]authz.Grant{oldGrant}, true)
		if err != nil {
			return nil, fmt.Errorf("create delete mutation: %w", err)
		}
		if err := service.Authorizer.DeleteRelationships(ctx, deleteMutation); err != nil {
			return nil, fmt.Errorf("delete old role: %w", err)
		}
	}

	writeMutation, err := authz.NewMutation([]authz.Grant{newGrant}, true)
	if err != nil {
		return nil, fmt.Errorf("create write mutation: %w", err)
	}
	if err := service.Authorizer.WriteRelationships(ctx, writeMutation); err != nil {
		return nil, fmt.Errorf("write grant: %w", err)
	}

	service.auditError(ctx, traits.SecurityAuditEvent{
		ActorSubject: actor.Subject,
		Action:       traits.AuditActionRoleChange,
		TargetUser:   command.UserID,
		TenantID:     command.TenantID,
		StackID:      command.StackID,
		OldRole:      existingRole,
		NewRole:      command.Role,
		Outcome:      traits.AuditOutcomeSuccess,
	})

	// Re-read grants for response.
	updatedGrants, err := service.Authorizer.ListGrants(ctx, authz.ListGrantsRequest{Stack: stack})
	if err != nil {
		return nil, fmt.Errorf("list updated grants: %w", err)
	}
	return buildGrantEntries(updatedGrants.Grants), nil
}

// RevokeStackGrantCommand asks the app to revoke a user's role on a stack.
type RevokeStackGrantCommand struct {
	TenantID traits.TenantID
	StackID  traits.StackID
	UserID   string
}

// RevokeStackGrant removes a user's direct role assignment on a stack.
func (service *Service) RevokeStackGrant(ctx context.Context, command RevokeStackGrantCommand) error {
	actor, err := requirePrincipalForGrant(ctx)
	if err != nil {
		return err
	}
	if err := validateRevokeStackGrantCommand(command); err != nil {
		return err
	}
	if err := authorizeStack(ctx, service.Authorizer, command.StackID, authz.PermissionManageAccess, ErrForbidden); err != nil {
		service.auditError(ctx, traits.SecurityAuditEvent{
			ActorSubject: actor.Subject,
			Action:       traits.AuditActionFailedAccessAttempt,
			TenantID:     command.TenantID,
			StackID:      command.StackID,
			Outcome:      traits.AuditOutcomeFailure,
		})
		return err
	}

	targetSubject, err := authz.SubjectFromKeycloakSub(command.UserID)
	if err != nil {
		return fmt.Errorf("resolve target subject: %w", err)
	}
	stack, err := authz.StackFromID(string(command.StackID))
	if err != nil {
		return fmt.Errorf("resolve stack: %w", err)
	}

	// Read current grants to check last-owner and find existing role.
	currentGrants, err := service.Authorizer.ListGrants(ctx, authz.ListGrantsRequest{Stack: stack})
	if err != nil {
		return fmt.Errorf("list current grants: %w", err)
	}

	var existingRole string
	for _, g := range currentGrants.Grants {
		if g.Subject() == targetSubject {
			existingRole = g.Role().String()
			break
		}
	}

	if existingRole == "" {
		return nil
	}

	// Last-owner protection.
	if existingRole == "owner" {
		ownerCount := 0
		for _, g := range currentGrants.Grants {
			if g.Role() == authz.RoleOwner {
				ownerCount++
			}
		}
		if ownerCount <= 1 {
			return ErrLastOwnerProtected
		}
	}

	role, err := authz.RoleFromDirectRelation(existingRole)
	if err != nil {
		return fmt.Errorf("resolve role: %w", err)
	}
	oldGrant, err := authz.NewGrant(targetSubject, stack, role)
	if err != nil {
		return fmt.Errorf("create grant: %w", err)
	}
	deleteMutation, err := authz.NewMutation([]authz.Grant{oldGrant}, true)
	if err != nil {
		return fmt.Errorf("create delete mutation: %w", err)
	}
	if err := service.Authorizer.DeleteRelationships(ctx, deleteMutation); err != nil {
		return fmt.Errorf("delete grant: %w", err)
	}

	service.auditError(ctx, traits.SecurityAuditEvent{
		ActorSubject: actor.Subject,
		Action:       traits.AuditActionRevoke,
		TargetUser:   command.UserID,
		TenantID:     command.TenantID,
		StackID:      command.StackID,
		OldRole:      existingRole,
		Outcome:      traits.AuditOutcomeSuccess,
	})

	return nil
}

// --- helpers ---

func requirePrincipalForGrant(ctx context.Context) (authn.Principal, error) {
	return requirePrincipal(ctx)
}

func subjectID(subject authz.Subject) string {
	s := subject.String()
	if len(s) > 5 && s[:5] == "user:" {
		return s[5:]
	}
	return s
}

func buildGrantEntries(grants []authz.Grant) []GrantEntry {
	entries := make([]GrantEntry, 0, len(grants))
	for _, g := range grants {
		entries = append(entries, GrantEntry{
			UserID: subjectID(g.Subject()),
			Role:   g.Role().String(),
		})
	}
	return entries
}

func validateListStackGrantsCommand(command ListStackGrantsCommand) error {
	switch {
	case command.TenantID == "":
		return fmt.Errorf("%w: tenant id is required", ErrInvalidCommand)
	case command.StackID == "":
		return fmt.Errorf("%w: stack id is required", ErrInvalidCommand)
	default:
		return nil
	}
}

func validatePutStackGrantCommand(command PutStackGrantCommand) error {
	switch {
	case command.TenantID == "":
		return fmt.Errorf("%w: tenant id is required", ErrInvalidCommand)
	case command.StackID == "":
		return fmt.Errorf("%w: stack id is required", ErrInvalidCommand)
	case command.UserID == "":
		return fmt.Errorf("%w: user id is required", ErrInvalidCommand)
	case command.Role == "":
		return fmt.Errorf("%w: role is required", ErrInvalidCommand)
	case !validGrantRole(command.Role):
		return fmt.Errorf("%w: role must be one of owner, operator, approver, viewer", ErrInvalidCommand)
	default:
		return nil
	}
}

func validateRevokeStackGrantCommand(command RevokeStackGrantCommand) error {
	switch {
	case command.TenantID == "":
		return fmt.Errorf("%w: tenant id is required", ErrInvalidCommand)
	case command.StackID == "":
		return fmt.Errorf("%w: stack id is required", ErrInvalidCommand)
	case command.UserID == "":
		return fmt.Errorf("%w: user id is required", ErrInvalidCommand)
	default:
		return nil
	}
}

func validGrantRole(role string) bool {
	switch role {
	case "owner", "operator", "approver", "viewer":
		return true
	default:
		return false
	}
}
