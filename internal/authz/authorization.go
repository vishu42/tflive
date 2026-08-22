// Package authz defines the provider-neutral authorization contract — a
// (Subject, Relation, Object) tuple mirroring OpenFGA's wire shape — and the
// handler that reconciles grants onto whichever provider implements it.
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

// Subject is an Object in the tuple's user slot. It wraps Object because on the
// wire both slots hold "type:id"; a future relation field carries usersets
// ("group:eng#member") with no restructuring.
type Subject struct {
	object Object
}

// SubjectFromOIDCSub returns the canonical authorization identifier for sub,
// the "sub" claim of a verified ID token. This is the one identifier tflive
// does not originate, so the character rules matter most here.
//
//	SubjectFromOIDCSub("00u1b2c3")      → Subject{"user:00u1b2c3"}, nil
//	SubjectFromOIDCSub("kc-sub-123")    → Subject{"user:kc-sub-123"}, nil
//	SubjectFromOIDCSub("alice#member")  → Subject{}, ErrInvalidInput  (userset)
//	SubjectFromOIDCSub("*")             → Subject{}, ErrInvalidInput  (everyone)
func SubjectFromOIDCSub(sub string) (Subject, error) {
	object, err := ObjectFromID(TypeUser, sub)
	if err != nil {
		return Subject{}, err
	}
	return Subject{object: object}, nil
}

// Type returns the subject's object type. Always TypeUser today, because
// SubjectFromOIDCSub is the only constructor and it always builds one; that is
// a fact about today's one call site, not something this method enforces.
// #141 adds platform subjects for the parent edge without changing Type.
//
//	SubjectFromOIDCSub("alice") then .Type()  → TypeUser
//	Subject{}.Type()                          → ""
func (subject Subject) Type() ObjectType {
	return subject.object.Type()
}

// String renders the canonical authorization identifier for a provider adapter.
//
//	SubjectFromOIDCSub("alice") then .String()  → "user:alice"
//	Subject{}.String()                          → ""
func (subject Subject) String() string {
	return subject.object.String()
}

// Valid reports whether the subject is a canonical, validated authorization
// ID for its wrapped object type — whatever that type is. It does not assert
// TypeUser: Subject wraps a general Object precisely so #141 can put
// platform:tflive in the user slot without restructuring, and today's single
// constructor (SubjectFromOIDCSub) is what keeps every Subject a user in
// practice, not this method.
//
//	SubjectFromOIDCSub("alice") then .Valid()  → true
//	Subject{}.Valid()                          → false
func (subject Subject) Valid() bool {
	return subject.object.Valid()
}

// ObjectType is an object type declared in the authorization model. It is not
// validated against a local list: OpenFGA validates type against the model, so
// an unknown type is refused against the single source of truth.
type ObjectType string

const (
	TypeUser  ObjectType = "user"
	TypeStack ObjectType = "stack"
)

// Object is a validated {type, id} in canonical "type:id" form. Its value is
// intentionally opaque so callers cannot construct unchecked IDs.
type Object struct {
	objectType ObjectType
	value      string
}

// ObjectFromID returns the canonical authorization identifier for id. The id
// must be a bare identifier: it is the caller's raw value, never an
// already-prefixed one. objectType gets the same structural character check
// as id: callers pass it as an exported ObjectType value, not always an
// in-package literal, so it must not be able to smuggle a ':', '#', or '*'
// into the rendered tuple.
//
//	ObjectFromID(TypeStack, "abc123")            → Object{"stack:abc123"}, nil
//	ObjectFromID(TypeUser, "00u1b2c3")           → Object{"user:00u1b2c3"}, nil
//	ObjectFromID(TypeStack, "stack:abc")         → Object{}, ErrInvalidInput  (already prefixed)
//	ObjectFromID(TypeUser, "al*ce")              → Object{}, ErrInvalidInput  (wildcard char)
//	ObjectFromID(TypeUser, "")                   → Object{}, ErrInvalidInput
//	ObjectFromID(ObjectType("stack:evil"), "abc") → Object{}, ErrInvalidInput  (type forges a prefix)
func ObjectFromID(objectType ObjectType, id string) (Object, error) {
	if objectType == "" || !safeTupleToken(string(objectType)) {
		return Object{}, fmt.Errorf("%w: invalid object type", ErrInvalidInput)
	}
	value, err := canonicalIdentifier(string(objectType), id)
	if err != nil {
		return Object{}, err
	}
	return Object{objectType: objectType, value: value}, nil
}

