import type { StackTemplate } from "../../api/types";

export default function InstalledTemplatePanel({ installedTemplate }: { installedTemplate: StackTemplate | null }) {
  return (
    <section className="panel">
      <h2>Installed template</h2>
      {installedTemplate ? (
        <dl className="revision-grid">
          <dt>Source</dt>
          <dd>{installedTemplate.source_template_id || "-"}</dd>
          <dt>Desired</dt>
          <dd>{installedTemplate.desired_template_revision_id || "-"}</dd>
          <dt>Applied</dt>
          <dd>{installedTemplate.last_applied_template_revision_id || "-"}</dd>
          <dt>Last run</dt>
          <dd>{installedTemplate.last_applied_run_id || "-"}</dd>
        </dl>
      ) : (
        <p className="muted">No stack template selected</p>
      )}
    </section>
  );
}
