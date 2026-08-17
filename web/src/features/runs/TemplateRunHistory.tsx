import { Link } from "react-router-dom";
import { useTemplateRunsQuery } from "../../api/queries";
import { tenantID } from "../../config";

interface TemplateRunHistoryProps {
  stackId: string;
  stackTemplateId: string;
}

// Every run recorded for the selected template on /stacks/:stackId/template.
// Read-only by design — the controls that start a run live in
// TemplateRunActions, so this section is purely a log you consult. It shares
// the runs query key with that component, so both render from one request.
export default function TemplateRunHistory({ stackId, stackTemplateId }: TemplateRunHistoryProps) {
  const runsQuery = useTemplateRunsQuery(tenantID, stackTemplateId);
  const runs = runsQuery.data ?? [];

  return (
    <section className="panel template-run-history" data-testid="template-run-history">
      <h2>Run history</h2>
      {runs.length === 0 ? (
        <p className="muted" data-testid="template-run-history-empty">
          No runs yet
        </p>
      ) : (
        <ul>
          {runs.map((historyRun) => (
            <li key={historyRun.id}>
              <Link to={`/stacks/${stackId}/runs/${historyRun.id}`} data-testid={`template-run-history-${historyRun.id}`}>
                {historyRun.operation} - {historyRun.status} - {historyRun.trigger_actor} - {historyRun.started_at}
              </Link>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
