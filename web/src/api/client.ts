import { loginLoopDetected, recordLoginAttempt } from "../auth/loginAttempts";
import type { Me } from "../auth/types";
import type {
  ApiErrorBody,
  DirectoryUser,
  CredentialMetadata,
  GrantView,
  ListGrantsResponse,
  Operation,
  SearchUsersResponse,
  Stack,
  StackTemplate,
  StackView,
  TemplateRegistration,
  TemplateRevision,
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

interface CredentialRequest {
  name: string;
  value: string;
}

interface CancelRunRequest {
  reason: string;
}

export function getMe(): Promise<Me> {
  return requestJSON("/v1/me");
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

/** Lists Stack credential names and metadata; secret values are never returned. */
export function listStackCredentials(tenantID: string, stackID: string): Promise<CredentialMetadata[]> {
  return requestJSON(`/v1/tenants/${encodeURIComponent(tenantID)}/stacks/${encodeURIComponent(stackID)}/credentials`);
}

/** Stores one Stack-scoped credential and returns write-only metadata. */
export function createStackCredential(tenantID: string, stackID: string, body: CredentialRequest): Promise<CredentialMetadata> {
  return requestJSON(`/v1/tenants/${encodeURIComponent(tenantID)}/stacks/${encodeURIComponent(stackID)}/credentials`, { method: "POST", body: JSON.stringify(body) });
}

/** Deletes one Stack-scoped credential. */
export function deleteStackCredential(tenantID: string, stackID: string, credentialID: string): Promise<void> {
  return requestNoContent(`/v1/tenants/${encodeURIComponent(tenantID)}/stacks/${encodeURIComponent(stackID)}/credentials/${encodeURIComponent(credentialID)}`, { method: "DELETE" });
}

/** Lists StackTemplate credential names and metadata without secret values. */
export function listStackTemplateCredentials(tenantID: string, stackTemplateID: string): Promise<CredentialMetadata[]> {
  return requestJSON(`/v1/tenants/${encodeURIComponent(tenantID)}/stack-templates/${encodeURIComponent(stackTemplateID)}/credentials`);
}

/** Stores one StackTemplate-scoped credential and returns write-only metadata. */
export function createStackTemplateCredential(tenantID: string, stackTemplateID: string, body: CredentialRequest): Promise<CredentialMetadata> {
  return requestJSON(`/v1/tenants/${encodeURIComponent(tenantID)}/stack-templates/${encodeURIComponent(stackTemplateID)}/credentials`, { method: "POST", body: JSON.stringify(body) });
}

/** Deletes one StackTemplate-scoped credential. */
export function deleteStackTemplateCredential(tenantID: string, stackTemplateID: string, credentialID: string): Promise<void> {
  return requestNoContent(`/v1/tenants/${encodeURIComponent(tenantID)}/stack-templates/${encodeURIComponent(stackTemplateID)}/credentials/${encodeURIComponent(credentialID)}`, { method: "DELETE" });
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

export function listTemplateRuns(tenantID: string, stackTemplateID: string): Promise<TemplateRun[]> {
  return requestJSON(`/v1/tenants/${encodeURIComponent(tenantID)}/stack-templates/${encodeURIComponent(stackTemplateID)}/runs`);
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

interface AssignStackRoleBody {
  user_sub: string;
  role: string;
}

export function listStackGrants(tenantID: string, stackID: string): Promise<ListGrantsResponse> {
  return requestJSON(`/v1/tenants/${encodeURIComponent(tenantID)}/stacks/${encodeURIComponent(stackID)}/grants`);
}

export function assignStackRole(tenantID: string, stackID: string, body: AssignStackRoleBody): Promise<GrantView> {
  return requestJSON(`/v1/tenants/${encodeURIComponent(tenantID)}/stacks/${encodeURIComponent(stackID)}/grants`, {
    method: "POST",
    body: JSON.stringify(body)
  });
}

export function revokeStackRole(tenantID: string, stackID: string, userSub: string): Promise<void> {
  return requestNoContent(`/v1/tenants/${encodeURIComponent(tenantID)}/stacks/${encodeURIComponent(stackID)}/grants/${encodeURIComponent(userSub)}`, {
    method: "DELETE"
  });
}

export function searchUsers(tenantID: string, q: string, first: number, max: number): Promise<SearchUsersResponse> {
  const params = new URLSearchParams({ q });
  if (first > 0) {
    params.set("first", String(first));
  }
  params.set("max", String(max));
  return requestJSON(`/v1/tenants/${encodeURIComponent(tenantID)}/users/search?${params.toString()}`);
}

export function loginURL(): string {
  const returnTo = `${globalThis.location.pathname}${globalThis.location.search}`;
  return `/v1/auth/login?return_to=${encodeURIComponent(returnTo)}`;
}

// Every request this module makes carries the session cookie, and the server
// slides the session's idle bound on any request that arrives more than a touch
// interval after the last one. SessionProvider counts them so it can tell "the
// bound may have moved since our last snapshot" from "this tab has said
// nothing at all", which is what keeps its expiry timer from renewing an
// unattended tab forever.
let requestsMade = 0;

export function apiRequestsMade(): number {
  return requestsMade;
}

async function fetchWithAuth(path: string, init: RequestInit): Promise<Response> {
  const headers = new Headers(init.headers);
  if (!headers.has("content-type")) {
    headers.set("content-type", "application/json");
  }

  // The session cookie is httpOnly: the browser attaches it and this code
  // cannot read it. There is no bearer header and nothing to renew.
  const response = await fetch(path, { method: "GET", ...init, headers, credentials: "same-origin" });
  // Counted once the server has answered, so the count only ever covers
  // requests that actually reached it and could have slid the bound.
  requestsMade += 1;

  if (response.status === 401 && !loginLoopDetected()) {
    // A full navigation, never fetch: following the redirect to the IdP as an
    // XHR would hit its origin cross-origin and die on CORS. Once the loop
    // guard has tripped, the redirect is skipped and the 401 surfaces as an
    // error instead, so the browser stops bouncing and SessionProvider can
    // explain what went wrong.
    recordLoginAttempt();
    globalThis.location.assign(loginURL());
  }

  return response;
}

export function logout(): void {
  // A real form POST, never fetch. The response is a 303 whose Location carries
  // the ID token as id_token_hint; a navigation lets the browser follow it
  // without script ever reading that header. POST rather than a link so a
  // cross-site image tag cannot log the user out.
  const form = document.createElement("form");
  form.method = "POST";
  form.action = "/v1/auth/logout";
  document.body.appendChild(form);
  form.submit();
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
