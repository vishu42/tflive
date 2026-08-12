// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import AddStackTemplateScreen from "./AddStackTemplateScreen";
import { queryKeys } from "../../api/queryKeys";
import type { TemplateRevision, TemplateVariable } from "../../api/types";
import { AuthContext } from "../../auth/AuthContext";
import type { AuthContextValue } from "../../auth/AuthContext";

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

function testQueryClient(): QueryClient {
  return new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
}

// AddStackTemplateScreen renders via useQueryErrorBoundary, which calls
// useAuth() unconditionally (see queryErrorBoundary.tsx and its use in every
// sibling screen test — StackTemplateScreen.test.tsx, CreateStackScreen.test.tsx,
// TemplateRegistryScreen.test.tsx, etc.). Without an AuthContext.Provider the
// hook throws "useAuth must be used within an AuthProvider" before the screen
// can render at all, so this wrapper is required infrastructure, not a test
// case change.
function authValue(overrides: Partial<AuthContextValue> = {}): AuthContextValue {
  return {
    me: { sub: "user_1", tenantID: "tenant_123", displayName: "Test User", globalCapabilities: { isPlatformAdmin: false, canCreateStack: false } },
    status: "authenticated",
    login: () => {},
    logout: () => {},
    ...overrides
  };
}

function LocationProbe() {
  const location = useLocation();
  return <span data-testid="location">{`${location.pathname}${location.search}`}</span>;
}

function renderScreen(queryClient: QueryClient) {
  return render(
    <QueryClientProvider client={queryClient}>
      <AuthContext.Provider value={authValue()}>
        <MemoryRouter initialEntries={["/stacks/stack_1/template/new"]}>
          <Routes>
            <Route path="/stacks/:stackId/template/new" element={<AddStackTemplateScreen />} />
            <Route path="/stacks/:stackId/template" element={<LocationProbe />} />
          </Routes>
        </MemoryRouter>
      </AuthContext.Provider>
    </QueryClientProvider>
  );
}

