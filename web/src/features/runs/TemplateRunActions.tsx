import { useEffect, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Loader2, Play } from "lucide-react";
import { isTerminalRunStatus } from "../../api/polling";
import { queryKeys } from "../../api/queryKeys";
import { useStartTemplateRunMutation, useTemplateRunsQuery } from "../../api/queries";
import type { Operation, StackTemplate } from "../../api/types";
import RequireCapability from "../../auth/RequireCapability";
import { tenantID } from "../../config";
import { hasFreshPlan, planStaleReason } from "../stacks/stackWorkflow";
import { isRunInFlightError } from "./runErrors";

interface TemplateRunActionsProps {
  stackId: string;
  stackTemplate: StackTemplate;
}

// The header of a template's Runs panel: its title and the actions that start
// a run. Actions on a run that already exists (approve, cancel) sit on that
// run's row in TemplateRunHistory instead, so it is always plain which run
// they act on. Destroy lives on the Settings tab: see TemplateDestroyPanel.
//
// Run state is derived entirely from the server's run history
// (useTemplateRunsQuery), not from local component state, so it is visible to
// every user who can view the stack — not just the browser tab that started a
// run.
export default function TemplateRunActions({ stackId, stackTemplate }: TemplateRunActionsProps) {
  const [errorMessage, setErrorMessage] = useState("");
  const queryClient = useQueryClient();

  const runsQuery = useTemplateRunsQuery(tenantID, stackTemplate.id);
  const runs = runsQuery.data ?? [];
  const runsReady = runsQuery.status === "success";
  const latestRun = runsReady ? runs[0] ?? null : null;
  const activeRun = runsReady ? runs.find((candidate) => !isTerminalRunStatus(candidate.status)) ?? null : null;

  const startRunMutation = useStartTemplateRunMutation(tenantID);

  const canPlan = runsReady && !activeRun;
  const canApply = runsReady && !activeRun && hasFreshPlan(stackTemplate);
  const staleReason = planStaleReason(stackTemplate);

  // plan_state and live_state live on the stack template, but the thing that
  // changes them is a run finishing — and only the runs query polls. Without
  // this the page would keep rendering the state the template had when it
  // loaded: Apply disabled after a plan that has since completed.
  //
  // This effect owns the invalidation for the whole template page: the other
  // tabs read the same two queries, and this header is on the default tab.
  const settledRun = latestRun && isTerminalRunStatus(latestRun.status) ? `${latestRun.id}:${latestRun.status}` : "";
  useEffect(() => {
    if (settledRun === "") {
      return;
    }
    void queryClient.invalidateQueries({ queryKey: queryKeys.stack(tenantID, stackId) });
  }, [settledRun, stackId, queryClient]);

  async function start(operation: Operation) {
    setErrorMessage("");
    try {
      await startRunMutation.mutateAsync({ stackTemplateID: stackTemplate.id, body: { operation } });
      await queryClient.invalidateQueries({ queryKey: queryKeys.templateRuns(tenantID, stackTemplate.id) });
    } catch (error) {
      setErrorMessage(error instanceof Error ? error.message : "Request failed");
      if (isRunInFlightError(error)) {
        await queryClient.invalidateQueries({ queryKey: queryKeys.templateRuns(tenantID, stackTemplate.id) });
      }
    }
  }

  const busyOperation = startRunMutation.isPending ? startRunMutation.variables?.body.operation : undefined;
  const controlsProps = {
    canPlan,
    canApply,
    busyOperation,
    onStart: (operation: Operation) => void start(operation)
  };

  return (
    <div className="template-run-actions" data-testid="template-run-actions">
      <header className="panel-header">
        <h2 className="section-title">Runs</h2>
        <RequireCapability
          capability="canOperate"
          stackId={stackId}
          fallback={<RunControls {...controlsProps} disabledReason="Starting a run requires operator access" />}
        >
          <RunControls {...controlsProps} />
        </RequireCapability>
      </header>
      {errorMessage && <p className="error-text">{errorMessage}</p>}
      {staleReason && (
        <p className="hint-text" data-testid="template-run-plan-stale">
          {staleReason}
        </p>
      )}
    </div>
  );
}

interface RunControlsProps {
  canPlan: boolean;
  canApply: boolean;
  busyOperation?: Operation;
  onStart: (operation: Operation) => void;
  disabledReason?: string;
}

// Plan is the primary action; Apply is secondary to it because it only ever
// follows a plan.
function RunControls({ canPlan, canApply, busyOperation, onStart, disabledReason }: RunControlsProps) {
  const locked = Boolean(disabledReason);
  return (
    <div className="template-run-controls">
      {disabledReason && (
        <p className="muted" data-testid="template-run-actions-disabled-reason">
          {disabledReason}
        </p>
      )}
      <button
        className="secondary-button"
        disabled={locked || !canApply || busyOperation === "apply"}
        onClick={() => onStart("apply")}
        type="button"
      >
        {busyOperation === "apply" ? <Loader2 size={16} className="spin" /> : <Play size={16} />}
        Apply
      </button>
      <button
        className="primary-button"
        disabled={locked || !canPlan || busyOperation === "plan"}
        onClick={() => onStart("plan")}
        type="button"
      >
        {busyOperation === "plan" ? <Loader2 size={16} className="spin" /> : <Play size={16} />}
        Plan
      </button>
    </div>
  );
}
