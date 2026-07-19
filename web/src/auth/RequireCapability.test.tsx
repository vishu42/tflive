import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { createMemoryRouter, MemoryRouter, RouterProvider } from "react-router-dom";
import { describe, expect, it } from "vitest";
import { AuthContext } from "./AuthContext";
import type { AuthContextValue } from "./AuthContext";
import RequireCapability from "./RequireCapability";
import { queryKeys } from "../api/queryKeys";
import type { StackView } from "../api/types";

function authValue(overrides: Partial<AuthContextValue> = {}): AuthContextValue {
  return {
    me: {
      sub: "user_1",
      displayName: "Test User",
      globalCapabilities: { isPlatformAdmin: false, canCreateStack: false }
    },
    status: "authenticated",
    login: () => {},
    logout: () => {},
    ...overrides
  };
}

function testQueryClient(): QueryClient {
  // staleTime: Infinity keeps seeded cache data from triggering a background
  // refetch on mount (the default staleTime: 0 would refetch stale data even
  // though it's already cached, defeating the "no additional fetch" assertions).
  return new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
}

function stackView(capabilities: Partial<StackView["stack"]["effectiveCapabilities"]>): StackView {
  return {
    stack: {
      id: "stack_1",
      tenant_id: "tenant_123",
      name: "Stack",
      slug: "stack",
      tags: {},
      default_credential_ids: [],
      created_by: "user_123",
      created_at: "2026-07-19T00:00:00Z",
      effectiveCapabilities: { canView: false, canOperate: false, canApprove: false, canManageAccess: false, ...capabilities }
    },
    templates: []
  };
}

function seededClient(stackId: string, capabilities: Partial<StackView["stack"]["effectiveCapabilities"]>): QueryClient {
  const queryClient = testQueryClient();
  queryClient.setQueryData(queryKeys.stack("tenant_123", stackId), stackView(capabilities));
  return queryClient;
}

function renderGate(
  ui: ReactElement,
  { auth = authValue(), queryClient = testQueryClient() }: { auth?: AuthContextValue; queryClient?: QueryClient } = {}
): string {
  return renderToStaticMarkup(
    <QueryClientProvider client={queryClient}>
      <AuthContext.Provider value={auth}>
        <MemoryRouter>{ui}</MemoryRouter>
      </AuthContext.Provider>
    </QueryClientProvider>
  );
}

describe("RequireCapability — gate mode (component-level)", () => {
  it("renders children when the global capability is allowed", () => {
    const markup = renderGate(
      <RequireCapability capability="canCreateStack">
        <span data-testid="protected">shown</span>
      </RequireCapability>,
      { auth: authValue({ me: { ...authValue().me!, globalCapabilities: { isPlatformAdmin: false, canCreateStack: true } } }) }
    );

    expect(markup).toContain('data-testid="protected"');
  });

  it("fails closed (renders nothing) when denied and no fallback is given", () => {
    const markup = renderGate(
      <RequireCapability capability="canCreateStack">
        <span data-testid="protected">shown</span>
      </RequireCapability>
    );

    expect(markup).toBe("");
  });

  it("renders the fallback when denied and a fallback is given", () => {
    const markup = renderGate(
      <RequireCapability capability="canCreateStack" fallback={<span data-testid="reason">Requires operator role</span>}>
        <span data-testid="protected">shown</span>
      </RequireCapability>
    );

    expect(markup).toContain('data-testid="reason"');
    expect(markup).not.toContain('data-testid="protected"');
  });

  it("renders nothing while the global capability is loading", () => {
    const markup = renderGate(
      <RequireCapability capability="canCreateStack" fallback={<span data-testid="reason">shown</span>}>
        <span data-testid="protected">shown</span>
      </RequireCapability>,
      { auth: authValue({ status: "loading", me: null }) }
    );

    expect(markup).toBe("");
  });

  it("reads a stack capability from the query cache using an explicit stackId, with no fetch", () => {
    const markup = renderGate(
      <RequireCapability capability="canOperate" stackId="stack_1">
        <span data-testid="protected">shown</span>
      </RequireCapability>,
      { queryClient: seededClient("stack_1", { canOperate: true }) }
    );

    expect(markup).toContain('data-testid="protected"');
  });

  it("falls back to the current route's stackId param when none is given explicitly", () => {
    const testRouter = createMemoryRouter(
      [
        {
          path: "/stacks/:stackId",
          element: (
            <RequireCapability capability="canOperate">
              <span data-testid="protected">shown</span>
            </RequireCapability>
          )
        }
      ],
      { initialEntries: ["/stacks/stack_1"] }
    );
    const markup = renderToStaticMarkup(
      <QueryClientProvider client={seededClient("stack_1", { canOperate: true })}>
        <AuthContext.Provider value={authValue()}>
          <RouterProvider router={testRouter} />
        </AuthContext.Provider>
      </QueryClientProvider>
    );

    expect(markup).toContain('data-testid="protected"');
  });

  it("renders nothing while a stack capability is loading (no cached data yet)", () => {
    const markup = renderGate(
      <RequireCapability capability="canOperate" stackId="stack_1">
        <span data-testid="protected">shown</span>
      </RequireCapability>
    );

    expect(markup).toBe("");
  });
});

describe("RequireCapability — route mode", () => {
  function renderRoute(
    capability: "canView" | "canManageAccess" | "canCreateStack",
    { auth = authValue(), queryClient = testQueryClient() }: { auth?: AuthContextValue; queryClient?: QueryClient } = {}
  ): string {
    const testRouter = createMemoryRouter(
      [
        {
          path: "/stacks/:stackId",
          element: <RequireCapability capability={capability} mode="route" />,
          children: [{ index: true, element: <span data-testid="protected-route">shown</span> }]
        }
      ],
      { initialEntries: ["/stacks/stack_1"] }
    );
    return renderToStaticMarkup(
      <QueryClientProvider client={queryClient}>
        <AuthContext.Provider value={auth}>
          <RouterProvider router={testRouter} />
        </AuthContext.Provider>
      </QueryClientProvider>
    );
  }

  it("renders the routed content when allowed", () => {
    const markup = renderRoute("canView", { queryClient: seededClient("stack_1", { canView: true }) });

    expect(markup).toContain('data-testid="protected-route"');
  });

  it("renders NotFound, identical to a real 404, when canView is denied", () => {
    const markup = renderRoute("canView", { queryClient: seededClient("stack_1", { canView: false }) });

    expect(markup).toContain('data-testid="route-not-found"');
    expect(markup).not.toContain('data-testid="route-access-denied"');
  });

  it("renders AccessDenied, not NotFound, when a non-canView capability is denied", () => {
    const markup = renderRoute("canManageAccess", { queryClient: seededClient("stack_1", { canView: true, canManageAccess: false }) });

    expect(markup).toContain('data-testid="route-access-denied"');
    expect(markup).not.toContain('data-testid="route-not-found"');
  });

  it("renders nothing while loading (no cached stack data yet)", () => {
    const markup = renderRoute("canView");

    expect(markup).toBe("");
  });
});
