# Endpoint Permission Matrix Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Enforce the AUTH-013 global and OpenFGA stack permission matrix on every existing API operation without disclosing inaccessible read resources.

**Architecture:** Authorization remains in `internal/app`, where the service derives the authenticated OpenFGA subject, resolves inherited resources to their stack, and calls the existing provider-neutral `authz.Authorizer`. Non-administrator stack lists scan tenant stacks in stable Postgres pages and authorize each bounded page with OpenFGA `BatchCheck`, preventing unary `ListObjects` truncation; HTTP handlers continue to decode requests and map stable application errors.

**Tech Stack:** Go, `net/http`, PostgreSQL with pgx, Keycloak principals, OpenFGA, Go standard-library tests.

## Global Constraints

- Every `/v1` route remains protected by the existing authentication middleware; `GET /healthz` remains public.
- `platform-admin` bypasses ordinary stack checks, but does not bypass authentication, tenant validation, or future self-approval protection.
- Catalog registration, status, revision list, and revision-variable routes require `platform-admin` or `stack-creator`.
- Explicit OpenFGA denial returns protected `404` for reads and `403` for mutations; authorization dependency failures fail closed as the existing stable `503` response.
- Non-administrator stack lists must scan stable tenant pages of at most 50 candidates and use `BatchCheck(can_view)` without returning partial results.
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

---

## Post-Review Corrections

### Task 6: Replace Unary ListObjects With Complete Batched Listing

**Files:**
- Modify: `internal/app/service.go:55-62`
- Modify: `internal/app/authorization.go:71-95`
- Modify: `internal/app/authorization_test.go`
- Modify: `internal/postgres/repositories.go:426-497`
- Modify: `internal/postgres/store_test.go`
- Modify: `internal/app/service_test.go`
- Modify: `internal/app/stack_authorization_test.go`
- Modify: `internal/api/server_test.go`
- Modify: `cmd/api/main_test.go`

**Interfaces:**
- Produces: `StackPageCursor { CreatedAt time.Time; ID traits.StackID }`.
- Produces: `StackRepository.ListStacksPage(ctx context.Context, tenantID traits.TenantID, after *StackPageCursor, limit int) ([]traits.Stack, error)`.
- Consumes: `authz.Authorizer.BatchCheck` with at most 50 `authz.CheckRequest` values.
- Removes: endpoint use of `ListAccessibleStacks`; retain the provider-neutral port for other bounded consumers.

- [ ] **Step 1: Write failing application tests for complete multi-page filtering**

Replace the old list-ID test with tests that configure a recording repository with 55 tenant stacks and a fake authorizer that allows every even-indexed stack. Assert:

```go
if got, want := repository.pageLimits, []int{50, 50}; !reflect.DeepEqual(got, want) {
    t.Fatalf("page limits = %#v, want %#v", got, want)
}
if got, want := authorizer.batchSizes, []int{50, 5}; !reflect.DeepEqual(got, want) {
    t.Fatalf("batch sizes = %#v, want %#v", got, want)
}
if len(stacks) != 28 {
    t.Fatalf("accessible stacks = %d, want 28", len(stacks))
}
```

Add separate tests proving an empty first page makes no OpenFGA call, a batch error returns no partial list, a result-count mismatch returns `authz.ErrMalformedResponse`, and `platform-admin` still calls `ListStacks` without paging or OpenFGA.

- [ ] **Step 2: Run the application tests to verify they fail**

Run: `go test ./internal/app -run 'TestListStacks(BatchesCompleteTenantScan|ReturnsNoPartialResults|RejectsMismatchedBatchResults|SkipsAuthorizationForEmptyTenant|AllowsPlatformAdmin)' -count=1`

Expected: FAIL because `ListStacks` still calls `ListAccessibleStacks` and `ListStacksByIDs`.

- [ ] **Step 3: Implement the paged repository contract**

Define the cursor and replace `ListStacksByIDs` in `StackRepository`:

```go
type StackPageCursor struct {
    CreatedAt time.Time
    ID        traits.StackID
}

type StackRepository interface {
    CreateStack(context.Context, traits.Stack) error
    GetStack(context.Context, traits.TenantID, traits.StackID) (traits.Stack, error)
    GetStackWithTemplates(context.Context, traits.TenantID, traits.StackID) (StackView, error)
    ListStacks(context.Context, traits.TenantID) ([]traits.Stack, error)
    ListStacksPage(context.Context, traits.TenantID, *StackPageCursor, int) ([]traits.Stack, error)
}
```

Implement a keyset query in Postgres. Pass `nil` for both cursor SQL parameters on the first page and the final record's values thereafter:

```sql
select id, tenant_id, name, slug, tags_json,
       default_credential_ids_json, created_by, created_at
from stacks
where tenant_id = $1
  and ($2::timestamptz is null or (created_at, id) < ($2, $3))
order by created_at desc, id desc
limit $4
```

Reject non-positive limits before querying. Remove `ListStacksByIDs` and its tests, then update every test double to implement `ListStacksPage` with copied cursor/page inputs.

- [ ] **Step 4: Write and run failing Postgres paging tests**

