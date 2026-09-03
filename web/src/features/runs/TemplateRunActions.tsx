import { useEffect, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { CircleStop, Loader2, Play, ShieldCheck } from "lucide-react";
import { Link } from "react-router-dom";
import { isTerminalRunStatus } from "../../api/polling";
import { queryKeys } from "../../api/queryKeys";
import { useApproveRunMutation, useCancelRunMutation, useStartTemplateRunMutation, useTemplateRunsQuery } from "../../api/queries";
import type { Operation, StackTemplate, TemplateRun } from "../../api/types";
import RequireCapability from "../../auth/RequireCapability";
import { tenantID } from "../../config";
import StatusRow from "../../shared/StatusRow";
import { hasFreshPlan, planStaleReason } from "../stacks/stackWorkflow";
import RunActionButton, { type RunActionButtonProps } from "./RunActionButton";

interface TemplateRunActionsProps {
  stackId: string;
  stackTemplate: StackTemplate;
}

function latestRunFor(runs: TemplateRun[], operation: Operation): TemplateRun | null {
  return runs.find((candidate) => candidate.operation === operation) ?? null;
}

// The operations header for the selected template on /stacks/:stackId/template.
// Run state is derived entirely from the server's run history
// (useTemplateRunsQuery), not from local component state, so it is visible to
// every user who can view the stack — not just the browser tab that started a
// run. Destroy deliberately lives elsewhere: see TemplateDestroyPanel.
export default function TemplateRunActions({ stackId, stackTemplate }: TemplateRunActionsProps) {
  const [errorMessage, setErrorMessage] = useState("");
  const queryClient = useQueryClient();

  const runsQuery = useTemplateRunsQuery(tenantID, stackTemplate.id);
  const runs = runsQuery.data ?? [];
  const runsReady = runsQuery.status === "success";
  const planRun = latestRunFor(runs, "plan");
  const applyRun = latestRunFor(runs, "apply");
  const latestRun = runsReady ? runs[0] ?? null : null;
  const activeRun = runsReady ? runs.find((candidate) => !isTerminalRunStatus(candidate.status)) ?? null : null;
  const approvalRun = runsReady ? runs.find((candidate) => candidate.status === "waiting_approval") ?? null : null;

  const startRunMutation = useStartTemplateRunMutation(tenantID);
  const approveRunMutation = useApproveRunMutation(tenantID);
  const cancelRunMutation = useCancelRunMutation(tenantID);

  const canPlan = runsReady && !activeRun;
  const canApply = runsReady && !activeRun && hasFreshPlan(stackTemplate);
  const staleReason = planStaleReason(stackTemplate);

  // plan_state and live_state live on the stack template, but the thing that
  // changes them is a run finishing — and only the runs query polls. Without
  // this the header would keep rendering the state the template had when the
  // screen loaded: Apply disabled after a plan that has since completed.
  //
  // This effect owns the invalidation for the whole template screen: the
  // history and danger-zone sections read the same two queries, and this
  // component is always rendered alongside them.
  const settledRun = latestRun && isTerminalRunStatus(latestRun.status) ? `${latestRun.id}:${latestRun.status}` : "";
  useEffect(() => {
    if (settledRun === "") {
      return;
    }
    void queryClient.invalidateQueries({ queryKey: queryKeys.stack(tenantID, stackId) });
  }, [settledRun, stackId, queryClient]);
  const canApprove = Boolean(approvalRun);
  const canCancel = Boolean(activeRun);

  async function runAction(action: () => Promise<void>) {
    setErrorMessage("");
    try {
      await action();
      await queryClient.invalidateQueries({ queryKey: queryKeys.templateRuns(tenantID, stackTemplate.id) });
    } catch (error) {
      setErrorMessage(error instanceof Error ? error.message : "Request failed");
    }
  }

  async function handlePlan() {
    await runAction(async () => {
      await startRunMutation.mutateAsync({ stackTemplateID: stackTemplate.id, body: { operation: "plan" } });
    });
  }

  async function handleApply() {
    await runAction(async () => {
      await startRunMutation.mutateAsync({ stackTemplateID: stackTemplate.id, body: { operation: "apply" } });
    });
  }

  async function handleApprove() {
    if (!approvalRun) {
      return;
    }
    await runAction(async () => {
      await approveRunMutation.mutateAsync(approvalRun.id);
    });
  }

  async function handleCancel() {
    if (!activeRun) {
      return;
    }
    await runAction(async () => {
      await cancelRunMutation.mutateAsync({ runID: activeRun.id, body: { reason: "canceled from template screen" } });
    });
  }

  const controlsProps = {
    canPlan,
    onPlan: handlePlan,
    planBusy: startRunMutation.isPending && startRunMutation.variables?.body.operation === "plan",
    canApply,
    onApply: handleApply,
    applyBusy: startRunMutation.isPending && startRunMutation.variables?.body.operation === "apply",
    canCancel,
    onCancel: handleCancel,
    cancelBusy: cancelRunMutation.isPending
  };

  return (
    <section className="panel template-run-actions" data-testid="template-run-actions">
      <h2>Runs</h2>
      {errorMessage && <p className="error-text">{errorMessage}</p>}
      <div className="template-run-states">
        <div>
          <StatusRow label="Plan" value={planRun?.status ?? "not started"} />
          {planRun && (
            <Link to={`/stacks/${stackId}/runs/${planRun.id}`} data-testid="template-run-plan-link">
              View plan run
            </Link>
          )}
        </div>
        <div>
          <StatusRow label="Apply" value={applyRun?.status ?? "not started"} />
          {applyRun && (
            <Link to={`/stacks/${stackId}/runs/${applyRun.id}`} data-testid="template-run-apply-link">
              View apply run
            </Link>
          )}
        </div>
      </div>
      {staleReason && (
        <p className="hint-text" data-testid="template-run-plan-stale">
          {staleReason}
        </p>
      )}
      {/* Operate and approve are separate capabilities, so they are separate
          gates — the wrapper only keeps their buttons on one line. */}
      <div className="template-run-controls">
        <RequireCapability
          capability="canOperate"
          stackId={stackId}
          fallback={<RunControls {...controlsProps} disabledReason="Plan, apply, and cancel require operator access" />}
        >
          <RunControls {...controlsProps} />
        </RequireCapability>
        <RequireCapability
          capability="canApprove"
          stackId={stackId}
          fallback={<ApproveButton enabled={false} onClick={handleApprove} busy={approveRunMutation.isPending} disabledReason="Approving requires approver access" />}
        >
          <ApproveButton enabled={canApprove} onClick={handleApprove} busy={approveRunMutation.isPending} />
        </RequireCapability>
      </div>
    </section>
  );
}

interface RunControlsProps {
  canPlan: boolean;
  onPlan: () => void;
  planBusy: boolean;
  canApply: boolean;
  onApply: () => void;
  applyBusy: boolean;
  canCancel: boolean;
  onCancel: () => void;
  cancelBusy: boolean;
  disabledReason?: string;
}

function RunControls({ canPlan, onPlan, planBusy, canApply, onApply, applyBusy, canCancel, onCancel, cancelBusy, disabledReason }: RunControlsProps) {
  const locked = Boolean(disabledReason);
  return (
    <div className="button-row">
      <button className="primary-button" disabled={locked || !canPlan || planBusy} onClick={onPlan} type="button">
        {planBusy ? <Loader2 size={16} className="spin" /> : <Play size={16} />}
        Plan
      </button>
      <button className="primary-button" disabled={locked || !canApply || applyBusy} onClick={onApply} type="button">
        {applyBusy ? <Loader2 size={16} className="spin" /> : <Play size={16} />}
        Apply
      </button>
      <button className="secondary-button" disabled={locked || !canCancel || cancelBusy} onClick={onCancel} type="button">
        {cancelBusy ? <Loader2 size={16} className="spin" /> : <CircleStop size={16} />}
        Cancel
      </button>
      {disabledReason && (
        <p className="muted" data-testid="template-run-actions-disabled-reason">
          {disabledReason}
        </p>
      )}
    </div>
  );
}

// This screen's approve action, bound to its own reason test id.
function ApproveButton(props: Omit<RunActionButtonProps, "label" | "icon" | "reasonTestID">) {
  return <RunActionButton {...props} label="Approve" icon={<ShieldCheck size={16} />} reasonTestID="template-run-approve-disabled-reason" />;
}
