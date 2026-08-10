# Minimalist Monochrome Design System Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the tflive web console's visual language with the Minimalist Monochrome design system — pure black-and-white palette, serif typography, zero border radius, line-based structure — across every screen, without losing operator-grade information density.

**Architecture:** A CSS custom-property token layer lands first, then `web/src/styles.css` is progressively drained into focused files under `web/src/styles/`. Existing class names are preserved throughout, so most screens inherit the new appearance with no TSX edits at all. Only genuinely new structure (page headers, status tones, editorial error screens) touches components.

**Tech Stack:** React 18, Vite 8, TypeScript 5.6, react-router-dom v6, TanStack Query v5, lucide-react, Vitest + Testing Library. No new runtime dependencies.

**Spec:** `docs/superpowers/specs/2026-08-10-monochrome-design-system-design.md`

## Global Constraints

- **Palette is absolute.** The only colors are `#FFFFFF`, `#000000`, `#F5F5F5`, `#525252`, `#E5E5E5`, plus `--color-critical: #C1121F`. No other color may appear anywhere.
- **`--color-critical` is used at exactly two sites:** the failure status tone and the destructive-action confirm step. It is always accompanied by a glyph *and* a text label — never the sole carrier of meaning.
- **Zero border radius everywhere.** Always written as `border-radius: var(--radius)`, never `0`, so the rule stays greppable.
- **No box-shadows.** Depth comes from inversion, border weight, and negative space.
- **Raw hex literals are permitted only in `web/src/styles/tokens.css`.** Every other stylesheet references custom properties. This is enforced by an automated guard test.
- **`data-testid` attributes and ARIA roles are immovable.** No test asserts on class names, so these attributes are the entire test contract. Any markup restructure carries every one across verbatim.
- **Transitions are 100ms maximum** (`var(--transition-fast)`), and all motion is disabled under `prefers-reduced-motion: reduce`.
- **Minimum 44×44px touch targets** on viewports at or below 760px.
- Fonts are self-hosted from `web/public/fonts/`. No runtime request to any third-party font host.

## Baseline (verified 2026-08-10)

`cd web && npm test` → **33 test files passed, 218 tests passed.**

The run also prints **8 errors** originating from `src/auth/useMeQuery.test.tsx` — unhandled promise rejections inside `oidc-client-ts` during the "returns error on 401 response" test. These are **pre-existing and unrelated to this work**. Do not attempt to fix them; only treat the "Test Files / Tests" summary lines as the pass criterion.

## File Structure

| File | Responsibility |
|---|---|
| `web/public/fonts/*.woff2` | 7 self-hosted latin-subset faces |
| `scripts/vendor-fonts.sh` | One-shot vendoring script (already written and verified) |
| `web/src/styles/tokens.css` | All custom properties. The only file allowed raw hex. |
| `web/src/styles/base.css` | Reset, `@font-face`, element defaults, textures, focus, reduced-motion, skip link |
| `web/src/styles/primitives.css` | Buttons, inputs, panels, rules, page headers, chips, badges |
| `web/src/styles/features.css` | Screen-specific rules: stacks, templates, runs, credentials, grants |
| `web/src/styles/styles.guard.test.ts` | Enforces the palette/radius/shadow constraints automatically |
| `web/src/styles.css` | Shrinks from 844 lines to a 4-line `@import` index |

`web/src/main.tsx` is never modified — keeping `styles.css` as the index preserves its import.

---

### Task 1: Token layer, fonts, and base stylesheet

Establishes the foundation. After this task the entire console renders monochrome and serif with layout intact. No component files are touched.

**Files:**
- Create: `web/public/fonts/` (7 `.woff2` files, via script)
- Create: `web/src/styles/tokens.css`
- Create: `web/src/styles/base.css`
- Create: `web/src/styles/styles.guard.test.ts`
- Modify: `web/index.html` (font preload)
- Modify: `web/src/styles.css:1-30` (delete legacy `:root`, `*`, `body`; prepend imports)
- Modify: `web/src/styles.css:142-147` (delete legacy `h1` rule)
- Commit: `scripts/vendor-fonts.sh` (already present, untracked)

**Interfaces:**
- Produces: custom properties consumed by every later task — `--color-fg`, `--color-bg`, `--color-muted`, `--color-muted-fg`, `--color-border`, `--color-border-light`, `--color-inverse-fg`, `--color-critical`, `--color-critical-fg`, `--font-display`, `--font-body`, `--font-mono`, `--text-xs` … `--text-8xl`, `--tracking-tighter` … `--tracking-widest`, `--leading-none` … `--leading-relaxed`, `--space-1` … `--space-24`, `--rule-hairline` … `--rule-ultra`, `--radius`, `--transition-fast`, `--control-height`, `--touch-target`.
- Produces: `.skip-link` class, consumed by Task 5.

- [ ] **Step 1: Vendor the font files**

The script is already written and verified (it produced 164KB across 7 files). Run it:

```bash
chmod +x scripts/vendor-fonts.sh
./scripts/vendor-fonts.sh
```

Expected output: seven lines each reporting a filename and a byte count in the 20,000–24,000 range, then `Done.`

- [ ] **Step 2: Verify the fonts landed**

```bash
ls -1 web/public/fonts/ && du -sh web/public/fonts
```

Expected: 6 files (`inter-400.woff2`, `inter-500.woff2`, `inter-600.woff2`, `inter-700.woff2`, `jetbrains-mono-400.woff2`, `jetbrains-mono-500.woff2`) totalling roughly 140K.

- [ ] **Step 3: Write the failing guard test**

Create `web/src/styles/styles.guard.test.ts`:

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

const REQUIRED_TOKENS = [
  "--color-bg", "--color-fg", "--color-muted", "--color-muted-fg",
  "--color-border", "--color-border-light", "--color-inverse-fg",
  "--color-critical", "--color-critical-fg",
  "--font-display", "--font-body", "--font-mono",
  "--radius", "--transition-fast"
];

describe("tokens.css", () => {
  it("defines every required custom property", () => {
    const tokens = readFileSync(join(STYLES_DIR, "tokens.css"), "utf8");
    for (const token of REQUIRED_TOKENS) {
      expect(tokens, `missing ${token}`).toContain(`${token}:`);
    }
  });

  it("pins border radius to zero", () => {
    const tokens = readFileSync(join(STYLES_DIR, "tokens.css"), "utf8");
    expect(tokens).toMatch(/--radius:\s*0\s*;/);
  });
});

