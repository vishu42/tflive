import type { Stack, StackTemplate, TemplateRevision, TemplateVariable } from "./api/types";

type Identified = {
  id: string;
};

export function nextSelectedStackID(stacks: Stack[], selectedStackID: string): string {
  return nextSelectedID(stacks, selectedStackID);
}

export function nextSelectedTemplateRevisionID(templateRevisions: TemplateRevision[], selectedTemplateRevisionID: string): string {
  return nextSelectedID(templateRevisions, selectedTemplateRevisionID);
}

export function nextSelectedStackTemplateID(stackTemplates: StackTemplate[], selectedStackTemplateID: string): string {
  return nextSelectedID(stackTemplates, selectedStackTemplateID);
}

export function findSelectedStack(stacks: Stack[], selectedStackID: string): Stack | null {
  return stacks.find((stack) => stack.id === selectedStackID) ?? null;
}

export function findSelectedTemplateRevision(templateRevisions: TemplateRevision[], selectedTemplateRevisionID: string): TemplateRevision | null {
  return templateRevisions.find((templateRevision) => templateRevision.id === selectedTemplateRevisionID) ?? null;
}

export function findSelectedStackTemplate(stackTemplates: StackTemplate[], selectedStackTemplateID: string): StackTemplate | null {
  return stackTemplates.find((stackTemplate) => stackTemplate.id === selectedStackTemplateID) ?? null;
}

export function stackLabel(stack: Stack): string {
  return stack.slug ? `${stack.name} (${stack.slug})` : stack.name;
}

export function templateRevisionLabel(templateRevision: TemplateRevision): string {
  const name = templateRevision.name.trim() || `${templateRevision.repo_owner}/${templateRevision.repo_name}`;
  return `${name} @ ${templateRevision.source_ref}`;
}

export function stackTemplateLabel(stackTemplate: StackTemplate): string {
  return `${stackTemplate.workspace_name} @ ${stackTemplate.selected_ref} (${stackTemplate.lifecycle})`;
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

function nextSelectedID<T extends Identified>(items: T[], selectedID: string): string {
  if (selectedID && items.some((item) => item.id === selectedID)) {
    return selectedID;
  }
  return items[0]?.id ?? "";
}
