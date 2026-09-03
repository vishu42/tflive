package domain

// The account boundary every other record hangs off.

// Tenant is an account boundary for product records.
type Tenant struct {
	ID   TenantID
	Name string
}
