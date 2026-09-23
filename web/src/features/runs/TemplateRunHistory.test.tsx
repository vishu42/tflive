// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import { queryKeys } from "../../api/queryKeys";
import type { TemplateRun } from "../../api/types";
import { AuthContext } from "../../auth/AuthContext";
import TemplateRunHistory from "./TemplateRunHistory";

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
    status: "queued",
    step: "",
    trigger_actor: "user_123",
    started_at: "2026-07-20T00:00:00Z",
    error_summary: "",
    run_number: 1,
    auto_approve: false,
    plan_summary: null,
    ...overrides
  };
}

function testQueryClient(): QueryClient {
  return new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
}

function seedRuns(queryClient: QueryClient, runs: TemplateRun[]) {
  queryClient.setQueryData(queryKeys.templateRuns("tenant_123", "stpl_1"), runs);
}

function renderHistory(queryClient: QueryClient) {
  return render(
    <QueryClientProvider client={queryClient}>
      <AuthContext.Provider
        value={{
          me: { sub: "user_1", tenantID: "tenant_123", displayName: "Test User", globalCapabilities: { isPlatformAdmin: false, canCreateStack: false, canPublishTemplate: false } },
          status: "authenticated",
          login: () => {},
          logout: () => {}
        }}
      >
        <MemoryRouter initialEntries={["/stacks/stack_1/templates/stpl_1/runs"]}>
          <TemplateRunHistory stackId="stack_1" stackTemplateId="stpl_1" />
        </MemoryRouter>
      </AuthContext.Provider>
    </QueryClientProvider>
  );
}

describe("TemplateRunHistory", () => {
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it("links every run in the history to its run detail screen", () => {
    const queryClient = testQueryClient();
    seedRuns(queryClient, [
      run({ id: "run_apply_1", run_number: 2, operation: "apply", status: "waiting_approval", trigger_actor: "someone_else", started_at: "2026-07-20T01:00:00Z" }),
      run({ id: "run_plan_1", run_number: 1, operation: "plan", status: "completed", trigger_actor: "someone_else", started_at: "2026-07-20T00:00:00Z" })
    ]);

    renderHistory(queryClient);

    // Links carry the run's number within its template, not its id.
    expect(screen.getByTestId("template-run-history-run_apply_1").getAttribute("href")).toBe("/stacks/stack_1/templates/stpl_1/runs/2");
    expect(screen.getByTestId("template-run-history-run_plan_1").getAttribute("href")).toBe("/stacks/stack_1/templates/stpl_1/runs/1");
  });

  it("lays each run out in run, type, status, changes, actor, and time columns", () => {
    const queryClient = testQueryClient();
    seedRuns(queryClient, [
      run({ id: "run_plan_1", run_number: 12, operation: "apply", status: "completed", trigger_actor: "vishu", plan_summary: { add: 3, change: 1, destroy: 0 } })
    ]);

    renderHistory(queryClient);

    expect(screen.getAllByRole("columnheader").map((header) => header.textContent)).toEqual(["Run", "Status", "Changes", "Actor", "Time"]);
    const cells = within(screen.getByTestId("template-run-row-run_plan_1")).getAllByRole("cell");
    expect(cells).toHaveLength(5);
    expect(cells[0].textContent).toBe("#12");
    expect(cells[1].textContent).toContain("Applied");
    expect(cells[2].textContent).toBe("+3 ~1 -0");
    expect(cells[3].textContent).toBe("vishu");
    expect(cells[4].querySelector("time")?.getAttribute("datetime")).toBe("2026-07-20T00:00:00Z");
  });

  // Only a plan waiting for approval can be acted on; a run in flight cannot
  // be stopped, so it adds no column.
  it("adds an actions column only while some plan waits for approval", () => {
    const queryClient = testQueryClient();
    seedRuns(queryClient, [
      run({ id: "run_apply_1", run_number: 2, operation: "apply", status: "waiting_approval" }),
      run({ id: "run_plan_1", run_number: 1, operation: "plan", status: "completed" })
    ]);

    renderHistory(queryClient);

    expect(screen.getAllByRole("columnheader").map((header) => header.textContent)).toEqual(["Run", "Status", "Changes", "Actor", "Time", "Actions"]);
    expect(within(screen.getByTestId("template-run-row-run_plan_1")).getAllByRole("cell")).toHaveLength(6);
  });

  it("adds no actions column for a run in flight", () => {
    const queryClient = testQueryClient();
    seedRuns(queryClient, [run({ id: "run_apply_1", run_number: 1, operation: "apply", status: "running" })]);

    renderHistory(queryClient);

    expect(screen.getAllByRole("columnheader").map((header) => header.textContent)).toEqual(["Run", "Status", "Changes", "Actor", "Time"]);
  });

  it("shows an empty state rather than a bare heading when no run has started", () => {
    const queryClient = testQueryClient();
    seedRuns(queryClient, []);

    renderHistory(queryClient);

    expect(screen.getByTestId("template-run-history-empty")).toBeTruthy();
  });
});
