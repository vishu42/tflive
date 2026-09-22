import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Check, CircleStop, Loader2, Trash2 } from "lucide-react";
import { Link } from "react-router-dom";
import { isTerminalRunStatus } from "../../api/polling";
import { queryKeys } from "../../api/queryKeys";
import { useApproveRunMutation, useCancelRunMutation, useTemplateRunsQuery } from "../../api/queries";
import type { TemplateRun } from "../../api/types";
import RequireCapability from "../../auth/RequireCapability";
import { tenantID } from "../../config";
import { formatDateTime } from "../../shared/formatTimestamp";
import { statusGlyph, statusTone } from "../../shared/statusTone";
import { planSummaryLabel } from "../stacks/stackWorkflow";
import { runStatusLabel } from "./runStatusLabel";

interface TemplateRunHistoryProps {
  stackId: string;
  stackTemplateId: string;
}

// Every run recorded for a template, newest first, as a table: its number
// (the link to its detail), where it is (which names its operation, so there is
// no Type column), what its plan would change, who started it and when. A run that is still going carries its own actions in a
// trailing column that only appears while some run has one: a plan waiting for
// approval offers Approve (Destroy, on a destroy run), which applies exactly that
// saved plan, and Discard; anything else in flight offers Cancel. Actions a
// viewer may not take are left out rather than disabled.
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

  const hasActions = runs.some((run) => !isTerminalRunStatus(run.status));
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
        <div className="data-table-frame">
          <table className={hasActions ? "data-table run-table run-table--actions" : "data-table run-table"}>
            <colgroup>
              <col className="data-table__col--xs" />
              <col className="data-table__col--lg" />
              <col className="data-table__col--md" />
              <col className="data-table__col--md" />
              <col />
              {hasActions && <col className="data-table__col--actions" />}
            </colgroup>
            <thead>
              <tr>
                <th scope="col">Run</th>
                <th scope="col">Status</th>
                <th scope="col">Changes</th>
                <th scope="col">Actor</th>
                <th scope="col">Time</th>
                {hasActions && (
                  <th scope="col">
                    <span className="visually-hidden">Actions</span>
                  </th>
                )}
              </tr>
            </thead>
            <tbody>
              {runs.map((run) => (
                <RunRow
                  key={run.id}
                  run={run}
                  to={`/stacks/${stackId}/templates/${stackTemplateId}/runs/${run.run_number}`}
                  hasActions={hasActions}
                  {...rowActions}
                />
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

interface RunRowProps {
  run: TemplateRun;
  to: string;
  hasActions: boolean;
  stackId: string;
  approvingRunID?: string;
  cancelingRunID?: string;
  onApprove: (run: TemplateRun) => void;
  onCancel: (run: TemplateRun) => void;
}

function RunRow({ run, to, hasActions, stackId, approvingRunID, cancelingRunID, onApprove, onCancel }: RunRowProps) {
  const tone = statusTone(run.status);
  const showApprove = run.status === "waiting_approval";
  const showCancel = !isTerminalRunStatus(run.status);
  const summary = planSummaryLabel(run.plan_summary);

  return (
    <tr data-testid={`template-run-row-${run.id}`}>
      <td>
        <Link className="data-table__link" to={to} data-testid={`template-run-history-${run.id}`}>
          #{run.run_number}
        </Link>
      </td>
      <td>
        <span className={`status-tone status-tone--${tone}`} title={run.status} data-testid={`template-run-status-${run.id}`}>
          <span className="status-tone__glyph" aria-hidden="true">
            {statusGlyph(tone)}
          </span>
          {runStatusLabel(run)}
        </span>
      </td>
      <td className="run-table__summary" title={summary ? "To add, to change, to destroy" : undefined} data-testid={`template-run-summary-${run.id}`}>
        {summary}
      </td>
      <td className="data-table__mono" title={run.trigger_actor}>
        {run.trigger_actor}
      </td>
      <td className="data-table__mono">
        <time dateTime={run.started_at} title={run.started_at}>
          {formatDateTime(run.started_at)}
        </time>
      </td>
      {hasActions && (
        <td className="data-table__actions">
          {showApprove ? (
            <WaitingRunActions
              run={run}
              stackId={stackId}
              approveBusy={approvingRunID === run.id}
              discardBusy={cancelingRunID === run.id}
              onApprove={() => onApprove(run)}
              onDiscard={() => onCancel(run)}
            />
          ) : (
            showCancel && (
              <RequireCapability capability="canOperate" stackId={stackId}>
                <button className="secondary-button" type="button" disabled={cancelingRunID === run.id} onClick={() => onCancel(run)}>
                  {cancelingRunID === run.id ? <Loader2 size={16} className="spin" /> : <CircleStop size={16} />}
                  Cancel
                </button>
              </RequireCapability>
            )
          )}
        </td>
      )}
    </tr>
  );
}

interface WaitingRunActionsProps {
  run: TemplateRun;
  stackId: string;
  approveBusy: boolean;
  discardBusy: boolean;
  onApprove: () => void;
  onDiscard: () => void;
}

// WaitingRunActions are what a plan waiting for approval offers: Discard, which
// throws the plan away, and Approve, which applies exactly that saved plan.
//
// On a destroy run approving destroys what the template manages, so the
// button is red, says how much it destroys, and takes a second click:
// approving is the irreversible step, since the Settings button only planned
// it. While it asks, Keep and Confirm take the place of Discard and Destroy,
// so there are never more than two buttons.
export function WaitingRunActions({ run, stackId, approveBusy, discardBusy, onApprove, onDiscard }: WaitingRunActionsProps) {
  const [confirming, setConfirming] = useState(false);
  const count = run.plan_summary?.destroy ?? 0;
  const title = `Destroy ${count} ${count === 1 ? "resource" : "resources"}`;

  if (confirming) {
    return (
      <RequireCapability capability="canApprove" stackId={stackId}>
        <button className="secondary-button" type="button" disabled={approveBusy} onClick={() => setConfirming(false)}>
          Keep
        </button>
        <button className="destructive-button" type="button" title={title} disabled={approveBusy} onClick={onApprove}>
          {approveBusy ? <Loader2 size={16} className="spin" /> : <Trash2 size={16} />}
          Confirm destroy
        </button>
      </RequireCapability>
    );
  }

  return (
    <>
      <RequireCapability capability="canOperate" stackId={stackId}>
        <button className="secondary-button" type="button" disabled={discardBusy} onClick={onDiscard}>
          {discardBusy ? <Loader2 size={16} className="spin" /> : <CircleStop size={16} />}
          Discard
        </button>
      </RequireCapability>
      <RequireCapability capability="canApprove" stackId={stackId}>
        {run.operation === "destroy" ? (
          <button className="destructive-button" type="button" title={title} disabled={approveBusy} onClick={() => setConfirming(true)}>
            <Trash2 size={16} />
            Destroy {count}
          </button>
        ) : (
          <button className="primary-button" type="button" disabled={approveBusy} onClick={onApprove}>
            {approveBusy ? <Loader2 size={16} className="spin" /> : <Check size={16} />}
            Approve
          </button>
        )}
      </RequireCapability>
    </>
  );
}
