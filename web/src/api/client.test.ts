// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  addTemplateToStack,
  ApiRequestError,
  approveRun,
  cancelRun,
  createStack,
  getTemplateRunLog,
  listStacks,
  listTemplateRevisions,
  listTemplateRuns,
  logout,
  registerTemplate,
  startTemplateRun,
  updateStackTemplateConfig,
  upgradeStackTemplate
} from "./client";

describe("api client", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("posts template registration to the tenant route", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(jsonResponse({ id: "template_registration_123" }));

    await registerTemplate("tenant_123", {
      repo_owner: "acme",
      repo_name: "infra",
      source_ref: "main",
      root_path: "."
    });

    expect(fetchMock).toHaveBeenCalledWith(
      "/v1/tenants/tenant_123/template-revisions",
      expect.objectContaining({
        method: "POST",
        body: JSON.stringify({
          repo_owner: "acme",
          repo_name: "infra",
          source_ref: "main",
          root_path: "."
        })
      })
    );
  });

  it("posts stack creation with tags", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(jsonResponse({ id: "stack_123" }));

    await createStack("tenant_123", {
      name: "Acme Prod",
      slug: "",
      tags: { env: "prod" },
      default_credential_ids: []
    });

    expect(fetchMock).toHaveBeenCalledWith(
      "/v1/tenants/tenant_123/stacks",
      expect.objectContaining({
        method: "POST",
        body: JSON.stringify({
          name: "Acme Prod",
          slug: "",
          tags: { env: "prod" },
          default_credential_ids: []
        })
      })
    );
  });

  it("lists tenant stacks and template revisions", async () => {
    const fetchMock = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValueOnce(jsonResponse([{ id: "stack_123" }]))
      .mockResolvedValueOnce(jsonResponse([{ id: "template_123" }]));

    const stacks = await listStacks("tenant_123");
    const templateRevisions = await listTemplateRevisions("tenant_123");

    expect(stacks).toEqual([{ id: "stack_123" }]);
    expect(templateRevisions).toEqual([{ id: "template_123" }]);
    expect(fetchMock).toHaveBeenNthCalledWith(
      1,
      "/v1/tenants/tenant_123/stacks",
      expect.objectContaining({ method: "GET" })
    );
    expect(fetchMock).toHaveBeenNthCalledWith(
      2,
      "/v1/tenants/tenant_123/template-revisions",
      expect.objectContaining({ method: "GET" })
    );
  });

  it("lists runs for a stack template", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(jsonResponse([{ id: "run_1", status: "waiting_approval" }]));

    const runs = await listTemplateRuns("tenant_123", "stpl_1");

    expect(runs).toEqual([{ id: "run_1", status: "waiting_approval" }]);
    expect(fetchMock).toHaveBeenCalledWith(
      "/v1/tenants/tenant_123/stack-templates/stpl_1/runs",
      expect.objectContaining({ method: "GET" })
    );
  });

  it("posts template install config", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(jsonResponse({ id: "stack_template_123" }));

    await addTemplateToStack("tenant_123", "stack_123", {
      template_revision_id: "template_123",
      config: { region: "us-east-1" }
    });

    expect(fetchMock).toHaveBeenCalledWith(
      "/v1/tenants/tenant_123/stacks/stack_123/templates",
      expect.objectContaining({
        method: "POST",
        body: JSON.stringify({
          template_revision_id: "template_123",
          config: { region: "us-east-1" }
        })
      })
    );
  });

  it("edits and upgrades installed stack templates", async () => {
    const fetchMock = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValueOnce(jsonResponse({ id: "stack_template_123" }))
      .mockResolvedValueOnce(jsonResponse({ id: "stack_template_123" }));

    await updateStackTemplateConfig("tenant_123", "stack_template_123", {
      config: { region: "us-west-2" }
    });
    await upgradeStackTemplate("tenant_123", "stack_template_123", {
      target_template_revision_id: "template_rev_2"
    });

    expect(fetchMock).toHaveBeenNthCalledWith(
      1,
      "/v1/tenants/tenant_123/stack-templates/stack_template_123/config",
      expect.objectContaining({
        method: "PATCH",
        body: JSON.stringify({ config: { region: "us-west-2" } })
      })
    );
    expect(fetchMock).toHaveBeenNthCalledWith(
      2,
      "/v1/tenants/tenant_123/stack-templates/stack_template_123/upgrade",
      expect.objectContaining({
        method: "POST",
        body: JSON.stringify({ target_template_revision_id: "template_rev_2" })
      })
    );
  });

  it("posts run operations, approvals, and cancellations", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(jsonResponse({ id: "run_123" }));

    await startTemplateRun("tenant_123", "stack_template_123", { operation: "apply" });
    await approveRun("tenant_123", "run_123");
    await cancelRun("tenant_123", "run_456", { reason: "manual stop" });

    expect(fetchMock).toHaveBeenNthCalledWith(
      1,
      "/v1/tenants/tenant_123/stack-templates/stack_template_123/runs",
      expect.objectContaining({ method: "POST" })
    );
    expect(fetchMock).toHaveBeenNthCalledWith(
      2,
      "/v1/tenants/tenant_123/template-runs/run_123/approval",
      expect.objectContaining({ method: "POST" })
    );
    expect(fetchMock).toHaveBeenNthCalledWith(
      3,
      "/v1/tenants/tenant_123/template-runs/run_456/cancellation",
      expect.objectContaining({ method: "POST" })
    );
    expect(JSON.parse(String(fetchMock.mock.calls[0][1]?.body))).toEqual({ operation: "apply" });
    expect(JSON.parse(String(fetchMock.mock.calls[1][1]?.body))).toEqual({});
    expect(JSON.parse(String(fetchMock.mock.calls[2][1]?.body))).toEqual({ reason: "manual stop" });
  });

  it("reads log bodies as text", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(textResponse("plan output\n"));

    const body = await getTemplateRunLog("tenant_123", "run_123", "plan");

    expect(body).toBe("plan output\n");
    expect(fetchMock).toHaveBeenCalledWith(
      "/v1/tenants/tenant_123/template-runs/run_123/logs/plan",
      expect.objectContaining({ method: "GET" })
    );
  });

  it("throws ApiRequestError with backend message", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(jsonResponse({ error: "invalid_request", message: "bad input" }, 400));

    try {
      await createStack("tenant_123", {
        name: "",
        slug: "",
        tags: {},
        default_credential_ids: []
      });
      throw new Error("expected createStack to fail");
    } catch (error) {
      expect(error).toBeInstanceOf(ApiRequestError);
      expect(error).toMatchObject({
        status: 400,
        code: "invalid_request",
        message: "bad input"
      });
    }
  });

  it("never includes actor identity fields in request bodies", async () => {
    const forbiddenFields = ["actor", "actor_id", "sub", "user_id", "on_behalf_of"];

    const fetchMock = vi.spyOn(globalThis, "fetch").mockImplementation(() => Promise.resolve(jsonResponse({ id: "ok" })));

    await registerTemplate("tenant_123", { repo_owner: "a", repo_name: "b", source_ref: "main", root_path: "." });
    await createStack("tenant_123", { name: "s", slug: "", tags: {}, default_credential_ids: [] });
    await addTemplateToStack("tenant_123", "stack_1", { template_revision_id: "rev_1", config: {} });
    await updateStackTemplateConfig("tenant_123", "st_1", { config: {} });
    await upgradeStackTemplate("tenant_123", "st_1", { target_template_revision_id: "rev_2" });
    await startTemplateRun("tenant_123", "st_1", { operation: "plan" });
    await approveRun("tenant_123", "run_1");
    await cancelRun("tenant_123", "run_2", { reason: "stop" });

    for (const call of fetchMock.mock.calls) {
      const init = call[1] as RequestInit | undefined;
      if (init?.body && typeof init.body === "string") {
        const parsed = JSON.parse(init.body);
        for (const field of forbiddenFields) {
          const hasField = Object.prototype.hasOwnProperty.call(parsed, field);
          expect(hasField, `request body contains forbidden field "${field}": ${JSON.stringify(parsed)}`).toBe(false);
        }
      }
    }
  });
});

