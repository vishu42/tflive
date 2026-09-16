# Issue backlog organisation

**Date:** 2026-09-16
**Status:** Resolved — all three decisions taken and applied on 2026-09-16

Survey of all 77 open issues on `vishu42/tflive`, to tie every issue to an epic
and retire the `model:*` labels.

## Done already

**The six `model:*` labels are deleted.** They were applied once, in a one-shot
triage (`docs/superpowers/specs/2026-07-20-issue-complexity-model-labels-design.md`,
whose own Non-goals say "one-shot analysis; may be revisited"), and covered 59
issues: 19 `750b`, 17 `500b`, 12 `1t`, 7 `1.25t`, 3 `250b`, 1 `1.5t`. Deleting a
label removes it from every issue, so the assignment is gone from GitHub. A
number→label snapshot was taken first and is reproducible from this table if it
is ever wanted back.

## How epics work here

Epics track children as markdown checklists under `## Issues` in the epic body.
GitHub's native sub-issues are **not** in use — `subIssues.totalCount` is 0 on
every epic checked. So "tie to an epic" means editing the epic body, and no
`epic:*` label exists.

Worth deciding separately whether to migrate to native sub-issues, which would
give rollup progress and a real parent field instead of a checklist that nothing
validates. Out of scope here.

## Current state

| | Count |
|---|---|
| Open issues | 77 |
| Epics | 9 |
| Open issues claimed by an epic | 22 |
| **Orphans** | **46** |

The nine epics: #146 UI/UX, #147 Observability, #148 Variable Input, #149 Runner
Isolation, #150 Drift Detection, #151 IAM, #156 GitHub Integration, #210 Identity,
#247 Fasten.

#156 is an epic with an empty checklist — its body says "Individual issues to be
broken out as the shape firms up", and #239 has since been filed squarely inside
its stated scope without being linked.

## Assignments to existing epics

These are unambiguous — each issue falls inside an epic's stated scope, and in
several cases the issue body already cross-references the epic's subject matter.

| Epic | Issues to add | Why |
|---|---|---|
| **#149 Runner Isolation** | #245, #246, #240, #172, #52 | The whole executor-hardening cluster. #245 (sandbox per run) and #246 (Temporal mTLS + authorizer) already cross-reference each other and #240; #172 is the environment allowlist that #245 lists as its requirement 5; #52 is world-readable `/tmp` run and artifact roots. |
| **#156 GitHub Integration** | #239 | The epic's Scope names "Private repository access" as its first bullet. #239 is that issue. |
| **#151 IAM** | #244 | Last-owner protection on stack roles is an OpenFGA-model question. |
| **#210 Identity** | #237, #94 | #237 bounds argon2id concurrency on the local login route (#211 lives in this epic). #94 is the duplicate Keycloak admin env var pair, which pairs with #197's Keycloak extraction. |
| **#146 UI/UX** | #168, #176, #101 | Screen comprehension, stacks-list pagination, and the last open `UI-0xx` route-guard bug. |
| **#147 Observability** | #44, #47 | Health endpoint that checks no dependency; S3 credentials not redacted the way the OpenFGA token is. |
| **#247 Fasten** | #55 | Leaked temp directories on activity retry is per-run disk waste, which is what this epic is about. Sits next to its existing unfiled "Run workspaces are never deleted" item. |

**#149 needs its body rewritten regardless.** It currently reads "the current
runner executes Terraform inside the worker process with shared credentials",
which `feat/control-plane-split` has made false — the executor holds no database
URL and no keys, and credentials arrive sealed to a per-run key. The epic's
purpose survives the split (isolation between *tenants* rather than between
tenant and platform) but its framing does not.

## The 28 issues with no epic to go to

Three clusters, none of which any existing epic covers.

### A. API edge — #37, #38, #39, #43, #45, #46, #49, #53, #57

Everything between the socket and the handler: no rate limiting, no
`ReadHeaderTimeout`, no request body cap, no field validation, an auth middleware
that default-allows any non-`/v1/` path, two different error schemas, discarded
JSON encode errors, and `io.ReadAll` on state upload.

These were filed as one audit batch and share a shape: individually small,
collectively the difference between "runs on a laptop" and "faces a network".

### B. Run correctness — #36, #40, #41, #42, #54, #56, #61, #241

Temporal and outbox semantics: missing heartbeats and timeouts, unbounded outbox
retries, stores that silently succeed on a no-op, approval/cancel TOCTOU, a
missing `WorkflowIDConflictPolicy`, cancellable post-terminal runs, replay
double-execution, and activity failures that bypass the retry policy by returning
`(output, nil)`.

**#247 explicitly refuses #36**: its "Explicitly not in this epic" section says
activity timeouts and heartbeats are "a correctness bug, not a density one, and
belongs in its own issue". #36 is that issue, and this is the epic it belongs to.

Two of these are also live findings from the `feat/control-plane-split` review,
which re-derived them independently: #36's timeout is now 10 minutes with
`MaximumAttempts: 1`, not the 60 seconds the issue text still claims.

### C. Datastore and deployment hygiene — #48, #50, #51, #58, #59, #60, #158

Schema and operational shape: no foreign keys, no advisory lock on the migration
runner, an unbounded `last_error` column, one-at-a-time variable inserts,
hardcoded Postgres credentials and unpinned `minio:latest` in compose, and the
legacy `DefaultCredentialIDs` path still to be removed.

### Left over

