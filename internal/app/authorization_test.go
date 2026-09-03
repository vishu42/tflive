package app

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/vishu42/tflive/internal/authn"
	"github.com/vishu42/tflive/internal/authz"
	"github.com/vishu42/tflive/internal/traits"
)

func TestGetStackChecksViewPermission(t *testing.T) {
	t.Parallel()

	authorizer := &permissionAuthorizer{allowed: true}
	stacks := &recordingStackRepository{view: StackView{Stack: traits.Stack{ID: "stack_123"}}}
	service := NewService(Service{Authorizer: authorizer, Stacks: stacks})
	ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: "user_123"})

	if _, err := service.GetStack(ctx, GetStackCommand{TenantID: "tenant_123", StackID: "stack_123"}); err != nil {
		t.Fatalf("GetStack() error = %v", err)
	}
	if got := authorizer.check.Relation; got != authz.RelationCanView {
		t.Fatalf("permission = %q, want %q", got, authz.RelationCanView)
	}
}

func TestGetStackDenialReturnsNotFound(t *testing.T) {
	t.Parallel()

	service := NewService(Service{
		Authorizer: &permissionAuthorizer{},
		Stacks:     &recordingStackRepository{},
	})
	ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: "user_123"})

	_, err := service.GetStack(ctx, GetStackCommand{TenantID: "tenant_123", StackID: "stack_123"})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}
}

func TestListStacksBatchesCompleteTenantScan(t *testing.T) {
	t.Parallel()

	all := make([]traits.Stack, 55)
	for i := range all {
		all[i] = traits.Stack{ID: traits.StackID(fmt.Sprintf("stack_%02d", i)), CreatedAt: time.Unix(int64(100-i), 0)}
	}
	repository := &pagedStackRepository{stacks: all}
	authorizer := &permissionAuthorizer{batchDecision: func(index int) bool { return index%2 == 0 }}
	service := NewService(Service{Authorizer: authorizer, Stacks: repository})
	ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: "user_123"})

	stacks, err := service.ListStacks(ctx, ListStacksCommand{TenantID: "tenant_123"})
	if err != nil {
		t.Fatalf("ListStacks() error = %v", err)
	}
	if len(stacks) != 28 {
		t.Fatalf("len(stacks) = %d, want 28", len(stacks))
	}
	if !reflect.DeepEqual(authorizer.batchSizes, []int{50, 5}) {
		t.Fatalf("batch sizes = %#v, want [50 5]", authorizer.batchSizes)
	}
	if repository.pageCalls != 2 {
		t.Fatalf("page calls = %d, want 2", repository.pageCalls)
	}
}

func TestListStacksReturnsNoPartialResults(t *testing.T) {
	t.Parallel()

	all := make([]traits.Stack, 55)
	for i := range all {
		all[i] = traits.Stack{ID: traits.StackID(fmt.Sprintf("stack_%02d", i)), CreatedAt: time.Unix(int64(100-i), 0)}
	}
	authorizer := &permissionAuthorizer{failBatch: 2, batchErr: authz.ErrUnavailable}
	service := NewService(Service{Authorizer: authorizer, Stacks: &pagedStackRepository{stacks: all}})
	ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: "user_123"})

	stacks, err := service.ListStacks(ctx, ListStacksCommand{TenantID: "tenant_123"})
	if !errors.Is(err, authz.ErrUnavailable) {
		t.Fatalf("error = %v, want ErrUnavailable", err)
	}
	if stacks != nil {
		t.Fatalf("stacks = %#v, want nil partial result", stacks)
	}
}

func TestListStacksRejectsMismatchedBatchResults(t *testing.T) {
	t.Parallel()

	authorizer := &permissionAuthorizer{truncateBatchResult: true}
	service := NewService(Service{Authorizer: authorizer, Stacks: &pagedStackRepository{stacks: []traits.Stack{{ID: "stack_1"}}}})
	ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: "user_123"})

	_, err := service.ListStacks(ctx, ListStacksCommand{TenantID: "tenant_123"})
	if !errors.Is(err, authz.ErrMalformedResponse) {
		t.Fatalf("error = %v, want ErrMalformedResponse", err)
	}
}

