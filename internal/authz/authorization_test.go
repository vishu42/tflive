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

// Guards the tightening in Task 2 against over-rejecting: real Keycloak, Okta,
// and UUID subjects must keep working.
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

func TestGrantAndMutationRequireValidatedDirectRoles(t *testing.T) {
	subject, err := SubjectFromOIDCSub("kc-sub-123")
	if err != nil {
		t.Fatalf("SubjectFromOIDCSub() error = %v", err)
	}
	stack, err := ObjectFromID(TypeStack, "stack-123")
	if err != nil {
		t.Fatalf("ObjectFromID() error = %v", err)
	}
	role, err := GrantRelation("owner")
	if err != nil {
		t.Fatalf("GrantRelation() error = %v", err)
	}
	grant, err := NewGrant(subject, stack, role)
	if err != nil || !grant.Valid() {
		t.Fatalf("NewGrant() = %#v, %v", grant, err)
	}
	mutation, err := NewMutation([]Grant{grant}, true)
	if err != nil || !mutation.Valid() || !mutation.Confirm() {
		t.Fatalf("NewMutation() = %#v, %v", mutation, err)
	}
	if _, err := NewGrant(Subject{}, stack, role); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("NewGrant() with zero subject error = %v", err)
	}
	if _, err := NewMutation([]Grant{{}}, false); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("NewMutation() with zero grant error = %v", err)
	}
}

func TestRequestsValidateOpaqueValuesAtAdapterBoundary(t *testing.T) {
	subject, err := SubjectFromOIDCSub("kc-sub-123")
	if err != nil {
		t.Fatalf("SubjectFromOIDCSub() error = %v", err)
	}
	stack, err := ObjectFromID(TypeStack, "stack-123")
	if err != nil {
		t.Fatalf("ObjectFromID() error = %v", err)
	}
	check := CheckRequest{Subject: subject, Stack: stack, Permission: RelationCanView}
	if !check.Valid() {
		t.Fatal("validated check request must be valid")
	}
	if (CheckRequest{}).Valid() {
		t.Fatal("zero check request must be invalid")
	}
	if !(BatchCheckRequest{Checks: []CheckRequest{check}}).Valid() {
		t.Fatal("validated batch request must be valid")
	}
	if (BatchCheckRequest{}).Valid() {
		t.Fatal("zero batch request must be invalid")
	}
	if !(ListAccessibleStacksRequest{Subject: subject, Permission: RelationCanView}).Valid() {
		t.Fatal("validated accessible-stack request must be valid")
	}
	if (ListAccessibleStacksRequest{}).Valid() {
		t.Fatal("zero accessible-stack request must be invalid")
	}
	if !(ListGrantsRequest{Stack: stack}).Valid() {
		t.Fatal("validated grants request must be valid")
	}
	if (ListGrantsRequest{}).Valid() {
		t.Fatal("zero grants request must be invalid")
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