// Type returns the object's declared type.
//
//	ObjectFromID(TypeStack, "abc").Type()  → TypeStack
//	Object{}.Type()                        → ""
func (object Object) Type() ObjectType {
	return object.objectType
}

// String renders the canonical authorization identifier for a provider adapter.
//
//	ObjectFromID(TypeStack, "abc").String()  → "stack:abc"
//	Object{}.String()                        → ""
func (object Object) String() string {
	return object.value
}

// Valid reports whether the object is a canonical, validated authorization ID.
// The zero Object is never valid, so a struct literal cannot bypass the
// constructor.
//
//	ObjectFromID(TypeStack, "abc") then .Valid()  → true
//	Object{}.Valid()                              → false
func (object Object) Valid() bool {
	return object.objectType != "" && validCanonicalIdentifier(string(object.objectType), object.value)
}

// canonicalIdentifier renders "kind:value" after refusing any value that could
// change a tuple's meaning rather than merely be malformed. ':' would forge the
// type prefix, '#' would make the value a userset reference, and '*' would make
// it the typed wildcard that matches every user.
//
//	canonicalIdentifier("user", "kc-sub-1")      → "user:kc-sub-1", nil
//	canonicalIdentifier("stack", "abc123")       → "stack:abc123", nil
//	canonicalIdentifier("user", "alice#member")  → "", ErrInvalidInput
//	canonicalIdentifier("user", "*")             → "", ErrInvalidInput
//	canonicalIdentifier("user", "a b")           → "", ErrInvalidInput
func canonicalIdentifier(kind, value string) (string, error) {
	if !safeTupleToken(value) {
		return "", fmt.Errorf("%w: invalid %s identifier", ErrInvalidInput, kind)
	}
	return kind + ":" + value, nil
}

// safeTupleToken reports whether token can appear in a tuple without changing
// its meaning. ':' would forge a type prefix, '#' would make a userset
// reference, and '*' would make the typed wildcard that matches every user.
//
//	safeTupleToken("can_view")  → true
//	safeTupleToken("a#b")       → false
//	safeTupleToken("")          → false
func safeTupleToken(token string) bool {
	if token == "" || strings.ContainsAny(token, ":#*") {
		return false
	}
	if strings.IndexFunc(token, unicode.IsSpace) >= 0 || strings.IndexFunc(token, unicode.IsControl) >= 0 {
		return false
	}
	return true
}

func validCanonicalIdentifier(kind, value string) bool {
	prefix := kind + ":"
	if !strings.HasPrefix(value, prefix) {
		return false
	}
	_, err := canonicalIdentifier(kind, strings.TrimPrefix(value, prefix))
	return err == nil
}

// CheckRequest asks whether Subject has Relation on Object.
type CheckRequest struct {
	Subject  Subject
	Relation Relation
	Object   Object
}

