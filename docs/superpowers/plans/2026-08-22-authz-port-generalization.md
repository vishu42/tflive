# Generalize the authz port to (Subject, Relation, Object) — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the stack-shaped `internal/authz` port with OpenFGA's three-slot tuple so `platform`, `template`, `credential_set`, and `github_integration` can be named through it, unblocking #141–#145.

**Architecture:** `Subject`, `Relation`, `Object` mirror OpenFGA's wire tuple. Validation that currently lives in type *names* (`Stack`, `Role`, `Permission`) moves into constructors. The port keeps exactly one refusal of its own — a nine-line allowlist of grantable relation names — because OpenFGA already rejects every other hazard on the write path, against the model itself. The adapter becomes close to a passthrough.

**Tech Stack:** Go 1.25, standard library `testing`, `net/http/httptest` for adapter tests. No new dependencies.

> **Paths moved after this plan was executed.** `internal/openfga` was split so
> the vendor package imports nothing from tflive. The step-by-step instructions
> below name the files as they were at the time and are left unedited, being a
> record of work already done. Current locations:
>
> | This plan says | Now |
> |---|---|
> | `internal/openfga/authorization_adapter.go` | `internal/authorizer/adapter.go` |
> | `internal/openfga/authorization_adapter_test.go` | `internal/authorizer/adapter_test.go` |
>
> The OpenFGA client kept `internal/openfga` and gained typed relationship
> methods (`Check`, `BatchCheck`, `Read`, `Write`); the adapter that implements
> `authz.Authorizer` moved out to `internal/authorizer`.

**Spec:** `docs/superpowers/specs/2026-08-22-authz-port-generalization-design.md`
(Background reasoning, including the reversals: `docs/superpowers/specs/2026-08-22-authz-port-generalization-considerations.md`)

## Global Constraints

- **This is a rename-and-reshape with zero behaviour change.** Every check that passes before must pass after. The only intentional behaviour changes are the tightened identifier validation (Task 2) and `BatchCheck` chunking (Task 8).
- **tflive is pre-production.** No backward compatibility, no deprecation shims, no aliases for old names. Delete and replace.
- **The queue key format `stack:<id>/user:<sub>` must not change.** It is built from the canonical formatters (`authz.StackGrantSpec`, `internal/authz/grant_handler.go`), and those produce identical strings after this change. Verified by a test in Task 6.
- **`go build ./...` and `go test ./...` must pass at every commit.** Because this changes an interface, intermediate states do not compile — Tasks 3–7 are therefore ordered so each ends compiling. Do not commit a broken build.
- **Naming, exactly:** `Stack` → `Object`; `StackFromID` → `ObjectFromID`; `SubjectFromKeycloakSub` → `SubjectFromOIDCSub`; `Role` and `Permission` → `Relation`; `RoleOwner` → `RelationOwner`; `PermissionView` → `RelationCanView` (and the three siblings); `Grant.Role()` → `Grant.Relation()`; `ListGrantsRequest.Stack` → `.Object`; `ListSubjectGrantsRequest.Stack` → `.Object`; `CheckRequest.Stack`/`.Permission` → `.Object`/`.Relation`.
- **Baseline before starting:** `go build ./...` clean, `go test ./internal/authz/... ./internal/openfga/...` = 121 passing.
- **Every function gets a doc comment with worked examples**, in Go's indented-block form so `go doc` renders them:

  ```go
  // GrantRelation admits only relations the grant API may write.
  //
  //	GrantRelation("viewer")  → Relation{"viewer"}, nil
  //	GrantRelation("parent")  → Relation{}, ErrInvalidInput
  ```

  Show at least one accepted and one rejected input for anything that validates —
  for this port the rejections *are* the documentation. This is a deliberate
  departure from the surrounding code, which uses bare one-line doc comments;
  apply it to functions this plan creates or rewrites, not to untouched code.
  Test *functions* take no inputs, so they get a comment stating which behaviour
  they pin and why — not a worked example. Test *helpers* get examples like any
  other function.

---

## File Structure

| File | Responsibility after this change |
|---|---|
| `internal/authz/authorization.go` | The port: `ObjectType`, `Object`, `Subject`, `Relation`, `Grant`, `Mutation`, requests, `Authorizer`. Modified throughout. |
| `internal/authz/relations.go` | **New.** The grantable allowlist, the `Relation` constructors, and the named relation values. Split out because it is the one piece carrying policy rather than shape, and it is what #141 edits. |
| `internal/authz/authorization_test.go` | Port tests. Rewritten. |
| `internal/authz/relations_test.go` | **New.** Allowlist and constructor tests — the tests that prove the refactor achieved its purpose. |
| `internal/authz/grant_handler.go` | `grantIdentity.stack` → `.object`; key format unchanged. |
| `internal/openfga/authorization_adapter.go` | Adapter. `ListAccessibleStacks` deleted; `BatchCheck` chunks; tuple builders read new field names. |
| `internal/openfga/authorization_adapter_test.go` | Adapter tests. Helpers rewritten, which carries most of the 724 lines mechanically. |
| `internal/app/authorization.go`, `service.go`, `stack_provisioning.go`, `grant_stack_owner_handler.go`, `mark_stack_ready_handler.go` | Call sites. Mechanical. |
| `internal/api/server.go` + test doubles in `internal/api`, `internal/app`, `cmd/api`, `cmd/worker` | Call sites and six `ListAccessibleStacks` stubs to delete. |

---

## Task 1: `ObjectType` and `Object`

**Files:**
- Modify: `internal/authz/authorization.go:37-51` (the `Stack` type), `:117-124` (`StackFromID`)
- Test: `internal/authz/authorization_test.go`

**Interfaces:**
- Consumes: nothing (first task)
- Produces: `type ObjectType string`; `const TypeUser, TypeStack ObjectType`; `type Object struct{...}`; `func ObjectFromID(objectType ObjectType, id string) (Object, error)`; `func (Object) Type() ObjectType`; `func (Object) String() string`; `func (Object) Valid() bool`

**Note:** This task leaves the tree not compiling — `Stack` is gone but its callers remain. That is expected and is why Steps 5–6 build only this one package. The tree compiles again at the end of Task 7. Do not attempt `go build ./...` until then.

