# AUTH-014: Security Audit Trail Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Persist append-only security audit records for authorization changes and sensitive authorization events.

**Architecture:** Single `security_audit_log` table with discrete columns, an `AuditRepository` interface, and direct inserts from `Service` methods with log-and-continue failure handling. No outbox, no async writes.

**Tech Stack:** Go 1.24, PostgreSQL 16, pgx/v5, hand-written SQL, repository pattern.

## Global Constraints

- Go 1.24 with standard library HTTP server
- PostgreSQL 16 via pgx/v5 connection pool, no ORM
- Hand-written SQL with `$1`, `$2` parameterized queries
- Repository pattern: interfaces in `internal/app/service.go`, implementations in `internal/postgres/repositories.go`
- Domain types in `internal/traits/traits.go`
- Test framework: `testing` + `testify` (postgres), standard `testing` (app)
- Existing migration numbering: `0009_security_audit.sql`

---

### Task 1: Add migration and domain types

**Files:**
- Create: `internal/postgres/migrations/0009_security_audit.sql`
- Modify: `internal/traits/traits.go`
- Modify: `internal/postgres/store_test.go`

**Interfaces:**
- Consumes: none
- Produces: `traits.AuditAction`, `traits.AuditOutcome`, `traits.SecurityAuditEvent` types; `security_audit_log` table

- [ ] **Step 1: Create the migration file**

```sql
-- internal/postgres/migrations/0009_security_audit.sql
create table security_audit_log (
    id              bigserial primary key,
    recorded_at     timestamptz not null default now(),
    actor_subject   text not null,
    action          text not null,
    target_user     text not null default '',
    tenant_id       text not null,
    stack_id        text not null default '',
    old_role        text not null default '',
    new_role        text not null default '',
    outcome         text not null check (outcome in ('success', 'failure')),
    correlation_id  text not null default ''
);

create index security_audit_log_actor_idx
    on security_audit_log (actor_subject, recorded_at desc);

create index security_audit_log_tenant_idx
    on security_audit_log (tenant_id, recorded_at desc);

create index security_audit_log_stack_idx
    on security_audit_log (stack_id, recorded_at desc)
    where stack_id != '';
```

- [ ] **Step 2: Add domain types to traits.go**

Append to `internal/traits/traits.go` after the existing type definitions:

```go
// AuditAction identifies a security-relevant authorization event.
type AuditAction string

const (
	AuditActionGrant               AuditAction = "grant"
	AuditActionRevoke              AuditAction = "revoke"
	AuditActionRoleChange          AuditAction = "role_change"
	AuditActionFailedAccessAttempt AuditAction = "failed_access_attempt"
	AuditActionSelfApprovalRejected AuditAction = "self_approval_rejected"
)

// AuditOutcome reports whether the audited operation succeeded or failed.
type AuditOutcome string

const (
	AuditOutcomeSuccess AuditOutcome = "success"
	AuditOutcomeFailure AuditOutcome = "failure"
)

// SecurityAuditEvent is one append-only authorization audit record.
type SecurityAuditEvent struct {
	ID            int64        `json:"id"`
	RecordedAt    time.Time    `json:"recorded_at"`
	ActorSubject  string       `json:"actor_subject"`
	Action        AuditAction  `json:"action"`
	TargetUser    string       `json:"target_user"`
	TenantID      TenantID     `json:"tenant_id"`
	StackID       StackID      `json:"stack_id"`
	OldRole       string       `json:"old_role"`
	NewRole       string       `json:"new_role"`
	Outcome       AuditOutcome `json:"outcome"`
	CorrelationID string       `json:"correlation_id"`
}
```

- [ ] **Step 3: Add migration schema verification test**

Add to `internal/postgres/store_test.go` (in the `TestMigrateAppliesSchema` table list):

```go
// Add "security_audit_log" to the table list in TestMigrateAppliesSchema
```

Find the `[]string{` block starting at line 53 and append `"security_audit_log"` after `"workflow_outbox"`.

