package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/vishu42/tflive/internal/authn"
	"github.com/vishu42/tflive/internal/authorization"
	"github.com/vishu42/tflive/internal/domain"
)

func requirePrincipal(ctx context.Context) (authn.Principal, error) {
	principal, ok := authn.PrincipalFromContext(ctx)
	if !ok || principal.Subject == "" {
		return authn.Principal{}, ErrUnauthenticated
	}
	return principal, nil
}

// requirePrincipalAndAuthorizer checks both preconditions of an authorization
// decision and returns the principal the caller will ask about. It decides
// nothing itself.
//
// The order is deliberate: authentication is checked before configuration, so
// an anonymous caller never learns the deployment is misconfigured. A nil
// authorizer is ErrUnavailable rather than ErrForbidden, because "I cannot
// tell" and "you may not" are different answers and only one of them is true.
//
//	authenticated, authorizer wired  → principal, nil
//	no principal, or empty Subject   → ErrUnauthenticated       (even if unwired)
//	authenticated, authorizer nil    → error (never a decision)
func requirePrincipalAndAuthorizer(ctx context.Context, auth *authorization.Authorization) (authn.Principal, error) {
	principal, err := requirePrincipal(ctx)
	if err != nil {
		return authn.Principal{}, err
	}
	if auth == nil {
		return authn.Principal{}, fmt.Errorf("authorization not configured")
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
//	OpenFGA unreachable                                       → error (never a decision)
func authorizePlatform(ctx context.Context, auth *authorization.Authorization, relation authorization.Relation) error {
	allowed, err := checkPlatform(ctx, auth, relation)
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
func checkPlatform(ctx context.Context, auth *authorization.Authorization, relation authorization.Relation) (bool, error) {
	principal, err := requirePrincipalAndAuthorizer(ctx, auth)
	if err != nil {
		return false, err
	}
	return auth.Can(ctx, principal.Subject, relation, authorization.Platform)
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
//	OpenFGA unreachable                                        → error (never a decision)
func authorizeStack(ctx context.Context, auth *authorization.Authorization, stackID domain.StackID, relation authorization.Relation, denied error) error {
	principal, err := requirePrincipalAndAuthorizer(ctx, auth)
	if err != nil {
		return err
	}
	object, err := authorization.ObjectFromID(authorization.TypeStack, string(stackID))
	if errors.Is(err, authorization.ErrInvalidInput) {
		return denied
	}
	if err != nil {
		return err
	}
	allowed, err := auth.Can(ctx, principal.Subject, relation, object)
	if err != nil {
		return err
	}
	if !allowed {
		return denied
	}
	return nil
}

//nolint:gocognit // Authorization logic; split it only as a reviewed refactor.
func listAccessibleStacks(ctx context.Context, auth *authorization.Authorization, repository StackRepository, tenantID domain.TenantID) ([]domain.Stack, error) {
	principal, err := requirePrincipalAndAuthorizer(ctx, auth)
	if err != nil {
		return nil, err
	}
	// Unlike authorizeStack, the bypass is kept here rather than left to the
	// model: one Check replaces a BatchCheck fan-out over every stack in the
	// tenant, all of which the model would answer true anyway.
	administrator, err := checkPlatform(ctx, auth, authorization.RelationCanAdminister)
	if err != nil {
		return nil, err
	}
	if administrator {
		return repository.ListStacks(ctx, tenantID)
	}
	const pageSize = 50
	var cursor *StackPageCursor
	var accessible []domain.Stack
	for {
		candidates, err := repository.ListStacksPage(ctx, tenantID, cursor, pageSize)
		if err != nil {
			return nil, fmt.Errorf("list stack candidates: %w", err)
		}
		if len(candidates) == 0 {
			return accessible, nil
		}
		if len(candidates) > pageSize {
			return nil, fmt.Errorf("stack candidate page exceeds limit")
		}
		if cursor != nil && !stackPageOrderBefore(domain.Stack{ID: cursor.ID, CreatedAt: cursor.CreatedAt}, candidates[0]) {
			return nil, fmt.Errorf("stack candidate page did not advance")
		}
		for i := 1; i < len(candidates); i++ {
			if !stackPageOrderBefore(candidates[i-1], candidates[i]) {
				return nil, fmt.Errorf("stack candidate page is not strictly ordered")
			}
		}

		checks := make([]authorization.Check, len(candidates))
		for i, candidate := range candidates {
			object, err := authorization.ObjectFromID(authorization.TypeStack, string(candidate.ID))
			if errors.Is(err, authorization.ErrInvalidInput) {
				return nil, fmt.Errorf("stack candidate has invalid ID")
			}
			if err != nil {
				return nil, err
			}
			checks[i] = authorization.Check{Relation: authorization.RelationCanView, Object: object}
		}
		allowed, err := auth.CanAll(ctx, principal.Subject, checks)
		if err != nil {
			return nil, err
		}
		if len(allowed) != len(candidates) {
			return nil, fmt.Errorf("batch result count does not match stack candidates")
		}
		for i, visible := range allowed {
			if visible {
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

func stackPageOrderBefore(left, right domain.Stack) bool {
	if !left.CreatedAt.Equal(right.CreatedAt) {
		return left.CreatedAt.After(right.CreatedAt)
	}
	return left.ID > right.ID
}

func (service *Service) authorizedStackTemplate(ctx context.Context, tenantID domain.TenantID, stackTemplateID domain.StackTemplateID, relation authorization.Relation, denied error) (domain.StackTemplate, error) {
	// Fails an unauthenticated or unconfigured request before the repository
	// read; authorizeStack below re-derives the principal for the Check.
	if _, err := requirePrincipalAndAuthorizer(ctx, service.Authorization); err != nil {
		return domain.StackTemplate{}, err
	}
	stackTemplate, err := service.StackTemplates.GetStackTemplate(ctx, tenantID, stackTemplateID)
	if errors.Is(err, ErrNotFound) {
		return domain.StackTemplate{}, denied
	}
	if err != nil {
		return domain.StackTemplate{}, err
	}
	if _, err := authorization.ObjectFromID(authorization.TypeStack, string(stackTemplate.StackID)); errors.Is(err, authorization.ErrInvalidInput) {
		return domain.StackTemplate{}, fmt.Errorf("stack template has invalid owning stack ID")
	} else if err != nil {
		return domain.StackTemplate{}, err
	}
	if err := authorizeStack(ctx, service.Authorization, stackTemplate.StackID, relation, denied); err != nil {
		return domain.StackTemplate{}, err
	}
	return stackTemplate, nil
}

// operableStackTemplate is authorizedStackTemplate for the mutating commands:
// the same lookup, with the refusal recorded and wrapped the one way all of
// them want it. The read-only callers deliberately do not use it -- they refuse
// with ErrNotFound and write no audit event.
func (service *Service) operableStackTemplate(
	ctx context.Context,
	actor domain.UserID,
	tenantID domain.TenantID,
	stackTemplateID domain.StackTemplateID,
) (domain.StackTemplate, error) {
	stackTemplate, err := service.authorizedStackTemplate(ctx, tenantID, stackTemplateID, authorization.RelationCanOperate, ErrForbidden)
	if err != nil {
		// No StackID: the refusal can precede resolving which stack owns the
		// template, so naming one here would sometimes be a guess.
		service.auditFailedAccess(ctx, actor, tenantID, "")
		return domain.StackTemplate{}, fmt.Errorf("get stack template: %w", err)
	}
	return stackTemplate, nil
}

// PlatformCapabilities is the global half of what GET /v1/me projects. The
// field names keep the wire contract the web client already reads; what
// changed is where the answers come from.
type PlatformCapabilities struct {
	IsPlatformAdmin    bool
	CanCreateStack     bool
	CanPublishTemplate bool
}

// platformCapabilityRelations is the order a platform BatchCheck is built in,
// and platformCapabilitiesFrom is the decoding of its results. The two are
// positional and must agree, so they are kept adjacent rather than written out
// separately at the point of use.
var platformCapabilityRelations = []authorization.Relation{
	authorization.RelationCanAdminister,
	authorization.RelationCanCreateStack,
	authorization.RelationCanPublishTemplate,
}

func platformCapabilitiesFrom(results []bool) PlatformCapabilities {
	return PlatformCapabilities{
		IsPlatformAdmin:    results[0],
		CanCreateStack:     results[1],
		CanPublishTemplate: results[2],
	}
}

// ResolvePlatformCapabilities answers every global question in one BatchCheck.
// An unauthenticated or unconfigured caller is not an error here: /v1/me is
// reachable before any tuple exists, and a principal that holds nothing is a
// legitimate answer rather than a failure.
func ResolvePlatformCapabilities(ctx context.Context, auth *authorization.Authorization) (PlatformCapabilities, error) {
	results, err := batchCheckObject(ctx, auth, authorization.Platform, platformCapabilityRelations)
	if err != nil {
		return PlatformCapabilities{}, err
	}
	return platformCapabilitiesFrom(results), nil
}

type StackCapabilities struct {
	CanView         bool
	CanOperate      bool
	CanApprove      bool
	CanManageAccess bool
}

// stackCapabilityRelations and stackCapabilitiesFrom are the request order and
// the result decoding for a stack BatchCheck. Both resolvers below share them,
// so a reordering cannot reach one caller and miss the other -- which, while
// each resolver spelled the order out for itself, would have silently returned
// the wrong permissions rather than failing.
var stackCapabilityRelations = []authorization.Relation{
	authorization.RelationCanView,
	authorization.RelationCanOperate,
	authorization.RelationCanApprove,
	authorization.RelationCanManageAccess,
}

func stackCapabilitiesFrom(results []bool) StackCapabilities {
	return StackCapabilities{
		CanView:         results[0],
		CanOperate:      results[1],
		CanApprove:      results[2],
		CanManageAccess: results[3],
	}
}

// batchCheckObject asks every relation about one object in one round trip and
// returns the answers in request order. A response of the wrong length fails
// the call: the callers decode it positionally, so a short or long result would
// otherwise be read as the wrong permission rather than as a failure.
func batchCheckObject(
	ctx context.Context,
	auth *authorization.Authorization,
	object authorization.Object,
	relations []authorization.Relation,
) ([]bool, error) {
	principal, err := requirePrincipalAndAuthorizer(ctx, auth)
	if err != nil {
		return nil, err
	}
	checks := make([]authorization.Check, len(relations))
	for i, relation := range relations {
		checks[i] = authorization.Check{Relation: relation, Object: object}
	}
	results, err := auth.CanAll(ctx, principal.Subject, checks)
	if err != nil {
		return nil, err
	}
	if len(results) != len(checks) {
		return nil, fmt.Errorf("batch result count does not match checks")
	}
	return results, nil
}

func ResolveStackCapabilities(ctx context.Context, auth *authorization.Authorization, stackID domain.StackID) (StackCapabilities, error) {
	// Checked before the stack ID is parsed, so an anonymous caller still gets
	// ErrUnauthenticated rather than a complaint about the ID. batchCheckObject
	// repeats this; it is a pure read of the context.
	if _, err := requirePrincipalAndAuthorizer(ctx, auth); err != nil {
		return StackCapabilities{}, err
	}
	object, err := authorization.ObjectFromID(authorization.TypeStack, string(stackID))
	if err != nil {
		return StackCapabilities{}, err
	}
	results, err := batchCheckObject(ctx, auth, object, stackCapabilityRelations)
	if err != nil {
		return StackCapabilities{}, err
	}
	return stackCapabilitiesFrom(results), nil
}

func ResolveStacksCapabilities(ctx context.Context, auth *authorization.Authorization, stacks []domain.Stack) (map[domain.StackID]StackCapabilities, error) {
	if len(stacks) == 0 {
		return map[domain.StackID]StackCapabilities{}, nil
	}
	principal, err := requirePrincipalAndAuthorizer(ctx, auth)
	if err != nil {
		return nil, err
	}
	administrator, err := checkPlatform(ctx, auth, authorization.RelationCanAdminister)
	if err != nil {
		return nil, err
	}
	// Kept for the same reason as listAccessibleStacks: stacks arrives from an
	// unbounded ListStacks, so this collapses four checks per stack in the
	// tenant into one.
	if administrator {
		all := StackCapabilities{CanView: true, CanOperate: true, CanApprove: true, CanManageAccess: true}
		result := make(map[domain.StackID]StackCapabilities, len(stacks))
		for _, s := range stacks {
			result[s.ID] = all
		}
		return result, nil
	}
	checks := make([]authorization.Check, 0, len(stacks)*len(stackCapabilityRelations))
	for _, s := range stacks {
		object, err := authorization.ObjectFromID(authorization.TypeStack, string(s.ID))
		if err != nil {
			return nil, err
		}
		for _, relation := range stackCapabilityRelations {
			checks = append(checks, authorization.Check{Relation: relation, Object: object})
		}
	}
	results, err := auth.CanAll(ctx, principal.Subject, checks)
	if err != nil {
		return nil, err
	}
	if len(results) != len(checks) {
		return nil, fmt.Errorf("batch result count does not match checks")
	}
	// One stack's relations occupy one contiguous run, in the order they were
	// appended above, so each run decodes with the same function the
	// single-stack resolver uses.
	caps := make(map[domain.StackID]StackCapabilities, len(stacks))
	for i, s := range stacks {
		base := i * len(stackCapabilityRelations)
		caps[s.ID] = stackCapabilitiesFrom(results[base : base+len(stackCapabilityRelations)])
	}
	return caps, nil
}

func (service *Service) authorizedTemplateRun(ctx context.Context, tenantID domain.TenantID, runID domain.TemplateRunID, relation authorization.Relation, denied error) (domain.TemplateRun, error) {
	if _, err := requirePrincipalAndAuthorizer(ctx, service.Authorization); err != nil {
		return domain.TemplateRun{}, err
	}
	run, err := service.TemplateRuns.GetTemplateRun(ctx, tenantID, runID)
	if errors.Is(err, ErrNotFound) {
		return domain.TemplateRun{}, denied
	}
	if err != nil {
		return domain.TemplateRun{}, err
	}
	if _, err := service.authorizedStackTemplate(ctx, tenantID, run.StackTemplateID, relation, denied); err != nil {
		return domain.TemplateRun{}, err
	}
	return run, nil
}
