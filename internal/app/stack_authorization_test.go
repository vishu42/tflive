package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/vishu42/tflive/internal/authn"
	"github.com/vishu42/tflive/internal/authorization"
	"github.com/vishu42/tflive/internal/domain"
)

func TestCreateStackRequiresCreatorRole(t *testing.T) {
	t.Parallel()

	stacks := &authorizationStackRepository{}
	auth := newTestAuthorization(t)
	service := NewService(Service{Stacks: stacks, Authorization: auth, StackIDs: fixedStackIDGenerator{id: "stack_new"}, Clock: fixedClock{now: time.Now()}})
	ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: "user_123"})

	_, err := service.CreateStack(ctx, CreateStackCommand{TenantID: "tenant_123", Name: "Acme"})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("error = %v, want ErrForbidden", err)
	}
	if stacks.calls != 0 {
		t.Fatal("unauthorized stack creation had side effects")
	}
}

// The inverse of what this file asserted before: the owner grant was a durable
// queue intent because a tuple write could not commit with the stack row. It
// can now, so creation writes both in one transaction, enqueues nothing, and
// returns a stack that is already usable.
func TestCreateStackWritesTheOwnerGrantInTheSameTransaction(t *testing.T) {
	t.Parallel()

	stacks := &authorizationStackRepository{}
	auth := newPlatformAuthorizer(t, platformEditor("user_123"))
	work := newRecordingWork(stacks)
	service := NewService(Service{Stacks: stacks, Work: work, Authorization: auth, StackIDs: fixedStackIDGenerator{id: "stack_new"}, Clock: fixedClock{now: time.Now()}})
	ctx := platformContext("user_123")

	stack, err := service.CreateStack(ctx, CreateStackCommand{TenantID: "tenant_123", Name: "Acme"})
	if err != nil {
		t.Fatalf("CreateStack() error = %v", err)
	}

	if stack.Status != domain.StackStatusReady {
		t.Fatalf("status = %q, want %q -- nothing is deferred any more", stack.Status, domain.StackStatusReady)
	}
	if stacks.created.Status != domain.StackStatusReady {
		t.Fatalf("persisted status = %q, want %q", stacks.created.Status, domain.StackStatusReady)
	}
	if len(work.requests) != 0 {
		t.Fatalf("enqueued %d requests, want 0 -- the grant is part of the commit", len(work.requests))
	}
	if stacks.calls != 1 {
		t.Fatalf("stack calls = %d, want 1", stacks.calls)
	}

	// The creator can reach the stack the moment the call returns, which is the
	// whole point of doing the write here rather than in a handler.
	allowed, err := auth.Can(ctx, "user_123", authorization.RelationCanManageAccess, mustStackObject(t, "stack_new"))
	if err != nil {
		t.Fatalf("Can() error = %v", err)
	}
	if !allowed {
		t.Fatal("the creator cannot manage the stack they just created")
	}
}

// The parent edge rides in the same mutation as the owner grant: a stack with
// one and not the other is broken either way.
func TestCreateStackWritesTheParentEdgeWithTheOwnerGrant(t *testing.T) {
	t.Parallel()

	stacks := &authorizationStackRepository{}
	auth := newPlatformAuthorizer(t, platformEditor("user_123"), platformAdmin("admin_123"))
	work := newRecordingWork(stacks)
	service := NewService(Service{Stacks: stacks, Work: work, Authorization: auth, StackIDs: fixedStackIDGenerator{id: "stack_new"}, Clock: fixedClock{now: time.Now()}})

	if _, err := service.CreateStack(platformContext("user_123"), CreateStackCommand{TenantID: "tenant_123", Name: "Acme"}); err != nil {
		t.Fatalf("CreateStack() error = %v", err)
	}

	// An administrator reaches the stack only through the parent edge.
	allowed, err := auth.Can(context.Background(), "admin_123", authorization.RelationCanManageAccess, mustStackObject(t, "stack_new"))
	if err != nil {
		t.Fatalf("Can() error = %v", err)
	}
	if !allowed {
		t.Fatal("a platform administrator cannot reach the new stack; the parent edge is missing")
	}
}

