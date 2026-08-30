// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { ApiRequestError } from "../api/client";
import { clearLoginAttempts, maxLoginAttempts, readLoginAttempts } from "./loginAttempts";
import SessionProvider, { REAUTH_DEFER_LIMIT_MS, REAUTH_RETRY_MS } from "./SessionProvider";

const attemptsKey = "tflive.auth.loginAttempts";

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
    sessionStorage.clear();
    clearLoginAttempts();
  });

  afterEach(() => {
    // Nothing configures Testing Library's automatic cleanup here, so mounted
    // trees would otherwise pile up and make a testid ambiguous across tests.
    cleanup();
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

  it("reschedules instead of navigating when a refetch shows the session slid forward", async () => {
    const nearExpiry = new Date(Date.now() + 61 * 1000).toISOString();
    const slidExpiry = new Date(Date.now() + 10 * 60 * 1000).toISOString();
    // The initial /v1/me snapshot looks like it is about to expire; the
    // refetch that the re-auth attempt triggers reports it was slid forward
    // in the meantime, as a server-side touch would do for a session that is
    // still active.
    getMe.mockResolvedValueOnce({ ...me, sessionExpiresAt: nearExpiry });
    getMe.mockResolvedValue({ ...me, sessionExpiresAt: slidExpiry });

    renderProvider(<div data-testid="child">ready</div>);
    await screen.findByTestId("child");

    await vi.advanceTimersByTimeAsync(2 * 1000);
    expect(assign).not.toHaveBeenCalled();
    expect(getMe).toHaveBeenCalledTimes(2);

    // The old bound has now passed with no navigation: the schedule moved
    // with the refetched, later expiry instead.
    await vi.advanceTimersByTimeAsync(60 * 1000);
    expect(assign).not.toHaveBeenCalled();
  });

  it("navigates once a refetch confirms the session has actually ended", async () => {
    const nearExpiry = new Date(Date.now() + 61 * 1000).toISOString();
    // Every call returns the same fixed bound, so the refetch the re-auth
    // attempt triggers finds nothing moved — the session genuinely is near
    // its end.
    getMe.mockResolvedValue({ ...me, sessionExpiresAt: nearExpiry });

    renderProvider(<div data-testid="child">ready</div>);
    await screen.findByTestId("child");

    await vi.advanceTimersByTimeAsync(2 * 1000);
    expect(getMe).toHaveBeenCalledTimes(2);
    expect(assign).toHaveBeenCalledWith("/v1/auth/login?return_to=%2Fstacks");
  });

  it("stops deferring re-authentication once the deferral limit is reached", async () => {
    getMe.mockResolvedValue({ ...me, sessionExpiresAt: new Date(Date.now() + 61 * 1000).toISOString() });

    // A form that never stops claiming unsaved work.
    const guard = document.createElement("div");
    guard.setAttribute("data-unsaved", "true");
    document.body.append(guard);

    renderProvider(<div data-testid="child">ready</div>);
    await screen.findByTestId("child");

    // Reach the re-auth moment, then hold past the deferral limit.
    await vi.advanceTimersByTimeAsync(1000 + REAUTH_DEFER_LIMIT_MS + REAUTH_RETRY_MS);
    expect(assign).toHaveBeenCalledTimes(1);

    guard.remove();
  });

  it("keeps deferring re-authentication while unsaved work is present and the limit has not been reached", async () => {
    getMe.mockResolvedValue({ ...me, sessionExpiresAt: new Date(Date.now() + 61 * 1000).toISOString() });

    const guard = document.createElement("div");
    guard.setAttribute("data-unsaved", "true");
    document.body.append(guard);

    renderProvider(<div data-testid="child">ready</div>);
    await screen.findByTestId("child");

    await vi.advanceTimersByTimeAsync(1000 + REAUTH_RETRY_MS * 2);
    expect(assign).not.toHaveBeenCalled();

    guard.remove();
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

  // A redirect reloads the page, so the count has to live somewhere that
  // outlives this component for the guard to see a loop rather than a bounce.
  it("records the login redirect where a page reload cannot erase it", async () => {
    getMe.mockRejectedValue(new ApiRequestError(401, "unauthorized", "unauthorized"));
    renderProvider(<div data-testid="child">ready</div>);
    await waitFor(() => {
      expect(assign).toHaveBeenCalled();
    });
    expect(sessionStorage.getItem(attemptsKey)).toBe("1");
  });

  it("stops redirecting and explains once the attempts are spent", async () => {
    sessionStorage.setItem(attemptsKey, String(maxLoginAttempts));
    getMe.mockRejectedValue(new ApiRequestError(401, "unauthorized", "unauthorized"));
    renderProvider(<div data-testid="child">ready</div>);
    expect(await screen.findByTestId("auth-loop-error")).toBeTruthy();
    expect(assign).not.toHaveBeenCalled();
  });

  it("retries from the loop screen with a fresh count", async () => {
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    sessionStorage.setItem(attemptsKey, String(maxLoginAttempts));
    getMe.mockRejectedValue(new ApiRequestError(401, "unauthorized", "unauthorized"));
    renderProvider(<div data-testid="child">ready</div>);
    await user.click(await screen.findByTestId("auth-loop-retry-button"));
    expect(assign).toHaveBeenCalledWith("/v1/auth/login?return_to=%2Fstacks");
    expect(readLoginAttempts()).toBe(1);
  });

  it("clears the recorded attempts once /v1/me resolves", async () => {
    sessionStorage.setItem(attemptsKey, "2");
    getMe.mockResolvedValue(me);
    renderProvider(<div data-testid="child">ready</div>);
    await screen.findByTestId("child");
    expect(sessionStorage.getItem(attemptsKey)).toBeNull();
  });
});
