# AUTH-021: Stack Access UI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add stack grant management endpoints (Go) and build the stack Access UI screen (React) so owners/admins can view, assign, replace, and revoke per-stack roles.

**Architecture:** Backend adds 3 HTTP endpoints wired through new service methods that authorize via `canManageAccess`, resolve user profiles from Keycloak, mutate OpenFGA relationships with confirmation, and audit. Frontend replaces the `/stacks/:stackId/access` route placeholder with a two-panel screen (grants list + user search/assign) using TanStack Query, following existing component/testing patterns.

**Tech Stack:** Go (net/http, authz.Authorizer, authdispatch.Outbox), React 18, React Router 6, TanStack Query 5, Vitest + Testing Library, lucide-react.

## Global Constraints

- All `/v1` endpoints require authentication via `authn.RequireAuthentication`
- Mutations use `authz.Mutation{confirm: true}` for higher-consistency writes
- Mutations create audit records via `traits.SecurityAuditEvent`
- Last-owner guard: cannot remove/demote the sole stack owner
- Frontend capability gating via `<RequireCapability>` — no frontend-only enforcement
- Error states: 401/403/404/503 handled by shared `useQueryErrorBoundary`
- No secrets, tokens, or credentials committed or logged
- Backend tests use `t.Parallel()` with `newAPITestDependencies()` pattern
- Frontend tests colocate `*.test.tsx` files, use mock auth provider

---

### Task 1: Backend — Service layer command types, sentinel errors, and ListStackGrants

**Files:**
- Modify: `internal/app/service.go` — add command/result types after existing command types (around line 300) + add `ListStackGrants` method after `SearchUsers` (around line 840)

**Interfaces:**
- Consumes: `authz.Authorizer`, `UserDirectory`, `authn.PrincipalFromContext`, `authorizeStack`, `requireAuthorizer`, `authz.StackFromID`, `authz.SubjectFromKeycloakSub`
- Produces: `ListStackGrantsCommand`, `GrantView`, `ListStackGrantsResult`, `ErrLastOwner`, `(service *Service) ListStackGrants(ctx, command) (ListStackGrantsResult, error)`

- [ ] **Step 1: Add sentinel error and command/result types**

Insert after `var ErrSelfApprovalForbidden` (service.go line 34):

```go
var ErrLastOwner = errors.New("cannot remove the last stack owner")
```

Insert after the `TemplateRunLog` command types and before the `Service` struct (before line 167):

```go
type ListStackGrantsCommand struct {
	TenantID traits.TenantID
	StackID  traits.StackID
}

type GrantView struct {
	UserSub     string `json:"userSub"`
	Role        string `json:"role"`
	DisplayName string `json:"displayName"`
	Email       string `json:"email"`
}

type ListStackGrantsResult struct {
	Grants []GrantView
}

type AssignStackRoleCommand struct {
	TenantID traits.TenantID
	StackID  traits.StackID
	UserSub  string
	Role     string
}

type RevokeStackRoleCommand struct {
	TenantID traits.TenantID
	StackID  traits.StackID
	UserSub  string
}
```

- [ ] **Step 2: Add `ListStackGrants` method**

Insert after `SearchUsers` method (after line 840):

```go
func (service *Service) ListStackGrants(ctx context.Context, command ListStackGrantsCommand) (ListStackGrantsResult, error) {
	if _, err := requireAuthorizer(ctx, service.Authorizer); err != nil {
		return ListStackGrantsResult{}, err
	}
	if err := authorizeStack(ctx, service.Authorizer, command.StackID, authz.PermissionManageAccess, ErrForbidden); err != nil {
		return ListStackGrantsResult{}, err
	}
	stack, err := authz.StackFromID(string(command.StackID))
	if err != nil {
		return ListStackGrantsResult{}, fmt.Errorf("list grants stack: %w", err)
	}
	result, err := service.Authorizer.ListGrants(ctx, authz.ListGrantsRequest{Stack: stack})
	if err != nil {
		return ListStackGrantsResult{}, err
	}

	grants := make([]GrantView, 0, len(result.Grants))
	for _, grant := range result.Grants {
		userSub := strings.TrimPrefix(grant.Subject().String(), "user:")
		role := grant.Role().String()
		gv := GrantView{UserSub: userSub, Role: role}

		if service.UserDirectory != nil {
			users, searchErr := service.UserDirectory.SearchUsers(ctx, userSub, 0, 1)
			if searchErr == nil && len(users) > 0 {
				gv.DisplayName = displayNameFromUser(users[0])
				gv.Email = users[0].Email
			}
		}
		if gv.DisplayName == "" {
			gv.DisplayName = userSub
		}
		grants = append(grants, gv)
	}

	return ListStackGrantsResult{Grants: grants}, nil
}

func displayNameFromUser(user DirectoryUser) string {
	if user.FirstName != "" || user.LastName != "" {
		return strings.TrimSpace(user.FirstName + " " + user.LastName)
	}
	return user.Username
}
```

- [ ] **Step 3: Verify compilation**

Run: `go build ./...` from worktree root
Expected: exit 0

- [ ] **Step 4: Commit**

```bash
git add internal/app/service.go
git commit -m "feat(app): add ListStackGrants command types and service method"
```

---

### Task 2: Backend — AssignStackRole and RevokeStackRole service methods

**Files:**
- Modify: `internal/app/service.go` — add methods after `ListStackGrants`

**Interfaces:**
- Consumes: `authz.NewGrant`, `authz.NewMutation`, `authz.RoleFromDirectRelation`, `authz.SubjectFromKeycloakSub`
- Produces: `(service *Service) AssignStackRole(ctx, command) (GrantView, error)`, `(service *Service) RevokeStackRole(ctx, command) error`

- [ ] **Step 1: Add `AssignStackRole` method**

Insert after `ListStackGrants`:

