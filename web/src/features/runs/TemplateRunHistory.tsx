import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { CircleStop, Loader2, ShieldCheck } from "lucide-react";
import { Link } from "react-router-dom";
import { isTerminalRunStatus } from "../../api/polling";
import { queryKeys } from "../../api/queryKeys";
import { useApproveRunMutation, useCancelRunMutation, useTemplateRunsQuery } from "../../api/queries";
import type { TemplateRun } from "../../api/types";
import RequireCapability from "../../auth/RequireCapability";
import { tenantID } from "../../config";
import { formatDateTime } from "../../shared/formatTimestamp";
import { statusGlyph, statusTone } from "../../shared/statusTone";

interface TemplateRunHistoryProps {
  stackId: string;
  stackTemplateId: string;
}

// Every run recorded for a template, newest first, one row each: its state,
// its number and operation (the link to its detail), who started it and when.
// A run that is still going carries its own actions — Approve while it waits
// for approval, Cancel until it finishes — so what they act on is the row they
// sit on. Actions a viewer may not take are left out rather than disabled.
export default function TemplateRunHistory({ stackId, stackTemplateId }: TemplateRunHistoryProps) {
  const [errorMessage, setErrorMessage] = useState("");
  const queryClient = useQueryClient();
  const runsQuery = useTemplateRunsQuery(tenantID, stackTemplateId);
  const runs = runsQuery.data ?? [];

  const approveRunMutation = useApproveRunMutation(tenantID);
  const cancelRunMutation = useCancelRunMutation(tenantID);

  async function runAction(action: () => Promise<void>) {
    setErrorMessage("");
    try {
      await action();
    } catch (error) {
      setErrorMessage(error instanceof Error ? error.message : "Request failed");
    }
    await queryClient.invalidateQueries({ queryKey: queryKeys.templateRuns(tenantID, stackTemplateId) });
  }

  const rowActions = {
    stackId,
    approvingRunID: approveRunMutation.isPending ? approveRunMutation.variables : undefined,
    cancelingRunID: cancelRunMutation.isPending ? cancelRunMutation.variables?.runID : undefined,
    onApprove: (run: TemplateRun) => void runAction(() => approveRunMutation.mutateAsync(run.id)),
    onCancel: (run: TemplateRun) =>
      void runAction(() => cancelRunMutation.mutateAsync({ runID: run.id, body: { reason: "canceled from the runs list" } }))
  };

  return (
    <div className="template-run-history" data-testid="template-run-history">
      {errorMessage && <p className="error-text">{errorMessage}</p>}
      {runs.length === 0 ? (
        <p className="muted" data-testid="template-run-history-empty">
          No runs yet. Plan to see what this template would change.
        </p>
      ) : (
        <ul className="run-list">
          {runs.map((run) => (
            <RunRow key={run.id} run={run} to={`/stacks/${stackId}/templates/${stackTemplateId}/runs/${run.run_number}`} {...rowActions} />
          ))}
        </ul>
      )}
    </div>
  );
}

interface RunRowProps {
  run: TemplateRun;
  to: string;
  stackId: string;
  approvingRunID?: string;
  cancelingRunID?: string;
  onApprove: (run: TemplateRun) => void;
  onCancel: (run: TemplateRun) => void;
}

function RunRow({ run, to, stackId, approvingRunID, cancelingRunID, onApprove, onCancel }: RunRowProps) {
  const tone = statusTone(run.status);
  const showApprove = run.status === "waiting_approval";
  const showCancel = !isTerminalRunStatus(run.status);

  return (
    <li className="run-list__row" data-testid={`template-run-row-${run.id}`}>
      <span className={`status-tone status-tone--${tone} run-list__status`}>
        <span className="status-tone__glyph" aria-hidden="true">
          {statusGlyph(tone)}
        </span>
        {run.status}
      </span>
      <Link className="run-list__name" to={to} data-testid={`template-run-history-${run.id}`}>
        <span className="run-list__number">#{run.run_number}</span> {run.operation}
      </Link>
      <span className="run-list__meta">
        {run.trigger_actor} ·{" "}
        <time dateTime={run.started_at} title={run.started_at}>
          {formatDateTime(run.started_at)}
        </time>
      </span>
      {(showApprove || showCancel) && (
        <span className="run-list__actions">
          {showCancel && (
            <RequireCapability capability="canOperate" stackId={stackId}>
              <button className="secondary-button" type="button" disabled={cancelingRunID === run.id} onClick={() => onCancel(run)}>
                {cancelingRunID === run.id ? <Loader2 size={16} className="spin" /> : <CircleStop size={16} />}
                Cancel
              </button>
            </RequireCapability>
          )}
          {showApprove && (
            <RequireCapability capability="canApprove" stackId={stackId}>
              <button className="primary-button" type="button" disabled={approvingRunID === run.id} onClick={() => onApprove(run)}>
                {approvingRunID === run.id ? <Loader2 size={16} className="spin" /> : <ShieldCheck size={16} />}
                Approve
              </button>
            </RequireCapability>
          )}
        </span>
      )}
    </li>
  );
}
