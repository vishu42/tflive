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
import TemplateDestroyPanel from "./TemplateDestroyPanel";

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

function renderPanel(queryClient: QueryClient, overrides: Partial<StackTemplate> = {}) {
  return render(
    <QueryClientProvider client={queryClient}>
      <AuthContext.Provider value={authValue()}>
        <MemoryRouter initialEntries={["/stacks/stack_1/template"]}>
          <TemplateDestroyPanel stackId="stack_1" stackTemplate={stackTemplate(overrides)} />
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

describe("TemplateDestroyPanel", () => {
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it("enables destroy for an active template and asks for confirmation on click", () => {
    const queryClient = testQueryClient();
    seedCapabilities(queryClient, allAllowed);
    seedRuns(queryClient, []);

    renderPanel(queryClient);

    const destroyButton = screen.getByRole("button", { name: /Destroy/ }) as HTMLButtonElement;
    expect(destroyButton.disabled).toBe(false);
    fireEvent.click(destroyButton);
    expect(screen.getByText(/This will permanently destroy all infrastructure/)).toBeTruthy();
    expect(screen.getByRole("button", { name: /Confirm destroy/ })).toBeTruthy();
  });

  it("keeps destroy disabled until run history has loaded", () => {
    vi.spyOn(globalThis, "fetch").mockReturnValue(new Promise(() => {}));
    const queryClient = testQueryClient();
    seedCapabilities(queryClient, allAllowed);

    renderPanel(queryClient);

    expect(isDisabled(screen.getByRole("button", { name: /Destroy/ }))).toBe(true);
  });

  it("disables confirmation if a run appears after destroy was requested", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(jsonResponse(run({ id: "unexpected" }), 201));
    const queryClient = testQueryClient();
    seedCapabilities(queryClient, allAllowed);
    seedRuns(queryClient, []);

    renderPanel(queryClient);

    fireEvent.click(screen.getByRole("button", { name: /Destroy/ }));
    queryClient.setQueryData(queryKeys.templateRuns("tenant_123", "stpl_1"), [run({ status: "queued" })]);

    await waitFor(() => expect(isDisabled(screen.getByRole("button", { name: /Confirm destroy/ }))).toBe(true));
    fireEvent.click(screen.getByRole("button", { name: /Confirm destroy/ }));
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("disables destroy when lifecycle is destroying", () => {
    const queryClient = testQueryClient();
    seedCapabilities(queryClient, allAllowed);
    seedRuns(queryClient, []);

    renderPanel(queryClient, { lifecycle: "destroying" });

    expect(isDisabled(screen.getByRole("button", { name: /Destroy/ }))).toBe(true);
  });

  it("disables destroy when an active run exists", () => {
    const queryClient = testQueryClient();
    seedCapabilities(queryClient, allAllowed);
    seedRuns(queryClient, [run({ operation: "plan", status: "queued" })]);

    renderPanel(queryClient);

    expect(isDisabled(screen.getByRole("button", { name: /Destroy/ }))).toBe(true);
  });

  it("disables destroy with a reason when canOperate is denied", () => {
    const queryClient = testQueryClient();
    seedCapabilities(queryClient, { ...allAllowed, canOperate: false });
    seedRuns(queryClient, []);

    renderPanel(queryClient);

    expect(isDisabled(screen.getByRole("button", { name: /Destroy/ }))).toBe(true);
    expect(screen.getByTestId("template-destroy-disabled-reason")).toBeTruthy();
  });

  it("starts a destroy run on confirm", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(
      jsonResponse(run({ id: "destroy_1", operation: "destroy", status: "completed" }), 201)
    );
    const queryClient = testQueryClient();
    seedCapabilities(queryClient, allAllowed);
    seedRuns(queryClient, []);

    renderPanel(queryClient);

    fireEvent.click(screen.getByRole("button", { name: /Destroy/ }));
    fireEvent.click(screen.getByRole("button", { name: /Confirm destroy/ }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        "/v1/tenants/tenant_123/stack-templates/stpl_1/runs",
        expect.objectContaining({ method: "POST", body: JSON.stringify({ operation: "destroy" }) })
      )
    );
  });

  it("reports a failed destroy in place instead of leaving the panel silent", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(jsonResponse({ message: "destroy rejected" }, 409));
    const queryClient = testQueryClient();
    seedCapabilities(queryClient, allAllowed);
    seedRuns(queryClient, []);

    renderPanel(queryClient);

    fireEvent.click(screen.getByRole("button", { name: /Destroy/ }));
    fireEvent.click(screen.getByRole("button", { name: /Confirm destroy/ }));

    await waitFor(() => expect(screen.getByTestId("template-destroy-error")).toBeTruthy());
  });
});
