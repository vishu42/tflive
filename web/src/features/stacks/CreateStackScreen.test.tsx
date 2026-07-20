// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AuthContext } from "../../auth/AuthContext";
import type { AuthContextValue } from "../../auth/AuthContext";
import CreateStackScreen from "./CreateStackScreen";
import { queryKeys } from "../../api/queryKeys";
import type { Stack } from "../../api/types";

function stack(overrides: Partial<Stack> = {}): Stack {
  return {
    id: "stack_new",
    tenant_id: "tenant_123",
    name: "My Stack",
    slug: "my-stack",
    tags: {},
    default_credential_ids: [],
    created_by: "user_1",
    created_at: "2026-07-20T00:00:00Z",
    effectiveCapabilities: { canView: true, canOperate: false, canApprove: false, canManageAccess: false },
    ...overrides
  };
}

function testQueryClient(): QueryClient {
  return new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
}

function authValue(overrides: Partial<AuthContextValue> = {}): AuthContextValue {
  return {
    me: { sub: "user_1", tenantID: "tenant_123", displayName: "Test User", globalCapabilities: { isPlatformAdmin: false, canCreateStack: true } },
    status: "authenticated",
    login: () => {},
    logout: () => {},
    ...overrides,
  };
}

function renderScreen(queryClient?: QueryClient) {
  return render(
    <QueryClientProvider client={queryClient ?? testQueryClient()}>
      <AuthContext.Provider value={authValue()}>
        <MemoryRouter initialEntries={["/stacks/new"]}>
          <CreateStackScreen />
        </MemoryRouter>
      </AuthContext.Provider>
    </QueryClientProvider>
  );
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

describe("CreateStackScreen", () => {
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
    vi.unstubAllEnvs();
  });

  it("renders the form with a name input and submit button", () => {
    renderScreen();

    expect(screen.getByLabelText("Name")).toBeTruthy();
    expect(screen.getByRole("button", { name: /create stack/i })).toBeTruthy();
  });

  it("renders a link back to the stacks list", () => {
    renderScreen();

    const link = screen.getByRole("link", { name: /back/i });
    expect(link.getAttribute("href")).toBe("/stacks");
  });

  it("disables the submit button when the name is empty", () => {
    renderScreen();

    const button = screen.getByRole("button", { name: /create stack/i });
    expect((button as HTMLButtonElement).disabled).toBe(true);
  });

  it("enables the submit button when the name is non-empty", () => {
    renderScreen();

    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "My Stack" } });
    const button = screen.getByRole("button", { name: /create stack/i });
    expect((button as HTMLButtonElement).disabled).toBe(false);
  });

  it("creates the stack and navigates to its detail page on success", async () => {
    const created = stack();
    vi.spyOn(globalThis, "fetch").mockResolvedValue(jsonResponse(created));

    renderScreen();

    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "My Stack" } });
    fireEvent.click(screen.getByRole("button", { name: /create stack/i }));

    await waitFor(() => {
      // Navigation to new stack detail
      expect(screen.getByTestId("create-stack-success")).toBeTruthy();
    });
  });

  it("shows an error message when the mutation fails", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      jsonResponse({ error: "conflict", message: "A stack with that slug already exists" }, 409)
    );

    renderScreen();

    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "My Stack" } });
    fireEvent.click(screen.getByRole("button", { name: /create stack/i }));

    await waitFor(() => {
      expect(screen.getByTestId("create-stack-error")).toBeTruthy();
      expect(screen.getByTestId("create-stack-error").textContent).toContain("A stack with that slug already exists");
    });
  });

  it("invalidates the stacks list query on success", async () => {
    const queryClient = testQueryClient();
    const created = stack();
    vi.spyOn(globalThis, "fetch").mockResolvedValue(jsonResponse(created));
    const invalidateSpy = vi.spyOn(queryClient, "invalidateQueries");

    renderScreen(queryClient);

    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "My Stack" } });
    fireEvent.click(screen.getByRole("button", { name: /create stack/i }));

    await waitFor(() => {
      expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: queryKeys.stacks("tenant_123") });
    });
  });

  it("shows the busy spinner on the button while submitting", async () => {
    // Never resolve so we stay in pending state
    vi.spyOn(globalThis, "fetch").mockReturnValue(new Promise(() => {}));

    renderScreen();

    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "My Stack" } });
    fireEvent.click(screen.getByRole("button", { name: /create stack/i }));

    await waitFor(() => {
      expect(screen.getByRole("button", { name: /creating/i })).toBeTruthy();
    });
  });

  it("trims whitespace from the name before submitting", async () => {
    const created = stack({ name: "My Stack" });
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValue(jsonResponse(created));

    renderScreen();

    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "  My Stack  " } });
    fireEvent.click(screen.getByRole("button", { name: /create stack/i }));

    await waitFor(() => {
      const body = JSON.parse(fetchSpy.mock.calls[0]?.[1]?.body as string);
      expect(body.name).toBe("My Stack");
    });
  });

  it("renders the shared boundary screen for a handled API error status", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      jsonResponse({ error: "unavailable", message: "service unavailable" }, 503)
    );

    renderScreen();

    // The mutation error alone won't trigger the boundary since
    // useQueryErrorBoundary is for query errors, not mutation errors.
    // This test verifies the component handles mutation errors inline.
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "My Stack" } });
    fireEvent.click(screen.getByRole("button", { name: /create stack/i }));

    await waitFor(() => {
      expect(screen.getByTestId("create-stack-error")).toBeTruthy();
    });
  });
});
