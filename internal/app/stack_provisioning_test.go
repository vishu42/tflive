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

const provisioningSubject = "6fdb4b4c-2a8f-4cf7-945f-38f67f6a0e91"

type recordingStackStatuses struct {
	calls  int
	tenant traits.TenantID
	stack  traits.StackID
	err    error
}

func (statuses *recordingStackStatuses) MarkStackReady(_ context.Context, tenantID traits.TenantID, stackID traits.StackID) error {
	statuses.calls++
	statuses.tenant = tenantID
	statuses.stack = stackID
	return statuses.err
}

func provisionPayload(t *testing.T, stackID, tenantID, subject string) json.RawMessage {
	t.Helper()
	payload, err := json.Marshal(ProvisionStackPayload{StackID: stackID, TenantID: tenantID, Subject: subject})
	if err != nil {
		t.Fatalf("marshal provision payload: %v", err)
	}
	return payload
}

// ungrantedAuthorizer stands in for the window before the owner tuple has
// landed in OpenFGA: every check and every capability is refused, but the batch
// shape stays well formed so capability resolution still runs.
type ungrantedAuthorizer struct {
	recordingAuthorizer
}

func (authorizer *ungrantedAuthorizer) Check(context.Context, authz.CheckRequest) (authz.CheckResult, error) {
	return authz.CheckResult{Allowed: false}, nil
}

func (authorizer *ungrantedAuthorizer) BatchCheck(_ context.Context, request authz.BatchCheckRequest) (authz.BatchCheckResult, error) {
	results := make([]authz.CheckResult, len(request.Checks))
	return authz.BatchCheckResult{Results: results}, nil
}

func getStackService(stacks *authorizationStackRepository) *Service {
	return NewService(Service{
		Stacks:            stacks,
		Authorizer:        &ungrantedAuthorizer{},
		TemplateRevisions: &recordingTemplateRepository{},
		Clock:             fixedClock{now: time.Now()},
	})
}

func TestGetStackLetsCreatorSeeTheirProvisioningStack(t *testing.T) {
	t.Parallel()

	stacks := &authorizationStackRepository{stack: traits.Stack{
		ID:        "stack_abc",
		TenantID:  "tenant_1",
		Status:    traits.StackStatusProvisioning,
		CreatedBy: provisioningSubject,
	}}
	service := getStackService(stacks)
	ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: provisioningSubject})

	if _, err := service.GetStack(ctx, GetStackCommand{TenantID: "tenant_1", StackID: "stack_abc"}); err != nil {
		t.Fatalf("GetStack returned error: %v — the creator must not 404 on their own new stack", err)
	}
}

func TestGetStackHidesProvisioningStackFromOthers(t *testing.T) {
	t.Parallel()

	stacks := &authorizationStackRepository{stack: traits.Stack{
		ID:        "stack_abc",
		TenantID:  "tenant_1",
		Status:    traits.StackStatusProvisioning,
		CreatedBy: "someone-else",
	}}
	service := getStackService(stacks)
	ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: provisioningSubject})

	_, err := service.GetStack(ctx, GetStackCommand{TenantID: "tenant_1", StackID: "stack_abc"})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound — the exception covers the creator only", err)
	}
}

func TestGetStackStopsExceptingOnceTheStackIsReady(t *testing.T) {
	t.Parallel()

	stacks := &authorizationStackRepository{stack: traits.Stack{
		ID:        "stack_abc",
		TenantID:  "tenant_1",
		Status:    traits.StackStatusReady,
		CreatedBy: provisioningSubject,
	}}
	service := getStackService(stacks)
	ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: provisioningSubject})

	_, err := service.GetStack(ctx, GetStackCommand{TenantID: "tenant_1", StackID: "stack_abc"})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound — a ready stack is OpenFGA's call alone", err)
	}
}

func mustOwnerGrant(t *testing.T, stackID, subject string) authz.Grant {
	t.Helper()
	stack, err := authz.StackFromID(stackID)
	if err != nil {
		t.Fatalf("StackFromID(%q): %v", stackID, err)
	}
	sub, err := authz.SubjectFromKeycloakSub(subject)
	if err != nil {
		t.Fatalf("SubjectFromKeycloakSub(%q): %v", subject, err)
	}
	grant, err := authz.NewGrant(sub, stack, authz.RoleOwner)
	if err != nil {
		t.Fatalf("NewGrant: %v", err)
	}
	return grant
}
