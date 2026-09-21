import { matchPath, NavLink, Outlet, useLocation, useParams } from "react-router-dom";
import { useStackQuery } from "../../api/queries";
import RequireCapability from "../../auth/RequireCapability";
import { tenantID } from "../../config";
import Breadcrumb from "../../shared/Breadcrumb";
import type { Crumb } from "../../shared/Breadcrumb";

// Pages below a tab, which the tabs alone cannot place. Each extends the
// trail past the stack name through the tab it was reached from.
const subPages: { pattern: string; label: string }[] = [
  { pattern: "/stacks/:stackId/template/new", label: "Add template" },
  { pattern: "/stacks/:stackId/template/:stackTemplateId/upgrade", label: "Change revision" },
  { pattern: "/stacks/:stackId/runs/:runId", label: "Run" }
];

// Layout route for /stacks/:stackId. The parent RequireCapability canView
// route guard has already resolved (and cached) the stack query before this
// renders, so the header reads from cache without its own loading state.
// Tab contents are owned by the nested routes rendered into <Outlet />.
export default function StackDetailShell() {
  const { stackId = "" } = useParams<{ stackId: string }>();
  const stack = useStackQuery(tenantID, stackId).data?.stack;
  const { pathname } = useLocation();
  const subPage = subPages.find((page) => matchPath(page.pattern, pathname));

  const stackCrumb: Crumb = { label: stack?.name ?? stackId };
  const crumbs: Crumb[] = subPage
    ? [
        { label: "Stacks", to: "/stacks" },
        { ...stackCrumb, to: `/stacks/${stackId}` },
        { label: "Template", to: `/stacks/${stackId}/template` },
        { label: subPage.label }
      ]
    : [{ label: "Stacks", to: "/stacks" }, stackCrumb];

  return (
    <section className="stack-detail-shell" data-testid="stack-detail-shell">
      <Breadcrumb items={crumbs} />
      <nav className="stack-detail-tabs" aria-label="Stack sections">
        <NavLink to="." end>
          Overview
        </NavLink>
        <NavLink to="template">Template</NavLink>
        <RequireCapability capability="canManageAccess">
          <NavLink to="environment">Environment</NavLink>
          <NavLink to="access">Access</NavLink>
        </RequireCapability>
      </nav>
      <Outlet />
    </section>
  );
}
