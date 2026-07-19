import { describe, expect, it } from "vitest";
import type { TemplateRevision } from "../../api/types";
import { findSelectedTemplateRevision, nextSelectedTemplateRevisionID, templateRevisionLabel } from "./templateWorkflow";

describe("template workflow helpers", () => {
  it("falls back to the first hydrated template revision when the current selection is absent", () => {
    const templateRevisions = [templateRevision({ id: "template_123", repo_name: "infra" })];

    expect(nextSelectedTemplateRevisionID(templateRevisions, "missing_template")).toBe("template_123");
  });

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
    expect(findSelectedTemplateRevision([selectedTemplateRevision], selectedTemplateRevision.id)).toEqual(selectedTemplateRevision);
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