describe.each(convertedStylesheets())("%s", (name) => {
  it("contains no raw hex colors", () => {
    const matches = read(name).match(/#[0-9a-fA-F]{3,8}\b/g) ?? [];
    expect(matches, `use var(--color-*) instead of ${matches.join(", ")}`).toEqual([]);
  });

  it("declares no non-zero border radius", () => {
    // The \s* must live INSIDE the lookahead. Left outside it can backtrack to
    // zero-width, so the lookahead inspects " var(--radius)" rather than
    // "var(--radius)", succeeds, and every compliant declaration is reported.
    const matches = read(name).match(/border-radius:(?!\s*var\(--radius\)\s*;)[^;]+;/g) ?? [];
    expect(matches, `use var(--radius): ${matches.join(", ")}`).toEqual([]);
  });

  it("declares no box-shadow", () => {
    const matches = read(name).match(/box-shadow:[^;]+;/g) ?? [];
    expect(matches, `monochrome has no shadows: ${matches.join(", ")}`).toEqual([]);
  });
});
```

- [ ] **Step 4: Run the guard test to verify it fails**

```bash
cd web && npx vitest run src/styles/styles.guard.test.ts
```

Expected: FAIL — both `tokens.css` assertions error with `ENOENT ... tokens.css`, since the token file does not exist yet. The `describe.each` block contributes no tests at this point because the directory contains only the test file itself.

- [ ] **Step 5: Create the token layer**

Create `web/src/styles/tokens.css`:

```css
/* The only file in the codebase permitted to contain raw hex literals.
   Everything else references these custom properties. */
:root {
  /* Palette — absolute. No other colors exist in this design. */
  --color-bg:           #FFFFFF;
  --color-fg:           #000000;
  --color-muted:        #F5F5F5;
  --color-muted-fg:     #525252;
  --color-border:       #000000;
  --color-border-light: #E5E5E5;
  --color-inverse-fg:   #FFFFFF;

  /* The single sanctioned exception. Used ONLY by the failure status tone and
     the destructive confirm step, and always alongside a glyph and a label so
     that meaning never depends on hue alone. */
  --color-critical:     #C1121F;
  --color-critical-fg:  #FFFFFF;

  /* Typefaces */
  --font-display: Inter, "Helvetica Neue", Helvetica, Arial, sans-serif;
  --font-body:    Inter, "Helvetica Neue", Helvetica, Arial, sans-serif;
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
  --text-5xl:  3.5rem;
  --text-6xl:  4.5rem;
  --text-7xl:  6rem;
  --text-8xl:  8rem;

  /* Tracking */
  --tracking-tighter: -0.05em;
  --tracking-tight:   -0.025em;
  --tracking-normal:  0;
  --tracking-wide:    0.05em;
  --tracking-widest:  0.1em;

  /* Leading */
  --leading-none:    1;
  --leading-tight:   1.15;
  --leading-normal:  1.4;
  --leading-relaxed: 1.625;

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

  /* Rules — the visual system replaces fills and shadows with lines */
  --rule-hairline: 1px;
  --rule-thin:     1px;
  --rule-medium:   2px;
  --rule-thick:    4px;
  --rule-ultra:    8px;

  /* Geometry and motion */
  --radius: 0;
  --transition-fast: 100ms;

  /* Controls */
  --control-height: 38px;
  --touch-target:   44px;
}
```

- [ ] **Step 6: Create the base stylesheet**

Create `web/src/styles/base.css`:

```css
/* ---- Self-hosted faces. Latin subset, vendored by scripts/vendor-fonts.sh ---- */
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
  font-synthesis: none;
  text-rendering: optimizeLegibility;
  -webkit-font-smoothing: antialiased;
}

body {
  position: relative;
  margin: 0;
  min-width: 320px;
  min-height: 100vh;
}

/* ---- Texture. Required by the design system to avoid flat design.
   Two layers on two pseudo-elements: horizontal hairlines for print rhythm,
   fractal noise for paper grain. Both sit behind content (z-index 0 against
   the z-index 1 on body's children), so nothing ever renders on top of a
   log stream or an input — patterning under monospace text measurably hurts
   scanning for errors. ---- */
body::before,
body::after {
  content: "";
  position: fixed;
  inset: 0;
  z-index: 0;
  pointer-events: none;
}

body::before {
  opacity: 0.015;
  background-image: repeating-linear-gradient(
    0deg,
    transparent,
    transparent 1px,
    var(--color-fg) 1px,
    var(--color-fg) 2px
  );
  background-size: 100% 4px;
}

body::after {
  opacity: 0.02;
  background-image: url("data:image/svg+xml,%3Csvg viewBox='0 0 256 256' xmlns='http://www.w3.org/2000/svg'%3E%3Cfilter id='noise'%3E%3CfeTurbulence type='fractalNoise' baseFrequency='0.8' numOctaves='4' stitchTiles='stitch'/%3E%3C/filter%3E%3Crect width='100%25' height='100%25' filter='url(%23noise)'/%3E%3C/svg%3E");
}

body > * {
  position: relative;
  z-index: 1;
}

/* ---- Typography defaults ---- */
h1, h2, h3, h4 {
  font-family: var(--font-display);
  font-weight: 700;
  letter-spacing: var(--tracking-tight);
  line-height: var(--leading-tight);
}

h1 {
  margin: 0;
  font-size: var(--text-5xl);
  letter-spacing: var(--tracking-tighter);
  line-height: var(--leading-none);
}

code, kbd, samp, pre {
  font-family: var(--font-mono);
}

a {
  color: var(--color-fg);
  text-decoration: none;
}

a:hover {
  text-decoration: underline;
}

button, input, textarea, select {
  font: inherit;
  border-radius: var(--radius);
}

button svg {
  flex: 0 0 auto;
}

/* ---- Focus. Required on every interactive element. ---- */
:focus-visible {
  outline: 3px solid var(--color-fg);
  outline-offset: 3px;
}

/* ---- Skip link ---- */
.skip-link {
  position: absolute;
  top: var(--space-2);
  left: var(--space-2);
  z-index: 100;
  transform: translateY(-200%);
  padding: var(--space-3) var(--space-4);
  border-radius: var(--radius);
  color: var(--color-inverse-fg);
  background: var(--color-fg);
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  letter-spacing: var(--tracking-widest);
  text-transform: uppercase;
}

.skip-link:focus {
  transform: translateY(0);
}

/* ---- Motion budget is tiny by design; honour the user's preference. ---- */
@media (prefers-reduced-motion: reduce) {
  *,
  *::before,
  *::after {
    animation-duration: 0.01ms !important;
    animation-iteration-count: 1 !important;
    transition-duration: 0.01ms !important;
    scroll-behavior: auto !important;
  }
}
```

- [ ] **Step 7: Wire the imports and delete the superseded legacy rules**

In `web/src/styles.css`, replace lines 1–30 (the legacy `:root`, `*`, `body`, and the `button, input, textarea, select` block) with:

```css
@import "./styles/tokens.css";
@import "./styles/base.css";
```

Then delete the now-superseded legacy `h1` rule at lines 142–147:

```css
h1 {
  margin: 0;
  color: #1f2933;
  font-size: 2rem;
  letter-spacing: 0;
}
```

Leave every other rule in `styles.css` untouched — later tasks drain them progressively.

> `@import` must precede all other rules in a CSS file. Vite inlines these at build time, so there is no extra network request.

- [ ] **Step 8: Add the font preloads**

In `web/index.html`, inside `<head>` and before the `<title>`, add preloads for the two faces needed on first paint. Preloading all seven would contend for bandwidth and defeat the purpose.

```html
    <link rel="preload" href="/fonts/inter-400.woff2" as="font" type="font/woff2" crossorigin />
    <link rel="preload" href="/fonts/inter-700.woff2" as="font" type="font/woff2" crossorigin />
