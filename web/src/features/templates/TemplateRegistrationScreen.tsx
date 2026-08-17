import { useEffect, useState } from "react";
import type { FormEvent } from "react";
import { ArrowLeft, Loader2, Send } from "lucide-react";
import { useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "react-router-dom";
import { isTerminalRegistrationStatus } from "../../api/polling";
import { queryKeys } from "../../api/queryKeys";
import { useRegisterTemplateMutation, useTemplateRegistrationQuery } from "../../api/queries";
import { tenantID } from "../../config";
import StatusRow from "../../shared/StatusRow";

// /templates/new owns the register-then-poll flow. Registration is
// asynchronous: the POST only returns a registration ID, and the revision the
// list screen needs to highlight does not exist until polling reports
// "completed". So the screen stays put and reports progress, then redirects to
// /templates?selected=<revisionID> once there is something to select. A
// terminal failure keeps the user here with the inputs intact to correct.
export default function TemplateRegistrationScreen() {
  const [repoOwner, setRepoOwner] = useState("hashicorp");
  const [repoName, setRepoName] = useState("");
  const [sourceRef, setSourceRef] = useState("main");
  const [rootPath, setRootPath] = useState(".");
  const [registrationID, setRegistrationID] = useState("");
  const [errorMessage, setErrorMessage] = useState("");

  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const registrationQuery = useTemplateRegistrationQuery(tenantID, registrationID);
  const registerTemplateMutation = useRegisterTemplateMutation(tenantID);

  const registration = registrationQuery.data ?? null;
  const registrationStatus = registration?.status ?? null;

  useEffect(() => {
    const data = registrationQuery.data;
    if (data?.status !== "completed" || !data.template_revision_id) {
      return;
    }
    queryClient.invalidateQueries({ queryKey: queryKeys.templateRevisions(tenantID) });
    navigate(`/templates?selected=${encodeURIComponent(data.template_revision_id)}`, { replace: true });
  }, [registrationQuery.data?.status, registrationQuery.data?.template_revision_id, queryClient, navigate]);

  // Busy spans the whole flow, not just the POST: the request is in flight, or
  // a registration is being polled and has not reached a terminal status.
  const settled = registrationStatus !== null && isTerminalRegistrationStatus(registrationStatus);
  const busy = registerTemplateMutation.isPending || (registrationID !== "" && !settled);

  async function handleRegister(event: FormEvent) {
    event.preventDefault();
    setErrorMessage("");
    // Drop any previous attempt so a retry after a failure polls the new
    // registration rather than reading the old terminal one.
    setRegistrationID("");
    try {
      const next = await registerTemplateMutation.mutateAsync({
        repo_owner: repoOwner,
        repo_name: repoName,
        source_ref: sourceRef,
        root_path: rootPath
      });
      setRegistrationID(next.id);
    } catch (error) {
      setErrorMessage(error instanceof Error ? error.message : "Request failed");
    }
  }

  return (
    <section className="template-registration-screen">
      {/* Title above the card, not inside it — see CreateStackScreen. */}
      <header className="page-header">
        <Link to="/templates" className="muted back-link" data-testid="template-registration-back">
          <ArrowLeft size={14} />
          Back to templates
        </Link>
        <h1>Register template</h1>
      </header>

      <section className="panel">
        {errorMessage && (
          <div className="alert" data-testid="template-registration-error">
            {errorMessage}
          </div>
        )}

        <form className="form-grid" onSubmit={handleRegister}>
          <label>
            Owner
            <input value={repoOwner} onChange={(event) => setRepoOwner(event.target.value)} />
          </label>
          <label>
            Repository
            <input value={repoName} onChange={(event) => setRepoName(event.target.value)} />
          </label>
          <label>
            Ref
            <input value={sourceRef} onChange={(event) => setSourceRef(event.target.value)} />
          </label>
          <label>
            Root path
            <input value={rootPath} onChange={(event) => setRootPath(event.target.value)} />
          </label>
          <button className="primary-button" disabled={busy} type="submit">
            {busy ? <Loader2 size={16} className="spin" /> : <Send size={16} />}
            Register
          </button>
        </form>

        <StatusRow label="Registration" value={registrationStatus ?? "not started"} />
        {registration?.error_summary && <p className="error-text">{registration.error_summary}</p>}
      </section>
    </section>
  );
}
