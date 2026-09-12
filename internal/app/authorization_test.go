package app

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/vishu42/tflive/internal/authn"
	"github.com/vishu42/tflive/internal/authorization"
	"github.com/vishu42/tflive/internal/domain"
)

func TestGetStackChecksViewPermission(t *testing.T) {
	t.Parallel()

	// viewer is the weakest role the model has, and it satisfies can_view and
	// nothing else. Granting only that proves can_view is the relation GetStack
	// asks for: any stronger requirement would refuse this principal.
	auth := newTestAuthorization(t)
	grantStack(t, auth, "user_123", "stack_123", authorization.RelationViewer)
	stacks := &recordingStackRepository{view: StackView{Stack: domain.Stack{ID: "stack_123"}}}
	service := NewService(Service{Authorization: auth, Stacks: stacks})
	ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: "user_123"})

	if _, err := service.GetStack(ctx, GetStackCommand{TenantID: "tenant_123", StackID: "stack_123"}); err != nil {
		t.Fatalf("GetStack() error = %v", err)
	}
}

func TestGetStackDenialReturnsNotFound(t *testing.T) {
	t.Parallel()

	service := NewService(Service{
		Authorization: newTestAuthorization(t),
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

	all := make([]domain.Stack, 55)
	for i := range all {
		all[i] = domain.Stack{ID: domain.StackID(fmt.Sprintf("stack_%02d", i)), CreatedAt: time.Unix(int64(100-i), 0)}
	}
	repository := &pagedStackRepository{stacks: all}
	auth := newTestAuthorization(t)
	// Every other stack, so the scan has to cross both the page boundary and
	// the fifty-check batch boundary and still return the right set.
	for i := 0; i < 55; i += 2 {
		grantStack(t, auth, "user_123", fmt.Sprintf("stack_%02d", i), authorization.RelationViewer)
	}
	service := NewService(Service{Authorization: auth, Stacks: repository})
	ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: "user_123"})

	stacks, err := service.ListStacks(ctx, ListStacksCommand{TenantID: "tenant_123"})
	if err != nil {
		t.Fatalf("ListStacks() error = %v", err)
	}
	if len(stacks) != 28 {
		t.Fatalf("len(stacks) = %d, want 28", len(stacks))
	}
	for _, stack := range stacks {
		var index int
		if _, err := fmt.Sscanf(string(stack.ID), "stack_%02d", &index); err != nil {
			t.Fatalf("unexpected stack id %q", stack.ID)
		}
		if index%2 != 0 {
			t.Fatalf("stack %q is visible but was never granted", stack.ID)
		}
	}
	if repository.pageCalls != 2 {
		t.Fatalf("page calls = %d, want 2", repository.pageCalls)
	}
}

func TestListStacksSkipsAuthorizationForEmptyTenant(t *testing.T) {
	t.Parallel()

	auth := newTestAuthorization(t)
	service := NewService(Service{Authorization: auth, Stacks: &pagedStackRepository{}})
	ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: "user_123"})

	stacks, err := service.ListStacks(ctx, ListStacksCommand{TenantID: "tenant_123"})
	if err != nil {
		t.Fatalf("ListStacks() error = %v", err)
	}
	if len(stacks) != 0 {
		t.Fatalf("stacks = %#v, want none for an empty tenant", stacks)
	}
}

func TestListStacksRejectsNonAdvancingPage(t *testing.T) {
	t.Parallel()

	all := make([]domain.Stack, 50)
	for i := range all {
		all[i] = domain.Stack{ID: domain.StackID(fmt.Sprintf("stack_%02d", 50-i)), CreatedAt: time.Unix(100, 0)}
	}
	service := NewService(Service{
		Authorization: newTestAuthorization(t),
		Stacks:     &pagedStackRepository{stacks: all, repeatPage: true},
	})
	ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: "user_123"})

	_, err := service.ListStacks(ctx, ListStacksCommand{TenantID: "tenant_123"})
	if err == nil {
		t.Fatal("error")
	}
}

func TestListStacksRejectsOversizedPage(t *testing.T) {
	t.Parallel()

	all := make([]domain.Stack, 51)
	for i := range all {
		all[i] = domain.Stack{ID: domain.StackID(fmt.Sprintf("stack_%02d", 51-i)), CreatedAt: time.Unix(100, 0)}
	}
	service := NewService(Service{Authorization: newTestAuthorization(t), Stacks: &pagedStackRepository{stacks: all, ignoreLimit: true}})
	ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: "user_123"})

	_, err := service.ListStacks(ctx, ListStacksCommand{TenantID: "tenant_123"})
	if err == nil {
		t.Fatal("error")
	}
}