```

- [ ] **Step 9: Run the guard test to verify it passes**

```bash
cd web && npx vitest run src/styles/styles.guard.test.ts
```

Expected: PASS. `tokens.css` satisfies both its checks and `base.css` passes all three constraint checks.

- [ ] **Step 10: Run the full suite and the build**

```bash
cd web && npm test 2>&1 | tail -5 && npm run build 2>&1 | tail -5
```

Expected: `Test Files 34 passed (34)`, `Tests 223 passed (223)` — the baseline 218 plus 5 new guard assertions. The 8 pre-existing `oidc-client-ts` errors still appear and are still expected. Build completes with no TypeScript errors.

- [ ] **Step 11: Commit**

```bash
git add scripts/vendor-fonts.sh web/public/fonts web/src/styles web/src/styles.css web/index.html
git commit -m "feat(web): add monochrome token layer, self-hosted serif faces, base styles"
```

---

### Task 2: Button and input primitives

Rewrites the interactive primitives in place under their existing class names, so every screen inherits them without a single component edit.

**Files:**
- Create: `web/src/styles/primitives.css`
- Modify: `web/src/styles.css` (add import; delete drained rules)

**Interfaces:**
- Consumes: all tokens from Task 1.
- Produces: `.primary-button`, `.secondary-button`, `.destructive-button`, `.icon-button` and the global `input`/`textarea`/`select`/`label` treatments, consumed by every feature screen.

- [ ] **Step 1: Create the primitives stylesheet with button and input rules**

Create `web/src/styles/primitives.css`:

```css
/* ---- Buttons. Rectangular, uppercase, monospace, instant inversion. ---- */
.primary-button,
.secondary-button,
.destructive-button {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: var(--space-2);
  min-height: var(--control-height);
  border-radius: var(--radius);
  padding: var(--space-3) var(--space-6);
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  font-weight: 500;
  letter-spacing: var(--tracking-widest);
  text-transform: uppercase;
  white-space: nowrap;
  cursor: pointer;
  transition: background var(--transition-fast), color var(--transition-fast),
    border-color var(--transition-fast);
}

.primary-button {
  border: var(--rule-medium) solid var(--color-fg);
  color: var(--color-inverse-fg);
  background: var(--color-fg);
}

.primary-button:hover:not(:disabled) {
  color: var(--color-fg);
  background: var(--color-bg);
}

.secondary-button {
  border: var(--rule-medium) solid var(--color-fg);
  color: var(--color-fg);
  background: transparent;
}

.secondary-button:hover:not(:disabled) {
  color: var(--color-inverse-fg);
  background: var(--color-fg);
}

/* Destructive stays black at rest deliberately. A button that is red before
   the user has committed to anything makes the colour ambient rather than
   meaningful; it earns --color-critical only at the confirm step below. */
.destructive-button {
  border: var(--rule-medium) solid var(--color-fg);
  color: var(--color-inverse-fg);
  background: var(--color-fg);
}

.destructive-button:hover:not(:disabled) {
  color: var(--color-fg);
  background: var(--color-bg);
}

.destructive-button[data-confirming="true"] {
  border-color: var(--color-critical);
  color: var(--color-critical-fg);
  background: var(--color-critical);
}

a.primary-button,
a.secondary-button {
  text-decoration: none;
}

button:disabled {
  cursor: not-allowed;
  opacity: 0.35;
}

.icon-button {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-width: var(--space-6);
  min-height: var(--space-6);
  border: 0;
  border-radius: var(--radius);
  color: var(--color-fg);
  background: transparent;
  cursor: pointer;
}

.icon-button:hover {
  color: var(--color-inverse-fg);
  background: var(--color-fg);
}

.icon-button:focus-visible {
  outline-offset: 2px;
}

/* ---- Inputs. Bottom rule only; focus thickens it rather than adding a ring. ---- */
label {
  display: grid;
  gap: var(--space-2);
  color: var(--color-muted-fg);
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  font-weight: 500;
  letter-spacing: var(--tracking-widest);
  text-transform: uppercase;
}

input,
textarea,
select {
  min-height: var(--control-height);
  min-width: 0;
  border: 0;
  border-bottom: var(--rule-medium) solid var(--color-fg);
  border-radius: var(--radius);
  padding: var(--space-2) 0;
  color: var(--color-fg);
  background: var(--color-bg);
  font-family: var(--font-body);
  font-size: var(--text-base);
}

input::placeholder,
textarea::placeholder {
  color: var(--color-muted-fg);
  font-style: italic;
}

input:focus,
textarea:focus,
select:focus {
  border-bottom-width: var(--rule-thick);
  outline: none;
}

textarea {
  resize: vertical;
}
```

- [ ] **Step 2: Import primitives and drain the superseded legacy rules**

In `web/src/styles.css`, add the import directly after the `base.css` import:

```css
@import "./styles/primitives.css";
```

Then **delete** these now-superseded blocks from `styles.css` (identified by their selectors, since line numbers shift as you delete):

- `label { ... }`
- `input, textarea, select { ... }`
- `.primary-button, .secondary-button { ... }`
- `.primary-button { ... }`
- `.secondary-button { ... }`
- `.destructive-button { ... }`
- `button:disabled { ... }`
- `.icon-button { ... }`
- `a.primary-button { ... }`
- `.form-row select { ... }`

Keep `.panel > .secondary-button { margin-top: 14px; }` for now — Task 3 relocates it.

- [ ] **Step 3: Run the guard test**

```bash
cd web && npx vitest run src/styles/styles.guard.test.ts
```

Expected: PASS, now covering `base.css` and `primitives.css` (3 more assertions, 8 total in the `describe.each` block).

- [ ] **Step 4: Run the full suite and build**

```bash
cd web && npm test 2>&1 | tail -5 && npm run build 2>&1 | tail -5
```

Expected: `Test Files 34 passed (34)`, `Tests 226 passed (226)`. Build clean.

- [ ] **Step 5: Commit**

```bash
git add web/src/styles/primitives.css web/src/styles.css
git commit -m "feat(web): monochrome button and input primitives"
```

---

### Task 3: Panels, rules, and the page header pattern

Adds the structural primitives, including the page header that carries the editorial voice on every working screen.

**Files:**
- Modify: `web/src/styles/primitives.css` (append)
- Modify: `web/src/styles.css` (drain `.panel`, `.workspace-header`, `.eyebrow`, `.alert`, `.muted`, `.error-text`)

**Interfaces:**
- Consumes: tokens from Task 1.
- Produces: `.panel`, `.panel--inverted`, `.panel.wide`, `.page-header`, `.eyebrow`, `.page-rule`, `.alert`, `.error-text`, `.muted`. `.page-header` and `.eyebrow` are consumed by Tasks 5, 7, and 8.

- [ ] **Step 1: Append the structural primitives**

Append to `web/src/styles/primitives.css`:

```css
/* ---- Panels ---- */
.panel {
  min-width: 0;
  border: var(--rule-thin) solid var(--color-border);
  border-radius: var(--radius);
  padding: var(--space-6);
  background: var(--color-bg);
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
  display: flex;
  align-items: center;
  gap: var(--space-2);
  margin: 0 0 var(--space-5);
  font-family: var(--font-display);
  font-size: var(--text-2xl);
  letter-spacing: var(--tracking-tight);
}

.panel > .secondary-button {
  margin-top: var(--space-5);
}

/* Inversion is the emphasis mechanism that replaces accent colour. Used
   sparingly — it loses all force if it appears everywhere. */
.panel--inverted {
  position: relative;
  border-color: var(--color-fg);
  color: var(--color-inverse-fg);
  background: var(--color-fg);
}

.panel--inverted::before {
  content: "";
  position: absolute;
  inset: 0;
  pointer-events: none;
  opacity: 0.03;
  background-image: repeating-linear-gradient(
    90deg,
    transparent,
    transparent 1px,
    var(--color-bg) 1px,
    var(--color-bg) 2px
  );
  background-size: 4px 100%;
}

.panel--inverted > * {
  position: relative;
}

/* ---- Page header. The editorial voice on working screens. ---- */
.page-header {
  margin-bottom: var(--space-8);
}

.eyebrow {
  margin: 0 0 var(--space-3);
  color: var(--color-muted-fg);
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  font-weight: 500;
  letter-spacing: var(--tracking-widest);
  text-transform: uppercase;
}

