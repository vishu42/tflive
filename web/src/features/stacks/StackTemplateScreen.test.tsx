// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AuthContext } from "../../auth/AuthContext";
import type { AuthContextValue } from "../../auth/AuthContext";
import StackTemplateScreen from "./StackTemplateScreen";
import { queryKeys } from "../../api/queryKeys";
import type { StackTemplate, StackView, TemplateRevision, TemplateVariable } from "../../api/types";
import type { StackCapabilities } from "../../auth/types";

const allAllowed: StackCapabilities = { canView: true, canOperate: true, canApprove: true, canManageAccess: true };

function stackView(capabilities: StackCapabilities, templates: StackTemplate[]): StackView {
  return {
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
    templates
  };
}

function stackTemplate(overrides: Partial<StackTemplate> = {}): StackTemplate {
  return {
    id: "st_1",
    stack_id: "stack_1",
    component_key: "vpc",
    source_template_id: "tmpl_src_1",
    desired_template_revision_id: "rev_1",
    last_applied_template_revision_id: "rev_0",
    selected_ref: "main",
    workspace_name: "ws-payments",
    display_name: "",
    config: { region: "us-east-1" },
    last_applied_run_id: "run_9",
    last_applied_ref: "main",
    created_by: "user_123",
    lifecycle: "active",
    ...overrides
  };
}

function templateRevision(overrides: Partial<TemplateRevision> = {}): TemplateRevision {
  return {
    id: "rev_1",
    tenant_id: "tenant_123",
    source_template_id: "tmpl_src_1",
    repo_owner: "hashicorp",
    repo_name: "vpc",
    source_ref: "main",
    resolved_commit_sha: "abcdef1234567890",
    root_path: ".",
    name: "vpc",
    description: "",
    tags: [],
    status: "active",
    created_at: "2026-07-19T00:00:00Z",
    ...overrides
  };
}

function variable(overrides: Partial<TemplateVariable> = {}): TemplateVariable {
  return {
    template_revision_id: "rev_1",
    name: "region",
    type_expression: "string",
    description: "",
    required: true,
    has_default: false,
    sensitive: false,
    has_validation: false,
    ...overrides
  };
}

// staleTime: Infinity keeps seeded cache data from triggering a background
// refetch on mount — see the identical rationale in StacksListScreen.test.tsx.
function testQueryClient(): QueryClient {
  return new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
}

function seedDefaultData(queryClient: QueryClient, capabilities: StackCapabilities = allAllowed) {
  queryClient.setQueryData(queryKeys.stack("tenant_123", "stack_1"), stackView(capabilities, [stackTemplate()]));
  queryClient.setQueryData(queryKeys.templateRevisions("tenant_123"), [
    templateRevision(),
    templateRevision({ id: "rev_2", source_ref: "v2", resolved_commit_sha: "1234567abcdef" })
  ]);
  queryClient.setQueryData(queryKeys.templateRevisionVariables("tenant_123", "rev_1"), [variable()]);
  queryClient.setQueryData(queryKeys.templateRevisionVariables("tenant_123", "rev_2"), [variable()]);
}

function authValue(overrides: Partial<AuthContextValue> = {}): AuthContextValue {
  return {
    me: { sub: "user_1", tenantID: "tenant_123", displayName: "Test User", globalCapabilities: { isPlatformAdmin: false, canCreateStack: false } },
    status: "authenticated",
    login: () => {},
    logout: () => {},
    ...overrides,
  };
}

function renderScreen(queryClient: QueryClient, auth?: AuthContextValue, initialEntry = "/stacks/stack_1/template") {
  return render(
    <QueryClientProvider client={queryClient}>
      <AuthContext.Provider value={auth ?? authValue()}>
        <MemoryRouter initialEntries={[initialEntry]}>
          <Routes>
            <Route path="/stacks/:stackId/template" element={<StackTemplateScreen />} />
          </Routes>
        </MemoryRouter>
      </AuthContext.Provider>
    </QueryClientProvider>
  );
}

