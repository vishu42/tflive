import { Loader2, Send } from "lucide-react";
import type { FormEvent } from "react";
import type { TemplateRevision } from "../../api/types";
import StatusRow from "../../shared/StatusRow";
import { templateRevisionLabel } from "./templateWorkflow";

interface TemplateRegistryPanelProps {
  templateRevisions: TemplateRevision[];
  selectedTemplateRevisionID: string;
  onSelectTemplateRevision: (templateRevisionID: string) => void;
  repoOwner: string;
  onRepoOwnerChange: (value: string) => void;
  repoName: string;
  onRepoNameChange: (value: string) => void;
  sourceRef: string;
  onSourceRefChange: (value: string) => void;
  rootPath: string;
  onRootPathChange: (value: string) => void;
  onSubmit: (event: FormEvent) => void;
  busy: boolean;
  templateRevisionStatus: string;
  registrationStatus: string;
  registrationErrorSummary: string;
}

export default function TemplateRegistryPanel({
  templateRevisions,
  selectedTemplateRevisionID,
  onSelectTemplateRevision,
  repoOwner,
  onRepoOwnerChange,
  repoName,
  onRepoNameChange,
  sourceRef,
  onSourceRefChange,
  rootPath,
  onRootPathChange,
  onSubmit,
  busy,
  templateRevisionStatus,
  registrationStatus,
  registrationErrorSummary
}: TemplateRegistryPanelProps) {
  return (
    <section className="panel">
      <h2>Template</h2>
      <label className="selector-label">
        Saved template
        <select value={selectedTemplateRevisionID} onChange={(event) => onSelectTemplateRevision(event.target.value)}>
          <option value="">Select template</option>
          {templateRevisions.map((templateRevision) => (
            <option key={templateRevision.id} value={templateRevision.id}>
              {templateRevisionLabel(templateRevision)}
            </option>
          ))}
        </select>
      </label>
      <form className="form-grid" onSubmit={onSubmit}>
        <label>
          Owner
          <input value={repoOwner} onChange={(event) => onRepoOwnerChange(event.target.value)} />
        </label>
        <label>
          Repository
          <input value={repoName} onChange={(event) => onRepoNameChange(event.target.value)} />
        </label>
        <label>
          Ref
          <input value={sourceRef} onChange={(event) => onSourceRefChange(event.target.value)} />
        </label>
        <label>
          Root path
          <input value={rootPath} onChange={(event) => onRootPathChange(event.target.value)} />
        </label>
        <button className="primary-button" disabled={busy} type="submit">
          {busy ? <Loader2 size={16} className="spin" /> : <Send size={16} />}
          Register
        </button>
      </form>
      <StatusRow label="Template revision" value={templateRevisionStatus} />
      <StatusRow label="Registration" value={registrationStatus} />
      {registrationErrorSummary && <p className="error-text">{registrationErrorSummary}</p>}
    </section>
  );
}