func TestListStacksSkipsAuthorizationForEmptyTenant(t *testing.T) {
	t.Parallel()

	authorizer := &permissionAuthorizer{}
	service := NewService(Service{Authorizer: authorizer, Stacks: &pagedStackRepository{}})
	ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: "user_123"})

	stacks, err := service.ListStacks(ctx, ListStacksCommand{TenantID: "tenant_123"})
	if err != nil {
		t.Fatalf("ListStacks() error = %v", err)
	}
	if len(stacks) != 0 || len(authorizer.batchSizes) != 0 {
		t.Fatalf("stacks=%#v batch sizes=%#v", stacks, authorizer.batchSizes)
	}
}

func TestListStacksRejectsNonAdvancingPage(t *testing.T) {
	t.Parallel()

	all := make([]traits.Stack, 50)
	for i := range all {
		all[i] = traits.Stack{ID: traits.StackID(fmt.Sprintf("stack_%02d", 50-i)), CreatedAt: time.Unix(100, 0)}
	}
	service := NewService(Service{
		Authorizer: &permissionAuthorizer{},
		Stacks:     &pagedStackRepository{stacks: all, repeatPage: true},
	})
	ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: "user_123"})

	_, err := service.ListStacks(ctx, ListStacksCommand{TenantID: "tenant_123"})
	if !errors.Is(err, authz.ErrMalformedResponse) {
		t.Fatalf("error = %v, want ErrMalformedResponse", err)
	}
}

func TestListStacksRejectsOversizedPage(t *testing.T) {
	t.Parallel()

	all := make([]traits.Stack, 51)
	for i := range all {
		all[i] = traits.Stack{ID: traits.StackID(fmt.Sprintf("stack_%02d", 51-i)), CreatedAt: time.Unix(100, 0)}
	}
	service := NewService(Service{Authorizer: &permissionAuthorizer{}, Stacks: &pagedStackRepository{stacks: all, ignoreLimit: true}})
	ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: "user_123"})

	_, err := service.ListStacks(ctx, ListStacksCommand{TenantID: "tenant_123"})
	if !errors.Is(err, authz.ErrMalformedResponse) {
		t.Fatalf("error = %v, want ErrMalformedResponse", err)
	}
}

func TestListStacksRejectsOutOfOrderPage(t *testing.T) {
	t.Parallel()

	stacks := []traits.Stack{
		{ID: "stack_a", CreatedAt: time.Unix(100, 0)},
		{ID: "stack_b", CreatedAt: time.Unix(100, 0)},
	}
	service := NewService(Service{Authorizer: &permissionAuthorizer{}, Stacks: &pagedStackRepository{stacks: stacks}})
	ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: "user_123"})

	_, err := service.ListStacks(ctx, ListStacksCommand{TenantID: "tenant_123"})
	if !errors.Is(err, authz.ErrMalformedResponse) {
		t.Fatalf("error = %v, want ErrMalformedResponse", err)
	}
}

func TestListStacksRejectsMalformedCandidateIDAsDependencyFailure(t *testing.T) {
	t.Parallel()

	service := NewService(Service{Authorizer: &permissionAuthorizer{}, Stacks: &pagedStackRepository{stacks: []traits.Stack{{ID: "bad:id"}}}})
	ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: "user_123"})

	_, err := service.ListStacks(ctx, ListStacksCommand{TenantID: "tenant_123"})
	if !errors.Is(err, authz.ErrMalformedResponse) {
		t.Fatalf("error = %v, want ErrMalformedResponse", err)
	}
}

func TestInheritedAuthorizationRequiresPrincipalBeforeRepositoryRead(t *testing.T) {
	t.Parallel()

	runs := &recordingTemplateRunRepository{}
	service := NewService(Service{Authorizer: &permissionAuthorizer{}, TemplateRuns: runs})

	_, err := service.GetTemplateRun(context.Background(), GetTemplateRunCommand{TenantID: "tenant_123", RunID: "run_123"})
	if !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("error = %v, want ErrUnauthenticated", err)
	}
	if runs.gotGetRunID != "" {
		t.Fatalf("repository run ID = %q, want no lookup", runs.gotGetRunID)
	}
}

