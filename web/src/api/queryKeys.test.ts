import { describe, expect, it } from "vitest";
import { queryKeys } from "./queryKeys";

describe("queryKeys", () => {
  it("builds tenant-scoped list keys", () => {
    expect(queryKeys.stacks("tenant_123")).toEqual(["stacks", "tenant_123"]);
    expect(queryKeys.templateRevisions("tenant_123")).toEqual(["templateRevisions", "tenant_123"]);
  });

  it("builds resource detail keys", () => {
    expect(queryKeys.stack("tenant_123", "stack_1")).toEqual(["stack", "tenant_123", "stack_1"]);
    expect(queryKeys.templateRegistration("tenant_123", "reg_1")).toEqual(["templateRegistration", "tenant_123", "reg_1"]);
    expect(queryKeys.templateRevisionVariables("tenant_123", "rev_1")).toEqual(["templateRevisionVariables", "tenant_123", "rev_1"]);
    expect(queryKeys.templateRun("tenant_123", "run_1")).toEqual(["templateRun", "tenant_123", "run_1"]);
  });

  it("builds status-tagged log keys so a status change produces a new key", () => {
    expect(queryKeys.templateRunLogs("tenant_123", "run_1", "planned")).toEqual([
      "templateRunLogs", "tenant_123", "run_1", "planned"
    ]);
    expect(queryKeys.templateRunLogs("tenant_123", "run_1", "completed")).not.toEqual(
      queryKeys.templateRunLogs("tenant_123", "run_1", "planned")
    );
    expect(queryKeys.templateRunLog("tenant_123", "run_1", "plan", "planned")).toEqual([
      "templateRunLog", "tenant_123", "run_1", "plan", "planned"
    ]);
  });
});
