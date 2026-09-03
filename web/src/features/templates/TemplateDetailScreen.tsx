import { ArrowLeft, Loader2, RefreshCw } from "lucide-react";
import { Link, useParams, useSearchParams } from "react-router-dom";
import { useTemplateRevisionsQuery } from "../../api/queries";
import NotFound from "../../app/NotFound";
import { tenantID } from "../../config";
import { formatTimestamp } from "../../shared/formatTimestamp";
import { useQueryErrorBoundary } from "../../shared/queryErrorBoundary";
import { statusGlyph } from "../../shared/statusTone";
import {
  revisionsForSourceTemplate,
  shortCommitSHA,
  templateDisplayName,
  templateRootPathLabel,
  unsettledStatusTone
} from "./templateWorkflow";

// /templates/:sourceTemplateId — every commit registered for one template.
//
// There is no source-template endpoint; the tenant-wide revisions list already
// carries `source_template_id` on every row, so this filters the same cache the
// registry screen renders. That list arrives ordered `created_at desc, id desc`
// and is not re-sorted here: sorting on `created_at` alone would drop the id
// tiebreaker and let same-timestamp rows shuffle between renders.
//
// The repository, root path and ref are part of the template's identity and so
// are fixed for every row below — they are stated once in the header rather
// than repeated down the list. Only the commit varies.
export default function TemplateDetailScreen() {
  const { sourceTemplateId = "" } = useParams<{ sourceTemplateId: string }>();
  const [searchParams] = useSearchParams();
  const selectedTemplateRevisionID = searchParams.get("selected") ?? "";
  const templateRevisionsQuery = useTemplateRevisionsQuery(tenantID);
  const boundary = useQueryErrorBoundary(templateRevisionsQuery.error);

  if (templateRevisionsQuery.status === "pending") {
    return (
      <section className="template-detail-screen" data-testid="template-detail-loading">
        <p className="muted">
          <Loader2 size={16} className="spin" /> Loading revisions…
        </p>
      </section>
    );
  }

  if (templateRevisionsQuery.status === "error") {
    if (boundary !== null) {
      return <>{boundary}</>;
    }
    return (
      <section className="template-detail-screen" data-testid="template-detail-error">
        <h1>Template</h1>
        <p className="muted">Something went wrong while loading this template.</p>
        <button
          className="primary-button"
          type="button"
          data-testid="template-detail-retry"
          onClick={() => templateRevisionsQuery.refetch()}
        >
          <RefreshCw size={16} />
          Retry
        </button>
      </section>
    );
  }

  const revisions = revisionsForSourceTemplate(templateRevisionsQuery.data, sourceTemplateId);
  const latestRevision = revisions[0];
  if (!latestRevision) {
    // An id that matches nothing is a bad URL, not an empty template: a source
    // template only exists once it has a revision.
    return <NotFound />;
  }

  const name = templateDisplayName(latestRevision);
  const rootPath = templateRootPathLabel(latestRevision.root_path, "");

  return (
    <section className="template-detail-screen">
      <header className="page-header">
        <Link to="/templates" className="muted back-link" data-testid="template-detail-back">
          <ArrowLeft size={14} />
          Back to templates
        </Link>
        <h1>{name}</h1>
        <p className="muted template-detail__identity" data-testid="template-detail-identity">
          {latestRevision.repo_owner}/{latestRevision.repo_name}
          {rootPath !== "" && <> · {rootPath}</>} · {latestRevision.source_ref}
        </p>
      </header>

      <section className="panel">
        <h2>Revisions</h2>
        <ul className="revisions-list" data-testid="template-revisions">
          {revisions.map((revision, index) => {
            const tone = unsettledStatusTone(revision.status);
            const selected = revision.id === selectedTemplateRevisionID;
            return (
              <li
                key={revision.id}
                data-testid={`revision-row-${revision.id}`}
                data-selected={selected ? "true" : undefined}
                aria-current={selected ? "true" : undefined}
              >
                <span className="revisions-list__sha">{shortCommitSHA(revision.resolved_commit_sha)}</span>
                {/* Registration time, not the commit's authoring date — the
                    commit's own date is not stored on the revision. */}
                <span className="revisions-list__date">{formatTimestamp(revision.created_at)}</span>
                {tone !== null && (
                  <span className={`status-tone status-tone--${tone}`}>
                    <span className="status-tone__glyph" aria-hidden="true">
                      {statusGlyph(tone)}
                    </span>
                    {revision.status}
                  </span>
                )}
                {/* Rendered on every row, hidden where it does not apply, so
                    the word reserves its width and the status pills form a
                    straight column instead of stepping in and out. */}
                <span
                  className="revisions-list__latest"
                  data-latest={index === 0 ? "true" : undefined}
                  data-testid={index === 0 ? "revision-latest" : undefined}
                >
                  latest
                </span>
              </li>
            );
          })}
        </ul>
      </section>
    </section>
  );
}
