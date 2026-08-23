# #214 — generalizing the authz port: considerations before the design

Feeds the design for [#214](https://github.com/vishu42/tflive/issues/214).
Written 2026-08-22, before any code.

> **Superseded in part.** §§1–3 below were overturned the same day by a probe
> against the real model — see the postscript. The reasoning is kept because it
> is what led to the probe, but the current position is the postscript's, and
> [the design](./2026-08-22-authz-port-generalization-design.md) follows that.

The target *shape* is not open. Decision 3 of
[the IAM surface analysis](./2026-08-20-iam-openfga-surface-analysis.md) settled
it: three slots mirroring OpenFGA's tuple, `Subject` wrapping `Object`, one
`Relation` type with a per-type registry replacing the `Role`/`Permission`
split. This document does not re-litigate any of that.

What is open is everything the shape does not determine: where the registry
lives, how the write path stays un-abusable, where #214 stops and #141 starts,
and which of the two loose threads the issue mentions get pulled here.

---

## 1. Where the registry lives, and whether it is derived

Decision 3 says "per-type registry" and stops. Three places it could go, and
they are not equivalent.

**(a) Hand-written table in `internal/authz`.** Provider-neutral, reads well,
sits next to the constructors that consume it. It also duplicates
`openfga/authorization-model.json`. Drift is silent in the bad direction: add a
relation to the model and forget the registry and the port refuses a legal
check; delete one from the model and keep it in the registry and the port
happily builds a tuple OpenFGA rejects with a 400, which `classify()` maps to
`ErrMalformedResponse` → **503**. A model edit becomes an availability incident
two layers away.

**(b) Derived from the model JSON at init, in `internal/openfga`.** No drift by
construction. The derivation rule is not the current biconditional — that is
what breaks on `parent` — but it is still mechanical: a relation whose
`directly_related_user_types` are *subject* kinds (`user`, later
`group#member`) is grantable; one whose direct types are *object* kinds
(`platform`) is structural; one with no direct types is derived-only. That rule
survives groups arriving, because groups extend one list.

The cost is that the registry stops being neutral. `internal/authz` would
either import the OpenFGA model or receive the registry by injection from the
adapter, and the port's guardrails would then be configured by the provider
they exist to constrain.

**(c) Hand-written in `internal/authz`, with a test in `internal/openfga`
asserting registry and model agree.** Registry stays neutral and readable;
drift is caught at `go test` rather than in production.

**Recommendation: (c).** Not merely as a compromise — (b) has a specific defect
that is easy to miss. `TestCanonicalModelAllowsDirectWritesOnlyToRoles`
currently earns its keep by asserting one hand-written statement (the four role
names) against an independent one (the model file). If the registry is
*derived* from the model, that test becomes vacuous: it asserts the model
against itself and can never fail. The invariant only has force while the two
statements are written independently. The issue says the invariant "moves to
the registry"; it is more accurate to say it becomes an equivalence test
*between* the registry and the model, and that is strictly stronger than what
we have now, because it also covers `platform` and every type #142–#144 add.

---

## 2. How the write path stays un-abusable

The stated hazard is concrete: with plain strings, `authz.Grant` could hold
`{platform:tflive, parent, stack:X}` and the grant endpoint could write it,
re-pointing a stack at a different platform. Three buckets on `Relation` are
necessary but not sufficient — the question is what the *write* types accept.

**(a) One `Tuple` type; `Mutation` holds `[]Tuple`.** Guarding happens at
`Relation` construction: an HTTP handler parsing a user-supplied role string
can only reach `GrantRelation`, so it can never obtain a structural relation to
put in a tuple. Simple, one write path in the adapter.

The weakness is that the protection is a property of *which constructor the
call site happened to call*, not of the type the endpoint holds. Code review is
the enforcement. A year from now someone adds a helper that takes a `Relation`
parameter, and the guarantee is gone with no type error.

**(b) Two write paths.** `Grant`/`Mutation` accept grantable relations only, by
construction; structural edges go through a separate constructor and a separate
adapter method. The grant endpoint holds a `Mutation` that *cannot express*
`parent` — the compiler enforces what review enforces in (a).

The cost is a second write path in the adapter, and `WriteRelationships` /
`DeleteRelationships` growing siblings.

**Recommendation: (b), but with the split at the type boundary only.** Both
paths can funnel into the same private `adapter.write()`; what differs is the
public constructor and the validation in front of it. That is perhaps 40 extra
lines and it converts the central safety property of this refactor from a
convention into a compile error.

**This is the one place #214 must anticipate #141.** Nothing writes a `parent`
edge until #141, so the structural path ships unexercised unless §3 resolves
otherwise. Designing an untestable guardrail is a real risk, and it is the
strongest argument for §3's option (b).

---

## 3. Where #214 stops and #141 starts

The issue sequences #214 ahead of #141, which is right for the *port*. But
three of #214's own deliverables have no member to act on until #141 exists:
the structural bucket is empty, `TestCanonicalModelAllowsDirectWritesOnlyToRoles`
still passes unchanged, and the registry knows exactly one object type.

**(a) Pure shape.** Rename and collapse types; registry has `stack` only; the
structural bucket is declared and empty. Smallest diff, but ships a guardrail
against a threat that does not yet exist and cannot yet be tested.

**(b) Shape, plus the `platform` type in the model — and nothing else.** Add
`type platform` with `root` / `admin` / `stack_creator` / `can_create_stack`,
and `parent: [platform]` plus the `admin from parent` terms on `stack`, exactly
as decision 2 specifies. Change **no enforcement**: Go still reads realm roles,
nothing writes a `parent` tuple, nothing writes a `platform#admin` tuple.

The model change is inert by construction — `admin from parent` resolves
through zero stored tuples, so every check returns exactly what it returns
today. But the registry now has real members in all three buckets, the
registry↔model equivalence test from §1 has something to prove, and §2's
structural path has a relation to be tested against. #141 then reduces to
"write the tuples and move enforcement," and #145 to "delete the realm role
reads."

**(c) #214 + #141 together.** Rejected: couples a mechanical port refactor to a
behavioural authorization change, and the combined diff would be
unreviewable.

**Recommendation: (b).** The extra cost is ~30 lines of model JSON, a
provisioner run, and some `.fga.yaml` cases. The return is that every guardrail
#214 introduces becomes testable in the same change that introduces it.
[§0 of the surface analysis](./2026-08-20-iam-openfga-surface-analysis.md) removes the usual objection:
adding a type to the model costs a wipe and re-provision, not a migration.

Worth confirming before committing: that the provisioner handles a model with
multiple types cleanly, and that `admin from parent` with no tuples is inert in
the `.fga.yaml` suite rather than merely in argument.

---

## 4. `ListAccessibleStacks` — delete

The issue asks for this to be resolved either way. The evidence points one
direction.

- No production caller. Six test doubles implement it to return an empty
  result: `cmd/api/main_test.go:472`, `cmd/worker/main_test.go:502`,
  `internal/app/stack_authorization_test.go:168`, `internal/app/service_test.go:2034`,
  `internal/app/authorization_test.go:279`, `internal/api/server_test.go:2582`,
  plus its own adapter test.
- **It does not become useful after #141 either.** The tempting argument is that
  decision 1 puts admin listing through `ListObjects`, so the method finds its
  caller then. It does not: `app.listAccessibleStacks` must join against
  `stacks` rows for tenant scoping and stable ordering, and page them. Bare
  object IDs capped at `ListObjectsMaxResults = 1000` behind a 3s deadline do
  not serve that. After #141 removes the platform-admin short-circuit, *every*
  user goes through the existing candidate-page + `BatchCheck` path — which is
  what the code already does and which never calls this method.

Deleting it also removes seven stub implementations from test doubles in a
change that is already going to churn those files heavily. Carrying it into a
generalized port would multiply it per object type, for nothing.

If a caller ever appears, it comes back as `ListAccessibleObjects` with a
design that accounts for the 1000/3s ceiling.

---

## 5. The 13-stack bug: fix here, or its own issue?

`ResolveStacksCapabilities` (`internal/app/authorization.go:230`) builds
`4 × len(stacks)` checks and sends them in one unchunked `BatchCheck`. OpenFGA
enforces `MaxChecksPerBatchCheck = 50` and returns 400; `classify()` maps that
to `ErrMalformedResponse` → **503 `authorization_unavailable`**. So
`GET /v1/tenants/{id}/stacks` fails at **13 accessible stacks**, today, for
ordinary users — it reaches `internal/api/server.go:349` on every list request.
Platform admins are masked by the `isPlatformAdmin` short-circuit, which is
precisely what #141 removes.

Note that `confirm()` already chunks, at 25 (`maxConfirmationChecks`). The limit
was known in one place and missed in the other, which is the argument for
decision 11 in general: **the port should own OpenFGA's limits, because "every
caller remembers" is what produced this.**

Against folding it in: it is a bug, not a refactor, and mixing a live fix into
a large mechanical change makes both harder to review and to revert.

**Recommendation: fix it in this change, but file it as its own issue first so
it is tracked independently.** The fix belongs in the adapter's `BatchCheck`
(chunk at 50, preserve caller ordering across chunks — the correlation-ID
bookkeeping already exists), it is small, and #214 rewrites every one of these
call sites anyway. Leaving a known 503 in code you are actively rewriting is
the worse trade. The regression test is a 13-stack (51-check) case, and it
should be written first and observed failing.

The wider half of decision 11 — `on_duplicate: "ignore"` / `on_missing:
"ignore"` replacing the confirm-after-failure dance — stays **out**. The
analysis flags openfga#3201 as open: `on_duplicate: "ignore"` still 409s on
concurrent first-time writes, which is exactly the grant handler's
at-least-once race. Chunking is a limit the port should hide; idempotent-write
flags are a behaviour change that needs its own justification.

---

## 6. Test strategy

The mechanical churn is large and mostly uninteresting:
`internal/openfga/authorization_adapter_test.go` (724 lines, stack-shaped),
`internal/authz/authorization_test.go` (6 tests on the closed enums), and every
`authz.Stack` / `authz.Permission` literal across `internal/app`,
`internal/api`, and both `cmd` test files.

Three things are worth deciding rather than defaulting:

- **Registry↔model equivalence test** (§1) — replaces
  `TestCanonicalModelAllowsDirectWritesOnlyToRoles` and is the load-bearing new
  test in this change.
- **Type-level negative tests** (§2) — assert that a structural relation cannot
  be constructed through the grant constructor, and that the grant API's types
  cannot express a `parent` edge. These are the tests that prove the refactor
  achieved its stated purpose rather than merely renaming things.
- **`.fga.yaml` cases for the `platform` type** if §3(b) is taken, including
  the "inert with no tuples" assertion. Note [#208](https://github.com/vishu42/tflive/issues/208):
  both model-semantics test mechanisms are opt-in and never run in CI, so cases
  added here buy nothing until that is fixed. Worth knowing whether #208 should
  come first.

---

## Summary of recommendations

| § | Question | Recommendation |
|---|---|---|
| 1 | Registry location | Hand-written in `authz`; equivalence test in `openfga`. Not derived — derivation makes the invariant vacuous. |
| 2 | Write-path guarding | Two typed write paths sharing one private adapter write. Compile error, not convention. |
| 3 | Scope boundary | Port shape **plus** the `platform` type in the model, inert. No enforcement change. |
| 4 | `ListAccessibleStacks` | Delete. It has no caller now and gains none at #141. |
| 5 | Chunking | Fix the 13-stack bug here, under its own issue. Idempotent-write flags stay out. |
| 6 | Tests | Equivalence test and type-level negative tests are the real deliverables; check #208 ordering. |

## Open, and needing your call

- **§3 is the decision that shapes the rest.** If the answer is (a) pure shape,
  then §2's structural path and §1's equivalence test both ship untested, and
  the case for §2(b)'s extra 40 lines weakens considerably.
- **#208 ordering** — whether model-semantics tests should be made to actually
  run before this change leans on them.


---

## Postscript — §§1–3 overturned by evidence (2026-08-22)

Everything above §4 rests on one assumption I never tested: that the port's
refusals are refusals *only the port* can make. Asked whether the design was
overengineered, I probed the four hazards against the candidate model with the
`fga` CLI instead of arguing:

| Tuple | OpenFGA's own verdict |
|---|---|
| `user:alice can_view stack:X` — write to a derived relation | **REJECTED** |
| `user:* viewer stack:X` — typed wildcard, grants everyone | **REJECTED** |
| `user:alice#member viewer stack:X` — userset injection | **REJECTED** |
| `platform:tflive parent stack:X` — structural edge | **ACCEPTED** |

Three of four are enforced by the server already, against the model itself and
therefore without drift. The `Role`/`Permission` split that §1 set out to
generalize duplicates a check OpenFGA does natively and better.

That collapses the design:

- **§1 (registry location) is moot.** There is no registry. The two closed enums
  become one `Relation` type plus a nine-line allowlist of grantable names.
- **§2 (write-path guarding) shrinks to that allowlist.** The three buckets were
  a classification of *why* a relation may not be written; only one reason
  survives contact with the server, so naming them earned nothing. `derived` and
  `structural` both reduce to "not in the allowlist."
- **§3 (scope boundary) reverses to (a), pure shape.** I recommended pulling the
  `platform` type into #214 *because* it made the registry's guardrails
  testable. With no registry, the reason is gone and the model change returns to
  #141.

Two smaller things the probe changed:

- The §1 argument that the bucket is *underivable* (because `platform#root` and
  `stack#viewer` are indistinguishable in the model) was correct and is now
  irrelevant — it argued about a table that no longer exists.
- It surfaced a real gap in the **existing** code, independent of any of this:
  `canonicalIdentifier` rejects `:`, whitespace, and control characters but
  allows `#` and `*`, the two characters that change a tuple's meaning. OpenFGA
  fails closed on both today, but only because our model declares `[user]`
  rather than `[user:*]` and `user` has no relations — both model facts that can
  change. The design tightens the check.

**§§4–6 stand as written.** `ListAccessibleStacks` is still deleted, #220 is
still fixed here, and #208 still does not block — this repo has no CI workflows
at all, so nothing load-bearing may depend on `.fga.yaml` running.

The lesson worth keeping: the hazard list came from decision 3, which inherited
it from the code's own comments, and three of its four entries had been
obsolete since the day the model gained type restrictions. A guardrail's
justification decays; probing costs minutes.
