# Minimalist Monochrome Design System Integration

## Goal

Replace the web console's current visual language with the Minimalist Monochrome design system: a pure black-and-white palette, serif typography, zero border radius, and a line-based visual structure. Every screen is converted. The result should read as editorial and deliberate rather than as a generic admin template, without sacrificing the information density an operator needs to run Terraform stacks.

## Current Context

The console is React 18 + Vite 8 + TypeScript, using react-router-dom v6, TanStack Query v5, and lucide-react. Tests are Vitest plus Testing Library across 33 test files.

All styling lives in a single hand-written `web/src/styles.css`: 844 lines, 109 flat rules, loosely grouped base to shell to workspace to panels to forms to buttons to features. Class naming is flat BEM-ish (`.panel`, `.primary-button`, `.stacks-list`, `.log-panel`). There is no Tailwind, no CSS modules, and no CSS-in-JS.

There is no design token layer of any kind. The file contains 101 hardcoded hex literals, 20 `border-radius` declarations, and 5 `box-shadow` declarations. The current palette is warm sage and off-white (`#f1f3ee` background, `#202833` text, `#d2d7cc` borders) with Inter, 6px radii, and soft shadows. This is close to the "Minimalist Modern" style that the target design system explicitly defines itself against, so the change is an inversion rather than a refinement.

Two facts shape the migration strategy:

No test asserts on class names. A search for `className` and `toHaveClass` across the 33 test files returns zero matches; the entire test contract rests on `data-testid` attributes and ARIA roles. Restyling is therefore close to risk-free as long as those attributes survive.

The app declares `font-family: Inter` but never loads it, so it has been silently falling back to a system sans stack. All three target typefaces need to be genuinely introduced, not swapped.

Only two files use inline `style={{ }}` attributes.

## Design Decisions

Five decisions were settled during brainstorming and constrain everything below.

**Scope.** Token layer plus a full restyle of every screen, not a phased or reference-implementation approach.

**Density translation.** The design system as written targets editorial and marketing pages: 9xl hero headlines, pull quotes, pricing tiers. This console is a dense operational tool with stack lists, run logs, credential forms, and grant tables. Applying the type scale literally would make it unusable. The chosen translation is *editorial chrome, dense data*: oversized serif page titles, thick section rules, and monospace metadata labels, while tables, logs, and forms stay compact and scannable. Drama lives in the chrome; precision lives in the data.

**Fonts.** Self-hosted woff2 subsets rather than a CDN link. This control plane is likely to run in private or egress-restricted networks where a Google Fonts request would fail and silently degrade the console to fallback serif.

**State signalling.** Monochrome everywhere except a single reserved red used only for failure states and destructive confirmation. This breaks the design system's absolute palette rule at exactly one point, in exchange for keeping the loudest possible operational signal in a tool that can destroy production infrastructure.

**CSS architecture.** A custom-property token layer with `styles.css` split into focused files, preserving existing class names. CSS Modules and Tailwind were both considered and rejected: each would rewrite `className` across all 25 components and abandon the flat-BEM idiom the codebase already applies consistently, spending a large diff on isolation this app does not need.

## Token Layer

`web/src/styles/tokens.css` holds every value. The 101 hex literals collapse to the palette below.

```css
:root {
  --color-bg:            #FFFFFF;
  --color-fg:            #000000;
  --color-muted:         #F5F5F5;
  --color-muted-fg:      #525252;
  --color-border:        #000000;
  --color-border-light:  #E5E5E5;
  --color-inverse-fg:    #FFFFFF;

  --color-critical:      #C1121F;
  --color-critical-fg:   #FFFFFF;

  --font-display: "Playfair Display", Georgia, serif;
  --font-body:    "Source Serif 4", Georgia, serif;
  --font-mono:    "JetBrains Mono", ui-monospace, SFMono-Regular, Menlo, monospace;

  --rule-hairline: 1px;
  --rule-thin:     1px;
  --rule-medium:   2px;
  --rule-thick:    4px;
  --rule-ultra:    8px;

  --radius: 0;
  --transition-fast: 100ms;
}
```

The type scale, spacing scale, tracking values, and leading values live alongside these, following the design system's published ramp (`--text-xs` at 0.75rem through `--text-8xl` at 8rem; tracking from `-0.05em` to `0.1em`).

`--radius: 0` is defined as a token and referenced at every call site rather than simply deleting the 20 existing declarations. This makes the zero-radius rule greppable and prevents a stray `6px` from creeping back in later.

### The reserved red

`--color-critical` is used at exactly two sites: the failure status chip and the destructive-action confirm step. It is always paired with both a glyph and a text label, never carrying meaning through hue alone. This keeps WCAG 1.4.1 satisfied and means state still reads correctly in greyscale or for a colorblind operator. The red is a volume boost on a signal that is already legible without it.

