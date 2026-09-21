import { renderToStaticMarkup } from "react-dom/server";
import { createMemoryRouter, Outlet, RouterProvider } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { StackTemplate } from "../../api/types";
import type { StackCapabilities } from "../../auth/types";
import { AuthContext } from "../../auth/AuthContext";
import type { AuthContextValue } from "../../auth/AuthContext";

vi.mock("../../auth/SessionProvider");

function authValue(): AuthContextValue {
  return {
    me: { sub: "user_1", tenantID: "tenant_123", displayName: "Test User", globalCapabilities: { isPlatformAdmin: false, canCreateStack: false, canPublishTemplate: false } },
    status: "authenticated",
    login: () => {},
    logout: () => {},
  };
}

// Renders the app's real routeConfig at a stack-scoped path with the stack
// query cache pre-seeded, mirroring router.test.tsx: retry: false +
// staleTime: Infinity so the seeded data never triggers a real fetch().
async function renderStackRoute(path: string, capabilities: StackCapabilities, templates: StackTemplate[] = []) {
  const { routeConfig } = await import("../../app/router");
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
    templates
  });

  const testRouter = createMemoryRouter(routeConfig, { initialEntries: [path] });
  return renderToStaticMarkup(
    <QueryClientProvider client={queryClient}>
      <AuthContext.Provider value={authValue()}>
        <RouterProvider router={testRouter} />
      </AuthContext.Provider>
    </QueryClientProvider>
  );
}

const allAllowed: StackCapabilities = { canView: true, canOperate: true, canApprove: true, canManageAccess: true };

