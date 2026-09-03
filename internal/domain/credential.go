package domain

// Encrypted credentials and the resources that own them.

import (
	"time"
)

// CredentialScope identifies the resource that owns one encrypted credential.
type CredentialScope string

const (
	CredentialScopeStack         CredentialScope = "stack"
	CredentialScopeStackTemplate CredentialScope = "stack_template"
)

// CredentialSet is a reference to runtime execution credentials.
type CredentialSet struct {
	ID              CredentialSetID // Stable identifier used by API deletion and audit references.
	TenantID        TenantID        // Tenant that owns the encrypted credential.
	StackID         StackID         // Stack owner; empty when StackTemplateID is populated.
	StackTemplateID StackTemplateID // Optional template owner for template-specific overrides.
	Name            string          // Provider environment variable name.
	Ciphertext      string          // Encrypted value; plaintext must never be persisted here.
	CreatedAt       time.Time       // Creation timestamp used for metadata display and auditing.
}
