import { useState } from "react";
import { CircleStop, Loader2, RefreshCw } from "lucide-react";
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
import { formatDateTime } from "../../shared/formatTimestamp";
import { useQueryErrorBoundary } from "../../shared/queryErrorBoundary";
import { statusGlyph, statusTone } from "../../shared/statusTone";
import { planSummaryLabel } from "../stacks/stackWorkflow";
import RunLogsPanel from "./RunLogsPanel";
import { runStatusLabel } from "./runStatusLabel";
import { WaitingRunActions } from "./TemplateRunHistory";

// /stacks/:stackId/templates/:stackTemplateId/runs/:runNumber — plan/apply
// detail with per-phase logs, reached from the Runs tab. The URL carries the
// run's number within its template, which is what people see; the run's id,
// which every run endpoint takes, comes from the template's runs list. That
// list is the one the Runs tab already loaded, so arriving from there costs no
// extra request. Logs arrive in the order their commands ran, and the screen
// opens on the latest: the plan, or the apply once there is one. Phase
// selection is derived (not effect-synced) so a stale choice falls back to the
// latest phase instead of rendering nothing.
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
  const selectedPhase = logs.find((log) => log.phase === chosenPhase)?.phase ?? logs[logs.length - 1]?.phase ?? "";
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
    <section className="run-detail-screen" data-testid="run-detail-screen">
      {run && (
        <>
          <header className="run-detail-header">
            <span className="run-detail-title">
              <span className="run-detail-number">Run #{run.run_number}</span>
              <span className={`status-tone status-tone--${statusTone(run.status)}`} title={run.status} data-testid="run-detail-status">
                <span className="status-tone__glyph" aria-hidden="true">
                  {statusGlyph(statusTone(run.status))}
                </span>
                {runStatusLabel(run)}
              </span>
            </span>
            {canCancel && (
              <span className="run-detail-actions">
                {canApprove ? (
                  <WaitingRunActions
                    run={run}
                    stackId={stackId}
                    approveBusy={approveRunMutation.isPending}
                    discardBusy={cancelRunMutation.isPending}
                    onApprove={handleApprove}
                    onDiscard={handleCancel}
                  />
                ) : (
                  <RequireCapability capability="canOperate" stackId={stackId}>
                    <button className="secondary-button" type="button" disabled={cancelRunMutation.isPending} onClick={handleCancel}>
                      {cancelRunMutation.isPending ? <Loader2 size={16} className="spin" /> : <CircleStop size={16} />}
                      Cancel
                    </button>
                  </RequireCapability>
                )}
              </span>
            )}
          </header>
          {errorMessage && <div className="alert">{errorMessage}</div>}
          <dl className="run-detail-facts">
            <div>
              <dt>Started</dt>
              <dd>
                <time dateTime={run.started_at} title={run.started_at}>
                  {formatDateTime(run.started_at)}
                </time>
              </dd>
            </div>
            <div>
              <dt>Completed</dt>
              <dd>
                {hasCompleted(run.completed_at) ? (
                  <time dateTime={run.completed_at} title={run.completed_at}>
                    {formatDateTime(run.completed_at ?? "")}
                  </time>
                ) : (
                  "—"
                )}
              </dd>
            </div>
            {run.plan_summary && (
              <div>
                <dt>Changes</dt>
                <dd title="To add, to change, to destroy">{planSummaryLabel(run.plan_summary)}</dd>
              </div>
            )}
            <div>
              <dt>Started by</dt>
              <dd>{run.trigger_actor}</dd>
            </div>
            <div>
              <dt>Source</dt>
              <dd>
                {run.selected_ref} @ {run.resolved_commit_sha.slice(0, 7)}
              </dd>
            </div>
          </dl>
          {run.error_summary && <p className="error-text">{run.error_summary}</p>}
        </>
      )}
      <RunLogsPanel logs={logs} selectedPhase={selectedPhase} onSelectPhase={setChosenPhase} logBody={logBody} />
    </section>
  );
}

// completed_at always arrives: an unfinished run reads as the zero time.
function hasCompleted(completedAt: string | undefined): boolean {
  return Boolean(completedAt) && !completedAt!.startsWith("0001-");
}
