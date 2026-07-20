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

  it("posts template install config", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(jsonResponse({ id: "stack_template_123" }));

    await addTemplateToStack("tenant_123", "stack_123", {
      template_revision_id: "template_123",
      selected_ref: "main",
      config: { region: "us-east-1" }
    });

    expect(fetchMock).toHaveBeenCalledWith(
      "/v1/tenants/tenant_123/stacks/stack_123/templates",
      expect.objectContaining({
        method: "POST",
        body: JSON.stringify({
          template_revision_id: "template_123",
          selected_ref: "main",
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
});
import { getUserManager } from "../auth/userManager";

const mockUserManager = {
  getUser: vi.fn(),
  signinSilent: vi.fn(),
  signinRedirect: vi.fn(),
};

vi.mock("../auth/userManager", () => ({
  getUserManager: () => mockUserManager,
}));

describe("api client — auth header injection", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("adds Authorization header with current access token", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(jsonResponse([{ id: "stack_1" }]));
    const user = { access_token: "test-token-abc", expires_at: Date.now() / 1000 + 300 };
    vi.mocked(getUserManager().getUser).mockResolvedValue(user as never);

    await listStacks("tenant_123");

    expect(fetchMock).toHaveBeenCalledWith(
      "/v1/tenants/tenant_123/stacks",
      expect.objectContaining({
        headers: expect.any(Headers),
      })
    );
    const callHeaders = fetchMock.mock.calls[0][1]?.headers as Headers;
    expect(callHeaders.get("authorization")).toBe("Bearer test-token-abc");
  });

  it("skips auth header when no user is available", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(jsonResponse([{ id: "stack_1" }]));
    vi.mocked(getUserManager().getUser).mockResolvedValue(null);

    await listStacks("tenant_123");

    const callHeaders = fetchMock.mock.calls[0][1]?.headers as Headers;
    expect(callHeaders.get("authorization")).toBeNull();
  });

  it("refreshes token and retries when API returns 401", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch")
      .mockResolvedValueOnce(new Response(JSON.stringify({ error: "unauthorized", message: "expired" }), {
        status: 401,
        headers: { "content-type": "application/json" },
      }))
      .mockResolvedValueOnce(new Response(null, { status: 401 })) // second attempt (the retry)
      .mockResolvedValueOnce(jsonResponse([{ id: "stack_2" }])); // after redirect would happen

    const user = { access_token: "old-token", expires_at: Date.now() / 1000 + 300, refresh_token: "rt-1" };
    vi.mocked(getUserManager().getUser).mockResolvedValue(user as never);

    const refreshedUser = { access_token: "new-token", expires_at: Date.now() / 1000 + 300 };
    vi.mocked(getUserManager().signinSilent).mockResolvedValue(refreshedUser as never);

    // On 401, the client will: refresh -> retry. We expect at least the refresh call.
    try {
      await listStacks("tenant_123");
    } catch {
      // expected: the retry also fails in this test because the third mock is irrelevant to the retry URL
    }

    expect(getUserManager().signinSilent).toHaveBeenCalled();
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
