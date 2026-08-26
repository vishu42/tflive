// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import { ApiRequestError } from "../api/client";
import SessionProvider from "./SessionProvider";

const getMe = vi.fn();
const logout = vi.fn();

vi.mock("../api/client", async () => {
  const actual = await vi.importActual<typeof import("../api/client")>("../api/client");
  return { ...actual, getMe: () => getMe(), logout: () => logout() };
});

function renderProvider(children: ReactNode) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={["/stacks"]}>
        <Routes>
          <Route element={<SessionProvider />}>
            <Route path="/stacks" element={children} />
          </Route>
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>
  );
}

const me = {
  sub: "user-123",
  displayName: "Ada",
  email: "ada@example.test",
  tenantID: "tenant_123",
  globalCapabilities: { isPlatformAdmin: false, canCreateStack: true },
  sessionExpiresAt: new Date(Date.now() + 60 * 60 * 1000).toISOString()
};

describe("SessionProvider", () => {
  let assign: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    assign = vi.fn();
    vi.stubGlobal("location", { ...window.location, assign, pathname: "/stacks", search: "" });
    getMe.mockReset();
    logout.mockReset();
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it("renders children once /v1/me resolves", async () => {
    getMe.mockResolvedValue(me);
    renderProvider(<div data-testid="child">ready</div>);
    expect(await screen.findByTestId("child")).toBeTruthy();
    expect(assign).not.toHaveBeenCalled();
  });

  it("navigates to the login route on a 401", async () => {
    getMe.mockRejectedValue(new ApiRequestError(401, "unauthorized", "unauthorized"));
    renderProvider(<div data-testid="child">ready</div>);
    await waitFor(() => {
      expect(assign).toHaveBeenCalledWith("/v1/auth/login?return_to=%2Fstacks");
    });
  });

  it("re-authenticates sixty seconds before the session expires", async () => {
    getMe.mockResolvedValue({ ...me, sessionExpiresAt: new Date(Date.now() + 120 * 1000).toISOString() });
    renderProvider(<div data-testid="child">ready</div>);
    await screen.findByTestId("child");

    expect(assign).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(61 * 1000);
    expect(assign).toHaveBeenCalledWith("/v1/auth/login?return_to=%2Fstacks");
  });

  it("navigates immediately when the session has already expired", async () => {
    getMe.mockResolvedValue({ ...me, sessionExpiresAt: new Date(Date.now() - 1000).toISOString() });
    renderProvider(<div data-testid="child">ready</div>);
    await waitFor(() => {
      expect(assign).toHaveBeenCalledWith("/v1/auth/login?return_to=%2Fstacks");
    });
  });

  it("renders an error state when /v1/me fails for a non-auth reason", async () => {
    getMe.mockRejectedValue(new ApiRequestError(503, "unavailable", "unavailable"));
    renderProvider(<div data-testid="child">ready</div>);
    expect(await screen.findByTestId("auth-error")).toBeTruthy();
    expect(assign).not.toHaveBeenCalled();
  });
});
