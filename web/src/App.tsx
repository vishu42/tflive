import {
  CheckCircle2,
  CircleStop,
  Loader2,
  Play,
  RefreshCw,
  Send,
  ShieldCheck,
  SquareTerminal,
  XCircle
} from "lucide-react";
import { useEffect, useState } from "react";
import type { FormEvent } from "react";
import {
  addTemplateToStack,
  ApiRequestError,
  approveRun,
  cancelRun,
  createStack,
  getTemplateRegistration,
  getTemplateRun,
  getTemplateRunLog,
  getTemplateVariables,
  listTemplateRunLogs,
  registerTemplate,
  startTemplateRun
} from "./api/client";
import type { Stack, StackTemplate, TemplateRegistration, TemplateRun, TemplateRunLog, TemplateVariable } from "./api/types";
import { isTerminalRegistrationStatus, isTerminalRunStatus, nextPollDelayMs } from "./polling";

export default function App() {
  const [tenantID, setTenantID] = useState("tenant_123");
  const [actor, setActor] = useState("user_123");
  const [repoOwner, setRepoOwner] = useState("hashicorp");
  const [repoName, setRepoName] = useState("");
  const [sourceRef, setSourceRef] = useState("main");
  const [rootPath, setRootPath] = useState(".");
  const [registration, setRegistration] = useState<TemplateRegistration | null>(null);
  const [variables, setVariables] = useState<TemplateVariable[]>([]);
  const [variableValues, setVariableValues] = useState<Record<string, string>>({});
  const [stackName, setStackName] = useState("Acme Prod");
  const [stackSlug, setStackSlug] = useState("");
  const [stack, setStack] = useState<Stack | null>(null);
  const [installedTemplate, setInstalledTemplate] = useState<StackTemplate | null>(null);
  const [planRun, setPlanRun] = useState<TemplateRun | null>(null);
  const [applyRun, setApplyRun] = useState<TemplateRun | null>(null);
  const [selectedRunKind, setSelectedRunKind] = useState<"plan" | "apply">("plan");
  const [logs, setLogs] = useState<TemplateRunLog[]>([]);
  const [selectedPhase, setSelectedPhase] = useState("plan");
  const [logBody, setLogBody] = useState("");
  const [busyAction, setBusyAction] = useState("");
  const [errorMessage, setErrorMessage] = useState("");

  const currentRun = selectedRunKind === "apply" ? applyRun : planRun;
  const canInstall = Boolean(registration?.status === "completed" && registration.template_id && stack);
  const canPlan = Boolean(installedTemplate && !planRun);
  const canApply = Boolean(installedTemplate && planRun?.status === "completed" && !applyRun);
  const canApprove = applyRun?.status === "waiting_approval";
  const canCancel = Boolean(currentRun && !isTerminalRunStatus(currentRun.status));

  useEffect(() => {
    if (!registration || isTerminalRegistrationStatus(registration.status)) {
      return;
    }

    let canceled = false;
    let failureCount = 0;
    let timer: number | undefined;

    const poll = async () => {
      if (canceled) {
        return;
      }
      try {
        const next = await getTemplateRegistration(tenantID, registration.id);
        if (!canceled) {
          setRegistration(next);
          failureCount = 0;
          if (!isTerminalRegistrationStatus(next.status)) {
            schedule();
          }
        }
      } catch (error) {
        if (!canceled) {
          failureCount += 1;
          setErrorMessage(messageFromError(error));
          schedule();
        }
      }
    };

    const schedule = () => {
      timer = window.setTimeout(poll, nextPollDelayMs(failureCount));
    };

    schedule();
    return () => {
      canceled = true;
      if (timer) {
        window.clearTimeout(timer);
      }
    };
  }, [registration, tenantID]);

  useEffect(() => {
    if (registration?.status !== "completed" || !registration.template_id) {
      return;
    }

    getTemplateVariables(tenantID, registration.template_id)
      .then((nextVariables) => {
        setVariables(nextVariables);
        setVariableValues((current) => {
          const next = { ...current };
          for (const variable of nextVariables) {
            if (!(variable.name in next)) {
              next[variable.name] = "";
            }
          }
          return next;
        });
      })
      .catch((error) => setErrorMessage(messageFromError(error)));
  }, [registration?.status, registration?.template_id, tenantID]);

  useEffect(() => {
    const run = currentRun;
    if (!run || isTerminalRunStatus(run.status)) {
      return;
    }

    let canceled = false;
    let failureCount = 0;
    let timer: number | undefined;

    const poll = async () => {
      if (canceled) {
        return;
      }
      try {
        const next = await getTemplateRun(tenantID, run.id);
        if (!canceled) {
          if (next.operation === "apply") {
            setApplyRun(next);
          } else {
            setPlanRun(next);
          }
          failureCount = 0;
          if (!isTerminalRunStatus(next.status)) {
            schedule();
          }
        }
      } catch (error) {
        if (!canceled) {
          failureCount += 1;
          setErrorMessage(messageFromError(error));
          schedule();
        }
      }
    };

    const schedule = () => {
      timer = window.setTimeout(poll, nextPollDelayMs(failureCount));
    };

    schedule();
    return () => {
      canceled = true;
      if (timer) {
        window.clearTimeout(timer);
      }
    };
  }, [currentRun?.id, currentRun?.status, tenantID]);

  useEffect(() => {
    if (!currentRun) {
      setLogs([]);
      setLogBody("");
      return;
    }

    listTemplateRunLogs(tenantID, currentRun.id)
      .then((nextLogs) => {
        setLogs(nextLogs);
        if (nextLogs.length > 0 && !nextLogs.some((log) => log.phase === selectedPhase)) {
          setSelectedPhase(nextLogs[0].phase);
        }
      })
      .catch(() => setLogs([]));
  }, [currentRun?.id, currentRun?.status, tenantID, selectedPhase]);

  useEffect(() => {
    if (!currentRun || !selectedPhase) {
      setLogBody("");
      return;
    }

    getTemplateRunLog(tenantID, currentRun.id, selectedPhase)
      .then(setLogBody)
      .catch(() => setLogBody(""));
  }, [currentRun?.id, currentRun?.status, selectedPhase, tenantID]);

  async function handleRegister(event: FormEvent) {
    event.preventDefault();
    await runAction("register", async () => {
      const next = await registerTemplate(tenantID, {
        repo_owner: repoOwner,
        repo_name: repoName,
        source_ref: sourceRef,
        root_path: rootPath,
        requested_by: actor
      });
      setRegistration(next);
      setVariables([]);
      setVariableValues({});
      setStack(null);
      setInstalledTemplate(null);
      setPlanRun(null);
      setApplyRun(null);
      setLogs([]);
      setLogBody("");
      setSelectedRunKind("plan");
    });
  }

  async function handleCreateStack(event: FormEvent) {
    event.preventDefault();
    await runAction("stack", async () => {
      const next = await createStack(tenantID, {
        name: stackName,
        slug: stackSlug,
        tags: {},
        default_credential_ids: [],
        actor
      });
      setStack(next);
    });
  }

  async function handleInstallTemplate() {
    if (!stack || !registration?.template_id) {
      return;
    }

    await runAction("install", async () => {
      const config = Object.fromEntries(
        variables
          .map((variable) => [variable.name, variableValues[variable.name] ?? ""])
          .filter(([, value]) => String(value).trim() !== "")
      );
      const next = await addTemplateToStack(tenantID, stack.id, {
        template_id: registration.template_id,
        selected_ref: registration.source_ref,
        config,
        actor
      });
      setInstalledTemplate(next);
      setPlanRun(null);
      setApplyRun(null);
      setLogs([]);
      setLogBody("");
      setSelectedRunKind("plan");
    });
  }

  async function handleStartRun(operation: "plan" | "apply") {
    if (!installedTemplate) {
      return;
    }

    await runAction(operation, async () => {
      const next = await startTemplateRun(tenantID, installedTemplate.id, {
        operation,
        trigger_actor: actor
      });
      if (operation === "apply") {
        setApplyRun(next);
        setSelectedRunKind("apply");
      } else {
        setPlanRun(next);
        setSelectedRunKind("plan");
      }
    });
  }

  async function handleApproveApply() {
    if (!applyRun) {
      return;
    }

    await runAction("approve", async () => {
      await approveRun(tenantID, applyRun.id, { approved_by: actor });
      const next = await getTemplateRun(tenantID, applyRun.id);
      setApplyRun(next);
    });
  }

  async function handleCancelRun() {
    if (!currentRun) {
      return;
    }

    await runAction("cancel", async () => {
      await cancelRun(tenantID, currentRun.id, {
        requested_by: actor,
        reason: "canceled from workflow console"
      });
      const next = await getTemplateRun(tenantID, currentRun.id);
      if (next.operation === "apply") {
        setApplyRun(next);
      } else {
        setPlanRun(next);
      }
    });
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
            <p className="eyebrow">Megagega</p>
            <h1>Terraform workflow console</h1>
          </div>
          <div className="runtime-fields">
            <label>
              Tenant
              <input value={tenantID} onChange={(event) => setTenantID(event.target.value)} />
            </label>
            <label>
              Actor
              <input value={actor} onChange={(event) => setActor(event.target.value)} />
            </label>
          </div>
        </header>

        {errorMessage && <div className="alert">{errorMessage}</div>}

        <div className="workflow-grid">
          <section className="panel">
            <h2>Template</h2>
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
            <StatusRow label="Registration" value={registration?.status ?? "not started"} />
            {registration?.error_summary && <p className="error-text">{registration.error_summary}</p>}
          </section>

          <section className="panel">
            <h2>Stack</h2>
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
            <StatusRow label="Stack" value={stack?.slug ?? "not created"} />
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
                      onChange={(event) => setVariableValues((current) => ({ ...current, [variable.name]: event.target.value }))}
                      placeholder={variable.type_expression || "value"}
                    />
                  </label>
                ))}
              </div>
            )}
            <button className="secondary-button" disabled={!canInstall || busyAction === "install"} onClick={handleInstallTemplate} type="button">
              {busyAction === "install" ? <Loader2 size={16} className="spin" /> : <ShieldCheck size={16} />}
              Install template
            </button>
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
              <dt>Template</dt>
              <dd>{registration?.template_id ?? "-"}</dd>
              <dt>Stack</dt>
              <dd>{stack?.id ?? "-"}</dd>
              <dt>Stack template</dt>
              <dd>{installedTemplate?.id ?? "-"}</dd>
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
    "running",
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
