import { CheckCircle2, RefreshCw, XCircle } from "lucide-react";

export default function StatusRow({ label, value }: { label: string; value: string }) {
  let icon = <CheckCircle2 size={15} />;
  if (value === "failed" || value === "invalid") {
    icon = <XCircle size={15} />;
  } else if (value.startsWith("not ") || inProgressStatus(value)) {
    icon = <RefreshCw size={15} />;
  }

  return (
    <div className="status-row">
      <span>{label}</span>
      <strong>
        {icon}
        {value}
      </strong>
    </div>
  );
}

function inProgressStatus(value: string): boolean {
  return [
    "pending",
    "pending_validation",
    "running",
    "validating",
    "queued",
    "locked",
    "workspace_prepared",
    "source_fetched",
    "init",
    "workspace_selected",
    "planned",
    "waiting_approval",
    "approved",
    "apply_started",
    "applied",
    "destroy_started",
    "destroyed",
    "cancel_requested",
    "canceling",
    "lock_released"
  ].includes(value);
}
