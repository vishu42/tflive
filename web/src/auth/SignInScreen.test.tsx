// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiRequestError } from "../api/client";
import SignInScreen, { safeReturnTo } from "./SignInScreen";
import { clearLoginAttempts, maxLoginAttempts } from "./loginAttempts";

vi.mock("../api/client", async () => {
  const actual = await vi.importActual<typeof import("../api/client")>("../api/client");
  return {
    ...actual,
    authMethods: vi.fn(),
    signInWithPassword: vi.fn()
  };
});

const { authMethods, signInWithPassword } = await import("../api/client");
const authMethodsMock = vi.mocked(authMethods);
const signInMock = vi.mocked(signInWithPassword);

const attemptsKey = "tflive.auth.loginAttempts";
const assign = vi.fn();
const reload = vi.fn();

function testQueryClient(): QueryClient {
  return new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
}

function renderSignIn(url = "/signin") {
  return render(
    <QueryClientProvider client={testQueryClient()}>
      <MemoryRouter initialEntries={[url]}>
        <SignInScreen />
      </MemoryRouter>
    </QueryClientProvider>
  );
}

async function signIn(username = "root", password = "hunter2") {
  const user = userEvent.setup();
  await user.type(screen.getByLabelText("Username"), username);
  await user.type(screen.getByLabelText("Password"), password);
  await user.click(screen.getByRole("button", { name: /sign in/i }));
  return user;
}

beforeEach(() => {
  vi.stubGlobal("location", { assign, reload, pathname: "/signin", search: "" });
  authMethodsMock.mockResolvedValue({ local: true, oidc: false });
  signInMock.mockResolvedValue(undefined);
});

afterEach(() => {
  cleanup();
  // clearLoginAttempts, not sessionStorage.clear: the module also holds a
  // once-per-page-load latch, and leaving it set makes every later test's
  // recorded attempt a silent no-op.
  clearLoginAttempts();
  vi.clearAllMocks();
  vi.unstubAllGlobals();
});

describe("safeReturnTo", () => {
  it("keeps an in-app path", () => {
    expect(safeReturnTo("/stacks?selected=st_1")).toBe("/stacks?selected=st_1");
  });

  // Everything here would send a freshly signed-in browser somewhere it should
  // not go: the first group all leave the origin, and /v1/auth/login restarts
  // sign-in the instant it succeeds. The rows mirror the server's table in
  // authn.TestSafeReturnTo -- the two are meant to be the same rule, and this
  // path never reaches the server to be caught there.
  it.each([
    ["absolute URL", "https://evil.test/steal"],
    ["protocol-relative", "//evil.test/steal"],
    ["backslash-relative", "/\\evil.test"],
    ["backslash-relative with a path", "/\\evil.test/steal"],
    ["a tab before the host", "/\t/evil.test"],
    ["a newline before the host", "/\n/evil.test"],
    ["a relative path", "stacks"],
    ["parent traversal", "/../../etc"],
    ["traversal into the API", "/stacks/../v1/auth/login"],
    ["the API", "/v1/auth/login"],
    ["the API root", "/v1"],
    ["nothing", null],
    ["empty", ""]
  ])("refuses %s", (_label, raw) => {
    expect(safeReturnTo(raw)).toBe("/");
  });

  // Only the /v1 segment itself is the API; a path that merely starts with
  // those characters is an app route like any other.
  it("keeps a /v1 lookalike path", () => {
    expect(safeReturnTo("/v10/stacks")).toBe("/v10/stacks");
  });
});

