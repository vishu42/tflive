import type { TemplateRun, TemplateRunStep } from "../../api/types";

// runHeadline says where a run is, in words, with the operation folded in so
// no separate Type is needed: "Destroy planned" rather than destroy +
// waiting_approval. Every destroy label says destroy, since the label is the
// only place either screen names the operation. The raw status is still what
// the tone and glyph come from.
//
// A saved plan that someone discarded ends canceled, and reads as discarded;
// nothing else ends canceled, since a running run cannot be stopped.
//
// A plan run only ever plans, so it reads as planning until it ends. An
// auto-approved apply run never plans on its own, so it reads as applying
// throughout. Any other run has plan counts only once a plan with changes has
// finished, so one that has them and is still working is applying, and one
// that failed with them failed applying.
function runHeadline(run: Pick<TemplateRun, "operation" | "status" | "plan_summary" | "auto_approve">): string {
  if (run.operation === "plan") {
    return planRunStatusLabel(run);
  }
  if (run.auto_approve) {
    return autoApprovedRunStatusLabel(run);
  }

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
      return destroy ? "Destroy discarded" : "Discarded";
    default:
      if (!planned) {
        return destroy ? "Planning destroy" : "Planning";
      }
      return destroy ? "Destroying" : "Applying";
  }
}

function planRunStatusLabel(run: Pick<TemplateRun, "status" | "plan_summary">): string {
  switch (run.status) {
    case "completed":
      return run.plan_summary ? "Plan finished" : "No changes";
    case "failed":
      return "Plan failed";
    case "canceled":
      return "Canceled";
    default:
      return "Planning";
  }
}

function autoApprovedRunStatusLabel(run: Pick<TemplateRun, "status">): string {
  switch (run.status) {
    case "completed":
      return "Applied";
    case "failed":
      return "Apply failed";
    case "canceled":
      return "Canceled";
    default:
      return "Applying";
  }
}

const STEP_LABELS: Record<TemplateRunStep, string> = {
  waiting_for_executor: "Waiting for an executor",
  preparing_workspace: "Preparing workspace",
  fetching_source: "Fetching source",
  restoring_plan: "Restoring saved plan",
  initializing: "Initializing",
  selecting_workspace: "Selecting workspace",
  planning: "Planning",
  saving_plan: "Saving plan",
  applying: "Applying"
};

// Steps the headline already names: "Planning · Planning" says nothing.
const HEADLINE_STEPS = new Set<TemplateRunStep>(["planning", "applying"]);

// runStatusLabel says where a run is, in words. The headline comes from the
// status with the operation folded in, so no separate Type is needed:
// "Destroy planned" rather than destroy + waiting_approval. A running run
// adds the step it is on, and a failed one the step it failed on, so a slow
// clone reads as a clone and a failed one says so.
export function runStatusLabel(
  run: Pick<TemplateRun, "operation" | "status" | "step" | "plan_summary" | "auto_approve">
): string {
  const headline = runHeadline(run);
  if (run.step === "" || HEADLINE_STEPS.has(run.step)) {
    return headline;
  }
  const step = STEP_LABELS[run.step];
  if (run.status === "running") {
    return `${headline} · ${step}`;
  }
  if (run.status === "failed") {
    return `${headline} while ${step.charAt(0).toLowerCase()}${step.slice(1)}`;
  }
  return headline;
}

// runProgressTag changes whenever a run moves: a new status, or a new step
// within one. Queries that must refresh as a run progresses, such as its
// logs, key on it.
export function runProgressTag(run: Pick<TemplateRun, "status" | "step">): string {
  return `${run.status}:${run.step}`;
}
