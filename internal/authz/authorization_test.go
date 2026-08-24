package authz

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
)

// Pins the canonical wire form and that an Object remembers its own type.
func TestObjectFromIDIsCanonicalAndTyped(t *testing.T) {
	object, err := ObjectFromID(TypeStack, "stack-123")
	if err != nil || object.String() != "stack:stack-123" {
		t.Fatalf("ObjectFromID() = %q, %v", object.String(), err)
	}
	if object.Type() != TypeStack {
		t.Fatalf("Type() = %q, want %q", object.Type(), TypeStack)
	}
	if !object.Valid() {
		t.Fatal("constructed object must be valid")
	}
}

// Pins that a struct literal cannot bypass ObjectFromID's validation.
func TestZeroObjectIsInvalid(t *testing.T) {
	if (Object{}).Valid() {
		t.Fatal("zero Object must not validate")
	}
}

// Pins that ObjectFromID is genuinely type-parameterised, not stack-only.
func TestObjectCarriesItsType(t *testing.T) {
	user, err := ObjectFromID(TypeUser, "alice")
	if err != nil || user.String() != "user:alice" {
		t.Fatalf("ObjectFromID(TypeUser) = %q, %v", user.String(), err)
	}
}

// Pins that ObjectFromID rejects a malformed object type with the same
// character rules the id slot already gets, not just an empty-string check.
// Before this guard, an exported ObjectType containing ':', '#', or '*' would
// pass through unchecked and let a caller forge the rendered tuple's type
// prefix (e.g. ObjectType("stack:evil") + "abc" → "stack:evil:abc", parsed by
// OpenFGA as type "stack", id "evil:abc").
func TestObjectFromIDRejectsMalformedType(t *testing.T) {
	if _, err := ObjectFromID(ObjectType(""), "abc"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf(`ObjectFromID("", "abc") error = %v, want ErrInvalidInput`, err)
	}
	unsafeTypes := []ObjectType{"stack:evil", "stack#member", "stack*"}
	for _, objectType := range unsafeTypes {
		if _, err := ObjectFromID(objectType, "abc"); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("ObjectFromID(%q, \"abc\") error = %v, want ErrInvalidInput", objectType, err)
		}
	}
}

// Pins the character rules. Each rejected input changes a tuple's meaning
// rather than merely being malformed, which is why this is the highest-value
// test in the package: sub is the one identifier tflive does not originate.
func TestCanonicalIdentifiersRejectTupleSyntax(t *testing.T) {
	// Each of these changes the meaning of a tuple rather than merely being
	// malformed: ':' forges the type prefix, '#' makes a userset reference,
	// '*' makes the typed wildcard that grants every user.
	unsafe := []string{"", " ", "user:already", "stack:already", "bad\nsubject", "alice#member", "*", "al*ce", "a#b"}
	for _, input := range unsafe {
		if _, err := ObjectFromID(TypeUser, input); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("ObjectFromID(TypeUser, %q) error = %v, want ErrInvalidInput", input, err)
		}
		if _, err := ObjectFromID(TypeStack, input); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("ObjectFromID(TypeStack, %q) error = %v, want ErrInvalidInput", input, err)
		}
	}
}

// Guards the identifier tightening against over-rejecting: real Keycloak,
// Okta, and UUID subjects must keep working.
func TestCanonicalIdentifiersAcceptOrdinarySubjects(t *testing.T) {
	// UUID and Okta-style subs must keep working.
	for _, input := range []string{"kc-sub-123", "00u1b2c3d4e5", "6f7a8b9c-1d2e-3f40-5a6b-7c8d9e0f1a2b"} {
		if _, err := ObjectFromID(TypeUser, input); err != nil {
			t.Fatalf("ObjectFromID(TypeUser, %q) error = %v", input, err)
		}
	}
}

// Pins that Subject wraps a user Object rather than being its own encoding.
func TestSubjectFromOIDCSubIsAUserObject(t *testing.T) {
	subject, err := SubjectFromOIDCSub("kc-sub-123")
	if err != nil || subject.String() != "user:kc-sub-123" {
		t.Fatalf("SubjectFromOIDCSub() = %q, %v", subject.String(), err)
	}
	if subject.Type() != TypeUser {
		t.Fatalf("Type() = %q, want %q", subject.Type(), TypeUser)
	}
}

