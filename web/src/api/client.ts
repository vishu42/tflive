import { getUserManager } from "../auth/userManager";
import type {
  ApiErrorBody,
  Operation,
  Stack,
  StackTemplate,
  StackView,
  TemplateRevision,
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
}

interface CreateStackRequest {
  name: string;
  slug: string;
  tags: Record<string, string>;
  default_credential_ids: string[];
}

interface AddTemplateToStackRequest {
  template_revision_id: string;
  component_key?: string;
  selected_ref: string;
  config: Record<string, unknown>;
}

interface UpdateStackTemplateConfigRequest {
  config: Record<string, unknown>;
}

interface UpgradeStackTemplateRequest {
  target_template_revision_id: string;
  config?: Record<string, unknown>;
}

interface StartRunRequest {
  operation: Operation;
}

interface CancelRunRequest {
  reason: string;
}

export function registerTemplate(tenantID: string, body: RegisterTemplateRequest): Promise<TemplateRegistration> {
  return requestJSON(`/v1/tenants/${encodeURIComponent(tenantID)}/template-revisions`, {
    method: "POST",
    body: JSON.stringify(body)
  });
}

export function getTemplateRegistration(tenantID: string, registrationID: string): Promise<TemplateRegistration> {
  return requestJSON(`/v1/tenants/${encodeURIComponent(tenantID)}/template-registrations/${encodeURIComponent(registrationID)}`);
}

export function getTemplateRevisionVariables(tenantID: string, templateRevisionID: string): Promise<TemplateVariable[]> {
  return requestJSON(`/v1/tenants/${encodeURIComponent(tenantID)}/template-revisions/${encodeURIComponent(templateRevisionID)}/variables`);
}

export function listTemplateRevisions(tenantID: string): Promise<TemplateRevision[]> {
  return requestJSON(`/v1/tenants/${encodeURIComponent(tenantID)}/template-revisions`);
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

export function approveRun(tenantID: string, runID: string): Promise<void> {
  return requestNoContent(`/v1/tenants/${encodeURIComponent(tenantID)}/template-runs/${encodeURIComponent(runID)}/approval`, {
    method: "POST",
    body: JSON.stringify({})
  });
}

export function cancelRun(tenantID: string, runID: string, body: CancelRunRequest): Promise<void> {
  return requestNoContent(`/v1/tenants/${encodeURIComponent(tenantID)}/template-runs/${encodeURIComponent(runID)}/cancellation`, {
    method: "POST",
    body: JSON.stringify(body)
  });
}

async function authHeaders(): Promise<HeadersInit> {
  const user = await getUserManager().getUser();
  if (!user?.access_token) {
    return {};
  }
  return { authorization: `Bearer ${user.access_token}` };
}

async function fetchWithAuth(path: string, init: RequestInit): Promise<Response> {
  const headers = new Headers(init.headers);
  if (!headers.has("content-type")) {
    headers.set("content-type", "application/json");
  }

  const authHdrs = await authHeaders();
  for (const [key, value] of Object.entries(authHdrs)) {
    headers.set(key, value);
  }

  const response = await fetch(path, { method: "GET", ...init, headers });

  if (response.status === 401) {
    try {
      await getUserManager().signinSilent();
      const retryUser = await getUserManager().getUser();
      if (retryUser?.access_token) {
        const retryHeaders = new Headers(init.headers);
        if (!retryHeaders.has("content-type")) {
          retryHeaders.set("content-type", "application/json");
        }
        retryHeaders.set("authorization", `Bearer ${retryUser.access_token}`);
        return fetch(path, { method: "GET", ...init, headers: retryHeaders });
      }
    } catch {
      getUserManager().signinRedirect();
      return response;
    }
  }

  return response;
}

async function requestJSON<T>(path: string, init: RequestInit = {}): Promise<T> {
  const response = await fetchWithAuth(path, init);
  await throwForError(response);
  return response.json() as Promise<T>;
}

async function requestText(path: string, init: RequestInit = {}): Promise<string> {
  const response = await fetchWithAuth(path, init);
  await throwForError(response);
  return response.text();
}

async function requestNoContent(path: string, init: RequestInit = {}): Promise<void> {
  const response = await fetchWithAuth(path, init);
  await throwForError(response);
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
