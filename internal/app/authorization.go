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

func requireAuthorizer(ctx context.Context, authorizer authz.Authorizer) (authn.Principal, error) {
	principal, err := requirePrincipal(ctx)
	if err != nil {
		return authn.Principal{}, err
	}
	if authorizer == nil {
		return authn.Principal{}, fmt.Errorf("%w: authorization not configured", authz.ErrUnavailable)
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
	principal, err := requireAuthorizer(ctx, authorizer)
	if err != nil {
		return err
	}
	stack, err := authz.StackFromID(string(stackID))
	if errors.Is(err, authz.ErrInvalidInput) {
		return denied
	}
	if err != nil {
		return err
	}
	if isPlatformAdmin(principal) {
		return nil
	}
	subject, err := authz.SubjectFromKeycloakSub(principal.Subject)
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
	principal, err := requireAuthorizer(ctx, authorizer)
	if err != nil {
		return nil, err
	}
	if isPlatformAdmin(principal) {
		return repository.ListStacks(ctx, tenantID)
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
		if len(candidates) > pageSize {
			return nil, fmt.Errorf("%w: stack candidate page exceeds limit", authz.ErrMalformedResponse)
		}
		if cursor != nil && !stackPageOrderBefore(traits.Stack{ID: cursor.ID, CreatedAt: cursor.CreatedAt}, candidates[0]) {
			return nil, fmt.Errorf("%w: stack candidate page did not advance", authz.ErrMalformedResponse)
		}
		for i := 1; i < len(candidates); i++ {
			if !stackPageOrderBefore(candidates[i-1], candidates[i]) {
				return nil, fmt.Errorf("%w: stack candidate page is not strictly ordered", authz.ErrMalformedResponse)
			}
		}

		checks := make([]authz.CheckRequest, len(candidates))
		for i, candidate := range candidates {
			stack, err := authz.StackFromID(string(candidate.ID))
			if errors.Is(err, authz.ErrInvalidInput) {
				return nil, fmt.Errorf("%w: stack candidate has invalid ID", authz.ErrMalformedResponse)
			}
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

func stackPageOrderBefore(left, right traits.Stack) bool {
	if !left.CreatedAt.Equal(right.CreatedAt) {
		return left.CreatedAt.After(right.CreatedAt)
	}
	return left.ID > right.ID
}

func (service *Service) authorizedStackTemplate(ctx context.Context, tenantID traits.TenantID, stackTemplateID traits.StackTemplateID, permission authz.Permission, denied error) (traits.StackTemplate, error) {
	principal, err := requireAuthorizer(ctx, service.Authorizer)
	if err != nil {
		return traits.StackTemplate{}, err
	}
	stackTemplate, err := service.StackTemplates.GetStackTemplate(ctx, tenantID, stackTemplateID)
	if errors.Is(err, ErrNotFound) {
		return traits.StackTemplate{}, denied
	}
	if err != nil {
		return traits.StackTemplate{}, err
	}
	if _, err := authz.StackFromID(string(stackTemplate.StackID)); errors.Is(err, authz.ErrInvalidInput) {
		return traits.StackTemplate{}, fmt.Errorf("%w: stack template has invalid owning stack ID", authz.ErrMalformedResponse)
	} else if err != nil {
		return traits.StackTemplate{}, err
	}
	if isPlatformAdmin(principal) {
		return stackTemplate, nil
	}
	if err := authorizeStack(ctx, service.Authorizer, stackTemplate.StackID, permission, denied); err != nil {
		return traits.StackTemplate{}, err
	}
	return stackTemplate, nil
}

type StackCapabilities struct {
	CanView         bool
	CanOperate      bool
	CanApprove      bool
	CanManageAccess bool
}

func ResolveStackCapabilities(ctx context.Context, authorizer authz.Authorizer, stackID traits.StackID) (StackCapabilities, error) {
	principal, err := requireAuthorizer(ctx, authorizer)
	if err != nil {
		return StackCapabilities{}, err
	}
	if isPlatformAdmin(principal) {
		return StackCapabilities{CanView: true, CanOperate: true, CanApprove: true, CanManageAccess: true}, nil
	}
	subject, err := authz.SubjectFromKeycloakSub(principal.Subject)
	if err != nil {
		return StackCapabilities{}, err
	}
	stack, err := authz.StackFromID(string(stackID))
	if err != nil {
		return StackCapabilities{}, err
	}

	permissions := []authz.Permission{
		authz.PermissionView, authz.PermissionOperate, authz.PermissionApprove, authz.PermissionManageAccess,
	}
	checks := make([]authz.CheckRequest, len(permissions))
	for i, perm := range permissions {
		checks[i] = authz.CheckRequest{Subject: subject, Stack: stack, Permission: perm}
	}
	result, err := authorizer.BatchCheck(ctx, authz.BatchCheckRequest{Checks: checks})
	if err != nil {
		return StackCapabilities{}, err
	}
	if len(result.Results) != len(permissions) {
		return StackCapabilities{}, fmt.Errorf("%w: batch result count does not match permissions", authz.ErrMalformedResponse)
	}
	return StackCapabilities{
		CanView:         result.Results[0].Allowed,
		CanOperate:      result.Results[1].Allowed,
		CanApprove:      result.Results[2].Allowed,
		CanManageAccess: result.Results[3].Allowed,
	}, nil
}

func ResolveStacksCapabilities(ctx context.Context, authorizer authz.Authorizer, stacks []traits.Stack) (map[traits.StackID]StackCapabilities, error) {
	if len(stacks) == 0 {
		return map[traits.StackID]StackCapabilities{}, nil
	}
	principal, err := requireAuthorizer(ctx, authorizer)
	if err != nil {
		return nil, err
	}
	if isPlatformAdmin(principal) {
		all := StackCapabilities{CanView: true, CanOperate: true, CanApprove: true, CanManageAccess: true}
		result := make(map[traits.StackID]StackCapabilities, len(stacks))
		for _, s := range stacks {
			result[s.ID] = all
		}
		return result, nil
	}
	subject, err := authz.SubjectFromKeycloakSub(principal.Subject)
	if err != nil {
		return nil, err
	}
	permissions := []authz.Permission{
		authz.PermissionView, authz.PermissionOperate, authz.PermissionApprove, authz.PermissionManageAccess,
	}
	checks := make([]authz.CheckRequest, 0, len(stacks)*len(permissions))
	for _, s := range stacks {
		stack, err := authz.StackFromID(string(s.ID))
		if err != nil {
			return nil, err
		}
		for _, perm := range permissions {
			checks = append(checks, authz.CheckRequest{Subject: subject, Stack: stack, Permission: perm})
		}
	}
	result, err := authorizer.BatchCheck(ctx, authz.BatchCheckRequest{Checks: checks})
	if err != nil {
		return nil, err
	}
	if len(result.Results) != len(checks) {
		return nil, fmt.Errorf("%w: batch result count does not match checks", authz.ErrMalformedResponse)
	}
	caps := make(map[traits.StackID]StackCapabilities, len(stacks))
	for i, s := range stacks {
		base := i * len(permissions)
		caps[s.ID] = StackCapabilities{
			CanView:         result.Results[base+0].Allowed,
			CanOperate:      result.Results[base+1].Allowed,
			CanApprove:      result.Results[base+2].Allowed,
			CanManageAccess: result.Results[base+3].Allowed,
		}
	}
	return caps, nil
}

func (service *Service) authorizedTemplateRun(ctx context.Context, tenantID traits.TenantID, runID traits.TemplateRunID, permission authz.Permission, denied error) (traits.TemplateRun, error) {
	if _, err := requireAuthorizer(ctx, service.Authorizer); err != nil {
		return traits.TemplateRun{}, err
	}
	run, err := service.TemplateRuns.GetTemplateRun(ctx, tenantID, runID)
	if errors.Is(err, ErrNotFound) {
		return traits.TemplateRun{}, denied
	}
	if err != nil {
		return traits.TemplateRun{}, err
	}
	if _, err := service.authorizedStackTemplate(ctx, tenantID, run.StackTemplateID, permission, denied); err != nil {
		return traits.TemplateRun{}, err
	}
	return run, nil
}