describe("SignInScreen", () => {
  // The password form is the one method that always exists -- root is a local
  // account and cannot be locked out -- so it must not wait on a request to
  // decide whether to render.
  it("renders the password form before /v1/auth/methods answers", () => {
    authMethodsMock.mockReturnValue(new Promise(() => {}));
    renderSignIn();

    expect(screen.getByLabelText("Username")).toBeTruthy();
    expect(screen.getByLabelText("Password")).toBeTruthy();
  });

  it("signs in and lands on the requested page", async () => {
    renderSignIn("/signin?return_to=%2Fstacks%3Fselected%3Dst_1");
    await signIn();

    await waitFor(() => {
      expect(signInMock).toHaveBeenCalledWith("root", "hunter2");
    });
    expect(assign).toHaveBeenCalledWith("/stacks?selected=st_1");
  });

  // An accepted sign-in is what the loop guard counts: if the cookie does not
  // survive the navigation above, this is the record that proves it.
  it("records the accepted sign-in", async () => {
    renderSignIn();
    await signIn();

    await waitFor(() => {
      expect(sessionStorage.getItem(attemptsKey)).toBe("1");
    });
  });

  it("reports a rejected password without navigating or spending an attempt", async () => {
    signInMock.mockRejectedValue(new ApiRequestError(401, "unauthorized", "authentication failed"));
    renderSignIn();
    await signIn();

    expect((await screen.findByTestId("signin-error")).textContent).toContain(
      "Incorrect username or password"
    );
    expect(assign).not.toHaveBeenCalled();
    expect(sessionStorage.getItem(attemptsKey)).toBeNull();
  });

  // A wrong password leaves the form usable. Reloading or clearing it would
  // make the second try start from an empty username.
  it("keeps what was typed after a rejection", async () => {
    signInMock.mockRejectedValue(new ApiRequestError(401, "unauthorized", "authentication failed"));
    renderSignIn();
    await signIn();

    await screen.findByTestId("signin-error");
    expect(screen.getByLabelText<HTMLInputElement>("Username").value).toBe("root");
  });

  // An outage is not a credential decision, and telling the user their password
  // is wrong would have them retyping a correct one until the store comes back.
  it("distinguishes an outage from a wrong password", async () => {
    signInMock.mockRejectedValue(new ApiRequestError(500, "internal_error", "internal error"));
    renderSignIn();
    await signIn();

    expect((await screen.findByTestId("signin-error")).textContent).toContain(
      "Sign-in is unavailable"
    );
  });

  it("offers SSO only where a provider is configured", async () => {
    renderSignIn();
    await waitFor(() => {
      expect(authMethodsMock).toHaveBeenCalled();
    });
    expect(screen.queryByTestId("signin-sso")).toBeNull();

    cleanup();
    authMethodsMock.mockResolvedValue({ local: true, oidc: true });
    renderSignIn();
    expect(await screen.findByTestId("signin-sso")).toBeTruthy();
  });

  // The SSO button leaves the SPA for the provider, carrying the same
  // return_to the password path would have used.
  it("starts the OIDC flow with a full navigation", async () => {
    authMethodsMock.mockResolvedValue({ local: true, oidc: true });
    renderSignIn("/signin?return_to=%2Fstacks");

    const user = userEvent.setup();
    await user.click(await screen.findByTestId("signin-sso"));

    expect(assign).toHaveBeenCalledWith("/v1/auth/login?return_to=%2Fstacks");
    expect(sessionStorage.getItem(attemptsKey)).toBe("1");
  });

  // Three accepted sign-ins that did not stick is a browser refusing the
  // cookie, not a user who cannot type their password.
  it("explains a blocked cookie once the attempts are spent", async () => {
    sessionStorage.setItem(attemptsKey, String(maxLoginAttempts));
    renderSignIn();

    expect(screen.getByTestId("signin-cookies-blocked")).toBeTruthy();
    expect(screen.queryByLabelText("Username")).toBeNull();
  });

  it("clears the count and reloads when the user tries again", async () => {
    sessionStorage.setItem(attemptsKey, String(maxLoginAttempts));
    renderSignIn();

    const user = userEvent.setup();
    await user.click(screen.getByTestId("signin-cookies-retry"));

    expect(sessionStorage.getItem(attemptsKey)).toBeNull();
    expect(reload).toHaveBeenCalled();
  });
});
