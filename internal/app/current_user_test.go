package app

import (
	"context"
	"errors"
	"testing"

	"github.com/vishu42/tflive/internal/authn"
	"github.com/vishu42/tflive/internal/authz"
	"github.com/vishu42/tflive/internal/traits"
)

func TestGetMeReturnsIdentityAndCapabilities(t *testing.T) {
	t.Parallel()

	service := NewService(Service{})
	ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{
		Subject:           "user-123",
		Name:              "Alice Smith",
		PreferredUsername: "alice",
		Email:             "alice@example.com",
		RealmRoles:        []string{"platform-admin", "stack-creator"},
	})

	me, err := service.GetMe(ctx)
	if err != nil {
		t.Fatalf("GetMe() error = %v", err)
	}
	if me.Subject != "user-123" {
		t.Fatalf("subject = %q, want user-123", me.Subject)
	}
	if me.Name != "Alice Smith" {
		t.Fatalf("name = %q, want Alice Smith", me.Name)
	}
	if me.PreferredUsername != "alice" {
		t.Fatalf("preferred_username = %q, want alice", me.PreferredUsername)
	}
	if me.Email != "alice@example.com" {
		t.Fatalf("email = %q, want alice@example.com", me.Email)
	}
	if !me.GlobalCapabilities.CanCreateStacks {
		t.Fatal("can_create_stacks = false, want true")
	}
	if !me.GlobalCapabilities.IsPlatformAdmin {
		t.Fatal("is_platform_admin = false, want true")
	}
}

func TestGetMeRequiresAuthentication(t *testing.T) {
	t.Parallel()

	service := NewService(Service{})

	_, err := service.GetMe(context.Background())
	if err != ErrUnauthenticated {
		t.Fatalf("GetMe() error = %v, want ErrUnauthenticated", err)
	}
}

func TestGetMeDerivesCapabilitiesFromRoles(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		roles           []string
		canCreateStacks bool
		isPlatformAdmin bool
	}{
		{name: "stack-creator only", roles: []string{"stack-creator"}, canCreateStacks: true, isPlatformAdmin: false},
		{name: "platform-admin only", roles: []string{"platform-admin"}, canCreateStacks: true, isPlatformAdmin: true},
		{name: "no roles", roles: nil, canCreateStacks: false, isPlatformAdmin: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			service := NewService(Service{})
			ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{
				Subject:    "user-123",
				RealmRoles: tt.roles,
			})

			me, err := service.GetMe(ctx)
			if err != nil {
				t.Fatalf("GetMe() error = %v", err)
			}
			if me.GlobalCapabilities.CanCreateStacks != tt.canCreateStacks {
				t.Fatalf("can_create_stacks = %v, want %v", me.GlobalCapabilities.CanCreateStacks, tt.canCreateStacks)
			}
			if me.GlobalCapabilities.IsPlatformAdmin != tt.isPlatformAdmin {
				t.Fatalf("is_platform_admin = %v, want %v", me.GlobalCapabilities.IsPlatformAdmin, tt.isPlatformAdmin)
			}
		})
	}
}

// --- grant management tests ---

func TestListStackGrantsRequiresManageAccess(t *testing.T) {
	t.Parallel()

	authorizer := &grantTestAuthorizer{denied: true}
	service := NewService(Service{Authorizer: authorizer})
	ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: "user-123"})

	_, err := service.ListStackGrants(ctx, ListStackGrantsCommand{TenantID: "tenant_1", StackID: "stack_1"})
	if err != ErrForbidden {
		t.Fatalf("error = %v, want ErrForbidden", err)
	}
}

