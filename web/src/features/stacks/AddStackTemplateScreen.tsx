import { useState } from "react";
import { ArrowLeft, Loader2, RefreshCw, ShieldCheck } from "lucide-react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { useAddTemplateToStackMutation, useTemplateRevisionVariablesQuery, useTemplateRevisionsQuery } from "../../api/queries";
import { tenantID } from "../../config";
import { useQueryErrorBoundary } from "../../shared/queryErrorBoundary";
import SectionLabel from "../../shared/SectionLabel";
import { groupTemplateRevisionsByRepository, templateRevisionRefLabel } from "../templates/templateWorkflow";
import { configFromVariableValues } from "./stackWorkflow";
import VariableFields from "./VariableFields";

// /stacks/:stackId/template/new — installing a template, extracted from the
// template screen where an always-present Install button sat next to an
// unrelated config form. The picker mirrors /templates so choosing here looks
// like browsing the registry.
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

  const variablesQuery = useTemplateRevisionVariablesQuery(tenantID, chosenRevision?.id ?? "");
  const variables = variablesQuery.data ?? [];

  const addTemplateToStackMutation = useAddTemplateToStackMutation(tenantID, stackId);

  function handleChoose(revisionID: string) {
    setChosenRevisionID(revisionID);
    setValues({});
    setErrorMessage("");
  }

  async function handleInstall() {
    if (!chosenRevision) {
      return;
    }
    setErrorMessage("");
    try {
      const installed = await addTemplateToStackMutation.mutateAsync({
        template_revision_id: chosenRevision.id,
        selected_ref: chosenRevision.source_ref,
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
    <section className="add-stack-template-screen" data-testid="add-stack-template-screen">
      <header className="page-header">
        <Link to={`/stacks/${stackId}/template`} className="muted back-link">
          <ArrowLeft size={14} />
          Back to templates
        </Link>
        <SectionLabel>Add template</SectionLabel>
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
          {groupTemplateRevisionsByRepository(templateRevisions).map((group) => (
            <section className="templates-group" key={group.key} data-testid={`template-group-${group.key}`}>
              <h2 className="templates-group__heading">
                <span className="templates-group__repo">
                  {group.repoOwner}/{group.repoName}
                </span>
              </h2>
              <ul className="templates-list">
                {group.templateRevisions.map((templateRevision) => (
                  <li key={templateRevision.id}>
                    <button
                      type="button"
                      className="template-choice"
                      data-testid={`add-template-choice-${templateRevision.id}`}
                      data-selected={templateRevision.id === chosenRevisionID ? "true" : undefined}
                      disabled={templateRevision.status !== "active"}
                      onClick={() => handleChoose(templateRevision.id)}
                    >
                      <span className="templates-list__name">{templateRevisionRefLabel(templateRevision)}</span>
                      <small className="muted">{templateRevision.status}</small>
                    </button>
                  </li>
                ))}
              </ul>
            </section>
          ))}
        </div>
      )}

      {chosenRevision && (
        <section className="panel wide" data-testid="add-stack-template-variables">
          <h2>Variables</h2>
          <VariableFields
            variables={variables}
            variableValues={values}
            onVariableValueChange={(name, value) => setValues((current) => ({ ...current, [name]: value }))}
            emptyMessage="This template declares no variables"
          />
          <div className="button-row form-actions">
            <button
              className="primary-button"
              type="button"
              disabled={addTemplateToStackMutation.isPending}
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
