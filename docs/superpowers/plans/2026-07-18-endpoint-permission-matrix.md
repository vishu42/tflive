# Endpoint Permission Matrix Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Enforce the AUTH-013 global and OpenFGA stack permission matrix on every existing API operation without disclosing inaccessible read resources.

**Architecture:** Authorization remains in `internal/app`, where the service derives the authenticated OpenFGA subject, resolves inherited resources to their stack, and calls the existing provider-neutral `authz.Authorizer`. The stack list obtains authorized stack IDs from OpenFGA before Postgres reads product data; HTTP handlers continue to decode requests and map stable application errors.

**Tech Stack:** Go, `net/http`, PostgreSQL with pgx, Keycloak principals, OpenFGA, Go standard-library tests.

## Global Constraints

- Every `/v1` route remains protected by the existing authentication middleware; `GET /healthz` remains public.
- `platform-admin` bypasses ordinary stack checks, but does not bypass authentication, tenant validation, or future self-approval protection.
- Catalog registration, status, revision list, and revision-variable routes require `platform-admin` or `stack-creator`.
- Explicit OpenFGA denial returns protected `404` for reads and `403` for mutations; authorization dependency failures fail closed as the existing stable `503` response.
- Stack lists must use `ListAccessibleStacks(can_view)` and fetch only returned IDs for non-administrators.
- Do not expose OpenFGA SDK or wire types outside `internal/openfga`.

---

## File Structure

| File | Responsibility |
|---|---|
| `internal/app/authorization.go` | Principal role helpers, OpenFGA stack checks, inherited-resource resolution, and authorization error mapping. |
| `internal/app/service.go` | Calls authorization helpers from each catalog, stack, stack-template, run, and log use case; expands the stack repository contract for filtered listing. |
| `internal/app/authorization_test.go` | Unit tests for global, direct-stack, inherited-resource, list, and dependency-failure behavior. |
| `internal/postgres/repositories.go` | Implements tenant-scoped stack lookup constrained to an explicit set of authorized IDs. |
| `internal/postgres/store_test.go` | Verifies the filtered stack query retains ordering, tenant isolation, and empty-ID behavior. |
| `internal/api/server_test.go` | Proves representative HTTP routes return the matrix's stable `403`, protected `404`, and `503` outcomes with no mutation side effects. |
| `docs/authentication.md` | Publishes the endpoint matrix, list behavior, and error/non-disclosure rules. |
| `docs/sprint/authn_and_authz/README.md` | Marks AUTH-013 complete only after all plan verification succeeds. |

### Task 1: Add Filtered Stack Listing

**Files:**
- Modify: `internal/app/service.go:55-61`
- Modify: `internal/postgres/repositories.go:426-458`
- Modify: `internal/postgres/store_test.go:625-680`
- Modify: `internal/app/service_test.go:1461-1507`
- Modify: `internal/api/server_test.go:1557-1603`

**Interfaces:**
- Produces: `StackRepository.ListStacksByIDs(ctx context.Context, tenantID traits.TenantID, stackIDs []traits.StackID) ([]traits.Stack, error)`.
- Consumes: OpenFGA `ListAccessibleStacksResult.Stacks`, whose values render as `stack:<application-id>`.

- [ ] **Step 1: Write the failing Postgres and recording-repository tests**

Add `TestListStacksByIDsReturnsOnlyRequestedTenantStacksNewestFirst` in `internal/postgres/store_test.go`. Insert two `tenant_123` stacks and one stack from another tenant; call:

```go
stacks, err := store.ListStacksByIDs(ctx, "tenant_123", []traits.StackID{"stack_old", "stack_new"})
if err != nil {
    t.Fatalf("ListStacksByIDs() error = %v", err)
}
if got := []traits.StackID{stacks[0].ID, stacks[1].ID}; !reflect.DeepEqual(got, []traits.StackID{"stack_new", "stack_old"}) {
    t.Fatalf("stack IDs = %#v", got)
}
```

Add an empty-ID test asserting `ListStacksByIDs(ctx, "tenant_123", nil)` returns an empty slice without querying all tenant stacks. Extend both recording stack repositories with `gotListStackIDs []traits.StackID` and a `ListStacksByIDs` method so they continue to satisfy the expanded interface.

- [ ] **Step 2: Run the focused test to verify it fails**

