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

export type TemplateRunStatus =
  | "queued"
  | "locked"
  | "workspace_prepared"
  | "source_fetched"
  | "workspace_selected"
  | "waiting_approval"
  | "approved"
  | "cancel_requested"
  | "canceling"
  | "canceled"
  | "lock_released"
  | "completed"
  | "failed"
  | "init_started"
  | "init_finished"
  | "plan_started"
  | "plan_finished"
  | "apply_started"
  | "apply_finished"
  | "destroy_started"
  | "destroy_finished";

export type Operation = "plan" | "apply" | "destroy";

export interface ApiErrorBody {
  error: string;
  message: string;
}

export interface TemplateRegistration {
  id: string;
  tenant_id: string;
  repo_owner: string;
  repo_name: string;
  source_ref: string;
  root_path: string;
  status: TemplateRegistrationStatus;
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
  last_planned_run_id: string;
  last_planned_at?: string;
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
  trigger_actor: string;
  started_at: string;
  completed_at?: string;
  error_summary: string;
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
