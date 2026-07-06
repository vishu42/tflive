import { afterEach, describe, expect, it, vi } from "vitest";
import {
  addTemplateToStack,
  ApiRequestError,
  approveRun,
  cancelRun,
  createStack,
  getTemplateRunLog,
  listStacks,
  listTemplates,
  registerTemplate,
  startTemplateRun
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
      root_path: ".",
      requested_by: "user_123"
    });

    expect(fetchMock).toHaveBeenCalledWith(
      "/v1/tenants/tenant_123/templates",
      expect.objectContaining({
        method: "POST",
        body: JSON.stringify({
          repo_owner: "acme",
          repo_name: "infra",
          source_ref: "main",
          root_path: ".",
          requested_by: "user_123"
        })
      })
    );
  });

  it("posts stack creation with actor and tags", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(jsonResponse({ id: "stack_123" }));

    await createStack("tenant_123", {
      name: "Acme Prod",
      slug: "",
      tags: { env: "prod" },
      default_credential_ids: [],
      actor: "user_123"
    });

    expect(fetchMock).toHaveBeenCalledWith(
      "/v1/tenants/tenant_123/stacks",
      expect.objectContaining({ method: "POST" })
    );
  });

  it("lists tenant stacks and templates", async () => {
    const fetchMock = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValueOnce(jsonResponse([{ id: "stack_123" }]))
      .mockResolvedValueOnce(jsonResponse([{ id: "template_123" }]));

    const stacks = await listStacks("tenant_123");
    const templates = await listTemplates("tenant_123");

    expect(stacks).toEqual([{ id: "stack_123" }]);
    expect(templates).toEqual([{ id: "template_123" }]);
    expect(fetchMock).toHaveBeenNthCalledWith(
      1,
      "/v1/tenants/tenant_123/stacks",
      expect.objectContaining({ method: "GET" })
    );
    expect(fetchMock).toHaveBeenNthCalledWith(
      2,
      "/v1/tenants/tenant_123/templates",
      expect.objectContaining({ method: "GET" })
    );
  });

  it("posts template install config", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(jsonResponse({ id: "stack_template_123" }));

    await addTemplateToStack("tenant_123", "stack_123", {
      template_id: "template_123",
      selected_ref: "main",
      config: { region: "us-east-1" },
      actor: "user_123"
    });

    expect(fetchMock).toHaveBeenCalledWith(
      "/v1/tenants/tenant_123/stacks/stack_123/templates",
      expect.objectContaining({ method: "POST" })
    );
  });

  it("posts run operations, approvals, and cancellations", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(jsonResponse({ id: "run_123" }));

    await startTemplateRun("tenant_123", "stack_template_123", { operation: "apply", trigger_actor: "user_123" });
    await approveRun("tenant_123", "run_123", { approved_by: "user_123" });
    await cancelRun("tenant_123", "run_456", { requested_by: "user_123", reason: "manual stop" });

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
        default_credential_ids: [],
        actor: "user_123"
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
