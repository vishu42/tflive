import { readdirSync, readFileSync } from "node:fs";
import { dirname, join, relative } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const SRC_DIR = join(dirname(fileURLToPath(import.meta.url)), "..");

function componentFiles(dir: string): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) return componentFiles(path);
    return entry.name.endsWith(".tsx") && !entry.name.includes(".test.") ? [path] : [];
  });
}

// Every <table> is a .data-table (primitives.css): fixed layout with its
// column widths declared in a <colgroup>, inside a .data-table-frame. Left to
// automatic layout, a table re-sizes its columns whenever a cell's content
// changes, so a status that moves from queued to waiting_approval shifts
// every column after it.
describe("tables", () => {
  const files = componentFiles(SRC_DIR).map((path) => ({ name: relative(SRC_DIR, path), source: readFileSync(path, "utf8") }));

  it("finds the tables it guards", () => {
    expect(files.some(({ source }) => source.includes("<table"))).toBe(true);
  });

  it("builds every table on .data-table", () => {
    const violations: string[] = [];
    for (const { name, source } of files) {
      const tables = [...source.matchAll(/<table\b([^>]*)>/g)];
      for (const [tag, attributes] of tables) {
        if (!/className=[{"][^>]*\bdata-table\b/.test(attributes)) violations.push(`${name}: ${tag} lacks the data-table class`);
      }
      const colgroups = source.match(/<colgroup>/g)?.length ?? 0;
      if (colgroups < tables.length) violations.push(`${name}: ${tables.length} table(s) but ${colgroups} <colgroup>`);
      const frames = source.match(/\bdata-table-frame\b/g)?.length ?? 0;
      if (frames < tables.length) violations.push(`${name}: ${tables.length} table(s) but ${frames} data-table-frame`);
    }
    expect(violations, violations.join("\n")).toEqual([]);
  });
});
