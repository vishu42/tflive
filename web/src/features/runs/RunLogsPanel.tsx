import { SquareTerminal } from "lucide-react";
import type { TemplateRunLog } from "../../api/types";

interface RunLogsPanelProps {
  logs: TemplateRunLog[];
  selectedPhase: string;
  onSelectPhase: (phase: string) => void;
  logBody: string;
}

export default function RunLogsPanel({ logs, selectedPhase, onSelectPhase, logBody }: RunLogsPanelProps) {
  return (
    <section className="panel wide log-panel">
      <h2>
        <SquareTerminal size={18} />
        Logs
      </h2>
      <div className="phase-row">
        {logs.map((log) => (
          <button className={selectedPhase === log.phase ? "active" : ""} key={log.phase} onClick={() => onSelectPhase(log.phase)} type="button">
            {log.phase}
          </button>
        ))}
      </div>
      <pre>{logBody || "No log body"}</pre>
    </section>
  );
}