func mustStackObject(t *testing.T, id string) authorization.Object {
	t.Helper()
	object, err := authorization.ObjectFromID(authorization.TypeStack, id)
	if err != nil {
		t.Fatalf("stack object %q: %v", id, err)
	}
	return object
}

func TestCreateStackAllowsPlatformAdmin(t *testing.T) {
	t.Parallel()

	stacks := &authorizationStackRepository{}
	work := newRecordingWork(stacks)
	service := NewService(Service{Stacks: stacks, Work: work, Authorization: newPlatformAuthorizer(t, platformAdmin("user_123")), StackIDs: fixedStackIDGenerator{id: "stack_new"}, Clock: fixedClock{now: time.Now()}})
	ctx := platformContext("user_123")

	if _, err := service.CreateStack(ctx, CreateStackCommand{TenantID: "tenant_123", Name: "Acme"}); err != nil {
		t.Fatalf("CreateStack() error = %v", err)
	}
	if stacks.calls != 1 || len(work.requests) != 0 {
		t.Fatalf("stack calls = %d, enqueued = %d, want 1 and 0 -- the grant is part of the commit", stacks.calls, len(work.requests))
	}
}

// The old "retains stack when owner assignment fails" case no longer exists:
// there is no separate owner-assignment step at request time. Its replacement
// is that a failing unit of work persists nothing at all.
func TestCreateStackPersistsNothingWhenUnitOfWorkFails(t *testing.T) {
	t.Parallel()

	stacks := &authorizationStackRepository{}
	work := newRecordingWork(stacks)
	work.err = errors.New("the unit of work failed")
	service := NewService(Service{Stacks: stacks, Work: work, Authorization: newPlatformAuthorizer(t, platformEditor("user_123")), StackIDs: fixedStackIDGenerator{id: "stack_new"}, Clock: fixedClock{now: time.Now()}})
	ctx := platformContext("user_123")

	_, err := service.CreateStack(ctx, CreateStackCommand{TenantID: "tenant_123", Name: "Acme"})
	if err == nil {
		t.Fatal("CreateStack() error = nil, want the unit of work's failure")
	}
	if stacks.calls != 0 || len(work.requests) != 0 {
		t.Fatalf("stack calls = %d, enqueued = %d, want 0 and 0", stacks.calls, len(work.requests))
	}
}

func TestCreateStackRejectsInvalidOpenFGASubjectBeforePersistence(t *testing.T) {
	t.Parallel()

	stacks := &authorizationStackRepository{}
	work := newRecordingWork(stacks)
	service := NewService(Service{Stacks: stacks, Work: work, Authorization: newTestAuthorization(t), StackIDs: fixedStackIDGenerator{id: "stack_new"}, Clock: fixedClock{now: time.Now()}})
	ctx := platformContext("user:bad")

	_, err := service.CreateStack(ctx, CreateStackCommand{TenantID: "tenant_123", Name: "Acme"})
	if !errors.Is(err, authorization.ErrInvalidInput) {
		t.Fatalf("error = %v, want ErrInvalidInput", err)
	}
	if stacks.calls != 0 || len(work.requests) != 0 {
		t.Fatalf("stack calls = %d, enqueued = %d, want 0 and 0", stacks.calls, len(work.requests))
	}
}

type authorizationStackRepository struct {
	calls   int
	created domain.Stack
	stack   domain.Stack
	getErr  error
}

func (repository *authorizationStackRepository) CreateStack(_ context.Context, stack domain.Stack) error {
	repository.calls++
	repository.created = stack
	return nil
}

func (repository *authorizationStackRepository) GetStack(context.Context, domain.TenantID, domain.StackID) (domain.Stack, error) {
	return repository.stack, repository.getErr
}
func (repository *authorizationStackRepository) GetStackWithTemplates(context.Context, domain.TenantID, domain.StackID) (StackView, error) {
	return StackView{}, nil
}
func (repository *authorizationStackRepository) ListStacks(context.Context, domain.TenantID) ([]domain.Stack, error) {
	return nil, nil
}
func (repository *authorizationStackRepository) ListStacksPage(context.Context, domain.TenantID, *StackPageCursor, int) ([]domain.Stack, error) {
	return nil, nil
}
