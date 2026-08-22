# #214 — generalize the authz port to (Subject, Relation, Object)

Design for [#214](https://github.com/vishu42/tflive/issues/214). 2026-08-22.

Decisions come from [decision 3 of the IAM surface analysis](./2026-08-20-iam-openfga-surface-analysis.md)
(the target shape) and from
[the considerations document](./2026-08-22-authz-port-generalization-considerations.md)
(everything the shape left open). Both were approved before this was written;
this document specifies, it does not re-argue.

---

## 1. What this change is

`internal/authz` today can only express one sentence: *"may this user do this
stack-verb to this stack?"* `platform`, `template`, `credential_set`, and
`github_integration` cannot be named through it at all, which silently blocks
#141, #142, #143, #144, and #145.

This replaces the stack-shaped port with OpenFGA's own three-slot tuple, and
moves the validation that currently lives in type *names* into a per-type
relation registry.

It changes **no authorization behaviour**. Every check that passes today passes
after, and every check that fails still fails. Verified, not asserted — see §4.

## 2. Target API in `internal/authz`

### 2.1 Object types

```go
// ObjectType is one of the object types declared in the authorization model.
type ObjectType string

const (
	TypeUser     ObjectType = "user"
	TypeStack    ObjectType = "stack"
	TypePlatform ObjectType = "platform"
)
```

`ObjectType` is a defined type rather than a bare string so a stray literal
needs a deliberate conversion. It is validated against the registry regardless,
so the conversion buys friction, not safety.

### 2.2 Object and Subject

```go
// Object is a validated {type, id} in canonical "type:id" form. Its value is
// intentionally opaque so callers cannot construct unchecked IDs.
type Object struct {
	objectType ObjectType
	value      string // "stack:abc123"
}

func ObjectFromID(objectType ObjectType, id string) (Object, error)
func (object Object) Type() ObjectType
func (object Object) String() string
func (object Object) Valid() bool
```

`ObjectFromID` errors when `objectType` is not a registered type, so
`ObjectFromID("stak", id)` fails at the port rather than as a 400 from OpenFGA
that `classify()` turns into a 503.

`canonicalIdentifier(kind, value)` is already kind-parameterised and is reused
unchanged; only its callers move.

```go
// Subject is an Object in the tuple's user slot.
type Subject struct {
	object Object
	// A future `relation string` field carries usersets ("group:eng#member")
	// with no restructuring. Out of scope — decision 10.
}

func SubjectFromOIDCSub(sub string) (Subject, error) // user:<sub>
func SubjectFromObject(object Object) (Subject, error)
func (subject Subject) Type() ObjectType
func (subject Subject) String() string
func (subject Subject) Valid() bool
```

`SubjectFromObject` exists because decision 2's parent tuple puts
`platform:tflive` in the user slot. It has no caller until #141; it is included
because `Subject` wrapping `Object` is meaningless without it, and its absence
would force #141 to reopen this type.

### 2.3 The registry

A three-level map — object type → relation name → definition — replacing the
two closed enums at [authorization.go:79](../../../internal/authz/authorization.go#L79)
and [:100](../../../internal/authz/authorization.go#L100).

```go
type bucket int

const (
	bucketDerived    bucket = iota // computed; never directly writable
	bucketGrantable                // writable, and reachable from the grant API
	bucketStructural               // writable, and NOT reachable from the grant API
)

type relationDef struct {
	bucket   bucket
	subjects []ObjectType // types admissible in the user slot; empty iff derived
}

var registry = map[ObjectType]map[string]relationDef{
	TypeUser: {},
	TypeStack: {
		"owner":    {bucketGrantable, []ObjectType{TypeUser}},
		"operator": {bucketGrantable, []ObjectType{TypeUser}},
		"approver": {bucketGrantable, []ObjectType{TypeUser}},
		"viewer":   {bucketGrantable, []ObjectType{TypeUser}},
		"parent":   {bucketStructural, []ObjectType{TypePlatform}},

		"can_view":          {bucketDerived, nil},
		"can_operate":       {bucketDerived, nil},
		"can_approve":       {bucketDerived, nil},
		"can_manage_access": {bucketDerived, nil},
	},
	TypePlatform: {
		"root":          {bucketStructural, []ObjectType{TypeUser}},
		"admin":         {bucketGrantable, []ObjectType{TypeUser}},
		"stack_creator": {bucketGrantable, []ObjectType{TypeUser}},

		"can_create_stack": {bucketDerived, nil},
	},
}
```

**Why the bucket is hand-written and not derived from the model.** The
considerations document argued this on the grounds that deriving it makes the
agreement test vacuous. Building the table exposed a harder reason: the bucket
is *not derivable at all*. `platform#root` and `stack#viewer` are
indistinguishable in the model — both are `[user]` direct relations — yet
`viewer` must be grantable and `root` must not be (decision 4: root's
non-revocability stops being a runtime string comparison only because the grant
API cannot name it). The model has no way to express "directly writable, but
not through the grant API." That is tflive policy, so it lives in tflive code.

### 2.4 Relations, and the three constructors

```go
// Relation is a validated relation on a specific object type.
type Relation struct {
	objectType ObjectType
	name       string
	bucket     bucket
}

// GrantableRelation is a Relation that the grant API may write.
type GrantableRelation struct{ relation Relation }

// CheckRelation admits any relation declared on the type, in any bucket.
func CheckRelation(objectType ObjectType, name string) (Relation, error)

// GrantRelation admits grantable relations only.
func GrantRelation(objectType ObjectType, name string) (GrantableRelation, error)
```

`CheckRelation` is deliberately the permissive one. Checking a direct role is
legal in OpenFGA and `confirm()` requires it: verifying a `viewer` write by
checking `can_view` proves nothing, because the answer can be true via `owner`
while the write failed.

Named values for the app layer, so call sites read as well as they do today and
`==` comparisons such as
[service.go:1342](../../../internal/app/service.go#L1342) survive as a rename:

```go
var (
	StackOwner    = mustGrantRelation(TypeStack, "owner")
	StackOperator = mustGrantRelation(TypeStack, "operator")
	StackApprover = mustGrantRelation(TypeStack, "approver")
	StackViewer   = mustGrantRelation(TypeStack, "viewer")

	StackCanView          = mustCheckRelation(TypeStack, "can_view")
	StackCanOperate       = mustCheckRelation(TypeStack, "can_operate")
	StackCanApprove       = mustCheckRelation(TypeStack, "can_approve")
	StackCanManageAccess  = mustCheckRelation(TypeStack, "can_manage_access")
)
```

`must*` panics at package init. A panic there is a programming error in a
literal table three lines above it, not a runtime condition.

**No `StructuralRelation` constructor ships in #214.** The structural bucket is
real and enforced — `GrantRelation(TypeStack, "parent")` returns an error — but
nothing writes a structural edge until #141, and adding a constructor with no
caller repeats the mistake §4 of the considerations document identifies in
`ListAccessibleStacks`. The bucket is exercised by the negative tests in §6.

### 2.5 The write path

```go
type Grant struct {
	subject  Subject
	object   Object
	relation GrantableRelation
}

func NewGrant(subject Subject, object Object, relation GrantableRelation) (Grant, error)
```

`NewGrant` takes `GrantableRelation`, not `Relation`. Passing a structural
relation is a **compile error**, not a validation failure — which is the point
of the split. An HTTP handler parsing a user-supplied role string can only
reach `GrantRelation`, so `authz.Grant` cannot be made to hold
`{platform:tflive, parent, stack:X}` by any code path.

`NewGrant` still returns an error, for the remaining runtime conditions:
invalid subject or object, a relation whose object type does not match the
object's, and a subject whose type is not in the relation's `subjects` list —
every grantable relation admits `[user]`, so this is what rejects a
`platform:` subject being given `stack#viewer`.

`Mutation` is unchanged in shape — `[]Grant` plus `confirm` — and therefore
inherits the guarantee.

### 2.6 Requests and the interface

```go
type CheckRequest struct {
	Subject  Subject
	Relation Relation
	Object   Object
}

type ListGrantsRequest struct{ Object Object }
type ListSubjectGrantsRequest struct {
	Subject Subject
	Object  Object
}

type Authorizer interface {
	Check(context.Context, CheckRequest) (CheckResult, error)
	BatchCheck(context.Context, BatchCheckRequest) (BatchCheckResult, error)
	ListGrants(context.Context, ListGrantsRequest) (ListGrantsResult, error)
	WriteRelationships(context.Context, Mutation) error
	DeleteRelationships(context.Context, Mutation) error
}
```

`CheckRequest.Valid()` additionally requires `Relation.objectType ==
Object.Type()`, which is the invariant the old closed enum enforced by
accident.

`ListAccessibleStacks` is **deleted** from the interface, the adapter, and
six test doubles. It has no production caller and gains none at #141:
`app.listAccessibleStacks` must join against `stacks` rows for tenant scoping
and stable ordering, which bare object IDs capped at `ListObjectsMaxResults =
1000` behind a 3s deadline cannot serve.

## 3. Adapter changes — `internal/openfga`

The adapter becomes close to a passthrough, which is the payoff: there is no
translation left to get wrong.

| Location | Change |
|---|---|
| `stackFromCanonicalObject` (:416) | → `objectFromCanonical(objectType, s)`; prefix comes from the requested type |
| `grantFromReadTuple` (:428) | takes `requestedObject Object`; relation parsed with `GrantRelation(object.Type(), …)` |
| `tupleForGrant` (:412), `tuple` (:450), `checkInput` (:454) | read `.Relation`/`.Object` instead of `.Role`/`.Stack` |
| `ListAccessibleStacks` (:99–146) | deleted |
| `BatchCheck` (:57) | chunks at 50 — §5 |

`grantFromReadTuple` parsing relations through `GrantRelation` means a stored
`parent` tuple read back through `ListGrants` is rejected as an invalid tuple
rather than surfacing as a grant. That is correct: `ListGrants` answers "who has
access," and a parent edge is not access. It matters at #141, when
`ListGrants` on a stack first starts seeing a non-grant tuple.

## 4. Model change — `openfga/authorization-model.json`

Per §3(b) of the considerations document: the `platform` type lands here, with
**no enforcement change**. Go still reads Keycloak realm roles; nothing writes a
`parent` or `platform#admin` tuple. #141 writes the tuples and moves
enforcement; #145 deletes the realm role reads.

Adds exactly decision 2's model:

```
type platform
  relations
    define root: [user]
    define admin: [user] or root
    define stack_creator: [user]
    define can_create_stack: admin or stack_creator

type stack
  relations
    …unchanged…
    define parent: [platform]
    define can_view: owner or operator or approver or viewer or admin from parent
    define can_operate: owner or operator or admin from parent
    define can_approve: owner or approver or admin from parent
    define can_manage_access: owner or admin from parent
```

**Verified before writing this, not predicted.** The candidate model was built
and run against the `fga` CLI:

- The existing suite — 20 checks — passes **unchanged** against the new model.
  With no `parent` and no `platform` tuples stored, `admin from parent`
  resolves through nothing, so the change is inert.
- A new 14-check suite confirms it is also *correct*: an admin reaches all four
  stack permissions through `parent`; `root` implies `admin`; `stack_creator`
  gets `can_create_stack` but no stack access; a stack owner gets no platform
  powers.

Both suites are committed as part of this change.

## 5. Chunking — the live 13-stack bug

`ResolveStacksCapabilities`
([authorization.go:230](../../../internal/app/authorization.go#L230)) builds
`4 × len(stacks)` checks and sends them in one unchunked `BatchCheck`. OpenFGA
enforces `MaxChecksPerBatchCheck = 50` and returns 400, which `classify()` maps
to `ErrMalformedResponse` → **503**. It is reached from
[server.go:349](../../../internal/api/server.go#L349) on every stacks-list
request, so `GET /v1/tenants/{id}/stacks` fails today for any ordinary user
with **13 or more accessible stacks**. Platform admins are masked by the
`isPlatformAdmin` short-circuit — exactly what #141 removes.

The fix goes in the adapter's `BatchCheck`, not the caller: chunk at 50,
preserving caller ordering by offsetting correlation IDs per chunk. `confirm()`
already chunks at 25, which is why the port and not each caller is the right
owner of OpenFGA's limits.

This is filed as its own issue so it is tracked independently of the refactor.

**Out of scope:** `on_duplicate: "ignore"` / `on_missing: "ignore"` replacing
the confirm-after-failure dance. openfga#3201 is open — `on_duplicate` still
409s on concurrent first-time writes of the same tuple, which is precisely the
grant handler's at-least-once race.

## 6. Testing

Written test-first. The mechanical churn is large and uninteresting; three
groups of tests are the actual deliverable.

**Registry ↔ model equivalence** (`internal/openfga`), replacing
`TestCanonicalModelAllowsDirectWritesOnlyToRoles`
([model_test.go:141](../../../internal/openfga/model_test.go#L141)). A plain Go
test over the parsed model JSON — no server, no `fga` CLI, no build tag. It
asserts:

1. registry and model declare the **same set** of (type, relation) pairs;
2. each relation's `subjects` equals the model's `directly_related_user_types`;
3. `bucket == derived` **iff** the model gives it no direct types.

It deliberately does *not* assert the grantable/structural split, which the
model cannot express (§2.3). That is pinned by named tests instead:
`parent` and `root` are structural; the four stack roles, `admin`, and
`stack_creator` are grantable.

**Type-level negative tests** (`internal/authz`) — the tests that prove the
refactor achieved its purpose rather than renaming things:

- `GrantRelation(TypeStack, "parent")` and `GrantRelation(TypePlatform, "root")` error.
- `GrantRelation(TypeStack, "can_view")` errors — no writing to a derived relation.
- `CheckRelation(TypeStack, "parent")` succeeds.
- `CheckRelation(TypeStack, "admin")` errors — right name, wrong type.
- `CheckRequest{Relation: stack relation, Object: platform object}` is invalid.
- `NewGrant` rejects a subject whose type is not in the relation's `subjects`.

That `NewGrant` cannot be *called* with a structural relation is enforced by the
compiler and so has no test; a comment records why none exists.

**Chunking regression** — a 51-check `BatchCheck` against the adapter asserting
two upstream requests and correctly ordered merged results, plus a 13-stack case
through `ResolveStacksCapabilities`. Both written first and observed failing.

**Model semantics** — the two `.fga.yaml` suites from §4. Note
[#208](https://github.com/vishu42/tflive/issues/208): this repo has **no CI
workflows at all**, so "fix #208 first" is not coherent without introducing CI,
which is out of scope here. This design therefore places nothing load-bearing on
`.fga.yaml`: the equivalence test above is a plain Go test and runs under
`go test`. The suites are committed, run manually during implementation, and
become automatic when CI arrives.

## 7. Call-site churn

Mechanical, and the compiler finds all of it.

- `internal/app/authorization.go` — `authorizeStack`, `listAccessibleStacks`,
  `ResolveStackCapabilities`, `ResolveStacksCapabilities`, `authorizedStackTemplate`.
- `internal/app/service.go` — ~20 sites; `authz.PermissionX` → `authz.StackCanX`,
  `authz.RoleOwner` → `authz.StackOwner`.
- `internal/app/stack_provisioning.go`, `grant_stack_owner_handler.go`,
  `mark_stack_ready_handler.go`.
- `internal/authz/grant_handler.go` — `grantIdentity.stack` → `.object`;
  `StackGrantSpec`'s key derivation is unchanged in **format**
  (`stack:<id>/user:<sub>`), because it is built from the canonical formatters
  and those are unchanged.
- Test doubles in `cmd/api`, `cmd/worker`, `internal/api`, `internal/app` — each
  loses its `ListAccessibleStacks` stub.

Renames: `Stack` → `Object`, `StackFromID` → `ObjectFromID`,
`SubjectFromKeycloakSub` → `SubjectFromOIDCSub`, `Role`/`Permission` →
`Relation`.

## 8. Explicitly out of scope

| | Lands in |
|---|---|
| Writing `parent` / `platform#admin` tuples | #141 |
| Moving enforcement off Keycloak realm roles | #141, #145 |
| Structural write path (`Edge` type, edge write methods) | #141, with its first caller |
| Platform grant API and UI | #215 |
| `template`, `credential_set`, `github_integration` types | #142, #143, #144 |
| Usersets / groups (`group:eng#member`) | Decision 10, deferred |
| `on_duplicate` / `on_missing` idempotent writes | Open decision 11 |
| Authoring the model in DSL | #209 |
| Making model-semantics tests run automatically | #208, needs CI first |
