# Typed Layout Primitives for `web/`

**Date:** 2026-08-15
**Status:** Approved design, pending implementation plan

## Problem

`web/src/styles/` is a disciplined token system on every axis except layout.
`tokens.css` defines a 4px spacing scale and `styles.guard.test.ts` mechanically
fails the build on an untokenized color, radius, or shadow.

Layout has no such constraint, and it shows. `features.css` carries 48 layout
declarations — 25 `display:flex` and 23 `display:grid` — plus 12 bespoke
`grid-template-columns`, several hand-tuned to values no one can justify:

```
grid-template-columns: 1.1fr 0.9fr;
grid-template-columns: minmax(280px, 0.85fr) minmax(320px, 1.15fr);
```

Related drift, found in the same survey:

- Four gaps written as raw `0.75rem` / `1rem` / `0.5rem` instead of `--space-*`.
- Two paddings written as `0.6rem 0.8rem` and `0.15rem 0`.
- Four distinct breakpoints — 760px, 768px, 900px, 920px — expressing two
  intents, mixing `min-width` and `max-width`.
- Single-use layout classes named after their one caller, e.g.
  `stack-template-right-column`.

Each new screen invents its own tracks, so an agent writing UI has no layout
vocabulary to reach for and defaults to tuning pixels until it looks right.

## Goals

1. An agent building a screen picks a named layout, never a pixel value.
2. Off-scale layout is a compile error or a failing test, not a review comment.
3. Layout lives in exactly one file.

## Non-goals

- Adopting a CSS framework. Tailwind was evaluated on 2026-08-15 and rejected:
  arbitrary values (`grid-cols-[minmax(280px,0.85fr)]`) are idiomatic Tailwind, so
  it would make hand-tuned layout terser rather than prevent it, while discarding
  the AA-contrast reasoning in `tokens.css`.
- Changing the component layer. Buying accessible dialogs/menus (Radix, shadcn)
  is a real opportunity but a separate decision.
- Any change to color, typography, or motion tokens.

## The vocabulary

Six primitives in `web/src/shared/layout.tsx`. Every size prop is a union of
scale steps, so off-scale values fail `tsc`.

```ts
type Space      = 1 | 2 | 3 | 4 | 5 | 6 | 8 | 10 | 12 | 16 | 24;
type MinTrack   = 140 | 160 | 200 | 280 | 320;
type Collapse   = "md" | "lg" | "never";
```

| Primitive | Props | Replaces |
|---|---|---|
| `Stack` | `gap`, `align`, `as` | vertical rhythm; the `display:grid; gap:` sites |
| `Row` | `gap`, `align`, `justify`, `wrap`, `as` | the 25 flex sites |
| `Grid` | `gap`, `cols`, `min`, `collapse` | fixed-count and auto-fit grids |
| `Split` | `gap`, `ratio`, `collapse` | asymmetric two-column screens |
| `Fields` | `gap` | `max-content minmax(0, 1fr)` label/value layouts |
| `Rows` | `gap`, `lead`, `trailing` | list rows with trailing action columns |

`Row` covers the flex surface because that surface is monotonous: 13 sites use
`align-items:center`, 9 use `justify-content:space-between`, 4 use
`flex-wrap:wrap`, and exactly one uses `flex-direction:column`.

`Split.ratio` is the central anti-tuning move. It accepts `"even" | "wide-left" |
"wide-right"` (`1fr 1fr`, `3fr 2fr`, `2fr 3fr`). The ratio *choice* is removed,
not merely respelled.

`Grid` accepts a fixed count, an auto-fit floor, or both:

```ts
type GridProps =
  | { cols: 2 | 3 | 4; min?: MinTrack; gap?: Space; collapse?: Collapse }
  | { cols?: never;    min:  MinTrack; gap?: Space; collapse?: Collapse };
```

`Rows` describes N flexible leading columns followed by M `auto` trailing
columns: `lead` (`1 | 2`) and `trailing` (`1 | 2`). The two call sites need
different splits — `.credential-row` is one flexible plus two auto,
`.credential-form` is two flexible plus one auto — so a single `trailing` prop
would not have covered both. These layouts must stay grids rather than become
flex rows, because their columns align *across* rows.

### Coverage

All 12 bespoke grids map onto the vocabulary with no free-form track strings:

| Current | Becomes |
|---|---|
| `repeat(2, minmax(140px, 1fr))` | `<Grid cols={2} min={140}>` |
| `repeat(2, minmax(160px, 1fr))` | `<Grid cols={2} min={160}>` |
| `repeat(auto-fit, minmax(200px, 1fr))` | `<Grid min={200}>` |
| `minmax(280px,0.85fr) minmax(320px,1.15fr)` | `<Split ratio="wide-right">` |
| `1.1fr 0.9fr` | `<Split ratio="even">` |
| `minmax(160px, 1fr) auto auto` | `<Rows lead={1} trailing={2}>` |
| `minmax(160px,1fr) minmax(180px,1fr) auto` | `<Rows lead={2} trailing={1}>` |
| `max-content minmax(0, 1fr)` (×2) | `<Fields>` |
| `1fr` (×3, inside `max-width` queries) | absorbed by `collapse` |