### Type scale by context

A console cannot put 128px type on every screen. The app does, however, have natural editorial moments: `NotFound`, `AccessDenied`, `ServiceUnavailable`, and the auth callback are full-viewport single-message screens. These host the genuinely oversized typography that the design system requires, rather than the requirement being waived.

| Context | Treatment |
|---|---|
| Editorial screens (404, denied, unavailable, callback) | `--text-8xl` desktop to `--text-5xl` mobile, Playfair, tracking-tighter, leading-none |
| Working-screen page titles | `--text-5xl` to `2rem` mobile, Playfair |
| Panel headings | `--text-2xl` Playfair |
| Body, form labels | `--text-base` to `--text-lg` Source Serif 4 |
| Table headers, metadata, IDs, timestamps, status | `--text-xs` JetBrains Mono, tracking-widest, uppercase |

Routing all technical chrome to monospace with wide tracking is what makes a dense table read as designed rather than merely small.

## File Architecture

```
web/src/styles/
  tokens.css      custom properties
  base.css        reset, @font-face, html/body, type defaults, textures, focus defaults
  primitives.css  .button-*, .panel, .rule, .chip, .page-header, .input, .drop-cap
  features.css    stacks, templates, runs, credentials, grants
web/src/styles.css   becomes a four-line @import index
web/public/fonts/    self-hosted woff2
```

Retaining `styles.css` as the index keeps the existing import in `main.tsx` valid, so that file is never touched.

### Fonts

woff2 only, latin subset, `font-display: swap`.

| Family | Weights | Purpose |
|---|---|---|
| Playfair Display | 400, 700, 400 italic | Display headings. Italic is required; the design system relies on it for emphasis and pull-quote treatments. |
| Source Serif 4 | 400, 600 | Body text, form labels |
| JetBrains Mono | 400, 500 | Metadata, IDs, timestamps, table headers, logs |

Approximately 8 files, 250 to 350KB total. `index.html` gains `<link rel="preload">` for Playfair 400 and Source Serif 400 only. Preloading all eight faces would contend for bandwidth and defeat the purpose.

## Component Primitives

### Buttons

All rectangular, uppercase, monospace, tracking-widest, with instant (100ms) inversion on hover.

| Class | Rest | Hover |
|---|---|---|
| `.primary-button` | Black fill, white text, no border | Inverts to white fill, black text, 2px black border |
| `.secondary-button` | Transparent, 2px black border | Fills black, text white |
| `.icon-button` and ghost variants | Transparent, no border | Underline appears |
| `.destructive-button` | Black fill, white text, trailing arrow glyph | Confirm step reveals `--color-critical` fill plus typed-name confirmation |

The destructive button stays black at rest deliberately. A red button that is red before the user has committed to anything makes the color ambient rather than meaningful; it earns the color only at the confirm step.

### Panels

`.panel` takes a 1px black border, zero radius, no shadow, and 24px padding (32px on viewports above 768px). A new `.panel--inverted` modifier (black fill, white text, white vertical-line texture) marks emphasis moments such as run summary headers and the active environment. It is used sparingly, because inversion loses all force if it appears everywhere.

### Inputs

White fill, 2px black bottom border only, zero radius, `--color-muted-fg` italic placeholder. Focus thickens the bottom border from 2px to 4px with no outline; the border change alone is the affordance.

### Status tones

The codebase carries three separate status unions: `TemplateRunStatus` (21 values), `TemplateRegistrationStatus` (5), and `TemplateRevisionStatus` (4). Twenty-one bespoke chip styles would be unmaintainable, so statuses map to a small set of visual tones instead.

`StatusRow` already performs a three-way classification via lucide icons (`CheckCircle2`, `XCircle`, `RefreshCw`) with an `inProgressStatus` helper. That classifier is extended to five tones and restyled; the existing helper is the natural home for the mapping.

| Tone | Statuses | Treatment |
|---|---|---|
| Settled | `completed`, `active`, `approved`, `lock_released`, `*_finished` | ● solid glyph, black, normal weight |
| In progress | `running`, `validating`, `canceling`, `*_started` | ◐ half glyph, 2px rule beneath with animated sweep |
| Waiting | `pending`, `pending_validation`, `queued`, `locked`, `waiting_approval` | ○ hollow glyph, `--color-muted-fg` |
| Failed | `failed`, `invalid`, `error` | ✕ inverted chip, `--color-critical` fill, white text |
| Canceled | `canceled`, `cancel_requested` | ⊘ glyph, `--color-muted-fg`, hairline strike rule |

`destroy_finished` renders as Settled but with a 4px rule above, marking destruction as terminal and consequential without claiming failure.

