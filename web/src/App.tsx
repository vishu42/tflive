import { useEffect, useState } from "react";
import type { FormEvent } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { ApiRequestError } from "./api/client";
import { isTerminalRunStatus } from "./api/polling";
import { queryKeys } from "./api/queryKeys";
import {
  useAddTemplateToStackMutation,
  useApproveRunMutation,
  useCancelRunMutation,
  useCreateStackMutation,
  useRegisterTemplateMutation,
  useStackQuery,
  useStacksQuery,
  useStartTemplateRunMutation,
  useTemplateRegistrationQuery,
  useTemplateRevisionVariablesQuery,
  useTemplateRevisionsQuery,
  useTemplateRunLogQuery,
  useTemplateRunLogsQuery,
  useTemplateRunQuery,
  useUpdateStackTemplateConfigMutation,
  useUpgradeStackTemplateMutation
} from "./api/queries";
import { tenantID } from "./config";
import InstalledTemplatePanel from "./features/stacks/InstalledTemplatePanel";
import StackPanel from "./features/stacks/StackPanel";
import VariablesPanel from "./features/stacks/VariablesPanel";
import {
  canUpgradeStackTemplate,
  canSaveStackTemplateConfig,
  configFromVariableValues,
  findSelectedStack,
  findSelectedStackTemplate,
  nextSelectedStackID,
  nextSelectedStackTemplateID,
  variableValuesFromConfig
} from "./features/stacks/stackWorkflow";
import TemplateRegistryPanel from "./features/templates/TemplateRegistryPanel";
import { findSelectedTemplateRevision, nextSelectedTemplateRevisionID } from "./features/templates/templateWorkflow";
import RunLogsPanel from "./features/runs/RunLogsPanel";
import RunsPanel from "./features/runs/RunsPanel";
import IdsPanel from "./shared/IdsPanel";

