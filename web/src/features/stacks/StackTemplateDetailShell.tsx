import { Loader2, RefreshCw } from "lucide-react";
import { matchPath, NavLink, Outlet, useLocation, useOutletContext, useParams } from "react-router-dom";
import { useStackQuery } from "../../api/queries";
import type { StackTemplate } from "../../api/types";
import RequireCapability from "../../auth/RequireCapability";
import { tenantID } from "../../config";
import { useQueryErrorBoundary } from "../../shared/queryErrorBoundary";
import { statusGlyph } from "../../shared/statusTone";
import { findSelectedStackTemplate, stackTemplateStatus } from "./stackWorkflow";

export interface StackTemplateOutletContext {
  stackId: string;
  stackTemplate: StackTemplate;
}

// The template every tab below this shell is about. Tabs only ever render
// inside the shell, which has already resolved the template, so this never
// has a missing value to handle.
export function useStackTemplateOutlet(): StackTemplateOutletContext {
  return useOutletContext<StackTemplateOutletContext>();
}

// Layout route for /stacks/:stackId/templates/:stackTemplateId. Resolves the
// template from the stack query the stack's route guard has already cached and
// hands it to the tab routes rendered into <Outlet />. Its state sits at the
// far end of its tab row.
//
// Runs is the first tab and the index redirects to it: operating the template
// is what people come here for. Run detail nests under runs/, so the Runs tab
// stays lit while reading one.
export default function StackTemplateDetailShell() {
  const { stackId = "", stackTemplateId = "" } = useParams<{ stackId: string; stackTemplateId: string }>();
  const { pathname } = useLocation();
  const stackQuery = useStackQuery(tenantID, stackId);
  const boundary = useQueryErrorBoundary(stackQuery.error);
  const stackTemplate = findSelectedStackTemplate(stackQuery.data?.templates ?? [], stackTemplateId);

  if (stackQuery.status === "pending") {
    return (
      <section className="stack-template-detail" data-testid="stack-template-loading">
        <p className="muted">
          <Loader2 size={16} className="spin" /> Loading template…
        </p>
      </section>
    );
  }

  if (stackQuery.status === "error") {
    if (boundary !== null) {
      return <>{boundary}</>;
    }
    return (
      <section className="stack-template-detail" data-testid="stack-template-error">
        <p className="muted">Something went wrong while loading the stack template.</p>
        <button className="primary-button" type="button" data-testid="stack-template-retry" onClick={() => stackQuery.refetch()}>
          <RefreshCw size={16} />
          Retry
        </button>
      </section>
    );
  }

  if (!stackTemplate) {
    return (
      <section className="stack-template-detail" data-testid="stack-template-missing">
        <p className="muted">That template is not installed on this stack.</p>
      </section>
    );
  }

  const context: StackTemplateOutletContext = { stackId, stackTemplate };
  const status = stackTemplateStatus(stackTemplate);
  const onRunPage = matchPath("/stacks/:stackId/templates/:stackTemplateId/runs/:runNumber", pathname) !== null;

  return (
    <section className="stack-template-detail" data-testid="stack-template-detail">
      <div className="stack-template-tabbar">
        <nav className="stack-detail-tabs" aria-label="Template sections">
          <NavLink to="runs">Runs</NavLink>
          <NavLink to="variables">Variables</NavLink>
          <RequireCapability capability="canManageAccess">
            <NavLink to="credentials">Credentials</NavLink>
          </RequireCapability>
          <NavLink to="settings">Settings</NavLink>
        </nav>
        {/* The template's state, on every tab; the description is on hover.
            Left off a run's page, where it would read as the run's state. */}
        {!onRunPage && (
          <span className={`status-tone status-tone--${status.tone}`} title={status.description} data-testid="stack-template-state">
            <span className="status-tone__glyph" aria-hidden="true">
              {statusGlyph(status.tone)}
            </span>
            {status.label}
          </span>
        )}
      </div>
      {/* Keyed on the template so a tab's local state, such as unsaved
          variable edits, never carries over to another template's page. */}
      <Outlet key={stackTemplate.id} context={context} />
    </section>
  );
}
