import { Loader2, Plus, RefreshCw } from "lucide-react";
import { Link, useParams } from "react-router-dom";
import { useStackQuery } from "../../api/queries";
import { tenantID } from "../../config";
import RequireCapability from "../../auth/RequireCapability";
import { useQueryErrorBoundary } from "../../shared/queryErrorBoundary";
import { statusGlyph } from "../../shared/statusTone";
import { stackTemplateLabel, stackTemplateStatus } from "./stackWorkflow";

// /stacks/:stackId/templates — the templates installed on a stack, and nothing
// else. Each row opens that template's own page at templates/:stackTemplateId,
// where its runs, variables, credentials and settings live on tabs.
export default function StackTemplateListScreen() {
  const { stackId = "" } = useParams<{ stackId: string }>();
  const stackQuery = useStackQuery(tenantID, stackId);
  const boundary = useQueryErrorBoundary(stackQuery.error);
  const stackTemplates = stackQuery.data?.templates ?? [];

  if (stackQuery.status === "pending") {
    return (
      <section className="stack-template-list-screen" data-testid="stack-template-loading">
        <p className="muted">
          <Loader2 size={16} className="spin" /> Loading templates…
        </p>
      </section>
    );
  }

  if (stackQuery.status === "error") {
    if (boundary !== null) {
      return <>{boundary}</>;
    }
    return (
      <section className="stack-template-list-screen" data-testid="stack-template-error">
        <p className="muted">Something went wrong while loading the stack templates.</p>
        <button className="primary-button" type="button" data-testid="stack-template-retry" onClick={() => stackQuery.refetch()}>
          <RefreshCw size={16} />
          Retry
        </button>
      </section>
    );
  }

  return (
    <section className="stack-template-list-screen" data-testid="stack-template-list-screen">
      <div className="stack-template-list-content" data-testid="stack-template-list-content">
        <header className="panel-header" data-testid="stack-template-panel-header">
          <h2 className="section-title">Stack templates</h2>
          <RequireCapability capability="canOperate">
            <Link className="primary-button" to={`/stacks/${stackId}/templates/new`} data-testid="add-stack-template-link">
              <Plus size={16} />
              Add template
            </Link>
          </RequireCapability>
        </header>
        {stackTemplates.length === 0 ? (
          <p className="muted" data-testid="stack-template-empty">
            No stack templates installed
          </p>
        ) : (
          <div className="stack-template-items" data-testid="stack-template-items">
            {stackTemplates.map((item) => {
              const status = stackTemplateStatus(item);
              return (
                <Link key={item.id} to={`/stacks/${stackId}/templates/${item.id}`} data-testid={`stack-template-link-${item.id}`}>
                  <span
                    className={`stack-template-item__dot status-tone--${status.tone}`}
                    data-testid={`stack-template-status-${item.id}`}
                    role="img"
                    aria-label={status.label}
                    title={status.label}
                  >
                    {statusGlyph(status.tone)}
                  </span>
                  <span className="stack-template-item__name">{stackTemplateLabel(item)}</span>
                  <small>{status.label}</small>
                </Link>
              );
            })}
          </div>
        )}
      </div>
    </section>
  );
}
