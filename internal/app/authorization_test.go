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

type permissionAuthorizer struct {
	allowed             bool
	check               authz.CheckRequest
	stacks              []authz.Stack
	batchSizes          []int
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

func (authorizer *permissionAuthorizer) ListAccessibleStacks(context.Context, authz.ListAccessibleStacksRequest) (authz.ListAccessibleStacksResult, error) {
	return authz.ListAccessibleStacksResult{Stacks: authorizer.stacks}, nil
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
	stacks    []traits.Stack
	pageCalls int
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
	if after != nil {
		for i, stack := range repository.stacks {
			if stack.ID == after.ID && stack.CreatedAt.Equal(after.CreatedAt) {
				start = i + 1
				break
			}
		}
	}
	end := min(start+limit, len(repository.stacks))
	return append([]traits.Stack(nil), repository.stacks[start:end]...), nil
}
