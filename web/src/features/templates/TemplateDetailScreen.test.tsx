// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AuthContext } from "../../auth/AuthContext";
import type { AuthContextValue } from "../../auth/AuthContext";
import TemplateDetailScreen from "./TemplateDetailScreen";
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

function authValue(canPublishTemplate = true): AuthContextValue {
  return {
    me: {
      sub: "user_1",
      tenantID: "tenant_123",
      displayName: "Test User",
      globalCapabilities: { isPlatformAdmin: false, canCreateStack: false, canPublishTemplate }
    },
    status: "authenticated",
    login: () => {},
    logout: () => {}
  };
}

function registration(overrides: Partial<TemplateRegistration> = {}): TemplateRegistration {
  return {
    id: "reg_1",
    tenant_id: "tenant_123",
    repo_owner: "hashicorp",
    repo_name: "terraform-aws-vpc",
    source_ref: "main",
    root_path: "environments/dev",
    status: "completed",
    template_revision_id: "rev_1",
    resolved_commit_sha: "abcdef1234567890",
    requested_by: "user_1",
    requested_at: "2026-07-20T00:00:00Z",
    error_summary: "",
    ...overrides
  };
}

function renderScreen(
  queryClient: QueryClient,
  initialEntry = "/templates/tpl_1",
  auth: AuthContextValue = authValue()
) {
  return render(
    <QueryClientProvider client={queryClient}>
      <AuthContext.Provider value={auth}>
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

  // ---- Sync: re-register the identity the header already states ----

  it("posts the identity from the header when Sync is clicked", async () => {
    const queryClient = testQueryClient();
    queryClient.setQueryData(queryKeys.templateRevisions("tenant_123"), [revision()]);
    const fetchMock = vi.spyOn(globalThis, "fetch").mockImplementation(async (input, init) => {
      if (init?.method === "POST") {
        return jsonResponse(registration({ status: "pending" }));
      }
      if (String(input).includes("/template-registrations/")) {
        return jsonResponse(registration());
      }
      return jsonResponse([revision()]);
    });

    renderScreen(queryClient);
    fireEvent.click(screen.getByTestId("template-sync"));

    await waitFor(() => expect(fetchMock.mock.calls.some(([, init]) => init?.method === "POST")).toBe(true));
    const postCall = fetchMock.mock.calls.find(([, init]) => init?.method === "POST");
    expect(String(postCall?.[0])).toContain("/v1/tenants/tenant_123/template-revisions");
    // Exactly the identity the template is keyed on, so the sync lands on this
    // template rather than minting a second one.
    expect(JSON.parse(String(postCall?.[1]?.body))).toEqual({
      repo_owner: "hashicorp",
      repo_name: "terraform-aws-vpc",
      source_ref: "main",
      root_path: "environments/dev"
    });
  });

  it("disables Sync while one is in flight, so repeated clicks do not queue parallel workflows", async () => {
    const queryClient = testQueryClient();
    queryClient.setQueryData(queryKeys.templateRevisions("tenant_123"), [revision()]);
    const fetchMock = vi.spyOn(globalThis, "fetch").mockImplementation(async (input, init) => {
      if (init?.method === "POST") {
        return jsonResponse(registration({ status: "pending" }));
      }
      if (String(input).includes("/template-registrations/")) {
        // Never reaches a terminal status, so the flow stays in flight.
        return jsonResponse(registration({ status: "running" }));
      }
      return jsonResponse([revision()]);
    });

    renderScreen(queryClient);
    const button = screen.getByTestId("template-sync") as HTMLButtonElement;
    expect(button.disabled).toBe(false);

    fireEvent.click(button);

    await waitFor(() => expect(button.disabled).toBe(true));
    fireEvent.click(button);
    expect(fetchMock.mock.calls.filter(([, init]) => init?.method === "POST")).toHaveLength(1);
  });

  it("shows the new revision at the top once the sync resolves a new commit", async () => {
    const queryClient = testQueryClient();
    queryClient.setQueryData(queryKeys.templateRevisions("tenant_123"), [revision()]);
    const fresh = revision({ id: "rev_2", resolved_commit_sha: "f17f9834444" });
    vi.spyOn(globalThis, "fetch").mockImplementation(async (input, init) => {
      if (init?.method === "POST") {
        return jsonResponse(registration({ status: "pending" }));
      }
      if (String(input).includes("/template-registrations/")) {
        return jsonResponse(registration({ template_revision_id: "rev_2" }));
      }
      return jsonResponse([fresh, revision()]);
    });

    renderScreen(queryClient);
    fireEvent.click(screen.getByTestId("template-sync"));

    // The one test that drives the real shape of the flow: a pending 202,
    // then a poll that completes. That poll is POLL_INTERVAL_MS away, so the
    // wait has to outlast it — the tests below start from a terminal
    // registration instead, and stay fast.
    await waitFor(() => expect(screen.getByTestId("revision-row-rev_2")).toBeTruthy(), { timeout: 3000 });
    expect(screen.getByTestId("revision-latest").closest("li")?.getAttribute("data-testid")).toBe("revision-row-rev_2");
    expect(screen.queryByTestId("template-sync-result")).toBeNull();
  });

  it("says the template is already up to date when the ref still resolves to a known commit", async () => {
    const queryClient = testQueryClient();
    queryClient.setQueryData(queryKeys.templateRevisions("tenant_123"), [revision()]);
    // The insert conflicted, so the registration names the revision the screen
    // was already showing.
    const unchanged = registration({ template_revision_id: "rev_1" });
    vi.spyOn(globalThis, "fetch").mockImplementation(async (input, init) =>
      init?.method === "POST" || String(input).includes("/template-registrations/")
        ? jsonResponse(unchanged)
        : jsonResponse([revision()])
    );

    renderScreen(queryClient);
    fireEvent.click(screen.getByTestId("template-sync"));

    await waitFor(() => expect(screen.getByTestId("template-sync-result").textContent).toContain("Already up to date"));
    expect(screen.getByTestId("template-revisions").querySelectorAll("li")).toHaveLength(1);
  });

  it("shows the registration's error when the sync fails", async () => {
    const queryClient = testQueryClient();
    queryClient.setQueryData(queryKeys.templateRevisions("tenant_123"), [revision()]);
    const failed = registration({ status: "failed", template_revision_id: "", error_summary: "ref not found: main" });
    vi.spyOn(globalThis, "fetch").mockImplementation(async (input, init) =>
      init?.method === "POST" || String(input).includes("/template-registrations/")
        ? jsonResponse(failed)
        : jsonResponse([revision()])
    );

    renderScreen(queryClient);
    fireEvent.click(screen.getByTestId("template-sync"));

    await waitFor(() => expect(screen.getByTestId("template-sync-error").textContent).toContain("ref not found: main"));
    // The failure is terminal, so the button is offered again.
    expect((screen.getByTestId("template-sync") as HTMLButtonElement).disabled).toBe(false);
  });

  it("surfaces a rejected POST rather than leaving the button spinning", async () => {
    const queryClient = testQueryClient();
    queryClient.setQueryData(queryKeys.templateRevisions("tenant_123"), [revision()]);
    vi.spyOn(globalThis, "fetch").mockImplementation(async (input, init) => {
      if (init?.method === "POST") {
        return jsonResponse({ error: "forbidden", message: "forbidden" }, 403);
      }
      return jsonResponse([revision()]);
    });

    renderScreen(queryClient);
    fireEvent.click(screen.getByTestId("template-sync"));

    await waitFor(() => expect(screen.getByTestId("template-sync-error")).toBeTruthy());
    expect((screen.getByTestId("template-sync") as HTMLButtonElement).disabled).toBe(false);
  });

  it("surfaces a failing registration poll rather than leaving the button spinning", async () => {
    const queryClient = testQueryClient();
    queryClient.setQueryData(queryKeys.templateRevisions("tenant_123"), [revision()]);
    // The POST seeds the cache with a `pending` registration; every poll after
    // that fails, so the status never reaches a terminal value on its own.
    vi.spyOn(globalThis, "fetch").mockImplementation(async (input, init) => {
      if (init?.method === "POST") {
        return jsonResponse(registration({ status: "pending", template_revision_id: "" }));
      }
      if (String(input).includes("/template-registrations/")) {
        return jsonResponse({ error: "forbidden", message: "forbidden" }, 403);
      }
      return jsonResponse([revision()]);
    });

    renderScreen(queryClient);
    fireEvent.click(screen.getByTestId("template-sync"));

    // The seeded `pending` data is fresh, so the first poll waits a full
    // POLL_INTERVAL_MS — past waitFor's default timeout.
    await waitFor(() => expect(screen.getByTestId("template-sync-error").textContent).toContain("forbidden"), {
      timeout: 3000
    });
    expect((screen.getByTestId("template-sync") as HTMLButtonElement).disabled).toBe(false);
  });

  it("hides Sync from a user without can_publish_template", () => {
    const queryClient = testQueryClient();
    queryClient.setQueryData(queryKeys.templateRevisions("tenant_123"), [revision()]);

    renderScreen(queryClient, "/templates/tpl_1", authValue(false));

    // The POST would 403, so the action is never offered.
    expect(screen.queryByTestId("template-sync")).toBeNull();
  });
});