Run: `go test ./internal/postgres -run 'TestListStacksByIDs' -count=1`

Expected: FAIL because `ListStacksByIDs` is undefined.

- [ ] **Step 3: Implement the filtered repository query**

Add the interface method and implement it on `*postgres.Store`. Return `[]traits.Stack{}` immediately for no IDs. For non-empty IDs, use pgx's array parameter support and preserve existing ordering:

```go
func (store *Store) ListStacksByIDs(ctx context.Context, tenantID traits.TenantID, stackIDs []traits.StackID) ([]traits.Stack, error) {
    if len(stackIDs) == 0 {
        return []traits.Stack{}, nil
    }
    rows, err := store.pool.Query(ctx, `
        select id, tenant_id, name, slug, tags_json, default_credential_ids_json, created_by, created_at
        from stacks
        where tenant_id = $1 and id = any($2)
        order by created_at desc, id desc
    `, tenantID, stackIDs)
    if err != nil {
        return nil, fmt.Errorf("list stacks by IDs: %w", err)
    }
    defer rows.Close()
    stacks := make([]traits.Stack, 0, len(stackIDs))
    for rows.Next() {
        stack, err := scanStack(rows)
        if err != nil {
            return nil, err
        }
        stacks = append(stacks, stack)
    }
    if err := rows.Err(); err != nil {
        return nil, fmt.Errorf("iterate stacks by IDs: %w", err)
    }
    return stacks, nil
}
```

Do not interpolate identifiers into SQL and do not call `ListStacks` as a fallback.

- [ ] **Step 4: Run focused tests to verify they pass**

Run: `go test ./internal/postgres ./internal/app ./internal/api -run 'TestListStacks(ByIDs|PassesTenant)' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit the filtered repository boundary**

```bash
git add internal/app/service.go internal/app/service_test.go internal/api/server_test.go internal/postgres/repositories.go internal/postgres/store_test.go
git commit -m "feat(authz): query authorized stacks by ID"
```

### Task 2: Add Application Authorization Primitives And Stack Lists

**Files:**
- Create: `internal/app/authorization.go`
- Create: `internal/app/authorization_test.go`
- Modify: `internal/app/service.go:35-53,633-662`

**Interfaces:**
- Produces: `authorizeStack(ctx context.Context, authorizer authz.Authorizer, stackID traits.StackID, permission authz.Permission, denied error) error`.
- Produces: `listAccessibleStacks(ctx context.Context, authorizer authz.Authorizer, repository StackRepository, tenantID traits.TenantID) ([]traits.Stack, error)`.
- Consumes: `authn.PrincipalFromContext`, `authz.SubjectFromKeycloakSub`, `authz.StackFromID`, `authz.Authorizer.Check`, and `authz.Authorizer.ListAccessibleStacks`.

- [ ] **Step 1: Write failing unit tests for direct checks and list filtering**

In `internal/app/authorization_test.go`, create a configurable fake authorizer recording `CheckRequest` and `ListAccessibleStacksRequest`. Add tests that prove:

```go
func TestGetStackChecksViewBeforeRepositoryRead(t *testing.T)
func TestGetStackDenialReturnsNotFoundWithoutRepositoryRead(t *testing.T)
func TestListStacksUsesAccessibleViewIDs(t *testing.T)
func TestListStacksReturnsEmptyWithoutTenantWideFallback(t *testing.T)
func TestListStacksAllowsPlatformAdminWithoutOpenFGACall(t *testing.T)
func TestStackAuthorizationUnavailablePropagates(t *testing.T)
```

For the direct denial test, configure `CheckResult{Allowed: false}` and assert `errors.Is(err, ErrNotFound)` plus zero `GetStackWithTemplates` calls. For the list test, return canonical `stack:stack_123` and assert the repository receives exactly `[]traits.StackID{"stack_123"}` with `PermissionView`.

- [ ] **Step 2: Run the focused tests to verify they fail**

Run: `go test ./internal/app -run 'Test(GetStackChecksView|GetStackDenial|ListStacksUsesAccessible|ListStacksReturnsEmpty|ListStacksAllowsPlatformAdmin|StackAuthorizationUnavailable)' -count=1`

Expected: FAIL because the use cases do not yet enforce OpenFGA decisions.

- [ ] **Step 3: Implement the primitives and apply them to direct stack reads and lists**

Create `internal/app/authorization.go` with role predicates and helpers. Use `ErrNotFound` as the passed denial error for read callers and return `ErrForbidden` for mutation callers in later tasks:

```go
func hasRealmRole(principal authn.Principal, wanted string) bool {
    for _, role := range principal.RealmRoles {
        if role == wanted {
            return true
        }
    }
    return false
}

