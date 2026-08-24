package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/vishu42/tflive/internal/authn"
	"github.com/vishu42/tflive/internal/authz"
	"github.com/vishu42/tflive/internal/traits"
)

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

// authorizePlatform answers "may the request's principal do relation to the
// platform singleton?" -- the global questions that used to be answered from
// Keycloak realm role claims.
//
// The relation is always a capability (can_create_stack, can_read_template),
// never a tier. Which tier satisfies a capability is the model's business, so
// re-tiering one is a model deploy that never touches this package.
//
//	principal holds platform editor, RelationCanCreateStack   → nil
//	principal holds platform viewer, RelationCanCreateStack   → ErrForbidden
//	principal holds platform viewer, RelationCanReadTemplate  → nil
//	OpenFGA unreachable                                       → authz.ErrUnavailable
func authorizePlatform(ctx context.Context, authorizer authz.Authorizer, relation authz.Relation) error {
	allowed, err := checkPlatform(ctx, authorizer, relation)
	if err != nil {
		return err
	}
	if !allowed {
		return ErrForbidden
	}
	return nil
}

// checkPlatform is authorizePlatform for callers that branch on the answer
// rather than refusing, which is the administrator list bypass below.
func checkPlatform(ctx context.Context, authorizer authz.Authorizer, relation authz.Relation) (bool, error) {
	principal, err := requireAuthorizer(ctx, authorizer)
	if err != nil {
		return false, err
	}
	subject, err := authz.SubjectFromOIDCSub(principal.Subject)
	if err != nil {
		return false, err
	}
	result, err := authorizer.Check(ctx, authz.CheckRequest{
		Subject:  subject,
		Relation: relation,
		Object:   authz.Platform,
	})
	if err != nil {
		return false, err
	}
	return result.Allowed, nil
}

