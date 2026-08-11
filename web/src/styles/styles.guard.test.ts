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
