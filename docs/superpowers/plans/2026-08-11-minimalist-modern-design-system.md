# Minimalist Modern Design System Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Restyle the entire tflive web console in the Minimalist Modern design system — warm off-white canvas, Electric Blue gradient accent, Calistoga/Inter/JetBrains Mono type, rounded surfaces, layered shadows, and purposeful CSS motion.

**Architecture:** The existing `web/src/styles/` token split is retained and rethemed. Primitives are rewritten under their existing class names so most screens restyle with no component edits. New motion and showpiece behaviour arrives as small, self-contained shared modules. `web/src/styles.css` continues draining toward a pure `@import` index.

**Tech Stack:** React 18, Vite 8, TypeScript 5.6, react-router-dom v6, TanStack Query v5, lucide-react, Vitest + Testing Library. No new runtime dependencies.

**Spec:** `docs/superpowers/specs/2026-08-11-minimalist-modern-design-system-design.md`

## Global Constraints

- **Raw hex literals are permitted only in `web/src/styles/tokens.css`.** Enforced by the guard test.
- **Every `border-radius` must reference a `var(--radius-*)` token.** Enforced.
- **Every `box-shadow` must reference a `var(--shadow-*)` token.** Enforced. This is why the input focus ring is defined as `--shadow-ring` rather than written inline.
- **Text-bearing status colours are `#15803D` and `#B45309`**, never `#16A34A` / `#D97706`, which fail WCAG AA on white at 3.2:1. The brighter values exist only as `--color-success-dot` / `--color-warning-dot` for dots and fills.
- **Every continuous animation sits behind `prefers-reduced-motion: reduce`**, and reduced motion must resolve `.reveal` elements to *visible*, never leave them hidden.
- **`.gradient-text` and `.panel--featured` require `forced-colors: active` fallbacks.** Both draw colour from background layers, which forced-colors replaces.
- **`data-testid` attributes and ARIA roles are immovable.** No test asserts on class names; these are the entire test contract.
- **No new runtime dependencies.** All motion is CSS plus one IntersectionObserver hook.
- **No new routes, queries, or product surface.** The stats band uses only data already fetched.
- Fonts are self-hosted from `web/public/fonts/`. No third-party font host at runtime.

## Baseline (verified 2026-08-11)

`cd web && npm test` → **34 test files passed, 226 tests passed.**

The run also prints **8 errors** from `src/auth/useMeQuery.test.tsx` — unhandled `oidc-client-ts` rejections during the "returns error on 401 response" test. **Pre-existing and unrelated.** Only the "Test Files / Tests" summary lines are the pass criterion.

Expected test counts are given per task. If your assertion counts differ by one or two because you wrote a slightly different number of `it` blocks, that is fine — **zero failures** is the criterion, not an exact total.

## Current State

`web/src/styles/` contains `tokens.css`, `base.css`, `primitives.css`, and `styles.guard.test.ts`, imported through `web/src/styles.css`. That index also still holds **91 legacy rules** awaiting drain.

`web/public/fonts/` already contains `inter-400/500/600/700.woff2` and `jetbrains-mono-400/500.woff2`. Only Calistoga is missing.

`web/src/shared/` contains `IdsPanel.tsx`, `StatusRow.tsx`, `listSelection.ts`, `queryErrorBoundary.tsx`. **There is no `statusTone.ts`** — it is created in Task 3.

## File Structure

| File | Responsibility |
|---|---|
| `web/src/styles/tokens.css` | All custom properties. Only file allowed raw hex. |
| `web/src/styles/base.css` | `@font-face`, reset, element defaults, textures, keyframes, `.reveal`, `.gradient-text`, focus, reduced-motion, forced-colors |
| `web/src/styles/primitives.css` | Buttons, inputs, panels, page header, messaging, section label, status pills, role badges |
| `web/src/styles/features.css` | Screen-specific rules (created Task 4) |
| `web/src/styles/styles.guard.test.ts` | Enforces palette, radius, shadow, and forced-colors rules |
| `web/src/shared/useInView.ts` | IntersectionObserver hook driving entrance reveals |
| `web/src/shared/statusTone.ts` | Maps 30 API status values onto five tones |
| `web/src/shared/SectionLabel.tsx` | The pill badge with optional pulsing dot |
| `web/src/shared/HeroGraphic.tsx` | Animated showpiece — CSS and inline SVG only |
| `web/src/shared/StatBand.tsx` | Inverted stats section with dot texture |

---

### Task 1: Token layer, base, primitives, and the inverted guard test

Retheme the whole foundation in one step. This lands most of the visual change and touches **no component files**, so it is entirely test-neutral.

It deliberately covers tokens *and* primitives together: renaming `--radius` to the `--radius-*` scale is a breaking change, and splitting it would produce an intermediate commit that fails its own guard test.

**Files:**
- Modify: `scripts/vendor-fonts.sh` (add Calistoga)
- Create: `web/public/fonts/calistoga-400.woff2` (via script)
- Rewrite: `web/src/styles/tokens.css`
- Rewrite: `web/src/styles/base.css`
- Rewrite: `web/src/styles/primitives.css`
- Rewrite: `web/src/styles/styles.guard.test.ts`
- Modify: `web/index.html` (preloads)

**Interfaces:**
- Produces every token consumed by all later tasks: `--color-bg`, `--color-fg`, `--color-muted`, `--color-muted-fg`, `--color-border`, `--color-card`, `--color-accent`, `--color-accent-2`, `--color-accent-fg`, `--gradient-accent`, `--color-accent-soft`, `--color-accent-border`, `--color-success`, `--color-warning`, `--color-danger`, `--color-success-dot`, `--color-warning-dot`, `--color-success-soft`, `--color-warning-soft`, `--color-danger-soft`, `--font-display`, `--font-body`, `--font-mono`, `--text-*`, `--tracking-*`, `--leading-*`, `--space-*`, `--radius-sm|md|lg|xl|full`, `--shadow-sm|md|lg|xl|accent|accent-lg|ring`, `--ease-out`, `--duration-fast|lift|entrance`.
- Produces CSS classes `.reveal`, `.gradient-text`, `.skip-link`, and keyframes `spin-slow`, `float`, `float-alt`, `pulse-dot`, consumed by Tasks 2 and 5-7.

- [ ] **Step 1: Add Calistoga to the vendoring script**

In `scripts/vendor-fonts.sh`, replace the `fetch` block with:

```bash
echo "Vendoring fonts into ${DEST}"
fetch "Calistoga"               calistoga-400
fetch "Inter:wght@400"          inter-400
fetch "Inter:wght@500"          inter-500
fetch "Inter:wght@600"          inter-600
fetch "Inter:wght@700"          inter-700
fetch "JetBrains+Mono:wght@400" jetbrains-mono-400
fetch "JetBrains+Mono:wght@500" jetbrains-mono-500
echo "Done."
```

Calistoga ships a single weight, so its family spec carries no `wght` axis. The API returns a `/* latin */` block exactly like the others, so the existing extraction logic works unchanged.

- [ ] **Step 2: Run the script and verify**

```bash
./scripts/vendor-fonts.sh && ls -1 web/public/fonts/ && du -sh web/public/fonts
```

Expected: 7 files including `calistoga-400.woff2`, totalling roughly 165K.

- [ ] **Step 3: Rewrite the token layer**

Replace the entire contents of `web/src/styles/tokens.css`:

```css
/* The only file in the codebase permitted to contain raw hex literals.
   Everything else references these custom properties. */
:root {
  /* Canvas and surfaces */
  --color-bg:        #FAFAFA;
  --color-fg:        #0F172A;
  --color-muted:     #F1F5F9;
  --color-muted-fg:  #64748B;
  --color-border:    #E2E8F0;
  --color-card:      #FFFFFF;

  /* The signature accent */
  --color-accent:    #0052FF;
  --color-accent-2:  #4D7CFF;
  --color-accent-fg: #FFFFFF;
  --gradient-accent: linear-gradient(135deg, #0052FF, #4D7CFF);

  --color-accent-soft:   rgba(0, 82, 255, 0.05);
  --color-accent-border: rgba(0, 82, 255, 0.30);
  --color-accent-wash:   rgba(0, 82, 255, 0.03);

  /* Semantic status.
     The -dot values are brighter and are ONLY for dots and fills, where the
     3:1 UI-component contrast threshold applies. Text uses the darker values:
     #16A34A is 3.24:1 on white and #D97706 is 3.20:1, both failing AA for
     normal text, while #15803D is 4.99:1 and #B45309 is 5.02:1. */
  --color-success:      #15803D;
  --color-warning:      #B45309;
  --color-danger:       #DC2626;
  --color-success-dot:  #16A34A;
  --color-warning-dot:  #D97706;
  --color-success-soft: rgba(22, 163, 74, 0.10);
  --color-warning-soft: rgba(180, 83, 9, 0.10);
  --color-danger-soft:  rgba(220, 38, 38, 0.10);

  /* Typefaces */
  --font-display: Calistoga, Georgia, serif;
  --font-body:    Inter, system-ui, -apple-system, "Segoe UI", sans-serif;
  --font-mono:    "JetBrains Mono", ui-monospace, SFMono-Regular, Menlo, Monaco,
                  Consolas, "Liberation Mono", monospace;

  /* Type scale */
  --text-xs:   0.75rem;
  --text-sm:   0.875rem;
  --text-base: 1rem;
  --text-lg:   1.125rem;
  --text-xl:   1.25rem;
  --text-2xl:  1.5rem;
  --text-3xl:  2rem;
  --text-4xl:  2.5rem;
  --text-5xl:  3.25rem;
  --text-6xl:  4rem;
  --text-7xl:  5.25rem;

  /* Tracking */
  --tracking-tighter: -0.02em;
  --tracking-tight:   -0.01em;
  --tracking-normal:  0;
  --tracking-wide:    0.05em;
  --tracking-label:   0.15em;

  /* Leading */
  --leading-none:    1.05;
  --leading-tight:   1.15;
  --leading-normal:  1.4;
  --leading-relaxed: 1.7;

  /* Spacing, 4px base */
  --space-1:  4px;
  --space-2:  8px;
  --space-3:  12px;
  --space-4:  16px;
  --space-5:  20px;
  --space-6:  24px;
  --space-8:  32px;
  --space-10: 40px;
  --space-12: 48px;
  --space-16: 64px;
  --space-24: 96px;

  /* Radii */
  --radius-sm:   6px;
  --radius-md:   8px;
  --radius-lg:   12px;
  --radius-xl:   16px;
  --radius-full: 9999px;

  /* Shadows. --shadow-ring exists so the focus ring can satisfy the guard
     rule that every box-shadow references a --shadow-* token. */
  --shadow-sm:        0 1px 3px rgba(0, 0, 0, 0.06);
  --shadow-md:        0 4px 6px rgba(0, 0, 0, 0.07);
  --shadow-lg:        0 10px 15px rgba(0, 0, 0, 0.08);
  --shadow-xl:        0 20px 25px rgba(0, 0, 0, 0.10);
  --shadow-accent:    0 4px 14px rgba(0, 82, 255, 0.25);
  --shadow-accent-lg: 0 8px 24px rgba(0, 82, 255, 0.35);
  --shadow-ring:      0 0 0 2px var(--color-bg), 0 0 0 4px var(--color-accent);

  /* Motion */
  --ease-out:          cubic-bezier(0.16, 1, 0.3, 1);
  --duration-fast:     200ms;
  --duration-lift:     300ms;
  --duration-entrance: 700ms;

  /* Controls */
  --control-height: 48px;
  --touch-target:   44px;
}
```