// Pins that a resource type cannot occupy the tuple's user slot. This is the
// constraint Subject exists to carry: while Subject wrapped Object, Valid()
// delegated to Object.Valid() and a stack passed as an actor. It cannot be
// reached through SubjectFromOIDCSub, so it is built by literal here on
// purpose -- the point is that the type refuses it however it was assembled.
func TestSubjectRejectsANonActorType(t *testing.T) {
	stackAsSubject := Subject{identifier: identifier{objectType: TypeStack, id: "abc"}}
	if stackAsSubject.Valid() {
		t.Fatal("a stack must not validate as a subject")
	}
	// The same identifier is a perfectly good object; only the slot differs.
	if !(Object{identifier: identifier{objectType: TypeStack, id: "abc"}}).Valid() {
		t.Fatal("stack:abc must still be a valid object")
	}
}

// The platform singleton is the one object every global capability is checked
// against, so a drifted id would not fail loudly -- it would answer every
// global question against an empty object. This pins both slots it occupies.
func TestPlatformSingletonIsFixedAndMayAct(t *testing.T) {
	if Platform.String() != "platform:tflive" {
		t.Fatalf("Platform = %q, want platform:tflive", Platform.String())
	}
	if !Platform.Valid() {
		t.Fatal("the platform singleton must be a valid object")
	}
	// The parent edge puts the platform in the user slot, so unlike a stack it
	// must validate as a subject.
	if PlatformSubject.String() != "platform:tflive" {
		t.Fatalf("PlatformSubject = %q, want platform:tflive", PlatformSubject.String())
	}
	if !PlatformSubject.Valid() {
		t.Fatal("the platform singleton must be able to occupy the user slot")
	}
}

// Pins that the bare identifier survives construction, so callers recover the
// raw OIDC "sub" through ID() rather than by stripping a prefix off String().
func TestIdentifierKeepsItsBareID(t *testing.T) {
	subject, err := SubjectFromOIDCSub("kc-sub-123")
	if err != nil {
		t.Fatal(err)
	}
	if subject.ID() != "kc-sub-123" {
		t.Fatalf("Subject.ID() = %q, want %q", subject.ID(), "kc-sub-123")
	}
	object, err := ObjectFromID(TypeStack, "abc123")
	if err != nil {
		t.Fatal(err)
	}
	if object.ID() != "abc123" {
		t.Fatalf("Object.ID() = %q, want %q", object.ID(), "abc123")
	}
	if (Object{}).ID() != "" {
		t.Fatal("zero Object must have an empty ID")
	}
}

// Pins that a struct literal cannot bypass SubjectFromOIDCSub's validation.
func TestZeroSubjectIsInvalid(t *testing.T) {
	if (Subject{}).Valid() {
		t.Fatal("zero Subject must not validate")
	}
}

// Pins that SubjectFromOIDCSub rejects the same tuple-corrupting inputs as
// every other identifier constructor: a userset reference, the typed
// wildcard, and an already-prefixed value. This is the subject constructor
// specifically, not merely coverage inferred from the shared code path.
func TestSubjectFromOIDCSubRejectsTupleSyntax(t *testing.T) {
	for _, sub := range []string{"", " ", "user:already", "alice#member", "*", "al*ce", "bad\nsubject"} {
		if _, err := SubjectFromOIDCSub(sub); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("SubjectFromOIDCSub(%q) error = %v, want ErrInvalidInput", sub, err)
		}
	}
}

// The test that proves this refactor achieved its purpose rather than renaming
// things: a Grant cannot hold a structural edge or a derived relation, however
// the Relation reached it.
func TestNewGrantRequiresAGrantableRelation(t *testing.T) {
	subject, err := SubjectFromOIDCSub("kc-sub-123")
	if err != nil {
		t.Fatal(err)
	}
	object, err := ObjectFromID(TypeStack, "stack-123")
	if err != nil {
		t.Fatal(err)
	}

	grant, err := NewGrant(subject, object, RelationOwner)
	if err != nil {
		t.Fatalf("NewGrant() error = %v", err)
	}
	if grant.Relation() != RelationOwner || grant.Object() != object || grant.Subject() != subject {
		t.Fatalf("NewGrant() = %#v", grant)
	}

	// A Grant must never be able to hold a structural edge or a derived
	// relation, however the Relation reached it.
	parent, err := NewRelation("parent")
	if err != nil {
		t.Fatal(err)
	}
	for _, relation := range []Relation{parent, RelationCanView, {}} {
		if _, err := NewGrant(subject, object, relation); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("NewGrant(%q) error = %v, want ErrInvalidInput", relation.String(), err)
		}
	}

	if _, err := NewGrant(Subject{}, object, RelationOwner); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("NewGrant with zero subject error = %v", err)
	}
	if _, err := NewGrant(subject, Object{}, RelationOwner); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("NewGrant with zero object error = %v", err)
	}
}