func TestStartTemplateRunDenialReturnsForbiddenBeforeMutation(t *testing.T) {
	t.Parallel()

	templates := &recordingStackTemplateRepository{stackTemplate: traits.StackTemplate{
		ID:       "stack_template_123",
		TenantID: "tenant_123",
		StackID:  "stack_123",
	}}
	service := NewService(Service{
		Authorizer:     &permissionAuthorizer{},
		StackTemplates: templates,
	})
	ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: "user_123"})

	_, err := service.StartTemplateRun(ctx, StartTemplateRunCommand{
		TenantID:        "tenant_123",
		StackTemplateID: "stack_template_123",
		Operation:       traits.OperationPlan,
	})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("error = %v, want ErrForbidden", err)
	}
}

func TestInheritedResourceMissingMutationReturnsForbidden(t *testing.T) {
	t.Parallel()

	service := NewService(Service{Authorizer: &permissionAuthorizer{allowed: true}, TemplateRuns: &recordingTemplateRunRepository{getErr: ErrNotFound}})

	err := service.ApproveRun(authenticatedContext(), ApproveRunCommand{TenantID: "tenant_123", RunID: "missing_run"})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("error = %v, want ErrForbidden", err)
	}
}

// stackCapabilityRelationsInOrder is the exact request order
// ResolveStackCapabilities and ResolveStacksCapabilities are required to
// send checks in. StackCapabilities' field build (CanView: Results[0],
// CanOperate: Results[1], CanApprove: Results[2], CanManageAccess:
// Results[3]) is a fixed literal that trusts the checks arrived in this
// order — it does not re-derive position from the Relation each result
// answers. A test that only inspects the boolean result of a bare
// index-driven double cannot tell the two positions apart on its own,
// because a permuted relations slice still produces the same number of
// checks at the same indices; only inspecting the recorded Relation at each
// position exposes the swap.
var stackCapabilityRelationsInOrder = []authz.Relation{
	authz.RelationCanView, authz.RelationCanOperate, authz.RelationCanApprove, authz.RelationCanManageAccess,
}

var stackCapabilityRelationNames = []string{"can_view", "can_operate", "can_approve", "can_manage_access"}

// capabilityWithOnly builds the StackCapabilities that has exactly one field
// set, keyed by its position in stackCapabilityRelationsInOrder.
func capabilityWithOnly(relationIndex int) StackCapabilities {
	switch relationIndex {
	case 0:
		return StackCapabilities{CanView: true}
	case 1:
		return StackCapabilities{CanOperate: true}
	case 2:
		return StackCapabilities{CanApprove: true}
	case 3:
		return StackCapabilities{CanManageAccess: true}
	default:
		panic("capabilityWithOnly: relationIndex out of range")
	}
}

// assertStackCapabilityRelationOrder fails the test unless authorizer
// recorded checks whose Relation values run through
// stackCapabilityRelationsInOrder, repeated once per stack in stacks'
// order — the exact request shape ResolveStackCapabilities and
// ResolveStacksCapabilities must produce. This is what actually catches a
// swap inside the relations slice literal: the boolean mapping assertions
// alone cannot, because they never look at which Relation a result
// answered.
func assertStackCapabilityRelationOrder(t *testing.T, authorizer *permissionAuthorizer, stacks int) {
	t.Helper()

	want := stacks * len(stackCapabilityRelationsInOrder)
	if len(authorizer.batchChecks) != want {
		t.Fatalf("batch checks sent = %d, want %d", len(authorizer.batchChecks), want)
	}
	for i, check := range authorizer.batchChecks {
		want := stackCapabilityRelationsInOrder[i%len(stackCapabilityRelationsInOrder)]
		if check.Relation != want {
			t.Fatalf("batchChecks[%d].Relation = %q, want %q (relations slice order changed)", i, check.Relation, want)
		}
	}
}

