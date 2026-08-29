import type { Stack, StackTemplate, TemplateRevision, TemplateVariable } from "../../api/types";
import { findSelectedID } from "../../shared/listSelection";

export function findSelectedStackTemplate(stackTemplates: StackTemplate[], selectedStackTemplateID: string): StackTemplate | null {
  return findSelectedID(stackTemplates, selectedStackTemplateID);
}

export function stackTemplateLabel(stackTemplate: StackTemplate): string {
  const name = stackTemplate.display_name || stackTemplate.workspace_name;
  return `${name} @ ${stackTemplate.source_ref} (${stackTemplate.lifecycle})`;
}

export function canUpgradeStackTemplate(stackTemplate: StackTemplate | null, templateRevision: TemplateRevision | null): boolean {
  if (!stackTemplate || !templateRevision || templateRevision.status !== "active") {
    return false;
  }
  if (stackTemplate.desired_template_revision_id === templateRevision.id) {
    return false;
  }
  return stackTemplate.source_template_id === templateRevision.source_template_id;
}

export interface UpgradeVariablePartition {
  added: TemplateVariable[];
  carried: TemplateVariable[];
  removed: TemplateVariable[];
}

/**
 * Revisions this stack template can change to: active revisions of the same
 * source template, minus the one already desired. The API's order is
 * preserved, but it is not treated as commit ancestry or recency.
 */
export function upgradeCandidateRevisions(
  revisions: TemplateRevision[],
  stackTemplate: StackTemplate | null
): TemplateRevision[] {
  if (!stackTemplate) {
    return [];
  }
  return revisions.filter((revision) => canUpgradeStackTemplate(stackTemplate, revision));
}

/**
 * Compares the installed revision's variables against a target revision's, by
 * name. Carried variables carry the *target* revision's definition, because
 * that is what the upgrade will be validated against — a variable can stay
 * named the same while becoming required or changing type.
 */
export function partitionUpgradeVariables(
  currentVariables: TemplateVariable[],
  targetVariables: TemplateVariable[]
): UpgradeVariablePartition {
  const currentNames = new Set(currentVariables.map((variable) => variable.name));
  const targetNames = new Set(targetVariables.map((variable) => variable.name));
  return {
    added: targetVariables.filter((variable) => !currentNames.has(variable.name)),
    carried: targetVariables.filter((variable) => currentNames.has(variable.name)),
    removed: currentVariables.filter((variable) => !targetNames.has(variable.name))
  };
}

/**
 * Save is available when the edited values differ from what is stored on the
 * stack template. It takes no revision: the template screen always renders the
 * installed template's desired revision, so there is no selection that could
 * disagree with it.
 */
export function canSaveInstalledTemplateConfig(
  stackTemplate: StackTemplate | null,
  variables: TemplateVariable[],
  variableValues: Record<string, string>
): boolean {
  if (!stackTemplate) {
    return false;
  }
  return hasConfigChanged(stackTemplate, variables, variableValues);
}

export function variableValuesFromConfig(config: Record<string, unknown>, variables: TemplateVariable[]): Record<string, string> {
  const values: Record<string, string> = {};
  for (const variable of variables) {
    const value = config[variable.name];
    values[variable.name] = value == null ? "" : String(value);
  }
  return values;
}

export function configFromVariableValues(variables: TemplateVariable[], values: Record<string, string>): Record<string, string> {
  const config: Record<string, string> = {};
  for (const variable of variables) {
    const value = values[variable.name]?.trim() ?? "";
    if (value !== "") {
      config[variable.name] = value;
    }
  }
  return config;
}

export function upsertStackTemplate(stackTemplates: StackTemplate[], stackTemplate: StackTemplate): StackTemplate[] {
  if (stackTemplates.some((item) => item.id === stackTemplate.id)) {
    return stackTemplates.map((item) => item.id === stackTemplate.id ? stackTemplate : item);
  }
  return [stackTemplate, ...stackTemplates];
}

export function canDestroyStackTemplate(stackTemplate: StackTemplate | null): boolean {
  return stackTemplate?.lifecycle === "active";
}

export function isDestroyingStackTemplate(stackTemplate: StackTemplate | null): boolean {
  return stackTemplate?.lifecycle === "destroying";
}

// hasFreshPlan asks the server's question — "is the completed plan still what
// would run?" — instead of the old client-side one, "is the latest run a
// completed plan?". Those differ whenever config was saved or the revision
// changed after planning, and the server rejects an apply in exactly the cases
// this returns false for.
export function hasFreshPlan(stackTemplate: StackTemplate | null): boolean {
  return stackTemplate?.plan_state === "matches";
}

// planStaleReason explains a disabled Apply. A gate the user cannot see the
// reason for reads as a broken button.
export function planStaleReason(stackTemplate: StackTemplate | null): string {
  if (stackTemplate?.plan_state !== "stale") {
    return "";
  }
  return "Config or revision changed since this plan — re-plan before applying";
}

function areStringRecordEqual(left: Record<string, string>, right: Record<string, string>): boolean {
  const leftKeys = Object.keys(left);
  const rightKeys = Object.keys(right);
  if (leftKeys.length !== rightKeys.length) {
    return false;
  }
  for (const key of leftKeys) {
    if ((left[key] ?? "") !== (right[key] ?? "")) {
      return false;
    }
  }
  return true;
}

function hasConfigChanged(
  stackTemplate: StackTemplate,
  variables: TemplateVariable[],
  variableValues: Record<string, string>
): boolean {
  const currentConfig = configFromVariableValues(variables, variableValues);
  const savedConfig = configFromVariableValues(variables, variableValuesFromConfig(stackTemplate.config, variables));
  return !areStringRecordEqual(currentConfig, savedConfig);
}
