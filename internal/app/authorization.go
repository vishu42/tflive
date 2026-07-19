package app

import (
	"context"
	"errors"
	"fmt"

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
	const pageSize = 50
	var cursor *StackPageCursor
	var accessible []traits.Stack
	for {
		candidates, err := repository.ListStacksPage(ctx, tenantID, cursor, pageSize)
		if err != nil {
			return nil, fmt.Errorf("list stack candidates: %w", err)
		}
		if len(candidates) == 0 {
			return accessible, nil
		}

		checks := make([]authz.CheckRequest, len(candidates))
		for i, candidate := range candidates {
			stack, err := authz.StackFromID(string(candidate.ID))
			if err != nil {
				return nil, err
			}
			checks[i] = authz.CheckRequest{Subject: subject, Stack: stack, Permission: authz.PermissionView}
		}
		result, err := authorizer.BatchCheck(ctx, authz.BatchCheckRequest{Checks: checks})
		if err != nil {
			return nil, err
		}
		if len(result.Results) != len(candidates) {
			return nil, fmt.Errorf("%w: batch result count does not match stack candidates", authz.ErrMalformedResponse)
		}
		for i, decision := range result.Results {
			if decision.Allowed {
				accessible = append(accessible, candidates[i])
			}
		}
		if len(candidates) < pageSize {
			return accessible, nil
		}
		last := candidates[len(candidates)-1]
		cursor = &StackPageCursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}
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
