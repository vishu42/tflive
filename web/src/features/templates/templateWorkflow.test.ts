import { describe, expect, it } from "vitest";
import type { TemplateRevision } from "../../api/types";
import {
  groupTemplateRevisionsByRepository,
  templateRevisionLabel
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

describe("groupTemplateRevisionsByRepository", () => {
  it("returns no groups for an empty list", () => {
    expect(groupTemplateRevisionsByRepository([])).toEqual([]);
  });

  it("collects every revision of one repository into a single group", () => {
    const groups = groupTemplateRevisionsByRepository([
      templateRevision({ id: "rev_1", repo_owner: "acme", repo_name: "infra", source_ref: "main" }),
      templateRevision({ id: "rev_2", repo_owner: "acme", repo_name: "infra", source_ref: "v2" })
    ]);

    expect(groups).toHaveLength(1);
    expect(groups[0].repoOwner).toBe("acme");
    expect(groups[0].repoName).toBe("infra");
    expect(groups[0].templateRevisions.map((revision) => revision.id)).toEqual(["rev_1", "rev_2"]);
  });

  it("separates repositories that share a name under different owners", () => {
    const groups = groupTemplateRevisionsByRepository([
      templateRevision({ id: "rev_1", repo_owner: "acme", repo_name: "infra" }),
      templateRevision({ id: "rev_2", repo_owner: "globex", repo_name: "infra" })
    ]);

    expect(groups).toHaveLength(2);
    expect(groups.map((group) => group.key)).toEqual(["acme/infra", "globex/infra"]);
  });

  it("preserves the incoming newest-first order both across and within groups", () => {
    // The API returns revisions ordered created_at desc, so first appearance
    // of a repository is its newest revision — that is what orders the groups.
    const groups = groupTemplateRevisionsByRepository([
      templateRevision({ id: "rev_3", repo_owner: "acme", repo_name: "newest" }),
      templateRevision({ id: "rev_2", repo_owner: "acme", repo_name: "older" }),
      templateRevision({ id: "rev_1", repo_owner: "acme", repo_name: "newest" })
    ]);

    expect(groups.map((group) => group.key)).toEqual(["acme/newest", "acme/older"]);
    expect(groups[0].templateRevisions.map((revision) => revision.id)).toEqual(["rev_3", "rev_1"]);
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
