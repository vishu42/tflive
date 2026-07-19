import { CheckCircle2, Loader2 } from "lucide-react";
import type { FormEvent } from "react";
import type { Stack, StackTemplate } from "../../api/types";
import StatusRow from "../../shared/StatusRow";
import { stackLabel, stackTemplateLabel } from "./stackWorkflow";

interface StackPanelProps {
  stacks: Stack[];
  selectedStackID: string;
  onSelectStack: (stackID: string) => void;
  stackName: string;
  onStackNameChange: (value: string) => void;
  stackSlug: string;
  onStackSlugChange: (value: string) => void;
  onSubmit: (event: FormEvent) => void;
  busy: boolean;
  stackStatus: string;
  stackTemplates: StackTemplate[];
  selectedStackTemplateID: string;
  onSelectStackTemplate: (stackTemplateID: string) => void;
}

export default function StackPanel({
  stacks,
  selectedStackID,
  onSelectStack,
  stackName,
  onStackNameChange,
  stackSlug,
  onStackSlugChange,
  onSubmit,
  busy,
  stackStatus,
  stackTemplates,
  selectedStackTemplateID,
  onSelectStackTemplate
}: StackPanelProps) {
  return (
    <section className="panel">
      <h2>Stack</h2>
      <label className="selector-label">
        Saved stack
        <select value={selectedStackID} onChange={(event) => onSelectStack(event.target.value)}>
          <option value="">Select stack</option>
          {stacks.map((item) => (
            <option key={item.id} value={item.id}>
              {stackLabel(item)}
            </option>
          ))}
        </select>
      </label>
      <form className="form-grid" onSubmit={onSubmit}>
        <label>
          Name
          <input value={stackName} onChange={(event) => onStackNameChange(event.target.value)} />
        </label>
        <label>
          Slug
          <input value={stackSlug} onChange={(event) => onStackSlugChange(event.target.value)} placeholder="optional" />
        </label>
        <button className="primary-button" disabled={busy} type="submit">
          {busy ? <Loader2 size={16} className="spin" /> : <CheckCircle2 size={16} />}
          Create stack
        </button>
      </form>
      <StatusRow label="Stack" value={stackStatus} />
      <div className="stack-template-list">
        <div className="stack-template-list-header">
          <h3>Stack templates</h3>
          <span>{stackTemplates.length}</span>
        </div>
        {stackTemplates.length === 0 ? (
          <p className="muted">No stack templates installed</p>
        ) : (
          <div className="stack-template-items">
            {stackTemplates.map((item) => (
              <button
                className={item.id === selectedStackTemplateID ? "active" : ""}
                key={item.id}
                onClick={() => onSelectStackTemplate(item.id)}
                type="button"
              >
                <span>{stackTemplateLabel(item)}</span>
                <small>{item.desired_template_revision_id}</small>
              </button>
            ))}
          </div>
        )}
      </div>
    </section>
  );
}
