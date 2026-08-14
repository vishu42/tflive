import { useEffect, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { CircleStop, Loader2, Play, ShieldCheck, Trash2 } from "lucide-react";
import { Link } from "react-router-dom";
import { isTerminalRunStatus } from "../../api/polling";
import { queryKeys } from "../../api/queryKeys";
import { useApproveRunMutation, useCancelRunMutation, useStartTemplateRunMutation, useTemplateRunsQuery } from "../../api/queries";
import type { Operation, StackTemplate, TemplateRun } from "../../api/types";
import RequireCapability from "../../auth/RequireCapability";
import { tenantID } from "../../config";
import StatusRow from "../../shared/StatusRow";
import { canDestroyStackTemplate, hasFreshPlan, isDestroyingStackTemplate, planStaleReason, stackTemplateLabel } from "../stacks/stackWorkflow";

interface RunsListRowProps {
  stackId: string;
  stackTemplate: StackTemplate;
}

function latestRunFor(runs: TemplateRun[], operation: Operation): TemplateRun | null {
  return runs.find((candidate) => candidate.operation === operation) ?? null;
}

// One row per installed stack template on /stacks/:stackId/runs. Run state is
// derived entirely from the server's run history (useTemplateRunsQuery), not
// from local component state, so it is visible to every user who can view the
// stack — not just the browser tab that started a run.
export default function RunsListRow({ stackId, stackTemplate }: RunsListRowProps) {
  const [errorMessage, setErrorMessage] = useState("");
  const [confirmDestroy, setConfirmDestroy] = useState(false);
  const queryClient = useQueryClient();

  const runsQuery = useTemplateRunsQuery(tenantID, stackTemplate.id);
  const runs = runsQuery.data ?? [];
  const planRun = latestRunFor(runs, "plan");
  const applyRun = latestRunFor(runs, "apply");
  const latestRun = runs[0] ?? null;
  const activeRun = latestRun && !isTerminalRunStatus(latestRun.status) ? latestRun : null;

  const startRunMutation = useStartTemplateRunMutation(tenantID);
  const approveRunMutation = useApproveRunMutation(tenantID);
  const cancelRunMutation = useCancelRunMutation(tenantID);

  const canPlan = !activeRun;
  const canApply = !activeRun && hasFreshPlan(stackTemplate);
  const staleReason = planStaleReason(stackTemplate);

  // plan_state and live_state live on the stack template, but the thing that
  // changes them is a run finishing — and only the runs query polls. Without
  // this the row would keep rendering the state the template had when the
  // screen loaded: Apply disabled after a plan that has since completed.
  const settledRun = latestRun && isTerminalRunStatus(latestRun.status) ? `${latestRun.id}:${latestRun.status}` : "";
  useEffect(() => {
    if (settledRun === "") {
      return;
    }
    void queryClient.invalidateQueries({ queryKey: queryKeys.stack(tenantID, stackId) });
  }, [settledRun, stackId, queryClient]);
  const canApprove = Boolean(latestRun?.status === "waiting_approval");
  const canCancel = Boolean(activeRun);
  const canDestroy = canDestroyStackTemplate(stackTemplate) && !activeRun;
  const destroying = isDestroyingStackTemplate(stackTemplate);
  const destroyBusy = startRunMutation.isPending && startRunMutation.variables?.body.operation === "destroy";

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
    if (!latestRun) {
      return;
    }
    await runAction(async () => {
      await approveRunMutation.mutateAsync(latestRun.id);
    });
  }

  async function handleCancel() {
    if (!activeRun) {
      return;
    }
    await runAction(async () => {
      await cancelRunMutation.mutateAsync({ runID: activeRun.id, body: { reason: "canceled from runs list" } });
    });
  }

  async function handleDestroy() {
    await runAction(async () => {
      await startRunMutation.mutateAsync({ stackTemplateID: stackTemplate.id, body: { operation: "destroy" } });
      setConfirmDestroy(false);
    });
  }

  const actionsProps = {
    canPlan,
    onPlan: handlePlan,
    planBusy: startRunMutation.isPending && startRunMutation.variables?.body.operation === "plan",
    canApply,
    onApply: handleApply,
    applyBusy: startRunMutation.isPending && startRunMutation.variables?.body.operation === "apply",
    canCancel,
    onCancel: handleCancel,
    cancelBusy: cancelRunMutation.isPending,
    canDestroy,
    destroying,
    onDestroy: handleDestroy,
    destroyBusy,
    confirmDestroy,
    onConfirmDestroy: () => setConfirmDestroy(true),
    onCancelConfirm: () => setConfirmDestroy(false)
  };

  return (
    <div className="panel" data-testid={`runs-row-${stackTemplate.id}`}>
      <h3>{stackTemplateLabel(stackTemplate)}</h3>
      {errorMessage && <p className="error-text">{errorMessage}</p>}
      <RequireCapability
        capability="canOperate"
        stackId={stackId}
        fallback={<RunsRowActions {...actionsProps} disabledReason="Plan, apply, and cancel require operator access" />}
      >
        <RunsRowActions {...actionsProps} />
      </RequireCapability>
      <RequireCapability
        capability="canApprove"
        stackId={stackId}
        fallback={<ApproveButton canApprove={false} onApprove={handleApprove} busy={approveRunMutation.isPending} disabledReason="Approving requires approver access" />}
      >
        <ApproveButton canApprove={canApprove} onApprove={handleApprove} busy={approveRunMutation.isPending} />
      </RequireCapability>
      <StatusRow label="Plan" value={planRun?.status ?? "not started"} />
      {staleReason && (
        <p className="hint-text" data-testid={`runs-row-${stackTemplate.id}-plan-stale`}>
          {staleReason}
        </p>
      )}
      {planRun && (
        <Link to={`/stacks/${stackId}/runs/${planRun.id}`} data-testid={`runs-row-${stackTemplate.id}-plan-link`}>
          View plan run
        </Link>
      )}
      <StatusRow label="Apply" value={applyRun?.status ?? "not started"} />
      {applyRun && (
        <Link to={`/stacks/${stackId}/runs/${applyRun.id}`} data-testid={`runs-row-${stackTemplate.id}-apply-link`}>
          View apply run
        </Link>
      )}
      {runs.length > 0 && (
        <div className="run-history" data-testid={`runs-row-${stackTemplate.id}-history`}>
          <h4>History</h4>
          <ul>
            {runs.map((historyRun) => (
              <li key={historyRun.id}>
                <Link to={`/stacks/${stackId}/runs/${historyRun.id}`} data-testid={`runs-row-${stackTemplate.id}-history-${historyRun.id}`}>
                  {historyRun.operation} — {historyRun.status} — {historyRun.trigger_actor} — {historyRun.started_at}
                </Link>
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  );
}

interface RunsRowActionsProps {
  canPlan: boolean;
  onPlan: () => void;
  planBusy: boolean;
  canApply: boolean;
  onApply: () => void;
  applyBusy: boolean;
  canCancel: boolean;
  onCancel: () => void;
  cancelBusy: boolean;
  canDestroy: boolean;
  destroying?: boolean;
  onDestroy: () => void;
  destroyBusy: boolean;
  confirmDestroy?: boolean;
  onConfirmDestroy?: () => void;
  onCancelConfirm?: () => void;
  disabledReason?: string;
}

function RunsRowActions({ canPlan, onPlan, planBusy, canApply, onApply, applyBusy, canCancel, onCancel, cancelBusy, canDestroy, destroying, onDestroy, destroyBusy, confirmDestroy, onConfirmDestroy, onCancelConfirm, disabledReason }: RunsRowActionsProps) {
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
      {confirmDestroy ? (
        <>
          <button className="destructive-button" disabled={destroyBusy} onClick={onDestroy} type="button">
            {destroyBusy ? <Loader2 size={16} className="spin" /> : <Trash2 size={16} />}
            Confirm destroy
          </button>
          <button className="secondary-button" disabled={destroyBusy} onClick={onCancelConfirm} type="button">
            Cancel
          </button>
        </>
      ) : (
        <button className="destructive-button" disabled={locked || destroying || !canDestroy || destroyBusy} onClick={onConfirmDestroy} type="button">
          <Trash2 size={16} />
          Destroy
        </button>
      )}
      {disabledReason && (
        <p className="muted" data-testid="runs-row-actions-disabled-reason">
          {disabledReason}
        </p>
      )}
      {confirmDestroy && (
        <p className="muted">This will permanently destroy all infrastructure managed by this template. This cannot be undone.</p>
      )}
    </div>
  );
}

function ApproveButton({
  canApprove,
  onApprove,
  busy,
  disabledReason
}: {
  canApprove: boolean;
  onApprove: () => void;
  busy: boolean;
  disabledReason?: string;
}) {
  const locked = Boolean(disabledReason);
  return (
    <div>
      <button className="secondary-button" disabled={locked || !canApprove || busy} onClick={onApprove} type="button">
        {busy ? <Loader2 size={16} className="spin" /> : <ShieldCheck size={16} />}
        Approve
      </button>
      {disabledReason && (
        <p className="muted" data-testid="runs-row-approve-disabled-reason">
          {disabledReason}
        </p>
      )}
    </div>
  );
}
