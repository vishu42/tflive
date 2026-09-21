import type { TemplateRun } from "../../api/types";

// runStatusLabel says where a run is in plan -> approve -> apply, in words,
// with the operation folded in so no separate Type is needed: "Destroy
// planned" rather than destroy + waiting_approval. Every destroy label says
// destroy, since the label is the only place either screen names the
// operation. The raw status is still what the tone and glyph come from.
//
// A run has plan counts only once a plan with changes has finished, so a run
// that has them and is still working is applying, and one that failed with
// them failed applying.
export function runStatusLabel(run: Pick<TemplateRun, "operation" | "status" | "plan_summary">): string {
  const destroy = run.operation === "destroy";
  const planned = run.plan_summary !== null && run.plan_summary !== undefined;

  switch (run.status) {
    case "waiting_approval":
      return destroy ? "Destroy planned" : "Planned";
    case "approved":
      return destroy ? "Destroy approved" : "Approved";
    case "completed":
      if (!planned) {
        return destroy ? "Nothing to destroy" : "No changes";
      }
      return destroy ? "Destroyed" : "Applied";
    case "failed":
      if (!planned) {
        return destroy ? "Destroy plan failed" : "Plan failed";
      }
      return destroy ? "Destroy failed" : "Apply failed";
    case "canceled":
      return destroy ? "Destroy canceled" : "Canceled";
    case "cancel_requested":
    case "canceling":
      return destroy ? "Canceling destroy" : "Canceling";
    default:
      if (!planned) {
        return destroy ? "Planning destroy" : "Planning";
      }
      return destroy ? "Destroying" : "Applying";
  }
}