Seed stacks with equal and unequal timestamps, call page one with limit 2, then page two with the last record cursor. Assert no duplicates, stable descending order, tenant isolation, and an empty final page.

Run: `go test ./internal/postgres -run 'TestListStacksPage' -count=1`

Expected: FAIL before `ListStacksPage` exists; with no `tflive_POSTGRES_TEST_DSN`, compilation succeeds and tests report skipped.

- [ ] **Step 5: Implement complete BatchCheck filtering**

Use a fixed batch size matching OpenFGA's default maximum:

```go
const stackAuthorizationPageSize = 50

for {
    candidates, err := repository.ListStacksPage(ctx, tenantID, cursor, stackAuthorizationPageSize)
    if err != nil { return nil, fmt.Errorf("list stack candidates: %w", err) }
    if len(candidates) == 0 { return accessible, nil }

    checks := make([]authz.CheckRequest, len(candidates))
    for i, candidate := range candidates {
        stack, err := authz.StackFromID(string(candidate.ID))
        if err != nil { return nil, err }
        checks[i] = authz.CheckRequest{Subject: subject, Stack: stack, Permission: authz.PermissionView}
    }
    result, err := authorizer.BatchCheck(ctx, authz.BatchCheckRequest{Checks: checks})
    if err != nil { return nil, err }
    if len(result.Results) != len(candidates) {
        return nil, fmt.Errorf("%w: batch result count does not match stack candidates", authz.ErrMalformedResponse)
    }
    for i, decision := range result.Results {
        if decision.Allowed { accessible = append(accessible, candidates[i]) }
    }
    if len(candidates) < stackAuthorizationPageSize { return accessible, nil }
    last := candidates[len(candidates)-1]
    cursor = &StackPageCursor{CreatedAt: last.CreatedAt, ID: last.ID}
}
```

Return results only after the complete scan succeeds; any later-page error discards earlier allowed records.

- [ ] **Step 6: Run focused and package tests**

Run: `go test ./internal/app ./internal/api ./cmd/api -count=1`

Expected: PASS.

- [ ] **Step 7: Commit complete stack listing**

```bash
git add cmd/api/main_test.go internal/app internal/api/server_test.go internal/postgres
git commit -m "fix(authz): make stack listing complete"
```

### Task 7: Make Inherited Denials Indistinguishable

**Files:**
- Modify: `internal/app/authorization.go:45-123`
- Modify: `internal/app/authorization_test.go`
- Modify: `internal/api/server_test.go`

**Interfaces:**
- Consumes: caller-supplied `denied error` in `authorizedStackTemplate` and `authorizedTemplateRun`.
- Produces: identical externally visible results for missing and inaccessible inherited resources.

- [ ] **Step 1: Write paired missing/inaccessible tests**

Add table-driven service and route tests for stack-template config, upgrade, start-run, approval, cancellation, run detail, run logs, and log body. For each route, issue one request for an existing but denied resource and one for a repository `app.ErrNotFound`. Assert both statuses match:

```go
wantStatus := http.StatusNotFound
if mutation {
    wantStatus = http.StatusForbidden
}
if missing.Code != wantStatus || denied.Code != wantStatus {
    t.Fatalf("missing=%d denied=%d want=%d", missing.Code, denied.Code, wantStatus)
}
```

For mutations, assert no repository mutation or workflow signal occurs in either case.

- [ ] **Step 2: Run the paired tests to verify they fail**

Run: `go test ./internal/app ./internal/api -run 'TestInheritedResource(MissingAndDeniedReadsMatch|MissingAndDeniedMutationsMatch)' -count=1`

Expected: FAIL because repository `ErrNotFound` currently bypasses the caller-supplied denial error.

- [ ] **Step 3: Map only not-found resolution failures to denial**

Update both resolvers without hiding internal failures:

```go
stackTemplate, err := service.StackTemplates.GetStackTemplate(ctx, tenantID, stackTemplateID)
if errors.Is(err, ErrNotFound) {
    return traits.StackTemplate{}, denied
}
if err != nil {
    return traits.StackTemplate{}, err
}
```

Apply the same pattern to `TemplateRuns.GetTemplateRun`. Keep platform-administrator bypass after existence resolution so missing mutation targets receive the same stable `403` policy.

- [ ] **Step 4: Map missing authorizer to stable dependency failure**

Replace both untyped nil-authorizer errors with:

```go
return fmt.Errorf("%w: authorization not configured", authz.ErrUnavailable)
```

Add application and API tests asserting a nil authorizer returns `503 authorization_unavailable` and no protected operation proceeds.

- [ ] **Step 5: Run focused and package tests**

Run: `go test ./internal/app ./internal/api -count=1`

Expected: PASS.

- [ ] **Step 6: Commit non-disclosure and stable failures**

```bash
git add internal/app/authorization.go internal/app/authorization_test.go internal/api/server_test.go
git commit -m "fix(authz): hide inherited resource existence"
```

### Task 8: Prove Every Route's Permission Matrix

**Files:**
- Modify: `internal/api/server_test.go`