func TestListStacksRejectsOutOfOrderPage(t *testing.T) {
	t.Parallel()

	stacks := []domain.Stack{
		{ID: "stack_a", CreatedAt: time.Unix(100, 0)},
		{ID: "stack_b", CreatedAt: time.Unix(100, 0)},
	}
	service := NewService(Service{Authorization: newTestAuthorization(t), Stacks: &pagedStackRepository{stacks: stacks}})
	ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: "user_123"})

	_, err := service.ListStacks(ctx, ListStacksCommand{TenantID: "tenant_123"})
	if err == nil {
		t.Fatal("error")
	}
}

func TestListStacksRejectsMalformedCandidateIDAsDependencyFailure(t *testing.T) {
	t.Parallel()

	service := NewService(Service{Authorization: newTestAuthorization(t), Stacks: &pagedStackRepository{stacks: []domain.Stack{{ID: "bad:id"}}}})
	ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: "user_123"})

	_, err := service.ListStacks(ctx, ListStacksCommand{TenantID: "tenant_123"})
	if err == nil {
		t.Fatal("error")
	}
}

func TestInheritedAuthorizationRequiresPrincipalBeforeRepositoryRead(t *testing.T) {
	t.Parallel()

	runs := &recordingTemplateRunRepository{}
	service := NewService(Service{Authorization: newTestAuthorization(t), TemplateRuns: runs})

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

	templates := &recordingStackTemplateRepository{stackTemplate: domain.StackTemplate{
		ID:       "stack_template_123",
		TenantID: "tenant_123",
		StackID:  "stack_123",
	}}
	service := NewService(Service{
		Authorization:     newTestAuthorization(t),
		StackTemplates: templates,
	})
	ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: "user_123"})

	_, err := service.StartTemplateRun(ctx, StartTemplateRunCommand{
		TenantID:        "tenant_123",
		StackTemplateID: "stack_template_123",
		Operation:       domain.OperationPlan,
	})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("error = %v, want ErrForbidden", err)
	}
}

func TestInheritedResourceMissingMutationReturnsForbidden(t *testing.T) {
	t.Parallel()

	service := NewService(Service{Authorization: newPlatformAuthorizer(t, platformAdmin("user_123")), TemplateRuns: &recordingTemplateRunRepository{getErr: ErrNotFound}})

	err := service.ApproveRun(authenticatedContext(), ApproveRunCommand{TenantID: "tenant_123", RunID: "missing_run"})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("error = %v, want ErrForbidden", err)
	}
}

// The three tests below pin the positional mapping from an ordered batch result
// onto capability fields. The field builds are fixed literals -- CanView from
// results[0], CanOperate from results[1], and so on -- that trust the checks
// arrived in the order the relations slice lists them, and do not re-derive
// position from the relation each answer belongs to. A permuted slice would
// send the same number of checks at the same indices, so nothing about the
// count can expose the swap.
//
// With a real engine the swap is exposed by the model's cascade instead. One
// role implies a known set of capabilities -- viewer implies can_view alone,
// operator adds can_operate, owner reaches all four -- so a permuted slice
// decodes a correct answer into the wrong fields and the expected set no
// longer matches.

// capabilitiesFor is the model's cascade, written out independently of the
// model so a change to either side fails here rather than agreeing silently.
func capabilitiesFor(relation authorization.Relation) StackCapabilities {
	switch relation {
	case authorization.RelationViewer:
		return StackCapabilities{CanView: true}
	case authorization.RelationOperator:
		return StackCapabilities{CanView: true, CanOperate: true}
	case authorization.RelationApprover:
		return StackCapabilities{CanView: true, CanApprove: true}
	case authorization.RelationOwner:
		return StackCapabilities{CanView: true, CanOperate: true, CanApprove: true, CanManageAccess: true}
	default:
		panic("capabilitiesFor: unexpected relation " + relation.String())
	}
}

var stackRoles = []authorization.Relation{
	authorization.RelationViewer,
	authorization.RelationOperator,
	authorization.RelationApprover,
	authorization.RelationOwner,
}

func TestResolveStackCapabilitiesMapsEachRelationPositionally(t *testing.T) {
	t.Parallel()

	for _, role := range stackRoles {
		t.Run(role.String(), func(t *testing.T) {
			t.Parallel()

			auth := newTestAuthorization(t)
			grantStack(t, auth, "user_123", "stack_123", role)
			ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: "user_123"})

			got, err := ResolveStackCapabilities(ctx, auth, "stack_123")
			if err != nil {
				t.Fatalf("ResolveStackCapabilities() error = %v", err)
			}
			if want := capabilitiesFor(role); got != want {
				t.Fatalf("ResolveStackCapabilities(%s) = %+v, want %+v", role, got, want)
			}
		})
	}
}

