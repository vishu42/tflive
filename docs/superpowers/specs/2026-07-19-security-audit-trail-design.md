# AUTH-014: Security Audit Trail Design

## Summary

Persist an append-only application audit trail for authorization changes and sensitive authorization events. Uses a single `security_audit_log` table with discrete columns, an `AuditRepository` interface, and direct inserts from app service methods with log-and-continue failure handling.

## Motivation

AUTH-014 (P0) requires audit records for grant, role change, revoke, failed access-management attempts, and self-approval rejections. AUTH-015 (self-approval prevention) and AUTH-017 (access management APIs) both depend on this being complete first.

## Design Decisions

| Decision | Choice | Rationale |
|---|---|---|
| Storage model | Single table, discrete columns | Uniform schema per acceptance criteria; follows existing repo-per-table pattern |
| Audit failure behavior | Log-and-continue | Acceptance criteria require fail-safe for access mutations; audit is best-effort |
| Integration point | Direct insert from app service | Simplest; no outbox or async complexity needed for best-effort writes |
| Append-only enforcement | Interface-level (no update/delete methods) | Matches codebase pattern; no DB-level trigger needed |

## Database Schema

Migration: `internal/postgres/migrations/0009_security_audit.sql`

```sql
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

### Column semantics

| Column | Required | Description |
|---|---|---|
| `id` | auto | Monotonic primary key for ordering |
| `recorded_at` | auto | UTC timestamp of audit write (database default) |
| `actor_subject` | yes | Keycloak `sub` claim of the authenticated actor |
| `action` | yes | One of: `grant`, `revoke`, `role_change`, `failed_access_attempt`, `self_approval_rejected` |
| `target_user` | no | Keycloak `sub` of the user being acted upon (empty when not applicable) |
| `tenant_id` | yes | Tenant that owns the audited context |
| `stack_id` | no | Stack being acted upon (empty for global operations) |
| `old_role` | no | Previous role value (empty for grants, non-applicable events) |
| `new_role` | no | New or assigned role value (empty for revokes, non-applicable events) |
| `outcome` | yes | `success` or `failure` |
| `correlation_id` | no | Request or workflow correlation identifier |

### Security invariants

- `actor_subject` stores only the Keycloak `sub` claim. Access tokens, passwords, client secrets, and session tokens are never stored.
- The table has no `UPDATE` or `DELETE` path through application code. The repository interface exposes only `AppendAuditEvent`.
- Empty string defaults for optional fields avoid NULL semantics while keeping the schema simple.

## Domain Types

Add to `internal/traits/traits.go`:

```go
type AuditAction string

const (
    AuditActionGrant                AuditAction = "grant"
    AuditActionRevoke               AuditAction = "revoke"
    AuditActionRoleChange           AuditAction = "role_change"
    AuditActionFailedAccessAttempt  AuditAction = "failed_access_attempt"
    AuditActionSelfApprovalRejected AuditAction = "self_approval_rejected"
)

type AuditOutcome string

const (
    AuditOutcomeSuccess AuditOutcome = "success"
    AuditOutcomeFailure AuditOutcome = "failure"
)

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

## Repository Interface

Add to `internal/app/service.go`:

```go
type AuditRepository interface {
    AppendAuditEvent(ctx context.Context, event traits.SecurityAuditEvent) error
}
```

Wired into `Service`:

```go
type Service struct {
    // ... existing fields ...
    Audit AuditRepository
}
```

## Repository Implementation

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

## Error Handling

A helper method on `Service` ensures audit failures never block access mutations:

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

Callers use `service.auditError(ctx, event)` after the mutation completes. The return value is always nil.

## Integration Points

### AUTH-014 scope (this ticket)

| Method | Action | When |
|---|---|---|
| `CreateStack` | `grant` / success | After successful owner grant write |
| `AddTemplateToStack` | `failed_access_attempt` / failure | When `authorizeStack` returns `ErrForbidden` |
| `StartTemplateRun` | `failed_access_attempt` / failure | When `authorizeStack` returns `ErrForbidden` |
| `UpdateStackTemplateConfig` | `failed_access_attempt` / failure | When `authorizeStack` returns `ErrForbidden` |
| `UpgradeStackTemplate` | `failed_access_attempt` / failure | When `authorizeStack` returns `ErrForbidden` |
| `GetStack` | `failed_access_attempt` / failure | When `authorizeStack` returns `ErrNotFound` (hidden resource) |

Note: `authorizeStack` is a package-level function without access to `Service.Audit`. Audit calls are placed in the `Service` methods that call it, after the authorization check returns a denial error. This avoids changing the `authorizeStack` signature for all callers.

### Future integration (AUTH-015, AUTH-017)

| Method | Action | When |
|---|---|---|
| `ApproveRun` | `self_approval_rejected` / failure | When approver sub matches trigger actor |
| `GrantRole` (AUTH-017) | `grant` or `role_change` / success | After successful role write |
| `RevokeRole` (AUTH-017) | `revoke` / success | After successful role removal |

## Testing

### Repository tests (`internal/postgres/store_test.go`)

- `TestAppendAuditEvent_Success` — insert event, verify all fields round-trip
- `TestAppendAuditEvent_EmptyOptionalFields` — target_user, stack_id, old_role, new_role default to empty
- `TestAppendAuditEvent_AllActions` — verify all five action types insert correctly
- `TestAppendAuditEvent_AutoRecordedAt` — verify `recorded_at` is populated by database default

### Integration tests (`internal/app/` package)

- `TestCreateStack_AuditsOwnerGrant` — after `CreateStack`, verify audit record with correct actor, action=grant, outcome=success, stack_id
- `TestAddTemplateToStack_AuditsDeniedAccess` — when `authorizeStack` denies, verify audit record with action=failed_access_attempt, outcome=failure
- `TestStartTemplateRun_AuditsDeniedAccess` — same pattern for run creation
- `TestAuditWriteFailure_DoesNotBlockMutation` — mock failing `AuditRepository`, verify access mutation still succeeds

### Unit test for `auditError`

- Nil `AuditRepository` is a no-op
- Error from `AppendAuditEvent` is swallowed (logged, not returned)

## Wiring

In `cmd/api/main.go`, the `Store` already implements all repository interfaces. Add `Audit: store` to the `Service` initialization block alongside existing repository wiring.

## Migration path

- New migration `0009_security_audit.sql` only adds a table and indexes. No existing tables are modified.
- Rollback: `DROP TABLE IF EXISTS security_audit_log`.
