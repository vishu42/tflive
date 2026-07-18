package authz

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
)

func TestCanonicalIdentifiers(t *testing.T) {
	subject, err := SubjectFromKeycloakSub("kc-sub-123")
	if err != nil || subject != Subject("user:kc-sub-123") {
		t.Fatalf("SubjectFromKeycloakSub() = %q, %v", subject, err)
	}

	stack, err := StackFromID("stack-123")
	if err != nil || stack != Stack("stack:stack-123") {
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
	if !RoleOwner.Valid() || !PermissionView.Valid() {
		t.Fatal("known values must validate")
	}
	if Role("can_view").Valid() || Permission("owner").Valid() {
		t.Fatal("roles and permissions must not overlap")
	}
}

func TestHTTPStatusMapsOnlyAuthorizationDependencyFailures(t *testing.T) {
	status, code, ok := HTTPStatus(fmt.Errorf("check: %w", ErrUnavailable))
	if !ok || status != http.StatusServiceUnavailable || code != "authorization_unavailable" {
		t.Fatalf("HTTPStatus() = %d, %q, %t", status, code, ok)
	}
	if _, _, ok := HTTPStatus(ErrInvalidInput); ok {
		t.Fatal("invalid input must not map to an availability response")
	}
}
