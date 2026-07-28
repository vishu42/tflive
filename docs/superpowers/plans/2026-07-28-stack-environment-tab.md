# Stack Environment Tab Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move Stack-scoped credential management into a dedicated, capability-gated Environment tab while keeping template-scoped overrides in the Template tab.

**Architecture:** Reuse the existing write-only `CredentialsPanel` and React Query credential hooks. Add an `EnvironmentScreen` nested route that owns only Stack credential queries/mutations; remove Stack credential wiring from `StackTemplateScreen` while retaining its StackTemplate credential panel. Add the Environment navigation link and route guard alongside Access.

**Tech Stack:** React 18, React Router 6, TanStack React Query, TypeScript, Vitest, Testing Library, existing capability gates.

## Global Constraints

- Preserve all unrelated working-tree changes.
- Stack credential values must remain write-only and must never be rendered or returned by list APIs.
- Do not automatically add the `TF_VAR_` prefix; explain the naming convention in the UI.
- StackTemplate credential behavior and runtime inheritance remain unchanged.
- Do not add backend or database schema changes.

---

### Task 1: Add the Environment screen test first

**Files:**
- Create: `web/src/features/stacks/EnvironmentScreen.test.tsx`
- Reference: `web/src/features/stacks/StackTemplateScreen.test.tsx`
- Reference: `web/src/api/queryKeys.ts`

**Interfaces:**
- Consumes the existing `CredentialMetadata` type and the existing credential query/mutation hooks.
- Produces executable expectations for the new `EnvironmentScreen` component.

- [ ] **Step 1: Write the failing tests**

Create a jsdom test harness with a `QueryClientProvider`, authenticated `AuthContext`, and a memory route at `/stacks/stack_1/environment`. Seed `queryKeys.stack("tenant_123", "stack_1")` with a stack whose `canManageAccess` capability is true. Add tests that:

```tsx
it("renders stack credential metadata without rendering secret values", async () => {
  queryClient.setQueryData(queryKeys.stackCredentials("tenant_123", "stack_1"), [
    { id: "credential_1", name: "TF_VAR_TEST", scope: "stack", created_at: "2026-07-19T00:00:00Z" }
  ]);

  renderScreen(queryClient);

  expect(screen.getByTestId("environment-screen")).toBeTruthy();
  expect(screen.getByText("TF_VAR_TEST")).toBeTruthy();
  expect(screen.getByText(/Values are write-only/)).toBeTruthy();
  expect(screen.queryByText("secret-value")).toBeNull();
});

it("creates a stack credential through the existing API mutation", async () => {
  vi.spyOn(globalThis, "fetch").mockResolvedValue(
    new Response(JSON.stringify({ id: "credential_2", name: "TF_VAR_TEST", scope: "stack", created_at: "2026-07-19T00:00:00Z" }), {
      status: 201,
      headers: { "content-type": "application/json" }
    })
  );

  renderScreen(queryClient);
  fireEvent.change(screen.getByLabelText(/Environment credential name/), { target: { value: "TF_VAR_TEST" } });
  fireEvent.change(screen.getByLabelText(/Environment credential value/), { target: { value: "secret-value" } });
  fireEvent.click(screen.getByRole("button", { name: /Add/ }));

  await waitFor(() => expect(globalThis.fetch).toHaveBeenCalledWith(
    expect.stringContaining("/stacks/stack_1/credentials"),
    expect.objectContaining({ method: "POST", body: JSON.stringify({ name: "TF_VAR_TEST", value: "secret-value" }) })
  ));
});
```

Also cover deletion by seeding one metadata record, clicking its accessible delete button, and asserting a `DELETE` request targets `/stacks/stack_1/credentials/credential_1`.

- [ ] **Step 2: Run the focused test to verify it fails**

Run: `cd web && npm test -- EnvironmentScreen.test.tsx`

Expected: FAIL because `EnvironmentScreen` and its route do not exist yet.

### Task 2: Implement the Environment screen and update panel guidance

**Files:**
- Create: `web/src/features/stacks/EnvironmentScreen.tsx`
- Modify: `web/src/features/stacks/CredentialsPanel.tsx`
- Test: `web/src/features/stacks/EnvironmentScreen.test.tsx`

**Interfaces:**
- Consumes `tenantID`, `useParams`, `useStackCredentialsQuery`, `useCreateStackCredentialMutation`, and `useDeleteStackCredentialMutation`.
- Produces `EnvironmentScreen` with `data-testid="environment-screen"` and the existing `CredentialsPanel` behavior.

- [ ] **Step 1: Implement the minimal screen**

Implement `EnvironmentScreen` with the same query error-boundary pattern used by `StackTemplateScreen`. Use `stackId` from `useParams`, call the stack credential query with `tenantID` and `stackId`, and pass the mutation functions to `CredentialsPanel`:

```tsx
const credentialsQuery = useStackCredentialsQuery(tenantID, stackId);
const createMutation = useCreateStackCredentialMutation(tenantID, stackId);
const deleteMutation = useDeleteStackCredentialMutation(tenantID, stackId);

return (
  <section className="stack-environment-screen" data-testid="environment-screen">
    <CredentialsPanel
      title="Environment credentials"
      credentials={credentialsQuery.data ?? []}
      loading={credentialsQuery.isPending}
      busy={createMutation.isPending || deleteMutation.isPending}
      onCreate={(name, value) => createMutation.mutateAsync({ name, value })}
      onDelete={(id) => deleteMutation.mutateAsync(id)}
    />
  </section>
);
```

