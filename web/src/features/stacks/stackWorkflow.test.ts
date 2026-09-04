import { describe, expect, it } from "vitest";
import type { Stack, StackTemplate, TemplateRevision, TemplateVariable } from "../../api/types";
import {
  canDestroyStackTemplate,
  canSaveInstalledTemplateConfig,
  canUpgradeStackTemplate,
  configFromVariableValues,
  findSelectedStackTemplate,
  isDestroyingStackTemplate,
  partitionUpgradeVariables,
  stackTemplateLabel,
  stackTemplateStatus,
  upgradeCandidateRevisions,
  upsertStackTemplate,
  variableValuesFromConfig
} from "./stackWorkflow";

describe("stack workflow helpers", () => {
  it("finds a stack template by id among a stack's installs", () => {
    const firstTemplate = stackTemplate({ id: "stack_template_first", workspace_name: "prod-web" });
    const activeTemplate = stackTemplate({ id: "stack_template_active", workspace_name: "prod-api" });
    const stackTemplates = [firstTemplate, activeTemplate];

    expect(findSelectedStackTemplate(stackTemplates, activeTemplate.id)).toEqual(activeTemplate);
    expect(findSelectedStackTemplate(stackTemplates, "missing_stack_template")).toBeNull();
  });

  // Two suffixes have now been dropped from this label. The lifecycle read
  // "(active)" on every row a user ever sees, and the status pill carries it.
  // The ref is part of a source template's identity, so it read the same on
  // every install of that template and separated nothing.
  it("formats stack template rows from display_name alone", () => {
    const withDisplayName = stackTemplate({
      display_name: "infra-templates/modules/vpc",
      source_ref: "release-2026-07",
      lifecycle: "active"
    });

    expect(stackTemplateLabel(withDisplayName)).toBe("infra-templates/modules/vpc");
  });

  it("falls back to workspace_name when display_name is empty", () => {
    const withoutDisplayName = stackTemplate({
      workspace_name: "meg_prod_a4de3e48",
      source_ref: "main",
      lifecycle: "active"
    });

    expect(stackTemplateLabel(withoutDisplayName)).toBe("meg_prod_a4de3e48");
  });

  // One case per cell of the stack template state matrix. Only three outcomes
  // exist, and covering all nine is the point: it pins down that plan_state is
  // not consulted, so a later change that reads it fails here.
  describe("stack template status pill", () => {
    const planStates = ["none", "stale", "matches"] as const;

    it.each(planStates)("reports applied when live matches, whatever the plan state (%s)", (planState) => {
      const status = stackTemplateStatus(stackTemplate({ lifecycle: "active", live_state: "matches", plan_state: planState }));

      expect(status).toMatchObject({ label: "applied", tone: "settled" });
    });

    it.each(planStates)("reports changed when live differs, whatever the plan state (%s)", (planState) => {
      const status = stackTemplateStatus(stackTemplate({ lifecycle: "active", live_state: "differs", plan_state: planState }));

      expect(status).toMatchObject({ label: "changed", tone: "waiting" });
    });

    // Includes "ready for first apply" (plan matches, nothing live). A reviewed
    // plan does not make a template applied, so it reads the same as a fresh
    // install; readiness belongs to the Apply button, not this pill.
    it.each(planStates)("reports not applied when nothing is live, whatever the plan state (%s)", (planState) => {
      const status = stackTemplateStatus(stackTemplate({ lifecycle: "active", live_state: "never", plan_state: planState }));

      expect(status).toMatchObject({ label: "not applied", tone: "canceled" });
    });

    // A template being torn down keeps the last_applied_* pointers of the apply
    // that put it live, so live_state alone would call this one applied.
    it("reports destroying ahead of a live state that still says matches", () => {
      const status = stackTemplateStatus(stackTemplate({ lifecycle: "destroying", live_state: "matches", plan_state: "matches" }));

      expect(status).toMatchObject({ label: "destroying", tone: "progress" });
    });

    // The row shows only an icon, so the description is the only place the
    // state is explained. Every state needs its own, or the panel says nothing.
    it("describes every state distinctly", () => {
      const descriptions = [
        stackTemplateStatus(stackTemplate({ lifecycle: "active", live_state: "matches" })),
        stackTemplateStatus(stackTemplate({ lifecycle: "active", live_state: "differs" })),
        stackTemplateStatus(stackTemplate({ lifecycle: "active", live_state: "never" })),
        stackTemplateStatus(stackTemplate({ lifecycle: "destroying", live_state: "matches" })),
        stackTemplateStatus(stackTemplate({ lifecycle: "failed", live_state: "matches" }))
      ].map((status) => status.description);

      expect(descriptions.every((text) => text.length > 0)).toBe(true);
      expect(new Set(descriptions).size).toBe(descriptions.length);
    });

    // The failed lifecycle is only ever reached from destroying, so the label
    // says which operation failed rather than implying the apply did.
    it("names an interrupted destroy rather than reporting a bare failure", () => {
      const status = stackTemplateStatus(stackTemplate({ lifecycle: "failed", live_state: "matches", plan_state: "matches" }));

      expect(status).toMatchObject({ label: "destroy failed", tone: "failed" });
    });
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

  it("offers only active revisions of the same source template as upgrade candidates", () => {
    const installed = stackTemplate({ source_template_id: "src_1", desired_template_revision_id: "rev_current" });
    const revisions = [
      templateRevision({ id: "rev_new", source_template_id: "src_1", status: "active" }),
      templateRevision({ id: "rev_current", source_template_id: "src_1", status: "active" }),
      templateRevision({ id: "rev_other_source", source_template_id: "src_2", status: "active" }),
      templateRevision({ id: "rev_pending", source_template_id: "src_1", status: "pending_validation" })
    ];

    expect(upgradeCandidateRevisions(revisions, installed).map((item) => item.id)).toEqual(["rev_new"]);
  });

  it("preserves the newest-first order the API returns for upgrade candidates", () => {
    const installed = stackTemplate({ source_template_id: "src_1", desired_template_revision_id: "rev_current" });
    const revisions = [
      templateRevision({ id: "rev_newest", source_template_id: "src_1", status: "active" }),
      templateRevision({ id: "rev_older", source_template_id: "src_1", status: "active" }),
      templateRevision({ id: "rev_current", source_template_id: "src_1", status: "active" })
    ];

    expect(upgradeCandidateRevisions(revisions, installed).map((item) => item.id)).toEqual(["rev_newest", "rev_older"]);
  });

  it("has no upgrade candidates without an installed template", () => {
    expect(upgradeCandidateRevisions([templateRevision({ id: "rev_new" })], null)).toEqual([]);
  });

  it("splits upgrade variables into added, carried, and removed by name", () => {
    const current = [variable({ name: "region" }), variable({ name: "legacy_flag" })];
    const target = [variable({ name: "region" }), variable({ name: "new_var" })];

    const partition = partitionUpgradeVariables(current, target);

    expect(partition.added.map((item) => item.name)).toEqual(["new_var"]);
    expect(partition.carried.map((item) => item.name)).toEqual(["region"]);
    expect(partition.removed.map((item) => item.name)).toEqual(["legacy_flag"]);
  });

  it("carries every variable when the revision's variable set is unchanged", () => {
    const variables = [variable({ name: "region" }), variable({ name: "cidr_block" })];

    const partition = partitionUpgradeVariables(variables, variables);

    expect(partition.carried.map((item) => item.name)).toEqual(["region", "cidr_block"]);
    expect(partition.added).toEqual([]);
    expect(partition.removed).toEqual([]);
  });

  it("treats a fully disjoint variable set as all added and all removed", () => {
    const partition = partitionUpgradeVariables([variable({ name: "old" })], [variable({ name: "new" })]);

    expect(partition.added.map((item) => item.name)).toEqual(["new"]);
    expect(partition.carried).toEqual([]);
    expect(partition.removed.map((item) => item.name)).toEqual(["old"]);
  });

  it("returns the target revision's variable definitions for carried variables", () => {
    const current = [variable({ name: "region", template_revision_id: "rev_current", required: false })];
    const target = [variable({ name: "region", template_revision_id: "rev_target", required: true })];

    const partition = partitionUpgradeVariables(current, target);

    expect(partition.carried[0].template_revision_id).toBe("rev_target");
    expect(partition.carried[0].required).toBe(true);
  });

  it("enables saving installed config only when a value differs from what is stored", () => {
    const installed = stackTemplate({ config: { region: "us-east-1" } });
    const variables = [variable({ name: "region" })];

    expect(canSaveInstalledTemplateConfig(installed, variables, { region: "us-east-1" })).toBe(false);
    expect(canSaveInstalledTemplateConfig(installed, variables, { region: "eu-west-1" })).toBe(true);
  });

  it("does not enable saving installed config without an installed template", () => {
    expect(canSaveInstalledTemplateConfig(null, [variable({ name: "region" })], { region: "eu-west-1" })).toBe(false);
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
    source_ref: "main",
    workspace_name: "prod-workspace",
    display_name: "",
    config: {},
    last_applied_run_id: "",
    last_planned_run_id: "",
    plan_state: "none",
    live_state: "never",
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

function variable(overrides: Partial<TemplateVariable>): TemplateVariable {
  return templateVariable(overrides);
}
