// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AuthContext } from "../../auth/AuthContext";
import type { AuthContextValue } from "../../auth/AuthContext";
import TemplateRegistrationScreen from "./TemplateRegistrationScreen";
import type { TemplateRegistration } from "../../api/types";

function registration(overrides: Partial<TemplateRegistration> = {}): TemplateRegistration {
  return {
    id: "reg_1",
    tenant_id: "tenant_123",
    repo_owner: "hashicorp",
    repo_name: "terraform-aws-vpc",
    source_ref: "main",
    root_path: ".",
    status: "completed",
    template_revision_id: "rev_2",
    resolved_commit_sha: "1234567abcdef",
    requested_by: "user_123",
    requested_at: "2026-07-20T00:00:00Z",
    error_summary: "",
    ...overrides
  };
}

function testQueryClient(): QueryClient {
  return new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
}

function authValue(): AuthContextValue {
  return {
    me: { sub: "user_1", tenantID: "tenant_123", displayName: "Test User", globalCapabilities: { isPlatformAdmin: false, canCreateStack: false } },
    status: "authenticated",
    login: () => {},
    logout: () => {},
  };
}

// Stands in for the list screen so a redirect is observable as rendered
// output, including the search string that carries the new selection.
function TemplatesListStub() {
  const location = useLocation();
  return <div data-testid="templates-list-stub">{location.search}</div>;
}

function renderScreen(queryClient: QueryClient) {
  return render(
    <QueryClientProvider client={queryClient}>
      <AuthContext.Provider value={authValue()}>
        <MemoryRouter initialEntries={["/templates/new"]}>
          <Routes>
            <Route path="/templates/new" element={<TemplateRegistrationScreen />} />
            <Route path="/templates" element={<TemplatesListStub />} />
          </Routes>
        </MemoryRouter>
      </AuthContext.Provider>
    </QueryClientProvider>
  );
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

describe("TemplateRegistrationScreen", () => {
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it("renders the registration form", () => {
    renderScreen(testQueryClient());

    expect(screen.getByLabelText(/Owner/)).toBeTruthy();
    expect(screen.getByLabelText(/Repository/)).toBeTruthy();
    expect(screen.getByLabelText(/Ref/)).toBeTruthy();
    expect(screen.getByLabelText(/Root path/)).toBeTruthy();
    expect(screen.getByRole("button", { name: /Register/ })).toBeTruthy();
  });

  it("posts the form values and redirects to the list with the new revision selected", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockImplementation(async (input, init) => {
      if (init?.method === "POST") {
        return jsonResponse(registration());
      }
      if (String(input).includes("/template-registrations/")) {
        return jsonResponse(registration());
      }
      throw new Error(`unexpected fetch: ${String(input)}`);
    });

    renderScreen(testQueryClient());

    fireEvent.change(screen.getByLabelText(/Repository/), { target: { value: "terraform-aws-eks" } });
    fireEvent.click(screen.getByRole("button", { name: /Register/ }));

    await waitFor(() => expect(screen.getByTestId("templates-list-stub")).toBeTruthy());
    expect(screen.getByTestId("templates-list-stub").textContent).toBe("?selected=rev_2");

    const postCall = fetchMock.mock.calls.find(([, init]) => init?.method === "POST");
    expect(postCall).toBeTruthy();
    expect(JSON.parse(String(postCall?.[1]?.body))).toEqual({
      repo_owner: "hashicorp",
      repo_name: "terraform-aws-eks",
      source_ref: "main",
      root_path: "."
    });
  });

  it("stays on the form and reports progress while registration is still running", async () => {
    vi.spyOn(globalThis, "fetch").mockImplementation(async (_input, init) => {
      if (init?.method === "POST") {
        return jsonResponse(registration({ status: "running", template_revision_id: "" }));
      }
      return jsonResponse(registration({ status: "running", template_revision_id: "" }));
    });

    renderScreen(testQueryClient());

    fireEvent.click(screen.getByRole("button", { name: /Register/ }));

    await waitFor(() => expect(screen.getByText("running")).toBeTruthy());
    expect(screen.queryByTestId("templates-list-stub")).toBeNull();
  });

  it("shows an error message when the register request fails", async () => {
    vi.spyOn(globalThis, "fetch").mockImplementation(async (_input, init) => {
      if (init?.method === "POST") {
        return jsonResponse({ error: "invalid_request", message: "repo_name is required" }, 400);
      }
      throw new Error("unexpected fetch");
    });

    renderScreen(testQueryClient());

    fireEvent.click(screen.getByRole("button", { name: /Register/ }));

    await waitFor(() => expect(screen.getByText(/repo_name is required/)).toBeTruthy());
    expect(screen.queryByTestId("templates-list-stub")).toBeNull();
  });

  it("surfaces the error summary and does not redirect when registration fails server-side", async () => {
    const failed = registration({ status: "failed", template_revision_id: "", error_summary: "clone failed: repository not found" });
    vi.spyOn(globalThis, "fetch").mockImplementation(async () => jsonResponse(failed));

    renderScreen(testQueryClient());

    fireEvent.click(screen.getByRole("button", { name: /Register/ }));

    await waitFor(() => expect(screen.getByText(/clone failed: repository not found/)).toBeTruthy());
    expect(screen.queryByTestId("templates-list-stub")).toBeNull();
    // A failed attempt must be retryable without a reload.
    expect(screen.getByRole("button", { name: /Register/ }).hasAttribute("disabled")).toBe(false);
  });

  it("links back to the templates list", () => {
    renderScreen(testQueryClient());

    expect(screen.getByTestId("template-registration-back").getAttribute("href")).toBe("/templates");
  });
});
