# Stack Template Screen Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Split the stack template screen's one overloaded form into three focused flows — configure the installed template, add a new template, upgrade to a different revision.

**Architecture:** `/stacks/:stackId/template` becomes read-and-configure only: it renders the installed template's variables and nothing else. Installing moves to `template/new` and upgrading to `template/:stackTemplateId/upgrade`, both guarded by `canOperate` route guards. Three new pure functions in `stackWorkflow.ts` carry the logic those screens need, so the screens stay thin and the rules are unit-tested in isolation.

**Tech Stack:** React 18 + TypeScript, react-router-dom v6, TanStack Query v5, Vitest + Testing Library (jsdom), lucide-react icons, plain CSS with design tokens.

**Design spec:** `docs/superpowers/specs/2026-08-12-stack-template-screen-redesign-design.md`

## Revision-order correction

The template revision record does not expose Git ancestry, so registration
order, timestamps, and commit SHAs cannot safely identify a newer revision.
The implemented flow therefore treats the existing upgrade endpoint as a
neutral revision-change action: the detail screen always offers **Change
revision** for an installed template, and the chooser lists every active
revision belonging to the same source template except the installed one. The
first returned candidate is only the default selection; it is not presented as
newer. When no alternatives exist, the chooser reports that plainly instead of
claiming the installed revision is latest.

## Global Constraints