.page-header h1 {
  font-family: var(--font-display);
  font-size: var(--text-5xl);
  letter-spacing: var(--tracking-tighter);
  line-height: var(--leading-none);
}

/* Thick rule plus bordered square — the design system's required decorative
   punctuation. Rendered from the rule element so no extra markup is needed. */
.page-rule {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  margin-top: var(--space-5);
  border: 0;
}

.page-rule::before {
  content: "";
  flex: 0 0 clamp(80px, 18vw, 200px);
  height: var(--rule-thick);
  background: var(--color-fg);
}

.page-rule::after {
  content: "";
  width: 12px;
  height: 12px;
  border: var(--rule-medium) solid var(--color-fg);
}

/* ---- Messaging ---- */
.alert {
  display: flex;
  align-items: flex-start;
  gap: var(--space-3);
  margin-bottom: var(--space-4);
  border: 0;
  border-left: var(--rule-thick) solid var(--color-critical);
  border-radius: var(--radius);
  padding: var(--space-3) var(--space-4);
  color: var(--color-fg);
  background: var(--color-muted);
  font-size: var(--text-sm);
}

.error-text {
  margin: var(--space-3) 0 0;
  color: var(--color-fg);
  font-size: var(--text-sm);
  font-weight: 600;
}

.error-text::before {
  content: "✕ ";
  color: var(--color-critical);
  font-family: var(--font-mono);
}

.muted {
  margin: 0;
  color: var(--color-muted-fg);
}
```

> The `.alert` left rule and the `.error-text` glyph are the failure-tone usages of `--color-critical`. Both keep a text label beside them, so meaning never rests on hue.

- [ ] **Step 2: Drain the superseded legacy rules**

Delete these blocks from `web/src/styles.css`:

- `.eyebrow { ... }`
- `.panel { ... }`
- `.panel.wide { ... }`
- `.panel h2 { ... }`
- `.panel > .secondary-button { ... }`
- `.alert, .error-text { ... }`
- `.alert { ... }`
- `.error-text { ... }`
- `.muted { ... }`

- [ ] **Step 3: Run the guard test**

```bash
cd web && npx vitest run src/styles/styles.guard.test.ts
```

Expected: PASS.

- [ ] **Step 4: Run the full suite and build**

```bash
cd web && npm test 2>&1 | tail -5 && npm run build 2>&1 | tail -5
```

Expected: `Test Files 34 passed (34)`, `Tests 226 passed (226)`. Build clean.

- [ ] **Step 5: Commit**

```bash
git add web/src/styles/primitives.css web/src/styles.css
git commit -m "feat(web): panel, page header, and messaging primitives"
```

---

### Task 4: Status tones and role badges

The one task with real logic. Replaces the three-way icon classifier in `StatusRow` with a five-tone system, and converts role badges from hue to border weight.

**Files:**
- Modify: `web/src/shared/StatusRow.tsx`
- Create: `web/src/shared/statusTone.ts`
- Create: `web/src/shared/statusTone.test.ts`
- Modify: `web/src/features/stacks/StackAccessScreen.tsx:145`
- Modify: `web/src/styles/primitives.css` (append)
- Modify: `web/src/styles.css` (drain `.role-badge*`, `.status-row*`)

**Interfaces:**
- Consumes: tokens from Task 1, `.panel` from Task 3.
- Produces: `statusTone(value: string): StatusTone` where `type StatusTone = "settled" | "progress" | "waiting" | "failed" | "canceled"`, and `statusGlyph(tone: StatusTone): string`. Produces CSS classes `.status-tone`, `.status-tone--{tone}`, `.role-badge--{owner|operator|approver|viewer}`.

- [ ] **Step 1: Write the failing tone-classifier test**

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

- [ ] **Step 2: Run the test to verify it fails**

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

- [ ] **Step 4: Run the test to verify it passes**

```bash
cd web && npx vitest run src/shared/statusTone.test.ts
```

Expected: PASS, 10 tests.

- [ ] **Step 5: Rewrite StatusRow to use the tones**

Replace the entire contents of `web/src/shared/StatusRow.tsx`. The lucide icons are dropped in favour of typographic glyphs, which is what the design system's line-based system calls for — and it removes three icon imports.

```tsx
import { statusGlyph, statusTone } from "./statusTone";

export default function StatusRow({ label, value }: { label: string; value: string }) {
  const tone = statusTone(value);

  return (
    <div className="status-row" data-status={value}>
      <span>{label}</span>
      <strong>
        <span className={`status-tone status-tone--${tone}`} aria-hidden="true">
          {statusGlyph(tone)}
        </span>
        {value}
      </strong>
    </div>
  );
}
```

> `data-status` carries the raw value so CSS can single out the one status that
> needs treatment beyond its tone — see `destroy_finished` in Step 7.

> The glyph is `aria-hidden` because the status text sits directly beside it. Screen readers announce the value itself; the glyph is redundant decoration for them but a primary channel for sighted users.

- [ ] **Step 6: Convert the role badge markup**

In `web/src/features/stacks/StackAccessScreen.tsx` line 145, change the class construction from the bare role name to a modifier:

```tsx
                <span className={`role-badge role-badge--${grant.role}`}>{grant.role}</span>
```

- [ ] **Step 7: Append the status and badge styles**

Append to `web/src/styles/primitives.css`:

```css
/* ---- Status tones ---- */
.status-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
  margin-top: var(--space-4);
  border-top: var(--rule-hairline) solid var(--color-border-light);
  padding-top: var(--space-3);
  color: var(--color-muted-fg);
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  letter-spacing: var(--tracking-wide);
  text-transform: uppercase;
}

.status-row strong {
  display: inline-flex;
  align-items: center;
  justify-content: flex-end;
  gap: var(--space-2);
  min-width: 0;
  color: var(--color-fg);
  font-weight: 500;
  overflow-wrap: anywhere;
  text-align: right;
}

.status-tone {
  font-size: var(--text-sm);
  line-height: 1;
}

.status-tone--settled {
  color: var(--color-fg);
}

.status-tone--waiting,
.status-tone--canceled {
  color: var(--color-muted-fg);
}

.status-tone--progress {
  color: var(--color-fg);
  animation: tone-pulse 1.2s steps(2, end) infinite;
}

/* Failure is the second and final --color-critical site. The glyph and the
   adjacent status text both carry the meaning; the colour only amplifies. */
.status-tone--failed {
  color: var(--color-critical);
}

@keyframes tone-pulse {
  50% {
    opacity: 0.35;
  }
}

/* Destruction is settled, not failed — but it is consequential enough to
   deserve a heavier rule than an ordinary finished phase. */
.status-row[data-status="destroy_finished"] {
  border-top-width: var(--rule-thick);
  border-top-color: var(--color-fg);
}

/* ---- Role badges. Border weight encodes authority, which the previous four
   hues never did — blue/green/amber/grey carry no ordering. ---- */
.role-badge {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-width: 84px;
  border-radius: var(--radius);
  padding: var(--space-1) var(--space-3);
  color: var(--color-fg);
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  font-weight: 500;
  letter-spacing: var(--tracking-widest);
  text-transform: uppercase;
}

.role-badge--owner {
  border: var(--rule-medium) solid var(--color-fg);
  color: var(--color-inverse-fg);
  background: var(--color-fg);
}

.role-badge--operator {
  border: var(--rule-medium) solid var(--color-fg);
}

.role-badge--approver {
  border: var(--rule-thin) solid var(--color-fg);
}

