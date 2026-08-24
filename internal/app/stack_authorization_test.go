package app

import (
	"context"
	"encoding/json"
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

func TestCreateStackEnqueuesProvisioningInsteadOfCallingOpenFGA(t *testing.T) {
	t.Parallel()

	stacks := &authorizationStackRepository{}
	authorizer := &recordingAuthorizer{tiers: newPlatformAuthorizer(platformEditor("user_123"))}
	work := newRecordingWork(stacks)
	service := NewService(Service{Stacks: stacks, Work: work, Authorizer: authorizer, StackIDs: fixedStackIDGenerator{id: "stack_123"}, Clock: fixedClock{now: time.Now()}})
	ctx := platformContext("user_123")

	stack, err := service.CreateStack(ctx, CreateStackCommand{TenantID: "tenant_123", Name: "Acme"})
	if err != nil {
		t.Fatalf("CreateStack() error = %v", err)
	}

	// The owner grant is now a durable intent, not a synchronous OpenFGA write.
	if authorizer.calls != 0 {
		t.Fatalf("authorization calls = %d, want 0 — delivery belongs to the controller", authorizer.calls)
	}
	if stack.Status != traits.StackStatusProvisioning {
		t.Fatalf("status = %q, want %q — the stack is not usable until the tuple lands", stack.Status, traits.StackStatusProvisioning)
	}
	if len(work.requests) != 1 {
		t.Fatalf("enqueued %d requests, want 1", len(work.requests))
	}
	if work.requests[0].Kind != KindGrantStackOwner {
		t.Fatalf("kind = %q, want %q", work.requests[0].Kind, KindGrantStackOwner)
	}

	var payload GrantStackOwnerPayload
	if err := json.Unmarshal(work.requests[0].Payload, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload.StackID != "stack_123" || payload.Subject != "user_123" || payload.TenantID != "tenant_123" {
		t.Fatalf("payload = %#v, want user_123 provisioning stack_123 in tenant_123", payload)
	}
	if stacks.calls != 1 {
		t.Fatalf("stack calls = %d, want 1", stacks.calls)
	}
	if stacks.created.Status != traits.StackStatusProvisioning {
		t.Fatalf("persisted status = %q, want %q", stacks.created.Status, traits.StackStatusProvisioning)
	}
}

func TestCreateStackAllowsPlatformAdmin(t *testing.T) {
	t.Parallel()

	stacks := &authorizationStackRepository{}
	work := newRecordingWork(stacks)
	service := NewService(Service{Stacks: stacks, Work: work, Authorizer: &recordingAuthorizer{tiers: newPlatformAuthorizer(platformAdmin("user_123"))}, StackIDs: fixedStackIDGenerator{id: "stack_123"}, Clock: fixedClock{now: time.Now()}})
	ctx := platformContext("user_123")

	if _, err := service.CreateStack(ctx, CreateStackCommand{TenantID: "tenant_123", Name: "Acme"}); err != nil {
		t.Fatalf("CreateStack() error = %v", err)
	}
	if stacks.calls != 1 || len(work.requests) != 1 {
		t.Fatalf("stack calls = %d, enqueued = %d, want 1 and 1", stacks.calls, len(work.requests))
	}
}

// The old "retains stack when owner assignment fails" case no longer exists:
// there is no separate owner-assignment step at request time. Its replacement
// is that a failing unit of work persists nothing at all.
func TestCreateStackPersistsNothingWhenUnitOfWorkFails(t *testing.T) {
	t.Parallel()

	stacks := &authorizationStackRepository{}
	work := newRecordingWork(stacks)
	work.err = authz.ErrUnavailable
	service := NewService(Service{Stacks: stacks, Work: work, Authorizer: &recordingAuthorizer{tiers: newPlatformAuthorizer(platformEditor("user_123"))}, StackIDs: fixedStackIDGenerator{id: "stack_123"}, Clock: fixedClock{now: time.Now()}})
	ctx := platformContext("user_123")

	_, err := service.CreateStack(ctx, CreateStackCommand{TenantID: "tenant_123", Name: "Acme"})
	if !errors.Is(err, authz.ErrUnavailable) {
		t.Fatalf("error = %v, want ErrUnavailable", err)
	}
	if stacks.calls != 0 || len(work.requests) != 0 {
		t.Fatalf("stack calls = %d, enqueued = %d, want 0 and 0", stacks.calls, len(work.requests))
	}
}

func TestCreateStackRejectsInvalidOpenFGASubjectBeforePersistence(t *testing.T) {
	t.Parallel()

	stacks := &authorizationStackRepository{}
	work := newRecordingWork(stacks)
	service := NewService(Service{Stacks: stacks, Work: work, Authorizer: &recordingAuthorizer{}, StackIDs: fixedStackIDGenerator{id: "stack_123"}, Clock: fixedClock{now: time.Now()}})
	ctx := platformContext("user:bad")

	_, err := service.CreateStack(ctx, CreateStackCommand{TenantID: "tenant_123", Name: "Acme"})
	if !errors.Is(err, authz.ErrInvalidInput) {
		t.Fatalf("error = %v, want ErrInvalidInput", err)
	}
	if stacks.calls != 0 || len(work.requests) != 0 {
		t.Fatalf("stack calls = %d, enqueued = %d, want 0 and 0", stacks.calls, len(work.requests))
	}
}

type authorizationStackRepository struct {
	calls   int
	created traits.Stack
	stack   traits.Stack
	getErr  error
}

func (repository *authorizationStackRepository) CreateStack(_ context.Context, stack traits.Stack) error {
	repository.calls++
	repository.created = stack
	return nil
}

func (repository *authorizationStackRepository) GetStack(context.Context, traits.TenantID, traits.StackID) (traits.Stack, error) {
	return repository.stack, repository.getErr
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
	grants   []authz.Grant
	// tiers answers the read side, so a test states which platform
	// capabilities its principal holds. Nil grants none, which is what an
	// unseeded subject is -- and what the "requires creator" case needs.
	tiers *platformAuthorizer
}

func (authorizer *recordingAuthorizer) Check(ctx context.Context, request authz.CheckRequest) (authz.CheckResult, error) {
	if authorizer.tiers == nil {
		return authz.CheckResult{}, nil
	}
	return authorizer.tiers.Check(ctx, request)
}
func (authorizer *recordingAuthorizer) BatchCheck(ctx context.Context, request authz.BatchCheckRequest) (authz.BatchCheckResult, error) {
	if authorizer.tiers == nil {
		return authz.BatchCheckResult{}, nil
	}
	return authorizer.tiers.BatchCheck(ctx, request)
}
func (authorizer *recordingAuthorizer) ListGrants(context.Context, authz.ListGrantsRequest) (authz.ListGrantsResult, error) {
	return authz.ListGrantsResult{Grants: authorizer.grants}, nil
}
func (authorizer *recordingAuthorizer) WriteRelationships(_ context.Context, mutation authz.Mutation) error {
	authorizer.calls++
	authorizer.mutation = mutation
	return authorizer.writeErr
}
func (authorizer *recordingAuthorizer) DeleteRelationships(context.Context, authz.Mutation) error {
	return nil
}