func isPlatformAdmin(principal authn.Principal) bool {
    return hasRealmRole(principal, "platform-admin")
}

func requirePrincipal(ctx context.Context) (authn.Principal, error) {
    principal, ok := authn.PrincipalFromContext(ctx)
    if !ok || principal.Subject == "" {
        return authn.Principal{}, ErrUnauthenticated
    }
    return principal, nil
}

func authorizeStack(ctx context.Context, authorizer authz.Authorizer, stackID traits.StackID, permission authz.Permission, denied error) error {
    principal, err := requirePrincipal(ctx)
    if err != nil || isPlatformAdmin(principal) { return err }
    if authorizer == nil { return errors.New("authorization not configured") }
    subject, err := authz.SubjectFromKeycloakSub(principal.Subject)
    if err != nil { return err }
    stack, err := authz.StackFromID(string(stackID))
    if err != nil { return err }
    result, err := authorizer.Check(ctx, authz.CheckRequest{Subject: subject, Stack: stack, Permission: permission})
    if err != nil { return err }
    if !result.Allowed { return denied }
    return nil
}
```

Treat a nil `Authorizer` as a regular non-nil configuration error, never as allow. In `GetStack`, validate the command then call `authorizeStack(..., PermissionView, ErrNotFound)` before `GetStackWithTemplates`. In `ListStacks`, require a principal; platform admins use `ListStacks`; other principals call `ListAccessibleStacks` with `PermissionView`, convert each validated `authz.Stack` by removing only the `stack:` prefix, and call `ListStacksByIDs`.

Update existing successful service tests to use `authn.ContextWithPrincipal` and an allowed fake authorizer. Update `apiAuthorizer` so its default `Check` result is allowed, and change successful stack/list API requests to `authenticatedRequest`; individual denial tests explicitly set `allowed: false`. This preserves the intent of pre-existing success tests while making authorization visible in their setup.

- [ ] **Step 4: Run focused tests to verify they pass**

Run: `go test ./internal/app -run 'Test(GetStackChecksView|GetStackDenial|ListStacksUsesAccessible|ListStacksReturnsEmpty|ListStacksAllowsPlatformAdmin|StackAuthorizationUnavailable)' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit direct stack enforcement**

```bash
git add internal/app/authorization.go internal/app/authorization_test.go internal/app/service.go
git commit -m "feat(authz): enforce stack view permissions"
```

### Task 3: Enforce Inherited Stack Permissions

**Files:**
- Modify: `internal/app/authorization.go`
- Modify: `internal/app/service.go:429-480,485-542,545-629,712-843`
- Modify: `internal/app/authorization_test.go`
- Modify: `internal/app/service_test.go:1435-1549`
- Modify: `internal/api/server_test.go:627-1344,1538-1745`

**Interfaces:**
- Produces: `authorizedStackTemplate(ctx context.Context, tenantID traits.TenantID, stackTemplateID traits.StackTemplateID, permission authz.Permission, denied error) (traits.StackTemplate, error)`.
- Produces: `authorizedTemplateRun(ctx context.Context, tenantID traits.TenantID, runID traits.TemplateRunID, permission authz.Permission, denied error) (traits.TemplateRun, error)`.
- Consumes: `StackTemplateRepository.GetStackTemplate` and `TemplateRunRepository.GetTemplateRun`; `traits.StackTemplate.StackID` connects each inherited resource to OpenFGA.

- [ ] **Step 1: Write failing permission-matrix unit tests**

Add table-driven tests that put `StackID: "stack_123"` on all stack-template fixtures and prove these calls use the stated permission before a write or protected payload read:

