import { useState } from "react";
import { ArrowLeft, Loader2, RefreshCw, ShieldCheck } from "lucide-react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { useAddTemplateToStackMutation, useTemplateRevisionVariablesQuery, useTemplateRevisionsQuery } from "../../api/queries";
import { tenantID } from "../../config";
import { useQueryErrorBoundary } from "../../shared/queryErrorBoundary";
import { formatTimestamp } from "../../shared/formatTimestamp";
import { statusGlyph } from "../../shared/statusTone";
import {
  activeRevisions,
  groupTemplatesByRepository,
  latestActiveRevision,
  revisionCountLabel,
  shortCommitSHA,
  templateRootPathLabel,
  unsettledStatusTone
} from "../templates/templateWorkflow";
import { configFromVariableValues } from "./stackWorkflow";
import VariableFields from "./VariableFields";

// /stacks/:stackId/template/new — installing a template, extracted from the
// template screen where an always-present Install button sat next to an
// unrelated config form. The picker mirrors /templates so choosing here looks
// like browsing the registry: one row per template, named, with the ref and
// commit demoted to the row's trailing meta.
//
// The install still posts a template_revision_id, so a row click resolves to
// the template's latest *active* revision and a template with more than one
// offers the rest in a dropdown. Picking a revision outright was the wrong
// default: installing almost always wants the newest validated commit, and
// moving between commits afterwards is UpgradeStackTemplateScreen's job.
export default function AddStackTemplateScreen() {
  const { stackId = "" } = useParams<{ stackId: string }>();
  const navigate = useNavigate();
  const [chosenRevisionID, setChosenRevisionID] = useState("");
  const [values, setValues] = useState<Record<string, string>>({});
  const [errorMessage, setErrorMessage] = useState("");

  const templateRevisionsQuery = useTemplateRevisionsQuery(tenantID);
  const boundary = useQueryErrorBoundary(templateRevisionsQuery.error);
  const templateRevisions = templateRevisionsQuery.data ?? [];
  const chosenRevision = templateRevisions.find((revision) => revision.id === chosenRevisionID) ?? null;

  // Grouped once and shared: the picker renders these buckets, and the panel
  // below finds the chosen template among them rather than re-deriving it from
  // the revision's source_template_id — which would need the same empty-id
  // fallback sourceTemplateKey already applies here.
  const repositoryGroups = groupTemplatesByRepository(templateRevisions);
  const chosenTemplate =
    repositoryGroups
      .flatMap((group) => group.sourceTemplates)
      .find((sourceTemplate) => sourceTemplate.revisions.some((revision) => revision.id === chosenRevisionID)) ?? null;
  // Newest-registered first, straight from the API's order — see
  // activeRevisions. The dropdown offers these in exactly this order, so its
  // first option is the one a row click already selected.
  const chosenTemplateRevisions = chosenTemplate ? activeRevisions(chosenTemplate.revisions) : [];

  const variablesQuery = useTemplateRevisionVariablesQuery(tenantID, chosenRevision?.id ?? "");
  const variables = variablesQuery.data ?? [];
  // useTemplateRevisionVariablesQuery keeps the previous revision's variables
  // visible (placeholderData: keepPreviousData) while it fetches the newly
  // chosen one, so isPending alone misses the revision-switch case — only
  // isFetching covers both "never fetched" and "refetching for a new key".
  const variablesLoading = chosenRevision !== null && variablesQuery.isFetching;
  const variablesFailed = chosenRevision !== null && variablesQuery.status === "error";

  const addTemplateToStackMutation = useAddTemplateToStackMutation(tenantID, stackId);
  // Read by SessionProvider's proactive re-auth timer: it defers navigating
  // away while a `[data-unsaved='true']` element is mounted, so variable
  // values entered here are never wiped out by a background sign-in redirect.
  const hasUnsavedValues = Object.keys(values).length > 0;

  function handleChoose(revisionID: string) {
    setChosenRevisionID(revisionID);
    setValues({});
    setErrorMessage("");
  }

  async function handleInstall() {
    // variablesFailed guards the request itself, not just the button: without
    // a loaded variable list, configFromVariableValues would build an empty
    // config and post it as if the template genuinely needed nothing.
    if (!chosenRevision || variablesLoading || variablesFailed) {
      return;
    }
    setErrorMessage("");
    try {
      const installed = await addTemplateToStackMutation.mutateAsync({
        template_revision_id: chosenRevision.id,
        config: configFromVariableValues(variables, values)
      });
      navigate(`/stacks/${stackId}/template?selected=${installed.id}`);
    } catch (error) {
      setErrorMessage(error instanceof Error ? error.message : "Request failed");
    }
  }

  if (templateRevisionsQuery.status === "pending") {
    return (
      <section className="add-stack-template-screen" data-testid="add-stack-template-loading">
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
      <section className="add-stack-template-screen" data-testid="add-stack-template-load-error">
        <p className="muted">Something went wrong while loading templates.</p>
        <button
          className="primary-button"
          type="button"
          data-testid="add-stack-template-retry"
          onClick={() => templateRevisionsQuery.refetch()}
        >
          <RefreshCw size={16} />
          Retry
        </button>
      </section>
    );
  }

  return (
    <section
      className="add-stack-template-screen"
      data-testid="add-stack-template-screen"
      data-unsaved={hasUnsavedValues ? "true" : undefined}
    >
      <header className="page-header">
        <Link to={`/stacks/${stackId}/template`} className="muted back-link">
          <ArrowLeft size={14} />
          Back to templates
        </Link>
        <h1>Add a template to this stack</h1>
      </header>

      {errorMessage && (
        <div className="alert" data-testid="add-stack-template-error">
          {errorMessage}
        </div>
      )}

      {templateRevisions.length === 0 ? (
        <section className="panel" data-testid="add-stack-template-none">
          <p className="muted">No templates are registered for this tenant yet.</p>
          <Link className="primary-button" to="/templates/new" data-testid="register-template-link">
            Register template
          </Link>
        </section>
      ) : (
        <div className="templates-groups">
          {repositoryGroups.map((group) => (
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
                  const installable = latestActiveRevision(sourceTemplate.revisions);
                  const chosenHere = sourceTemplate.revisions.some((revision) => revision.id === chosenRevisionID);
                  const tone = unsettledStatusTone(sourceTemplate.latestRevision.status);
                  const rootPath = templateRootPathLabel(sourceTemplate.rootPath, sourceTemplate.name);
                  const installableCount = activeRevisions(sourceTemplate.revisions).length;
                  // The commit shown is the one a click would install, which is
                  // not necessarily the newest revision — see
                  // latestActiveRevision.
                  const shownRevision = installable ?? sourceTemplate.latestRevision;
                  return (
                    <li key={sourceTemplate.sourceTemplateID}>
                      <button
                        type="button"
                        className="template-choice"
                        data-testid={`add-template-choice-${sourceTemplate.sourceTemplateID}`}
                        data-selected={chosenHere ? "true" : undefined}
                        // No active revision means nothing here can be
                        // installed. The row stays visible, and its status pill
                        // below says why, rather than leaving a dead button.
                        disabled={installable === null}
                        onClick={() => installable && handleChoose(installable.id)}
                      >
                        <span className="templates-list__main">
                          <span className="templates-list__name">{sourceTemplate.name}</span>
                          {rootPath !== "" && <small className="muted">{rootPath}</small>}
                        </span>
                        <span className="templates-list__meta">
                          {tone !== null && (
                            <span className={`status-tone status-tone--${tone}`}>
                              <span className="status-tone__glyph" aria-hidden="true">
                                {statusGlyph(tone)}
                              </span>
                              {sourceTemplate.latestRevision.status}
                            </span>
                          )}
                          <span className="templates-list__rev">
                            {sourceTemplate.sourceRef} · {shortCommitSHA(shownRevision.resolved_commit_sha)}
                            {/* Only worth saying where it means the dropdown
                                below has something to offer. */}
                            {installableCount > 1 && <> · {revisionCountLabel(installableCount)}</>}
                          </span>
                        </span>
                      </button>
                    </li>
                  );
                })}
              </ul>
            </section>
          ))}
        </div>
      )}

      {chosenRevision && (
        <section className="panel wide" data-testid="add-stack-template-variables">
          {/* Only where there is a choice to make. One active revision needs no
              control, and the row click has already selected it. Options carry
              the commit and its registration date — the name and ref are fixed
              for the whole template and already named in the row above. */}
          {chosenTemplateRevisions.length > 1 && (
            <label className="selector-label">
              Revision
              <select
                data-testid="add-template-revision-select"
                value={chosenRevisionID}
                onChange={(event) => handleChoose(event.target.value)}
              >
                {chosenTemplateRevisions.map((candidate, index) => (
                  <option key={candidate.id} value={candidate.id}>
                    {shortCommitSHA(candidate.resolved_commit_sha)} · {formatTimestamp(candidate.created_at)}
                    {index === 0 ? " · latest" : ""}
                  </option>
                ))}
              </select>
            </label>
          )}

          <h2>Variables</h2>
          {variablesLoading ? (
            <p className="muted" data-testid="add-stack-template-variables-loading">
              <Loader2 size={16} className="spin" /> Loading variables…
            </p>
          ) : variablesFailed ? (
            // Distinct from the empty message on purpose: an unresolved fetch
            // must never read as "this template declares no variables", which
            // is a claim we cannot make when we could not load them. Kept
            // inline rather than replacing the screen, so the picker above
            // stays usable and another revision can be chosen.
            <p className="muted" data-testid="add-stack-template-variables-error">
              Could not load this template&rsquo;s variables.{" "}
              <button className="secondary-button" type="button" onClick={() => variablesQuery.refetch()}>
                <RefreshCw size={16} />
                Retry
              </button>
            </p>
          ) : (
            <VariableFields
              variables={variables}
              variableValues={values}
              onVariableValueChange={(name, value) => setValues((current) => ({ ...current, [name]: value }))}
              emptyMessage="This template declares no variables"
            />
          )}
          <div className="button-row form-actions">
            <button
              className="primary-button"
              type="button"
              disabled={variablesLoading || variablesFailed || addTemplateToStackMutation.isPending}
              onClick={handleInstall}
            >
              {addTemplateToStackMutation.isPending ? <Loader2 size={16} className="spin" /> : <ShieldCheck size={16} />}
              Install
            </button>
          </div>
        </section>
      )}
    </section>
  );
}
