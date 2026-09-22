import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Loader2, Trash2 } from "lucide-react";
import { useNavigate } from "react-router-dom";
import { isTerminalRunStatus } from "../../api/polling";
import { queryKeys } from "../../api/queryKeys";
import { useStartTemplateRunMutation, useTemplateRunsQuery } from "../../api/queries";
import type { StackTemplate, TemplateRun } from "../../api/types";
import RequireCapability from "../../auth/RequireCapability";
import { tenantID } from "../../config";
import { canDestroyStackTemplate, isDestroyingStackTemplate } from "../stacks/stackWorkflow";
import { isRunInFlightError } from "./runErrors";

interface TemplateDestroyPanelProps {
  stackId: string;
  stackTemplate: StackTemplate;
}

// Destroy lives on the Settings tab, a tab away from Plan, because it is the
// one operation that cannot be undone.
//
// Destroy here only plans the destroy: it shows what would be destroyed and
// destroys nothing. Destroying happens when that plan is approved, on the
// run's row, which is why the red "Destroy N resources" confirmation lives
// there. A destroy is never auto-approved, so that confirmation is always the
// irreversible click.
export default function TemplateDestroyPanel({ stackId, stackTemplate }: TemplateDestroyPanelProps) {
  const [errorMessage, setErrorMessage] = useState("");
  const queryClient = useQueryClient();
  const navigate = useNavigate();

  // Shares the runs query key with the Runs tab, so this costs no extra
  // request — it is read here only to keep Destroy disabled while a run is
  // still in flight.
  const runsQuery = useTemplateRunsQuery(tenantID, stackTemplate.id);
  const runs = runsQuery.data ?? [];
  const runsReady = runsQuery.status === "success";
  const activeRun = runsReady ? runs.find((candidate) => !isTerminalRunStatus(candidate.status)) ?? null : null;

  const startRunMutation = useStartTemplateRunMutation(tenantID);
  const canDestroy = runsReady && canDestroyStackTemplate(stackTemplate) && !activeRun;
  const destroying = isDestroyingStackTemplate(stackTemplate);
  const destroyBusy = startRunMutation.isPending;

  async function handleDestroy() {
    const currentRuns = queryClient.getQueryData<TemplateRun[]>(queryKeys.templateRuns(tenantID, stackTemplate.id));
    const currentRunActive = currentRuns?.some((candidate) => !isTerminalRunStatus(candidate.status)) ?? true;
    if (!canDestroy || currentRunActive) {
      return;
    }
    setErrorMessage("");
    try {
      await startRunMutation.mutateAsync({ stackTemplateID: stackTemplate.id, body: { operation: "destroy" } });
      await queryClient.invalidateQueries({ queryKey: queryKeys.templateRuns(tenantID, stackTemplate.id) });
      // The plan, and the button that destroys, are on the Runs tab.
      navigate(`/stacks/${stackId}/templates/${stackTemplate.id}/runs`);
    } catch (error) {
      setErrorMessage(error instanceof Error ? error.message : "Request failed");
      if (isRunInFlightError(error)) {
        await queryClient.invalidateQueries({ queryKey: queryKeys.templateRuns(tenantID, stackTemplate.id) });
      }
    }
  }

  const controlProps = { canDestroy, destroying, onDestroy: handleDestroy, destroyBusy };

  return (
    <section className="panel danger-zone" data-testid="template-destroy-panel">
      <h2>Danger zone</h2>
      <p className="muted">
        Plans the destruction of all infrastructure this template manages. Nothing is destroyed until that plan is approved, and then it
        cannot be undone.
      </p>
      {errorMessage && (
        <p className="error-text" data-testid="template-destroy-error">
          {errorMessage}
        </p>
      )}
      <RequireCapability
        capability="canOperate"
        stackId={stackId}
        fallback={<DestroyControl {...controlProps} disabledReason="Destroying requires operator access" />}
      >
        <DestroyControl {...controlProps} />
      </RequireCapability>
    </section>
  );
}

interface DestroyControlProps {
  canDestroy: boolean;
  destroying: boolean;
  onDestroy: () => void;
  destroyBusy: boolean;
  disabledReason?: string;
}

function DestroyControl({ canDestroy, destroying, onDestroy, destroyBusy, disabledReason }: DestroyControlProps) {
  const disabled = Boolean(disabledReason) || destroying || !canDestroy || destroyBusy;
  return (
    <div className="button-row">
      <button className="destructive-button" disabled={disabled} onClick={onDestroy} type="button">
        {destroyBusy ? <Loader2 size={16} className="spin" /> : <Trash2 size={16} />}
        Destroy
      </button>
      {disabledReason && (
        <p className="muted" data-testid="template-destroy-disabled-reason">
          {disabledReason}
        </p>
      )}
    </div>
  );
}
