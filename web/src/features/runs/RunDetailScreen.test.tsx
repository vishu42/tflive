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
    me: { sub: "user_1", displayName: "Test User", globalCapabilities: { isPlatformAdmin: false, canCreateStack: false } },
    status: "authenticated",
    login: () => {},
    logout: () => {}
  };
}

function seedCapabilities(queryClient: QueryClient, capabilities: StackCapabilities) {
  queryClient.setQueryData(queryKeys.stack("tenant_123", "stack_1"), { stack: stack(capabilities), templates: [] });
}

function renderScreen(queryClient: QueryClient) {
  return render(
    <QueryClientProvider client={queryClient}>
      <AuthContext.Provider value={authValue()}>
        <MemoryRouter initialEntries={["/stacks/stack_1/runs/run_1"]}>
          <Routes>
            <Route path="/stacks/:stackId/runs/:runId" element={<RunDetailScreen />} />
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
    queryClient.setQueryData(queryKeys.templateRun("tenant_123", "run_1"), run({ status: "completed" }));

    vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
      const url = String(input);
      if (url.endsWith("/logs")) {
        return jsonResponse([runLog({ phase: "plan" }), runLog({ phase: "init" })]);
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
    expect(screen.getByText("plan")).toBeTruthy();
    await waitFor(() => expect(screen.getByText("plan log body")).toBeTruthy());

    fireEvent.click(screen.getByRole("button", { name: "init" }));
    await waitFor(() => expect(screen.getByText("init log body")).toBeTruthy());
  });

  it("enables Approve only for a waiting_approval apply run, gated by canApprove", async () => {
    const queryClient = testQueryClient();
    seedCapabilities(queryClient, allAllowed);
    queryClient.setQueryData(queryKeys.templateRun("tenant_123", "run_1"), run({ operation: "apply", status: "waiting_approval" }));
    vi.spyOn(globalThis, "fetch").mockResolvedValue(jsonResponse([]));

    renderScreen(queryClient);

    expect(isDisabled(screen.getByRole("button", { name: /Approve/ }))).toBe(false);
  });

  it("disables Approve with a reason when canApprove is denied", async () => {
    const queryClient = testQueryClient();
    seedCapabilities(queryClient, { ...allAllowed, canApprove: false });
    queryClient.setQueryData(queryKeys.templateRun("tenant_123", "run_1"), run({ operation: "apply", status: "waiting_approval" }));
    vi.spyOn(globalThis, "fetch").mockResolvedValue(jsonResponse([]));

    renderScreen(queryClient);

    expect(isDisabled(screen.getByRole("button", { name: /Approve/ }))).toBe(true);
    expect(screen.getByTestId("run-detail-approve-disabled-reason")).toBeTruthy();
  });

  it("enables Cancel for a non-terminal run and calls the cancellation endpoint, gated by canOperate", async () => {
    const queryClient = testQueryClient();
    seedCapabilities(queryClient, allAllowed);
    queryClient.setQueryData(queryKeys.templateRun("tenant_123", "run_1"), run({ status: "planned" }));
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

  it("disables Cancel with a reason when canOperate is denied", async () => {
    const queryClient = testQueryClient();
    seedCapabilities(queryClient, { ...allAllowed, canOperate: false });
    queryClient.setQueryData(queryKeys.templateRun("tenant_123", "run_1"), run({ status: "planned" }));
    vi.spyOn(globalThis, "fetch").mockResolvedValue(jsonResponse([]));

    renderScreen(queryClient);

    expect(isDisabled(screen.getByRole("button", { name: /Cancel/ }))).toBe(true);
    expect(screen.getByTestId("run-detail-cancel-disabled-reason")).toBeTruthy();
  });
});
