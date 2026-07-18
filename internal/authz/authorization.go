// Package authz defines the provider-neutral authorization contract.
package authz

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode"
)

var (
	ErrInvalidInput      = errors.New("invalid authorization input")
	ErrTimeout           = errors.New("authorization timeout")
	ErrUnavailable       = errors.New("authorization unavailable")
	ErrMalformedResponse = errors.New("malformed authorization response")
	ErrWriteUnconfirmed  = errors.New("authorization write unconfirmed")
)

// Subject is a validated Keycloak subject in its canonical authorization form.
type Subject string

// Stack is a validated application stack ID in its canonical authorization form.
type Stack string

// Role is a directly assignable stack relationship.
type Role string

const (
	RoleOwner    Role = "owner"
	RoleOperator Role = "operator"
	RoleApprover Role = "approver"
	RoleViewer   Role = "viewer"
)

// Valid reports whether the role is a direct, writable relationship.
func (role Role) Valid() bool {
	switch role {
	case RoleOwner, RoleOperator, RoleApprover, RoleViewer:
		return true
	default:
		return false
	}
}

// Permission is a derived authorization relationship.
type Permission string

const (
	PermissionView         Permission = "can_view"
	PermissionOperate      Permission = "can_operate"
	PermissionApprove      Permission = "can_approve"
	PermissionManageAccess Permission = "can_manage_access"
)

// Valid reports whether the permission is a supported derived relationship.
func (permission Permission) Valid() bool {
	switch permission {
	case PermissionView, PermissionOperate, PermissionApprove, PermissionManageAccess:
		return true
	default:
		return false
	}
}

// SubjectFromKeycloakSub returns the canonical authorization identifier for sub.
func SubjectFromKeycloakSub(sub string) (Subject, error) {
	value, err := canonicalIdentifier("user", sub)
	return Subject(value), err
}

// StackFromID returns the canonical authorization identifier for id.
func StackFromID(id string) (Stack, error) {
	value, err := canonicalIdentifier("stack", id)
	return Stack(value), err
}

func canonicalIdentifier(kind, value string) (string, error) {
	if value == "" || strings.Contains(value, ":") || strings.IndexFunc(value, unicode.IsSpace) >= 0 || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return "", fmt.Errorf("%w: invalid %s identifier", ErrInvalidInput, kind)
	}
	return kind + ":" + value, nil
}

// CheckRequest asks whether Subject has Permission for Stack.
type CheckRequest struct {
	Subject    Subject
	Stack      Stack
	Permission Permission
}

// CheckResult is the explicit outcome of a Check request.
type CheckResult struct {
	Allowed bool
}

// BatchCheckRequest groups independent checks into one authorization request.
type BatchCheckRequest struct {
	Checks []CheckRequest
}

// BatchCheckResult contains one result, in request order, for every batch check.
type BatchCheckResult struct {
	Results []CheckResult
}

// ListAccessibleStacksRequest asks for stacks a subject can access through a
// derived permission.
type ListAccessibleStacksRequest struct {
	Subject    Subject
	Permission Permission
}

// ListAccessibleStacksResult contains only validated canonical stack IDs.
type ListAccessibleStacksResult struct {
	Stacks []Stack
}

// ListGrantsRequest asks for direct role assignments on a stack.
type ListGrantsRequest struct {
	Stack Stack
}

// ListGrantsResult contains only validated direct role assignments.
type ListGrantsResult struct {
	Grants []Grant
}

// Grant is a direct role assignment for a subject on a stack.
type Grant struct {
	Subject Subject
	Stack   Stack
	Role    Role
}

// Mutation changes a set of direct role assignments. Confirmation requests
// that the adapter verifies the resulting state before reporting success.
type Mutation struct {
	Grants  []Grant
	Confirm bool
}

// Authorizer is the provider-neutral authorization port.
type Authorizer interface {
	Check(context.Context, CheckRequest) (CheckResult, error)
	BatchCheck(context.Context, BatchCheckRequest) (BatchCheckResult, error)
	ListAccessibleStacks(context.Context, ListAccessibleStacksRequest) (ListAccessibleStacksResult, error)
	ListGrants(context.Context, ListGrantsRequest) (ListGrantsResult, error)
	WriteRelationships(context.Context, Mutation) error
	DeleteRelationships(context.Context, Mutation) error
}

// HTTPStatus maps authorization dependency failures to stable API responses.
func HTTPStatus(err error) (status int, code string, ok bool) {
	switch {
	case errors.Is(err, ErrWriteUnconfirmed):
		return http.StatusServiceUnavailable, "authorization_write_unconfirmed", true
	case errors.Is(err, ErrTimeout), errors.Is(err, ErrUnavailable), errors.Is(err, ErrMalformedResponse):
		return http.StatusServiceUnavailable, "authorization_unavailable", true
	default:
		return 0, "", false
	}
}
