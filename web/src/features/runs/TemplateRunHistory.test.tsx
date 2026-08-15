// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import { queryKeys } from "../../api/queryKeys";
import type { TemplateRun } from "../../api/types";
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
    trigger_actor: "user_123",
    started_at: "2026-07-20T00:00:00Z",
    error_summary: "",
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
      <MemoryRouter initialEntries={["/stacks/stack_1/template"]}>
        <TemplateRunHistory stackId="stack_1" stackTemplateId="stpl_1" />
      </MemoryRouter>
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
      run({ id: "run_apply_1", operation: "apply", status: "waiting_approval", trigger_actor: "someone_else", started_at: "2026-07-20T01:00:00Z" }),
      run({ id: "run_plan_1", operation: "plan", status: "completed", trigger_actor: "someone_else", started_at: "2026-07-20T00:00:00Z" })
    ]);

    renderHistory(queryClient);

    expect(screen.getByTestId("template-run-history-run_apply_1").getAttribute("href")).toBe("/stacks/stack_1/runs/run_apply_1");
    expect(screen.getByTestId("template-run-history-run_plan_1").getAttribute("href")).toBe("/stacks/stack_1/runs/run_plan_1");
  });

  it("describes each run by operation, status, actor, and start time", () => {
    const queryClient = testQueryClient();
    seedRuns(queryClient, [run({ id: "run_plan_1", operation: "plan", status: "completed", trigger_actor: "vishu" })]);

    renderHistory(queryClient);

    const entry = screen.getByTestId("template-run-history-run_plan_1").textContent ?? "";
    expect(entry).toContain("plan");
    expect(entry).toContain("completed");
    expect(entry).toContain("vishu");
    expect(entry).toContain("2026-07-20T00:00:00Z");
  });

  it("shows an empty state rather than a bare heading when no run has started", () => {
    const queryClient = testQueryClient();
    seedRuns(queryClient, []);

    renderHistory(queryClient);

    expect(screen.getByTestId("template-run-history-empty")).toBeTruthy();
  });
});
