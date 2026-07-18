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

    const testRouter = createMemoryRouter(routeConfig, { initialEntries: ["/"] });
    const markup = renderToStaticMarkup(<RouterProvider router={testRouter} />);

    expect(markup).toContain("Terraform workflow console");
    expect(markup).toContain('data-testid="tenant-context"');
  });

  it("renders a placeholder for every reserved screen in the route map", async () => {
    vi.stubEnv("VITE_TFLIVE_TENANT_ID", "tenant_123");
    const { routeConfig } = await import("./router");

    const reservedPaths = [
      "/stacks",
      "/stacks/new",
      "/stacks/stack_1",
      "/stacks/stack_1/template",
      "/stacks/stack_1/runs",
      "/stacks/stack_1/runs/run_1",
      "/stacks/stack_1/access",
      "/templates",
      "/auth/callback"
    ];

    for (const path of reservedPaths) {
      const testRouter = createMemoryRouter(routeConfig, { initialEntries: [path] });
      const markup = renderToStaticMarkup(<RouterProvider router={testRouter} />);
      expect(markup, `expected a placeholder at ${path}`).toContain('data-testid="route-placeholder"');
    }
  });

  it("renders the 404 screen for unknown paths", async () => {
    vi.stubEnv("VITE_TFLIVE_TENANT_ID", "tenant_123");
    const { routeConfig } = await import("./router");

    const testRouter = createMemoryRouter(routeConfig, { initialEntries: ["/nonexistent"] });
    const markup = renderToStaticMarkup(<RouterProvider router={testRouter} />);

    expect(markup).toContain('data-testid="route-not-found"');
  });
});
