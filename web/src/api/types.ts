export type TemplateRegistrationStatus =
  | "pending"
  | "running"
  | "completed"
  | "invalid"
  | "failed";

export type TemplateStatus =
  | "pending_validation"
  | "validating"
  | "active"
  | "invalid";

export type TemplateRunStatus =
  | "queued"
  | "locked"
  | "workspace_prepared"
  | "source_fetched"
  | "init"
  | "workspace_selected"
  | "planned"
  | "waiting_approval"
  | "approved"
  | "apply_started"
  | "applied"
  | "destroy_started"
  | "destroyed"
  | "cancel_requested"
  | "canceling"
  | "canceled"
  | "lock_released"
  | "completed"
  | "failed";

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
  template_id: string;
  resolved_commit_sha: string;
  requested_by: string;
  requested_at: string;
  completed_at?: string;
  error_summary: string;
}

export interface Template {
  id: string;
  tenant_id: string;
  repo_owner: string;
  repo_name: string;
  source_ref: string;
  resolved_commit_sha: string;
  root_path: string;
  name: string;
  description: string;
  tags: string[];
  status: TemplateStatus;
  created_at: string;
}

export interface TemplateVariable {
  template_id: string;
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
}

export interface StackTemplate {
  id: string;
  stack_id: string;
  template_id: string;
  selected_ref: string;
  workspace_name: string;
  config: Record<string, unknown>;
  last_applied_run_id: string;
  last_applied_ref: string;
  last_applied_at?: string;
  created_by: string;
  lifecycle: string;
}

export interface StackView {
  stack: Stack;
  templates: StackTemplate[];
}

export interface TemplateRun {
  id: string;
  tenant_id: string;
  stack_template_id: string;
  operation: Operation;
  selected_ref: string;
  resolved_commit_sha: string;
  workspace_name: string;
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
