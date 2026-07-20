import { useState } from "react";
import { CircleStop, Loader2, Play, ShieldCheck } from "lucide-react";
import { Link } from "react-router-dom";
import { isTerminalRunStatus } from "../../api/polling";
import { useApproveRunMutation, useCancelRunMutation, useStartTemplateRunMutation, useTemplateRunQuery } from "../../api/queries";
import type { StackTemplate } from "../../api/types";
import RequireCapability from "../../auth/RequireCapability";
import { tenantID } from "../../config";
import StatusRow from "../../shared/StatusRow";
import { stackTemplateLabel } from "../stacks/stackWorkflow";

interface RunsListRowProps {
  stackId: string;
  stackTemplate: StackTemplate;
}

// One row per installed stack template on /stacks/:stackId/runs — session-scoped
// plan/apply tracking (like the legacy RunsPanel), generalized across the
// stack's components instead of a single selected one. Log viewing lives on
// the run detail route; this row only starts runs and links to them.
export default function RunsListRow({ stackId, stackTemplate }: RunsListRowProps) {
  const [planRunID, setPlanRunID] = useState("");
  const [applyRunID, setApplyRunID] = useState("");
  const [errorMessage, setErrorMessage] = useState("");

  const planRunQuery = useTemplateRunQuery(tenantID, planRunID, { poll: true });
  const applyRunQuery = useTemplateRunQuery(tenantID, applyRunID, { poll: true });
  const planRun = planRunQuery.data ?? null;
  const applyRun = applyRunQuery.data ?? null;

  const startRunMutation = useStartTemplateRunMutation(tenantID);
  const approveRunMutation = useApproveRunMutation(tenantID);
  const cancelRunMutation = useCancelRunMutation(tenantID);

  const canPlan = !planRun;
  const canApply = Boolean(planRun?.status === "completed" && !applyRun);
  const canApprove = applyRun?.status === "waiting_approval";
  const activeRun = applyRun && !isTerminalRunStatus(applyRun.status) ? applyRun : planRun && !isTerminalRunStatus(planRun.status) ? planRun : null;
  const canCancel = Boolean(activeRun);

  async function runAction(action: () => Promise<void>) {
    setErrorMessage("");
    try {
      await action();
    } catch (error) {
      setErrorMessage(error instanceof Error ? error.message : "Request failed");
    }
  }

  async function handlePlan() {
    await runAction(async () => {
      const next = await startRunMutation.mutateAsync({ stackTemplateID: stackTemplate.id, body: { operation: "plan" } });
      setPlanRunID(next.id);
    });
  }

  async function handleApply() {
    await runAction(async () => {
      const next = await startRunMutation.mutateAsync({ stackTemplateID: stackTemplate.id, body: { operation: "apply" } });
      setApplyRunID(next.id);
    });
  }

  async function handleApprove() {
    if (!applyRunID) {
      return;
    }
    await runAction(async () => {
      await approveRunMutation.mutateAsync(applyRunID);
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

  const actionsProps = {
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
  disabledReason?: string;
}

function RunsRowActions({ canPlan, onPlan, planBusy, canApply, onApply, applyBusy, canCancel, onCancel, cancelBusy, disabledReason }: RunsRowActionsProps) {
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
        <p className="muted" data-testid="runs-row-actions-disabled-reason">
          {disabledReason}
        </p>
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