- **#152** (configurable `tofu`/`terraform` binary) fits none of the three. It is
  about *which* runner binary executes a run, which is the same axis as #138
  (KubernetesJobRunner). Proposal: widen #149 from "Runner Isolation" to cover
  runner pluggability and put both there.
- **#187** is a reference issue by its own first line — "Recorded here because
  #179 is closed and this is where the next person will look." It is
  documentation of deliberate boundaries, not work. It should stay epic-less;
  forcing it into one misrepresents it.

## Stale issues found on the way

Flagged rather than acted on, because each is a close-or-rewrite call.

**#43 (No CORS configuration for production)** contradicts a decision since
recorded in #210: "the browser never reaches the API cross-origin. Vite proxies
`/v1` in development and nginx proxies it in production, which is why
`internal/api` carries no CORS handling at all." #43 reads as a gap; the
architecture says it is a deliberate absence. Either close it won't-do citing
#210, or rewrite it as "document why there is no CORS layer". Note #26's
acceptance criteria still require CORS to be "explicitly configured", so the two
need to agree.

**#36** has stale specifics: it says `StartToCloseTimeout: time.Minute` for all
activities. It is now 10 minutes on `RunTerraform` with `MaximumAttempts: 1`.
The bug is real and arguably worse than described — a failed apply is not
retried — but the text needs updating before anyone works it.

**#61, #55, #52, #41** all cite pre-split file paths and line numbers
(`cmd/worker`, `DefaultWorkerRunRoot`). The underlying findings survive; the
coordinates do not.

**#24–#28 (AUTH-022 … AUTH-026)** are the tail of a July 2026 auth sprint:
backend integration tests, frontend journeys, secrets/transport hardening,
runbooks, and a release gate. The sprint's architecture has since been replaced
twice over — app-owned sessions (#216), local accounts (#211), Keycloak leaving
the application (#197) — so their acceptance criteria describe a system that no
longer exists. Several criteria are Keycloak Admin Console-shaped. They are not
worthless: the *intent* (an auth test suite, runbooks, a hardening pass) is
still unmet.

## Decisions taken

All three were approved and applied on 2026-09-16.

**1. Three epics created.**

- **#250 [Epic] API Edge — hardening between the socket and the handler** — #37, #38, #39, #45, #46, #49, #53, #57
- **#251 [Epic] Run Correctness — Temporal, activity and outbox semantics** — #36, #40, #41, #42, #51, #54, #56, #61, #241
- **#252 [Epic] Datastore and Deployment Hygiene** — #48, #50, #58, #59, #60, #158

#51 moved from cluster C to #251: it is an outbox column, and it belongs with the
rest of the outbox rather than with schema hygiene.

**#149 widened** from "Runner Isolation" to "[Epic] Runner — isolation and
pluggability", taking #152. Which binary executes a run and what it executes in
are one question once a sandbox exists, since the sandbox image is where a pinned
`tofu` or `terraform` version comes from.

**2. #24–#28 closed as superseded**, each with a comment recording what survives.
The pattern that mattered: these were not closed because the work is done, but
because the specifications had become misleading about what the work now is.

Three pieces of intent are now **untracked** as a result, and were called out in
the close comments rather than silently dropped:

| Orphaned intent | From | Where it should go |
|---|---|---|
| Refuse to start with insecure configuration | #26 | #250 |
| Bounded timeouts and fail-closed responses on auth dependency outages | #26 | #250 |
| An automated release gate — clean-checkout build, green suite, frontend production build | #28 | No epic owns CI; standalone |

The larger survivors — an auth integration suite and end-to-end sign-in journeys
(#24, #25), and operator runbooks (#27) — were deliberately not refiled, because
their right scope depends on where #210 lands. Refiling them now would produce
tickets needing a rewrite before anyone read them.

**3. #43 closed won't-do.** The API is never reached cross-origin by design; the
proxy topology is recorded in #210, and adding permissive CORS to a
cookie-authenticated API is how CSRF returns. The real gap underneath it — the
proxy requirement is undocumented — is a deployment-docs question. #26's matching
CORS criterion closed in the same pass, so the two no longer disagree.

## Final state

| | Before | After |
|---|---|---|
| Open issues | 77 | 74 |
| Epics | 9 | 12 |
| Orphans | 46 | 1 |

The one remaining orphan is **#187**, which stays epic-less on purpose: it is a
reference issue documenting the deliberate boundaries of the #179 state model,
not work to be done.

Also corrected in the same pass: eight completed children (#128–#131 in #146,
#141/#145/#209/#214 in #151) were still showing as unticked boxes, so both epics
read as further from done than they are.

## Still worth doing

- **#149's children have stale titles.** #240 still says "after
  scrubConsumedSecret", a function that no longer exists, and names the GitHub App
  key that is no longer on the executor. Its scope narrowed to the executor's
  in-process memory; a comment on it says so, but the title does not.
- **#36's text understates it.** It claims a 60-second timeout on all activities.
  It is 10 minutes on `RunTerraform`, paired with `MaximumAttempts: 1`, so the
  failure mode is worse than described: not retried, and infrastructure keeps
  mutating after the activity is marked Failed.
- **#61, #55, #52, #41** cite pre-split coordinates that will not resolve.
- **Native sub-issues.** Epics are markdown checklists that nothing validates —
  this survey found eight stale ticks and would not have found a typo'd number.
  GitHub's sub-issues would give a real parent field and rollup progress.
