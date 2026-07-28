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
import RunsListRow from "./RunsListRow";

function stackTemplate(overrides: Partial<StackTemplate> = {}): StackTemplate {
  return {
    id: "stpl_1",
    stack_id: "stack_1",
    component_key: "primary",
    source_template_id: "tpl_1",
    desired_template_revision_id: "rev_1",
    last_applied_template_revision_id: "",
    selected_ref: "main",
    workspace_name: "acme-prod-primary",
    display_name: "",
    config: {},
    last_applied_run_id: "",
    last_applied_ref: "",
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

function renderRow(queryClient: QueryClient, overrides: Partial<StackTemplate> = {}) {
  return render(
    <QueryClientProvider client={queryClient}>
      <AuthContext.Provider value={authValue()}>
        <MemoryRouter initialEntries={["/stacks/stack_1/runs"]}>
          <RunsListRow stackId="stack_1" stackTemplate={stackTemplate(overrides)} />
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

describe("RunsListRow", () => {
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it("shows the component label with Plan enabled and Apply/Approve disabled when no run has started", () => {
    const queryClient = testQueryClient();
    seedCapabilities(queryClient, allAllowed);
    seedRuns(queryClient, []);

    renderRow(queryClient);

    expect(screen.getByText("acme-prod-primary @ main (active)")).toBeTruthy();
    expect(isDisabled(screen.getByRole("button", { name: /Plan/ }))).toBe(false);
    expect(isDisabled(screen.getByRole("button", { name: /Apply/ }))).toBe(true);
    expect(isDisabled(screen.getByRole("button", { name: /Approve/ }))).toBe(true);
    expect(isDisabled(screen.getByRole("button", { name: /Cancel/ }))).toBe(true);
  });

  it("treats a failed run as terminal and does not render a Terminate action", () => {
    const queryClient = testQueryClient();
    seedCapabilities(queryClient, allAllowed);
    seedRuns(queryClient, [run({ status: "failed", error_summary: "activity failed" })]);

    renderRow(queryClient);

    expect(isDisabled(screen.getByRole("button", { name: /Plan/ }))).toBe(false);
    expect(isDisabled(screen.getByRole("button", { name: /Cancel/ }))).toBe(true);
    expect(screen.queryByRole("button", { name: /Terminate/ })).toBeNull();
  });

  it("disables Plan/Apply/Cancel with a reason when canOperate is denied", () => {
    const queryClient = testQueryClient();
    seedCapabilities(queryClient, { ...allAllowed, canOperate: false });
    seedRuns(queryClient, []);

    renderRow(queryClient);

    expect(isDisabled(screen.getByRole("button", { name: /Plan/ }))).toBe(true);
    expect(isDisabled(screen.getByRole("button", { name: /Apply/ }))).toBe(true);
    expect(isDisabled(screen.getByRole("button", { name: /Cancel/ }))).toBe(true);
    expect(screen.getByTestId("runs-row-actions-disabled-reason")).toBeTruthy();
  });

  it("disables Approve with a reason when canApprove is denied", () => {
    const queryClient = testQueryClient();
    seedCapabilities(queryClient, { ...allAllowed, canApprove: false });
    seedRuns(queryClient, []);

    renderRow(queryClient);

    expect(isDisabled(screen.getByRole("button", { name: /Approve/ }))).toBe(true);
    expect(screen.getByTestId("runs-row-approve-disabled-reason")).toBeTruthy();
  });

  it("shows a run history entry and an enabled Approve button for a run started by a different user", () => {
    const queryClient = testQueryClient();
    seedCapabilities(queryClient, allAllowed);
    seedRuns(queryClient, [
      run({ id: "run_apply_1", operation: "apply", status: "waiting_approval", trigger_actor: "someone_else", started_at: "2026-07-20T01:00:00Z" }),
      run({ id: "run_plan_1", operation: "plan", status: "completed", trigger_actor: "someone_else", started_at: "2026-07-20T00:00:00Z" })
    ]);

    renderRow(queryClient);

    expect(isDisabled(screen.getByRole("button", { name: /Approve/ }))).toBe(false);
    expect(screen.getByTestId("runs-row-stpl_1-history-run_apply_1")).toBeTruthy();
    expect(screen.getByTestId("runs-row-stpl_1-history-run_plan_1")).toBeTruthy();
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

    renderRow(queryClient);
    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith("/v1/tenants/tenant_123/stack-templates/stpl_1/runs", expect.objectContaining({ method: "GET" }))
    );

    fireEvent.click(screen.getByRole("button", { name: /Plan/ }));
    await waitFor(() => expect(screen.getByTestId("runs-row-stpl_1-plan-link")).toBeTruthy());
    expect(screen.getByTestId("runs-row-stpl_1-plan-link").getAttribute("href")).toBe("/stacks/stack_1/runs/run_plan_1");
    await waitFor(() => expect(isDisabled(screen.getByRole("button", { name: /Apply/ }))).toBe(false));

    fireEvent.click(screen.getByRole("button", { name: /Apply/ }));
    await waitFor(() => expect(screen.getByTestId("runs-row-stpl_1-apply-link")).toBeTruthy());
    expect(screen.getByTestId("runs-row-stpl_1-apply-link").getAttribute("href")).toBe("/stacks/stack_1/runs/run_apply_1");
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

  it("shows destroy button for active templates and confirmation on click", () => {
    const queryClient = testQueryClient();
    seedCapabilities(queryClient, allAllowed);
    seedRuns(queryClient, []);

    renderRow(queryClient);

    const destroyButton = screen.getByRole("button", { name: /Destroy/ }) as HTMLButtonElement;
    expect(destroyButton.disabled).toBe(false);
    fireEvent.click(destroyButton);
    expect(screen.getByText(/This will permanently destroy all infrastructure/)).toBeTruthy();
    expect(screen.getByRole("button", { name: /Confirm destroy/ })).toBeTruthy();
  });

  it("disables destroy button when lifecycle is destroying", () => {
    const queryClient = testQueryClient();
    seedCapabilities(queryClient, allAllowed);
    seedRuns(queryClient, []);

    renderRow(queryClient, { lifecycle: "destroying" });

    expect(isDisabled(screen.getByRole("button", { name: /Destroy/ }))).toBe(true);
  });

  it("disables destroy button when an active run exists", () => {
    const queryClient = testQueryClient();
    seedCapabilities(queryClient, allAllowed);
    seedRuns(queryClient, [run({ operation: "plan", status: "queued" })]);

    renderRow(queryClient);

    expect(isDisabled(screen.getByRole("button", { name: /Destroy/ }))).toBe(true);
  });

  it("disables destroy with a reason when canOperate is denied", () => {
    const queryClient = testQueryClient();
    seedCapabilities(queryClient, { ...allAllowed, canOperate: false });
    seedRuns(queryClient, []);

    renderRow(queryClient);

    expect(isDisabled(screen.getByRole("button", { name: /Destroy/ }))).toBe(true);
    expect(screen.getByTestId("runs-row-actions-disabled-reason")).toBeTruthy();
  });

  it("executes destroy mutation on confirm", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(
      jsonResponse(run({ id: "destroy_1", operation: "destroy", status: "completed" }), 201)
    );
    const queryClient = testQueryClient();
    seedCapabilities(queryClient, allAllowed);
    seedRuns(queryClient, []);

    renderRow(queryClient);

    fireEvent.click(screen.getByRole("button", { name: /Destroy/ }));
    fireEvent.click(screen.getByRole("button", { name: /Confirm destroy/ }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        "/v1/tenants/tenant_123/stack-templates/stpl_1/runs",
        expect.objectContaining({ method: "POST", body: JSON.stringify({ operation: "destroy" }) })
      )
    );
  });
});