// Pins that checking admits relations writing refuses — the write/check
// asymmetry the two constructors exist for.
func TestCheckRequestValidity(t *testing.T) {
	subject, err := SubjectFromOIDCSub("kc-sub-123")
	if err != nil {
		t.Fatal(err)
	}
	object, err := ObjectFromID(TypeStack, "stack-123")
	if err != nil {
		t.Fatal(err)
	}
	if !(CheckRequest{Subject: subject, Relation: RelationCanView, Object: object}).Valid() {
		t.Fatal("well-formed check request must validate")
	}
	if (CheckRequest{}).Valid() {
		t.Fatal("zero check request must not validate")
	}
	if (CheckRequest{Subject: subject, Object: object}).Valid() {
		t.Fatal("check request without a relation must not validate")
	}
}

// Pins that both list requests refuse to cross the adapter boundary half-built.
func TestListRequestsValidity(t *testing.T) {
	subject, err := SubjectFromOIDCSub("kc-sub-123")
	if err != nil {
		t.Fatal(err)
	}
	object, err := ObjectFromID(TypeStack, "stack-123")
	if err != nil {
		t.Fatal(err)
	}
	if !(ListGrantsRequest{Object: object}).Valid() {
		t.Fatal("well-formed grants request must validate")
	}
	if (ListGrantsRequest{}).Valid() {
		t.Fatal("zero grants request must not validate")
	}
	if !(ListSubjectGrantsRequest{Subject: subject, Object: object}).Valid() {
		t.Fatal("well-formed subject grants request must validate")
	}
	if (ListSubjectGrantsRequest{}).Valid() {
		t.Fatal("zero subject grants request must not validate")
	}
}

// Pins that Mutation still requires validated grants, independent of what
// makes a Grant valid.
func TestMutationRequiresValidatedGrants(t *testing.T) {
	subject, err := SubjectFromOIDCSub("kc-sub-123")
	if err != nil {
		t.Fatalf("SubjectFromOIDCSub() error = %v", err)
	}
	object, err := ObjectFromID(TypeStack, "stack-123")
	if err != nil {
		t.Fatalf("ObjectFromID() error = %v", err)
	}
	grant, err := NewGrant(subject, object, RelationOwner)
	if err != nil {
		t.Fatalf("NewGrant() error = %v", err)
	}
	mutation, err := NewMutation([]Grant{grant}, true)
	if err != nil || !mutation.Valid() || !mutation.Confirm() {
		t.Fatalf("NewMutation() = %#v, %v", mutation, err)
	}
	if _, err := NewMutation([]Grant{{}}, false); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("NewMutation() with zero grant error = %v", err)
	}
}

// Pins that a batch request is valid only when every check in it is, on top
// of the single-request cases TestCheckRequestValidity already covers.
func TestBatchCheckRequestValidity(t *testing.T) {
	subject, err := SubjectFromOIDCSub("kc-sub-123")
	if err != nil {
		t.Fatalf("SubjectFromOIDCSub() error = %v", err)
	}
	object, err := ObjectFromID(TypeStack, "stack-123")
	if err != nil {
		t.Fatalf("ObjectFromID() error = %v", err)
	}
	check := CheckRequest{Subject: subject, Relation: RelationCanView, Object: object}
	if !(BatchCheckRequest{Checks: []CheckRequest{check}}).Valid() {
		t.Fatal("validated batch request must be valid")
	}
	if (BatchCheckRequest{}).Valid() {
		t.Fatal("zero batch request must be invalid")
	}
	if (BatchCheckRequest{Checks: []CheckRequest{check, {}}}).Valid() {
		t.Fatal("batch request with one invalid check must be invalid")
	}
}

func TestHTTPStatusMapsAuthorizationDependencyFailures(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		code string
	}{
		{name: "timeout", err: ErrTimeout, code: "authorization_unavailable"},
		{name: "unavailable", err: ErrUnavailable, code: "authorization_unavailable"},
		{name: "malformed", err: ErrMalformedResponse, code: "authorization_unavailable"},
		{name: "unconfirmed write", err: ErrWriteUnconfirmed, code: "authorization_write_unconfirmed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			status, code, ok := HTTPStatus(fmt.Errorf("check: %w", test.err))
			if !ok || status != http.StatusServiceUnavailable || code != test.code {
				t.Fatalf("HTTPStatus() = %d, %q, %t", status, code, ok)
			}
		})
	}
	if _, _, ok := HTTPStatus(ErrInvalidInput); ok {
		t.Fatal("invalid input must not map to an availability response")
	}
}