- [ ] **Step 1: Write the failing test**

Add to `internal/authz/authorization_test.go`, replacing `TestCanonicalIdentifiers`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/authz/ -run TestObject -v`
Expected: FAIL — `undefined: ObjectFromID`, `undefined: TypeStack`

- [ ] **Step 3: Write minimal implementation**

In `internal/authz/authorization.go`, delete the `Stack` type (lines 37–51) and `StackFromID` (lines 117–124), and add:

```go
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
// already-prefixed one.
//
//	ObjectFromID(TypeStack, "abc123")      → Object{"stack:abc123"}, nil
//	ObjectFromID(TypeUser, "00u1b2c3")     → Object{"user:00u1b2c3"}, nil
//	ObjectFromID(TypeStack, "stack:abc")   → Object{}, ErrInvalidInput  (already prefixed)
//	ObjectFromID(TypeUser, "al*ce")        → Object{}, ErrInvalidInput  (wildcard char)
//	ObjectFromID(TypeUser, "")             → Object{}, ErrInvalidInput
func ObjectFromID(objectType ObjectType, id string) (Object, error) {
	if objectType == "" {
		return Object{}, fmt.Errorf("%w: object type is required", ErrInvalidInput)
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/authz/ -run TestObject -v`
Expected: PASS (other tests in the package still fail to compile — that is handled in Task 3)

- [ ] **Step 5: Commit**

```bash
git add internal/authz/authorization.go internal/authz/authorization_test.go
git commit -m "refactor(authz): replace Stack with a typed Object"
```

---

## Task 2: Tighten identifier validation

**Files:**
- Modify: `internal/authz/authorization.go` (`canonicalIdentifier`, currently lines 126-131)
- Test: `internal/authz/authorization_test.go`

**Interfaces:**
- Consumes: `ObjectFromID`, `TypeUser`, `TypeStack` from Task 1
- Produces: no new symbols; `canonicalIdentifier` additionally rejects `#` and `*`

**Why:** `sub` comes from the customer's IdP and is the one identifier tflive does not originate. `#` turns `user:<sub>` into a userset reference and `*` turns it into the everyone-wildcard. OpenFGA fails closed on both today, but only because our model declares `[user]` rather than `[user:*]` and `user` has no relations — both model facts that can change.

- [ ] **Step 1: Write the failing test**

Add to `internal/authz/authorization_test.go`:

```go
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
```

Delete the now-superseded `TestCanonicalIdentifiersRejectUnsafeAndPrefixedValues`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/authz/ -run TestCanonicalIdentifiersRejectTupleSyntax -v`
Expected: FAIL — `ObjectFromID(TypeUser, "alice#member") error = <nil>, want ErrInvalidInput`

- [ ] **Step 3: Write minimal implementation**

Replace `canonicalIdentifier` in `internal/authz/authorization.go`:

```go
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
	if value == "" ||
		strings.ContainsAny(value, ":#*") ||
		strings.IndexFunc(value, unicode.IsSpace) >= 0 ||
		strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return "", fmt.Errorf("%w: invalid %s identifier", ErrInvalidInput, kind)
	}
	return kind + ":" + value, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/authz/ -run TestCanonicalIdentifiers -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/authz/authorization.go internal/authz/authorization_test.go
git commit -m "fix(authz): reject # and * in canonical identifiers

Both change a tuple's meaning rather than merely being malformed: '#'
makes a userset reference, '*' makes the typed wildcard that matches
every user. OpenFGA fails closed on both today, but only because our
model declares [user] rather than [user:*] and user has no relations."
```

---

## Task 3: `Relation`, the grantable allowlist, and `Subject`

**Files:**
- Create: `internal/authz/relations.go`
- Create: `internal/authz/relations_test.go`
- Modify: `internal/authz/authorization.go` — delete `Role` (lines 53-85) and `Permission` (87-107); replace `Subject` (23-35) and `SubjectFromKeycloakSub` (109-116)
- Modify: `internal/authz/authorization_test.go` — delete `TestOnlyDirectRolesAndDerivedPermissionsAreValid`

**Interfaces:**
- Consumes: `Object`, `ObjectFromID`, `ObjectType`, `TypeUser` from Task 1
- Produces: `type Relation struct{...}`; `func NewRelation(name string) (Relation, error)`; `func GrantRelation(name string) (Relation, error)`; `func (Relation) String() string`; `func (Relation) Valid() bool`; `func (Relation) Grantable() bool`; vars `RelationOwner`, `RelationOperator`, `RelationApprover`, `RelationViewer`, `RelationCanView`, `RelationCanOperate`, `RelationCanApprove`, `RelationCanManageAccess`; `type Subject struct{...}`; `func SubjectFromOIDCSub(sub string) (Subject, error)`; `func (Subject) Type() ObjectType`; `func (Subject) String() string`; `func (Subject) Valid() bool`

- [ ] **Step 1: Write the failing test**

Create `internal/authz/relations_test.go`:

```go
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

// Pins the allowlist's contents. #141 extends this with admin and stack_creator.
func TestGrantRelationAcceptsTheFourStackRoles(t *testing.T) {
	for _, name := range []string{"owner", "operator", "approver", "viewer"} {
		relation, err := GrantRelation(name)
		if err != nil {
			t.Fatalf("GrantRelation(%q) error = %v", name, err)
		}
		if !relation.Grantable() {
			t.Fatalf("GrantRelation(%q) produced a non-grantable relation", name)
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
```

Add to `internal/authz/authorization_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/authz/ -run 'TestGrantRelation|TestNewRelation|TestSubjectFromOIDC' -v`
Expected: FAIL — `undefined: GrantRelation`, `undefined: NewRelation`, `undefined: SubjectFromOIDCSub`

- [ ] **Step 3: Write minimal implementation**

Create `internal/authz/relations.go`:

```go
package authz

import (
	"fmt"
	"strings"
	"unicode"
)

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
	if name == "" || strings.ContainsAny(name, ":#*") {
		return false
	}
	if strings.IndexFunc(name, unicode.IsSpace) >= 0 || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return false
	}
	return true
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
```

In `internal/authz/authorization.go`, delete the `Role` and `Permission` blocks entirely, and replace the `Subject` block and `SubjectFromKeycloakSub` with:

```go
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

// Type returns the subject's object type. Always TypeUser today; #141 adds
// platform subjects for the parent edge.
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

// Valid reports whether the subject is a canonical, validated authorization ID.
//
//	SubjectFromOIDCSub("alice") then .Valid()  → true
//	Subject{}.Valid()                          → false
func (subject Subject) Valid() bool {
	return subject.object.Valid()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/authz/ -run 'TestGrantRelation|TestNewRelation|TestSubject|TestNamedRelation|TestZeroRelation' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/authz/relations.go internal/authz/relations_test.go internal/authz/authorization.go internal/authz/authorization_test.go
git commit -m "refactor(authz): collapse Role and Permission into Relation

The Role/Permission split duplicated a check OpenFGA makes natively:
a derived relation declares no directly_related_user_types, so the
server refuses a write to it against the model itself. What the server
cannot refuse is a structural edge like {platform:tflive, parent,
stack:X} — parent must be directly writable for platform-admin
inheritance to exist. The grantable allowlist guards exactly that."
```

---

## Task 4: Requests, `Grant`, `Mutation`, and the `Authorizer` interface

**Files:**
- Modify: `internal/authz/authorization.go` — `CheckRequest`, `ListAccessibleStacks*` (delete), `ListGrantsRequest`, `Grant`, `ListSubjectGrantsRequest`, `Authorizer`
- Modify: `internal/authz/authorization_test.go`

**Interfaces:**
- Consumes: `Object`, `Subject`, `Relation`, `GrantRelation` from Tasks 1 and 3
- Produces: `CheckRequest{Subject, Relation, Object}`; `ListGrantsRequest{Object}`; `ListSubjectGrantsRequest{Subject, Object}`; `func NewGrant(Subject, Object, Relation) (Grant, error)`; `func (Grant) Subject() Subject`; `func (Grant) Object() Object`; `func (Grant) Relation() Relation`; `Authorizer` without `ListAccessibleStacks`

- [ ] **Step 1: Write the failing test**

Replace `TestGrantAndMutationRequireValidatedDirectRoles` and the `ListAccessibleStacksRequest` assertions in `internal/authz/authorization_test.go` with:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/authz/ -run 'TestNewGrant|TestCheckRequest|TestListRequests' -v`
Expected: FAIL — `unknown field Relation in struct literal of type CheckRequest`

- [ ] **Step 3: Write minimal implementation**

In `internal/authz/authorization.go`:

Replace `CheckRequest` and its `Valid`:

```go
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
```

Delete `ListAccessibleStacksRequest`, its `Valid`, and `ListAccessibleStacksResult` entirely.

Replace `ListGrantsRequest`, `ListSubjectGrantsRequest`, and `Grant`:

```go
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
```

Replace the `Authorizer` interface, dropping `ListAccessibleStacks`:

```go
// Authorizer is the provider-neutral authorization port.
type Authorizer interface {
	Check(context.Context, CheckRequest) (CheckResult, error)
	BatchCheck(context.Context, BatchCheckRequest) (BatchCheckResult, error)
	ListGrants(context.Context, ListGrantsRequest) (ListGrantsResult, error)
	WriteRelationships(context.Context, Mutation) error
	DeleteRelationships(context.Context, Mutation) error
}
```

`Mutation`, `NewMutation`, `CheckResult`, `BatchCheckRequest`, `BatchCheckResult`, `ListGrantsResult`, `SubjectGrantLister`, and `HTTPStatus` are unchanged.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/authz/ -v`
Expected: PASS — the whole `authz` package now compiles and passes

- [ ] **Step 5: Commit**

```bash
git add internal/authz/authorization.go internal/authz/authorization_test.go
git commit -m "refactor(authz): reshape requests to (Subject, Relation, Object)

Also deletes ListAccessibleStacks from the port. It has no production
caller and gains none at #141: app.listAccessibleStacks must join
against stacks rows for tenant scoping and stable ordering, which bare
object IDs capped at ListObjectsMaxResults=1000 behind a 3s deadline
cannot serve."
```

---

## Task 5: The grant handler

**Files:**
- Modify: `internal/authz/grant_handler.go`
- Modify: `internal/authz/grant_handler_test.go`

**Interfaces:**
- Consumes: everything from Tasks 1–4
- Produces: `grantIdentity{object, subject, relation, hasRelation}` (package-private); `StackGrantSpec` unchanged in behaviour

**Critical:** the queue key format `stack:<id>/user:<sub>` must not change. It is persisted, and changing it splits one resource across two keys, disabling the mutual exclusion the unique partial index provides.

- [ ] **Step 1: Write the failing test**

Add to `internal/authz/grant_handler_test.go`:

```go
// Pins a frozen contract. The key is persisted, so a format change splits one
// resource across two keys and disables the queue's mutual exclusion.
func TestStackGrantKeyFormatIsUnchanged(t *testing.T) {
	// The key is persisted. Changing its format splits one resource across two
	// keys and disables the queue's mutual exclusion.
	key, err := stackGrantKey([]byte(`{"stack_id":"stack-123","subject":"kc-sub-456","role":"viewer"}`))
	if err != nil {
		t.Fatalf("stackGrantKey() error = %v", err)
	}
	if key != "stack:stack-123/user:kc-sub-456" {
		t.Fatalf("stackGrantKey() = %q, want %q", key, "stack:stack-123/user:kc-sub-456")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/authz/ -run TestStackGrantKeyFormatIsUnchanged -v`
Expected: FAIL to compile — the package's other files still reference `StackFromID` and `Role`

- [ ] **Step 3: Write minimal implementation**

In `internal/authz/grant_handler.go`, replace `grantIdentity` and `parseGrantPayload`:

```go
// grantIdentity is the parsed payload. Relation is a struct, not a string, so
// it is parsed once here and compared by value; hasRelation distinguishes
// "grant this role" from "revoke everything".
type grantIdentity struct {
	object      Object
	subject     Subject
	relation    Relation
	hasRelation bool
}

// parseGrantPayload decodes a reconcile_stack_grant payload into validated
// identifiers. An empty Role means "revoke everything", which is why hasRelation
// is separate from relation.
//
//	{"stack_id":"s1","subject":"u1","role":"viewer"}
//	  → grantIdentity{stack:s1, user:u1, viewer, hasRelation:true}, nil
//	{"stack_id":"s1","subject":"u1","role":""}
//	  → grantIdentity{stack:s1, user:u1, <zero>, hasRelation:false}, nil
//	{"stack_id":"s1","subject":"u1","role":"parent"}
//	  → grantIdentity{}, error  (not grantable)
//	{"stack_id":"","subject":"u1","role":"viewer"}
//	  → grantIdentity{}, error
func parseGrantPayload(payload json.RawMessage) (grantIdentity, error) {
	var parsed GrantPayload
	if err := json.Unmarshal(payload, &parsed); err != nil {
		return grantIdentity{}, fmt.Errorf("decode stack grant payload: %w", err)
	}
	object, err := ObjectFromID(TypeStack, parsed.StackID)
	if err != nil {
		return grantIdentity{}, fmt.Errorf("parse stack grant stack: %w", err)
	}
	subject, err := SubjectFromOIDCSub(parsed.Subject)
	if err != nil {
		return grantIdentity{}, fmt.Errorf("parse stack grant subject: %w", err)
	}
	identity := grantIdentity{object: object, subject: subject}
	if parsed.Role != "" {
		relation, err := GrantRelation(parsed.Role)
		if err != nil {
			return grantIdentity{}, fmt.Errorf("parse stack grant role: %w", err)
		}
		identity.relation = relation
		identity.hasRelation = true
	}
	return identity, nil
}
```

Update `stackGrantKey` to use the new field — the produced string is identical:

```go
// stackGrantKey derives the queue's mutual-exclusion key. The format is a
// frozen contract: keys are persisted, and changing it splits one resource
// across two keys, disabling the unique partial index's exclusion.
//
//	{"stack_id":"s1","subject":"u1","role":"viewer"}  → "stack:s1/user:u1", nil
//	{"stack_id":"s1","subject":"u1","role":""}        → "stack:s1/user:u1", nil
//	{"stack_id":"bad:id","subject":"u1"}              → "", error
func stackGrantKey(payload json.RawMessage) (string, error) {
	identity, err := parseGrantPayload(payload)
	if err != nil {
		return "", err
	}
	return identity.object.String() + "/" + identity.subject.String(), nil
}
```

In `Deliver`, rename the three uses:

```go
	current, err := handler.relationships.ListSubjectGrants(ctx, ListSubjectGrantsRequest{
		Subject: identity.subject,
		Object:  identity.object,
	})
```

```go
	for _, grant := range current.Grants {
		if identity.hasRelation && grant.Relation() == identity.relation {
			satisfied = true
			continue
		}
		stale = append(stale, grant)
	}
```

```go
	if !identity.hasRelation || satisfied {
		return nil, nil
	}

	grant, err := NewGrant(identity.subject, identity.object, identity.relation)
```

Then update `internal/authz/grant_handler_test.go` mechanically: `StackFromID` → `ObjectFromID(TypeStack, …)`, `SubjectFromKeycloakSub` → `SubjectFromOIDCSub`, `RoleFromDirectRelation` → `GrantRelation`, `.Role()` → `.Relation()`, `ListSubjectGrantsRequest{… Stack: …}` → `{… Object: …}`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/authz/... -v 2>&1 | tail -20`
Expected: PASS, all tests in the package

- [ ] **Step 5: Commit**

```bash
git add internal/authz/grant_handler.go internal/authz/grant_handler_test.go
git commit -m "refactor(authz): move the grant handler onto Object and Relation

The queue key format stack:<id>/user:<sub> is unchanged and now pinned
by a test: it is persisted, and changing it would split one resource
across two keys."
```

---

## Task 6: The OpenFGA adapter

**Files:**
- Modify: `internal/openfga/authorization_adapter.go`
- Modify: `internal/openfga/authorization_adapter_test.go`

**Interfaces:**
- Consumes: the whole `authz` package as of Task 5
- Produces: `AuthorizationAdapter` implementing the new `authz.Authorizer`; `objectFromCanonical(objectType authz.ObjectType, raw string) (authz.Object, error)`; `grantFromReadTuple(key *tupleKey, requestedObject authz.Object) (authz.Grant, error)`

- [ ] **Step 1: Write the failing test**

In `internal/openfga/authorization_adapter_test.go`, replace the three helpers at lines 649-673. Every other test in the file then updates mechanically by following the compiler.

```go
// mustSubject builds a Subject or fails the test.
//
//	mustSubject(t, "alice")  → Subject{"user:alice"}
//	mustSubject(t, "a*b")    → t.Fatal
func mustSubject(t *testing.T, sub string) authz.Subject {
	t.Helper()
	subject, err := authz.SubjectFromOIDCSub(sub)
	if err != nil {
		t.Fatal(err)
	}
	return subject
}

// mustObject builds an Object of any type, or fails the test.
//
//	mustObject(t, authz.TypeStack, "one")  → Object{"stack:one"}
//	mustObject(t, authz.TypeUser, "alice") → Object{"user:alice"}
func mustObject(t *testing.T, objectType authz.ObjectType, id string) authz.Object {
	t.Helper()
	object, err := authz.ObjectFromID(objectType, id)
	if err != nil {
		t.Fatal(err)
	}
	return object
}

// mustStack is mustObject fixed to TypeStack, which most tests want.
//
//	mustStack(t, "one")  → Object{"stack:one"}
func mustStack(t *testing.T, id string) authz.Object {
	t.Helper()
	return mustObject(t, authz.TypeStack, id)
}

// mustGrant builds a Grant from bare IDs, or fails the test.
//
//	mustGrant(t, "alice", "one", authz.RelationOwner)
//	  → Grant{user:alice, stack:one, owner}
//	mustGrant(t, "alice", "one", authz.RelationCanView)  → t.Fatal (not grantable)
func mustGrant(t *testing.T, subject, stack string, relation authz.Relation) authz.Grant {
	t.Helper()
	grant, err := authz.NewGrant(mustSubject(t, subject), mustStack(t, stack), relation)
	if err != nil {
		t.Fatal(err)
	}
	return grant
}
```

Add a new test asserting the read path refuses a structural tuple — this is the case that starts mattering at #141:

```go
// Pins that a parent edge read back from OpenFGA is refused rather than
// surfacing as a grant. Inert until #141 writes the first one.
func TestListGrantsRejectsAStructuralTuple(t *testing.T) {
	t.Parallel()

	adapter := adapterForHandler(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// A parent edge is a legal tuple on the wire; it is not a grant.
		fmt.Fprint(w, `{"tuples":[{"key":{"user":"platform:tflive","relation":"parent","object":"stack:one"}}],"continuation_token":""}`)
	})

	_, err := adapter.ListGrants(context.Background(), authz.ListGrantsRequest{Object: mustStack(t, "one")})
	if !errors.Is(err, authz.ErrMalformedResponse) {
		t.Fatalf("ListGrants() error = %v, want ErrMalformedResponse", err)
	}
}
```

Delete `TestAuthorizationAdapterListAccessibleStacksWithConfiguredModel` (lines ~225-260) entirely.

**Helper note:** `adapterForHandler(t, http.HandlerFunc)` already exists in this file and wires an `httptest` server to a configured adapter with `t.Cleanup`. Use it — do not add a second helper. This test file is `package openfga` (not `openfga_test`), so unexported identifiers such as `tupleKey` are in scope.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/openfga/ 2>&1 | head -20`
Expected: FAIL to compile — `authz.Stack` undefined, `request.Permission` undefined, and `AuthorizationAdapter` no longer satisfies `authz.Authorizer`

- [ ] **Step 3: Write minimal implementation**

In `internal/openfga/authorization_adapter.go`:

Delete `ListAccessibleStacks` entirely (the whole method, roughly lines 99-146).

Replace the tuple builders at the bottom of the file:

```go
// tupleForGrant renders a Grant as OpenFGA's wire tuple.
//
//	NewGrant(user:alice, stack:abc, RelationOwner)
//	  → tupleKey{User: "user:alice", Relation: "owner", Object: "stack:abc"}
func tupleForGrant(grant authz.Grant) tupleKey {
	return tupleKey{User: grant.Subject().String(), Relation: grant.Relation().String(), Object: grant.Object().String()}
}

// objectFromCanonical parses a wire object string back into a validated Object,
// requiring it to round-trip exactly so a malformed response cannot smuggle a
// different identifier past validation.
//
//	objectFromCanonical(TypeStack, "stack:abc")   → Object{"stack:abc"}, nil
//	objectFromCanonical(TypeStack, "user:abc")    → Object{}, error  (wrong prefix)
//	objectFromCanonical(TypeStack, "stack:a:b")   → Object{}, error
//	objectFromCanonical(TypeStack, "abc")         → Object{}, error  (no prefix)
func objectFromCanonical(objectType authz.ObjectType, raw string) (authz.Object, error) {
	prefix := string(objectType) + ":"
	if !strings.HasPrefix(raw, prefix) {
		return authz.Object{}, fmt.Errorf("missing %s prefix", objectType)
	}
	object, err := authz.ObjectFromID(objectType, strings.TrimPrefix(raw, prefix))
	if err != nil || object.String() != raw {
		return authz.Object{}, fmt.Errorf("invalid %s object", objectType)
	}
	return object, nil
}

// grantFromReadTuple converts one tuple from a read response into a Grant,
// refusing anything that is not a grant on the object that was asked about.
//
//	{user:alice, owner, stack:abc}, stack:abc      → Grant{…}, nil
//	{platform:tflive, parent, stack:abc}, stack:abc → Grant{}, error  (not a grant)
//	{user:alice, can_view, stack:abc}, stack:abc   → Grant{}, error
//	{user:alice, owner, stack:other}, stack:abc    → Grant{}, error  (wrong object)
//	nil, stack:abc                                 → Grant{}, error
func grantFromReadTuple(key *tupleKey, requestedObject authz.Object) (authz.Grant, error) {
	const subjectPrefix = "user:"
	if key == nil || key.Object != requestedObject.String() || !strings.HasPrefix(key.User, subjectPrefix) {
		return authz.Grant{}, fmt.Errorf("invalid tuple key")
	}
	subject, err := authz.SubjectFromOIDCSub(strings.TrimPrefix(key.User, subjectPrefix))
	if err != nil || subject.String() != key.User {
		return authz.Grant{}, fmt.Errorf("invalid tuple subject")
	}
	// GrantRelation, not NewRelation: ListGrants answers "who has access", and
	// a structural edge such as parent is not access. Once #141 writes parent
	// edges, this is what keeps them out of the grant list.
	relation, err := authz.GrantRelation(key.Relation)
	if err != nil {
		return authz.Grant{}, fmt.Errorf("invalid tuple relation")
	}
	return authz.NewGrant(subject, requestedObject, relation)
}

// tuple renders a CheckRequest as OpenFGA's wire tuple.
//
//	CheckRequest{user:alice, can_view, stack:abc}
//	  → tupleKey{User: "user:alice", Relation: "can_view", Object: "stack:abc"}
func tuple(request authz.CheckRequest) tupleKey {
	return tupleKey{User: request.Subject.String(), Relation: request.Relation.String(), Object: request.Object.String()}
}
```

In `validMutation`, rename the tuple key construction:

```go
		key := tupleKey{User: grant.Subject().String(), Relation: grant.Relation().String(), Object: grant.Object().String()}
```

In `ListGrants`, replace `request.Stack` with `request.Object` (three occurrences: the `input.TupleKey.Object` assignment and the two `grantFromReadTuple` calls), and in the sort comparator replace `.Role()` with `.Relation()`:

```go
	sort.Slice(result.Grants, func(i, j int) bool {
		if result.Grants[i].Subject().String() != result.Grants[j].Subject().String() {
			return result.Grants[i].Subject().String() < result.Grants[j].Subject().String()
		}
		return result.Grants[i].Relation().String() < result.Grants[j].Relation().String()
	})
```

In `ListSubjectGrants`, replace `request.Stack` with `request.Object` (two occurrences).

In `ListGrants`'s dedupe map, rename the struct field:

```go
	seenGrants := map[struct{ subject, relation string }]struct{}{}
```
```go
			key := struct{ subject, relation string }{subject: grant.Subject().String(), relation: grant.Relation().String()}
```

Then follow the compiler through `authorization_adapter_test.go`, replacing `authz.Stack` → `authz.Object`, `mustStack` calls unchanged (the helper now returns `Object`), `authz.RoleOwner` → `authz.RelationOwner`, `authz.PermissionView` → `authz.RelationCanView`, `Stack:` → `Object:`, `Permission:` → `Relation:`, and `.Role()` → `.Relation()`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/authz/... ./internal/openfga/... 2>&1 | tail -10`
Expected: PASS in both packages

- [ ] **Step 5: Commit**

```bash
git add internal/openfga/authorization_adapter.go internal/openfga/authorization_adapter_test.go
git commit -m "refactor(openfga): move the adapter onto the generalized port

Also deletes ListAccessibleStacks. grantFromReadTuple now parses through
GrantRelation, so a stored parent edge read back by ListGrants is
rejected rather than surfacing as a grant — which starts mattering at
#141."
```

---

## Task 7: Application call sites and test doubles

**Files:**
- Modify: `internal/app/authorization.go`, `internal/app/service.go`, `internal/app/stack_provisioning.go`, `internal/app/grant_stack_owner_handler.go`, `internal/app/mark_stack_ready_handler.go`
- Modify: `internal/api/server.go`
- Modify (delete one stub each): `cmd/api/main_test.go:472-474`, `cmd/worker/main_test.go:502-504`, `internal/api/server_test.go:2582-2588`, `internal/app/authorization_test.go:279-281`, `internal/app/service_test.go:2034-2036`, `internal/app/stack_authorization_test.go:168-170`
- Modify: `internal/app/*_test.go`, `internal/api/server_test.go`, `cmd/*/main_test.go` — mechanical renames

**Interfaces:**
- Consumes: the whole `authz` package and the adapter as of Task 6
- Produces: a compiling, passing tree — this is the task that closes the window opened in Task 1

- [ ] **Step 1: Write the failing test**

No new test. This task is defined by the existing suite going green again; the compiler enumerates the work. Confirm the starting point:

Run: `go build ./... 2>&1 | head -30`
Expected: a list of errors in `internal/app`, `internal/api`, `cmd/api`, `cmd/worker`

- [ ] **Step 2: Apply the renames**

In `internal/app/authorization.go`:

```go
// authorizeStack answers "may the request's principal do relation to this
// stack?", returning denied rather than a generic error so a caller controls
// whether the client sees 403 or 404.
//
//	stack owned by alice, principal alice, RelationCanOperate  → nil
//	stack owned by alice, principal bob, RelationCanOperate    → denied
//	principal is platform-admin, any stack                     → nil (short-circuit)
//	stackID = "bad:id"                                         → denied
//	OpenFGA unreachable                                        → authz.ErrUnavailable
func authorizeStack(ctx context.Context, authorizer authz.Authorizer, stackID traits.StackID, relation authz.Relation, denied error) error {
	principal, err := requireAuthorizer(ctx, authorizer)
	if err != nil {
		return err
	}
	object, err := authz.ObjectFromID(authz.TypeStack, string(stackID))
	if errors.Is(err, authz.ErrInvalidInput) {
		return denied
	}
	if err != nil {
		return err
	}
	if isPlatformAdmin(principal) {
		return nil
	}
	subject, err := authz.SubjectFromOIDCSub(principal.Subject)
	if err != nil {
		return err
	}
	result, err := authorizer.Check(ctx, authz.CheckRequest{Subject: subject, Relation: relation, Object: object})
	if err != nil {
		return err
	}
	if !result.Allowed {
		return denied
	}
	return nil
}
```

In the same file, in `listAccessibleStacks`, `ResolveStackCapabilities`, `ResolveStacksCapabilities`, and `authorizedStackTemplate`:

- `authz.StackFromID(x)` → `authz.ObjectFromID(authz.TypeStack, x)`
- `authz.SubjectFromKeycloakSub(x)` → `authz.SubjectFromOIDCSub(x)`
- `authz.CheckRequest{Subject: s, Stack: st, Permission: p}` → `authz.CheckRequest{Subject: s, Relation: p, Object: st}`
- the `permissions := []authz.Permission{...}` slices → `relations := []authz.Relation{authz.RelationCanView, authz.RelationCanOperate, authz.RelationCanApprove, authz.RelationCanManageAccess}`
- the `permission authz.Permission` parameters on `authorizedStackTemplate` and `authorizedTemplateRun` → `relation authz.Relation`

In `internal/app/service.go` (~20 sites): `authz.PermissionView` → `authz.RelationCanView`, `authz.PermissionOperate` → `authz.RelationCanOperate`, `authz.PermissionApprove` → `authz.RelationCanApprove`, `authz.PermissionManageAccess` → `authz.RelationCanManageAccess`, `authz.RoleOwner` → `authz.RelationOwner`, `authz.RoleFromDirectRelation` → `authz.GrantRelation`, `authz.StackFromID` → `authz.ObjectFromID(authz.TypeStack, …)`, `authz.SubjectFromKeycloakSub` → `authz.SubjectFromOIDCSub`, `g.Role()` → `g.Relation()`, `authz.ListGrantsRequest{Stack: …}` → `{Object: …}`.

`listGrantsForStack`'s parameter type changes:

```go
// listGrantsForStack reads every direct role assignment on one stack. Structural
// tuples such as parent edges are not grants and never appear in the result.
//
//	stack:abc with alice=owner, bob=viewer
//	  → ListGrantsResult{Grants: [{user:alice, owner}, {user:bob, viewer}]}, nil
//	stack:abc with no grants  → ListGrantsResult{}, nil
func (service *Service) listGrantsForStack(ctx context.Context, object authz.Object) (authz.ListGrantsResult, error) {
	return service.Authorizer.ListGrants(ctx, authz.ListGrantsRequest{Object: object})
}
```

In `internal/app/stack_provisioning.go`, `grant_stack_owner_handler.go`, and `mark_stack_ready_handler.go`: the same `StackFromID` / `SubjectFromKeycloakSub` / `RoleOwner` / `.Role()` renames.

- [ ] **Step 3: Delete the six `ListAccessibleStacks` stubs**

Each test double implements it only to satisfy the old interface. Delete the method from each — nothing else in those files changes:

```bash
# The six files, for reference while editing:
#   cmd/api/main_test.go              (testAuthorizer)
#   cmd/worker/main_test.go           (recordingWorkerAuthorizer)
#   internal/api/server_test.go       (apiAuthorizer)
#   internal/app/authorization_test.go (permissionAuthorizer)
#   internal/app/service_test.go      (denyingAuthorizer)
#   internal/app/stack_authorization_test.go (recordingAuthorizer)
```

Note `internal/app/authorization_test.go`'s `permissionAuthorizer` (line 248) also has a `stacks []authz.Stack` field used only by that stub — delete the field, and any test setup that populates it, along with the method.

- [ ] **Step 4: Run the whole suite**

Run: `go build ./... && go test ./... 2>&1 | tail -20`
Expected: build clean, all tests pass

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "refactor(app,api): move call sites onto the generalized authz port

Also deletes the six ListAccessibleStacks stubs from test doubles."
```

---

## Task 8: Chunk `BatchCheck` at 50 (fixes #220)

**Files:**
- Modify: `internal/openfga/authorization_adapter.go` (`BatchCheck`, and the `maxConfirmationChecks` const block)
- Test: `internal/openfga/authorization_adapter_test.go`

**Interfaces:**
- Consumes: the adapter as of Task 7
- Produces: `BatchCheck` splitting requests into upstream calls of at most 50 checks, preserving caller ordering

**Why:** `ResolveStacksCapabilities` builds `4 × len(stacks)` checks. OpenFGA enforces `MaxChecksPerBatchCheck = 50` and returns 400, which `classify()` maps to `ErrMalformedResponse` → 503. `GET /v1/tenants/{id}/stacks` therefore fails today for any ordinary user with 13 or more accessible stacks.

- [ ] **Step 1: Write the failing test**

Add to `internal/openfga/authorization_adapter_test.go`:

```go
// Pins #220. Ordering is asserted as well as chunking, because a merge that
// loses the caller's positions silently returns another stack's answer.
func TestBatchCheckChunksAtFiftyAndPreservesOrder(t *testing.T) {
	t.Parallel()

	const total = 51
	var mu sync.Mutex
	var batchSizes []int

	adapter := adapterForHandler(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Checks []struct {
				TupleKey      tupleKey `json:"tuple_key"`
				CorrelationID string   `json:"correlation_id"`
			} `json:"checks"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode batch-check body: %v", err)
		}
		mu.Lock()
		batchSizes = append(batchSizes, len(body.Checks))
		mu.Unlock()

		// Answer true only for the stack whose ID encodes an even number, so a
		// misordered merge produces a visibly wrong result rather than passing.
		results := map[string]any{}
		for _, check := range body.Checks {
			allowed := strings.HasSuffix(check.TupleKey.Object, "0") || strings.HasSuffix(check.TupleKey.Object, "2") ||
				strings.HasSuffix(check.TupleKey.Object, "4") || strings.HasSuffix(check.TupleKey.Object, "6") ||
				strings.HasSuffix(check.TupleKey.Object, "8")
			results[check.CorrelationID] = map[string]any{"allowed": allowed}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"result": results})
	})

	checks := make([]authz.CheckRequest, total)
	for i := range checks {
		checks[i] = authz.CheckRequest{
			Subject:  mustSubject(t, "alice"),
			Relation: authz.RelationCanView,
			Object:   mustStack(t, fmt.Sprintf("stack-%d", i)),
		}
	}

	result, err := adapter.BatchCheck(context.Background(), authz.BatchCheckRequest{Checks: checks})
	if err != nil {
		t.Fatalf("BatchCheck() error = %v", err)
	}
	if len(result.Results) != total {
		t.Fatalf("len(Results) = %d, want %d", len(result.Results), total)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(batchSizes) != 2 {
		t.Fatalf("upstream requests = %d (sizes %v), want 2", len(batchSizes), batchSizes)
	}
	for _, size := range batchSizes {
		if size > 50 {
			t.Fatalf("upstream batch of %d exceeds OpenFGA's limit of 50", size)
		}
	}

	for i, decision := range result.Results {
		wantAllowed := i%10 == 0 || i%10 == 2 || i%10 == 4 || i%10 == 6 || i%10 == 8
		if decision.Allowed != wantAllowed {
			t.Fatalf("Results[%d].Allowed = %t, want %t (ordering lost across chunks)", i, decision.Allowed, wantAllowed)
		}
	}
}
```

Ensure the file imports `sync`, `strings`, `fmt`, and `encoding/json` — add whichever are missing. `net/http/httptest` is not needed; `adapterForHandler` owns the server.

**Do not add an app-level 13-stack test.** It is the obvious thing to reach for and it would be worthless: `permissionAuthorizer` (`internal/app/authorization_test.go:248`) is a fake that returns one result per check with no 50-limit of its own, so such a test passes *before* the fix and proves nothing. The limit belongs to OpenFGA, so the only place a regression test can bite is the adapter boundary — which is what the test above covers.

After the fix, `ResolveStacksCapabilities` still correctly hands the port a single 52-check request; the adapter splits it. There is therefore nothing new to assert in `internal/app` at all. End-to-end confirmation at 13 stacks belongs to the gated integration test (`internal/openfga/live_test.go`, `OPENFGA_INTEGRATION=1`).

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/openfga/ -run TestBatchCheckChunks -v`
Expected: FAIL — `upstream requests = 1 (sizes [51]), want 2`

- [ ] **Step 3: Write minimal implementation**

In `internal/openfga/authorization_adapter.go`, add the limit next to the existing one:

```go
const maxConfirmationChecks = 25

// maxBatchChecks is OpenFGA's MaxChecksPerBatchCheck server default. Exceeding
// it returns 400, which classify() maps to ErrMalformedResponse and the API
// surfaces as a 503. The port owns this limit so no caller has to remember it.
const maxBatchChecks = 50
```

Replace the body of `BatchCheck` with a chunking loop. The per-chunk request and response handling is the existing code unchanged; only the loop and the result offset are new:

```go
// BatchCheck evaluates independently correlated permission checks and preserves
// the caller's input ordering. It splits the request into upstream calls of at
// most maxBatchChecks, so callers never have to know OpenFGA's limit.
//
//	 12 checks  → 1 upstream request,  12 results in input order
//	 51 checks  → 2 upstream requests (50 + 1), 51 results in input order
//	 52 checks  → 2 upstream requests (50 + 2)   [the 13-stack case, #220]
//	  0 checks  → ErrInvalidInput (BatchCheckRequest.Valid rejects it)
//	upstream 5xx → ErrUnavailable
func (adapter *AuthorizationAdapter) BatchCheck(ctx context.Context, request authz.BatchCheckRequest) (authz.BatchCheckResult, error) {
	if !request.Valid() {
		return authz.BatchCheckResult{}, fmt.Errorf("%w: invalid authorization batch check", authz.ErrInvalidInput)
	}

	type batchCheck struct {
		TupleKey      tupleKey `json:"tuple_key"`
		CorrelationID string   `json:"correlation_id"`
	}

	result := authz.BatchCheckResult{Results: make([]authz.CheckResult, len(request.Checks))}
	for start := 0; start < len(request.Checks); start += maxBatchChecks {
		end := start + maxBatchChecks
		if end > len(request.Checks) {
			end = len(request.Checks)
		}
		chunk := request.Checks[start:end]

		input := struct {
			AuthorizationModelID string       `json:"authorization_model_id"`
			Checks               []batchCheck `json:"checks"`
		}{AuthorizationModelID: adapter.modelID, Checks: make([]batchCheck, len(chunk))}
		for index, check := range chunk {
			input.Checks[index] = batchCheck{TupleKey: tuple(check), CorrelationID: strconv.Itoa(index)}
		}

		var response struct {
			Result map[string]struct {
				Allowed *bool `json:"allowed"`
			} `json:"result"`
		}
		err := adapter.client.doJSON(ctx, http.MethodPost, adapter.client.endpoint("stores", adapter.storeID, "batch-check"), nil, input, &response, http.StatusOK)
		if err != nil {
			return authz.BatchCheckResult{}, adapter.classify(err)
		}
		if len(response.Result) != len(chunk) {
			return authz.BatchCheckResult{}, fmt.Errorf("%w: batch check correlation results do not match requests", authz.ErrMalformedResponse)
		}

		for index := range chunk {
			correlationID := strconv.Itoa(index)
			check, ok := response.Result[correlationID]
			if !ok || check.Allowed == nil {
				return authz.BatchCheckResult{}, fmt.Errorf("%w: batch check result %q is missing or invalid", authz.ErrMalformedResponse, correlationID)
			}
			result.Results[start+index] = authz.CheckResult{Allowed: *check.Allowed}
		}
	}
	return result, nil
}
```

Correlation IDs restart at `0` within each chunk; `start+index` is what puts each answer back in the caller's position.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/openfga/ -run TestBatchCheck -v && go test ./internal/app/... 2>&1 | tail -5`
Expected: PASS — the new chunking test, and no regression in `internal/app`

- [ ] **Step 5: Commit**

```bash
git add internal/openfga/authorization_adapter.go internal/openfga/authorization_adapter_test.go
git commit -m "fix(openfga): chunk BatchCheck at OpenFGA's 50-check limit

Closes #220. ResolveStacksCapabilities builds 4*len(stacks) checks, so
GET /v1/tenants/{id}/stacks returned 503 authorization_unavailable for
any ordinary user with 13 or more accessible stacks. confirm() already
chunked at 25; the limit was known in one place and missed in the other,
which is why the port and not each caller now owns it."
```

---

## Task 9: Final verification and cleanup

**Files:**
- Modify: `internal/authz/authorization.go` (package doc comment, line 1-2)
- Verify only: everything else

**Interfaces:**
- Consumes: everything
- Produces: nothing new

- [ ] **Step 1: Update the package doc**

The current comment says the package reconciles *stack* grants. Replace the first two lines of `internal/authz/authorization.go`:

```go
// Package authz defines the provider-neutral authorization contract — a
// (Subject, Relation, Object) tuple mirroring OpenFGA's wire shape — and the
// handler that reconciles grants onto whichever provider implements it.
package authz
```

- [ ] **Step 2: Confirm no stale names survive**

Run:
```bash
grep -rn --include="*.go" "SubjectFromKeycloakSub\|StackFromID\|RoleFromDirectRelation\|authz\.Permission\|authz\.Role\b\|ListAccessibleStacks" . || echo "clean"
```
Expected: `clean`

- [ ] **Step 3: Confirm the allowlist is the only place naming grantable relations**

Run:
```bash
grep -rn --include="*.go" '"owner": true' internal/
```
Expected: exactly one hit, `internal/authz/relations.go`

- [ ] **Step 4: Full verification**

Run:
```bash
go build ./... && go vet ./... && go test ./... 2>&1 | tail -20
```
Expected: build clean, vet clean, all tests pass. Compare the `internal/authz` + `internal/openfga` count against the 121 baseline — it should be higher, never lower.

- [ ] **Step 5: Commit**

```bash
git add internal/authz/authorization.go
git commit -m "docs(authz): describe the package as the generalized port"
```

---

## What this plan deliberately does not do

Each is out of scope per the spec's §8, and adding any of them here is a plan violation:

| | Lands in |
|---|---|
| The `platform` type and `parent` edge in `openfga/authorization-model.json` | #141 |
| Writing `parent` / `platform#admin` tuples | #141 |
| Moving enforcement off Keycloak realm roles | #141, #145 |
| `SubjectFromObject`, and any structural write path | #141, with its first caller |
| Adding `admin` / `stack_creator` to the allowlist | #141 |
| `template`, `credential_set`, `github_integration` object types | #142, #143, #144 |
| Usersets / groups | Decision 10, deferred |
| `on_duplicate` / `on_missing` idempotent writes | Open decision 11 — openfga#3201 is still open |
| Authoring the model in DSL | #209 |
