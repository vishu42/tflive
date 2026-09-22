import { describe, expect, it } from "vitest";
import type { TemplateRun } from "../../api/types";
import { runStatusLabel } from "./runStatusLabel";

const counts = { add: 1, change: 0, destroy: 0 };

function label(operation: TemplateRun["operation"], status: TemplateRun["status"], planned: boolean, autoApprove = false): string {
  return runStatusLabel({ operation, status, plan_summary: planned ? counts : null, auto_approve: autoApprove });
}

describe("runStatusLabel", () => {
  it.each([
    ["apply", "plan_started", false, "Planning"],
    ["destroy", "queued", false, "Planning destroy"],
    ["apply", "waiting_approval", true, "Planned"],
    ["destroy", "waiting_approval", true, "Destroy planned"],
    ["apply", "approved", true, "Approved"],
    ["destroy", "approved", true, "Destroy approved"],
    ["apply", "apply_started", true, "Applying"],
    ["destroy", "locked", true, "Destroying"],
    ["apply", "completed", false, "No changes"],
    ["destroy", "completed", false, "Nothing to destroy"],
    ["apply", "completed", true, "Applied"],
    ["destroy", "completed", true, "Destroyed"],
    ["apply", "failed", false, "Plan failed"],
    ["destroy", "failed", false, "Destroy plan failed"],
    ["apply", "failed", true, "Apply failed"],
    ["destroy", "failed", true, "Destroy failed"],
    ["apply", "canceling", true, "Canceling"],
    ["destroy", "canceling", true, "Canceling destroy"],
    ["apply", "canceled", true, "Canceled"],
    ["destroy", "canceled", false, "Destroy canceled"]
  ] as const)("%s %s (planned: %s) reads %s", (operation, status, planned, expected) => {
    expect(label(operation, status, planned)).toBe(expected);
  });

  // A plan run applies nothing, so no label of its says it did, even once
  // its plan has counts.
  it.each([
    ["queued", false, "Planning"],
    ["plan_finished", true, "Planning"],
    ["completed", true, "Plan finished"],
    ["completed", false, "No changes"],
    ["failed", true, "Plan failed"],
    ["canceling", false, "Canceling"],
    ["canceled", false, "Canceled"]
  ] as const)("plan run %s (planned: %s) reads %s", (status, planned, expected) => {
    expect(label("plan", status, planned)).toBe(expected);
  });

  // An auto-approved apply never plans on its own, so it is applying from the
  // start, before it has any counts.
  it.each([
    ["queued", false, "Applying"],
    ["init_started", false, "Applying"],
    ["completed", true, "Applied"],
    ["failed", false, "Apply failed"],
    ["canceled", false, "Canceled"]
  ] as const)("auto-approved apply %s (counts: %s) reads %s", (status, planned, expected) => {
    expect(label("apply", status, planned, true)).toBe(expected);
  });
});
