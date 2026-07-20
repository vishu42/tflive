// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from "vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import StackAccessScreen from "./StackAccessScreen";
import type { ListGrantsResponse, SearchUsersResponse } from "../../api/types";

function wrapper(initialRoute = "/stacks/stack_123/access") {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false }
    }
  });
  return function Wrapper({ children }: { children: React.ReactNode }) {
    return (
      <QueryClientProvider client={queryClient}>
        <MemoryRouter initialEntries={[initialRoute]}>
          <Routes>
            <Route path="/stacks/:stackId/access" element={children} />
          </Routes>
        </MemoryRouter>
      </QueryClientProvider>
    );
  };
}

const emptyGrants: ListGrantsResponse = { grants: [] };

const twoGrants: ListGrantsResponse = {
  grants: [
    {
      userSub: "u1",
      role: "owner",
      displayName: "Alice",
      email: "alice@example.com"
    },
    {
      userSub: "u2",
      role: "viewer",
      displayName: "Bob",
      email: "bob@example.com"
    }
  ]
};

const searchResults: SearchUsersResponse = {
  users: [
    {
      id: "u3",
      username: "charlie",
      email: "charlie@example.com",
      firstName: "Charlie",
      lastName: "Brown"
    }
  ],
  first: 0,
  max: 20
};

