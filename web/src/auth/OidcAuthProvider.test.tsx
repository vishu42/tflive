// @vitest-environment jsdom
import { render, waitFor } from "@testing-library/react";
import { describe, expect, it, vi, beforeEach, afterEach } from "vitest";
import type { ReactNode } from "react";

const mockGetUser = vi.fn();
const mockSigninRedirect = vi.fn();
const mockSigninRedirectCallback = vi.fn();
const mockSignoutRedirect = vi.fn();
const mockSignoutRedirectCallback = vi.fn();
const mockSigninSilent = vi.fn();

let currentPathname = "/";
let currentSearch = "";

vi.mock("./userManager", () => ({
  getUserManager: () => ({
    getUser: mockGetUser,
    signinRedirect: mockSigninRedirect,
    signinRedirectCallback: mockSigninRedirectCallback,
    signoutRedirect: mockSignoutRedirect,
    signoutRedirectCallback: mockSignoutRedirectCallback,
    signinSilent: mockSigninSilent,
    settings: { authority: "http://localhost:8082/realms/tflive" },
  }),
}));

const mockNavigate = vi.fn();

vi.mock("react-router-dom", async (importOriginal) => {
  const actual = await importOriginal<typeof import("react-router-dom")>();
  return {
    ...actual,
    useLocation: () => ({ pathname: currentPathname, search: currentSearch }),
    useNavigate: () => mockNavigate,
  };
});

function userFixture() {
  return {
    id_token: "",
    session_state: null,
    access_token: "at-fixture",
    refresh_token: "rt-fixture",
    token_type: "Bearer",
    scope: "openid",
    profile: {
      sub: "user-1",
      preferred_username: "testuser",
      email: "test@local",
      realm_access: { roles: ["platform-admin"] },
    },
    expires_at: Date.now() / 1000 + 300,
    state: null,
  };
}

describe("OidcAuthProvider", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    currentPathname = "/";
    currentSearch = "";
  });

  afterEach(() => {
    vi.resetModules();
  });

  function renderProvider(children: React.ReactNode) {
    const { createMemoryRouter, RouterProvider } = require("react-router-dom");
    const router = createMemoryRouter(
      [
        {
          path: "/",
          element: children,
          children: [{ index: true, element: <div /> }],
        },
        {
          path: "/stacks",
          element: children,
          children: [{ index: true, element: <div data-testid="child">hello</div> }],
        },
        {
          path: "/auth/callback",
          element: children,
          children: [{ index: true, element: <div /> }],
        },
      ],
      { initialEntries: [{ pathname: currentPathname, search: currentSearch }] }
    );
    return render(<RouterProvider router={router} />);
  }

  async function importProvider() {
    const mod = await import("./OidcAuthProvider");
    return mod.default;
  }

  it("renders children via Outlet when user is already authenticated", async () => {
    mockGetUser.mockResolvedValue(userFixture());
    currentPathname = "/stacks";

    const OidcAuthProvider = await importProvider();
    const { container } = renderProvider(<OidcAuthProvider />);

    await waitFor(() => {
      expect(container.innerHTML).toContain('data-testid="child"');
    });
    expect(mockSigninRedirect).not.toHaveBeenCalled();
  });

  it("triggers signin redirect when no user is found", async () => {
    mockGetUser.mockResolvedValue(null);
    mockSigninRedirect.mockResolvedValue(undefined);

    const OidcAuthProvider = await importProvider();
    renderProvider(<OidcAuthProvider />);

    await waitFor(() => {
      expect(mockSigninRedirect).toHaveBeenCalled();
    });
  });

  it("renders error UI when getUser fails (Keycloak unreachable)", async () => {
    mockGetUser.mockRejectedValue(new Error("Network error"));

    const OidcAuthProvider = await importProvider();
    const { container } = renderProvider(<OidcAuthProvider />);

    await waitFor(() => {
      expect(container.innerHTML).toContain('data-testid="auth-error"');
      expect(container.innerHTML).toContain('data-testid="auth-retry-button"');
    });
  });

  it("handles signin callback, then navigates to original route", async () => {
    currentPathname = "/auth/callback";
    currentSearch = "?code=abc&state=xyz";
    mockSigninRedirectCallback.mockResolvedValue(userFixture());

    const OidcAuthProvider = await importProvider();
    renderProvider(<OidcAuthProvider />);

    await waitFor(() => {
      expect(mockSigninRedirectCallback).toHaveBeenCalled();
    });
    await waitFor(() => {
      expect(mockNavigate).toHaveBeenCalled();
    });
  });

  it("handles signout callback, then redirects to signin", async () => {
    currentPathname = "/auth/callback";
    currentSearch = "?state=xyz";
    mockSignoutRedirectCallback.mockResolvedValue({} as never);
    mockSigninRedirect.mockResolvedValue(undefined);

    const OidcAuthProvider = await importProvider();
    renderProvider(<OidcAuthProvider />);

    await waitFor(() => {
      expect(mockSignoutRedirectCallback).toHaveBeenCalled();
    });
    await waitFor(() => {
      expect(mockSigninRedirect).toHaveBeenCalled();
    });
  });
});
