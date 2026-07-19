package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/vishu42/tflive/internal/authn"
	"github.com/vishu42/tflive/internal/authz"
	"github.com/vishu42/tflive/internal/traits"
)

func TestCreateStackRequiresCreatorRole(t *testing.T) {
	t.Parallel()

	stacks := &authorizationStackRepository{}
	authorizer := &recordingAuthorizer{}
	service := NewService(Service{Stacks: stacks, Authorizer: authorizer, StackIDs: fixedStackIDGenerator{id: "stack_123"}, Clock: fixedClock{now: time.Now()}})
	ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: "user_123"})

	_, err := service.CreateStack(ctx, CreateStackCommand{TenantID: "tenant_123", Name: "Acme"})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("error = %v, want ErrForbidden", err)
	}
	if stacks.calls != 0 || authorizer.calls != 0 {
		t.Fatal("unauthorized stack creation had side effects")
	}
}

func TestCreateStackAssignsConfirmedOwner(t *testing.T) {
	t.Parallel()

	stacks := &authorizationStackRepository{}
	authorizer := &recordingAuthorizer{}
	service := NewService(Service{Stacks: stacks, Authorizer: authorizer, StackIDs: fixedStackIDGenerator{id: "stack_123"}, Clock: fixedClock{now: time.Now()}})
	ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: "user_123", RealmRoles: []string{"stack-creator"}})

	if _, err := service.CreateStack(ctx, CreateStackCommand{TenantID: "tenant_123", Name: "Acme"}); err != nil {
		t.Fatalf("CreateStack() error = %v", err)
	}
	if authorizer.calls != 1 || !authorizer.mutation.Confirm() {
		t.Fatalf("owner mutation = %#v, calls = %d", authorizer.mutation, authorizer.calls)
	}
	grant := authorizer.mutation.Grants()[0]
	if grant.Subject().String() != "user:user_123" || grant.Stack().String() != "stack:stack_123" || grant.Role() != authz.RoleOwner {
		t.Fatalf("grant = %#v", grant)
	}
	if stacks.ownerGrant != grant {
		t.Fatalf("persisted owner intent = %#v, want %#v", stacks.ownerGrant, grant)
	}
}

func TestCreateStackAllowsPlatformAdmin(t *testing.T) {
	t.Parallel()

	stacks := &authorizationStackRepository{}
	authorizer := &recordingAuthorizer{}
	service := NewService(Service{Stacks: stacks, Authorizer: authorizer, StackIDs: fixedStackIDGenerator{id: "stack_123"}, Clock: fixedClock{now: time.Now()}})
	ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: "user_123", RealmRoles: []string{"platform-admin"}})

	if _, err := service.CreateStack(ctx, CreateStackCommand{TenantID: "tenant_123", Name: "Acme"}); err != nil {
		t.Fatalf("CreateStack() error = %v", err)
	}
	if stacks.calls != 1 || authorizer.calls != 1 {
		t.Fatalf("stack calls = %d, authorization calls = %d", stacks.calls, authorizer.calls)
	}
}

func TestCreateStackRetainsStackWhenOwnerAssignmentFails(t *testing.T) {
	t.Parallel()

	stacks := &authorizationStackRepository{}
	authorizer := &recordingAuthorizer{writeErr: authz.ErrUnavailable}
	service := NewService(Service{Stacks: stacks, Authorizer: authorizer, StackIDs: fixedStackIDGenerator{id: "stack_123"}, Clock: fixedClock{now: time.Now()}})
	ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: "user_123", RealmRoles: []string{"stack-creator"}})

	_, err := service.CreateStack(ctx, CreateStackCommand{TenantID: "tenant_123", Name: "Acme"})
	if !errors.Is(err, authz.ErrUnavailable) {
		t.Fatalf("error = %v, want ErrUnavailable", err)
	}
	if stacks.calls != 1 || authorizer.calls != 1 {
		t.Fatalf("stack calls = %d, authorization calls = %d", stacks.calls, authorizer.calls)
	}
}

func TestCreateStackRejectsInvalidOpenFGASubjectBeforePersistence(t *testing.T) {
	t.Parallel()

	stacks := &authorizationStackRepository{}
	authorizer := &recordingAuthorizer{}
	service := NewService(Service{Stacks: stacks, Authorizer: authorizer, StackIDs: fixedStackIDGenerator{id: "stack_123"}, Clock: fixedClock{now: time.Now()}})
	ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: "user:bad", RealmRoles: []string{"stack-creator"}})

	_, err := service.CreateStack(ctx, CreateStackCommand{TenantID: "tenant_123", Name: "Acme"})
	if !errors.Is(err, authz.ErrInvalidInput) {
		t.Fatalf("error = %v, want ErrInvalidInput", err)
	}
	if stacks.calls != 0 || authorizer.calls != 0 {
		t.Fatalf("stack calls = %d, authorization calls = %d", stacks.calls, authorizer.calls)
	}
}

type authorizationStackRepository struct {
	calls      int
	ownerGrant authz.Grant
}

func (repository *authorizationStackRepository) CreateStack(context.Context, traits.Stack) error {
	repository.calls++
	return nil
}

func (repository *authorizationStackRepository) CreateStackWithOwnerIntent(_ context.Context, _ traits.Stack, grant authz.Grant) error {
	repository.calls++
	repository.ownerGrant = grant
	return nil
}

func (repository *authorizationStackRepository) GetStack(context.Context, traits.TenantID, traits.StackID) (traits.Stack, error) {
	return traits.Stack{}, nil
}
func (repository *authorizationStackRepository) GetStackWithTemplates(context.Context, traits.TenantID, traits.StackID) (StackView, error) {
	return StackView{}, nil
}
func (repository *authorizationStackRepository) ListStacks(context.Context, traits.TenantID) ([]traits.Stack, error) {
	return nil, nil
}
func (repository *authorizationStackRepository) ListStacksPage(context.Context, traits.TenantID, *StackPageCursor, int) ([]traits.Stack, error) {
	return nil, nil
}

type recordingAuthorizer struct {
	calls    int
	mutation authz.Mutation
	writeErr error
}

func (authorizer *recordingAuthorizer) Check(context.Context, authz.CheckRequest) (authz.CheckResult, error) {
	return authz.CheckResult{}, nil
}
func (authorizer *recordingAuthorizer) BatchCheck(context.Context, authz.BatchCheckRequest) (authz.BatchCheckResult, error) {
	return authz.BatchCheckResult{}, nil
}
func (authorizer *recordingAuthorizer) ListAccessibleStacks(context.Context, authz.ListAccessibleStacksRequest) (authz.ListAccessibleStacksResult, error) {
	return authz.ListAccessibleStacksResult{}, nil
}
func (authorizer *recordingAuthorizer) ListGrants(context.Context, authz.ListGrantsRequest) (authz.ListGrantsResult, error) {
	return authz.ListGrantsResult{}, nil
}
func (authorizer *recordingAuthorizer) WriteRelationships(_ context.Context, mutation authz.Mutation) error {
	authorizer.calls++
	authorizer.mutation = mutation
	return authorizer.writeErr
}
func (authorizer *recordingAuthorizer) DeleteRelationships(context.Context, authz.Mutation) error {
	return nil
}
