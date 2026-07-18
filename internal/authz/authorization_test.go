package authz

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
)

func TestCanonicalIdentifiers(t *testing.T) {
	subject, err := SubjectFromKeycloakSub("kc-sub-123")
	if err != nil || subject.String() != "user:kc-sub-123" {
		t.Fatalf("SubjectFromKeycloakSub() = %q, %v", subject, err)
	}

	stack, err := StackFromID("stack-123")
	if err != nil || stack.String() != "stack:stack-123" {
		t.Fatalf("StackFromID() = %q, %v", stack, err)
	}
}

func TestCanonicalIdentifiersRejectUnsafeAndPrefixedValues(t *testing.T) {
	for _, input := range []string{"", " ", "user:already", "stack:already", "bad\nsubject"} {
		if _, err := SubjectFromKeycloakSub(input); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("subject %q error = %v", input, err)
		}
		if _, err := StackFromID(input); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("stack %q error = %v", input, err)
		}
	}
}

func TestOnlyDirectRolesAndDerivedPermissionsAreValid(t *testing.T) {
	for _, role := range []Role{RoleOwner, RoleOperator, RoleApprover, RoleViewer} {
		if !role.Valid() {
			t.Fatalf("named direct role %q must validate", role.String())
		}
	}
	if !PermissionView.Valid() {
		t.Fatal("known permission must validate")
	}
	if _, err := RoleFromDirectRelation("can_view"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("derived permission as role error = %v", err)
	}
	if Permission("owner").Valid() {
		t.Fatal("roles and permissions must not overlap")
	}
}

func TestGrantAndMutationRequireValidatedDirectRoles(t *testing.T) {
	subject, err := SubjectFromKeycloakSub("kc-sub-123")
	if err != nil {
		t.Fatalf("SubjectFromKeycloakSub() error = %v", err)
	}
	stack, err := StackFromID("stack-123")
	if err != nil {
		t.Fatalf("StackFromID() error = %v", err)
	}
	role, err := RoleFromDirectRelation("owner")
	if err != nil {
		t.Fatalf("RoleFromDirectRelation() error = %v", err)
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
	subject, err := SubjectFromKeycloakSub("kc-sub-123")
	if err != nil {
		t.Fatalf("SubjectFromKeycloakSub() error = %v", err)
	}
	stack, err := StackFromID("stack-123")
	if err != nil {
		t.Fatalf("StackFromID() error = %v", err)
	}
	check := CheckRequest{Subject: subject, Stack: stack, Permission: PermissionView}
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
	if !(ListAccessibleStacksRequest{Subject: subject, Permission: PermissionView}).Valid() {
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
