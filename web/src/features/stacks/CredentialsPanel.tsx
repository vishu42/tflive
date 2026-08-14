import { useState } from "react";
import { Loader2, Plus, Trash2 } from "lucide-react";
import type { CredentialMetadata } from "../../api/types";

interface CredentialsPanelProps {
  title: string;
  // Distinguishes this panel's scope from a near-identically-named one
  // elsewhere (e.g. the stack-scoped Environment tab). Optional so existing
  // call sites that have no such ambiguity to resolve need no change.
  subtitle?: string;
  credentials: CredentialMetadata[];
  loading: boolean;
  busy: boolean;
  onCreate: (name: string, value: string) => Promise<void>;
  onDelete: (id: string) => Promise<void>;
}

/** Renders write-only credential management for either a Stack or StackTemplate scope. */
export default function CredentialsPanel({ title, subtitle, credentials, loading, busy, onCreate, onDelete }: CredentialsPanelProps) {
  const [name, setName] = useState("");
  const [value, setValue] = useState("");
  const [error, setError] = useState("");

  /** Validates the form, sends the value once, then clears it from local state. */
  async function submit() {
    if (!name.trim() || !value) {
      setError("Name and value are required");
      return;
    }
    setError("");
    try {
      await onCreate(name.trim(), value);
      setName("");
      setValue("");
    } catch (nextError) {
      setError(nextError instanceof Error ? nextError.message : "Request failed");
    }
  }

  return (
    <section className="panel credentials-panel">
      <h2>{title}</h2>
      {subtitle && <p className="muted">{subtitle}</p>}
      <p className="muted">Values are write-only and injected only when Terraform runs. Use TF_VAR_NAME for Terraform variables; provider credentials keep their provider-specific names.</p>
      <div className="credentials-list">
        {loading ? <p className="muted"><Loader2 size={15} className="spin" /> Loading credentials…</p> : credentials.length === 0 ? <p className="muted">No credentials configured</p> : credentials.map((credential) => (
          <div className="credential-row" key={credential.id}>
            <code>{credential.name}</code>
            <span className="muted">configured</span>
            <button className="icon-button" disabled={busy} onClick={() => void onDelete(credential.id)} type="button" aria-label={`Delete ${credential.name}`}><Trash2 size={15} /></button>
          </div>
        ))}
      </div>
      <div className="credential-form">
        <input aria-label={`${title.replace(/ credentials$/, " credential")} name`} placeholder="AWS_ACCESS_KEY_ID" value={name} onChange={(event) => setName(event.target.value)} />
        <input aria-label={`${title.replace(/ credentials$/, " credential")} value`} placeholder="Secret value" type="password" value={value} onChange={(event) => setValue(event.target.value)} />
        <button className="secondary-button" disabled={busy} onClick={() => void submit()} type="button"><Plus size={15} /> Add</button>
      </div>
      {error && <p className="error-text">{error}</p>}
    </section>
  );
}