```go
func (service *Service) AssignStackRole(ctx context.Context, command AssignStackRoleCommand) (GrantView, error) {
	principal, err := requirePrincipal(ctx)
	if err != nil {
		return GrantView{}, err
	}
	if err := authorizeStack(ctx, service.Authorizer, command.StackID, authz.PermissionManageAccess, ErrForbidden); err != nil {
		return GrantView{}, err
	}

	role, err := authz.RoleFromDirectRelation(command.Role)
	if err != nil {
		return GrantView{}, fmt.Errorf("%w: %v", ErrInvalidCommand, err)
	}

	if service.UserDirectory != nil {
		users, searchErr := service.UserDirectory.SearchUsers(ctx, command.UserSub, 0, 1)
		if searchErr != nil || len(users) == 0 {
			return GrantView{}, fmt.Errorf("%w: target user not found in directory", ErrInvalidCommand)
		}
	}

	stack, err := authz.StackFromID(string(command.StackID))
	if err != nil {
		return GrantView{}, fmt.Errorf("assign role stack: %w", err)
	}
	subject, err := authz.SubjectFromKeycloakSub(command.UserSub)
	if err != nil {
		return GrantView{}, fmt.Errorf("%w: invalid user sub", ErrInvalidCommand)
	}

	currentGrants, err := service.listGrantsForStack(ctx, stack)
	if err != nil {
		return GrantView{}, err
	}

	var currentRole string
	for _, g := range currentGrants.Grants {
		if g.Subject().String() == subject.String() {
			currentRole = g.Role().String()
			break
		}
	}

	if currentRole == "owner" && command.Role != "owner" {
		ownerCount := 0
		for _, g := range currentGrants.Grants {
			if g.Role() == authz.RoleOwner {
				ownerCount++
			}
		}
		if ownerCount == 1 {
			return GrantView{}, fmt.Errorf("%w: assign another owner before changing this role", ErrLastOwner)
		}
	}

	if currentRole == command.Role {
		return GrantView{UserSub: command.UserSub, Role: command.Role}, nil
	}

	grant, err := authz.NewGrant(subject, stack, role)
	if err != nil {
		return GrantView{}, fmt.Errorf("create assign grant: %w", err)
	}
	addMutation, err := authz.NewMutation([]authz.Grant{grant}, true)
	if err != nil {
		return GrantView{}, fmt.Errorf("create assign mutation: %w", err)
	}

	if currentRole != "" {
		oldRole, _ := authz.RoleFromDirectRelation(currentRole)
		oldGrant, _ := authz.NewGrant(subject, stack, oldRole)
		delMutation, delErr := authz.NewMutation([]authz.Grant{oldGrant}, false)
		if delErr != nil {
			return GrantView{}, fmt.Errorf("create remove-old-role mutation: %w", delErr)
		}
		if err := service.Authorizer.DeleteRelationships(ctx, delMutation); err != nil {
			return GrantView{}, fmt.Errorf("remove existing role: %w", err)
		}
	}

	if err := service.Authorizer.WriteRelationships(ctx, addMutation); err != nil {
		return GrantView{}, fmt.Errorf("assign stack role: %w", err)
	}

	service.auditError(ctx, traits.SecurityAuditEvent{
		ActorSubject: principal.Subject,
		Action:       traits.AuditActionGrant,
		TargetUser:   command.UserSub,
		TenantID:     command.TenantID,
		StackID:      command.StackID,
		OldRole:      currentRole,
		NewRole:      command.Role,
		Outcome:      traits.AuditOutcomeSuccess,
	})

	return GrantView{UserSub: command.UserSub, Role: command.Role}, nil
}
```

- [ ] **Step 2: Add helper and `RevokeStackRole` method**

Insert the helper:

```go
func (service *Service) listGrantsForStack(ctx context.Context, stack authz.Stack) (authz.ListGrantsResult, error) {
	return service.Authorizer.ListGrants(ctx, authz.ListGrantsRequest{Stack: stack})
}
```

Insert `RevokeStackRole`:

```go
func (service *Service) RevokeStackRole(ctx context.Context, command RevokeStackRoleCommand) error {
	principal, err := requirePrincipal(ctx)
	if err != nil {
		return err
	}
	if err := authorizeStack(ctx, service.Authorizer, command.StackID, authz.PermissionManageAccess, ErrForbidden); err != nil {
		return err
	}

	stack, err := authz.StackFromID(string(command.StackID))
	if err != nil {
		return fmt.Errorf("revoke role stack: %w", err)
	}
	subject, err := authz.SubjectFromKeycloakSub(command.UserSub)
	if err != nil {
		return fmt.Errorf("%w: invalid user sub", ErrInvalidCommand)
	}

	currentGrants, err := service.listGrantsForStack(ctx, stack)
	if err != nil {
		return err
	}

	var targetRole string
	ownerCount := 0
	for _, g := range currentGrants.Grants {
		if g.Role() == authz.RoleOwner {
			ownerCount++
		}
		if g.Subject().String() == subject.String() {
			targetRole = g.Role().String()
		}
	}

	if targetRole == "" {
		return nil
	}

	if targetRole == "owner" && ownerCount == 1 {
		return fmt.Errorf("%w: cannot remove the last owner; assign another owner first", ErrLastOwner)
	}

	role, _ := authz.RoleFromDirectRelation(targetRole)
	grant, _ := authz.NewGrant(subject, stack, role)
	mutation, err := authz.NewMutation([]authz.Grant{grant}, true)
	if err != nil {
		return fmt.Errorf("create revoke mutation: %w", err)
	}

	if err := service.Authorizer.DeleteRelationships(ctx, mutation); err != nil {
		return fmt.Errorf("revoke stack role: %w", err)
	}

	service.auditError(ctx, traits.SecurityAuditEvent{
		ActorSubject: principal.Subject,
		Action:       traits.AuditActionRevoke,
		TargetUser:   command.UserSub,
		TenantID:     command.TenantID,
		StackID:      command.StackID,
		OldRole:      targetRole,
		Outcome:      traits.AuditOutcomeSuccess,
	})

	return nil
}
```

- [ ] **Step 3: Verify compilation**

Run: `go build ./...`
Expected: exit 0

- [ ] **Step 4: Commit**

```bash
git add internal/app/service.go
git commit -m "feat(app): add AssignStackRole and RevokeStackRole service methods"
```

---

### Task 3: Backend — HTTP handlers for grant endpoints

**Files:**
- Modify: `internal/api/server.go` — add 3 route registrations in `NewServer` (after users/search route, line 78), add 3 handler methods before `writeAppError` (before line 658)

**Interfaces:**
- Consumes: `app.ListStackGrantsCommand`, `app.AssignStackRoleCommand`, `app.RevokeStackRoleCommand`, `app.GrantView`, existing `writeJSON`, `writeAppError`, `decodeRequestBody`
- Produces: `handleListStackGrants`, `handleAssignStackRole`, `handleRevokeStackRole`

- [ ] **Step 1: Add route registrations**

In `NewServer`, after line 78 (`server.handleTenantRoute("GET /v1/tenants/{tenant_id}/users/search", ...)`), add:

```go
server.handleTenantRoute("GET /v1/tenants/{tenant_id}/stacks/{stack_id}/grants", server.handleListStackGrants)
server.handleTenantRoute("PUT /v1/tenants/{tenant_id}/stacks/{stack_id}/grants", server.handleAssignStackRole)
server.handleTenantRoute("DELETE /v1/tenants/{tenant_id}/stacks/{stack_id}/grants/{user_sub}", server.handleRevokeStackRole)
```

