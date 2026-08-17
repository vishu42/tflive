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
    // The text colours must remain darker than the brighter dot/fill variants.
    expect(tokens).toMatch(/--color-success:\s*#166534/i);
    expect(tokens).toMatch(/--color-warning:\s*#92400E/i);
    expect(tokens).toMatch(/--color-danger:\s*#B91C1C/i);
    expect(tokens).toMatch(/--color-muted-fg:\s*#475569/i);
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

/** A declaration block: its selector and its properties. Comments are already
    stripped by read(), so a value mentioning px in prose cannot trip these. */
function parseRules(css: string): { selector: string; decl: Record<string, string> }[] {
  const parsed: { selector: string; decl: Record<string, string> }[] = [];
  for (const match of css.matchAll(/([^{}]+)\{([^{}]*)\}/g)) {
    const selector = match[1].trim().replace(/\s+/g, " ");
    // At-rule preludes (@media, @supports) wrap rules rather than declaring any.
    if (!selector || selector.startsWith("@")) continue;
    const decl: Record<string, string> = {};
    for (const part of match[2].split(";")) {
      const colon = part.indexOf(":");
      if (colon === -1) continue;
      decl[part.slice(0, colon).trim()] = part.slice(colon + 1).trim();
    }
    parsed.push({ selector, decl });
  }
  return parsed;
}

const SCALES: Record<string, Record<number, string>> = {
  "font-size": { 12: "--text-xs", 14: "--text-sm", 16: "--text-base", 18: "--text-lg",
                 20: "--text-xl", 24: "--text-2xl", 32: "--text-3xl", 40: "--text-4xl",
                 52: "--text-5xl", 64: "--text-6xl", 84: "--text-7xl" },
  "border-radius": { 4: "--radius-sm", 6: "--radius-md", 8: "--radius-lg",
                     12: "--radius-xl", 9999: "--radius-full" },
  spacing: { 4: "--space-1", 8: "--space-2", 12: "--space-3", 16: "--space-4",
             20: "--space-5", 24: "--space-6", 32: "--space-8", 40: "--space-10",
             48: "--space-12", 64: "--space-16", 96: "--space-24" }
};

const SIZED_PROPS = ["font-size", "border-radius", "gap", "padding", "margin",
                     "min-height", "min-width",
                     "padding-top", "padding-bottom", "padding-left", "padding-right"];

/** Deliberately off-scale sizes. Each carries its reason so that silencing a
    finding stays a decision rather than a habit. */
const OFF_SCALE_ALLOWED = new Set([
  ".log-panel pre|min-height",          // log viewport; arbitrary scroll height, no token fits
  ".role-badge|min-width",              // sized to the longest role label so the pills form a column
  "body|min-width"                      // minimum supported viewport width, not a spacing value
]);

/** Geist tightens tracking in three tiers as type grows: -0.06em at 40px and
    above, -0.04em from 24 to 32px, -0.02em below that. */
function expectedTracking(px: number): string {
  if (px >= 40) return "--tracking-tightest";
  if (px >= 24) return "--tracking-tighter";
  return "--tracking-tight";
}

describe.each(convertedStylesheets())("%s token discipline", (name) => {
  const parsed = parseRules(read(name));

  it("expresses every size as a token", () => {
    const violations: string[] = [];
    for (const { selector, decl } of parsed) {
      for (const prop of SIZED_PROPS) {
        const value = decl[prop];
        if (!value || value.includes("var(")) continue;
        if (OFF_SCALE_ALLOWED.has(`${selector}|${prop}`)) continue;
        for (const [, num, unit] of value.matchAll(/(-?\d*\.?\d+)(rem|px)/g)) {
          const px = parseFloat(num) * (unit === "rem" ? 16 : 1);
          // 0 resets, 1px hairlines and 2px rings are not scale values.
          if (px === 0 || px === 1 || px === 2) continue;
          const scale = SCALES[prop] ?? SCALES.spacing;
          const token = scale[px];
          violations.push(
            `${selector} { ${prop}: ${value} } -> ${px}px, ` +
              (token ? `use var(${token})` : "off-scale")
          );
        }
      }
    }
    expect(violations, violations.join("\n")).toEqual([]);
  });

  // --control-height only governs when nothing else sets the height. Add vertical
  // padding and the line box plus padding overshoots it, so the token goes inert
  // and the control silently keeps its old size. This has bitten buttons, inputs,
  // the app header and .runtime-value.
  it("never defeats a height token with vertical padding", () => {
    const violations: string[] = [];
    for (const { selector, decl } of parsed) {
      const heightToken = ["--control-height", "--touch-target"].find((t) =>
        decl["min-height"]?.includes(t)
      );
      if (!heightToken) continue;
      const shorthand = decl.padding?.split(/\s+/)[0];
      const vertical = [shorthand, decl["padding-top"], decl["padding-bottom"]].find(
        (v) => v && v !== "0" && v !== "0px"
      );
      if (vertical) {
        violations.push(`${selector} { min-height: var(${heightToken}); padding: ${vertical} ... }`);
      }
    }
    expect(violations, `set vertical padding to 0 so the token governs:\n${violations.join("\n")}`)
      .toEqual([]);
  });

  it("matches letter-spacing to the size tier", () => {
    const violations: string[] = [];
    for (const { selector, decl } of parsed) {
      const size = decl["font-size"];
      const spacing = decl["letter-spacing"];
      if (!size?.includes("--text-") || !spacing?.includes("--tracking-tight")) continue;
      const token = size.match(/--text-[\w-]+/)?.[0];
      const px = Object.entries(SCALES["font-size"]).find(([, t]) => t === token)?.[0];
      if (!px) continue;
      const want = expectedTracking(Number(px));
      const have = spacing.match(/--tracking-[\w-]+/)?.[0];
      if (have !== want) {
        violations.push(`${selector} { ${token} is ${px}px } has ${have}, wants ${want}`);
      }
    }
    expect(violations, violations.join("\n")).toEqual([]);
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
