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
import TemplateRunHistory from "./TemplateRunHistory";

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
    pending_plan_run_id: "",
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
    run_number: 1,
    auto_approve: false,
    plan_summary: null,
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
        <MemoryRouter initialEntries={["/stacks/stack_1/templates/stpl_1/runs"]}>
          {/* The Runs tab as TemplateRunsTab composes it: starting a run in
              the header, acting on one from its row below. */}
          <TemplateRunActions stackId="stack_1" stackTemplate={stackTemplate(overrides)} />
          <TemplateRunHistory stackId="stack_1" stackTemplateId="stpl_1" />
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

function button(name: RegExp): HTMLButtonElement | null {
  return screen.queryByRole("button", { name }) as HTMLButtonElement | null;
}

describe("TemplateRunActions", () => {
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it("enables Plan and Apply and offers no run actions when no run has started", () => {
    const queryClient = testQueryClient();
    seedCapabilities(queryClient, allAllowed);
    seedRuns(queryClient, []);

    renderActions(queryClient);

    expect(isDisabled(screen.getByRole("button", { name: /Plan/ }))).toBe(false);
    expect(isDisabled(screen.getByRole("button", { name: /^Apply$/ }))).toBe(false);
    expect(button(/Approve|Cancel|Discard/)).toBeNull();
  });

  it("keeps Plan disabled until run history has loaded", () => {
    vi.spyOn(globalThis, "fetch").mockReturnValue(new Promise(() => {}));
    const queryClient = testQueryClient();
    seedCapabilities(queryClient, allAllowed);

    renderActions(queryClient);

    expect(isDisabled(screen.getByRole("button", { name: /Plan/ }))).toBe(true);
  });

  // A run in flight cannot be stopped, so its row offers nothing; the header
  // waits for it.
  it("blocks a new plan while an older run is still active, and offers no action on that run's row", () => {
    const queryClient = testQueryClient();
    seedCapabilities(queryClient, allAllowed);
    seedRuns(queryClient, [
      run({ id: "newer_completed", operation: "plan", status: "completed", started_at: "2026-07-20T01:00:00Z" }),
      run({ id: "older_active", operation: "plan", status: "queued", started_at: "2026-07-20T00:00:00Z" })
    ]);

    renderActions(queryClient);

    expect(isDisabled(screen.getByRole("button", { name: /Plan/ }))).toBe(true);
    expect(screen.getByTestId("template-run-row-older_active").querySelector("button")).toBeNull();
    expect(screen.getByTestId("template-run-row-newer_completed").querySelector("button")).toBeNull();
  });

  it("omits the Destroy action, which lives on the Settings tab", () => {
    const queryClient = testQueryClient();
    seedCapabilities(queryClient, allAllowed);
    seedRuns(queryClient, []);

    renderActions(queryClient);

    expect(button(/Destroy/)).toBeNull();
  });

  it("treats a failed run as terminal: Plan is open again and the run takes no actions", () => {
    const queryClient = testQueryClient();
    seedCapabilities(queryClient, allAllowed);
    seedRuns(queryClient, [run({ status: "failed", error_summary: "activity failed" })]);

    renderActions(queryClient);

    expect(isDisabled(screen.getByRole("button", { name: /Plan/ }))).toBe(false);
    expect(button(/Cancel/)).toBeNull();
  });

  it("disables Plan with a reason, and hides Cancel, when canOperate is denied", () => {
    const queryClient = testQueryClient();
    seedCapabilities(queryClient, { ...allAllowed, canOperate: false });
    seedRuns(queryClient, [run({ id: "run_active", operation: "plan", status: "plan_started" })]);

    renderActions(queryClient);

    expect(isDisabled(screen.getByRole("button", { name: /Plan/ }))).toBe(true);
    expect(screen.getByTestId("template-run-actions-disabled-reason")).toBeTruthy();
    expect(button(/Cancel/)).toBeNull();
  });

  // Auto-approve is an approval given in advance, and so is Approve on a
  // waiting plan: neither is offered without approve access. Discarding the
  // plan is an operator action, so it stays, and so does starting an Apply,
  // which only saves a plan for someone else to approve.
  it("hides Approve and auto-approve when canApprove is denied", () => {
    const queryClient = testQueryClient();
    seedCapabilities(queryClient, { ...allAllowed, canApprove: false });
    seedRuns(queryClient, [run({ id: "run_waiting", operation: "apply", status: "waiting_approval" })]);

    renderActions(queryClient);

    expect(button(/^Approve$/)).toBeNull();
    expect(screen.queryByTestId("template-run-auto-approve")).toBeNull();
    expect(button(/Discard/)).toBeTruthy();
    expect(button(/^Apply$/)).toBeTruthy();
  });

  it("offers Approve and Discard on the row of a plan waiting for approval that a different user started", () => {
    const queryClient = testQueryClient();
    seedCapabilities(queryClient, allAllowed);
    seedRuns(queryClient, [
      run({ id: "run_waiting", run_number: 2, operation: "apply", status: "waiting_approval", trigger_actor: "someone_else", plan_summary: { add: 1, change: 0, destroy: 0 } }),
      run({ id: "run_done", operation: "apply", status: "completed", trigger_actor: "someone_else" })
    ]);

    renderActions(queryClient);

    const row = screen.getByTestId("template-run-row-run_waiting");
    const labels = Array.from(row.querySelectorAll("button")).map((candidate) => candidate.textContent);
    expect(labels).toEqual(["Discard", "Approve"]);
    expect(screen.getByTestId("template-run-summary-run_waiting").textContent).toBe("+1 ~0 -0");
  });

  // Plan only plans, so it never carries auto-approve; Apply carries it when
  // the box is ticked, and then applies straight away.
  it("starts a plan, an apply, and an auto-approved apply", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockImplementation(async (_input, init) => {
      if (init?.method === "POST") {
        return jsonResponse(run({ id: "run_new", status: "completed" }), 201);
      }
      return jsonResponse([]);
    });
    const queryClient = testQueryClient();
    seedCapabilities(queryClient, allAllowed);
    seedRuns(queryClient, []);

    renderActions(queryClient);

    const posted = () => fetchMock.mock.calls.filter(([, init]) => init?.method === "POST").map(([, init]) => JSON.parse(String(init?.body)));
    fireEvent.click(screen.getByRole("button", { name: /Plan/ }));
    await waitFor(() => expect(posted()).toEqual([{ operation: "plan" }]));
    await waitFor(() => expect(isDisabled(screen.getByRole("button", { name: /^Apply$/ }))).toBe(false));

    fireEvent.click(screen.getByRole("button", { name: /^Apply$/ }));
    await waitFor(() => expect(posted()).toEqual([{ operation: "plan" }, { operation: "apply" }]));
    await waitFor(() => expect(isDisabled(screen.getByRole("button", { name: /Plan/ }))).toBe(false));

    fireEvent.click(screen.getByRole("checkbox", { name: /Auto Apply/ }));
    fireEvent.click(screen.getByRole("button", { name: /Plan/ }));
    await waitFor(() => expect(posted()).toHaveLength(3));
    await waitFor(() => expect(isDisabled(screen.getByRole("button", { name: /^Apply$/ }))).toBe(false));
    fireEvent.click(screen.getByRole("button", { name: /^Apply$/ }));
    await waitFor(() =>
      expect(posted()).toEqual([{ operation: "plan" }, { operation: "apply" }, { operation: "plan" }, { operation: "apply", auto_approve: true }])
    );
  });

  // The server owns the "one run at a time" rule now, and a 409 means this tab's
  // history is behind: another tab or another user started a run since the last
  // poll. Showing the message is not enough - the buttons would stay enabled and
  // invite the same refused click again.
  it("refetches run history when the server refuses a second run as already in flight", async () => {
    const queryClient = testQueryClient();
    seedCapabilities(queryClient, allAllowed);
    // What this tab believes: nothing running, so Plan is enabled.
    let runsState: TemplateRun[] = [];

    vi.spyOn(globalThis, "fetch").mockImplementation(async (input, init) => {
      const url = String(input);
      const method = init?.method ?? "GET";
      if (url.endsWith("/stack-templates/stpl_1/runs") && method === "GET") {
        return jsonResponse(runsState);
      }
      if (url.endsWith("/stack-templates/stpl_1/runs") && method === "POST") {
        // What the server knows: someone else's run is already going.
        runsState = [run({ id: "run_elsewhere", operation: "plan", status: "apply_started" })];
        return jsonResponse({ error: "run_in_flight", message: "create template run: a run is already in flight for this stack template" }, 409);
      }
      throw new Error(`unexpected fetch: ${url} ${method}`);
    });

    renderActions(queryClient);
    await waitFor(() => expect(isDisabled(screen.getByRole("button", { name: /Plan/ }))).toBe(false));

    fireEvent.click(screen.getByRole("button", { name: /Plan/ }));

    await waitFor(() => expect(screen.getByText(/already in flight/)).toBeTruthy());
    // The refetch is what puts the buttons back in the state the server already
    // believes they are in: Plan refused, and the run that won listed.
    await waitFor(() => expect(isDisabled(screen.getByRole("button", { name: /Plan/ }))).toBe(true));
    expect(screen.getByTestId("template-run-row-run_elsewhere")).toBeTruthy();
  });

  it("walks apply → approve from persisted history, and reflects each step without a page reload", async () => {
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
        // The apply's plan runs and waits for approval with what it would change.
        const created = run({ id: "run_plan_1", run_number: 1, operation: "apply", status: "waiting_approval", plan_summary: { add: 2, change: 0, destroy: 0 } });
        runsState = [created];
        return jsonResponse(created, 201);
      }
      if (url.endsWith("/template-runs/run_plan_1/approval") && method === "POST") {
        runsState = runsState.map((existing) => ({ ...existing, status: "approved" }));
        return new Response(null, { status: 204 });
      }
      if (url.endsWith("/stacks/stack_1")) {
        return jsonResponse(queryClient.getQueryData(queryKeys.stack("tenant_123", "stack_1")));
      }
      throw new Error(`unexpected fetch: ${url} ${method}`);
    });

    renderActions(queryClient);
    // Wait for the run history to actually load (Plan enabled), not just for
    // the request to have been issued: the request settling and the button's
    // disabled state flipping are separate ticks.
    await waitFor(() => expect(isDisabled(screen.getByRole("button", { name: /Plan/ }))).toBe(false));

    fireEvent.click(screen.getByRole("button", { name: /^Apply$/ }));
    await waitFor(() => expect(screen.getByTestId("template-run-history-run_plan_1")).toBeTruthy());
    expect(screen.getByTestId("template-run-history-run_plan_1").getAttribute("href")).toBe("/stacks/stack_1/templates/stpl_1/runs/1");
    await waitFor(() => expect(button(/^Approve$/)).toBeTruthy());

    fireEvent.click(screen.getByRole("button", { name: /^Approve$/ }));
    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        expect.stringContaining("/template-runs/run_plan_1/approval"),
        expect.objectContaining({ method: "POST" })
      )
    );
    // Approved, the run no longer waits: its row drops Approve and Discard,
    // and offers nothing while it applies.
    await waitFor(() => expect(button(/^Approve$/)).toBeNull());
    expect(button(/Discard/)).toBeNull();
  });
});
