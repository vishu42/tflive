import { useState } from "react";
import { CircleStop, Loader2, RefreshCw, ShieldCheck } from "lucide-react";
import { useParams } from "react-router-dom";
import { isTerminalRunStatus } from "../../api/polling";
import { useApproveRunMutation, useCancelRunMutation, useTemplateRunLogQuery, useTemplateRunLogsQuery, useTemplateRunQuery } from "../../api/queries";
import RequireCapability from "../../auth/RequireCapability";
import { tenantID } from "../../config";
import StatusRow from "../../shared/StatusRow";
import { useQueryErrorBoundary } from "../../shared/queryErrorBoundary";
import RunLogsPanel from "./RunLogsPanel";

// /stacks/:stackId/runs/:runId — plan/apply detail with per-phase logs,
// reached from a RunsListRow link. Reuses the existing RunLogsPanel
// component unchanged. Phase selection is derived (not effect-synced) for
// the same SSR-safety reason documented on StackTemplateScreen.
export default function RunDetailScreen() {
  const { stackId = "", runId = "" } = useParams<{ stackId: string; runId: string }>();
  const [chosenPhase, setChosenPhase] = useState("");
  const [errorMessage, setErrorMessage] = useState("");

  const runQuery = useTemplateRunQuery(tenantID, runId, { poll: true });
  const boundary = useQueryErrorBoundary(runQuery.error);
  const run = runQuery.data ?? null;

  const logsQuery = useTemplateRunLogsQuery(tenantID, runId, run?.status ?? "");
  const logs = logsQuery.data ?? [];
  const selectedPhase = logs.find((log) => log.phase === chosenPhase)?.phase ?? logs[0]?.phase ?? "";
  const logQuery = useTemplateRunLogQuery(tenantID, runId, selectedPhase, run?.status ?? "");
  const logBody = logQuery.data ?? "";

  const approveRunMutation = useApproveRunMutation(tenantID);
  const cancelRunMutation = useCancelRunMutation(tenantID);

  const canApprove = Boolean(run && run.operation === "apply" && run.status === "waiting_approval");
  const canCancel = Boolean(run && !isTerminalRunStatus(run.status));

  async function runAction(action: () => Promise<void>) {
    setErrorMessage("");
    try {
      await action();
    } catch (error) {
      setErrorMessage(error instanceof Error ? error.message : "Request failed");
    }
  }

  async function handleApprove() {
    await runAction(async () => {
      await approveRunMutation.mutateAsync(runId);
    });
  }

  async function handleCancel() {
    await runAction(async () => {
      await cancelRunMutation.mutateAsync({ runID: runId, body: { reason: "canceled from run detail" } });
    });
  }

  if (runQuery.status === "pending") {
    return (
      <section className="run-detail-screen" data-testid="run-detail-loading">
        <p className="muted">
          <Loader2 size={16} className="spin" /> Loading run…
        </p>
      </section>
    );
  }

  if (runQuery.status === "error") {
    if (boundary !== null) {
      return <>{boundary}</>;
    }
    return (
      <section className="run-detail-screen" data-testid="run-detail-error">
        <p className="muted">Something went wrong while loading the run.</p>
        <button className="primary-button" type="button" data-testid="run-detail-retry" onClick={() => runQuery.refetch()}>
          <RefreshCw size={16} />
          Retry
        </button>
      </section>
    );
  }

  return (
    <section className="run-detail-screen workflow-grid" data-testid="run-detail-screen">
      <section className="panel">
        <h2>Run</h2>
        {errorMessage && <div className="alert">{errorMessage}</div>}
        <div className="button-row">
          <RequireCapability
            capability="canApprove"
            stackId={stackId}
            fallback={
              <ApproveButton canApprove={false} onApprove={handleApprove} busy={approveRunMutation.isPending} disabledReason="Approving requires approver access" />
            }
          >
            <ApproveButton canApprove={canApprove} onApprove={handleApprove} busy={approveRunMutation.isPending} />
          </RequireCapability>
          <RequireCapability
            capability="canOperate"
            stackId={stackId}
            fallback={<CancelButton canCancel={false} onCancel={handleCancel} busy={cancelRunMutation.isPending} disabledReason="Canceling requires operator access" />}
          >
            <CancelButton canCancel={canCancel} onCancel={handleCancel} busy={cancelRunMutation.isPending} />
          </RequireCapability>
        </div>
        <StatusRow label="Operation" value={run?.operation ?? ""} />
        <StatusRow label="Status" value={run?.status ?? ""} />
        <StatusRow label="Started" value={run?.started_at ?? ""} />
        <StatusRow label="Completed" value={run?.completed_at ?? "not completed"} />
        {run?.error_summary && <p className="error-text">{run.error_summary}</p>}
      </section>
      <RunLogsPanel logs={logs} selectedPhase={selectedPhase} onSelectPhase={setChosenPhase} logBody={logBody} />
    </section>
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
        <p className="muted" data-testid="run-detail-approve-disabled-reason">
          {disabledReason}
        </p>
      )}
    </div>
  );
}

function CancelButton({
  canCancel,
  onCancel,
  busy,
  disabledReason
}: {
  canCancel: boolean;
  onCancel: () => void;
  busy: boolean;
  disabledReason?: string;
}) {
  const locked = Boolean(disabledReason);
  return (
    <div>
      <button className="secondary-button" disabled={locked || !canCancel || busy} onClick={onCancel} type="button">
        {busy ? <Loader2 size={16} className="spin" /> : <CircleStop size={16} />}
        Cancel
      </button>
      {disabledReason && (
        <p className="muted" data-testid="run-detail-cancel-disabled-reason">
          {disabledReason}
        </p>
      )}
    </div>
  );
}
