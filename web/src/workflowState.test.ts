import { describe, expect, it } from "vitest";
import type { Stack, StackTemplate, Template } from "./api/types";
import {
  findSelectedStack,
  findSelectedStackTemplate,
  findSelectedTemplate,
  nextSelectedStackID,
  nextSelectedStackTemplateID,
  nextSelectedTemplateID,
  stackLabel,
  stackTemplateLabel,
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

  it("keeps the active stack template when it still belongs to the selected stack", () => {
    const firstTemplate = stackTemplate({ id: "stack_template_first", workspace_name: "prod-web" });
    const activeTemplate = stackTemplate({ id: "stack_template_active", workspace_name: "prod-api" });
    const stackTemplates = [firstTemplate, activeTemplate];

    expect(nextSelectedStackTemplateID(stackTemplates, activeTemplate.id)).toBe(activeTemplate.id);
    expect(findSelectedStackTemplate(stackTemplates, activeTemplate.id)).toEqual(activeTemplate);
  });

  it("falls back to the first stack template when the active one is absent", () => {
    const firstTemplate = stackTemplate({ id: "stack_template_first", workspace_name: "prod-web" });
    const secondTemplate = stackTemplate({ id: "stack_template_second", workspace_name: "prod-api" });

    expect(nextSelectedStackTemplateID([firstTemplate, secondTemplate], "missing_stack_template")).toBe(firstTemplate.id);
    expect(nextSelectedStackTemplateID([], "missing_stack_template")).toBe("");
  });

  it("formats stack template rows from workspace, ref, and lifecycle", () => {
    const installedTemplate = stackTemplate({
      workspace_name: "prod-web",
      selected_ref: "release-2026-07",
      lifecycle: "active"
    });

    expect(stackTemplateLabel(installedTemplate)).toBe("prod-web @ release-2026-07 (active)");
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

function stackTemplate(overrides: Partial<StackTemplate>): StackTemplate {
  return {
    id: "stack_template_123",
    stack_id: "stack_123",
    template_id: "template_123",
    selected_ref: "main",
    workspace_name: "prod-workspace",
    config: {},
    last_applied_run_id: "",
    last_applied_ref: "",
    created_by: "user_123",
    lifecycle: "active",
    ...overrides
  };
}
