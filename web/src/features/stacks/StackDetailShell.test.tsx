import { renderToStaticMarkup } from "react-dom/server";
import { createMemoryRouter, RouterProvider } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { StackCapabilities } from "../../auth/types";

// Renders the app's real routeConfig at a stack-scoped path with the stack
// query cache pre-seeded, mirroring router.test.tsx: retry: false +
// staleTime: Infinity so the seeded data never triggers a real fetch().
async function renderStackRoute(path: string, capabilities: StackCapabilities) {
  const { routeConfig } = await import("../../app/router");
  const { default: MockAuthProvider } = await import("../../auth/MockAuthProvider");
  const { QueryClient, QueryClientProvider } = await import("@tanstack/react-query");
  const { queryKeys } = await import("../../api/queryKeys");

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
      effectiveCapabilities: capabilities
    },
    templates: []
  });

  const testRouter = createMemoryRouter(routeConfig, { initialEntries: [path] });
  return renderToStaticMarkup(
    <QueryClientProvider client={queryClient}>
      <MockAuthProvider>
        <RouterProvider router={testRouter} />
      </MockAuthProvider>
    </QueryClientProvider>
  );
}

const allAllowed: StackCapabilities = { canView: true, canOperate: true, canApprove: true, canManageAccess: true };

describe("StackDetailShell", () => {
  afterEach(() => {
    vi.unstubAllEnvs();
    vi.resetModules();
  });

  it("renders the stack name and all four tabs when every capability is granted", async () => {
    vi.stubEnv("VITE_TFLIVE_TENANT_ID", "tenant_123");
    const markup = await renderStackRoute("/stacks/stack_1", allAllowed);

    expect(markup).toContain('data-testid="stack-detail-shell"');
    expect(markup).toContain("Payments");
    expect(markup).toContain('href="/stacks/stack_1"');
    expect(markup).toContain('href="/stacks/stack_1/template"');
    expect(markup).toContain('href="/stacks/stack_1/runs"');
    expect(markup).toContain('href="/stacks/stack_1/access"');
  });

  it("omits the Access tab when canManageAccess is denied", async () => {
    vi.stubEnv("VITE_TFLIVE_TENANT_ID", "tenant_123");
    const markup = await renderStackRoute("/stacks/stack_1", { ...allAllowed, canManageAccess: false });

    expect(markup).toContain('data-testid="stack-detail-shell"');
    expect(markup).toContain('href="/stacks/stack_1/runs"');
    expect(markup).not.toContain('href="/stacks/stack_1/access"');
  });

  it("renders each nested route's content inside the shell", async () => {
    vi.stubEnv("VITE_TFLIVE_TENANT_ID", "tenant_123");
    for (const path of ["/stacks/stack_1/runs", "/stacks/stack_1/runs/run_1", "/stacks/stack_1/access"]) {
      const markup = await renderStackRoute(path, allAllowed);
      expect(markup, `expected shell chrome at ${path}`).toContain('data-testid="stack-detail-shell"');
      expect(markup, `expected nested content at ${path}`).toContain('data-testid="route-placeholder"');
    }
    // The template tab is a real screen now; its revisions query is unseeded
    // here, so the shell renders its loading state as the nested content.
    const markup = await renderStackRoute("/stacks/stack_1/template", allAllowed);
    expect(markup).toContain('data-testid="stack-detail-shell"');
    expect(markup).toContain('data-testid="stack-template-loading"');
  });

  it("marks the tab matching the current route as current", async () => {
    vi.stubEnv("VITE_TFLIVE_TENANT_ID", "tenant_123");
    const markup = await renderStackRoute("/stacks/stack_1/runs", allAllowed);

    expect(markup).toMatch(/aria-current="page"[^>]*>Runs|href="\/stacks\/stack_1\/runs"[^>]*aria-current="page"/);
  });

  it("still renders NotFound (no shell chrome) when canView is denied", async () => {
    vi.stubEnv("VITE_TFLIVE_TENANT_ID", "tenant_123");
    const markup = await renderStackRoute("/stacks/stack_1", {
      canView: false,
      canOperate: false,
      canApprove: false,
      canManageAccess: false
    });

    expect(markup).toContain('data-testid="route-not-found"');
    expect(markup).not.toContain('data-testid="stack-detail-shell"');
  });
});
