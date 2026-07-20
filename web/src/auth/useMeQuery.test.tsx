/// @vitest-environment jsdom

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { useMeQuery } from "./useMeQuery";

function wrapper({ children }: { children: React.ReactNode }) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return (
    <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  );
}

describe("useMeQuery", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("returns MeResponse data on success", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(
        JSON.stringify({
          sub: "user_1",
          displayName: "Alice",
          email: "alice@example.com",
          globalCapabilities: { isPlatformAdmin: true, canCreateStack: false },
          tenantID: "tenant_123",
        }),
        { status: 200, headers: { "content-type": "application/json" } }
      )
    );

    const { result } = renderHook(() => useMeQuery(), { wrapper });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));

    expect(result.current.data).toEqual({
      sub: "user_1",
      displayName: "Alice",
      email: "alice@example.com",
      globalCapabilities: { isPlatformAdmin: true, canCreateStack: false },
      tenantID: "tenant_123",
    });
  });

  it("returns error on 401 response", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(
        JSON.stringify({ error: "unauthorized", message: "authentication required" }),
        { status: 401, headers: { "content-type": "application/json" } }
      )
    );

    const { result } = renderHook(() => useMeQuery(), { wrapper });

    await waitFor(() => expect(result.current.isError).toBe(true));
    expect(result.current.error).toBeTruthy();
  });

  it("does not fetch when disabled", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({}), { status: 200, headers: { "content-type": "application/json" } })
    );

    const { result } = renderHook(() => useMeQuery({ enabled: false }), { wrapper });

    expect(result.current.isLoading).toBe(false);
    expect(result.current.isPending).toBe(true);
    expect(fetchMock).not.toHaveBeenCalled();
  });
});