- [ ] **Step 4: Rewrite the base stylesheet**

Replace the entire contents of `web/src/styles/base.css`:

```css
/* ---- Self-hosted faces. Latin subset, vendored by scripts/vendor-fonts.sh ---- */
@font-face {
  font-family: Calistoga;
  font-style: normal;
  font-weight: 400;
  font-display: swap;
  src: url("/fonts/calistoga-400.woff2") format("woff2");
}
@font-face {
  font-family: Inter;
  font-style: normal;
  font-weight: 400;
  font-display: swap;
  src: url("/fonts/inter-400.woff2") format("woff2");
}
@font-face {
  font-family: Inter;
  font-style: normal;
  font-weight: 500;
  font-display: swap;
  src: url("/fonts/inter-500.woff2") format("woff2");
}
@font-face {
  font-family: Inter;
  font-style: normal;
  font-weight: 600;
  font-display: swap;
  src: url("/fonts/inter-600.woff2") format("woff2");
}
@font-face {
  font-family: Inter;
  font-style: normal;
  font-weight: 700;
  font-display: swap;
  src: url("/fonts/inter-700.woff2") format("woff2");
}
@font-face {
  font-family: "JetBrains Mono";
  font-style: normal;
  font-weight: 400;
  font-display: swap;
  src: url("/fonts/jetbrains-mono-400.woff2") format("woff2");
}
@font-face {
  font-family: "JetBrains Mono";
  font-style: normal;
  font-weight: 500;
  font-display: swap;
  src: url("/fonts/jetbrains-mono-500.woff2") format("woff2");
}

/* ---- Reset and root ---- */
* {
  box-sizing: border-box;
}

:root {
  color: var(--color-fg);
  background: var(--color-bg);
  font-family: var(--font-body);
  font-size: var(--text-base);
  line-height: var(--leading-relaxed);
  text-rendering: optimizeLegibility;
  -webkit-font-smoothing: antialiased;
}

body {
  position: relative;
  margin: 0;
  min-width: 320px;
  min-height: 100vh;
}

/* ---- Ambient accent glows. Large, heavily blurred, very low opacity —
   felt rather than seen. Sits behind all content. ---- */
body::before {
  content: "";
  position: fixed;
  inset: 0;
  z-index: 0;
  pointer-events: none;
  background:
    radial-gradient(60rem 40rem at 85% -10%, var(--color-accent-wash), transparent 70%),
    radial-gradient(50rem 35rem at -10% 110%, var(--color-accent-wash), transparent 70%);
}

body > * {
  position: relative;
  z-index: 1;
}

/* ---- Typography ---- */
h1, h2 {
  margin: 0;
  font-family: var(--font-display);
  font-weight: 400;
  letter-spacing: var(--tracking-tighter);
  line-height: var(--leading-none);
}

h1 { font-size: var(--text-4xl); }
h2 { font-size: var(--text-3xl); }

h3, h4 {
  margin: 0;
  font-family: var(--font-body);
  font-weight: 600;
  letter-spacing: var(--tracking-tight);
  line-height: var(--leading-tight);
}

h3 { font-size: var(--text-xl); }
h4 { font-size: var(--text-lg); }

code, kbd, samp, pre {
  font-family: var(--font-mono);
}

a {
  color: var(--color-accent);
  text-decoration: none;
}

a:hover {
  text-decoration: underline;
}

button, input, textarea, select {
  font: inherit;
}

button svg {
  flex: 0 0 auto;
}

/* ---- Gradient text ---- */
.gradient-text {
  background: var(--gradient-accent);
  -webkit-background-clip: text;
  background-clip: text;
  color: transparent;
}

/* Forced-colors replaces backgrounds but not the clip, so without this the
   text renders completely invisible in Windows High Contrast mode. */
@media (forced-colors: active) {
  .gradient-text {
    color: CanvasText;
    background: none;
  }
}

/* ---- Entrance reveals. Driven by useInView setting data-visible. ---- */
.reveal {
  opacity: 0;
  transform: translateY(28px);
  transition:
    opacity var(--duration-entrance) var(--ease-out),
    transform var(--duration-entrance) var(--ease-out);
  transition-delay: var(--reveal-delay, 0ms);
}

.reveal[data-visible="true"] {
  opacity: 1;
  transform: none;
}

/* ---- Continuous motion ---- */
@keyframes spin-slow {
  to { transform: rotate(360deg); }
}

@keyframes float {
  0%, 100% { transform: translateY(0); }
  50%      { transform: translateY(-10px); }
}

@keyframes float-alt {
  0%, 100% { transform: translateY(0); }
  50%      { transform: translateY(10px); }
}

@keyframes pulse-dot {
  0%, 100% { transform: scale(1);   opacity: 1; }
  50%      { transform: scale(1.3); opacity: 0.7; }
}

@keyframes spin {
  to { transform: rotate(360deg); }
}

/* ---- Focus ---- */
:focus-visible {
  outline: 2px solid var(--color-accent);
  outline-offset: 2px;
}

/* ---- Skip link ---- */
.skip-link {
  position: absolute;
  top: var(--space-2);
  left: var(--space-2);
  z-index: 100;
  transform: translateY(-200%);
  border-radius: var(--radius-lg);
  padding: var(--space-3) var(--space-4);
  color: var(--color-accent-fg);
  background: var(--gradient-accent);
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  letter-spacing: var(--tracking-label);
  text-transform: uppercase;
}

.skip-link:focus {
  transform: translateY(0);
}

/* ---- Reduced motion. Continuous animation stops, and reveals resolve to
   VISIBLE — disabling only the transition would leave content hidden. ---- */
@media (prefers-reduced-motion: reduce) {
  *,
  *::before,
  *::after {
    animation-duration: 0.01ms !important;
    animation-iteration-count: 1 !important;
    transition-duration: 0.01ms !important;
    scroll-behavior: auto !important;
  }

  .reveal {
    opacity: 1;
    transform: none;
  }
}
```

- [ ] **Step 5: Rewrite the primitives stylesheet**

Replace the entire contents of `web/src/styles/primitives.css`:

```css
/* ---- Buttons ---- */
.primary-button,
.secondary-button,
.destructive-button {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: var(--space-2);
  min-height: var(--control-height);
  border-radius: var(--radius-lg);
  padding: var(--space-3) var(--space-6);
  font-family: var(--font-body);
  font-size: var(--text-sm);
  font-weight: 500;
  letter-spacing: var(--tracking-tight);
  white-space: nowrap;
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-out);
}

.primary-button {
  border: 0;
  color: var(--color-accent-fg);
  background: var(--gradient-accent);
  box-shadow: var(--shadow-sm);
}

.primary-button:hover:not(:disabled) {
  transform: translateY(-2px);
  box-shadow: var(--shadow-accent-lg);
  filter: brightness(1.1);
}

.primary-button:active:not(:disabled) {
  transform: scale(0.98);
}

.secondary-button {
  border: 1px solid var(--color-border);
  color: var(--color-fg);
  background: transparent;
}

.secondary-button:hover:not(:disabled) {
  border-color: var(--color-accent-border);
  background: var(--color-muted);
  box-shadow: var(--shadow-sm);
}

.destructive-button {
  border: 0;
  color: var(--color-accent-fg);
  background: var(--color-danger);
  box-shadow: var(--shadow-sm);
}

.destructive-button:hover:not(:disabled) {
  transform: translateY(-2px);
  box-shadow: var(--shadow-lg);
  filter: brightness(1.05);
}

/* Arrow glyphs advance on hover. */
.primary-button .btn-arrow,
.destructive-button .btn-arrow {
  transition: transform var(--duration-fast) var(--ease-out);
}

.primary-button:hover .btn-arrow,
.destructive-button:hover .btn-arrow {
  transform: translateX(4px);
}

a.primary-button,
a.secondary-button {
  text-decoration: none;
}

button:disabled {
  cursor: not-allowed;
  opacity: 0.5;
}

.icon-button {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-width: var(--space-8);
  min-height: var(--space-8);
  border: 0;
  border-radius: var(--radius-md);
  color: var(--color-muted-fg);
  background: transparent;
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-out);
}

.icon-button:hover {
  color: var(--color-fg);
  background: var(--color-muted);
}

/* ---- Inputs ---- */
label {
  display: grid;
  gap: var(--space-2);
  color: var(--color-muted-fg);
  font-family: var(--font-body);
  font-size: var(--text-sm);
  font-weight: 500;
}

input,
textarea,
select {
  min-height: var(--control-height);
  min-width: 0;
  border: 1px solid var(--color-border);
  border-radius: var(--radius-lg);
  padding: var(--space-3) var(--space-4);
  color: var(--color-fg);
  background: var(--color-card);
  font-family: var(--font-body);
  font-size: var(--text-base);
  transition: border-color var(--duration-fast) var(--ease-out),
              box-shadow var(--duration-fast) var(--ease-out);
}

input::placeholder,
textarea::placeholder {
  color: var(--color-muted-fg);
}

input:focus,
textarea:focus,
select:focus {
  border-color: var(--color-accent);
  box-shadow: var(--shadow-ring);
  outline: none;
}

textarea {
  min-height: 96px;
  resize: vertical;
}

/* ---- Panels ---- */
.panel {
  min-width: 0;
  border: 1px solid var(--color-border);
  border-radius: var(--radius-xl);
  padding: var(--space-6);
  background: var(--color-card);
  box-shadow: var(--shadow-md);
  transition: box-shadow var(--duration-lift) var(--ease-out);
}

@media (min-width: 768px) {
  .panel {
    padding: var(--space-8);
  }
}

.panel.wide {
  grid-column: 1 / -1;
}

.panel h2 {
  margin: 0 0 var(--space-5);
  font-family: var(--font-body);
  font-weight: 600;
  font-size: var(--text-xl);
  letter-spacing: var(--tracking-tight);
  line-height: var(--leading-tight);
}

.panel > .secondary-button {
  margin-top: var(--space-5);
}

/* Gradient border with no wrapper element: the card colour paints the
   padding box while the gradient paints the border box. */
.panel--featured {
  border: 2px solid transparent;
  background:
    linear-gradient(var(--color-card), var(--color-card)) padding-box,
    var(--gradient-accent) border-box;
}

/* forced-colors replaces backgrounds, which is where this border's colour
   lives — without this the featured panel loses all emphasis. */
@media (forced-colors: active) {
  .panel--featured {
    border-color: CanvasText;
  }
}

.panel--inverted {
  position: relative;
  overflow: hidden;
  border-color: var(--color-fg);
  color: var(--color-card);
  background: var(--color-fg);
}

.panel--inverted::before {
  content: "";
  position: absolute;
  inset: 0;
  pointer-events: none;
  opacity: 0.03;
  background-image: radial-gradient(circle, var(--color-card) 1px, transparent 1px);
  background-size: 32px 32px;
}

.panel--inverted > * {
  position: relative;
}

/* ---- Page header ---- */
.page-header {
  margin-bottom: var(--space-8);
}

.page-header h1 {
  font-family: var(--font-display);
  font-size: var(--text-3xl);
  letter-spacing: var(--tracking-tighter);
  line-height: var(--leading-tight);
}

@media (min-width: 768px) {
  .page-header h1 {
    font-size: var(--text-4xl);
  }
}

.eyebrow {
  margin: 0 0 var(--space-3);
  color: var(--color-muted-fg);
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  letter-spacing: var(--tracking-label);
  text-transform: uppercase;
}

/* ---- Messaging ---- */
.alert {
  display: flex;
  align-items: flex-start;
  gap: var(--space-3);
  margin-bottom: var(--space-4);
  border: 1px solid var(--color-danger);
  border-radius: var(--radius-lg);
  padding: var(--space-4);
  color: var(--color-danger);
  background: var(--color-danger-soft);
  font-size: var(--text-sm);
}

.error-text {
  margin: var(--space-3) 0 0;
  color: var(--color-danger);
  font-size: var(--text-sm);
  font-weight: 500;
}

.muted {
  margin: 0;
  color: var(--color-muted-fg);
}
```