describe("AddStackTemplateScreen", () => {
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it("groups selectable revisions by repository", () => {
    const queryClient = testQueryClient();
    queryClient.setQueryData(queryKeys.templateRevisions("tenant_123"), [
      templateRevision(),
      templateRevision({ id: "rev_2", repo_owner: "my-org", repo_name: "rds" })
    ]);

    renderScreen(queryClient);

    expect(screen.getByTestId("template-group-hashicorp/vpc")).toBeTruthy();
    expect(screen.getByTestId("template-group-my-org/rds")).toBeTruthy();
  });

  it("hides the variables form until a revision is chosen", () => {
    const queryClient = testQueryClient();
    queryClient.setQueryData(queryKeys.templateRevisions("tenant_123"), [templateRevision()]);
    queryClient.setQueryData(queryKeys.templateRevisionVariables("tenant_123", "rev_1"), [variable()]);

    renderScreen(queryClient);

    expect(screen.queryByTestId("add-stack-template-variables")).toBeNull();

    fireEvent.click(screen.getByTestId("add-template-choice-rev_1"));

    expect(screen.getByTestId("add-stack-template-variables")).toBeTruthy();
    expect((screen.getByLabelText(/region/) as HTMLInputElement).value).toBe("");
  });

  it("does not allow choosing a revision that is not active", () => {
    const queryClient = testQueryClient();
    queryClient.setQueryData(queryKeys.templateRevisions("tenant_123"), [
      templateRevision({ id: "rev_pending", status: "pending_validation" })
    ]);

    renderScreen(queryClient);

    expect((screen.getByTestId("add-template-choice-rev_pending") as HTMLButtonElement).disabled).toBe(true);
  });

  it("installs the chosen revision and returns to the template screen with it selected", async () => {
    const queryClient = testQueryClient();
    queryClient.setQueryData(queryKeys.templateRevisions("tenant_123"), [templateRevision()]);
    queryClient.setQueryData(queryKeys.templateRevisionVariables("tenant_123", "rev_1"), [variable()]);
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({ id: "st_new" }), {
        status: 200,
        headers: { "content-type": "application/json" }
      })
    );

    renderScreen(queryClient);

    fireEvent.click(screen.getByTestId("add-template-choice-rev_1"));
    fireEvent.change(screen.getByLabelText(/region/), { target: { value: "eu-west-1" } });
    fireEvent.click(screen.getByRole("button", { name: /Install/ }));

    await waitFor(() => expect(screen.getByTestId("location")).toBeTruthy());
    expect(screen.getByTestId("location").textContent).toBe("/stacks/stack_1/template?selected=st_new");

    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("/v1/tenants/tenant_123/stacks/stack_1/templates");
    expect(init.method).toBe("POST");
    expect(JSON.parse(init.body as string)).toEqual({
      template_revision_id: "rev_1",
      selected_ref: "main",
      config: { region: "eu-west-1" }
    });
  });

  it("surfaces a failed install without leaving the screen", async () => {
    const queryClient = testQueryClient();
    queryClient.setQueryData(queryKeys.templateRevisions("tenant_123"), [templateRevision()]);
    queryClient.setQueryData(queryKeys.templateRevisionVariables("tenant_123", "rev_1"), [variable()]);
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({ error: "conflict", message: "already installed" }), {
        status: 409,
        headers: { "content-type": "application/json" }
      })
    );

    renderScreen(queryClient);

    fireEvent.click(screen.getByTestId("add-template-choice-rev_1"));
    fireEvent.click(screen.getByRole("button", { name: /Install/ }));

    await waitFor(() => expect(screen.getByTestId("add-stack-template-error")).toBeTruthy());
  });

  it("disables Install and hides the empty-variables message while the chosen revision's variables are loading", () => {
    const queryClient = testQueryClient();
    queryClient.setQueryData(queryKeys.templateRevisions("tenant_123"), [templateRevision()]);
    // Variables for rev_1 are deliberately left unseeded, and fetch never
    // resolves, so useTemplateRevisionVariablesQuery stays pending.
    vi.spyOn(globalThis, "fetch").mockReturnValue(new Promise(() => {}));

    renderScreen(queryClient);

    fireEvent.click(screen.getByTestId("add-template-choice-rev_1"));

    expect(screen.getByTestId("add-stack-template-variables-loading")).toBeTruthy();
    expect(screen.queryByText("This template declares no variables")).toBeNull();
    expect((screen.getByRole("button", { name: /Install/ }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("does not render the previous revision's variable names after switching to a revision whose variables have not loaded", () => {
    const queryClient = testQueryClient();
    queryClient.setQueryData(queryKeys.templateRevisions("tenant_123"), [
      templateRevision(),
      templateRevision({ id: "rev_2", repo_owner: "my-org", repo_name: "rds" })
    ]);
    queryClient.setQueryData(queryKeys.templateRevisionVariables("tenant_123", "rev_1"), [variable()]);
    // rev_2's variables are deliberately left unseeded, and fetch never
    // resolves. Without gating on isFetching, keepPreviousData would keep
    // rendering rev_1's "region" field under rev_2's name.
    vi.spyOn(globalThis, "fetch").mockReturnValue(new Promise(() => {}));

    renderScreen(queryClient);

    fireEvent.click(screen.getByTestId("add-template-choice-rev_1"));
    expect(screen.getByLabelText(/region/)).toBeTruthy();

    fireEvent.click(screen.getByTestId("add-template-choice-rev_2"));

    expect(screen.queryByLabelText(/region/)).toBeNull();
    expect(screen.getByTestId("add-stack-template-variables-loading")).toBeTruthy();
  });

  it("shows an error instead of claiming the template has no variables when they fail to load", async () => {
    const queryClient = testQueryClient();
    queryClient.setQueryData(queryKeys.templateRevisions("tenant_123"), [templateRevision()]);
    // A permanently failed variables fetch leaves data undefined, which renders
    // as the empty message — telling the user this template declares no
    // variables when in truth we could not find out. Install must not be
    // offered against a config we cannot build.
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({ error: "unauthorized", message: "unauthorized" }), {
        status: 401,
        headers: { "content-type": "application/json" }
      })
    );

    renderScreen(queryClient);

    fireEvent.click(screen.getByTestId("add-template-choice-rev_1"));

    await waitFor(() => expect(screen.getByTestId("add-stack-template-variables-error")).toBeTruthy());
    expect(screen.queryByText("This template declares no variables")).toBeNull();
    expect((screen.getByRole("button", { name: /Install/ }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("points at template registration when the tenant has none", () => {
    const queryClient = testQueryClient();
    queryClient.setQueryData(queryKeys.templateRevisions("tenant_123"), []);

    renderScreen(queryClient);

    expect(screen.getByTestId("add-stack-template-none")).toBeTruthy();
    expect(screen.getByTestId("register-template-link").getAttribute("href")).toBe("/templates/new");
  });
});
