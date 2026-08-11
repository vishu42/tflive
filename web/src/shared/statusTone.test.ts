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

  it("distinguishes a requested cancellation from a completed cancellation", () => {
    expect(statusTone("canceled")).toBe("canceled");
    expect(statusTone("cancel_requested")).toBe("progress");
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
