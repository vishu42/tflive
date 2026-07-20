import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderToStaticMarkup } from "react-dom/server";
import { createMemoryRouter, RouterProvider } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

describe("routeConfig", () => {
  afterEach(() => {
    vi.unstubAllEnvs();
    vi.resetModules();
  });

  it("renders the existing workflow console unchanged at the index route", async () => {
    vi.stubEnv("VITE_TFLIVE_TENANT_ID", "tenant_123");
    const { routeConfig } = await import("./router");
    const { default: MockAuthProvider } = await import("../auth/MockAuthProvider");
    const queryClient = new QueryClient();

    const testRouter = createMemoryRouter(routeConfig, { initialEntries: ["/"] });
    const markup = renderToStaticMarkup(
      <QueryClientProvider client={queryClient}>
        <MockAuthProvider>
          <RouterProvider router={testRouter} />
        </MockAuthProvider>
      </QueryClientProvider>
    );

    expect(markup).toContain("Terraform workflow console");
    expect(markup).toContain('data-testid="tenant-context"');
  });

  it("renders a placeholder for every reserved screen a signed-in operator can reach", async () => {
    vi.stubEnv("VITE_TFLIVE_TENANT_ID", "tenant_123");
    const { routeConfig } = await import("./router");
    const { default: MockAuthProvider } = await import("../auth/MockAuthProvider");
    const { QueryClient, QueryClientProvider } = await import("@tanstack/react-query");
    const { queryKeys } = await import("../api/queryKeys");

    // The default mock role ("operator") has canCreateStack: true (see auth/mockUsers.ts),
    // so /stacks/new needs no seeded query data — it resolves from useAuth() alone.
    const ungatedAndGloballyAllowedPaths = ["/stacks/new", "/templates", "/auth/callback"];
    for (const path of ungatedAndGloballyAllowedPaths) {
      const testRouter = createMemoryRouter(routeConfig, { initialEntries: [path] });
      const markup = renderToStaticMarkup(
        <QueryClientProvider client={new QueryClient()}>
          <MockAuthProvider>
            <RouterProvider router={testRouter} />
          </MockAuthProvider>
        </QueryClientProvider>
      );
      expect(markup, `expected a placeholder at ${path}`).toContain('data-testid="route-placeholder"');
    }

    // canView-gated stack routes resolve once the stack query cache has effectiveCapabilities.
    // retry: false + staleTime: Infinity: this test seeds the cache directly and
    // must never let TanStack Query's default refetch-on-mount fire a real,
    // unmocked fetch() for stale data — see the identical rationale in
    // useStackCapabilities.test.tsx (Task 2) and RequireCapability.test.tsx (Task 3).
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
    queryClient.setQueryData(queryKeys.stack("tenant_123", "stack_1"), {
      stack: {
        id: "stack_1",
        tenant_id: "tenant_123",
        name: "Stack",
        slug: "stack",
        tags: {},
        default_credential_ids: [],
        created_by: "user_123",
        created_at: "2026-07-19T00:00:00Z",
        effectiveCapabilities: { canView: true, canOperate: true, canApprove: true, canManageAccess: true }
      },
      templates: []
    });
    const stackScopedPaths = [
      "/stacks/stack_1",
      "/stacks/stack_1/template",
      "/stacks/stack_1/runs",
      "/stacks/stack_1/runs/run_1",
      "/stacks/stack_1/access"
    ];
    for (const path of stackScopedPaths) {
      const testRouter = createMemoryRouter(routeConfig, { initialEntries: [path] });
      const markup = renderToStaticMarkup(
        <QueryClientProvider client={queryClient}>
          <MockAuthProvider>
            <RouterProvider router={testRouter} />
          </MockAuthProvider>
        </QueryClientProvider>
      );
      expect(markup, `expected a placeholder at ${path}`).toContain('data-testid="route-placeholder"');
    }
  });

  it("renders the stacks list screen at /stacks", async () => {
    vi.stubEnv("VITE_TFLIVE_TENANT_ID", "tenant_123");
    const { routeConfig } = await import("./router");
    const { default: MockAuthProvider } = await import("../auth/MockAuthProvider");
    const { QueryClient, QueryClientProvider } = await import("@tanstack/react-query");
    const { queryKeys } = await import("../api/queryKeys");

    // retry: false + staleTime: Infinity: this test seeds the cache directly and
    // must never let TanStack Query's default refetch-on-mount fire a real,
    // unmocked fetch() for stale data — see useStackCapabilities.test.tsx.
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
    queryClient.setQueryData(queryKeys.stacks("tenant_123"), [
      {
        id: "stack_1",
        tenant_id: "tenant_123",
        name: "Payments",
        slug: "payments",
        tags: {},
        default_credential_ids: [],
        created_by: "user_123",
        created_at: "2026-07-19T00:00:00Z",
        effectiveCapabilities: { canView: true, canOperate: false, canApprove: false, canManageAccess: false }
      }
    ]);

    const testRouter = createMemoryRouter(routeConfig, { initialEntries: ["/stacks"] });
    const markup = renderToStaticMarkup(
      <QueryClientProvider client={queryClient}>
        <MockAuthProvider>
          <RouterProvider router={testRouter} />
        </MockAuthProvider>
      </QueryClientProvider>
    );

    expect(markup).toContain('data-testid="stacks-list"');
    expect(markup).toContain("Payments");
    expect(markup).not.toContain('data-testid="route-placeholder"');
  });

  it("renders a 404, not a permission leak, when canView is denied for a stack route", async () => {
    vi.stubEnv("VITE_TFLIVE_TENANT_ID", "tenant_123");
    const { routeConfig } = await import("./router");
    const { default: MockAuthProvider } = await import("../auth/MockAuthProvider");
    const { QueryClient, QueryClientProvider } = await import("@tanstack/react-query");
    const { queryKeys } = await import("../api/queryKeys");

    // retry: false + staleTime: Infinity: this test seeds the cache directly and
    // must never let TanStack Query's default refetch-on-mount fire a real,
    // unmocked fetch() for stale data — see the identical rationale in
    // useStackCapabilities.test.tsx (Task 2) and RequireCapability.test.tsx (Task 3).
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
    queryClient.setQueryData(queryKeys.stack("tenant_123", "stack_1"), {
      stack: {
        id: "stack_1",
        tenant_id: "tenant_123",
        name: "Stack",
        slug: "stack",
        tags: {},
        default_credential_ids: [],
        created_by: "user_123",
        created_at: "2026-07-19T00:00:00Z",
        effectiveCapabilities: { canView: false, canOperate: false, canApprove: false, canManageAccess: false }
      },
      templates: []
    });

    const testRouter = createMemoryRouter(routeConfig, { initialEntries: ["/stacks/stack_1"] });
    const markup = renderToStaticMarkup(
      <QueryClientProvider client={queryClient}>
        <MockAuthProvider>
          <RouterProvider router={testRouter} />
        </MockAuthProvider>
      </QueryClientProvider>
    );

    expect(markup).toContain('data-testid="route-not-found"');
  });

  it("renders AccessDenied for /stacks/:stackId/access when canManageAccess is denied but canView is allowed", async () => {
    vi.stubEnv("VITE_TFLIVE_TENANT_ID", "tenant_123");
    const { routeConfig } = await import("./router");
    const { default: MockAuthProvider } = await import("../auth/MockAuthProvider");
    const { QueryClient, QueryClientProvider } = await import("@tanstack/react-query");
    const { queryKeys } = await import("../api/queryKeys");

    // retry: false + staleTime: Infinity: this test seeds the cache directly and
    // must never let TanStack Query's default refetch-on-mount fire a real,
    // unmocked fetch() for stale data — see the identical rationale in
    // useStackCapabilities.test.tsx (Task 2) and RequireCapability.test.tsx (Task 3).
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
    queryClient.setQueryData(queryKeys.stack("tenant_123", "stack_1"), {
      stack: {
        id: "stack_1",
        tenant_id: "tenant_123",
        name: "Stack",
        slug: "stack",
        tags: {},
        default_credential_ids: [],
        created_by: "user_123",
        created_at: "2026-07-19T00:00:00Z",
        effectiveCapabilities: { canView: true, canOperate: false, canApprove: false, canManageAccess: false }
      },
      templates: []
    });

    const testRouter = createMemoryRouter(routeConfig, { initialEntries: ["/stacks/stack_1/access"] });
    const markup = renderToStaticMarkup(
      <QueryClientProvider client={queryClient}>
        <MockAuthProvider>
          <RouterProvider router={testRouter} />
        </MockAuthProvider>
      </QueryClientProvider>
    );

    expect(markup).toContain('data-testid="route-access-denied"');
  });

  it("renders AccessDenied at /stacks/new for a role without canCreateStack", async () => {
    vi.stubEnv("VITE_TFLIVE_TENANT_ID", "tenant_123");
    vi.stubEnv("VITE_TFLIVE_MOCK_USER_ROLE", "viewer");
    const { routeConfig } = await import("./router");
    const { default: MockAuthProvider } = await import("../auth/MockAuthProvider");
    const { QueryClient, QueryClientProvider } = await import("@tanstack/react-query");

    const testRouter = createMemoryRouter(routeConfig, { initialEntries: ["/stacks/new"] });
    const markup = renderToStaticMarkup(
      <QueryClientProvider client={new QueryClient()}>
        <MockAuthProvider>
          <RouterProvider router={testRouter} />
        </MockAuthProvider>
      </QueryClientProvider>
    );

    expect(markup).toContain('data-testid="route-access-denied"');
  });

  it("renders the 404 screen for unknown paths", async () => {
    vi.stubEnv("VITE_TFLIVE_TENANT_ID", "tenant_123");
    const { routeConfig } = await import("./router");
    const { default: MockAuthProvider } = await import("../auth/MockAuthProvider");

    const testRouter = createMemoryRouter(routeConfig, { initialEntries: ["/nonexistent"] });
    const markup = renderToStaticMarkup(
      <MockAuthProvider>
        <RouterProvider router={testRouter} />
      </MockAuthProvider>
    );

    expect(markup).toContain('data-testid="route-not-found"');
  });
});
