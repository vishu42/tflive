// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AuthContext } from "../../auth/AuthContext";
import type { AuthContextValue } from "../../auth/AuthContext";
import TemplateRegistryScreen from "./TemplateRegistryScreen";
import { queryKeys } from "../../api/queryKeys";
import type { TemplateRevision } from "../../api/types";

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

function renderScreen(queryClient: QueryClient, initialEntry = "/templates") {
  return render(
    <QueryClientProvider client={queryClient}>
      <AuthContext.Provider value={authValue()}>
        <MemoryRouter initialEntries={[initialEntry]}>
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
    vi.unstubAllGlobals();
  });

  it("shows a loading state while the template revisions query is pending", () => {
    vi.spyOn(globalThis, "fetch").mockReturnValue(new Promise(() => {}));

    renderScreen(testQueryClient());

    expect(screen.getByTestId("template-registry-loading")).toBeTruthy();
  });

  it("lists one row per template, named after the template", () => {
    const queryClient = testQueryClient();
    queryClient.setQueryData(queryKeys.templateRevisions("tenant_123"), [
      revision(),
      revision({ id: "rev_2", source_template_id: "tpl_2", name: "EKS", repo_name: "terraform-aws-eks" })
    ]);

    renderScreen(queryClient);

    expect(screen.getByTestId("templates-list")).toBeTruthy();
    // The name leads the row — it is what answers "what is this?".
    expect(screen.getByTestId("template-row-tpl_1").querySelector(".templates-list__name")?.textContent).toBe("VPC");
    expect(screen.getByTestId("template-row-tpl_2").querySelector(".templates-list__name")?.textContent).toBe("EKS");
  });

  it("collapses every revision of one template into a single row", () => {
    const queryClient = testQueryClient();
    queryClient.setQueryData(queryKeys.templateRevisions("tenant_123"), [
      revision({ id: "rev_2", resolved_commit_sha: "44b2e0199999" }),
      revision({ id: "rev_1" })
    ]);

    renderScreen(queryClient);

    const row = screen.getByTestId("template-row-tpl_1");
    expect(screen.getByTestId("templates-list").querySelectorAll("li")).toHaveLength(1);
    // The count is the affordance saying there is something behind the click.
    expect(row.textContent).toContain("2 revisions");
    // The newest revision names the row's commit.
    expect(row.textContent).toContain("44b2e01");
  });

  it("links a row to its template's revisions", () => {
    const queryClient = testQueryClient();
    queryClient.setQueryData(queryKeys.templateRevisions("tenant_123"), [revision()]);

    renderScreen(queryClient);

    const link = screen.getByTestId("template-row-tpl_1").querySelector("a");
    expect(link?.getAttribute("href")).toBe("/templates/tpl_1");
  });

  it("groups templates under one heading per repository", () => {
    const queryClient = testQueryClient();
    queryClient.setQueryData(queryKeys.templateRevisions("tenant_123"), [
      revision({ id: "rev_1", source_template_id: "tpl_1", source_ref: "main" }),
      // A different ref is a different source template, not another revision.
      revision({ id: "rev_2", source_template_id: "tpl_2", source_ref: "v2", resolved_commit_sha: "44b2e0199999" }),
      revision({ id: "rev_3", source_template_id: "tpl_3", name: "EKS", repo_name: "terraform-aws-eks" })
    ]);

    renderScreen(queryClient);

    const vpcGroup = screen.getByTestId("template-group-hashicorp/terraform-aws-vpc");
    const eksGroup = screen.getByTestId("template-group-hashicorp/terraform-aws-eks");

    expect(vpcGroup.textContent).toContain("hashicorp/terraform-aws-vpc");
    expect(vpcGroup.querySelectorAll("li")).toHaveLength(2);
    expect(eksGroup.querySelectorAll("li")).toHaveLength(1);
    // Each row belongs to exactly one group.
    expect(vpcGroup.contains(screen.getByTestId("template-row-tpl_2"))).toBe(true);
    expect(eksGroup.contains(screen.getByTestId("template-row-tpl_3"))).toBe(true);
  });

  it("counts templates per repository, not revisions", () => {
    const queryClient = testQueryClient();
    queryClient.setQueryData(queryKeys.templateRevisions("tenant_123"), [
      revision({ id: "rev_1" }),
      revision({ id: "rev_2", resolved_commit_sha: "44b2e0199999" })
    ]);

    renderScreen(queryClient);

    // Two revisions of one template are one template.
    expect(screen.getByTestId("template-group-count-hashicorp/terraform-aws-vpc").textContent).toBe("1");
  });

  it("keeps the repository out of the row itself now that the heading carries it", () => {
    const queryClient = testQueryClient();
    queryClient.setQueryData(queryKeys.templateRevisions("tenant_123"), [revision()]);

    renderScreen(queryClient);

    const row = screen.getByTestId("template-row-tpl_1");
    expect(row.textContent).not.toContain("hashicorp/terraform-aws-vpc");
    expect(row.textContent).toContain("main");
    expect(row.textContent).toContain("abcdef1");
  });

  it("says nothing about an active template, and spells out any other status", () => {
    const queryClient = testQueryClient();
    queryClient.setQueryData(queryKeys.templateRevisions("tenant_123"), [
      revision(),
      revision({ id: "rev_2", source_template_id: "tpl_2", name: "EKS", status: "invalid" })
    ]);

    renderScreen(queryClient);

    // Active is the ordinary outcome; its absence is the signal.
    expect(screen.getByTestId("template-row-tpl_1").textContent).not.toContain("active");
    expect(screen.getByTestId("template-row-tpl_1").querySelector(".status-tone")).toBeNull();
    // Invalid is the one status a user must act on, so it keeps its pill.
    expect(screen.getByTestId("template-row-tpl_2").textContent).toContain("invalid");
    expect(screen.getByTestId("template-row-tpl_2").querySelector(".status-tone")).toBeTruthy();
  });

  it("highlights the template holding the revision named by the selected param", () => {
    const queryClient = testQueryClient();
    queryClient.setQueryData(queryKeys.templateRevisions("tenant_123"), [
      revision({ id: "rev_1" }),
      revision({ id: "rev_2", source_template_id: "tpl_2", name: "EKS", repo_name: "terraform-aws-eks" })
    ]);

    renderScreen(queryClient, "/templates?selected=rev_2");

    const eksGroup = screen.getByTestId("template-group-hashicorp/terraform-aws-eks");
    const selected = screen.getByTestId("template-row-tpl_2");
    expect(selected.getAttribute("data-selected")).toBe("true");
    expect(eksGroup.contains(selected)).toBe(true);
  });

  it("does not render the registration form — it lives at /templates/new", () => {
    const queryClient = testQueryClient();
    queryClient.setQueryData(queryKeys.templateRevisions("tenant_123"), [revision()]);

    renderScreen(queryClient);

    expect(screen.queryByLabelText(/Repository/)).toBeNull();
    expect(screen.queryByRole("button", { name: /Register/ })).toBeNull();
  });

  it("links to the registration route from the header", () => {
    const queryClient = testQueryClient();
    queryClient.setQueryData(queryKeys.templateRevisions("tenant_123"), [revision()]);

    renderScreen(queryClient);

    const link = screen.getByTestId("register-template-link");
    expect(link.getAttribute("href")).toBe("/templates/new");
  });

  it("leaves every other template unhighlighted", () => {
    const queryClient = testQueryClient();
    queryClient.setQueryData(queryKeys.templateRevisions("tenant_123"), [
      revision(),
      revision({ id: "rev_2", source_template_id: "tpl_2", name: "EKS", repo_name: "terraform-aws-eks" })
    ]);

    renderScreen(queryClient, "/templates?selected=rev_2");

    expect(screen.getByTestId("template-row-tpl_2").getAttribute("data-selected")).toBe("true");
    expect(screen.getByTestId("template-row-tpl_1").getAttribute("data-selected")).toBeNull();
  });

  it("renders an empty state when no templates are registered", () => {
    const queryClient = testQueryClient();
    queryClient.setQueryData(queryKeys.templateRevisions("tenant_123"), []);

    renderScreen(queryClient);

    expect(screen.getByTestId("templates-list-empty")).toBeTruthy();
    expect(screen.queryByTestId("templates-list")).toBeNull();
    // The register affordance must survive the empty state — it is the only
    // way out of it.
    expect(screen.getByTestId("register-template-link")).toBeTruthy();
  });

  it("renders the shared boundary screen for a handled API error status", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      jsonResponse({ error: "unavailable", message: "service unavailable" }, 503)
    );

    renderScreen(testQueryClient());

    await waitFor(() => expect(screen.getByTestId("route-service-unavailable")).toBeTruthy());
  });

  it("renders the generic error state when the API returns 401", async () => {
    // The 401 drives client.ts's fetchWithAuth to navigate via
    // globalThis.location.assign; stub it so jsdom does not attempt (and
    // warn about) a real navigation.
    vi.stubGlobal("location", { ...window.location, assign: vi.fn() });
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      jsonResponse({ error: "unauthorized", message: "unauthorized" }, 401)
    );

    renderScreen(testQueryClient());

    await waitFor(() => expect(screen.getByTestId("template-registry-error")).toBeTruthy());
  });

  it("renders AccessDenied when the API returns 403", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      jsonResponse({ error: "forbidden", message: "forbidden" }, 403)
    );

    renderScreen(testQueryClient());

    await waitFor(() => expect(screen.getByTestId("route-access-denied")).toBeTruthy());
  });

  it("renders NotFound when the API returns 404", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      jsonResponse({ error: "not_found", message: "not found" }, 404)
    );

    renderScreen(testQueryClient());

    await waitFor(() => expect(screen.getByTestId("route-not-found")).toBeTruthy());
  });

  it("renders a retryable generic error state for unhandled failures", async () => {
    const fetchMock = vi
      .spyOn(globalThis, "fetch")
      .mockRejectedValueOnce(new TypeError("network down"))
      .mockResolvedValueOnce(jsonResponse([revision()]));

    renderScreen(testQueryClient());

    await waitFor(() => expect(screen.getByTestId("template-registry-error")).toBeTruthy());
    fireEvent.click(screen.getByTestId("template-registry-retry"));
    await waitFor(() => expect(screen.getByTestId("templates-list")).toBeTruthy());
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });
});
