// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  registrationRefetchInterval,
  runRefetchInterval,
  useAddTemplateToStackMutation,
  useApproveRunMutation,
  useCancelRunMutation,
  useCreateStackMutation,
  useRegisterTemplateMutation,
  useStacksQuery,
  useStackQuery,
  useStartTemplateRunMutation,
  useTemplateRegistrationQuery,
  useTemplateRevisionVariablesQuery,
  useTemplateRevisionsQuery,
  useTemplateRunLogQuery,
  useTemplateRunLogsQuery,
  useTemplateRunQuery,
  useUpdateStackTemplateConfigMutation,
  useUpgradeStackTemplateMutation
} from "./queries";
import { queryKeys } from "./queryKeys";

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

  it("keeps showing the previous status's logs while the new status's fetch is in flight", async () => {
    let resolveSecondFetch: ((response: Response) => void) | undefined;
    const fetchMock = vi.spyOn(globalThis, "fetch").mockImplementationOnce(() =>
      Promise.resolve(jsonResponse([{ phase: "plan" }]))
    );
    const queryClient = testQueryClient();
    const { result, rerender } = renderHook(
      ({ statusTag }: { statusTag: string }) => useTemplateRunLogsQuery("tenant_123", "run_1", statusTag),
      { wrapper: wrapper(queryClient), initialProps: { statusTag: "planned" } }
    );
    await waitFor(() => expect(result.current.data).toEqual([{ phase: "plan" }]));

    fetchMock.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          resolveSecondFetch = resolve;
        })
    );
    rerender({ statusTag: "completed" });

    // The new status key's fetch is now in flight; without keepPreviousData this would
    // momentarily be undefined, causing the logs panel to flash empty.
    await waitFor(() => expect(result.current.isFetching).toBe(true));
    expect(result.current.data).toEqual([{ phase: "plan" }]);
    expect(result.current.isPlaceholderData).toBe(true);

    resolveSecondFetch?.(jsonResponse([{ phase: "apply" }]));
    await waitFor(() => expect(result.current.data).toEqual([{ phase: "apply" }]));
    expect(result.current.isPlaceholderData).toBe(false);
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

describe("mutation hooks", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("registers a template and seeds the registration query cache", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(jsonResponse({ id: "reg_1", status: "pending" }));
    const queryClient = testQueryClient();
    const { result } = renderHook(() => useRegisterTemplateMutation("tenant_123"), { wrapper: wrapper(queryClient) });

    await act(async () => {
      await result.current.mutateAsync({ repo_owner: "acme", repo_name: "infra", source_ref: "main", root_path: "." });
    });

    expect(queryClient.getQueryData(queryKeys.templateRegistration("tenant_123", "reg_1"))).toEqual({
      id: "reg_1",
      status: "pending"
    });
  });

  it("creates a stack and invalidates the stacks list", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(jsonResponse({ id: "stack_1" }));
    const queryClient = testQueryClient();
    const invalidateSpy = vi.spyOn(queryClient, "invalidateQueries");
    const { result } = renderHook(() => useCreateStackMutation("tenant_123"), { wrapper: wrapper(queryClient) });

    await act(async () => {
      await result.current.mutateAsync({ name: "Acme Prod", slug: "", tags: {}, default_credential_ids: [] });
    });

    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: queryKeys.stacks("tenant_123") });
  });

  it("installs a template on a stack and invalidates that stack's detail", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(jsonResponse({ id: "stack_template_1" }));
    const queryClient = testQueryClient();
    const invalidateSpy = vi.spyOn(queryClient, "invalidateQueries");
    const { result } = renderHook(() => useAddTemplateToStackMutation("tenant_123", "stack_1"), {
      wrapper: wrapper(queryClient)
    });

    await act(async () => {
      await result.current.mutateAsync({ template_revision_id: "rev_1", selected_ref: "main", config: {} });
    });

    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: queryKeys.stack("tenant_123", "stack_1") });
  });

  it("saves stack template config and invalidates that stack's detail", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(jsonResponse({ id: "stack_template_1" }));
    const queryClient = testQueryClient();
    const invalidateSpy = vi.spyOn(queryClient, "invalidateQueries");
    const { result } = renderHook(() => useUpdateStackTemplateConfigMutation("tenant_123", "stack_1"), {
      wrapper: wrapper(queryClient)
    });

    await act(async () => {
      await result.current.mutateAsync({ stackTemplateID: "stack_template_1", body: { config: { region: "us-west-2" } } });
    });

    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: queryKeys.stack("tenant_123", "stack_1") });
  });

  it("upgrades a stack template and invalidates that stack's detail", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(jsonResponse({ id: "stack_template_1" }));
    const queryClient = testQueryClient();
    const invalidateSpy = vi.spyOn(queryClient, "invalidateQueries");
    const { result } = renderHook(() => useUpgradeStackTemplateMutation("tenant_123", "stack_1"), {
      wrapper: wrapper(queryClient)
    });

    await act(async () => {
      await result.current.mutateAsync({ stackTemplateID: "stack_template_1", body: { target_template_revision_id: "rev_2" } });
    });

    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: queryKeys.stack("tenant_123", "stack_1") });
  });

  it("starts a run and seeds that run's query cache", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(jsonResponse({ id: "run_1", status: "queued" }));
    const queryClient = testQueryClient();
    const { result } = renderHook(() => useStartTemplateRunMutation("tenant_123"), { wrapper: wrapper(queryClient) });

    await act(async () => {
      await result.current.mutateAsync({ stackTemplateID: "stack_template_1", body: { operation: "plan" } });
    });

    expect(queryClient.getQueryData(queryKeys.templateRun("tenant_123", "run_1"))).toEqual({
      id: "run_1",
      status: "queued"
    });
  });

  it("approves a run and invalidates that run's query", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response(null, { status: 204 }));
    const queryClient = testQueryClient();
    const invalidateSpy = vi.spyOn(queryClient, "invalidateQueries");
    const { result } = renderHook(() => useApproveRunMutation("tenant_123"), { wrapper: wrapper(queryClient) });

    await act(async () => {
      await result.current.mutateAsync("run_1");
    });

    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: queryKeys.templateRun("tenant_123", "run_1") });
  });

  it("cancels a run and invalidates that run's query", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response(null, { status: 204 }));
    const queryClient = testQueryClient();
    const invalidateSpy = vi.spyOn(queryClient, "invalidateQueries");
    const { result } = renderHook(() => useCancelRunMutation("tenant_123"), { wrapper: wrapper(queryClient) });

    await act(async () => {
      await result.current.mutateAsync({ runID: "run_1", body: { reason: "manual stop" } });
    });

    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: queryKeys.templateRun("tenant_123", "run_1") });
  });
});