.role-badge--viewer {
  border: var(--rule-thin) solid var(--color-border-light);
  color: var(--color-muted-fg);
}
```

- [ ] **Step 8: Drain the superseded legacy rules**

Delete from `web/src/styles.css`: `.status-row { ... }`, `.status-row strong { ... }`, `.role-badge { ... }`, `.role-badge.owner { ... }`, `.role-badge.operator { ... }`, `.role-badge.approver { ... }`, `.role-badge.viewer { ... }`.

- [ ] **Step 9: Run the full suite and build**

```bash
cd web && npm test 2>&1 | tail -5 && npm run build 2>&1 | tail -5
```

Expected: `Test Files 35 passed (35)`, `Tests 236 passed (236)`. Build clean.

If any `StatusRow` or `StackAccessScreen` test fails, the cause is almost certainly a lost `data-testid` — re-check Steps 5 and 6 against the originals before changing any test.

- [ ] **Step 10: Commit**

```bash
git add web/src/shared/statusTone.ts web/src/shared/statusTone.test.ts \
        web/src/shared/StatusRow.tsx web/src/features/stacks/StackAccessScreen.tsx \
        web/src/styles/primitives.css web/src/styles.css
git commit -m "feat(web): five-tone status system and weight-based role badges"
```

---

### Task 5: Application shell, navigation, and skip link

**Files:**
- Modify: `web/src/app/AppShell.tsx`
- Modify: `web/src/styles/features.css` (create)
- Modify: `web/src/styles.css` (import; drain shell rules)

**Interfaces:**
- Consumes: `.skip-link` (Task 1), `.eyebrow` (Task 3).
- Produces: `web/src/styles/features.css`, extended by Tasks 7 and 8.

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
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: var(--space-3) var(--space-6);
  border-bottom: var(--rule-thick) solid var(--color-fg);
  padding: var(--space-4) var(--space-6);
  background: var(--color-bg);
}

.app-wordmark {
  font-family: var(--font-display);
  font-size: var(--text-xl);
  font-weight: 700;
  letter-spacing: var(--tracking-widest);
  text-transform: uppercase;
}

.app-nav {
  display: flex;
  align-items: center;
  gap: var(--space-6);
}

.app-nav a {
  border-bottom: var(--rule-medium) solid transparent;
  padding: var(--space-2) 0;
  color: var(--color-fg);
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  letter-spacing: var(--tracking-widest);
  text-transform: uppercase;
  text-decoration: none;
  transition: border-color var(--transition-fast);
}

.app-nav a:hover,
.app-nav a:focus-visible {
  border-bottom-color: var(--color-fg);
  text-decoration: none;
}

.app-frame-identity {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--space-3) var(--space-5);
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  letter-spacing: var(--tracking-wide);
}

.identity-menu {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  text-transform: uppercase;
}

.identity-menu button {
  border: var(--rule-thin) solid var(--color-fg);
  border-radius: var(--radius);
  padding: var(--space-1) var(--space-3);
  color: var(--color-fg);
  background: transparent;
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  letter-spacing: var(--tracking-widest);
  text-transform: uppercase;
  cursor: pointer;
  transition: background var(--transition-fast), color var(--transition-fast);
}

.identity-menu button:hover {
  color: var(--color-inverse-fg);
  background: var(--color-fg);
}

.app-frame-identity .runtime-field {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}

.app-frame-identity .runtime-value {
  min-height: 0;
  border: 0;
  padding: 0;
  background: transparent;
  font-size: var(--text-xs);
}

.app-frame-content {
  width: min(1280px, 100%);
  margin: 0 auto;
  padding: var(--space-8) var(--space-6);
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
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  letter-spacing: var(--tracking-wide);
}

.runtime-value {
  min-height: var(--control-height);
  display: flex;
  align-items: center;
  border: var(--rule-thin) solid var(--color-border-light);
  border-radius: var(--radius);
  padding: var(--space-2) var(--space-3);
  color: var(--color-fg);
  background: var(--color-muted);
  font-family: var(--font-mono);
}

.debug-panel {
  margin: var(--space-8) var(--space-6);
  border-top: var(--rule-thin) solid var(--color-border-light);
  padding-top: var(--space-4);
  font-family: var(--font-mono);
  font-size: var(--text-xs);
}

.debug-panel summary {
  color: var(--color-muted-fg);
  letter-spacing: var(--tracking-widest);
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

In `web/src/app/AppShell.tsx`, replace the opening of the returned JSX (the `<div className="app-frame">` through the `<nav>` element) with the version below. Every existing `data-testid` is preserved; the additions are the skip link, the wordmark, and `id="main-content"`.

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
      <main className="app-frame-content" id="main-content">
        <Outlet />
      </main>
```

- [ ] **Step 4: Add the brand wrapper style**

Append to `web/src/styles/features.css`:

```css
.app-frame-brand {
  display: flex;
  align-items: center;
  gap: var(--space-8);
}
```

- [ ] **Step 5: Run the full suite and build**

```bash
cd web && npm test 2>&1 | tail -5 && npm run build 2>&1 | tail -5
```

Expected: `Test Files 35 passed (35)`, `Tests 239 passed (239)` — creating `features.css` adds three guard assertions. Build clean. `AppShell.test.tsx` must still pass; if it does not, a `data-testid` was dropped in Step 3.

- [ ] **Step 6: Commit**

```bash
git add web/src/styles/features.css web/src/styles.css web/src/app/AppShell.tsx
git commit -m "feat(web): monochrome application shell with wordmark and skip link"
```

---

### Task 6: Editorial screens

The four full-viewport single-message screens carry the design system's required oversized typography. This is where `--text-8xl` genuinely belongs.

**Files:**
- Modify: `web/src/app/NotFound.tsx`
- Modify: `web/src/app/AccessDenied.tsx`
- Modify: `web/src/app/ServiceUnavailable.tsx`
- Modify: `web/src/app/RoutePlaceholder.tsx`
- Modify: `web/src/styles/features.css` (append)

**Interfaces:**
- Consumes: tokens (Task 1), `.secondary-button` (Task 2).
- Produces: `.editorial`, `.editorial__display`, `.editorial__lede`.

- [ ] **Step 1: Read the four current screens**

```bash
cd web && cat src/app/NotFound.tsx src/app/AccessDenied.tsx src/app/ServiceUnavailable.tsx src/app/RoutePlaceholder.tsx
```

Note every `data-testid` and every piece of copy. Both must survive verbatim — the tests assert on the copy strings and the test ids, and changing user-facing wording is out of scope for a restyle.

- [ ] **Step 2: Append the editorial styles**

Append to `web/src/styles/features.css`:

```css
/* ---- Editorial screens. Full-viewport, single message, oversized display. ---- */
.editorial {
  display: flex;
  flex-direction: column;
  justify-content: center;
  min-height: 60vh;
  padding: var(--space-12) 0;
}

.editorial__display {
  margin: 0;
  font-family: var(--font-display);
  font-size: var(--text-5xl);
  font-weight: 700;
  letter-spacing: var(--tracking-tighter);
  line-height: var(--leading-none);
  text-transform: uppercase;
}

@media (min-width: 768px) {
  .editorial__display {
    font-size: var(--text-7xl);
  }
}

@media (min-width: 1100px) {
  .editorial__display {
    font-size: var(--text-8xl);
  }
}

.editorial hr {
  width: 100%;
  margin: var(--space-8) 0;
  border: 0;
  border-top: var(--rule-ultra) solid var(--color-fg);
}

.editorial__lede {
  max-width: 46ch;
  margin: 0 0 var(--space-8);
  color: var(--color-muted-fg);
  font-family: var(--font-body);
  font-size: var(--text-xl);
  line-height: var(--leading-relaxed);
}

.editorial .secondary-button {
  align-self: flex-start;
}
```

