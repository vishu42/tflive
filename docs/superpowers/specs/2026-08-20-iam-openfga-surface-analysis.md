# IAM epic (#151) — surface analysis

**Status:** analysis, plus a standing constraint (§0), five settled decisions
(§8), and a roadmap for the identity layer (§9). Not a spec and not a plan.
Written 2026-08-20 against `main` at 14251dc; extended 2026-08-21.

**The epic has since been split.** #151 keeps authorization — the OpenFGA model,
the port, and every "may this principal do X" question. Identity moved to
**#210**: token verification, provider portability, the users projection, local
accounts, and Keycloak extraction. The two meet at exactly one point, #145
removing `RealmRoles` from `authn.Principal`. Sections 1-7a below were written
before the split and describe both halves.

The goal as stated in #151: tflive accepts any OIDC provider's JWT access
token, stores no user records of its own beyond a projection, and lets
OpenFGA answer every authorization question with no exception.

---

## 0. Standing constraint: there is no data to preserve

tflive is pre-production. Nothing is deployed, nobody uses it, and all state is
disposable. A single `postgres-data` volume (`docker-compose.yaml:23`) backs the
app database, OpenFGA, and Temporal, so `docker compose down -v` is a complete
reset.

**Therefore, until first deploy, prefer the clean change over the compatible
one.** Concretely, none of the following are costs in this epic:

- **Backfilling tuples.** Wipe and re-provision instead.
- **Migrating the queue key format.** `internal/authz/grant_handler.go:44-49`
  declares `"stack:<id>/user:<sub>"` a frozen contract requiring a drained
  queue or a new `Kind`. That constraint protects a *running* deployment.
  There is none, so the format can simply change.
- **Preserving the `/v1/me` wire shape.** Its only consumer is our own web app.
  The ~20 test files pinning `{isPlatformAdmin, canCreateStack}` are work, not
  a compatibility risk.
- **Accumulated OpenFGA models.** `Bootstrap`'s fail-closed ambiguity check
  exists for stores that have collected models over time; a fresh store has one.

What this does **not** excuse, because none of it is about compatibility:

- Security invariants — the grant API must still be unable to write a `parent`
  edge (§7a, §8). That is correctness, not migration.
- The `BatchCheck` chunking bug (§7a). Live defect, wipe or no wipe.
- The go 1.25.7 floor on embedding OpenFGA (§3.2), or any OpenFGA limit.