An earlier draft gave `Grid` a free-form `cols` string as an escape hatch for the
two `Rows` cases. `Rows` removes it. There is deliberately no way to express an
arbitrary track list.

## Emission model

Primitives render a semantic class plus enumerable data attributes. Every CSS
rule lives in `primitives.css`.

```jsx
<Grid cols={2} min={280} gap={6} collapse="md">
// → <div class="grid" data-cols="2" data-min="280" data-gap="6" data-collapse="md">
```

Inline `style={{...}}` is rejected as an emission mechanism: it is an unguardable
hole through which any value could flow, and `features.css` already carries an
"Inline-style drain (Task 8)" section from a prior cleanup of exactly that
problem. Data attributes are a closed set the CSS must enumerate, so an
unsupported value has no rule behind it and fails visibly in the styleguide.

This is what forces `min` to be a scale rather than a number. The values in use
today are 140, 160, 200, 280, and 320; those become `--layout-min-*` tokens in
`tokens.css`. `min={237}` is a type error.

Breakpoints resolve to two values: `md` = 768px, `lg` = 900px. The existing 760px
and 920px queries normalize onto them.

## Enforcement

Four rules added to `styles.guard.test.ts`, following its existing
regex-per-property pattern.

1. **Spacing tokens.** Every `gap` and `padding` uses `var(--space-*)` or `0`.
   Catches the four raw `rem` gaps and both raw paddings.
2. **Single home for layout.** `display:grid`, `display:flex`,
   `grid-template-columns`, and `gap` may not appear outside `primitives.css`.
3. **Approved breakpoints.** `@media` width queries use only 768px and 900px.
4. **No lengths in tracks.** `grid-template-columns` contains no length unit
   outside `var(--layout-min-*)`.

Rule 2 is the payoff, and it is only reachable because the migration is a full
retrofit: afterwards `features.css` holds color, type, and decoration only.

Rule 3 cannot use custom properties — CSS variables do not work inside `@media`
queries. The two breakpoints are documented constants in a single comment block
in `primitives.css`; the guard test is the actual enforcement.

Rules 2 and 3 are introduced with a grandfather allowlist of files not yet
migrated. The allowlist shrinks each batch and is deleted in the final batch,
giving the migration a mechanical progress metric.

## Migration

Six batches, ordered so the riskiest conversions happen after the vocabulary has
been proven on well-covered code.

| Batch | Scope | Rationale |
|---|---|---|
| 1 | `layout.tsx`, `primitives.css` layout layer, guard rules with full allowlist, `StyleGuide.tsx` entry | No feature markup changes; establishes the vocabulary and its documentation |
| 2 | `shared/` — `StatBand`, `StatusRow`, `IdsPanel`, `HeroGraphic` | Most reused, best test coverage; the canary for whether `Fields`/`Rows` are expressive enough |
| 3 | `app/` — `AppShell`, `NotFound`, `AccessDenied`, `ServiceUnavailable`, `RoutePlaceholder` | Application frame; small and visually distinctive |
| 4 | `features/stacks/` (15 components) | Largest surface, including the `Split` screens |
| 5 | `features/runs/`, `features/templates/` | Remaining screens |
| 6 | Delete allowlist, delete dead layout CSS, flip rules 2–3 to hard failures | End state |

If batch 2 shows the vocabulary cannot express a real layout without a new
escape hatch, stop and revise this spec rather than widening `Grid`.

## Testing

- **Existing suite.** 24 test files across `app/`, `shared/`, `auth/`, and
  `features/` run per batch. They assert on `data-testid` and text, not class
  names, so they should survive the retrofit; a test that breaks indicates a real
  structural change worth inspecting.
- **Primitive unit tests.** `shared/layout.test.tsx` asserts each primitive emits
  the expected class and data attributes for representative props.
- **Guard tests.** Run per batch; the shrinking allowlist tracks progress.
- **Runtime verification.** The `web:verify` skill builds, launches, and drives
  the app to observe each batch's screens. Applied per batch rather than once at
  the end, so a regression is attributable to a small diff.
- **Styleguide.** `StyleGuide.tsx` gains a layout section rendering every
  primitive at every supported prop value, which doubles as the visual diff
  surface and as the reference an agent reads.

## Risks

- **Breakpoint normalization changes rendered output.** Two collapse points move,
  by 8px and 20px. This is intended, and it is the only deliberate visual change
  in the migration.
- **`Fields` and `Rows` each serve two call sites.** Two is thin evidence for a
  primitive. Batch 2 is the checkpoint; if either fails to generalize, fold it
  back rather than adding props.
- **Test suite asserts on some class names.** Any test coupled to a layout class
  being retired needs rewriting against `data-testid` instead — a change to the
  test's assertion target, not a weakening of its coverage.