export default function App() {
  const [repoOwner, setRepoOwner] = useState("hashicorp");
  const [repoName, setRepoName] = useState("");
  const [sourceRef, setSourceRef] = useState("main");
  const [rootPath, setRootPath] = useState(".");
  const [registrationID, setRegistrationID] = useState("");
  const [selectedTemplateRevisionID, setSelectedTemplateRevisionID] = useState("");
  const [variableValues, setVariableValues] = useState<Record<string, string>>({});
  const [stackName, setStackName] = useState("Acme Prod");
  const [stackSlug, setStackSlug] = useState("");
  const [selectedStackID, setSelectedStackID] = useState("");
  const [selectedStackTemplateID, setSelectedStackTemplateID] = useState("");
  const [planRunID, setPlanRunID] = useState("");
  const [applyRunID, setApplyRunID] = useState("");
  const [selectedRunKind, setSelectedRunKind] = useState<"plan" | "apply">("plan");
  const [selectedPhase, setSelectedPhase] = useState("plan");
  const [busyAction, setBusyAction] = useState("");
  const [errorMessage, setErrorMessage] = useState("");

  const queryClient = useQueryClient();

  const stacksQuery = useStacksQuery(tenantID);
  const templateRevisionsQuery = useTemplateRevisionsQuery(tenantID);
  const stackQuery = useStackQuery(tenantID, selectedStackID);
  const registrationQuery = useTemplateRegistrationQuery(tenantID, registrationID);

  const stacks = stacksQuery.data ?? [];
  const templateRevisions = templateRevisionsQuery.data ?? [];
  const stack = stackQuery.data?.stack ?? null;
  const stackTemplates = stackQuery.data?.templates ?? [];
  const registration = registrationQuery.data ?? null;

  const selectedTemplateRevision = findSelectedTemplateRevision(templateRevisions, selectedTemplateRevisionID);
  const selectedStack = findSelectedStack(stacks, selectedStackID);
  const installedTemplate = findSelectedStackTemplate(stackTemplates, selectedStackTemplateID);

  const variablesQuery = useTemplateRevisionVariablesQuery(
    tenantID,
    selectedTemplateRevision?.status === "active" ? selectedTemplateRevision.id : ""
  );
  const variables = variablesQuery.data ?? [];

  const planRunQuery = useTemplateRunQuery(tenantID, planRunID, { poll: selectedRunKind === "plan" });
  const applyRunQuery = useTemplateRunQuery(tenantID, applyRunID, { poll: selectedRunKind === "apply" });
  const planRun = planRunQuery.data ?? null;
  const applyRun = applyRunQuery.data ?? null;
  const currentRun = selectedRunKind === "apply" ? applyRun : planRun;
  const currentRunID = selectedRunKind === "apply" ? applyRunID : planRunID;

  const logsQuery = useTemplateRunLogsQuery(tenantID, currentRunID, currentRun?.status ?? "");
  const logQuery = useTemplateRunLogQuery(tenantID, currentRunID, selectedPhase, currentRun?.status ?? "");
  const logs = logsQuery.data ?? [];
  const logBody = logQuery.data ?? "";

  const canInstall = Boolean(selectedTemplateRevision?.status === "active" && stack);
  const canSaveConfig = canSaveStackTemplateConfig(installedTemplate, selectedTemplateRevision, variables, variableValues);
  const canUpgrade = canUpgradeStackTemplate(installedTemplate, selectedTemplateRevision);
  const canPlan = Boolean(installedTemplate && !planRun);
  const canApply = Boolean(installedTemplate && planRun?.status === "completed" && !applyRun);
  const canApprove = applyRun?.status === "waiting_approval";
  const canCancel = Boolean(currentRun && !isTerminalRunStatus(currentRun.status));

  useEffect(() => {
    if (stacksQuery.data) {
      setSelectedStackID((current) => nextSelectedStackID(stacksQuery.data, current));
    }
  }, [stacksQuery.data]);

  useEffect(() => {
    if (templateRevisionsQuery.data) {
      setSelectedTemplateRevisionID((current) => nextSelectedTemplateRevisionID(templateRevisionsQuery.data, current));
    }
  }, [templateRevisionsQuery.data]);

  useEffect(() => {
    setPlanRunID("");
    setApplyRunID("");
    setSelectedRunKind("plan");
  }, [selectedStackID]);

  useEffect(() => {
    const templates = stackQuery.data?.templates;
    if (templates) {
      setSelectedStackTemplateID((current) => nextSelectedStackTemplateID(templates, current));
    }
  }, [stackQuery.data]);

  useEffect(() => {
    if (!installedTemplate?.desired_template_revision_id) {
      return;
    }
    setSelectedTemplateRevisionID(installedTemplate.desired_template_revision_id);
  }, [installedTemplate?.id, installedTemplate?.desired_template_revision_id]);

  useEffect(() => {
    const nextVariables = variablesQuery.data;
    if (!nextVariables) {
      return;
    }
    setVariableValues((current) => {
      const next = installedTemplate
        ? variableValuesFromConfig(installedTemplate.config, nextVariables)
        : { ...current };
      for (const variable of nextVariables) {
        if (!(variable.name in next)) {
          next[variable.name] = "";
        }
      }
      return next;
    });
  }, [variablesQuery.data, installedTemplate]);

  useEffect(() => {
    if (!selectedTemplateRevision || selectedTemplateRevision.status !== "active") {
      setVariableValues({});
    }
  }, [selectedTemplateRevision?.id, selectedTemplateRevision?.status]);

  useEffect(() => {
    const data = registrationQuery.data;
    if (data?.status !== "completed" || !data.template_revision_id) {
      return;
    }
    queryClient.invalidateQueries({ queryKey: queryKeys.templateRevisions(tenantID) });
    setSelectedTemplateRevisionID(data.template_revision_id);
  }, [registrationQuery.data?.status, registrationQuery.data?.template_revision_id, queryClient]);

  useEffect(() => {
    const status = applyRunQuery.data?.status;
    if (!applyRunID || !selectedStackID || !status || !isTerminalRunStatus(status)) {
      return;
    }
    queryClient.invalidateQueries({ queryKey: queryKeys.stack(tenantID, selectedStackID) });
  }, [applyRunQuery.data?.status, applyRunID, selectedStackID, queryClient]);

  useEffect(() => {
    const nextLogs = logsQuery.data;
    if (nextLogs && nextLogs.length > 0 && !nextLogs.some((log) => log.phase === selectedPhase)) {
      setSelectedPhase(nextLogs[0].phase);
    }
  }, [logsQuery.data, selectedPhase]);

  const registerTemplateMutation = useRegisterTemplateMutation(tenantID);
  const createStackMutation = useCreateStackMutation(tenantID);
  const addTemplateToStackMutation = useAddTemplateToStackMutation(tenantID, selectedStackID);
  const updateStackTemplateConfigMutation = useUpdateStackTemplateConfigMutation(tenantID, selectedStackID);
  const upgradeStackTemplateMutation = useUpgradeStackTemplateMutation(tenantID, selectedStackID);
  const startTemplateRunMutation = useStartTemplateRunMutation(tenantID);
  const approveRunMutation = useApproveRunMutation(tenantID);
  const cancelRunMutation = useCancelRunMutation(tenantID);

  async function handleRegister(event: FormEvent) {
    event.preventDefault();
    await runAction("register", async () => {
      const next = await registerTemplateMutation.mutateAsync({
        repo_owner: repoOwner,
        repo_name: repoName,
        source_ref: sourceRef,
        root_path: rootPath
      });
      setRegistrationID(next.id);
      setSelectedTemplateRevisionID("");
      resetRunState();
    });
  }

  async function handleCreateStack(event: FormEvent) {
    event.preventDefault();
    await runAction("stack", async () => {
      const next = await createStackMutation.mutateAsync({
        name: stackName,
        slug: stackSlug,
        tags: {},
        default_credential_ids: []
      });
      setSelectedStackID(next.id);
      resetRunState();
    });
  }

  async function handleInstallTemplate() {
    if (!stack || !selectedTemplateRevision) {
      return;
    }

    await runAction("install", async () => {
      const config = configFromVariableValues(variables, variableValues);
      const next = await addTemplateToStackMutation.mutateAsync({
        template_revision_id: selectedTemplateRevision.id,
        selected_ref: selectedTemplateRevision.source_ref,
        config
      });
      setSelectedStackTemplateID(next.id);
      resetRunState();
    });
  }

  async function handleSaveStackTemplateConfig() {
    if (!installedTemplate || !canSaveConfig) {
      return;
    }

    await runAction("config", async () => {
      const next = await updateStackTemplateConfigMutation.mutateAsync({
        stackTemplateID: installedTemplate.id,
        body: { config: configFromVariableValues(variables, variableValues) }
      });
      setSelectedStackTemplateID(next.id);
      resetRunState();
    });
  }

  async function handleUpgradeStackTemplate() {
    if (!installedTemplate || !selectedTemplateRevision || !canUpgrade) {
      return;
    }

    await runAction("upgrade", async () => {
      const next = await upgradeStackTemplateMutation.mutateAsync({
        stackTemplateID: installedTemplate.id,
        body: {
          target_template_revision_id: selectedTemplateRevision.id,
          config: configFromVariableValues(variables, variableValues)
        }
      });
      setSelectedStackTemplateID(next.id);
      resetRunState();
    });
  }

  function handleSelectStackTemplate(stackTemplateID: string) {
    if (stackTemplateID === selectedStackTemplateID) {
      return;
    }
    setSelectedStackTemplateID(stackTemplateID);
    resetRunState();
  }

  async function handleStartRun(operation: "plan" | "apply") {
    if (!installedTemplate) {
      return;
    }

    await runAction(operation, async () => {
      const next = await startTemplateRunMutation.mutateAsync({
        stackTemplateID: installedTemplate.id,
        body: { operation }
      });
      if (operation === "apply") {
        setApplyRunID(next.id);
        setSelectedRunKind("apply");
      } else {
        setPlanRunID(next.id);
        setSelectedRunKind("plan");
      }
    });
  }

  async function handleApproveApply() {
    if (!applyRunID) {
      return;
    }

    await runAction("approve", async () => {
      await approveRunMutation.mutateAsync(applyRunID);
    });
  }

  async function handleCancelRun() {
    if (!currentRunID) {
      return;
    }

    await runAction("cancel", async () => {
      await cancelRunMutation.mutateAsync({
        runID: currentRunID,
        body: { reason: "canceled from workflow console" }
      });
    });
  }

  function resetRunState() {
    setPlanRunID("");
    setApplyRunID("");
    setSelectedRunKind("plan");
  }

  async function runAction(name: string, action: () => Promise<void>) {
    setBusyAction(name);
    setErrorMessage("");
    try {
      await action();
    } catch (error) {
      setErrorMessage(messageFromError(error));
    } finally {
      setBusyAction("");
    }
  }

  return (
    <main className="app-shell">
      <section className="workspace">
        <header className="workspace-header">
          <div>
            <p className="eyebrow">tflive</p>
            <h1>Terraform workflow console</h1>
          </div>
          <div className="runtime-fields">
            <div className="runtime-field">
              <span>Tenant</span>
              <span className="runtime-value" data-testid="tenant-context">{tenantID}</span>
            </div>
          </div>
        </header>

        {errorMessage && <div className="alert">{errorMessage}</div>}

        <div className="workflow-grid">
          <TemplateRegistryPanel
            templateRevisions={templateRevisions}
            selectedTemplateRevisionID={selectedTemplateRevisionID}
            onSelectTemplateRevision={setSelectedTemplateRevisionID}
            repoOwner={repoOwner}
            onRepoOwnerChange={setRepoOwner}
            repoName={repoName}
            onRepoNameChange={setRepoName}
            sourceRef={sourceRef}
            onSourceRefChange={setSourceRef}
            rootPath={rootPath}
            onRootPathChange={setRootPath}
            onSubmit={handleRegister}
            busy={busyAction === "register"}
            templateRevisionStatus={selectedTemplateRevision?.status ?? "not selected"}
            registrationStatus={registration?.status ?? "not started"}
            registrationErrorSummary={registration?.error_summary ?? ""}
          />

          <StackPanel
            stacks={stacks}
            selectedStackID={selectedStackID}
            onSelectStack={setSelectedStackID}
            stackName={stackName}
            onStackNameChange={setStackName}
            stackSlug={stackSlug}
            onStackSlugChange={setStackSlug}
            onSubmit={handleCreateStack}
            busy={busyAction === "stack"}
            stackStatus={stack?.slug || selectedStack?.slug || "not selected"}
            stackTemplates={stackTemplates}
            selectedStackTemplateID={selectedStackTemplateID}
            onSelectStackTemplate={handleSelectStackTemplate}
          />

          <InstalledTemplatePanel installedTemplate={installedTemplate} />

          <VariablesPanel
            variables={variables}
            variableValues={variableValues}
            onVariableValueChange={(name, value) => {
              setVariableValues((current) => ({ ...current, [name]: value }));
            }}
            canInstall={canInstall}
            onInstall={handleInstallTemplate}
            installBusy={busyAction === "install"}
            canSaveConfig={canSaveConfig}
            onSaveConfig={handleSaveStackTemplateConfig}
            configBusy={busyAction === "config"}
            canUpgrade={canUpgrade}
            onUpgrade={handleUpgradeStackTemplate}
            upgradeBusy={busyAction === "upgrade"}
            installedTemplateStatus={installedTemplate?.workspace_name ?? "not installed"}
          />

          <RunsPanel
            canPlan={canPlan}
            onPlan={() => handleStartRun("plan")}
            planBusy={busyAction === "plan"}
            canApply={canApply}
            onApply={() => handleStartRun("apply")}
            applyBusy={busyAction === "apply"}
            canApprove={canApprove}
            onApprove={handleApproveApply}
            approveBusy={busyAction === "approve"}
            canCancel={canCancel}
            onCancel={handleCancelRun}
            cancelBusy={busyAction === "cancel"}
            selectedRunKind={selectedRunKind}
            onSelectRunKind={setSelectedRunKind}
            currentRunStatus={currentRun?.status ?? "not started"}
            currentRunErrorSummary={currentRun?.error_summary ?? ""}
          />

          <RunLogsPanel logs={logs} selectedPhase={selectedPhase} onSelectPhase={setSelectedPhase} logBody={logBody} />

          <IdsPanel
            registrationID={registration?.id ?? "-"}
            templateRevisionID={selectedTemplateRevision?.id ?? registration?.template_revision_id ?? "-"}
            stackID={(stack?.id ?? selectedStackID) || "-"}
            stackTemplateID={installedTemplate?.id ?? "-"}
            desiredRevisionID={installedTemplate?.desired_template_revision_id ?? "-"}
            appliedRevisionID={installedTemplate?.last_applied_template_revision_id || "-"}
            planRunID={planRun?.id ?? "-"}
            applyRunID={applyRun?.id ?? "-"}
          />
        </div>
      </section>
    </main>
  );
}

function messageFromError(error: unknown): string {
  if (error instanceof ApiRequestError) {
    return error.message;
  }
  if (error instanceof Error) {
    return error.message;
  }
  return "Request failed";
}
