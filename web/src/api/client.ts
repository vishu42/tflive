import type {
  ApiErrorBody,
  Operation,
  Stack,
  StackTemplate,
  StackView,
  Template,
  TemplateRegistration,
  TemplateRun,
  TemplateRunLog,
  TemplateVariable
} from "./types";

export class ApiRequestError extends Error {
  constructor(
    public readonly status: number,
    public readonly code: string,
    message: string
  ) {
    super(message);
    this.name = "ApiRequestError";
  }
}

interface RegisterTemplateRequest {
  repo_owner: string;
  repo_name: string;
  source_ref: string;
  root_path: string;
  requested_by: string;
}

interface CreateStackRequest {
  name: string;
  slug: string;
  tags: Record<string, string>;
  default_credential_ids: string[];
  actor: string;
}

interface AddTemplateToStackRequest {
  template_id: string;
  component_key?: string;
  selected_ref: string;
  config: Record<string, unknown>;
  actor: string;
}

interface UpdateStackTemplateConfigRequest {
  config: Record<string, unknown>;
  actor: string;
}

interface UpgradeStackTemplateRequest {
  target_template_revision_id: string;
  config?: Record<string, unknown>;
  actor: string;
}

interface StartRunRequest {
  operation: Operation;
  trigger_actor: string;
}

interface ApproveRunRequest {
  approved_by: string;
}

interface CancelRunRequest {
  requested_by: string;
  reason: string;
}

export function registerTemplate(tenantID: string, body: RegisterTemplateRequest): Promise<TemplateRegistration> {
  return requestJSON(`/v1/tenants/${encodeURIComponent(tenantID)}/templates`, {
    method: "POST",
    body: JSON.stringify(body)
  });
}

export function getTemplateRegistration(tenantID: string, registrationID: string): Promise<TemplateRegistration> {
  return requestJSON(`/v1/tenants/${encodeURIComponent(tenantID)}/template-registrations/${encodeURIComponent(registrationID)}`);
}

export function getTemplateVariables(tenantID: string, templateID: string): Promise<TemplateVariable[]> {
  return requestJSON(`/v1/tenants/${encodeURIComponent(tenantID)}/templates/${encodeURIComponent(templateID)}/variables`);
}

export function listTemplates(tenantID: string): Promise<Template[]> {
  return requestJSON(`/v1/tenants/${encodeURIComponent(tenantID)}/templates`);
}

export function createStack(tenantID: string, body: CreateStackRequest): Promise<Stack> {
  return requestJSON(`/v1/tenants/${encodeURIComponent(tenantID)}/stacks`, {
    method: "POST",
    body: JSON.stringify(body)
  });
}

export function listStacks(tenantID: string): Promise<Stack[]> {
  return requestJSON(`/v1/tenants/${encodeURIComponent(tenantID)}/stacks`);
}

export function getStack(tenantID: string, stackID: string): Promise<StackView> {
  return requestJSON(`/v1/tenants/${encodeURIComponent(tenantID)}/stacks/${encodeURIComponent(stackID)}`);
}

export function addTemplateToStack(tenantID: string, stackID: string, body: AddTemplateToStackRequest): Promise<StackTemplate> {
  return requestJSON(`/v1/tenants/${encodeURIComponent(tenantID)}/stacks/${encodeURIComponent(stackID)}/templates`, {
    method: "POST",
    body: JSON.stringify(body)
  });
}

export function updateStackTemplateConfig(tenantID: string, stackTemplateID: string, body: UpdateStackTemplateConfigRequest): Promise<StackTemplate> {
  return requestJSON(`/v1/tenants/${encodeURIComponent(tenantID)}/stack-templates/${encodeURIComponent(stackTemplateID)}/config`, {
    method: "PATCH",
    body: JSON.stringify(body)
  });
}

export function upgradeStackTemplate(tenantID: string, stackTemplateID: string, body: UpgradeStackTemplateRequest): Promise<StackTemplate> {
  return requestJSON(`/v1/tenants/${encodeURIComponent(tenantID)}/stack-templates/${encodeURIComponent(stackTemplateID)}/upgrade`, {
    method: "POST",
    body: JSON.stringify(body)
  });
}

export function startTemplateRun(tenantID: string, stackTemplateID: string, body: StartRunRequest): Promise<TemplateRun> {
  return requestJSON(`/v1/tenants/${encodeURIComponent(tenantID)}/stack-templates/${encodeURIComponent(stackTemplateID)}/runs`, {
    method: "POST",
    body: JSON.stringify(body)
  });
}

export function getTemplateRun(tenantID: string, runID: string): Promise<TemplateRun> {
  return requestJSON(`/v1/tenants/${encodeURIComponent(tenantID)}/template-runs/${encodeURIComponent(runID)}`);
}

export function listTemplateRunLogs(tenantID: string, runID: string): Promise<TemplateRunLog[]> {
  return requestJSON(`/v1/tenants/${encodeURIComponent(tenantID)}/template-runs/${encodeURIComponent(runID)}/logs`);
}

export function getTemplateRunLog(tenantID: string, runID: string, phase: string): Promise<string> {
  return requestText(`/v1/tenants/${encodeURIComponent(tenantID)}/template-runs/${encodeURIComponent(runID)}/logs/${encodeURIComponent(phase)}`);
}

export function approveRun(tenantID: string, runID: string, body: ApproveRunRequest): Promise<void> {
  return requestNoContent(`/v1/tenants/${encodeURIComponent(tenantID)}/template-runs/${encodeURIComponent(runID)}/approval`, {
    method: "POST",
    body: JSON.stringify(body)
  });
}

export function cancelRun(tenantID: string, runID: string, body: CancelRunRequest): Promise<void> {
  return requestNoContent(`/v1/tenants/${encodeURIComponent(tenantID)}/template-runs/${encodeURIComponent(runID)}/cancellation`, {
    method: "POST",
    body: JSON.stringify(body)
  });
}

async function requestJSON<T>(path: string, init: RequestInit = {}): Promise<T> {
  const response = await fetch(path, withJSONHeaders(init));
  await throwForError(response);
  return response.json() as Promise<T>;
}

async function requestText(path: string, init: RequestInit = {}): Promise<string> {
  const response = await fetch(path, withJSONHeaders(init));
  await throwForError(response);
  return response.text();
}

async function requestNoContent(path: string, init: RequestInit = {}): Promise<void> {
  const response = await fetch(path, withJSONHeaders(init));
  await throwForError(response);
}

function withJSONHeaders(init: RequestInit): RequestInit {
  const headers = new Headers(init.headers);
  if (!headers.has("content-type")) {
    headers.set("content-type", "application/json");
  }
  return {
    method: "GET",
    ...init,
    headers
  };
}

async function throwForError(response: Response): Promise<void> {
  if (response.ok) {
    return;
  }

  let body: ApiErrorBody = { error: "request_failed", message: response.statusText || "request failed" };
  if (response.headers.get("content-type")?.includes("application/json")) {
    body = (await response.json()) as ApiErrorBody;
  }
  throw new ApiRequestError(response.status, body.error, body.message);
}
