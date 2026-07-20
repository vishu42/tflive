import { useEffect, useState } from "react";
import type { FormEvent } from "react";
import { Loader2, RefreshCw } from "lucide-react";
import { useQueryClient } from "@tanstack/react-query";
import { queryKeys } from "../../api/queryKeys";
import {
  useRegisterTemplateMutation,
  useTemplateRegistrationQuery,
  useTemplateRevisionsQuery
} from "../../api/queries";
import { tenantID } from "../../config";
import { useQueryErrorBoundary } from "../../shared/queryErrorBoundary";
import TemplateRegistryPanel from "./TemplateRegistryPanel";
import { findSelectedTemplateRevision, nextSelectedTemplateRevisionID } from "./templateWorkflow";

// Standalone /templates screen: registration form + revision listing,
// carrying over the register-then-poll flow the legacy console runs in
// App.tsx. Registration status arrives via useTemplateRegistrationQuery's
// polling; once it completes, the revisions list is invalidated and the
// freshly minted revision becomes the selection.
export default function TemplateRegistryScreen() {
  const [repoOwner, setRepoOwner] = useState("hashicorp");
  const [repoName, setRepoName] = useState("");
  const [sourceRef, setSourceRef] = useState("main");
  const [rootPath, setRootPath] = useState(".");
  const [registrationID, setRegistrationID] = useState("");
  const [selectedTemplateRevisionID, setSelectedTemplateRevisionID] = useState("");
  const [errorMessage, setErrorMessage] = useState("");

  const queryClient = useQueryClient();
  const templateRevisionsQuery = useTemplateRevisionsQuery(tenantID);
  const registrationQuery = useTemplateRegistrationQuery(tenantID, registrationID);
  const registerTemplateMutation = useRegisterTemplateMutation(tenantID);
  const boundary = useQueryErrorBoundary(templateRevisionsQuery.error);

  const templateRevisions = templateRevisionsQuery.data ?? [];
  const registration = registrationQuery.data ?? null;
  const selectedTemplateRevision = findSelectedTemplateRevision(templateRevisions, selectedTemplateRevisionID);

  useEffect(() => {
    if (templateRevisionsQuery.data) {
      setSelectedTemplateRevisionID((current) => nextSelectedTemplateRevisionID(templateRevisionsQuery.data, current));
    }
  }, [templateRevisionsQuery.data]);

  useEffect(() => {
    const data = registrationQuery.data;
    if (data?.status !== "completed" || !data.template_revision_id) {
      return;
    }
    queryClient.invalidateQueries({ queryKey: queryKeys.templateRevisions(tenantID) });
    setSelectedTemplateRevisionID(data.template_revision_id);
  }, [registrationQuery.data?.status, registrationQuery.data?.template_revision_id, queryClient]);

  async function handleRegister(event: FormEvent) {
    event.preventDefault();
    setErrorMessage("");
    try {
      const next = await registerTemplateMutation.mutateAsync({
        repo_owner: repoOwner,
        repo_name: repoName,
        source_ref: sourceRef,
        root_path: rootPath
      });
      setRegistrationID(next.id);
      setSelectedTemplateRevisionID("");
    } catch (error) {
      setErrorMessage(error instanceof Error ? error.message : "Request failed");
    }
  }

  if (templateRevisionsQuery.status === "pending") {
    return (
      <section className="template-registry-screen" data-testid="template-registry-loading">
        <p className="muted">
          <Loader2 size={16} className="spin" /> Loading templates…
        </p>
      </section>
    );
  }

  if (templateRevisionsQuery.status === "error") {
    if (boundary !== null) {
      return <>{boundary}</>;
    }
    return (
      <section className="template-registry-screen" data-testid="template-registry-error">
        <h1>Templates</h1>
        <p className="muted">Something went wrong while loading templates.</p>
        <button
          className="primary-button"
          type="button"
          data-testid="template-registry-retry"
          onClick={() => templateRevisionsQuery.refetch()}
        >
          <RefreshCw size={16} />
          Retry
        </button>
      </section>
    );
  }

  return (
    <section className="template-registry-screen">
      <header className="template-registry-header">
        <h1>Templates</h1>
      </header>
      {errorMessage && <div className="alert">{errorMessage}</div>}
      <TemplateRegistryPanel
        templateRevisions={templateRevisions}
        selectedTemplateRevisionID={selectedTemplateRevisionID}
        onSelectTemplateRevision={setSelectedTemplateRevisionID}
        repoOwner={repoOwner}
        onRepoOwnerChange={setRepoOwner}
        repoName={repoName}
        onRepoNameChange={setRepoName}
        sourceRef={sourceRef}
        onSourceRefChange={setSourceRef}
        rootPath={rootPath}
        onRootPathChange={setRootPath}
        onSubmit={handleRegister}
        busy={registerTemplateMutation.isPending}
        templateRevisionStatus={selectedTemplateRevision?.status ?? "not selected"}
        registrationStatus={registration?.status ?? "not started"}
        registrationErrorSummary={registration?.error_summary ?? ""}
      />
    </section>
  );
}
