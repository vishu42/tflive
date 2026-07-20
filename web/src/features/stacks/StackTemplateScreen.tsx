import { useState } from "react";
import { Loader2, RefreshCw } from "lucide-react";
import { useParams } from "react-router-dom";
import {
  useAddTemplateToStackMutation,
  useStackQuery,
  useTemplateRevisionVariablesQuery,
  useTemplateRevisionsQuery,
  useUpdateStackTemplateConfigMutation,
  useUpgradeStackTemplateMutation
} from "../../api/queries";
import { tenantID } from "../../config";
import RequireCapability from "../../auth/RequireCapability";
import { useQueryErrorBoundary } from "../../shared/queryErrorBoundary";
import InstalledTemplatePanel from "./InstalledTemplatePanel";
import VariablesPanel from "./VariablesPanel";
import {
  canSaveStackTemplateConfig,
  canUpgradeStackTemplate,
  configFromVariableValues,
  findSelectedStackTemplate,
  stackTemplateLabel,
  variableValuesFromConfig
} from "./stackWorkflow";
import { findSelectedTemplateRevision, templateRevisionLabel } from "../templates/templateWorkflow";

// /stacks/:stackId/template — installed template + variables, migrated from
// the legacy console in App.tsx. Selection is derived rather than synced via
// effects: the installed template defaults to the stack's first entry and the
// target revision defaults to that template's desired revision, so the screen
// renders complete on first pass (including under SSR in tests). Edit and
// upgrade actions are gated by canOperate; the denied state re-renders the
// panel locked with an explanation instead of hiding it.
export default function StackTemplateScreen() {
  const { stackId = "" } = useParams<{ stackId: string }>();
  const [chosenStackTemplateID, setChosenStackTemplateID] = useState("");
  const [chosenRevisionID, setChosenRevisionID] = useState("");
  const [editedValues, setEditedValues] = useState<Record<string, string>>({});
  const [errorMessage, setErrorMessage] = useState("");

  const stackQuery = useStackQuery(tenantID, stackId);
  const templateRevisionsQuery = useTemplateRevisionsQuery(tenantID);
  const boundary = useQueryErrorBoundary(stackQuery.error ?? templateRevisionsQuery.error);

  const stack = stackQuery.data?.stack ?? null;
  const stackTemplates = stackQuery.data?.templates ?? [];
  const templateRevisions = templateRevisionsQuery.data ?? [];

  const installedTemplate =
    findSelectedStackTemplate(stackTemplates, chosenStackTemplateID) ?? stackTemplates[0] ?? null;
  const selectedTemplateRevision =
    findSelectedTemplateRevision(templateRevisions, chosenRevisionID) ??
    findSelectedTemplateRevision(templateRevisions, installedTemplate?.desired_template_revision_id ?? "") ??
    templateRevisions[0] ??
    null;

  const variablesQuery = useTemplateRevisionVariablesQuery(
    tenantID,
    selectedTemplateRevision?.status === "active" ? selectedTemplateRevision.id : ""
  );
  const variables = variablesQuery.data ?? [];

  // Displayed values are the installed config overlaid with unsaved edits.
  const baseValues = installedTemplate ? variableValuesFromConfig(installedTemplate.config, variables) : {};
  const variableValues: Record<string, string> = {};
  for (const variable of variables) {
    variableValues[variable.name] = editedValues[variable.name] ?? baseValues[variable.name] ?? "";
  }

  const addTemplateToStackMutation = useAddTemplateToStackMutation(tenantID, stackId);
  const updateStackTemplateConfigMutation = useUpdateStackTemplateConfigMutation(tenantID, stackId);
  const upgradeStackTemplateMutation = useUpgradeStackTemplateMutation(tenantID, stackId);

  const canInstall = Boolean(selectedTemplateRevision?.status === "active" && stack);
  const canSaveConfig = canSaveStackTemplateConfig(installedTemplate, selectedTemplateRevision, variables, variableValues);
  const canUpgrade = canUpgradeStackTemplate(installedTemplate, selectedTemplateRevision);

  function handleSelectStackTemplate(stackTemplateID: string) {
    if (stackTemplateID === installedTemplate?.id) {
      return;
    }
    setChosenStackTemplateID(stackTemplateID);
    setChosenRevisionID("");
    setEditedValues({});
  }

  function handleVariableValueChange(name: string, value: string) {
    setEditedValues((current) => ({ ...current, [name]: value }));
  }

  async function handleInstallTemplate() {
    if (!stack || !selectedTemplateRevision) {
      return;
    }
    await runAction(async () => {
      const next = await addTemplateToStackMutation.mutateAsync({
        template_revision_id: selectedTemplateRevision.id,
        selected_ref: selectedTemplateRevision.source_ref,
        config: configFromVariableValues(variables, variableValues)
      });
      setChosenStackTemplateID(next.id);
      setEditedValues({});
    });
  }

  async function handleSaveStackTemplateConfig() {
    if (!installedTemplate || !canSaveConfig) {
      return;
    }
    await runAction(async () => {
      const next = await updateStackTemplateConfigMutation.mutateAsync({
        stackTemplateID: installedTemplate.id,
        body: { config: configFromVariableValues(variables, variableValues) }
      });
      setChosenStackTemplateID(next.id);
      setEditedValues({});
    });
  }

  async function handleUpgradeStackTemplate() {
    if (!installedTemplate || !selectedTemplateRevision || !canUpgrade) {
      return;
    }
    await runAction(async () => {
      const next = await upgradeStackTemplateMutation.mutateAsync({
        stackTemplateID: installedTemplate.id,
        body: {
          target_template_revision_id: selectedTemplateRevision.id,
          config: configFromVariableValues(variables, variableValues)
        }
      });
      setChosenStackTemplateID(next.id);
      setEditedValues({});
    });
  }

  async function runAction(action: () => Promise<void>) {
    setErrorMessage("");
    try {
      await action();
    } catch (error) {
      setErrorMessage(error instanceof Error ? error.message : "Request failed");
    }
  }

  if (stackQuery.status === "pending" || templateRevisionsQuery.status === "pending") {
    return (
      <section className="stack-template-screen" data-testid="stack-template-loading">
        <p className="muted">
          <Loader2 size={16} className="spin" /> Loading template…
        </p>
      </section>
    );
  }

  if (stackQuery.status === "error" || templateRevisionsQuery.status === "error") {
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
            templateRevisionsQuery.refetch();
          }}
        >
          <RefreshCw size={16} />
          Retry
        </button>
      </section>
    );
  }

  const variablesPanelProps = {
    variables,
    variableValues,
    onVariableValueChange: handleVariableValueChange,
    canInstall,
    onInstall: handleInstallTemplate,
    installBusy: addTemplateToStackMutation.isPending,
    canSaveConfig,
    onSaveConfig: handleSaveStackTemplateConfig,
    configBusy: updateStackTemplateConfigMutation.isPending,
    canUpgrade,
    onUpgrade: handleUpgradeStackTemplate,
    upgradeBusy: upgradeStackTemplateMutation.isPending,
    installedTemplateStatus: installedTemplate?.workspace_name ?? "not installed"
  };

  return (
    <section className="stack-template-screen" data-testid="stack-template-screen">
      {errorMessage && <div className="alert">{errorMessage}</div>}
      <div className="workflow-grid">
        <section className="panel">
          <h2>Stack templates</h2>
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
                  <small>{item.desired_template_revision_id}</small>
                </button>
              ))}
            </div>
          )}
          <label className="selector-label">
            Template revision
            <select
              data-testid="stack-template-revision-select"
              value={selectedTemplateRevision?.id ?? ""}
              onChange={(event) => setChosenRevisionID(event.target.value)}
            >
              <option value="">Select revision</option>
              {templateRevisions.map((templateRevision) => (
                <option key={templateRevision.id} value={templateRevision.id}>
                  {templateRevisionLabel(templateRevision)}
                </option>
              ))}
            </select>
          </label>
        </section>
        <InstalledTemplatePanel installedTemplate={installedTemplate} />
        <RequireCapability
          capability="canOperate"
          fallback={<VariablesPanel {...variablesPanelProps} disabledReason="Editing and upgrading require operator access" />}
        >
          <VariablesPanel {...variablesPanelProps} />
        </RequireCapability>
      </div>
    </section>
  );
}