- Work from `web/`. Verify with `npm test` and `npm run build` (build type-checks).
- Tests sit beside the code they cover, named `*.test.ts` / `*.test.tsx`. jsdom tests need the `// @vitest-environment jsdom` pragma on line 1.
- **No raw hex colors in any stylesheet except `tokens.css`.** `src/styles/styles.guard.test.ts` fails the build otherwise. Use `var(--color-*)`.
- The tenant ID comes from `tenantID` in `src/config.ts`. Tests stub it via the seeded query keys using `"tenant_123"`.
- Do **not** modify `src/App.tsx` — the legacy console at `/`. It still imports `VariablesPanel` and `InstalledTemplatePanel` and calls the four-argument `canSaveStackTemplateConfig`. All three must keep working unchanged.
- Do **not** change the installed-templates list on the left of the template screen (issue #128 owns that).
- Commit subjects are short, imperative, lowercase-prefixed: `feat:`, `fix:`, `test:`, `refactor:`, `docs:`.
- Every commit ends with:
  ```
  Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
  ```

## File Structure

| File | Responsibility |
|---|---|
| `src/features/stacks/stackWorkflow.ts` | *(modify)* add `upgradeCandidateRevisions`, `partitionUpgradeVariables`, `canSaveInstalledTemplateConfig` |
| `src/features/stacks/VariableFields.tsx` | *(create)* presentational variable inputs — no mutations, no revisions |
| `src/features/stacks/StackTemplateConfigPanel.tsx` | *(create)* `VariableFields` + a single Save config action |
| `src/features/stacks/StackTemplateScreen.tsx` | *(rewrite)* installed template config only |
| `src/features/stacks/AddStackTemplateScreen.tsx` | *(create)* repo-grouped picker + variables + install |
| `src/features/stacks/UpgradeStackTemplateScreen.tsx` | *(create)* target picker + variable diff + upgrade |
| `src/app/router.tsx` | *(modify)* two new guarded routes |
| `src/styles/features.css` | *(modify)* styles for the new screens |

---

### Task 1: Pure functions in stackWorkflow

**Files:**
- Modify: `web/src/features/stacks/stackWorkflow.ts`
- Test: `web/src/features/stacks/stackWorkflow.test.ts`

**Interfaces:**
- Consumes: existing `canUpgradeStackTemplate`, `configFromVariableValues`, `variableValuesFromConfig` from the same module.
- Produces:
  ```ts
  upgradeCandidateRevisions(revisions: TemplateRevision[], stackTemplate: StackTemplate | null): TemplateRevision[]
  partitionUpgradeVariables(currentVariables: TemplateVariable[], targetVariables: TemplateVariable[]): UpgradeVariablePartition
  canSaveInstalledTemplateConfig(stackTemplate: StackTemplate | null, variables: TemplateVariable[], variableValues: Record<string, string>): boolean

  export interface UpgradeVariablePartition {
    added: TemplateVariable[];
    carried: TemplateVariable[];
    removed: TemplateVariable[];
  }
  ```

- [ ] **Step 1: Write the failing tests**

Append to `web/src/features/stacks/stackWorkflow.test.ts`. The file already has `stackTemplate()`, `templateRevision()`, and `variable()` factories at the bottom — reuse them; do not redefine them.

Add the three new names to the existing import block at the top of the file:

```ts
import {
  canDestroyStackTemplate,
  canSaveInstalledTemplateConfig,
  canUpgradeStackTemplate,
  canSaveStackTemplateConfig,
  configFromVariableValues,
  findSelectedStack,
  findSelectedStackTemplate,
  isDestroyingStackTemplate,
  nextSelectedStackID,
  nextSelectedStackTemplateID,
  partitionUpgradeVariables,
  stackLabel,
  stackTemplateLabel,
  upgradeCandidateRevisions,
  upsertStackTemplate,
  variableValuesFromConfig
} from "./stackWorkflow";
```

Then append these tests inside the existing `describe("stack workflow helpers", ...)` block:

```ts
  it("offers only active revisions of the same source template as upgrade candidates", () => {
    const installed = stackTemplate({ source_template_id: "src_1", desired_template_revision_id: "rev_current" });
    const revisions = [
      templateRevision({ id: "rev_new", source_template_id: "src_1", status: "active" }),
      templateRevision({ id: "rev_current", source_template_id: "src_1", status: "active" }),
      templateRevision({ id: "rev_other_source", source_template_id: "src_2", status: "active" }),
      templateRevision({ id: "rev_pending", source_template_id: "src_1", status: "pending_validation" })
    ];

    expect(upgradeCandidateRevisions(revisions, installed).map((item) => item.id)).toEqual(["rev_new"]);
  });

  it("preserves the newest-first order the API returns for upgrade candidates", () => {
    const installed = stackTemplate({ source_template_id: "src_1", desired_template_revision_id: "rev_current" });
    const revisions = [
      templateRevision({ id: "rev_newest", source_template_id: "src_1", status: "active" }),
      templateRevision({ id: "rev_older", source_template_id: "src_1", status: "active" }),
      templateRevision({ id: "rev_current", source_template_id: "src_1", status: "active" })
    ];

    expect(upgradeCandidateRevisions(revisions, installed).map((item) => item.id)).toEqual(["rev_newest", "rev_older"]);
  });

  it("has no upgrade candidates without an installed template", () => {
    expect(upgradeCandidateRevisions([templateRevision({ id: "rev_new" })], null)).toEqual([]);
  });

  it("splits upgrade variables into added, carried, and removed by name", () => {
    const current = [variable({ name: "region" }), variable({ name: "legacy_flag" })];
    const target = [variable({ name: "region" }), variable({ name: "new_var" })];

    const partition = partitionUpgradeVariables(current, target);

    expect(partition.added.map((item) => item.name)).toEqual(["new_var"]);
    expect(partition.carried.map((item) => item.name)).toEqual(["region"]);
    expect(partition.removed.map((item) => item.name)).toEqual(["legacy_flag"]);
  });

  it("carries every variable when the revision's variable set is unchanged", () => {
    const variables = [variable({ name: "region" }), variable({ name: "cidr_block" })];

    const partition = partitionUpgradeVariables(variables, variables);

    expect(partition.carried.map((item) => item.name)).toEqual(["region", "cidr_block"]);
    expect(partition.added).toEqual([]);
    expect(partition.removed).toEqual([]);
  });

  it("treats a fully disjoint variable set as all added and all removed", () => {
    const partition = partitionUpgradeVariables([variable({ name: "old" })], [variable({ name: "new" })]);

    expect(partition.added.map((item) => item.name)).toEqual(["new"]);
    expect(partition.carried).toEqual([]);
    expect(partition.removed.map((item) => item.name)).toEqual(["old"]);
  });

  it("returns the target revision's variable definitions for carried variables", () => {
    const current = [variable({ name: "region", template_revision_id: "rev_current", required: false })];
    const target = [variable({ name: "region", template_revision_id: "rev_target", required: true })];

    const partition = partitionUpgradeVariables(current, target);

    expect(partition.carried[0].template_revision_id).toBe("rev_target");
    expect(partition.carried[0].required).toBe(true);
  });

  it("enables saving installed config only when a value differs from what is stored", () => {
    const installed = stackTemplate({ config: { region: "us-east-1" } });
    const variables = [variable({ name: "region" })];

    expect(canSaveInstalledTemplateConfig(installed, variables, { region: "us-east-1" })).toBe(false);
    expect(canSaveInstalledTemplateConfig(installed, variables, { region: "eu-west-1" })).toBe(true);
  });

  it("does not enable saving installed config without an installed template", () => {
    expect(canSaveInstalledTemplateConfig(null, [variable({ name: "region" })], { region: "eu-west-1" })).toBe(false);
  });
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `npm test -- src/features/stacks/stackWorkflow.test.ts`
Expected: FAIL — `upgradeCandidateRevisions is not a function` (and the same for the other two new names).

- [ ] **Step 3: Implement the three functions**

Append to `web/src/features/stacks/stackWorkflow.ts`, after `canSaveStackTemplateConfig`:

```ts
export interface UpgradeVariablePartition {
  added: TemplateVariable[];
  carried: TemplateVariable[];
  removed: TemplateVariable[];
}

/**
 * Revisions this stack template can be upgraded to: active revisions of the
 * same source template, minus the one already desired. The API returns
 * revisions newest-first and that order is preserved, so the first candidate
 * is the newest.
 */
export function upgradeCandidateRevisions(
  revisions: TemplateRevision[],
  stackTemplate: StackTemplate | null
): TemplateRevision[] {
  if (!stackTemplate) {
    return [];
  }
  return revisions.filter((revision) => canUpgradeStackTemplate(stackTemplate, revision));
}

/**
 * Compares the installed revision's variables against a target revision's, by
 * name. Carried variables carry the *target* revision's definition, because
 * that is what the upgrade will be validated against — a variable can stay
 * named the same while becoming required or changing type.
 */
export function partitionUpgradeVariables(
  currentVariables: TemplateVariable[],
  targetVariables: TemplateVariable[]
): UpgradeVariablePartition {
  const currentNames = new Set(currentVariables.map((variable) => variable.name));
  const targetNames = new Set(targetVariables.map((variable) => variable.name));
  return {
    added: targetVariables.filter((variable) => !currentNames.has(variable.name)),
    carried: targetVariables.filter((variable) => currentNames.has(variable.name)),
    removed: currentVariables.filter((variable) => !targetNames.has(variable.name))
  };
}

/**
 * Save is available when the edited values differ from what is stored on the
 * stack template. Unlike canSaveStackTemplateConfig this takes no revision:
 * the template screen always renders the installed template's desired
 * revision, so there is no selection that could disagree with it.
 */
export function canSaveInstalledTemplateConfig(
  stackTemplate: StackTemplate | null,
  variables: TemplateVariable[],
  variableValues: Record<string, string>
): boolean {
  if (!stackTemplate) {
    return false;
  }
  const currentConfig = configFromVariableValues(variables, variableValues);
  const savedConfig = configFromVariableValues(variables, variableValuesFromConfig(stackTemplate.config, variables));
  return !areStringRecordEqual(currentConfig, savedConfig);
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `npm test -- src/features/stacks/stackWorkflow.test.ts`
Expected: PASS, including the pre-existing `canSaveStackTemplateConfig` tests, which must be untouched.

- [ ] **Step 5: Commit**

```bash
git add src/features/stacks/stackWorkflow.ts src/features/stacks/stackWorkflow.test.ts
git commit -m "$(cat <<'EOF'
feat: add upgrade candidate and variable partition helpers

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: VariableFields and StackTemplateConfigPanel

**Files:**
- Create: `web/src/features/stacks/VariableFields.tsx`
- Create: `web/src/features/stacks/StackTemplateConfigPanel.tsx`
- Test: `web/src/features/stacks/StackTemplateConfigPanel.test.tsx`

**Interfaces:**
- Consumes: `TemplateVariable` from `src/api/types`.
- Produces:
  ```tsx
  <VariableFields
    variables={TemplateVariable[]}
    variableValues={Record<string, string>}
    onVariableValueChange={(name: string, value: string) => void}
    disabled={boolean}          // optional, defaults false
    emptyMessage={string}       // optional, defaults "No variables loaded"
  />

  <StackTemplateConfigPanel
    variables={TemplateVariable[]}
    variableValues={Record<string, string>}
    onVariableValueChange={(name: string, value: string) => void}
    canSave={boolean}
    onSave={() => void}
    saveBusy={boolean}
    disabledReason={string | undefined}   // when set, everything renders locked
  />
  ```

`VariableFields` renders the exact markup `VariablesPanel` uses today (`div.variable-grid` of `label` + `input`), so `screen.getByLabelText(/region/)` keeps working in every test.

- [ ] **Step 1: Write the failing test**

Create `web/src/features/stacks/StackTemplateConfigPanel.test.tsx`:

```tsx
// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { TemplateVariable } from "../../api/types";
import StackTemplateConfigPanel from "./StackTemplateConfigPanel";

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

function renderPanel(overrides: Partial<React.ComponentProps<typeof StackTemplateConfigPanel>> = {}) {
  const props = {
    variables: [variable()],
    variableValues: { region: "us-east-1" },
    onVariableValueChange: vi.fn(),
    canSave: true,
    onSave: vi.fn(),
    saveBusy: false,
    ...overrides
  };
  render(<StackTemplateConfigPanel {...props} />);
  return props;
}

describe("StackTemplateConfigPanel", () => {
  afterEach(cleanup);

  it("offers exactly one action", () => {
    renderPanel();

    expect(screen.getAllByRole("button")).toHaveLength(1);
    expect(screen.getByRole("button", { name: /Save config/ })).toBeTruthy();
  });

  it("reports edits to a variable value", () => {
    const props = renderPanel();

    fireEvent.change(screen.getByLabelText(/region/), { target: { value: "eu-west-1" } });

    expect(props.onVariableValueChange).toHaveBeenCalledWith("region", "eu-west-1");
  });

  it("disables save when canSave is false", () => {
    renderPanel({ canSave: false });

    expect((screen.getByRole("button", { name: /Save config/ }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("locks every input and the action when a disabled reason is given", () => {
    renderPanel({ disabledReason: "Editing requires operator access" });

    expect(screen.getByTestId("variables-disabled-reason").textContent).toContain("Editing requires operator access");
    expect((screen.getByRole("button", { name: /Save config/ }) as HTMLButtonElement).disabled).toBe(true);
    expect((screen.getByLabelText(/region/) as HTMLInputElement).disabled).toBe(true);
  });

  it("renders a message instead of a grid when the revision has no variables", () => {
    renderPanel({ variables: [], variableValues: {} });

    expect(screen.getByText("No variables loaded")).toBeTruthy();
  });
});
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `npm test -- src/features/stacks/StackTemplateConfigPanel.test.tsx`
Expected: FAIL — cannot resolve `./StackTemplateConfigPanel`.

- [ ] **Step 3: Create VariableFields**

Create `web/src/features/stacks/VariableFields.tsx`:

```tsx
import type { TemplateVariable } from "../../api/types";

interface VariableFieldsProps {
  variables: TemplateVariable[];
  variableValues: Record<string, string>;
  onVariableValueChange: (name: string, value: string) => void;
  disabled?: boolean;
  emptyMessage?: string;
}

/**
 * The variable input grid, and nothing else — no mutations, no revision
 * selection, no actions. Shared by the template, add, and upgrade screens so
 * all three render variables identically.
 */
export default function VariableFields({
  variables,
  variableValues,
  onVariableValueChange,
  disabled = false,
  emptyMessage = "No variables loaded"
}: VariableFieldsProps) {
  if (variables.length === 0) {
    return <p className="muted">{emptyMessage}</p>;
  }
  return (
    <div className="variable-grid">
      {variables.map((variable) => (
        <label key={variable.name}>
          {variable.name}
          {variable.required ? " *" : ""}
          <input
            value={variableValues[variable.name] ?? ""}
            onChange={(event) => onVariableValueChange(variable.name, event.target.value)}
            placeholder={variable.type_expression || "value"}
            disabled={disabled}
          />
        </label>
      ))}
    </div>
  );
}
```

- [ ] **Step 4: Create StackTemplateConfigPanel**

Create `web/src/features/stacks/StackTemplateConfigPanel.tsx`:

```tsx
import { Loader2, Save } from "lucide-react";
import type { TemplateVariable } from "../../api/types";
import VariableFields from "./VariableFields";

interface StackTemplateConfigPanelProps {
  variables: TemplateVariable[];
  variableValues: Record<string, string>;
  onVariableValueChange: (name: string, value: string) => void;
  canSave: boolean;
  onSave: () => void;
  saveBusy: boolean;
  // When set, the inputs and the action render disabled and the reason is
  // shown — the capability gate's denied-with-reason state (AUTH-020).
  disabledReason?: string;
}

/**
 * Variables for the installed template's desired revision, with a single
 * action. Installing and upgrading live on their own screens, so this panel
 * never has to switch modes.
 */
export default function StackTemplateConfigPanel({
  variables,
  variableValues,
  onVariableValueChange,
  canSave,
  onSave,
  saveBusy,
  disabledReason
}: StackTemplateConfigPanelProps) {
  const locked = Boolean(disabledReason);
  return (
    <section className="panel wide" data-testid="stack-template-config">
      <h2>Variables</h2>
      <VariableFields
        variables={variables}
        variableValues={variableValues}
        onVariableValueChange={onVariableValueChange}
        disabled={locked}
      />
      <div className="button-row form-actions">
        <button className="primary-button" disabled={locked || !canSave || saveBusy} onClick={onSave} type="button">
          {saveBusy ? <Loader2 size={16} className="spin" /> : <Save size={16} />}
          Save config
        </button>
      </div>
      {disabledReason && (
        <p className="muted" data-testid="variables-disabled-reason">
          {disabledReason}
        </p>
      )}
    </section>
  );
}
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `npm test -- src/features/stacks/StackTemplateConfigPanel.test.tsx`
Expected: PASS (5 tests).

- [ ] **Step 6: Commit**

```bash
git add src/features/stacks/VariableFields.tsx src/features/stacks/StackTemplateConfigPanel.tsx src/features/stacks/StackTemplateConfigPanel.test.tsx
git commit -m "$(cat <<'EOF'
feat: add single-action stack template config panel

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 3: Rewrite StackTemplateScreen

**Files:**
- Modify: `web/src/features/stacks/StackTemplateScreen.tsx`
- Modify: `web/src/features/stacks/StackTemplateScreen.test.tsx`
- Modify: `web/src/styles/features.css`

**Interfaces:**
- Consumes: `canSaveInstalledTemplateConfig`, `upgradeCandidateRevisions` (Task 1); `StackTemplateConfigPanel` (Task 2).
- Produces: routes `template/new` and `template/:stackTemplateId/upgrade` are linked from here; both are registered in Tasks 4 and 5. **The links will 404 until those tasks land — that is expected.** Selection is published as `?selected=<stackTemplateId>`.

**Three behavior changes to get right:**

1. **Loading no longer waits on the revisions query.** It gates on the stack query, plus the variables query *only when a template is installed* (the variables query is `enabled: false` with an empty revision ID, so it stays `pending` forever when nothing is installed and would hang the screen).
2. **The revisions query's error is non-fatal.** The error boundary reads `stackQuery.error ?? variablesQuery.error`. A failed revisions query only hides the update cue.
3. **Selection comes from `?selected=`,** falling back to the first installed template.

- [ ] **Step 1: Rewrite the test file**

Replace the whole of `web/src/features/stacks/StackTemplateScreen.test.tsx`. Keep lines 1–134 of the existing file exactly as they are — the imports, the `allAllowed` constant, the `stackView` / `stackTemplate` / `templateRevision` / `variable` / `testQueryClient` / `seedDefaultData` / `authValue` / `actionButton` helpers, and the `afterEach` — with one edit, described next.

`seedDefaultData` needs no change: `templateRevision()` already defaults `source_template_id` to `"tmpl_src_1"`, which matches `stackTemplate()`, so the seeded `rev_2` is already a valid upgrade candidate for the installed template.

The one edit is to `renderScreen`, which takes an initial URL so the `?selected=` test can drive it:

```tsx
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
```

Then replace every test in the `describe("StackTemplateScreen", ...)` block with:

```tsx
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

  it("offers an upgrade link when a newer active revision of the same source exists", () => {
    const queryClient = testQueryClient();
    seedDefaultData(queryClient);

    renderScreen(queryClient);

    expect(screen.getByTestId("stack-template-update-available")).toBeTruthy();
    expect(screen.getByTestId("upgrade-stack-template-link").getAttribute("href")).toBe(
      "/stacks/stack_1/template/st_1/upgrade"
    );
  });

  it("reports being up to date when the only revision is the installed one", () => {
    const queryClient = testQueryClient();
    seedDefaultData(queryClient);
    queryClient.setQueryData(queryKeys.templateRevisions("tenant_123"), [templateRevision()]);

    renderScreen(queryClient);

    expect(screen.getByTestId("stack-template-up-to-date")).toBeTruthy();
    expect(screen.queryByTestId("upgrade-stack-template-link")).toBeNull();
  });

  it("ignores revisions of a different source template when deciding the update cue", () => {
    const queryClient = testQueryClient();
    seedDefaultData(queryClient);
    queryClient.setQueryData(queryKeys.templateRevisions("tenant_123"), [
      templateRevision(),
      templateRevision({ id: "rev_elsewhere", source_template_id: "tmpl_src_2" })
    ]);

    renderScreen(queryClient);

    expect(screen.getByTestId("stack-template-up-to-date")).toBeTruthy();
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
    expect(screen.queryByTestId("upgrade-stack-template-link")).toBeNull();
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

  it("still shows the screen when only the revisions query fails", async () => {
    const queryClient = testQueryClient();
    seedDefaultData(queryClient);
    // removeQueries, not setQueryData(..., undefined) — TanStack Query
    // ignores an undefined value, which would leave the seeded list in place.
    queryClient.removeQueries({ queryKey: queryKeys.templateRevisions("tenant_123") });
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({ error: "unauthorized", message: "unauthorized" }), {
        status: 401,
        headers: { "content-type": "application/json" }
      })
    );

    renderScreen(queryClient);

    await waitFor(() => expect(screen.getByTestId("stack-template-config")).toBeTruthy());
    expect(screen.queryByTestId("stack-template-update-available")).toBeNull();
    expect(screen.queryByTestId("stack-template-up-to-date")).toBeNull();
  });

  it("shows Destroying badge when lifecycle is destroying", () => {
    const queryClient = testQueryClient();
    seedDefaultData(queryClient, allAllowed);
    queryClient.setQueryData(queryKeys.stack("tenant_123", "stack_1"),
      stackView(allAllowed, [stackTemplate({ lifecycle: "destroying" })]));

    renderScreen(queryClient);

    expect(screen.getByText("Destroying…")).toBeTruthy();
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
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `npm test -- src/features/stacks/StackTemplateScreen.test.tsx`
Expected: FAIL — no `add-stack-template-link`, no `stack-template-config`, and the revision select still present.

- [ ] **Step 3: Rewrite the screen**

Replace the whole of `web/src/features/stacks/StackTemplateScreen.tsx`:

```tsx
import { useState } from "react";
import { ArrowUpCircle, CheckCircle2, Loader2, Plus, RefreshCw } from "lucide-react";
import { Link, useParams, useSearchParams } from "react-router-dom";
import {
  useCreateStackTemplateCredentialMutation,
  useDeleteStackTemplateCredentialMutation,
  useStackQuery,
  useStackTemplateCredentialsQuery,
  useTemplateRevisionVariablesQuery,
  useTemplateRevisionsQuery,
  useUpdateStackTemplateConfigMutation
} from "../../api/queries";
import { tenantID } from "../../config";
import RequireCapability from "../../auth/RequireCapability";
import { useQueryErrorBoundary } from "../../shared/queryErrorBoundary";
import StackTemplateConfigPanel from "./StackTemplateConfigPanel";
import CredentialsPanel from "./CredentialsPanel";
import {
  canSaveInstalledTemplateConfig,
  configFromVariableValues,
  findSelectedStackTemplate,
  isDestroyingStackTemplate,
  stackTemplateLabel,
  upgradeCandidateRevisions,
  variableValuesFromConfig
} from "./stackWorkflow";

// /stacks/:stackId/template — configures the installed template and nothing
// else. Installing lives at template/new and upgrading at
// template/:stackTemplateId/upgrade, so this screen has no revision selector
// and its form has exactly one action.
//
// Selection is URL state (?selected=<stackTemplateID>) rather than component
// state, so the add and upgrade screens can hand back the template they just
// acted on and a selected row survives a reload.
export default function StackTemplateScreen() {
  const { stackId = "" } = useParams<{ stackId: string }>();
  const [searchParams, setSearchParams] = useSearchParams();
  const [editedValues, setEditedValues] = useState<Record<string, string>>({});
  const [errorMessage, setErrorMessage] = useState("");

  const stackQuery = useStackQuery(tenantID, stackId);
  const stackTemplates = stackQuery.data?.templates ?? [];
  const installedTemplate =
    findSelectedStackTemplate(stackTemplates, searchParams.get("selected") ?? "") ?? stackTemplates[0] ?? null;

  const variablesQuery = useTemplateRevisionVariablesQuery(
    tenantID,
    installedTemplate?.desired_template_revision_id ?? ""
  );
  const variables = variablesQuery.data ?? [];

  // Answers one question only: does a newer active revision of this
  // template's source template exist? A failure here costs the update cue,
  // not the screen, so it is deliberately absent from the error boundary.
  const templateRevisionsQuery = useTemplateRevisionsQuery(tenantID);
  const upgradeCandidates = upgradeCandidateRevisions(templateRevisionsQuery.data ?? [], installedTemplate);
  const updateCueReady = installedTemplate !== null && templateRevisionsQuery.status === "success";

  const boundary = useQueryErrorBoundary(stackQuery.error ?? variablesQuery.error);

  // Displayed values are the installed config overlaid with unsaved edits.
  const baseValues = installedTemplate ? variableValuesFromConfig(installedTemplate.config, variables) : {};
  const variableValues: Record<string, string> = {};
  for (const variable of variables) {
    variableValues[variable.name] = editedValues[variable.name] ?? baseValues[variable.name] ?? "";
  }

  const destroying = isDestroyingStackTemplate(installedTemplate);
  const updateStackTemplateConfigMutation = useUpdateStackTemplateConfigMutation(tenantID, stackId);
  const templateCredentialsQuery = useStackTemplateCredentialsQuery(tenantID, installedTemplate?.id ?? "");
  const createTemplateCredentialMutation = useCreateStackTemplateCredentialMutation(tenantID, installedTemplate?.id ?? "");
  const deleteTemplateCredentialMutation = useDeleteStackTemplateCredentialMutation(tenantID, installedTemplate?.id ?? "");

  const canSaveConfig = canSaveInstalledTemplateConfig(installedTemplate, variables, variableValues);

  function handleSelectStackTemplate(stackTemplateID: string) {
    if (stackTemplateID === installedTemplate?.id) {
      return;
    }
    setSearchParams({ selected: stackTemplateID }, { replace: true });
    setEditedValues({});
  }

  function handleVariableValueChange(name: string, value: string) {
    setEditedValues((current) => ({ ...current, [name]: value }));
  }

  async function handleSaveStackTemplateConfig() {
    if (!installedTemplate || !canSaveConfig) {
      return;
    }
    setErrorMessage("");
    try {
      await updateStackTemplateConfigMutation.mutateAsync({
        stackTemplateID: installedTemplate.id,
        body: { config: configFromVariableValues(variables, variableValues) }
      });
      setEditedValues({});
    } catch (error) {
      setErrorMessage(error instanceof Error ? error.message : "Request failed");
    }
  }

  /** Persists a credential that overrides the Stack value for this template only. */
  async function createTemplateCredential(name: string, value: string) {
    await createTemplateCredentialMutation.mutateAsync({ name, value });
  }

  // The variables query is disabled without an installed template, so it
  // never leaves "pending" — only wait on it once something is installed.
  const waitingOnVariables = installedTemplate !== null && variablesQuery.status === "pending";
  if (stackQuery.status === "pending" || waitingOnVariables) {
    return (
      <section className="stack-template-screen" data-testid="stack-template-loading">
        <p className="muted">
          <Loader2 size={16} className="spin" /> Loading template…
        </p>
      </section>
    );
  }

  if (stackQuery.status === "error" || variablesQuery.status === "error") {
    if (boundary !== null) {
      return <>{boundary}</>;
    }
    return (
      <section className="stack-template-screen" data-testid="stack-template-error">
        <p className="muted">Something went wrong while loading the stack template.</p>
        <button
          className="primary-button"
          type="button"
          data-testid="stack-template-retry"
          onClick={() => {
            stackQuery.refetch();
            variablesQuery.refetch();
          }}
        >
          <RefreshCw size={16} />
          Retry
        </button>
      </section>
    );
  }

  const addTemplateLink = (
    <Link className="primary-button" to={`/stacks/${stackId}/template/new`} data-testid="add-stack-template-link">
      <Plus size={16} />
      Add template
    </Link>
  );

  return (
    <section className="stack-template-screen" data-testid="stack-template-screen">
      {errorMessage && <div className="alert">{errorMessage}</div>}
      <div className="workflow-grid">
        <section className="panel">
          <header className="panel-header">
            <h2>Stack templates</h2>
            <RequireCapability capability="canOperate">{addTemplateLink}</RequireCapability>
          </header>
          {stackTemplates.length === 0 ? (
            <p className="muted" data-testid="stack-template-empty">
              No stack templates installed
            </p>
          ) : (
            <div className="stack-template-items">
              {stackTemplates.map((item) => (
                <button
                  className={item.id === installedTemplate?.id ? "active" : ""}
                  key={item.id}
                  onClick={() => handleSelectStackTemplate(item.id)}
                  type="button"
                >
                  <span>{stackTemplateLabel(item)}</span>
                  {item.lifecycle === "destroying" && (
                    <small className="lifecycle-badge destroying">Destroying…</small>
                  )}
                  <small>{item.desired_template_revision_id}</small>
                </button>
              ))}
            </div>
          )}
        </section>
        {installedTemplate && (
          <>
            {updateCueReady && (
              <section className="panel stack-template-update">
                {upgradeCandidates.length === 0 ? (
                  <p className="muted" data-testid="stack-template-up-to-date">
                    <CheckCircle2 size={16} />
                    Up to date
                  </p>
                ) : (
                  <>
                    <p data-testid="stack-template-update-available">
                      <ArrowUpCircle size={16} />
                      Update available
                    </p>
                    <RequireCapability capability="canOperate">
                      <Link
                        className="secondary-button"
                        to={`/stacks/${stackId}/template/${installedTemplate.id}/upgrade`}
                        data-testid="upgrade-stack-template-link"
                      >
                        Upgrade
                      </Link>
                    </RequireCapability>
                  </>
                )}
              </section>
            )}
            <RequireCapability
              capability="canOperate"
              fallback={
                <StackTemplateConfigPanel
                  variables={variables}
                  variableValues={variableValues}
                  onVariableValueChange={handleVariableValueChange}
                  canSave={false}
                  onSave={handleSaveStackTemplateConfig}
                  saveBusy={false}
                  disabledReason="Editing requires operator access"
                />
              }
            >
              <StackTemplateConfigPanel
                variables={variables}
                variableValues={variableValues}
                onVariableValueChange={handleVariableValueChange}
                canSave={canSaveConfig}
                onSave={handleSaveStackTemplateConfig}
                saveBusy={updateStackTemplateConfigMutation.isPending}
                disabledReason={destroying ? "Destroy in progress" : undefined}
              />
            </RequireCapability>
            <RequireCapability capability="canManageAccess">
              <CredentialsPanel
                title="Template credentials"
                credentials={templateCredentialsQuery.data ?? []}
                loading={templateCredentialsQuery.isPending}
                busy={createTemplateCredentialMutation.isPending || deleteTemplateCredentialMutation.isPending}
                onCreate={createTemplateCredential}
                onDelete={(id) => deleteTemplateCredentialMutation.mutateAsync(id)}
              />
            </RequireCapability>
          </>
        )}
      </div>
    </section>
  );
}
```

Delete the now-unused `InstalledTemplatePanel` import — the file itself stays on disk for `App.tsx`.

- [ ] **Step 4: Add the styles**

Append to `web/src/styles/features.css`. Use tokens only — no hex values, or `styles.guard.test.ts` fails.

```css
.panel-header {
  align-items: center;
  display: flex;
  gap: 1rem;
  justify-content: space-between;
}

.panel-header h2 {
  margin: 0;
}

.stack-template-update {
  align-items: center;
  display: flex;
  gap: 0.75rem;
  justify-content: space-between;
}

.stack-template-update p {
  align-items: center;
  display: flex;
  gap: 0.5rem;
  margin: 0;
}

.stack-template-update [data-testid="stack-template-update-available"] {
  color: var(--color-accent);
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `npm test -- src/features/stacks/StackTemplateScreen.test.tsx src/styles/styles.guard.test.ts`
Expected: PASS.

- [ ] **Step 6: Confirm the legacy console still compiles**

Run: `npm run build`
Expected: success. If it reports an error in `src/App.tsx`, you changed a shared signature — revert that change; `App.tsx` is off limits.

- [ ] **Step 7: Commit**

```bash
git add src/features/stacks/StackTemplateScreen.tsx src/features/stacks/StackTemplateScreen.test.tsx src/styles/features.css
git commit -m "$(cat <<'EOF'
feat: reduce stack template screen to installed template config

Removes the raw-ID panel, the tenant-wide revision dropdown, and the
always-present install button. Adds an update-available cue linking to
the dedicated upgrade screen.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 4: AddStackTemplateScreen

**Files:**
- Create: `web/src/features/stacks/AddStackTemplateScreen.tsx`
- Test: `web/src/features/stacks/AddStackTemplateScreen.test.tsx`
- Modify: `web/src/app/router.tsx`
- Modify: `web/src/app/router.test.tsx`
- Modify: `web/src/styles/features.css`

**Interfaces:**
- Consumes: `groupTemplateRevisionsByRepository`, `templateRevisionRefLabel` from `../templates/templateWorkflow`; `configFromVariableValues` from `./stackWorkflow`; `VariableFields` (Task 2).
- Produces: route `stacks/:stackId/template/new`, linked from Task 3. On success it navigates to `/stacks/:stackId/template?selected=<newStackTemplateID>`.

The install request body is `{ template_revision_id, selected_ref, config }` — the shape `AddTemplateToStackRequest` declares in `src/api/client.ts:46`.

- [ ] **Step 1: Write the failing test**

Create `web/src/features/stacks/AddStackTemplateScreen.test.tsx`:

```tsx
// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import AddStackTemplateScreen from "./AddStackTemplateScreen";
import { queryKeys } from "../../api/queryKeys";
import type { TemplateRevision, TemplateVariable } from "../../api/types";

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

function LocationProbe() {
  const location = useLocation();
  return <span data-testid="location">{`${location.pathname}${location.search}`}</span>;
}

function renderScreen(queryClient: QueryClient) {
  return render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter initialEntries={["/stacks/stack_1/template/new"]}>
        <Routes>
          <Route path="/stacks/:stackId/template/new" element={<AddStackTemplateScreen />} />
          <Route path="/stacks/:stackId/template" element={<LocationProbe />} />
        </Routes>
      </MemoryRouter>
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

  it("points at template registration when the tenant has none", () => {
    const queryClient = testQueryClient();
    queryClient.setQueryData(queryKeys.templateRevisions("tenant_123"), []);

    renderScreen(queryClient);

    expect(screen.getByTestId("add-stack-template-none")).toBeTruthy();
    expect(screen.getByTestId("register-template-link").getAttribute("href")).toBe("/templates/new");
  });
});
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `npm test -- src/features/stacks/AddStackTemplateScreen.test.tsx`
Expected: FAIL — cannot resolve `./AddStackTemplateScreen`.

- [ ] **Step 3: Create the screen**

Create `web/src/features/stacks/AddStackTemplateScreen.tsx`:

```tsx
import { useState } from "react";
import { ArrowLeft, Loader2, RefreshCw, ShieldCheck } from "lucide-react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { useAddTemplateToStackMutation, useTemplateRevisionVariablesQuery, useTemplateRevisionsQuery } from "../../api/queries";
import { tenantID } from "../../config";
import { useQueryErrorBoundary } from "../../shared/queryErrorBoundary";
import SectionLabel from "../../shared/SectionLabel";
import { groupTemplateRevisionsByRepository, templateRevisionRefLabel } from "../templates/templateWorkflow";
import { configFromVariableValues } from "./stackWorkflow";
import VariableFields from "./VariableFields";

// /stacks/:stackId/template/new — installing a template, extracted from the
// template screen where an always-present Install button sat next to an
// unrelated config form. The picker mirrors /templates so choosing here looks
// like browsing the registry.
export default function AddStackTemplateScreen() {
  const { stackId = "" } = useParams<{ stackId: string }>();
  const navigate = useNavigate();
  const [chosenRevisionID, setChosenRevisionID] = useState("");
  const [values, setValues] = useState<Record<string, string>>({});
  const [errorMessage, setErrorMessage] = useState("");

  const templateRevisionsQuery = useTemplateRevisionsQuery(tenantID);
  const boundary = useQueryErrorBoundary(templateRevisionsQuery.error);
  const templateRevisions = templateRevisionsQuery.data ?? [];
  const chosenRevision = templateRevisions.find((revision) => revision.id === chosenRevisionID) ?? null;

  const variablesQuery = useTemplateRevisionVariablesQuery(tenantID, chosenRevision?.id ?? "");
  const variables = variablesQuery.data ?? [];

  const addTemplateToStackMutation = useAddTemplateToStackMutation(tenantID, stackId);

  function handleChoose(revisionID: string) {
    setChosenRevisionID(revisionID);
    setValues({});
    setErrorMessage("");
  }

  async function handleInstall() {
    if (!chosenRevision) {
      return;
    }
    setErrorMessage("");
    try {
      const installed = await addTemplateToStackMutation.mutateAsync({
        template_revision_id: chosenRevision.id,
        selected_ref: chosenRevision.source_ref,
        config: configFromVariableValues(variables, values)
      });
      navigate(`/stacks/${stackId}/template?selected=${installed.id}`);
    } catch (error) {
      setErrorMessage(error instanceof Error ? error.message : "Request failed");
    }
  }

  if (templateRevisionsQuery.status === "pending") {
    return (
      <section className="add-stack-template-screen" data-testid="add-stack-template-loading">
        <p className="muted">
          <Loader2 size={16} className="spin" /> Loading templates…
        </p>
      </section>
    );
  }

  if (templateRevisionsQuery.status === "error") {
    if (boundary !== null) {
      return <>{boundary}</>;
    }
    return (
      <section className="add-stack-template-screen" data-testid="add-stack-template-load-error">
        <p className="muted">Something went wrong while loading templates.</p>
        <button
          className="primary-button"
          type="button"
          data-testid="add-stack-template-retry"
          onClick={() => templateRevisionsQuery.refetch()}
        >
          <RefreshCw size={16} />
          Retry
        </button>
      </section>
    );
  }

  return (
    <section className="add-stack-template-screen" data-testid="add-stack-template-screen">
      <header className="page-header">
        <Link to={`/stacks/${stackId}/template`} className="muted back-link">
          <ArrowLeft size={14} />
          Back to templates
        </Link>
        <SectionLabel>Add template</SectionLabel>
        <h1>Add a template to this stack</h1>
      </header>

      {errorMessage && (
        <div className="alert" data-testid="add-stack-template-error">
          {errorMessage}
        </div>
      )}

      {templateRevisions.length === 0 ? (
        <section className="panel" data-testid="add-stack-template-none">
          <p className="muted">No templates are registered for this tenant yet.</p>
          <Link className="primary-button" to="/templates/new" data-testid="register-template-link">
            Register template
          </Link>
        </section>
      ) : (
        <div className="templates-groups">
          {groupTemplateRevisionsByRepository(templateRevisions).map((group) => (
            <section className="templates-group" key={group.key} data-testid={`template-group-${group.key}`}>
              <h2 className="templates-group__heading">
                <span className="templates-group__repo">
                  {group.repoOwner}/{group.repoName}
                </span>
              </h2>
              <ul className="templates-list">
                {group.templateRevisions.map((templateRevision) => (
                  <li key={templateRevision.id}>
                    <button
                      type="button"
                      className="template-choice"
                      data-testid={`add-template-choice-${templateRevision.id}`}
                      data-selected={templateRevision.id === chosenRevisionID ? "true" : undefined}
                      disabled={templateRevision.status !== "active"}
                      onClick={() => handleChoose(templateRevision.id)}
                    >
                      <span className="templates-list__name">{templateRevisionRefLabel(templateRevision)}</span>
                      <small className="muted">{templateRevision.status}</small>
                    </button>
                  </li>
                ))}
              </ul>
            </section>
          ))}
        </div>
      )}

      {chosenRevision && (
        <section className="panel wide" data-testid="add-stack-template-variables">
          <h2>Variables</h2>
          <VariableFields
            variables={variables}
            variableValues={values}
            onVariableValueChange={(name, value) => setValues((current) => ({ ...current, [name]: value }))}
            emptyMessage="This template declares no variables"
          />
          <div className="button-row form-actions">
            <button
              className="primary-button"
              type="button"
              disabled={addTemplateToStackMutation.isPending}
              onClick={handleInstall}
            >
              {addTemplateToStackMutation.isPending ? <Loader2 size={16} className="spin" /> : <ShieldCheck size={16} />}
              Install
            </button>
          </div>
        </section>
      )}
    </section>
  );
}
```

- [ ] **Step 4: Add the styles**

Append to `web/src/styles/features.css` — tokens only:

```css
.template-choice {
  align-items: center;
  background: transparent;
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
  color: var(--color-fg);
  cursor: pointer;
  display: flex;
  gap: 0.75rem;
  justify-content: space-between;
  padding: 0.6rem 0.8rem;
  width: 100%;
}

.template-choice:disabled {
  color: var(--color-muted-fg);
  cursor: not-allowed;
}

.template-choice[data-selected="true"] {
  border-color: var(--color-accent-border);
  box-shadow: var(--shadow-ring);
}
```

- [ ] **Step 5: Register the route**

In `web/src/app/router.tsx`, add the import beside the other stack feature imports:

```tsx
import AddStackTemplateScreen from "../features/stacks/AddStackTemplateScreen";
```

Then add this child immediately after the `{ path: "template", element: <StackTemplateScreen /> }` entry:

```tsx
                  {
                    path: "template/new",
                    element: <RequireCapability capability="canOperate" mode="route" />,
                    children: [{ index: true, element: <AddStackTemplateScreen /> }]
                  },
```

- [ ] **Step 6: Cover the route**

Append this test inside the top-level `describe` in `web/src/app/router.test.tsx`:

```tsx
  it("renders the add template screen at /stacks/:stackId/template/new", async () => {
    vi.stubEnv("VITE_TFLIVE_TENANT_ID", "tenant_123");
    const { routeConfig } = await import("./router");
    const { createMemoryRouter, RouterProvider } = await import("react-router-dom");
    const { QueryClient, QueryClientProvider } = await import("@tanstack/react-query");
    const { queryKeys } = await import("../api/queryKeys");

    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
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
        effectiveCapabilities: { canView: true, canOperate: true, canApprove: true, canManageAccess: true }
      },
      templates: []
    });
    queryClient.setQueryData(queryKeys.templateRevisions("tenant_123"), []);

    const testRouter = createMemoryRouter(routeConfig, { initialEntries: ["/stacks/stack_1/template/new"] });
    const markup = renderToStaticMarkup(
      <QueryClientProvider client={queryClient}>
        <AuthContext.Provider value={authValue()}>
          <RouterProvider router={testRouter} />
        </AuthContext.Provider>
      </QueryClientProvider>
    );

    expect(markup).toContain('data-testid="add-stack-template-none"');
  });
```

If `createMemoryRouter`, `RouterProvider`, `renderToStaticMarkup`, `AuthContext`, or `authValue` are already imported at the top of that file, use the existing bindings instead of re-importing.

- [ ] **Step 7: Run the tests**

Run: `npm test -- src/features/stacks/AddStackTemplateScreen.test.tsx src/app/router.test.tsx src/styles/styles.guard.test.ts`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add src/features/stacks/AddStackTemplateScreen.tsx src/features/stacks/AddStackTemplateScreen.test.tsx src/app/router.tsx src/app/router.test.tsx src/styles/features.css
git commit -m "$(cat <<'EOF'
feat: add dedicated screen for installing a stack template

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 5: UpgradeStackTemplateScreen

**Files:**
- Create: `web/src/features/stacks/UpgradeStackTemplateScreen.tsx`
- Test: `web/src/features/stacks/UpgradeStackTemplateScreen.test.tsx`
- Modify: `web/src/app/router.tsx`
- Modify: `web/src/styles/features.css`

**Interfaces:**
- Consumes: `upgradeCandidateRevisions`, `partitionUpgradeVariables`, `configFromVariableValues`, `variableValuesFromConfig`, `findSelectedStackTemplate` (Task 1 and existing); `VariableFields` (Task 2).
- Produces: route `stacks/:stackId/template/:stackTemplateId/upgrade`, linked from Task 3. On success it navigates to `/stacks/:stackId/template?selected=<stackTemplateId>`.

The upgrade request body is `{ target_template_revision_id, config }` — `UpgradeStackTemplateRequest` in `src/api/client.ts:57`.

**The key behavior:** `config` is built from the *target* revision's variables only (`added` + `carried`), so a variable dropped by the new revision is excluded structurally rather than by convention.

- [ ] **Step 1: Write the failing test**

Create `web/src/features/stacks/UpgradeStackTemplateScreen.test.tsx`:

```tsx
// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import UpgradeStackTemplateScreen from "./UpgradeStackTemplateScreen";
import { queryKeys } from "../../api/queryKeys";
import type { StackTemplate, StackView, TemplateRevision, TemplateVariable } from "../../api/types";

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

function renderScreen(queryClient: QueryClient) {
  return render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter initialEntries={["/stacks/stack_1/template/st_1/upgrade"]}>
        <Routes>
          <Route path="/stacks/:stackId/template/:stackTemplateId/upgrade" element={<UpgradeStackTemplateScreen />} />
          <Route path="/stacks/:stackId/template" element={<LocationProbe />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>
  );
}

describe("UpgradeStackTemplateScreen", () => {
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it("preselects the newest candidate revision", () => {
    const queryClient = testQueryClient();
    seedUpgradeable(queryClient);

    renderScreen(queryClient);

    expect((screen.getByTestId("upgrade-target-select") as HTMLSelectElement).value).toBe("rev_next");
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
    fireEvent.click(screen.getByRole("button", { name: /Upgrade/ }));

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

  it("reports being on the latest revision when nothing can be upgraded to", () => {
    const queryClient = testQueryClient();
    seedUpgradeable(queryClient);
    queryClient.setQueryData(queryKeys.templateRevisions("tenant_123"), [templateRevision({ id: "rev_current" })]);

    renderScreen(queryClient);

    expect(screen.getByTestId("upgrade-already-latest")).toBeTruthy();
    expect(screen.queryByRole("button", { name: /Upgrade/ })).toBeNull();
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

    fireEvent.click(screen.getByRole("button", { name: /Upgrade/ }));

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
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `npm test -- src/features/stacks/UpgradeStackTemplateScreen.test.tsx`
Expected: FAIL — cannot resolve `./UpgradeStackTemplateScreen`.

- [ ] **Step 3: Create the screen**

Create `web/src/features/stacks/UpgradeStackTemplateScreen.tsx`:

```tsx
import { useState } from "react";
import { ArrowLeft, ArrowUpCircle, Loader2, RefreshCw } from "lucide-react";
import { Link, useNavigate, useParams } from "react-router-dom";
import {
  useStackQuery,
  useTemplateRevisionVariablesQuery,
  useTemplateRevisionsQuery,
  useUpgradeStackTemplateMutation
} from "../../api/queries";
import { tenantID } from "../../config";
import { useQueryErrorBoundary } from "../../shared/queryErrorBoundary";
import SectionLabel from "../../shared/SectionLabel";
import { templateRevisionLabel } from "../templates/templateWorkflow";
import {
  configFromVariableValues,
  findSelectedStackTemplate,
  partitionUpgradeVariables,
  stackTemplateLabel,
  upgradeCandidateRevisions,
  variableValuesFromConfig
} from "./stackWorkflow";
import VariableFields from "./VariableFields";

// /stacks/:stackId/template/:stackTemplateId/upgrade — moving an installed
// template to a different revision of the same source template. The old
// screen hid this behind a tenant-wide revision dropdown whose effect
// depended on an invisible source-template match; here the candidates are
// filtered to the valid ones and the variable changes are stated outright.
export default function UpgradeStackTemplateScreen() {
  const { stackId = "", stackTemplateId = "" } = useParams<{ stackId: string; stackTemplateId: string }>();
  const navigate = useNavigate();
  const [chosenTargetID, setChosenTargetID] = useState("");
  const [editedValues, setEditedValues] = useState<Record<string, string>>({});
  const [errorMessage, setErrorMessage] = useState("");

  const stackQuery = useStackQuery(tenantID, stackId);
  const templateRevisionsQuery = useTemplateRevisionsQuery(tenantID);
  const boundary = useQueryErrorBoundary(stackQuery.error ?? templateRevisionsQuery.error);

  const stackTemplate = findSelectedStackTemplate(stackQuery.data?.templates ?? [], stackTemplateId);
  const candidates = upgradeCandidateRevisions(templateRevisionsQuery.data ?? [], stackTemplate);
  const targetRevision = candidates.find((revision) => revision.id === chosenTargetID) ?? candidates[0] ?? null;

  const currentVariablesQuery = useTemplateRevisionVariablesQuery(
    tenantID,
    stackTemplate?.desired_template_revision_id ?? ""
  );
  const targetVariablesQuery = useTemplateRevisionVariablesQuery(tenantID, targetRevision?.id ?? "");
  const partition = partitionUpgradeVariables(currentVariablesQuery.data ?? [], targetVariablesQuery.data ?? []);

  // The config sent with the upgrade covers the target revision's variables
  // only, so anything the new revision dropped is excluded structurally.
  const targetVariables = [...partition.added, ...partition.carried];
  const baseValues = stackTemplate ? variableValuesFromConfig(stackTemplate.config, targetVariables) : {};
  const variableValues: Record<string, string> = {};
  for (const variable of targetVariables) {
    variableValues[variable.name] = editedValues[variable.name] ?? baseValues[variable.name] ?? "";
  }

  const upgradeStackTemplateMutation = useUpgradeStackTemplateMutation(tenantID, stackId);

  async function handleUpgrade() {
    if (!stackTemplate || !targetRevision) {
      return;
    }
    setErrorMessage("");
    try {
      await upgradeStackTemplateMutation.mutateAsync({
        stackTemplateID: stackTemplate.id,
        body: {
          target_template_revision_id: targetRevision.id,
          config: configFromVariableValues(targetVariables, variableValues)
        }
      });
      navigate(`/stacks/${stackId}/template?selected=${stackTemplate.id}`);
    } catch (error) {
      setErrorMessage(error instanceof Error ? error.message : "Request failed");
    }
  }

  if (stackQuery.status === "pending" || templateRevisionsQuery.status === "pending") {
    return (
      <section className="upgrade-stack-template-screen" data-testid="upgrade-loading">
        <p className="muted">
          <Loader2 size={16} className="spin" /> Loading revisions…
        </p>
      </section>
    );
  }

  if (stackQuery.status === "error" || templateRevisionsQuery.status === "error") {
    if (boundary !== null) {
      return <>{boundary}</>;
    }
    return (
      <section className="upgrade-stack-template-screen" data-testid="upgrade-load-error">
        <p className="muted">Something went wrong while loading revisions.</p>
        <button
          className="primary-button"
          type="button"
          data-testid="upgrade-retry"
          onClick={() => {
            stackQuery.refetch();
            templateRevisionsQuery.refetch();
          }}
        >
          <RefreshCw size={16} />
          Retry
        </button>
      </section>
    );
  }

  const backLink = (
    <Link to={`/stacks/${stackId}/template`} className="muted back-link">
      <ArrowLeft size={14} />
      Back to templates
    </Link>
  );

  if (!stackTemplate) {
    return (
      <section className="upgrade-stack-template-screen" data-testid="upgrade-template-missing">
        {backLink}
        <p className="muted">That template is not installed on this stack.</p>
      </section>
    );
  }

  return (
    <section className="upgrade-stack-template-screen" data-testid="upgrade-stack-template-screen">
      <header className="page-header">
        {backLink}
        <SectionLabel>Upgrade</SectionLabel>
        <h1>{stackTemplateLabel(stackTemplate)}</h1>
      </header>

      {errorMessage && (
        <div className="alert" data-testid="upgrade-stack-template-error">
          {errorMessage}
        </div>
      )}

      {candidates.length === 0 ? (
        <section className="panel" data-testid="upgrade-already-latest">
          <p className="muted">Already on the latest revision.</p>
        </section>
      ) : (
        <section className="panel wide">
          <label className="selector-label">
            Target revision
            <select
              data-testid="upgrade-target-select"
              value={targetRevision?.id ?? ""}
              onChange={(event) => {
                setChosenTargetID(event.target.value);
                setEditedValues({});
              }}
            >
              {candidates.map((candidate) => (
                <option key={candidate.id} value={candidate.id}>
                  {templateRevisionLabel(candidate)}
                </option>
              ))}
            </select>
          </label>

          <h2>Variables</h2>
          <VariableFields
            variables={targetVariables}
            variableValues={variableValues}
            onVariableValueChange={(name, value) => setEditedValues((current) => ({ ...current, [name]: value }))}
            emptyMessage="This revision declares no variables"
          />

          {partition.added.length > 0 && (
            <ul className="upgrade-variable-notes">
              {partition.added.map((variable) => (
                <li key={variable.name} data-testid={`upgrade-added-${variable.name}`}>
                  {variable.name} is new in this revision
                </li>
              ))}
            </ul>
          )}

          {partition.removed.length > 0 && (
            <ul className="upgrade-variable-notes upgrade-variable-notes--removed">
              {partition.removed.map((variable) => (
                <li key={variable.name} data-testid={`upgrade-removed-${variable.name}`}>
                  {variable.name} is no longer used and will be dropped
                </li>
              ))}
            </ul>
          )}

          <div className="button-row form-actions">
            <button
              className="primary-button"
              type="button"
              disabled={upgradeStackTemplateMutation.isPending}
              onClick={handleUpgrade}
            >
              {upgradeStackTemplateMutation.isPending ? (
                <Loader2 size={16} className="spin" />
              ) : (
                <ArrowUpCircle size={16} />
              )}
              Upgrade
            </button>
          </div>
        </section>
      )}
    </section>
  );
}
```

- [ ] **Step 4: Add the styles**

Append to `web/src/styles/features.css` — tokens only:

```css
.upgrade-variable-notes {
  color: var(--color-muted-fg);
  font-size: 0.85rem;
  list-style: none;
  margin: 0.75rem 0 0;
  padding: 0;
}

.upgrade-variable-notes li {
  padding: 0.15rem 0;
}

.upgrade-variable-notes--removed li {
  color: var(--color-warning);
}
```

- [ ] **Step 5: Register the route**

In `web/src/app/router.tsx`, add the import:

```tsx
import UpgradeStackTemplateScreen from "../features/stacks/UpgradeStackTemplateScreen";
```

Then add this child immediately after the `template/new` entry from Task 4:

```tsx
                  {
                    path: "template/:stackTemplateId/upgrade",
                    element: <RequireCapability capability="canOperate" mode="route" />,
                    children: [{ index: true, element: <UpgradeStackTemplateScreen /> }]
                  },
```

- [ ] **Step 6: Cover the route**

Append this test inside the top-level `describe` in `web/src/app/router.test.tsx`, next to the `template/new` test added in Task 4. Reuse any bindings already imported at the top of that file rather than re-importing them.

```tsx
  it("renders the upgrade screen at /stacks/:stackId/template/:stackTemplateId/upgrade", async () => {
    vi.stubEnv("VITE_TFLIVE_TENANT_ID", "tenant_123");
    const { routeConfig } = await import("./router");
    const { createMemoryRouter, RouterProvider } = await import("react-router-dom");
    const { QueryClient, QueryClientProvider } = await import("@tanstack/react-query");
    const { queryKeys } = await import("../api/queryKeys");

    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
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
        effectiveCapabilities: { canView: true, canOperate: true, canApprove: true, canManageAccess: true }
      },
      templates: []
    });
    queryClient.setQueryData(queryKeys.templateRevisions("tenant_123"), []);

    const testRouter = createMemoryRouter(routeConfig, {
      initialEntries: ["/stacks/stack_1/template/st_1/upgrade"]
    });
    const markup = renderToStaticMarkup(
      <QueryClientProvider client={queryClient}>
        <AuthContext.Provider value={authValue()}>
          <RouterProvider router={testRouter} />
        </AuthContext.Provider>
      </QueryClientProvider>
    );

    // The seeded stack has no installed templates, so the route resolves to
    // the screen's own missing-template state rather than a 404.
    expect(markup).toContain('data-testid="upgrade-template-missing"');
  });
```

- [ ] **Step 7: Run the tests**

Run: `npm test -- src/features/stacks/UpgradeStackTemplateScreen.test.tsx src/app/router.test.tsx src/styles/styles.guard.test.ts`
Expected: PASS.

- [ ] **Step 8: Run the whole suite and the build**

Run: `npm test`
Expected: PASS, all files.

Run: `npm run build`
Expected: success, no TypeScript errors.

If `src/App.tsx` fails to compile, a shared signature changed — revert that change.

- [ ] **Step 9: Commit**

```bash
git add src/features/stacks/UpgradeStackTemplateScreen.tsx src/features/stacks/UpgradeStackTemplateScreen.test.tsx src/app/router.tsx src/app/router.test.tsx src/styles/features.css
git commit -m "$(cat <<'EOF'
feat: add dedicated screen for upgrading a stack template

Filters candidates to active revisions of the same source template and
states which variables the target revision adds and drops.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
EOF
)"
```

---

## Verification

From `web/`:

```bash
npm test
npm run build
```

Then drive the real app with the `web:verify` skill and confirm by hand:

1. `/stacks/:id/template` shows no raw IDs, no revision dropdown, no Install button.
2. `+ Add template` reaches the picker; installing returns to the template screen with the new template selected.
3. The update cue reads "Up to date" or "Update available" correctly.
4. Upgrade lists only same-source revisions and states added/removed variables.
5. `/` (the legacy console) still renders and still installs/upgrades as before.

## Issue impact

On merge:
- Closes #129 — the raw-ID panel is gone rather than rewritten.
- Contributes to #168.
- #128 remains open by explicit decision.