- [ ] **Step 2: Add handler methods and DTOs**

Insert before `writeAppError` (before line 658):

```go
type listStackGrantsResponse struct {
	Grants []app.GrantView `json:"grants"`
}

func (server *Server) handleListStackGrants(response http.ResponseWriter, request *http.Request) {
	result, err := server.service.ListStackGrants(request.Context(), app.ListStackGrantsCommand{
		TenantID: traits.TenantID(request.PathValue("tenant_id")),
		StackID:  traits.StackID(request.PathValue("stack_id")),
	})
	if err != nil {
		writeAppError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, listStackGrantsResponse{Grants: result.Grants})
}

type assignStackRoleRequest struct {
	UserSub string `json:"userSub"`
	Role    string `json:"role"`
}

func (server *Server) handleAssignStackRole(response http.ResponseWriter, request *http.Request) {
	var body assignStackRoleRequest
	if !decodeRequestBody(response, request, &body) {
		return
	}
	grant, err := server.service.AssignStackRole(request.Context(), app.AssignStackRoleCommand{
		TenantID: traits.TenantID(request.PathValue("tenant_id")),
		StackID:  traits.StackID(request.PathValue("stack_id")),
		UserSub:  body.UserSub,
		Role:     body.Role,
	})
	if err != nil {
		writeAppError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, grant)
}

func (server *Server) handleRevokeStackRole(response http.ResponseWriter, request *http.Request) {
	err := server.service.RevokeStackRole(request.Context(), app.RevokeStackRoleCommand{
		TenantID: traits.TenantID(request.PathValue("tenant_id")),
		StackID:  traits.StackID(request.PathValue("stack_id")),
		UserSub:  request.PathValue("user_sub"),
	})
	if err != nil {
		writeAppError(response, err)
		return
	}
	response.WriteHeader(http.StatusNoContent)
}
```

- [ ] **Step 3: Add `ErrLastOwner` to `writeAppError`**

In `writeAppError`, add a case before the `default` case (after the conflict block):

```go
case errors.Is(err, app.ErrLastOwner):
	writeError(response, http.StatusConflict, "last_owner", err.Error())
```

- [ ] **Step 4: Verify compilation**

Run: `go build ./...`
Expected: exit 0

- [ ] **Step 5: Commit**

```bash
git add internal/api/server.go
git commit -m "feat(api): add stack grant list, assign, and revoke HTTP handlers"
```

---

### Task 4: Backend — Handler integration tests

**Files:**
- Modify: `internal/api/server_test.go` — add test functions before `TestMeReturnsIdentityWithGlobalCapabilities` (before line 2694)

**Interfaces:**
- Consumes: `apiAuthorizer` (needs `grants` and `listGrantsErr` fields), `apiTestDependencies`
- Produces: `TestListStackGrantsSuccess`, `TestListStackGrantsForbidden`, `TestAssignStackRoleSuccess`, `TestRevokeStackRoleSuccess`, `TestRevokeStackRoleLastOwner`, etc.

- [ ] **Step 1: Extend `apiAuthorizer` with grants support**

Add fields to `apiAuthorizer` (around line 2245, after the `truncateBatchResult` field):

```go
grants        []authz.Grant
listGrantsErr error
deleteErr     error
```

Replace the existing no-op `ListGrants` (line 2303) with:

```go
func (authorizer *apiAuthorizer) ListGrants(context.Context, authz.ListGrantsRequest) (authz.ListGrantsResult, error) {
	if authorizer.listGrantsErr != nil {
		return authz.ListGrantsResult{}, authorizer.listGrantsErr
	}
	return authz.ListGrantsResult{Grants: authorizer.grants}, nil
}
```

Replace the existing no-op `DeleteRelationships` (line 2309) with:

```go
func (authorizer *apiAuthorizer) DeleteRelationships(context.Context, authz.Mutation) error {
	return authorizer.deleteErr
}
```

- [ ] **Step 2: Add `apiTestDependencies` grants helper**

Add a method to `apiTestDependencies` after the `service()` method:

```go
func (deps *apiTestDependencies) withGrants(grants []authz.Grant) *apiTestDependencies {
	deps.authorizer.grants = grants
	return deps
}
```

- [ ] **Step 3: Add tests**

Insert before `TestMeReturnsIdentityWithGlobalCapabilities` (before line 2694):

