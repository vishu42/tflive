package authz

import (
	"errors"
	"testing"
)

// The allowlist exists for exactly one tuple OpenFGA itself accepts:
// {platform:tflive, parent, stack:X}. A grant endpoint must never write it.
func TestGrantRelationRefusesNonGrantableRelations(t *testing.T) {
	for _, name := range []string{"parent", "root", "can_view", "can_operate", "can_approve", "can_manage_access", "nonsense"} {
		if _, err := GrantRelation(name); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("GrantRelation(%q) error = %v, want ErrInvalidInput", name, err)
		}
	}
}

// Pins the allowlist's contents, including its size: an added key (a
// premature "admin", say) is invisible to a test that only checks the four
// known names work, so this also asserts set equality against
// grantableRelations directly. #141 extends this with admin and
// stack_creator, and must update the "want" set below when it does.
func TestGrantRelationAcceptsTheFourStackRoles(t *testing.T) {
	names := []string{"owner", "operator", "approver", "viewer"}
	for _, name := range names {
		relation, err := GrantRelation(name)
		if err != nil {
			t.Fatalf("GrantRelation(%q) error = %v", name, err)
		}
		if !relation.Grantable() {
			t.Fatalf("GrantRelation(%q) produced a non-grantable relation", name)
		}
	}

	want := make(map[string]bool, len(names))
	for _, name := range names {
		want[name] = true
	}
	if len(grantableRelations) != len(want) {
		t.Fatalf("grantableRelations = %v, want exactly %v", grantableRelations, want)
	}
	for name := range want {
		if !grantableRelations[name] {
			t.Fatalf("grantableRelations is missing %q: %v", name, grantableRelations)
		}
	}
}

// Checking is a different question from writing, and OpenFGA answers it for any
// relation. NewRelation must therefore admit what GrantRelation refuses.
func TestNewRelationAdmitsRelationsThatCannotBeGranted(t *testing.T) {
	for _, name := range []string{"can_view", "parent", "admin"} {
		relation, err := NewRelation(name)
		if err != nil {
			t.Fatalf("NewRelation(%q) error = %v", name, err)
		}
		if relation.String() != name {
			t.Fatalf("NewRelation(%q).String() = %q", name, relation.String())
		}
		if relation.Grantable() {
			t.Fatalf("NewRelation(%q) must not be grantable", name)
		}
	}
}

// Pins that relation names obey the same character rules as identifiers.
func TestNewRelationRejectsNamesThatCorruptATuple(t *testing.T) {
	for _, name := range []string{"", " ", "can view", "can:view", "can#view", "*", "bad\nrelation"} {
		if _, err := NewRelation(name); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("NewRelation(%q) error = %v, want ErrInvalidInput", name, err)
		}
	}
}

// Pins that a zero Relation cannot be smuggled into a write.
func TestZeroRelationIsInvalidAndNotGrantable(t *testing.T) {
	if (Relation{}).Valid() {
		t.Fatal("zero Relation must not validate")
	}
	if (Relation{}).Grantable() {
		t.Fatal("zero Relation must not be grantable")
	}
}

// Pins that the package-level table is wired to the right constructors — a
// permission accidentally built with mustGrantRelation would grant write access.
func TestNamedRelationValuesMatchTheirBucket(t *testing.T) {
	for _, relation := range []Relation{RelationOwner, RelationOperator, RelationApprover, RelationViewer} {
		if !relation.Grantable() {
			t.Fatalf("named role %q must be grantable", relation.String())
		}
	}
	for _, relation := range []Relation{RelationCanView, RelationCanOperate, RelationCanApprove, RelationCanManageAccess} {
		if relation.Grantable() {
			t.Fatalf("named permission %q must not be grantable", relation.String())
		}
		if !relation.Valid() {
			t.Fatalf("named permission %q must be valid", relation.String())
		}
	}
}
