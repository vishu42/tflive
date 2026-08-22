package authz

import "fmt"

// grantableRelations is the port's one genuine refusal — the only hazard
// OpenFGA does not reject on the write path.
//
// A structural edge such as {platform:tflive, parent, stack:X} is a perfectly
// legal tuple: `parent` declares [platform] as a direct type because
// platform-admin inheritance requires it. So the server cannot tell stack
// provisioning writing that edge from a grant endpoint writing it, and nothing
// but this list stops the latter.
//
// Derived relations such as can_view are absent too, but the server already
// refuses those: they declare no directly_related_user_types at all. They are
// excluded here for readability, not for safety.
//
// #141 adds "admin" and "stack_creator". It must NOT add "parent" or "root".
var grantableRelations = map[string]bool{
	"owner":    true,
	"operator": true,
	"approver": true,
	"viewer":   true,
}

// Relation is a validated relation name. Its value is intentionally opaque so
// callers cannot construct unchecked relations.
type Relation struct {
	value string
}

// NewRelation admits any well-formed relation name. Checking is always safe:
// OpenFGA evaluates whatever relation it is asked about and rejects one that
// does not exist on the object's type. Use this for CheckRequest.
//
//	NewRelation("can_view")  → Relation{"can_view"}, nil
//	NewRelation("owner")     → Relation{"owner"}, nil
//	NewRelation("parent")    → Relation{"parent"}, nil   (checkable, not grantable)
//	NewRelation("can view")  → Relation{}, ErrInvalidInput
//	NewRelation("")          → Relation{}, ErrInvalidInput
func NewRelation(name string) (Relation, error) {
	if !safeRelationName(name) {
		return Relation{}, fmt.Errorf("%w: invalid relation name", ErrInvalidInput)
	}
	return Relation{value: name}, nil
}

// GrantRelation admits only relations the grant API may write. It is the write
// path's counterpart to NewRelation: not a stricter version of the same rule,
// but the other endpoint's genuinely different rule.
//
//	GrantRelation("viewer")    → Relation{"viewer"}, nil
//	GrantRelation("owner")     → Relation{"owner"}, nil
//	GrantRelation("parent")    → Relation{}, ErrInvalidInput  (structural edge)
//	GrantRelation("can_view")  → Relation{}, ErrInvalidInput  (derived)
//	GrantRelation("nonsense")  → Relation{}, ErrInvalidInput
func GrantRelation(name string) (Relation, error) {
	relation, err := NewRelation(name)
	if err != nil {
		return Relation{}, err
	}
	if !relation.Grantable() {
		return Relation{}, fmt.Errorf("%w: relation %q may not be granted", ErrInvalidInput, name)
	}
	return relation, nil
}

// String renders the relation name for a provider adapter.
//
//	RelationOwner.String()    → "owner"
//	RelationCanView.String()  → "can_view"
//	Relation{}.String()       → ""
func (relation Relation) String() string {
	return relation.value
}

// Valid reports whether the relation is a well-formed name. It says nothing
// about whether the relation may be written — see Grantable.
//
//	RelationCanView.Valid()  → true
//	Relation{}.Valid()       → false
func (relation Relation) Valid() bool {
	return safeRelationName(relation.value)
}

// Grantable reports whether the grant API may write this relation.
//
//	RelationOwner.Grantable()    → true
//	RelationCanView.Grantable()  → false  (derived; OpenFGA refuses it too)
//	Relation{}.Grantable()       → false
//
// A "parent" relation built through NewRelation also reports false, which is
// the refusal this whole type exists for.
func (relation Relation) Grantable() bool {
	return grantableRelations[relation.value]
}

// safeRelationName rejects names that would corrupt a tuple's encoding, using
// the same character rules as canonicalIdentifier.
//
//	safeRelationName("can_view")  → true
//	safeRelationName("can#view")  → false
//	safeRelationName("")          → false
func safeRelationName(name string) bool {
	return safeTupleToken(name)
}

// Named relations. must* panics at package init, which can only be a
// programming error in the literal table above it.
var (
	RelationOwner    = mustGrantRelation("owner")
	RelationOperator = mustGrantRelation("operator")
	RelationApprover = mustGrantRelation("approver")
	RelationViewer   = mustGrantRelation("viewer")

	RelationCanView         = mustRelation("can_view")
	RelationCanOperate      = mustRelation("can_operate")
	RelationCanApprove      = mustRelation("can_approve")
	RelationCanManageAccess = mustRelation("can_manage_access")
)

// mustRelation is NewRelation for the package-level table below. It panics on
// error, which at init time can only be a typo three lines above it.
//
//	mustRelation("can_view")  → Relation{"can_view"}
//	mustRelation("can view")  → panics
func mustRelation(name string) Relation {
	relation, err := NewRelation(name)
	if err != nil {
		panic(err)
	}
	return relation
}

// mustGrantRelation is GrantRelation for the package-level table below.
//
//	mustGrantRelation("owner")   → Relation{"owner"}
//	mustGrantRelation("parent")  → panics (not in grantableRelations)
func mustGrantRelation(name string) Relation {
	relation, err := GrantRelation(name)
	if err != nil {
		panic(err)
	}
	return relation
}
