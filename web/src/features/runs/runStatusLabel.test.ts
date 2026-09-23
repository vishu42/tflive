import { describe, expect, it } from "vitest";
import type { TemplateRun } from "../../api/types";
import { runProgressTag, runStatusLabel } from "./runStatusLabel";

const counts = { add: 1, change: 0, destroy: 0 };

function label(
  operation: TemplateRun["operation"],
  status: TemplateRun["status"],
  planned: boolean,
  autoApprove = false,
  step: TemplateRun["step"] = ""
): string {
  return runStatusLabel({ operation, status, step, plan_summary: planned ? counts : null, auto_approve: autoApprove });
}

describe("runStatusLabel", () => {
  it.each([
    ["apply", "running", false, "Planning"],
    ["destroy", "queued", false, "Planning destroy"],
    ["apply", "waiting_approval", true, "Planned"],
    ["destroy", "waiting_approval", true, "Destroy planned"],
    ["apply", "approved", true, "Approved"],
    ["destroy", "approved", true, "Destroy approved"],
    ["apply", "running", true, "Applying"],
    ["destroy", "running", true, "Destroying"],
    ["apply", "completed", false, "No changes"],
    ["destroy", "completed", false, "Nothing to destroy"],
    ["apply", "completed", true, "Applied"],
    ["destroy", "completed", true, "Destroyed"],
    ["apply", "failed", false, "Plan failed"],
    ["destroy", "failed", false, "Destroy plan failed"],
    ["apply", "failed", true, "Apply failed"],
    ["destroy", "failed", true, "Destroy failed"],
    ["apply", "canceled", true, "Discarded"],
    ["destroy", "canceled", true, "Destroy discarded"]
  ] as const)("%s %s (planned: %s) reads %s", (operation, status, planned, expected) => {
    expect(label(operation, status, planned)).toBe(expected);
  });

  // A plan run applies nothing, so no label of its says it did, even once
  // its plan has counts.
  it.each([
    ["queued", false, "Planning"],
    ["running", true, "Planning"],
    ["completed", true, "Plan finished"],
    ["completed", false, "No changes"],
    ["failed", true, "Plan failed"]
  ] as const)("plan run %s (planned: %s) reads %s", (status, planned, expected) => {
    expect(label("plan", status, planned)).toBe(expected);
  });

  // An auto-approved apply never plans on its own, so it is applying from the
  // start, before it has any counts.
  it.each([
    ["queued", false, "Applying"],
    ["running", false, "Applying"],
    ["completed", true, "Applied"],
    ["failed", false, "Apply failed"],
    ["canceled", false, "Canceled"]
  ] as const)("auto-approved apply %s (counts: %s) reads %s", (status, planned, expected) => {
    expect(label("apply", status, planned, true)).toBe(expected);
  });

  // A running run says what it is doing; a failed one says what it was doing.
  // Planning and applying add nothing the headline does not already say.
  it.each([
    ["plan", "running", false, false, "fetching_source", "Planning · Fetching source"],
    ["apply", "running", true, false, "waiting_for_executor", "Applying · Waiting for an executor"],
    ["destroy", "running", true, false, "restoring_plan", "Destroying · Restoring saved plan"],
    ["apply", "running", false, true, "initializing", "Applying · Initializing"],
    ["apply", "running", false, false, "planning", "Planning"],
    ["apply", "running", true, false, "applying", "Applying"],
    ["apply", "failed", false, false, "fetching_source", "Plan failed while fetching source"],
    ["destroy", "failed", true, false, "waiting_for_executor", "Destroy failed while waiting for an executor"],
    ["apply", "failed", true, false, "applying", "Apply failed"],
    ["apply", "waiting_approval", true, false, "saving_plan", "Planned"],
    ["apply", "completed", true, false, "applying", "Applied"]
  ] as const)("%s %s (planned: %s, auto: %s) on %s reads %s", (operation, status, planned, autoApprove, step, expected) => {
    expect(label(operation, status, planned, autoApprove, step)).toBe(expected);
  });
});

// Logs are refetched when this tag changes. Status alone stays running for a
// whole run, so the step has to be part of it.
describe("runProgressTag", () => {
  it("changes when the step changes within one status", () => {
    expect(runProgressTag({ status: "running", step: "initializing" })).not.toBe(
      runProgressTag({ status: "running", step: "planning" })
    );
  });

  it("changes when the status changes on the same step", () => {
    expect(runProgressTag({ status: "running", step: "applying" })).not.toBe(
      runProgressTag({ status: "completed", step: "applying" })
    );
  });
});
