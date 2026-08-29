import type { TemplateRevision } from "../../api/types";

export interface TemplateRepositoryGroup {
  /** "owner/name" — stable within a tenant, so it doubles as the React key. */
  key: string;
  repoOwner: string;
  repoName: string;
  templateRevisions: TemplateRevision[];
}

/**
 * Buckets revisions by the repository they came from. Grouping is by
 * owner/name only, which is deliberately coarser than the backend's source
 * template identity (owner, name, root path, ref): every ref and root path of
 * one repository lands in the same group, and the rows carry that detail.
 *
 * Insertion order is preserved both across and within groups. The API returns
 * revisions newest-first, so a repository sorts by its most recent revision
 * and a freshly registered template surfaces at the top.
 */
export function groupTemplateRevisionsByRepository(templateRevisions: TemplateRevision[]): TemplateRepositoryGroup[] {
  const groups = new Map<string, TemplateRepositoryGroup>();
  for (const templateRevision of templateRevisions) {
    const key = `${templateRevision.repo_owner}/${templateRevision.repo_name}`;
    const group = groups.get(key);
    if (group) {
      group.templateRevisions.push(templateRevision);
      continue;
    }
    groups.set(key, {
      key,
      repoOwner: templateRevision.repo_owner,
      repoName: templateRevision.repo_name,
      templateRevisions: [templateRevision]
    });
  }
  return Array.from(groups.values());
}

export function templateRevisionLabel(templateRevision: TemplateRevision): string {
  const name = templateRevision.name.trim() || `${templateRevision.repo_owner}/${templateRevision.repo_name}`;
  const shortSHA = shortCommitSHA(templateRevision.resolved_commit_sha);
  return shortSHA ? `${name} @ ${templateRevision.source_ref} · ${shortSHA}` : `${name} @ ${templateRevision.source_ref}`;
}

/**
 * Labels a revision for display beneath its repository heading, where the
 * owner/name is already on screen and only the ref and commit distinguish one
 * row from the next.
 */
export function templateRevisionRefLabel(templateRevision: TemplateRevision): string {
  const shortSHA = shortCommitSHA(templateRevision.resolved_commit_sha);
  return shortSHA ? `${templateRevision.source_ref} · ${shortSHA}` : templateRevision.source_ref;
}

function shortCommitSHA(commitSHA: string): string {
  return commitSHA.trim().slice(0, 7);
}
