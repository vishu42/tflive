// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AuthContext } from "../../auth/AuthContext";
import type { AuthContextValue } from "../../auth/AuthContext";
import StacksListScreen from "./StacksListScreen";
import { queryKeys } from "../../api/queryKeys";
import type { Stack } from "../../api/types";

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
    effectiveCapabilities: { canView: true, canOperate: false, canApprove: false, canManageAccess: false },
    ...overrides
  };
}

// staleTime: Infinity keeps seeded cache data from triggering a background
// refetch on mount — see the identical rationale in useStackCapabilities.test.tsx.
function testQueryClient(): QueryClient {
  return new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
}

function authValue(overrides: Partial<AuthContextValue> = {}): AuthContextValue {
  return {
    me: { sub: "user_1", displayName: "Test User", globalCapabilities: { isPlatformAdmin: false, canCreateStack: true } },
    status: "authenticated",
    login: () => {},
    logout: () => {},
    ...overrides,
  };
}

function renderScreen(queryClient: QueryClient, auth?: AuthContextValue) {
  return render(
    <QueryClientProvider client={queryClient}>
      <AuthContext.Provider value={auth ?? authValue()}>
        <MemoryRouter initialEntries={["/stacks"]}>
          <StacksListScreen />
        </MemoryRouter>
      </AuthContext.Provider>
    </QueryClientProvider>
  );
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

describe("StacksListScreen", () => {
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
    vi.unstubAllEnvs();
  });

  it("shows a loading state while the stacks query is pending", () => {
    vi.spyOn(globalThis, "fetch").mockReturnValue(new Promise(() => {}));

    renderScreen(testQueryClient());

    expect(screen.getByTestId("stacks-list-loading")).toBeTruthy();
  });

  it("renders the stacks returned by the API as links to their detail routes", () => {
    const queryClient = testQueryClient();
    queryClient.setQueryData(queryKeys.stacks("tenant_123"), [
      stack(),
      stack({ id: "stack_2", name: "Billing", slug: "billing" })
    ]);

    renderScreen(queryClient);

    const list = screen.getByTestId("stacks-list");
    expect(list.textContent).toContain("Payments");
    expect(list.textContent).toContain("Billing");
    const hrefs = screen.getAllByRole("link").map((link) => link.getAttribute("href"));
    expect(hrefs).toContain("/stacks/stack_1");
    expect(hrefs).toContain("/stacks/stack_2");
  });

  it("renders an empty state when the API returns no visible stacks", () => {
    const queryClient = testQueryClient();
    queryClient.setQueryData(queryKeys.stacks("tenant_123"), []);

    renderScreen(queryClient);

    expect(screen.getByTestId("stacks-list-empty")).toBeTruthy();
  });

  it("renders the shared boundary screen for a handled API error status", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      jsonResponse({ error: "unavailable", message: "service unavailable" }, 503)
    );

    renderScreen(testQueryClient());

    await waitFor(() => expect(screen.getByTestId("route-service-unavailable")).toBeTruthy());
  });

  it("renders a retryable generic error state for unhandled failures", async () => {
    const fetchMock = vi
      .spyOn(globalThis, "fetch")
      .mockRejectedValueOnce(new TypeError("network down"))
      .mockResolvedValueOnce(jsonResponse([stack()]));

    renderScreen(testQueryClient());

    await waitFor(() => expect(screen.getByTestId("stacks-list-error")).toBeTruthy());
    fireEvent.click(screen.getByTestId("stacks-list-retry"));
    await waitFor(() => expect(screen.getByTestId("stacks-list")).toBeTruthy());
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("shows the create-stack action for a role with canCreateStack", () => {
    const queryClient = testQueryClient();
    queryClient.setQueryData(queryKeys.stacks("tenant_123"), [stack()]);

    renderScreen(queryClient);

    expect(screen.getByTestId("create-stack-link").getAttribute("href")).toBe("/stacks/new");
  });

  it("hides the create-stack action for a role without canCreateStack", () => {
    const queryClient = testQueryClient();
    queryClient.setQueryData(queryKeys.stacks("tenant_123"), [stack()]);

    renderScreen(queryClient, authValue({ me: { ...authValue().me!, globalCapabilities: { isPlatformAdmin: false, canCreateStack: false } } }));

    expect(screen.getByTestId("stacks-list")).toBeTruthy();
    expect(screen.queryByTestId("create-stack-link")).toBeNull();
  });
});
