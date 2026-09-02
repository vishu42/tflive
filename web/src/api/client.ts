import { loginLoopDetected } from "../auth/loginAttempts";
import type { Me } from "../auth/types";
import type {
  ApiErrorBody,
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

// The app's own sign-in screen. Not a server route: local accounts always
// exist — root is one and cannot be locked out — so there is always a password
// form to render, and an SSO button beside it only where a provider is
// configured. Which method to use is the person's choice, and sending the
// browser straight to the IdP would make it the client's.
export const signInPath = "/signin";

export function loginURL(): string {
  const returnTo = `${globalThis.location.pathname}${globalThis.location.search}`;
  return `${signInPath}?return_to=${encodeURIComponent(returnTo)}`;
}

// The OIDC authorization request, reached by a full navigation from the SSO
// button. Never fetch: it redirects to the provider's origin, where an XHR
// dies on CORS.
export function ssoLoginURL(returnTo: string): string {
  return `/v1/auth/login?return_to=${encodeURIComponent(returnTo)}`;
}

export interface AuthMethods {
  local: boolean;
  oidc: boolean;
}

// Which ways in this deployment has, so the sign-in screen renders the ones
// that exist rather than discovering them by failing. Public and pre-session by
// necessity: it is read before anyone has signed in.
export function authMethods(): Promise<AuthMethods> {
  return requestJSON(`/v1/auth/methods`, {}, { redirectOnUnauthorized: false });
}

// Signs in against tflive's own account table. Answers 204 and sets the session
// cookie; the identity comes from the /v1/me the caller makes afterwards.
//
// The 401 redirect is off. Here a 401 is a wrong password, not a lost session,
// and sending the browser to the sign-in screen it is already looking at would
// replace the error message with a page reload that loses what was typed.
export function signInWithPassword(username: string, password: string): Promise<void> {
  return requestNoContent(
    `/v1/auth/login`,
    { method: "POST", body: JSON.stringify({ username, password }) },
    { redirectOnUnauthorized: false }
  );
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

// Per-request overrides. Only the 401 behaviour is negotiable, and only two
// requests negotiate it: the ones the sign-in screen itself makes, where a 401
// is an answer rather than a lost session.
interface RequestOptions {
  redirectOnUnauthorized?: boolean;
}

async function fetchWithAuth(path: string, init: RequestInit, options: RequestOptions = {}): Promise<Response> {
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

  const redirect = options.redirectOnUnauthorized !== false;
  if (
    response.status === 401 &&
    redirect &&
    !loginLoopDetected() &&
    // Already there. The sign-in screen's own requests opt out above, so this
    // only catches a future one that forgets to, but the failure it prevents is
    // a reload that wipes the form mid-typing.
    globalThis.location.pathname !== signInPath
  ) {
    // A full navigation rather than a router push: this runs outside React, and
    // a reload is also what re-reads the session state from scratch. Once the
    // loop guard has tripped the navigation is skipped and the 401 surfaces as
    // an error instead, so the browser stops bouncing and SessionProvider can
    // explain what went wrong.
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

async function requestJSON<T>(path: string, init: RequestInit = {}, options: RequestOptions = {}): Promise<T> {
  const response = await fetchWithAuth(path, init, options);
  await throwForError(response);
  return response.json() as Promise<T>;
}

async function requestText(path: string, init: RequestInit = {}): Promise<string> {
  const response = await fetchWithAuth(path, init);
  await throwForError(response);
  return response.text();
}

async function requestNoContent(path: string, init: RequestInit = {}, options: RequestOptions = {}): Promise<void> {
  const response = await fetchWithAuth(path, init, options);
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
