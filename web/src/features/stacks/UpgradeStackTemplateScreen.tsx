import { useState } from "react";
import { ArrowLeft, ArrowUpCircle, Loader2, RefreshCw } from "lucide-react";
import { Link, useNavigate, useParams } from "react-router-dom";
import {
  useStackQuery,
  useTemplateRevisionVariablesQuery,
  useTemplateRevisionsQuery,
  useUpgradeStackTemplateMutation
} from "../../api/queries";
import { tenantID } from "../../config";
import { useQueryErrorBoundary } from "../../shared/queryErrorBoundary";
import SectionLabel from "../../shared/SectionLabel";
import { templateRevisionLabel } from "../templates/templateWorkflow";
import {
  configFromVariableValues,
  findSelectedStackTemplate,
  partitionUpgradeVariables,
  stackTemplateLabel,
  upgradeCandidateRevisions,
  variableValuesFromConfig
} from "./stackWorkflow";
import VariableFields from "./VariableFields";

// /stacks/:stackId/template/:stackTemplateId/upgrade — moving an installed
// template to a different revision of the same source template. The old
// screen hid this behind a tenant-wide revision dropdown whose effect
// depended on an invisible source-template match; here the candidates are
// filtered to the valid ones and the variable changes are stated outright.
export default function UpgradeStackTemplateScreen() {
  const { stackId = "", stackTemplateId = "" } = useParams<{ stackId: string; stackTemplateId: string }>();
  const navigate = useNavigate();
  const [chosenTargetID, setChosenTargetID] = useState("");
  const [editedValues, setEditedValues] = useState<Record<string, string>>({});
  const [errorMessage, setErrorMessage] = useState("");

  const stackQuery = useStackQuery(tenantID, stackId);
  const templateRevisionsQuery = useTemplateRevisionsQuery(tenantID);
  const boundary = useQueryErrorBoundary(stackQuery.error ?? templateRevisionsQuery.error);

  const stackTemplate = findSelectedStackTemplate(stackQuery.data?.templates ?? [], stackTemplateId);
  const candidates = upgradeCandidateRevisions(templateRevisionsQuery.data ?? [], stackTemplate);
  const targetRevision = candidates.find((revision) => revision.id === chosenTargetID) ?? candidates[0] ?? null;

  const currentVariablesQuery = useTemplateRevisionVariablesQuery(
    tenantID,
    stackTemplate?.desired_template_revision_id ?? ""
  );
  const targetVariablesQuery = useTemplateRevisionVariablesQuery(tenantID, targetRevision?.id ?? "");
  const partition = partitionUpgradeVariables(currentVariablesQuery.data ?? [], targetVariablesQuery.data ?? []);

  // The config sent with the upgrade covers the target revision's variables
  // only, so anything the new revision dropped is excluded structurally.
  const targetVariables = [...partition.added, ...partition.carried];
  const baseValues = stackTemplate ? variableValuesFromConfig(stackTemplate.config, targetVariables) : {};
  const variableValues: Record<string, string> = {};
  for (const variable of targetVariables) {
    variableValues[variable.name] = editedValues[variable.name] ?? baseValues[variable.name] ?? "";
  }

  const upgradeStackTemplateMutation = useUpgradeStackTemplateMutation(tenantID, stackId);

  async function handleUpgrade() {
    if (!stackTemplate || !targetRevision) {
      return;
    }
    setErrorMessage("");
    try {
      await upgradeStackTemplateMutation.mutateAsync({
        stackTemplateID: stackTemplate.id,
        body: {
          target_template_revision_id: targetRevision.id,
          config: configFromVariableValues(targetVariables, variableValues)
        }
      });
      navigate(`/stacks/${stackId}/template?selected=${stackTemplate.id}`);
    } catch (error) {
      setErrorMessage(error instanceof Error ? error.message : "Request failed");
    }
  }

  if (stackQuery.status === "pending" || templateRevisionsQuery.status === "pending") {
    return (
      <section className="upgrade-stack-template-screen" data-testid="upgrade-loading">
        <p className="muted">
          <Loader2 size={16} className="spin" /> Loading revisions…
        </p>
      </section>
    );
  }

  if (stackQuery.status === "error" || templateRevisionsQuery.status === "error") {
    if (boundary !== null) {
      return <>{boundary}</>;
    }
    return (
      <section className="upgrade-stack-template-screen" data-testid="upgrade-load-error">
        <p className="muted">Something went wrong while loading revisions.</p>
        <button
          className="primary-button"
          type="button"
          data-testid="upgrade-retry"
          onClick={() => {
            stackQuery.refetch();
            templateRevisionsQuery.refetch();
          }}
        >
          <RefreshCw size={16} />
          Retry
        </button>
      </section>
    );
  }

  const backLink = (
    <Link to={`/stacks/${stackId}/template`} className="muted back-link">
      <ArrowLeft size={14} />
      Back to templates
    </Link>
  );

  if (!stackTemplate) {
    return (
      <section className="upgrade-stack-template-screen" data-testid="upgrade-template-missing">
        {backLink}
        <p className="muted">That template is not installed on this stack.</p>
      </section>
    );
  }

  return (
    <section className="upgrade-stack-template-screen" data-testid="upgrade-stack-template-screen">
      <header className="page-header">
        {backLink}
        <SectionLabel>Upgrade</SectionLabel>
        <h1>{stackTemplateLabel(stackTemplate)}</h1>
      </header>

      {errorMessage && (
        <div className="alert" data-testid="upgrade-stack-template-error">
          {errorMessage}
        </div>
      )}

      {candidates.length === 0 ? (
        <section className="panel" data-testid="upgrade-already-latest">
          <p className="muted">Already on the latest revision.</p>
        </section>
      ) : (
        <section className="panel wide">
          <label className="selector-label">
            Target revision
            <select
              data-testid="upgrade-target-select"
              value={targetRevision?.id ?? ""}
              onChange={(event) => {
                setChosenTargetID(event.target.value);
                setEditedValues({});
              }}
            >
              {candidates.map((candidate) => (
                <option key={candidate.id} value={candidate.id}>
                  {templateRevisionLabel(candidate)}
                </option>
              ))}
            </select>
          </label>

          <h2>Variables</h2>
          <VariableFields
            variables={targetVariables}
            variableValues={variableValues}
            onVariableValueChange={(name, value) => setEditedValues((current) => ({ ...current, [name]: value }))}
            emptyMessage="This revision declares no variables"
          />

          {partition.added.length > 0 && (
            <ul className="upgrade-variable-notes">
              {partition.added.map((variable) => (
                <li key={variable.name} data-testid={`upgrade-added-${variable.name}`}>
                  {variable.name} is new in this revision
                </li>
              ))}
            </ul>
          )}

          {partition.removed.length > 0 && (
            <ul className="upgrade-variable-notes upgrade-variable-notes--removed">
              {partition.removed.map((variable) => (
                <li key={variable.name} data-testid={`upgrade-removed-${variable.name}`}>
                  {variable.name} is no longer used and will be dropped
                </li>
              ))}
            </ul>
          )}

          <div className="button-row form-actions">
            <button
              className="primary-button"
              type="button"
              disabled={upgradeStackTemplateMutation.isPending}
              onClick={handleUpgrade}
            >
              {upgradeStackTemplateMutation.isPending ? (
                <Loader2 size={16} className="spin" />
              ) : (
                <ArrowUpCircle size={16} />
              )}
              Upgrade
            </button>
          </div>
        </section>
      )}
    </section>
  );
}