- [ ] **Step 6: Rewrite the guard test**

Replace the entire contents of `web/src/styles/styles.guard.test.ts`:

```ts
import { readdirSync, readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const STYLES_DIR = dirname(fileURLToPath(import.meta.url));

/** Stylesheets subject to the palette rules. tokens.css is the sole exemption. */
function convertedStylesheets(): string[] {
  return readdirSync(STYLES_DIR)
    .filter((name) => name.endsWith(".css") && name !== "tokens.css")
    .sort();
}

function read(name: string): string {
  // Comments may legitimately mention hex values while explaining a decision.
  return readFileSync(join(STYLES_DIR, name), "utf8").replace(/\/\*[\s\S]*?\*\//g, "");
}

function readAll(): string {
  return convertedStylesheets().map(read).join("\n");
}

const REQUIRED_TOKENS = [
  "--color-bg", "--color-fg", "--color-muted", "--color-muted-fg",
  "--color-border", "--color-card",
  "--color-accent", "--color-accent-2", "--color-accent-fg", "--gradient-accent",
  "--color-accent-soft", "--color-accent-border",
  "--color-success", "--color-warning", "--color-danger",
  "--color-success-dot", "--color-warning-dot",
  "--font-display", "--font-body", "--font-mono",
  "--radius-sm", "--radius-md", "--radius-lg", "--radius-xl", "--radius-full",
  "--shadow-sm", "--shadow-md", "--shadow-lg", "--shadow-xl",
  "--shadow-accent", "--shadow-accent-lg", "--shadow-ring",
  "--ease-out", "--duration-fast", "--duration-lift", "--duration-entrance"
];

describe("tokens.css", () => {
  it("defines every required custom property", () => {
    const tokens = readFileSync(join(STYLES_DIR, "tokens.css"), "utf8");
    for (const token of REQUIRED_TOKENS) {
      expect(tokens, `missing ${token}`).toContain(`${token}:`);
    }
  });

  it("uses the AA-safe status colours for text", () => {
    const tokens = readFileSync(join(STYLES_DIR, "tokens.css"), "utf8");
    // #16A34A is 3.24:1 on white and #D97706 is 3.20:1 — both fail AA for
    // normal text. They may only appear as the -dot variants.
    expect(tokens).toMatch(/--color-success:\s*#15803D/i);
    expect(tokens).toMatch(/--color-warning:\s*#B45309/i);
  });
});

describe.each(convertedStylesheets())("%s", (name) => {
  it("contains no raw hex colors", () => {
    const matches = read(name).match(/#[0-9a-fA-F]{3,8}\b/g) ?? [];
    expect(matches, `use var(--color-*) instead of ${matches.join(", ")}`).toEqual([]);
  });

  it("uses a radius token for every border-radius", () => {
    // The \s* must live INSIDE the lookahead. Left outside it can backtrack to
    // zero-width, so the lookahead inspects " var(...)" rather than "var(...)",
    // succeeds, and every compliant declaration is reported as a violation.
    const matches =
      read(name).match(/border-radius:(?!\s*var\(--radius-[a-z]+\)\s*;)[^;]+;/g) ?? [];
    expect(matches, `use var(--radius-*): ${matches.join(", ")}`).toEqual([]);
  });

  it("uses a shadow token for every box-shadow", () => {
    const matches =
      read(name).match(/box-shadow:(?!\s*var\(--shadow-[a-z-]+\)\s*;)[^;]+;/g) ?? [];
    expect(matches, `use var(--shadow-*): ${matches.join(", ")}`).toEqual([]);
  });
});

describe("accessibility fallbacks", () => {
  it("gives gradient text a forced-colors fallback", () => {
    // background-clip text with color:transparent renders invisible under
    // Windows High Contrast unless the colour is restored explicitly.
    const css = readAll();
    expect(css).toMatch(/@media\s*\(forced-colors:\s*active\)/);
    expect(css).toMatch(/\.gradient-text\s*\{[^}]*color:\s*CanvasText/);
  });
});
```

- [ ] **Step 7: Run the guard test**

```bash
cd web && npx vitest run src/styles/styles.guard.test.ts
```

Expected: PASS. If the `border-radius` or `box-shadow` assertions fail, read the reported declarations — they name the exact offending lines.

- [ ] **Step 8: Update the font preloads**

In `web/index.html`, replace the two existing `<link rel="preload">` lines with:

```html
    <link rel="preload" href="/fonts/inter-400.woff2" as="font" type="font/woff2" crossorigin />
    <link rel="preload" href="/fonts/calistoga-400.woff2" as="font" type="font/woff2" crossorigin />
```

- [ ] **Step 9: Run the full suite and build**

```bash
cd web && npm test 2>&1 | grep -E 'Test Files|Tests ' && npm run build 2>&1 | tail -3
```

Expected: `Test Files 34 passed (34)`, `Tests 227 passed (227)`. Build clean.

- [ ] **Step 10: Verify visually**

The app cannot boot without Keycloak, so render the compiled CSS against representative markup:

```bash
cd web && npm run build && ls dist/assets/*.css
```

Write a scratch HTML file that links that stylesheet and contains a `.page-header` with an `h1`, a `.panel`, a `.panel--featured`, a `.primary-button`, a `.secondary-button`, and an `input`. Serve `dist/` with `npx vite preview` and screenshot it with headless Chrome using `--virtual-time-budget=8000`. Confirm Calistoga renders on the `h1` (distinctly rounded slab serif, clearly not Georgia), the gradient button shows its blue gradient, and panels have rounded corners with visible shadow.

Put the scratch file in `dist/`, which is gitignored, and delete it afterwards.

- [ ] **Step 11: Commit**

```bash
git add scripts/vendor-fonts.sh web/public/fonts web/src/styles web/index.html
git commit -m "feat(web): retheme token layer, base, and primitives to Minimalist Modern"
```

---

### Task 2: Motion hook and showpiece components

Adds the four new shared modules. No existing screen changes yet.

**Files:**
- Create: `web/src/shared/useInView.ts`
- Create: `web/src/shared/useInView.test.tsx`
- Create: `web/src/shared/SectionLabel.tsx`
- Create: `web/src/shared/SectionLabel.test.tsx`
- Create: `web/src/shared/HeroGraphic.tsx`
- Create: `web/src/shared/StatBand.tsx`
- Create: `web/src/shared/StatBand.test.tsx`
- Modify: `web/src/styles/primitives.css` (append)

**Interfaces:**
- Consumes: all tokens and `.reveal`, keyframes from Task 1.
- Produces:
  - `useInView<T extends Element>(): { ref: RefObject<T | null>; visible: boolean }`
  - `<SectionLabel pulse?: boolean>{children}</SectionLabel>`
  - `<HeroGraphic />` — no props
  - `<StatBand items: { label: string; value: string | number }[] />`
  - CSS classes `.section-label`, `.hero-graphic`, `.stat-band`

- [ ] **Step 1: Write the failing useInView test**

Create `web/src/shared/useInView.test.tsx`:

```tsx
// @vitest-environment jsdom
import { render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useInView } from "./useInView";

type ObserverCallback = (entries: { isIntersecting: boolean }[]) => void;

let callbacks: ObserverCallback[] = [];
let disconnected = 0;

beforeEach(() => {
  callbacks = [];
  disconnected = 0;
  vi.stubGlobal(
    "IntersectionObserver",
    class {
      constructor(cb: ObserverCallback) {
        callbacks.push(cb);
      }
      observe() {}
      disconnect() {
        disconnected += 1;
      }
      unobserve() {}
    }
  );
});

afterEach(() => {
  vi.unstubAllGlobals();
});

function Probe() {
  const { ref, visible } = useInView<HTMLDivElement>();
  return (
    <div ref={ref} data-testid="probe" data-visible={visible}>
      content
    </div>
  );
}

describe("useInView", () => {
  it("starts not visible", () => {
    render(<Probe />);
    expect(screen.getByTestId("probe").dataset.visible).toBe("false");
  });

  it("becomes visible once the element intersects", () => {
    render(<Probe />);
    callbacks[0]([{ isIntersecting: true }]);
    expect(screen.getByTestId("probe").dataset.visible).toBe("true");
  });

  it("stays visible after the element leaves the viewport", () => {
    render(<Probe />);
    callbacks[0]([{ isIntersecting: true }]);
    callbacks[0]([{ isIntersecting: false }]);
    expect(screen.getByTestId("probe").dataset.visible).toBe("true");
  });

  it("disconnects the observer on unmount", () => {
    const view = render(<Probe />);
    view.unmount();
    expect(disconnected).toBeGreaterThan(0);
  });
});
```

- [ ] **Step 2: Run it to verify it fails**

```bash
cd web && npx vitest run src/shared/useInView.test.tsx
```

Expected: FAIL — cannot resolve `./useInView`.

- [ ] **Step 3: Implement the hook**

Create `web/src/shared/useInView.ts`:

```ts
import { useEffect, useRef, useState } from "react";

/**
 * Fires once when the element first enters the viewport, then disconnects.
 * Thresholds match the design system's viewport options: 15% visible, with a
 * 60px bottom margin so content settles before it animates.
 */
export function useInView<T extends Element>() {
  const ref = useRef<T | null>(null);
  const [visible, setVisible] = useState(false);

  useEffect(() => {
    const node = ref.current;
    if (!node) return;

    // Environments without IntersectionObserver (older jsdom, SSR) should show
    // content rather than hide it forever.
    if (typeof IntersectionObserver === "undefined") {
      setVisible(true);
      return;
    }

    const observer = new IntersectionObserver(
      (entries) => {
        if (entries.some((entry) => entry.isIntersecting)) {
          setVisible(true);
          observer.disconnect();
        }
      },
      { threshold: 0.15, rootMargin: "0px 0px -60px 0px" }
    );

    observer.observe(node);
    return () => observer.disconnect();
  }, []);

  return { ref, visible };
}
```

- [ ] **Step 4: Run it to verify it passes**

```bash
cd web && npx vitest run src/shared/useInView.test.tsx
```

Expected: PASS, 4 tests.

