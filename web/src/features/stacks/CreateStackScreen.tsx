import { ArrowLeft, Loader2 } from "lucide-react";
import { useState, type FormEvent } from "react";
import { Link, useNavigate } from "react-router-dom";
import { useCreateStackMutation } from "../../api/queries";
import { tenantID } from "../../config";

export default function CreateStackScreen() {
  const navigate = useNavigate();
  const mutation = useCreateStackMutation(tenantID);
  const [name, setName] = useState("");
  const [errorMessage, setErrorMessage] = useState("");

  const trimmed = name.trim();

  const handleSubmit = async (event: FormEvent) => {
    event.preventDefault();
    if (trimmed === "") return;
    setErrorMessage("");
    try {
      const result = await mutation.mutateAsync({ name: trimmed, slug: "", tags: {}, default_credential_ids: [] });
      navigate(`/stacks/${result.id}`);
    } catch (err) {
      setErrorMessage(err instanceof Error ? err.message : "Failed to create stack");
    }
  };

  if (mutation.isSuccess && mutation.data) {
    return (
      <section data-testid="create-stack-success">
        <p className="muted">Redirecting to your new stack…</p>
      </section>
    );
  }

  return (
    <section className="panel">
      <header>
        <Link to="/stacks" className="muted" style={{ display: "inline-flex", alignItems: "center", gap: 6, marginBottom: 12 }}>
          <ArrowLeft size={14} />
          Back to stacks
        </Link>
        <h1>Create stack</h1>
      </header>

      {errorMessage && (
        <div className="alert" data-testid="create-stack-error">
          {errorMessage}
        </div>
      )}

      <form className="form-grid" onSubmit={handleSubmit}>
        <label>
          Name
          <input
            value={name}
            onChange={(event) => setName(event.target.value)}
            placeholder="e.g. Production"
            autoFocus
          />
        </label>
        <button className="primary-button" disabled={trimmed === "" || mutation.isPending} type="submit">
          {mutation.isPending ? (
            <>
              <Loader2 size={16} className="spin" />
              Creating…
            </>
          ) : (
            "Create stack"
          )}
        </button>
      </form>
    </section>
  );
}
