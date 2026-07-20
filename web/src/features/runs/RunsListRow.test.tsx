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

    renderRow(queryClient);

    expect(screen.getByText("acme-prod-primary @ main (active)")).toBeTruthy();
    expect(isDisabled(screen.getByRole("button", { name: /Plan/ }))).toBe(false);
    expect(isDisabled(screen.getByRole("button", { name: /Apply/ }))).toBe(true);
    expect(isDisabled(screen.getByRole("button", { name: /Approve/ }))).toBe(true);
    expect(isDisabled(screen.getByRole("button", { name: /Cancel/ }))).toBe(true);
  });

  it("disables Plan/Apply/Cancel with a reason when canOperate is denied", () => {
    const queryClient = testQueryClient();
    seedCapabilities(queryClient, { ...allAllowed, canOperate: false });

    renderRow(queryClient);

    expect(isDisabled(screen.getByRole("button", { name: /Plan/ }))).toBe(true);
    expect(isDisabled(screen.getByRole("button", { name: /Apply/ }))).toBe(true);
    expect(isDisabled(screen.getByRole("button", { name: /Cancel/ }))).toBe(true);
    expect(screen.getByTestId("runs-row-actions-disabled-reason")).toBeTruthy();
  });

  it("disables Approve with a reason when canApprove is denied", () => {
    const queryClient = testQueryClient();
    seedCapabilities(queryClient, { ...allAllowed, canApprove: false });

    renderRow(queryClient);

    expect(isDisabled(screen.getByRole("button", { name: /Approve/ }))).toBe(true);
    expect(screen.getByTestId("runs-row-approve-disabled-reason")).toBeTruthy();
  });

  it("walks plan → apply → approve, enabling each step's actions and linking to the run detail route", async () => {
    const queryClient = testQueryClient();
    seedCapabilities(queryClient, allAllowed);

    const fetchMock = vi.spyOn(globalThis, "fetch").mockImplementation(async (input, init) => {
      const url = String(input);
      if (init?.method === "POST" && url.endsWith("/stack-templates/stpl_1/runs")) {
        // Return each run already resolved (rather than "queued" + relying on
        // interval polling to reach a terminal state) so the test verifies
        // the canApply/canApprove derivation, not TanStack Query's own
        // already-tested refetchInterval mechanics.
        const body = JSON.parse(String(init.body)) as { operation: string };
        return body.operation === "apply"
          ? jsonResponse(run({ id: "run_apply_1", operation: "apply", status: "waiting_approval" }))
          : jsonResponse(run({ id: "run_plan_1", operation: "plan", status: "completed" }));
      }
      if (url.endsWith("/template-runs/run_plan_1")) {
        return jsonResponse(run({ id: "run_plan_1", operation: "plan", status: "completed" }));
      }
      if (url.endsWith("/template-runs/run_apply_1")) {
        return jsonResponse(run({ id: "run_apply_1", operation: "apply", status: "waiting_approval" }));
      }
      if (init?.method === "POST" && url.endsWith("/template-runs/run_apply_1/approval")) {
        return new Response(null, { status: 204 });
      }
      throw new Error(`unexpected fetch: ${url} ${init?.method ?? "GET"}`);
    });

    renderRow(queryClient);

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
  });
});
