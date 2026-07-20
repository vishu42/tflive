// @vitest-environment jsdom
import { renderToStaticMarkup } from "react-dom/server";
import { createMemoryRouter, RouterProvider } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import { AuthContext } from "../auth/AuthContext";
import type { AuthContextValue } from "../auth/AuthContext";

function authValue(): AuthContextValue {
  return {
    me: {
      sub: "user_1",
      displayName: "Otto Operator",
      globalCapabilities: { isPlatformAdmin: false, canCreateStack: true },
    },
    status: "authenticated",
    login: () => {},
    logout: () => {},
  };
}

function TestAuthWrapper({ children }: { children: ReactNode }) {
  return <AuthContext.Provider value={authValue()}>{children}</AuthContext.Provider>;
}

describe("AppShell", () => {
  afterEach(() => {
    vi.unstubAllEnvs();
  });

  it("renders nav, an identity slot, a static tenant indicator, and routed content", async () => {
    vi.stubEnv("VITE_TFLIVE_TENANT_ID", "tenant_123");
    const { default: AppShell } = await import("./AppShell");

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
      <TestAuthWrapper>
        <RouterProvider router={testRouter} />
      </TestAuthWrapper>
    );

    expect(markup).toContain('href="/stacks"');
    expect(markup).toContain('href="/templates"');
    expect(markup).toContain('data-testid="identity-menu"');
    expect(markup).toContain('data-testid="shell-tenant-context"');
    expect(markup).toContain(">tenant_123<");
    expect(markup).not.toContain("<input");
    expect(markup).toContain('data-testid="outlet-content"');
  });

  it("displays the user's display name and a logout control", async () => {
    vi.stubEnv("VITE_TFLIVE_TENANT_ID", "tenant_123");
    const { default: AppShell } = await import("./AppShell");

    const testRouter = createMemoryRouter(
      [{ path: "/", element: <AppShell />, children: [{ index: true, element: <div /> }] }],
      { initialEntries: ["/"] }
    );

    const markup = renderToStaticMarkup(
      <TestAuthWrapper>
        <RouterProvider router={testRouter} />
      </TestAuthWrapper>
    );

    expect(markup).toContain("Otto Operator");
    expect(markup).toContain('data-testid="logout-button"');
  });
});