- [ ] **Step 5: Write the SectionLabel and StatBand tests**

Create `web/src/shared/SectionLabel.test.tsx`:

```tsx
// @vitest-environment jsdom
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import SectionLabel from "./SectionLabel";

describe("SectionLabel", () => {
  it("renders its text", () => {
    render(<SectionLabel>Stacks</SectionLabel>);
    expect(screen.getByText("Stacks")).toBeTruthy();
  });

  it("marks the dot as decorative for screen readers", () => {
    const { container } = render(<SectionLabel pulse>Runs</SectionLabel>);
    const dot = container.querySelector(".section-label__dot");
    expect(dot?.getAttribute("aria-hidden")).toBe("true");
  });
});
```

Create `web/src/shared/StatBand.test.tsx`:

```tsx
// @vitest-environment jsdom
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import StatBand from "./StatBand";

describe("StatBand", () => {
  it("renders a label and value for each item", () => {
    render(<StatBand items={[{ label: "Stacks", value: 4 }, { label: "Running", value: 1 }]} />);
    expect(screen.getByText("Stacks")).toBeTruthy();
    expect(screen.getByText("4")).toBeTruthy();
    expect(screen.getByText("Running")).toBeTruthy();
    expect(screen.getByText("1")).toBeTruthy();
  });

  it("renders nothing when given no items", () => {
    const { container } = render(<StatBand items={[]} />);
    expect(container.querySelector(".stat-band")).toBeNull();
  });
});
```

- [ ] **Step 6: Run both to verify they fail**

```bash
cd web && npx vitest run src/shared/SectionLabel.test.tsx src/shared/StatBand.test.tsx
```

Expected: FAIL — cannot resolve `./SectionLabel` and `./StatBand`.

- [ ] **Step 7: Implement the three components**

Create `web/src/shared/SectionLabel.tsx`:

```tsx
import type { ReactNode } from "react";

export default function SectionLabel({
  children,
  pulse = false
}: {
  children: ReactNode;
  pulse?: boolean;
}) {
  return (
    <span className="section-label">
      <span
        className={`section-label__dot${pulse ? " section-label__dot--pulse" : ""}`}
        aria-hidden="true"
      />
      {children}
    </span>
  );
}
```

Create `web/src/shared/StatBand.tsx`:

```tsx
export type Stat = { label: string; value: string | number };

export default function StatBand({ items }: { items: Stat[] }) {
  if (items.length === 0) return null;

  return (
    <section className="stat-band panel panel--inverted" data-testid="stat-band">
      <dl className="stat-band__grid">
        {items.map((item) => (
          <div key={item.label} className="stat-band__item">
            <dd className="stat-band__value">{item.value}</dd>
            <dt className="stat-band__label">{item.label}</dt>
          </div>
        ))}
      </dl>
    </section>
  );
}
```

Create `web/src/shared/HeroGraphic.tsx`:

```tsx
/**
 * Decorative showpiece. Pure CSS and inline SVG — no data, no props, no JS
 * animation. Hidden from assistive technology entirely.
 */
export default function HeroGraphic() {
  return (
    <div className="hero-graphic" aria-hidden="true">
      <svg className="hero-graphic__ring" viewBox="0 0 200 200" role="presentation">
        <circle
          cx="100"
          cy="100"
          r="92"
          fill="none"
          stroke="currentColor"
          strokeWidth="1"
          strokeDasharray="4 8"
        />
      </svg>

      <div className="hero-graphic__blob" />

      <div className="hero-graphic__card hero-graphic__card--one">
        <span className="hero-graphic__bar" />
        <span className="hero-graphic__bar hero-graphic__bar--short" />
      </div>

      <div className="hero-graphic__card hero-graphic__card--two">
        <span className="hero-graphic__bar hero-graphic__bar--short" />
        <span className="hero-graphic__bar" />
      </div>

      <div className="hero-graphic__dots">
        {Array.from({ length: 9 }, (_, i) => (
          <span key={i} />
        ))}
      </div>

      <div className="hero-graphic__corner" />
    </div>
  );
}
```

- [ ] **Step 8: Append the component styles**

Append to `web/src/styles/primitives.css`:

```css
/* ---- Section label ---- */
.section-label {
  display: inline-flex;
  align-items: center;
  gap: var(--space-3);
  border: 1px solid var(--color-accent-border);
  border-radius: var(--radius-full);
  padding: var(--space-2) var(--space-5);
  color: var(--color-accent);
  background: var(--color-accent-soft);
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  letter-spacing: var(--tracking-label);
  text-transform: uppercase;
}

.section-label__dot {
  width: 8px;
  height: 8px;
  border-radius: var(--radius-full);
  background: var(--color-accent);
}

.section-label__dot--pulse {
  animation: pulse-dot 2s var(--ease-out) infinite;
}

/* ---- Stat band ---- */
.stat-band {
  margin: var(--space-8) 0;
}

.stat-band__grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--space-6);
  margin: 0;
}

@media (min-width: 768px) {
  .stat-band__grid {
    grid-template-columns: repeat(4, minmax(0, 1fr));
  }
}

.stat-band__item {
  display: grid;
  gap: var(--space-1);
}

.stat-band__value {
  margin: 0;
  font-family: var(--font-display);
  font-size: var(--text-3xl);
  line-height: var(--leading-none);
}

.stat-band__label {
  color: var(--color-muted-fg);
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  letter-spacing: var(--tracking-label);
  text-transform: uppercase;
}

/* ---- Hero graphic ---- */
.hero-graphic {
  position: relative;
  width: 100%;
  max-width: 420px;
  aspect-ratio: 1;
  color: var(--color-accent-border);
}

.hero-graphic__ring {
  position: absolute;
  inset: 0;
  width: 100%;
  height: 100%;
  animation: spin-slow 60s linear infinite;
}

.hero-graphic__blob {
  position: absolute;
  inset: 18%;
  border-radius: var(--radius-full);
  background: var(--gradient-accent);
  opacity: 0.12;
}

.hero-graphic__card {
  position: absolute;
  display: grid;
  gap: var(--space-2);
  width: 45%;
  border: 1px solid var(--color-border);
  border-radius: var(--radius-lg);
  padding: var(--space-4);
  background: var(--color-card);
  box-shadow: var(--shadow-lg);
}

.hero-graphic__card--one {
  top: 12%;
  left: 0;
  animation: float 5s ease-in-out infinite;
}

.hero-graphic__card--two {
  right: 0;
  bottom: 16%;
  animation: float-alt 4s ease-in-out infinite;
}

.hero-graphic__bar {
  height: 6px;
  border-radius: var(--radius-full);
  background: var(--gradient-accent);
}

.hero-graphic__bar--short {
  width: 55%;
  background: var(--color-border);
}

.hero-graphic__dots {
  position: absolute;
  right: 12%;
  top: 8%;
  display: grid;
  grid-template-columns: repeat(3, 4px);
  gap: var(--space-2);
}

.hero-graphic__dots span {
  width: 4px;
  height: 4px;
  border-radius: var(--radius-full);
  background: var(--color-accent);
  opacity: 0.35;
}

.hero-graphic__corner {
  position: absolute;
  left: 8%;
  bottom: 6%;
  width: 48px;
  height: 48px;
  border-radius: var(--radius-md);
  background: var(--color-accent);
  box-shadow: var(--shadow-accent);
}
```

- [ ] **Step 9: Run the new tests**

```bash
cd web && npx vitest run src/shared/SectionLabel.test.tsx src/shared/StatBand.test.tsx
```

Expected: PASS, 4 tests.

- [ ] **Step 10: Run the full suite and build**

```bash
cd web && npm test 2>&1 | grep -E 'Test Files|Tests ' && npm run build 2>&1 | tail -3
```

Expected: `Test Files 37 passed (37)`, `Tests 235 passed (235)`. Build clean.

- [ ] **Step 11: Commit**

```bash
git add web/src/shared/useInView.ts web/src/shared/useInView.test.tsx \
        web/src/shared/SectionLabel.tsx web/src/shared/SectionLabel.test.tsx \
        web/src/shared/HeroGraphic.tsx \
        web/src/shared/StatBand.tsx web/src/shared/StatBand.test.tsx \
        web/src/styles/primitives.css
git commit -m "feat(web): add useInView hook, section label, hero graphic, stat band"
```

---

### Task 3: Status tones and role badges

Creates the five-tone classifier that was designed but never built, and converts both status rendering and role badges to the semantic palette.

**Files:**
- Create: `web/src/shared/statusTone.ts`
- Create: `web/src/shared/statusTone.test.ts`
- Rewrite: `web/src/shared/StatusRow.tsx`
- Modify: `web/src/features/stacks/StackAccessScreen.tsx:145`
- Modify: `web/src/styles/primitives.css` (append)
- Modify: `web/src/styles.css` (drain `.role-badge*`, `.status-row*`)

**Interfaces:**
- Consumes: tokens from Task 1.
- Produces: `statusTone(value: string): StatusTone` where `type StatusTone = "settled" | "progress" | "waiting" | "failed" | "canceled"`, and `statusGlyph(tone: StatusTone): string`. CSS classes `.status-tone`, `.status-tone--{tone}`, `.role-badge--{owner|operator|approver|viewer}`.

- [ ] **Step 1: Write the failing classifier test**

Create `web/src/shared/statusTone.test.ts`:

```ts
import { describe, expect, it } from "vitest";
import { statusGlyph, statusTone } from "./statusTone";

describe("statusTone", () => {
  it("classifies terminal success states as settled", () => {
    expect(statusTone("completed")).toBe("settled");
    expect(statusTone("active")).toBe("settled");
    expect(statusTone("approved")).toBe("settled");
    expect(statusTone("lock_released")).toBe("settled");
  });

  it("classifies finished pipeline phases as settled", () => {
    expect(statusTone("init_finished")).toBe("settled");
    expect(statusTone("plan_finished")).toBe("settled");
    expect(statusTone("apply_finished")).toBe("settled");
    expect(statusTone("destroy_finished")).toBe("settled");
  });

  it("classifies intermediate pipeline steps as settled", () => {
    expect(statusTone("workspace_prepared")).toBe("settled");
    expect(statusTone("source_fetched")).toBe("settled");
    expect(statusTone("workspace_selected")).toBe("settled");
  });

  it("classifies active work as progress", () => {
    expect(statusTone("running")).toBe("progress");
    expect(statusTone("validating")).toBe("progress");
    expect(statusTone("canceling")).toBe("progress");
    expect(statusTone("init_started")).toBe("progress");
    expect(statusTone("plan_started")).toBe("progress");
    expect(statusTone("apply_started")).toBe("progress");
    expect(statusTone("destroy_started")).toBe("progress");
  });

  it("classifies queued and blocked states as waiting", () => {
    expect(statusTone("pending")).toBe("waiting");
    expect(statusTone("pending_validation")).toBe("waiting");
    expect(statusTone("queued")).toBe("waiting");
    expect(statusTone("locked")).toBe("waiting");
    expect(statusTone("waiting_approval")).toBe("waiting");
  });

  it("classifies failure states as failed", () => {
    expect(statusTone("failed")).toBe("failed");
    expect(statusTone("invalid")).toBe("failed");
    expect(statusTone("error")).toBe("failed");
  });

  it("classifies cancellation as canceled", () => {
    expect(statusTone("canceled")).toBe("canceled");
    expect(statusTone("cancel_requested")).toBe("canceled");
  });

  it("treats 'not ...' phrasing used by StatusRow callers as waiting", () => {
    expect(statusTone("not configured")).toBe("waiting");
    expect(statusTone("not installed")).toBe("waiting");
  });

  it("falls back to settled for unrecognised values", () => {
    expect(statusTone("something_new")).toBe("settled");
  });
});

describe("statusGlyph", () => {
  it("gives every tone a distinct non-empty glyph", () => {
    const tones = ["settled", "progress", "waiting", "failed", "canceled"] as const;
    const glyphs = tones.map(statusGlyph);
    expect(new Set(glyphs).size).toBe(tones.length);
    for (const glyph of glyphs) {
      expect(glyph.trim().length).toBeGreaterThan(0);
    }
  });
});
```

