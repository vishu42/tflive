export default function App() {
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
              <input defaultValue="tenant_123" aria-label="Tenant ID" />
            </label>
            <label>
              Actor
              <input defaultValue="user_123" aria-label="Actor ID" />
            </label>
          </div>
        </header>
        <div className="empty-panel">No active workflow</div>
      </section>
    </main>
  );
}
