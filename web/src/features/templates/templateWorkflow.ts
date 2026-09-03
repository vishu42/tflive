import type { TemplateRevision } from "../../api/types";
import { statusTone } from "../../shared/statusTone";
import type { StatusTone } from "../../shared/statusTone";

/**
 * The tone to render a revision's status in, or null when it deserves no
 * indicator at all.
 *
 * `active` is the ordinary outcome of registering a template — validation
 * passed — so spelling it out on every row spends the right edge saying
 * "normal". Showing nothing makes its absence the signal and leaves `invalid`,
 * the one status a user has to act on, as the only thing that interrupts a
 * scan.
 *
 * The gate is on the literal status rather than its tone because `active` maps
 * to `settled`, which is where a future settled-but-not-active value would land
 * and silently vanish along with it.
 */
export function unsettledStatusTone(status: string): StatusTone | null {
  return status === "active" ? null : statusTone(status);
}

/**
 * One source template: the stable identity that revisions accumulate under.
 *
 * The backend keys `source_templates` on (tenant, repo owner, repo name, root
 * path, source ref) and stamps every revision with the resulting
 * `source_template_id`, so bucketing on that one field reproduces the server's
 * identity exactly — there is no need to rebuild the tuple here.
 *
 * The consequence that shapes both screens: `sourceRef` cannot vary within a
 * group (it is part of the key), while `resolved_commit_sha` is the only thing
 * that does. So the ref belongs on the template row and the commits belong on
 * the revisions behind it.
 */
export interface SourceTemplateGroup {
  /** Also the React key and the `:sourceTemplateId` route param. */
  sourceTemplateID: string;
  name: string;
  rootPath: string;
  sourceRef: string;
  /**
   * First in API order, which is `created_at desc, id desc` — most recently
   * registered, not necessarily the newest commit. `SourceTemplate`'s own
   * `latest_template_revision_id` is not exposed to the client.
   */
  latestRevision: TemplateRevision;
  revisions: TemplateRevision[];
}

export interface TemplateRepositoryGroup {
  /** "owner/name" — stable within a tenant, so it doubles as the React key. */
  key: string;
  repoOwner: string;
  repoName: string;
  sourceTemplates: SourceTemplateGroup[];
}

/**
 * Buckets revisions into templates, then templates into the repositories they
 * came from. One repository can hold several templates — a different root path
 * or a different ref is a different template.
 *
 * Insertion order is preserved at both levels. The API returns revisions
 * newest-first, so a template sorts by its most recent revision, a repository
 * by its most recently touched template, and a freshly registered revision
 * surfaces its template at the top.
 */
export function groupTemplatesByRepository(templateRevisions: TemplateRevision[]): TemplateRepositoryGroup[] {
  const repositories = new Map<string, TemplateRepositoryGroup>();
  const sourceTemplates = new Map<string, SourceTemplateGroup>();

  for (const templateRevision of templateRevisions) {
    const sourceTemplateID = sourceTemplateKey(templateRevision);
    const existing = sourceTemplates.get(sourceTemplateID);
    if (existing) {
      existing.revisions.push(templateRevision);
      continue;
    }

    // First sighting is the newest revision, so it names the template.
    const sourceTemplate: SourceTemplateGroup = {
      sourceTemplateID,
      name: templateDisplayName(templateRevision),
      rootPath: templateRevision.root_path,
      sourceRef: templateRevision.source_ref,
      latestRevision: templateRevision,
      revisions: [templateRevision]
    };
    sourceTemplates.set(sourceTemplateID, sourceTemplate);

    const repositoryKey = `${templateRevision.repo_owner}/${templateRevision.repo_name}`;
    const repository = repositories.get(repositoryKey);
    if (repository) {
      repository.sourceTemplates.push(sourceTemplate);
      continue;
    }
    repositories.set(repositoryKey, {
      key: repositoryKey,
      repoOwner: templateRevision.repo_owner,
      repoName: templateRevision.repo_name,
      sourceTemplates: [sourceTemplate]
    });
  }

  return Array.from(repositories.values());
}