describe("api client — session cookie auth", () => {
  const assign = vi.fn();

  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it("sends requests with same-origin credentials and no bearer header", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(jsonResponse([{ id: "stack_1" }]));

    await listStacks("tenant_123");

    expect(fetchMock).toHaveBeenCalledWith(
      "/v1/tenants/tenant_123/stacks",
      expect.objectContaining({ credentials: "same-origin" })
    );
    const callHeaders = fetchMock.mock.calls[0][1]?.headers as Headers;
    expect(callHeaders.get("authorization")).toBeNull();
  });

  it("navigates to the login route on a 401, never fetches it", async () => {
    vi.stubGlobal("location", { assign, pathname: "/stacks", search: "?selected=st_1" });
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({ error: "unauthorized", message: "expired" }), {
        status: 401,
        headers: { "content-type": "application/json" }
      })
    );

    try {
      await listStacks("tenant_123");
    } catch {
      // A 401 still throws ApiRequestError for the caller; the navigation
      // above is what actually recovers the session.
    }

    expect(assign).toHaveBeenCalledWith("/v1/auth/login?return_to=%2Fstacks%3Fselected%3Dst_1");
  });
});

describe("api client — logout()", () => {
  afterEach(() => {
    vi.restoreAllMocks();
    document.body.innerHTML = "";
  });

  // Regression guard for the fix two tasks prior: the /v1/auth/logout 303's
  // Location header carries the raw ID token as id_token_hint. A Location
  // header is not script-readable, but a fetch response body is — so
  // logout() must drive a real browser navigation via a form POST, never
  // fetch(), or a future edit reintroduces that credential leak silently.
  it("logs out via a real form POST, never fetch, so the id_token_hint in the 303 Location never reaches script", () => {
    const fetchMock = vi.spyOn(globalThis, "fetch");
    // jsdom does not implement real form submission and logs a "not
    // implemented" error if left unstubbed.
    const submitMock = vi.spyOn(HTMLFormElement.prototype, "submit").mockImplementation(() => {});

    logout();

    expect(fetchMock).not.toHaveBeenCalled();
    const form = document.body.querySelector("form");
    expect(form).not.toBeNull();
    expect(form?.method).toBe("post");
    expect(form?.getAttribute("action")).toBe("/v1/auth/logout");
    expect(submitMock).toHaveBeenCalledTimes(1);
  });
});

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "content-type": "application/json" }
  });
}

function textResponse(body: string, status = 200): Response {
  return new Response(body, {
    status,
    headers: { "content-type": "text/plain; charset=utf-8" }
  });
}