func TestListStackGrantsReturnsGrants(t *testing.T) {
	t.Parallel()

	subj, _ := authz.SubjectFromKeycloakSub("user-123")
	stk, _ := authz.StackFromID("stack_1")
	grant1, _ := authz.NewGrant(subj, stk, authz.RoleOwner)
	authorizer := &grantTestAuthorizer{
		grants: authz.ListGrantsResult{Grants: []authz.Grant{grant1}},
	}
	service := NewService(Service{Authorizer: authorizer})
	ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: "user-123"})

	entries, err := service.ListStackGrants(ctx, ListStackGrantsCommand{TenantID: "tenant_1", StackID: "stack_1"})
	if err != nil {
		t.Fatalf("ListStackGrants() error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("grants = %d, want 1", len(entries))
	}
	if entries[0].UserID != "user-123" || entries[0].Role != "owner" {
		t.Fatalf("grant = %+v, want user-123/owner", entries[0])
	}
}

func TestPutStackGrantAssignsNewRole(t *testing.T) {
	t.Parallel()

	authorizer := &grantTestAuthorizer{
		grants:    authz.ListGrantsResult{},
		writeErr:  nil,
		deleteErr: nil,
	}
	userDir := &grantTestUserDirectory{user: DirectoryUser{ID: "target-123", Username: "bob"}}
	service := NewService(Service{Authorizer: authorizer, UserDirectory: userDir})
	ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: "owner-123"})

	entries, err := service.PutStackGrant(ctx, PutStackGrantCommand{
		TenantID: "tenant_1",
		StackID:  "stack_1",
		UserID:   "target-123",
		Role:     "operator",
	})
	if err != nil {
		t.Fatalf("PutStackGrant() error = %v", err)
	}
	if authorizer.writeCalls != 1 {
		t.Fatalf("write calls = %d, want 1", authorizer.writeCalls)
	}
	_ = entries
}

func TestPutStackGrantIdempotentForSameRole(t *testing.T) {
	t.Parallel()

	subj, _ := authz.SubjectFromKeycloakSub("target-123")
	stk, _ := authz.StackFromID("stack_1")
	existingGrant, _ := authz.NewGrant(subj, stk, authz.RoleOperator)
	authorizer := &grantTestAuthorizer{
		grants: authz.ListGrantsResult{Grants: []authz.Grant{existingGrant}},
	}
	userDir := &grantTestUserDirectory{user: DirectoryUser{ID: "target-123", Username: "bob"}}
	service := NewService(Service{Authorizer: authorizer, UserDirectory: userDir})
	ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: "owner-123"})

	entries, err := service.PutStackGrant(ctx, PutStackGrantCommand{
		TenantID: "tenant_1",
		StackID:  "stack_1",
		UserID:   "target-123",
		Role:     "operator",
	})
	if err != nil {
		t.Fatalf("PutStackGrant() error = %v", err)
	}
	if authorizer.writeCalls != 0 {
		t.Fatalf("write calls = %d, want 0 (idempotent)", authorizer.writeCalls)
	}
	if len(entries) != 1 || entries[0].Role != "operator" {
		t.Fatalf("entries = %+v, want [operator]", entries)
	}
}

func TestPutStackGrantRejectsLastOwnerRemoval(t *testing.T) {
	t.Parallel()

	subj, _ := authz.SubjectFromKeycloakSub("owner-123")
	stk, _ := authz.StackFromID("stack_1")
	onlyOwner, _ := authz.NewGrant(subj, stk, authz.RoleOwner)
	authorizer := &grantTestAuthorizer{
		grants: authz.ListGrantsResult{Grants: []authz.Grant{onlyOwner}},
	}
	userDir := &grantTestUserDirectory{user: DirectoryUser{ID: "owner-123", Username: "alice"}}
	service := NewService(Service{Authorizer: authorizer, UserDirectory: userDir})
	ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: "admin-123"})

	_, err := service.PutStackGrant(ctx, PutStackGrantCommand{
		TenantID: "tenant_1",
		StackID:  "stack_1",
		UserID:   "owner-123",
		Role:     "viewer",
	})
	if !errors.Is(err, ErrLastOwnerProtected) {
		t.Fatalf("error = %v, want ErrLastOwnerProtected", err)
	}
}