- [ ] **Step 2: Run it to verify it fails**

```bash
cd web && npx vitest run src/shared/statusTone.test.ts
```

Expected: FAIL — cannot resolve `./statusTone`.

- [ ] **Step 3: Implement the classifier**

Create `web/src/shared/statusTone.ts`:

```ts
/**
 * The API exposes three separate status unions (TemplateRunStatus with 21
 * values, TemplateRegistrationStatus with 5, TemplateRevisionStatus with 4).
 * Rendering 30 bespoke treatments would be unmaintainable, so every status
 * maps onto one of five visual tones.
 */
export type StatusTone = "settled" | "progress" | "waiting" | "failed" | "canceled";

const FAILED = new Set(["failed", "invalid", "error"]);
const CANCELED = new Set(["canceled", "cancel_requested"]);
const WAITING = new Set([
  "pending",
  "pending_validation",
  "queued",
  "locked",
  "waiting_approval"
]);
const PROGRESS = new Set(["running", "validating", "canceling"]);

export function statusTone(value: string): StatusTone {
  if (FAILED.has(value)) return "failed";
  if (CANCELED.has(value)) return "canceled";
  if (WAITING.has(value)) return "waiting";
  if (PROGRESS.has(value)) return "progress";

  // Phase statuses arrive as `<phase>_started` / `<phase>_finished`.
  if (value.endsWith("_started")) return "progress";

  // Callers pass human phrases such as "not configured" for absent resources.
  if (value.startsWith("not ")) return "waiting";

  // Finished phases and intermediate pipeline steps are settled, matching the
  // previous behaviour where they received the default success icon.
  return "settled";
}

const GLYPHS: Record<StatusTone, string> = {
  settled: "●",
  progress: "◐",
  waiting: "○",
  failed: "✕",
  canceled: "⊘"
};

export function statusGlyph(tone: StatusTone): string {
  return GLYPHS[tone];
}
```

- [ ] **Step 4: Run it to verify it passes**

```bash
cd web && npx vitest run src/shared/statusTone.test.ts
```

Expected: PASS, 10 tests.

- [ ] **Step 5: Rewrite StatusRow**

Replace the entire contents of `web/src/shared/StatusRow.tsx`. This drops the three lucide icon imports in favour of typographic glyphs carried by the tinted pill.

```tsx
import { statusGlyph, statusTone } from "./statusTone";

export default function StatusRow({ label, value }: { label: string; value: string }) {
  const tone = statusTone(value);

  return (
    <div className="status-row" data-status={value}>
      <span>{label}</span>
      <strong>
        <span className={`status-tone status-tone--${tone}`}>
          <span className="status-tone__glyph" aria-hidden="true">
            {statusGlyph(tone)}
          </span>
          {value}
        </span>
      </strong>
    </div>
  );
}
```

The glyph is `aria-hidden` because the status text sits immediately beside it; screen readers announce the value itself.

- [ ] **Step 6: Convert the role badge markup**

In `web/src/features/stacks/StackAccessScreen.tsx` line 145, change the class construction from the bare role name to a modifier:

```tsx
                <span className={`role-badge role-badge--${grant.role}`}>{grant.role}</span>
```

- [ ] **Step 7: Append status and badge styles**

Append to `web/src/styles/primitives.css`:

```css
/* ---- Status tones ---- */
.status-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
  margin-top: var(--space-4);
  border-top: 1px solid var(--color-border);
  padding-top: var(--space-3);
  color: var(--color-muted-fg);
  font-size: var(--text-sm);
}

.status-row strong {
  min-width: 0;
  font-weight: 500;
  text-align: right;
}

.status-tone {
  display: inline-flex;
  align-items: center;
  gap: var(--space-2);
  border-radius: var(--radius-full);
  padding: var(--space-1) var(--space-3);
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  overflow-wrap: anywhere;
}

.status-tone__glyph {
  font-size: var(--text-sm);
  line-height: 1;
}

.status-tone--settled {
  color: var(--color-success);
  background: var(--color-success-soft);
}

.status-tone--progress {
  color: var(--color-accent);
  background: var(--color-accent-soft);
}

.status-tone--progress .status-tone__glyph {
  animation: pulse-dot 2s var(--ease-out) infinite;
}

.status-tone--waiting {
  color: var(--color-warning);
  background: var(--color-warning-soft);
}

.status-tone--failed {
  color: var(--color-danger);
  background: var(--color-danger-soft);
}

.status-tone--canceled {
  color: var(--color-muted-fg);
  background: var(--color-muted);
}

/* Destruction is settled, not failed, but consequential enough to deserve a
   heavier rule than an ordinary finished phase. */
.status-row[data-status="destroy_finished"] {
  border-top-width: 2px;
  border-top-color: var(--color-fg);
}

/* ---- Role badges ---- */
.role-badge {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-width: 84px;
  border-radius: var(--radius-full);
  padding: var(--space-1) var(--space-3);
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  letter-spacing: var(--tracking-wide);
  text-transform: uppercase;
}

.role-badge--owner {
  color: var(--color-accent);
  background: var(--color-accent-soft);
}

.role-badge--operator {
  color: var(--color-success);
  background: var(--color-success-soft);
}

.role-badge--approver {
  color: var(--color-warning);
  background: var(--color-warning-soft);
}

.role-badge--viewer {
  color: var(--color-muted-fg);
  background: var(--color-muted);
}
```

- [ ] **Step 8: Drain the superseded legacy rules**

Delete these blocks from `web/src/styles.css`, identified by selector since line numbers shift as you delete: `.status-row`, `.status-row strong`, `.role-badge`, `.role-badge.owner`, `.role-badge.operator`, `.role-badge.approver`, `.role-badge.viewer`.

- [ ] **Step 9: Run the full suite and build**

```bash
cd web && npm test 2>&1 | grep -E 'Test Files|Tests ' && npm run build 2>&1 | tail -3
```

Expected: `Test Files 38 passed (38)`, `Tests 245 passed (245)`. Build clean.

If a `StatusRow` or `StackAccessScreen` test fails, a `data-testid` was almost certainly lost — re-check Steps 5 and 6 against the originals before touching any test.

- [ ] **Step 10: Commit**

```bash
git add web/src/shared/statusTone.ts web/src/shared/statusTone.test.ts \
        web/src/shared/StatusRow.tsx web/src/features/stacks/StackAccessScreen.tsx \
        web/src/styles/primitives.css web/src/styles.css
git commit -m "feat(web): five-tone semantic status system and tinted role badges"
```

---

### Task 4: Application shell and navigation

**Files:**
- Create: `web/src/styles/features.css`
- Modify: `web/src/styles.css` (add import; drain shell rules)
- Modify: `web/src/app/AppShell.tsx`

**Interfaces:**
- Consumes: tokens (Task 1), `.skip-link` (Task 1).
- Produces: `web/src/styles/features.css`, extended by Tasks 6 and 7.

- [ ] **Step 1: Create features.css with the shell rules**

Create `web/src/styles/features.css`:

```css
/* ---- Application frame ---- */
.app-shell {
  min-height: 100vh;
  padding: var(--space-6);
}

.app-frame {
  min-height: 100vh;
}

.app-frame-header {
  position: sticky;
  top: 0;
  /* Must stay BELOW .search-dropdown (10) and .undo-banner (20). base.css
     establishes one stacking context at the app root and nothing uses
     portals, so an opaque header with a higher z-index would paint over an
     open dropdown as soon as the page scrolls. */
  z-index: 5;
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: var(--space-3) var(--space-6);
  border-bottom: 1px solid var(--color-border);
  padding: var(--space-4) var(--space-6);
  background: var(--color-card);
  box-shadow: var(--shadow-sm);
}

.app-frame-brand {
  display: flex;
  align-items: center;
  gap: var(--space-8);
}

.app-wordmark {
  font-family: var(--font-display);
  font-size: var(--text-xl);
  line-height: 1;
}

.app-nav {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}

.app-nav a {
  position: relative;
  border-radius: var(--radius-md);
  padding: var(--space-2) var(--space-3);
  color: var(--color-muted-fg);
  font-size: var(--text-sm);
  font-weight: 500;
  text-decoration: none;
  transition: all var(--duration-fast) var(--ease-out);
}

.app-nav a:hover {
  color: var(--color-fg);
  background: var(--color-muted);
  text-decoration: none;
}

.app-frame-identity {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--space-3) var(--space-5);
  font-size: var(--text-sm);
}

.identity-menu {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  color: var(--color-muted-fg);
}

.identity-menu button {
  min-height: var(--touch-target);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-lg);
  padding: var(--space-2) var(--space-4);
  color: var(--color-fg);
  background: transparent;
  font-size: var(--text-sm);
  font-weight: 500;
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-out);
}

.identity-menu button:hover {
  border-color: var(--color-accent-border);
  background: var(--color-muted);
}

.app-frame-identity .runtime-field {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}

.app-frame-identity .runtime-value {
  min-height: 0;
  padding: var(--space-1) var(--space-3);
  font-size: var(--text-xs);
}

.app-frame-content {
  width: min(1280px, 100%);
  margin: 0 auto;
  padding: var(--space-10) var(--space-6);
}

/* The legacy console still carries its own standalone shell padding; zero it
   out when nested inside the router frame so "/" doesn't double-pad. */
.app-frame-content .app-shell {
  min-height: 0;
  padding: 0;
}

.workspace {
  width: min(1280px, 100%);
  margin: 0 auto;
}

.workspace-header {
  display: flex;
  align-items: flex-end;
  justify-content: space-between;
  gap: var(--space-6);
  margin-bottom: var(--space-6);
}

.runtime-fields {
  display: grid;
  grid-template-columns: repeat(2, minmax(140px, 1fr));
  gap: var(--space-3);
}

.runtime-field {
  display: grid;
  gap: var(--space-2);
  color: var(--color-muted-fg);
  font-size: var(--text-sm);
}

.runtime-value {
  display: flex;
  align-items: center;
  min-height: var(--control-height);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-lg);
  padding: var(--space-2) var(--space-3);
  color: var(--color-fg);
  background: var(--color-muted);
  font-family: var(--font-mono);
  font-size: var(--text-sm);
}

.debug-panel {
  margin: var(--space-8) var(--space-6);
  border-top: 1px solid var(--color-border);
  padding-top: var(--space-4);
  font-family: var(--font-mono);
  font-size: var(--text-xs);
}

.debug-panel summary {
  color: var(--color-muted-fg);
  letter-spacing: var(--tracking-label);
  text-transform: uppercase;
  cursor: pointer;
}

.id-grid {
  display: grid;
  grid-template-columns: max-content minmax(0, 1fr);
  gap: var(--space-2) var(--space-4);
  margin: var(--space-4) 0 0;
}

.id-grid dt {
  color: var(--color-muted-fg);
}

.id-grid dd {
  min-width: 0;
  margin: 0;
  overflow-wrap: anywhere;
  font-family: var(--font-mono);
}
```

