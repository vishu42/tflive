import type { Stack, StackTemplate, TemplateRevision, TemplateVariable } from "../../api/types";
import { findSelectedID } from "../../shared/listSelection";
import type { StatusTone } from "../../shared/statusTone";

export function findSelectedStackTemplate(stackTemplates: StackTemplate[], selectedStackTemplateID: string): StackTemplate | null {
  return findSelectedID(stackTemplates, selectedStackTemplateID);
}

export function stackTemplateLabel(stackTemplate: StackTemplate): string {
  const name = stackTemplate.display_name || stackTemplate.workspace_name;
  return `${name} @ ${stackTemplate.source_ref}`;
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
  return "Config or revision changed since this plan - re-plan before applying";
}

export interface StackTemplateStatus {
  label: string;
  tone: StatusTone;
  /** What the state means, for the detail panel. The list row shows only the
      icon, so this is where the vocabulary is actually taught. */
  description: string;
}

// stackTemplateStatus answers "is desired state live?" for one row of the list.
// It is deliberately a projection of live_state alone: plan_state answers a
// different question — "can I act now?" — and the Apply button already carries
// that, with planStaleReason putting the reason on the disabled control.
//
// lifecycle is read first because a template being torn down keeps the
// last_applied_* pointers of the apply that put it live, so live_state would
// report a destroying template as applied.
//
// The tone is returned rather than derived through statusTone(): that helper
// classifies API status strings, and would read "changed" and "destroying" as
// settled.
export function stackTemplateStatus(stackTemplate: StackTemplate): StackTemplateStatus {
  if (stackTemplate.lifecycle === "destroying") {
    return {
      label: "destroying",
      tone: "progress",
      description: "A destroy run is tearing this template's resources down."
    };
  }
  // Only an interrupted destroy sets this lifecycle — a failed apply leaves the
  // template active — so the label says destroy rather than claiming the
  // infrastructure itself failed.
  if (stackTemplate.lifecycle === "failed") {
    return {
      label: "destroy failed",
      tone: "failed",
      description: "A destroy run stopped before it finished. Some resources may still exist."
    };
  }
  switch (stackTemplate.live_state) {
    case "matches":
      // The last apply's intent equals desired. It does not claim reality still
      // matches — that is drift, and it needs a refresh to observe — so this
      // says applied rather than in sync.
      return {
        label: "applied",
        tone: "settled",
        description: "The last apply ran the revision and config that are desired now."
      };
    case "differs":
      // Live infrastructure no longer matches what was asked for, so this is
      // the one row state that wants attention. It keeps the warning tone and
      // "not applied" takes a muted one: a template nobody has applied yet is
      // not a problem to be drawn to.
      return {
        label: "changed",
        tone: "waiting",
        description: "The revision or config changed after the last apply. Plan, then apply, to bring it live."
      };
    default:
      return {
        label: "not applied",
        tone: "canceled",
        description: "Nothing has been applied yet, so this template has no live resources."
      };
  }
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
