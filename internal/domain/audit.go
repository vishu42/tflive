package domain

// The security audit trail.

import (
	"time"
)

// AuditAction identifies a security-relevant authorization event.
type AuditAction string

const (
	AuditActionGrant                AuditAction = "grant"
	AuditActionRevoke               AuditAction = "revoke"
	AuditActionRoleChange           AuditAction = "role_change"
	AuditActionFailedAccessAttempt  AuditAction = "failed_access_attempt"
	AuditActionSelfApprovalRejected AuditAction = "self_approval_rejected"
	AuditActionApprovalGranted      AuditAction = "approval_granted"
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
