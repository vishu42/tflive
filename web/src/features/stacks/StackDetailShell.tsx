import { NavLink, Outlet, useParams } from "react-router-dom";
import { useStackQuery } from "../../api/queries";
import RequireCapability from "../../auth/RequireCapability";
import { tenantID } from "../../config";
import SectionLabel from "../../shared/SectionLabel";

// Layout route for /stacks/:stackId. The parent RequireCapability canView
// route guard has already resolved (and cached) the stack query before this
// renders, so the header reads from cache without its own loading state.
// Tab contents are owned by the nested routes rendered into <Outlet />.
export default function StackDetailShell() {
  const { stackId = "" } = useParams<{ stackId: string }>();
  const stack = useStackQuery(tenantID, stackId).data?.stack;

  return (
    <section className="stack-detail-shell" data-testid="stack-detail-shell">
      <header className="stack-detail-header">
        <div className="page-header">
          <SectionLabel>Stack</SectionLabel>
          <h1>{stack?.name ?? stackId}</h1>
          {stack && <small className="muted">{stack.slug}</small>}
        </div>
      </header>
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