// Pins the positional mapping from BatchCheck's ordered results onto
// StackCapabilities fields, and the request order that mapping depends on.
// A double that allows everything or denies everything would pass under any
// permutation of the relations slice, so this allows exactly one relation at
// a time and asserts exactly the matching capability flips true while the
// other three stay false — for each of the four positions — and separately
// asserts the checks were sent in the exact relation order the field build
// assumes.
func TestResolveStackCapabilitiesMapsEachRelationPositionally(t *testing.T) {
	t.Parallel()

	for relationIndex, name := range stackCapabilityRelationNames {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			authorizer := &permissionAuthorizer{batchDecision: func(i int) bool { return i == relationIndex }}
			ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: "user_123"})

			got, err := ResolveStackCapabilities(ctx, authorizer, "stack_123")
			if err != nil {
				t.Fatalf("ResolveStackCapabilities() error = %v", err)
			}
			if want := capabilityWithOnly(relationIndex); got != want {
				t.Fatalf("ResolveStackCapabilities() = %+v, want %+v", got, want)
			}
			assertStackCapabilityRelationOrder(t, authorizer, 1)
		})
	}
}

// Pins the positional mapping ResolveStacksCapabilities applies to a single
// flattened BatchCheck result, including the base := i * len(relations)
// stride that walks from one stack's four results to the next, and the
// request order that mapping depends on. Two stacks are required to
// exercise the stride at all; a single stack would leave base == 0 for
// every case. This allows exactly one (stack, relation) pair at a time and
// asserts exactly the matching capability flips true, on the matching stack
// only, for every stack/relation combination — and separately asserts the
// checks were sent in the exact relation order, repeated once per stack,
// that the field build assumes.
func TestResolveStacksCapabilitiesMapsEachStackAndRelationPositionally(t *testing.T) {
	t.Parallel()

	stacks := []traits.Stack{{ID: "stack_a"}, {ID: "stack_b"}}

	for stackIndex, stack := range stacks {
		for relationIndex, name := range stackCapabilityRelationNames {
			t.Run(fmt.Sprintf("%s/%s", stack.ID, name), func(t *testing.T) {
				t.Parallel()

				wantIndex := stackIndex*len(stackCapabilityRelationsInOrder) + relationIndex
				authorizer := &permissionAuthorizer{batchDecision: func(i int) bool { return i == wantIndex }}
				ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: "user_123"})

				got, err := ResolveStacksCapabilities(ctx, authorizer, stacks)
				if err != nil {
					t.Fatalf("ResolveStacksCapabilities() error = %v", err)
				}
				for _, s := range stacks {
					want := StackCapabilities{}
					if s.ID == stack.ID {
						want = capabilityWithOnly(relationIndex)
					}
					if got[s.ID] != want {
						t.Fatalf("caps[%q] = %+v, want %+v", s.ID, got[s.ID], want)
					}
				}
				assertStackCapabilityRelationOrder(t, authorizer, len(stacks))
			})
		}
	}
}

// platformCapabilityRelationsInOrder mirrors stackCapabilityRelationsInOrder
// for the platform pair, and for the same reason: it is written out
// independently of the production slice so a permutation of that slice fails
// here instead of being copied into the assertion.
var platformCapabilityRelationsInOrder = []authz.Relation{
	authz.RelationCanAdminister, authz.RelationCanCreateStack,
}

var platformCapabilityRelationNames = []string{"can_administer", "can_create_stack"}

// Pins ResolvePlatformCapabilities the way the two stack tests pin their
// resolvers. The platform pair had no such test, which is what made a
// reordering of its relations silently return IsPlatformAdmin for
// can_create_stack.
func TestResolvePlatformCapabilitiesMapsEachRelationPositionally(t *testing.T) {
	t.Parallel()

	for relationIndex, name := range platformCapabilityRelationNames {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			authorizer := &permissionAuthorizer{batchDecision: func(i int) bool { return i == relationIndex }}
			ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: "user_123"})

			got, err := ResolvePlatformCapabilities(ctx, authorizer)
			if err != nil {
				t.Fatalf("ResolvePlatformCapabilities() error = %v", err)
			}
			want := PlatformCapabilities{IsPlatformAdmin: relationIndex == 0, CanCreateStack: relationIndex == 1}
			if got != want {
				t.Fatalf("ResolvePlatformCapabilities() = %+v, want %+v", got, want)
			}

			// The boolean assertion above cannot see a permuted relations
			// slice on its own, so the recorded Relation at each position is
			// checked too.
			if len(authorizer.batchChecks) != len(platformCapabilityRelationsInOrder) {
				t.Fatalf("batch checks sent = %d, want %d", len(authorizer.batchChecks), len(platformCapabilityRelationsInOrder))
			}
			for i, check := range authorizer.batchChecks {
				if check.Relation != platformCapabilityRelationsInOrder[i] {
					t.Fatalf("batchChecks[%d].Relation = %q, want %q (relations slice order changed)", i, check.Relation, platformCapabilityRelationsInOrder[i])
				}
				if check.Object != authz.Platform {
					t.Fatalf("batchChecks[%d].Object = %q, want the platform singleton", i, check.Object)
				}
			}
		})
	}
}