```go
func TestListStackGrantsSuccess(t *testing.T) {
	t.Parallel()

	subject, _ := authz.SubjectFromKeycloakSub("user_abc")
	stack, _ := authz.StackFromID("stack_123")
	grant, _ := authz.NewGrant(subject, stack, authz.RoleOwner)
	deps := newAPITestDependencies().withGrants([]authz.Grant{grant})
	server := NewServer(deps.service(), configuredTenantID)
	response := httptest.NewRecorder()
	request := authenticatedRequest(http.MethodGet, "/v1/tenants/tenant_123/stacks/stack_123/grants", nil)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusOK, response.Body.String())
	}
	var body listStackGrantsResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Grants) != 1 {
		t.Fatalf("grants = %d, want 1", len(body.Grants))
	}
	if body.Grants[0].Role != "owner" {
		t.Fatalf("role = %q, want owner", body.Grants[0].Role)
	}
	if body.Grants[0].UserSub != "user_abc" {
		t.Fatalf("userSub = %q, want user_abc", body.Grants[0].UserSub)
	}
}

func TestListStackGrantsForbidden(t *testing.T) {
	t.Parallel()

	deps := newAPITestDependencies()
	deps.authorizer.denied = true
	server := NewServer(deps.service(), configuredTenantID)
	response := httptest.NewRecorder()
	request := authenticatedRequest(http.MethodGet, "/v1/tenants/tenant_123/stacks/stack_123/grants", nil)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusForbidden, response.Body.String())
	}
}

func TestAssignStackRoleSuccess(t *testing.T) {
	t.Parallel()

	deps := newAPITestDependencies()
	deps.userDirectory = apiFakeUserDirectory{users: []app.DirectoryUser{{ID: "user_abc", Username: "alice"}}}
	server := NewServer(deps.service(), configuredTenantID)
	body := `{"userSub":"user_abc","role":"viewer"}`
	response := httptest.NewRecorder()
	request := authenticatedRequest(http.MethodPut, "/v1/tenants/tenant_123/stacks/stack_123/grants", strings.NewReader(body))

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusOK, response.Body.String())
	}
}

func TestAssignStackRoleInvalidRole(t *testing.T) {
	t.Parallel()

	deps := newAPITestDependencies()
	server := NewServer(deps.service(), configuredTenantID)
	body := `{"userSub":"user_abc","role":"superadmin"}`
	response := httptest.NewRecorder()
	request := authenticatedRequest(http.MethodPut, "/v1/tenants/tenant_123/stacks/stack_123/grants", strings.NewReader(body))

	server.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusBadRequest, response.Body.String())
	}
}

func TestRevokeStackRoleSuccess(t *testing.T) {
	t.Parallel()

	subject, _ := authz.SubjectFromKeycloakSub("user_abc")
	stack, _ := authz.StackFromID("stack_123")
	ownerSubject, _ := authz.SubjectFromKeycloakSub("admin")
	ownerGrant, _ := authz.NewGrant(ownerSubject, stack, authz.RoleOwner)
	viewerGrant, _ := authz.NewGrant(subject, stack, authz.RoleViewer)
	deps := newAPITestDependencies().withGrants([]authz.Grant{ownerGrant, viewerGrant})
	server := NewServer(deps.service(), configuredTenantID)
	response := httptest.NewRecorder()
	request := authenticatedRequest(http.MethodDelete, "/v1/tenants/tenant_123/stacks/stack_123/grants/user_abc", nil)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusNoContent, response.Body.String())
	}
}

func TestRevokeStackRoleLastOwner(t *testing.T) {
	t.Parallel()

	subject, _ := authz.SubjectFromKeycloakSub("user_abc")
	stack, _ := authz.StackFromID("stack_123")
	grant, _ := authz.NewGrant(subject, stack, authz.RoleOwner)
	deps := newAPITestDependencies().withGrants([]authz.Grant{grant})
	server := NewServer(deps.service(), configuredTenantID)
	response := httptest.NewRecorder()
	request := authenticatedRequest(http.MethodDelete, "/v1/tenants/tenant_123/stacks/stack_123/grants/user_abc", nil)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusConflict, response.Body.String())
	}
}

func TestListStackGrantsEmpty(t *testing.T) {
	t.Parallel()

	deps := newAPITestDependencies()
	server := NewServer(deps.service(), configuredTenantID)
	response := httptest.NewRecorder()
	request := authenticatedRequest(http.MethodGet, "/v1/tenants/tenant_123/stacks/stack_123/grants", nil)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusOK, response.Body.String())
	}
	var body listStackGrantsResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Grants) != 0 {
		t.Fatalf("grants = %d, want 0", len(body.Grants))
	}
}

func TestAssignStackRoleForbidden(t *testing.T) {
	t.Parallel()

	deps := newAPITestDependencies()
	deps.authorizer.denied = true
	server := NewServer(deps.service(), configuredTenantID)
	body := `{"userSub":"user_abc","role":"viewer"}`
	response := httptest.NewRecorder()
	request := authenticatedRequest(http.MethodPut, "/v1/tenants/tenant_123/stacks/stack_123/grants", strings.NewReader(body))

	server.ServeHTTP(response, request)

	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusForbidden, response.Body.String())
	}
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/api/... -count=1 -run "TestListStackGrants|TestAssignStackRole|TestRevokeStackRole"`
Expected: all PASS

- [ ] **Step 5: Commit**

```bash
git add internal/api/server_test.go
git commit -m "test(api): add integration tests for stack grant endpoints"
```

---

### Task 5: Frontend — API types and client functions

**Files:**
- Modify: `web/src/api/types.ts` — add `GrantView` and `DirectoryUser` types
- Modify: `web/src/api/client.ts` — add `listStackGrants`, `assignStackRole`, `revokeStackRole`, `searchUsers`
- Modify: `web/src/api/queryKeys.ts` — add `stackGrants`, `userSearch` keys

**Interfaces:**
- Consumes: `requestJSON`, `requestNoContent`, `encodeURIComponent`
- Produces: `listStackGrants(tenantID, stackID)`, `assignStackRole(tenantID, stackID, body)`, `revokeStackRole(tenantID, stackID, userSub)`, `searchUsers(tenantID, q, first, max)`, `queryKeys.stackGrants`, `queryKeys.userSearch`

- [ ] **Step 1: Add types to `web/src/api/types.ts`**

Append after the last type definition (after `TemplateRunLog`):

```typescript
export interface GrantView {
  userSub: string;
  role: string;
  displayName: string;
  email: string;
}

export interface ListGrantsResponse {
  grants: GrantView[];
}

export interface DirectoryUser {
  id: string;
  username: string;
  email: string;
  firstName: string;
  lastName: string;
}

export interface SearchUsersResponse {
  users: DirectoryUser[];
  first: number;
  max: number;
}
```

- [ ] **Step 2: Add client functions to `web/src/api/client.ts`**

Add before `async function authHeaders()` (before line 157):

```typescript
interface AssignStackRoleBody {
  userSub: string;
  role: string;
}

export function listStackGrants(tenantID: string, stackID: string): Promise<ListGrantsResponse> {
  return requestJSON(`/v1/tenants/${encodeURIComponent(tenantID)}/stacks/${encodeURIComponent(stackID)}/grants`);
}

export function assignStackRole(tenantID: string, stackID: string, body: AssignStackRoleBody): Promise<GrantView> {
  return requestJSON(`/v1/tenants/${encodeURIComponent(tenantID)}/stacks/${encodeURIComponent(stackID)}/grants`, {
    method: "PUT",
    body: JSON.stringify(body)
  });
}

export function revokeStackRole(tenantID: string, stackID: string, userSub: string): Promise<void> {
  return requestNoContent(`/v1/tenants/${encodeURIComponent(tenantID)}/stacks/${encodeURIComponent(stackID)}/grants/${encodeURIComponent(userSub)}`, {
    method: "DELETE"
  });
}

export function searchUsers(tenantID: string, q: string, first: number, max: number): Promise<SearchUsersResponse> {
  const params = new URLSearchParams({ q });
  if (first > 0) {
    params.set("first", String(first));
  }
  params.set("max", String(max));
  return requestJSON(`/v1/tenants/${encodeURIComponent(tenantID)}/users/search?${params.toString()}`);
}
```

Add the import for the new types at the top of `client.ts`, updating the import from `./types`:

```typescript
import type {
  ApiErrorBody,
  DirectoryUser,
  GrantView,
  ListGrantsResponse,
  Operation,
  SearchUsersResponse,
  Stack,
  StackTemplate,
  StackView,
  TemplateRegistration,
  TemplateRevision,
  TemplateRun,
  TemplateRunLog,
  TemplateVariable
} from "./types";
```

- [ ] **Step 3: Add query keys to `web/src/api/queryKeys.ts`**

Add after the existing keys:

```typescript
stackGrants: (tenantID: string, stackID: string) => ["stackGrants", tenantID, stackID] as const,
userSearch: (tenantID: string, query: string) => ["userSearch", tenantID, query] as const,
```

