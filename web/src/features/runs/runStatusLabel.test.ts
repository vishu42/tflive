import { describe, expect, it } from "vitest";
import type { TemplateRun } from "../../api/types";
import { runStatusLabel } from "./runStatusLabel";

const counts = { add: 1, change: 0, destroy: 0 };

function label(operation: TemplateRun["operation"], status: TemplateRun["status"], planned: boolean): string {
  return runStatusLabel({ operation, status, plan_summary: planned ? counts : null });
}

describe("runStatusLabel", () => {
  it.each([
    ["plan", "plan_started", false, "Planning"],
    ["destroy", "queued", false, "Planning destroy"],
    ["plan", "waiting_approval", true, "Planned"],
    ["destroy", "waiting_approval", true, "Destroy planned"],
    ["plan", "approved", true, "Approved"],
    ["destroy", "approved", true, "Destroy approved"],
    ["plan", "apply_started", true, "Applying"],
    ["destroy", "locked", true, "Destroying"],
    ["plan", "completed", false, "No changes"],
    ["destroy", "completed", false, "Nothing to destroy"],
    ["plan", "completed", true, "Applied"],
    ["destroy", "completed", true, "Destroyed"],
    ["plan", "failed", false, "Plan failed"],
    ["destroy", "failed", false, "Destroy plan failed"],
    ["plan", "failed", true, "Apply failed"],
    ["destroy", "failed", true, "Destroy failed"],
    ["plan", "canceling", true, "Canceling"],
    ["destroy", "canceling", true, "Canceling destroy"],
    ["plan", "canceled", true, "Canceled"],
    ["destroy", "canceled", false, "Destroy canceled"]
  ] as const)("%s %s (planned: %s) reads %s", (operation, status, planned, expected) => {
    expect(label(operation, status, planned)).toBe(expected);
  });
});
