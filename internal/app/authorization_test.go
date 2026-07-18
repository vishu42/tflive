package app

import (
	"context"
	"errors"
	"testing"

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
	if got := authorizer.check.Permission; got != authz.PermissionView {
		t.Fatalf("permission = %q, want %q", got, authz.PermissionView)
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

func TestListStacksUsesAccessibleViewIDs(t *testing.T) {
	t.Parallel()

	stack, err := authz.StackFromID("stack_123")
	if err != nil {
		t.Fatal(err)
	}
	authorizer := &permissionAuthorizer{stacks: []authz.Stack{stack}}
	repository := &recordingStackRepository{list: []traits.Stack{{ID: "stack_123"}}}
	service := NewService(Service{Authorizer: authorizer, Stacks: repository})
	ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: "user_123"})

	stacks, err := service.ListStacks(ctx, ListStacksCommand{TenantID: "tenant_123"})
	if err != nil {
		t.Fatalf("ListStacks() error = %v", err)
	}
	if len(stacks) != 1 || stacks[0].ID != "stack_123" {
		t.Fatalf("stacks = %#v", stacks)
	}
	if got := repository.gotListStackIDs; len(got) != 1 || got[0] != "stack_123" {
		t.Fatalf("stack IDs = %#v", got)
	}
}

type permissionAuthorizer struct {
	allowed bool
	check   authz.CheckRequest
	stacks  []authz.Stack
}

func (authorizer *permissionAuthorizer) Check(_ context.Context, request authz.CheckRequest) (authz.CheckResult, error) {
	authorizer.check = request
	return authz.CheckResult{Allowed: authorizer.allowed}, nil
}

func (authorizer *permissionAuthorizer) BatchCheck(context.Context, authz.BatchCheckRequest) (authz.BatchCheckResult, error) {
	return authz.BatchCheckResult{}, nil
}

func (authorizer *permissionAuthorizer) ListAccessibleStacks(context.Context, authz.ListAccessibleStacksRequest) (authz.ListAccessibleStacksResult, error) {
	return authz.ListAccessibleStacksResult{Stacks: authorizer.stacks}, nil
}

func (authorizer *permissionAuthorizer) ListGrants(context.Context, authz.ListGrantsRequest) (authz.ListGrantsResult, error) {
	return authz.ListGrantsResult{}, nil
}

func (authorizer *permissionAuthorizer) WriteRelationships(context.Context, authz.Mutation) error { return nil }
func (authorizer *permissionAuthorizer) DeleteRelationships(context.Context, authz.Mutation) error {
	return nil
}
