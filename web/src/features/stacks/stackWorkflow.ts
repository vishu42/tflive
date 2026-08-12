import type { Stack, StackTemplate, TemplateRevision, TemplateVariable } from "../../api/types";
import { findSelectedID, nextSelectedID } from "../../shared/listSelection";

export function nextSelectedStackID(stacks: Stack[], selectedStackID: string): string {
  return nextSelectedID(stacks, selectedStackID);
}

export function nextSelectedStackTemplateID(stackTemplates: StackTemplate[], selectedStackTemplateID: string): string {
  return nextSelectedID(stackTemplates, selectedStackTemplateID);
}

export function findSelectedStack(stacks: Stack[], selectedStackID: string): Stack | null {
  return findSelectedID(stacks, selectedStackID);
}

export function findSelectedStackTemplate(stackTemplates: StackTemplate[], selectedStackTemplateID: string): StackTemplate | null {
  return findSelectedID(stackTemplates, selectedStackTemplateID);
}

export function stackLabel(stack: Stack): string {
  return stack.slug ? `${stack.name} (${stack.slug})` : stack.name;
}

export function stackTemplateLabel(stackTemplate: StackTemplate): string {
  const name = stackTemplate.display_name || stackTemplate.workspace_name;
  return `${name} @ ${stackTemplate.selected_ref} (${stackTemplate.lifecycle})`;
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

export function canSaveStackTemplateConfig(
  stackTemplate: StackTemplate | null,
  templateRevision: TemplateRevision | null,
  variables: TemplateVariable[],
  variableValues: Record<string, string>
): boolean {
  if (!stackTemplate || !templateRevision || templateRevision.id !== stackTemplate.desired_template_revision_id) {
    return false;
  }

  return hasConfigChanged(stackTemplate, variables, variableValues);
}

export interface UpgradeVariablePartition {
  added: TemplateVariable[];
  carried: TemplateVariable[];
  removed: TemplateVariable[];
}

/**
 * Revisions this stack template can be upgraded to: active revisions of the
 * same source template, minus the one already desired. The API returns
 * revisions newest-first and that order is preserved, so the first candidate
 * is the newest.
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
 * stack template. Unlike canSaveStackTemplateConfig this takes no revision:
 * the template screen always renders the installed template's desired
 * revision, so there is no selection that could disagree with it.
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