- [ ] **Step 4: Verify TypeScript compilation**

Run: `npx tsc --noEmit`
Expected: exit 0

- [ ] **Step 5: Commit**

```bash
git add web/src/api/types.ts web/src/api/client.ts web/src/api/queryKeys.ts
git commit -m "feat(web): add stack grant and user search API client functions"
```

---

### Task 6: Frontend — TanStack Query hooks

**Files:**
- Modify: `web/src/api/queries.ts` — add 4 hooks after existing mutation hooks

**Interfaces:**
- Consumes: `useQuery`, `useMutation`, `useQueryClient`, `queryKeys`, `client.*`
- Produces: `useStackGrantsQuery`, `useSearchUsersQuery`, `useAssignStackRoleMutation`, `useRevokeStackRoleMutation`

- [ ] **Step 1: Add query hooks**

Append after `useCancelRunMutation` (after line 171):

```typescript
export function useStackGrantsQuery(tenantID: string, stackID: string) {
  return useQuery({
    queryKey: queryKeys.stackGrants(tenantID, stackID),
    queryFn: () => client.listStackGrants(tenantID, stackID),
    enabled: stackID !== ""
  });
}

export function useSearchUsersQuery(tenantID: string, query: string) {
  return useQuery({
    queryKey: queryKeys.userSearch(tenantID, query),
    queryFn: () => client.searchUsers(tenantID, query, 0, 20),
    enabled: query.length >= 2
  });
}

export function useAssignStackRoleMutation(tenantID: string, stackID: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: Parameters<typeof client.assignStackRole>[2]) => client.assignStackRole(tenantID, stackID, body),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.stackGrants(tenantID, stackID) });
    }
  });
}

export function useRevokeStackRoleMutation(tenantID: string, stackID: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (userSub: string) => client.revokeStackRole(tenantID, stackID, userSub),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.stackGrants(tenantID, stackID) });
    }
  });
}
```

Add the import for `DirectoryUser` in the `useSearchUsersQuery` — no additional imports needed since the type flows through the client function's return type.

- [ ] **Step 2: Verify TypeScript compilation**

Run: `npx tsc --noEmit`
Expected: exit 0

- [ ] **Step 3: Commit**

```bash
git add web/src/api/queries.ts
git commit -m "feat(web): add stack grants and user search TanStack Query hooks"
```

---

### Task 7: Frontend — CSS styles for access management

**Files:**
- Modify: `web/src/styles.css` — append new styles before the media queries (before line 544)

**Interfaces:**
- Consumes: none (pure CSS)
- Produces: `.grant-row`, `.role-badge`, `.role-badge.owner`, `.role-badge.operator`, `.role-badge.approver`, `.role-badge.viewer`, `.grant-actions`, `.search-dropdown`, `.search-result-item`, `.search-result-item.assigned`, `.selected-user-card`, `.undo-banner`, `.form-row`

- [ ] **Step 1: Add CSS**

Insert before `@media (max-width: 920px)` line (before line 544):

```css
.grant-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  border: 1px solid #d2d7cc;
  border-radius: 6px;
  padding: 10px 12px;
  background: #ffffff;
}

.grant-row .grant-user {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.grant-row .grant-user span {
  font-size: 0.88rem;
  font-weight: 650;
}

.grant-row .grant-user small {
  color: #66705f;
  font-size: 0.78rem;
}

.role-badge {
  display: inline-flex;
  align-items: center;
  min-width: 72px;
  justify-content: center;
  border-radius: 4px;
  padding: 2px 8px;
  font-size: 0.75rem;
  font-weight: 700;
  text-transform: uppercase;
}

.role-badge.owner {
  color: #24467c;
  background: #eef4ff;
}

.role-badge.operator {
  color: #2f6f5e;
  background: #eef7f3;
}

.role-badge.approver {
  color: #7a5d3c;
  background: #fef7ee;
}

.role-badge.viewer {
  color: #536064;
  background: #f8faf7;
}

.grant-actions {
  display: flex;
  gap: 6px;
}

.grant-actions button {
  min-height: 28px;
  border: 1px solid #c8cfc4;
  border-radius: 4px;
  padding: 3px 8px;
  font-size: 0.8rem;
  color: #293541;
  background: #ffffff;
  cursor: pointer;
}

.grant-actions button.danger {
  color: #991b1b;
  border-color: #f3b7a8;
}

.grants-list {
  display: grid;
  gap: 6px;
  margin: 0;
  padding: 0;
  list-style: none;
}

.search-dropdown {
  position: absolute;
  top: 100%;
  left: 0;
  right: 0;
  z-index: 10;
  border: 1px solid #c8cfc4;
  border-radius: 6px;
  margin-top: 2px;
  background: #ffffff;
  box-shadow: 0 4px 12px rgba(31, 41, 51, 0.1);
  max-height: 200px;
  overflow-y: auto;
}

.search-result-item {
  padding: 8px 10px;
  cursor: pointer;
  font-size: 0.88rem;
}

.search-result-item:hover,
.search-result-item.focused {
  background: #eef4ff;
}

.search-result-item.assigned {
  color: #66705f;
  cursor: default;
  background: #f8faf7;
}

.search-result-item small {
  display: block;
  color: #66705f;
  font-size: 0.76rem;
}

.search-wrapper {
  position: relative;
}

.selected-user-card {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  border: 1px solid #c8cfc4;
  border-radius: 6px;
  padding: 8px 10px;
  margin-top: 10px;
  background: #eef4ff;
}

.selected-user-card span {
  font-weight: 650;
  font-size: 0.88rem;
}

.selected-user-card button {
  border: none;
  background: none;
  cursor: pointer;
  color: #536064;
  padding: 2px;
}

.undo-banner {
  position: fixed;
  bottom: 0;
  left: 0;
  right: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 12px;
  padding: 10px 24px;
  background: #eef7f3;
  border-top: 1px solid #d2d7cc;
  font-size: 0.88rem;
  z-index: 20;
  animation: slideUp 0.2s ease-out;
}

.undo-banner button {
  min-height: 28px;
  border: 1px solid #2f6f5e;
  border-radius: 4px;
  padding: 3px 10px;
  color: #2f6f5e;
  background: #ffffff;
  font-weight: 650;
  cursor: pointer;
}

@keyframes slideUp {
  from {
    transform: translateY(100%);
  }
  to {
    transform: translateY(0);
  }
}

.form-row {
  display: grid;
  gap: 10px;
}

.form-row select {
  min-height: 38px;
  border: 1px solid #c8cfc4;
  border-radius: 6px;
  padding: 8px 10px;
  color: #202833;
  background: #ffffff;
}
```

