import { useState } from "react";
import { CircleStop, Loader2, RefreshCw, ShieldCheck } from "lucide-react";
import { useParams } from "react-router-dom";
import { isTerminalRunStatus } from "../../api/polling";
import {
  useApproveRunMutation,
  useCancelRunMutation,
  useTemplateRunLogQuery,
  useTemplateRunLogsQuery,
  useTemplateRunQuery,
  useTemplateRunsQuery
} from "../../api/queries";
import RequireCapability from "../../auth/RequireCapability";
import { tenantID } from "../../config";
import StatusRow from "../../shared/StatusRow";
import { useQueryErrorBoundary } from "../../shared/queryErrorBoundary";
import RunActionButton, { type RunActionButtonProps } from "./RunActionButton";
import RunLogsPanel from "./RunLogsPanel";

// /stacks/:stackId/templates/:stackTemplateId/runs/:runNumber — plan/apply
// detail with per-phase logs, reached from the Runs tab. The URL carries the
// run's number within its template, which is what people see; the run's id,
// which every run endpoint takes, comes from the template's runs list. That
// list is the one the Runs tab already loaded, so arriving from there costs no
// extra request. Phase selection is derived (not effect-synced) so a stale
// choice falls back to the first phase instead of rendering nothing.
export default function RunDetailScreen() {
  const {
    stackId = "",
    stackTemplateId = "",
    runNumber = ""
  } = useParams<{ stackId: string; stackTemplateId: string; runNumber: string }>();
  const [chosenPhase, setChosenPhase] = useState("");
  const [errorMessage, setErrorMessage] = useState("");

  const runsQuery = useTemplateRunsQuery(tenantID, stackTemplateId);
  const runId = runsQuery.data?.find((candidate) => String(candidate.run_number) === runNumber)?.id ?? "";

  const runQuery = useTemplateRunQuery(tenantID, runId, { poll: true });
  const boundary = useQueryErrorBoundary(runsQuery.error ?? runQuery.error);
  const run = runQuery.data ?? null;

  const logsQuery = useTemplateRunLogsQuery(tenantID, runId, run?.status ?? "");
  const logs = logsQuery.data ?? [];
  const selectedPhase = logs.find((log) => log.phase === chosenPhase)?.phase ?? logs[0]?.phase ?? "";
  const logQuery = useTemplateRunLogQuery(tenantID, runId, selectedPhase, run?.status ?? "");
  const logBody = logQuery.data ?? "";

  const approveRunMutation = useApproveRunMutation(tenantID);
  const cancelRunMutation = useCancelRunMutation(tenantID);

  const canApprove = Boolean(run && run.status === "waiting_approval");
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

  // A cached list can predate a run that was just started, so a number it
  // lacks only means "no such run" once a refetch has confirmed it.
  if (runsQuery.status === "success" && !runsQuery.isFetching && runId === "") {
    return (
      <section className="run-detail-screen" data-testid="run-detail-missing">
        <p className="muted">This template has no run #{runNumber}.</p>
      </section>
    );
  }

  if (runsQuery.status === "error" || runQuery.status === "error") {
    if (boundary !== null) {
      return <>{boundary}</>;
    }
    return (
      <section className="run-detail-screen" data-testid="run-detail-error">
        <p className="muted">Something went wrong while loading the run.</p>
        <button
          className="primary-button"
          type="button"
          data-testid="run-detail-retry"
          onClick={() => (runsQuery.status === "error" ? runsQuery.refetch() : runQuery.refetch())}
        >
          <RefreshCw size={16} />
          Retry
        </button>
      </section>
    );
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

  return (
    <section className="run-detail-screen workflow-grid" data-testid="run-detail-screen">
      <section className="panel">
        <h2>{run ? `Run #${run.run_number}` : "Run"}</h2>
        {errorMessage && <div className="alert">{errorMessage}</div>}
        <div className="button-row">
          <RequireCapability
            capability="canApprove"
            stackId={stackId}
            fallback={<ApproveButton enabled={false} onClick={handleApprove} busy={approveRunMutation.isPending} disabledReason="Approving requires approver access" />}
          >
            <ApproveButton enabled={canApprove} onClick={handleApprove} busy={approveRunMutation.isPending} />
          </RequireCapability>
          <RequireCapability
            capability="canOperate"
            stackId={stackId}
            fallback={<CancelButton enabled={false} onClick={handleCancel} busy={cancelRunMutation.isPending} disabledReason="Canceling requires operator access" />}
          >
            <CancelButton enabled={canCancel} onClick={handleCancel} busy={cancelRunMutation.isPending} />
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

// This screen's two actions, bound to its own reason test ids. Everything else
// about them is RunActionButton's.
type ScreenAction = Omit<RunActionButtonProps, "label" | "icon" | "reasonTestID">;

function ApproveButton(props: ScreenAction) {
  return <RunActionButton {...props} label="Approve" icon={<ShieldCheck size={16} />} reasonTestID="run-detail-approve-disabled-reason" />;
}

function CancelButton(props: ScreenAction) {
  return <RunActionButton {...props} label="Cancel" icon={<CircleStop size={16} />} reasonTestID="run-detail-cancel-disabled-reason" />;
}
