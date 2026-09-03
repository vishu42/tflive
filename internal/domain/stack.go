package domain

// Stacks and the multi-template runs that operate on them.

import (
	"time"
)

// StackStatus reports whether a stack has finished provisioning. Creation
// enqueues the owner grant rather than writing it inline, so the stack exists
// before anyone can act on it; the status makes that window visible instead of
// leaving the caller to guess why access is missing.
type StackStatus string

const (
	StackStatusProvisioning StackStatus = "provisioning"
	StackStatusReady        StackStatus = "ready"
)

// Stack is a logical infrastructure composition.
type Stack struct {
	ID       StackID           `json:"id"`
	TenantID TenantID          `json:"tenant_id"`
	Name     string            `json:"name"`
	Slug     string            `json:"slug"`
	Status   StackStatus       `json:"status"`
	Tags     map[string]string `json:"tags"`
	// DefaultCredentialIDs is retained for backward-compatible reads; new credentials are scope-owned records.
	DefaultCredentialIDs []CredentialSetID `json:"default_credential_ids"`
	CreatedBy            UserID            `json:"created_by"`
	CreatedAt            time.Time         `json:"created_at"`
}

// StackRun represents a coordinated multi-template operation.
type StackRun struct {
	ID        StackRunID
	TenantID  TenantID
	StackID   StackID
	Operation OperationType
	Status    TemplateRunStatus
}
