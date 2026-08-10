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
