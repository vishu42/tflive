import { Loader2, Plus, RefreshCw } from "lucide-react";
import { Link } from "react-router-dom";
import { useStacksQuery } from "../../api/queries";
import RequireCapability from "../../auth/RequireCapability";
import { tenantID } from "../../config";
import HeroGraphic from "../../shared/HeroGraphic";
import { useQueryErrorBoundary } from "../../shared/queryErrorBoundary";
import SectionLabel from "../../shared/SectionLabel";
import { useInView } from "../../shared/useInView";

// The list is authz-filtered by the backend (AUTH-013) — the screen renders
// whatever listStacks returns and never filters client-side.
export default function StacksListScreen() {
  const { data: stacks, status, error, refetch } = useStacksQuery(tenantID);
  const boundary = useQueryErrorBoundary(error);
  const { ref: emptyStateRef, visible: emptyStateVisible } = useInView<HTMLDivElement>();

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
        <div className="page-header">
          <SectionLabel pulse>Stacks</SectionLabel>
          <h1>Stacks</h1>
        </div>
        <RequireCapability capability="canCreateStack">
          <Link className="primary-button" to="/stacks/new" data-testid="create-stack-link">
            <Plus size={16} />
            Create stack
          </Link>
        </RequireCapability>
      </header>
      {stacks.length === 0 ? (
        <section className="showcase showcase--compact" data-testid="stacks-list-empty">
          <div className="showcase__body reveal" ref={emptyStateRef} data-visible={emptyStateVisible}>
            <SectionLabel pulse>Get started</SectionLabel>
            <h2 className="showcase__title gradient-text">No stacks yet</h2>
            <p className="showcase__lede">No stacks visible to you yet.</p>
          </div>
          <div className="showcase__visual">
            <HeroGraphic />
          </div>
        </section>
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