- [ ] **Step 3: Restructure each screen**

Apply this shape to all four, substituting the display word and keeping each file's existing copy, links, and `data-testid` attributes exactly as found in Step 1:

| File | Display word |
|---|---|
| `NotFound.tsx` | `404` |
| `AccessDenied.tsx` | `Denied` |
| `ServiceUnavailable.tsx` | `Offline` |
| `RoutePlaceholder.tsx` | `Soon` |

The pattern, shown for `NotFound.tsx`:

```tsx
    <section className="editorial" data-testid="not-found">
      <p className="eyebrow">Error / 404</p>
      <h1 className="editorial__display">404</h1>
      <hr />
      <p className="editorial__lede">{/* existing copy, unchanged */}</p>
      {/* existing link/button, unchanged, with className="secondary-button" */}
    </section>
```

Keep the original root-element `data-testid` for each file rather than the illustrative `not-found` shown above.

- [ ] **Step 4: Run the full suite and build**

```bash
cd web && npm test 2>&1 | tail -5 && npm run build 2>&1 | tail -5
```

Expected: `Test Files 35 passed (35)`, `Tests 239 passed (239)`. `NotFound.test.tsx`, `AccessDenied.test.tsx`, `ServiceUnavailable.test.tsx`, and `RoutePlaceholder.test.tsx` must all pass unchanged. A failure here means copy or a test id was altered — fix the component, never the test.

- [ ] **Step 5: Commit**

```bash
git add web/src/app/NotFound.tsx web/src/app/AccessDenied.tsx \
        web/src/app/ServiceUnavailable.tsx web/src/app/RoutePlaceholder.tsx \
        web/src/styles/features.css
git commit -m "feat(web): editorial treatment for error and placeholder screens"
```

---

### Task 7: Stacks feature screens

**Files:**
- Modify: `web/src/features/stacks/StacksListScreen.tsx`
- Modify: `web/src/features/stacks/StackDetailShell.tsx`
- Modify: `web/src/styles/features.css` (append)
- Modify: `web/src/styles.css` (drain stacks rules)

**Interfaces:**
- Consumes: `.page-header`, `.eyebrow`, `.page-rule` (Task 3), `.role-badge--*` (Task 4).
- Produces: `.stacks-list`, `.stack-detail-tabs`, `.grant-row`, `.credential-row`, `.search-dropdown` treatments.

- [ ] **Step 1: Append the stacks styles**

Append to `web/src/styles/features.css`:

```css
/* ---- Stacks list. Dense rows, hairline dividers, full inversion on hover. ---- */
.stacks-list-header {
  display: flex;
  align-items: flex-end;
  justify-content: space-between;
  gap: var(--space-4);
  margin-bottom: var(--space-6);
}

.stacks-list {
  display: grid;
  gap: 0;
  margin: 0;
  border-top: var(--rule-thin) solid var(--color-fg);
  padding: 0;
  list-style: none;
}

.stacks-list li {
  display: flex;
  align-items: baseline;
  gap: var(--space-4);
  border-bottom: var(--rule-hairline) solid var(--color-border-light);
  padding: var(--space-4);
  background: var(--color-bg);
  transition: background var(--transition-fast), color var(--transition-fast);
}

.stacks-list li:hover {
  color: var(--color-inverse-fg);
  background: var(--color-fg);
}

.stacks-list li a {
  color: inherit;
  font-family: var(--font-body);
  font-size: var(--text-lg);
  font-weight: 600;
}

.stacks-list li small {
  color: inherit;
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  letter-spacing: var(--tracking-wide);
  opacity: 0.7;
}

/* ---- Tabs. Underline-driven, no fills. ---- */
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
  gap: var(--space-5);
  margin-bottom: var(--space-6);
}

.run-tabs button,
.phase-row button,
.stack-detail-tabs a {
  min-height: var(--space-8);
  border: 0;
  border-bottom: var(--rule-medium) solid transparent;
  border-radius: var(--radius);
  padding: var(--space-2) 0;
  color: var(--color-muted-fg);
  background: transparent;
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  letter-spacing: var(--tracking-widest);
  text-transform: uppercase;
  text-decoration: none;
  cursor: pointer;
  transition: color var(--transition-fast), border-color var(--transition-fast);
}

.run-tabs button:hover,
.phase-row button:hover,
.stack-detail-tabs a:hover {
  color: var(--color-fg);
  border-bottom-color: var(--color-border-light);
  text-decoration: none;
}

.run-tabs button.active,
.phase-row button.active,
.stack-detail-tabs a.active {
  color: var(--color-fg);
  border-bottom-color: var(--color-fg);
}

.stack-detail-tabs a {
  display: inline-flex;
  align-items: center;
}

/* ---- Grants ---- */
.grants-list {
  display: grid;
  gap: 0;
  margin: 0;
  border-top: var(--rule-thin) solid var(--color-fg);
  padding: 0;
  list-style: none;
}

.grant-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
  border-bottom: var(--rule-hairline) solid var(--color-border-light);
  padding: var(--space-3) var(--space-2);
  background: var(--color-bg);
}

.grant-row .grant-user {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  min-width: 0;
}

.grant-row .grant-user span {
  font-size: var(--text-base);
  font-weight: 600;
}

.grant-row .grant-user small {
  color: var(--color-muted-fg);
  font-family: var(--font-mono);
  font-size: var(--text-xs);
}

.grant-actions {
  display: flex;
  gap: var(--space-2);
}

.grant-actions button {
  min-height: var(--space-8);
  border: var(--rule-thin) solid var(--color-fg);
  border-radius: var(--radius);
  padding: var(--space-1) var(--space-3);
  color: var(--color-fg);
  background: transparent;
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  letter-spacing: var(--tracking-wide);
  text-transform: uppercase;
  cursor: pointer;
  transition: background var(--transition-fast), color var(--transition-fast);
}

.grant-actions button:hover {
  color: var(--color-inverse-fg);
  background: var(--color-fg);
}

.grant-actions button.danger:hover {
  border-color: var(--color-critical);
  background: var(--color-critical);
}

/* ---- Credentials ---- */
.credentials-panel {
  grid-column: 1 / -1;
}

.credentials-list {
  display: grid;
  gap: 0;
  margin: var(--space-4) 0;
  border-top: var(--rule-thin) solid var(--color-fg);
}

.credential-row {
  display: grid;
  grid-template-columns: minmax(160px, 1fr) auto auto;
  align-items: center;
  gap: var(--space-3);
  border-bottom: var(--rule-hairline) solid var(--color-border-light);
  padding: var(--space-3) 0;
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
  z-index: 10;
  max-height: 240px;
  overflow-y: auto;
  border: var(--rule-medium) solid var(--color-fg);
  border-radius: var(--radius);
  margin-top: var(--rule-medium);
  background: var(--color-bg);
}

.search-result-item {
  border-bottom: var(--rule-hairline) solid var(--color-border-light);
  padding: var(--space-2) var(--space-3);
  font-size: var(--text-sm);
  cursor: pointer;
}

.search-result-item:hover,
.search-result-item.focused {
  color: var(--color-inverse-fg);
  background: var(--color-fg);
}

.search-result-item.assigned {
  color: var(--color-muted-fg);
  background: var(--color-muted);
  cursor: default;
}

.search-result-item small {
  display: block;
  color: inherit;
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  opacity: 0.7;
}

.selected-user-card {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
  border: var(--rule-medium) solid var(--color-fg);
  border-radius: var(--radius);
  margin-top: var(--space-3);
  padding: var(--space-3);
  background: var(--color-bg);
}

.selected-user-card span {
  font-size: var(--text-base);
  font-weight: 600;
}

.selected-user-card button {
  border: 0;
  padding: var(--space-1);
  color: var(--color-fg);
  background: none;
  cursor: pointer;
}

/* ---- Undo banner. Inverted so it reads as a system-level interruption. ---- */
.undo-banner {
  position: fixed;
  bottom: 0;
  left: 0;
  right: 0;
  z-index: 20;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: var(--space-3);
  border-top: var(--rule-thick) solid var(--color-fg);
  padding: var(--space-3) var(--space-6);
  color: var(--color-inverse-fg);
  background: var(--color-fg);
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  letter-spacing: var(--tracking-wide);
  text-transform: uppercase;
}

.undo-banner button {
  min-height: var(--space-8);
  border: var(--rule-thin) solid var(--color-bg);
  border-radius: var(--radius);
  padding: var(--space-1) var(--space-3);
  color: var(--color-inverse-fg);
  background: transparent;
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  letter-spacing: var(--tracking-widest);
  text-transform: uppercase;
  cursor: pointer;
}

.undo-banner button:hover {
  color: var(--color-fg);
  background: var(--color-bg);
}

/* ---- Forms and layout grids ---- */
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

Delete from `web/src/styles.css`: `.workflow-grid`, `.form-grid, .variable-grid`, `.selector-label`, `.button-row, .phase-row, .run-tabs, .stack-detail-tabs`, `.form-actions`, `.run-tabs button, .phase-row button, .stack-detail-tabs a`, `.run-tabs button.active, …`, `.stack-detail-header`, `.stack-detail-tabs a`, `.credentials-panel`, `.credentials-list`, `.credential-row`, `.credential-form`, the `@media (max-width: 700px)` block, `.stacks-list-header`, `.stacks-list-header h1`, `.stacks-list`, `.stacks-list li`, `.stacks-list li a`, `.grant-row`, `.grant-row .grant-user`, `.grant-row .grant-user span`, `.grant-row .grant-user small`, `.grant-actions`, `.grant-actions button`, `.grant-actions button.danger`, `.grants-list`, `.search-dropdown`, `.search-result-item`, `.search-result-item:hover, .search-result-item.focused`, `.search-result-item.assigned`, `.search-result-item small`, `.search-wrapper`, `.selected-user-card`, `.selected-user-card span`, `.selected-user-card button`, `.undo-banner`, `.undo-banner button`, `@keyframes slideUp`, `.form-row`, `.form-row select`.

- [ ] **Step 3: Convert the stacks list header to a page header**

In `web/src/features/stacks/StacksListScreen.tsx`, wrap the existing `<h1>` in the page-header pattern. Keep the existing heading text and all sibling elements (the create-stack action) exactly as they are:

```tsx
      <div className="stacks-list-header">
        <div className="page-header">
          <p className="eyebrow">Stacks / Index</p>
          {/* existing <h1> element, unchanged */}
          <hr className="page-rule" />
        </div>
        {/* existing action element(s), unchanged */}
      </div>