The items that *do* become expensive at first deploy are the ones that turn
into external contracts: the OIDC claim contract (#198), the queue key format,
and the `/v1/me` shape. Settle those deliberately before shipping, not after.

---

## 1. The fact that dominates everything else

The `authz` port is **stack-shaped, deliberately and thoroughly**. It is not a
general authorization port with a stack convenience layer; stacks are welded
into the type system.

```go
// internal/authz/authorization.go
type CheckRequest struct {
    Subject    Subject
    Stack      Stack        // <- not Object
    Permission Permission
}

const (                     // <- closed set, all four are stack verbs
    PermissionView         Permission = "can_view"
    PermissionOperate      Permission = "can_operate"
    PermissionApprove      Permission = "can_approve"
    PermissionManageAccess Permission = "can_manage_access"
)

func (role Role) Valid() bool {
    switch role.value {
    case "owner", "operator", "approver", "viewer":   // <- closed set
```

`platform`, `template`, `credential_set`, and `github_integration` cannot be
expressed through this port at all. Five of the epic's thirteen issues
(#141, #145, #142, #143, #144) silently require the port to change shape
first, and none of them says so.

**Generalizing it is cheaper than it looks.** The validation helper is already
kind-parameterised:

```go
// internal/authz/authorization.go:129
func canonicalIdentifier(kind, value string) (string, error)
```

`Subject` and `Stack` are the same struct with a different `kind` string baked
in. The mechanical shape of the change is `Stack{value}` → `Object{kind,
value}`, `Permission`/`Role` from closed enums to per-kind validated sets.

**Where it is genuinely expensive** — every one of these hardcodes `stack:`:

| Location | What breaks |
|---|---|
| `internal/openfga/authorization_adapter.go:416` | `stackFromCanonicalObject` |
| `:428` | `grantFromReadTuple(key, requestedStack)` |
| `:412` `:450` `:454` | `tupleForGrant`, `tuple`, `checkInput` |
| `internal/openfga/model_test.go:141` | `TestCanonicalModelAllowsDirectWritesOnlyToRoles` — pins "the only directly-writable relations are the four stack roles" as a model invariant. It has to be re-expressed per type, not deleted; it is the thing stopping someone writing a tuple straight onto `can_view`. |
| `internal/authz/authorization_test.go` | 6 tests, all built on the closed enums |

**And one buried landmine.** `internal/authz/grant_handler.go:44-49`:

> The key is `"stack:<id>/user:<sub>"`, built from the canonical formatters so
> it cannot drift from the identity OpenFGA uses. **This derivation is a frozen
> contract**: keys are persisted, and changing the format splits one resource
> across two keys, disabling the mutual exclusion the unique partial index
> provides. To change it, drain the queue with producers stopped, or introduce
> a new Kind and let the old one drain.

So grants on new resource types cannot reuse `reconcile_stack_grant` as-is.
**Discharged by §0:** the drain-or-new-`Kind` requirement protects a running
deployment, and there isn't one — so generalize the key format directly and
wipe. Worth knowing the constraint exists, because it reappears the moment
anything is deployed.

---

## 2. Complete inventory of Keycloak-shaped decisions

Everything flows from one field:

```go
// internal/authn/principal.go:20
RealmRoles []string    // filtered to {"platform-admin", "stack-creator"} at :49
```

**Producers** — `oidc_verifier.go:148` reads `realm_access.roles`;
`principal.go:45` normalizes it.

**Consumers** — exactly two, and they disagree:

- *Enforcement:* `internal/app/authorization.go:13-24` (`hasRealmRole`,
  `isPlatformAdmin`) and `internal/app/service.go:53` (`canCreateStack`)
- *Projection:* `internal/auth/me.go:36` (`capabilitiesFromRoles`)

That duplication **is** #201. It is not a bug that happened to appear; two
independent derivations of one decision will always drift, and this pair drifted
on `platform-admin` → `canCreateStack`.

**`isPlatformAdmin` short-circuits OpenFGA in seven places:**

| Site | Effect today |
|---|---|
| `authorization.go:50` | `requireTemplateCatalogAccess` — admin **or** stack-creator |
| `authorization.go:68` | `authorizeStack` — returns before any Check |
| `authorization.go:90` | `listAccessibleStacks` — **skips paging entirely**, straight to `repository.ListStacks` |
| `authorization.go:175` | `authorizedStackTemplate` |
| `authorization.go:196` | `ResolveStackCapabilities` — returns all-true |
| `authorization.go:238` | `ResolveStacksCapabilities` — returns all-true for every stack |
| `service.go:1188` | (template catalog operation) |

The `:90`, `:196`, and `:238` ones are the interesting ones — see §4.

**Non-role Keycloak coupling:**

- `oidc_verifier.go:132` — `typ: Bearer`. Fails closed. #195. Hard blocker.
- `internal/keycloak/directory.go` (210 lines) behind `app.UserDirectory`
  (`internal/app/directory.go`), wired at `cmd/api/main.go:175-183`, serving
  `GET /v1/tenants/{id}/users/search` (`api/server.go:101`). #155.
- `internal/keycloak/` total: ~1490 non-test lines, largest non-core package.
- `authz.SubjectFromKeycloakSub` — the name is a lie after this epic; ~6 call
  sites. Cosmetic but it is in the port's public surface.

---

## 3. Considerations on the authorization model

### 3.1 The platform-admin bypass: in Go, or in the model?

Two ways to satisfy "OpenFGA answers everything," and they are not equivalent.

**(a) Keep the branch, move the source.** Replace `isPlatformAdmin(principal)`
with `authorizer.Check(subject, platform:tflive, admin)`. Minimal diff — seven
call sites each grow one check. But the branch is still in Go, admin is still a
special case in application code, and it costs an extra round trip on every
request that has one.

**(b) Put it in the model.** Give `stack` a `platform` parent relation and
derive through it:

```
can_view = owner or operator or approver or viewer or admin from platform
```

One check answers the whole question. No Go branching at all — which is what
"no exception" actually means. `ListObjects` then naturally returns every stack
for an admin, so `:90` stops being a special case rather than becoming a slow one.

Cost of (b): every stack needs a `platform:tflive` parent tuple written at
creation, and existing stacks need a backfill. Also worth checking whether the
`stack` object should hang off `tenant` instead of `platform` — the app already
has a `TenantID` everywhere and #151 has nothing to say about it. Getting the
parent wrong is expensive to undo once tuples exist.

I lean (b) hard, but it is the single biggest modeling call in the epic and it
deserves a real argument.

### 3.2 Latency and availability regression

Today a platform admin's request touches OpenFGA **zero** times. After this it
touches it on every request, fail-closed. Two specific cliffs:

- `ResolveStacksCapabilities` (`:238`) currently returns all-true instantly for
  an admin. Without the bypass, an admin listing 50 stacks issues **200
  checks** (4 permissions × 50) through `BatchCheck`. Approach (b) does not fix
  this by itself — it just makes them all resolve through the parent.
- `listAccessibleStacks` (`:90`) currently does one repository query for an
  admin. Without the bypass it pages 50 at a time and batch-checks each page.

This also re-opens the embedded-OpenFGA question shelved on 2026-08-04. That
scoping still stands: `openfga/pkg/server` ≥ v1.12.0 needs **go 1.25.7** (repo
is on 1.24.0), forces `pgx/v5` 5.7.6 → 5.10.0, module count 98 → ~280, ~3-5
days across ~7 PRs. Not a prerequisite, but the epic makes the in-process hop
much more attractive than it was when it was deferred. Note there is still no
CI (`.github/` absent), so a toolchain bump has no automated safety net.

### 3.3 Root user (#153) contradicts the epic's own premise

As written, #153 is an env-configured identity that bypasses the API. That is a
second decision path, which is exactly what "no exception" rules out.

Alternative worth considering: **root is a tuple, not a bypass.** The configured
sub gets `platform#admin` reconciled at every boot, and the grant API refuses to
delete that specific tuple. Same break-glass property — root cannot be locked
out — but OpenFGA still answers every question, and root's access is visible in
the tuple store instead of hidden in a config value. The `actor_type: root`
audit marker still works; it is a property of the principal, not of the
decision path.

### 3.4 `ListAccessibleStacks` has no production caller

It is in the `Authorizer` interface (`authorization.go:323`) and fully
implemented in the adapter (`authorization_adapter.go:102-143`, with duplicate
detection and validation), but nothing outside tests calls it —
`app.listAccessibleStacks` hand-rolls paging plus `BatchCheck` instead.

Either it is dead weight to delete, or it is the thing `:85-149` should have
been using and the paging loop is the accident. Worth resolving during this
epic rather than carrying an unused method into a generalized port, where it
becomes `ListAccessibleObjects` and multiplies.

---

## 4. Considerations on the token contract

### 4.1 `stringClaim` is asymmetric and it is worse than #195 says

```go
// internal/authn/oidc_verifier.go:162
func stringClaim(token jwt.Token, name string) (string, bool) {
    if !token.Has(name) { return "", true }   // absent -> ("", true)
```

Absent returns `("", true)`. So `typ` absent → `""` → `!= "Bearer"` → rejected
(effectively **mandatory**), while `name`, `preferred_username`, `email` absent
→ `""` → accepted (effectively **optional**). One helper, two opposite
behaviours, neither visible at the call site.

`realmRoles` at `:173` has the same shape. Once #155 makes `email` and `name`
feed a persisted `users` projection, "absent" and "empty string" stop being
interchangeable — they become two different rows. Fixing the asymmetry is a
prerequisite for the projection, not just tidiness.

### 4.2 The claim contract becomes a public API

Today the required set is implicit and partly accidental: `aud: tflive-api`,
`sub`, `exp`, `iss`, plus the three optional-by-accident display claims. #198
wants it documented; the sharper point is that **once it is documented it is
frozen**, so it should be settled before it is written down — which is what
#198's own sequencing note says.

`docs/authentication.md` is 17.6K and heavily Keycloak-specific (`## Local
Realm`, `## Global Roles`, `## Keycloak Provisioner Configuration`). It is not
a doc that gets edited; it is a doc that gets restructured.

### 4.3 #199 is real and under-rated

The issuer/discovery-URL split is filed as "not needed for the local demo," but
it blocks *every* containerised deployment, which is the deployment the BYO-IdP
customer has. Sequencing it late means the epic completes without anyone being
able to actually use it in Kubernetes.

---

## 5. Where I'd contest the epic's sequencing

The epic orders portability blockers as #195 → #141 → #145 → #201 → #155.

- **#201 is not a separate step.** It exists *only* because enforcement and
  projection derive separately. Once #145 routes both through one resolver, the
  divergence is impossible by construction. Doing it as its own item means
  fixing it twice — once as a patch, once by deletion. It should be an
  acceptance test on #145, not an issue.
- **The port generalization is unlisted and blocks five issues.** It should be
  its own item, before #141.
- **#199 probably belongs early**, not in the "once agnostic" bucket (§4.3).
- **#153's shape must be settled before #141**, since "root is a tuple" makes it
  part of platform-singleton bootstrap and "root is a bypass" makes it a
  separate mechanism. Same issue, two very different implementations.
- **#196 (go-oidc) is correctly flagged as isolated** — ~1180 lines of tests are
  the safety net, and four strictness properties (duplicate `kid` rejection,
  response size limits, fail-closed staleness, manual refresh control) must be
  preserved in a wrapper or consciously surrendered. Point 3 is a genuine policy
  choice, not an oversight. Do not interleave it.

---

## 6. Tests that will fight this

- `internal/api/server_test.go:3024` — `{roles: ["platform-admin"], wantAdmin:
  true, wantCreate: false}` **pins the #201 defect**. Codifies implementation,
  not intent.
- `internal/openfga/model_test.go:141` — invariant tied to the four stack roles.
- `internal/authz/authorization_test.go` — 6 tests on the closed enums.
- `internal/openfga/authorization_adapter_test.go` — 724 lines, stack-shaped.
- `internal/app/service_test.go` (2880 lines) and `authorization_test.go` —
  every test constructing a principal with realm roles.
- **Web:** ~20 test files construct `globalCapabilities: {isPlatformAdmin,
  canCreateStack}`. The wire shape of `/v1/me` can stay identical if we want it
  to — worth deciding deliberately rather than letting it change by accident,
  since `RequireCapability` and `router.tsx:59` are built on those two keys.

---

## 7. Things not in the epic that surfaced anyway

- The frozen queue-key contract (§1) — a deployment constraint, not a code one.
- `ListAccessibleStacks` being uncalled (§3.4).
- Whether `stack` should hang off `tenant` rather than `platform` (§3.1).
- `SubjectFromKeycloakSub` naming leaking through the port.
- `internal/auth/` is a 47-line package containing only `me.go` and `doc.go`,
  sitting next to `internal/authn/` and `internal/authz/`. Three packages whose
  names differ by two characters. If `me.go` becomes the shared
  enforcement-and-projection resolver, it stops being a projection helper and
  the naming needs to stop being a coin flip.

---

## 7a. OpenFGA's actual API contract, and what it implies for port shape

Verified against `openfga/openfga:v1.15.1` (the version pinned in
`docker-compose.yaml:104`), reading `pkg/server/config/config.go` and
`pkg/server/commands/` at that tag, plus the `openfga/api` proto. Not recalled.

### The whole data model is three strings

```
TupleKey { user, relation, object, condition? }
```

`object` is `"<type>:<id>"`. `user` is `"<type>:<id>"`, `"<type>:*"`, or
`"<type>:<id>#<relation>"`. That is it. Every endpoint is a different question
asked of that one shape:

| Endpoint (`POST /stores/{id}/…`) | Question | Shape |
|---|---|---|
| `check` | may this user? | 1 tuple → `allowed` |
| `batch-check` | …for N tuples | N tuples + `correlation_id` → map |
| `list-objects` | which objects of type T? | (user, relation, type) → `[]object` |
| `streamed-list-objects` | same, unbounded | stream |
| `list-users` | who has this on this object? | (object, relation, filters) → `[]user` |
| `expand` | why? | (relation, object) → tree |
| `read` | what is *stored*? | partial filter → tuples, paginated |
| `write` | change tuples | `writes` + `deletes`, atomic |

**Two distinctions our port invented that OpenFGA does not have:**

1. **`Permission` vs `Role`.** On the wire `relation` is one string field;
   `can_view` and `owner` are the same kind of thing. Our split is ours. It is
   *worth keeping* — it is what stops anyone writing a tuple straight onto
   `can_view` — but it has to become **per-type**, because `can_view` belongs to
   `stack` and `admin` belongs to `platform`. One global enum cannot survive.
2. **`stack` as a type.** OpenFGA validates `type` against the model and
   nothing else. Every `stack:` literal in the adapter maps to nothing on the
   wire; it is purely our constraint.

### The `user` field is far richer than our `Subject`

Our `Subject` models only `user:<sub>`. OpenFGA also accepts `user:*`
(typed wildcard) and `group:eng#member` (a **userset** — a set-valued subject).

This is not academic for a BYO-IdP epic. Customers arrive with groups already
in Okta/Entra, and the natural mapping is a group claim → `group:<id>#member`.
**The port cannot express a grant to a group today**, and nothing in #151
mentions it. If `Object{Type, ID}` is the generalization on the object side,
`Subject` needs the mirror on the user side or we will redo this.

Related, and possibly better: **`contextual_tuples`** on `check`,
`batch-check`, and `list-objects` — tuples supplied per request and never
persisted. That is a direct answer to "the customer's IdP owns group
membership": pass the token's group claims as contextual tuples on each
request, persist nothing, and OpenFGA still owns the decision. Worth weighing
against persisting group tuples and reconciling them.

### Write: atomic, bidirectional, non-idempotent by default

- `writes` and `deletes` go in **one atomic request**. Our adapter forbids
  mixing them — `authorization_adapter.go:350-352`, *"relationship write must
  contain exactly one mutation direction"*. That is our restriction, not
  OpenFGA's, and it is why grant reconciliation does delete-then-write as two
  non-atomic calls (`grant_handler.go:94-119`).
- **100 tuples max** per request, summed across writes and deletes
  (`DefaultMaxTuplesPerWrite = 100`).
- Rewriting an existing tuple errors; deleting a missing one errors. Setting
  `"on_duplicate": "ignore"` on `writes` and `"on_missing": "ignore"` on
  `deletes` makes them idempotent. Merged Sept 2025 (openfga#2648, #2681), so
  **present in our v1.15.1**. The adapter's confirm-after-failure dance
  (`WriteRelationships`/`DeleteRelationships` + `confirm()`, ~100 lines) is
  largely what these flags now do natively.
- **But do not delete that dance yet.** openfga#3201 is *open*:
  `on_duplicate: "ignore"` still returns 409 on concurrent first-time writes of
  the same tuple. Our grant handler is an at-least-once queue consumer, which is
  exactly that race.

### Hard limits (v1.15.1 defaults, `pkg/server/config/config.go`)

| Limit | Default |
|---|---|
| `MaxChecksPerBatchCheck` | **50** |
| `MaxTuplesPerWrite` | 100 |
| `ListObjectsMaxResults` | 1000 |
| `ListObjectsDeadline` | 3s |
| `ListUsersMaxResults` / `Deadline` | 1000 / 3s |
| `RequestTimeout` | 3s |
| `MaxTypesPerAuthorizationModel` | 100 |
| `ResolveNodeLimit` (eval depth) | 25 |

`ListObjectsMaxResults = 1000` bears directly on §3.1: if platform-admin moves
into the model, `ListObjects` for an admin silently caps at 1000 stacks and
gives up after 3s. Approach (b) needs an answer for that.

### This surfaced a live bug (not future — now)

`ResolveStacksCapabilities` (`internal/app/authorization.go:253`) builds
`4 × len(stacks)` checks and sends them in **one unchunked `BatchCheck`**. The
server enforces 50 (`pkg/server/commands/batch_check_command.go:132`) and
returns 400. Our `classify()` maps a non-429/non-5xx status to
`ErrMalformedResponse` → **503 `authorization_unavailable`**.

So `GET /v1/tenants/{id}/stacks` fails at **13 accessible stacks**. Platform
admins are masked today by the `:238` short-circuit — which this epic removes,
extending the failure to everyone. Note `confirm()` already chunks at 25
(`maxConfirmationChecks`), so the constraint was known in one place and missed
in the other.

Not executed — derived from both sources. Worth a 13-stack test to confirm.

### What the wire contract implies for the port

**Settled — see §8 decision 3**, which resolves the first three of these:

- `Object{Type, ID}` replaces `Stack`; `canonicalIdentifier(kind, value)`
  already does the work.
- `Subject` widens to *user or userset* (§ above).
- One `Relation` type replaces `Permission`/`Role`, plus a **per-type registry**
  declaring which relations are directly writable and which are derived-only —
  that registry is where the invariant currently pinned by
  `TestCanonicalModelAllowsDirectWritesOnlyToRoles` moves to.
- **Chunking belongs in the port**, not in each caller's memory. The bug above
  is what "each caller remembers" produces. (Still open — decision 10.)

---

## 8. Decisions

### Settled (2026-08-20)

**1. The platform-admin bypass goes in the model, not in Go.** §3.1 option (b).

The deciding argument was not elegance — it was that option (a) cannot answer
*"list every stack this admin can see."* `ListObjects` only returns objects
reachable through stored tuples, so with a Go-side check an admin's stack list
comes back empty and the `if isPlatformAdmin` branch survives in the listing
path. Putting the edge in the model is the only version where the special case
actually disappears.

**2. The parent object is the `platform` singleton, and the relation is named
`parent`.**

`platform` vs `tenant` is a non-decision today: `TFLIVE_TENANT_ID`
(`internal/config/auth.go:115`) is one fixed value per deployment, enforced by
the single-tenant boundary design, so platform and tenant are 1:1. If real
multi-tenancy ever arrives it invalidates far more than this tuple.

The relation is `parent`, not `platform`, so the tuple reads as a sentence:

```
{ user: "platform:tflive", relation: "parent", object: "stack:X" }
   → "platform:tflive is the parent of stack:X"
```

`define platform: [platform]` would make the rule read marginally better
(`admin from platform`) at the cost of a stuttering tuple. Tuples are what gets
read during an incident; `admin from parent` is still unambiguous in the model.

### The model, in full

This is the complete target model for the `platform` + `stack` stage — every
type declared, nothing elided. `template`, `credential_set`, and
`github_integration` bolt on later the same way, each with its own
`parent: [platform]`.

```
model
  schema 1.1

type user

type platform
  relations
    define root: [user]
    define admin: [user] or root
    define stack_creator: [user]

    define can_create_stack: admin or stack_creator

type stack
  relations
    define owner: [user]
    define operator: [user]
    define approver: [user]
    define viewer: [user]
    define parent: [platform]

    define can_view: owner or operator or approver or viewer or admin from parent
    define can_operate: owner or operator or admin from parent
    define can_approve: owner or approver or admin from parent
    define can_manage_access: owner or admin from parent
```

Three things to know about it:

- **`type user` is genuinely empty.** It declares `user:` as a valid object
  type and nothing more. That matches what we have today —
  `openfga/authorization-model.json:4-6` is literally `{"type": "user"}`.
- **`platform` is a singleton by convention only.** Nothing in the model
  prevents `platform:other`; we simply never write any ID but `platform:tflive`.
  The whole design leans on that, so it needs stating somewhere enforceable —
  most likely the bootstrap path and the port's object constructor.
- **`can_create_stack: admin or stack_creator` is the structural fix for #201.**
  It becomes the single definition of that question, so enforcement
  (`internal/app/service.go:53`) and the `/v1/me` projection
  (`internal/auth/me.go:36`) resolve the same check and cannot drift apart
  again. This is what §5 means by "#201 disappears rather than gets fixed."

Consequences, all of which need to land together:

- Stack creation writes **two** tuples, not one. The owner grant already goes
  through the queue (`grant_stack_owner_handler.go`); the parent edge needs the
  same transaction, or there is a window where a stack exists that no admin can
  see.
- The parent edge must **not** be writable through the grant API — see the
  three-bucket point in §7a. An endpoint that grants a viewer role must not be
  able to re-point a stack at a different platform.
- ~~Existing stacks need a one-off backfill.~~ Discharged by §0 — wipe and
  re-provision.
- `TestCanonicalModelAllowsDirectWritesOnlyToRoles`
  (`internal/openfga/model_test.go:141`) fails immediately: `parent` has direct
  types and is not one of the four roles. Its biconditional has to become a
  three-bucket registry.
- Admin stack listing inherits `ListObjectsMaxResults = 1000` and a 3s deadline
  (§7a). Acceptable now; it is a real ceiling.

**3. The port mirrors OpenFGA's tuple shape. Validation moves from the type
names into per-type constructors.**

```go
type CheckRequest struct {
    Subject  Subject   // opaque, validated
    Relation Relation  // opaque, validated against the object's type
    Object   Object    // {kind, id} — opaque, validated
}
```

Three slots, same as the wire.

*Why.* The port currently has four types that are secretly two. `Subject` and
`Stack` are the same struct with a different `kind` baked in (§1). `Role` and
`Permission` are the same string with different validation. Mirroring the wire
collapses them into `Object` + `Relation` and reduces the adapter to nearly a
passthrough — there is no translation left to get wrong.

*What must not be lost.* "Same shape as OpenFGA" must not become "three plain
strings." The port's whole value is that it refuses things, and the code says
so out loud:

> `Subject` — *"Its value is intentionally opaque so callers cannot construct
> unchecked IDs."*
> `Role` — *"Its value is intentionally opaque so derived permissions cannot be
> used as relationship-write targets."*

With plain strings, `authz.Grant` could hold `{platform:tflive, parent,
stack:X}` and the grant endpoint could write a structural edge. That is the
hazard, and it is not hypothetical — it is the exact tuple decision 2
introduces.

*Where the guardrails go instead.* `Relation` cannot stay one global enum,
because `can_view` is only legal on `stack` and `admin` only on `platform`.
Validity becomes *"is this relation legal for this object type, for this
purpose?"* — the per-type registry, holding the three buckets from §7a:

| Constructor | Admits | Used by |
|---|---|---|
| `GrantRelation(type, name)` | grantable roles only | the grant API — the only thing it can build |
| `CheckRelation(type, name)` | any relation of that type | authorization checks, **and** `confirm()`, which legitimately checks `owner` |
| structural (e.g. `parent`) | its own constructor | stack provisioning only; unreachable from the grant path |

`CheckRelation` is deliberately the permissive one. Checking a direct role is
legal in OpenFGA and `confirm()` genuinely needs it — verifying a `viewer`
write by checking `can_view` would prove nothing, since the answer could be
true via `owner` while the write failed.

*The simplification that falls out.* On the wire the `user` field and the
`object` field hold the same kind of value — `type:id`. Decision 2's tuple
proves it: `platform:tflive` sits in the `user` slot. So `Subject` is not a
separate concept; it is an `Object` that happens to be on the left, and can
simply wrap one. When groups arrive it gains an optional relation suffix
(`group:eng#member`) with no restructuring — which is why decision 9 below can
stay open without blocking this.

*Knock-on renames, all mechanical:* `Stack` → `Object`; `StackFromID` →
`ObjectFromID(kind, id)`; `ListAccessibleStacks` → `ListAccessibleObjects` (or
deleted — §3.4 says nothing calls it); `SubjectFromKeycloakSub` →
`SubjectFromOIDCSub` (§2).

**4. Root (#153) is a tuple, not a bypass — and it gets its own relation.**

Settles §3.3. The env-configured identity holds a real relationship in OpenFGA;
there is no second decision path.

*The product shape this serves,* stated by the user on 2026-08-20:

> As they install the app, they get a root user baked in. They can then log in
> as root, assign other users as admin, and use admin as the daily driver.

So root is a **recovery and bootstrap identity, not a working account** — which
is also what makes the `actor_type: root` audit marker in #153 worth having.

*Why a separate relation rather than reusing `admin`* — the permissions are
identical, so the question was whether the distinction earns its keep. It does,
because **boot-time seeding is mandatory regardless**: a fresh deployment has
zero admins, and granting admin requires being an admin, so nobody could ever
become one. Something must write the first tuple from config at boot no matter
what. Given that, the choice is only what relation it uses:

- Seed as `admin` — the API accepts a delete, then add-only reconcile silently
  restores it on the next restart. It *looks* revocable and is not. Worst
  outcome.
- Seed as `root` — the API refuses the delete outright. Same behaviour, stated
  honestly.

Rather than making root an ordinary `admin` tuple that Go refuses to delete,
`platform` gains a dedicated `root` relation with `admin` computed from it (see
the model above):

```
define root: [user]
define admin: [user] or root
```

The tuple is `user:<configured-sub> root of platform:tflive`, and admin follows
automatically. Three reasons this beats a flat `admin` tuple:

- `root` sits **outside the grantable bucket**, so per decision 3 the grant API
  cannot construct a mutation targeting it at all — the same structural
  protection the `parent` edge gets. Non-revocability stops being a runtime
  string comparison against an env var.
- Revoking someone's `admin` can never accidentally strike root; they are
  different relations.
- Root is *discoverable* — list the `root` tuples — instead of inferable only
  from configuration.

Operational details to settle when this is built:

- **Env var name.** #153 says "Keycloak subject"; after this epic it must be
  IdP-neutral. Something like `TFLIVE_ROOT_SUBJECT`.
- **Reconciled at boot, add-only.** Write the tuple if absent, every startup.
  Do **not** delete the previous root's tuple when the env value changes —
  that risks removing access from someone legitimately granted since. Demoting
  a former root is a deliberate operation, not a side effect of a config edit.
- **Fail closed at boot.** If the tuple cannot be written, the API should not
  start. Starting with no reachable admin is the worse failure.
- **No bootstrap ordering problem.** OpenFGA has no "create object" step —
  objects exist implicitly the moment a tuple references them, so
  `platform:tflive` needs no prior provisioning.
- This replaces the seeding currently done at `internal/keycloak/provisioner.go:142-155`,
  which creates the realm roles and assigns them. That path goes away with #197.

**On the apparent contradiction with "no exception":** the non-revocability is
enforced by our code, not by OpenFGA — so is this a second mechanism? No. The
invariant is *"OpenFGA answers every authorization question,"* not *"Go contains
no logic about authorization data."* A restriction on the **write** path is a
different category from a branch in the **decision** path. Every read of "may
this principal do X" still goes to OpenFGA, root included.

#### Two gaps this decision exposes, neither covered anywhere in the epic

**Gap A — nothing can grant `platform#admin` to anyone.** Grant endpoints exist
only for stacks (`internal/api/server.go:106-110`:
`GET`/`POST`/`DELETE /v1/tenants/{id}/stacks/{id}/grants`). #141 seeds the
*first* admin at bootstrap and stops there. Today the second admin is created in
the Keycloak console; #197 removes that console, and nothing replaces it. The
"assign other users as admin" step of the product shape above therefore has no
implementation path: it needs a platform-level grant API plus UI. Wants its own
issue.

**Gap B — how does the installer name the root subject?** tflive owns no
identity, so root is a `sub` from the customer's IdP: an opaque UUID the
operator must extract from their IdP console or a decoded token before the app
will admit them. That is a poor first-run experience for what is meant to be
the easy path.

| Option | Mechanism | Assessment |
|---|---|---|
| `TFLIVE_ROOT_SUBJECT=<sub>` | operator supplies the IdP subject | **Preferred.** Safe; needs a companion "how do I find my sub" answer |
| `TFLIVE_ROOT_EMAIL=<email>` | match the `email` claim at verification | **Reject.** Emails are mutable and often unverified; an IdP where a user can set their own email address hands out root |
| first-login-wins | first successful sign-in claims root | Zero config, but a stranger takes root if the app is reachable before the operator signs in |

Recommended direction: configure by `sub`, and solve discoverability separately
— e.g. log the subject on each sign-in, or surface it on a setup screen while no
root is configured, so the operator can copy it and restart. Also wants its own
issue.

**5. The API becomes the OIDC client. The browser stops holding an IdP token.**
(#216)

Today the SPA is a *public* client: it runs authorization-code + PKCE itself
(`web/src/auth/oidcConfig.ts`), holds the **access token**, and sends it as a
bearer (`web/src/api/client.ts:236`). Because the API validates an *access*
token, `aud` must be a resource identifier — which on Okta requires a custom
authorization server, which requires the **API Access Management** add-on. "Bring
your own IdP" currently means "and buy an Okta SKU."

Instead, follow ArgoCD (`util/oidc/oidc.go`, read at master): the server
redirects to the IdP (`:430`), receives the callback and exchanges the code
itself (`:574`), extracts and verifies the **ID token** (`:581`, `:587`), and
sets it as an httpOnly `SameSite=lax` cookie (`:638`). Refresh tokens stay
server-side, encrypted (`:620`). Requests are accepted from the cookie **or** an
`Authorization` header (`server/server.go:1666`), so a future CLI still works.

*Why it solves the problem.* An ID token's audience is the **OAuth client ID**,
verified by `coreos/go-oidc` at `oidc/verify.go:253`:

```go
if !slices.Contains(t.Audience, v.config.ClientID) {
```

Audience validation stays meaningful — a client ID is app-specific — but no
custom authorization server is needed. Okta's free org server works, and so do
Google, Dex, Entra ID, and Auth0. The "does not work" provider list collapses to
GitHub, which offers no OIDC for user login at all.

*Verified before committing to it:*

- **We are already single-origin.** The browser never reaches the API
  cross-origin: Vite proxies `/v1` in dev (`web/vite.config.ts`) and nginx
  proxies it in prod (`deploy/web/nginx.conf`). That is why `internal/api` has
  **no CORS handling anywhere**. Cookies work with no CORS work — the same
  topology ArgoCD relies on. This was the objection I expected to kill the plan;
  it does not apply.
- **go-oidc's audience check** as quoted above.

*Consequences:*

- `OIDC_AUDIENCE` and `OIDC_CLIENT_ID` become the same value, so the former can
  be dropped from config and derived.
- Scope must add **`offline_access`**; Okta issues no refresh token without it.
  Current scope is `openid profile email`.
- **One confidential client replaces two.** `tflive-web` (public) and
  `tflive-api` collapse into one, shrinking the IdP fixture and helping #197.
- Cookies put **CSRF in scope**; bearer-in-header was immune by construction.
  `SameSite=lax` is ArgoCD's answer and the right starting point, but it is new
  surface.
- `web/src/auth/` loses `oidc-client-ts`, PKCE handling, and token storage.

*A gap found in go-oidc while verifying this.* When `aud` holds multiple values,
OIDC requires an `azp` claim naming the client the token was actually issued to.
go-oidc does not check it, and says so in a comment above the audience check.
A token with `aud: [tflive, other]` and `azp: other` passes. Worth adding to
#196's list of strictness properties as a fifth item.

### Still open

6. **Accept the latency regression, or does it pull embedded OpenFGA in?** (§3.2)
7. **One generalized queue `Kind` for grants, or one per resource type?** (§1 —
   the *migration* half is discharged by §0; the design choice remains.)
8. **What shape should `/v1/me` be?** (§6) Not a compatibility question (§0) —
   purely what the frontend should be told once capabilities come from OpenFGA.
9. **Spec scope** — the port generalization plus platform singleton alone, or
   that plus the three new resource types in one design?
10. **Groups.** Deferred by the user on 2026-08-21. Decision 3 keeps the port
    ready (`Subject` wraps `Object`, gains a `#relation` suffix later), so this
    forces no rewrite. When it returns there are three options: tflive-native
    groups in OpenFGA (no IdP coupling, no licensing wall), reading a `groups`
    claim (**re-imposes the Okta API Access Management requirement** that
    decision 5 just removed — this is ArgoCD's documented caveat), or SCIM.
11. **Does the port take over chunking and idempotent writes** (`on_duplicate` /
    `on_missing`), or stay a thin translator? (§7a)

---

## 9. Roadmap for the identity layer (#210)

Ordered by dependency and risk, not by the epic's original listing.

| | Issue | Why here |
|---|---|---|
| 1 | **#195** — drop `typ: Bearer`, fix the `stringClaim` asymmetry | Fails closed. Nothing in this layer can be tested against any other issuer until it is gone. Small and self-contained. |
| 2 | **#196** — migrate to go-oidc | Deletes ~950 lines of the verifier. Do it *before* building on that code, not after. #216 is built on go-oidc's ID token verifier, so doing #196 second means writing the callback path twice. Security-critical with ~1180 lines of tests; gets its own slot, does not interleave. |
| 3 | **#216** — move the OIDC flow server-side | The topology change (decision 5). Everything downstream assumes it. |
| 4 | **#155** — identity projection | Before local accounts, so local users land in the projection through the ordinary path with no special-casing. |
| 5 | **#211** — local accounts, `iss`-routed | |
| 6 | **#212** — seed root | Closes the "how does the installer name the root subject" gap entirely: root is a local account with a known sub, so there is no "go find your subject in Okta" step. |
| 7 | **#213** — local login form | |
| 8 | **#198** — claim contract | Once the contract is actually settled, now including the local issuer as one of the providers. |
| 9 | **#197** — Keycloak extraction | Last. With local accounts covering the demo, this is cleanup rather than a blocker, and #216 collapses the fixture from two clients to one. |

**#199 stays backlogged.** The local stack already solves it correctly:
`KC_HOSTNAME` plus a Docker network alias (`docker-compose.yaml:45`, `:52`) make
`keycloak.localhost:8082` resolve from both the browser and the API container,
so issuer identity and discovery URL are the same string and the equality pin at
`oidc_provider.go:65` holds unrelaxed. In production a customer's IdP has a real
public URL reachable from both sides. #216 shrinks it further — only the API
calls the token and JWKS endpoints. It becomes real only where the API cannot
resolve the public issuer hostname: split-horizon DNS, or egress restrictions in
a locked-down cluster.

*I originally promoted #199 to second place on the grounds that a local issuer
would make the API fetch discovery from itself. That justification died with
decision 5 — the in-process key path never self-fetches. Recorded because the
same wrong reasoning is easy to repeat.*

### What a customer actually does, after #210

In their IdP — one client, whatever the vendor calls it (Okta "app
integration", Entra "app registration", Auth0 "application", Keycloak
"client"):

1. Create it as a **web application**, not a SPA — that is what makes it a
   confidential client with a secret
2. Grant types: authorization code + refresh token
3. Sign-in redirect URI: `<web-origin>/v1/auth/callback`
4. Assign the users or groups who should have access
5. Copy the client ID and secret

In tflive — four values:

```
OIDC_ISSUER_URL=https://acme.okta.com
OIDC_CLIENT_ID=0oa1b2c3...
OIDC_CLIENT_SECRET=...
OIDC_SCOPES=openid profile email offline_access
```

**What they do not do:** create a custom authorization server, buy Okta's API
Access Management add-on, configure an audience, or write claim mappers. The
issuer is the plain org URL with no `/oauth2/<id>` path.

The resulting ID token carries `aud` = the client ID and `sub` = the IdP's user
ID (`00u…` on Okta), which becomes `user:00u…` in OpenFGA tuples.

Then, to get a human administering: they sign in once — which lands them in the
projection (#155) — and root grants them `platform#admin` via #215. **Order
matters**: a user who has never signed in is not grantable. That is #155's
stated, accepted limitation and belongs in the documentation rather than being
discovered cold.

**Unverified:** whether Okta's *org* authorization server issues refresh tokens
for `offline_access`. Standard scopes work there, but the org server has
documented restrictions and this specific one was not confirmed. If it does not,
sessions end at ID token expiry rather than refreshing — degraded, not broken.

### Note on comparisons

ArgoCD is the right model for us because its topology matches: a server that is
both the OIDC client and the API, behind a single origin. Grafana is **not** a
useful comparison — it is an OAuth client that reads userinfo, in a different
position entirely. An earlier draft of this analysis cited Grafana and
Kubernetes as evidence for an ID-token-everywhere convention; the Kubernetes
claim was never verified and the Grafana one does not transfer. Decision 5
rests only on ArgoCD's source, go-oidc's source, and our own proxy
configuration.
