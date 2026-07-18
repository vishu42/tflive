# Stack Creation Authorization Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Permit only globally authorized users to create stacks and give each persisted stack a confirmed OpenFGA owner grant for its authenticated creator.

**Architecture:** Keycloak remains the source of the normalized global `platform-admin` and `stack-creator` roles in `authn.Principal`. `app.Service.CreateStack` applies that global decision, persists a stack, then uses the provider-neutral `authz.Authorizer` port to write a confirmed `owner` relationship. The HTTP layer maps forbidden and authorization dependency errors, and API startup injects the configured OpenFGA adapter.

**Tech Stack:** Go, `net/http`, existing `internal/authn` middleware, `internal/authz` port, OpenFGA HTTP adapter, Go standard-library tests.

## Global Constraints

- Preserve Keycloak as the global-role source for AUTH-011; do not add tenant/platform objects or global relations to the OpenFGA model.
- Stack ownership must use canonical `user:<Keycloak sub>` and `stack:<stack ID>` identifiers, the direct `owner` role, and `Mutation{confirm: true}` through its constructor.
- Reject callers lacking both global roles before generating a stack ID, persisting state, or calling OpenFGA.
- If ownership delivery fails after persistence, return a stable authorization `503`; do not implement rollback, durable retry, or reconciliation in this issue.
- Do not introduce endpoint authorization beyond stack creation; AUTH-013 owns the permission matrix.
- Run all shell commands through `rtk`.

---

### Task 1: Enforce Creation Role And Assign Owner In The Application Service

**Files:**
- Modify: `internal/app/service.go:15-18,19-39,135-151,336-367`
- Modify: `internal/app/service_test.go:11-21,81-146,1469-1480`

**Interfaces:**
- Consumes: `authn.PrincipalFromContext(context.Context) (authn.Principal, bool)` and `authz.Authorizer.WriteRelationships(context.Context, authz.Mutation) error`.
- Produces: `ErrForbidden error`, `Service.Authorizer authz.Authorizer`, and `CreateStack(context.Context, CreateStackCommand) (traits.Stack, error)` that writes a confirmed `owner` grant after persistence.

- [ ] **Step 1: Add failing service tests for both permitted roles and the denied role**

Add a helper that supplies explicit roles and a recording authorizer implementing every `authz.Authorizer` method. Add table-driven tests whose successful cases assert the captured mutation is confirmed and contains `user:<subject>`, `stack:stack_123`, and `owner`; assert the denied case has no generated ID, repository write, or authorizer call.

```go
func authenticatedContextWithRoles(roles ...string) context.Context {
    return authn.ContextWithPrincipal(context.Background(), authn.Principal{
        Subject: keycloakSubject, RealmRoles: roles,
    })
}

func TestCreateStackRequiresGlobalCreatorRole(t *testing.T) {
    stacks := &recordingStackRepository{}
    authorizer := &recordingAuthorizer{}
    service := NewService(Service{Stacks: stacks, Authorizer: authorizer})

    _, err := service.CreateStack(authenticatedContextWithRoles(), CreateStackCommand{
        TenantID: "tenant_123", Name: "Acme Prod",
    })

    if !errors.Is(err, ErrForbidden) { t.Fatalf("error = %v, want ErrForbidden", err) }
    if stacks.createCalls != 0 || authorizer.writeCalls != 0 { t.Fatal("unauthorized creation had side effects") }
}
```

- [ ] **Step 2: Run the new service tests to verify they fail**

Run: `rtk go test ./internal/app -run 'TestCreateStack(RequiresGlobalCreatorRole|.*Owner)' -count=1`

Expected: FAIL because `ErrForbidden`, `Authorizer`, and the owner mutation behavior do not exist.

- [ ] **Step 3: Add the minimal global-role and ownership behavior**

Import `slices` and `internal/authz`, define `ErrForbidden`, add `Authorizer authz.Authorizer` to `Service`, and use the principal itself rather than only `authenticatedActor` so the role decision occurs before ID allocation. Keep `authenticatedActor` for other use cases. Add an `ErrAuthorizationNotConfigured` guard before calling the port so incomplete test or process wiring cannot panic. Update every existing `CreateStack` test fixture to include an allow-recording authorizer and make `authenticatedContext()` include `stack-creator` by default; use `authenticatedContextWithRoles()` for role-specific cases.

```go
var ErrForbidden = errors.New("forbidden")
var ErrAuthorizationNotConfigured = errors.New("authorization not configured")

func canCreateStack(principal authn.Principal) bool {
    return slices.Contains(principal.RealmRoles, "platform-admin") ||
        slices.Contains(principal.RealmRoles, "stack-creator")
}

if service.Authorizer == nil {
    return traits.Stack{}, ErrAuthorizationNotConfigured
}

// Immediately after service.Stacks.CreateStack succeeds:
subject, err := authz.SubjectFromKeycloakSub(principal.Subject)
if err != nil { return traits.Stack{}, fmt.Errorf("create owner subject: %w", err) }
authorizedStack, err := authz.StackFromID(string(stack.ID))
if err != nil { return traits.Stack{}, fmt.Errorf("create owner stack: %w", err) }
grant, err := authz.NewGrant(subject, authorizedStack, authz.RoleOwner)
if err != nil { return traits.Stack{}, fmt.Errorf("create owner grant: %w", err) }
mutation, err := authz.NewMutation([]authz.Grant{grant}, true)
if err != nil { return traits.Stack{}, fmt.Errorf("create owner mutation: %w", err) }
if err := service.Authorizer.WriteRelationships(ctx, mutation); err != nil {
    return traits.Stack{}, fmt.Errorf("assign stack owner: %w", err)
}
```