```

- [ ] **Step 4: Add an eyebrow to the stack detail header**

In `web/src/features/stacks/StackDetailShell.tsx`, wrap the contents of `.stack-detail-header` so an eyebrow sits above the existing heading:

```tsx
      <div className="stack-detail-header">
        <div className="page-header">
          <p className="eyebrow">Stack / Detail</p>
          {/* existing heading element and its siblings, unchanged */}
        </div>
      </div>
```

Do not add a `.page-rule` here. The tab row immediately below already provides a horizontal division, and two rules stacked would read as a mistake.

- [ ] **Step 5: Run the full suite and build**

```bash
cd web && npm test 2>&1 | tail -5 && npm run build 2>&1 | tail -5
```

Expected: `Test Files 35 passed (35)`, `Tests 239 passed (239)`. Build clean.

- [ ] **Step 6: Commit**

```bash
git add web/src/styles/features.css web/src/styles.css \
        web/src/features/stacks/StacksListScreen.tsx \
        web/src/features/stacks/StackDetailShell.tsx
git commit -m "feat(web): monochrome stacks, grants, and credentials screens"
```

---

### Task 8: Templates and runs screens

Includes the log panel, which is already an inverted element and becomes the design's most natural black surface.

**Files:**
- Modify: `web/src/features/templates/TemplateRegistryScreen.tsx`
- Modify: `web/src/features/runs/RunsListScreen.tsx`
- Modify: `web/src/styles/features.css` (append)
- Modify: `web/src/styles.css` (drain remaining rules)

**Interfaces:**
- Consumes: `.page-header`, `.eyebrow`, `.page-rule` (Task 3), `.status-tone--*` (Task 4).
- Produces: `.log-panel`, `.stack-template-*`, `.revision-grid` treatments.

- [ ] **Step 1: Append the templates and runs styles**

Append to `web/src/styles/features.css`:

```css
/* ---- Template lists ---- */
.stack-template-list {
  display: grid;
  gap: var(--space-3);
  margin-top: var(--space-5);
  border-top: var(--rule-thin) solid var(--color-fg);
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
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  font-weight: 500;
  letter-spacing: var(--tracking-widest);
  text-transform: uppercase;
}

.stack-template-list-header span {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-width: 26px;
  min-height: var(--space-6);
  border: var(--rule-thin) solid var(--color-fg);
  border-radius: var(--radius);
  padding: 0 var(--space-2);
  color: var(--color-fg);
  background: transparent;
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  font-weight: 500;
}

.stack-template-items {
  display: grid;
  gap: 0;
  border-top: var(--rule-hairline) solid var(--color-border-light);
}

.stack-template-items button {
  display: grid;
  gap: var(--space-1);
  width: 100%;
  min-height: 56px;
  border: 0;
  border-bottom: var(--rule-hairline) solid var(--color-border-light);
  border-left: var(--rule-thick) solid transparent;
  border-radius: var(--radius);
  padding: var(--space-3) var(--space-4);
  color: var(--color-fg);
  background: var(--color-bg);
  text-align: left;
  cursor: pointer;
  transition: background var(--transition-fast), color var(--transition-fast),
    border-color var(--transition-fast);
}

.stack-template-items button:hover {
  color: var(--color-inverse-fg);
  background: var(--color-fg);
}

.stack-template-items button.active {
  border-left-color: var(--color-fg);
  background: var(--color-muted);
}

.stack-template-items span,
.stack-template-items small {
  min-width: 0;
  overflow-wrap: anywhere;
}

.stack-template-items span {
  font-size: var(--text-base);
  font-weight: 600;
}

.stack-template-items small {
  color: inherit;
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  opacity: 0.7;
}

.lifecycle-badge {
  display: inline-flex;
  align-items: center;
  border: var(--rule-thin) solid var(--color-fg);
  border-radius: var(--radius);
  padding: 0 var(--space-2);
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  letter-spacing: var(--tracking-wide);
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
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  letter-spacing: var(--tracking-wide);
  text-transform: uppercase;
}

.revision-grid dd {
  min-width: 0;
  margin: 0;
  color: var(--color-fg);
  overflow-wrap: anywhere;
  font-family: var(--font-mono);
  font-size: var(--text-sm);
}

