import type { TemplateRevision } from "../../api/types";
import { nextSelectedID } from "../../shared/listSelection";

export function nextSelectedTemplateRevisionID(templateRevisions: TemplateRevision[], selectedTemplateRevisionID: string): string {
  return nextSelectedID(templateRevisions, selectedTemplateRevisionID);
}

export function findSelectedTemplateRevision(templateRevisions: TemplateRevision[], selectedTemplateRevisionID: string): TemplateRevision | null {
  return templateRevisions.find((templateRevision) => templateRevision.id === selectedTemplateRevisionID) ?? null;
}

export function templateRevisionLabel(templateRevision: TemplateRevision): string {
  const name = templateRevision.name.trim() || `${templateRevision.repo_owner}/${templateRevision.repo_name}`;
  const shortSHA = shortCommitSHA(templateRevision.resolved_commit_sha);
  return shortSHA ? `${name} @ ${templateRevision.source_ref} · ${shortSHA}` : `${name} @ ${templateRevision.source_ref}`;
}

function shortCommitSHA(commitSHA: string): string {
  return commitSHA.trim().slice(0, 7);
}