Intermediate pipeline statuses (`workspace_prepared`, `source_fetched`, `workspace_selected`, `init_finished`, `plan_finished`) fall under Settled, which matches the current behaviour where they receive the default success icon.

### Role badges

Separate from status. `.role-badge` currently distinguishes four roles — `owner`, `operator`, `approver`, `viewer` — by hue alone (blue, green, amber, grey). Monochrome replaces hue with visual weight, which lets the badge encode authority level rather than arbitrary identity:

| Role | Treatment |
|---|---|
| `owner` | Inverted: black fill, white text |
| `operator` | 2px black border, black text |
| `approver` | 1px black border, black text |
| `viewer` | 1px `--color-border-light` border, `--color-muted-fg` text |

This is an improvement on the current design, not merely a translation: the existing four colors carry no ordering, whereas descending border weight reads as descending privilege at a glance.

### Page header

The most repeated new structure, and where the editorial voice lives on working screens:

```
STACKS / INDEX          mono xs, tracking-widest, --color-muted-fg
Stacks                  Playfair 5xl, tracking-tight, leading-none
▬▬▬▬▬▬▬▬  ▫             4px rule plus 12px bordered square
```

### Lists and tables

Density is preserved. Monospace `xs` uppercase headers, 1px `--color-border-light` hairline row dividers, compact row padding. Row hover inverts fully to black fill and white text at 100ms, which is dramatic, instant, and doubles as a scanning aid on wide tables.

### Editorial screens

`NotFound`, `AccessDenied`, and `ServiceUnavailable` receive the full treatment: an `8xl` Playfair headline (`404`, `DENIED`, `OFFLINE`), a thick rule, one line of Source Serif explanation, and a ghost-button return link.

## Textures

The design system requires layered subtle textures to avoid flat design. Global noise at `0.02` opacity and horizontal hairlines at `0.015` apply to the body; inverted sections use white vertical lines at `0.03`.

One deliberate exception: no texture behind log output, form inputs, or the credentials panel. Patterned backgrounds under monospace log streams measurably hurt readability. Texture belongs on chrome and empty space, not under content someone is scanning for an error.

## Accessibility

Focus-visible styling on all interactive elements: `3px solid var(--color-fg)` with 3px offset for buttons and links, 2px offset for icon buttons. Inputs use border-thickening instead of an outline.

Pure black on white is 21:1. The reserved red on white is 6.4:1, which passes AA at its bold `xs` usage, and it is never the sole channel for meaning.

`prefers-reduced-motion` disables the existing `.spin` loader animation and the running-status rule sweep, falling back to static glyphs.

Minimum 44x44px touch targets on mobile require a media-query bump to the dense row padding. This is the main point where density and accessibility genuinely conflict, and accessibility wins.

A skip link is added to the shell as a visible black button.

## Migration Order

The sequence is what makes a full restyle safe rather than a big-bang rewrite.

1. **Tokens, base, and fonts.** Everything turns monochrome and serif; layout is unchanged. No TSX touched.
2. **Primitives rewritten under existing class names.** `.primary-button`, `.secondary-button`, `.panel`, `.panel h2`, `.form-grid` and peers keep their selectors and receive new bodies. Every screen inherits the new appearance without edits. No TSX touched.
3. **Per-screen TSX**, only where new structure is genuinely required: page headers, the extended status-tone classifier in `StatusRow`, monochrome role badges, inverted panels, and the editorial error screens. `.lifecycle-badge` in `StackTemplateScreen` is restyled alongside the role badges.
4. **Convert the two files still using inline styles.**

Steps 1 and 2 are test-neutral, so the majority of the visual transformation lands with no test risk.

## Risks

The single real hazard is step 3. Because no test asserts on class names, the entire test contract rests on `data-testid` attributes and ARIA roles. Any markup restructure must carry every one of them across verbatim. These attributes are treated as immovable.

A secondary risk is scope creep in `features.css`. The five feature areas have accumulated screen-specific rules that may duplicate what the new primitives provide; the temptation is to refactor beyond what the restyle requires. Deduplication is in scope only where a feature rule is directly replaced by a primitive.

## Testing

`npm test` runs after each screen in step 3 rather than batched at the end, so failures are attributed to a single screen. `npm run build` (which runs `tsc -b`) confirms the TypeScript layer stays clean. Actual command output is reported rather than asserted.

## Non-Goals

Dark mode is not in scope. The design system specifies a pure white background, and inversion is already the mechanism for emphasis; adding a theme system would dilute that.

No changes to component logic, data fetching, routing, or API contracts. This is a presentation-layer change only.

No new runtime dependencies. The restyle is achieved with custom properties and plain CSS.
