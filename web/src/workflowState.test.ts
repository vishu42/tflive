import { describe, expect, it } from "vitest";
import type { Stack, Template } from "./api/types";
import {
  findSelectedStack,
  findSelectedTemplate,
  nextSelectedStackID,
  nextSelectedTemplateID,
  stackLabel,
  templateLabel
} from "./workflowState";

describe("workflow state helpers", () => {
  it("keeps a newly created stack selected after refreshing from the database", () => {
    const oldStack = stack({ id: "stack_old", name: "Old Stack", slug: "old-stack" });
    const createdStack = stack({ id: "stack_new", name: "New Stack", slug: "new-stack" });
    const stacks = [createdStack, oldStack];

    expect(nextSelectedStackID(stacks, createdStack.id)).toBe(createdStack.id);
    expect(findSelectedStack(stacks, createdStack.id)).toEqual(createdStack);
  });

  it("falls back to the first hydrated stack and template when the current selection is absent", () => {
    const stacks = [stack({ id: "stack_123", name: "Prod", slug: "prod" })];
    const templates = [template({ id: "template_123", repo_name: "infra" })];

    expect(nextSelectedStackID(stacks, "missing_stack")).toBe("stack_123");
    expect(nextSelectedTemplateID(templates, "missing_template")).toBe("template_123");
  });

  it("formats menu labels from persisted stack and template rows", () => {
    const selectedStack = stack({ id: "stack_123", name: "Prod", slug: "prod" });
    const selectedTemplate = template({
      id: "template_123",
      repo_owner: "acme",
      repo_name: "infra",
      source_ref: "main",
      name: ""
    });

    expect(stackLabel(selectedStack)).toBe("Prod (prod)");
    expect(templateLabel(selectedTemplate)).toBe("acme/infra @ main");
    expect(findSelectedTemplate([selectedTemplate], selectedTemplate.id)).toEqual(selectedTemplate);
  });
});

function stack(overrides: Partial<Stack>): Stack {
  return {
    id: "stack_123",
    tenant_id: "tenant_123",
    name: "Stack",
    slug: "stack",
    tags: {},
    default_credential_ids: [],
    created_by: "user_123",
    created_at: "2026-07-06T00:00:00Z",
    ...overrides
  };
}

function template(overrides: Partial<Template>): Template {
  return {
    id: "template_123",
    tenant_id: "tenant_123",
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