/* ---- Log panel. Already an inverted surface; monochrome makes it exact.
   Deliberately carries no texture — patterning behind a monospace log stream
   measurably hurts scanning for errors. ---- */
.log-panel pre {
  min-height: 260px;
  max-height: 460px;
  overflow: auto;
  border: var(--rule-medium) solid var(--color-fg);
  border-radius: var(--radius);
  margin: 0;
  padding: var(--space-4);
  color: var(--color-inverse-fg);
  background: var(--color-fg);
  font-family: var(--font-mono);
  font-size: var(--text-sm);
  line-height: var(--leading-normal);
  white-space: pre-wrap;
}

.spin {
  animation: spin 0.9s linear infinite;
}

@keyframes spin {
  to {
    transform: rotate(360deg);
  }
}
```

- [ ] **Step 2: Drain the remaining legacy rules**

Delete from `web/src/styles.css`: `.stack-template-list`, `.stack-template-list-header`, `.stack-template-list h3`, `.stack-template-list-header span`, `.stack-template-items`, `.stack-template-items button`, `.stack-template-items button.active`, `.stack-template-items span, .stack-template-items small`, `.stack-template-items span`, `.stack-template-items small`, `.revision-grid`, `.revision-grid dt`, `.revision-grid dd`, `.log-panel pre`, `.spin`, `@keyframes spin`.

- [ ] **Step 3: Add page headers to the templates and runs screens**

In `web/src/features/templates/TemplateRegistryScreen.tsx`, wrap the existing `<h1>` so it reads:

```tsx
        <div className="page-header">
          <p className="eyebrow">Templates / Registry</p>
          {/* existing <h1> element, unchanged */}
          <hr className="page-rule" />
        </div>
```

In `web/src/features/runs/RunsListScreen.tsx`, apply the identical structure with a different eyebrow:

```tsx
        <div className="page-header">
          <p className="eyebrow">Runs / History</p>
          {/* existing <h1> element, unchanged */}
          <hr className="page-rule" />
        </div>
```

Preserve every existing heading string, sibling element, and `data-testid` exactly as found.

- [ ] **Step 4: Run the full suite and build**

```bash
cd web && npm test 2>&1 | tail -5 && npm run build 2>&1 | tail -5
```

Expected: `Test Files 35 passed (35)`, `Tests 239 passed (239)`. Build clean.

- [ ] **Step 5: Commit**

```bash
git add web/src/styles/features.css web/src/styles.css \
        web/src/features/templates/TemplateRegistryScreen.tsx \
        web/src/features/runs/RunsListScreen.tsx
git commit -m "feat(web): monochrome templates, runs, and log panel"
```

---

### Task 9: Responsive pass, touch targets, and final sweep

Closes out the migration: `styles.css` becomes a pure index, responsive rules are rebuilt on tokens, and the guard test is extended to prove no legacy styling survives anywhere.

**Files:**
- Modify: `web/src/styles/features.css` (append responsive block)
- Modify: `web/src/styles.css` (reduce to imports only)
- Modify: `web/src/styles/styles.guard.test.ts` (extend)
- Modify: the two files still using inline styles

- [ ] **Step 1: Find the two remaining inline-style files**

```bash
cd web && grep -rn 'style={{' src
```

Convert each to a class in `features.css` unless the value is genuinely dynamic (for example a computed width percentage), in which case leave it — inline styles for computed values are correct and are not a design-system violation.

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

  .credential-form {
    grid-template-columns: 1fr;
  }

  .page-header h1 {
    font-size: var(--text-3xl);
  }

  /* The monochrome drama must survive on mobile, but 44px touch targets win
     over density where the two conflict. */
  .app-nav a,
  .run-tabs button,
  .phase-row button,
  .stack-detail-tabs a,
  .grant-actions button,
  .identity-menu button,
  .icon-button {
    min-height: var(--touch-target);
  }

  .stacks-list li,
  .grant-row,
  .credential-row {
    min-height: var(--touch-target);
    padding-top: var(--space-3);
    padding-bottom: var(--space-3);
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
cd web && grep -oE '^\.[a-z0-9-]+' src/styles/*.css | sed 's/.*://' | sort -u > /tmp/new-classes.txt
git show HEAD~8:web/src/styles.css | grep -oE '^\.[a-z0-9-]+' | sort -u > /tmp/old-classes.txt
comm -23 /tmp/old-classes.txt /tmp/new-classes.txt
```

Expected: empty output. Any class listed was dropped during the drain and must be restored to `features.css` in the new style. (`HEAD~8` assumes one commit per task from Task 1; adjust the ref if the history differs.)

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

- [ ] **Step 6: Run the guard test**

```bash
cd web && npx vitest run src/styles/styles.guard.test.ts
```

Expected: PASS, now covering `base.css`, `primitives.css`, `features.css`, and the index.

- [ ] **Step 7: Run the full suite and build**

```bash
cd web && npm test 2>&1 | tail -5 && npm run build 2>&1 | tail -5
```

Expected: `Test Files 35 passed (35)`, `Tests 240 passed (240)`. Build clean.

- [ ] **Step 8: Verify the design constraints hold across the whole stylesheet set**

```bash
cd web
echo "--- raw hex outside tokens.css (expect 0) ---"
grep -hoE '#[0-9a-fA-F]{3,8}' src/styles/base.css src/styles/primitives.css src/styles/features.css | wc -l
echo "--- border-radius not using the token (expect 0) ---"
grep -hoE 'border-radius:[^;]+' src/styles/*.css | grep -v 'var(--radius)' | wc -l
echo "--- box-shadow (expect 0) ---"
grep -hoE 'box-shadow' src/styles/*.css | wc -l
```

Expected: `0`, `0`, `0` (each possibly with leading whitespace from `wc`).

> These use BSD-compatible flags only — macOS `grep` has no `-P`. The guard
> test already enforces all three constraints programmatically; this step is
> a human-readable confirmation, not the primary check.

- [ ] **Step 9: Observe the result at runtime**

Use the project's `web:verify` skill, which documents the working recipe (stub API on 8081, Vite on 5173, headless Chrome with `--virtual-time-budget=6000`). Capture screenshots of the stacks list, a stack detail screen, and a 404 to confirm the monochrome treatment renders as intended and that the fonts actually load rather than falling back to Georgia.

- [ ] **Step 10: Commit**

```bash
git add web/src/styles web/src/styles.css
git commit -m "feat(web): responsive pass, touch targets, and monochrome guard coverage"
```

---

## Notes for the implementer

**On draining `styles.css`.** Each task deletes the rules it has replaced. Work by selector, not by line number — line numbers shift with every deletion. After each task, `grep -c '^\.' web/src/styles.css` should be strictly decreasing.

**On test failures in Tasks 4–8.** No test asserts on class names, so a failure means markup changed in a way that lost a `data-testid`, an ARIA role, or a copy string. Fix the component. Do not edit a test to accommodate a restyle — that converts a caught regression into a silent one.

**On the reserved red.** It appears at exactly three declarations across the whole codebase: `.status-tone--failed` (color), `.alert` (left border), and `.destructive-button[data-confirming="true"]` (fill), plus the `.error-text::before` glyph and the `.grant-actions button.danger:hover` fill. If you find yourself adding a sixth, stop — that is scope creep on a constraint the spec deliberately drew tight.

**On wiring the destructive confirm.** Task 2 defines `.destructive-button[data-confirming="true"]` but no component sets that attribute yet. Wiring a real two-step confirm flow is a behaviour change, not a restyle, and is deliberately out of scope. The style is defined so the affordance exists the moment someone implements the flow.