describe("StackAccessScreen", () => {
  beforeEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it("shows loading state for grants", () => {
    vi.spyOn(globalThis, "fetch").mockImplementation(
      () => new Promise(() => {})
    );

    render(<StackAccessScreen />, { wrapper: wrapper() });

    expect(screen.getByText("Loading grants...")).toBeDefined();
  });

  it("renders grants list when data is loaded", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify(twoGrants), {
        status: 200,
        headers: { "content-type": "application/json" }
      })
    );

    render(<StackAccessScreen />, { wrapper: wrapper() });

    await waitFor(() => {
      expect(screen.getByText("Alice")).toBeDefined();
    });
    expect(screen.getByText("Bob")).toBeDefined();
    expect(screen.getByText("owner")).toBeDefined();
    expect(screen.getByText("viewer")).toBeDefined();
  });

  it("shows empty state when no grants", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify(emptyGrants), {
        status: 200,
        headers: { "content-type": "application/json" }
      })
    );

    render(<StackAccessScreen />, { wrapper: wrapper() });

    await waitFor(() => {
      expect(
        screen.getByText(
          "No users have been assigned access yet. Use the panel on the right to add the first grant."
        )
      ).toBeDefined();
    });
  });

  it("shows search results when typing 2+ characters", async () => {
    vi.spyOn(globalThis, "fetch")
      .mockResolvedValueOnce(
        new Response(JSON.stringify(emptyGrants), {
          status: 200,
          headers: { "content-type": "application/json" }
        })
      )
      .mockResolvedValueOnce(
        new Response(JSON.stringify(searchResults), {
          status: 200,
          headers: { "content-type": "application/json" }
        })
      );

    render(<StackAccessScreen />, { wrapper: wrapper() });
    const user = userEvent.setup();

    await waitFor(() => {
      expect(screen.getByText(/No users/)).toBeDefined();
    });

    const searchInput = screen.getByLabelText("Search users");
    await user.click(searchInput);
    await user.type(searchInput, "cha");

    await waitFor(() => {
      expect(screen.getByText("Charlie Brown")).toBeDefined();
    });
  });

  it("shows confirm state and triggers revoke", async () => {
    vi.spyOn(globalThis, "fetch")
      .mockResolvedValueOnce(
        new Response(JSON.stringify(twoGrants), {
          status: 200,
          headers: { "content-type": "application/json" }
        })
      )
      .mockResolvedValueOnce(
        new Response(null, {
          status: 204
        })
      )
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ grants: [twoGrants.grants[0]] }), {
          status: 200,
          headers: { "content-type": "application/json" }
        })
      );

    render(<StackAccessScreen />, { wrapper: wrapper() });
    const user = userEvent.setup();

    await waitFor(() => {
      expect(screen.getByText("Bob")).toBeDefined();
    });

    const revokeButton = screen.getByLabelText("Revoke Bob's viewer role");
    await user.click(revokeButton);

    expect(screen.getByText("Remove access?")).toBeDefined();
    expect(screen.getByText("Confirm")).toBeDefined();

    await user.click(screen.getByText("Confirm"));

    await waitFor(() => {
      expect(screen.getByText(/Removed Bob.*viewer access/)).toBeDefined();
    });
  });

  it("shows error banner on failed revoke", async () => {
    vi.spyOn(globalThis, "fetch")
      .mockResolvedValueOnce(
        new Response(JSON.stringify(twoGrants), {
          status: 200,
          headers: { "content-type": "application/json" }
        })
      )
      .mockResolvedValueOnce(
        new Response(
          JSON.stringify({
            error: "last_owner",
            message: "cannot remove the last owner"
          }),
          {
            status: 409,
            headers: { "content-type": "application/json" }
          }
        )
      );

    render(<StackAccessScreen />, { wrapper: wrapper() });
    const user = userEvent.setup();

    await waitFor(() => {
      expect(screen.getByText("Alice")).toBeDefined();
    });

    await user.click(screen.getByLabelText("Revoke Alice's owner role"));
    await user.click(screen.getByText("Confirm"));

    await waitFor(() => {
      expect(
        screen.getByText("cannot remove the last owner")
      ).toBeDefined();
    });
  });

  it("assigns role when selecting a user and clicking assign", async () => {
    vi.spyOn(globalThis, "fetch")
      .mockResolvedValueOnce(
        new Response(JSON.stringify(emptyGrants), {
          status: 200,
          headers: { "content-type": "application/json" }
        })
      )
      .mockResolvedValueOnce(
        new Response(JSON.stringify(searchResults), {
          status: 200,
          headers: { "content-type": "application/json" }
        })
      )
      .mockResolvedValueOnce(
        new Response(
          JSON.stringify({
            userSub: "u3",
            role: "viewer",
            displayName: "Charlie Brown"
          }),
          {
            status: 200,
            headers: { "content-type": "application/json" }
          }
        )
      )
      .mockResolvedValueOnce(
        new Response(
          JSON.stringify({
            grants: [
              {
                userSub: "u3",
                role: "viewer",
                displayName: "Charlie Brown",
                email: "charlie@example.com"
              }
            ]
          }),
          {
            status: 200,
            headers: { "content-type": "application/json" }
          }
        )
      );

    render(<StackAccessScreen />, { wrapper: wrapper() });
    const user = userEvent.setup();

    await waitFor(() => {
      expect(screen.getByText(/No users/)).toBeDefined();
    });

    await user.click(screen.getByLabelText("Search users"));
    await user.type(screen.getByLabelText("Search users"), "cha");

    await waitFor(() => {
      expect(screen.getByText("Charlie Brown")).toBeDefined();
    });

    await user.click(screen.getByText("Charlie Brown"));
    await user.click(screen.getByRole("button", { name: "Assign Role" }));

    await waitFor(() => {
      expect(globalThis.fetch).toHaveBeenCalledTimes(4);
    });
  });

  it("has proper tab order for keyboard accessibility", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify(emptyGrants), {
        status: 200,
        headers: { "content-type": "application/json" }
      })
    );

    render(<StackAccessScreen />, { wrapper: wrapper() });
    const user = userEvent.setup();

    await waitFor(() => {
      expect(screen.getByText(/No users/)).toBeDefined();
    });

    await user.tab();
    expect(screen.getByLabelText("Search users")).toEqual(
      document.activeElement
    );

    await user.tab();
    expect(screen.getByLabelText("Role")).toEqual(document.activeElement);

    const assignButton = screen.getByRole("button", { name: "Assign Role" });
    expect((assignButton as HTMLButtonElement).disabled).toBe(true);
  });
});