**Interfaces:**
- Consumes: the documented route matrix in `docs/authentication.md`.
- Produces: one route-level assertion for every current endpoint's global role or OpenFGA permission.

- [ ] **Step 1: Replace representative role tests with a complete route table**

Create a table containing all non-health routes. Each row specifies method, path, valid body, expected permission or global role, allowed role, denied role, allowed status, denied status, and a side-effect assertion for mutations. The stack-scoped rows must include:

```go
{name: "stack detail", permission: authz.PermissionView, allowedRole: authz.RoleViewer, deniedRole: authz.Role{}, deniedStatus: http.StatusNotFound}
{name: "install template", permission: authz.PermissionOperate, allowedRole: authz.RoleOperator, deniedRole: authz.RoleViewer, deniedStatus: http.StatusForbidden}
{name: "update config", permission: authz.PermissionOperate, allowedRole: authz.RoleOwner, deniedRole: authz.RoleApprover, deniedStatus: http.StatusForbidden}
{name: "upgrade template", permission: authz.PermissionOperate, allowedRole: authz.RoleOperator, deniedRole: authz.RoleViewer, deniedStatus: http.StatusForbidden}
{name: "start run", permission: authz.PermissionOperate, allowedRole: authz.RoleOperator, deniedRole: authz.RoleApprover, deniedStatus: http.StatusForbidden}
{name: "run detail", permission: authz.PermissionView, allowedRole: authz.RoleViewer, deniedRole: authz.Role{}, deniedStatus: http.StatusNotFound}
{name: "run logs", permission: authz.PermissionView, allowedRole: authz.RoleApprover, deniedRole: authz.Role{}, deniedStatus: http.StatusNotFound}
{name: "run log body", permission: authz.PermissionView, allowedRole: authz.RoleViewer, deniedRole: authz.Role{}, deniedStatus: http.StatusNotFound}
{name: "approve run", permission: authz.PermissionApprove, allowedRole: authz.RoleApprover, deniedRole: authz.RoleOperator, deniedStatus: http.StatusForbidden}
{name: "cancel run", permission: authz.PermissionOperate, allowedRole: authz.RoleOwner, deniedRole: authz.RoleApprover, deniedStatus: http.StatusForbidden}
```

Keep separate global-route rows proving `platform-admin` and `stack-creator` access plus ordinary-user `403`. Stack creation remains global-role tested and must not call OpenFGA `Check` before creation.

- [ ] **Step 2: Make test doubles enforce real filtering and failures**

The recording stack repository's `ListStacksPage` must slice its configured ordered list by cursor and limit. Extend `apiAuthorizer` with `batchErr`, `batchResults`, recorded batch requests, and role-derived decisions. Never make the fake return the complete stack list regardless of requested page or authorization result.

- [ ] **Step 3: Run matrix tests**

Run: `go test ./internal/api -run 'TestEndpointPermissionMatrix' -count=1`

Expected: PASS only when every route invokes the documented permission and denied mutations have no side effects.

- [ ] **Step 4: Run all API tests and commit**

Run: `go test ./internal/api -count=1`

Expected: PASS.

```bash
git add internal/api/server_test.go
git commit -m "test(authz): cover complete endpoint matrix"
```

### Task 9: Update Operations Documentation And Verify

**Files:**
- Modify: `docs/authentication.md:264-300`
- Modify: `docs/sprint/authn_and_authz/README.md:76`

**Interfaces:**
- Consumes: complete paged BatchCheck behavior and non-disclosure policy from Tasks 6-8.
- Produces: accurate operations documentation and final AUTH-013 completion evidence.

- [ ] **Step 1: Correct stack-list documentation**

Replace the `ListObjects` description with keyset-paged Postgres candidates and bounded `BatchCheck(can_view)` calls. Document the 50-check page bound, all-or-nothing response, stable ordering, and `503` behavior on any page failure.

- [ ] **Step 2: Correct inherited-resource error documentation**

State explicitly that missing and inaccessible inherited reads both return `404`, while missing and inaccessible inherited mutations both return `403`. Document nil authorizer as `503 authorization_unavailable`.

- [ ] **Step 3: Run formatting and complete verification**

Run: `gofmt -w cmd/api/main_test.go internal/app/authorization.go internal/app/authorization_test.go internal/app/service.go internal/app/service_test.go internal/app/stack_authorization_test.go internal/api/server_test.go internal/postgres/repositories.go internal/postgres/store_test.go`

Run: `go test ./... && go vet ./... && git diff --check`

Expected: all commands exit zero. Postgres runtime tests may report skipped when `tflive_POSTGRES_TEST_DSN` is unset, but must compile.

- [ ] **Step 4: Confirm worktree ownership and status**

Run: `git status --short` in both the feature worktree and main checkout. The tracked plan must exist only in the feature branch; neither checkout may contain an untracked copy.

- [ ] **Step 5: Commit final documentation**

```bash
git add docs/authentication.md docs/sprint/authn_and_authz/README.md docs/superpowers/plans/2026-07-18-endpoint-permission-matrix.md
git commit -m "docs(auth): document complete authorization listing"
```
