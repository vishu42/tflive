import type { Stack, StackTemplate, Template } from "./api/types";

type Identified = {
  id: string;
};

export function nextSelectedStackID(stacks: Stack[], selectedStackID: string): string {
  return nextSelectedID(stacks, selectedStackID);
}

export function nextSelectedTemplateID(templates: Template[], selectedTemplateID: string): string {
  return nextSelectedID(templates, selectedTemplateID);
}

export function nextSelectedStackTemplateID(stackTemplates: StackTemplate[], selectedStackTemplateID: string): string {
  return nextSelectedID(stackTemplates, selectedStackTemplateID);
}

export function findSelectedStack(stacks: Stack[], selectedStackID: string): Stack | null {
  return stacks.find((stack) => stack.id === selectedStackID) ?? null;
}

export function findSelectedTemplate(templates: Template[], selectedTemplateID: string): Template | null {
  return templates.find((template) => template.id === selectedTemplateID) ?? null;
}

export function findSelectedStackTemplate(stackTemplates: StackTemplate[], selectedStackTemplateID: string): StackTemplate | null {
  return stackTemplates.find((stackTemplate) => stackTemplate.id === selectedStackTemplateID) ?? null;
}

export function stackLabel(stack: Stack): string {
  return stack.slug ? `${stack.name} (${stack.slug})` : stack.name;
}

export function templateLabel(template: Template): string {
  const name = template.name.trim() || `${template.repo_owner}/${template.repo_name}`;
  return `${name} @ ${template.source_ref}`;
}

export function stackTemplateLabel(stackTemplate: StackTemplate): string {
  return `${stackTemplate.workspace_name} @ ${stackTemplate.selected_ref} (${stackTemplate.lifecycle})`;
}

function nextSelectedID<T extends Identified>(items: T[], selectedID: string): string {
  if (selectedID && items.some((item) => item.id === selectedID)) {
    return selectedID;
  }
  return items[0]?.id ?? "";
}
