# Minimalist Modern Design System Integration

## Goal

Replace the web console's visual language with the Minimalist Modern design system: a warm off-white canvas, an Electric Blue gradient accent used sparingly but boldly, a Calistoga/Inter/JetBrains Mono type system, rounded surfaces, layered shadows, and purposeful motion. Every screen is converted. The result should feel confident and alive — professional but design-forward — without slowing down an operator running Terraform stacks.

## Current Context

This supersedes `2026-08-10-monochrome-design-system-design.md`. That spec described a Minimalist Monochrome system — pure black and white, serif display type, zero border radius, no shadows, near-zero motion. It was implemented through three of nine planned tasks and then rejected on visual review. The two designs are near-exact opposites, which the earlier brief acknowledged: it defined itself explicitly against "Minimalist Modern."

The console is React 18 + Vite 8 + TypeScript, using react-router-dom v6, TanStack Query v5, and lucide-react. Tests are Vitest plus Testing Library, currently 34 files and 226 tests, all passing.

Four code commits from the monochrome effort are on `feat/monochrome-design-system`. Most of that work survives this pivot, because it separated architecture from styling:

- `web/src/styles/` exists with `tokens.css`, `base.css`, and `primitives.css`, imported through `web/src/styles.css` as an index. The split is design-system-agnostic.
- `web/src/styles.css` has been drained from 109 rules to 91. That drain continues unchanged.
- Existing class names were deliberately preserved, so screens restyle without component edits. That strategy still holds.
- `scripts/vendor-fonts.sh` works and is verified. **Inter and JetBrains Mono are already vendored** — two of this system's three faces. Only Calistoga is new.
- The five-tone status classifier designed in the previous round was **never built** — the earlier plan stopped after its third task. `web/src/shared/StatusRow.tsx` is still the original, classifying with three lucide icons via an inline `inProgressStatus` helper. `web/src/shared/statusTone.ts` must therefore be created here, not carried over. Its design survives intact: 30 status values across three API unions map onto five visual tones.
- `@types/node` is a devDependency, required for the guard test to typecheck.

One artefact must invert rather than carry over: `web/src/styles/styles.guard.test.ts` currently fails the build on any `box-shadow` and any non-zero `border-radius`. This design requires both.

No test asserts on class names; the entire test contract is `data-testid` attributes and ARIA roles.

The app cannot be driven end to end in development: `oidc-client-ts` cannot reach Keycloak, so React never mounts and `#root` stays empty. Visual verification therefore happens by building and rendering the compiled CSS against representative markup, not by driving the running app.

## Design Decisions

**Architecture.** Keep the `styles/` split, the token layer, the class-name preservation strategy, and the `styles.css` drain. Retheme the values rather than resetting to `main`. Rebuilding this infrastructure would discard work that is independent of which design system is chosen.

**Signature elements.** Implement the full system including the showpieces — animated hero graphic, rotating rings, floating cards, gradient text, pulsing indicators, inverted sections — not a reduced console-only subset.

**Animation.** Pure CSS keyframes plus one small `useInView` hook built on IntersectionObserver. Framer Motion is named in the brief but rejected here: it would add roughly 35-50KB gzip to a bundle currently at 108KB, and every effect the brief describes is achievable in CSS. No new runtime dependencies.

**Status colour.** A full semantic palette — accent for in-progress, green for success, amber for waiting, red for failure, slate for cancelled. This dilutes the brief's "single electrifying accent" principle, accepted deliberately because operators reading 30 distinct run states benefit more from conventional colour semantics than from accent purity.

**Showpiece placement.** Showpieces live on empty states and full-viewport screens: the stacks-list empty state, `NotFound`, `AccessDenied`, `ServiceUnavailable`, and the auth callback. Screens displaying real data get the chrome instead. No new routes or product surface are invented.

## Token Layer

`web/src/styles/tokens.css` remains the only file permitted raw hex.

