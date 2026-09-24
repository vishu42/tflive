/**
 * The API exposes three separate status unions (TemplateRunStatus with 7
 * values, TemplateRegistrationStatus with 5, TemplateRevisionStatus with 4).
 * Every status maps onto one of five visual tones.
 */
export type StatusTone = "settled" | "progress" | "waiting" | "failed" | "canceled";

const FAILED = new Set(["failed", "invalid", "error"]);
const CANCELED = new Set(["canceled"]);
const WAITING = new Set(["pending", "pending_validation", "queued", "waiting_approval"]);
const PROGRESS = new Set(["running", "validating"]);

export function statusTone(value: string): StatusTone {
  if (FAILED.has(value)) return "failed";
  if (CANCELED.has(value)) return "canceled";
  if (WAITING.has(value)) return "waiting";
  if (PROGRESS.has(value)) return "progress";

  // Callers pass human phrases such as "not configured" for absent resources.
  if (value.startsWith("not ")) return "waiting";

  // Completed, active, approved, and anything unrecognised are settled.
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