- [ ] **Step 2: Commit**

```bash
git add web/src/styles.css
git commit -m "feat(web): add access management CSS styles"
```

---

### Task 8: Frontend — StackAccessScreen component

**Files:**
- Create: `web/src/features/stacks/StackAccessScreen.tsx`

**Interfaces:**
- Consumes: `useStackGrantsQuery`, `useSearchUsersQuery`, `useAssignStackRoleMutation`, `useRevokeStackRoleMutation`, `useParams`, `RequireCapability`, `tenantID`
- Produces: `StackAccessScreen` (default export)

- [ ] **Step 1: Create the component**

```typescript
import { useState, useEffect, useRef, useCallback } from "react";
import { useParams } from "react-router-dom";
import { Loader2, Search, Shield, Trash2, X } from "lucide-react";
import {
  useStackGrantsQuery,
  useSearchUsersQuery,
  useAssignStackRoleMutation,
  useRevokeStackRoleMutation
} from "../../api/queries";
import type { DirectoryUser, GrantView } from "../../api/types";
import { tenantID } from "../../config";

const ROLES = ["owner", "operator", "approver", "viewer"] as const;
type Role = (typeof ROLES)[number];

interface UndoEntry {
  userSub: string;
  role: string;
  displayName: string;
}

export default function StackAccessScreen() {
  const { stackId = "" } = useParams<{ stackId: string }>();
  const grants = useStackGrantsQuery(tenantID, stackId);

  const [search, setSearch] = useState("");
  const [selectedUser, setSelectedUser] = useState<DirectoryUser | null>(null);
  const [selectedRole, setSelectedRole] = useState<Role>("viewer");
  const [searchFocused, setSearchFocused] = useState(false);
  const [undoEntry, setUndoEntry] = useState<UndoEntry | null>(null);
  const [mutationError, setMutationError] = useState("");
  const [confirmRevoke, setConfirmRevoke] = useState<string | null>(null);
  const debouncedSearch = useDebounce(search, 300);
  const searchResults = useSearchUsersQuery(tenantID, debouncedSearch);
  const assignMutation = useAssignStackRoleMutation(tenantID, stackId);
  const revokeMutation = useRevokeStackRoleMutation(tenantID, stackId);
  const searchInputRef = useRef<HTMLInputElement>(null);
  const undoTimeoutRef = useRef<ReturnType<typeof setTimeout>>();

  const assignedSubs = new Set(
    (grants.data?.grants ?? []).map((g) => g.userSub)
  );

  const handleAssign = useCallback(async () => {
    if (!selectedUser) return;
    setMutationError("");
    try {
      await assignMutation.mutateAsync({
        userSub: selectedUser.id,
        role: selectedRole
      });
      setSelectedUser(null);
      setSearch("");
    } catch (err) {
      setMutationError(
        err instanceof Error ? err.message : "Failed to assign role"
      );
    }
  }, [selectedUser, selectedRole, assignMutation]);

  const handleRevoke = useCallback(
    async (grant: GrantView) => {
      setMutationError("");
      setConfirmRevoke(null);
      try {
        await revokeMutation.mutateAsync(grant.userSub);
        setUndoEntry({
          userSub: grant.userSub,
          role: grant.role,
          displayName: grant.displayName
        });
      } catch (err) {
        setMutationError(
          err instanceof Error ? err.message : "Failed to revoke role"
        );
      }
    },
    [revokeMutation]
  );

  const handleUndo = useCallback(async () => {
    if (!undoEntry) return;
    setMutationError("");
    try {
      await assignMutation.mutateAsync({
        userSub: undoEntry.userSub,
        role: undoEntry.role
      });
    } catch {
      setMutationError("Failed to restore role");
    }
    setUndoEntry(null);
  }, [undoEntry, assignMutation]);

  useEffect(() => {
    if (!undoEntry) return;
    undoTimeoutRef.current = setTimeout(() => setUndoEntry(null), 5000);
    return () => clearTimeout(undoTimeoutRef.current);
  }, [undoEntry]);

  useEffect(() => {
    if (revokeMutation.isSuccess || assignMutation.isSuccess) {
      setMutationError("");
    }
  }, [revokeMutation.isSuccess, assignMutation.isSuccess]);

  return (
    <section className="workflow-grid">
      <section className="panel">
        <h2>
          <Shield size={16} />
          Current Grants
        </h2>
        {grants.isLoading && (
          <p className="muted">
            <Loader2 size={14} className="spin" /> Loading grants...
          </p>
        )}
        {grants.isError && (
          <div className="alert">
            Failed to load grants.
            <button
              className="secondary-button"
              onClick={() => grants.refetch()}
              style={{ marginLeft: 10 }}
            >
              Retry
            </button>
          </div>
        )}
        {grants.data && grants.data.grants.length === 0 && (
          <p className="muted">
            No users have been assigned access yet. Use the panel on the right
            to add the first grant.
          </p>
        )}
        {grants.data && grants.data.grants.length > 0 && (
          <ul className="grants-list">
            {grants.data.grants.map((grant) => (
              <li key={grant.userSub} className="grant-row">
                <div className="grant-user">
                  <span>{grant.displayName}</span>
                  {grant.email && <small>{grant.email}</small>}
                </div>
                <span className={`role-badge ${grant.role}`}>{grant.role}</span>
                <div className="grant-actions">
                  {confirmRevoke === grant.userSub ? (
                    <>
                      <span style={{ fontSize: "0.82rem" }}>
                        Remove access?
                      </span>
                      <button
                        className="danger"
                        onClick={() => handleRevoke(grant)}
                      >
                        Confirm
                      </button>
                      <button onClick={() => setConfirmRevoke(null)}>
                        Cancel
                      </button>
                    </>
                  ) : (
                    <button
                      className="danger"
                      onClick={() => setConfirmRevoke(grant.userSub)}
                      aria-label={`Revoke ${grant.displayName}'s ${grant.role} role`}
                    >
                      <Trash2 size={14} />
                    </button>
                  )}
                </div>
              </li>
            ))}
          </ul>
        )}
      </section>

      <section className="panel">
        <h2>
          <Search size={16} />
          Assign Role
        </h2>
        {mutationError && <div className="alert">{mutationError}</div>}
        <div className="form-row">
          <div className="search-wrapper">
            <input
              ref={searchInputRef}
              type="text"
              placeholder="Search users by name or email..."
              value={search}
              onChange={(e) => {
                setSearch(e.target.value);
                setSelectedUser(null);
              }}
              onFocus={() => setSearchFocused(true)}
              onBlur={() => setTimeout(() => setSearchFocused(false), 150)}
              onKeyDown={(e) => {
                if (e.key === "Escape") {
                  setSearch("");
                  setSearchFocused(false);
                  searchInputRef.current?.blur();
                }
              }}
              aria-label="Search users"
              autoComplete="off"
            />
            {searchFocused &&
              debouncedSearch.length >= 2 &&
              searchResults.data && (
                <div className="search-dropdown">
                  {searchResults.data.users.length === 0 && (
                    <div className="search-result-item muted">
                      No users found
                    </div>
                  )}
                  {searchResults.data.users.map((user) => {
                    const assigned = assignedSubs.has(user.id);
                    const currentGrant = grants.data?.grants.find(
                      (g) => g.userSub === user.id
                    );
                    return (
                      <div
                        key={user.id}
                        className={`search-result-item${assigned ? " assigned" : ""}`}
                        onClick={() => {
                          if (!assigned) {
                            setSelectedUser(user);
                            setSearchFocused(false);
                          }
                        }}
                        role="option"
                        aria-selected={selectedUser?.id === user.id}
                      >
                        {user.firstName
                          ? `${user.firstName} ${user.lastName || ""}`.trim()
                          : user.username}
                        <small>
                          {user.email || user.username}
                          {currentGrant && ` — ${currentGrant.role}`}
                          {assigned && !currentGrant && " — assigned"}
                        </small>
                      </div>
                    );
                  })}
                </div>
              )}
          </div>

          {selectedUser && (
            <div className="selected-user-card">
              <span>
                {selectedUser.firstName
                  ? `${selectedUser.firstName} ${selectedUser.lastName || ""}`.trim()
                  : selectedUser.username}
              </span>
              <button
                onClick={() => setSelectedUser(null)}
                aria-label="Clear selected user"
              >
                <X size={14} />
              </button>
            </div>
          )}

          <div>
            <label htmlFor="role-select">Role</label>
            <select
              id="role-select"
              value={selectedRole}
              onChange={(e) => setSelectedRole(e.target.value as Role)}
            >
              {ROLES.map((r) => (
                <option key={r} value={r}>
                  {r.charAt(0).toUpperCase() + r.slice(1)}
                </option>
              ))}
            </select>
          </div>

          <button
            className="primary-button"
            onClick={handleAssign}
            disabled={!selectedUser || assignMutation.isPending}
          >
            {assignMutation.isPending ? (
              <>
                <Loader2 size={14} className="spin" />
                {assignedSubs.has(selectedUser?.id ?? "")
                  ? "Replacing..."
                  : "Assigning..."}
              </>
            ) : assignedSubs.has(selectedUser?.id ?? "") ? (
              "Replace Role"
            ) : (
              "Assign Role"
            )}
          </button>
        </div>
      </section>

      {undoEntry && (
        <div className="undo-banner">
          <span>
            Removed {undoEntry.displayName}&apos;s {undoEntry.role} access.
          </span>
          <button onClick={handleUndo}>
            {assignMutation.isPending ? (
              <Loader2 size={14} className="spin" />
            ) : (
              "Undo"
            )}
          </button>
        </div>
      )}
    </section>
  );
}

