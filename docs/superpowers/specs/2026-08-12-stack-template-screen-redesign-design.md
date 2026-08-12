# Stack template screen redesign

Date: 2026-08-12

## Problem

`/stacks/:stackId/template` is hard to make sense of (issue #168). Three
distinct defects share one root cause.

**The side panel shows raw database IDs.** `InstalledTemplatePanel` renders
`source_template_id`, `desired_template_revision_id`,
`last_applied_template_revision_id`, and `last_applied_run_id` verbatim. None
of them mean anything to a user (issue #129).

**One dropdown does three unrelated jobs.** The "Template revision" select
lists every revision in the tenant, and the selection simultaneously:

1. chooses a template to install,
2. chooses an upgrade target — but only when the picked revision happens to
   share a `source_template_id` with the installed template, which
   `canUpgradeStackTemplate` checks silently, and
3. re-targets the variables form, because `variables` are read from the
   selected revision rather than the installed one.

Pick a revision from a different source template and the primary action
silently changes from Upgrade to Install, with no explanation.

**The install button is always present.** It renders whether or not a
selection makes installing meaningful, including when the user is looking at
an already-installed template.

Underneath all three: the screen conflates *configuring what is installed*
with *installing something new* with *moving to a different revision*. These
are three operations with three different request payloads sharing one form,
switched by state the user cannot see.

## Goals

- The template screen configures the installed template. Nothing else.
- Installing a template is a deliberate, separate flow.
- Upgrading is a deliberate, separate flow that shows what actually changes.
- Delete the raw-ID panel.

## Non-goals

- The installed-templates list on the left is **unchanged**, including its
  `stackTemplateLabel()` output and the raw `desired_template_revision_id`
  line under each row. Issue #128 covers that and stays open. This leaves a
  known inconsistency: the right column will carry no raw IDs while each list
  row still shows one.
- The runs screen (issue #130) is untouched by this spec.
- Complex/sensitive variable input (epic #148) is untouched.

## Routes

Both new screens nest under the existing `template` segment, inside
`StackDetailShell`, so the stack header and tabs stay visible for context.
The segment stays singular to avoid introducing a second noun.

```
stacks/:stackId                          RequireCapability canView   (route)
  StackDetailShell
    template                             StackTemplateScreen
    template/new                         RequireCapability canOperate (route)
                                           → AddStackTemplateScreen
    template/:stackTemplateId/upgrade    RequireCapability canOperate (route)
                                           → UpgradeStackTemplateScreen
    environment | runs | runs/:runId | access        unchanged
```

Install and upgrade are gated as **route guards** (`mode="route"`, so a denied
user gets `AccessDenied`). The existing in-place locked-panel gate
(`mode="gate"` with a `disabledReason` fallback) is retained only for Save
config on the detail screen, which is still worth showing read-only.

### Selection moves to the URL

`StackTemplateScreen` currently holds the selected stack template in
`useState`. It moves to a `?selected=<stackTemplateId>` search param, matching
the pattern already used by `TemplateRegistryScreen`. This is what lets the
install and upgrade screens return the user to the template they just acted
on, and it makes a selected row survive a reload or a shared link.

Fallback order for the rendered selection is unchanged in spirit: the
`?selected=` template if it exists in the stack, otherwise the first installed
template, otherwise none.

## Screen 1 — StackTemplateScreen (rewritten)

`chosenRevisionID` is deleted. The screen always renders the installed
template's `desired_template_revision_id`. There is no revision selector and
no install button.

```
┌────────────────────────┬──────────────────────────────────────┐
│ Stack templates        │ ✓ Up to date        │  or:  ↑ Update │
│              [+ Add]   │                     │  available     │
│                        │                     │   [ Upgrade ]  │
│ meg_prod_a4de3e48      ├──────────────────────────────────────┤
│   @ main (active)      │ Variables                            │
│   template_e4f7614…    │   cidr_block   [ 10.0.0.0/16      ]  │
│                        │   region       [ us-east-1        ]  │
│ meg_prod_9c1188fa      │                      [ Save config ] │
│   @ main (active)      ├──────────────────────────────────────┤
│   template_3c8f11d…    │ Template credentials                 │
│                        │ Overrides the stack environment for  │
│ (unchanged — #128)     │ this template only.                  │
│                        │   AWS_PROFILE                [ × ]   │
└────────────────────────┴──────────────────────────────────────┘
```

State: `editedValues`, `errorMessage`. Selection comes from the URL.

Queries:

- `useStackQuery(tenantID, stackId)` — unchanged.
- `useTemplateRevisionVariablesQuery(tenantID, installedTemplate.desired_template_revision_id)`
  — now pinned to the installed revision rather than a selected one.
- `useTemplateRevisionsQuery(tenantID)` — **retained with a new job**. It no
  longer feeds an install picker; it answers only "does a newer active
  revision of this template's source template exist?", which drives the
  up-to-date / update-available cue. The query key is shared with other
  screens, so this is usually a cache hit.

The cue renders in three states, derived from `upgradeCandidateRevisions()`:

| Condition | Render |
|---|---|
| revisions query pending | nothing (no flash) |
| no candidates | `✓ Up to date` |
| one or more candidates | `↑ Update available` + Upgrade link to `template/:stackTemplateId/upgrade` |

The Upgrade link and the `+ Add template` link are both wrapped in a
`canOperate` gate so a viewer does not see actions they cannot take; the route
guards are the actual enforcement.

The credentials panel keeps its current scope (stack-template-scoped
overrides) and moves under the variables form, retitled **"Template
credentials"** with the subtitle "Overrides the stack environment for this
template only." The previous title, "Environment", collided with the
stack-scoped Environment tab and contributed to #168.

Empty state (no installed templates): the right column shows a short prompt
and the same `+ Add template` action, rather than an empty variables form.

## Screen 2 — AddStackTemplateScreen

Route `template/new`. One screen, no wizard state.

```
┌─────────────────────────────────────────────┐
│ ← Back        Add template to prod-vpc      │
├─────────────────────────────────────────────┤
│ hashicorp/terraform-aws-vpc              2  │
│   ● main · 9f21abc                   active │
│     v1.2.0 · a4de3e4                 active │
│ my-org/rds-module                        1  │
│     main · 3c8f11d                   active │
├─────────────────────────────────────────────┤
│ Variables                                   │
│   cidr_block *  [                        ]  │
│   region     *  [                        ]  │
│                              [ Install ]    │
└─────────────────────────────────────────────┘
```

The picker reuses `groupTemplateRevisionsByRepository()` and
`templateRevisionRefLabel()` so choosing a template here looks like browsing
`/templates`. Only `status === "active"` revisions are selectable, matching
the existing `canInstall` condition; non-active revisions render disabled with
their status shown.

The variables form is absent until a revision is picked, then renders
`VariableFields` for that revision with empty values.

Install calls `useAddTemplateToStackMutation` with
`{ template_revision_id, selected_ref, config }` — the same payload the
current screen builds — then navigates to `../template?selected=<newId>`.

Branches: pending, error with retry, and an empty state when the tenant has no
registered templates (linking to `/templates/new`), following the structure of
`TemplateRegistryScreen`.

## Screen 3 — UpgradeStackTemplateScreen

Route `template/:stackTemplateId/upgrade`. This is where the current design
hides the most behavior, so the screen states it explicitly.

Candidates are active revisions of the *same source template*, excluding the
current desired revision — the constraint `canUpgradeStackTemplate` already
enforces but never surfaces. They are offered in a `<select>` holding only
those candidates. The API returns revisions newest-first, so the first
candidate is the newest and is preselected; changing the selection re-runs the
variable diff below.

Because `upgradeStackTemplate` sends a full `config` alongside
`target_template_revision_id`, the screen loads variables for **both** the
current and the target revision and shows the difference:

```
Upgrade  meg_prod_a4de3e48
main · a4de3e4  →  [ main · 9f21abc  ▾ ]

  + new_var       [                  ]     new in this revision
    cidr_block    [ 10.0.0.0/16      ]     carried over
    region        [ us-east-1        ]     carried over
  − legacy_flag                            no longer used, will be dropped

                                    [ Upgrade ]
```

- **added** — present in target, absent in current. Empty input, marked new.
- **carried** — present in both. Prefilled from the installed config.
- **removed** — present in current, absent in target. Rendered read-only and
  greyed, labelled as dropped. Not sent in the config.

The submitted config is built by `configFromVariableValues(targetVariables,
values)`, so removed variables are excluded structurally, not by convention.

On success, navigate to `../../template?selected=<stackTemplateId>`.

Empty state: when there are no candidates, the screen renders "Already on the
latest revision" with a link back, and no form.

## Component inventory

| File | Change |
|---|---|
| `InstalledTemplatePanel.tsx` | **deleted** |
| `VariablesPanel.tsx` | **deleted** — its three-action form is the thing being dismantled |
| `VariableFields.tsx` | **new** — presentational only: variables, values, onChange, disabled. Shared by all three screens |
| `StackTemplateConfigPanel.tsx` | **new** — `VariableFields` plus a single Save config action and the `disabledReason` locked state |
| `AddStackTemplateScreen.tsx` | **new** |
| `UpgradeStackTemplateScreen.tsx` | **new** |
| `StackTemplateScreen.tsx` | shrinks — loses `chosenRevisionID`, the revision dropdown, and the install path |
| `app/router.tsx` | two new guarded routes |

Each new component has one job and a small prop surface: `VariableFields`
knows nothing about mutations, `StackTemplateConfigPanel` knows nothing about
revisions, and the two flow screens own their own mutation and navigation.

## Pure functions (`stackWorkflow.ts`)

```ts
upgradeCandidateRevisions(
  revisions: TemplateRevision[],
  stackTemplate: StackTemplate | null
): TemplateRevision[]
// active revisions sharing source_template_id, excluding the current
// desired revision; input order (newest-first) preserved.

partitionUpgradeVariables(
  currentVariables: TemplateVariable[],
  targetVariables: TemplateVariable[]
): { added: TemplateVariable[]; carried: TemplateVariable[]; removed: TemplateVariable[] }
// compared by variable name.

canSaveStackTemplateConfig(
  stackTemplate: StackTemplate | null,
  variables: TemplateVariable[],
  variableValues: Record<string, string>
): boolean
// signature change: the templateRevision parameter and its
// "selected revision equals desired" guard are removed, because the
// screen no longer has a revision selector to disagree with.
```

`canUpgradeStackTemplate` is retained — `upgradeCandidateRevisions` uses the
same rule, and the upgrade screen uses it to validate the chosen target before
submitting.

## Error handling

Unchanged patterns. Query failures go through `useQueryErrorBoundary` with a
retry button; mutation failures set a local `errorMessage` rendered in an
`.alert`, via the existing `runAction` wrapper. Each new screen implements the
same pending / error / empty branches the feature screens already use.

## Testing

Unit tests (`stackWorkflow.test.ts`):

- `upgradeCandidateRevisions` — filters by source template, drops the current
  desired revision, drops non-active revisions, preserves order, handles a
  null stack template.
- `partitionUpgradeVariables` — added, carried, removed, and the all-identical
  and disjoint cases.
- `canSaveStackTemplateConfig` — updated for the new signature.

Component tests:

- `AddStackTemplateScreen` — variables form hidden until a revision is picked;
  non-active revisions not selectable; Install posts the expected body;
  navigates back with `?selected=`; empty-registry state.
- `UpgradeStackTemplateScreen` — only same-source candidates listed; newest
  preselected; added/carried/removed each render distinctly; Upgrade posts
  `target_template_revision_id` plus a config excluding removed variables;
  "already on the latest revision" state.
- `StackTemplateScreen` — install button and revision dropdown are **gone**;
  raw IDs are **gone**; Save config is the only form action; up-to-date vs
  update-available cue; credentials panel titled "Template credentials";
  `?selected=` drives the rendered template.
- `router.test.tsx` — the two new routes resolve and are capability-guarded.

Validation: `npm test` and `npm run build` from `web/`.

## Issue impact

- Closes #129 — the raw-ID panel is deleted rather than rewritten, so the
  issue's proposed fix is obsolete.
- Contributes to #168.
- #128 stays open by explicit decision; see Non-goals.
