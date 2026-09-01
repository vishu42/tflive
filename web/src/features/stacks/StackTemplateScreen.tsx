import { useState } from "react";
import { Loader2, Plus, RefreshCw } from "lucide-react";
import { Link, useParams, useSearchParams } from "react-router-dom";
import {
  useCreateStackTemplateCredentialMutation,
  useDeleteStackTemplateCredentialMutation,
  useStackQuery,
  useStackTemplateCredentialsQuery,
  useTemplateRevisionVariablesQuery,
  useUpdateStackTemplateConfigMutation
} from "../../api/queries";
import { tenantID } from "../../config";
import RequireCapability from "../../auth/RequireCapability";
import { useQueryErrorBoundary } from "../../shared/queryErrorBoundary";
import StackTemplateConfigPanel from "./StackTemplateConfigPanel";
import CredentialsPanel from "./CredentialsPanel";
import TemplateRunActions from "../runs/TemplateRunActions";
import TemplateRunHistory from "../runs/TemplateRunHistory";
import TemplateDestroyPanel from "../runs/TemplateDestroyPanel";
import {
  canSaveInstalledTemplateConfig,
  configFromVariableValues,
  findSelectedStackTemplate,
  isDestroyingStackTemplate,
  stackTemplateLabel,
  variableValuesFromConfig
} from "./stackWorkflow";

// /stacks/:stackId/template — configures and operates the installed template.
// Installing lives at template/new and choosing another revision lives at
// template/:stackTemplateId/upgrade, so this screen has no revision selector
// and its configuration form has exactly one action.
//
// Runs are inline rather than on their own tab (issue #130). The right column
// reads top-to-bottom as operate → configure → review → destroy: run actions
// sit above the forms so plan/apply need no scrolling and the stale-plan
// warning reads as a banner over the config it refers to, while history and
// the irreversible destroy sit at the foot.
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
  // Read by SessionProvider's proactive re-auth timer: it defers navigating
  // away while a `[data-unsaved='true']` element is mounted, so an in-flight
  // edit here is never wiped out by a background sign-in redirect.
  const hasUnsavedConfig = Object.keys(editedValues).length > 0;

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

  const configPanelProps = {
    variables,
    variableValues,
    onVariableValueChange: handleVariableValueChange,
    canSave: canSaveConfig,
    onSave: handleSaveStackTemplateConfig,
    saveBusy: updateStackTemplateConfigMutation.isPending,
    disabledReason: destroying ? "Destroy in progress" : undefined
  };

  return (
    <section className="stack-template-screen" data-testid="stack-template-screen">
      {errorMessage && <div className="alert">{errorMessage}</div>}
      <div className="workflow-grid">
        <section className="panel">
          <div className="stack-template-list-content" data-testid="stack-template-list-content">
            <header className="panel-header" data-testid="stack-template-panel-header">
              <h2>Stack templates</h2>
              <RequireCapability capability="canOperate">{addTemplateLink}</RequireCapability>
            </header>
            {stackTemplates.length === 0 ? (
              <p className="muted" data-testid="stack-template-empty">
                No stack templates installed
              </p>
            ) : (
              <div className="stack-template-items" data-testid="stack-template-items">
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
          </div>
        </section>
        {installedTemplate && (
          <div className="stack-template-right-column" data-testid="stack-template-right-column">
            <section className="stack-template-revision-action" data-testid="stack-template-revision-action">
              <p className="muted">
                <RefreshCw size={16} />
                Choose a template revision
              </p>
              <RequireCapability capability="canOperate">
                {destroying ? (
                  <>
                    <button
                      className="secondary-button"
                      type="button"
                      disabled
                      data-testid="change-stack-template-revision-link"
                    >
                      Change revision
                    </button>
                    <p className="muted" data-testid="upgrade-disabled-reason">
                      Destroy in progress
                    </p>
                  </>
                ) : (
                  <Link
                    className="secondary-button"
                    to={`/stacks/${stackId}/template/${installedTemplate.id}/upgrade`}
                    data-testid="change-stack-template-revision-link"
                  >
                    Change revision
                  </Link>
                )}
              </RequireCapability>
            </section>
            <TemplateRunActions stackId={stackId} stackTemplate={installedTemplate} />
            <div data-unsaved={hasUnsavedConfig ? "true" : undefined}>
              <RequireCapability
                capability="canOperate"
                fallback={
                  <StackTemplateConfigPanel
                    {...configPanelProps}
                    canSave={false}
                    saveBusy={false}
                    disabledReason="Editing requires operator access"
                  />
                }
              >
                <StackTemplateConfigPanel {...configPanelProps} />
              </RequireCapability>
            </div>
            <RequireCapability capability="canManageAccess">
              <CredentialsPanel
                title="Template credentials"
                subtitle="Overrides the stack environment for this template only."
                credentials={templateCredentialsQuery.data ?? []}
                loading={templateCredentialsQuery.isPending}
                busy={createTemplateCredentialMutation.isPending || deleteTemplateCredentialMutation.isPending}
                onCreate={createTemplateCredential}
                onDelete={(id) => deleteTemplateCredentialMutation.mutateAsync(id)}
              />
            </RequireCapability>
            <TemplateRunHistory stackId={stackId} stackTemplateId={installedTemplate.id} />
            <TemplateDestroyPanel stackId={stackId} stackTemplate={installedTemplate} />
          </div>
        )}
      </div>
    </section>
  );
}