export interface TemplateRevisionRepositoryGroup {
  key: string;
  repoOwner: string;
  repoName: string;
  templateRevisions: TemplateRevision[];
}

/**
 * Buckets revisions by repository, one row per revision.
 *
 * This is the shape the stack template pickers need: installing or upgrading
 * picks a *revision*, so collapsing revisions into templates the way the
 * registry screen does would hide the very thing being chosen. Grouping is by
 * owner/name only, so every ref and root path of one repository lands in the
 * same group and the rows carry that detail.
 *
 * Insertion order is preserved both across and within groups.
 */
export function groupTemplateRevisionsByRepository(
  templateRevisions: TemplateRevision[]
): TemplateRevisionRepositoryGroup[] {
  const groups = new Map<string, TemplateRevisionRepositoryGroup>();
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

/**
 * Every revision of one source template, newest-first. Reads from the same
 * tenant-wide list the registry screen renders — `canUpgradeStackTemplate`
 * already filters that list the same way, and no per-template endpoint exists.
 */
export function revisionsForSourceTemplate(
  templateRevisions: TemplateRevision[],
  sourceTemplateID: string
): TemplateRevision[] {
  if (sourceTemplateID === "") {
    return [];
  }
  return templateRevisions.filter((templateRevision) => sourceTemplateKey(templateRevision) === sourceTemplateID);
}

/**
 * What to call a template. `name` is inferred per revision and can be blank, so
 * fall through to the root path and finally the repository. A root path of "."
 * names nothing and is skipped.
 */
export function templateDisplayName(templateRevision: TemplateRevision): string {
  const name = templateRevision.name.trim();
  if (name !== "") {
    return name;
  }
  const rootPath = templateRevision.root_path.trim();
  if (rootPath !== "" && rootPath !== ".") {
    return rootPath;
  }
  return templateRevision.repo_name;
}

/**
 * The root path as a subline, or "" when it would add nothing — a module at the
 * repository root, or a path already serving as the template's name.
 */
export function templateRootPathLabel(rootPath: string, name: string): string {
  const trimmed = rootPath.trim();
  if (trimmed === "" || trimmed === "." || trimmed === name) {
    return "";
  }
  return trimmed;
}

export function revisionCountLabel(count: number): string {
  return count === 1 ? "1 revision" : `${count} revisions`;
}

export function templateRevisionLabel(templateRevision: TemplateRevision): string {
  const name = templateRevision.name.trim() || `${templateRevision.repo_owner}/${templateRevision.repo_name}`;
  const shortSHA = shortCommitSHA(templateRevision.resolved_commit_sha);
  return shortSHA ? `${name} @ ${templateRevision.source_ref} · ${shortSHA}` : `${name} @ ${templateRevision.source_ref}`;
}

/**
 * Labels a revision where the repository is already on screen and only the ref
 * and commit distinguish one row from the next. Still used by the stack
 * template pickers, which choose a revision rather than a template.
 */
export function templateRevisionRefLabel(templateRevision: TemplateRevision): string {
  const shortSHA = shortCommitSHA(templateRevision.resolved_commit_sha);
  return shortSHA ? `${templateRevision.source_ref} · ${shortSHA}` : templateRevision.source_ref;
}

export function shortCommitSHA(commitSHA: string): string {
  return commitSHA.trim().slice(0, 7);
}

/**
 * `source_template_id` is `not null default ''`, so a row written before the
 * backfill would carry an empty id. Falling back to the identity tuple the
 * backend keys on keeps those rows apart instead of collapsing every template
 * in the tenant into one. JSON encoding the tuple keeps the parts unambiguous
 * without picking a separator a repository name or path might contain.
 */
function sourceTemplateKey(templateRevision: TemplateRevision): string {
  if (templateRevision.source_template_id !== "") {
    return templateRevision.source_template_id;
  }
  return JSON.stringify([
    templateRevision.repo_owner,
    templateRevision.repo_name,
    templateRevision.root_path,
    templateRevision.source_ref
  ]);
}
