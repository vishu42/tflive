import { renderToStaticMarkup } from "react-dom/server";
import { createMemoryRouter, RouterProvider } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

describe("AppShell", () => {
  afterEach(() => {
    vi.unstubAllEnvs();
    vi.resetModules();
  });

  it("renders nav, an identity slot, a static tenant indicator, and routed content", async () => {
    vi.stubEnv("VITE_TFLIVE_TENANT_ID", "tenant_123");
    const { default: AppShell } = await import("./AppShell");
    const { default: MockAuthProvider } = await import("../auth/MockAuthProvider");

    const testRouter = createMemoryRouter(
      [
        {
          path: "/",
          element: <AppShell />,
          children: [{ index: true, element: <div data-testid="outlet-content">child</div> }]
        }
      ],
      { initialEntries: ["/"] }
    );

    const markup = renderToStaticMarkup(
      <MockAuthProvider>
        <RouterProvider router={testRouter} />
      </MockAuthProvider>
    );

    expect(markup).toContain('href="/stacks"');
    expect(markup).toContain('href="/templates"');
    expect(markup).toContain('data-testid="identity-menu"');
    expect(markup).toContain('data-testid="shell-tenant-context"');
    expect(markup).toContain(">tenant_123<");
    expect(markup).not.toContain("<input");
    expect(markup).toContain('data-testid="outlet-content"');
  });

  it("displays the mock user's display name and a logout control", async () => {
    vi.stubEnv("VITE_TFLIVE_TENANT_ID", "tenant_123");
    const { default: AppShell } = await import("./AppShell");
    const { default: MockAuthProvider } = await import("../auth/MockAuthProvider");
    const { mockUsers } = await import("../auth/mockUsers");

    const testRouter = createMemoryRouter(
      [{ path: "/", element: <AppShell />, children: [{ index: true, element: <div /> }] }],
      { initialEntries: ["/"] }
    );

    const markup = renderToStaticMarkup(
      <MockAuthProvider>
        <RouterProvider router={testRouter} />
      </MockAuthProvider>
    );

    expect(markup).toContain(mockUsers.operator.displayName);
    expect(markup).toContain('data-testid="logout-button"');
  });
});
