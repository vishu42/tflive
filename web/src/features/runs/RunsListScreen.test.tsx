// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AuthContext } from "../../auth/AuthContext";
import type { AuthContextValue } from "../../auth/AuthContext";
import { queryKeys } from "../../api/queryKeys";
import type { Stack, StackTemplate } from "../../api/types";
import RunsListScreen from "./RunsListScreen";

function stack(overrides: Partial<Stack> = {}): Stack {
  return {
    id: "stack_1",
    tenant_id: "tenant_123",
    name: "Payments",
    slug: "payments",
    tags: {},
    default_credential_ids: [],
    created_by: "user_123",
    created_at: "2026-07-19T00:00:00Z",
    effectiveCapabilities: { canView: true, canOperate: true, canApprove: true, canManageAccess: true },
    ...overrides
  };
}

function stackTemplate(overrides: Partial<StackTemplate> = {}): StackTemplate {
  return {
    id: "stpl_1",
    stack_id: "stack_1",
    component_key: "primary",
    source_template_id: "tpl_1",
    desired_template_revision_id: "rev_1",
    last_applied_template_revision_id: "",
    selected_ref: "main",
    workspace_name: "acme-prod-primary",
    config: {},
    last_applied_run_id: "",
    last_applied_ref: "",
    created_by: "user_123",
    lifecycle: "active",
    ...overrides
  };
}

function testQueryClient(): QueryClient {
  return new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
}

function authValue(): AuthContextValue {
  return {
    me: { sub: "user_1", displayName: "Test User", globalCapabilities: { isPlatformAdmin: false, canCreateStack: false } },
    status: "authenticated",
    login: () => {},
    logout: () => {}
  };
}

function renderScreen(queryClient: QueryClient) {
  return render(
    <QueryClientProvider client={queryClient}>
      <AuthContext.Provider value={authValue()}>
        <MemoryRouter initialEntries={["/stacks/stack_1/runs"]}>
          <Routes>
            <Route path="/stacks/:stackId/runs" element={<RunsListScreen />} />
          </Routes>
        </MemoryRouter>
      </AuthContext.Provider>
    </QueryClientProvider>
  );
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

describe("RunsListScreen", () => {
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it("shows a loading state while the stack query is pending", () => {
    vi.spyOn(globalThis, "fetch").mockReturnValue(new Promise(() => {}));

    renderScreen(testQueryClient());

    expect(screen.getByTestId("runs-list-loading")).toBeTruthy();
  });

  it("renders the shared boundary screen for a handled API error status", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(jsonResponse({ error: "unavailable", message: "service unavailable" }, 503));

    renderScreen(testQueryClient());

    await waitFor(() => expect(screen.getByTestId("route-service-unavailable")).toBeTruthy());
  });

  it("renders a retryable generic error state for unhandled failures", async () => {
    const fetchMock = vi
      .spyOn(globalThis, "fetch")
      .mockRejectedValueOnce(new TypeError("network down"))
      .mockResolvedValueOnce(jsonResponse({ stack: stack(), templates: [] }));

    renderScreen(testQueryClient());

    await waitFor(() => expect(screen.getByTestId("runs-list-error")).toBeTruthy());
    fireEvent.click(screen.getByTestId("runs-list-retry"));
    await waitFor(() => expect(screen.getByTestId("runs-list-empty")).toBeTruthy());
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("renders an empty state when the stack has no installed templates", () => {
    const queryClient = testQueryClient();
    queryClient.setQueryData(queryKeys.stack("tenant_123", "stack_1"), { stack: stack(), templates: [] });

    renderScreen(queryClient);

    expect(screen.getByTestId("runs-list-empty")).toBeTruthy();
  });

  it("renders one row per installed stack template", () => {
    const queryClient = testQueryClient();
    queryClient.setQueryData(queryKeys.stack("tenant_123", "stack_1"), {
      stack: stack(),
      templates: [stackTemplate(), stackTemplate({ id: "stpl_2", workspace_name: "acme-prod-secondary" })]
    });

    renderScreen(queryClient);

    expect(screen.getByTestId("runs-list")).toBeTruthy();
    expect(screen.getByTestId("runs-row-stpl_1")).toBeTruthy();
    expect(screen.getByTestId("runs-row-stpl_2")).toBeTruthy();
  });
});
