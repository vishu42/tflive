import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderToStaticMarkup } from "react-dom/server";
import { createMemoryRouter, Outlet, RouterProvider } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AuthContext } from "../auth/AuthContext";
import type { AuthContextValue } from "../auth/AuthContext";

vi.mock("../auth/SessionProvider");

function authValue(overrides: Partial<AuthContextValue> = {}): AuthContextValue {
  return {
    me: { sub: "user_1", tenantID: "tenant_123", displayName: "Test User", globalCapabilities: { isPlatformAdmin: false, canCreateStack: false } },
    status: "authenticated",
    login: () => {},
    logout: () => {},
    ...overrides,
  };
}

describe("routeConfig", () => {
  afterEach(() => {
    vi.unstubAllEnvs();
    vi.resetModules();
  });

  // "/" is not a screen. The design spec's route map has no row for it, but
  // a bare sign-in lands there, so it must redirect rather than render. The
  // redirect is a loader, so it resolves during router initialization —
  // before any element renders — which is what this asserts.
  it("redirects the index route to /stacks without rendering a screen", async () => {
    vi.stubEnv("VITE_TFLIVE_TENANT_ID", "tenant_123");
    const { routeConfig } = await import("./router");

    const testRouter = createMemoryRouter(routeConfig, { initialEntries: ["/"] });
    await vi.waitFor(() => expect(testRouter.state.initialized).toBe(true));

    expect(testRouter.state.location.pathname).toBe("/stacks");
  });

  it("renders a placeholder for every reserved screen a signed-in operator can reach", async () => {
    vi.stubEnv("VITE_TFLIVE_TENANT_ID", "tenant_123");
    const { routeConfig } = await import("./router");

    const { QueryClient, QueryClientProvider } = await import("@tanstack/react-query");
    const { queryKeys } = await import("../api/queryKeys");

    // /stacks/new now renders the real CreateStackScreen (Task 2 of
    // create-stack-ui). No reserved screens remain that are reachable purely
    // via global capabilities — the rest require a stack scoped query.
    {
      const testRouter = createMemoryRouter(routeConfig, { initialEntries: ["/stacks/new"] });
      const markup = renderToStaticMarkup(
        <QueryClientProvider client={new QueryClient()}>
          <AuthContext.Provider value={authValue({ me: { ...authValue().me!, globalCapabilities: { isPlatformAdmin: false, canCreateStack: true } } })}>
            <RouterProvider router={testRouter} />
          </AuthContext.Provider>
        </QueryClientProvider>
      );
      expect(markup).toContain("Create stack");
      expect(markup).not.toContain('data-testid="route-placeholder"');
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
    const stackScopedPaths = ["/stacks/stack_1"];
    for (const path of stackScopedPaths) {
      const testRouter = createMemoryRouter(routeConfig, { initialEntries: [path] });
      const markup = renderToStaticMarkup(
        <QueryClientProvider client={queryClient}>
          <AuthContext.Provider value={authValue({ me: { ...authValue().me!, globalCapabilities: { isPlatformAdmin: false, canCreateStack: true } } })}>
            <RouterProvider router={testRouter} />
          </AuthContext.Provider>
        </QueryClientProvider>
      );
      expect(markup, `expected a placeholder at ${path}`).toContain('data-testid="route-placeholder"');
    }

    // Run detail is a real screen (not a reserved placeholder) — no run has
    // been started, so it renders its own loading state. There is no runs list
    // route to check: runs render inline on the template screen (issue #130).
    const runDetailRouter = createMemoryRouter(routeConfig, { initialEntries: ["/stacks/stack_1/runs/run_1"] });
    const runDetailMarkup = renderToStaticMarkup(
      <QueryClientProvider client={queryClient}>
        <AuthContext.Provider value={authValue({ me: { ...authValue().me!, globalCapabilities: { isPlatformAdmin: false, canCreateStack: true } } })}>
          <RouterProvider router={runDetailRouter} />
        </AuthContext.Provider>
      </QueryClientProvider>
    );
    expect(runDetailMarkup).toContain('data-testid="run-detail-loading"');
  });

  it("renders the stack template screen at /stacks/:stackId/template", async () => {
    vi.stubEnv("VITE_TFLIVE_TENANT_ID", "tenant_123");
    const { routeConfig } = await import("./router");
    const { QueryClient, QueryClientProvider } = await import("@tanstack/react-query");
    const { queryKeys } = await import("../api/queryKeys");

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
    queryClient.setQueryData(queryKeys.templateRevisions("tenant_123"), []);

    const testRouter = createMemoryRouter(routeConfig, { initialEntries: ["/stacks/stack_1/template"] });
    const markup = renderToStaticMarkup(
      <QueryClientProvider client={queryClient}>
        <AuthContext.Provider value={authValue()}>
          <RouterProvider router={testRouter} />
        </AuthContext.Provider>
      </QueryClientProvider>
    );

    expect(markup).toContain('data-testid="stack-template-screen"');
    expect(markup).toContain('data-testid="stack-template-empty"');
    expect(markup).not.toContain('data-testid="route-placeholder"');
  });

  it("renders the stacks list screen at /stacks", async () => {
    vi.stubEnv("VITE_TFLIVE_TENANT_ID", "tenant_123");
    const { routeConfig } = await import("./router");

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
        <AuthContext.Provider value={authValue()}>
          <RouterProvider router={testRouter} />
        </AuthContext.Provider>
      </QueryClientProvider>
    );

    expect(markup).toContain('data-testid="stacks-list"');
    expect(markup).toContain("Payments");
    expect(markup).not.toContain('data-testid="route-placeholder"');
  });

  it("renders the template registry screen at /templates", async () => {
    vi.stubEnv("VITE_TFLIVE_TENANT_ID", "tenant_123");
    const { routeConfig } = await import("./router");

    const { QueryClient, QueryClientProvider } = await import("@tanstack/react-query");
    const { queryKeys } = await import("../api/queryKeys");

    // retry: false + staleTime: Infinity: this test seeds the cache directly and
    // must never let TanStack Query's default refetch-on-mount fire a real,
    // unmocked fetch() for stale data — see useStackCapabilities.test.tsx.
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
    queryClient.setQueryData(queryKeys.templateRevisions("tenant_123"), [
      {
        id: "rev_1",
        tenant_id: "tenant_123",
        source_template_id: "tpl_1",
        repo_owner: "hashicorp",
        repo_name: "terraform-aws-vpc",
        source_ref: "main",
        resolved_commit_sha: "abcdef1234567890",
        root_path: ".",
        name: "VPC",
        description: "",
        tags: [],
        status: "active"
      }
    ]);

    const testRouter = createMemoryRouter(routeConfig, { initialEntries: ["/templates"] });
    const markup = renderToStaticMarkup(
      <QueryClientProvider client={queryClient}>
        <AuthContext.Provider value={authValue()}>
          <RouterProvider router={testRouter} />
        </AuthContext.Provider>
      </QueryClientProvider>
    );

    expect(markup).toContain('data-testid="templates-list"');
    expect(markup).toContain("VPC");
    expect(markup).not.toContain('data-testid="route-placeholder"');
  });

  it("renders the template registration screen at /templates/new", async () => {
    vi.stubEnv("VITE_TFLIVE_TENANT_ID", "tenant_123");
    const { routeConfig } = await import("./router");

    const { QueryClient, QueryClientProvider } = await import("@tanstack/react-query");
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });

    const testRouter = createMemoryRouter(routeConfig, { initialEntries: ["/templates/new"] });
    const markup = renderToStaticMarkup(
      <QueryClientProvider client={queryClient}>
        <AuthContext.Provider value={authValue()}>
          <RouterProvider router={testRouter} />
        </AuthContext.Provider>
      </QueryClientProvider>
    );

    expect(markup).toContain("Register template");
    expect(markup).toContain("Root path");
    expect(markup).not.toContain('data-testid="route-not-found"');
  });

  it("renders a 404, not a permission leak, when canView is denied for a stack route", async () => {
    vi.stubEnv("VITE_TFLIVE_TENANT_ID", "tenant_123");
    const { routeConfig } = await import("./router");

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
        <AuthContext.Provider value={authValue()}>
          <RouterProvider router={testRouter} />
        </AuthContext.Provider>
      </QueryClientProvider>
    );

    expect(markup).toContain('data-testid="route-not-found"');
  });

  it("renders AccessDenied for /stacks/:stackId/access when canManageAccess is denied but canView is allowed", async () => {
    vi.stubEnv("VITE_TFLIVE_TENANT_ID", "tenant_123");
    const { routeConfig } = await import("./router");

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
        <AuthContext.Provider value={authValue()}>
          <RouterProvider router={testRouter} />
        </AuthContext.Provider>
      </QueryClientProvider>
    );

    expect(markup).toContain('data-testid="route-access-denied"');
  });

  it("renders the CreateStackScreen when canCreateStack is allowed", async () => {
    vi.stubEnv("VITE_TFLIVE_TENANT_ID", "tenant_123");
    const { routeConfig } = await import("./router");

    const { QueryClient, QueryClientProvider } = await import("@tanstack/react-query");

    const testRouter = createMemoryRouter(routeConfig, { initialEntries: ["/stacks/new"] });
    const markup = renderToStaticMarkup(
      <QueryClientProvider client={new QueryClient()}>
        <AuthContext.Provider value={authValue()}>
          <RouterProvider router={testRouter} />
        </AuthContext.Provider>
      </QueryClientProvider>
    );

    expect(markup).toContain("Create stack");
    expect(markup).not.toContain('data-testid="route-placeholder"');
  });

  // The sign-in screen is a sibling of "/", not a child, because everything
  // under "/" renders inside SessionProvider -- which resolves only with a
  // session, and this is the screen you are shown because you have none.
  // Nested, it could never mount. SessionProvider is mocked here, so the proof
  // is that the screen renders with no AuthContext around it at all.
  it("renders the sign-in screen at /signin outside the session boundary", async () => {
    vi.stubEnv("VITE_TFLIVE_TENANT_ID", "tenant_123");
    const { routeConfig } = await import("./router");

    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const testRouter = createMemoryRouter(routeConfig, { initialEntries: ["/signin"] });
    const markup = renderToStaticMarkup(
      <QueryClientProvider client={queryClient}>
        <RouterProvider router={testRouter} />
      </QueryClientProvider>
    );

    expect(markup).toContain('class="signin-page"');
    expect(markup).toContain("Sign in");
  });

  it("renders the 404 screen for unknown paths", async () => {
    vi.stubEnv("VITE_TFLIVE_TENANT_ID", "tenant_123");
    const { routeConfig } = await import("./router");


    const testRouter = createMemoryRouter(routeConfig, { initialEntries: ["/nonexistent"] });
    const markup = renderToStaticMarkup(
      <AuthContext.Provider value={authValue()}>
        <RouterProvider router={testRouter} />
      </AuthContext.Provider>
    );

    expect(markup).toContain('data-testid="route-not-found"');
  });

  it("renders the add template screen at /stacks/:stackId/template/new", async () => {
    vi.stubEnv("VITE_TFLIVE_TENANT_ID", "tenant_123");
    const { routeConfig } = await import("./router");
    const { createMemoryRouter, RouterProvider } = await import("react-router-dom");
    const { QueryClient, QueryClientProvider } = await import("@tanstack/react-query");
    const { queryKeys } = await import("../api/queryKeys");

    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
    queryClient.setQueryData(queryKeys.stack("tenant_123", "stack_1"), {
      stack: {
        id: "stack_1",
        tenant_id: "tenant_123",
        name: "Payments",
        slug: "payments",
        tags: {},
        default_credential_ids: [],
        created_by: "user_123",
        created_at: "2026-07-19T00:00:00Z",
        effectiveCapabilities: { canView: true, canOperate: true, canApprove: true, canManageAccess: true }
      },
      templates: []
    });
    queryClient.setQueryData(queryKeys.templateRevisions("tenant_123"), []);

    const testRouter = createMemoryRouter(routeConfig, { initialEntries: ["/stacks/stack_1/template/new"] });
    const markup = renderToStaticMarkup(
      <QueryClientProvider client={queryClient}>
        <AuthContext.Provider value={authValue()}>
          <RouterProvider router={testRouter} />
        </AuthContext.Provider>
      </QueryClientProvider>
    );

    expect(markup).toContain('data-testid="add-stack-template-none"');
  });

  it("renders the upgrade screen at /stacks/:stackId/template/:stackTemplateId/upgrade", async () => {
    vi.stubEnv("VITE_TFLIVE_TENANT_ID", "tenant_123");
    const { routeConfig } = await import("./router");
    const { createMemoryRouter, RouterProvider } = await import("react-router-dom");
    const { QueryClient, QueryClientProvider } = await import("@tanstack/react-query");
    const { queryKeys } = await import("../api/queryKeys");

    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
    queryClient.setQueryData(queryKeys.stack("tenant_123", "stack_1"), {
      stack: {
        id: "stack_1",
        tenant_id: "tenant_123",
        name: "Payments",
        slug: "payments",
        tags: {},
        default_credential_ids: [],
        created_by: "user_123",
        created_at: "2026-07-19T00:00:00Z",
        effectiveCapabilities: { canView: true, canOperate: true, canApprove: true, canManageAccess: true }
      },
      templates: []
    });
    queryClient.setQueryData(queryKeys.templateRevisions("tenant_123"), []);

    const testRouter = createMemoryRouter(routeConfig, {
      initialEntries: ["/stacks/stack_1/template/st_1/upgrade"]
    });
    const markup = renderToStaticMarkup(
      <QueryClientProvider client={queryClient}>
        <AuthContext.Provider value={authValue()}>
          <RouterProvider router={testRouter} />
        </AuthContext.Provider>
      </QueryClientProvider>
    );

    // The seeded stack has no installed templates, so the route resolves to
    // the screen's own missing-template state rather than a 404.
    expect(markup).toContain('data-testid="upgrade-template-missing"');
  });

  it("renders AccessDenied for /stacks/:stackId/template/new when canOperate is denied but canView is allowed", async () => {
    vi.stubEnv("VITE_TFLIVE_TENANT_ID", "tenant_123");
    const { routeConfig } = await import("./router");
    const { createMemoryRouter, RouterProvider } = await import("react-router-dom");
    const { QueryClient, QueryClientProvider } = await import("@tanstack/react-query");
    const { queryKeys } = await import("../api/queryKeys");

    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
    queryClient.setQueryData(queryKeys.stack("tenant_123", "stack_1"), {
      stack: {
        id: "stack_1",
        tenant_id: "tenant_123",
        name: "Payments",
        slug: "payments",
        tags: {},
        default_credential_ids: [],
        created_by: "user_123",
        created_at: "2026-07-19T00:00:00Z",
        effectiveCapabilities: { canView: true, canOperate: false, canApprove: false, canManageAccess: false }
      },
      templates: []
    });

    const testRouter = createMemoryRouter(routeConfig, { initialEntries: ["/stacks/stack_1/template/new"] });
    const markup = renderToStaticMarkup(
      <QueryClientProvider client={queryClient}>
        <AuthContext.Provider value={authValue()}>
          <RouterProvider router={testRouter} />
        </AuthContext.Provider>
      </QueryClientProvider>
    );

    expect(markup).toContain('data-testid="route-access-denied"');
  });

  it("renders AccessDenied for /stacks/:stackId/template/:stackTemplateId/upgrade when canOperate is denied but canView is allowed", async () => {
    vi.stubEnv("VITE_TFLIVE_TENANT_ID", "tenant_123");
    const { routeConfig } = await import("./router");
    const { createMemoryRouter, RouterProvider } = await import("react-router-dom");
    const { QueryClient, QueryClientProvider } = await import("@tanstack/react-query");
    const { queryKeys } = await import("../api/queryKeys");

    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
    queryClient.setQueryData(queryKeys.stack("tenant_123", "stack_1"), {
      stack: {
        id: "stack_1",
        tenant_id: "tenant_123",
        name: "Payments",
        slug: "payments",
        tags: {},
        default_credential_ids: [],
        created_by: "user_123",
        created_at: "2026-07-19T00:00:00Z",
        effectiveCapabilities: { canView: true, canOperate: false, canApprove: false, canManageAccess: false }
      },
      templates: []
    });

    const testRouter = createMemoryRouter(routeConfig, {
      initialEntries: ["/stacks/stack_1/template/st_1/upgrade"]
    });
    const markup = renderToStaticMarkup(
      <QueryClientProvider client={queryClient}>
        <AuthContext.Provider value={authValue()}>
          <RouterProvider router={testRouter} />
        </AuthContext.Provider>
      </QueryClientProvider>
    );

    expect(markup).toContain('data-testid="route-access-denied"');
  });
});