function useDebounce<T>(value: T, delay: number): T {
  const [debounced, setDebounced] = useState(value);
  useEffect(() => {
    const timer = setTimeout(() => setDebounced(value), delay);
    return () => clearTimeout(timer);
  }, [value, delay]);
  return debounced;
}
```

- [ ] **Step 2: Verify TypeScript compilation**

Run: `npx tsc --noEmit`
Expected: exit 0

- [ ] **Step 3: Commit**

```bash
git add web/src/features/stacks/StackAccessScreen.tsx
git commit -m "feat(web): add StackAccessScreen with grant list and role assignment"
```

---

### Task 9: Frontend — Route wiring

**Files:**
- Modify: `web/src/app/router.tsx` — replace `RoutePlaceholder` with `StackAccessScreen`

- [ ] **Step 1: Update router**

Add import:
```typescript
import StackAccessScreen from "../features/stacks/StackAccessScreen";
```

Replace line 51:
```tsx
children: [{ index: true, element: <RoutePlaceholder title="Access" /> }]
```
With:
```tsx
children: [{ index: true, element: <StackAccessScreen /> }]
```

- [ ] **Step 2: Verify TypeScript compilation**

Run: `npx tsc --noEmit`
Expected: exit 0

- [ ] **Step 3: Commit**

```bash
git add web/src/app/router.tsx
git commit -m "feat(web): wire StackAccessScreen to /stacks/:stackId/access route"
```

---

### Task 10: Frontend — StackAccessScreen tests

**Files:**
- Create: `web/src/features/stacks/StackAccessScreen.test.tsx`

- [ ] **Step 1: Create tests**

```typescript
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import StackAccessScreen from "./StackAccessScreen";
import type { ListGrantsResponse, SearchUsersResponse } from "../../api/types";

function wrapper(initialRoute = "/stacks/stack_123/access") {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false }
    }
  });
  return function Wrapper({ children }: { children: React.ReactNode }) {
    return (
      <QueryClientProvider client={queryClient}>
        <MemoryRouter initialEntries={[initialRoute]}>
          {children}
        </MemoryRouter>
      </QueryClientProvider>
    );
  };
}

const emptyGrants: ListGrantsResponse = { grants: [] };

const twoGrants: ListGrantsResponse = {
  grants: [
    {
      userSub: "u1",
      role: "owner",
      displayName: "Alice",
      email: "alice@example.com"
    },
    {
      userSub: "u2",
      role: "viewer",
      displayName: "Bob",
      email: "bob@example.com"
    }
  ]
};

const searchResults: SearchUsersResponse = {
  users: [
    {
      id: "u3",
      username: "charlie",
      email: "charlie@example.com",
      firstName: "Charlie",
      lastName: "Brown"
    }
  ],
  first: 0,
  max: 20
};

