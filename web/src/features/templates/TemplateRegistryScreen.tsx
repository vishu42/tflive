import { Loader2, Plus, RefreshCw } from "lucide-react";
import { Link, useSearchParams } from "react-router-dom";
import { useTemplateRevisionsQuery } from "../../api/queries";
import { tenantID } from "../../config";
import HeroGraphic from "../../shared/HeroGraphic";
import { useQueryErrorBoundary } from "../../shared/queryErrorBoundary";
import SectionLabel from "../../shared/SectionLabel";
import { statusGlyph, statusTone } from "../../shared/statusTone";
import { useInView } from "../../shared/useInView";
import { groupTemplateRevisionsByRepository, templateRevisionRefLabel } from "./templateWorkflow";

// /templates lists registered template revisions and nothing else; the
// registration form lives at /templates/new. Selection is URL state
// (?selected=<revisionID>) rather than component state so that the
// registration screen can hand back the revision it just minted, and so a
// highlighted row survives a reload or a shared link.
export default function TemplateRegistryScreen() {
  const [searchParams] = useSearchParams();
  const selectedTemplateRevisionID = searchParams.get("selected") ?? "";
  const templateRevisionsQuery = useTemplateRevisionsQuery(tenantID);
  const boundary = useQueryErrorBoundary(templateRevisionsQuery.error);
  const { ref: emptyStateRef, visible: emptyStateVisible } = useInView<HTMLDivElement>();

  if (templateRevisionsQuery.status === "pending") {
    return (
      <section className="template-registry-screen" data-testid="template-registry-loading">
        <p className="muted">
          <Loader2 size={16} className="spin" /> Loading templates…
        </p>
      </section>
    );
  }

  if (templateRevisionsQuery.status === "error") {
    if (boundary !== null) {
      return <>{boundary}</>;
    }
    return (
      <section className="template-registry-screen" data-testid="template-registry-error">
        <h1>Templates</h1>
        <p className="muted">Something went wrong while loading templates.</p>
        <button
          className="primary-button"
          type="button"
          data-testid="template-registry-retry"
          onClick={() => templateRevisionsQuery.refetch()}
        >
          <RefreshCw size={16} />
          Retry
        </button>
      </section>
    );
  }

  const templateRevisions = templateRevisionsQuery.data;

  return (
    <section className="template-registry-screen">
      <header className="templates-list-header">
        <div className="page-header">
          <SectionLabel>Templates</SectionLabel>
          <h1>Templates</h1>
        </div>
        <Link className="primary-button" to="/templates/new" data-testid="register-template-link">
          <Plus size={16} />
          Register template
        </Link>
      </header>
      {templateRevisions.length === 0 ? (
        <section className="showcase showcase--compact" data-testid="templates-list-empty">
          <div className="showcase__body reveal" ref={emptyStateRef} data-visible={emptyStateVisible}>
            <SectionLabel pulse>Get started</SectionLabel>
            <h2 className="showcase__title gradient-text">No templates yet</h2>
            <p className="showcase__lede">Register a Terraform module to make it available to your stacks.</p>
          </div>
          <div className="showcase__visual">
            <HeroGraphic />
          </div>
        </section>
      ) : (
        <div className="templates-groups" data-testid="templates-list">
          {groupTemplateRevisionsByRepository(templateRevisions).map((group) => (
            <section className="templates-group" key={group.key} data-testid={`template-group-${group.key}`}>
              <h2 className="templates-group__heading">
                <span className="templates-group__repo">
                  {group.repoOwner}/{group.repoName}
                </span>
                <span className="templates-group__count" data-testid={`template-group-count-${group.key}`}>
                  {group.templateRevisions.length}
                </span>
              </h2>
              <ul className="templates-list">
                {group.templateRevisions.map((templateRevision) => {
                  const selected = templateRevision.id === selectedTemplateRevisionID;
                  const tone = statusTone(templateRevision.status);
                  // The heading already names the repository, so the row shows
                  // only what separates one revision from another.
                  const details = [templateRevision.name.trim(), templateRevision.root_path]
                    .filter((part) => part !== "")
                    .join(" · ");
                  return (
                    <li
                      key={templateRevision.id}
                      data-testid={`template-row-${templateRevision.id}`}
                      data-selected={selected ? "true" : undefined}
                      aria-current={selected ? "true" : undefined}
                    >
                      <div className="templates-list__main">
                        <span className="templates-list__name">{templateRevisionRefLabel(templateRevision)}</span>
                        {details !== "" && <small className="muted">{details}</small>}
                      </div>
                      <span className={`status-tone status-tone--${tone}`}>
                        <span className="status-tone__glyph" aria-hidden="true">
                          {statusGlyph(tone)}
                        </span>
                        {templateRevision.status}
                      </span>
                    </li>
                  );
                })}
              </ul>
            </section>
          ))}
        </div>
      )}
    </section>
  );
}