Use the existing error-boundary rendering convention for handled API errors and a local retry state for unhandled query errors. Do not add a second API client or duplicate credential form logic.

- [ ] **Step 2: Update credential form copy and accessible labels**

Keep the write-only behavior, but add helper text such as `Use TF_VAR_NAME for Terraform variables; provider credentials keep their provider-specific names.` For the Environment screen, ensure the name and value inputs have labels that include `Environment credential name` and `Environment credential value` so tests and assistive technology can identify them. Do not display the submitted value after success.

- [ ] **Step 3: Run the focused test to verify it passes**

Run: `cd web && npm test -- EnvironmentScreen.test.tsx`

Expected: PASS for rendering metadata, write-only behavior, create, and delete.

### Task 3: Add the route and capability-gated Environment tab

**Files:**
- Modify: `web/src/app/router.tsx`
- Modify: `web/src/features/stacks/StackDetailShell.tsx`
- Modify: `web/src/features/stacks/StackDetailShell.test.tsx`

**Interfaces:**
- Consumes `EnvironmentScreen` and the existing `RequireCapability` route/navigation guard.
- Produces `/stacks/:stackId/environment` and a visible Environment tab only for `canManageAccess` users.

- [ ] **Step 1: Extend shell tests with the failing expectations**

Update the authorized shell test to expect `href="/stacks/stack_1/environment"`. Update the denied-capability test to assert that the Environment link is absent. Update the nested-route test to render `/stacks/stack_1/environment` and assert `data-testid="environment-screen"` (seed the credentials query or accept its loading state).

- [ ] **Step 2: Run the shell tests to verify the new expectations fail**

Run: `cd web && npm test -- StackDetailShell.test.tsx`

Expected: FAIL because the shell and route do not yet expose Environment.

- [ ] **Step 3: Add the guarded route and navigation link**

Import `EnvironmentScreen` in `router.tsx` and add this nested route beside `template`, `runs`, and `access`:

```tsx
{
  path: "environment",
  element: <RequireCapability capability="canManageAccess" mode="route" />,
  children: [{ index: true, element: <EnvironmentScreen /> }]
}
```

In `StackDetailShell.tsx`, add the Environment `NavLink` inside `RequireCapability capability="canManageAccess"`, next to Access:

```tsx
<NavLink to="environment">Environment</NavLink>
```

- [ ] **Step 4: Run the shell tests to verify they pass**

Run: `cd web && npm test -- StackDetailShell.test.tsx`

Expected: PASS, including authorization visibility and nested route rendering.

### Task 4: Move Stack credential ownership out of the Template screen

**Files:**
- Modify: `web/src/features/stacks/StackTemplateScreen.tsx`
- Modify: `web/src/features/stacks/StackTemplateScreen.test.tsx`

**Interfaces:**
- Preserves template-scoped credential management using `useStackTemplateCredentialsQuery`, `useCreateStackTemplateCredentialMutation`, and `useDeleteStackTemplateCredentialMutation`.
- Removes Stack-scoped credential queries, mutations, handlers, and panel rendering from the Template screen.

- [ ] **Step 1: Add the failing regression expectation**

In the Template screen test, seed both stack and template credential query keys with metadata and assert that the rendered Template screen contains the template credential name but not the stack credential name or `Stack credentials` heading.

- [ ] **Step 2: Run the regression test to verify it fails**

Run: `cd web && npm test -- StackTemplateScreen.test.tsx`

Expected: FAIL because the current Template screen still renders the Stack credential panel.

- [ ] **Step 3: Remove only Stack-scoped wiring**

Delete the Stack credential query and create/delete mutation declarations, the `createStackCredential` handler, and the Stack `CredentialsPanel` from `StackTemplateScreen.tsx`. Retain the `RequireCapability` wrapper around the template-scoped panel and all template credential hooks/handlers.

- [ ] **Step 4: Run the regression test to verify it passes**

Run: `cd web && npm test -- StackTemplateScreen.test.tsx`

Expected: PASS with template credentials still available and Stack credentials absent.

### Task 5: Run the complete frontend verification

**Files:**
- Verify: `web/src/features/stacks/EnvironmentScreen.tsx`
- Verify: `web/src/app/router.tsx`
- Verify: `web/src/features/stacks/StackDetailShell.tsx`
- Verify: `web/src/features/stacks/StackTemplateScreen.tsx`
- Verify: `web/src/features/stacks/CredentialsPanel.tsx`

- [ ] **Step 1: Run all frontend tests**

Run: `cd web && npm test`

Expected: PASS with no failing tests.

- [ ] **Step 2: Run the production typecheck and build**

Run: `cd web && npm run build`

Expected: TypeScript compilation and Vite build complete successfully.

- [ ] **Step 3: Review the final diff**

Run: `rtk git diff --check && rtk git diff --stat`

Confirm that only the Environment tab/screen, credential panel copy/accessibility, routing, focused tests, and the implementation plan are part of this feature change; preserve unrelated pre-existing modifications.
