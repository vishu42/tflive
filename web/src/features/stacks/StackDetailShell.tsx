import { matchPath, NavLink, Outlet, useLocation, useParams } from "react-router-dom";
import { useStackQuery } from "../../api/queries";
import RequireCapability from "../../auth/RequireCapability";
import { tenantID } from "../../config";
import Breadcrumb from "../../shared/Breadcrumb";
import type { Crumb } from "../../shared/Breadcrumb";
import { statusGlyph } from "../../shared/statusTone";
import { stackTemplateLabel, stackTemplateStatus } from "./stackWorkflow";

// Where a page sits below the Templates tab, which the stack tabs alone
// cannot say. The template's own name is a crumb once you are on its page;
// run detail and change revision extend the trail past it.
function templateCrumbs(stackId: string, pathname: string, templateLabel: (id: string) => string): Crumb[] | null {
  const templates: Crumb = { label: "Templates", to: `/stacks/${stackId}/templates` };

  if (matchPath("/stacks/:stackId/templates/new", pathname)) {
    return [templates, { label: "Add template" }];
  }
  const run = matchPath("/stacks/:stackId/templates/:stackTemplateId/runs/:runNumber", pathname);
  if (run) {
    const id = run.params.stackTemplateId ?? "";
    return [templates, { label: templateLabel(id), to: `/stacks/${stackId}/templates/${id}` }, { label: `Run #${run.params.runNumber}` }];
  }
  const upgrade = matchPath("/stacks/:stackId/templates/:stackTemplateId/upgrade", pathname);
  if (upgrade) {
    const id = upgrade.params.stackTemplateId ?? "";
    return [templates, { label: templateLabel(id), to: `/stacks/${stackId}/templates/${id}` }, { label: "Change revision" }];
  }
  const template = matchPath({ path: "/stacks/:stackId/templates/:stackTemplateId", end: false }, pathname);
  if (template) {
    return [templates, { label: templateLabel(template.params.stackTemplateId ?? "") }];
  }
  return null;
}

// Layout route for /stacks/:stackId. The parent RequireCapability canView
// route guard has already resolved (and cached) the stack query before this
// renders, so the header reads from cache without its own loading state.
// Tab contents are owned by the nested routes rendered into <Outlet />.
export default function StackDetailShell() {
  const { stackId = "" } = useParams<{ stackId: string }>();
  const stackData = useStackQuery(tenantID, stackId).data;
  const stack = stackData?.stack;
  const { pathname } = useLocation();

  const templateLabel = (id: string) => {
    const stackTemplate = stackData?.templates.find((candidate) => candidate.id === id);
    return stackTemplate ? stackTemplateLabel(stackTemplate) : id;
  };
  const trail = templateCrumbs(stackId, pathname, templateLabel);

  // On a template's own page the template's tabs replace the stack's, the way
  // a project's tabs replace a team's: one row of tabs, for the thing you are
  // looking at, and the breadcrumb for the way back up.
  const templateMatch = matchPath({ path: "/stacks/:stackId/templates/:stackTemplateId", end: false }, pathname);
  const currentTemplate =
    templateMatch && templateMatch.params.stackTemplateId !== "new"
      ? stackData?.templates.find((candidate) => candidate.id === templateMatch.params.stackTemplateId) ?? null
      : null;
  // The state pill belongs to the template, so it shows on the template's own
  // tabs and not on a run below it, where it would read as the run's state.
  const onRunPage = matchPath("/stacks/:stackId/templates/:stackTemplateId/runs/:runNumber", pathname) !== null;
  const currentTemplateStatus = currentTemplate && !onRunPage ? stackTemplateStatus(currentTemplate) : null;

  const stackCrumb: Crumb = { label: stack?.name ?? stackId };
  const crumbs: Crumb[] = trail
    ? [{ label: "Stacks", to: "/stacks" }, { ...stackCrumb, to: `/stacks/${stackId}` }, ...trail]
    : [{ label: "Stacks", to: "/stacks" }, stackCrumb];

  return (
    <section className="stack-detail-shell" data-testid="stack-detail-shell">
      <Breadcrumb
        items={crumbs}
        detail={
          currentTemplateStatus && (
            <span
              className={`status-tone status-tone--${currentTemplateStatus.tone}`}
              title={currentTemplateStatus.description}
              data-testid="stack-template-state"
            >
              <span className="status-tone__glyph" aria-hidden="true">
                {statusGlyph(currentTemplateStatus.tone)}
              </span>
              {currentTemplateStatus.label}
            </span>
          )
        }
      />
      {!currentTemplate && (
        <nav className="stack-detail-tabs" aria-label="Stack sections">
          <NavLink to="." end>
            Overview
          </NavLink>
          <NavLink to="templates">Templates</NavLink>
          <RequireCapability capability="canManageAccess">
            <NavLink to="environment">Environment</NavLink>
            <NavLink to="access">Access</NavLink>
          </RequireCapability>
        </nav>
      )}
      <Outlet />
    </section>
  );
}
