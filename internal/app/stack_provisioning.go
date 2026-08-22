package app

import (
	"context"
	"fmt"

	"github.com/vishu42/tflive/internal/authz"
	"github.com/vishu42/tflive/internal/traits"
)

// This file holds the domain half of stack provisioning: what it means to
// provision a stack and to mark one ready. The two queue kinds that drive it
// live beside their handlers in grant_stack_owner_handler.go and
// mark_stack_ready_handler.go, which are thin adapters over the methods here.

// StackStatusRepository flips a provisioned stack to its terminal status.
type StackStatusRepository interface {
	MarkStackReady(ctx context.Context, tenantID traits.TenantID, stackID traits.StackID) error
}

// StackProvisioner is the slice of Service the provisioning handlers need.
type StackProvisioner interface {
	GrantStackOwner(ctx context.Context, command GrantStackOwnerCommand) error
	MarkStackReady(ctx context.Context, tenantID traits.TenantID, stackID traits.StackID) error
}

// GrantStackOwnerCommand grants the founding owner their role on a new stack.
type GrantStackOwnerCommand struct {
	TenantID traits.TenantID
	StackID  traits.StackID
	Subject  string
}

// GrantStackOwner writes the founding owner tuple for a newly created stack.
//
// It reads before writing so that a replayed delivery is a no-op rather than a
// duplicate-tuple error that would retry forever. It deliberately runs no
// permission check: the caller is the queue, not a principal, and the request
// was authorized when CreateStack accepted it.
func (service *Service) GrantStackOwner(ctx context.Context, command GrantStackOwnerCommand) error {
	if service.Authorizer == nil {
		return fmt.Errorf("%w: authorization not configured", authz.ErrUnavailable)
	}
	object, err := authz.ObjectFromID(authz.TypeStack, string(command.StackID))
	if err != nil {
		return fmt.Errorf("grant stack owner: %w", err)
	}
	subject, err := authz.SubjectFromOIDCSub(command.Subject)
	if err != nil {
		return fmt.Errorf("grant stack owner subject: %w", err)
	}

	current, err := service.listGrantsForStack(ctx, object)
	if err != nil {
		return fmt.Errorf("read stack grants: %w", err)
	}
	for _, grant := range current.Grants {
		if grant.Subject().String() == subject.String() && grant.Relation() == authz.RelationOwner {
			return nil
		}
	}

	grant, err := authz.NewGrant(subject, object, authz.RelationOwner)
	if err != nil {
		return fmt.Errorf("build owner grant: %w", err)
	}
	mutation, err := authz.NewMutation([]authz.Grant{grant}, true)
	if err != nil {
		return fmt.Errorf("build owner mutation: %w", err)
	}
	if err := service.Authorizer.WriteRelationships(ctx, mutation); err != nil {
		return fmt.Errorf("write owner grant: %w", err)
	}
	return nil
}

// MarkStackReady records that a stack has finished provisioning.
func (service *Service) MarkStackReady(ctx context.Context, tenantID traits.TenantID, stackID traits.StackID) error {
	if service.StackStatuses == nil {
		return fmt.Errorf("%w: stack status repository not configured", authz.ErrUnavailable)
	}
	if err := service.StackStatuses.MarkStackReady(ctx, tenantID, stackID); err != nil {
		return fmt.Errorf("mark stack ready: %w", err)
	}
	return nil
}
