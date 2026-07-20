import { Link, Outlet } from "react-router-dom";
import { useAuth } from "../auth/AuthContext";
import { tenantID } from "../config";

const navItems: { to: string; label: string }[] = [
  { to: "/stacks", label: "Stacks" },
  { to: "/templates", label: "Templates" }
];

const isDebug = import.meta.env.DEV || import.meta.env.VITE_DEBUG === "true";

export default function AppShell() {
  const { me, logout, status } = useAuth();

  return (
    <div className="app-frame">
      <header className="app-frame-header">
        <nav className="app-nav" aria-label="Primary">
          {navItems.map((item) => (
            <Link key={item.to} to={item.to}>
              {item.label}
            </Link>
          ))}
        </nav>
        <div className="app-frame-identity">
          <div className="identity-menu" data-testid="identity-menu">
            {status === "loading" && (
              <span data-testid="identity-loading">Loading...</span>
            )}
            {me && (
              <>
                <span data-testid="identity-display-name">{me.displayName}</span>
                <button type="button" data-testid="logout-button" onClick={logout}>
                  Log out
                </button>
              </>
            )}
          </div>
          <div className="runtime-field">
            <span>Tenant</span>
            <span className="runtime-value" data-testid="shell-tenant-context">
              {tenantID}
            </span>
          </div>
        </div>
      </header>
      <main className="app-frame-content">
        <Outlet />
      </main>
      {isDebug && (
        <details className="debug-panel" data-testid="debug-panel">
          <summary>IDs (debug)</summary>
          <dl className="id-grid">
            <dt>Auth status</dt>
            <dd data-testid="debug-auth-status">{status}</dd>
            <dt>User sub</dt>
            <dd data-testid="debug-user-sub">{me?.sub ?? "-"}</dd>
            <dt>Display name</dt>
            <dd data-testid="debug-display-name">{me?.displayName ?? "-"}</dd>
            <dt>Tenant</dt>
            <dd data-testid="debug-tenant">{tenantID}</dd>
            <dt>isPlatformAdmin</dt>
            <dd data-testid="debug-is-platform-admin">{me?.globalCapabilities.isPlatformAdmin.toString() ?? "-"}</dd>
            <dt>canCreateStack</dt>
            <dd data-testid="debug-can-create-stack">{me?.globalCapabilities.canCreateStack.toString() ?? "-"}</dd>
          </dl>
        </details>
      )}
    </div>
  );
}
