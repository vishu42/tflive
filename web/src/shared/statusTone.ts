/**
 * The API exposes three separate status unions (TemplateRunStatus with 21
 * values, TemplateRegistrationStatus with 5, TemplateRevisionStatus with 4).
 * Rendering 30 bespoke treatments would be unmaintainable, so every status
 * maps onto one of five visual tones.
 */
export type StatusTone = "settled" | "progress" | "waiting" | "failed" | "canceled";

const FAILED = new Set(["failed", "invalid", "error"]);
const CANCELED = new Set(["canceled", "cancel_requested"]);
const WAITING = new Set([
  "pending",
  "pending_validation",
  "queued",
  "locked",
  "waiting_approval"
]);
const PROGRESS = new Set(["running", "validating", "canceling"]);

export function statusTone(value: string): StatusTone {
  if (FAILED.has(value)) return "failed";
  if (CANCELED.has(value)) return "canceled";
  if (WAITING.has(value)) return "waiting";
  if (PROGRESS.has(value)) return "progress";

  // Phase statuses arrive as `<phase>_started` / `<phase>_finished`.
  if (value.endsWith("_started")) return "progress";

  // Callers pass human phrases such as "not configured" for absent resources.
  if (value.startsWith("not ")) return "waiting";

  // Finished phases and intermediate pipeline steps are settled. This is a
  // deliberate behaviour change from the previous implementation, which
  // over-broadly treated finished phases (e.g. workspace_prepared,
  // source_fetched, init_finished, workspace_selected, plan_finished,
  // approved, apply_finished, destroy_finished, lock_released) as
  // in-progress and rendered a spinner for them instead of the settled icon.
  return "settled";
}

const GLYPHS: Record<StatusTone, string> = {
  settled: "●",
  progress: "◐",
  waiting: "○",
  failed: "✕",
  canceled: "⊘"
};

export function statusGlyph(tone: StatusTone): string {
  return GLYPHS[tone];
}
