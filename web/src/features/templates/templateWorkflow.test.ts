import { describe, expect, it } from "vitest";
import type { TemplateRevision } from "../../api/types";
import {
  groupTemplatesByRepository,
  revisionCountLabel,
  revisionsForSourceTemplate,
  templateDisplayName,
  templateRevisionLabel,
  templateRootPathLabel,
  unsettledStatusTone
} from "./templateWorkflow";

describe("template workflow helpers", () => {
  it("formats menu labels from persisted template revision rows", () => {
    const selectedTemplateRevision = templateRevision({
      id: "template_123",
      repo_owner: "acme",
      repo_name: "infra",
      source_ref: "main",
      resolved_commit_sha: "abcdef1234567890",
      name: ""
    });

    expect(templateRevisionLabel(selectedTemplateRevision)).toBe("acme/infra @ main · abcdef1");
  });
});

describe("templateDisplayName", () => {
  it("prefers the revision's own name", () => {
    expect(templateDisplayName(templateRevision({ name: "Supabase" }))).toBe("Supabase");
  });

  it("falls back to the root path when the name is blank", () => {
    expect(templateDisplayName(templateRevision({ name: "  ", root_path: "environments/dev" }))).toBe(
      "environments/dev"
    );
  });

  it("falls back to the repository when the module sits at the root", () => {
    expect(templateDisplayName(templateRevision({ name: "", root_path: ".", repo_name: "infra" }))).toBe("infra");
  });
});

describe("templateRootPathLabel", () => {
  it("drops a path that would add nothing", () => {
    expect(templateRootPathLabel(".", "infra")).toBe("");
    expect(templateRootPathLabel("", "infra")).toBe("");
    // Already serving as the name, so repeating it below is noise.
    expect(templateRootPathLabel("environments/dev", "environments/dev")).toBe("");
  });

  it("keeps a path that locates the module", () => {
    expect(templateRootPathLabel("environments/dev", "Supabase")).toBe("environments/dev");
  });
});

describe("revisionCountLabel", () => {
  it("singularises one revision", () => {
    expect(revisionCountLabel(1)).toBe("1 revision");
    expect(revisionCountLabel(4)).toBe("4 revisions");
  });
});

describe("unsettledStatusTone", () => {
  it("gives active no indicator at all", () => {
    expect(unsettledStatusTone("active")).toBeNull();
  });

  it("tones every status a user might have to act on", () => {
    expect(unsettledStatusTone("invalid")).toBe("failed");
    expect(unsettledStatusTone("validating")).toBe("progress");
    expect(unsettledStatusTone("pending_validation")).toBe("waiting");
  });
});

describe("groupTemplatesByRepository", () => {
  it("returns no groups for an empty list", () => {
    expect(groupTemplatesByRepository([])).toEqual([]);
  });

  it("collapses every revision of one source template into a single template", () => {
    const groups = groupTemplatesByRepository([
      templateRevision({ id: "rev_2", source_template_id: "src_1", resolved_commit_sha: "bbbbbbb" }),
      templateRevision({ id: "rev_1", source_template_id: "src_1", resolved_commit_sha: "aaaaaaa" })
    ]);

    expect(groups).toHaveLength(1);
    expect(groups[0].sourceTemplates).toHaveLength(1);
    expect(groups[0].sourceTemplates[0].revisions.map((revision) => revision.id)).toEqual(["rev_2", "rev_1"]);
  });

  it("keeps different refs of one repository apart, because the ref is part of the identity", () => {
    const groups = groupTemplatesByRepository([
      templateRevision({ id: "rev_1", source_template_id: "src_main", source_ref: "main" }),
      templateRevision({ id: "rev_2", source_template_id: "src_v2", source_ref: "v2" })
    ]);

    expect(groups).toHaveLength(1);
    expect(groups[0].sourceTemplates.map((template) => template.sourceRef)).toEqual(["main", "v2"]);
  });

  it("separates repositories that share a name under different owners", () => {
    const groups = groupTemplatesByRepository([
      templateRevision({ id: "rev_1", source_template_id: "src_1", repo_owner: "acme", repo_name: "infra" }),
      templateRevision({ id: "rev_2", source_template_id: "src_2", repo_owner: "globex", repo_name: "infra" })
    ]);

    expect(groups.map((group) => group.key)).toEqual(["acme/infra", "globex/infra"]);
  });

  it("takes the newest revision as the template's name and latest", () => {
    // The API returns revisions created_at desc, so the first sighting is the
    // newest — and `name` is per-revision, so it can drift between them.
    const groups = groupTemplatesByRepository([
      templateRevision({ id: "rev_2", source_template_id: "src_1", name: "Renamed", resolved_commit_sha: "f17f983a" }),
      templateRevision({ id: "rev_1", source_template_id: "src_1", name: "Original", resolved_commit_sha: "c17860ca" })
    ]);

    const template = groups[0].sourceTemplates[0];
    expect(template.name).toBe("Renamed");
    expect(template.latestRevision.id).toBe("rev_2");
  });

  it("preserves the incoming newest-first order across templates and repositories", () => {
    const groups = groupTemplatesByRepository([
      templateRevision({ id: "rev_3", source_template_id: "src_a", repo_owner: "acme", repo_name: "newest" }),
      templateRevision({ id: "rev_2", source_template_id: "src_b", repo_owner: "acme", repo_name: "older" }),
      templateRevision({ id: "rev_1", source_template_id: "src_a", repo_owner: "acme", repo_name: "newest" })
    ]);

    expect(groups.map((group) => group.key)).toEqual(["acme/newest", "acme/older"]);
    expect(groups[0].sourceTemplates[0].revisions.map((revision) => revision.id)).toEqual(["rev_3", "rev_1"]);
  });

  it("keeps templates apart when the source template id is missing", () => {
    // `source_template_id` is `not null default ''`; without the tuple fallback
    // every such row would collapse into one bogus template.
    const groups = groupTemplatesByRepository([
      templateRevision({ id: "rev_1", source_template_id: "", root_path: "a" }),
      templateRevision({ id: "rev_2", source_template_id: "", root_path: "b" })
    ]);

    expect(groups[0].sourceTemplates).toHaveLength(2);
  });
});

describe("revisionsForSourceTemplate", () => {
  const revisions = [
    templateRevision({ id: "rev_3", source_template_id: "src_1" }),
    templateRevision({ id: "rev_2", source_template_id: "src_2" }),
    templateRevision({ id: "rev_1", source_template_id: "src_1" })
  ];

  it("returns only that template's revisions, in the order received", () => {
    expect(revisionsForSourceTemplate(revisions, "src_1").map((revision) => revision.id)).toEqual(["rev_3", "rev_1"]);
  });

  it("returns nothing for an unknown or empty id", () => {
    expect(revisionsForSourceTemplate(revisions, "src_missing")).toEqual([]);
    expect(revisionsForSourceTemplate(revisions, "")).toEqual([]);
  });
});

function templateRevision(overrides: Partial<TemplateRevision>): TemplateRevision {
  return {
    id: "template_123",
    tenant_id: "tenant_123",
    source_template_id: "source_template_123",
    repo_owner: "hashicorp",
    repo_name: "terraform-template",
    source_ref: "main",
    resolved_commit_sha: "abc123",
    root_path: ".",
    name: "Terraform Template",
    description: "",
    tags: [],
    status: "active",
    created_at: "2026-07-06T00:00:00Z",
    ...overrides
  };
}
