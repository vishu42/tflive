import { Loader2, Plus, RefreshCw } from "lucide-react";
import { Link, useSearchParams } from "react-router-dom";
import { useTemplateRevisionsQuery } from "../../api/queries";
import { tenantID } from "../../config";
import HeroGraphic from "../../shared/HeroGraphic";
import { useQueryErrorBoundary } from "../../shared/queryErrorBoundary";
import { statusGlyph } from "../../shared/statusTone";
import { useInView } from "../../shared/useInView";
import {
  groupTemplatesByRepository,
  revisionCountLabel,
  shortCommitSHA,
  templateRootPathLabel,
  unsettledStatusTone
} from "./templateWorkflow";

// /templates lists one row per template, not per revision: a template
// accumulates a revision for every distinct commit its ref resolves to, so
// listing revisions here grows a wall of rows that differ only by SHA. The
// commits live behind the row, at /templates/:sourceTemplateId.
//
// Selection is URL state (?selected=<revisionID>) rather than component state
// so that the registration screen can hand back the revision it just minted,
// and so a highlighted row survives a reload or a shared link. The param names
// a revision, so the row highlighted is the template that revision belongs to.
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
            <h2 className="showcase__title gradient-text">No templates yet</h2>
            <p className="showcase__lede">Register a Terraform module to make it available to your stacks.</p>
          </div>
          <div className="showcase__visual">
            <HeroGraphic />
          </div>
        </section>
      ) : (
        <div className="templates-groups" data-testid="templates-list">
          {groupTemplatesByRepository(templateRevisions).map((group) => (
            <section className="templates-group" key={group.key} data-testid={`template-group-${group.key}`}>
              <h2 className="templates-group__heading">
                <span className="templates-group__repo">
                  {group.repoOwner}/{group.repoName}
                </span>
                <span className="templates-group__count" data-testid={`template-group-count-${group.key}`}>
                  {group.sourceTemplates.length}
                </span>
              </h2>
              <ul className="templates-list">
                {group.sourceTemplates.map((sourceTemplate) => {
                  const selected = sourceTemplate.revisions.some(
                    (revision) => revision.id === selectedTemplateRevisionID
                  );
                  const { status } = sourceTemplate.latestRevision;
                  const unsettledTone = unsettledStatusTone(status);
                  const rootPath = templateRootPathLabel(sourceTemplate.rootPath, sourceTemplate.name);
                  return (
                    <li
                      key={sourceTemplate.sourceTemplateID}
                      data-testid={`template-row-${sourceTemplate.sourceTemplateID}`}
                      data-selected={selected ? "true" : undefined}
                      aria-current={selected ? "true" : undefined}
                    >
                      <div className="templates-list__main">
                        <Link
                          className="templates-list__name"
                          to={`/templates/${encodeURIComponent(sourceTemplate.sourceTemplateID)}`}
                        >
                          {sourceTemplate.name}
                        </Link>
                        {rootPath !== "" && <small className="muted">{rootPath}</small>}
                      </div>
                      <span className="templates-list__meta">
                        {unsettledTone !== null && (
                          <span className={`status-tone status-tone--${unsettledTone}`}>
                            <span className="status-tone__glyph" aria-hidden="true">
                              {statusGlyph(unsettledTone)}
                            </span>
                            {status}
                          </span>
                        )}
                        <span className="templates-list__rev">
                          {sourceTemplate.sourceRef} · {shortCommitSHA(sourceTemplate.latestRevision.resolved_commit_sha)}{" "}
                          · {revisionCountLabel(sourceTemplate.revisions.length)}
                        </span>
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
