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
// Its value is intentionally opaque so callers cannot construct unchecked IDs.
type Subject struct {
	value string
}

// String renders the canonical authorization identifier for a provider adapter.
func (subject Subject) String() string {
	return subject.value
}

// Valid reports whether the subject is a canonical, validated authorization ID.
func (subject Subject) Valid() bool {
	return validCanonicalIdentifier("user", subject.value)
}

// Stack is a validated application stack ID in its canonical authorization form.
// Its value is intentionally opaque so callers cannot construct unchecked IDs.
type Stack struct {
	value string
}

// String renders the canonical authorization identifier for a provider adapter.
func (stack Stack) String() string {
	return stack.value
}

// Valid reports whether the stack is a canonical, validated authorization ID.
func (stack Stack) Valid() bool {
	return validCanonicalIdentifier("stack", stack.value)
}

// Role is a directly assignable stack relationship. Its value is intentionally
// opaque so derived permissions cannot be used as relationship-write targets.
type Role struct {
	value string
}

var (
	RoleOwner    = Role{value: "owner"}
	RoleOperator = Role{value: "operator"}
	RoleApprover = Role{value: "approver"}
	RoleViewer   = Role{value: "viewer"}
)

// RoleFromDirectRelation returns one of the direct, writable role relations.
func RoleFromDirectRelation(relation string) (Role, error) {
	role := Role{value: relation}
	if !role.Valid() {
		return Role{}, fmt.Errorf("%w: invalid direct role", ErrInvalidInput)
	}
	return role, nil
}

// String renders the direct relationship name for a provider adapter.
func (role Role) String() string {
	return role.value
}

// Valid reports whether the role is a direct, writable relationship.
func (role Role) Valid() bool {
	switch role.value {
	case "owner", "operator", "approver", "viewer":
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
	if err != nil {
		return Subject{}, err
	}
	return Subject{value: value}, nil
}

// StackFromID returns the canonical authorization identifier for id.
func StackFromID(id string) (Stack, error) {
	value, err := canonicalIdentifier("stack", id)
	if err != nil {
		return Stack{}, err
	}
	return Stack{value: value}, nil
}

func canonicalIdentifier(kind, value string) (string, error) {
	if value == "" || strings.Contains(value, ":") || strings.IndexFunc(value, unicode.IsSpace) >= 0 || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return "", fmt.Errorf("%w: invalid %s identifier", ErrInvalidInput, kind)
	}
	return kind + ":" + value, nil
}

func validCanonicalIdentifier(kind, value string) bool {
	prefix := kind + ":"
	if !strings.HasPrefix(value, prefix) {
		return false
	}
	_, err := canonicalIdentifier(kind, strings.TrimPrefix(value, prefix))
	return err == nil
}

// CheckRequest asks whether Subject has Permission for Stack.
type CheckRequest struct {
	Subject    Subject
	Stack      Stack
	Permission Permission
}

// Valid reports whether the request can safely cross the adapter boundary.
func (request CheckRequest) Valid() bool {
	return request.Subject.Valid() && request.Stack.Valid() && request.Permission.Valid()
}

// CheckResult is the explicit outcome of a Check request.
type CheckResult struct {
	Allowed bool
}

// BatchCheckRequest groups independent checks into one authorization request.
type BatchCheckRequest struct {
	Checks []CheckRequest
}

// Valid reports whether every requested check can cross the adapter boundary.
func (request BatchCheckRequest) Valid() bool {
	if len(request.Checks) == 0 {
		return false
	}
	for _, check := range request.Checks {
		if !check.Valid() {
			return false
		}
	}
	return true
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

// Valid reports whether the request can safely cross the adapter boundary.
func (request ListAccessibleStacksRequest) Valid() bool {
	return request.Subject.Valid() && request.Permission.Valid()
}

// ListAccessibleStacksResult contains only validated canonical stack IDs.
type ListAccessibleStacksResult struct {
	Stacks []Stack
}

// ListGrantsRequest asks for direct role assignments on a stack.
type ListGrantsRequest struct {
	Stack Stack
}

// Valid reports whether the request can safely cross the adapter boundary.
func (request ListGrantsRequest) Valid() bool {
	return request.Stack.Valid()
}

// ListGrantsResult contains only validated direct role assignments.
type ListGrantsResult struct {
	Grants []Grant
}

// Grant is a direct role assignment for a subject on a stack.
type Grant struct {
	subject Subject
	stack   Stack
	role    Role
}

// NewGrant returns a validated direct role assignment.
func NewGrant(subject Subject, stack Stack, role Role) (Grant, error) {
	grant := Grant{subject: subject, stack: stack, role: role}
	if !grant.Valid() {
		return Grant{}, fmt.Errorf("%w: invalid direct role grant", ErrInvalidInput)
	}
	return grant, nil
}

// Subject returns the grant subject.
func (grant Grant) Subject() Subject {
	return grant.subject
}

// Stack returns the grant stack.
func (grant Grant) Stack() Stack {
	return grant.stack
}

// Role returns the grant's direct role.
func (grant Grant) Role() Role {
	return grant.role
}

// Valid reports whether the grant has validated identifiers and a direct role.
func (grant Grant) Valid() bool {
	return grant.subject.Valid() && grant.stack.Valid() && grant.role.Valid()
}

// Mutation changes a set of direct role assignments. Confirmation requests
// that the adapter verifies the resulting state before reporting success.
type Mutation struct {
	grants  []Grant
	confirm bool
}

// NewMutation returns a validated relationship mutation. It copies grants so
// callers cannot alter the mutation after validation.
func NewMutation(grants []Grant, confirm bool) (Mutation, error) {
	if len(grants) == 0 {
		return Mutation{}, fmt.Errorf("%w: relationship mutation has no grants", ErrInvalidInput)
	}
	validated := make([]Grant, len(grants))
	for i, grant := range grants {
		if !grant.Valid() {
			return Mutation{}, fmt.Errorf("%w: invalid relationship mutation grant", ErrInvalidInput)
		}
		validated[i] = grant
	}
	return Mutation{grants: validated, confirm: confirm}, nil
}

// Grants returns a copy of the mutation's validated direct grants.
func (mutation Mutation) Grants() []Grant {
	return append([]Grant(nil), mutation.grants...)
}

// Confirm reports whether the mutation should be confirmed before success.
func (mutation Mutation) Confirm() bool {
	return mutation.confirm
}

// Valid reports whether the mutation contains one or more validated grants.
func (mutation Mutation) Valid() bool {
	if len(mutation.grants) == 0 {
		return false
	}
	for _, grant := range mutation.grants {
		if !grant.Valid() {
			return false
		}
	}
	return true
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
