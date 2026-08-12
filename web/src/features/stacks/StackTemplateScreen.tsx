import { useState } from "react";
import { ArrowUpCircle, CheckCircle2, Loader2, Plus, RefreshCw } from "lucide-react";
import { Link, useParams, useSearchParams } from "react-router-dom";
import {
  useCreateStackTemplateCredentialMutation,
  useDeleteStackTemplateCredentialMutation,
  useStackQuery,
  useStackTemplateCredentialsQuery,
  useTemplateRevisionVariablesQuery,
  useTemplateRevisionsQuery,
  useUpdateStackTemplateConfigMutation
} from "../../api/queries";
import { tenantID } from "../../config";
import RequireCapability from "../../auth/RequireCapability";
import { useQueryErrorBoundary } from "../../shared/queryErrorBoundary";
import StackTemplateConfigPanel from "./StackTemplateConfigPanel";
import CredentialsPanel from "./CredentialsPanel";
import {
  canSaveInstalledTemplateConfig,
  configFromVariableValues,
  findSelectedStackTemplate,
  isDestroyingStackTemplate,
  stackTemplateLabel,
  upgradeCandidateRevisions,
  variableValuesFromConfig
} from "./stackWorkflow";

// /stacks/:stackId/template — configures the installed template and nothing
// else. Installing lives at template/new and upgrading at
// template/:stackTemplateId/upgrade, so this screen has no revision selector
// and its form has exactly one action.
//
// Selection is URL state (?selected=<stackTemplateID>) rather than component
// state, so the add and upgrade screens can hand back the template they just
// acted on and a selected row survives a reload.
export default function StackTemplateScreen() {
  const { stackId = "" } = useParams<{ stackId: string }>();
  const [searchParams, setSearchParams] = useSearchParams();
  const [editedValues, setEditedValues] = useState<Record<string, string>>({});
  const [errorMessage, setErrorMessage] = useState("");

  const stackQuery = useStackQuery(tenantID, stackId);
  const stackTemplates = stackQuery.data?.templates ?? [];
  const installedTemplate =
    findSelectedStackTemplate(stackTemplates, searchParams.get("selected") ?? "") ?? stackTemplates[0] ?? null;

  const variablesQuery = useTemplateRevisionVariablesQuery(
    tenantID,
    installedTemplate?.desired_template_revision_id ?? ""
  );
  const variables = variablesQuery.data ?? [];

  // Answers one question only: does a newer active revision of this
  // template's source template exist? A failure here costs the update cue,
  // not the screen, so it is deliberately absent from the error boundary.
  const templateRevisionsQuery = useTemplateRevisionsQuery(tenantID);
  const upgradeCandidates = upgradeCandidateRevisions(templateRevisionsQuery.data ?? [], installedTemplate);
  const updateCueReady = installedTemplate !== null && templateRevisionsQuery.status === "success";

  const boundary = useQueryErrorBoundary(stackQuery.error ?? variablesQuery.error);

  // Displayed values are the installed config overlaid with unsaved edits.
  const baseValues = installedTemplate ? variableValuesFromConfig(installedTemplate.config, variables) : {};
  const variableValues: Record<string, string> = {};
  for (const variable of variables) {
    variableValues[variable.name] = editedValues[variable.name] ?? baseValues[variable.name] ?? "";
  }

  const destroying = isDestroyingStackTemplate(installedTemplate);
  const updateStackTemplateConfigMutation = useUpdateStackTemplateConfigMutation(tenantID, stackId);
  const templateCredentialsQuery = useStackTemplateCredentialsQuery(tenantID, installedTemplate?.id ?? "");
  const createTemplateCredentialMutation = useCreateStackTemplateCredentialMutation(tenantID, installedTemplate?.id ?? "");
  const deleteTemplateCredentialMutation = useDeleteStackTemplateCredentialMutation(tenantID, installedTemplate?.id ?? "");

  const canSaveConfig = canSaveInstalledTemplateConfig(installedTemplate, variables, variableValues);

  function handleSelectStackTemplate(stackTemplateID: string) {
    if (stackTemplateID === installedTemplate?.id) {
      return;
    }
    setSearchParams({ selected: stackTemplateID }, { replace: true });
    setEditedValues({});
  }

  function handleVariableValueChange(name: string, value: string) {
    setEditedValues((current) => ({ ...current, [name]: value }));
  }

  async function handleSaveStackTemplateConfig() {
    if (!installedTemplate || !canSaveConfig) {
      return;
    }
    setErrorMessage("");
    try {
      await updateStackTemplateConfigMutation.mutateAsync({
        stackTemplateID: installedTemplate.id,
        body: { config: configFromVariableValues(variables, variableValues) }
      });
      setEditedValues({});
    } catch (error) {
      setErrorMessage(error instanceof Error ? error.message : "Request failed");
    }
  }

  /** Persists a credential that overrides the Stack value for this template only. */
  async function createTemplateCredential(name: string, value: string) {
    await createTemplateCredentialMutation.mutateAsync({ name, value });
  }

  // The variables query is disabled without an installed template, so it
  // never leaves "pending" — only wait on it once something is installed.
  const waitingOnVariables = installedTemplate !== null && variablesQuery.status === "pending";
  if (stackQuery.status === "pending" || waitingOnVariables) {
    return (
      <section className="stack-template-screen" data-testid="stack-template-loading">
        <p className="muted">
          <Loader2 size={16} className="spin" /> Loading template…
        </p>
      </section>
    );
  }

  if (stackQuery.status === "error" || variablesQuery.status === "error") {
    if (boundary !== null) {
      return <>{boundary}</>;
    }
    return (
      <section className="stack-template-screen" data-testid="stack-template-error">
        <p className="muted">Something went wrong while loading the stack template.</p>
        <button
          className="primary-button"
          type="button"
          data-testid="stack-template-retry"
          onClick={() => {
            stackQuery.refetch();
            variablesQuery.refetch();
          }}
        >
          <RefreshCw size={16} />
          Retry
        </button>
      </section>
    );
  }

  const addTemplateLink = (
    <Link className="primary-button" to={`/stacks/${stackId}/template/new`} data-testid="add-stack-template-link">
      <Plus size={16} />
      Add template
    </Link>
  );

  return (
    <section className="stack-template-screen" data-testid="stack-template-screen">
      {errorMessage && <div className="alert">{errorMessage}</div>}
      <div className="workflow-grid">
        <section className="panel">
          <header className="panel-header">
            <h2>Stack templates</h2>
            <RequireCapability capability="canOperate">{addTemplateLink}</RequireCapability>
          </header>
          {stackTemplates.length === 0 ? (
            <p className="muted" data-testid="stack-template-empty">
              No stack templates installed
            </p>
          ) : (
            <div className="stack-template-items">
              {stackTemplates.map((item) => (
                <button
                  className={item.id === installedTemplate?.id ? "active" : ""}
                  key={item.id}
                  onClick={() => handleSelectStackTemplate(item.id)}
                  type="button"
                >
                  <span>{stackTemplateLabel(item)}</span>
                  {item.lifecycle === "destroying" && (
                    <small className="lifecycle-badge destroying">Destroying…</small>
                  )}
                  <small>{item.desired_template_revision_id}</small>
                </button>
              ))}
            </div>
          )}
        </section>
        {installedTemplate && (
          <>
            {updateCueReady && (
              <section className="panel stack-template-update">
                {upgradeCandidates.length === 0 ? (
                  <p className="muted" data-testid="stack-template-up-to-date">
                    <CheckCircle2 size={16} />
                    Up to date
                  </p>
                ) : (
                  <>
                    <p data-testid="stack-template-update-available">
                      <ArrowUpCircle size={16} />
                      Update available
                    </p>
                    <RequireCapability capability="canOperate">
                      <Link
                        className="secondary-button"
                        to={`/stacks/${stackId}/template/${installedTemplate.id}/upgrade`}
                        data-testid="upgrade-stack-template-link"
                      >
                        Upgrade
                      </Link>
                    </RequireCapability>
                  </>
                )}
              </section>
            )}
            <RequireCapability
              capability="canOperate"
              fallback={
                <StackTemplateConfigPanel
                  variables={variables}
                  variableValues={variableValues}
                  onVariableValueChange={handleVariableValueChange}
                  canSave={false}
                  onSave={handleSaveStackTemplateConfig}
                  saveBusy={false}
                  disabledReason="Editing requires operator access"
                />
              }
            >
              <StackTemplateConfigPanel
                variables={variables}
                variableValues={variableValues}
                onVariableValueChange={handleVariableValueChange}
                canSave={canSaveConfig}
                onSave={handleSaveStackTemplateConfig}
                saveBusy={updateStackTemplateConfigMutation.isPending}
                disabledReason={destroying ? "Destroy in progress" : undefined}
              />
            </RequireCapability>
            <RequireCapability capability="canManageAccess">
              <CredentialsPanel
                title="Template credentials"
                credentials={templateCredentialsQuery.data ?? []}
                loading={templateCredentialsQuery.isPending}
                busy={createTemplateCredentialMutation.isPending || deleteTemplateCredentialMutation.isPending}
                onCreate={createTemplateCredential}
                onDelete={(id) => deleteTemplateCredentialMutation.mutateAsync(id)}
              />
            </RequireCapability>
          </>
        )}
      </div>
    </section>
  );
}