// Valid reports whether the request can safely cross the adapter boundary.
//
//	CheckRequest{user:alice, can_view, stack:abc}.Valid()  → true
//	CheckRequest{user:alice, parent, stack:abc}.Valid()    → true   (checking is safe)
//	CheckRequest{user:alice, <zero>, stack:abc}.Valid()    → false
//	CheckRequest{}.Valid()                                 → false
func (request CheckRequest) Valid() bool {
	return request.Subject.Valid() && request.Relation.Valid() && request.Object.Valid()
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

// ListGrantsRequest asks for direct role assignments on an object.
type ListGrantsRequest struct {
	Object Object
}

// Valid reports whether the request can safely cross the adapter boundary.
//
//	ListGrantsRequest{Object: stack:abc}.Valid()  → true
//	ListGrantsRequest{}.Valid()                   → false
func (request ListGrantsRequest) Valid() bool {
	return request.Object.Valid()
}

// ListGrantsResult contains only validated grants. Valid checks well-formed
// identifiers and a well-formed relation name, not that the relation is a
// direct, writable one — see Grant.Valid.
type ListGrantsResult struct {
	Grants []Grant
}

// Grant is a direct, grantable role assignment for a subject on an object.
type Grant struct {
	subject  Subject
	object   Object
	relation Relation
}

// NewGrant returns a validated direct role assignment. It refuses any relation
// the grant API may not write, so a Grant can never hold a structural edge
// however the Relation reached it.
//
//	NewGrant(user:alice, stack:abc, RelationOwner)    → Grant{…}, nil
//	NewGrant(user:alice, stack:abc, RelationCanView)  → Grant{}, ErrInvalidInput
//	NewGrant(user:alice, stack:abc, <"parent">)       → Grant{}, ErrInvalidInput
//	NewGrant(Subject{}, stack:abc, RelationOwner)     → Grant{}, ErrInvalidInput
//	NewGrant(user:alice, Object{}, RelationOwner)     → Grant{}, ErrInvalidInput
func NewGrant(subject Subject, object Object, relation Relation) (Grant, error) {
	grant := Grant{subject: subject, object: object, relation: relation}
	if !grant.Valid() {
		return Grant{}, fmt.Errorf("%w: invalid direct role grant", ErrInvalidInput)
	}
	return grant, nil
}

// Subject returns the grant subject.
//
//	NewGrant(user:alice, stack:abc, RelationOwner) then .Subject().String()  → "user:alice"
func (grant Grant) Subject() Subject {
	return grant.subject
}

// Object returns the grant object.
//
//	NewGrant(user:alice, stack:abc, RelationOwner) then .Object().String()  → "stack:abc"
func (grant Grant) Object() Object {
	return grant.object
}

// Relation returns the grant's direct relation. It is comparable by value, so
// callers can write grant.Relation() == RelationOwner.
//
//	NewGrant(user:alice, stack:abc, RelationOwner) then .Relation()  → RelationOwner
func (grant Grant) Relation() Relation {
	return grant.relation
}

// Valid reports whether the grant has validated identifiers and a grantable
// relation. Grantable, not merely valid: this is what keeps a structural edge
// out of a Mutation.
//
//	NewGrant(user:alice, stack:abc, RelationOwner) then .Valid()  → true
//	Grant{}.Valid()                                               → false
func (grant Grant) Valid() bool {
	return grant.subject.Valid() && grant.object.Valid() && grant.relation.Grantable()
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

// ListSubjectGrantsRequest asks for one subject's direct roles on one object.
type ListSubjectGrantsRequest struct {
	Subject Subject
	Object  Object
}

// Valid reports whether the request names a well-formed subject and object.
//
//	ListSubjectGrantsRequest{user:alice, stack:abc}.Valid()  → true
//	ListSubjectGrantsRequest{Object: stack:abc}.Valid()      → false  (no subject)
func (request ListSubjectGrantsRequest) Valid() bool {
	return request.Subject.Valid() && request.Object.Valid()
}

// SubjectGrantLister reads the direct roles one subject holds on one object.
//
// It is deliberately separate from Authorizer: only reconciling handlers need
// this narrow read, and widening Authorizer would force every implementation
// to grow a method it never calls.
type SubjectGrantLister interface {
	ListSubjectGrants(context.Context, ListSubjectGrantsRequest) (ListGrantsResult, error)
}

// Authorizer is the provider-neutral authorization port.
type Authorizer interface {
	Check(context.Context, CheckRequest) (CheckResult, error)
	BatchCheck(context.Context, BatchCheckRequest) (BatchCheckResult, error)
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
