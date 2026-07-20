import { Loader2, Plus, RefreshCw } from "lucide-react";
import { Link } from "react-router-dom";
import { useStacksQuery } from "../../api/queries";
import RequireCapability from "../../auth/RequireCapability";
import { tenantID } from "../../config";
import { useQueryErrorBoundary } from "../../shared/queryErrorBoundary";

// The list is authz-filtered by the backend (mocked today, real
// ListObjects-based filtering once AUTH-013 lands) — the screen renders
// whatever listStacks returns and never filters client-side.
export default function StacksListScreen() {
  const { data: stacks, status, error, refetch } = useStacksQuery(tenantID);
  const boundary = useQueryErrorBoundary(error);

  if (status === "pending") {
    return (
      <section className="stacks-list-screen" data-testid="stacks-list-loading">
        <p className="muted">
          <Loader2 size={16} className="spin" /> Loading stacks…
        </p>
      </section>
    );
  }

  if (status === "error") {
    if (boundary !== null) {
      return <>{boundary}</>;
    }
    return (
      <section className="stacks-list-screen" data-testid="stacks-list-error">
        <h1>Stacks</h1>
        <p className="muted">Something went wrong while loading stacks.</p>
        <button className="primary-button" type="button" data-testid="stacks-list-retry" onClick={() => refetch()}>
          <RefreshCw size={16} />
          Retry
        </button>
      </section>
    );
  }

  return (
    <section className="stacks-list-screen">
      <header className="stacks-list-header">
        <h1>Stacks</h1>
        <RequireCapability capability="canCreateStack">
          <Link className="primary-button" to="/stacks/new" data-testid="create-stack-link">
            <Plus size={16} />
            Create stack
          </Link>
        </RequireCapability>
      </header>
      {stacks.length === 0 ? (
        <p className="muted" data-testid="stacks-list-empty">
          No stacks visible to you yet.
        </p>
      ) : (
        <ul className="stacks-list" data-testid="stacks-list">
          {stacks.map((stack) => (
            <li key={stack.id}>
              <Link to={`/stacks/${stack.id}`}>{stack.name}</Link>
              <small className="muted">{stack.slug}</small>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
