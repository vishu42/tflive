// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  registrationRefetchInterval,
  runRefetchInterval,
  useStacksQuery,
  useStackQuery,
  useTemplateRegistrationQuery,
  useTemplateRevisionVariablesQuery,
  useTemplateRevisionsQuery,
  useTemplateRunLogQuery,
  useTemplateRunLogsQuery,
  useTemplateRunQuery
} from "./queries";

function wrapper(queryClient: QueryClient) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
  };
}

function testQueryClient(): QueryClient {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } });
}

function jsonResponse(body: unknown): Response {
  return new Response(JSON.stringify(body), { status: 200, headers: { "content-type": "application/json" } });
}

describe("read-only query hooks", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("fetches the tenant's stacks", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(jsonResponse([{ id: "stack_1" }]));
    const { result } = renderHook(() => useStacksQuery("tenant_123"), { wrapper: wrapper(testQueryClient()) });

    await waitFor(() => expect(result.current.data).toEqual([{ id: "stack_1" }]));
    expect(fetch).toHaveBeenCalledWith("/v1/tenants/tenant_123/stacks", expect.objectContaining({ method: "GET" }));
  });

  it("fetches the tenant's template revisions", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(jsonResponse([{ id: "rev_1" }]));
    const { result } = renderHook(() => useTemplateRevisionsQuery("tenant_123"), { wrapper: wrapper(testQueryClient()) });

    await waitFor(() => expect(result.current.data).toEqual([{ id: "rev_1" }]));
    expect(fetch).toHaveBeenCalledWith(
      "/v1/tenants/tenant_123/template-revisions",
      expect.objectContaining({ method: "GET" })
    );
  });

  it("fetches stack detail only when a stack id is given", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(jsonResponse({ stack: { id: "stack_1" }, templates: [] }));
    const { result, rerender } = renderHook(({ stackID }: { stackID: string }) => useStackQuery("tenant_123", stackID), {
      wrapper: wrapper(testQueryClient()),
      initialProps: { stackID: "" }
    });

    expect(result.current.fetchStatus).toBe("idle");
    expect(fetchMock).not.toHaveBeenCalled();

    rerender({ stackID: "stack_1" });
    await waitFor(() => expect(result.current.data).toEqual({ stack: { id: "stack_1" }, templates: [] }));
    expect(fetchMock).toHaveBeenCalledWith("/v1/tenants/tenant_123/stacks/stack_1", expect.objectContaining({ method: "GET" }));
  });

  it("fetches template revision variables only when a revision id is given", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(jsonResponse([{ name: "region" }]));
    const { result } = renderHook(() => useTemplateRevisionVariablesQuery("tenant_123", ""), {
      wrapper: wrapper(testQueryClient())
    });

    expect(result.current.fetchStatus).toBe("idle");
    expect(fetchMock).not.toHaveBeenCalled();
  });
});

describe("polling-aware query hooks", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("fetches template registration status", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(jsonResponse({ id: "reg_1", status: "running" }));
    const { result } = renderHook(() => useTemplateRegistrationQuery("tenant_123", "reg_1"), {
      wrapper: wrapper(testQueryClient())
    });

    await waitFor(() => expect(result.current.data).toEqual({ id: "reg_1", status: "running" }));
  });

  it("fetches a template run's status regardless of poll option", async () => {
    vi.spyOn(globalThis, "fetch").mockImplementation(() =>
      Promise.resolve(jsonResponse({ id: "run_1", status: "planned" }))
    );
    const { result: polled } = renderHook(() => useTemplateRunQuery("tenant_123", "run_1", { poll: true }), {
      wrapper: wrapper(testQueryClient())
    });
    await waitFor(() => expect(polled.current.data).toEqual({ id: "run_1", status: "planned" }));

    const { result: unpolled } = renderHook(() => useTemplateRunQuery("tenant_123", "run_1", { poll: false }), {
      wrapper: wrapper(testQueryClient())
    });
    await waitFor(() => expect(unpolled.current.data).toEqual({ id: "run_1", status: "planned" }));
  });

  describe("refetch interval selectors", () => {
    it("polls a non-terminal registration and stops once terminal", () => {
      expect(registrationRefetchInterval("running")).toBe(1500);
      expect(registrationRefetchInterval("completed")).toBe(false);
      expect(registrationRefetchInterval(undefined)).toBe(1500);
    });

    it("only polls a run when told to, and stops on terminal status", () => {
      expect(runRefetchInterval("planned", true)).toBe(1500);
      expect(runRefetchInterval("completed", true)).toBe(false);
      expect(runRefetchInterval("planned", false)).toBe(false);
    });
  });

  it("keys run logs by status so a status change produces a refetch", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(jsonResponse([{ phase: "plan" }]));
    const queryClient = testQueryClient();
    const { result, rerender } = renderHook(
      ({ statusTag }: { statusTag: string }) => useTemplateRunLogsQuery("tenant_123", "run_1", statusTag),
      { wrapper: wrapper(queryClient), initialProps: { statusTag: "planned" } }
    );
    await waitFor(() => expect(result.current.data).toEqual([{ phase: "plan" }]));
    expect(fetchMock).toHaveBeenCalledTimes(1);

    rerender({ statusTag: "completed" });
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2));
    expect(fetchMock).toHaveBeenLastCalledWith(
      "/v1/tenants/tenant_123/template-runs/run_1/logs",
      expect.objectContaining({ method: "GET" })
    );
  });

  it("fetches a single log phase as text", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response("plan output\n", { status: 200, headers: { "content-type": "text/plain" } })
    );
    const { result } = renderHook(() => useTemplateRunLogQuery("tenant_123", "run_1", "plan", "planned"), {
      wrapper: wrapper(testQueryClient())
    });

    await waitFor(() => expect(result.current.data).toBe("plan output\n"));
  });
});
