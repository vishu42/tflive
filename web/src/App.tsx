import {
  ArrowUpCircle,
  CheckCircle2,
  CircleStop,
  Loader2,
  Play,
  RefreshCw,
  Save,
  Send,
  ShieldCheck,
  SquareTerminal,
  XCircle
} from "lucide-react";
import { useEffect, useState } from "react";
import type { FormEvent } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { ApiRequestError } from "./api/client";
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
import { isTerminalRunStatus } from "./polling";
import {
  canUpgradeStackTemplate,
  canSaveStackTemplateConfig,
  configFromVariableValues,
  findSelectedStack,
  findSelectedStackTemplate,
  findSelectedTemplateRevision,
  nextSelectedStackID,
  nextSelectedStackTemplateID,
  nextSelectedTemplateRevisionID,
  stackLabel,
  stackTemplateLabel,
  templateRevisionLabel,
  variableValuesFromConfig
} from "./workflowState";

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
          <section className="panel">
            <h2>Template</h2>
            <label className="selector-label">
              Saved template
              <select value={selectedTemplateRevisionID} onChange={(event) => setSelectedTemplateRevisionID(event.target.value)}>
                <option value="">Select template</option>
                {templateRevisions.map((templateRevision) => (
                  <option key={templateRevision.id} value={templateRevision.id}>
                    {templateRevisionLabel(templateRevision)}
                  </option>
                ))}
              </select>
            </label>
            <form className="form-grid" onSubmit={handleRegister}>
              <label>
                Owner
                <input value={repoOwner} onChange={(event) => setRepoOwner(event.target.value)} />
              </label>
              <label>
                Repository
                <input value={repoName} onChange={(event) => setRepoName(event.target.value)} />
              </label>
              <label>
                Ref
                <input value={sourceRef} onChange={(event) => setSourceRef(event.target.value)} />
              </label>
              <label>
                Root path
                <input value={rootPath} onChange={(event) => setRootPath(event.target.value)} />
              </label>
              <button className="primary-button" disabled={busyAction === "register"} type="submit">
                {busyAction === "register" ? <Loader2 size={16} className="spin" /> : <Send size={16} />}
                Register
              </button>
            </form>
            <StatusRow label="Template revision" value={selectedTemplateRevision?.status ?? "not selected"} />
            <StatusRow label="Registration" value={registration?.status ?? "not started"} />
            {registration?.error_summary && <p className="error-text">{registration.error_summary}</p>}
          </section>

          <section className="panel">
            <h2>Stack</h2>
            <label className="selector-label">
              Saved stack
              <select value={selectedStackID} onChange={(event) => setSelectedStackID(event.target.value)}>
                <option value="">Select stack</option>
                {stacks.map((item) => (
                  <option key={item.id} value={item.id}>
                    {stackLabel(item)}
                  </option>
                ))}
              </select>
            </label>
            <form className="form-grid" onSubmit={handleCreateStack}>
              <label>
                Name
                <input value={stackName} onChange={(event) => setStackName(event.target.value)} />
              </label>
              <label>
                Slug
                <input value={stackSlug} onChange={(event) => setStackSlug(event.target.value)} placeholder="optional" />
              </label>
              <button className="primary-button" disabled={busyAction === "stack"} type="submit">
                {busyAction === "stack" ? <Loader2 size={16} className="spin" /> : <CheckCircle2 size={16} />}
                Create stack
              </button>
            </form>
            <StatusRow label="Stack" value={stack?.slug || selectedStack?.slug || "not selected"} />
            <div className="stack-template-list">
              <div className="stack-template-list-header">
                <h3>Stack templates</h3>
                <span>{stackTemplates.length}</span>
              </div>
              {stackTemplates.length === 0 ? (
                <p className="muted">No stack templates installed</p>
              ) : (
                <div className="stack-template-items">
                  {stackTemplates.map((item) => (
                    <button
                      className={item.id === selectedStackTemplateID ? "active" : ""}
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
            </div>
          </section>

          <section className="panel">
            <h2>Installed template</h2>
            {installedTemplate ? (
              <dl className="revision-grid">
                <dt>Source</dt>
                <dd>{installedTemplate.source_template_id || "-"}</dd>
                <dt>Desired</dt>
                <dd>{installedTemplate.desired_template_revision_id || "-"}</dd>
                <dt>Applied</dt>
                <dd>{installedTemplate.last_applied_template_revision_id || "-"}</dd>
                <dt>Last run</dt>
                <dd>{installedTemplate.last_applied_run_id || "-"}</dd>
              </dl>
            ) : (
              <p className="muted">No stack template selected</p>
            )}
          </section>

          <section className="panel wide">
            <h2>Variables</h2>
            {variables.length === 0 ? (
              <p className="muted">No variables loaded</p>
            ) : (
              <div className="variable-grid">
                {variables.map((variable) => (
                  <label key={variable.name}>
                    {variable.name}
                    {variable.required ? " *" : ""}
                    <input
                      value={variableValues[variable.name] ?? ""}
                      onChange={(event) => {
                        setVariableValues((current) => ({ ...current, [variable.name]: event.target.value }))
                      }}
                      placeholder={variable.type_expression || "value"}
                    />
                  </label>
                ))}
              </div>
            )}
            <div className="button-row form-actions">
              <button className="secondary-button" disabled={!canInstall || busyAction === "install"} onClick={handleInstallTemplate} type="button">
                {busyAction === "install" ? <Loader2 size={16} className="spin" /> : <ShieldCheck size={16} />}
                Install template
              </button>
              <button className="secondary-button" disabled={!canSaveConfig || busyAction === "config"} onClick={handleSaveStackTemplateConfig} type="button">
                {busyAction === "config" ? <Loader2 size={16} className="spin" /> : <Save size={16} />}
                Save config
              </button>
              <button className="primary-button" disabled={!canUpgrade || busyAction === "upgrade"} onClick={handleUpgradeStackTemplate} type="button">
                {busyAction === "upgrade" ? <Loader2 size={16} className="spin" /> : <ArrowUpCircle size={16} />}
                Upgrade
              </button>
            </div>
            <StatusRow label="Installed template" value={installedTemplate?.workspace_name ?? "not installed"} />
          </section>

          <section className="panel">
            <h2>Runs</h2>
            <div className="button-row">
              <button className="primary-button" disabled={!canPlan || busyAction === "plan"} onClick={() => handleStartRun("plan")} type="button">
                {busyAction === "plan" ? <Loader2 size={16} className="spin" /> : <Play size={16} />}
                Plan
              </button>
              <button className="primary-button" disabled={!canApply || busyAction === "apply"} onClick={() => handleStartRun("apply")} type="button">
                {busyAction === "apply" ? <Loader2 size={16} className="spin" /> : <Play size={16} />}
                Apply
              </button>
              <button className="secondary-button" disabled={!canApprove || busyAction === "approve"} onClick={handleApproveApply} type="button">
                {busyAction === "approve" ? <Loader2 size={16} className="spin" /> : <ShieldCheck size={16} />}
                Approve
              </button>
              <button className="secondary-button" disabled={!canCancel || busyAction === "cancel"} onClick={handleCancelRun} type="button">
                {busyAction === "cancel" ? <Loader2 size={16} className="spin" /> : <CircleStop size={16} />}
                Cancel
              </button>
            </div>
            <div className="run-tabs">
              <button className={selectedRunKind === "plan" ? "active" : ""} onClick={() => setSelectedRunKind("plan")} type="button">
                Plan
              </button>
              <button className={selectedRunKind === "apply" ? "active" : ""} onClick={() => setSelectedRunKind("apply")} type="button">
                Apply
              </button>
            </div>
            <StatusRow label="Current run" value={currentRun?.status ?? "not started"} />
            {currentRun?.error_summary && <p className="error-text">{currentRun.error_summary}</p>}
          </section>

          <section className="panel wide log-panel">
            <h2>
              <SquareTerminal size={18} />
              Logs
            </h2>
            <div className="phase-row">
              {logs.map((log) => (
                <button className={selectedPhase === log.phase ? "active" : ""} key={log.phase} onClick={() => setSelectedPhase(log.phase)} type="button">
                  {log.phase}
                </button>
              ))}
            </div>
            <pre>{logBody || "No log body"}</pre>
          </section>

          <section className="panel wide">
            <h2>IDs</h2>
            <dl className="id-grid">
              <dt>Registration</dt>
              <dd>{registration?.id ?? "-"}</dd>
              <dt>Template revision</dt>
              <dd>{selectedTemplateRevision?.id ?? registration?.template_revision_id ?? "-"}</dd>
              <dt>Stack</dt>
              <dd>{(stack?.id ?? selectedStackID) || "-"}</dd>
              <dt>Stack template</dt>
              <dd>{installedTemplate?.id ?? "-"}</dd>
              <dt>Desired revision</dt>
              <dd>{installedTemplate?.desired_template_revision_id ?? "-"}</dd>
              <dt>Applied revision</dt>
              <dd>{installedTemplate?.last_applied_template_revision_id || "-"}</dd>
              <dt>Plan run</dt>
              <dd>{planRun?.id ?? "-"}</dd>
              <dt>Apply run</dt>
              <dd>{applyRun?.id ?? "-"}</dd>
            </dl>
          </section>
        </div>
      </section>
    </main>
  );
}

function StatusRow({ label, value }: { label: string; value: string }) {
  let icon = <CheckCircle2 size={15} />;
  if (value === "failed" || value === "invalid") {
    icon = <XCircle size={15} />;
  } else if (value.startsWith("not ") || inProgressStatus(value)) {
    icon = <RefreshCw size={15} />;
  }

  return (
    <div className="status-row">
      <span>{label}</span>
      <strong>
        {icon}
        {value}
      </strong>
    </div>
  );
}

function inProgressStatus(value: string): boolean {
  return [
    "pending",
    "pending_validation",
    "running",
    "validating",
    "queued",
    "locked",
    "workspace_prepared",
    "source_fetched",
    "init",
    "workspace_selected",
    "planned",
    "waiting_approval",
    "approved",
    "apply_started",
    "applied",
    "destroy_started",
    "destroyed",
    "cancel_requested",
    "canceling",
    "lock_released"
  ].includes(value);
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
