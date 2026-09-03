import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Loader2, Trash2 } from "lucide-react";
import { isTerminalRunStatus } from "../../api/polling";
import { queryKeys } from "../../api/queryKeys";
import { useStartTemplateRunMutation, useTemplateRunsQuery } from "../../api/queries";
import type { StackTemplate, TemplateRun } from "../../api/types";
import RequireCapability from "../../auth/RequireCapability";
import { tenantID } from "../../config";
import { canDestroyStackTemplate, isDestroyingStackTemplate } from "../stacks/stackWorkflow";

interface TemplateDestroyPanelProps {
  stackId: string;
  stackTemplate: StackTemplate;
}

// Destroy is separated from the plan/apply controls in TemplateRunActions and
// placed at the foot of the template screen on purpose: it is the one run
// operation that cannot be undone, so it does not belong under the cursor next
// to Apply. The two-step confirm is the second guard.
export default function TemplateDestroyPanel({ stackId, stackTemplate }: TemplateDestroyPanelProps) {
  const [errorMessage, setErrorMessage] = useState("");
  const [confirmDestroy, setConfirmDestroy] = useState(false);
  const queryClient = useQueryClient();

  // Shares the runs query key with TemplateRunActions, so this costs no extra
  // request — it is read here only to keep Destroy disabled while a run is
  // still in flight.
  const runsQuery = useTemplateRunsQuery(tenantID, stackTemplate.id);
  const runs = runsQuery.data ?? [];
  const runsReady = runsQuery.status === "success";
  const activeRun = runsReady ? runs.find((candidate) => !isTerminalRunStatus(candidate.status)) ?? null : null;

  const startRunMutation = useStartTemplateRunMutation(tenantID);
  const canDestroy = runsReady && canDestroyStackTemplate(stackTemplate) && !activeRun;
  const destroying = isDestroyingStackTemplate(stackTemplate);
  const destroyBusy = startRunMutation.isPending && startRunMutation.variables?.body.operation === "destroy";

  async function handleDestroy() {
    const currentRuns = queryClient.getQueryData<TemplateRun[]>(queryKeys.templateRuns(tenantID, stackTemplate.id));
    const currentRunActive = currentRuns?.some((candidate) => !isTerminalRunStatus(candidate.status)) ?? true;
    if (!canDestroy || !canDestroyStackTemplate(stackTemplate) || isDestroyingStackTemplate(stackTemplate) || currentRunActive) {
      setConfirmDestroy(false);
      return;
    }
    setErrorMessage("");
    try {
      await startRunMutation.mutateAsync({ stackTemplateID: stackTemplate.id, body: { operation: "destroy" } });
      setConfirmDestroy(false);
      await queryClient.invalidateQueries({ queryKey: queryKeys.templateRuns(tenantID, stackTemplate.id) });
    } catch (error) {
      setErrorMessage(error instanceof Error ? error.message : "Request failed");
    }
  }

  const controlProps = {
    canDestroy,
    destroying,
    onDestroy: handleDestroy,
    destroyBusy,
    confirmDestroy,
    onConfirmDestroy: () => setConfirmDestroy(true),
    onCancelConfirm: () => setConfirmDestroy(false)
  };

  return (
    <section className="panel danger-zone" data-testid="template-destroy-panel">
      <h2>Danger zone</h2>
      <p className="muted">Destroys all infrastructure this template manages. This cannot be undone.</p>
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
  confirmDestroy: boolean;
  onConfirmDestroy: () => void;
  onCancelConfirm: () => void;
  disabledReason?: string;
}

function DestroyControl({
  canDestroy,
  destroying,
  onDestroy,
  destroyBusy,
  confirmDestroy,
  onConfirmDestroy,
  onCancelConfirm,
  disabledReason
  }: DestroyControlProps) {
  const locked = Boolean(disabledReason);
  const confirmationDisabled = locked || destroying || !canDestroy || destroyBusy;
  return (
    <div className="button-row">
      {confirmDestroy ? (
        <>
          <button className="destructive-button" disabled={confirmationDisabled} onClick={onDestroy} type="button">
            {destroyBusy ? <Loader2 size={16} className="spin" /> : <Trash2 size={16} />}
            Confirm destroy
          </button>
          <button className="secondary-button" disabled={destroyBusy} onClick={onCancelConfirm} type="button">
            Cancel
          </button>
        </>
      ) : (
        <button className="destructive-button" disabled={locked || destroying || !canDestroy || destroyBusy} onClick={onConfirmDestroy} type="button">
          <Trash2 size={16} />
          Destroy
        </button>
      )}
      {disabledReason && (
        <p className="muted" data-testid="template-destroy-disabled-reason">
          {disabledReason}
        </p>
      )}
    </div>
  );
}