func TestPutStackGrantValidatesTargetUser(t *testing.T) {
	t.Parallel()

	authorizer := &grantTestAuthorizer{grants: authz.ListGrantsResult{}}
	userDir := &grantTestUserDirectory{err: ErrTargetUserNotFound}
	service := NewService(Service{Authorizer: authorizer, UserDirectory: userDir})
	ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: "admin-123"})

	_, err := service.PutStackGrant(ctx, PutStackGrantCommand{
		TenantID: "tenant_1",
		StackID:  "stack_1",
		UserID:   "nonexistent-123",
		Role:     "viewer",
	})
	if err == nil {
		t.Fatal("error = nil, want ErrTargetUserNotFound")
	}
}

func TestRevokeStackGrantRemovesRole(t *testing.T) {
	t.Parallel()

	subj, _ := authz.SubjectFromKeycloakSub("target-123")
	stk, _ := authz.StackFromID("stack_1")
	existingGrant, _ := authz.NewGrant(subj, stk, authz.RoleOperator)
	authorizer := &grantTestAuthorizer{
		grants: authz.ListGrantsResult{Grants: []authz.Grant{existingGrant}},
	}
	service := NewService(Service{Authorizer: authorizer})
	ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: "owner-123"})

	err := service.RevokeStackGrant(ctx, RevokeStackGrantCommand{
		TenantID: "tenant_1",
		StackID:  "stack_1",
		UserID:   "target-123",
	})
	if err != nil {
		t.Fatalf("RevokeStackGrant() error = %v", err)
	}
	if authorizer.deleteCalls != 1 {
		t.Fatalf("delete calls = %d, want 1", authorizer.deleteCalls)
	}
}

func TestRevokeStackGrantRejectsLastOwnerRemoval(t *testing.T) {
	t.Parallel()

	subj, _ := authz.SubjectFromKeycloakSub("owner-123")
	stk, _ := authz.StackFromID("stack_1")
	onlyOwner, _ := authz.NewGrant(subj, stk, authz.RoleOwner)
	authorizer := &grantTestAuthorizer{
		grants: authz.ListGrantsResult{Grants: []authz.Grant{onlyOwner}},
	}
	service := NewService(Service{Authorizer: authorizer})
	ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: "admin-123"})

	err := service.RevokeStackGrant(ctx, RevokeStackGrantCommand{
		TenantID: "tenant_1",
		StackID:  "stack_1",
		UserID:   "owner-123",
	})
	if !errors.Is(err, ErrLastOwnerProtected) {
		t.Fatalf("error = %v, want ErrLastOwnerProtected", err)
	}
}

func TestPutStackGrantCreatesAuditRecord(t *testing.T) {
	t.Parallel()

	authorizer := &grantTestAuthorizer{grants: authz.ListGrantsResult{}}
	userDir := &grantTestUserDirectory{user: DirectoryUser{ID: "target-123"}}
	audit := &recordingAuditRepository{}
	service := NewService(Service{Authorizer: authorizer, UserDirectory: userDir, Audit: audit})
	ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: "admin-123"})

	_, err := service.PutStackGrant(ctx, PutStackGrantCommand{
		TenantID: "tenant_1",
		StackID:  "stack_1",
		UserID:   "target-123",
		Role:     "viewer",
	})
	if err != nil {
		t.Fatalf("PutStackGrant() error = %v", err)
	}
	if len(audit.events) != 1 {
		t.Fatalf("audit events = %d, want 1", len(audit.events))
	}
	if audit.events[0].Action != traits.AuditActionRoleChange {
		t.Fatalf("audit action = %q, want %q", audit.events[0].Action, traits.AuditActionRoleChange)
	}
	if audit.events[0].TargetUser != "target-123" {
		t.Fatalf("audit target = %q, want target-123", audit.events[0].TargetUser)
	}
}