- [ ] **Step 2: Import features.css and drain the legacy shell rules**

Add to `web/src/styles.css` after the primitives import:

```css
@import "./styles/features.css";
```

Delete from `styles.css`: `.app-shell`, `.app-frame`, `.app-frame-header`, `.app-nav`, `.app-nav a`, `.app-nav a:hover`, `.app-frame-identity`, `.identity-menu`, `.identity-menu button`, `.app-frame-identity .runtime-field`, `.app-frame-identity .runtime-value`, `.app-frame-content`, `.app-frame-content .app-shell`, `.workspace`, `.workspace-header`, `.runtime-fields`, `.runtime-field`, `.runtime-value`, `.id-grid`, `.id-grid dt`, `.id-grid dd`.

- [ ] **Step 3: Add the wordmark and skip link to AppShell**

In `web/src/app/AppShell.tsx`, replace the opening of the returned JSX — from `<div className="app-frame">` through the closing `</nav>` — with the version below. Every existing `data-testid` is preserved; the additions are the skip link, the wordmark wrapper, and `id="main-content"`.

```tsx
    <div className="app-frame">
      <a className="skip-link" href="#main-content">
        Skip to content
      </a>
      <header className="app-frame-header">
        <div className="app-frame-brand">
          <span className="app-wordmark">tflive</span>
          <nav className="app-nav" aria-label="Primary">
            {navItems.map((item) => (
              <Link key={item.to} to={item.to}>
                {item.label}
              </Link>
            ))}
          </nav>
        </div>
```

Then change the `<main>` element to carry the anchor target:

```tsx
      <main className="app-frame-content" id="main-content" tabIndex={-1}>
        <Outlet />
      </main>
```

`tabIndex={-1}` is required, not optional. `<main>` is not natively focusable, so without it the browser scrolls the target into view but leaves keyboard focus where it was — the user's next Tab resumes from the top of the document and the skip link accomplishes nothing.

- [ ] **Step 4: Run the full suite and build**

```bash
cd web && npm test 2>&1 | grep -E 'Test Files|Tests ' && npm run build 2>&1 | tail -3
```

Expected: `Test Files 38 passed (38)`, `Tests 248 passed (248)` — creating `features.css` adds three guard assertions. Build clean. `AppShell.test.tsx` must still pass; if it does not, a `data-testid` was dropped in Step 3.

- [ ] **Step 5: Commit**

```bash
git add web/src/styles/features.css web/src/styles.css web/src/app/AppShell.tsx
git commit -m "feat(web): restyle application shell with sticky header and wordmark"
```

---

### Task 5: Full-viewport screens with showpieces

**Files:**
- Modify: `web/src/app/NotFound.tsx`
- Modify: `web/src/app/AccessDenied.tsx`
- Modify: `web/src/app/ServiceUnavailable.tsx`
- Modify: `web/src/app/RoutePlaceholder.tsx`
- Modify: `web/src/styles/features.css` (append)

**Interfaces:**
- Consumes: `SectionLabel` and `HeroGraphic` (Task 2), `.gradient-text` (Task 1), `.secondary-button` (Task 1).
- Produces: `.showcase`, `.showcase__body`, `.showcase__title`, `.showcase__lede`.

- [ ] **Step 1: Read the four current screens**

```bash
cd web && cat src/app/NotFound.tsx src/app/AccessDenied.tsx src/app/ServiceUnavailable.tsx src/app/RoutePlaceholder.tsx
```

Note every `data-testid` and every string of user-facing copy. Both must survive verbatim — the tests assert on copy, and rewording is out of scope for a restyle.

- [ ] **Step 2: Append the showcase styles**

Append to `web/src/styles/features.css`:

```css
/* ---- Full-viewport showcase screens ---- */
.showcase {
  display: grid;
  align-items: center;
  gap: var(--space-12);
  min-height: 60vh;
  padding: var(--space-12) 0;
}

@media (min-width: 900px) {
  .showcase {
    grid-template-columns: 1.1fr 0.9fr;
  }
}

.showcase__body {
  display: grid;
  gap: var(--space-5);
  justify-items: start;
}

.showcase__title {
  margin: 0;
  font-family: var(--font-display);
  font-size: var(--text-5xl);
  letter-spacing: var(--tracking-tighter);
  line-height: var(--leading-none);
}

@media (min-width: 768px) {
  .showcase__title {
    font-size: var(--text-6xl);
  }
}

.showcase__lede {
  max-width: 46ch;
  margin: 0;
  color: var(--color-muted-fg);
  font-size: var(--text-lg);
  line-height: var(--leading-relaxed);
}

/* The decorative graphic is noise on a small screen — hide it rather than
   shrinking it into illegibility. */
.showcase__visual {
  display: none;
}

@media (min-width: 900px) {
  .showcase__visual {
    display: flex;
    justify-content: center;
  }
}

/* Compact variant, used by the stacks empty state in Task 6: it sits inside
   a page that already has a header, so it neither fills the viewport nor
   needs a full-size title. These two values are the only difference. */
.showcase--compact {
  min-height: 0;
}

.showcase--compact .showcase__title {
  font-size: var(--text-4xl);
}

@media (min-width: 768px) {
  .showcase--compact .showcase__title {
    font-size: var(--text-5xl);
  }
}
```

- [ ] **Step 3: Restructure each screen**

Apply this shape to all four, substituting the label and title, and keeping each file's existing copy, links, and `data-testid` attributes exactly as found in Step 1.

| File | Label | Title | Gradient word |
|---|---|---|---|
| `NotFound.tsx` | `Error 404` | `Page not found` | `found` |
| `AccessDenied.tsx` | `Access` | `Access denied` | `denied` |
| `ServiceUnavailable.tsx` | `Status` | `Service unavailable` | `unavailable` |
| `RoutePlaceholder.tsx` | `Coming soon` | `Not built yet` | `yet` |

The pattern, shown for `NotFound.tsx`:

```tsx
import HeroGraphic from "../shared/HeroGraphic";
import SectionLabel from "../shared/SectionLabel";

// ...

    <section className="showcase" data-testid="not-found">
      <div className="showcase__body">
        <SectionLabel>Error 404</SectionLabel>
        <h1 className="showcase__title">
          Page not <span className="gradient-text">found</span>
        </h1>
        <p className="showcase__lede">{/* existing copy, unchanged */}</p>
        {/* existing link/button, unchanged, with className="secondary-button" */}
      </div>
      <div className="showcase__visual">
        <HeroGraphic />
      </div>
    </section>
```

Keep each file's original root `data-testid` rather than the illustrative `not-found` shown here.

- [ ] **Step 4: Run the full suite and build**

```bash
cd web && npm test 2>&1 | grep -E 'Test Files|Tests ' && npm run build 2>&1 | tail -3
```

Expected: `Test Files 38 passed (38)`, `Tests 248 passed (248)`. `NotFound.test.tsx`, `AccessDenied.test.tsx`, `ServiceUnavailable.test.tsx`, and `RoutePlaceholder.test.tsx` must pass unchanged. A failure means copy or a test id changed — fix the component, never the test.

- [ ] **Step 5: Commit**

```bash
git add web/src/app/NotFound.tsx web/src/app/AccessDenied.tsx \
        web/src/app/ServiceUnavailable.tsx web/src/app/RoutePlaceholder.tsx \
        web/src/styles/features.css
git commit -m "feat(web): showpiece treatment for error and placeholder screens"
```

---

### Task 6: Stacks screens, empty-state hero, and stats band

**Files:**
- Modify: `web/src/features/stacks/StacksListScreen.tsx`
- Modify: `web/src/features/stacks/StackDetailShell.tsx`
- Modify: `web/src/styles/features.css` (append)
- Modify: `web/src/styles.css` (drain stacks rules)

**Interfaces:**
- Consumes: `SectionLabel`, `HeroGraphic`, `StatBand` (Task 2), `useInView` (Task 2), `statusTone` (Task 3).
- Produces: `.stacks-list`, `.stack-detail-tabs`, `.grant-row`, `.credential-row`, `.search-dropdown` treatments.

- [ ] **Step 1: Append the stacks styles**

Append to `web/src/styles/features.css`:

```css
/* ---- Stacks list ---- */
.stacks-list-header {
  display: flex;
  align-items: flex-end;
  justify-content: space-between;
  gap: var(--space-4);
  margin-bottom: var(--space-6);
}

.stacks-list {
  display: grid;
  gap: var(--space-3);
  margin: 0;
  padding: 0;
  list-style: none;
}

.stacks-list li {
  display: flex;
  align-items: center;
  gap: var(--space-4);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-xl);
  padding: var(--space-4) var(--space-5);
  background: var(--color-card);
  box-shadow: var(--shadow-sm);
  transition: all var(--duration-lift) var(--ease-out);
}

.stacks-list li:hover {
  transform: translateY(-2px);
  border-color: var(--color-accent-border);
  box-shadow: var(--shadow-lg);
}

.stacks-list li a {
  color: var(--color-fg);
  font-size: var(--text-lg);
  font-weight: 600;
  text-decoration: none;
}

.stacks-list li small {
  color: var(--color-muted-fg);
  font-family: var(--font-mono);
  font-size: var(--text-xs);
}

/* The stacks empty state reuses `.showcase .showcase--compact` from Task 5.
   No `.empty-state` rules are needed — do not add any. */

/* ---- Tabs ---- */
.stack-detail-header {
  display: flex;
  align-items: baseline;
  gap: var(--space-3);
  margin-bottom: var(--space-4);
}

.button-row,
.phase-row,
.run-tabs,
.stack-detail-tabs {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
  margin-bottom: var(--space-6);
}

.run-tabs button,
.phase-row button,
.stack-detail-tabs a {
  position: relative;
  min-height: var(--touch-target);
  border: 0;
  border-radius: var(--radius-md);
  padding: var(--space-2) var(--space-4);
  color: var(--color-muted-fg);
  background: transparent;
  font-family: var(--font-body);
  font-size: var(--text-sm);
  font-weight: 500;
  text-decoration: none;
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-out);
}

.run-tabs button:hover,
.phase-row button:hover,
.stack-detail-tabs a:hover {
  color: var(--color-fg);
  background: var(--color-muted);
  text-decoration: none;
}

.run-tabs button.active,
.phase-row button.active,
.stack-detail-tabs a.active {
  color: var(--color-accent);
}

.run-tabs button.active::after,
.phase-row button.active::after,
.stack-detail-tabs a.active::after {
  content: "";
  position: absolute;
  left: var(--space-4);
  right: var(--space-4);
  bottom: 2px;
  height: 2px;
  border-radius: var(--radius-full);
  background: var(--gradient-accent);
}

.stack-detail-tabs a {
  display: inline-flex;
  align-items: center;
}

/* ---- Grants ---- */
.grants-list {
  display: grid;
  gap: var(--space-2);
  margin: 0;
  padding: 0;
  list-style: none;
}

.grant-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-lg);
  padding: var(--space-3) var(--space-4);
  background: var(--color-card);
  transition: border-color var(--duration-fast) var(--ease-out);
}

.grant-row:hover {
  border-color: var(--color-accent-border);
}

.grant-row .grant-user {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  min-width: 0;
}

.grant-row .grant-user span {
  font-size: var(--text-base);
  font-weight: 500;
}

.grant-row .grant-user small {
  color: var(--color-muted-fg);
  font-size: var(--text-xs);
}

.grant-actions {
  display: flex;
  gap: var(--space-2);
}

.grant-actions button {
  min-height: var(--touch-target);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
  padding: var(--space-1) var(--space-3);
  color: var(--color-fg);
  background: transparent;
  font-size: var(--text-sm);
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-out);
}

.grant-actions button:hover {
  border-color: var(--color-accent-border);
  background: var(--color-muted);
}

.grant-actions button.danger {
  color: var(--color-danger);
}

.grant-actions button.danger:hover {
  border-color: var(--color-danger);
  background: var(--color-danger-soft);
}

/* ---- Credentials ---- */
.credentials-panel {
  grid-column: 1 / -1;
}

.credentials-list {
  display: grid;
  gap: var(--space-2);
  margin: var(--space-4) 0;
}

.credential-row {
  display: grid;
  grid-template-columns: minmax(160px, 1fr) auto auto;
  align-items: center;
  gap: var(--space-3);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-lg);
  padding: var(--space-3) var(--space-4);
  font-family: var(--font-mono);
  font-size: var(--text-sm);
}

.credential-form {
  display: grid;
  grid-template-columns: minmax(160px, 1fr) minmax(180px, 1fr) auto;
  gap: var(--space-4);
  align-items: end;
}

/* ---- User search ---- */
.search-wrapper {
  position: relative;
}

.search-dropdown {
  position: absolute;
  top: 100%;
  left: 0;
  right: 0;
  /* Must stay ABOVE .app-frame-header (5) — the sticky header is opaque and
     would otherwise clip this dropdown once the page scrolls. */
  z-index: 10;
  max-height: 240px;
  overflow-y: auto;
  border: 1px solid var(--color-border);
  border-radius: var(--radius-lg);
  margin-top: var(--space-1);
  background: var(--color-card);
  box-shadow: var(--shadow-lg);
}

.search-result-item {
  padding: var(--space-3) var(--space-4);
  font-size: var(--text-sm);
  cursor: pointer;
  transition: background var(--duration-fast) var(--ease-out);
}

.search-result-item:hover,
.search-result-item.focused {
  background: var(--color-accent-soft);
}

.search-result-item.assigned {
  color: var(--color-muted-fg);
  background: var(--color-muted);
  cursor: default;
}

.search-result-item small {
  display: block;
  color: var(--color-muted-fg);
  font-family: var(--font-mono);
  font-size: var(--text-xs);
}

.selected-user-card {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
  border: 1px solid var(--color-accent-border);
  border-radius: var(--radius-lg);
  margin-top: var(--space-3);
  padding: var(--space-3) var(--space-4);
  background: var(--color-accent-soft);
}

.selected-user-card span {
  font-size: var(--text-base);
  font-weight: 500;
}

.selected-user-card button {
  border: 0;
  padding: var(--space-1);
  color: var(--color-muted-fg);
  background: none;
  cursor: pointer;
}

/* ---- Undo banner ---- */
.undo-banner {
  position: fixed;
  bottom: var(--space-6);
  left: 50%;
  transform: translateX(-50%);
  /* Must stay ABOVE .app-frame-header (5) and .search-dropdown (10). */
  z-index: 20;
  display: flex;
  align-items: center;
  gap: var(--space-4);
  border-radius: var(--radius-xl);
  padding: var(--space-3) var(--space-5);
  color: var(--color-card);
  background: var(--color-fg);
  box-shadow: var(--shadow-xl);
  font-size: var(--text-sm);
}

.undo-banner button {
  min-height: var(--touch-target);
  border: 0;
  border-radius: var(--radius-md);
  padding: var(--space-2) var(--space-4);
  color: var(--color-accent-fg);
  background: var(--gradient-accent);
  font-size: var(--text-sm);
  font-weight: 500;
  cursor: pointer;
}

/* ---- Layout grids ---- */
.workflow-grid {
  display: grid;
  grid-template-columns: minmax(280px, 0.85fr) minmax(320px, 1.15fr);
  gap: var(--space-6);
}

.form-grid,
.variable-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(160px, 1fr));
  gap: var(--space-5);
}

.form-row {
  display: grid;
  gap: var(--space-4);
}

.form-actions {
  margin: var(--space-6) 0 0;
}

.selector-label {
  margin-bottom: var(--space-4);
}
```

- [ ] **Step 2: Drain the superseded legacy rules**

Delete from `web/src/styles.css`: `.workflow-grid`, `.form-grid, .variable-grid`, `.selector-label`, `.button-row, .phase-row, .run-tabs, .stack-detail-tabs`, `.form-actions`, `.run-tabs button, .phase-row button, .stack-detail-tabs a`, `.run-tabs button.active, …`, `.stack-detail-header`, `.stack-detail-tabs a`, `.credentials-panel`, `.credentials-list`, `.credential-row`, `.credential-form`, the `@media (max-width: 700px)` block, `.stacks-list-header`, `.stacks-list-header h1`, `.stacks-list`, `.stacks-list li`, `.stacks-list li a`, `.grant-row`, `.grant-row .grant-user`, `.grant-row .grant-user span`, `.grant-row .grant-user small`, `.grant-actions`, `.grant-actions button`, `.grant-actions button.danger`, `.grants-list`, `.search-dropdown`, `.search-result-item`, `.search-result-item:hover, .search-result-item.focused`, `.search-result-item.assigned`, `.search-result-item small`, `.search-wrapper`, `.selected-user-card`, `.selected-user-card span`, `.selected-user-card button`, `.undo-banner`, `.undo-banner button`, `@keyframes slideUp`, `.form-row`.

- [ ] **Step 3: Add the section label and stats band to the stacks list**

In `web/src/features/stacks/StacksListScreen.tsx`, wrap the existing `<h1>` and add the band. Keep the existing heading text and the create-stack action exactly as they are.

```tsx
import SectionLabel from "../../shared/SectionLabel";
import StatBand from "../../shared/StatBand";
import { statusTone } from "../../shared/statusTone";
```

```tsx
      <div className="stacks-list-header">
        <div className="page-header">
          <SectionLabel pulse>Stacks</SectionLabel>
          {/* existing <h1> element, unchanged */}
        </div>
        {/* existing action element(s), unchanged */}
      </div>
```

Immediately after that header block, derive the band from data already on the client — no new query:

```tsx
      <StatBand
        items={[
          { label: "Stacks", value: stacks.length },
          {
            label: "Active",
            value: stacks.filter((s) => statusTone(s.status ?? "") === "progress").length
          }
        ]}
      />
```

Replace `stacks` with whatever the component already calls its loaded array. If the stack objects carry no status field, render only the `Stacks` count — do **not** add a query to obtain one.

- [ ] **Step 4: Add the empty-state hero**

Still in `StacksListScreen.tsx`, when the loaded list is empty, render this instead of the empty list:

```tsx
      <section className="showcase showcase--compact" data-testid="stacks-empty">
        <div className="showcase__body">
          <SectionLabel pulse>Get started</SectionLabel>
          <h2 className="showcase__title">
            No stacks <span className="gradient-text">yet</span>
          </h2>
          <p className="showcase__lede">
            A stack pairs a Terraform template with an environment. Create your
            first one to start planning and applying infrastructure.
          </p>
          {/* the existing create-stack link/button, unchanged */}
        </div>
        <div className="showcase__visual">
          <HeroGraphic />
        </div>
      </section>
```

Add `import HeroGraphic from "../../shared/HeroGraphic";` alongside the other imports. If the screen already renders an empty-state message, keep its existing copy in the `<p>` rather than the wording above, and keep any existing `data-testid`.

- [ ] **Step 5: Add a section label to the stack detail header**

In `web/src/features/stacks/StackDetailShell.tsx`, wrap the contents of `.stack-detail-header` so a label sits above the existing heading:

```tsx
      <div className="stack-detail-header">
        <div className="page-header">
          <SectionLabel>Stack</SectionLabel>
          {/* existing heading element and its siblings, unchanged */}
        </div>
      </div>
```

Add `import SectionLabel from "../../shared/SectionLabel";`.

- [ ] **Step 6: Run the full suite and build**

```bash
cd web && npm test 2>&1 | grep -E 'Test Files|Tests ' && npm run build 2>&1 | tail -3
```

Expected: `Test Files 38 passed (38)`, `Tests 248 passed (248)`. Build clean.

If `StacksListScreen.test.tsx` fails, the most likely cause is the empty-state branch changing what renders when the list is empty. Check what the test expects to find and preserve that element and its `data-testid` inside the new markup.

- [ ] **Step 7: Commit**

```bash
git add web/src/styles/features.css web/src/styles.css \
        web/src/features/stacks/StacksListScreen.tsx \
        web/src/features/stacks/StackDetailShell.tsx
git commit -m "feat(web): restyle stacks screens with empty-state hero and stats band"
```

---

### Task 7: Templates, runs, and the log panel

**Files:**
- Modify: `web/src/features/templates/TemplateRegistryScreen.tsx`
- Modify: `web/src/features/runs/RunsListScreen.tsx`
- Modify: `web/src/styles/features.css` (append)
- Modify: `web/src/styles.css` (drain remaining feature rules)

**Interfaces:**
- Consumes: `SectionLabel` (Task 2), tokens (Task 1).
- Produces: `.log-panel`, `.stack-template-*`, `.revision-grid` treatments.

- [ ] **Step 1: Append the templates and runs styles**

Append to `web/src/styles/features.css`:

