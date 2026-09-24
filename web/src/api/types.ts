import type { StackCapabilities } from "../auth/types";

export type TemplateRegistrationStatus =
  | "pending"
  | "running"
  | "completed"
  | "invalid"
  | "failed";

export type TemplateRevisionStatus =
  | "pending_validation"
  | "validating"
  | "active"
  | "invalid";

// A run's lifecycle state. What a running run is doing is its step.
export type TemplateRunStatus =
  | "queued"
  | "running"
  | "waiting_approval"
  | "approved"
  | "completed"
  | "failed"
  | "canceled";

// What a run is doing, or, once it ended, what it was doing last. Nothing
// about what the run may do next depends on it.
export type TemplateRunStep =
  | "waiting_for_executor"
  | "preparing_workspace"
  | "fetching_source"
  | "restoring_plan"
  | "initializing"
  | "selecting_workspace"
  | "planning"
  | "saving_plan"
  | "applying";

// "apply" survives only on runs from before saved plans; a run is started as a
// plan or a destroy, and approving its plan is what applies it.
export type Operation = "plan" | "apply" | "destroy";

export interface ApiErrorBody {
  error: string;
  message: string;
}

// What a sync is doing, or, once it ended, what it was doing last. Nothing
// about what the registration may do next depends on it.
export type TemplateRegistrationStep = "syncing";

export interface TemplateRegistration {
  id: string;
  tenant_id: string;
  repo_owner: string;
  repo_name: string;
  source_ref: string;
  root_path: string;
  status: TemplateRegistrationStatus;
  // Empty until the sync starts its first step.
  step: TemplateRegistrationStep | "";
  template_revision_id: string;
  resolved_commit_sha: string;
  requested_by: string;
  requested_at: string;
  completed_at?: string;
  error_summary: string;
}

export interface TemplateRevision {
  id: string;
  tenant_id: string;
  source_template_id: string;
  repo_owner: string;
  repo_name: string;
  source_ref: string;
  resolved_commit_sha: string;
  root_path: string;
  name: string;
  description: string;
  tags: string[];
  status: TemplateRevisionStatus;
  created_at: string;
}

export interface TemplateVariable {
  template_revision_id: string;
  name: string;
  type_expression: string;
  description: string;
  required: boolean;
  has_default: boolean;
  sensitive: boolean;
  has_validation: boolean;
}

export interface Stack {
  id: string;
  tenant_id: string;
  name: string;
  slug: string;
  tags: Record<string, string>;
  default_credential_ids: string[];
  created_by: string;
  created_at: string;
  // Per-stack authorization booleans resolved by the backend for AUTH-017; see auth/types.ts.
  effectiveCapabilities: StackCapabilities;
}

export interface StackTemplate {
  id: string;
  stack_id: string;
  component_key: string;
  source_template_id: string;
  desired_template_revision_id: string;
  last_applied_template_revision_id: string;
  // The desired revision's ref, resolved by the server per request. A label,
  // not component state — the component does not own a ref.
  source_ref: string;
  workspace_name: string;
  display_name: string;
  config: Record<string, unknown>;
  last_applied_run_id: string;
  last_applied_at?: string;
  pending_plan_run_id: string;
  pending_plan_at?: string;
  plan_state: PlanState;
  live_state: LiveState;
  created_by: string;
  lifecycle: string;
}

// Both states are computed by the server from snapshots the client never sees.
// "matches" on plan_state is the only condition under which an apply runs what
// was reviewed; anything else means the plan describes something other than
// desired state.
export type PlanState = "none" | "stale" | "matches";
export type LiveState = "never" | "differs" | "matches";

export interface StackView {
  stack: Stack;
  templates: StackTemplate[];
}

export interface CredentialMetadata {
  id: string;
  name: string;
  scope: "stack" | "stack_template";
  stack_id?: string;
  stack_template_id?: string;
  created_at: string;
}

export interface PlanSummary {
  add: number;
  change: number;
  destroy: number;
}

export interface TemplateRun {
  id: string;
  tenant_id: string;
  stack_template_id: string;
  template_revision_id: string;
  source_template_id: string;
  operation: Operation;
  selected_ref: string;
  resolved_commit_sha: string;
  workspace_name: string;
  config_json: Record<string, unknown>;
  backend_type: string;
  backend_config_hash: string;
  status: TemplateRunStatus;
  // Empty until the run starts its first step.
  step: TemplateRunStep | "";
  trigger_actor: string;
  started_at: string;
  completed_at?: string;
  error_summary: string;
  // Counts runs within one stack template, from 1. Shown as "Run #N".
  run_number: number;
  // An apply run that applies straight away, with no saved plan and no
  // approval.
  auto_approve: boolean;
  // What the plan would change, or, for an auto-approved apply, what it
  // changed. Null until a plan with changes, or the apply, finishes.
  plan_summary: PlanSummary | null;
}

export interface TemplateRunLog {
  tenant_id: string;
  run_id: string;
  phase: string;
  object_key: string;
  content_type: string;
  size_bytes: number;
  uploaded_at: string;
}

export interface GrantView {
  userSub: string;
  role: string;
  displayName: string;
  email: string;
}

export interface ListGrantsResponse {
  grants: GrantView[];
}

// A row of the local identity projection: what the user's ID token asserted at
// their last sign-in. `sub` is the only stable key and the value a grant is
// written against; the other two are display data.
export interface UserProfile {
  sub: string;
  displayName: string;
  email: string;
}

export interface SearchUsersResponse {
  users: UserProfile[];
  first: number;
  max: number;
}
