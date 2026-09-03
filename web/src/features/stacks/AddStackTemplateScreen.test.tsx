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
    vi.unstubAllGlobals();
  });

  it("groups selectable templates by repository", () => {
    const queryClient = testQueryClient();
    queryClient.setQueryData(queryKeys.templateRevisions("tenant_123"), [
      templateRevision(),
      templateRevision({ id: "rev_2", source_template_id: "tmpl_src_2", repo_owner: "my-org", repo_name: "rds" })
    ]);

    renderScreen(queryClient);

    expect(screen.getByTestId("template-group-hashicorp/vpc")).toBeTruthy();
    expect(screen.getByTestId("template-group-my-org/rds")).toBeTruthy();
    // No count pill here: it would count templates while the rows count
    // installable revisions, two adjacent numbers meaning different things.
    expect(screen.queryByTestId("template-group-count-hashicorp/vpc")).toBeNull();
  });

  it("shows one row per template, named after the template", () => {
    const queryClient = testQueryClient();
    queryClient.setQueryData(queryKeys.templateRevisions("tenant_123"), [
      templateRevision({ id: "rev_2", resolved_commit_sha: "44b2e0199999" }),
      templateRevision({ id: "rev_1" })
    ]);

    renderScreen(queryClient);

    const row = screen.getByTestId("add-template-choice-tmpl_src_1");
    // The name leads; the ref and commit are demoted to the trailing meta.
    expect(row.querySelector(".templates-list__name")?.textContent).toBe("vpc");
    expect(row.textContent).toContain("main");
    expect(row.textContent).toContain("44b2e01");
    // Two revisions of one template are one row.
    expect(document.querySelectorAll(".template-choices li")).toHaveLength(1);
  });

  it("says nothing about a template whose latest revision is active", () => {
    const queryClient = testQueryClient();
    queryClient.setQueryData(queryKeys.templateRevisions("tenant_123"), [templateRevision()]);

    renderScreen(queryClient);

    const row = screen.getByTestId("add-template-choice-tmpl_src_1");
    expect(row.textContent).not.toContain("active");
    expect(row.querySelector(".status-tone")).toBeNull();
  });

  it("names the chosen template in the panel that configures it", () => {
    const queryClient = testQueryClient();
    queryClient.setQueryData(queryKeys.templateRevisions("tenant_123"), [
      templateRevision(),
      templateRevision({ id: "rev_2", source_template_id: "tmpl_src_2", name: "rds", repo_owner: "my-org", repo_name: "rds" })
    ]);
    queryClient.setQueryData(queryKeys.templateRevisionVariables("tenant_123", "rev_1"), [variable()]);

    renderScreen(queryClient);

    // Before choosing, the pane invites a choice rather than sitting empty.
    expect(screen.getByTestId("add-stack-template-unchosen")).toBeTruthy();

    fireEvent.click(screen.getByTestId("add-template-choice-tmpl_src_1"));

    // Named, because position alone said nothing: stacked under the list, this
    // panel read as belonging to whichever template rendered last.
    expect(screen.getByTestId("add-stack-template-variables").textContent).toContain("vpc");
    expect(screen.queryByTestId("add-stack-template-unchosen")).toBeNull();
  });

  it("hides the variables form until a revision is chosen", () => {
    const queryClient = testQueryClient();
    queryClient.setQueryData(queryKeys.templateRevisions("tenant_123"), [templateRevision()]);
    queryClient.setQueryData(queryKeys.templateRevisionVariables("tenant_123", "rev_1"), [variable()]);

    renderScreen(queryClient);

    expect(screen.queryByTestId("add-stack-template-variables")).toBeNull();

    fireEvent.click(screen.getByTestId("add-template-choice-tmpl_src_1"));

    expect(screen.getByTestId("add-stack-template-variables")).toBeTruthy();
    expect((screen.getByLabelText(/region/) as HTMLInputElement).value).toBe("");
  });

  it("marks itself unsaved once a variable value is typed, so SessionProvider's proactive re-auth defers", () => {
    const queryClient = testQueryClient();
    queryClient.setQueryData(queryKeys.templateRevisions("tenant_123"), [templateRevision()]);
    queryClient.setQueryData(queryKeys.templateRevisionVariables("tenant_123", "rev_1"), [variable()]);

    renderScreen(queryClient);
    fireEvent.click(screen.getByTestId("add-template-choice-tmpl_src_1"));

    expect(document.querySelector("[data-unsaved='true']")).toBeNull();
    fireEvent.change(screen.getByLabelText(/region/), { target: { value: "eu-west-1" } });
    expect(document.querySelector("[data-unsaved='true']")).not.toBeNull();
  });

  it("does not allow choosing a template with no active revision, and says why", () => {
    const queryClient = testQueryClient();
    queryClient.setQueryData(queryKeys.templateRevisions("tenant_123"), [
      templateRevision({ id: "rev_pending", status: "pending_validation" })
    ]);

    renderScreen(queryClient);

    const row = screen.getByTestId("add-template-choice-tmpl_src_1");
    expect((row as HTMLButtonElement).disabled).toBe(true);
    // A dead button with no reason on it is what the status pill is for.
    expect(row.textContent).toContain("pending_validation");
  });

  it("installs the newest active revision when a newer one failed validation", () => {
    const queryClient = testQueryClient();
    queryClient.setQueryData(queryKeys.templateRevisions("tenant_123"), [
      templateRevision({ id: "rev_bad", status: "invalid", resolved_commit_sha: "44b2e0199999" }),
      templateRevision({ id: "rev_good", status: "active" })
    ]);
    queryClient.setQueryData(queryKeys.templateRevisionVariables("tenant_123", "rev_good"), [variable()]);

    renderScreen(queryClient);

    const row = screen.getByTestId("add-template-choice-tmpl_src_1");
    // Selectable, because a validated revision is still available to install —
    // and the commit shown is that one, not the newer broken one.
    expect((row as HTMLButtonElement).disabled).toBe(false);
    expect(row.textContent).toContain("abcdef1");
    expect(row.textContent).not.toContain("44b2e01");
    // The pill still reports the latest revision's state, so a failed
    // validation is not hidden behind an older success.
    expect(row.textContent).toContain("invalid");

    fireEvent.click(row);

    expect(screen.getByTestId("add-stack-template-variables")).toBeTruthy();
    expect(screen.getByLabelText(/region/)).toBeTruthy();
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

    fireEvent.click(screen.getByTestId("add-template-choice-tmpl_src_1"));
    fireEvent.change(screen.getByLabelText(/region/), { target: { value: "eu-west-1" } });
    fireEvent.click(screen.getByRole("button", { name: /Install/ }));

    await waitFor(() => expect(screen.getByTestId("location")).toBeTruthy());
    expect(screen.getByTestId("location").textContent).toBe("/stacks/stack_1/template?selected=st_new");

    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("/v1/tenants/tenant_123/stacks/stack_1/templates");
    expect(init.method).toBe("POST");
    // No ref is sent: the revision already determines it, and a client-supplied
    // ref could disagree with the revision being installed.
    expect(JSON.parse(init.body as string)).toEqual({
      template_revision_id: "rev_1",
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

    fireEvent.click(screen.getByTestId("add-template-choice-tmpl_src_1"));
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

    fireEvent.click(screen.getByTestId("add-template-choice-tmpl_src_1"));

    expect(screen.getByTestId("add-stack-template-variables-loading")).toBeTruthy();
    expect(screen.queryByText("This template declares no variables")).toBeNull();
    expect((screen.getByRole("button", { name: /Install/ }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("does not render the previous revision's variable names after switching to a revision whose variables have not loaded", () => {
    const queryClient = testQueryClient();
    queryClient.setQueryData(queryKeys.templateRevisions("tenant_123"), [
      templateRevision(),
      templateRevision({ id: "rev_2", source_template_id: "tmpl_src_2", repo_owner: "my-org", repo_name: "rds" })
    ]);
    queryClient.setQueryData(queryKeys.templateRevisionVariables("tenant_123", "rev_1"), [variable()]);
    // rev_2's variables are deliberately left unseeded, and fetch never
    // resolves. Without gating on isFetching, keepPreviousData would keep
    // rendering rev_1's "region" field under rev_2's name.
    vi.spyOn(globalThis, "fetch").mockReturnValue(new Promise(() => {}));

    renderScreen(queryClient);

    fireEvent.click(screen.getByTestId("add-template-choice-tmpl_src_1"));
    expect(screen.getByLabelText(/region/)).toBeTruthy();

    fireEvent.click(screen.getByTestId("add-template-choice-tmpl_src_2"));

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
    //
    // The 401 also drives client.ts's fetchWithAuth to navigate via
    // globalThis.location.assign; stub it so jsdom does not attempt (and
    // warn about) a real navigation.
    vi.stubGlobal("location", { ...window.location, assign: vi.fn() });
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({ error: "unauthorized", message: "unauthorized" }), {
        status: 401,
        headers: { "content-type": "application/json" }
      })
    );

    renderScreen(queryClient);

    fireEvent.click(screen.getByTestId("add-template-choice-tmpl_src_1"));

    await waitFor(() => expect(screen.getByTestId("add-stack-template-variables-error")).toBeTruthy());
    expect(screen.queryByText("This template declares no variables")).toBeNull();
    expect((screen.getByRole("button", { name: /Install/ }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("offers no revision picker when the template has only one active revision", () => {
    const queryClient = testQueryClient();
    queryClient.setQueryData(queryKeys.templateRevisions("tenant_123"), [
      templateRevision(),
      // Not active, so not an alternative to choose between.
      templateRevision({ id: "rev_old", status: "invalid", resolved_commit_sha: "44b2e0199999" })
    ]);
    queryClient.setQueryData(queryKeys.templateRevisionVariables("tenant_123", "rev_1"), [variable()]);

    renderScreen(queryClient);
    fireEvent.click(screen.getByTestId("add-template-choice-tmpl_src_1"));

    expect(screen.queryByTestId("add-template-revision-select")).toBeNull();
  });

  it("lists a template's active revisions newest-registered first, defaulting to the latest", () => {
    const queryClient = testQueryClient();
    // The API returns created_at desc, id desc; the screen renders that order
    // as received and must not re-sort it.
    queryClient.setQueryData(queryKeys.templateRevisions("tenant_123"), [
      templateRevision({ id: "rev_new", resolved_commit_sha: "f17f9834444", created_at: "2026-08-19T10:00:00Z" }),
      templateRevision({ id: "rev_mid", resolved_commit_sha: "a91c2045555", created_at: "2026-08-02T09:30:00Z" }),
      templateRevision({ id: "rev_old", resolved_commit_sha: "3c0e1126666", created_at: "2026-06-28T14:15:00Z" })
    ]);
    queryClient.setQueryData(queryKeys.templateRevisionVariables("tenant_123", "rev_new"), [variable()]);

    renderScreen(queryClient);
    fireEvent.click(screen.getByTestId("add-template-choice-tmpl_src_1"));

    const select = screen.getByTestId("add-template-revision-select") as HTMLSelectElement;
    expect(Array.from(select.options).map((option) => option.value)).toEqual(["rev_new", "rev_mid", "rev_old"]);
    expect(select.value).toBe("rev_new");
    expect(select.options[0].textContent).toContain("f17f983");
    expect(select.options[0].textContent).toContain("19 Aug 2026");
    expect(select.options[0].textContent).toContain("latest");
    expect(select.options[1].textContent).not.toContain("latest");
  });

  it("installs the revision chosen in the picker rather than the default", async () => {
    const queryClient = testQueryClient();
    queryClient.setQueryData(queryKeys.templateRevisions("tenant_123"), [
      templateRevision({ id: "rev_new", resolved_commit_sha: "f17f9834444" }),
      templateRevision({ id: "rev_old", resolved_commit_sha: "3c0e1126666" })
    ]);
    queryClient.setQueryData(queryKeys.templateRevisionVariables("tenant_123", "rev_new"), [variable()]);
    queryClient.setQueryData(queryKeys.templateRevisionVariables("tenant_123", "rev_old"), [variable()]);
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({ id: "st_new" }), {
        status: 200,
        headers: { "content-type": "application/json" }
      })
    );

    renderScreen(queryClient);
    fireEvent.click(screen.getByTestId("add-template-choice-tmpl_src_1"));
    fireEvent.change(screen.getByLabelText(/region/), { target: { value: "eu-west-1" } });
    fireEvent.change(screen.getByTestId("add-template-revision-select"), { target: { value: "rev_old" } });

    // Switching revision clears typed values, the same as choosing a template:
    // the new revision's variables are what the config must be built from.
    expect((screen.getByLabelText(/region/) as HTMLInputElement).value).toBe("");

    fireEvent.change(screen.getByLabelText(/region/), { target: { value: "us-east-1" } });
    fireEvent.click(screen.getByRole("button", { name: /Install/ }));

    await waitFor(() => expect(screen.getByTestId("location")).toBeTruthy());
    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(JSON.parse(init.body as string)).toEqual({
      template_revision_id: "rev_old",
      config: { region: "us-east-1" }
    });
  });

  it("points at template registration when the tenant has none", () => {
    const queryClient = testQueryClient();
    queryClient.setQueryData(queryKeys.templateRevisions("tenant_123"), []);

    renderScreen(queryClient);

    expect(screen.getByTestId("add-stack-template-none")).toBeTruthy();
    expect(screen.getByTestId("register-template-link").getAttribute("href")).toBe("/templates/new");
  });
});