Ensure `NewService` validates the required `Stacks` and `Authorizer` dependencies only when `CreateStack` is called, or return a clear error rather than panicking; retain the test-friendly construction used by unrelated use cases.

- [ ] **Step 4: Add failure-path service tests**

Cover a repository error and an authorizer error. The former must make zero authorizer calls. The latter must preserve the captured repository stack and return the wrapped authorization error; it must not attempt deletion or a second write.

```go
authorizer := &recordingAuthorizer{writeErr: authz.ErrUnavailable}
_, err := service.CreateStack(authenticatedContextWithRoles("stack-creator"), command)
if !errors.Is(err, authz.ErrUnavailable) { t.Fatalf("error = %v, want ErrUnavailable", err) }
if stacks.createCalls != 1 { t.Fatalf("create calls = %d, want 1", stacks.createCalls) }
if authorizer.writeCalls != 1 { t.Fatalf("write calls = %d, want 1", authorizer.writeCalls) }
```

- [ ] **Step 5: Run focused service tests**

Run: `rtk go test ./internal/app -count=1`

Expected: PASS.

- [ ] **Step 6: Commit the application service change**

```bash
git add internal/app/service.go internal/app/service_test.go
git commit -m "feat(authz): authorize stack creation ownership"
```

### Task 2: Map Authorization Outcomes Through The HTTP API

**Files:**
- Modify: `internal/api/server.go:11-14,580-598`
- Modify: `internal/api/server_test.go:13-27,388-428`

**Interfaces:**
- Consumes: `app.ErrForbidden` and `authz.HTTPStatus(error) (int, string, bool)`.
- Produces: `writeAppError` mappings of `ErrForbidden` to `403 forbidden`, authorization dependency errors to their stable `503` codes, and API tests that make creation requests with normalized role claims.

- [ ] **Step 1: Update the successful create-stack API test to include a creator role and install a recording authorizer**

Make the request context carry `RealmRoles: []string{"stack-creator"}` and configure the API test service with the recording authorizer from the application test equivalent. Assert the created response stays `201` and that an owner mutation was issued with confirmation.

```go
ctx := authn.ContextWithPrincipal(request.Context(), authn.Principal{
    Subject: apiKeycloakSubject, RealmRoles: []string{"stack-creator"},
})
request = request.WithContext(ctx)
```

- [ ] **Step 2: Add failing HTTP error-mapping tests**

Exercise `POST /v1/tenants/tenant_123/stacks` with an authenticated principal lacking global roles and with a `stack-creator` principal whose authorizer returns `authz.ErrUnavailable`.

```go
if response.Code != http.StatusForbidden { t.Fatalf("status = %d, want 403", response.Code) }
if body.Error != "forbidden" { t.Fatalf("error = %q, want forbidden", body.Error) }

if response.Code != http.StatusServiceUnavailable { t.Fatalf("status = %d, want 503", response.Code) }
if body.Error != "authorization_unavailable" { t.Fatalf("error = %q", body.Error) }
```

- [ ] **Step 3: Run the API tests to verify the mapping tests fail**

Run: `rtk go test ./internal/api -run 'TestCreateStack|TestWriteAppError' -count=1`

Expected: FAIL because `writeAppError` does not yet map forbidden or authorization-port errors.

- [ ] **Step 4: Implement stable error mappings**

Import `internal/authz`, map `app.ErrForbidden` before conflict handling, and delegate recognized authorization errors to the port’s existing mapper.

```go
case errors.Is(err, app.ErrForbidden):
    writeError(response, http.StatusForbidden, "forbidden", "forbidden")
default:
    if status, code, ok := authz.HTTPStatus(err); ok {
        writeError(response, status, code, "authorization service unavailable")
        return
    }
    writeError(response, http.StatusInternalServerError, "internal_error", "internal server error")
```

- [ ] **Step 5: Run the focused API package tests**

Run: `rtk go test ./internal/api -count=1`

Expected: PASS.

- [ ] **Step 6: Commit the HTTP error behavior**

```bash
git add internal/api/server.go internal/api/server_test.go
git commit -m "feat(api): report stack creation authorization errors"
```

### Task 3: Wire The OpenFGA Adapter At API Startup

**Files:**
- Modify: `cmd/api/main.go:14-23,46-56,109-180`
- Modify: `cmd/api/main_test.go:17-23,99-173,400-470`

