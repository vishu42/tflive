import { useState } from "react";
import { ArrowLeft, Loader2, RefreshCw } from "lucide-react";
import { Link, useNavigate, useParams } from "react-router-dom";
import {
  useStackQuery,
  useTemplateRevisionVariablesQuery,
  useTemplateRevisionsQuery,
  useUpgradeStackTemplateMutation
} from "../../api/queries";
import { tenantID } from "../../config";
import { useQueryErrorBoundary } from "../../shared/queryErrorBoundary";
import { templateRevisionLabel } from "../templates/templateWorkflow";
import {
  configFromVariableValues,
  findSelectedStackTemplate,
  isDestroyingStackTemplate,
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

  const stackTemplate = findSelectedStackTemplate(stackQuery.data?.templates ?? [], stackTemplateId);
  const candidates = upgradeCandidateRevisions(templateRevisionsQuery.data ?? [], stackTemplate);
  const targetRevision = candidates.find((revision) => revision.id === chosenTargetID) ?? candidates[0] ?? null;

  const currentVariablesQuery = useTemplateRevisionVariablesQuery(
    tenantID,
    stackTemplate?.desired_template_revision_id ?? ""
  );
  const targetVariablesQuery = useTemplateRevisionVariablesQuery(tenantID, targetRevision?.id ?? "");
  const partition = partitionUpgradeVariables(currentVariablesQuery.data ?? [], targetVariablesQuery.data ?? []);

  // Every query feeding the diff belongs here. A failed variables fetch leaves
  // data undefined, which the partition above reads as "that revision declares
  // no variables" — indistinguishable from a genuinely empty revision, and so
  // rendered as a confident (and wrong) diff. The loading guards below cannot
  // catch it either: a failed query is "error", not "pending", and isFetching
  // is false once retries are spent. Only treating it as an error does.
  const boundary = useQueryErrorBoundary(
    stackQuery.error ?? templateRevisionsQuery.error ?? currentVariablesQuery.error ?? targetVariablesQuery.error
  );

  // useTemplateRevisionVariablesQuery sets placeholderData: keepPreviousData,
  // so switching the target dropdown flips targetVariablesQuery.status to
  // "success" immediately while data still serves the *previous* target's
  // variables during the refetch — status/isPending can't see this, only
  // isFetching can (same distinction AddStackTemplateScreen's revision
  // switch relies on). Left ungated, the diff notes and the posted config
  // would both be built from the stale target while target_template_revision_id
  // (computed synchronously above) already points at the new one.
  //
  // currentVariablesQuery does not need the same treatment: its query key is
  // stackTemplate.desired_template_revision_id, which only changes if the
  // installed template itself changes — and nothing on this screen lets a
  // user do that without a fresh navigation to a different stackTemplateId
  // (no in-page control mutates it, unlike the target dropdown). A URL edit
  // or history navigation to a different stackTemplateId while this
  // component stays mounted could in principle hit the same window, but
  // that's not a control this screen exposes, so it's out of scope here.
  const targetVariablesRefreshing = targetRevision !== null && targetVariablesQuery.isFetching;

  // The config sent with the upgrade covers the target revision's variables
  // only, so anything the new revision dropped is excluded structurally.
  const targetVariables = [...partition.added, ...partition.carried];
  const baseValues = stackTemplate ? variableValuesFromConfig(stackTemplate.config, targetVariables) : {};
  const variableValues: Record<string, string> = {};
  for (const variable of targetVariables) {
    variableValues[variable.name] = editedValues[variable.name] ?? baseValues[variable.name] ?? "";
  }

  const upgradeStackTemplateMutation = useUpgradeStackTemplateMutation(tenantID, stackId);
  // Read by SessionProvider's proactive re-auth timer: it defers navigating
  // away while a `[data-unsaved='true']` element is mounted, so values typed
  // into the upgrade form are never wiped out by a background sign-in redirect.
  const hasUnsavedValues = Object.keys(editedValues).length > 0;

  async function handleUpgrade() {
    if (!stackTemplate || !targetRevision || targetVariablesRefreshing) {
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

  // Both variable queries are disabled (and so sit in "pending" forever)
  // whenever there is no revision id to fetch yet — no installed template, or
  // no upgrade candidate. Only wait on a query once something is actually
  // being fetched, mirroring StackTemplateScreen's waitingOnVariables guard.
  // Both sides matter: if only the target were gated, the partition below
  // would compare a real "current" list against an empty "target" list and
  // mislabel everything carried as newly added; if only the current side were
  // gated, first paint would compare an empty "current" against the real
  // target and mislabel everything as dropped.
  const waitingOnCurrentVariables = stackTemplate !== null && currentVariablesQuery.status === "pending";
  const waitingOnTargetVariables = targetRevision !== null && targetVariablesQuery.status === "pending";

  if (
    stackQuery.status === "pending" ||
    templateRevisionsQuery.status === "pending" ||
    waitingOnCurrentVariables ||
    waitingOnTargetVariables
  ) {
    return (
      <section className="upgrade-stack-template-screen" data-testid="upgrade-loading">
        <p className="muted">
          <Loader2 size={16} className="spin" /> Loading revisions…
        </p>
      </section>
    );
  }

  if (
    stackQuery.status === "error" ||
    templateRevisionsQuery.status === "error" ||
    currentVariablesQuery.status === "error" ||
    targetVariablesQuery.status === "error"
  ) {
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
            currentVariablesQuery.refetch();
            targetVariablesQuery.refetch();
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

  // Guards the direct-URL path: the template screen already hides/disables
  // the link that would bring a user here while a destroy is in progress
  // (see StackTemplateScreen.tsx), but this route is still reachable
  // directly. The backend rejects the upgrade with a 409 regardless — this
  // just states that outright instead of rendering a form for an action that
  // cannot succeed.
  if (isDestroyingStackTemplate(stackTemplate)) {
    return (
      <section className="upgrade-stack-template-screen" data-testid="upgrade-destroying">
        {backLink}
        <p className="muted">Destroy in progress — this template cannot change revision right now.</p>
      </section>
    );
  }

  return (
    <section
      className="upgrade-stack-template-screen"
      data-testid="upgrade-stack-template-screen"
      data-unsaved={hasUnsavedValues ? "true" : undefined}
    >
      <header className="page-header">
        {backLink}
        <h1>{stackTemplateLabel(stackTemplate)}</h1>
      </header>

      {errorMessage && (
        <div className="alert" data-testid="upgrade-stack-template-error">
          {errorMessage}
        </div>
      )}

      {candidates.length === 0 ? (
        <section className="panel" data-testid="upgrade-no-alternatives">
          <p className="muted">No other active revisions available.</p>
        </section>
      ) : (
        <section className="panel wide">
          <label className="selector-label">
            Revision to apply
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
          {targetVariablesRefreshing ? (
            // Inline, not the full-screen upgrade-loading state: a dropdown
            // change is a small update to an already-rendered screen, not a
            // fresh page load, and a full-screen spinner on every selection
            // would be a worse experience than the bug this suppresses. This
            // only replaces the parts that are wrong while the previous
            // target's data is still being served — the fields themselves
            // and the diff notes below — not the revision picker.
            <p className="muted" data-testid="upgrade-target-variables-loading">
              <Loader2 size={16} className="spin" /> Loading variables for the selected revision…
            </p>
          ) : (
            <>
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
            </>
          )}

          <div className="button-row form-actions">
            <button
              className="primary-button"
              type="button"
              disabled={targetVariablesRefreshing || upgradeStackTemplateMutation.isPending}
              onClick={handleUpgrade}
            >
              {upgradeStackTemplateMutation.isPending ? (
                <Loader2 size={16} className="spin" />
              ) : (
                <RefreshCw size={16} />
              )}
              Change revision
            </button>
          </div>
        </section>
      )}
    </section>
  );
}
