// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AuthContext } from "../../auth/AuthContext";
import type { AuthContextValue } from "../../auth/AuthContext";
import type { StackCapabilities } from "../../auth/types";
import { queryKeys } from "../../api/queryKeys";
import type { Stack, TemplateRun, TemplateRunLog } from "../../api/types";
import RunDetailScreen from "./RunDetailScreen";

function stack(capabilities: StackCapabilities): Stack {
  return {
    id: "stack_1",
    tenant_id: "tenant_123",
    name: "Payments",
    slug: "payments",
    tags: {},
    default_credential_ids: [],
    created_by: "user_123",
    created_at: "2026-07-19T00:00:00Z",
    effectiveCapabilities: capabilities
  };
}

function run(overrides: Partial<TemplateRun> = {}): TemplateRun {
  return {
    id: "run_1",
    tenant_id: "tenant_123",
    stack_template_id: "stpl_1",
    template_revision_id: "rev_1",
    source_template_id: "tpl_1",
    operation: "plan",
    selected_ref: "main",
    resolved_commit_sha: "abcdef1234567890",
    workspace_name: "acme-prod-primary",
    config_json: {},
    backend_type: "s3",
    backend_config_hash: "hash",
    status: "completed",
    trigger_actor: "user_123",
    started_at: "2026-07-20T00:00:00Z",
    error_summary: "",
    run_number: 1,
    auto_approve: false,
    plan_summary: null,
    ...overrides
  };
}

function runLog(overrides: Partial<TemplateRunLog> = {}): TemplateRunLog {
  return {
    tenant_id: "tenant_123",
    run_id: "run_1",
    phase: "plan",
    object_key: "key",
    content_type: "text/plain",
    size_bytes: 10,
    uploaded_at: "2026-07-20T00:01:00Z",
    ...overrides
  };
}

const allAllowed: StackCapabilities = { canView: true, canOperate: true, canApprove: true, canManageAccess: true };

function testQueryClient(): QueryClient {
  return new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
}

function authValue(): AuthContextValue {
  return {
    me: { sub: "user_1", tenantID: "tenant_123", displayName: "Test User", globalCapabilities: { isPlatformAdmin: false, canCreateStack: false, canPublishTemplate: false } },
    status: "authenticated",
    login: () => {},
    logout: () => {}
  };
}

function seedCapabilities(queryClient: QueryClient, capabilities: StackCapabilities) {
  queryClient.setQueryData(queryKeys.stack("tenant_123", "stack_1"), { stack: stack(capabilities), templates: [] });
}

