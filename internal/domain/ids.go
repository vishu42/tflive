package domain

// Identifiers shared by every record in the domain.

// ID is the common storage shape for platform identifiers.
type ID string

// Valid reports whether the ID is non-empty.
func (id ID) Valid() bool {
	return id != ""
}

type (
	TenantID               ID
	UserID                 ID
	SourceTemplateID       ID
	TemplateRevisionID     ID
	TemplateRegistrationID ID
	StackID                ID
	StackTemplateID        ID
	TemplateRunID          ID
	StackRunID             ID
	CredentialSetID        ID
)