// authorizeStack answers "may the request's principal do relation to this
// stack?", returning denied rather than a generic error so a caller controls
// whether the client sees 403 or 404.
//
// A platform administrator is not special-cased here. The model derives it:
// can_manage_access includes "can_administer from parent", so the same Check
// that answers for an owner answers for an administrator.
//
//	stack owned by alice, principal alice, RelationCanOperate  → nil
//	stack owned by alice, principal bob, RelationCanOperate    → denied
//	principal holds platform admin, any stack                  → nil (from the model)
//	stackID = "bad:id"                                         → denied
//	OpenFGA unreachable                                        → authz.ErrUnavailable
func authorizeStack(ctx context.Context, authorizer authz.Authorizer, stackID traits.StackID, relation authz.Relation, denied error) error {
	principal, err := requireAuthorizer(ctx, authorizer)
	if err != nil {
		return err
	}
	object, err := authz.ObjectFromID(authz.TypeStack, string(stackID))
	if errors.Is(err, authz.ErrInvalidInput) {
		return denied
	}
	if err != nil {
		return err
	}
	subject, err := authz.SubjectFromOIDCSub(principal.Subject)
	if err != nil {
		return err
	}
	result, err := authorizer.Check(ctx, authz.CheckRequest{Subject: subject, Relation: relation, Object: object})
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
	// Unlike authorizeStack, the bypass is kept here rather than left to the
	// model: one Check replaces a BatchCheck fan-out over every stack in the
	// tenant, all of which the model would answer true anyway.
	administrator, err := checkPlatform(ctx, authorizer, authz.RelationCanAdminister)
	if err != nil {
		return nil, err
	}
	if administrator {
		return repository.ListStacks(ctx, tenantID)
	}
	subject, err := authz.SubjectFromOIDCSub(principal.Subject)
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
			object, err := authz.ObjectFromID(authz.TypeStack, string(candidate.ID))
			if errors.Is(err, authz.ErrInvalidInput) {
				return nil, fmt.Errorf("%w: stack candidate has invalid ID", authz.ErrMalformedResponse)
			}
			if err != nil {
				return nil, err
			}
			checks[i] = authz.CheckRequest{Subject: subject, Relation: authz.RelationCanView, Object: object}
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

func (service *Service) authorizedStackTemplate(ctx context.Context, tenantID traits.TenantID, stackTemplateID traits.StackTemplateID, relation authz.Relation, denied error) (traits.StackTemplate, error) {
	// Fails an unauthenticated or unconfigured request before the repository
	// read; authorizeStack below re-derives the principal for the Check.
	if _, err := requireAuthorizer(ctx, service.Authorizer); err != nil {
		return traits.StackTemplate{}, err
	}
	stackTemplate, err := service.StackTemplates.GetStackTemplate(ctx, tenantID, stackTemplateID)
	if errors.Is(err, ErrNotFound) {
		return traits.StackTemplate{}, denied
	}
	if err != nil {
		return traits.StackTemplate{}, err
	}
	if _, err := authz.ObjectFromID(authz.TypeStack, string(stackTemplate.StackID)); errors.Is(err, authz.ErrInvalidInput) {
		return traits.StackTemplate{}, fmt.Errorf("%w: stack template has invalid owning stack ID", authz.ErrMalformedResponse)
	} else if err != nil {
		return traits.StackTemplate{}, err
	}
	if err := authorizeStack(ctx, service.Authorizer, stackTemplate.StackID, relation, denied); err != nil {
		return traits.StackTemplate{}, err
	}
	return stackTemplate, nil
}

// PlatformCapabilities is the global half of what GET /v1/me projects. The
// field names keep the wire contract the web client already reads; what
// changed is where the answers come from.
type PlatformCapabilities struct {
	IsPlatformAdmin bool
	CanCreateStack  bool
}

// ResolvePlatformCapabilities answers both global questions in one BatchCheck.
// An unauthenticated or unconfigured caller is not an error here: /v1/me is
// reachable before any tuple exists, and a principal that holds nothing is a
// legitimate answer rather than a failure.
func ResolvePlatformCapabilities(ctx context.Context, authorizer authz.Authorizer) (PlatformCapabilities, error) {
	principal, err := requireAuthorizer(ctx, authorizer)
	if err != nil {
		return PlatformCapabilities{}, err
	}
	subject, err := authz.SubjectFromOIDCSub(principal.Subject)
	if err != nil {
		return PlatformCapabilities{}, err
	}
	relations := []authz.Relation{authz.RelationCanAdminister, authz.RelationCanCreateStack}
	checks := make([]authz.CheckRequest, len(relations))
	for i, relation := range relations {
		checks[i] = authz.CheckRequest{Subject: subject, Relation: relation, Object: authz.Platform}
	}
	result, err := authorizer.BatchCheck(ctx, authz.BatchCheckRequest{Checks: checks})
	if err != nil {
		return PlatformCapabilities{}, err
	}
	if len(result.Results) != len(relations) {
		return PlatformCapabilities{}, fmt.Errorf("%w: batch result count does not match platform capabilities", authz.ErrMalformedResponse)
	}
	return PlatformCapabilities{
		IsPlatformAdmin: result.Results[0].Allowed,
		CanCreateStack:  result.Results[1].Allowed,
	}, nil
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
	subject, err := authz.SubjectFromOIDCSub(principal.Subject)
	if err != nil {
		return StackCapabilities{}, err
	}
	object, err := authz.ObjectFromID(authz.TypeStack, string(stackID))
	if err != nil {
		return StackCapabilities{}, err
	}

	relations := []authz.Relation{
		authz.RelationCanView, authz.RelationCanOperate, authz.RelationCanApprove, authz.RelationCanManageAccess,
	}
	checks := make([]authz.CheckRequest, len(relations))
	for i, relation := range relations {
		checks[i] = authz.CheckRequest{Subject: subject, Relation: relation, Object: object}
	}
	result, err := authorizer.BatchCheck(ctx, authz.BatchCheckRequest{Checks: checks})
	if err != nil {
		return StackCapabilities{}, err
	}
	if len(result.Results) != len(relations) {
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
	administrator, err := checkPlatform(ctx, authorizer, authz.RelationCanAdminister)
	if err != nil {
		return nil, err
	}
	// Kept for the same reason as listAccessibleStacks: stacks arrives from an
	// unbounded ListStacks, so this collapses four checks per stack in the
	// tenant into one.
	if administrator {
		all := StackCapabilities{CanView: true, CanOperate: true, CanApprove: true, CanManageAccess: true}
		result := make(map[traits.StackID]StackCapabilities, len(stacks))
		for _, s := range stacks {
			result[s.ID] = all
		}
		return result, nil
	}
	subject, err := authz.SubjectFromOIDCSub(principal.Subject)
	if err != nil {
		return nil, err
	}
	relations := []authz.Relation{
		authz.RelationCanView, authz.RelationCanOperate, authz.RelationCanApprove, authz.RelationCanManageAccess,
	}
	checks := make([]authz.CheckRequest, 0, len(stacks)*len(relations))
	for _, s := range stacks {
		object, err := authz.ObjectFromID(authz.TypeStack, string(s.ID))
		if err != nil {
			return nil, err
		}
		for _, relation := range relations {
			checks = append(checks, authz.CheckRequest{Subject: subject, Relation: relation, Object: object})
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
		base := i * len(relations)
		caps[s.ID] = StackCapabilities{
			CanView:         result.Results[base+0].Allowed,
			CanOperate:      result.Results[base+1].Allowed,
			CanApprove:      result.Results[base+2].Allowed,
			CanManageAccess: result.Results[base+3].Allowed,
		}
	}
	return caps, nil
}

func (service *Service) authorizedTemplateRun(ctx context.Context, tenantID traits.TenantID, runID traits.TemplateRunID, relation authz.Relation, denied error) (traits.TemplateRun, error) {
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
	if _, err := service.authorizedStackTemplate(ctx, tenantID, run.StackTemplateID, relation, denied); err != nil {
		return traits.TemplateRun{}, err
	}
	return run, nil
}
