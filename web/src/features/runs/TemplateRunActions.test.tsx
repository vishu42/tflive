// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AuthContext } from "../../auth/AuthContext";
import type { AuthContextValue } from "../../auth/AuthContext";
import type { StackCapabilities } from "../../auth/types";
import { queryKeys } from "../../api/queryKeys";
import type { StackTemplate, TemplateRun } from "../../api/types";
import TemplateRunActions from "./TemplateRunActions";

function stackTemplate(overrides: Partial<StackTemplate> = {}): StackTemplate {
  return {
    id: "stpl_1",
    stack_id: "stack_1",
    component_key: "primary",
    source_template_id: "tpl_1",
    desired_template_revision_id: "rev_1",
    last_applied_template_revision_id: "",
    source_ref: "main",
    workspace_name: "acme-prod-primary",
    display_name: "",
    config: {},
    last_applied_run_id: "",
    last_planned_run_id: "",
    plan_state: "none",
    live_state: "never",
    created_by: "user_123",
    lifecycle: "active",
    ...overrides
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
    status: "queued",
    trigger_actor: "user_123",
    started_at: "2026-07-20T00:00:00Z",
    error_summary: "",
    ...overrides
  };
}

const allAllowed: StackCapabilities = { canView: true, canOperate: true, canApprove: true, canManageAccess: true };

function testQueryClient(): QueryClient {
  return new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
}

function authValue(): AuthContextValue {
  return {
    me: { sub: "user_1", tenantID: "tenant_123", displayName: "Test User", globalCapabilities: { isPlatformAdmin: false, canCreateStack: false } },
    status: "authenticated",
    login: () => {},
    logout: () => {}
  };
}

function seedCapabilities(queryClient: QueryClient, capabilities: StackCapabilities) {
  queryClient.setQueryData(queryKeys.stack("tenant_123", "stack_1"), {
    stack: {
      id: "stack_1",
      tenant_id: "tenant_123",
      name: "Payments",
      slug: "payments",
      tags: {},
      default_credential_ids: [],
      created_by: "user_123",
      created_at: "2026-07-19T00:00:00Z",
      effectiveCapabilities: capabilities
    },
    templates: []
  });
}

function seedRuns(queryClient: QueryClient, runs: TemplateRun[]) {
  queryClient.setQueryData(queryKeys.templateRuns("tenant_123", "stpl_1"), runs);
}

function actionsElement(queryClient: QueryClient, overrides: Partial<StackTemplate> = {}) {
  return (
    <QueryClientProvider client={queryClient}>
      <AuthContext.Provider value={authValue()}>
        <MemoryRouter initialEntries={["/stacks/stack_1/template"]}>
          <TemplateRunActions stackId="stack_1" stackTemplate={stackTemplate(overrides)} />
        </MemoryRouter>
      </AuthContext.Provider>
    </QueryClientProvider>
  );
}

