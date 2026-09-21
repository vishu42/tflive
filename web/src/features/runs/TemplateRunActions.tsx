import { useEffect, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Loader2, Play } from "lucide-react";
import { isTerminalRunStatus } from "../../api/polling";
import { queryKeys } from "../../api/queryKeys";
import { useStartTemplateRunMutation, useTemplateRunsQuery } from "../../api/queries";
import type { StackTemplate } from "../../api/types";
import RequireCapability from "../../auth/RequireCapability";
import { tenantID } from "../../config";
import { isRunInFlightError } from "./runErrors";

interface TemplateRunActionsProps {
  stackId: string;
  stackTemplate: StackTemplate;
}

// The header of a template's Runs tab: its title and Plan. A plan with changes
// waits for approval on its own row, where approving it applies it; the row is
// where Apply and Discard live, so it is always plain which run they act on.
// Destroy lives on the Settings tab: see TemplateDestroyPanel.
//
// Run state is derived entirely from the server's run history
// (useTemplateRunsQuery), not from local component state, so it is visible to
// every user who can view the stack — not just the browser tab that started a
// run.
export default function TemplateRunActions({ stackId, stackTemplate }: TemplateRunActionsProps) {
  const [errorMessage, setErrorMessage] = useState("");
  const [autoApprove, setAutoApprove] = useState(false);
  const queryClient = useQueryClient();

  const runsQuery = useTemplateRunsQuery(tenantID, stackTemplate.id);
  const runs = runsQuery.data ?? [];
  const runsReady = runsQuery.status === "success";
  const latestRun = runsReady ? runs[0] ?? null : null;
  const activeRun = runsReady ? runs.find((candidate) => !isTerminalRunStatus(candidate.status)) ?? null : null;

  const startRunMutation = useStartTemplateRunMutation(tenantID);
  const canPlan = runsReady && !activeRun && stackTemplate.lifecycle === "active";

  // plan_state and live_state live on the stack template, but the thing that
  // changes them is a run finishing — and only the runs query polls. Without
  // this the page would keep rendering the state the template had when it
  // loaded.
  //
  // This effect owns the invalidation for the whole template page: the other
  // tabs read the same two queries, and this header is on the default tab.
  const settledRun = latestRun && isTerminalRunStatus(latestRun.status) ? `${latestRun.id}:${latestRun.status}` : "";
  const waitingRun = latestRun?.status === "waiting_approval" ? latestRun.id : "";
  useEffect(() => {
    if (settledRun === "" && waitingRun === "") {
      return;
    }
    void queryClient.invalidateQueries({ queryKey: queryKeys.stack(tenantID, stackId) });
  }, [settledRun, waitingRun, stackId, queryClient]);

  async function handlePlan() {
    setErrorMessage("");
    try {
      await startRunMutation.mutateAsync({ stackTemplateID: stackTemplate.id, body: { operation: "plan", auto_approve: autoApprove } });
      await queryClient.invalidateQueries({ queryKey: queryKeys.templateRuns(tenantID, stackTemplate.id) });
    } catch (error) {
      setErrorMessage(error instanceof Error ? error.message : "Request failed");
      if (isRunInFlightError(error)) {
        await queryClient.invalidateQueries({ queryKey: queryKeys.templateRuns(tenantID, stackTemplate.id) });
      }
    }
  }

  const planBusy = startRunMutation.isPending;
  const planButton = (disabledReason?: string) => (
    <>
      {disabledReason && (
        <p className="muted" data-testid="template-run-actions-disabled-reason">
          {disabledReason}
        </p>
      )}
      <button className="primary-button" disabled={Boolean(disabledReason) || !canPlan || planBusy} onClick={handlePlan} type="button">
        {planBusy ? <Loader2 size={16} className="spin" /> : <Play size={16} />}
        Plan
      </button>
    </>
  );

  return (
    <div className="template-run-actions" data-testid="template-run-actions">
      <header className="panel-header">
        <h2 className="section-title">Runs</h2>
        <div className="template-run-controls">
          {/* Auto-approve is an approval given in advance, so it is offered
              only to someone who could approve the plan afterwards. */}
          <RequireCapability capability="canApprove" stackId={stackId}>
            <label className="checkbox-label" data-testid="template-run-auto-approve">
              <input type="checkbox" checked={autoApprove} onChange={(event) => setAutoApprove(event.target.checked)} />
              Auto Apply
            </label>
          </RequireCapability>
          <RequireCapability capability="canOperate" stackId={stackId} fallback={planButton("Starting a run requires operator access")}>
            {planButton()}
          </RequireCapability>
        </div>
      </header>
      {errorMessage && <p className="error-text">{errorMessage}</p>}
    </div>
  );
}
