package app

import (
	"context"
	"errors"
	"strings"

	"github.com/vishu42/tflive/internal/authn"
	"github.com/vishu42/tflive/internal/authz"
	"github.com/vishu42/tflive/internal/traits"
)

func hasRealmRole(principal authn.Principal, wanted string) bool {
	for _, role := range principal.RealmRoles {
		if role == wanted {
			return true
		}
	}
	return false
}

func isPlatformAdmin(principal authn.Principal) bool {
	return hasRealmRole(principal, "platform-admin")
}

func requirePrincipal(ctx context.Context) (authn.Principal, error) {
	principal, ok := authn.PrincipalFromContext(ctx)
	if !ok || principal.Subject == "" {
		return authn.Principal{}, ErrUnauthenticated
	}
	return principal, nil
}

func requireTemplateCatalogAccess(ctx context.Context) error {
	principal, err := requirePrincipal(ctx)
	if err != nil {
		return err
	}
	if isPlatformAdmin(principal) || hasRealmRole(principal, "stack-creator") {
		return nil
	}
	return ErrForbidden
}

func authorizeStack(ctx context.Context, authorizer authz.Authorizer, stackID traits.StackID, permission authz.Permission, denied error) error {
	principal, err := requirePrincipal(ctx)
	if err != nil || isPlatformAdmin(principal) {
		return err
	}
	if authorizer == nil {
		return errors.New("authorization not configured")
	}
	subject, err := authz.SubjectFromKeycloakSub(principal.Subject)
	if err != nil {
		return err
	}
	stack, err := authz.StackFromID(string(stackID))
	if err != nil {
		return err
	}
	result, err := authorizer.Check(ctx, authz.CheckRequest{Subject: subject, Stack: stack, Permission: permission})
	if err != nil {
		return err
	}
	if !result.Allowed {
		return denied
	}
	return nil
}

func listAccessibleStacks(ctx context.Context, authorizer authz.Authorizer, repository StackRepository, tenantID traits.TenantID) ([]traits.Stack, error) {
	principal, err := requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if isPlatformAdmin(principal) {
		return repository.ListStacks(ctx, tenantID)
	}
	if authorizer == nil {
		return nil, errors.New("authorization not configured")
	}
	subject, err := authz.SubjectFromKeycloakSub(principal.Subject)
	if err != nil {
		return nil, err
	}
	result, err := authorizer.ListAccessibleStacks(ctx, authz.ListAccessibleStacksRequest{Subject: subject, Permission: authz.PermissionView})
	if err != nil {
		return nil, err
	}
	stackIDs := make([]traits.StackID, 0, len(result.Stacks))
	for _, stack := range result.Stacks {
		stackIDs = append(stackIDs, traits.StackID(strings.TrimPrefix(stack.String(), "stack:")))
	}
	return repository.ListStacksByIDs(ctx, tenantID, stackIDs)
}

func (service *Service) authorizedStackTemplate(ctx context.Context, tenantID traits.TenantID, stackTemplateID traits.StackTemplateID, permission authz.Permission, denied error) (traits.StackTemplate, error) {
	stackTemplate, err := service.StackTemplates.GetStackTemplate(ctx, tenantID, stackTemplateID)
	if err != nil {
		return traits.StackTemplate{}, err
	}
	if err := authorizeStack(ctx, service.Authorizer, stackTemplate.StackID, permission, denied); err != nil {
		return traits.StackTemplate{}, err
	}
	return stackTemplate, nil
}

func (service *Service) authorizedTemplateRun(ctx context.Context, tenantID traits.TenantID, runID traits.TemplateRunID, permission authz.Permission, denied error) (traits.TemplateRun, error) {
	run, err := service.TemplateRuns.GetTemplateRun(ctx, tenantID, runID)
	if err != nil {
		return traits.TemplateRun{}, err
	}
	principal, err := requirePrincipal(ctx)
	if err != nil {
		return traits.TemplateRun{}, err
	}
	if isPlatformAdmin(principal) {
		return run, nil
	}
	if _, err := service.authorizedStackTemplate(ctx, tenantID, run.StackTemplateID, permission, denied); err != nil {
		return traits.TemplateRun{}, err
	}
	return run, nil
}