function renderActions(queryClient: QueryClient, overrides: Partial<StackTemplate> = {}) {
  return render(actionsElement(queryClient, overrides));
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

function isDisabled(element: HTMLElement): boolean {
  return (element as HTMLButtonElement).disabled;
}

describe("TemplateRunActions", () => {
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it("enables Plan and disables Apply/Approve/Cancel when no run has started", () => {
    const queryClient = testQueryClient();
    seedCapabilities(queryClient, allAllowed);
    seedRuns(queryClient, []);

    renderActions(queryClient);

    expect(isDisabled(screen.getByRole("button", { name: /Plan/ }))).toBe(false);
    expect(isDisabled(screen.getByRole("button", { name: /Apply/ }))).toBe(true);
    expect(isDisabled(screen.getByRole("button", { name: /Approve/ }))).toBe(true);
    expect(isDisabled(screen.getByRole("button", { name: /Cancel/ }))).toBe(true);
  });

  it("keeps run operations disabled until run history has loaded", () => {
    vi.spyOn(globalThis, "fetch").mockReturnValue(new Promise(() => {}));
    const queryClient = testQueryClient();
    seedCapabilities(queryClient, allAllowed);

    renderActions(queryClient);

    expect(isDisabled(screen.getByRole("button", { name: /Plan/ }))).toBe(true);
    expect(isDisabled(screen.getByRole("button", { name: /Apply/ }))).toBe(true);
    expect(isDisabled(screen.getByRole("button", { name: /Cancel/ }))).toBe(true);
  });

  it("blocks new operations when an older run is still active", () => {
    const queryClient = testQueryClient();
    seedCapabilities(queryClient, allAllowed);
    seedRuns(queryClient, [
      run({ id: "newer_completed", operation: "plan", status: "completed", started_at: "2026-07-20T01:00:00Z" }),
      run({ id: "older_active", operation: "apply", status: "queued", started_at: "2026-07-20T00:00:00Z" })
    ]);

    renderActions(queryClient);

    expect(isDisabled(screen.getByRole("button", { name: /Plan/ }))).toBe(true);
    expect(isDisabled(screen.getByRole("button", { name: /Apply/ }))).toBe(true);
    expect(isDisabled(screen.getByRole("button", { name: /Cancel/ }))).toBe(false);
  });

  it("omits the Destroy action, which lives in the danger zone", () => {
    const queryClient = testQueryClient();
    seedCapabilities(queryClient, allAllowed);
    seedRuns(queryClient, []);

    renderActions(queryClient);

    expect(screen.queryByRole("button", { name: /Destroy/ })).toBeNull();
  });

  it("treats a failed run as terminal and does not render a Terminate action", () => {
    const queryClient = testQueryClient();
    seedCapabilities(queryClient, allAllowed);
    seedRuns(queryClient, [run({ status: "failed", error_summary: "activity failed" })]);

    renderActions(queryClient);

    expect(isDisabled(screen.getByRole("button", { name: /Plan/ }))).toBe(false);
    expect(isDisabled(screen.getByRole("button", { name: /Cancel/ }))).toBe(true);
    expect(screen.queryByRole("button", { name: /Terminate/ })).toBeNull();
  });

  it("disables Plan/Apply/Cancel with a reason when canOperate is denied", () => {
    const queryClient = testQueryClient();
    seedCapabilities(queryClient, { ...allAllowed, canOperate: false });
    seedRuns(queryClient, []);

    renderActions(queryClient);

    expect(isDisabled(screen.getByRole("button", { name: /Plan/ }))).toBe(true);
    expect(isDisabled(screen.getByRole("button", { name: /Apply/ }))).toBe(true);
    expect(isDisabled(screen.getByRole("button", { name: /Cancel/ }))).toBe(true);
    expect(screen.getByTestId("template-run-actions-disabled-reason")).toBeTruthy();
  });

  it("disables Approve with a reason when canApprove is denied", () => {
    const queryClient = testQueryClient();
    seedCapabilities(queryClient, { ...allAllowed, canApprove: false });
    seedRuns(queryClient, []);

    renderActions(queryClient);

    expect(isDisabled(screen.getByRole("button", { name: /Approve/ }))).toBe(true);
    expect(screen.getByTestId("template-run-approve-disabled-reason")).toBeTruthy();
  });

  it("enables Approve for a run awaiting approval that a different user started", () => {
    const queryClient = testQueryClient();
    seedCapabilities(queryClient, allAllowed);
    seedRuns(queryClient, [
      run({ id: "run_apply_1", operation: "apply", status: "waiting_approval", trigger_actor: "someone_else", started_at: "2026-07-20T01:00:00Z" }),
      run({ id: "run_plan_1", operation: "plan", status: "completed", trigger_actor: "someone_else", started_at: "2026-07-20T00:00:00Z" })
    ]);

    renderActions(queryClient);

    expect(isDisabled(screen.getByRole("button", { name: /Approve/ }))).toBe(false);
  });

  it("enables Apply when the server reports the plan still matches desired state", () => {
    const queryClient = testQueryClient();
    seedCapabilities(queryClient, allAllowed);
    seedRuns(queryClient, [run({ id: "run_plan_1", operation: "plan", status: "completed" })]);

    renderActions(queryClient, { last_planned_run_id: "run_plan_1", plan_state: "matches" });

    expect(isDisabled(screen.getByRole("button", { name: /Apply/ }))).toBe(false);
    expect(screen.queryByTestId("template-run-plan-stale")).toBeNull();
  });

  it("disables Apply and explains why when config changed after the plan completed", () => {
    const queryClient = testQueryClient();
    seedCapabilities(queryClient, allAllowed);
    // The completed plan is still the latest run — the old client-side check
    // would have enabled Apply here and run something nobody reviewed.
    seedRuns(queryClient, [run({ id: "run_plan_1", operation: "plan", status: "completed" })]);

    renderActions(queryClient, { last_planned_run_id: "run_plan_1", plan_state: "stale" });

    expect(isDisabled(screen.getByRole("button", { name: /Apply/ }))).toBe(true);
    expect(screen.getByTestId("template-run-plan-stale").textContent).toContain("re-plan before applying");
  });

  it("walks plan → apply → approve using persisted history, and immediately reflects each step without a page reload", async () => {
    const queryClient = testQueryClient();
    seedCapabilities(queryClient, allAllowed);
    let runsState: TemplateRun[] = [];

    const fetchMock = vi.spyOn(globalThis, "fetch").mockImplementation(async (input, init) => {
      const url = String(input);
      const method = init?.method ?? "GET";
      if (url.endsWith("/stack-templates/stpl_1/runs") && method === "GET") {
        return jsonResponse(runsState);
      }
      if (url.endsWith("/stack-templates/stpl_1/runs") && method === "POST") {
        const body = JSON.parse(String(init?.body)) as { operation: string };
        const created =
          body.operation === "apply"
            ? run({ id: "run_apply_1", operation: "apply", status: "waiting_approval", started_at: "2026-07-20T00:05:00Z" })
            : run({ id: "run_plan_1", operation: "plan", status: "completed", started_at: "2026-07-20T00:00:00Z" });
        runsState = [created, ...runsState];
        return jsonResponse(created, 201);
      }
      if (url.endsWith("/template-runs/run_apply_1/approval") && method === "POST") {
        runsState = runsState.map((existing) => (existing.id === "run_apply_1" ? { ...existing, status: "approved" } : existing));
        return new Response(null, { status: 204 });
      }
      throw new Error(`unexpected fetch: ${url} ${method}`);
    });

    const view = renderActions(queryClient);
    // Wait for the run history to actually load (Plan enabled), not just for
    // the request to have been issued: the request settling and the button's
    // disabled state flipping are separate ticks, and asserting only the
    // former raced ahead of the latter and clicked a still-disabled button.
    await waitFor(() => expect(isDisabled(screen.getByRole("button", { name: /Plan/ }))).toBe(false));

    fireEvent.click(screen.getByRole("button", { name: /Plan/ }));
    await waitFor(() => expect(screen.getByTestId("template-run-plan-link")).toBeTruthy());
    expect(screen.getByTestId("template-run-plan-link").getAttribute("href")).toBe("/stacks/stack_1/runs/run_plan_1");

    // A completed plan run in the history is no longer enough on its own: Apply
    // waits for the server to report that the plan still matches desired state.
    expect(isDisabled(screen.getByRole("button", { name: /Apply/ }))).toBe(true);
    view.rerender(actionsElement(queryClient, { last_planned_run_id: "run_plan_1", plan_state: "matches" }));
    await waitFor(() => expect(isDisabled(screen.getByRole("button", { name: /Apply/ }))).toBe(false));

    fireEvent.click(screen.getByRole("button", { name: /Apply/ }));
    await waitFor(() => expect(screen.getByTestId("template-run-apply-link")).toBeTruthy());
    expect(screen.getByTestId("template-run-apply-link").getAttribute("href")).toBe("/stacks/stack_1/runs/run_apply_1");
    await waitFor(() => expect(isDisabled(screen.getByRole("button", { name: /Approve/ }))).toBe(false));
    expect(isDisabled(screen.getByRole("button", { name: /Cancel/ }))).toBe(false);

    fireEvent.click(screen.getByRole("button", { name: /Approve/ }));
    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        expect.stringContaining("/template-runs/run_apply_1/approval"),
        expect.objectContaining({ method: "POST" })
      )
    );
    await waitFor(() => expect(isDisabled(screen.getByRole("button", { name: /Approve/ }))).toBe(true));
  });
});
