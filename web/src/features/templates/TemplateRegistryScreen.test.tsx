// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AuthContext } from "../../auth/AuthContext";
import type { AuthContextValue } from "../../auth/AuthContext";
import TemplateRegistryScreen from "./TemplateRegistryScreen";
import { queryKeys } from "../../api/queryKeys";
import type { TemplateRegistration, TemplateRevision } from "../../api/types";

function revision(overrides: Partial<TemplateRevision> = {}): TemplateRevision {
  return {
    id: "rev_1",
    tenant_id: "tenant_123",
    source_template_id: "tpl_1",
    repo_owner: "hashicorp",
    repo_name: "terraform-aws-vpc",
    source_ref: "main",
    resolved_commit_sha: "abcdef1234567890",
    root_path: ".",
    name: "VPC",
    description: "",
    tags: [],
    status: "active",
    created_at: "2026-07-19T00:00:00Z",
    ...overrides
  };
}

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

// staleTime: Infinity keeps seeded cache data from triggering a background
// refetch on mount — see the identical rationale in StacksListScreen.test.tsx.
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

function renderScreen(queryClient: QueryClient) {
  return render(
    <QueryClientProvider client={queryClient}>
      <AuthContext.Provider value={authValue()}>
        <MemoryRouter initialEntries={["/templates"]}>
          <TemplateRegistryScreen />
        </MemoryRouter>
      </AuthContext.Provider>
    </QueryClientProvider>
  );
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

describe("TemplateRegistryScreen", () => {
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it("shows a loading state while the template revisions query is pending", () => {
    vi.spyOn(globalThis, "fetch").mockReturnValue(new Promise(() => {}));

    renderScreen(testQueryClient());

    expect(screen.getByTestId("template-registry-loading")).toBeTruthy();
  });

  it("lists saved template revisions and auto-selects the first one", () => {
    const queryClient = testQueryClient();
    queryClient.setQueryData(queryKeys.templateRevisions("tenant_123"), [
      revision(),
      revision({ id: "rev_2", name: "EKS", repo_name: "terraform-aws-eks" })
    ]);

    renderScreen(queryClient);

    const select = screen.getByLabelText<HTMLSelectElement>(/Saved template/);
    expect(select.value).toBe("rev_1");
    const optionText = Array.from(select.options).map((option) => option.textContent);
    expect(optionText.some((text) => text?.includes("VPC"))).toBe(true);
    expect(optionText.some((text) => text?.includes("EKS"))).toBe(true);
  });

  it("renders the shared boundary screen for a handled API error status", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      jsonResponse({ error: "unavailable", message: "service unavailable" }, 503)
    );

    renderScreen(testQueryClient());

    await waitFor(() => expect(screen.getByTestId("route-service-unavailable")).toBeTruthy());
  });

  it("renders a retryable generic error state for unhandled failures", async () => {
    const fetchMock = vi
      .spyOn(globalThis, "fetch")
      .mockRejectedValueOnce(new TypeError("network down"))
      .mockResolvedValueOnce(jsonResponse([revision()]));

    renderScreen(testQueryClient());

    await waitFor(() => expect(screen.getByTestId("template-registry-error")).toBeTruthy());
    fireEvent.click(screen.getByTestId("template-registry-retry"));
    await waitFor(() => expect(screen.getByLabelText(/Saved template/)).toBeTruthy());
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("registers a template and selects the new revision once registration completes", async () => {
    const queryClient = testQueryClient();
    queryClient.setQueryData(queryKeys.templateRevisions("tenant_123"), [revision()]);
    const fetchMock = vi.spyOn(globalThis, "fetch").mockImplementation(async (input, init) => {
      if (init?.method === "POST") {
        return jsonResponse(registration());
      }
      if (String(input).endsWith("/template-revisions")) {
        return jsonResponse([revision(), revision({ id: "rev_2", name: "EKS", repo_name: "terraform-aws-eks" })]);
      }
      throw new Error(`unexpected fetch: ${String(input)}`);
    });

    renderScreen(queryClient);

    fireEvent.change(screen.getByLabelText(/Repository/), { target: { value: "terraform-aws-eks" } });
    fireEvent.click(screen.getByRole("button", { name: /Register/ }));

    await waitFor(() => expect(screen.getByText("completed")).toBeTruthy());
    await waitFor(() => expect(screen.getByLabelText<HTMLSelectElement>(/Saved template/).value).toBe("rev_2"));

    const postCall = fetchMock.mock.calls.find(([, init]) => init?.method === "POST");
    expect(postCall).toBeTruthy();
    expect(JSON.parse(String(postCall?.[1]?.body))).toEqual({
      repo_owner: "hashicorp",
      repo_name: "terraform-aws-eks",
      source_ref: "main",
      root_path: "."
    });
  });

  it("shows an error message when registration fails", async () => {
    const queryClient = testQueryClient();
    queryClient.setQueryData(queryKeys.templateRevisions("tenant_123"), [revision()]);
    vi.spyOn(globalThis, "fetch").mockImplementation(async (_input, init) => {
      if (init?.method === "POST") {
        return jsonResponse({ error: "invalid_request", message: "repo_name is required" }, 400);
      }
      throw new Error("unexpected fetch");
    });

    renderScreen(queryClient);

    fireEvent.click(screen.getByRole("button", { name: /Register/ }));

    await waitFor(() => expect(screen.getByText(/repo_name is required/)).toBeTruthy());
  });
});
