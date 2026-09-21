import { Loader2 } from "lucide-react";
import { useState, type FormEvent } from "react";
import { useNavigate } from "react-router-dom";
import { useCreateStackMutation } from "../../api/queries";
import { tenantID } from "../../config";
import Breadcrumb from "../../shared/Breadcrumb";

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
    // Read by SessionProvider's proactive re-auth timer: it defers navigating
    // away while a `[data-unsaved='true']` element is mounted, so a
    // half-typed stack name is never wiped out by a background sign-in
    // redirect.
    <section data-unsaved={trimmed !== "" ? "true" : undefined}>
      <Breadcrumb items={[{ label: "Stacks", to: "/stacks" }, { label: "Create stack" }]} />

      <section className="panel">
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
    </section>
  );
}