```css
:root {
  /* Canvas & surfaces */
  --color-bg:        #FAFAFA;
  --color-fg:        #0F172A;
  --color-muted:     #F1F5F9;
  --color-muted-fg:  #475569;
  --color-border:    #E2E8F0;
  --color-card:      #FFFFFF;

  /* The signature accent */
  --color-accent:    #0052FF;
  --color-accent-2:  #4D7CFF;
  --color-accent-fg: #FFFFFF;
  --gradient-accent: linear-gradient(135deg, #0052FF, #4D7CFF);

  /* Accent tints, for badges and washes */
  --color-accent-soft:   rgba(0, 82, 255, 0.05);
  --color-accent-border: rgba(0, 82, 255, 0.30);

  /* Semantic status — text-safe values, see contrast note */
  --color-success:      #166534;
  --color-warning:      #92400E;
  --color-danger:       #B91C1C;
  --color-success-dot:  #16A34A;
  --color-warning-dot:  #D97706;
  --color-success-soft: rgba(22, 163, 74, 0.10);
  --color-warning-soft: rgba(180, 83, 9, 0.10);
  --color-danger-soft:  rgba(220, 38, 38, 0.10);

  /* Typefaces */
  --font-display: "Calistoga", Georgia, serif;
  --font-body:    Inter, system-ui, -apple-system, sans-serif;
  --font-mono:    "JetBrains Mono", ui-monospace, SFMono-Regular, Menlo, monospace;

  /* Radii */
  --radius-sm:   6px;
  --radius-md:   8px;
  --radius-lg:   12px;
  --radius-xl:   16px;
  --radius-full: 9999px;

  /* Shadows */
  --shadow-sm:        0 1px 3px rgba(0, 0, 0, 0.06);
  --shadow-md:        0 4px 6px rgba(0, 0, 0, 0.07);
  --shadow-lg:        0 10px 15px rgba(0, 0, 0, 0.08);
  --shadow-xl:        0 20px 25px rgba(0, 0, 0, 0.10);
  --shadow-accent:    0 4px 14px rgba(0, 82, 255, 0.25);
  --shadow-accent-lg: 0 8px 24px rgba(0, 82, 255, 0.35);

  /* Motion */
  --ease-out:          cubic-bezier(0.16, 1, 0.3, 1);
  --duration-fast:     200ms;
  --duration-lift:     300ms;
  --duration-entrance: 700ms;
}
```

The type scale, tracking, leading, and spacing tokens from the previous round are retained unchanged; they are numeric ramps with no stylistic commitment.

### Contrast finding

The semantic colours implied by the brief were checked against the white card surface before adoption:

| Colour | Contrast on white | Verdict |
|---|---|---|
| `#0052FF` accent | 5.70:1 | Passes AA |
| `#B91C1C` danger | 5.54:1 on the tinted surface | Passes AA |
| `#16A34A` green | 3.24:1 | Fails AA for normal text |
| `#D97706` amber | 3.20:1 | Fails AA for normal text |

Green and amber at the brighter dot values would put "completed" and "queued" labels below the accessible threshold on a console where those words carry operational meaning. Text-bearing status uses the darker `#166534` and `#92400E` values, and danger uses `#B91C1C`, so text remains AA-readable against the tinted surfaces as well as the canvas. The brighter values are retained as `--color-success-dot` and `--color-warning-dot` for dots and fills, where the 3:1 UI-component threshold applies. The visual language is unchanged; only the text values shift.

### Fonts

woff2, latin subset, `font-display: swap`, self-hosted in `web/public/fonts/` and vendored by `scripts/vendor-fonts.sh`.

| Family | Weights | Purpose | Status |
|---|---|---|---|
| Calistoga | 400 | Display headings (h1, h2) | To vendor |
| Inter | 400, 500, 600, 700 | Body, UI, card titles, smaller headings | Already vendored |
| JetBrains Mono | 400, 500 | Section labels, badges, IDs, timestamps, logs | Already vendored |

`index.html` preloads Inter 400 and Calistoga 400 only.

## Animation System

One hook, everything else CSS.

`web/src/shared/useInView.ts` wraps IntersectionObserver with `threshold: 0.15`, `rootMargin: "-60px"`, and fire-once semantics, matching the brief's viewport options. It returns a ref and a boolean; consumers set `data-visible` from the boolean.

```css
.reveal {
  opacity: 0;
  transform: translateY(28px);
  transition: opacity var(--duration-entrance) var(--ease-out),
              transform var(--duration-entrance) var(--ease-out);
  transition-delay: var(--reveal-delay, 0ms);
}

.reveal[data-visible="true"] {
  opacity: 1;
  transform: none;
}
```