**Interfaces:**
- Consumes: `config.OpenFGAConfig` and `openfga.NewAuthorizationAdapter(openfga.Config) (*openfga.AuthorizationAdapter, error)`.
- Produces: an `apiDependencies.newAuthorizer func(openfga.Config) (authz.Authorizer, error)` dependency and `app.Service.Authorizer` configured with explicit API URL, store ID, model ID, API token, and timeout.

- [ ] **Step 1: Add a failing startup wiring test and recording constructor**

Add `newAuthorizer` to `apiDependencies`; in `newRecordingAPIDependencies`, capture the supplied `openfga.Config` and return a recording authorizer. Assert `TestRunWiresTemporalDispatcher` also sees that authorizer in `deps.service.Authorizer` and that its fields equal the `OPENFGA_*` test environment values.

```go
newAuthorizer: func(cfg openfga.Config) (authz.Authorizer, error) {
    deps.openFGAConfig = cfg
    return deps.authorizer, nil
},
```

- [ ] **Step 2: Run the startup wiring test to verify it fails**

Run: `rtk go test ./cmd/api -run TestRunWiresTemporalDispatcher -count=1`

Expected: FAIL because no OpenFGA authorizer constructor is injected into the service.

- [ ] **Step 3: Construct and inject the adapter using validated runtime config**

Use the already validated `cfg.Security.OpenFGA` values; do not re-read environment variables or log the API token.

```go
authorizer, err := deps.newAuthorizer(openfga.Config{
    APIURL: cfg.Security.OpenFGA.APIURL, StoreID: cfg.Security.OpenFGA.StoreID,
    ModelID: cfg.Security.OpenFGA.ModelID, APIToken: cfg.Security.OpenFGA.APIToken.Value(),
    HTTPTimeout: cfg.Security.OpenFGA.RequestTimeout,
})
if err != nil { return fmt.Errorf("create authorization adapter: %w", err) }

service, err := deps.newService(app.Service{
    Stacks: store, Authorizer: authorizer,
    // retain every existing repository, dispatcher, and reader assignment
})
```

Set `defaultAPIDependencies().newAuthorizer` to `openfga.NewAuthorizationAdapter`.

- [ ] **Step 4: Run command-package tests**

Run: `rtk go test ./cmd/api -count=1`

Expected: PASS.

- [ ] **Step 5: Commit startup wiring**

```bash
git add cmd/api/main.go cmd/api/main_test.go
git commit -m "feat(api): wire OpenFGA stack ownership authorizer"
```

### Task 4: Complete Documentation And Full Verification

**Files:**
- Modify: `docs/sprint/authn_and_authz/README.md:74`
- Modify: `docs/authentication.md`
- Modify: `backlog.txt`
- Create: `docs/superpowers/specs/2026-07-18-stack-creation-authorization-design.md`

**Interfaces:**
- Consumes: the completed runtime behavior from Tasks 1-3.
- Produces: updated AUTH-011 status, operational documentation for globally authorized creation and confirmed ownership, and a deferred follow-up documenting OpenFGA-centralized global authorization.

- [ ] **Step 1: Document the finalized behavior**

Add concise authentication documentation stating that `stack-creator` or `platform-admin` is required to create a stack, creation writes a confirmed owner relationship for the authenticated subject, and an ownership-confirmation outage returns `503` after product persistence pending AUTH-012 recovery. Add this backlog entry if not already present:

```text
- evaluate moving global authorization from Keycloak realm roles to OpenFGA through a tenant or platform object; update the model, provisioning, and creation permission checks
```

- [ ] **Step 2: Run the full test suite before marking the backlog item complete**

Run: `rtk go test ./...`

Expected: PASS for all packages.

- [ ] **Step 3: Mark AUTH-011 Done only after the full suite passes**

Change the AUTH-011 status in `docs/sprint/authn_and_authz/README.md` from `Not Started` to `Done`.

- [ ] **Step 4: Verify documentation and diff quality**

Run: `rtk git diff --check`

Expected: no output and exit status 0.

- [ ] **Step 5: Commit documentation and backlog updates**

```bash
git add docs/authentication.md docs/sprint/authn_and_authz/README.md docs/superpowers/specs/2026-07-18-stack-creation-authorization-design.md backlog.txt
git commit -m "docs(authz): complete stack creation ownership"
```

## Plan Self-Review

- Spec coverage: Task 1 covers global-role enforcement, canonical owner grants, higher-consistency confirmation, and persistence-first failure behavior. Task 2 covers stable `403` and `503` HTTP outcomes. Task 3 covers production adapter wiring. Task 4 covers documentation, the AUTH-011 status, the deferred centralized-OpenFGA decision, and full verification.
- Placeholder scan: no incomplete requirements or deferred implementation placeholders are present; AUTH-012 and AUTH-013 exclusions are explicit scope boundaries.
- Type consistency: all planned ownership writes use `authz.NewGrant`, `authz.NewMutation`, and `authz.Authorizer.WriteRelationships`; startup constructs the exact `openfga.Config` accepted by `openfga.NewAuthorizationAdapter`.
