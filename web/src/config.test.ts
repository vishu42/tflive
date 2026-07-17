import { describe, expect, it } from "vitest";
import { resolveTenantID } from "./config";

describe("tenant configuration", () => {
  it("trims and returns an explicitly configured tenant", () => {
    expect(resolveTenantID("  tenant_prod-1  ", false)).toBe("tenant_prod-1");
  });

  it("uses tenant_123 only for missing local-development configuration", () => {
    expect(resolveTenantID(undefined, true)).toBe("tenant_123");
    expect(resolveTenantID("   ", true)).toBe("tenant_123");
  });

  it("requires an explicit production tenant", () => {
    expect(() => resolveTenantID(undefined, false)).toThrow("VITE_TFLIVE_TENANT_ID is required");
    expect(() => resolveTenantID("   ", false)).toThrow("VITE_TFLIVE_TENANT_ID is required");
  });

  it.each(["-tenant", "tenant/value", "tenant value", "tenant!", "a".repeat(129)])(
    "rejects malformed tenant %s",
    (value) => {
      expect(() => resolveTenantID(value, false)).toThrow("VITE_TFLIVE_TENANT_ID must start");
    }
  );
});