// The URL names the run by number; the screen finds its id in the template's
// runs list. Every test here is about the run itself, so the list is seeded
// with run #1 unless a test has already put something else there.
function renderScreen(queryClient: QueryClient, auth?: AuthContextValue, runNumber = "1") {
  if (queryClient.getQueryData(queryKeys.templateRuns("tenant_123", "stpl_1")) === undefined) {
    queryClient.setQueryData(queryKeys.templateRuns("tenant_123", "stpl_1"), [run()]);
  }
  return render(
    <QueryClientProvider client={queryClient}>
      <AuthContext.Provider value={auth ?? authValue()}>
        <MemoryRouter initialEntries={[`/stacks/stack_1/templates/stpl_1/runs/${runNumber}`]}>
          <Routes>
            <Route path="/stacks/:stackId/templates/:stackTemplateId/runs/:runNumber" element={<RunDetailScreen />} />
          </Routes>
        </MemoryRouter>
      </AuthContext.Provider>
    </QueryClientProvider>
  );
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

function isDisabled(element: HTMLElement): boolean {
  return (element as HTMLButtonElement).disabled;
}

describe("RunDetailScreen", () => {
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it("finds the run by its number within the template", async () => {
    const queryClient = testQueryClient();
    seedCapabilities(queryClient, allAllowed);
    queryClient.setQueryData(queryKeys.templateRuns("tenant_123", "stpl_1"), [
      run({ id: "run_newer", run_number: 2 }),
      run({ id: "run_older", run_number: 1 })
    ]);
    queryClient.setQueryData(queryKeys.templateRun("tenant_123", "run_older"), run({ id: "run_older", run_number: 1, status: "failed", error_summary: "the older one" }));
    vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
      if (String(input).includes("/template-runs/run_older/logs")) {
        return jsonResponse([]);
      }
      throw new Error(`unexpected fetch: ${String(input)}`);
    });

    renderScreen(queryClient, undefined, "1");

    // The breadcrumb names the run; the screen shows the run it resolved.
    expect(screen.getByTestId("run-detail-status").textContent).toContain("Plan failed");
    expect(screen.getByText("the older one")).toBeTruthy();
  });

  it("says so when the template has no run with that number", () => {
    const queryClient = testQueryClient();
    seedCapabilities(queryClient, allAllowed);

    renderScreen(queryClient, undefined, "9");

    expect(screen.getByTestId("run-detail-missing").textContent).toContain("#9");
  });

  it("shows a loading state while the run query is pending", () => {
    vi.spyOn(globalThis, "fetch").mockReturnValue(new Promise(() => {}));

    renderScreen(testQueryClient());

    expect(screen.getByTestId("run-detail-loading")).toBeTruthy();
  });

  it("renders the shared boundary screen for a handled API error status", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(jsonResponse({ error: "unavailable", message: "service unavailable" }, 503));

    renderScreen(testQueryClient());

    await waitFor(() => expect(screen.getByTestId("route-service-unavailable")).toBeTruthy());
  });

  it("renders the generic error state when the API returns 401", async () => {
    // The 401 drives client.ts's fetchWithAuth to navigate via
    // globalThis.location.assign; stub it so jsdom does not attempt (and
    // warn about) a real navigation.
    vi.stubGlobal("location", { ...window.location, assign: vi.fn() });
    vi.spyOn(globalThis, "fetch").mockResolvedValue(jsonResponse({ error: "unauthorized", message: "unauthorized" }, 401));

    renderScreen(testQueryClient());

    await waitFor(() => expect(screen.getByTestId("run-detail-error")).toBeTruthy());
  });

  it("renders AccessDenied when the API returns 403", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(jsonResponse({ error: "forbidden", message: "forbidden" }, 403));

    renderScreen(testQueryClient());

    await waitFor(() => expect(screen.getByTestId("route-access-denied")).toBeTruthy());
  });

  it("renders NotFound when the API returns 404", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(jsonResponse({ error: "not_found", message: "not found" }, 404));

    renderScreen(testQueryClient());

    await waitFor(() => expect(screen.getByTestId("route-not-found")).toBeTruthy());
  });

  it("renders a retryable generic error state for unhandled failures", async () => {
    let runCalls = 0;
    const fetchMock = vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
      const url = String(input);
      if (url.endsWith("/template-runs/run_1")) {
        runCalls += 1;
        if (runCalls === 1) {
          throw new TypeError("network down");
        }
        return jsonResponse(run());
      }
      if (url.endsWith("/logs")) {
        return jsonResponse([]);
      }
      throw new Error(`unexpected fetch: ${url}`);
    });

    renderScreen(testQueryClient());

    await waitFor(() => expect(screen.getByTestId("run-detail-error")).toBeTruthy());
    fireEvent.click(screen.getByTestId("run-detail-retry"));
    await waitFor(() => expect(screen.getByTestId("run-detail-screen")).toBeTruthy());
    expect(fetchMock.mock.calls.filter(([reqInput]) => String(reqInput).endsWith("/template-runs/run_1")).length).toBe(2);
  });

  it("renders the run summary and logs, switching phase and log body when a phase tab is clicked", async () => {
    const queryClient = testQueryClient();
    seedCapabilities(queryClient, allAllowed);
    queryClient.setQueryData(queryKeys.templateRun("tenant_123", "run_1"), run({ status: "completed", run_number: 4 }));

    vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
      const url = String(input);
      if (url.endsWith("/logs")) {
        return jsonResponse([runLog({ phase: "init" }), runLog({ phase: "plan" })]);
      }
      if (url.endsWith("/logs/plan")) {
        return new Response("plan log body", { status: 200, headers: { "content-type": "text/plain" } });
      }
      if (url.endsWith("/logs/init")) {
        return new Response("init log body", { status: 200, headers: { "content-type": "text/plain" } });
      }
      throw new Error(`unexpected fetch: ${url}`);
    });

    renderScreen(queryClient);

    expect(screen.getByTestId("run-detail-screen")).toBeTruthy();
    expect(screen.getByTestId("run-detail-status").textContent).toContain("No changes");
    expect(screen.getByText("main @ abcdef1")).toBeTruthy();
    await waitFor(() => expect(screen.getByText("plan log body")).toBeTruthy());

    fireEvent.click(screen.getByRole("button", { name: "init" }));
    await waitFor(() => expect(screen.getByText("init log body")).toBeTruthy());
  });

  it("renders the activity failure summary for a failed run", () => {
    const queryClient = testQueryClient();
    seedCapabilities(queryClient, allAllowed);
    queryClient.setQueryData(
      queryKeys.templateRun("tenant_123", "run_1"),
      run({ status: "failed", error_summary: "template run activity failed: log upload failed" })
    );
    vi.spyOn(globalThis, "fetch").mockResolvedValue(jsonResponse([]));

    renderScreen(queryClient);

    expect(screen.getByText("template run activity failed: log upload failed")).toBeTruthy();
    // A finished run takes no actions, so none are offered.
    expect(screen.queryByRole("button", { name: /Cancel/ })).toBeNull();
  });

  it("offers Approve and Discard for a plan waiting for approval, and shows what it would change", async () => {
    const queryClient = testQueryClient();
    seedCapabilities(queryClient, allAllowed);
    queryClient.setQueryData(
      queryKeys.templateRun("tenant_123", "run_1"),
      run({ status: "waiting_approval", plan_summary: { add: 2, change: 0, destroy: 1 } })
    );
    vi.spyOn(globalThis, "fetch").mockResolvedValue(jsonResponse([]));

    renderScreen(queryClient);

    expect(isDisabled(screen.getByRole("button", { name: /^Approve$/ }))).toBe(false);
    expect(screen.getByRole("button", { name: /Discard/ })).toBeTruthy();
    expect(screen.getByText("+2 ~0 -1")).toBeTruthy();
  });

  // Approving a destroy plan is what destroys, so it names the count and takes
  // a second click before it calls the approval endpoint.
  it("asks for a second click before approving a destroy plan", async () => {
    const queryClient = testQueryClient();
    seedCapabilities(queryClient, allAllowed);
    queryClient.setQueryData(
      queryKeys.templateRun("tenant_123", "run_1"),
      run({ operation: "destroy", status: "waiting_approval", plan_summary: { add: 0, change: 0, destroy: 4 } })
    );
    const fetchMock = vi.spyOn(globalThis, "fetch").mockImplementation(async (input, init) => {
      if (init?.method === "POST") {
        return new Response(null, { status: 204 });
      }
      if (String(input).endsWith("/template-runs/run_1")) {
        return jsonResponse(run({ operation: "destroy", status: "approved", plan_summary: { add: 0, change: 0, destroy: 4 } }));
      }
      return jsonResponse([]);
    });

    renderScreen(queryClient);

    fireEvent.click(screen.getByRole("button", { name: /Destroy 4/ }));
    expect(fetchMock.mock.calls.some(([, init]) => init?.method === "POST")).toBe(false);
    fireEvent.click(screen.getByRole("button", { name: /Confirm/ }));
    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(expect.stringContaining("/template-runs/run_1/approval"), expect.objectContaining({ method: "POST" }))
    );
  });

  it("hides Approve when canApprove is denied", async () => {
    const queryClient = testQueryClient();
    seedCapabilities(queryClient, { ...allAllowed, canApprove: false });
    queryClient.setQueryData(queryKeys.templateRun("tenant_123", "run_1"), run({ status: "waiting_approval" }));
    vi.spyOn(globalThis, "fetch").mockResolvedValue(jsonResponse([]));

    renderScreen(queryClient);

    expect(screen.queryByRole("button", { name: /^Approve$/ })).toBeNull();
  });

  it("enables Cancel for a non-terminal run and calls the cancellation endpoint, gated by canOperate", async () => {
    const queryClient = testQueryClient();
    seedCapabilities(queryClient, allAllowed);
    queryClient.setQueryData(queryKeys.templateRun("tenant_123", "run_1"), run({ status: "plan_finished" }));
    const fetchMock = vi.spyOn(globalThis, "fetch").mockImplementation(async (input, init) => {
      const url = String(input);
      if (url.endsWith("/logs")) {
        return jsonResponse([]);
      }
      if (init?.method === "POST" && url.endsWith("/template-runs/run_1/cancellation")) {
        return new Response(null, { status: 204 });
      }
      throw new Error(`unexpected fetch: ${url} ${init?.method ?? "GET"}`);
    });

    renderScreen(queryClient);

    expect(isDisabled(screen.getByRole("button", { name: /Cancel/ }))).toBe(false);
    fireEvent.click(screen.getByRole("button", { name: /Cancel/ }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        expect.stringContaining("/template-runs/run_1/cancellation"),
        expect.objectContaining({ method: "POST" })
      )
    );
  });

  it("hides Cancel when canOperate is denied", async () => {
    const queryClient = testQueryClient();
    seedCapabilities(queryClient, { ...allAllowed, canOperate: false });
    queryClient.setQueryData(queryKeys.templateRun("tenant_123", "run_1"), run({ status: "plan_finished" }));
    vi.spyOn(globalThis, "fetch").mockResolvedValue(jsonResponse([]));

    renderScreen(queryClient);

    expect(screen.queryByRole("button", { name: /Cancel/ })).toBeNull();
  });
});
