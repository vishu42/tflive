# Run History & Cross-User Approval Visibility Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make a stack template's run history visible to every user who can view the stack (not just the browser tab that started it), so a pending `waiting_approval` apply run — and its Approve button — becomes visible to any user holding the approve grant, and the Runs screen stops losing history on reload.

**Architecture:** Add a `ListTemplateRuns` read path mirrored end-to-end on the existing `ListTemplateRunLogs`/`GetTemplateRun` pattern (Postgres repository → `Service` method gated by the same `authz.PermissionView` check already used for reading a single run → `GET` route on the existing `/stack-templates/{id}/runs` path → frontend client/query hook). `RunsListRow.tsx` then derives its "current plan"/"current apply"/"active run" state from this server-fetched history instead of from local `useState`, which is the root cause of the bug: local state is invisible to every session except the one that set it.

**Tech Stack:** Go 1.22 `net/http` mux with method-prefixed patterns, pgx/Postgres, React + TanStack Query, Vitest/Testing Library.

## Global Constraints

- Reuse `authz.PermissionView` for the new list endpoint — the same permission `GetTemplateRun` already requires. This is a visibility fix, not a new authorization concept.
- No new dependencies (Go modules or npm packages).
- No database migration — `template_runs_tenant_id_stack_template_id_idx` already exists ([migrations/0001_app_repositories.sql](internal/postgres/migrations/0001_app_repositories.sql)).
- Follow existing file conventions exactly: repository methods return `app.ErrNotFound` on `pgx.ErrNoRows`, wrap other errors with `fmt.Errorf("...: %w", err)`; service commands validate before authorizing; API handlers use `writeAppError`/`writeJSON`.

---

### Task 1: Postgres repository — `ListTemplateRuns`

**Files:**
- Modify: `internal/postgres/repositories.go` (add method after `GetTemplateRun`, which ends at line 1027)
- Test: `internal/postgres/store_test.go` (add after `TestRecordTemplateRunLogReturnsNotFoundForOtherTenant`, which ends at line 1678)