function actionButton(name: RegExp): HTMLButtonElement {
  return screen.getByRole("button", { name }) as HTMLButtonElement;
}

describe("StackTemplateScreen", () => {
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
    vi.unstubAllEnvs();
  });

  it("shows a loading state while the installed template's variables are pending", () => {
    vi.spyOn(globalThis, "fetch").mockReturnValue(new Promise(() => {}));
    const queryClient = testQueryClient();
    queryClient.setQueryData(queryKeys.stack("tenant_123", "stack_1"), stackView(allAllowed, [stackTemplate()]));

    renderScreen(queryClient);

    expect(screen.getByTestId("stack-template-loading")).toBeTruthy();
  });

  it("renders variables prefilled from the installed template's config", () => {
    const queryClient = testQueryClient();
    seedDefaultData(queryClient);

    renderScreen(queryClient);

    const input = screen.getByLabelText(/region/) as HTMLInputElement;
    expect(input.value).toBe("us-east-1");
  });

  it("shows no raw identifiers anywhere in the configuration column", () => {
    const queryClient = testQueryClient();
    seedDefaultData(queryClient);

    renderScreen(queryClient);

    const config = screen.getByTestId("stack-template-config");
    expect(config.textContent).not.toContain("tmpl_src_1");
    expect(config.textContent).not.toContain("rev_1");
    expect(config.textContent).not.toContain("rev_0");
    expect(config.textContent).not.toContain("run_9");
    expect(screen.queryByText("Installed template")).toBeNull();
  });

  it("no longer offers installing or selecting a revision from this screen", () => {
    const queryClient = testQueryClient();
    seedDefaultData(queryClient);

    renderScreen(queryClient);

    expect(screen.queryByTestId("stack-template-revision-select")).toBeNull();
    expect(screen.queryByRole("button", { name: /Install template/ })).toBeNull();
  });

  it("links to the add template screen", () => {
    const queryClient = testQueryClient();
    seedDefaultData(queryClient);

    renderScreen(queryClient);

    expect(screen.getByTestId("add-stack-template-link").getAttribute("href")).toBe("/stacks/stack_1/template/new");
  });

  it("keeps a visible gap between the template list header and its choices", () => {
    const queryClient = testQueryClient();
    seedDefaultData(queryClient);

    renderScreen(queryClient);

    const listContent = screen.getByTestId("stack-template-list-content");
    const header = screen.getByTestId("stack-template-panel-header");
    const items = screen.getByTestId("stack-template-items");

    expect(listContent.contains(header)).toBe(true);
    expect(listContent.contains(items)).toBe(true);
    expect(listContent.className).toContain("stack-template-list-content");
  });

  it("offers a revision-selection link for an installed template", () => {
    const queryClient = testQueryClient();
    seedDefaultData(queryClient);

    renderScreen(queryClient);

    expect(screen.getByTestId("stack-template-revision-action")).toBeTruthy();
    expect(screen.getByTestId("change-stack-template-revision-link").getAttribute("href")).toBe(
      "/stacks/stack_1/template/st_1/upgrade"
    );
  });

  it("keeps the revision action visually secondary to the configuration panel", () => {
    const queryClient = testQueryClient();
    seedDefaultData(queryClient);

    renderScreen(queryClient);

    const revisionAction = screen.getByTestId("stack-template-revision-action");
    expect(revisionAction.className).toContain("stack-template-revision-action");
    expect(revisionAction.className).not.toContain("panel");
  });

  it("keeps revision selection available when the only revision is installed", () => {
    const queryClient = testQueryClient();
    seedDefaultData(queryClient);
    queryClient.setQueryData(queryKeys.templateRevisions("tenant_123"), [templateRevision()]);

    renderScreen(queryClient);

    expect(screen.getByTestId("stack-template-revision-action")).toBeTruthy();
    expect(screen.getByTestId("change-stack-template-revision-link")).toBeTruthy();
  });

  it("does not wait for the tenant revision list before showing revision selection", () => {
    const queryClient = testQueryClient();
    seedDefaultData(queryClient);
    queryClient.removeQueries({ queryKey: queryKeys.templateRevisions("tenant_123") });

    renderScreen(queryClient);

    expect(screen.getByTestId("stack-template-revision-action")).toBeTruthy();
    expect(screen.getByTestId("change-stack-template-revision-link")).toBeTruthy();
  });

  it("renders the template selected by the URL rather than the first installed one", () => {
    const queryClient = testQueryClient();
    seedDefaultData(queryClient);
    queryClient.setQueryData(
      queryKeys.stack("tenant_123", "stack_1"),
      stackView(allAllowed, [
        stackTemplate(),
        stackTemplate({ id: "st_2", desired_template_revision_id: "rev_2", config: { region: "ap-south-1" } })
      ])
    );

    renderScreen(queryClient, undefined, "/stacks/stack_1/template?selected=st_2");

    expect((screen.getByLabelText(/region/) as HTMLInputElement).value).toBe("ap-south-1");
  });

  it("keeps Stack credentials out of the Template screen and titles the panel by scope", () => {
    const queryClient = testQueryClient();
    seedDefaultData(queryClient);
    queryClient.setQueryData(queryKeys.stackCredentials("tenant_123", "stack_1"), [
      { id: "stack_credential", name: "STACK_ONLY", scope: "stack", created_at: "2026-07-19T00:00:00Z" }
    ]);
    queryClient.setQueryData(queryKeys.stackTemplateCredentials("tenant_123", "st_1"), [
      { id: "template_credential", name: "TEMPLATE_ONLY", scope: "stack_template", created_at: "2026-07-19T00:00:00Z" }
    ]);

    renderScreen(queryClient);

    expect(screen.getByText("TEMPLATE_ONLY")).toBeTruthy();
    expect(screen.queryByText("STACK_ONLY")).toBeNull();
    expect(screen.getByText("Template credentials")).toBeTruthy();
    expect(screen.getByText("Overrides the stack environment for this template only.")).toBeTruthy();
  });

  it("keeps the revision action, variables, and credentials panels in the right column", () => {
    const queryClient = testQueryClient();
    seedDefaultData(queryClient);

    renderScreen(queryClient);

    const rightColumn = screen.getByTestId("stack-template-right-column");
    expect(rightColumn.contains(screen.getByTestId("stack-template-revision-action"))).toBe(true);
    expect(rightColumn.contains(screen.getByTestId("stack-template-config"))).toBe(true);
    expect(rightColumn.contains(screen.getByText("Template credentials"))).toBe(true);
  });

  it("disables save while values match the installed config and enables it after an edit", () => {
    const queryClient = testQueryClient();
    seedDefaultData(queryClient);

    renderScreen(queryClient);

    expect(actionButton(/Save config/).disabled).toBe(true);
    fireEvent.change(screen.getByLabelText(/region/), { target: { value: "eu-west-1" } });
    expect(actionButton(/Save config/).disabled).toBe(false);
  });

  it("locks configuration and hides operator links when canOperate is denied", async () => {
    const queryClient = testQueryClient();
    seedDefaultData(queryClient, { ...allAllowed, canOperate: false });

    renderScreen(queryClient);

    await waitFor(() => expect(screen.getByTestId("variables-disabled-reason")).toBeTruthy());
    expect(actionButton(/Save config/).disabled).toBe(true);
    expect((screen.getByLabelText(/region/) as HTMLInputElement).disabled).toBe(true);
    expect(screen.queryByTestId("add-stack-template-link")).toBeNull();
    expect(screen.queryByTestId("change-stack-template-revision-link")).toBeNull();
  });

  it("prompts to add a template when the stack has none installed", () => {
    const queryClient = testQueryClient();
    queryClient.setQueryData(queryKeys.stack("tenant_123", "stack_1"), stackView(allAllowed, []));
    queryClient.setQueryData(queryKeys.templateRevisions("tenant_123"), [templateRevision()]);

    renderScreen(queryClient);

    expect(screen.getByTestId("stack-template-empty")).toBeTruthy();
    expect(screen.getByTestId("add-stack-template-link")).toBeTruthy();
    expect(screen.queryByTestId("stack-template-config")).toBeNull();
  });

  it("does not need the revisions query to render the installed template", async () => {
    const queryClient = testQueryClient();
    seedDefaultData(queryClient);
    queryClient.removeQueries({ queryKey: queryKeys.templateRevisions("tenant_123") });

    renderScreen(queryClient);

    await waitFor(() => expect(screen.getByTestId("stack-template-config")).toBeTruthy());
    expect(screen.getByTestId("change-stack-template-revision-link")).toBeTruthy();
  });

  it("shows Destroying badge when lifecycle is destroying", () => {
    const queryClient = testQueryClient();
    seedDefaultData(queryClient, allAllowed);
    queryClient.setQueryData(queryKeys.stack("tenant_123", "stack_1"),
      stackView(allAllowed, [stackTemplate({ lifecycle: "destroying" })]));

    renderScreen(queryClient);

    expect(screen.getByText("Destroying…")).toBeTruthy();
  });

  it("disables revision selection while the installed template is destroying", () => {
    const queryClient = testQueryClient();
    seedDefaultData(queryClient, allAllowed);
    queryClient.setQueryData(queryKeys.stack("tenant_123", "stack_1"),
      stackView(allAllowed, [stackTemplate({ lifecycle: "destroying" })]));

    renderScreen(queryClient);

    expect(screen.getByTestId("stack-template-revision-action")).toBeTruthy();
    const changeRevisionControl = screen.getByTestId("change-stack-template-revision-link") as HTMLButtonElement;
    expect(changeRevisionControl.tagName).toBe("BUTTON");
    expect(changeRevisionControl.disabled).toBe(true);
    expect(screen.getByTestId("upgrade-disabled-reason").textContent).toBe("Destroy in progress");
  });

  it("renders the shared boundary screen for a handled API error status", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({ error: "unavailable", message: "service unavailable" }), {
        status: 503,
        headers: { "content-type": "application/json" }
      })
    );
    const queryClient = testQueryClient();
    queryClient.setQueryData(queryKeys.stack("tenant_123", "stack_1"), stackView(allAllowed, [stackTemplate()]));

    renderScreen(queryClient);

    await waitFor(() => expect(screen.getByTestId("route-service-unavailable")).toBeTruthy());
  });

  it("renders the generic error state when the API returns 401", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({ error: "unauthorized", message: "unauthorized" }), {
        status: 401,
        headers: { "content-type": "application/json" }
      })
    );
    const queryClient = testQueryClient();
    queryClient.setQueryData(queryKeys.stack("tenant_123", "stack_1"), stackView(allAllowed, [stackTemplate()]));

    renderScreen(queryClient);

    await waitFor(() => expect(screen.getByTestId("stack-template-error")).toBeTruthy());
  });

  it("renders AccessDenied when the API returns 403", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({ error: "forbidden", message: "forbidden" }), {
        status: 403,
        headers: { "content-type": "application/json" }
      })
    );
    const queryClient = testQueryClient();
    queryClient.setQueryData(queryKeys.stack("tenant_123", "stack_1"), stackView(allAllowed, [stackTemplate()]));

    renderScreen(queryClient);

    await waitFor(() => expect(screen.getByTestId("route-access-denied")).toBeTruthy());
  });

  it("renders NotFound when the API returns 404", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({ error: "not_found", message: "not found" }), {
        status: 404,
        headers: { "content-type": "application/json" }
      })
    );
    const queryClient = testQueryClient();
    queryClient.setQueryData(queryKeys.stack("tenant_123", "stack_1"), stackView(allAllowed, [stackTemplate()]));

    renderScreen(queryClient);

    await waitFor(() => expect(screen.getByTestId("route-not-found")).toBeTruthy());
  });
});
