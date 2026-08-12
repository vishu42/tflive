// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import UpgradeStackTemplateScreen from "./UpgradeStackTemplateScreen";
import { queryKeys } from "../../api/queryKeys";
import type { StackTemplate, StackView, TemplateRevision, TemplateVariable } from "../../api/types";
import { AuthContext } from "../../auth/AuthContext";
import type { AuthContextValue } from "../../auth/AuthContext";

function stackTemplate(overrides: Partial<StackTemplate> = {}): StackTemplate {
  return {
    id: "st_1",
    stack_id: "stack_1",
    component_key: "vpc",
    source_template_id: "tmpl_src_1",
    desired_template_revision_id: "rev_current",
    last_applied_template_revision_id: "rev_current",
    selected_ref: "main",
    workspace_name: "ws-payments",
    display_name: "",
    config: { region: "us-east-1", legacy_flag: "yes" },
    last_applied_run_id: "run_9",
    last_applied_ref: "main",
    created_by: "user_123",
    lifecycle: "active",
    ...overrides
  };
}

function stackView(templates: StackTemplate[]): StackView {
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
      effectiveCapabilities: { canView: true, canOperate: true, canApprove: true, canManageAccess: true }
    },
    templates
  };
}

function templateRevision(overrides: Partial<TemplateRevision> = {}): TemplateRevision {
  return {
    id: "rev_current",
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
    template_revision_id: "rev_current",
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

/** Installed rev_current has region + legacy_flag; rev_next drops legacy_flag and adds new_var. */
function seedUpgradeable(queryClient: QueryClient) {
  queryClient.setQueryData(queryKeys.stack("tenant_123", "stack_1"), stackView([stackTemplate()]));
  queryClient.setQueryData(queryKeys.templateRevisions("tenant_123"), [
    templateRevision({ id: "rev_next", source_ref: "main", resolved_commit_sha: "9f21abc0000000" }),
    templateRevision({ id: "rev_current" })
  ]);
  queryClient.setQueryData(queryKeys.templateRevisionVariables("tenant_123", "rev_current"), [
    variable({ name: "region" }),
    variable({ name: "legacy_flag" })
  ]);
  queryClient.setQueryData(queryKeys.templateRevisionVariables("tenant_123", "rev_next"), [
    variable({ name: "region", template_revision_id: "rev_next" }),
    variable({ name: "new_var", template_revision_id: "rev_next" })
  ]);
}

function LocationProbe() {
  const location = useLocation();
  return <span data-testid="location">{`${location.pathname}${location.search}`}</span>;
}

// UpgradeStackTemplateScreen renders via useQueryErrorBoundary, which calls
// useAuth() unconditionally (see queryErrorBoundary.tsx and its use in every
// sibling screen test — AddStackTemplateScreen.test.tsx, StackTemplateScreen.test.tsx,
// etc.). Without an AuthContext.Provider the hook throws "useAuth must be used
// within an AuthProvider" before the screen can render at all, so this
// wrapper is required infrastructure, not a test case change.
function authValue(overrides: Partial<AuthContextValue> = {}): AuthContextValue {
  return {
    me: { sub: "user_1", tenantID: "tenant_123", displayName: "Test User", globalCapabilities: { isPlatformAdmin: false, canCreateStack: false } },
    status: "authenticated",
    login: () => {},
    logout: () => {},
    ...overrides
  };
}

function renderScreen(queryClient: QueryClient) {
  return render(
    <QueryClientProvider client={queryClient}>
      <AuthContext.Provider value={authValue()}>
        <MemoryRouter initialEntries={["/stacks/stack_1/template/st_1/upgrade"]}>
          <Routes>
            <Route path="/stacks/:stackId/template/:stackTemplateId/upgrade" element={<UpgradeStackTemplateScreen />} />
            <Route path="/stacks/:stackId/template" element={<LocationProbe />} />
          </Routes>
        </MemoryRouter>
      </AuthContext.Provider>
    </QueryClientProvider>
  );
}

describe("UpgradeStackTemplateScreen", () => {
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it("preselects the first same-source alternative revision", () => {
    const queryClient = testQueryClient();
    seedUpgradeable(queryClient);

    renderScreen(queryClient);

    expect((screen.getByTestId("upgrade-target-select") as HTMLSelectElement).value).toBe("rev_next");
  });

  it("allows selecting an active same-source revision even when its commit is older", () => {
    const queryClient = testQueryClient();
    queryClient.setQueryData(queryKeys.stack("tenant_123", "stack_1"), stackView([stackTemplate()]));
    queryClient.setQueryData(queryKeys.templateRevisions("tenant_123"), [
      templateRevision({
        id: "rev_older",
        resolved_commit_sha: "0000000000000000",
        created_at: "2026-07-01T00:00:00Z"
      }),
      templateRevision({ id: "rev_current" })
    ]);
    queryClient.setQueryData(queryKeys.templateRevisionVariables("tenant_123", "rev_current"), [variable()]);
    queryClient.setQueryData(queryKeys.templateRevisionVariables("tenant_123", "rev_older"), [variable({ template_revision_id: "rev_older" })]);

    renderScreen(queryClient);

    const options = Array.from((screen.getByTestId("upgrade-target-select") as HTMLSelectElement).options);
    expect(options.map((option) => option.value)).toEqual(["rev_older"]);
  });

  it("excludes the installed revision and other source templates from the candidates", () => {
    const queryClient = testQueryClient();
    seedUpgradeable(queryClient);
    queryClient.setQueryData(queryKeys.templateRevisions("tenant_123"), [
      templateRevision({ id: "rev_next" }),
      templateRevision({ id: "rev_current" }),
      templateRevision({ id: "rev_elsewhere", source_template_id: "tmpl_src_2" }),
      templateRevision({ id: "rev_pending", status: "pending_validation" })
    ]);

    renderScreen(queryClient);

    const options = Array.from((screen.getByTestId("upgrade-target-select") as HTMLSelectElement).options);
    expect(options.map((option) => option.value)).toEqual(["rev_next"]);
  });

  it("marks variables as added, carried, or removed against the target revision", () => {
    const queryClient = testQueryClient();
    seedUpgradeable(queryClient);

    renderScreen(queryClient);

    expect(screen.getByTestId("upgrade-added-new_var")).toBeTruthy();
    expect(screen.getByTestId("upgrade-removed-legacy_flag")).toBeTruthy();
    expect((screen.getByLabelText(/region/) as HTMLInputElement).value).toBe("us-east-1");
  });

  it("upgrades with a config built only from the target revision's variables", async () => {
    const queryClient = testQueryClient();
    seedUpgradeable(queryClient);
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({ id: "st_1" }), {
        status: 200,
        headers: { "content-type": "application/json" }
      })
    );

    renderScreen(queryClient);

    fireEvent.change(screen.getByLabelText(/new_var/), { target: { value: "enabled" } });
    fireEvent.click(screen.getByRole("button", { name: /Change revision/ }));

    await waitFor(() => expect(screen.getByTestId("location")).toBeTruthy());
    expect(screen.getByTestId("location").textContent).toBe("/stacks/stack_1/template?selected=st_1");

    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("/v1/tenants/tenant_123/stack-templates/st_1/upgrade");
    expect(init.method).toBe("POST");
    expect(JSON.parse(init.body as string)).toEqual({
      target_template_revision_id: "rev_next",
      config: { region: "us-east-1", new_var: "enabled" }
    });
  });

  it("renders the loading state instead of a false diff while the target revision's variables have not loaded", () => {
    const queryClient = testQueryClient();
    queryClient.setQueryData(queryKeys.stack("tenant_123", "stack_1"), stackView([stackTemplate()]));
    queryClient.setQueryData(queryKeys.templateRevisions("tenant_123"), [
      templateRevision({ id: "rev_next", source_ref: "main", resolved_commit_sha: "9f21abc0000000" }),
      templateRevision({ id: "rev_current" })
    ]);
    queryClient.setQueryData(queryKeys.templateRevisionVariables("tenant_123", "rev_current"), [
      variable({ name: "region" }),
      variable({ name: "legacy_flag" })
    ]);
    // rev_next's variables are deliberately left unseeded, and fetch never
    // resolves — on arrival this is the default first frame, not an edge
    // case, since the target revision's variables are never in cache yet.
    vi.spyOn(globalThis, "fetch").mockReturnValue(new Promise(() => {}));

    renderScreen(queryClient);

    expect(screen.getByTestId("upgrade-loading")).toBeTruthy();
    expect(screen.queryByText(/is no longer used and will be dropped/)).toBeNull();
    expect(screen.queryByText(/is new in this revision/)).toBeNull();
    expect(screen.queryByRole("button", { name: /Change revision/ })).toBeNull();
  });

  it("suppresses the previous target's fields and diff notes while switching to a target whose variables have not loaded", () => {
    const queryClient = testQueryClient();
    queryClient.setQueryData(queryKeys.stack("tenant_123", "stack_1"), stackView([stackTemplate()]));
    queryClient.setQueryData(queryKeys.templateRevisions("tenant_123"), [
      templateRevision({ id: "rev_next", source_ref: "main", resolved_commit_sha: "9f21abc0000000" }),
      templateRevision({ id: "rev_next_2", source_ref: "main", resolved_commit_sha: "aa00bb1111111" }),
      templateRevision({ id: "rev_current" })
    ]);
    queryClient.setQueryData(queryKeys.templateRevisionVariables("tenant_123", "rev_current"), [
      variable({ name: "region" }),
      variable({ name: "legacy_flag" })
    ]);
    queryClient.setQueryData(queryKeys.templateRevisionVariables("tenant_123", "rev_next"), [
      variable({ name: "region", template_revision_id: "rev_next" }),
      variable({ name: "new_var", template_revision_id: "rev_next" })
    ]);
    // rev_next_2's variables are deliberately left unseeded.

    renderScreen(queryClient);

    // The preselected first candidate, rev_next, has already loaded.
    expect(screen.getByTestId("upgrade-added-new_var")).toBeTruthy();
    expect(screen.getByLabelText(/new_var/)).toBeTruthy();

    // Switch to the second candidate with fetch stalled: keepPreviousData
    // means targetVariablesQuery.status flips to "success" immediately and
    // keeps serving rev_next's variables/diff while the rev_next_2 fetch
    // never resolves. Only isFetching-gating stops that from being shown as
    // current.
    vi.spyOn(globalThis, "fetch").mockReturnValue(new Promise(() => {}));
    fireEvent.change(screen.getByTestId("upgrade-target-select"), { target: { value: "rev_next_2" } });

    expect(screen.getByTestId("upgrade-target-variables-loading")).toBeTruthy();
    expect(screen.queryByLabelText(/new_var/)).toBeNull();
    expect(screen.queryByTestId("upgrade-added-new_var")).toBeNull();
    expect(screen.queryByTestId("upgrade-removed-legacy_flag")).toBeNull();
    expect((screen.getByRole("button", { name: /Change revision/ }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("guards the direct-URL path when the installed template is mid-destroy", () => {
    const queryClient = testQueryClient();
    seedUpgradeable(queryClient);
    queryClient.setQueryData(
      queryKeys.stack("tenant_123", "stack_1"),
      stackView([stackTemplate({ lifecycle: "destroying" })])
    );

    renderScreen(queryClient);

    expect(screen.getByTestId("upgrade-destroying")).toBeTruthy();
    expect(screen.queryByRole("button", { name: /Change revision/ })).toBeNull();
  });

  it("reports when no other active revisions are available", () => {
    const queryClient = testQueryClient();
    seedUpgradeable(queryClient);
    queryClient.setQueryData(queryKeys.templateRevisions("tenant_123"), [templateRevision({ id: "rev_current" })]);

    renderScreen(queryClient);

    expect(screen.getByTestId("upgrade-no-alternatives")).toBeTruthy();
    expect(screen.getByText("No other active revisions available.")).toBeTruthy();
    expect(screen.queryByRole("button", { name: /Change revision/ })).toBeNull();
  });

  it("surfaces a failed upgrade without leaving the screen", async () => {
    const queryClient = testQueryClient();
    seedUpgradeable(queryClient);
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({ error: "conflict", message: "run in progress" }), {
        status: 409,
        headers: { "content-type": "application/json" }
      })
    );

    renderScreen(queryClient);

    fireEvent.click(screen.getByRole("button", { name: /Change revision/ }));

    await waitFor(() => expect(screen.getByTestId("upgrade-stack-template-error")).toBeTruthy());
  });

  it("renders not found when the stack template id is not installed", () => {
    const queryClient = testQueryClient();
    seedUpgradeable(queryClient);
    queryClient.setQueryData(queryKeys.stack("tenant_123", "stack_1"), stackView([]));

    renderScreen(queryClient);

    expect(screen.getByTestId("upgrade-template-missing")).toBeTruthy();
  });
});
