# Typed Layout Primitives Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the 48 ad-hoc layout declarations in `web/src/styles/features.css` with six typed React layout primitives whose props are constrained to the existing `--space-*` scale, so layout can no longer be hand-tuned in pixels.

**Architecture:** Primitives in `web/src/shared/layout.tsx` render a shared `l` base class plus enumerable `data-*` attributes; every CSS rule lives in `web/src/styles/primitives.css`. Inline `style` is never used, so no unenumerated value can reach the DOM. Four new rules in `web/src/styles/styles.guard.test.ts` make layout outside `primitives.css` a test failure, introduced behind a grandfather allowlist that shrinks each batch.

**Tech Stack:** React 18, TypeScript 5.6, Vite 8, Vitest 4, Testing Library. No new dependencies.

**Spec:** `docs/superpowers/specs/2026-08-15-web-layout-primitives-design.md`

## Global Constraints

- All work happens in `web/`. Run commands from that directory.
- Test command: `npm test` (`vitest run`). Single file: `npx vitest run src/path/file.test.tsx`.
- Type check: `npx tsc -b`. Full build: `npm run build`.
- No new npm dependencies.
- `tokens.css` is the only file permitted to contain raw hex literals.
- Approved breakpoints are exactly **768px** (`md`) and **900px** (`lg`). Existing 760px and 920px queries normalize onto them.
- Spacing scale (`Space`): `1 | 2 | 3 | 4 | 5 | 6 | 8 | 10 | 12 | 16 | 24`.
- Track floor scale (`MinTrack`): `140 | 160 | 200 | 280 | 320`.
- Never use inline `style={{...}}` to express layout.
- Tests assert on `data-testid` and text, never on layout class names.

## Migration Procedure

Tasks 8, 9, 11–16 all apply this procedure. It is spelled out once here; each task lists its own files, classes, and verification.

1. **Ensure a test exists.** If the component has no test file, write a characterization test first against current rendered output (roles, text, `data-testid`), and commit it before changing markup.
2. **Split each CSS class in two.** Layout declarations (`display:grid`, `display:flex`, `grid-template-columns`, `gap`, `align-items`, `justify-content`, `flex-wrap`, `flex-direction`) move into primitive props. Everything else (border, radius, padding, color, font, margin) stays in the class.
3. **Replace the markup.** Wrap in the primitive, keep the decorative class via `className`, keep every `data-testid` and `aria-*` attribute exactly as-is.
4. **Delete the layout declarations** from the now-decoration-only class.
5. **Shrink the allowlist** in `styles.guard.test.ts` by removing the file just migrated, if it was listed.
6. **Verify:** `npm test`, then `npx tsc -b`, then the `web:verify` skill on the affected screen.
7. **Commit.**

---

### Task 1: Layout tokens

**Files:**
- Modify: `web/src/styles/tokens.css`
- Test: `web/src/styles/styles.guard.test.ts:24-36` (extend `REQUIRED_TOKENS`)

**Interfaces:**
- Consumes: nothing.
- Produces: CSS custom properties `--layout-min-0`, `--layout-min-140`, `--layout-min-160`, `--layout-min-200`, `--layout-min-280`, `--layout-min-320`.

- [ ] **Step 1: Write the failing test**

Add the six names to the `REQUIRED_TOKENS` array in `styles.guard.test.ts`:

```ts
  "--layout-min-0", "--layout-min-140", "--layout-min-160",
  "--layout-min-200", "--layout-min-280", "--layout-min-320"
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest run src/styles/styles.guard.test.ts`
Expected: FAIL — `missing --layout-min-0`

- [ ] **Step 3: Write minimal implementation**

Append to `tokens.css`, inside `:root`, after the `/* Controls */` block:

```css
  /* Track floors for the layout primitives. These are the only length values
     grid-template-columns may reference; see styles.guard.test.ts. */
  --layout-min-0:   0px;
  --layout-min-140: 140px;
  --layout-min-160: 160px;
  --layout-min-200: 200px;
  --layout-min-280: 280px;
  --layout-min-320: 320px;
```

- [ ] **Step 4: Run test to verify it passes**

Run: `npx vitest run src/styles/styles.guard.test.ts`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add src/styles/tokens.css src/styles/styles.guard.test.ts
git commit -m "feat(web): add layout track-floor tokens"
```

---

### Task 2: Stack and Row primitives

**Files:**
- Create: `web/src/shared/layout.tsx`
- Create: `web/src/shared/layout.test.tsx`
- Modify: `web/src/styles/primitives.css` (append layout section)

**Interfaces:**
- Consumes: `--layout-min-*` from Task 1.
- Produces:
  - `type Space = 1|2|3|4|5|6|8|10|12|16|24`
  - `type MinTrack = 140|160|200|280|320`
  - `type Collapse = "md"|"lg"|"never"`
  - `type Align = "start"|"center"|"end"|"baseline"|"stretch"`
  - `type Justify = "start"|"center"|"end"|"between"`
  - `Stack(props: { gap?: Space; align?: Align; as?: ElementType; className?: string; children: ReactNode })`
  - `Row(props: { gap?: Space; align?: Align; justify?: Justify; wrap?: boolean; as?: ElementType; className?: string; children: ReactNode })`

- [ ] **Step 1: Write the failing test**

Create `web/src/shared/layout.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { Row, Stack } from "./layout";

describe("Stack", () => {
  it("emits the base and variant classes with no data attributes by default", () => {
    render(<Stack><span>child</span></Stack>);
    const el = screen.getByText("child").parentElement!;
    expect(el.className).toBe("l stack");
    expect(el.getAttribute("data-gap")).toBeNull();
  });

  it("emits gap and align as data attributes", () => {
    render(<Stack gap={6} align="center"><span>child</span></Stack>);
    const el = screen.getByText("child").parentElement!;
    expect(el.getAttribute("data-gap")).toBe("6");
    expect(el.getAttribute("data-align")).toBe("center");
  });

  it("renders a custom element and preserves a decorative class", () => {
    render(<Stack as="section" className="panel"><span>child</span></Stack>);
    const el = screen.getByText("child").parentElement!;
    expect(el.tagName).toBe("SECTION");
    expect(el.className).toBe("l stack panel");
  });

  it("never emits an inline style attribute", () => {
    render(<Stack gap={4}><span>child</span></Stack>);
    expect(screen.getByText("child").parentElement!.getAttribute("style")).toBeNull();
  });
});