Stagger is `--reveal-delay: calc(var(--i) * 100ms)` set per child, the CSS equivalent of `staggerChildren: 0.1`.

Three continuous keyframes, all disabled under `prefers-reduced-motion`:

| Keyframe | Timing | Used by |
|---|---|---|
| `spin-slow` | 60s linear infinite | Rotating dashed ring in the hero graphic |
| `float` | 4s and 5s ease-in-out infinite, ±10px | Floating cards; offset durations prevent lockstep |
| `pulse-dot` | 2s infinite, scale 1→1.3→1, opacity 1→0.7→1 | Live indicators and section-label dots |

Under `prefers-reduced-motion: reduce`, continuous animations stop **and** reveals resolve to their visible state. Disabling only the transition would leave revealed content permanently invisible.

## Showpieces

`web/src/shared/HeroGraphic.tsx` is presentational, takes no props, and uses only CSS and inline SVG: a rotating dashed ring, two floating cards on offset timings, gradient-filled geometry, a 3×3 decorative dot grid, and a solid accent corner block carrying `--shadow-accent`.

`web/src/shared/SectionLabel.tsx` renders the repeated pill badge: `--radius-full`, 1px `--color-accent-border`, `--color-accent-soft` fill, an 8px accent dot that optionally pulses, and uppercase monospace text at `0.15em` tracking.

`web/src/shared/StatBand.tsx` renders the inverted section: `--color-fg` background, light text, dot-pattern texture at 3% opacity. Its figures derive from data the screens already fetch — total stacks and counts by tone. No new queries.

Placement:

| Surface | Treatment |
|---|---|
| Stacks list, empty state | Section badge, gradient-word headline, `HeroGraphic`, gradient CTA |
| 404, denied, unavailable, callback | Section badge, gradient headline word, `HeroGraphic` |
| Stacks list, with data | Section badge, `StatBand`, elevated rows, pulsing live dots |
| All other working screens | Badges, gradient buttons, elevated cards, hover lifts |

### Gradient text

```css
.gradient-text {
  background: var(--gradient-accent);
  -webkit-background-clip: text;
  background-clip: text;
  color: transparent;
}

@media (forced-colors: active) {
  .gradient-text {
    color: CanvasText;
    background: none;
  }
}
```

The forced-colors fallback is required, not optional. Windows High Contrast overrides the background but not the clip, so without it the text renders completely invisible.

## Component Primitives

### Buttons

All use `--radius-lg`, a 48px minimum height, and `transition-all var(--duration-fast) var(--ease-out)`.

| Class | Rest | Hover |
|---|---|---|
| `.primary-button` | `--gradient-accent` fill, white text, `--shadow-sm` | `translateY(-2px)`, `--shadow-accent-lg`, `brightness(1.1)`; active `scale(0.98)` |
| `.secondary-button` | Transparent, 1px `--color-border` | `--color-muted` fill, border to `--color-accent-border` |
| `.icon-button` and ghost | No fill or border, `--color-muted-fg` | `--color-fg` |
| `.destructive-button` | Solid `--color-danger`, white text | Deepened shadow; retains the `data-confirming` two-step affordance |

Trailing arrow glyphs translate right on hover via a sibling selector.

### Cards

`.panel` is `--color-card`, 1px `--color-border`, `--radius-xl`, `--shadow-md`, padding `--space-6` rising to `--space-8` above 768px. Interactive panels lift to `--shadow-xl` with a 3% accent gradient wash on hover.

`.panel--featured` uses the double-background technique for a gradient border, which needs no wrapper element:

```css
.panel--featured {
  border: 2px solid transparent;
  background: linear-gradient(var(--color-card), var(--color-card)) padding-box,
              var(--gradient-accent) border-box;
}

@media (forced-colors: active) {
  .panel--featured {
    border-color: CanvasText;
  }
}
```

The forced-colors fallback matters here: the border is drawn from a background layer, and forced-colors replaces backgrounds, so without it a "featured" panel loses its emphasis entirely and becomes indistinguishable from a standard one.

`.panel--inverted` is `--color-fg` background with light text and dot-pattern texture.

### Inputs

48px tall, 1px `--color-border`, `--radius-lg`, transparent background, placeholder at `--color-muted-fg`. Focus draws a 2px accent ring at 2px offset via layered `box-shadow`.