func TestRevokeStackGrantCreatesAuditRecord(t *testing.T) {
	t.Parallel()

	subj, _ := authz.SubjectFromKeycloakSub("target-123")
	stk, _ := authz.StackFromID("stack_1")
	existingGrant, _ := authz.NewGrant(subj, stk, authz.RoleViewer)
	authorizer := &grantTestAuthorizer{
		grants: authz.ListGrantsResult{Grants: []authz.Grant{existingGrant}},
	}
	audit := &recordingAuditRepository{}
	service := NewService(Service{Authorizer: authorizer, Audit: audit})
	ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: "admin-123"})

	err := service.RevokeStackGrant(ctx, RevokeStackGrantCommand{
		TenantID: "tenant_1",
		StackID:  "stack_1",
		UserID:   "target-123",
	})
	if err != nil {
		t.Fatalf("RevokeStackGrant() error = %v", err)
	}
	if len(audit.events) != 1 {
		t.Fatalf("audit events = %d, want 1", len(audit.events))
	}
	if audit.events[0].Action != traits.AuditActionRevoke {
		t.Fatalf("audit action = %q, want %q", audit.events[0].Action, traits.AuditActionRevoke)
	}
}

func TestPutStackGrantDependencyFailureFailsClosed(t *testing.T) {
	t.Parallel()

	authorizer := &grantTestAuthorizer{grants: authz.ListGrantsResult{}, writeErr: authz.ErrUnavailable}
	userDir := &grantTestUserDirectory{user: DirectoryUser{ID: "target-123"}}
	service := NewService(Service{Authorizer: authorizer, UserDirectory: userDir})
	ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: "admin-123"})

	_, err := service.PutStackGrant(ctx, PutStackGrantCommand{
		TenantID: "tenant_1",
		StackID:  "stack_1",
		UserID:   "target-123",
		Role:     "viewer",
	})
	if !errors.Is(err, authz.ErrUnavailable) {
		t.Fatalf("error = %v, want ErrUnavailable", err)
	}
}

// --- test doubles ---

type grantTestAuthorizer struct {
	grants      authz.ListGrantsResult
	writeErr    error
	deleteErr   error
	writeCalls  int
	deleteCalls int
	denied      bool
}

func (a *grantTestAuthorizer) Check(_ context.Context, request authz.CheckRequest) (authz.CheckResult, error) {
	if a.denied {
		return authz.CheckResult{Allowed: false}, nil
	}
	return authz.CheckResult{Allowed: true}, nil
}
func (a *grantTestAuthorizer) BatchCheck(ctx context.Context, request authz.BatchCheckRequest) (authz.BatchCheckResult, error) {
	result := authz.BatchCheckResult{Results: make([]authz.CheckResult, len(request.Checks))}
	for i := range request.Checks {
		decision, err := a.Check(ctx, request.Checks[i])
		if err != nil {
			return authz.BatchCheckResult{}, err
		}
		result.Results[i] = decision
	}
	return result, nil
}
func (a *grantTestAuthorizer) ListAccessibleStacks(context.Context, authz.ListAccessibleStacksRequest) (authz.ListAccessibleStacksResult, error) {
	return authz.ListAccessibleStacksResult{}, nil
}
func (a *grantTestAuthorizer) ListGrants(context.Context, authz.ListGrantsRequest) (authz.ListGrantsResult, error) {
	return a.grants, nil
}
func (a *grantTestAuthorizer) WriteRelationships(_ context.Context, _ authz.Mutation) error {
	a.writeCalls++
	return a.writeErr
}
func (a *grantTestAuthorizer) DeleteRelationships(_ context.Context, _ authz.Mutation) error {
	a.deleteCalls++
	return a.deleteErr
}

type grantTestUserDirectory struct {
	user DirectoryUser
	err  error
}

func (d *grantTestUserDirectory) SearchUsers(context.Context, string, int, int) ([]DirectoryUser, error) {
	return nil, nil
}
func (d *grantTestUserDirectory) GetUserByID(context.Context, string) (DirectoryUser, error) {
	if d.err != nil {
		return DirectoryUser{}, d.err
	}
	return d.user, nil
}
