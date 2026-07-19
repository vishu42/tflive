// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  useStacksQuery,
  useStackQuery,
  useTemplateRevisionVariablesQuery,
  useTemplateRevisionsQuery
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