describe("StackAccessScreen", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it("shows loading state for grants", () => {
    vi.spyOn(globalThis, "fetch").mockImplementation(
      () => new Promise(() => {})
    );

    render(<StackAccessScreen />, { wrapper: wrapper() });

    expect(screen.getByText("Loading grants...")).toBeDefined();
  });

  it("renders grants list when data is loaded", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify(twoGrants), {
        status: 200,
        headers: { "content-type": "application/json" }
      })
    );

    render(<StackAccessScreen />, { wrapper: wrapper() });

    await waitFor(() => {
      expect(screen.getByText("Alice")).toBeDefined();
    });
    expect(screen.getByText("Bob")).toBeDefined();
    expect(screen.getByText("owner")).toBeDefined();
    expect(screen.getByText("viewer")).toBeDefined();
  });

  it("shows empty state when no grants", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify(emptyGrants), {
        status: 200,
        headers: { "content-type": "application/json" }
      })
    );

    render(<StackAccessScreen />, { wrapper: wrapper() });

    await waitFor(() => {
      expect(
        screen.getByText(
          "No users have been assigned access yet. Use the panel on the right to add the first grant."
        )
      ).toBeDefined();
    });
  });

  it("shows search results when typing 2+ characters", async () => {
    vi.spyOn(globalThis, "fetch")
      .mockResolvedValueOnce(
        new Response(JSON.stringify(emptyGrants), {
          status: 200,
          headers: { "content-type": "application/json" }
        })
      )
      .mockResolvedValueOnce(
        new Response(JSON.stringify(searchResults), {
          status: 200,
          headers: { "content-type": "application/json" }
        })
      );

    render(<StackAccessScreen />, { wrapper: wrapper() });
    const user = userEvent.setup();

    await waitFor(() => {
      expect(screen.getByText(/No users/)).toBeDefined();
    });

    const searchInput = screen.getByLabelText("Search users");
    await user.click(searchInput);
    await user.type(searchInput, "cha");

    await waitFor(() => {
      expect(screen.getByText("Charlie Brown")).toBeDefined();
    });
  });

  it("shows confirm state and triggers revoke", async () => {
    vi.spyOn(globalThis, "fetch")
      .mockResolvedValueOnce(
        new Response(JSON.stringify(twoGrants), {
          status: 200,
          headers: { "content-type": "application/json" }
        })
      )
      .mockResolvedValueOnce(
        new Response(null, {
          status: 204
        })
      )
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ grants: [twoGrants.grants[0]] }), {
          status: 200,
          headers: { "content-type": "application/json" }
        })
      );

    render(<StackAccessScreen />, { wrapper: wrapper() });
    const user = userEvent.setup();

    await waitFor(() => {
      expect(screen.getByText("Bob")).toBeDefined();
    });

    const revokeButton = screen.getByLabelText("Revoke Bob's viewer role");
    await user.click(revokeButton);

    expect(screen.getByText("Remove access?")).toBeDefined();
    expect(screen.getByText("Confirm")).toBeDefined();

    await user.click(screen.getByText("Confirm"));

    await waitFor(() => {
      expect(screen.getByText(/Removed Bob's viewer access/)).toBeDefined();
    });
  });

  it("shows error banner on failed revoke", async () => {
    vi.spyOn(globalThis, "fetch")
      .mockResolvedValueOnce(
        new Response(JSON.stringify(twoGrants), {
          status: 200,
          headers: { "content-type": "application/json" }
        })
      )
      .mockResolvedValueOnce(
        new Response(
          JSON.stringify({
            error: "last_owner",
            message: "cannot remove the last owner"
          }),
          {
            status: 409,
            headers: { "content-type": "application/json" }
          }
        )
      );

    render(<StackAccessScreen />, { wrapper: wrapper() });
    const user = userEvent.setup();

    await waitFor(() => {
      expect(screen.getByText("Alice")).toBeDefined();
    });

    await user.click(screen.getByLabelText("Revoke Alice's owner role"));
    await user.click(screen.getByText("Confirm"));

    await waitFor(() => {
      expect(
        screen.getByText("cannot remove the last owner")
      ).toBeDefined();
    });
  });

  it("assigns role when selecting a user and clicking assign", async () => {
    vi.spyOn(globalThis, "fetch")
      .mockResolvedValueOnce(
        new Response(JSON.stringify(emptyGrants), {
          status: 200,
          headers: { "content-type": "application/json" }
        })
      )
      .mockResolvedValueOnce(
        new Response(JSON.stringify(searchResults), {
          status: 200,
          headers: { "content-type": "application/json" }
        })
      )
      .mockResolvedValueOnce(
        new Response(
          JSON.stringify({
            userSub: "u3",
            role: "viewer",
            displayName: "Charlie Brown"
          }),
          {
            status: 200,
            headers: { "content-type": "application/json" }
          }
        )
      )
      .mockResolvedValueOnce(
        new Response(
          JSON.stringify({
            grants: [
              {
                userSub: "u3",
                role: "viewer",
                displayName: "Charlie Brown",
                email: "charlie@example.com"
              }
            ]
          }),
          {
            status: 200,
            headers: { "content-type": "application/json" }
          }
        )
      );

    render(<StackAccessScreen />, { wrapper: wrapper() });
    const user = userEvent.setup();

    await waitFor(() => {
      expect(screen.getByText(/No users/)).toBeDefined();
    });

    await user.click(screen.getByLabelText("Search users"));
    await user.type(screen.getByLabelText("Search users"), "cha");

    await waitFor(() => {
      expect(screen.getByText("Charlie Brown")).toBeDefined();
    });

    await user.click(screen.getByText("Charlie Brown"));
    await user.click(screen.getByText("Assign Role"));

    await waitFor(() => {
      expect(globalThis.fetch).toHaveBeenCalledTimes(4);
    });
  });

  it("has proper tab order for keyboard accessibility", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify(emptyGrants), {
        status: 200,
        headers: { "content-type": "application/json" }
      })
    );

    render(<StackAccessScreen />, { wrapper: wrapper() });
    const user = userEvent.setup();

    await waitFor(() => {
      expect(screen.getByText(/No users/)).toBeDefined();
    });

    await user.tab();
    expect(screen.getByLabelText("Search users")).toEqual(
      document.activeElement
    );

    await user.tab();
    expect(screen.getByLabelText("Role")).toEqual(document.activeElement);

    await user.tab();
    expect(screen.getByText("Assign Role")).toEqual(document.activeElement);
  });
});
```

- [ ] **Step 2: Run tests**

Run: `npx vitest run src/features/stacks/StackAccessScreen.test.tsx`
Expected: all PASS

- [ ] **Step 3: Commit**

```bash
git add web/src/features/stacks/StackAccessScreen.test.tsx
git commit -m "test(web): add StackAccessScreen component tests"
```

---

### Task 11: Integration — Run all tests and verify

- [ ] **Step 1: Run backend tests**

Run: `go test ./internal/... -count=1`
Expected: all PASS

- [ ] **Step 2: Run frontend tests**

Run: `npm test`
Workdir: `web/`
Expected: all PASS

- [ ] **Step 3: Verify frontend build**

Run: `npm run build`
Workdir: `web/`
Expected: exit 0

- [ ] **Step 4: Update sprint backlog**

Update `docs/sprint/authn_and_authz/README.md` row 84 (AUTH-021) status from `Not Started` to `Done`.

- [ ] **Step 5: Final commit**

```bash
git add docs/sprint/authn_and_authz/README.md
git commit -m "docs: mark AUTH-021 as Done in sprint backlog"
```
