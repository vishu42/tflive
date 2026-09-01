// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import EnvironmentScreen from "./EnvironmentScreen";
import { queryKeys } from "../../api/queryKeys";
import { AuthContext } from "../../auth/AuthContext";
import type { AuthContextValue } from "../../auth/AuthContext";

function authValue(): AuthContextValue {
  return {
    me: { sub: "user_1", tenantID: "tenant_123", displayName: "Test User", globalCapabilities: { isPlatformAdmin: false, canCreateStack: false } },
    status: "authenticated",
    login: () => {},
    logout: () => {}
  };
}

function testQueryClient(): QueryClient {
  return new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
}

function renderScreen(queryClient: QueryClient) {
  return render(
    <QueryClientProvider client={queryClient}>
      <AuthContext.Provider value={authValue()}>
        <MemoryRouter initialEntries={["/stacks/stack_1/environment"]}>
          <Routes>
            <Route path="/stacks/:stackId/environment" element={<EnvironmentScreen />} />
          </Routes>
        </MemoryRouter>
      </AuthContext.Provider>
    </QueryClientProvider>
  );
}

describe("EnvironmentScreen", () => {
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it("renders stack credential metadata without rendering secret values", () => {
    const queryClient = testQueryClient();
    queryClient.setQueryData(queryKeys.stackCredentials("tenant_123", "stack_1"), [
      { id: "credential_1", name: "TF_VAR_TEST", scope: "stack", created_at: "2026-07-19T00:00:00Z" }
    ]);

    renderScreen(queryClient);

    expect(screen.getByTestId("environment-screen")).toBeTruthy();
    expect(screen.getByText("TF_VAR_TEST")).toBeTruthy();
    expect(screen.getByText(/Values are write-only/)).toBeTruthy();
    expect(screen.queryByText("secret-value")).toBeNull();
  });

  it("marks itself unsaved once a credential secret is typed, so SessionProvider's proactive re-auth defers", () => {
    const queryClient = testQueryClient();
    queryClient.setQueryData(queryKeys.stackCredentials("tenant_123", "stack_1"), []);

    renderScreen(queryClient);

    expect(document.querySelector("[data-unsaved='true']")).toBeNull();
    fireEvent.change(screen.getByLabelText(/Environment credential value/), { target: { value: "secret-value" } });
    expect(document.querySelector("[data-unsaved='true']")).not.toBeNull();
  });

  it("creates a stack credential through the existing API mutation", async () => {
    const queryClient = testQueryClient();
    queryClient.setQueryData(queryKeys.stackCredentials("tenant_123", "stack_1"), []);
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({ id: "credential_2", name: "TF_VAR_TEST", scope: "stack", created_at: "2026-07-19T00:00:00Z" }), {
        status: 201,
        headers: { "content-type": "application/json" }
      })
    );

    renderScreen(queryClient);
    fireEvent.change(screen.getByLabelText(/Environment credential name/), { target: { value: "TF_VAR_TEST" } });
    fireEvent.change(screen.getByLabelText(/Environment credential value/), { target: { value: "secret-value" } });
    fireEvent.click(screen.getByRole("button", { name: /Add/ }));

    await waitFor(() => expect(globalThis.fetch).toHaveBeenCalledWith(
      expect.stringContaining("/stacks/stack_1/credentials"),
      expect.objectContaining({ method: "POST", body: JSON.stringify({ name: "TF_VAR_TEST", value: "secret-value" }) })
    ));
  });

  it("deletes a stack credential through the existing API mutation", async () => {
    const queryClient = testQueryClient();
    queryClient.setQueryData(queryKeys.stackCredentials("tenant_123", "stack_1"), [
      { id: "credential_1", name: "TF_VAR_TEST", scope: "stack", created_at: "2026-07-19T00:00:00Z" }
    ]);
    vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response(null, { status: 204 }));

    renderScreen(queryClient);
    fireEvent.click(screen.getByRole("button", { name: "Delete TF_VAR_TEST" }));

    await waitFor(() => expect(globalThis.fetch).toHaveBeenCalledWith(
      "/v1/tenants/tenant_123/stacks/stack_1/credentials/credential_1",
      expect.objectContaining({ method: "DELETE" })
    ));
  });
});