```go
tests := []struct {
    name       string
    permission authz.Permission
    call       func(*Service, context.Context) error
}{
    {"install", authz.PermissionOperate, func(s *Service, c context.Context) error { _, err := s.AddTemplateToStack(c, addCommand); return err }},
    {"config", authz.PermissionOperate, func(s *Service, c context.Context) error { _, err := s.UpdateStackTemplateConfig(c, updateCommand); return err }},
    {"upgrade", authz.PermissionOperate, func(s *Service, c context.Context) error { _, err := s.UpgradeStackTemplate(c, upgradeCommand); return err }},
    {"start", authz.PermissionOperate, func(s *Service, c context.Context) error { _, err := s.StartTemplateRun(c, startCommand); return err }},
    {"approve", authz.PermissionApprove, func(s *Service, c context.Context) error { return s.ApproveRun(c, approveCommand) }},
    {"cancel", authz.PermissionOperate, func(s *Service, c context.Context) error { return s.CancelRun(c, cancelCommand) }},
}
```

Also test that denied mutations return `ErrForbidden` and do not create templates/runs, update config, write approval/cancellation, or signal workflows. Test that denied run reads, log lists, and log body reads return `ErrNotFound`; OpenFGA errors must propagate unchanged. Add a role table for owner, operator, approver, viewer, and unassigned decisions using the fake authorizer response appropriate to the requested relation.

- [ ] **Step 2: Run the focused tests to verify they fail**

Run: `go test ./internal/app -run 'Test(OperatePermissionMatrix|ApprovePermissionMatrix|DeniedInheritedReads|DeniedMutationsHaveNoSideEffects|InheritedAuthorizationUnavailable)' -count=1`

Expected: FAIL because stack-template and run operations currently access repositories before authorization.

- [ ] **Step 3: Implement inherited-resource checks**

Implement `authorizedStackTemplate` to retrieve the minimal template record, invoke `authorizeStack` with its `StackID`, and return the record only after authorization succeeds. Implement `authorizedTemplateRun` to retrieve the run, retrieve `run.StackTemplateID`, then authorize that template's `StackID`.

Use them at the beginning of each relevant use case:

```go
// AddTemplateToStack: authorizeStack(ctx, service.Authorizer, command.StackID, authz.PermissionOperate, ErrForbidden)
// UpdateStackTemplateConfig, UpgradeStackTemplate, StartTemplateRun: authorizedStackTemplate(..., authz.PermissionOperate, ErrForbidden)
// ApproveRun: authorizedTemplateRun(..., authz.PermissionApprove, ErrForbidden)
// CancelRun: authorizedTemplateRun(..., authz.PermissionOperate, ErrForbidden)
// GetTemplateRun, GetTemplateRunLog, ListTemplateRunLogs: authorizedTemplateRun(..., authz.PermissionView, ErrNotFound)
```

Reuse resolved records inside each use case rather than querying them a second time. For log functions, pass an already-authorized run to a private helper or keep the first resolved run in scope so the log metadata/object-store read happens only after `can_view` succeeds.

- [ ] **Step 4: Add failing route-level denial tests and verify they fail**

Extend `apiAuthorizer` to record check requests and configure `allowed bool` and `checkErr error`. In `internal/api/server_test.go`, add a table covering a denied direct stack read (`404`), a denied inherited run/log read (`404`), and denied install/config/upgrade/start/approve/cancel mutations (`403`). Assert each mutation recorder remains untouched. Add one test returning `authz.ErrUnavailable` and assert status `503` with `authorization_unavailable`.

Run: `go test ./internal/api -run 'Test(ProtectedResourceRoutes|StackMutationRoutes|AuthorizationDependencyFailure)' -count=1`

Expected: FAIL until the service-level enforcement is complete.

- [ ] **Step 5: Run application and API matrix tests to verify they pass**

Run: `go test ./internal/app ./internal/api -run 'Test(OperatePermissionMatrix|ApprovePermissionMatrix|DeniedInheritedReads|DeniedMutationsHaveNoSideEffects|InheritedAuthorizationUnavailable|ProtectedResourceRoutes|StackMutationRoutes|AuthorizationDependencyFailure)' -count=1`

Expected: PASS.

- [ ] **Step 6: Commit inherited-resource enforcement**

```bash
git add internal/app/authorization.go internal/app/authorization_test.go internal/app/service.go internal/app/service_test.go internal/api/server_test.go
git commit -m "feat(authz): enforce inherited stack permissions"
```

### Task 4: Enforce Global Template Catalog Permissions

