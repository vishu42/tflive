import { describe, expect, it } from "vitest";
import { statusGlyph, statusTone } from "./statusTone";

describe("statusTone", () => {
  it("classifies terminal success states as settled", () => {
    expect(statusTone("completed")).toBe("settled");
    expect(statusTone("active")).toBe("settled");
    expect(statusTone("approved")).toBe("settled");
  });

  it("classifies active work as progress", () => {
    expect(statusTone("running")).toBe("progress");
    expect(statusTone("validating")).toBe("progress");
  });

  it("classifies queued and blocked states as waiting", () => {
    expect(statusTone("pending")).toBe("waiting");
    expect(statusTone("pending_validation")).toBe("waiting");
    expect(statusTone("queued")).toBe("waiting");
    expect(statusTone("waiting_approval")).toBe("waiting");
  });

  it("classifies failure states as failed", () => {
    expect(statusTone("failed")).toBe("failed");
    expect(statusTone("invalid")).toBe("failed");
    expect(statusTone("error")).toBe("failed");
  });

  it("classifies a discarded run, which ends canceled, as canceled", () => {
    expect(statusTone("canceled")).toBe("canceled");
  });

  it("treats 'not ...' phrasing used by StatusRow callers as waiting", () => {
    expect(statusTone("not configured")).toBe("waiting");
    expect(statusTone("not installed")).toBe("waiting");
  });

  it("falls back to settled for unrecognised values", () => {
    expect(statusTone("something_new")).toBe("settled");
    expect(statusTone("plan_started")).toBe("settled");
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
