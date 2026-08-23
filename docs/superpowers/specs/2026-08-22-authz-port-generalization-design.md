# #214 — generalize the authz port to (Subject, Relation, Object)

Design for [#214](https://github.com/vishu42/tflive/issues/214). 2026-08-22.

The shape comes from [decision 3 of the IAM surface analysis](./2026-08-20-iam-openfga-surface-analysis.md).
The open questions were worked through in
[the considerations document](./2026-08-22-authz-port-generalization-considerations.md);
its postscript records where its own recommendations were overturned by
evidence, and this design follows the postscript, not the body.

---

## 1. What this change is

`internal/authz` can express one sentence: *"may this user do this stack-verb to
this stack?"* `platform`, `template`, `credential_set`, and
`github_integration` cannot be named through it at all, which silently blocks
#141, #142, #143, #144, and #145.

This replaces the stack-shaped port with OpenFGA's own three-slot tuple. It
changes no authorization behaviour.

The port stops duplicating what the server enforces (§2) and keeps exactly one
refusal of its own (§3.4). Net effect on `internal/authz` is a **reduction** in
concepts: four types become three, two closed enums become one type plus a
nine-line allowlist.

## 2. What the port must actually refuse

**The same three strings mean different things on different endpoints**, and
this is the fact the whole design turns on. `check` asks a question and will
evaluate any relation; `write` stores a fact and validates it against the
model's `directly_related_user_types`. So a relation illegal to *store* can be
entirely legal to *ask about* — indeed `user:alice can_view stack:X` is the
primary question this port exists to ask, sent by `authorizeStack` on every
request.

The current port refuses several tuples on the grounds that they are dangerous.
Each was tested against the real model with the `fga` CLI rather than assumed,
**on the write path**:

| Stored tuple | OpenFGA's own verdict |
|---|---|
| `user:alice can_view stack:X` — derived relation | **REJECTED** — `type 'user' is not an allowed type restriction for 'stack#can_view'` |
| `user:* viewer stack:X` — typed wildcard, grants everyone | **REJECTED** — wildcard not an allowed type restriction |
| `user:alice#member viewer stack:X` — userset injection | **REJECTED** — `relation 'user#member' not found` |
| `platform:tflive parent stack:X` — structural edge | **ACCEPTED** |
| `user:alice viewer stack:X` — control | ACCEPTED |

To be explicit, since the first row is easy to misread: `can_view` is rejected
as a *stored tuple* only. Asked as a check against a subject holding `owner`, it
returns `true`, and must.

**Three of the four are already enforced by the server** on the path that
matters. The `Role` / `Permission` split — the thing that would otherwise have
to generalize into a per-type registry — duplicates a check OpenFGA does
natively, against the model itself, and therefore without drift.

So the registry, the three-way relation classification, and the
registry-versus-model agreement test are all deleted from this design. The one
row the server does not refuse is handled by §3.4.

This write/check asymmetry is also *why* §3.4 has two constructors rather than
one validated `Relation` type. They are not "strict" and "lax" versions of the
same rule — they are the two endpoints' genuinely different rules:
`NewRelation` for the question, `GrantRelation` for the fact.

## 3. Target API in `internal/authz`

### 3.1 Object types

```go
// ObjectType is an object type declared in the authorization model.
type ObjectType string

const (
	TypeUser  ObjectType = "user"
	TypeStack ObjectType = "stack"
)
```

A defined type rather than a bare string, so a stray literal needs a deliberate
conversion. It is **not** validated against a local list of known types:
OpenFGA validates `type` against the model, so `ObjectType("stak")` is refused
at the server against the single source of truth. `TypePlatform` arrives with
#141.

### 3.2 Object and Subject

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

```go
// Subject is the tuple's user slot: who is acting.
type Subject struct {
	identifier // shared {objectType, id}; Object embeds the same
}

func SubjectFromOIDCSub(sub string) (Subject, error) // user:<sub>
func (subject Subject) Type() ObjectType
func (subject Subject) ID() string
func (subject Subject) String() string
func (subject Subject) Valid() bool
```

> **Superseded as built.** `Subject` was specified above as wrapping `Object`,
> so that a future `relation` field could carry usersets ("group:eng#member")
> without restructuring. As implemented they are siblings over a shared
> unexported `identifier`, because siblings give the same extension path —
> `Subject` gains the field, `Object` is untouched — without the cost the
> wrapping carried: `Subject.Valid()` delegated to `Object.Valid()`, so nothing
> stopped a resource type occupying the user slot. `Subject.Valid()` now checks
> a `subjectTypes` allowlist, which is the constraint the separate type exists
> to carry. `identifier` also stores `{objectType, id}` rather than the rendered
> `"type:id"`, which is what makes `ID()` possible and removed a
> `strings.TrimPrefix(…, "user:")` from `internal/app`.

The opaque unexported field is the whole enforcement mechanism, and it is worth
one line: with `type Object string`, `Object("garbage")` compiles and validation
can be skipped by forgetting to call the constructor. With an unexported field
it cannot.

`Subject` wraps `Object` because on the wire both slots hold `type:id`, and
because that is what lets the userset suffix arrive later without restructuring.
A `SubjectFromObject` constructor — needed to put `platform:tflive` in the user
slot — is **not** included: nothing writes that tuple until #141, and it arrives
with its caller.

### 3.3 Identifier validation, tightened

`canonicalIdentifier` is reused, with its rejected-character set widened:

| Character | Today | After | Why |
|---|---|---|---|
| `:` | rejected | rejected | would forge the type prefix |
| whitespace, control | rejected | rejected | malformed tuples |
| `#` | **allowed** | **rejected** | turns `user:<sub>` into a userset reference |
| `*` | **allowed** | **rejected** | turns `user:<sub>` into the everyone-wildcard |

`sub` comes from the customer's IdP and is the one identifier we do not
originate. The §2 probe shows OpenFGA currently fails closed on both — but only
because our model declares `[user]` rather than `[user:*]`, and because `user`
has no relations. Both are model facts that could change. Two characters in an
existing check removes the dependency.

Empty IDs stay rejected, as today.

### 3.4 Relations

```go
// Relation is a validated relation name.
type Relation struct{ value string }

// NewRelation admits any relation name. Checking is always safe: OpenFGA
// rejects a relation that does not exist on the object's type.
func NewRelation(name string) (Relation, error)

// GrantRelation admits only relations the grant API may write.
func GrantRelation(name string) (Relation, error)
```

The allowlist is the port's one genuine refusal — the only row in §2's table
the server does not catch:

```go
// The one refusal OpenFGA cannot make for us. A structural edge such as
// stack#parent is a perfectly legal tuple, so nothing but this list stops a
// grant endpoint writing one. Derived relations are absent too, but the
// server already refuses those; they are here for readability, not safety.
var grantableRelations = map[string]bool{
	"owner": true, "operator": true, "approver": true, "viewer": true,
}
```

It gains `admin` and `stack_creator` at #141. It is deliberately **not** keyed
by object type: type/relation mismatches (`can_view` on a `platform` object) are
programming errors that OpenFGA rejects, and keying by type buys a better error
message in exchange for a table that can drift from the model.

Named values, preserving the shape of today's call sites so the diff is a
rename:

```go
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
```

`must*` panics at package init — a programming error in a literal table three
lines above it, not a runtime condition.

### 3.5 Grants and requests

```go
type Grant struct {
	subject  Subject
	object   Object
	relation Relation
}

// NewGrant errors unless the relation is grantable, so a Grant can never hold
// a structural edge however it was constructed.
func NewGrant(subject Subject, object Object, relation Relation) (Grant, error)
```

`Mutation` is unchanged in shape — `[]Grant` plus `confirm` — and inherits the
guarantee.

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

`ListAccessibleStacks` is **deleted** from the interface, the adapter, and six
test doubles. It has no production caller and gains none at #141:
`app.listAccessibleStacks` must join against `stacks` rows for tenant scoping
and stable ordering, which bare object IDs capped at `ListObjectsMaxResults =
1000` behind a 3s deadline cannot serve.

## 4. Adapter changes — `internal/openfga`

The adapter becomes close to a passthrough, which is the payoff: there is no
translation left to get wrong.

| Location | Change |
|---|---|
| `stackFromCanonicalObject` (:416) | → `objectFromCanonical(objectType, s)`; prefix comes from the requested type |
| `grantFromReadTuple` (:428) | takes `requestedObject Object`; relation parsed with `GrantRelation` |
| `tupleForGrant` (:412), `tuple` (:450), `checkInput` (:454) | read `.Relation` / `.Object` instead of `.Role` / `.Stack` |
| `ListAccessibleStacks` (:99–146) | deleted |
| `BatchCheck` (:57) | chunks at 50 — §5 |

`grantFromReadTuple` refusing a structural relation means a stored `parent`
tuple read back by `ListGrants` never surfaces as a grant. That is correct —
`ListGrants` answers "who has access," and a parent edge is not access — and it
starts mattering at #141.

> **Superseded as built.** The wording above did not distinguish *skip the
> tuple* from *fail the whole call*, and as first written it did the latter:
> one `parent` edge made `ListGrants` return `ErrMalformedResponse` for that
> stack. Since #141 writes such an edge on every stack, and `GrantStackOwner`
> reads grants before writing to stay idempotent on replay, that would have
> hung stack creation rather than merely breaking the grants list.
>
> As built, `grantFromReadTuple` classifies rather than refuses, and the test is
> storability, not grantability:
>
> - **skipped** — a relation OpenFGA stores that is not access (`parent`,
>   `root`), or a non-`user:` subject. The tuple legitimately exists; "who has
>   access" is still completely answered without it.
> - **fails the call** — a nil key, an object other than the one requested, a
>   non-canonical subject, or a relation OpenFGA would never have stored
>   (`can_view` and the other derived relations declare no
>   `directly_related_user_types`, so the write is rejected). Being handed one
>   of these means the store or the response cannot be trusted, and skipping it
>   would return an ordinary-looking list with the anomaly silently removed.
>
> The distinction matters because the grants list is not only displayed:
> `authz.grant_handler` computes deletes from it, and `AssignStackRole` and
> `RevokeStackRole` diff against it. A silently shortened list is a wrong
> premise for a mutation.

## 5. Chunking — the live 13-stack bug ([#220](https://github.com/vishu42/tflive/issues/220))

`ResolveStacksCapabilities`
([authorization.go:230](../../../internal/app/authorization.go#L230)) builds
`4 × len(stacks)` checks and sends them in one unchunked `BatchCheck`. OpenFGA
enforces `MaxChecksPerBatchCheck = 50` and returns 400, which `classify()` maps
to `ErrMalformedResponse` → **503**. It is reached from
[server.go:349](../../../internal/api/server.go#L349) on every stacks-list
request, so `GET /v1/tenants/{id}/stacks` fails today for any ordinary user with
**13 or more accessible stacks**. Platform admins are masked by the
`isPlatformAdmin` short-circuit — which is what #141 removes.

The fix goes in the adapter's `BatchCheck`: chunk at 50, preserving caller
ordering by offsetting correlation IDs per chunk. `confirm()` already chunks at
25, which is why the port and not each caller should own OpenFGA's limits.

**Out of scope:** `on_duplicate: "ignore"` / `on_missing: "ignore"` replacing the
confirm-after-failure dance. openfga#3201 is open — `on_duplicate` still 409s on
concurrent first-time writes of the same tuple, which is exactly the grant
handler's at-least-once race.

## 6. Testing

Written test-first. Most of the churn is mechanical and the compiler finds it;
three things are the real deliverable.

**Identifier validation** (`internal/authz`) — `#` and `*` rejected in both the
subject and object slots, alongside the existing `:`/whitespace/control/empty
cases. These are the tests that matter most, because this is the only input we
do not originate.

**The grantable allowlist** (`internal/authz`):

- `GrantRelation("parent")` errors — the §2 row the server does not catch.
- `GrantRelation("can_view")` errors.
- `NewRelation("parent")` and `NewRelation("can_view")` succeed.
- `NewGrant` with a non-grantable relation errors, however the relation was
  built.

**Chunking regression** ([#220](https://github.com/vishu42/tflive/issues/220)) —
a 51-check `BatchCheck` against the adapter asserting two upstream requests and
correctly ordered merged results, plus a 13-stack case through
`ResolveStacksCapabilities`. Both written first and observed failing.

There is deliberately **no** registry-versus-model agreement test: §2 shows the
model already enforces those invariants at the server, and a local copy of them
would be a second source of truth that can drift.

## 7. Call-site churn

Mechanical, and the compiler finds all of it.

- `internal/app/authorization.go` — `authorizeStack`, `listAccessibleStacks`,
  `ResolveStackCapabilities`, `ResolveStacksCapabilities`,
  `authorizedStackTemplate`.
- `internal/app/service.go` — ~20 sites; `authz.PermissionX` →
  `authz.RelationCanX`, `authz.RoleOwner` → `authz.RelationOwner`.
- `internal/app/stack_provisioning.go`, `grant_stack_owner_handler.go`,
  `mark_stack_ready_handler.go`.
- `internal/authz/grant_handler.go` — `grantIdentity.stack` → `.object`.
  `StackGrantSpec`'s key format (`stack:<id>/user:<sub>`) is **unchanged**,
  because it is built from the canonical formatters and those are unchanged.
- Test doubles in `cmd/api`, `cmd/worker`, `internal/api`, `internal/app` — each
  loses its `ListAccessibleStacks` stub.

Renames: `Stack` → `Object`, `StackFromID` → `ObjectFromID`,
`SubjectFromKeycloakSub` → `SubjectFromOIDCSub`, `Role` and `Permission` →
`Relation`.

## 8. Explicitly out of scope

| | Lands in |
|---|---|
| The `platform` type and the `parent` edge in the model | #141 |
| Writing `parent` / `platform#admin` tuples | #141 |
| Moving enforcement off Keycloak realm roles | #141, #145 |
| `SubjectFromObject`, and any structural write path | #141, with its first caller |
| Platform grant API and UI | #215 |
| `template`, `credential_set`, `github_integration` types | #142, #143, #144 |
| Usersets / groups (`group:eng#member`) | Decision 10, deferred |
| `on_duplicate` / `on_missing` idempotent writes | Open decision 11 |
| Authoring the model in DSL | #209 |

The two `.fga.yaml` suites written while verifying the earlier draft of this
design — the existing 20 checks passing unchanged against a model carrying
`platform` and `parent`, and 14 new checks confirming `admin from parent`
resolves correctly — are kept in the scratchpad and belong to #141. They are
evidence that its model change is inert and correct, gathered early.