describe("Row", () => {
  it("emits justify and wrap", () => {
    render(<Row gap={3} justify="between" wrap><span>child</span></Row>);
    const el = screen.getByText("child").parentElement!;
    expect(el.className).toBe("l row");
    expect(el.getAttribute("data-justify")).toBe("between");
    expect(el.getAttribute("data-wrap")).toBe("true");
  });

  it("omits data-wrap when wrap is false", () => {
    render(<Row wrap={false}><span>child</span></Row>);
    expect(screen.getByText("child").parentElement!.getAttribute("data-wrap")).toBeNull();
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest run src/shared/layout.test.tsx`
Expected: FAIL — cannot resolve `./layout`

- [ ] **Step 3: Write minimal implementation**

Create `web/src/shared/layout.tsx`:

```tsx
import type { ElementType, ReactNode } from "react";

export type Space = 1 | 2 | 3 | 4 | 5 | 6 | 8 | 10 | 12 | 16 | 24;
export type MinTrack = 140 | 160 | 200 | 280 | 320;
export type Collapse = "md" | "lg" | "never";
export type Align = "start" | "center" | "end" | "baseline" | "stretch";
export type Justify = "start" | "center" | "end" | "between";

/** Joins the base class, the variant class, and any decorative class. */
function cx(variant: string, className?: string): string {
  return className ? `l ${variant} ${className}` : `l ${variant}`;
}

interface CommonProps {
  gap?: Space;
  as?: ElementType;
  className?: string;
  children: ReactNode;
}

export function Stack({ gap, align, as: Tag = "div", className, children }: CommonProps & { align?: Align }) {
  return (
    <Tag className={cx("stack", className)} data-gap={gap} data-align={align}>
      {children}
    </Tag>
  );
}

export function Row({
  gap, align, justify, wrap, as: Tag = "div", className, children
}: CommonProps & { align?: Align; justify?: Justify; wrap?: boolean }) {
  return (
    <Tag
      className={cx("row", className)}
      data-gap={gap}
      data-align={align}
      data-justify={justify}
      data-wrap={wrap || undefined}
    >
      {children}
    </Tag>
  );
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `npx vitest run src/shared/layout.test.tsx`
Expected: PASS

- [ ] **Step 5: Add the CSS**

Append to `web/src/styles/primitives.css`:

```css
/* ---- Layout primitives ----
   The single home for layout. styles.guard.test.ts fails the build on any
   display:grid, display:flex, grid-template-columns, or gap declared outside
   this section. Breakpoints are literal because CSS custom properties do not
   resolve inside @media queries; the only approved widths are 768px and 900px. */

.l { gap: var(--space-4); }
.l[data-gap="1"]  { gap: var(--space-1); }
.l[data-gap="2"]  { gap: var(--space-2); }
.l[data-gap="3"]  { gap: var(--space-3); }
.l[data-gap="4"]  { gap: var(--space-4); }
.l[data-gap="5"]  { gap: var(--space-5); }
.l[data-gap="6"]  { gap: var(--space-6); }
.l[data-gap="8"]  { gap: var(--space-8); }
.l[data-gap="10"] { gap: var(--space-10); }
.l[data-gap="12"] { gap: var(--space-12); }
.l[data-gap="16"] { gap: var(--space-16); }
.l[data-gap="24"] { gap: var(--space-24); }

.stack { display: grid; }
.stack[data-align="start"]   { justify-items: start; }
.stack[data-align="center"]  { justify-items: center; }
.stack[data-align="end"]     { justify-items: end; }
.stack[data-align="stretch"] { justify-items: stretch; }

.row { display: flex; align-items: center; }
.row[data-align="start"]    { align-items: flex-start; }
.row[data-align="end"]      { align-items: flex-end; }
.row[data-align="baseline"] { align-items: baseline; }
.row[data-align="stretch"]  { align-items: stretch; }
.row[data-justify="start"]   { justify-content: flex-start; }
.row[data-justify="center"]  { justify-content: center; }
.row[data-justify="end"]     { justify-content: flex-end; }
.row[data-justify="between"] { justify-content: space-between; }
.row[data-wrap="true"] { flex-wrap: wrap; }
```

- [ ] **Step 6: Verify the build and full suite**

Run: `npx tsc -b && npm test`
Expected: PASS, no regressions

- [ ] **Step 7: Commit**

```bash
git add src/shared/layout.tsx src/shared/layout.test.tsx src/styles/primitives.css
git commit -m "feat(web): add Stack and Row layout primitives"
```

---

### Task 3: Grid and Split primitives

**Files:**
- Modify: `web/src/shared/layout.tsx`
- Modify: `web/src/shared/layout.test.tsx`
- Modify: `web/src/styles/primitives.css` (layout section)

**Interfaces:**
- Consumes: `Space`, `MinTrack`, `Collapse`, `cx` from Task 2.
- Produces:
  - `Grid(props: { cols: 2|3|4; min?: MinTrack; gap?: Space; collapse?: Collapse; className?: string; children: ReactNode } | { cols?: never; min: MinTrack; gap?: Space; collapse?: Collapse; className?: string; children: ReactNode })`
  - `Split(props: { ratio?: "even"|"wide-left"|"wide-right"; gap?: Space; collapse?: Collapse; className?: string; children: ReactNode })`

- [ ] **Step 1: Write the failing test**

Append to `web/src/shared/layout.test.tsx`:

```tsx
import { Grid, Split } from "./layout";

describe("Grid", () => {
  it("emits a fixed column count", () => {
    render(<Grid cols={2} gap={6}><span>child</span></Grid>);
    const el = screen.getByText("child").parentElement!;
    expect(el.className).toBe("l grid");
    expect(el.getAttribute("data-cols")).toBe("2");
    expect(el.getAttribute("data-min")).toBeNull();
  });

  it("emits a track floor alongside a column count", () => {
    render(<Grid cols={2} min={140}><span>child</span></Grid>);
    const el = screen.getByText("child").parentElement!;
    expect(el.getAttribute("data-min")).toBe("140");
  });

  it("emits an auto-fit grid when only min is given", () => {
    render(<Grid min={200}><span>child</span></Grid>);
    const el = screen.getByText("child").parentElement!;
    expect(el.getAttribute("data-cols")).toBeNull();
    expect(el.getAttribute("data-min")).toBe("200");
  });

  it("emits the collapse breakpoint", () => {
    render(<Grid cols={3} collapse="md"><span>child</span></Grid>);
    expect(screen.getByText("child").parentElement!.getAttribute("data-collapse")).toBe("md");
  });
});

describe("Split", () => {
  it("defaults to no ratio attribute", () => {
    render(<Split><span>child</span></Split>);
    const el = screen.getByText("child").parentElement!;
    expect(el.className).toBe("l split");
    expect(el.getAttribute("data-ratio")).toBeNull();
  });

  it("emits the ratio", () => {
    render(<Split ratio="wide-right" gap={6} collapse="lg"><span>child</span></Split>);
    const el = screen.getByText("child").parentElement!;
    expect(el.getAttribute("data-ratio")).toBe("wide-right");
    expect(el.getAttribute("data-collapse")).toBe("lg");
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest run src/shared/layout.test.tsx`
Expected: FAIL — `Grid` is not exported

- [ ] **Step 3: Write minimal implementation**

Append to `web/src/shared/layout.tsx`:

```tsx
type GridProps = CommonProps & { collapse?: Collapse } & (
  | { cols: 2 | 3 | 4; min?: MinTrack }
  | { cols?: never; min: MinTrack }
);

export function Grid({ gap, cols, min, collapse, as: Tag = "div", className, children }: GridProps) {
  return (
    <Tag
      className={cx("grid", className)}
      data-gap={gap}
      data-cols={cols}
      data-min={min}
      data-collapse={collapse}
    >
      {children}
    </Tag>
  );
}

export function Split({
  gap, ratio, collapse, as: Tag = "div", className, children
}: CommonProps & { ratio?: "even" | "wide-left" | "wide-right"; collapse?: Collapse }) {
  return (
    <Tag className={cx("split", className)} data-gap={gap} data-ratio={ratio} data-collapse={collapse}>
      {children}
    </Tag>
  );
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `npx vitest run src/shared/layout.test.tsx`
Expected: PASS

- [ ] **Step 5: Add the CSS**

Append to the layout section of `web/src/styles/primitives.css`:

```css
/* Track floors. --track-min defaults to zero so an auto-fit grid without an
   explicit floor behaves as a plain 1fr repeat. */
.l { --track-min: var(--layout-min-0); }
.l[data-min="140"] { --track-min: var(--layout-min-140); }
.l[data-min="160"] { --track-min: var(--layout-min-160); }
.l[data-min="200"] { --track-min: var(--layout-min-200); }
.l[data-min="280"] { --track-min: var(--layout-min-280); }
.l[data-min="320"] { --track-min: var(--layout-min-320); }

.grid, .split { display: grid; grid-template-columns: var(--tracks); }

.grid { --tracks: repeat(auto-fit, minmax(var(--track-min), 1fr)); }
.grid[data-cols="2"] { --tracks: repeat(2, minmax(var(--track-min), 1fr)); }
.grid[data-cols="3"] { --tracks: repeat(3, minmax(var(--track-min), 1fr)); }
.grid[data-cols="4"] { --tracks: repeat(4, minmax(var(--track-min), 1fr)); }

.split { --tracks: 1fr 1fr; }
.split[data-ratio="wide-left"]  { --tracks: 3fr 2fr; }
.split[data-ratio="wide-right"] { --tracks: 2fr 3fr; }

/* Collapse is mobile-first: a collapsing element is single-column until its
   breakpoint, which keeps every media query on an approved min-width. */
.l[data-collapse="md"], .l[data-collapse="lg"] { grid-template-columns: 1fr; }

@media (min-width: 768px) {
  .l[data-collapse="md"] { grid-template-columns: var(--tracks); }
}

@media (min-width: 900px) {
  .l[data-collapse="lg"] { grid-template-columns: var(--tracks); }
}
```

- [ ] **Step 6: Verify**

Run: `npx tsc -b && npm test`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add src/shared/layout.tsx src/shared/layout.test.tsx src/styles/primitives.css
git commit -m "feat(web): add Grid and Split layout primitives"
```

---

### Task 4: Fields and Rows primitives

**Files:**
- Modify: `web/src/shared/layout.tsx`
- Modify: `web/src/shared/layout.test.tsx`
- Modify: `web/src/styles/primitives.css` (layout section)

**Interfaces:**
- Consumes: `Space`, `cx`, `CommonProps` from Task 2; `--track-min` from Task 3.
- Produces:
  - `Fields(props: { gap?: Space; columnGap?: Space; as?: ElementType; className?: string; children: ReactNode })`
  - `Rows(props: { lead?: 1|2; trailing?: 1|2; min?: MinTrack; gap?: Space; align?: Align; className?: string; children: ReactNode })`

- [ ] **Step 1: Write the failing test**

Append to `web/src/shared/layout.test.tsx`:

```tsx
import { Fields, Rows } from "./layout";

describe("Fields", () => {
  it("emits row and column gaps independently", () => {
    render(<Fields as="dl" gap={2} columnGap={4}><dt>k</dt></Fields>);
    const el = screen.getByText("k").parentElement!;
    expect(el.tagName).toBe("DL");
    expect(el.className).toBe("l fields");
    expect(el.getAttribute("data-gap")).toBe("2");
    expect(el.getAttribute("data-col-gap")).toBe("4");
  });
});

describe("Rows", () => {
  it("emits lead and trailing column counts", () => {
    render(<Rows lead={1} trailing={2} min={160} align="center"><span>child</span></Rows>);
    const el = screen.getByText("child").parentElement!;
    expect(el.className).toBe("l rows");
    expect(el.getAttribute("data-lead")).toBe("1");
    expect(el.getAttribute("data-trailing")).toBe("2");
    expect(el.getAttribute("data-min")).toBe("160");
    expect(el.getAttribute("data-align")).toBe("center");
  });

  it("defaults to one leading and one trailing column", () => {
    render(<Rows><span>child</span></Rows>);
    const el = screen.getByText("child").parentElement!;
    expect(el.getAttribute("data-lead")).toBe("1");
    expect(el.getAttribute("data-trailing")).toBe("1");
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest run src/shared/layout.test.tsx`
Expected: FAIL — `Fields` is not exported

- [ ] **Step 3: Write minimal implementation**

Append to `web/src/shared/layout.tsx`:

```tsx
export function Fields({
  gap, columnGap, as: Tag = "dl", className, children
}: CommonProps & { columnGap?: Space }) {
  return (
    <Tag className={cx("fields", className)} data-gap={gap} data-col-gap={columnGap}>
      {children}
    </Tag>
  );
}

export function Rows({
  gap, lead = 1, trailing = 1, min, align, as: Tag = "div", className, children
}: CommonProps & { lead?: 1 | 2; trailing?: 1 | 2; min?: MinTrack; align?: Align }) {
  return (
    <Tag
      className={cx("rows", className)}
      data-gap={gap}
      data-lead={lead}
      data-trailing={trailing}
      data-min={min}
      data-align={align}
    >
      {children}
    </Tag>
  );
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `npx vitest run src/shared/layout.test.tsx`
Expected: PASS

- [ ] **Step 5: Add the CSS**

Append to the layout section of `web/src/styles/primitives.css`. These rules must come after the `.l[data-gap]` block so the column-gap override wins on equal specificity:

```css
.fields, .rows { display: grid; grid-template-columns: var(--tracks); }

.fields { --tracks: max-content minmax(0, 1fr); }
.fields[data-col-gap="2"] { column-gap: var(--space-2); }
.fields[data-col-gap="4"] { column-gap: var(--space-4); }
.fields[data-col-gap="6"] { column-gap: var(--space-6); }

.rows { align-items: center; }
.rows[data-align="end"]      { align-items: flex-end; }
.rows[data-align="start"]    { align-items: flex-start; }
.rows[data-align="baseline"] { align-items: baseline; }
.rows[data-align="stretch"]  { align-items: stretch; }
.rows[data-lead="1"][data-trailing="1"] { --tracks: minmax(var(--track-min), 1fr) auto; }
.rows[data-lead="1"][data-trailing="2"] { --tracks: minmax(var(--track-min), 1fr) auto auto; }
.rows[data-lead="2"][data-trailing="1"] { --tracks: repeat(2, minmax(var(--track-min), 1fr)) auto; }
.rows[data-lead="2"][data-trailing="2"] { --tracks: repeat(2, minmax(var(--track-min), 1fr)) auto auto; }
```

- [ ] **Step 6: Verify**

Run: `npx tsc -b && npm test`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add src/shared/layout.tsx src/shared/layout.test.tsx src/styles/primitives.css
git commit -m "feat(web): add Fields and Rows layout primitives"
```

---

### Task 5: Styleguide layout section

**Files:**
- Modify: `web/src/dev/StyleGuide.tsx`
- Modify: `web/src/dev/StyleGuide.test.tsx`

**Interfaces:**
- Consumes: all six primitives from Tasks 2–4.
- Produces: a `<Section id="layout">` rendering every primitive at every supported prop value. This is the reference an agent reads before writing a screen.

- [ ] **Step 1: Write the failing test**

Add to `web/src/dev/StyleGuide.test.tsx`:

```tsx
it("documents every layout primitive", () => {
  render(<StyleGuide />);
  const section = screen.getByTestId("sg-layout");
  for (const name of ["Stack", "Row", "Grid", "Split", "Fields", "Rows"]) {
    expect(within(section).getByText(name)).toBeTruthy();
  }
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest run src/dev/StyleGuide.test.tsx`
Expected: FAIL — unable to find `sg-layout`

- [ ] **Step 3: Write minimal implementation**

Import the primitives into `StyleGuide.tsx` with `import { Fields, Grid, Row, Rows, Split, Stack } from "../shared/layout";` and add this inside the returned tree, following the file's existing `Section`/`Specimen` pattern:

```tsx
<Section id="layout" title="Layout">
  <div data-testid="sg-layout">
    <Specimen label="Stack">
      {([2, 4, 6] as const).map((gap) => (
        <Stack key={gap} gap={gap}>
          <div className="sg__box">gap {gap}</div>
          <div className="sg__box">gap {gap}</div>
        </Stack>
      ))}
    </Specimen>

    <Specimen label="Row">
      {(["start", "center", "end", "between"] as const).map((justify) => (
        <Row key={justify} gap={3} justify={justify}>
          <div className="sg__box">{justify}</div>
          <div className="sg__box">b</div>
        </Row>
      ))}
      <Row gap={2} wrap>
        {Array.from({ length: 8 }, (_, i) => <div className="sg__box" key={i}>wrap {i}</div>)}
      </Row>
    </Specimen>

    <Specimen label="Grid">
      {([2, 3, 4] as const).map((cols) => (
        <Grid key={cols} cols={cols} gap={4} collapse="md">
          {Array.from({ length: cols }, (_, i) => <div className="sg__box" key={i}>cols {cols}</div>)}
        </Grid>
      ))}
      <Grid min={200} gap={4}>
        {Array.from({ length: 5 }, (_, i) => <div className="sg__box" key={i}>auto-fit 200</div>)}
      </Grid>
    </Specimen>

    <Specimen label="Split">
      {(["even", "wide-left", "wide-right"] as const).map((ratio) => (
        <Split key={ratio} ratio={ratio} gap={6} collapse="lg">
          <div className="sg__box">{ratio} left</div>
          <div className="sg__box">{ratio} right</div>
        </Split>
      ))}
    </Specimen>

    <Specimen label="Fields">
      <Fields gap={2} columnGap={4}>
        <dt>Registration</dt><dd>reg-1</dd>
        <dt>Stack</dt><dd>stack-1</dd>
      </Fields>
    </Specimen>

    <Specimen label="Rows">
      {([1, 2] as const).map((lead) => ([1, 2] as const).map((trailing) => (
        <Rows key={`${lead}-${trailing}`} lead={lead} trailing={trailing} min={160} gap={3}>
          <div className="sg__box">lead {lead}</div>
          {lead === 2 && <div className="sg__box">lead 2b</div>}
          <div className="sg__box">auto</div>
          {trailing === 2 && <div className="sg__box">auto</div>}
        </Rows>
      )))}
    </Specimen>
  </div>
</Section>
```

Add a neutral `.sg__box` specimen block to `web/src/dev/styleguide.css` so each cell is visible:

```css
.sg__box {
  border: 1px solid var(--color-border);
  border-radius: var(--radius-sm);
  padding: var(--space-2);
  background: var(--color-muted);
  font-size: var(--text-xs);
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `npx vitest run src/dev/StyleGuide.test.tsx`
Expected: PASS

- [ ] **Step 5: Verify visually**

Use the `web:verify` skill to open the styleguide route and confirm every specimen renders with visible structure.

- [ ] **Step 6: Commit**

```bash
git add src/dev/StyleGuide.tsx src/dev/StyleGuide.test.tsx
git commit -m "docs(web): document the layout primitives in the styleguide"
```

---

### Task 6: Guard rule 1 — spacing tokens

**Files:**
- Modify: `web/src/styles/styles.guard.test.ts`
- Modify: `web/src/styles/features.css` (fix the six existing violations)

**Interfaces:**
- Consumes: nothing.
- Produces: a per-stylesheet test asserting every `gap` and `padding` uses `var(--space-*)` or `0`.

Known violations to fix: four gaps written as `0.75rem` (×2), `1rem`, `0.5rem`; two paddings written as `0.6rem 0.8rem` and `0.15rem 0`. Map each to the nearest scale step: `0.5rem`→`--space-2`, `0.75rem`→`--space-3`, `1rem`→`--space-4`, `0.6rem 0.8rem`→`var(--space-2) var(--space-3)`, `0.15rem 0`→`var(--space-1) 0`.

- [ ] **Step 1: Write the failing test**

Add inside the existing `describe.each(convertedStylesheets())` block in `styles.guard.test.ts`:

```ts
  it("uses a spacing token for every gap", () => {
    const matches =
      read(name).match(/(?<![-a-z])gap:(?!\s*(?:var\(--space-[0-9]+\)\s*){1,2};)[^;]+;/g) ?? [];
    expect(matches, `use var(--space-*): ${matches.join(", ")}`).toEqual([]);
  });

  it("uses a spacing token for every padding", () => {
    const matches =
      read(name).match(/(?<![-a-z])padding:(?!\s*(?:0|(?:var\(--space-[0-9]+\)|0)(?:\s+(?:var\(--space-[0-9]+\)|0))*)\s*;)[^;]+;/g) ?? [];
    expect(matches, `use var(--space-*): ${matches.join(", ")}`).toEqual([]);
  });
```

The `(?<![-a-z])` guard keeps `gap:` from matching inside `column-gap:` or `row-gap:`, and `padding:` from matching `padding-left:`.

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest run src/styles/styles.guard.test.ts`
Expected: FAIL listing the six violations above

- [ ] **Step 3: Fix the violations**

Apply the mappings listed above in `features.css`.

- [ ] **Step 4: Run test to verify it passes**

Run: `npx vitest run src/styles/styles.guard.test.ts && npm test`
Expected: PASS

- [ ] **Step 5: Verify visually**

Use the `web:verify` skill. `0.6rem 0.8rem` → `8px 12px` is a real change of 1.6px and 0.8px; confirm nothing shifts perceptibly.

- [ ] **Step 6: Commit**

```bash
git add src/styles/styles.guard.test.ts src/styles/features.css
git commit -m "feat(web): guard gap and padding against untokenized values"
```

---

### Task 7: Guard rules 2–4 behind an allowlist

**Files:**
- Modify: `web/src/styles/styles.guard.test.ts`

**Interfaces:**
- Consumes: nothing.
- Produces: `LAYOUT_ALLOWLIST` — a `Set<string>` of stylesheet names still permitted to declare layout. Later tasks remove entries; Task 18 deletes the set.

- [ ] **Step 1: Write the failing test**

Add to `styles.guard.test.ts`:

```ts
/** Stylesheets not yet migrated to the layout primitives. Shrinks to empty. */
const LAYOUT_ALLOWLIST = new Set(["features.css"]);

describe.each(convertedStylesheets())("%s layout", (name) => {
  it("declares no layout outside primitives.css", () => {
    if (name === "primitives.css" || LAYOUT_ALLOWLIST.has(name)) return;
    const matches =
      read(name).match(/(display:\s*(grid|flex)|grid-template-columns:)[^;]*;/g) ?? [];
    expect(matches, `move to a layout primitive: ${matches.join(", ")}`).toEqual([]);
  });

  it("uses only approved breakpoints", () => {
    if (LAYOUT_ALLOWLIST.has(name)) return;
    const widths = read(name).match(/\((?:min|max)-width:\s*([0-9]+)px\)/g) ?? [];
    const bad = widths.filter((w) => !/(768|900)px/.test(w));
    expect(bad, `use 768px or 900px: ${bad.join(", ")}`).toEqual([]);
  });

  it("uses no length units in grid tracks", () => {
    if (LAYOUT_ALLOWLIST.has(name)) return;
    // --tracks is checked alongside grid-template-columns: the primitives set
    // their track lists through that custom property, so checking only the
    // longhand would leave the actual track definitions unguarded.
    const tracks = read(name).match(/(grid-template-columns|--tracks):[^;]+;/g) ?? [];
    const bad = tracks.filter((t) => /[0-9]+(px|rem|em|%)/.test(t.replace(/var\(--layout-min-[0-9]+\)/g, "")));
    expect(bad, `use var(--layout-min-*): ${bad.join(", ")}`).toEqual([]);
  });
});
```

- [ ] **Step 2: Run test to verify it passes with the allowlist**

Run: `npx vitest run src/styles/styles.guard.test.ts`
Expected: PASS — the guard reads only `src/styles/`, so the checked set is `base.css`, `features.css`, and `primitives.css`. `features.css` is allowlisted, `primitives.css` is exempt by name, leaving `base.css` genuinely checked. (`src/dev/styleguide.css` is outside `STYLES_DIR` and is not covered by any of these rules.)

- [ ] **Step 3: Verify the rule actually bites**

Temporarily add `display: grid;` to a rule in `base.css`, run the test, confirm FAIL, then revert.

Run: `npx vitest run src/styles/styles.guard.test.ts`
Expected: FAIL, then PASS after revert

- [ ] **Step 4: Commit**

```bash
git add src/styles/styles.guard.test.ts
git commit -m "feat(web): guard layout, breakpoints, and grid tracks behind an allowlist"
```

---

### Task 8: Canary A — Fields via IdsPanel and AppShell

**Files:**
- Create: `web/src/shared/IdsPanel.test.tsx`
- Modify: `web/src/shared/IdsPanel.tsx`
- Modify: `web/src/app/AppShell.tsx`
- Modify: `web/src/styles/features.css:176-181` (`.id-grid`)

**Interfaces:**
- Consumes: `Fields` from Task 4.
- Produces: nothing new.

`.id-grid` is `display:grid; grid-template-columns: max-content minmax(0,1fr); gap: var(--space-2) var(--space-4); margin: var(--space-4) 0 0;` — the first three declarations move to `<Fields gap={2} columnGap={4}>`, the `margin` stays.

- [ ] **Step 1: Write the characterization test**

`IdsPanel.tsx` has no test. Create `web/src/shared/IdsPanel.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import IdsPanel from "./IdsPanel";

const ids = {
  registrationID: "reg-1", templateRevisionID: "trev-1", stackID: "stack-1",
  stackTemplateID: "st-1", desiredRevisionID: "desired-1", appliedRevisionID: "applied-1",
  planRunID: "plan-1", applyRunID: "apply-1"
};

describe("IdsPanel", () => {
  it("pairs every label with its identifier", () => {
    render(<IdsPanel {...ids} />);
    for (const [label, value] of [
      ["Registration", "reg-1"], ["Template revision", "trev-1"], ["Stack", "stack-1"],
      ["Stack template", "st-1"], ["Desired revision", "desired-1"],
      ["Applied revision", "applied-1"], ["Plan run", "plan-1"], ["Apply run", "apply-1"]
    ]) {
      const term = screen.getByText(label);
      expect(term.nextElementSibling?.textContent).toBe(value);
    }
  });
});
```

- [ ] **Step 2: Run it against the unmigrated component**

Run: `npx vitest run src/shared/IdsPanel.test.tsx`
Expected: PASS — this characterizes current behavior before any change

- [ ] **Step 3: Commit the test alone**

```bash
git add src/shared/IdsPanel.test.tsx
git commit -m "test(web): characterize IdsPanel before layout migration"
```

- [ ] **Step 4: Migrate the markup**

In `IdsPanel.tsx`, replace `<dl className="id-grid">` with:

```tsx
<Fields className="id-grid" gap={2} columnGap={4}>
```

Import with `import { Fields } from "./layout";`. Apply the same replacement to the `id-grid` usage in `AppShell.tsx`, importing from `../shared/layout`.

- [ ] **Step 5: Strip layout from the CSS**

In `features.css`, `.id-grid` becomes:

```css
.id-grid {
  margin: var(--space-4) 0 0;
}
```

- [ ] **Step 6: Verify**

Run: `npx vitest run src/shared/IdsPanel.test.tsx src/app/AppShell.test.tsx && npx tsc -b && npm test`
Expected: PASS

Then use the `web:verify` skill on a screen rendering the IDs panel and confirm the two-column label/value alignment is unchanged.

- [ ] **Step 7: Commit**

```bash
git add src/shared/IdsPanel.tsx src/app/AppShell.tsx src/styles/features.css
git commit -m "refactor(web): move the IDs grid onto the Fields primitive"
```

---

### Task 9: Canary B — Rows via CredentialsPanel

**Files:**
- Create: `web/src/features/stacks/CredentialsPanel.test.tsx`
- Modify: `web/src/features/stacks/CredentialsPanel.tsx`
- Modify: `web/src/styles/features.css:542-560` (`.credential-row`, `.credential-form`)

**Interfaces:**
- Consumes: `Rows`, `Stack` from Tasks 2 and 4.
- Produces: nothing new.

This is the only `Rows` call site in the codebase and it has no test. `.credential-row` is one flexible plus two auto columns; `.credential-form` is two flexible plus one auto.

**Deliberate visual change:** `.credential-form`'s second track is `minmax(180px, 1fr)`, and 180 is not on the `MinTrack` scale. It snaps to 160. Both flexible columns then share one floor, which is what `Rows` expresses.

- [ ] **Step 1: Write the characterization test**

Create `web/src/features/stacks/CredentialsPanel.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import CredentialsPanel from "./CredentialsPanel";

const base = {
  title: "Stack credentials", credentials: [], loading: false, busy: false,
  onCreate: vi.fn().mockResolvedValue(undefined), onDelete: vi.fn().mockResolvedValue(undefined)
};

describe("CredentialsPanel", () => {
  it("lists configured credentials with a delete control", () => {
    render(<CredentialsPanel {...base} credentials={[{ id: "c1", name: "AWS_ACCESS_KEY_ID" }] as never} />);
    expect(screen.getByText("AWS_ACCESS_KEY_ID")).toBeTruthy();
    expect(screen.getByLabelText("Delete AWS_ACCESS_KEY_ID")).toBeTruthy();
  });

  it("reports an empty state", () => {
    render(<CredentialsPanel {...base} />);
    expect(screen.getByText("No credentials configured")).toBeTruthy();
  });

  it("requires both a name and a value", async () => {
    render(<CredentialsPanel {...base} />);
    await userEvent.click(screen.getByRole("button", { name: /add/i }));
    expect(screen.getByText("Name and value are required")).toBeTruthy();
  });

  it("submits a trimmed name and clears the form", async () => {
    const onCreate = vi.fn().mockResolvedValue(undefined);
    render(<CredentialsPanel {...base} onCreate={onCreate} />);
    await userEvent.type(screen.getByLabelText("Stack credential name"), "  KEY  ");
    await userEvent.type(screen.getByLabelText("Stack credential value"), "secret");
    await userEvent.click(screen.getByRole("button", { name: /add/i }));
    expect(onCreate).toHaveBeenCalledWith("KEY", "secret");
  });
});
```

- [ ] **Step 2: Run it against the unmigrated component**

Run: `npx vitest run src/features/stacks/CredentialsPanel.test.tsx`
Expected: PASS

- [ ] **Step 3: Commit the test alone**

```bash
git add src/features/stacks/CredentialsPanel.test.tsx
git commit -m "test(web): characterize CredentialsPanel before layout migration"
```

- [ ] **Step 4: Migrate the markup**

In `CredentialsPanel.tsx`, import `{ Rows, Stack } from "../../shared/layout"` and replace:

```tsx
<Stack className="credentials-list" gap={2}>
...
<Rows className="credential-row" key={credential.id} lead={1} trailing={2} min={160} gap={3}>
...
<Rows className="credential-form" lead={2} trailing={1} min={160} gap={4} align="end">
```

- [ ] **Step 5: Strip layout from the CSS**

```css
.credentials-list {
  margin: var(--space-4) 0;
}

.credential-row {
  border: 1px solid var(--color-border);
  border-radius: var(--radius-lg);
  padding: var(--space-3) var(--space-4);
  font-family: var(--font-mono);
  font-size: var(--text-sm);
}
```

Delete `.credential-form` entirely — it was layout only.

- [ ] **Step 6: Verify**

Run: `npx vitest run src/features/stacks/CredentialsPanel.test.tsx && npx tsc -b && npm test`
Expected: PASS

Then use the `web:verify` skill on the Environment tab and confirm the credential rows and the add-credential form still align.

- [ ] **Step 7: Commit**

```bash
git add src/features/stacks/CredentialsPanel.tsx src/styles/features.css
git commit -m "refactor(web): move credential rows onto the Rows primitive"
```

---

### Task 10: Canary checkpoint

**Files:** none — this is a review gate, not a code change.

- [ ] **Step 1: Assess whether the vocabulary held**

Answer explicitly, in writing, before continuing:

1. Did `Fields` and `Rows` express their call sites without new props beyond those in Tasks 2–4?
2. Did any migration need an escape hatch, an inline `style`, or a bespoke layout class?
3. Did the `credential-form` 180→160 snap change anything perceptible?

- [ ] **Step 2: Stop if the answer to 1 or 2 is unfavourable**

If either primitive needed widening, halt and revise the spec. Per the spec's stated risk, fold the primitive back rather than adding props. Do not proceed to Task 11 with a vocabulary that has already started to leak.

- [ ] **Step 3: Report to the human and get approval to continue**

---

### Task 11: Remaining `shared/` and `app/` components

**Files:**
- Modify: `web/src/shared/StatBand.tsx`, `web/src/shared/StatusRow.tsx`, `web/src/shared/HeroGraphic.tsx`
- Modify: `web/src/app/NotFound.tsx`, `web/src/app/AccessDenied.tsx`, `web/src/app/ServiceUnavailable.tsx`, `web/src/app/RoutePlaceholder.tsx`
- Modify: `web/src/styles/primitives.css` (`.stat-band__grid`, `.stat-band__item`, `.status-row`)

**Interfaces:**
- Consumes: all six primitives.
- Produces: nothing new.

Apply the **Migration Procedure**. Specific conversions:

- `.stat-band__grid`: `repeat(2, minmax(0,1fr))` with a 768px query widening to `repeat(4, …)` → `<Grid cols={4} collapse="md" gap={6}>`. Note this changes the sub-768px rendering from two columns to one; if two columns must be preserved below 768px, keep `<Grid cols={2}>` and add a second `Grid cols={4}` above the breakpoint rather than widening `collapse`.
- `.stat-band__item`: `display:flex; flex-direction:column-reverse; gap:var(--space-1)` → this is the one `column-reverse` in the codebase. `Stack` does not express reversal. Swap the `<dt>`/`<dd>` order in the markup so the visual result matches with a plain `<Stack gap={1}>`, keeping `<dt>` before `<dd>` in the DOM for correct definition-list semantics.
- `.status-row`: `display:flex; gap:var(--space-3)` plus `justify-content:space-between` → `<Row className="status-row" gap={3} justify="between">`.

- [ ] **Step 1: Migrate each component per the Migration Procedure**
- [ ] **Step 2: Run the suite**

Run: `npm test && npx tsc -b`
Expected: PASS, including `StatBand.test.tsx` and the four `app/` tests

- [ ] **Step 3: Verify visually**

Use the `web:verify` skill on a stack detail screen (stat band, status rows) and each of the four error screens.

- [ ] **Step 4: Commit**

```bash
git add src/shared src/app src/styles/primitives.css
git commit -m "refactor(web): move shared and app-frame layout onto the primitives"
```

---

### Task 12: `features/stacks` — list and detail screens

**Files:**
- Modify: `web/src/features/stacks/StacksListScreen.tsx`, `StackDetailShell.tsx`, `StackPanel.tsx`, `StackTemplateScreen.tsx`
- Modify: `web/src/styles/features.css` — the `Stacks list`, `Tabs`, and `Layout grids` sections

**Interfaces:**
- Consumes: all six primitives.
- Produces: nothing new.

Apply the **Migration Procedure**. Specific conversions:

- `.workflow-grid`: `minmax(280px, 0.85fr) minmax(320px, 1.15fr)` → `<Split ratio="wide-right" gap={6} collapse="lg">`. This is a deliberate ratio change from 0.85/1.15 to 2fr/3fr.
- `.stack-template-right-column`: a single-use layout class → `<Stack gap={4}>`, and delete the class.
- The `1.1fr 0.9fr` grid inside the 900px query → `<Split ratio="even" collapse="lg">`.
- `repeat(auto-fit, minmax(200px, 1fr))` → `<Grid min={200}>`.

- [ ] **Step 1: Migrate each component per the Migration Procedure**
- [ ] **Step 2: Run the suite**

Run: `npm test && npx tsc -b`
Expected: PASS, including the 19.6K `StackTemplateScreen.test.tsx`

- [ ] **Step 3: Verify visually**

Use the `web:verify` skill on the stacks list and the stack template screen, checking the two-column workflow layout at wide and narrow widths.

- [ ] **Step 4: Commit**

```bash
git add src/features/stacks src/styles/features.css
git commit -m "refactor(web): move stack list and detail layout onto the primitives"
```

---

### Task 13: `features/stacks` — forms and panels

**Files:**
- Modify: `web/src/features/stacks/CreateStackScreen.tsx`, `AddStackTemplateScreen.tsx`, `UpgradeStackTemplateScreen.tsx`, `EnvironmentScreen.tsx`, `StackAccessScreen.tsx`
- Modify: `web/src/styles/features.css` — the `Grants` and `User search` sections

**Interfaces:**
- Consumes: all six primitives.
- Produces: nothing new.

Apply the **Migration Procedure**. Specific conversions:

- `repeat(2, minmax(140px, 1fr))` → `<Grid cols={2} min={140}>`.
- The grants list rows → `<Row justify="between" gap={3}>`.
- Role badge rows → `<Row gap={2} wrap>`.

- [ ] **Step 1: Migrate each component per the Migration Procedure**
- [ ] **Step 2: Run the suite**

Run: `npm test && npx tsc -b`
Expected: PASS, including `StackAccessScreen.test.tsx`, `CreateStackScreen.test.tsx`, `AddStackTemplateScreen.test.tsx`, `UpgradeStackTemplateScreen.test.tsx`

- [ ] **Step 3: Verify visually**

Use the `web:verify` skill on the create-stack, add-template, and access screens.

- [ ] **Step 4: Commit**

```bash
git add src/features/stacks src/styles/features.css
git commit -m "refactor(web): move stack form and panel layout onto the primitives"
```

---

### Task 14: `features/stacks` — remaining panels

**Files:**
- Modify: `web/src/features/stacks/InstalledTemplatePanel.tsx`, `StackTemplateConfigPanel.tsx`, `VariablesPanel.tsx`, `VariableFields.tsx`
- Modify: `web/src/styles/features.css` — the `Metadata grids` section

**Interfaces:**
- Consumes: all six primitives.
- Produces: nothing new.

Apply the **Migration Procedure**. Specific conversions:

- `.revision-grid`: `max-content minmax(0,1fr)` with `gap: var(--space-2) var(--space-4)` → `<Fields gap={2} columnGap={4}>`, keeping `margin: 0`.
- `repeat(2, minmax(160px, 1fr))` → `<Grid cols={2} min={160}>`.

- [ ] **Step 1: Migrate each component per the Migration Procedure**
- [ ] **Step 2: Run the suite**

Run: `npm test && npx tsc -b`
Expected: PASS, including `StackTemplateConfigPanel.test.tsx`

- [ ] **Step 3: Verify visually**

Use the `web:verify` skill on the installed-template and variables panels.

- [ ] **Step 4: Commit**

```bash
git add src/features/stacks src/styles/features.css
git commit -m "refactor(web): move remaining stack panel layout onto the primitives"
```

---

### Task 15: `features/runs`

**Files:**
- Modify: `web/src/features/runs/RunDetailScreen.tsx`, `RunLogsPanel.tsx`, `RunsPanel.tsx`, `TemplateDestroyPanel.tsx`, `TemplateRunActions.tsx`, `TemplateRunHistory.tsx`
- Modify: `web/src/styles/features.css` — the `Log panel`, `Inline-style drain (Task 8)`, and `Inline runs on the stack template screen` sections

**Interfaces:**
- Consumes: all six primitives.
- Produces: nothing new.

Apply the **Migration Procedure**. The `Log panel` section carries a comment explaining it deliberately has no texture — preserve that comment and every decorative rule; only layout moves.

- [ ] **Step 1: Migrate each component per the Migration Procedure**
- [ ] **Step 2: Run the suite**

Run: `npm test && npx tsc -b`
Expected: PASS

- [ ] **Step 3: Verify visually**

Use the `web:verify` skill on a run detail screen with logs, and on the inline runs list on the stack template screen.

- [ ] **Step 4: Commit**

```bash
git add src/features/runs src/styles/features.css
git commit -m "refactor(web): move run screen layout onto the primitives"
```

---

### Task 16: `features/templates`

**Files:**
- Modify: `web/src/features/templates/TemplateRegistrationScreen.tsx`, `TemplateRegistryPanel.tsx`, `TemplateRegistryScreen.tsx`
- Modify: `web/src/styles/features.css` — the `Templates list` and `Undo banner` sections

**Interfaces:**
- Consumes: all six primitives.
- Produces: nothing new.

Apply the **Migration Procedure**. The undo banner's `justify-content: space-between` row → `<Row justify="between" gap={4}>`.

- [ ] **Step 1: Migrate each component per the Migration Procedure**
- [ ] **Step 2: Run the suite**

Run: `npm test && npx tsc -b`
Expected: PASS

- [ ] **Step 3: Verify visually**

Use the `web:verify` skill on the template registry screen, triggering the undo banner.

- [ ] **Step 4: Commit**

```bash
git add src/features/templates src/styles/features.css
git commit -m "refactor(web): move template screen layout onto the primitives"
```

---

### Task 17: Normalize the breakpoints

**Files:**
- Modify: `web/src/styles/features.css:861` (`max-width: 920px`), `:869` (`max-width: 760px`)

**Interfaces:**
- Consumes: nothing.
- Produces: a codebase whose only width queries are 768px and 900px.

By this point most collapse behavior lives in `collapse` props. Any remaining `max-width` query must be rewritten mobile-first onto an approved `min-width`.

**Deliberate visual change:** the 920px collapse point moves to 900px and the 760px point moves to 768px.

- [ ] **Step 1: Rewrite each remaining query**

Convert `@media (max-width: 920px) { .x { grid-template-columns: 1fr; } }` into a default single-column rule plus `@media (min-width: 900px) { .x { grid-template-columns: var(--tracks); } }`, or delete it if a `collapse` prop already covers the element.

- [ ] **Step 2: Run the guard test**

Run: `npx vitest run src/styles/styles.guard.test.ts`
Expected: PASS — the breakpoint rule no longer needs the allowlist for these files

- [ ] **Step 3: Verify visually**

Use the `web:verify` skill at viewport widths 759, 768, 899, and 901 px, confirming each collapse fires at the intended point.

- [ ] **Step 4: Commit**

```bash
git add src/styles/features.css
git commit -m "refactor(web): normalize breakpoints onto 768px and 900px"
```

---

### Task 18: Delete the allowlist and close the loop

**Files:**
- Modify: `web/src/styles/styles.guard.test.ts` (delete `LAYOUT_ALLOWLIST` and its guards)
- Modify: `web/src/styles/features.css` (delete dead layout-only classes)

**Interfaces:**
- Consumes: Tasks 8–17 complete.
- Produces: rules 2–4 as unconditional failures.

- [ ] **Step 1: Delete the allowlist**

Remove the `LAYOUT_ALLOWLIST` declaration and the two `if (LAYOUT_ALLOWLIST.has(name)) return;` early returns from `styles.guard.test.ts`.

- [ ] **Step 2: Run the test to find what is left**

Run: `npx vitest run src/styles/styles.guard.test.ts`
Expected: FAIL, listing any layout declarations still in `features.css`

- [ ] **Step 3: Migrate or delete each remaining declaration**

Every failure is either a missed call site (migrate it per the Migration Procedure) or a class with no remaining callers (delete it). Confirm the latter with `grep -rn "class-name" src --include="*.tsx"` before deleting.

- [ ] **Step 4: Run the test to verify it passes**

Run: `npx vitest run src/styles/styles.guard.test.ts && npm test && npm run build`
Expected: PASS

- [ ] **Step 5: Confirm the invariant holds**

Run: `grep -cE "display: *(grid|flex)|grid-template-columns" src/styles/features.css`
Expected: `0`

- [ ] **Step 6: Verify the whole app**

Use the `web:verify` skill across every migrated screen one final time.

- [ ] **Step 7: Commit**

```bash
git add src/styles/styles.guard.test.ts src/styles/features.css
git commit -m "feat(web): make layout outside primitives.css a hard failure"
```

---

## Deliberate visual changes

Every intended rendering change, collected so review can confirm each was wanted:

| Task | Change |
|---|---|
| 6 | `padding: 0.6rem 0.8rem` → `8px 12px`; `0.15rem 0` → `4px 0` |
| 9 | `credential-form` second track floor 180px → 160px |
| 11 | `stat-band` sub-768px rendering, if `collapse="md"` is used |
| 12 | `workflow-grid` ratio 0.85/1.15 → 2fr/3fr; `1.1fr 0.9fr` → 1fr/1fr |
| 17 | collapse points 920px → 900px and 760px → 768px |