**Interfaces:**
- Produces: `func (store *Store) ListTemplateRuns(ctx context.Context, tenantID traits.TenantID, stackTemplateID traits.StackTemplateID) ([]traits.TemplateRun, error)`
- Consumes: `traits.TemplateRun` (existing struct, [traits.go:303](internal/traits/traits.go#L303)), `openMigratedTestPool`/`seedTemplateRun` test helpers (existing, `internal/postgres/store_test.go:2064` and `:2075`)

- [ ] **Step 1: Write the failing test**

Add to `internal/postgres/store_test.go`:

```go
func TestListTemplateRunsReturnsMostRecentFirstScopedToStackTemplate(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)

	seedTemplateRun(t, ctx, pool, traits.TemplateRun{
		ID:              traits.TemplateRunID("run_older"),
		TenantID:        traits.TenantID("tenant_123"),
		StackTemplateID: traits.StackTemplateID("stack_template_123"),
		Operation:       traits.OperationPlan,
		SelectedRef:     "main",
		WorkspaceName:   "mtp_acme_prod_vpc_a13f9c",
		Status:          traits.TemplateRunCompleted,
		TriggerActor:    traits.UserID("user_123"),
		StartedAt:       time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC),
	})
	seedTemplateRun(t, ctx, pool, traits.TemplateRun{
		ID:              traits.TemplateRunID("run_newer"),
		TenantID:        traits.TenantID("tenant_123"),
		StackTemplateID: traits.StackTemplateID("stack_template_123"),
		Operation:       traits.OperationApply,
		SelectedRef:     "main",
		WorkspaceName:   "mtp_acme_prod_vpc_a13f9c",
		Status:          traits.TemplateRunWaitingApproval,
		TriggerActor:    traits.UserID("user_456"),
		StartedAt:       time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC),
	})
	seedTemplateRun(t, ctx, pool, traits.TemplateRun{
		ID:              traits.TemplateRunID("run_other_stack_template"),
		TenantID:        traits.TenantID("tenant_123"),
		StackTemplateID: traits.StackTemplateID("stack_template_456"),
		Operation:       traits.OperationPlan,
		SelectedRef:     "main",
		WorkspaceName:   "mtp_acme_prod_vpc_other",
		Status:          traits.TemplateRunCompleted,
		TriggerActor:    traits.UserID("user_123"),
		StartedAt:       time.Date(2026, 7, 20, 11, 0, 0, 0, time.UTC),
	})

	runs, err := store.ListTemplateRuns(ctx, traits.TenantID("tenant_123"), traits.StackTemplateID("stack_template_123"))
	if err != nil {
		t.Fatalf("ListTemplateRuns returned error: %v", err)
	}

	if len(runs) != 2 {
		t.Fatalf("len(runs) = %d, want 2", len(runs))
	}
	if runs[0].ID != traits.TemplateRunID("run_newer") || runs[1].ID != traits.TemplateRunID("run_older") {
		t.Fatalf("run order = %q, %q; want run_newer, run_older", runs[0].ID, runs[1].ID)
	}
	if runs[0].Status != traits.TemplateRunWaitingApproval {
		t.Fatalf("newest run status = %q, want waiting_approval", runs[0].Status)
	}
	if runs[0].TriggerActor != traits.UserID("user_456") {
		t.Fatalf("newest run trigger actor = %q, want user_456", runs[0].TriggerActor)
	}
}

func TestListTemplateRunsReturnsEmptySliceWhenNoneExist(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)

	runs, err := store.ListTemplateRuns(ctx, traits.TenantID("tenant_123"), traits.StackTemplateID("stack_template_nonexistent"))
	if err != nil {
		t.Fatalf("ListTemplateRuns returned error: %v", err)
	}
	if runs == nil {
		t.Fatal("runs = nil, want empty slice")
	}
	if len(runs) != 0 {
		t.Fatalf("len(runs) = %d, want 0", len(runs))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/postgres/... -run TestListTemplateRuns -v`
Expected: FAIL with `store.ListTemplateRuns undefined (type *Store has no field or method ListTemplateRuns)`

- [ ] **Step 3: Write minimal implementation**

Add to `internal/postgres/repositories.go` immediately after the closing brace of `GetTemplateRun` (line 1027):

```go
func (store *Store) ListTemplateRuns(ctx context.Context, tenantID traits.TenantID, stackTemplateID traits.StackTemplateID) ([]traits.TemplateRun, error) {
	rows, err := store.pool.Query(ctx, `
		select
			id,
			tenant_id,
			stack_template_id,
			template_revision_id,
			source_template_id,
			operation,
			selected_ref,
			resolved_commit_sha,
			workspace_name,
			config_json,
			backend_type,
			backend_config_hash,
			status,
			trigger_actor,
			started_at,
			completed_at,
			error_summary
		from template_runs
		where tenant_id = $1
			and stack_template_id = $2
		order by started_at desc nulls last, id desc
	`, tenantID, stackTemplateID)
	if err != nil {
		return nil, fmt.Errorf("list template runs: %w", err)
	}
	defer rows.Close()

	var runs []traits.TemplateRun
	for rows.Next() {
		var run traits.TemplateRun
		var startedAt sql.NullTime
		var completedAt sql.NullTime
		if err := rows.Scan(
			&run.ID,
			&run.TenantID,
			&run.StackTemplateID,
			&run.TemplateRevisionID,
			&run.SourceTemplateID,
			&run.Operation,
			&run.SelectedRef,
			&run.ResolvedCommitSHA,
			&run.WorkspaceName,
			&run.ConfigJSON,
			&run.BackendType,
			&run.BackendConfigHash,
			&run.Status,
			&run.TriggerActor,
			&startedAt,
			&completedAt,
			&run.ErrorSummary,
		); err != nil {
			return nil, fmt.Errorf("scan template run: %w", err)
		}
		if startedAt.Valid {
			run.StartedAt = startedAt.Time
		}
		if completedAt.Valid {
			run.CompletedAt = completedAt.Time
		}
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list template runs: %w", err)
	}

	return runs, nil
}
```

`internal/postgres/repositories.go` already imports `database/sql` (used by `GetTemplateRun`'s `sql.NullTime`) and `fmt`, so no new imports are needed.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/postgres/... -run TestListTemplateRuns -v`
Expected: PASS (requires a reachable Postgres test instance, same as every other test in this file — no new setup needed beyond what the suite already requires)

- [ ] **Step 5: Commit**

```bash
git add internal/postgres/repositories.go internal/postgres/store_test.go
git commit -m "feat: add ListTemplateRuns repository method"
```

---

### Task 2: Service layer — `ListTemplateRuns` command

**Files:**
- Modify: `internal/app/service.go`
  - Add `ListTemplateRuns` to the `TemplateRunRepository` interface (line 88, alongside `GetTemplateRun`)
  - Add `ListTemplateRunsCommand` struct (after `GetTemplateRunCommand`, line 354)
  - Add `service.ListTemplateRuns` method (after `GetTemplateRun` method, which ends at line 1189)
  - Add `validateListTemplateRunsCommand` (after `validateGetTemplateRunCommand`, which ends at line 1408)
- Modify: `internal/app/service_test.go` — add `ListTemplateRuns` to `recordingTemplateRunRepository` (starts line 1878); add 2 tests
- Modify: `internal/api/server_test.go` — add `ListTemplateRuns` to its own `recordingTemplateRunRepository` (starts line 2458), so the `api` package's fake keeps satisfying the widened interface
- Modify: `cmd/api/main_test.go` — add `ListTemplateRuns` to `recordingStore` (alongside `GetTemplateRun`, line 612), so `cmd/api`'s fake keeps satisfying the widened interface

**Interfaces:**
- Consumes: `store.ListTemplateRuns` (Task 1), `service.authorizedStackTemplate(ctx, tenantID, stackTemplateID, permission, denied)` (existing, [authorization.go:158](internal/app/authorization.go#L158))
- Produces: `type ListTemplateRunsCommand struct { TenantID traits.TenantID; StackTemplateID traits.StackTemplateID }`, `func (service *Service) ListTemplateRuns(ctx context.Context, command ListTemplateRunsCommand) ([]traits.TemplateRun, error)` — consumed by Task 3's HTTP handler.

- [ ] **Step 1: Write the failing tests**

Add to `internal/app/service_test.go`, after `TestGetTemplateRunReturnsTenantScopedRun` (ends line 1294):

```go
func TestListTemplateRunsReturnsRunsScopedToStackTemplate(t *testing.T) {
	t.Parallel()

	runs := &recordingTemplateRunRepository{
		list: []traits.TemplateRun{
			{ID: traits.TemplateRunID("run_newer"), TenantID: traits.TenantID("tenant_123"), StackTemplateID: traits.StackTemplateID("stack_template_123"), Operation: traits.OperationApply, Status: traits.TemplateRunWaitingApproval},
			{ID: traits.TemplateRunID("run_older"), TenantID: traits.TenantID("tenant_123"), StackTemplateID: traits.StackTemplateID("stack_template_123"), Operation: traits.OperationPlan, Status: traits.TemplateRunCompleted},
		},
	}
	service := NewService(Service{
		Authorizer:     &permissionAuthorizer{allowed: true},
		TemplateRuns:   runs,
		StackTemplates: &recordingStackTemplateRepository{stackTemplate: traits.StackTemplate{ID: "stack_template_123", TenantID: "tenant_123", StackID: "stack_123"}},
	})
	ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: keycloakSubject})

	got, err := service.ListTemplateRuns(ctx, ListTemplateRunsCommand{
		TenantID:        traits.TenantID("tenant_123"),
		StackTemplateID: traits.StackTemplateID("stack_template_123"),
	})
	if err != nil {
		t.Fatalf("ListTemplateRuns returned error: %v", err)
	}

	if runs.gotListTenantID != traits.TenantID("tenant_123") {
		t.Fatalf("tenant lookup = %q, want tenant_123", runs.gotListTenantID)
	}
	if runs.gotListStackTemplateID != traits.StackTemplateID("stack_template_123") {
		t.Fatalf("stack template lookup = %q, want stack_template_123", runs.gotListStackTemplateID)
	}
	if len(got) != 2 || got[0].ID != traits.TemplateRunID("run_newer") {
		t.Fatalf("runs = %#v, want run_newer first", got)
	}
}

func TestListTemplateRunsNormalizesNilAndRequiresStackTemplateID(t *testing.T) {
	t.Parallel()

	service := NewService(Service{
		Authorizer:     &permissionAuthorizer{allowed: true},
		TemplateRuns:   &recordingTemplateRunRepository{},
		StackTemplates: &recordingStackTemplateRepository{stackTemplate: traits.StackTemplate{ID: "stack_template_123", TenantID: "tenant_123", StackID: "stack_123"}},
	})
	ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: keycloakSubject})

	got, err := service.ListTemplateRuns(ctx, ListTemplateRunsCommand{
		TenantID:        traits.TenantID("tenant_123"),
		StackTemplateID: traits.StackTemplateID("stack_template_123"),
	})
	if err != nil {
		t.Fatalf("ListTemplateRuns returned error: %v", err)
	}
	if got == nil {
		t.Fatal("runs = nil, want empty slice")
	}
	if len(got) != 0 {
		t.Fatalf("len(runs) = %d, want 0", len(got))
	}

	_, err = service.ListTemplateRuns(ctx, ListTemplateRunsCommand{TenantID: traits.TenantID("tenant_123")})
	if !errors.Is(err, ErrInvalidCommand) {
		t.Fatalf("error = %v, want ErrInvalidCommand", err)
	}
}
```

Add `ListTemplateRuns` and `gotListTenantID`/`gotListStackTemplateID` to `recordingTemplateRunRepository` in `internal/app/service_test.go` (struct starts line 1878):

```go
type recordingTemplateRunRepository struct {
	created                traits.TemplateRun
	run                    traits.TemplateRun
	list                   []traits.TemplateRun
	approval               traits.TemplateRunApproval
	cancellation           traits.TemplateRunCancellation
	gotGetTenantID         traits.TenantID
	gotGetRunID            traits.TemplateRunID
	gotListTenantID        traits.TenantID
	gotListStackTemplateID traits.StackTemplateID
	getErr                 error
	approvalErr            error
	cancellationErr        error
}
```

```go
func (repository *recordingTemplateRunRepository) ListTemplateRuns(_ context.Context, tenantID traits.TenantID, stackTemplateID traits.StackTemplateID) ([]traits.TemplateRun, error) {
	repository.gotListTenantID = tenantID
	repository.gotListStackTemplateID = stackTemplateID
	return repository.list, nil
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/app/... -run TestListTemplateRuns -v`
Expected: FAIL to compile — `ListTemplateRunsCommand undefined`, `service.ListTemplateRuns undefined`, and `*recordingTemplateRunRepository does not implement TemplateRunRepository` once the interface is widened (do Step 3 below before running, or expect the compile error to name the missing method — either way this is the "fails for the right reason" signal).

- [ ] **Step 3: Write minimal implementation**

In `internal/app/service.go`, widen the interface (line 88 area):

```go
// TemplateRunRepository persists TemplateRun records and run decisions.
type TemplateRunRepository interface {
	CreateTemplateRun(ctx context.Context, run traits.TemplateRun) error
	GetTemplateRun(ctx context.Context, tenantID traits.TenantID, runID traits.TemplateRunID) (traits.TemplateRun, error)
	// ListTemplateRuns returns tenant-owned runs for one stack template, most recent first.
	ListTemplateRuns(ctx context.Context, tenantID traits.TenantID, stackTemplateID traits.StackTemplateID) ([]traits.TemplateRun, error)
	// ApproveTemplateRun records approval only when the tenant-owned run is waiting for approval.
	ApproveTemplateRun(ctx context.Context, approval traits.TemplateRunApproval) error
	// RequestTemplateRunCancellation records cancellation only when the tenant-owned run can still stop.
	RequestTemplateRunCancellation(ctx context.Context, cancellation traits.TemplateRunCancellation) error
}
```

Add the command struct after `GetTemplateRunCommand` (line 354):

```go
type ListTemplateRunsCommand struct {
	TenantID        traits.TenantID
	StackTemplateID traits.StackTemplateID
}
```

Add the service method after `GetTemplateRun` (after line 1189, before `GetTemplateRunLog`):

```go
// ListTemplateRuns returns tenant-owned runs for one stack template, most recent first,
// visible to anyone who can view the owning stack.
func (service *Service) ListTemplateRuns(ctx context.Context, command ListTemplateRunsCommand) ([]traits.TemplateRun, error) {
	if err := validateListTemplateRunsCommand(command); err != nil {
		return nil, err
	}

	if _, err := service.authorizedStackTemplate(ctx, command.TenantID, command.StackTemplateID, authz.PermissionView, ErrNotFound); err != nil {
		return nil, fmt.Errorf("list template runs: %w", err)
	}

	runs, err := service.TemplateRuns.ListTemplateRuns(ctx, command.TenantID, command.StackTemplateID)
	if err != nil {
		return nil, fmt.Errorf("list template runs: %w", err)
	}
	if runs == nil {
		return []traits.TemplateRun{}, nil
	}

	return runs, nil
}
```

Add the validator after `validateGetTemplateRunCommand` (after line 1408):

```go
func validateListTemplateRunsCommand(command ListTemplateRunsCommand) error {
	switch {
	case command.TenantID == "":
		return fmt.Errorf("%w: tenant id is required", ErrInvalidCommand)
	case command.StackTemplateID == "":
		return fmt.Errorf("%w: stack template id is required", ErrInvalidCommand)
	default:
		return nil
	}
}
```

In `internal/api/server_test.go`, add the same two fields and method to its own `recordingTemplateRunRepository` (struct starts line 2458) — identical shape to the one added above in `internal/app/service_test.go`:

```go
	list                   []traits.TemplateRun
	gotListTenantID        traits.TenantID
	gotListStackTemplateID traits.StackTemplateID
```

```go
func (repository *recordingTemplateRunRepository) ListTemplateRuns(_ context.Context, tenantID traits.TenantID, stackTemplateID traits.StackTemplateID) ([]traits.TemplateRun, error) {
	repository.gotListTenantID = tenantID
	repository.gotListStackTemplateID = stackTemplateID
	return repository.list, nil
}
```

In `cmd/api/main_test.go`, add a stub next to `GetTemplateRun` (line 612-614), matching that file's argument-less stub style:

```go
func (recordingStore) ListTemplateRuns(context.Context, traits.TenantID, traits.StackTemplateID) ([]traits.TemplateRun, error) {
	return nil, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go build ./... && go test ./internal/app/... ./internal/api/... ./cmd/api/... -run TestListTemplateRuns -v`
Expected: PASS, and the build succeeds across all three packages (confirms all three fakes still satisfy `TemplateRunRepository`)

- [ ] **Step 5: Commit**

```bash
git add internal/app/service.go internal/app/service_test.go internal/api/server_test.go cmd/api/main_test.go
git commit -m "feat: add ListTemplateRuns service command"
```

---

### Task 3: API route — `GET .../stack-templates/{stack_template_id}/runs`

**Files:**
- Modify: `internal/api/server.go`
  - Register route (after line 70, the existing `POST .../runs` registration)
  - Add `handleListTemplateRuns` (after `handleGetTemplateRun`, which ends at line 463)
- Modify: `internal/api/server_test.go`
  - Add `TestListTemplateRunsReturnsRunsForStackTemplate` (standalone test, near `TestListTemplateRevisionsReturnsTenantTemplateRevisions`, line 867)
  - Extend `TestStackRoleRoutesUseInheritedPermissions` (line 1591) with a viewer row
  - Extend `TestStackRoleRoutesDenyInsufficientRoles` (line 1647) with an unassigned row
  - Extend `TestInheritedRouteMissingAndDeniedStatusesMatch` (line 1773) with a "stack-template" resource row
  - Extend `newPermissionMatrixDependencies` (line 1825) to seed `deps.templateRuns.list`

**Interfaces:**
- Consumes: `service.ListTemplateRuns` (Task 2), `server.handleTenantRoute` (existing), `writeAppError`/`writeJSON` (existing, used throughout `server.go`)
- Produces: `GET /v1/tenants/{tenant_id}/stack-templates/{stack_template_id}/runs` → `[]traits.TemplateRun` JSON array — consumed by Task 4's frontend client.

- [ ] **Step 1: Write the failing test**

Add to `internal/api/server_test.go`, after `TestListTemplateRevisionsReturnsTenantTemplateRevisions` (ends line 910):

```go
func TestListTemplateRunsReturnsRunsForStackTemplate(t *testing.T) {
	t.Parallel()

	startedAt := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	deps := newAPITestDependencies()
	deps.templateRuns.list = []traits.TemplateRun{
		{
			ID:              traits.TemplateRunID("run_apply_1"),
			TenantID:        traits.TenantID("tenant_123"),
			StackTemplateID: traits.StackTemplateID("stack_template_123"),
			Operation:       traits.OperationApply,
			Status:          traits.TemplateRunWaitingApproval,
			TriggerActor:    traits.UserID("user_456"),
			StartedAt:       startedAt,
		},
	}
	deps.stackTemplates.stackTemplate = traits.StackTemplate{
		ID:       traits.StackTemplateID("stack_template_123"),
		TenantID: traits.TenantID("tenant_123"),
		StackID:  traits.StackID("stack_123"),
	}
	server := NewServer(deps.service(), configuredTenantID)
	response := httptest.NewRecorder()
	request := authenticatedRequest(http.MethodGet, "/v1/tenants/tenant_123/stack-templates/stack_template_123/runs", nil)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusOK, response.Body.String())
	}
	if deps.templateRuns.gotListStackTemplateID != traits.StackTemplateID("stack_template_123") {
		t.Fatalf("stack template lookup = %q, want stack_template_123", deps.templateRuns.gotListStackTemplateID)
	}

	var body []traits.TemplateRun
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body) != 1 || body[0].ID != traits.TemplateRunID("run_apply_1") || body[0].Status != traits.TemplateRunWaitingApproval {
		t.Fatalf("runs = %#v", body)
	}
}
```

Extend the three permission-matrix tests. In `TestStackRoleRoutesUseInheritedPermissions`'s table (line 1602), add:

```go
		{name: "viewer lists run history", role: authz.RoleViewer, method: http.MethodGet, path: "/v1/tenants/tenant_123/stack-templates/stack_template_123/runs", status: http.StatusOK, permission: authz.PermissionView},
```

In `TestStackRoleRoutesDenyInsufficientRoles`'s table (line 1658), add:

```go
		{name: "unassigned cannot list run history", method: http.MethodGet, path: "/v1/tenants/tenant_123/stack-templates/stack_template_123/runs", status: http.StatusNotFound, permission: authz.PermissionView},
```

In `TestInheritedRouteMissingAndDeniedStatusesMatch`'s table (line 1784), add:

```go
		{name: "run history", method: http.MethodGet, path: "/v1/tenants/tenant_123/stack-templates/stack_template_123/runs", status: http.StatusForbidden, resource: "stack-template"},
```

(This row's `resource: "stack-template"` reuses the existing branch at line 1801-1805 that sets `deps.stackTemplates.getErr = app.ErrNotFound` for the "missing" condition — no new branching needed.)

In `newPermissionMatrixDependencies` (line 1825), add one line so the new route has data to return on the allowed path:

```go
	deps.templateRuns.list = []traits.TemplateRun{deps.templateRuns.run}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/api/... -run TestListTemplateRuns -v`
Expected: FAIL with 404 (no route registered) since `handleListTemplateRuns` and its registration don't exist yet

- [ ] **Step 3: Write minimal implementation**

In `internal/api/server.go`, add the route registration immediately after line 70 (the existing `POST .../runs` line):

```go
	// Lists runs for an installed stack template, most recent first.
	server.handleTenantRoute("GET /v1/tenants/{tenant_id}/stack-templates/{stack_template_id}/runs", server.handleListTemplateRuns)
```

Add the handler after `handleGetTemplateRun` (after line 463, before `handleGetTemplateRunLog` at line 465):

```go
func (server *Server) handleListTemplateRuns(response http.ResponseWriter, request *http.Request) {
	runs, err := server.service.ListTemplateRuns(request.Context(), app.ListTemplateRunsCommand{
		TenantID:        traits.TenantID(request.PathValue("tenant_id")),
		StackTemplateID: traits.StackTemplateID(request.PathValue("stack_template_id")),
	})
	if err != nil {
		writeAppError(response, err)
		return
	}

	writeJSON(response, http.StatusOK, runs)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/api/... -v -run 'TestListTemplateRuns|TestStackRoleRoutesUseInheritedPermissions|TestStackRoleRoutesDenyInsufficientRoles|TestInheritedRouteMissingAndDeniedStatusesMatch'`
Expected: PASS for all

- [ ] **Step 5: Run the full Go test suite**

Run: `go build ./... && go test ./...`
Expected: PASS (confirms nothing else broke across `cmd/...` and `internal/...`)

- [ ] **Step 6: Commit**

```bash
git add internal/api/server.go internal/api/server_test.go
git commit -m "feat: expose GET .../stack-templates/{id}/runs endpoint"
```

---

### Task 4: Frontend API client, query key, and query hook

**Files:**
- Modify: `web/src/api/client.ts` — add `listTemplateRuns`
- Modify: `web/src/api/client.test.ts` — add coverage
- Modify: `web/src/api/queryKeys.ts` — add `templateRuns` key
- Modify: `web/src/api/queryKeys.test.ts` — add coverage
- Modify: `web/src/api/queries.ts` — add `useTemplateRunsQuery`
- Modify: `web/src/api/queries.test.tsx` — add coverage

**Interfaces:**
- Consumes: `TemplateRun` type (existing, `web/src/api/types.ts:122`), `requestJSON` (existing, `web/src/api/client.ts`)
- Produces: `client.listTemplateRuns(tenantID: string, stackTemplateID: string): Promise<TemplateRun[]>`, `queryKeys.templateRuns(tenantID: string, stackTemplateID: string)`, `useTemplateRunsQuery(tenantID: string, stackTemplateID: string)` — consumed by Task 5's `RunsListRow`.

- [ ] **Step 1: Write the failing tests**

Add to `web/src/api/client.test.ts`, import `listTemplateRuns` alongside the other named imports (line 2-15), then add a test near the other list tests (after "lists tenant stacks and template revisions", line 91):

```ts
  it("lists runs for a stack template", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(jsonResponse([{ id: "run_1", status: "waiting_approval" }]));

    const runs = await listTemplateRuns("tenant_123", "stpl_1");

    expect(runs).toEqual([{ id: "run_1", status: "waiting_approval" }]);
    expect(fetchMock).toHaveBeenCalledWith(
      "/v1/tenants/tenant_123/stack-templates/stpl_1/runs",
      expect.objectContaining({ method: "GET" })
    );
  });
```

Add to `web/src/api/queryKeys.test.ts`, inside the "builds resource detail keys" test (or as its own assertion in that block):

```ts
    expect(queryKeys.templateRuns("tenant_123", "stpl_1")).toEqual(["templateRuns", "tenant_123", "stpl_1"]);
```

Add to `web/src/api/queries.test.tsx`, import `useTemplateRunsQuery` alongside the other named imports (line 6-25), then add a test in the "read-only query hooks" describe block (after "fetches template revision variables only when a revision id is given", line 89):

```ts
  it("fetches runs for a stack template", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(jsonResponse([{ id: "run_1", status: "waiting_approval" }]));
    const { result } = renderHook(() => useTemplateRunsQuery("tenant_123", "stpl_1"), { wrapper: wrapper(testQueryClient()) });

    await waitFor(() => expect(result.current.data).toEqual([{ id: "run_1", status: "waiting_approval" }]));
    expect(fetchMock).toHaveBeenCalledWith(
      "/v1/tenants/tenant_123/stack-templates/stpl_1/runs",
      expect.objectContaining({ method: "GET" })
    );
  });

  it("does not fetch runs when the stack template id is empty", () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(jsonResponse([]));
    const { result } = renderHook(() => useTemplateRunsQuery("tenant_123", ""), { wrapper: wrapper(testQueryClient()) });

    expect(result.current.fetchStatus).toBe("idle");
    expect(fetchMock).not.toHaveBeenCalled();
  });
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd web && npx vitest run src/api/client.test.ts src/api/queryKeys.test.ts src/api/queries.test.tsx`
Expected: FAIL — `listTemplateRuns`/`queryKeys.templateRuns`/`useTemplateRunsQuery` are not exported

- [ ] **Step 3: Write minimal implementation**

In `web/src/api/client.ts`, add after `getTemplateRun` (line 137):

```ts
export function listTemplateRuns(tenantID: string, stackTemplateID: string): Promise<TemplateRun[]> {
  return requestJSON(`/v1/tenants/${encodeURIComponent(tenantID)}/stack-templates/${encodeURIComponent(stackTemplateID)}/runs`);
}
```

In `web/src/api/queryKeys.ts`, add after `templateRun` (line 10):

```ts
  templateRuns: (tenantID: string, stackTemplateID: string) => ["templateRuns", tenantID, stackTemplateID] as const,
```

In `web/src/api/queries.ts`, add after `useTemplateRunQuery` (line 69):

```ts
export function useTemplateRunsQuery(tenantID: string, stackTemplateID: string) {
  return useQuery({
    queryKey: queryKeys.templateRuns(tenantID, stackTemplateID),
    queryFn: () => client.listTemplateRuns(tenantID, stackTemplateID),
    enabled: stackTemplateID !== "",
    refetchInterval: POLL_INTERVAL_MS
  });
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd web && npx vitest run src/api/client.test.ts src/api/queryKeys.test.ts src/api/queries.test.tsx`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add web/src/api/client.ts web/src/api/client.test.ts web/src/api/queryKeys.ts web/src/api/queryKeys.test.ts web/src/api/queries.ts web/src/api/queries.test.tsx
git commit -m "feat: add listTemplateRuns client, query key, and query hook"
```

---

### Task 5: `RunsListRow` — derive state from server history, add history list, fix cross-user visibility

**Files:**
- Modify: `web/src/features/runs/RunsListRow.tsx`
- Modify: `web/src/features/runs/RunsListRow.test.tsx`
- Modify: `web/src/features/runs/RunsListScreen.test.tsx` (seed the new query so its existing tests don't hit a real network call)

**Interfaces:**
- Consumes: `useTemplateRunsQuery` (Task 4), `isTerminalRunStatus` (existing, `web/src/api/polling.ts`), `queryKeys.templateRuns` (Task 4)
- Produces: no new exports — this is a leaf component; `RunDetailScreen.tsx` and `RunsListScreen.tsx` are unaffected callers.

This task changes the component's control-flow derivation, not just its data source. Previously `canPlan = !planRun` meant "a plan has never been started in this browser tab" — a proxy for "nothing is in flight" that only worked because tab-local state vanished on reload. Once state is sourced from persisted history, that proxy would permanently disable Plan after the first run ever completes. The fix: derive from the single most recent run of *any* operation (`latestRun`) to decide whether an action is in flight, while still tracking the most recent run *per operation* (`planRun`, `applyRun`) for display and links — this preserves every existing test's expected outputs for the single-cycle-in-one-session case while correctly supporting repeat cycles and cross-user visibility.

- [ ] **Step 1: Write the failing test**

Replace `web/src/features/runs/RunsListRow.test.tsx` in full:

```tsx
// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AuthContext } from "../../auth/AuthContext";
import type { AuthContextValue } from "../../auth/AuthContext";
import type { StackCapabilities } from "../../auth/types";
import { queryKeys } from "../../api/queryKeys";
import type { StackTemplate, TemplateRun } from "../../api/types";
import RunsListRow from "./RunsListRow";

function stackTemplate(overrides: Partial<StackTemplate> = {}): StackTemplate {
  return {
    id: "stpl_1",
    stack_id: "stack_1",
    component_key: "primary",
    source_template_id: "tpl_1",
    desired_template_revision_id: "rev_1",
    last_applied_template_revision_id: "",
    selected_ref: "main",
    workspace_name: "acme-prod-primary",
    config: {},
    last_applied_run_id: "",
    last_applied_ref: "",
    created_by: "user_123",
    lifecycle: "active",
    ...overrides
  };
}

function run(overrides: Partial<TemplateRun> = {}): TemplateRun {
  return {
    id: "run_1",
    tenant_id: "tenant_123",
    stack_template_id: "stpl_1",
    template_revision_id: "rev_1",
    source_template_id: "tpl_1",
    operation: "plan",
    selected_ref: "main",
    resolved_commit_sha: "abcdef1234567890",
    workspace_name: "acme-prod-primary",
    config_json: {},
    backend_type: "s3",
    backend_config_hash: "hash",
    status: "queued",
    trigger_actor: "user_123",
    started_at: "2026-07-20T00:00:00Z",
    error_summary: "",
    ...overrides
  };
}

const allAllowed: StackCapabilities = { canView: true, canOperate: true, canApprove: true, canManageAccess: true };

function testQueryClient(): QueryClient {
  return new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
}

function authValue(): AuthContextValue {
  return {
    me: { sub: "user_1", tenantID: "tenant_123", displayName: "Test User", globalCapabilities: { isPlatformAdmin: false, canCreateStack: false } },
    status: "authenticated",
    login: () => {},
    logout: () => {}
  };
}

function seedCapabilities(queryClient: QueryClient, capabilities: StackCapabilities) {
  queryClient.setQueryData(queryKeys.stack("tenant_123", "stack_1"), {
    stack: {
      id: "stack_1",
      tenant_id: "tenant_123",
      name: "Payments",
      slug: "payments",
      tags: {},
      default_credential_ids: [],
      created_by: "user_123",
      created_at: "2026-07-19T00:00:00Z",
      effectiveCapabilities: capabilities
    },
    templates: []
  });
}

function seedRuns(queryClient: QueryClient, runs: TemplateRun[]) {
  queryClient.setQueryData(queryKeys.templateRuns("tenant_123", "stpl_1"), runs);
}

function renderRow(queryClient: QueryClient, overrides: Partial<StackTemplate> = {}) {
  return render(
    <QueryClientProvider client={queryClient}>
      <AuthContext.Provider value={authValue()}>
        <MemoryRouter initialEntries={["/stacks/stack_1/runs"]}>
          <RunsListRow stackId="stack_1" stackTemplate={stackTemplate(overrides)} />
        </MemoryRouter>
      </AuthContext.Provider>
    </QueryClientProvider>
  );
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

function isDisabled(element: HTMLElement): boolean {
  return (element as HTMLButtonElement).disabled;
}

describe("RunsListRow", () => {
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it("shows the component label with Plan enabled and Apply/Approve disabled when no run has started", () => {
    const queryClient = testQueryClient();
    seedCapabilities(queryClient, allAllowed);
    seedRuns(queryClient, []);

    renderRow(queryClient);

    expect(screen.getByText("acme-prod-primary @ main (active)")).toBeTruthy();
    expect(isDisabled(screen.getByRole("button", { name: /Plan/ }))).toBe(false);
    expect(isDisabled(screen.getByRole("button", { name: /Apply/ }))).toBe(true);
    expect(isDisabled(screen.getByRole("button", { name: /Approve/ }))).toBe(true);
    expect(isDisabled(screen.getByRole("button", { name: /Cancel/ }))).toBe(true);
  });

  it("disables Plan/Apply/Cancel with a reason when canOperate is denied", () => {
    const queryClient = testQueryClient();
    seedCapabilities(queryClient, { ...allAllowed, canOperate: false });
    seedRuns(queryClient, []);

    renderRow(queryClient);

    expect(isDisabled(screen.getByRole("button", { name: /Plan/ }))).toBe(true);
    expect(isDisabled(screen.getByRole("button", { name: /Apply/ }))).toBe(true);
    expect(isDisabled(screen.getByRole("button", { name: /Cancel/ }))).toBe(true);
    expect(screen.getByTestId("runs-row-actions-disabled-reason")).toBeTruthy();
  });

  it("disables Approve with a reason when canApprove is denied", () => {
    const queryClient = testQueryClient();
    seedCapabilities(queryClient, { ...allAllowed, canApprove: false });
    seedRuns(queryClient, []);

    renderRow(queryClient);

    expect(isDisabled(screen.getByRole("button", { name: /Approve/ }))).toBe(true);
    expect(screen.getByTestId("runs-row-approve-disabled-reason")).toBeTruthy();
  });

  it("shows a run history entry and an enabled Approve button for a run started by a different user", () => {
    const queryClient = testQueryClient();
    seedCapabilities(queryClient, allAllowed);
    seedRuns(queryClient, [
      run({ id: "run_apply_1", operation: "apply", status: "waiting_approval", trigger_actor: "someone_else", started_at: "2026-07-20T01:00:00Z" }),
      run({ id: "run_plan_1", operation: "plan", status: "completed", trigger_actor: "someone_else", started_at: "2026-07-20T00:00:00Z" })
    ]);

    renderRow(queryClient);

    expect(isDisabled(screen.getByRole("button", { name: /Approve/ }))).toBe(false);
    expect(screen.getByTestId("runs-row-stpl_1-history-run_apply_1")).toBeTruthy();
    expect(screen.getByTestId("runs-row-stpl_1-history-run_plan_1")).toBeTruthy();
  });

  it("walks plan → apply → approve using persisted history, and immediately reflects each step without a page reload", async () => {
    const queryClient = testQueryClient();
    seedCapabilities(queryClient, allAllowed);
    let runsState: TemplateRun[] = [];

    const fetchMock = vi.spyOn(globalThis, "fetch").mockImplementation(async (input, init) => {
      const url = String(input);
      const method = init?.method ?? "GET";
      if (url.endsWith("/stack-templates/stpl_1/runs") && method === "GET") {
        return jsonResponse(runsState);
      }
      if (url.endsWith("/stack-templates/stpl_1/runs") && method === "POST") {
        const body = JSON.parse(String(init?.body)) as { operation: string };
        const created =
          body.operation === "apply"
            ? run({ id: "run_apply_1", operation: "apply", status: "waiting_approval", started_at: "2026-07-20T00:05:00Z" })
            : run({ id: "run_plan_1", operation: "plan", status: "completed", started_at: "2026-07-20T00:00:00Z" });
        runsState = [created, ...runsState];
        return jsonResponse(created, 201);
      }
      if (url.endsWith("/template-runs/run_apply_1/approval") && method === "POST") {
        runsState = runsState.map((existing) => (existing.id === "run_apply_1" ? { ...existing, status: "approved" } : existing));
        return new Response(null, { status: 204 });
      }
      throw new Error(`unexpected fetch: ${url} ${method}`);
    });

    renderRow(queryClient);
    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith("/v1/tenants/tenant_123/stack-templates/stpl_1/runs", expect.objectContaining({ method: "GET" }))
    );

    fireEvent.click(screen.getByRole("button", { name: /Plan/ }));
    await waitFor(() => expect(screen.getByTestId("runs-row-stpl_1-plan-link")).toBeTruthy());
    expect(screen.getByTestId("runs-row-stpl_1-plan-link").getAttribute("href")).toBe("/stacks/stack_1/runs/run_plan_1");
    await waitFor(() => expect(isDisabled(screen.getByRole("button", { name: /Apply/ }))).toBe(false));

    fireEvent.click(screen.getByRole("button", { name: /Apply/ }));
    await waitFor(() => expect(screen.getByTestId("runs-row-stpl_1-apply-link")).toBeTruthy());
    expect(screen.getByTestId("runs-row-stpl_1-apply-link").getAttribute("href")).toBe("/stacks/stack_1/runs/run_apply_1");
    await waitFor(() => expect(isDisabled(screen.getByRole("button", { name: /Approve/ }))).toBe(false));
    expect(isDisabled(screen.getByRole("button", { name: /Cancel/ }))).toBe(false);

    fireEvent.click(screen.getByRole("button", { name: /Approve/ }));
    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        expect.stringContaining("/template-runs/run_apply_1/approval"),
        expect.objectContaining({ method: "POST" })
      )
    );
    await waitFor(() => expect(isDisabled(screen.getByRole("button", { name: /Approve/ }))).toBe(true));
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npx vitest run src/features/runs/RunsListRow.test.tsx`
Expected: FAIL — the "different user" and history-entry tests fail because the current component never reads `queryKeys.templateRuns`; `screen.getByTestId("runs-row-stpl_1-history-run_apply_1")` throws "not found"

- [ ] **Step 3: Write minimal implementation**

Replace `web/src/features/runs/RunsListRow.tsx` in full:

```tsx
import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { CircleStop, Loader2, Play, ShieldCheck } from "lucide-react";
import { Link } from "react-router-dom";
import { isTerminalRunStatus } from "../../api/polling";
import { queryKeys } from "../../api/queryKeys";
import { useApproveRunMutation, useCancelRunMutation, useStartTemplateRunMutation, useTemplateRunsQuery } from "../../api/queries";
import type { Operation, StackTemplate, TemplateRun } from "../../api/types";
import RequireCapability from "../../auth/RequireCapability";
import { tenantID } from "../../config";
import StatusRow from "../../shared/StatusRow";
import { stackTemplateLabel } from "../stacks/stackWorkflow";

interface RunsListRowProps {
  stackId: string;
  stackTemplate: StackTemplate;
}

function latestRunFor(runs: TemplateRun[], operation: Operation): TemplateRun | null {
  return runs.find((candidate) => candidate.operation === operation) ?? null;
}

// One row per installed stack template on /stacks/:stackId/runs. Run state is
// derived entirely from the server's run history (useTemplateRunsQuery), not
// from local component state, so it is visible to every user who can view the
// stack — not just the browser tab that started a run.
export default function RunsListRow({ stackId, stackTemplate }: RunsListRowProps) {
  const [errorMessage, setErrorMessage] = useState("");
  const queryClient = useQueryClient();

  const runsQuery = useTemplateRunsQuery(tenantID, stackTemplate.id);
  const runs = runsQuery.data ?? [];
  const planRun = latestRunFor(runs, "plan");
  const applyRun = latestRunFor(runs, "apply");
  const latestRun = runs[0] ?? null;
  const activeRun = latestRun && !isTerminalRunStatus(latestRun.status) ? latestRun : null;

  const startRunMutation = useStartTemplateRunMutation(tenantID);
  const approveRunMutation = useApproveRunMutation(tenantID);
  const cancelRunMutation = useCancelRunMutation(tenantID);

  const canPlan = !activeRun;
  const canApply = Boolean(!activeRun && latestRun?.operation === "plan" && latestRun.status === "completed");
  const canApprove = Boolean(latestRun?.operation === "apply" && latestRun.status === "waiting_approval");
  const canCancel = Boolean(activeRun);

  async function runAction(action: () => Promise<void>) {
    setErrorMessage("");
    try {
      await action();
      await queryClient.invalidateQueries({ queryKey: queryKeys.templateRuns(tenantID, stackTemplate.id) });
    } catch (error) {
      setErrorMessage(error instanceof Error ? error.message : "Request failed");
    }
  }

  async function handlePlan() {
    await runAction(async () => {
      await startRunMutation.mutateAsync({ stackTemplateID: stackTemplate.id, body: { operation: "plan" } });
    });
  }

  async function handleApply() {
    await runAction(async () => {
      await startRunMutation.mutateAsync({ stackTemplateID: stackTemplate.id, body: { operation: "apply" } });
    });
  }

  async function handleApprove() {
    if (!applyRun) {
      return;
    }
    await runAction(async () => {
      await approveRunMutation.mutateAsync(applyRun.id);
    });
  }

  async function handleCancel() {
    if (!activeRun) {
      return;
    }
    await runAction(async () => {
      await cancelRunMutation.mutateAsync({ runID: activeRun.id, body: { reason: "canceled from runs list" } });
    });
  }

  const actionsProps = {
    canPlan,
    onPlan: handlePlan,
    planBusy: startRunMutation.isPending && startRunMutation.variables?.body.operation === "plan",
    canApply,
    onApply: handleApply,
    applyBusy: startRunMutation.isPending && startRunMutation.variables?.body.operation === "apply",
    canCancel,
    onCancel: handleCancel,
    cancelBusy: cancelRunMutation.isPending
  };

  return (
    <div className="panel" data-testid={`runs-row-${stackTemplate.id}`}>
      <h3>{stackTemplateLabel(stackTemplate)}</h3>
      {errorMessage && <p className="error-text">{errorMessage}</p>}
      <RequireCapability
        capability="canOperate"
        stackId={stackId}
        fallback={<RunsRowActions {...actionsProps} disabledReason="Plan, apply, and cancel require operator access" />}
      >
        <RunsRowActions {...actionsProps} />
      </RequireCapability>
      <RequireCapability
        capability="canApprove"
        stackId={stackId}
        fallback={<ApproveButton canApprove={false} onApprove={handleApprove} busy={approveRunMutation.isPending} disabledReason="Approving requires approver access" />}
      >
        <ApproveButton canApprove={canApprove} onApprove={handleApprove} busy={approveRunMutation.isPending} />
      </RequireCapability>
      <StatusRow label="Plan" value={planRun?.status ?? "not started"} />
      {planRun && (
        <Link to={`/stacks/${stackId}/runs/${planRun.id}`} data-testid={`runs-row-${stackTemplate.id}-plan-link`}>
          View plan run
        </Link>
      )}
      <StatusRow label="Apply" value={applyRun?.status ?? "not started"} />
      {applyRun && (
        <Link to={`/stacks/${stackId}/runs/${applyRun.id}`} data-testid={`runs-row-${stackTemplate.id}-apply-link`}>
          View apply run
        </Link>
      )}
      {runs.length > 0 && (
        <div className="run-history" data-testid={`runs-row-${stackTemplate.id}-history`}>
          <h4>History</h4>
          <ul>
            {runs.map((historyRun) => (
              <li key={historyRun.id}>
                <Link to={`/stacks/${stackId}/runs/${historyRun.id}`} data-testid={`runs-row-${stackTemplate.id}-history-${historyRun.id}`}>
                  {historyRun.operation} — {historyRun.status} — {historyRun.trigger_actor} — {historyRun.started_at}
                </Link>
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  );
}

interface RunsRowActionsProps {
  canPlan: boolean;
  onPlan: () => void;
  planBusy: boolean;
  canApply: boolean;
  onApply: () => void;
  applyBusy: boolean;
  canCancel: boolean;
  onCancel: () => void;
  cancelBusy: boolean;
  disabledReason?: string;
}

function RunsRowActions({ canPlan, onPlan, planBusy, canApply, onApply, applyBusy, canCancel, onCancel, cancelBusy, disabledReason }: RunsRowActionsProps) {
  const locked = Boolean(disabledReason);
  return (
    <div className="button-row">
      <button className="primary-button" disabled={locked || !canPlan || planBusy} onClick={onPlan} type="button">
        {planBusy ? <Loader2 size={16} className="spin" /> : <Play size={16} />}
        Plan
      </button>
      <button className="primary-button" disabled={locked || !canApply || applyBusy} onClick={onApply} type="button">
        {applyBusy ? <Loader2 size={16} className="spin" /> : <Play size={16} />}
        Apply
      </button>
      <button className="secondary-button" disabled={locked || !canCancel || cancelBusy} onClick={onCancel} type="button">
        {cancelBusy ? <Loader2 size={16} className="spin" /> : <CircleStop size={16} />}
        Cancel
      </button>
      {disabledReason && (
        <p className="muted" data-testid="runs-row-actions-disabled-reason">
          {disabledReason}
        </p>
      )}
    </div>
  );
}

function ApproveButton({
  canApprove,
  onApprove,
  busy,
  disabledReason
}: {
  canApprove: boolean;
  onApprove: () => void;
  busy: boolean;
  disabledReason?: string;
}) {
  const locked = Boolean(disabledReason);
  return (
    <div>
      <button className="secondary-button" disabled={locked || !canApprove || busy} onClick={onApprove} type="button">
        {busy ? <Loader2 size={16} className="spin" /> : <ShieldCheck size={16} />}
        Approve
      </button>
      {disabledReason && (
        <p className="muted" data-testid="runs-row-approve-disabled-reason">
          {disabledReason}
        </p>
      )}
    </div>
  );
}
```

Update `web/src/features/runs/RunsListScreen.test.tsx` so its existing tests (which render `RunsListRow` without a fetch mock) seed empty run history instead of hitting a real network call. Add the import and a helper, then call it wherever a stack view with templates is seeded:

Add near the top, after the `stackTemplate` helper (line 45):

```ts
import { queryKeys } from "../../api/queryKeys";
```

(This import already exists on line 8 — reuse it; no duplicate import.)

In the test "renders one row per installed stack template" (line 147), after `queryClient.setQueryData(queryKeys.stack(...), ...)`, add:

```ts
    queryClient.setQueryData(queryKeys.templateRuns("tenant_123", "stpl_1"), []);
    queryClient.setQueryData(queryKeys.templateRuns("tenant_123", "stpl_2"), []);
```

In `seedStackView` (line 161-166), add a third parameter and seed the run history for the default `stackTemplate()` id it uses:

```ts
  function seedStackView(queryClient: QueryClient, capabilities: StackCapabilities) {
    queryClient.setQueryData(queryKeys.stack("tenant_123", "stack_1"), {
      stack: stack({ effectiveCapabilities: capabilities }),
      templates: [stackTemplate()]
    });
    queryClient.setQueryData(queryKeys.templateRuns("tenant_123", "stpl_1"), []);
  }
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd web && npx vitest run src/features/runs/RunsListRow.test.tsx src/features/runs/RunsListScreen.test.tsx`
Expected: PASS

- [ ] **Step 5: Run the full frontend test suite**

Run: `cd web && npx vitest run`
Expected: PASS across all files (confirms `RunDetailScreen.tsx` and other consumers are unaffected, since they don't import from `RunsListRow.tsx`)

- [ ] **Step 6: Commit**

```bash
git add web/src/features/runs/RunsListRow.tsx web/src/features/runs/RunsListRow.test.tsx web/src/features/runs/RunsListScreen.test.tsx
git commit -m "fix: derive RunsListRow state from server run history for cross-user visibility"
```

---

### Task 6: Manual verification in the running app

**Files:** none (verification only)

- [ ] **Step 1: Start the stack**

Run: `docker-compose up -d` (from repo root, brings up Postgres/Temporal/MinIO used by local dev — check `docs/architecture.md` if any service fails to start) then run the API (`go run ./cmd/api`) and the web dev server (`cd web && npm run dev`), or use the project's `run` skill if one is configured.

- [ ] **Step 2: Reproduce the original bug is fixed**

As User A (operator), start an apply run on a stack template that requires approval. As User B (owner, in a different browser/session), open `/stacks/:stackId/runs` for the same stack.
Expected: User B sees the apply run in the history list with status `waiting_approval` and an enabled Approve button, without needing to know the run ID or have started it themselves.

- [ ] **Step 3: Reproduce history persistence**

As User A, reload `/stacks/:stackId/runs` after a plan/apply cycle has completed.
Expected: the Plan/Apply status rows and the history list still show the completed runs — no reset to "not started".

- [ ] **Step 4: Report results**

Confirm both checks pass, or note any discrepancy (e.g. polling delay before the run appears — should resolve within the 1.5s poll interval) before marking the plan complete.

---

## Self-Review Notes

- **Spec coverage:** Task 1-3 cover the backend read path (spec's "Backend" section). Task 4 covers the client/query-hook layer. Task 5 covers the `RunsListRow` rewrite and history list (spec's "Frontend" section), including the necessary `canPlan`/`canApply` derivation fix that the spec's code sketch implied but didn't spell out — called out explicitly in Task 5's preamble. Task 6 covers manual UI verification requested during brainstorming (passive visibility, full history).
- **Placeholder scan:** No TBD/TODO; every step has complete, compilable code matching this repo's existing conventions (verified against `GetTemplateRun`, `ListTemplateRunLogs`, `ListTemplateRevisions`, and their tests).
- **Type consistency:** `ListTemplateRunsCommand{TenantID, StackTemplateID}` used identically in Task 2's method and Task 3's handler. `client.listTemplateRuns` / `queryKeys.templateRuns` / `useTemplateRunsQuery` names match across Task 4 and Task 5. `recordingTemplateRunRepository.ListTemplateRuns` added consistently to both `internal/app/service_test.go` and `internal/api/server_test.go` fakes, plus `recordingStore` in `cmd/api/main_test.go` — all three implementers of `TemplateRunRepository` are updated so the interface widening in Task 2 doesn't break compilation anywhere in the module.
