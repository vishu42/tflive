import { Link, Outlet } from "react-router-dom";
import { tenantID } from "../config";

const navItems: { to: string; label: string }[] = [
  { to: "/stacks", label: "Stacks" },
  { to: "/templates", label: "Templates" }
];

export default function AppShell() {
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
          <div className="identity-menu" data-testid="identity-menu" />
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
    </div>
  );
}