- [ ] **Step 4: Add migration content verification test**

Add a new test function to `internal/postgres/store_test.go`:

```go
func TestSecurityAuditLogMigrationDefinesAppendOnlyTable(t *testing.T) {
	t.Parallel()

	migration, err := migrationFiles.ReadFile("migrations/0009_security_audit.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	sql := strings.ToLower(string(migration))

	for _, snippet := range []string{
		"create table security_audit_log",
		"id bigserial primary key",
		"recorded_at timestamptz not null default now()",
		"actor_subject text not null",
		"action text not null",
		"target_user text not null default ''",
		"tenant_id text not null",
		"stack_id text not null default ''",
		"old_role text not null default ''",
		"new_role text not null default ''",
		"outcome text not null check (outcome in ('success', 'failure'))",
		"correlation_id text not null default ''",
		"security_audit_log_actor_idx",
		"security_audit_log_tenant_idx",
		"security_audit_log_stack_idx",
	} {
		if !strings.Contains(sql, snippet) {
			t.Fatalf("migration 0009 is missing snippet %q", snippet)
		}
	}
}
```

- [ ] **Step 5: Run tests**

Run: `go test ./internal/postgres/ -run TestMigrateAppliesSchema -v -count=1`
Expected: PASS

Run: `go test ./internal/postgres/ -run TestSecurityAuditLogMigration -v -count=1`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/postgres/migrations/0009_security_audit.sql internal/traits/traits.go internal/postgres/store_test.go
git commit -m "feat: add security_audit_log migration and domain types"
```

---

### Task 2: Add AuditRepository interface, Store implementation, and wiring

**Files:**
- Modify: `internal/app/service.go`
- Modify: `internal/postgres/repositories.go`
- Modify: `internal/postgres/store_test.go`
- Modify: `cmd/api/main.go`

**Interfaces:**
- Consumes: `traits.SecurityAuditEvent` from Task 1
- Produces: `app.AuditRepository` interface, `Store.AppendAuditEvent` method, `Service.Audit` field

- [ ] **Step 1: Add AuditRepository interface to service.go**

Add to `internal/app/service.go` after the `WorkflowDispatcher` interface:

```go
// AuditRepository appends security audit events. It exposes no update or
// delete methods, enforcing append-only semantics through the interface.
type AuditRepository interface {
	AppendAuditEvent(ctx context.Context, event traits.SecurityAuditEvent) error
}
```

- [ ] **Step 2: Add Audit field to Service struct**

Add to the `Service` struct in `internal/app/service.go`:

```go
type Service struct {
	// ... existing fields ...
	Audit AuditRepository
}
```

- [ ] **Step 3: Add Store.AppendAuditEvent implementation**

Add to `internal/postgres/repositories.go`:

```go
func (store *Store) AppendAuditEvent(ctx context.Context, event traits.SecurityAuditEvent) error {
	_, err := store.pool.Exec(ctx,
		`INSERT INTO security_audit_log
			(actor_subject, action, target_user, tenant_id, stack_id, old_role, new_role, outcome, correlation_id)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		event.ActorSubject, event.Action, event.TargetUser,
		event.TenantID, event.StackID, event.OldRole, event.NewRole,
		event.Outcome, event.CorrelationID,
	)
	return err
}
```

- [ ] **Step 4: Add compile-time interface check**

Add to `internal/postgres/store_test.go` after the existing interface checks:

```go
_ app.AuditRepository = (*Store)(nil)
```

- [ ] **Step 5: Wire Audit into Service in main.go**

Add `Audit: store,` to the `app.Service{}` initialization in `cmd/api/main.go`:

```go
service, err := deps.newService(app.Service{
	// ... existing fields ...
	Audit: store,
})
```

- [ ] **Step 6: Run tests**

Run: `go build ./...`
Expected: PASS (compiles with no errors)

Run: `go test ./internal/postgres/ -run TestMigrateAppliesSchema -v -count=1`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/app/service.go internal/postgres/repositories.go internal/postgres/store_test.go cmd/api/main.go
git commit -m "feat: add AuditRepository interface and Store implementation"
```

---

### Task 3: Add auditError helper and repository tests

**Files:**
- Modify: `internal/app/service.go`
- Modify: `internal/postgres/store_test.go`

**Interfaces:**
- Consumes: `AuditRepository` from Task 2
- Produces: `Service.auditError` method, repository tests

- [ ] **Step 1: Add auditError helper to service.go**

Add to `internal/app/service.go` after the `NewService` function:

```go
func (service *Service) auditError(ctx context.Context, event traits.SecurityAuditEvent) {
	if service.Audit == nil {
		return
	}
	if err := service.Audit.AppendAuditEvent(ctx, event); err != nil {
		log.Printf("security audit write failed: action=%s actor=%s outcome=%s err=%v",
			event.Action, event.ActorSubject, event.Outcome, err)
	}
}
```

Add `"log"` to the imports in `internal/app/service.go`.

- [ ] **Step 2: Add repository test for AppendAuditEvent**

Add to `internal/postgres/store_test.go`:

```go
func TestAppendAuditEventSuccess(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)

	event := traits.SecurityAuditEvent{
		ActorSubject:  "user:6fdb4b4c-2a8f-4cf7-945f-38f67f6a0e91",
		Action:        traits.AuditActionGrant,
		TargetUser:    "user:target-subject",
		TenantID:      traits.TenantID("tenant_123"),
		StackID:       traits.StackID("stack_123"),
		OldRole:       "",
		NewRole:       "owner",
		Outcome:       traits.AuditOutcomeSuccess,
		CorrelationID: "req_abc123",
	}

	if err := store.AppendAuditEvent(ctx, event); err != nil {
		t.Fatalf("AppendAuditEvent returned error: %v", err)
	}

	var got struct {
		ID           int64
		ActorSubject string
		Action       string
		TargetUser   string
		TenantID     string
		StackID      string
		OldRole      string
		NewRole      string
		Outcome      string
		CorrelationID string
		RecordedAt   time.Time
	}
	err := pool.QueryRow(ctx, `
		SELECT id, actor_subject, action, target_user, tenant_id, stack_id,
		       old_role, new_role, outcome, correlation_id, recorded_at
		FROM security_audit_log
		ORDER BY id DESC LIMIT 1
	`).Scan(
		&got.ID, &got.ActorSubject, &got.Action, &got.TargetUser,
		&got.TenantID, &got.StackID, &got.OldRole, &got.NewRole,
		&got.Outcome, &got.CorrelationID, &got.RecordedAt,
	)
	if err != nil {
		t.Fatalf("read audit event: %v", err)
	}

	if got.ActorSubject != event.ActorSubject {
		t.Fatalf("actor_subject = %q, want %q", got.ActorSubject, event.ActorSubject)
	}
	if got.Action != "grant" {
		t.Fatalf("action = %q, want grant", got.Action)
	}
	if got.TargetUser != "user:target-subject" {
		t.Fatalf("target_user = %q, want user:target-subject", got.TargetUser)
	}
	if got.TenantID != "tenant_123" {
		t.Fatalf("tenant_id = %q, want tenant_123", got.TenantID)
	}
	if got.StackID != "stack_123" {
		t.Fatalf("stack_id = %q, want stack_123", got.StackID)
	}
	if got.OldRole != "" {
		t.Fatalf("old_role = %q, want empty", got.OldRole)
	}
	if got.NewRole != "owner" {
		t.Fatalf("new_role = %q, want owner", got.NewRole)
	}
	if got.Outcome != "success" {
		t.Fatalf("outcome = %q, want success", got.Outcome)
	}
	if got.CorrelationID != "req_abc123" {
		t.Fatalf("correlation_id = %q, want req_abc123", got.CorrelationID)
	}
	if got.RecordedAt.IsZero() {
		t.Fatal("recorded_at is zero, want populated by database default")
	}
	if got.ID <= 0 {
		t.Fatalf("id = %d, want positive", got.ID)
	}
}
```

- [ ] **Step 3: Add repository test for empty optional fields**

```go
func TestAppendAuditEventEmptyOptionalFields(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)

	event := traits.SecurityAuditEvent{
		ActorSubject: "user:actor",
		Action:       traits.AuditActionFailedAccessAttempt,
		TenantID:     traits.TenantID("tenant_123"),
		Outcome:      traits.AuditOutcomeFailure,
	}

	if err := store.AppendAuditEvent(ctx, event); err != nil {
		t.Fatalf("AppendAuditEvent returned error: %v", err)
	}

	var targetUser, stackID, oldRole, newRole, correlationID string
	err := pool.QueryRow(ctx, `
		SELECT target_user, stack_id, old_role, new_role, correlation_id
		FROM security_audit_log ORDER BY id DESC LIMIT 1
	`).Scan(&targetUser, &stackID, &oldRole, &newRole, &correlationID)
	if err != nil {
		t.Fatalf("read audit event: %v", err)
	}

	if targetUser != "" {
		t.Fatalf("target_user = %q, want empty", targetUser)
	}
	if stackID != "" {
		t.Fatalf("stack_id = %q, want empty", stackID)
	}
	if oldRole != "" {
		t.Fatalf("old_role = %q, want empty", oldRole)
	}
	if newRole != "" {
		t.Fatalf("new_role = %q, want empty", newRole)
	}
	if correlationID != "" {
		t.Fatalf("correlation_id = %q, want empty", correlationID)
	}
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/postgres/ -run TestAppendAuditEvent -v -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/app/service.go internal/postgres/store_test.go
git commit -m "feat: add auditError helper and repository tests for security audit"
```

---

### Task 4: Integrate audit writes into CreateStack

**Files:**
- Modify: `internal/app/service.go`
- Modify: `internal/app/service_test.go`

**Interfaces:**
- Consumes: `auditError` helper from Task 3, `AuditRepository` from Task 2
- Produces: Audit record after successful owner grant in `CreateStack`

- [ ] **Step 1: Add audit call to CreateStack**

In `internal/app/service.go`, find the `CreateStack` method. After the successful authorization outbox completion block (after `service.AuthorizationOutbox.CompleteAuthorizationRelationship`), add:

```go
service.auditError(ctx, traits.SecurityAuditEvent{
	ActorSubject:  string(actor),
	Action:        traits.AuditActionGrant,
	TargetUser:    string(actor),
	TenantID:      command.TenantID,
	StackID:       stack.ID,
	NewRole:       "owner",
	Outcome:       traits.AuditOutcomeSuccess,
	CorrelationID: "",
})
```

This goes just before the `return stack, nil` at the end of `CreateStack`.

- [ ] **Step 2: Add recording audit repository for tests**

Add to `internal/app/service_test.go`:

```go
type recordingAuditRepository struct {
	events []traits.SecurityAuditEvent
}

func (repository *recordingAuditRepository) AppendAuditEvent(_ context.Context, event traits.SecurityAuditEvent) error {
	repository.events = append(repository.events, event)
	return nil
}
```

- [ ] **Step 3: Add test for CreateStack audit record**

Add to `internal/app/service_test.go`:

```go
func TestCreateStackAuditsOwnerGrant(t *testing.T) {
	t.Parallel()

	ctx := authenticatedContext()
	now := time.Date(2026, 7, 6, 13, 30, 0, 0, time.UTC)
	audit := &recordingAuditRepository{}
	service := NewService(Service{
		Stacks:     &recordingStackRepository{},
		Authorizer: &recordingAuthorizer{},
		StackIDs:   fixedStackIDGenerator{id: traits.StackID("stack_123")},
		Clock:      fixedClock{now: now},
		Audit:      audit,
	})

	_, err := service.CreateStack(ctx, CreateStackCommand{
		TenantID: traits.TenantID("tenant_123"),
		Name:     "Acme Prod",
		Tags:     map[string]string{"env": "prod"},
	})
	if err != nil {
		t.Fatalf("CreateStack returned error: %v", err)
	}

	if len(audit.events) != 1 {
		t.Fatalf("audit events = %d, want 1", len(audit.events))
	}
	event := audit.events[0]
	if event.ActorSubject != keycloakSubject {
		t.Fatalf("actor_subject = %q, want %q", event.ActorSubject, keycloakSubject)
	}
	if event.Action != traits.AuditActionGrant {
		t.Fatalf("action = %q, want %q", event.Action, traits.AuditActionGrant)
	}
	if event.TargetUser != keycloakSubject {
		t.Fatalf("target_user = %q, want %q", event.TargetUser, keycloakSubject)
	}
	if event.TenantID != traits.TenantID("tenant_123") {
		t.Fatalf("tenant_id = %q, want tenant_123", event.TenantID)
	}
	if event.StackID != traits.StackID("stack_123") {
		t.Fatalf("stack_id = %q, want stack_123", event.StackID)
	}
	if event.NewRole != "owner" {
		t.Fatalf("new_role = %q, want owner", event.NewRole)
	}
	if event.Outcome != traits.AuditOutcomeSuccess {
		t.Fatalf("outcome = %q, want %q", event.Outcome, traits.AuditOutcomeSuccess)
	}
}
```

- [ ] **Step 4: Add test for audit write failure does not block CreateStack**

Add to `internal/app/service_test.go`:

```go
type failingAuditRepository struct{}

func (failingAuditRepository) AppendAuditEvent(_ context.Context, _ traits.SecurityAuditEvent) error {
	return errors.New("database unavailable")
}

func TestCreateStackAuditWriteFailureDoesNotBlockMutation(t *testing.T) {
	t.Parallel()

	ctx := authenticatedContext()
	service := NewService(Service{
		Stacks:     &recordingStackRepository{},
		Authorizer: &recordingAuthorizer{},
		StackIDs:   fixedStackIDGenerator{id: traits.StackID("stack_123")},
		Clock:      fixedClock{now: time.Now()},
		Audit:      failingAuditRepository{},
	})

	stack, err := service.CreateStack(ctx, CreateStackCommand{
		TenantID: traits.TenantID("tenant_123"),
		Name:     "Acme Prod",
		Tags:     map[string]string{},
	})
	if err != nil {
		t.Fatalf("CreateStack returned error: %v", err)
	}
	if stack.ID != traits.StackID("stack_123") {
		t.Fatalf("stack ID = %q, want stack_123", stack.ID)
	}
}
```

- [ ] **Step 5: Add test for nil AuditRepository is a no-op**

Add to `internal/app/service_test.go`:

```go
func TestCreateStackWithNilAuditRepositoryDoesNotPanic(t *testing.T) {
	t.Parallel()

	ctx := authenticatedContext()
	service := NewService(Service{
		Stacks:     &recordingStackRepository{},
		Authorizer: &recordingAuthorizer{},
		StackIDs:   fixedStackIDGenerator{id: traits.StackID("stack_123")},
		Clock:      fixedClock{now: time.Now()},
		Audit:      nil,
	})

	_, err := service.CreateStack(ctx, CreateStackCommand{
		TenantID: traits.TenantID("tenant_123"),
		Name:     "Acme Prod",
		Tags:     map[string]string{},
	})
	if err != nil {
		t.Fatalf("CreateStack returned error: %v", err)
	}
}
```

- [ ] **Step 6: Run tests**

Run: `go test ./internal/app/ -run TestCreateStack -v -count=1`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/app/service.go internal/app/service_test.go
git commit -m "feat: audit owner grant on stack creation"
```

---

### Task 5: Integrate audit writes into authorization denial paths

**Files:**
- Modify: `internal/app/service.go`
- Modify: `internal/app/service_test.go`

**Interfaces:**
- Consumes: `auditError` helper from Task 3
- Produces: `failed_access_attempt` audit records when Service methods encounter authorization denials

- [ ] **Step 1: Add audit call to AddTemplateToStack on auth denial**

In `internal/app/service.go`, find `AddTemplateToStack`. After the `authorizeStack` call that returns `ErrForbidden` (line ~451), add an audit call:

```go
if err := authorizeStack(ctx, service.Authorizer, command.StackID, authz.PermissionOperate, ErrForbidden); err != nil {
	service.auditError(ctx, traits.SecurityAuditEvent{
		ActorSubject:  string(actor),
		Action:        traits.AuditActionFailedAccessAttempt,
		TenantID:      command.TenantID,
		StackID:       command.StackID,
		Outcome:       traits.AuditOutcomeFailure,
	})
	return traits.StackTemplate{}, err
}
```

Note: The actor is already extracted before this point in `AddTemplateToStack`. The original code is:
```go
actor, err := authenticatedActor(ctx)
if err != nil {
    return traits.StackTemplate{}, err
}
// ... validation ...
if err := authorizeStack(ctx, service.Authorizer, command.StackID, authz.PermissionOperate, ErrForbidden); err != nil {
    return traits.StackTemplate{}, err
}
```

Change the `authorizeStack` error return to the audit-wrapped version shown above.

- [ ] **Step 2: Add audit call to StartTemplateRun on auth denial**

Same pattern in `StartTemplateRun`. After the `authorizedStackTemplate` call that may return `ErrForbidden` (line ~509):

```go
stackTemplate, err := service.authorizedStackTemplate(ctx, command.TenantID, command.StackTemplateID, authz.PermissionOperate, ErrForbidden)
if err != nil {
	service.auditError(ctx, traits.SecurityAuditEvent{
		ActorSubject:  string(actor),
		Action:        traits.AuditActionFailedAccessAttempt,
		TenantID:      command.TenantID,
		StackID:       "", // stack ID unknown at this point; authorizedStackTemplate resolves it internally
		Outcome:       traits.AuditOutcomeFailure,
	})
	return traits.TemplateRun{}, fmt.Errorf("get stack template: %w", err)
}
```

- [ ] **Step 3: Add audit call to UpdateStackTemplateConfig on auth denial**

Same pattern in `UpdateStackTemplateConfig`:

```go
stackTemplate, err := service.authorizedStackTemplate(ctx, command.TenantID, command.StackTemplateID, authz.PermissionOperate, ErrForbidden)
if err != nil {
	service.auditError(ctx, traits.SecurityAuditEvent{
		ActorSubject:  string(actor),
		Action:        traits.AuditActionFailedAccessAttempt,
		TenantID:      command.TenantID,
		Outcome:       traits.AuditOutcomeFailure,
	})
	return traits.StackTemplate{}, fmt.Errorf("get stack template: %w", err)
}
```

Note: `UpdateStackTemplateConfig` already extracts `actor` via `authenticatedActor(ctx)` at the top.

- [ ] **Step 4: Add audit call to UpgradeStackTemplate on auth denial**

Same pattern in `UpgradeStackTemplate`:

```go
stackTemplate, err := service.authorizedStackTemplate(ctx, command.TenantID, command.StackTemplateID, authz.PermissionOperate, ErrForbidden)
if err != nil {
	service.auditError(ctx, traits.SecurityAuditEvent{
		ActorSubject:  string(actor),
		Action:        traits.AuditActionFailedAccessAttempt,
		TenantID:      command.TenantID,
		Outcome:       traits.AuditOutcomeFailure,
	})
	return traits.StackTemplate{}, fmt.Errorf("get stack template: %w", err)
}
```

Note: `UpgradeStackTemplate` already extracts `actor` via `authenticatedActor(ctx)` at the top.

- [ ] **Step 5: Add test for authorization denial audit**

Add to `internal/app/service_test.go`:

```go
type denyingAuthorizer struct{}

func (denyingAuthorizer) Check(_ context.Context, _ authz.CheckRequest) (authz.CheckResult, error) {
	return authz.CheckResult{Allowed: false}, nil
}
func (denyingAuthorizer) BatchCheck(_ context.Context, _ authz.BatchCheckRequest) (authz.BatchCheckResult, error) {
	return authz.BatchCheckResult{}, nil
}
func (denyingAuthorizer) ListAccessibleStacks(_ context.Context, _ authz.ListAccessibleStacksRequest) (authz.ListAccessibleStacksResult, error) {
	return authz.ListAccessibleStacksResult{}, nil
}
func (denyingAuthorizer) ListGrants(_ context.Context, _ authz.ListGrantsRequest) (authz.ListGrantsResult, error) {
	return authz.ListGrantsResult{}, nil
}
func (denyingAuthorizer) WriteRelationships(_ context.Context, _ authz.Mutation) error {
	return nil
}
func (denyingAuthorizer) DeleteRelationships(_ context.Context, _ authz.Mutation) error {
	return nil
}

func TestAddTemplateToStackAuditsAuthorizationDenial(t *testing.T) {
	t.Parallel()

	audit := &recordingAuditRepository{}
	service := NewService(Service{
		Authorizer: &denyingAuthorizer{},
		Stacks: &recordingStackRepository{
			stack: traits.Stack{ID: "stack_123", TenantID: "tenant_123", Slug: "acme-prod"},
		},
		TemplateRevisionMetadata: &recordingTemplateRepository{
			template: traits.TemplateRevision{ID: "template_123", TenantID: "tenant_123", Status: traits.TemplateRevisionActive},
		},
		TemplateRevisions: &recordingTemplateRepository{
			variables: []traits.TemplateVariable{{Name: "region", Required: true, HasDefault: true}},
		},
		StackTemplateInstaller: &recordingStackTemplateInstaller{},
		StackTemplateIDs:       fixedStackTemplateIDGenerator{id: traits.StackTemplateID("stack_template_a1b2c3d4")},
		Audit:                  audit,
	})

	_, err := service.AddTemplateToStack(authenticatedContext(), AddTemplateToStackCommand{
		TenantID:           traits.TenantID("tenant_123"),
		StackID:            traits.StackID("stack_123"),
		TemplateRevisionID: traits.TemplateRevisionID("template_123"),
		SelectedRef:        "main",
		ConfigJSON:         json.RawMessage(`{"region":"us-east-1"}`),
	})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("error = %v, want ErrForbidden", err)
	}

	if len(audit.events) != 1 {
		t.Fatalf("audit events = %d, want 1", len(audit.events))
	}
	event := audit.events[0]
	if event.Action != traits.AuditActionFailedAccessAttempt {
		t.Fatalf("action = %q, want %q", event.Action, traits.AuditActionFailedAccessAttempt)
	}
	if event.Outcome != traits.AuditOutcomeFailure {
		t.Fatalf("outcome = %q, want %q", event.Outcome, traits.AuditOutcomeFailure)
	}
	if event.ActorSubject != keycloakSubject {
		t.Fatalf("actor_subject = %q, want %q", event.ActorSubject, keycloakSubject)
	}
}
```

- [ ] **Step 6: Run tests**

Run: `go test ./internal/app/ -run "TestAddTemplateToStack|TestStartTemplateRun|TestUpdateStackTemplateConfig|TestUpgradeStackTemplate" -v -count=1`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/app/service.go internal/app/service_test.go
git commit -m "feat: audit authorization denials in access mutation methods"
```

---

### Task 6: Update sprint documentation

**Files:**
- Modify: `docs/sprint/authn_and_authz/README.md`

**Interfaces:**
- Consumes: all tasks above
- Produces: Updated sprint backlog status

- [ ] **Step 1: Update AUTH-014 status**

In `docs/sprint/authn_and_authz/README.md`, find the AUTH-014 row and change `Not Started` to `Done`.

- [ ] **Step 2: Run full test suite**

Run: `go test ./... -count=1`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add docs/sprint/authn_and_authz/README.md
git commit -m "docs: mark AUTH-014 as Done in sprint backlog"
```
