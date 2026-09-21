import { isTerminalRunStatus } from "../../api/polling";
import { useTemplateRunsQuery } from "../../api/queries";
import type { TemplateRun } from "../../api/types";
import { tenantID } from "../../config";

// useRunInFlight returns the template's unfinished run, if it has one. A run
// snapshots desired state when it starts, so config and revision cannot change
// until it finishes or is discarded; the server refuses, and the screens that
// edit them say why before anyone tries.
export function useRunInFlight(stackTemplateId: string): TemplateRun | null {
  const runs = useTemplateRunsQuery(tenantID, stackTemplateId).data ?? [];
  return runs.find((run) => !isTerminalRunStatus(run.status)) ?? null;
}

// runInFlightReason is the sentence a locked control shows.
export function runInFlightReason(run: TemplateRun, change: string): string {
  const action = run.status === "waiting_approval" ? "Apply or discard" : "Wait for";
  return `${action} run #${run.run_number} before ${change}`;
}
