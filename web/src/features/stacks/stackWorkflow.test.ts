import { describe, expect, it } from "vitest";
import type { Stack, StackTemplate, TemplateRevision, TemplateVariable } from "../../api/types";
import {
  canDestroyStackTemplate,
  canUpgradeStackTemplate,
  canSaveStackTemplateConfig,
  configFromVariableValues,
  findSelectedStack,
  findSelectedStackTemplate,
  isDestroyingStackTemplate,
  nextSelectedStackID,
  nextSelectedStackTemplateID,
  stackLabel,
  stackTemplateLabel,
  upsertStackTemplate,
  variableValuesFromConfig
} from "./stackWorkflow";

describe("stack workflow helpers", () => {
  it("keeps a newly created stack selected after refreshing from the database", () => {
    const oldStack = stack({ id: "stack_old", name: "Old Stack", slug: "old-stack" });
    const createdStack = stack({ id: "stack_new", name: "New Stack", slug: "new-stack" });
    const stacks = [createdStack, oldStack];

    expect(nextSelectedStackID(stacks, createdStack.id)).toBe(createdStack.id);
    expect(findSelectedStack(stacks, createdStack.id)).toEqual(createdStack);
  });

  it("falls back to the first hydrated stack when the current selection is absent", () => {
    const stacks = [stack({ id: "stack_123", name: "Prod", slug: "prod" })];

    expect(nextSelectedStackID(stacks, "missing_stack")).toBe("stack_123");
  });

  it("formats menu labels from persisted stack rows", () => {
    const selectedStack = stack({ id: "stack_123", name: "Prod", slug: "prod" });

    expect(stackLabel(selectedStack)).toBe("Prod (prod)");
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

  it("formats stack template rows from display_name, ref, and lifecycle", () => {
    const withDisplayName = stackTemplate({
      display_name: "infra-templates/modules/vpc",
      selected_ref: "release-2026-07",
      lifecycle: "active"
    });

    expect(stackTemplateLabel(withDisplayName)).toBe("infra-templates/modules/vpc @ release-2026-07 (active)");
  });

  it("falls back to workspace_name when display_name is empty", () => {
    const withoutDisplayName = stackTemplate({
      workspace_name: "meg_prod_a4de3e48",
      selected_ref: "main",
      lifecycle: "active"
    });

    expect(stackTemplateLabel(withoutDisplayName)).toBe("meg_prod_a4de3e48 @ main (active)");
  });

  it("allows upgrades only to a different active revision from the same source template", () => {
    const installedTemplate = stackTemplate({
      source_template_id: "source_template_vpc",
      desired_template_revision_id: "template_rev_1"
    });

    expect(canUpgradeStackTemplate(installedTemplate, templateRevision({
      id: "template_rev_2",
      source_template_id: "source_template_vpc",
      status: "active"
    }))).toBe(true);
    expect(canUpgradeStackTemplate(installedTemplate, templateRevision({
      id: "template_rev_1",
      source_template_id: "source_template_vpc",
      status: "active"
    }))).toBe(false);
    expect(canUpgradeStackTemplate(installedTemplate, templateRevision({
      id: "template_rev_3",
      source_template_id: "source_template_db",
      status: "active"
    }))).toBe(false);
    expect(canUpgradeStackTemplate(installedTemplate, templateRevision({
      id: "template_rev_4",
      source_template_id: "source_template_vpc",
      status: "invalid"
    }))).toBe(false);
  });

  it("enables saving only when the editable config actually changes", () => {
    const installedTemplate = stackTemplate({
      desired_template_revision_id: "template_rev_1",
      config: {
        region: "us-east-1",
        size: 3
      }
    });
    const selectedTemplateRevision = templateRevision({ id: "template_rev_1" });
    const variables = [
      templateVariable({ name: "region" }),
      templateVariable({ name: "size" })
    ];

    expect(canSaveStackTemplateConfig(installedTemplate, selectedTemplateRevision, variables, {
      region: " us-east-1 ",
      size: "3"
    })).toBe(false);
    expect(canSaveStackTemplateConfig(installedTemplate, selectedTemplateRevision, variables, {
      region: "us-west-2",
      size: "3"
    })).toBe(true);
    expect(canSaveStackTemplateConfig(installedTemplate, templateRevision({ id: "template_rev_2" }), variables, {
      region: "us-west-2",
      size: "3"
    })).toBe(false);
  });

  it("maps stack template config to editable variable values and request config", () => {
    const variables = [
      templateVariable({ name: "region" }),
      templateVariable({ name: "size" })
    ];

    expect(variableValuesFromConfig({ region: "us-east-1", size: 3, unused: true }, variables)).toEqual({
      region: "us-east-1",
      size: "3"
    });
    expect(configFromVariableValues(variables, {
      region: " us-west-2 ",
      size: "",
      unused: "ignored"
    })).toEqual({ region: "us-west-2" });
  });

  // canDestroyStackTemplate returns true only when the lifecycle is "active";
  // null, "destroying", and "destroyed" templates are not eligible for destroy.
  it("allows destroy only for active stack templates", () => {
    expect(canDestroyStackTemplate(stackTemplate({ lifecycle: "active" }))).toBe(true);
    expect(canDestroyStackTemplate(stackTemplate({ lifecycle: "destroying" }))).toBe(false);
    expect(canDestroyStackTemplate(stackTemplate({ lifecycle: "destroyed" }))).toBe(false);
    expect(canDestroyStackTemplate(null)).toBe(false);
  });

  // isDestroyingStackTemplate returns true only when lifecycle is "destroying",
  // which gates the disabling of edit actions while a destroy run is in flight.
  it("detects destroying state from stack template lifecycle", () => {
    expect(isDestroyingStackTemplate(stackTemplate({ lifecycle: "destroying" }))).toBe(true);
    expect(isDestroyingStackTemplate(stackTemplate({ lifecycle: "active" }))).toBe(false);
    expect(isDestroyingStackTemplate(null)).toBe(false);
  });

  it("upserts refreshed stack templates without changing other installs", () => {
    const firstTemplate = stackTemplate({ id: "stack_template_first", desired_template_revision_id: "template_old" });
    const secondTemplate = stackTemplate({ id: "stack_template_second", desired_template_revision_id: "template_same" });
    const updatedFirst = stackTemplate({ id: "stack_template_first", desired_template_revision_id: "template_new" });

    expect(upsertStackTemplate([firstTemplate, secondTemplate], updatedFirst)).toEqual([updatedFirst, secondTemplate]);
    expect(upsertStackTemplate([secondTemplate], updatedFirst)).toEqual([updatedFirst, secondTemplate]);
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
    effectiveCapabilities: { canView: true, canOperate: true, canApprove: true, canManageAccess: true },
    ...overrides
  };
}

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

function stackTemplate(overrides: Partial<StackTemplate>): StackTemplate {
  return {
    id: "stack_template_123",
    stack_id: "stack_123",
    component_key: "primary",
    source_template_id: "source_template_123",
    desired_template_revision_id: "template_123",
    last_applied_template_revision_id: "",
    selected_ref: "main",
    workspace_name: "prod-workspace",
    display_name: "",
    config: {},
    last_applied_run_id: "",
    last_applied_ref: "",
    created_by: "user_123",
    lifecycle: "active",
    ...overrides
  };
}

function templateVariable(overrides: Partial<TemplateVariable>): TemplateVariable {
  return {
    template_revision_id: "template_123",
    name: "region",
    type_expression: "string",
    description: "",
    required: true,
    has_default: false,
    sensitive: false,
    has_validation: false,
    ...overrides
  };
}
