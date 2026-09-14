package domain

// Stacks and the multi-template runs that operate on them.

import (
	"time"
)

// StackStatus is a stack's lifecycle state. Ready is the only value: creation
// writes the stack row and its owner grant in one transaction, so a stack is
// usable the moment it exists. The field stays on the wire so a later state
// has somewhere to go.
type StackStatus string

const (
	StackStatusReady StackStatus = "ready"
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