```css
/* ---- Template lists ---- */
.stack-template-list {
  display: grid;
  gap: var(--space-3);
  margin-top: var(--space-5);
  border-top: 1px solid var(--color-border);
  padding-top: var(--space-4);
}

.stack-template-list-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
}

.stack-template-list h3 {
  margin: 0;
  font-size: var(--text-sm);
  font-weight: 600;
}

.stack-template-list-header span {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-width: 26px;
  border-radius: var(--radius-full);
  padding: var(--space-1) var(--space-2);
  color: var(--color-accent);
  background: var(--color-accent-soft);
  font-family: var(--font-mono);
  font-size: var(--text-xs);
}

.stack-template-items {
  display: grid;
  gap: var(--space-2);
}

.stack-template-items button {
  display: grid;
  gap: var(--space-1);
  width: 100%;
  min-height: 56px;
  border: 1px solid var(--color-border);
  border-radius: var(--radius-lg);
  padding: var(--space-3) var(--space-4);
  color: var(--color-fg);
  background: var(--color-card);
  text-align: left;
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-out);
}

.stack-template-items button:hover {
  transform: translateY(-1px);
  border-color: var(--color-accent-border);
  box-shadow: var(--shadow-md);
}

.stack-template-items button.active {
  border-color: var(--color-accent);
  background: var(--color-accent-soft);
}

.stack-template-items span,
.stack-template-items small {
  min-width: 0;
  overflow-wrap: anywhere;
}

.stack-template-items span {
  font-size: var(--text-base);
  font-weight: 500;
}

.stack-template-items small {
  color: var(--color-muted-fg);
  font-family: var(--font-mono);
  font-size: var(--text-xs);
}

.lifecycle-badge {
  display: inline-flex;
  align-items: center;
  border-radius: var(--radius-full);
  padding: var(--space-1) var(--space-3);
  color: var(--color-warning);
  background: var(--color-warning-soft);
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  text-transform: uppercase;
}

/* ---- Metadata grids ---- */
.revision-grid {
  display: grid;
  grid-template-columns: max-content minmax(0, 1fr);
  gap: var(--space-2) var(--space-4);
  margin: 0;
}

.revision-grid dt {
  color: var(--color-muted-fg);
  font-size: var(--text-sm);
}

.revision-grid dd {
  min-width: 0;
  margin: 0;
  color: var(--color-fg);
  overflow-wrap: anywhere;
  font-family: var(--font-mono);
  font-size: var(--text-sm);
}

/* ---- Log panel. Deliberately carries no texture: patterning behind a
   monospace log stream measurably hurts scanning for errors. ---- */
.log-panel pre {
  min-height: 260px;
  max-height: 460px;
  overflow: auto;
  border-radius: var(--radius-xl);
  margin: 0;
  padding: var(--space-5);
  color: var(--color-card);
  background: var(--color-fg);
  font-family: var(--font-mono);
  font-size: var(--text-sm);
  line-height: var(--leading-normal);
  white-space: pre-wrap;
}

.spin {
  animation: spin 0.9s linear infinite;
}
```

- [ ] **Step 2: Drain the remaining legacy rules**

Delete from `web/src/styles.css`: `.stack-template-list`, `.stack-template-list-header`, `.stack-template-list h3`, `.stack-template-list-header span`, `.stack-template-items`, `.stack-template-items button`, `.stack-template-items button.active`, `.stack-template-items span, .stack-template-items small`, `.stack-template-items span`, `.stack-template-items small`, `.revision-grid`, `.revision-grid dt`, `.revision-grid dd`, `.log-panel pre`, `.spin`, `@keyframes spin`.

The `spin` keyframes now live in `base.css`, so deleting the copy here is required to avoid a duplicate definition.

- [ ] **Step 3: Add section labels to the templates and runs screens**

In `web/src/features/templates/TemplateRegistryScreen.tsx`, wrap the existing `<h1>`:

```tsx
        <div className="page-header">
          <SectionLabel>Templates</SectionLabel>
          {/* existing <h1> element, unchanged */}
        </div>
```

Add `import SectionLabel from "../../shared/SectionLabel";`.

In `web/src/features/runs/RunsListScreen.tsx`, apply the identical structure with a different label:

```tsx
        <div className="page-header">
          <SectionLabel pulse>Runs</SectionLabel>
          {/* existing <h1> element, unchanged */}
        </div>
```

Add `import SectionLabel from "../../shared/SectionLabel";`.

Preserve every existing heading string, sibling element, and `data-testid` exactly as found.

- [ ] **Step 4: Run the full suite and build**

```bash
cd web && npm test 2>&1 | grep -E 'Test Files|Tests ' && npm run build 2>&1 | tail -3
```

Expected: `Test Files 38 passed (38)`, `Tests 248 passed (248)`. Build clean.

- [ ] **Step 5: Commit**

```bash
git add web/src/styles/features.css web/src/styles.css \
        web/src/features/templates/TemplateRegistryScreen.tsx \
        web/src/features/runs/RunsListScreen.tsx
git commit -m "feat(web): restyle templates, runs, and log panel"
```

---

### Task 8: Responsive pass, reduced-motion audit, and final drain

**Files:**
- Modify: `web/src/styles/features.css` (append responsive block)
- Modify: `web/src/styles.css` (reduce to imports only)
- Modify: `web/src/styles/styles.guard.test.ts` (extend)

- [ ] **Step 1: Find any remaining inline styles**

```bash
cd web && grep -rn 'style={{' src
```

Convert each to a class in `features.css`, unless the value is genuinely dynamic (a computed width percentage, for instance), in which case leave it — inline styles for computed values are correct and are not a design-system violation.

- [ ] **Step 2: Append the responsive block**

Append to `web/src/styles/features.css`:

```css
@media (max-width: 920px) {
  .workflow-grid,
  .form-grid,
  .variable-grid {
    grid-template-columns: 1fr;
  }
}

@media (max-width: 760px) {
  .app-shell {
    padding: var(--space-4);
  }

  .app-frame-header {
    padding: var(--space-3) var(--space-4);
  }

  .app-frame-brand {
    gap: var(--space-4);
  }

  .app-frame-content {
    padding: var(--space-6) var(--space-4);
  }

  .app-frame-content .app-shell {
    padding: 0;
  }

  .workspace-header,
  .stacks-list-header {
    align-items: stretch;
    flex-direction: column;
  }

  .runtime-fields {
    grid-template-columns: 1fr;
  }

  .credential-form,
  .credential-row {
    grid-template-columns: 1fr;
  }

  .page-header h1 {
    font-size: var(--text-2xl);
  }

  /* Primary CTAs go full width on mobile, per the design system. */
  .form-actions .primary-button,
  .stacks-list-header .primary-button {
    width: 100%;
  }

  /* 44px minimum touch targets where density and accessibility conflict. */
  .app-nav a,
  .icon-button,
  .selected-user-card button {
    min-height: var(--touch-target);
    display: inline-flex;
    align-items: center;
  }

  .undo-banner {
    left: var(--space-4);
    right: var(--space-4);
    transform: none;
  }
}
```

- [ ] **Step 3: Reduce styles.css to a pure index**

Every rule should now be drained. Replace the entire contents of `web/src/styles.css` with:

```css
@import "./styles/tokens.css";
@import "./styles/base.css";
@import "./styles/primitives.css";
@import "./styles/features.css";
```

- [ ] **Step 4: Verify nothing was lost in the drain**

```bash
cd web
grep -hoE '^\.[a-z0-9-]+' src/styles/*.css | sort -u > /tmp/new-classes.txt
git show main:web/src/styles.css | grep -oE '^\.[a-z0-9-]+' | sort -u > /tmp/old-classes.txt
comm -23 /tmp/old-classes.txt /tmp/new-classes.txt
```

Expected: empty output. Any class listed was dropped during the drain and must be restored to `features.css` in the new style.

- [ ] **Step 5: Extend the guard test to cover the index**

Add this block to `web/src/styles/styles.guard.test.ts`:

```ts
describe("styles.css index", () => {
  it("contains nothing but imports", () => {
    const index = readFileSync(join(STYLES_DIR, "..", "styles.css"), "utf8")
      .replace(/\/\*[\s\S]*?\*\//g, "")
      .trim();
    const lines = index.split("\n").map((l) => l.trim()).filter(Boolean);
    expect(lines.every((line) => line.startsWith("@import"))).toBe(true);
    expect(lines).toHaveLength(4);
  });
});
```

- [ ] **Step 6: Audit reduced motion**

```bash
cd web && grep -n 'animation:' src/styles/*.css
```

Every hit must be a class that the `prefers-reduced-motion` block in `base.css` neutralises. That block uses a universal selector, so it covers all of them; this step confirms no stylesheet re-enables motion inside its own media query after the reduced-motion block.

Then confirm the reveal fallback exists:

```bash
cd web && grep -A4 'prefers-reduced-motion' src/styles/base.css | grep -A3 '\.reveal'
```

Expected: shows `.reveal { opacity: 1; transform: none; }`.

- [ ] **Step 7: Run the full suite and build**

```bash
cd web && npm test 2>&1 | grep -E 'Test Files|Tests ' && npm run build 2>&1 | tail -3
```

Expected: `Test Files 38 passed (38)`, `Tests 249 passed (249)`. Build clean.

- [ ] **Step 8: Verify the design constraints hold**

```bash
cd web
echo "--- raw hex outside tokens.css (expect 0) ---"
grep -hoE '#[0-9a-fA-F]{3,8}' src/styles/base.css src/styles/primitives.css src/styles/features.css | wc -l
echo "--- border-radius without a token (expect 0) ---"
grep -hoE 'border-radius:[^;]+' src/styles/*.css | grep -v 'var(--radius-' | wc -l
echo "--- box-shadow without a token (expect 0) ---"
grep -hoE 'box-shadow:[^;]+' src/styles/*.css | grep -v 'var(--shadow-' | wc -l
```

Expected: `0`, `0`, `0`. These use BSD-compatible flags only — macOS `grep` has no `-P`.

- [ ] **Step 9: Verify visually**

Build, then render the compiled CSS against representative markup covering the shell, a page header with `SectionLabel`, a stats band, a stacks list row, all five status tones, all four role badges, the button set, and the hero graphic. Serve `dist/` with `npx vite preview` and screenshot with headless Chrome at `--virtual-time-budget=8000` and `--window-size=1240,1000`.

Confirm: Calistoga renders on headings; the gradient shows on primary buttons and gradient-text words; cards have rounded corners and visible shadow; status pills use the semantic colours; the hero graphic's ring, floating cards and corner block are present.

Put the scratch file in `dist/`, which is gitignored, and delete it afterwards.

- [ ] **Step 10: Commit**

```bash
git add web/src/styles web/src/styles.css
git commit -m "feat(web): responsive pass, reduced-motion audit, and final style drain"
```

---

## Notes for the implementer

**On draining `styles.css`.** Each task deletes the rules it has replaced. Work by selector, not line number — line numbers shift with every deletion. After each task, `grep -c '^\.' web/src/styles.css` should be strictly decreasing from its starting value of 91.

**On test failures in Tasks 3-7.** No test asserts on class names, so a failure means markup changed in a way that lost a `data-testid`, an ARIA role, or a copy string. Fix the component. Never edit a test to accommodate a restyle — that turns a caught regression into a silent one.

**On the semantic palette.** `--color-success` and `--color-warning` are the darker, AA-safe values and must be used for anything text-bearing. The `-dot` variants exist solely for dots and solid fills. If you find yourself wanting `#16A34A` on a text colour, you want `--color-success` instead.

**On the destructive confirm.** `.destructive-button` is styled but no component sets `data-confirming`. Wiring a real two-step confirm flow is a behaviour change, not a restyle, and is deliberately out of scope. The style exists so the affordance is ready when someone implements the flow.

**On `.btn-arrow`.** Task 1 styles it so a trailing arrow advances on hover, but no task adds arrows to existing buttons — changing button copy is not a restyle. Wrap the glyph in `<span className="btn-arrow">→</span>` on any button that already renders one, and use it for new CTAs you author. An unused class here is intentional, not an oversight.

**On `HeroGraphic` and reduced motion.** The graphic is `aria-hidden` and purely decorative, so under `prefers-reduced-motion` it simply stops moving rather than disappearing. That is intended — a static abstract composition still reads as designed.
