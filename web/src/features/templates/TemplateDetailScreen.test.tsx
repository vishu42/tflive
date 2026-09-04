// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AuthContext } from "../../auth/AuthContext";
import type { AuthContextValue } from "../../auth/AuthContext";
import TemplateDetailScreen from "./TemplateDetailScreen";
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
    root_path: "environments/dev",
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
    me: {
      sub: "user_1",
      tenantID: "tenant_123",
      displayName: "Test User",
      globalCapabilities: { isPlatformAdmin: false, canCreateStack: false }
    },
    status: "authenticated",
    login: () => {},
    logout: () => {}
  };
}

function renderScreen(queryClient: QueryClient, initialEntry = "/templates/tpl_1") {
  return render(
    <QueryClientProvider client={queryClient}>
      <AuthContext.Provider value={authValue()}>
        <MemoryRouter initialEntries={[initialEntry]}>
          <Routes>
            <Route path="/templates/:sourceTemplateId" element={<TemplateDetailScreen />} />
          </Routes>
        </MemoryRouter>
      </AuthContext.Provider>
    </QueryClientProvider>
  );
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

describe("TemplateDetailScreen", () => {
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it("shows a loading state while the template revisions query is pending", () => {
    vi.spyOn(globalThis, "fetch").mockReturnValue(new Promise(() => {}));

    renderScreen(testQueryClient());

    expect(screen.getByTestId("template-detail-loading")).toBeTruthy();
  });

  it("lists every revision of the template, newest first", () => {
    const queryClient = testQueryClient();
    queryClient.setQueryData(queryKeys.templateRevisions("tenant_123"), [
      revision({ id: "rev_3", resolved_commit_sha: "f17f9834444" }),
      revision({ id: "rev_2", resolved_commit_sha: "a91c2045555" }),
      // A different template's revision must not leak in.
      revision({ id: "rev_9", source_template_id: "tpl_2", resolved_commit_sha: "9999999" })
    ]);

    renderScreen(queryClient);

    const rows = screen.getByTestId("template-revisions").querySelectorAll("li");
    expect(rows).toHaveLength(2);
    // The API's created_at desc order is rendered as received, not re-sorted.
    expect(rows[0].textContent).toContain("f17f983");
    expect(rows[1].textContent).toContain("a91c204");
    expect(screen.queryByTestId("revision-row-rev_9")).toBeNull();
  });

  it("marks only the newest revision as latest", () => {
    const queryClient = testQueryClient();
    queryClient.setQueryData(queryKeys.templateRevisions("tenant_123"), [
      revision({ id: "rev_2", resolved_commit_sha: "f17f9834444" }),
      revision({ id: "rev_1" })
    ]);

    renderScreen(queryClient);

    expect(screen.getAllByTestId("revision-latest")).toHaveLength(1);
    // The marker is present on every row to reserve its width, so what
    // distinguishes them is the attribute, not the text.
    expect(screen.getByTestId("revision-row-rev_2").querySelector("[data-latest]")).toBeTruthy();
    expect(screen.getByTestId("revision-row-rev_1").querySelector("[data-latest]")).toBeNull();
  });

  it("says nothing about an active revision, and spells out any other status", () => {
    const queryClient = testQueryClient();
    queryClient.setQueryData(queryKeys.templateRevisions("tenant_123"), [
      revision({ id: "rev_2", status: "invalid", resolved_commit_sha: "f17f9834444" }),
      revision({ id: "rev_1", status: "active" })
    ]);

    renderScreen(queryClient);

    // Same rule as the registry list: active is the ordinary outcome, so its
    // absence is the signal and only the exceptions interrupt a scan.
    expect(screen.getByTestId("revision-row-rev_1").textContent).not.toContain("active");
    expect(screen.getByTestId("revision-row-rev_1").querySelector(".status-tone")).toBeNull();
    expect(screen.getByTestId("revision-row-rev_2").textContent).toContain("invalid");
    expect(screen.getByTestId("revision-row-rev_2").querySelector(".status-tone")).toBeTruthy();
  });

  it("states the identity the rows all share, once, in the header", () => {
    const queryClient = testQueryClient();
    queryClient.setQueryData(queryKeys.templateRevisions("tenant_123"), [revision()]);

    renderScreen(queryClient);

    const identity = screen.getByTestId("template-detail-identity").textContent ?? "";
    expect(identity).toContain("hashicorp/terraform-aws-vpc");
    expect(identity).toContain("environments/dev");
    expect(identity).toContain("main");
    // The ref is fixed for the whole template, so no row repeats it.
    expect(screen.getByTestId("revision-row-rev_1").textContent).not.toContain("main");
  });

  it("renders the date each revision was registered", () => {
    const queryClient = testQueryClient();
    queryClient.setQueryData(queryKeys.templateRevisions("tenant_123"), [
      revision({ created_at: "2026-07-19T00:00:00Z" })
    ]);

    renderScreen(queryClient);

    expect(screen.getByTestId("revision-row-rev_1").textContent).toContain("19 Jul 2026");
  });

  it("highlights the revision named by the selected search param", () => {
    const queryClient = testQueryClient();
    queryClient.setQueryData(queryKeys.templateRevisions("tenant_123"), [
      revision({ id: "rev_2", resolved_commit_sha: "f17f9834444" }),
      revision({ id: "rev_1" })
    ]);

    renderScreen(queryClient, "/templates/tpl_1?selected=rev_1");

    expect(screen.getByTestId("revision-row-rev_1").getAttribute("data-selected")).toBe("true");
    expect(screen.getByTestId("revision-row-rev_2").getAttribute("data-selected")).toBeNull();
  });

  it("links back to the registry", () => {
    const queryClient = testQueryClient();
    queryClient.setQueryData(queryKeys.templateRevisions("tenant_123"), [revision()]);

    renderScreen(queryClient);

    expect(screen.getByTestId("template-detail-back").getAttribute("href")).toBe("/templates");
  });

  it("renders NotFound for an id no revision belongs to", () => {
    const queryClient = testQueryClient();
    queryClient.setQueryData(queryKeys.templateRevisions("tenant_123"), [revision()]);

    renderScreen(queryClient, "/templates/tpl_missing");

    expect(screen.getByTestId("route-not-found")).toBeTruthy();
  });

  it("renders the shared boundary screen for a handled API error status", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      jsonResponse({ error: "unavailable", message: "service unavailable" }, 503)
    );

    renderScreen(testQueryClient());

    await waitFor(() => expect(screen.getByTestId("route-service-unavailable")).toBeTruthy());
  });

  it("renders a retryable generic error state for unhandled failures", async () => {
    vi.spyOn(globalThis, "fetch").mockRejectedValue(new TypeError("network down"));

    renderScreen(testQueryClient());

    await waitFor(() => expect(screen.getByTestId("template-detail-error")).toBeTruthy());
  });
});
