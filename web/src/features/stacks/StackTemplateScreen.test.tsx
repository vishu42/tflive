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

function authValue(): AuthContextValue {
  return {
    me: { sub: "user_1", displayName: "Test User", globalCapabilities: { isPlatformAdmin: false, canCreateStack: false } },
    status: "authenticated",
    login: () => {},
    logout: () => {}
  };
}

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

function renderScreen(queryClient: QueryClient) {
  return render(
    <QueryClientProvider client={queryClient}>
      <AuthContext.Provider value={authValue()}>
        <MemoryRouter initialEntries={["/stacks/stack_1/template"]}>
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

  it("shows a loading state while the template revisions query is pending", () => {
    vi.spyOn(globalThis, "fetch").mockReturnValue(new Promise(() => {}));
    const queryClient = testQueryClient();
    queryClient.setQueryData(queryKeys.stack("tenant_123", "stack_1"), stackView(allAllowed, [stackTemplate()]));

    renderScreen(queryClient);

    expect(screen.getByTestId("stack-template-loading")).toBeTruthy();
  });

  it("renders the installed template details and variables prefilled from its config", () => {
    const queryClient = testQueryClient();
    seedDefaultData(queryClient);

    renderScreen(queryClient);

    const details = screen.getByTestId("stack-template-screen");
    expect(details.textContent).toContain("tmpl_src_1");
    expect(details.textContent).toContain("rev_1");
    expect(details.textContent).toContain("rev_0");
    expect(details.textContent).toContain("ws-payments");
    const input = screen.getByLabelText(/region/) as HTMLInputElement;
    expect(input.value).toBe("us-east-1");
  });

  it("disables save while values match the installed config and enables it after an edit", () => {
    const queryClient = testQueryClient();
    seedDefaultData(queryClient);

    renderScreen(queryClient);

    expect(actionButton(/Save config/).disabled).toBe(true);
    fireEvent.change(screen.getByLabelText(/region/), { target: { value: "eu-west-1" } });
    expect(actionButton(/Save config/).disabled).toBe(false);
  });

  it("enables upgrade after selecting a different active revision", () => {
    const queryClient = testQueryClient();
    seedDefaultData(queryClient);

    renderScreen(queryClient);

    expect(actionButton(/Upgrade/).disabled).toBe(true);
    fireEvent.change(screen.getByTestId("stack-template-revision-select"), { target: { value: "rev_2" } });
    expect(actionButton(/Upgrade/).disabled).toBe(false);
  });

  it("renders every action disabled with a reason when canOperate is denied", async () => {
    const queryClient = testQueryClient();
    seedDefaultData(queryClient, { ...allAllowed, canOperate: false });

    renderScreen(queryClient);

    await waitFor(() => expect(screen.getByTestId("variables-disabled-reason")).toBeTruthy());
    expect(actionButton(/Install template/).disabled).toBe(true);
    expect(actionButton(/Save config/).disabled).toBe(true);
    expect(actionButton(/Upgrade/).disabled).toBe(true);
    expect((screen.getByLabelText(/region/) as HTMLInputElement).disabled).toBe(true);
  });

  it("offers install for the selected active revision when no template is installed yet", () => {
    const queryClient = testQueryClient();
    queryClient.setQueryData(queryKeys.stack("tenant_123", "stack_1"), stackView(allAllowed, []));
    queryClient.setQueryData(queryKeys.templateRevisions("tenant_123"), [templateRevision()]);
    queryClient.setQueryData(queryKeys.templateRevisionVariables("tenant_123", "rev_1"), [variable()]);

    renderScreen(queryClient);

    expect(screen.getByTestId("stack-template-empty")).toBeTruthy();
    expect(actionButton(/Install template/).disabled).toBe(false);
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
});