// Pins the stride ResolveStacksCapabilities walks from one stack's block of
// results to the next. Two stacks are required to exercise it at all; with one,
// base is zero for every case and a broken stride would still pass.
func TestResolveStacksCapabilitiesMapsEachStackAndRelationPositionally(t *testing.T) {
	t.Parallel()

	stacks := []domain.Stack{{ID: "stack_a"}, {ID: "stack_b"}}

	for _, stack := range stacks {
		for _, role := range stackRoles {
			t.Run(string(stack.ID)+"/"+role.String(), func(t *testing.T) {
				t.Parallel()

				auth := newTestAuthorization(t)
				grantStack(t, auth, "user_123", string(stack.ID), role)
				ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: "user_123"})

				got, err := ResolveStacksCapabilities(ctx, auth, stacks)
				if err != nil {
					t.Fatalf("ResolveStacksCapabilities() error = %v", err)
				}
				for _, other := range stacks {
					want := StackCapabilities{}
					if other.ID == stack.ID {
						want = capabilitiesFor(role)
					}
					if got[other.ID] != want {
						t.Fatalf("caps[%q] = %+v, want %+v", other.ID, got[other.ID], want)
					}
				}
			})
		}
	}
}

// Pins ResolvePlatformCapabilities the way the two stack tests pin theirs. The
// platform pair had no such test, which is what let a reordering of its
// relations silently return IsPlatformAdmin for can_create_stack.
func TestResolvePlatformCapabilitiesMapsEachRelationPositionally(t *testing.T) {
	t.Parallel()

	cases := []struct {
		role authorization.Relation
		want PlatformCapabilities
	}{
		// viewer reaches neither capability; editor reaches can_create_stack
		// through can_edit; admin reaches both through can_administer.
		{authorization.RelationViewer, PlatformCapabilities{}},
		{authorization.RelationEditor, PlatformCapabilities{CanCreateStack: true}},
		{authorization.RelationAdmin, PlatformCapabilities{IsPlatformAdmin: true, CanCreateStack: true}},
	}

	for _, testCase := range cases {
		t.Run(testCase.role.String(), func(t *testing.T) {
			t.Parallel()

			auth := newTestAuthorization(t)
			grantPlatform(t, auth, "user_123", testCase.role)
			ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: "user_123"})

			got, err := ResolvePlatformCapabilities(ctx, auth)
			if err != nil {
				t.Fatalf("ResolvePlatformCapabilities() error = %v", err)
			}
			if got != testCase.want {
				t.Fatalf("ResolvePlatformCapabilities(%s) = %+v, want %+v", testCase.role, got, testCase.want)
			}
		})
	}
}

// An unconfigured authorization is an error, not a refusal. "I cannot tell" and
// "you may not" are different answers, and only one of them is true here.
func TestMissingAuthorizerIsUnavailable(t *testing.T) {
	t.Parallel()

	service := NewService(Service{Stacks: &recordingStackRepository{}})
	ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: "user_123"})

	_, err := service.GetStack(ctx, GetStackCommand{TenantID: "tenant_123", StackID: "stack_123"})
	if err == nil {
		t.Fatal("GetStack() error = nil, want a failure")
	}
	if errors.Is(err, ErrForbidden) || errors.Is(err, ErrNotFound) {
		t.Fatalf("error = %v, want a failure rather than a refusal", err)
	}
}

type pagedStackRepository struct {
	stacks      []domain.Stack
	pageCalls   int
	repeatPage  bool
	ignoreLimit bool
}

func (*pagedStackRepository) CreateStack(context.Context, domain.Stack) error { return nil }
func (*pagedStackRepository) GetStack(context.Context, domain.TenantID, domain.StackID) (domain.Stack, error) {
	return domain.Stack{}, nil
}
func (*pagedStackRepository) GetStackWithTemplates(context.Context, domain.TenantID, domain.StackID) (StackView, error) {
	return StackView{}, nil
}
func (repository *pagedStackRepository) ListStacks(context.Context, domain.TenantID) ([]domain.Stack, error) {
	return repository.stacks, nil
}
func (repository *pagedStackRepository) ListStacksPage(_ context.Context, _ domain.TenantID, after *StackPageCursor, limit int) ([]domain.Stack, error) {
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
	return append([]domain.Stack(nil), repository.stacks[start:end]...), nil
}