func TestMissingAuthorizerIsUnavailable(t *testing.T) {
	t.Parallel()

	service := NewService(Service{Stacks: &recordingStackRepository{}})
	ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: "user_123"})

	_, err := service.GetStack(ctx, GetStackCommand{TenantID: "tenant_123", StackID: "stack_123"})
	if !errors.Is(err, authz.ErrUnavailable) {
		t.Fatalf("error = %v, want ErrUnavailable", err)
	}
}

type permissionAuthorizer struct {
	allowed             bool
	check               authz.CheckRequest
	batchSizes          []int
	batchChecks         []authz.CheckRequest
	batchErr            error
	failBatch           int
	truncateBatchResult bool
	batchDecision       func(int) bool
}

func (authorizer *permissionAuthorizer) Check(_ context.Context, request authz.CheckRequest) (authz.CheckResult, error) {
	authorizer.check = request
	return authz.CheckResult{Allowed: authorizer.allowed}, nil
}

func (authorizer *permissionAuthorizer) BatchCheck(_ context.Context, request authz.BatchCheckRequest) (authz.BatchCheckResult, error) {
	authorizer.batchSizes = append(authorizer.batchSizes, len(request.Checks))
	authorizer.batchChecks = append(authorizer.batchChecks, request.Checks...)
	if authorizer.batchErr != nil && (authorizer.failBatch == 0 || authorizer.failBatch == len(authorizer.batchSizes)) {
		return authz.BatchCheckResult{}, authorizer.batchErr
	}
	results := make([]authz.CheckResult, len(request.Checks))
	for i := range results {
		results[i].Allowed = authorizer.batchDecision == nil || authorizer.batchDecision(i)
	}
	if authorizer.truncateBatchResult && len(results) > 0 {
		results = results[:len(results)-1]
	}
	return authz.BatchCheckResult{Results: results}, nil
}

func (authorizer *permissionAuthorizer) ListGrants(context.Context, authz.ListGrantsRequest) (authz.ListGrantsResult, error) {
	return authz.ListGrantsResult{}, nil
}

func (authorizer *permissionAuthorizer) WriteRelationships(context.Context, authz.Mutation) error {
	return nil
}
func (authorizer *permissionAuthorizer) DeleteRelationships(context.Context, authz.Mutation) error {
	return nil
}

type pagedStackRepository struct {
	stacks      []traits.Stack
	pageCalls   int
	repeatPage  bool
	ignoreLimit bool
}

func (*pagedStackRepository) CreateStack(context.Context, traits.Stack) error { return nil }
func (*pagedStackRepository) GetStack(context.Context, traits.TenantID, traits.StackID) (traits.Stack, error) {
	return traits.Stack{}, nil
}
func (*pagedStackRepository) GetStackWithTemplates(context.Context, traits.TenantID, traits.StackID) (StackView, error) {
	return StackView{}, nil
}
func (repository *pagedStackRepository) ListStacks(context.Context, traits.TenantID) ([]traits.Stack, error) {
	return repository.stacks, nil
}
func (repository *pagedStackRepository) ListStacksPage(_ context.Context, _ traits.TenantID, after *StackPageCursor, limit int) ([]traits.Stack, error) {
	repository.pageCalls++
	start := 0
	if after != nil && !repository.repeatPage {
		for i, stack := range repository.stacks {
			if stack.ID == after.ID && stack.CreatedAt.Equal(after.CreatedAt) {
				start = i + 1
				break
			}
		}
	}
	end := len(repository.stacks)
	if !repository.ignoreLimit {
		end = min(start+limit, end)
	}
	return append([]traits.Stack(nil), repository.stacks[start:end]...), nil
}