**Files:**
- Modify: `internal/app/authorization.go`
- Modify: `internal/app/service.go:315-354,664-709`
- Modify: `internal/app/authorization_test.go`
- Modify: `internal/api/server_test.go:822-941,1488-1555`

**Interfaces:**
- Produces: `requireTemplateCatalogAccess(ctx context.Context) error`.
- Consumes: `authn.Principal.RealmRoles`; allows only `platform-admin` and `stack-creator`.

- [ ] **Step 1: Write failing catalog authorization tests**

Add application tests for `RegisterTemplate`, `GetTemplateRegistration`, `ListTemplateRevisions`, and `GetTemplateRevisionVariables`. Use an ordinary principal with no global roles and assert `ErrForbidden` plus no repository or workflow activity. Repeat each with `stack-creator` and `platform-admin` and assert the existing success behavior remains.

Add route-level table coverage that asserts `403` with error code `forbidden` for the same four routes under an ordinary authenticated principal.

- [ ] **Step 2: Run the focused tests to verify they fail**

Run: `go test ./internal/app ./internal/api -run 'Test(TemplateCatalogRequiresGlobalRole|TemplateCatalogRoutesRejectOrdinaryUser|TemplateCatalogAllowsCreatorAndAdmin)' -count=1`

Expected: FAIL because catalog methods currently accept any authenticated caller.

- [ ] **Step 3: Implement the catalog role gate**

Add the helper beside `isPlatformAdmin`:

```go
func requireTemplateCatalogAccess(ctx context.Context) error {
    principal, err := requirePrincipal(ctx)
    if err != nil {
        return err
    }
    if isPlatformAdmin(principal) || hasRealmRole(principal, "stack-creator") {
        return nil
    }
    return ErrForbidden
}
```

Call it after command validation and before any catalog repository or workflow call in all four use cases. `RegisterTemplate` may obtain the actor from the already-validated principal instead of parsing the context twice.

- [ ] **Step 4: Run focused tests to verify they pass**

Run: `go test ./internal/app ./internal/api -run 'Test(TemplateCatalogRequiresGlobalRole|TemplateCatalogRoutesRejectOrdinaryUser|TemplateCatalogAllowsCreatorAndAdmin)' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit catalog authorization**

```bash
git add internal/app/authorization.go internal/app/authorization_test.go internal/app/service.go internal/api/server_test.go
git commit -m "feat(authz): restrict template catalog routes"
```

### Task 5: Document, Verify, And Close AUTH-013

**Files:**
- Modify: `docs/authentication.md:243-263`
- Modify: `docs/sprint/authn_and_authz/README.md:76`

**Interfaces:**
- Consumes: the implemented route and permission matrix from `docs/superpowers/specs/2026-07-18-endpoint-permission-matrix-design.md`.
- Produces: operational documentation that names the permitted global roles, stack permissions, protected `404`, mutation `403`, and fail-closed `503` outcomes.

- [ ] **Step 1: Add the runtime endpoint enforcement documentation**

After the existing runtime authorization bullets in `docs/authentication.md`, add a concise table for every current route. State that catalog routes require `platform-admin` or `stack-creator`; describe `can_view`, `can_operate`, and `can_approve` mappings; state that `can_manage_access` has no current route; and document that non-admin lists use `ListObjects(can_view)` before a tenant-scoped ID query. State the protected-read `404`, denied-mutation `403`, and authorization-service `503` behavior.

- [ ] **Step 2: Mark the backlog item done**

Change AUTH-013's status in `docs/sprint/authn_and_authz/README.md` from `Not Started` to `Done` only after the test commands in the next steps succeed.

- [ ] **Step 3: Run the complete test suite**

Run: `go test ./...`

Expected: PASS.

- [ ] **Step 4: Run formatting and diff validation**

Run: `gofmt -w internal/app/authorization.go internal/app/authorization_test.go internal/app/service.go internal/app/service_test.go internal/api/server_test.go internal/postgres/repositories.go internal/postgres/store_test.go`

Run: `go test ./... && git diff --check`

Expected: PASS with no formatting changes left and no whitespace errors.

- [ ] **Step 5: Commit documentation and backlog status**

```bash
git add docs/authentication.md docs/sprint/authn_and_authz/README.md
git commit -m "docs(auth): publish endpoint permission matrix"
```