### Status tones

`statusTone.ts` is created in this effort (see Current Context). Tones render as tinted pills:

| Tone | Text | Background | Extra |
|---|---|---|---|
| Settled | `--color-success` | `--color-success-soft` | — |
| In progress | `--color-accent` | `--color-accent-soft` | `pulse-dot` on the dot |
| Waiting | `--color-warning` | `--color-warning-soft` | — |
| Failed | `--color-danger` | `--color-danger-soft` | — |
| Cancelled | `--color-muted-fg` | `--color-muted` | — |

`destroy_finished` renders as Settled with a heavier top rule, as before.

### Role badges

Tinted pills, since hue is no longer forbidden: `owner` accent, `operator` success, `approver` warning, `viewer` slate. Each uses the soft background with the text-safe foreground.

### Tabs and navigation

Ghost by default in `--color-muted-fg`. The active state takes `--color-accent` text plus a 2px gradient underline bar with `--radius-full`.

### Log panel

`--color-fg` background, light monospace text, `--radius-xl`. Deliberately carries no texture: patterning behind a log stream measurably hurts scanning for errors, and that holds regardless of design system.

## Guard Test

`styles.guard.test.ts` inverts rather than disappears. It enforces:

1. `tokens.css` defines every required custom property.
2. No stylesheet outside `tokens.css` contains a raw hex literal.
3. Every `border-radius` declaration references a `var(--radius-*)` token.
4. Every `box-shadow` declaration references a `var(--shadow-*)` token.
5. A `forced-colors: active` block exists covering `.gradient-text`.

Rule 5 asserts an accessibility affordance, so it cannot be silently dropped during later refactoring.

## Accessibility

`focus-visible` draws a 2px `--color-accent` ring at 2px offset on all interactive elements. Contrast is verified above. Touch targets are at least 44px; buttons and inputs at 48px exceed it by default.

`prefers-reduced-motion: reduce` stops continuous animation and resolves reveals to visible.

`forced-colors: active` restores gradient text to system colours.

A skip link is retained from the previous round.

## Migration Order

1. Retheme `tokens.css`, vendor Calistoga, update `base.css` for the new element defaults, textures, keyframes, and reduced-motion rules, **rewrite `primitives.css` under the existing class names**, and rewrite the guard test.
2. Add `useInView`, `SectionLabel`, `HeroGraphic`, and `StatBand` as new shared modules.
3. Restyle status tones and role badges.
4. Application shell and navigation.
5. Full-viewport screens with showpieces.
6. Stacks screens, including the empty state hero and the stats band.
7. Templates and runs screens, including the log panel.
8. Responsive pass, reduced-motion audit, and drain `styles.css` to a pure import index.

Step 1 deliberately covers both the token layer and the primitives, because the rename from `--radius` to the `--radius-*` scale is a breaking change: `primitives.css` references `var(--radius)` today, and the rewritten guard test requires a `--radius-*` token. Splitting them would leave an intermediate commit that fails its own guard. It stays test-neutral despite its size, since it touches no component files, and it delivers most of the visual change in one reviewable step.

## Testing

`npm test` and `npm run build` run after each screen rather than batched, so failures attribute to a single change. `statusTone.ts` is written test-first, with a case per status tone. `useInView` requires an `IntersectionObserver` stub under jsdom and gets its own test.

Because the app cannot boot without Keycloak, visual verification builds the project and renders the compiled CSS against representative markup in a static harness, screenshotted with headless Chrome.

Actual command output is reported rather than asserted.

## Risks

`data-testid` attributes and ARIA roles are the entire test contract and must survive every markup change verbatim.

The showpieces introduce continuous animation to the console for the first time. Every continuous keyframe must be behind `prefers-reduced-motion`, and none may run underneath tabular data.

Gradient text and gradient borders both draw colour from background layers, which forced-colors mode replaces. Both therefore need explicit `forced-colors: active` fallbacks, and both have them. The guard test covers the text case, since an invisible headline is the more severe failure.

## Non-Goals

No new routes, queries, or product surface. The stats band uses only data already fetched.

No new runtime dependencies. All motion is CSS plus one IntersectionObserver hook.

No changes to component logic, data fetching, routing, or API contracts. This is a presentation-layer change.

Dark mode remains out of scope.
