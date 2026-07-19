import { CircleStop, Loader2, Play, ShieldCheck } from "lucide-react";
import StatusRow from "../../shared/StatusRow";

interface RunsPanelProps {
  canPlan: boolean;
  onPlan: () => void;
  planBusy: boolean;
  canApply: boolean;
  onApply: () => void;
  applyBusy: boolean;
  canApprove: boolean;
  onApprove: () => void;
  approveBusy: boolean;
  canCancel: boolean;
  onCancel: () => void;
  cancelBusy: boolean;
  selectedRunKind: "plan" | "apply";
  onSelectRunKind: (runKind: "plan" | "apply") => void;
  currentRunStatus: string;
  currentRunErrorSummary: string;
}

export default function RunsPanel({
  canPlan,
  onPlan,
  planBusy,
  canApply,
  onApply,
  applyBusy,
  canApprove,
  onApprove,
  approveBusy,
  canCancel,
  onCancel,
  cancelBusy,
  selectedRunKind,
  onSelectRunKind,
  currentRunStatus,
  currentRunErrorSummary
}: RunsPanelProps) {
  return (
    <section className="panel">
      <h2>Runs</h2>
      <div className="button-row">
        <button className="primary-button" disabled={!canPlan || planBusy} onClick={onPlan} type="button">
          {planBusy ? <Loader2 size={16} className="spin" /> : <Play size={16} />}
          Plan
        </button>
        <button className="primary-button" disabled={!canApply || applyBusy} onClick={onApply} type="button">
          {applyBusy ? <Loader2 size={16} className="spin" /> : <Play size={16} />}
          Apply
        </button>
        <button className="secondary-button" disabled={!canApprove || approveBusy} onClick={onApprove} type="button">
          {approveBusy ? <Loader2 size={16} className="spin" /> : <ShieldCheck size={16} />}
          Approve
        </button>
        <button className="secondary-button" disabled={!canCancel || cancelBusy} onClick={onCancel} type="button">
          {cancelBusy ? <Loader2 size={16} className="spin" /> : <CircleStop size={16} />}
          Cancel
        </button>
      </div>
      <div className="run-tabs">
        <button className={selectedRunKind === "plan" ? "active" : ""} onClick={() => onSelectRunKind("plan")} type="button">
          Plan
        </button>
        <button className={selectedRunKind === "apply" ? "active" : ""} onClick={() => onSelectRunKind("apply")} type="button">
          Apply
        </button>
      </div>
      <StatusRow label="Current run" value={currentRunStatus} />
      {currentRunErrorSummary && <p className="error-text">{currentRunErrorSummary}</p>}
    </section>
  );
}