const vpc: StackTemplate = {
  id: "st_1",
  stack_id: "stack_1",
  component_key: "vpc",
  source_template_id: "tmpl_src_1",
  desired_template_revision_id: "rev_1",
  last_applied_template_revision_id: "",
  source_ref: "main",
  workspace_name: "ws-payments",
  display_name: "Network",
  config: {},
  last_applied_run_id: "",
  last_planned_run_id: "",
  plan_state: "none",
  live_state: "never",
  created_by: "user_123",
  lifecycle: "active"
};

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
    expect(markup).toContain('href="/stacks/stack_1/templates"');
    expect(markup).toContain('href="/stacks/stack_1/access"');
    expect(markup).toContain('href="/stacks/stack_1/environment"');
  });

  // Runs live on each template's own page, so the stack has no Runs tab and
  // no runs routes of its own.
  it("offers no Runs tab", async () => {
    vi.stubEnv("VITE_TFLIVE_TENANT_ID", "tenant_123");
    const markup = await renderStackRoute("/stacks/stack_1", allAllowed);

    expect(markup).not.toContain('href="/stacks/stack_1/runs"');
    expect(markup).not.toContain(">Runs<");
  });

  it("no longer resolves the standalone runs routes", async () => {
    vi.stubEnv("VITE_TFLIVE_TENANT_ID", "tenant_123");
    for (const path of ["/stacks/stack_1/runs", "/stacks/stack_1/runs/run_1", "/stacks/stack_1/template"]) {
      const markup = await renderStackRoute(path, allAllowed);
      expect(markup).toContain('data-testid="route-not-found"');
      expect(markup).not.toContain('data-testid="stack-detail-shell"');
    }
  });

  it("omits the Access tab when canManageAccess is denied", async () => {
    vi.stubEnv("VITE_TFLIVE_TENANT_ID", "tenant_123");
    const markup = await renderStackRoute("/stacks/stack_1", { ...allAllowed, canManageAccess: false });

    expect(markup).toContain('data-testid="stack-detail-shell"');
    expect(markup).toContain('href="/stacks/stack_1/templates"');
    expect(markup).not.toContain('href="/stacks/stack_1/access"');
    expect(markup).not.toContain('href="/stacks/stack_1/environment"');
  });

  it("renders each nested route's content inside the shell", async () => {
    vi.stubEnv("VITE_TFLIVE_TENANT_ID", "tenant_123");
    // The access tab is a real screen now with its own loading state; the
    // grants query is unseeded here, so the shell renders the StackAccessScreen
    // component as the nested content.
    const accessMarkup = await renderStackRoute("/stacks/stack_1/access", allAllowed);
    expect(accessMarkup).toContain('data-testid="stack-detail-shell"');
    expect(accessMarkup).toContain("Current Grants");

    const environmentMarkup = await renderStackRoute("/stacks/stack_1/environment", allAllowed);
    expect(environmentMarkup).toContain('data-testid="stack-detail-shell"');
    expect(environmentMarkup).toContain('data-testid="environment-loading"');

    // The seeded stack view has no installed templates, so the list renders
    // its empty state as the nested content.
    const templateMarkup = await renderStackRoute("/stacks/stack_1/templates", allAllowed);
    expect(templateMarkup).toContain('data-testid="stack-detail-shell"');
    expect(templateMarkup).toContain('data-testid="stack-template-empty"');

    // A template's page nests inside the stack shell, with its own tabs.
    const detailMarkup = await renderStackRoute("/stacks/stack_1/templates/st_1/settings", allAllowed, [vpc]);
    expect(detailMarkup).toContain('data-testid="stack-detail-shell"');
    expect(detailMarkup).toContain('data-testid="stack-template-detail"');
    expect(detailMarkup).toContain('data-testid="template-settings-tab"');

    // Run detail's runs query is unseeded here, so the shell renders its
    // loading state as the nested content.
    const runDetailMarkup = await renderStackRoute("/stacks/stack_1/templates/st_1/runs/1", allAllowed, [vpc]);
    expect(runDetailMarkup).toContain('data-testid="stack-template-detail"');
    expect(runDetailMarkup).toContain('data-testid="run-detail-loading"');
  });

  it("titles the page with a Stacks / stack breadcrumb", async () => {
    vi.stubEnv("VITE_TFLIVE_TENANT_ID", "tenant_123");
    const markup = await renderStackRoute("/stacks/stack_1/templates", allAllowed);

    expect(markup).toMatch(/<nav class="breadcrumb" aria-label="Breadcrumb">.*href="\/stacks">Stacks<.*<h1 aria-current="page">Payments<\/h1>/);
    expect(markup).not.toContain("breadcrumb__detail");
  });

  it("names the template on its own page", async () => {
    vi.stubEnv("VITE_TFLIVE_TENANT_ID", "tenant_123");
    const markup = await renderStackRoute("/stacks/stack_1/templates/st_1/settings", allAllowed, [vpc]);

    expect(markup).toMatch(
      /<nav class="breadcrumb".*href="\/stacks\/stack_1">Payments<.*href="\/stacks\/stack_1\/templates">Templates<.*<h1 aria-current="page">Network<\/h1>/
    );
  });

  it("extends the breadcrumb through the template on a page below it", async () => {
    vi.stubEnv("VITE_TFLIVE_TENANT_ID", "tenant_123");
    const runMarkup = await renderStackRoute("/stacks/stack_1/templates/st_1/runs/4", allAllowed, [vpc]);
    expect(runMarkup).toMatch(
      /<nav class="breadcrumb".*href="\/stacks\/stack_1\/templates">Templates<.*href="\/stacks\/stack_1\/templates\/st_1">Network<.*<h1 aria-current="page">Run #4<\/h1>/
    );

    const upgradeMarkup = await renderStackRoute("/stacks/stack_1/templates/st_1/upgrade", allAllowed, [vpc]);
    expect(upgradeMarkup).toMatch(/href="\/stacks\/stack_1\/templates\/st_1">Network<.*<h1 aria-current="page">Change revision<\/h1>/);

    const addMarkup = await renderStackRoute("/stacks/stack_1/templates/new", allAllowed);
    expect(addMarkup).toMatch(/href="\/stacks\/stack_1\/templates">Templates<.*<h1 aria-current="page">Add template<\/h1>/);
  });

  it("marks the tab matching the current route as current", async () => {
    vi.stubEnv("VITE_TFLIVE_TENANT_ID", "tenant_123");
    const markup = await renderStackRoute("/stacks/stack_1/templates", allAllowed);

    expect(markup).toMatch(/aria-current="page"[^>]*>Templates|href="\/stacks\/stack_1\/templates"[^>]*aria-current="page"/);
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
