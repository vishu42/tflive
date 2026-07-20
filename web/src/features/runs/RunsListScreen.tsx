import { RefreshCw } from "lucide-react";
import { useParams } from "react-router-dom";
import { useStackQuery } from "../../api/queries";
import { tenantID } from "../../config";
import { useQueryErrorBoundary } from "../../shared/queryErrorBoundary";
import RunsListRow from "./RunsListRow";

// /stacks/:stackId/runs — one row per installed stack template, migrated
// from the legacy single-component RunsPanel in App.tsx (see RunsListRow for
// the per-row plan/apply/approve/cancel behavior).
export default function RunsListScreen() {
  const { stackId = "" } = useParams<{ stackId: string }>();
  const stackQuery = useStackQuery(tenantID, stackId);
  const boundary = useQueryErrorBoundary(stackQuery.error);

  if (stackQuery.status === "pending") {
    return (
      <section className="runs-list-screen" data-testid="runs-list-loading">
        <p className="muted">Loading runs…</p>
      </section>
    );
  }

  if (stackQuery.status === "error") {
    if (boundary !== null) {
      return <>{boundary}</>;
    }
    return (
      <section className="runs-list-screen" data-testid="runs-list-error">
        <p className="muted">Something went wrong while loading runs.</p>
        <button className="primary-button" type="button" data-testid="runs-list-retry" onClick={() => stackQuery.refetch()}>
          <RefreshCw size={16} />
          Retry
        </button>
      </section>
    );
  }

  const stackTemplates = stackQuery.data?.templates ?? [];

  if (stackTemplates.length === 0) {
    return (
      <section className="runs-list-screen" data-testid="runs-list-empty">
        <p className="muted">No stack templates installed</p>
      </section>
    );
  }

  return (
    <section className="runs-list-screen workflow-grid" data-testid="runs-list">
      {stackTemplates.map((stackTemplate) => (
        <RunsListRow key={stackTemplate.id} stackId={stackId} stackTemplate={stackTemplate} />
      ))}
    </section>
  );
}
