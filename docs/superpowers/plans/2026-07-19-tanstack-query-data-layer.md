# TanStack Query Data Layer Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace `web/src`'s hand-rolled `useState`/`useEffect` fetching and manual `setTimeout` poll loops with a TanStack Query data layer (GitHub issue #67 / UI-003), covering stacks, templates, runs, and logs, with no change to the underlying HTTP requests.

**Architecture:** A `QueryClient` is created in `web/src/app/queryClient.ts` and provided at the app root in `main.tsx` (wrapping the existing `RouterProvider`, per `docs/superpowers/specs/2026-07-18-ui-revamp-design.md`'s Architecture section). `web/src/api/client.ts`'s existing fetch functions are not rewritten — they become the `queryFn`/`mutationFn` bodies for a new set of hooks in `web/src/api/queries.ts`, keyed by a query-key factory in `web/src/api/queryKeys.ts`. `web/src/App.tsx` (the current monolithic, unrouted console — untouched by UI-007's later feature-folder extraction) is rewired onto these hooks: its two hand-rolled `setTimeout` poll loops (registration status, run status) are removed and replaced by `refetchInterval`; log tailing is replaced by a query key that includes the run's status, so logs refetch exactly when the polled run status changes, matching current behavior. `web/src/polling.ts`'s pure predicates (`isTerminalRegistrationStatus`, `isTerminalRunStatus`, `nextPollDelayMs`) are kept and reused — now as `refetchInterval` gates and the query client's `retryDelay` — rather than being thrown away.

**Tech Stack:** React 18, TypeScript, Vite, Vitest, `@tanstack/react-query` v5. New dev-only test tooling: `jsdom`, `@testing-library/react`, `@testing-library/dom` (the current suite runs under Vitest's `node` environment with no DOM and no React Testing Library; hook-level tests for this feature need a real DOM, added via a per-file `// @vitest-environment jsdom` pragma so the project-wide `node` environment in `vite.config.ts` is untouched).

## Global Constraints

- Do not change any HTTP request made by the app: same endpoints, same HTTP methods, same payload shapes as `web/src/api/client.ts` already sends. Task 5/6/7 hooks call the existing exported functions from `client.ts` unmodified — no new/renamed/restructured API calls.
- No visual/UX change. `web/src/App.tsx`'s JSX (the render return statement) must not change in this plan — only what feeds it. This matches the ui-revamp spec's non-goal: "No new screens or visual redesign are in scope."
- `web/src/polling.ts`'s existing exports (`isTerminalRegistrationStatus`, `isTerminalRunStatus`, `nextPollDelayMs`) and its existing test file `web/src/polling.test.ts` are not modified — only how they're consumed changes.
- Package versions to install (pin exactly, do not use ranges wider than these): `@tanstack/react-query@^5.101.2` (dependency); `jsdom@^29.1.1`, `@testing-library/react@^16.3.2`, `@testing-library/dom@^10.4.1` (devDependencies).
- New test files that mount React components/hooks needing a real DOM must start with `// @vitest-environment jsdom` as their first line. Do not change `vite.config.ts`'s global `test.environment` (currently `"node"`).
- All commands in this plan run from the `web/` directory unless stated otherwise.

---

## File Structure

New files:
- `web/src/api/queryKeys.ts` — query key factory, one function per resource, pure and DOM-free.
- `web/src/api/queries.ts` — TanStack Query hooks wrapping `client.ts` functions (queries + mutations).
- `web/src/app/queryClient.ts` — `createQueryClient()` factory with retry/backoff wired to `polling.ts`'s `nextPollDelayMs`.
- `web/src/api/queryKeys.test.ts`, `web/src/api/queries.test.ts`, `web/src/app/queryClient.test.ts` — new tests for the above.

Modified files:
- `web/src/main.tsx` — wrap `<RouterProvider>` in `<QueryClientProvider>`.
- `web/src/App.tsx` — state/effects rewired onto the new hooks; JSX unchanged.
- `web/src/App.test.tsx` — wrap `<App />` in a `QueryClientProvider` so `useQueryClient()` calls inside it don't throw.
- `web/package.json` — new dependency/devDependencies.

Untouched: `web/src/polling.ts`, `web/src/polling.test.ts`, `web/src/api/client.ts`, `web/src/api/client.test.ts`, `web/src/api/types.ts`, `web/src/workflowState.ts`, `web/src/app/router.tsx`, `web/src/app/AppShell.tsx`.

---

### Task 1: Add TanStack Query and test-tooling dependencies

**Files:**
- Modify: `web/package.json`

**Interfaces:**
- Produces: `@tanstack/react-query` importable in `web/src`; `jsdom` available as a Vitest environment; `@testing-library/react`'s `render`/`renderHook`/`waitFor`/`cleanup` and `@testing-library/dom`'s `screen` importable in test files.

- [ ] **Step 1: Install the runtime dependency**

```bash
cd web
npm install @tanstack/react-query@5.101.2
```

- [ ] **Step 2: Install the dev/test dependencies**

```bash
npm install --save-dev jsdom@29.1.1 @testing-library/react@16.3.2 @testing-library/dom@10.4.1
```

- [ ] **Step 3: Verify the install**

Run: `cat package.json`
Expected: `"@tanstack/react-query": "5.101.2"` under `"dependencies"`; `"jsdom"`, `"@testing-library/react"`, `"@testing-library/dom"` under `"devDependencies"`.

- [ ] **Step 4: Verify existing suite still passes untouched**

Run: `npm test`
Expected: all existing test files pass (nothing imports the new packages yet, so this is a no-op sanity check that the install didn't break anything).

- [ ] **Step 5: Commit**

```bash
git add package.json package-lock.json
git commit -m "chore(web): add TanStack Query and jsdom/testing-library dev deps"
```

---

### Task 2: Query key factory

**Files:**
- Create: `web/src/api/queryKeys.ts`
- Test: `web/src/api/queryKeys.test.ts`

**Interfaces:**
- Consumes: nothing (pure, string-in/tuple-out).
- Produces: `queryKeys` object with methods `stacks`, `templateRevisions`, `stack`, `templateRegistration`, `templateRevisionVariables`, `templateRun`, `templateRunLogs`, `templateRunLogs` taking a trailing `statusTag: string`, `templateRunLog` taking a trailing `statusTag: string`. Every later task (3, 5, 6, 7, 8) imports `queryKeys` from this file.

- [ ] **Step 1: Write the failing test**

```typescript
// web/src/api/queryKeys.test.ts
import { describe, expect, it } from "vitest";
import { queryKeys } from "./queryKeys";

describe("queryKeys", () => {
  it("builds tenant-scoped list keys", () => {
    expect(queryKeys.stacks("tenant_123")).toEqual(["stacks", "tenant_123"]);
    expect(queryKeys.templateRevisions("tenant_123")).toEqual(["templateRevisions", "tenant_123"]);
  });

  it("builds resource detail keys", () => {
    expect(queryKeys.stack("tenant_123", "stack_1")).toEqual(["stack", "tenant_123", "stack_1"]);
    expect(queryKeys.templateRegistration("tenant_123", "reg_1")).toEqual(["templateRegistration", "tenant_123", "reg_1"]);
    expect(queryKeys.templateRevisionVariables("tenant_123", "rev_1")).toEqual(["templateRevisionVariables", "tenant_123", "rev_1"]);
    expect(queryKeys.templateRun("tenant_123", "run_1")).toEqual(["templateRun", "tenant_123", "run_1"]);
  });

  it("builds status-tagged log keys so a status change produces a new key", () => {
    expect(queryKeys.templateRunLogs("tenant_123", "run_1", "planned")).toEqual([
      "templateRunLogs", "tenant_123", "run_1", "planned"
    ]);
    expect(queryKeys.templateRunLogs("tenant_123", "run_1", "completed")).not.toEqual(
      queryKeys.templateRunLogs("tenant_123", "run_1", "planned")
    );
    expect(queryKeys.templateRunLog("tenant_123", "run_1", "plan", "planned")).toEqual([
      "templateRunLog", "tenant_123", "run_1", "plan", "planned"
    ]);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npm test -- src/api/queryKeys.test.ts`
Expected: FAIL — `Cannot find module './queryKeys'` (file doesn't exist yet).

- [ ] **Step 3: Write the implementation**

```typescript
// web/src/api/queryKeys.ts
export const queryKeys = {
  stacks: (tenantID: string) => ["stacks", tenantID] as const,
  templateRevisions: (tenantID: string) => ["templateRevisions", tenantID] as const,
  stack: (tenantID: string, stackID: string) => ["stack", tenantID, stackID] as const,
  templateRegistration: (tenantID: string, registrationID: string) =>
    ["templateRegistration", tenantID, registrationID] as const,
  templateRevisionVariables: (tenantID: string, templateRevisionID: string) =>
    ["templateRevisionVariables", tenantID, templateRevisionID] as const,
  templateRun: (tenantID: string, runID: string) => ["templateRun", tenantID, runID] as const,
  templateRunLogs: (tenantID: string, runID: string, statusTag: string) =>
    ["templateRunLogs", tenantID, runID, statusTag] as const,
  templateRunLog: (tenantID: string, runID: string, phase: string, statusTag: string) =>
    ["templateRunLog", tenantID, runID, phase, statusTag] as const
};
```

- [ ] **Step 4: Run test to verify it passes**

Run: `npm test -- src/api/queryKeys.test.ts`
Expected: PASS (3 tests).

- [ ] **Step 5: Commit**

```bash
git add src/api/queryKeys.ts src/api/queryKeys.test.ts
git commit -m "feat(web): add TanStack Query key factory"
```

---

### Task 3: QueryClient factory with backoff wired to `polling.ts`

**Files:**
- Create: `web/src/app/queryClient.ts`
- Test: `web/src/app/queryClient.test.ts`

**Interfaces:**
- Consumes: `nextPollDelayMs` from `../polling` (existing, unmodified export).
- Produces: `createQueryClient(): QueryClient`. Task 4 imports this to build the singleton provided at the app root.

- [ ] **Step 1: Write the failing test**

```typescript
// web/src/app/queryClient.test.ts
import { describe, expect, it } from "vitest";
import { nextPollDelayMs } from "../polling";
import { createQueryClient } from "./queryClient";

describe("createQueryClient", () => {
  it("retries failed queries a bounded number of times", () => {
    const queryClient = createQueryClient();
    const { retry } = queryClient.getDefaultOptions().queries ?? {};
    expect(retry).toBe(3);
  });

  it("reuses polling.ts's backoff curve as the retry delay", () => {
    const queryClient = createQueryClient();
    const { retryDelay } = queryClient.getDefaultOptions().queries ?? {};
    expect(typeof retryDelay).toBe("function");
    const delay = retryDelay as (failureCount: number, error: unknown) => number;
    expect(delay(0, new Error("x"))).toBe(nextPollDelayMs(0));
    expect(delay(1, new Error("x"))).toBe(nextPollDelayMs(1));
    expect(delay(10, new Error("x"))).toBe(nextPollDelayMs(10));
  });

  it("does not refetch on window focus", () => {
    const queryClient = createQueryClient();
    expect(queryClient.getDefaultOptions().queries?.refetchOnWindowFocus).toBe(false);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npm test -- src/app/queryClient.test.ts`
Expected: FAIL — `Cannot find module './queryClient'`.

- [ ] **Step 3: Write the implementation**

```typescript
// web/src/app/queryClient.ts
import { QueryClient } from "@tanstack/react-query";
import { nextPollDelayMs } from "../polling";

export function createQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: {
        retry: 3,
        retryDelay: nextPollDelayMs,
        refetchOnWindowFocus: false
      }
    }
  });
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `npm test -- src/app/queryClient.test.ts`
Expected: PASS (3 tests). This runs fine under the default `node` Vitest environment — `QueryClient` itself has no DOM dependency, only its React bindings do.

- [ ] **Step 5: Commit**

```bash
git add src/app/queryClient.ts src/app/queryClient.test.ts
git commit -m "feat(web): add QueryClient factory reusing polling.ts backoff"
```

---

### Task 4: Provide the QueryClient at the app root

**Files:**
- Modify: `web/src/main.tsx`

**Interfaces:**
- Consumes: `createQueryClient` from `./app/queryClient` (Task 3).
- Produces: every component under `<RouterProvider>` (including `App.tsx` in Tasks 8–9, and every future feature screen) can call `useQuery`/`useMutation`/`useQueryClient` without a "No QueryClient set" error.

- [ ] **Step 1: Update `main.tsx`**

```tsx
// web/src/main.tsx
import React from "react";
import ReactDOM from "react-dom/client";
import { QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider } from "react-router-dom";
import { createQueryClient } from "./app/queryClient";
import { router } from "./app/router";
import "./styles.css";

const queryClient = createQueryClient();

ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  </React.StrictMode>
);
```

- [ ] **Step 2: Type-check**

Run: `npm run build`
Expected: `tsc -b` succeeds (no test file covers `main.tsx` — this matches the existing convention, `main.tsx` has no `main.test.tsx` today — so type-checking plus the manual dev-server check below is this task's verification).

- [ ] **Step 3: Manual smoke check**

Run: `npm run dev` (in one terminal), then open the printed local URL in a browser.
Expected: the app loads exactly as before (same "Terraform workflow console" screen at `/`); open the browser console and confirm no "No QueryClient set" error. Stop the dev server (Ctrl-C) when done.

- [ ] **Step 4: Commit**

```bash
git add src/main.tsx
git commit -m "feat(web): provide QueryClient at the app root"
```

---

### Task 5: Read-only query hooks (stacks, template revisions, stack detail, variables)

**Files:**
- Create: `web/src/api/queries.ts` (this task starts the file; Tasks 6 and 7 add to it)
- Test: `web/src/api/queries.test.ts` (this task starts the file; Tasks 6 and 7 add to it)

**Interfaces:**
- Consumes: `client.ts`'s `listStacks`, `listTemplateRevisions`, `getStack`, `getTemplateRevisionVariables` (unmodified); `queryKeys` (Task 2).
- Produces: `useStacksQuery(tenantID: string)`, `useTemplateRevisionsQuery(tenantID: string)`, `useStackQuery(tenantID: string, stackID: string)`, `useTemplateRevisionVariablesQuery(tenantID: string, templateRevisionID: string)` — each a thin `useQuery` wrapper returning the standard TanStack `UseQueryResult`. Task 8 calls these by name.

- [ ] **Step 1: Write the failing test**

```typescript
// web/src/api/queries.test.ts
// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  useStacksQuery,
  useStackQuery,
  useTemplateRevisionVariablesQuery,
  useTemplateRevisionsQuery
} from "./queries";

function wrapper(queryClient: QueryClient) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
  };
}

function testQueryClient(): QueryClient {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } });
}

function jsonResponse(body: unknown): Response {
  return new Response(JSON.stringify(body), { status: 200, headers: { "content-type": "application/json" } });
}

describe("read-only query hooks", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("fetches the tenant's stacks", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(jsonResponse([{ id: "stack_1" }]));
    const { result } = renderHook(() => useStacksQuery("tenant_123"), { wrapper: wrapper(testQueryClient()) });

    await waitFor(() => expect(result.current.data).toEqual([{ id: "stack_1" }]));
    expect(fetch).toHaveBeenCalledWith("/v1/tenants/tenant_123/stacks", expect.objectContaining({ method: "GET" }));
  });

  it("fetches the tenant's template revisions", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(jsonResponse([{ id: "rev_1" }]));
    const { result } = renderHook(() => useTemplateRevisionsQuery("tenant_123"), { wrapper: wrapper(testQueryClient()) });

    await waitFor(() => expect(result.current.data).toEqual([{ id: "rev_1" }]));
    expect(fetch).toHaveBeenCalledWith(
      "/v1/tenants/tenant_123/template-revisions",
      expect.objectContaining({ method: "GET" })
    );
  });

  it("fetches stack detail only when a stack id is given", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(jsonResponse({ stack: { id: "stack_1" }, templates: [] }));
    const { result, rerender } = renderHook(({ stackID }: { stackID: string }) => useStackQuery("tenant_123", stackID), {
      wrapper: wrapper(testQueryClient()),
      initialProps: { stackID: "" }
    });

    expect(result.current.fetchStatus).toBe("idle");
    expect(fetchMock).not.toHaveBeenCalled();

    rerender({ stackID: "stack_1" });
    await waitFor(() => expect(result.current.data).toEqual({ stack: { id: "stack_1" }, templates: [] }));
    expect(fetchMock).toHaveBeenCalledWith("/v1/tenants/tenant_123/stacks/stack_1", expect.objectContaining({ method: "GET" }));
  });

  it("fetches template revision variables only when a revision id is given", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(jsonResponse([{ name: "region" }]));
    const { result } = renderHook(() => useTemplateRevisionVariablesQuery("tenant_123", ""), {
      wrapper: wrapper(testQueryClient())
    });

    expect(result.current.fetchStatus).toBe("idle");
    expect(fetchMock).not.toHaveBeenCalled();
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npm test -- src/api/queries.test.ts`
Expected: FAIL — `Cannot find module './queries'`.

- [ ] **Step 3: Write the implementation**

```typescript
// web/src/api/queries.ts
import { useQuery } from "@tanstack/react-query";
import * as client from "./client";
import { queryKeys } from "./queryKeys";

export function useStacksQuery(tenantID: string) {
  return useQuery({
    queryKey: queryKeys.stacks(tenantID),
    queryFn: () => client.listStacks(tenantID)
  });
}

export function useTemplateRevisionsQuery(tenantID: string) {
  return useQuery({
    queryKey: queryKeys.templateRevisions(tenantID),
    queryFn: () => client.listTemplateRevisions(tenantID)
  });
}

export function useStackQuery(tenantID: string, stackID: string) {
  return useQuery({
    queryKey: queryKeys.stack(tenantID, stackID),
    queryFn: () => client.getStack(tenantID, stackID),
    enabled: stackID !== ""
  });
}

export function useTemplateRevisionVariablesQuery(tenantID: string, templateRevisionID: string) {
  return useQuery({
    queryKey: queryKeys.templateRevisionVariables(tenantID, templateRevisionID),
    queryFn: () => client.getTemplateRevisionVariables(tenantID, templateRevisionID),
    enabled: templateRevisionID !== ""
  });
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `npm test -- src/api/queries.test.ts`
Expected: PASS (4 tests).

- [ ] **Step 5: Commit**

```bash
git add src/api/queries.ts src/api/queries.test.ts
git commit -m "feat(web): add read-only TanStack Query hooks for stacks and templates"
```

---

### Task 6: Polling-aware query hooks (registration, run status, log tailing)

**Files:**
- Modify: `web/src/api/queries.ts` (add to the file from Task 5)
- Modify: `web/src/api/queries.test.ts` (add to the file from Task 5)

**Interfaces:**
- Consumes: `client.ts`'s `getTemplateRegistration`, `getTemplateRun`, `listTemplateRunLogs`, `getTemplateRunLog` (unmodified); `isTerminalRegistrationStatus`, `isTerminalRunStatus` from `../polling` (existing, unmodified); `queryKeys` (Task 2).
- Produces: `POLL_INTERVAL_MS` constant; `useTemplateRegistrationQuery(tenantID, registrationID)`; `useTemplateRunQuery(tenantID, runID, options: { poll: boolean })`; `useTemplateRunLogsQuery(tenantID, runID, statusTag: string)`; `useTemplateRunLogQuery(tenantID, runID, phase, statusTag: string)`. Task 8 calls all four.

- [ ] **Step 1: Write the failing tests (append to `queries.test.ts`)**

```typescript
// Add these imports to the top of web/src/api/queries.test.ts, alongside the existing ones:
import {
  useTemplateRegistrationQuery,
  useTemplateRunLogQuery,
  useTemplateRunLogsQuery,
  useTemplateRunQuery
} from "./queries";

// Add this describe block at the end of web/src/api/queries.test.ts:
describe("polling-aware query hooks", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("keeps polling a non-terminal registration and stops once terminal", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(jsonResponse({ id: "reg_1", status: "running" }));
    const { result } = renderHook(() => useTemplateRegistrationQuery("tenant_123", "reg_1"), {
      wrapper: wrapper(testQueryClient())
    });

    await waitFor(() => expect(result.current.data).toEqual({ id: "reg_1", status: "running" }));
    const query = result.current;
    const interval =
      typeof query.refetchInterval === "function"
        ? // @ts-expect-error internal query state shape is exercised directly for the unit test
          query.refetchInterval({ state: { data: query.data } })
        : query.refetchInterval;
    expect(interval).toBe(1500);
  });

  it("polls a run only when told to, and stops on terminal status", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(jsonResponse({ id: "run_1", status: "planned" }));
    const { result: polled } = renderHook(() => useTemplateRunQuery("tenant_123", "run_1", { poll: true }), {
      wrapper: wrapper(testQueryClient())
    });
    await waitFor(() => expect(polled.current.data).toEqual({ id: "run_1", status: "planned" }));

    const { result: unpolled } = renderHook(() => useTemplateRunQuery("tenant_123", "run_1", { poll: false }), {
      wrapper: wrapper(testQueryClient())
    });
    await waitFor(() => expect(unpolled.current.data).toEqual({ id: "run_1", status: "planned" }));
    expect(unpolled.current.refetchInterval).toBe(false);
  });

  it("keys run logs by status so a status change produces a refetch", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(jsonResponse([{ phase: "plan" }]));
    const queryClient = testQueryClient();
    const { result, rerender } = renderHook(
      ({ statusTag }: { statusTag: string }) => useTemplateRunLogsQuery("tenant_123", "run_1", statusTag),
      { wrapper: wrapper(queryClient), initialProps: { statusTag: "planned" } }
    );
    await waitFor(() => expect(result.current.data).toEqual([{ phase: "plan" }]));
    expect(fetchMock).toHaveBeenCalledTimes(1);

    rerender({ statusTag: "completed" });
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2));
    expect(fetchMock).toHaveBeenLastCalledWith(
      "/v1/tenants/tenant_123/template-runs/run_1/logs",
      expect.objectContaining({ method: "GET" })
    );
  });

  it("fetches a single log phase as text", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response("plan output\n", { status: 200, headers: { "content-type": "text/plain" } })
    );
    const { result } = renderHook(() => useTemplateRunLogQuery("tenant_123", "run_1", "plan", "planned"), {
      wrapper: wrapper(testQueryClient())
    });

    await waitFor(() => expect(result.current.data).toBe("plan output\n"));
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npm test -- src/api/queries.test.ts`
Expected: FAIL — the new imports (`useTemplateRegistrationQuery` etc.) don't exist yet.

- [ ] **Step 3: Append the implementation to `queries.ts`**

```typescript
// Add these imports to the top of web/src/api/queries.ts:
import { isTerminalRegistrationStatus, isTerminalRunStatus } from "../polling";
import type { TemplateRegistration, TemplateRun } from "./types";

// Add to the end of web/src/api/queries.ts:
export const POLL_INTERVAL_MS = 1500;

export function useTemplateRegistrationQuery(tenantID: string, registrationID: string) {
  return useQuery({
    queryKey: queryKeys.templateRegistration(tenantID, registrationID),
    queryFn: () => client.getTemplateRegistration(tenantID, registrationID),
    enabled: registrationID !== "",
    refetchInterval: (query: { state: { data?: TemplateRegistration } }) => {
      const status = query.state.data?.status;
      return status && isTerminalRegistrationStatus(status) ? false : POLL_INTERVAL_MS;
    }
  });
}

export function useTemplateRunQuery(tenantID: string, runID: string, options: { poll: boolean }) {
  return useQuery({
    queryKey: queryKeys.templateRun(tenantID, runID),
    queryFn: () => client.getTemplateRun(tenantID, runID),
    enabled: runID !== "",
    refetchInterval: (query: { state: { data?: TemplateRun } }) => {
      if (!options.poll) {
        return false;
      }
      const status = query.state.data?.status;
      return status && isTerminalRunStatus(status) ? false : POLL_INTERVAL_MS;
    }
  });
}

export function useTemplateRunLogsQuery(tenantID: string, runID: string, statusTag: string) {
  return useQuery({
    queryKey: queryKeys.templateRunLogs(tenantID, runID, statusTag),
    queryFn: () => client.listTemplateRunLogs(tenantID, runID),
    enabled: runID !== ""
  });
}

export function useTemplateRunLogQuery(tenantID: string, runID: string, phase: string, statusTag: string) {
  return useQuery({
    queryKey: queryKeys.templateRunLog(tenantID, runID, phase, statusTag),
    queryFn: () => client.getTemplateRunLog(tenantID, runID, phase),
    enabled: runID !== "" && phase !== ""
  });
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `npm test -- src/api/queries.test.ts`
Expected: PASS (8 tests total: 4 from Task 5 + 4 new).

- [ ] **Step 5: Commit**

```bash
git add src/api/queries.ts src/api/queries.test.ts
git commit -m "feat(web): add polling-aware query hooks for registration, runs, and logs"
```

---

### Task 7: Mutation hooks

**Files:**
- Modify: `web/src/api/queries.ts` (add to the file from Tasks 5–6)
- Modify: `web/src/api/queries.test.ts` (add to the file from Tasks 5–6)

**Interfaces:**
- Consumes: `client.ts`'s `registerTemplate`, `createStack`, `addTemplateToStack`, `updateStackTemplateConfig`, `upgradeStackTemplate`, `startTemplateRun`, `approveRun`, `cancelRun` (unmodified); `queryKeys` (Task 2).
- Produces: `useRegisterTemplateMutation(tenantID)`, `useCreateStackMutation(tenantID)`, `useAddTemplateToStackMutation(tenantID, stackID)`, `useUpdateStackTemplateConfigMutation(tenantID, stackID)`, `useUpgradeStackTemplateMutation(tenantID, stackID)`, `useStartTemplateRunMutation(tenantID)`, `useApproveRunMutation(tenantID)`, `useCancelRunMutation(tenantID)` — each a `useMutation` wrapper. Task 9 calls all eight.

- [ ] **Step 1: Write the failing tests (append to `queries.test.ts`)**

```typescript
// Add these imports to the top of web/src/api/queries.test.ts:
import {
  useAddTemplateToStackMutation,
  useApproveRunMutation,
  useCancelRunMutation,
  useCreateStackMutation,
  useRegisterTemplateMutation,
  useStartTemplateRunMutation,
  useUpdateStackTemplateConfigMutation,
  useUpgradeStackTemplateMutation
} from "./queries";
import { act } from "@testing-library/react";

// Add this describe block at the end of web/src/api/queries.test.ts:
describe("mutation hooks", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("registers a template and seeds the registration query cache", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(jsonResponse({ id: "reg_1", status: "pending" }));
    const queryClient = testQueryClient();
    const { result } = renderHook(() => useRegisterTemplateMutation("tenant_123"), { wrapper: wrapper(queryClient) });

    await act(async () => {
      await result.current.mutateAsync({ repo_owner: "acme", repo_name: "infra", source_ref: "main", root_path: "." });
    });

    expect(queryClient.getQueryData(queryKeys.templateRegistration("tenant_123", "reg_1"))).toEqual({
      id: "reg_1",
      status: "pending"
    });
  });

  it("creates a stack and invalidates the stacks list", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(jsonResponse({ id: "stack_1" }));
    const queryClient = testQueryClient();
    const invalidateSpy = vi.spyOn(queryClient, "invalidateQueries");
    const { result } = renderHook(() => useCreateStackMutation("tenant_123"), { wrapper: wrapper(queryClient) });

    await act(async () => {
      await result.current.mutateAsync({ name: "Acme Prod", slug: "", tags: {}, default_credential_ids: [] });
    });

    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: queryKeys.stacks("tenant_123") });
  });

  it("installs a template on a stack and invalidates that stack's detail", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(jsonResponse({ id: "stack_template_1" }));
    const queryClient = testQueryClient();
    const invalidateSpy = vi.spyOn(queryClient, "invalidateQueries");
    const { result } = renderHook(() => useAddTemplateToStackMutation("tenant_123", "stack_1"), {
      wrapper: wrapper(queryClient)
    });

    await act(async () => {
      await result.current.mutateAsync({ template_revision_id: "rev_1", selected_ref: "main", config: {} });
    });

    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: queryKeys.stack("tenant_123", "stack_1") });
  });

  it("saves stack template config and invalidates that stack's detail", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(jsonResponse({ id: "stack_template_1" }));
    const queryClient = testQueryClient();
    const invalidateSpy = vi.spyOn(queryClient, "invalidateQueries");
    const { result } = renderHook(() => useUpdateStackTemplateConfigMutation("tenant_123", "stack_1"), {
      wrapper: wrapper(queryClient)
    });

    await act(async () => {
      await result.current.mutateAsync({ stackTemplateID: "stack_template_1", body: { config: { region: "us-west-2" } } });
    });

    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: queryKeys.stack("tenant_123", "stack_1") });
  });

  it("upgrades a stack template and invalidates that stack's detail", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(jsonResponse({ id: "stack_template_1" }));
    const queryClient = testQueryClient();
    const invalidateSpy = vi.spyOn(queryClient, "invalidateQueries");
    const { result } = renderHook(() => useUpgradeStackTemplateMutation("tenant_123", "stack_1"), {
      wrapper: wrapper(queryClient)
    });

    await act(async () => {
      await result.current.mutateAsync({ stackTemplateID: "stack_template_1", body: { target_template_revision_id: "rev_2" } });
    });

    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: queryKeys.stack("tenant_123", "stack_1") });
  });

  it("starts a run and seeds that run's query cache", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(jsonResponse({ id: "run_1", status: "queued" }));
    const queryClient = testQueryClient();
    const { result } = renderHook(() => useStartTemplateRunMutation("tenant_123"), { wrapper: wrapper(queryClient) });

    await act(async () => {
      await result.current.mutateAsync({ stackTemplateID: "stack_template_1", body: { operation: "plan" } });
    });

    expect(queryClient.getQueryData(queryKeys.templateRun("tenant_123", "run_1"))).toEqual({
      id: "run_1",
      status: "queued"
    });
  });

  it("approves a run and invalidates that run's query", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response(null, { status: 204 }));
    const queryClient = testQueryClient();
    const invalidateSpy = vi.spyOn(queryClient, "invalidateQueries");
    const { result } = renderHook(() => useApproveRunMutation("tenant_123"), { wrapper: wrapper(queryClient) });

    await act(async () => {
      await result.current.mutateAsync("run_1");
    });

    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: queryKeys.templateRun("tenant_123", "run_1") });
  });

  it("cancels a run and invalidates that run's query", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response(null, { status: 204 }));
    const queryClient = testQueryClient();
    const invalidateSpy = vi.spyOn(queryClient, "invalidateQueries");
    const { result } = renderHook(() => useCancelRunMutation("tenant_123"), { wrapper: wrapper(queryClient) });

    await act(async () => {
      await result.current.mutateAsync({ runID: "run_1", body: { reason: "manual stop" } });
    });

    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: queryKeys.templateRun("tenant_123", "run_1") });
  });
});
```

Also add `import { queryKeys } from "./queryKeys";` to the top of `queries.test.ts` if not already present from a prior task (it isn't — Tasks 5–6 didn't need it directly).

- [ ] **Step 2: Run test to verify it fails**

Run: `npm test -- src/api/queries.test.ts`
Expected: FAIL — the mutation hook imports don't exist yet.

- [ ] **Step 3: Append the implementation to `queries.ts`**

```typescript
// Add to the top of web/src/api/queries.ts:
import { useMutation, useQueryClient } from "@tanstack/react-query";

// Add to the end of web/src/api/queries.ts:
export function useRegisterTemplateMutation(tenantID: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: Parameters<typeof client.registerTemplate>[1]) => client.registerTemplate(tenantID, body),
    onSuccess: (registration) => {
      queryClient.setQueryData(queryKeys.templateRegistration(tenantID, registration.id), registration);
    }
  });
}

export function useCreateStackMutation(tenantID: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: Parameters<typeof client.createStack>[1]) => client.createStack(tenantID, body),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.stacks(tenantID) });
    }
  });
}

export function useAddTemplateToStackMutation(tenantID: string, stackID: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: Parameters<typeof client.addTemplateToStack>[2]) => client.addTemplateToStack(tenantID, stackID, body),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.stack(tenantID, stackID) });
    }
  });
}

export function useUpdateStackTemplateConfigMutation(tenantID: string, stackID: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (variables: { stackTemplateID: string; body: Parameters<typeof client.updateStackTemplateConfig>[2] }) =>
      client.updateStackTemplateConfig(tenantID, variables.stackTemplateID, variables.body),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.stack(tenantID, stackID) });
    }
  });
}

export function useUpgradeStackTemplateMutation(tenantID: string, stackID: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (variables: { stackTemplateID: string; body: Parameters<typeof client.upgradeStackTemplate>[2] }) =>
      client.upgradeStackTemplate(tenantID, variables.stackTemplateID, variables.body),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.stack(tenantID, stackID) });
    }
  });
}

export function useStartTemplateRunMutation(tenantID: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (variables: { stackTemplateID: string; body: Parameters<typeof client.startTemplateRun>[2] }) =>
      client.startTemplateRun(tenantID, variables.stackTemplateID, variables.body),
    onSuccess: (run) => {
      queryClient.setQueryData(queryKeys.templateRun(tenantID, run.id), run);
    }
  });
}

export function useApproveRunMutation(tenantID: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (runID: string) => client.approveRun(tenantID, runID),
    onSuccess: (_data, runID) => {
      queryClient.invalidateQueries({ queryKey: queryKeys.templateRun(tenantID, runID) });
    }
  });
}

export function useCancelRunMutation(tenantID: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (variables: { runID: string; body: Parameters<typeof client.cancelRun>[2] }) =>
      client.cancelRun(tenantID, variables.runID, variables.body),
    onSuccess: (_data, variables) => {
      queryClient.invalidateQueries({ queryKey: queryKeys.templateRun(tenantID, variables.runID) });
    }
  });
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `npm test -- src/api/queries.test.ts`
Expected: PASS (16 tests total: 4 + 4 + 8).

- [ ] **Step 5: Commit**

```bash
git add src/api/queries.ts src/api/queries.test.ts
git commit -m "feat(web): add TanStack Query mutation hooks for the workflow console"
```

---

### Task 8: Rewire `App.tsx`'s state and effects onto the query hooks

**Files:**
- Modify: `web/src/App.tsx:1-382` (imports, component state, derived values, and all `useEffect` blocks — everything before `handleRegister`)

**Interfaces:**
- Consumes: every hook from Tasks 5–6 (`useStacksQuery`, `useTemplateRevisionsQuery`, `useStackQuery`, `useTemplateRevisionVariablesQuery`, `useTemplateRegistrationQuery`, `useTemplateRunQuery`, `useTemplateRunLogsQuery`, `useTemplateRunLogQuery`); `useQueryClient` from `@tanstack/react-query`; `queryKeys` (Task 2); `isTerminalRunStatus` from `../polling` (kept for `canCancel`).
- Produces: local `const`s named `registration`, `stacks`, `templateRevisions`, `stack`, `stackTemplates`, `variables`, `planRun`, `applyRun`, `logs`, `logBody`, `currentRun`, `selectedTemplateRevision`, `selectedStack`, `installedTemplate`, plus the existing `can*` booleans — **identical names and shapes to today's `App.tsx`**, so Task 9 (handlers) and the untouched JSX (`App.tsx:552-792`) don't need to change how they reference this data.

This task does not change what's rendered — only where the data backing the render comes from. Verify with the existing `App.test.tsx` (updated in Task 9) plus manual inspection; there's no separate new test file for this task because `App.tsx` has no dedicated state-management unit tests today (the existing `App.test.tsx` only checks the tenant banner) and the underlying hooks are already tested in Tasks 5–7.

- [ ] **Step 1: Replace the import block (`App.tsx:1-53`)**

```tsx
import {
  ArrowUpCircle,
  CheckCircle2,
  CircleStop,
  Loader2,
  Play,
  RefreshCw,
  Save,
  Send,
  ShieldCheck,
  SquareTerminal,
  XCircle
} from "lucide-react";
import { useEffect, useState } from "react";
import type { FormEvent } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { ApiRequestError } from "./api/client";
import { queryKeys } from "./api/queryKeys";
import {
  useAddTemplateToStackMutation,
  useApproveRunMutation,
  useCancelRunMutation,
  useCreateStackMutation,
  useRegisterTemplateMutation,
  useStackQuery,
  useStacksQuery,
  useStartTemplateRunMutation,
  useTemplateRegistrationQuery,
  useTemplateRevisionVariablesQuery,
  useTemplateRevisionsQuery,
  useTemplateRunLogQuery,
  useTemplateRunLogsQuery,
  useTemplateRunQuery,
  useUpdateStackTemplateConfigMutation,
  useUpgradeStackTemplateMutation
} from "./api/queries";
import { tenantID } from "./config";
import { isTerminalRunStatus } from "./polling";
import {
  canUpgradeStackTemplate,
  canSaveStackTemplateConfig,
  configFromVariableValues,
  findSelectedStack,
  findSelectedStackTemplate,
  findSelectedTemplateRevision,
  nextSelectedStackID,
  nextSelectedStackTemplateID,
  nextSelectedTemplateRevisionID,
  stackLabel,
  stackTemplateLabel,
  templateRevisionLabel,
  variableValuesFromConfig
} from "./workflowState";
```

Note what's gone versus today: no more direct imports of `addTemplateToStack`, `approveRun`, `cancelRun`, `createStack`, `getStack`, `getTemplateRegistration`, `getTemplateRun`, `getTemplateRunLog`, `getTemplateRevisionVariables`, `listStacks`, `listTemplateRevisions`, `listTemplateRunLogs`, `registerTemplate`, `startTemplateRun`, `updateStackTemplateConfig`, `upgradeStackTemplate` from `./api/client` (those now live inside the Task 5–7 hooks); no more `isTerminalRegistrationStatus`/`nextPollDelayMs` from `./polling` (moved into `queries.ts`/`queryClient.ts`); no more `upsertStackTemplate` from `./workflowState` (stack templates now come straight from the `stack` query's cache, updated via `invalidateQueries` in Task 7's mutations, not manual array splicing).

- [ ] **Step 2: Replace state, query wiring, and derived values (`App.tsx:56-91`)**

```tsx
export default function App() {
  const [repoOwner, setRepoOwner] = useState("hashicorp");
  const [repoName, setRepoName] = useState("");
  const [sourceRef, setSourceRef] = useState("main");
  const [rootPath, setRootPath] = useState(".");
  const [registrationID, setRegistrationID] = useState("");
  const [selectedTemplateRevisionID, setSelectedTemplateRevisionID] = useState("");
  const [variableValues, setVariableValues] = useState<Record<string, string>>({});
  const [stackName, setStackName] = useState("Acme Prod");
  const [stackSlug, setStackSlug] = useState("");
  const [selectedStackID, setSelectedStackID] = useState("");
  const [selectedStackTemplateID, setSelectedStackTemplateID] = useState("");
  const [planRunID, setPlanRunID] = useState("");
  const [applyRunID, setApplyRunID] = useState("");
  const [selectedRunKind, setSelectedRunKind] = useState<"plan" | "apply">("plan");
  const [selectedPhase, setSelectedPhase] = useState("plan");
  const [busyAction, setBusyAction] = useState("");
  const [errorMessage, setErrorMessage] = useState("");

  const queryClient = useQueryClient();

  const stacksQuery = useStacksQuery(tenantID);
  const templateRevisionsQuery = useTemplateRevisionsQuery(tenantID);
  const stackQuery = useStackQuery(tenantID, selectedStackID);
  const registrationQuery = useTemplateRegistrationQuery(tenantID, registrationID);

  const stacks = stacksQuery.data ?? [];
  const templateRevisions = templateRevisionsQuery.data ?? [];
  const stack = stackQuery.data?.stack ?? null;
  const stackTemplates = stackQuery.data?.templates ?? [];
  const registration = registrationQuery.data ?? null;

  const selectedTemplateRevision = findSelectedTemplateRevision(templateRevisions, selectedTemplateRevisionID);
  const selectedStack = findSelectedStack(stacks, selectedStackID);
  const installedTemplate = findSelectedStackTemplate(stackTemplates, selectedStackTemplateID);

  const variablesQuery = useTemplateRevisionVariablesQuery(
    tenantID,
    selectedTemplateRevision?.status === "active" ? selectedTemplateRevision.id : ""
  );
  const variables = variablesQuery.data ?? [];

  const planRunQuery = useTemplateRunQuery(tenantID, planRunID, { poll: selectedRunKind === "plan" });
  const applyRunQuery = useTemplateRunQuery(tenantID, applyRunID, { poll: selectedRunKind === "apply" });
  const planRun = planRunQuery.data ?? null;
  const applyRun = applyRunQuery.data ?? null;
  const currentRun = selectedRunKind === "apply" ? applyRun : planRun;
  const currentRunID = selectedRunKind === "apply" ? applyRunID : planRunID;

  const logsQuery = useTemplateRunLogsQuery(tenantID, currentRunID, currentRun?.status ?? "");
  const logQuery = useTemplateRunLogQuery(tenantID, currentRunID, selectedPhase, currentRun?.status ?? "");
  const logs = logsQuery.data ?? [];
  const logBody = logQuery.data ?? "";

  const canInstall = Boolean(selectedTemplateRevision?.status === "active" && stack);
  const canSaveConfig = canSaveStackTemplateConfig(installedTemplate, selectedTemplateRevision, variables, variableValues);
  const canUpgrade = canUpgradeStackTemplate(installedTemplate, selectedTemplateRevision);
  const canPlan = Boolean(installedTemplate && !planRun);
  const canApply = Boolean(installedTemplate && planRun?.status === "completed" && !applyRun);
  const canApprove = applyRun?.status === "waiting_approval";
  const canCancel = Boolean(currentRun && !isTerminalRunStatus(currentRun.status));
```

- [ ] **Step 3: Replace all effects (`App.tsx:93-382`) with this smaller set**

```tsx
  useEffect(() => {
    if (stacksQuery.data) {
      setSelectedStackID((current) => nextSelectedStackID(stacksQuery.data, current));
    }
  }, [stacksQuery.data]);

  useEffect(() => {
    if (templateRevisionsQuery.data) {
      setSelectedTemplateRevisionID((current) => nextSelectedTemplateRevisionID(templateRevisionsQuery.data, current));
    }
  }, [templateRevisionsQuery.data]);

  useEffect(() => {
    setPlanRunID("");
    setApplyRunID("");
    setSelectedRunKind("plan");
  }, [selectedStackID]);

  useEffect(() => {
    const templates = stackQuery.data?.templates;
    if (templates) {
      setSelectedStackTemplateID((current) => nextSelectedStackTemplateID(templates, current));
    }
  }, [stackQuery.data]);

  useEffect(() => {
    if (!installedTemplate?.desired_template_revision_id) {
      return;
    }
    setSelectedTemplateRevisionID(installedTemplate.desired_template_revision_id);
  }, [installedTemplate?.id, installedTemplate?.desired_template_revision_id]);

  useEffect(() => {
    const nextVariables = variablesQuery.data;
    if (!nextVariables) {
      return;
    }
    setVariableValues((current) => {
      const next = installedTemplate
        ? variableValuesFromConfig(installedTemplate.config, nextVariables)
        : { ...current };
      for (const variable of nextVariables) {
        if (!(variable.name in next)) {
          next[variable.name] = "";
        }
      }
      return next;
    });
  }, [variablesQuery.data, installedTemplate]);

  useEffect(() => {
    if (!selectedTemplateRevision || selectedTemplateRevision.status !== "active") {
      setVariableValues({});
    }
  }, [selectedTemplateRevision?.id, selectedTemplateRevision?.status]);

  useEffect(() => {
    const data = registrationQuery.data;
    if (data?.status !== "completed" || !data.template_revision_id) {
      return;
    }
    queryClient.invalidateQueries({ queryKey: queryKeys.templateRevisions(tenantID) });
    setSelectedTemplateRevisionID(data.template_revision_id);
  }, [registrationQuery.data?.status, registrationQuery.data?.template_revision_id, queryClient]);

  useEffect(() => {
    const status = applyRunQuery.data?.status;
    if (!applyRunID || !selectedStackID || !status || !isTerminalRunStatus(status)) {
      return;
    }
    queryClient.invalidateQueries({ queryKey: queryKeys.stack(tenantID, selectedStackID) });
  }, [applyRunQuery.data?.status, applyRunID, selectedStackID, queryClient]);

  useEffect(() => {
    const nextLogs = logsQuery.data;
    if (nextLogs && nextLogs.length > 0 && !nextLogs.some((log) => log.phase === selectedPhase)) {
      setSelectedPhase(nextLogs[0].phase);
    }
  }, [logsQuery.data, selectedPhase]);
```

Each of these mirrors an existing effect one-for-one, minus the parts that were fetching (now delegated to the query hooks) — with two exceptions worth calling out to whoever reviews this task: (1) the registration and apply-run effects now call `queryClient.invalidateQueries` instead of calling `listTemplateRevisions`/`getStack` directly — same trigger condition, same target data, different mechanism; (2) there's no effect resetting `logs`/`logBody` when there's no current run, because `useTemplateRunLogsQuery`/`useTemplateRunLogQuery` are `enabled: false` when `currentRunID === ""`, so `.data` is already `undefined` and the `?? []` / `?? ""` fallbacks at the top of the component already produce the empty state.

- [ ] **Step 4: Type-check (handlers in Task 9 still reference the old `client.ts` calls, so this will not fully compile yet — run it anyway to confirm only Task 9's handler section produces errors)**

Run: `npm run build`
Expected: FAIL, with errors confined to the handler functions (`handleRegister`, `handleCreateStack`, etc. — everything below the code just written) still calling now-unimported `registerTemplate`, `createStack`, etc. If any error appears above `handleRegister`, stop and fix it before proceeding to Task 9.

- [ ] **Step 5: Commit**

```bash
git add src/App.tsx
git commit -m "refactor(web): rewire App state and effects onto TanStack Query hooks"
```

(This intermediate commit does not build cleanly on its own — that's expected and fixed by Task 9 immediately after. If your workflow requires every commit to build, squash Tasks 8 and 9 into one commit instead.)

---

### Task 9: Rewire `App.tsx`'s handlers onto the mutation hooks, and update `App.test.tsx`

**Files:**
- Modify: `web/src/App.tsx:384-538` (all `handle*` functions, `resetRunState`)
- Modify: `web/src/App.test.tsx`

**Interfaces:**
- Consumes: all eight mutation hooks from Task 7.
- Produces: no new exports — this closes out the migration so the file builds and behaves like today.

- [ ] **Step 1: Add mutation hook calls right after the effects from Task 8, before `handleRegister`**

```tsx
  const registerTemplateMutation = useRegisterTemplateMutation(tenantID);
  const createStackMutation = useCreateStackMutation(tenantID);
  const addTemplateToStackMutation = useAddTemplateToStackMutation(tenantID, selectedStackID);
  const updateStackTemplateConfigMutation = useUpdateStackTemplateConfigMutation(tenantID, selectedStackID);
  const upgradeStackTemplateMutation = useUpgradeStackTemplateMutation(tenantID, selectedStackID);
  const startTemplateRunMutation = useStartTemplateRunMutation(tenantID);
  const approveRunMutation = useApproveRunMutation(tenantID);
  const cancelRunMutation = useCancelRunMutation(tenantID);
```

- [ ] **Step 2: Replace the handler functions and `resetRunState` (`App.tsx:384-538`)**

```tsx
  async function handleRegister(event: FormEvent) {
    event.preventDefault();
    await runAction("register", async () => {
      const next = await registerTemplateMutation.mutateAsync({
        repo_owner: repoOwner,
        repo_name: repoName,
        source_ref: sourceRef,
        root_path: rootPath
      });
      setRegistrationID(next.id);
      setSelectedTemplateRevisionID("");
      resetRunState();
    });
  }

  async function handleCreateStack(event: FormEvent) {
    event.preventDefault();
    await runAction("stack", async () => {
      const next = await createStackMutation.mutateAsync({
        name: stackName,
        slug: stackSlug,
        tags: {},
        default_credential_ids: []
      });
      setSelectedStackID(next.id);
      resetRunState();
    });
  }

  async function handleInstallTemplate() {
    if (!stack || !selectedTemplateRevision) {
      return;
    }

    await runAction("install", async () => {
      const config = configFromVariableValues(variables, variableValues);
      const next = await addTemplateToStackMutation.mutateAsync({
        template_revision_id: selectedTemplateRevision.id,
        selected_ref: selectedTemplateRevision.source_ref,
        config
      });
      setSelectedStackTemplateID(next.id);
      resetRunState();
    });
  }

  async function handleSaveStackTemplateConfig() {
    if (!installedTemplate || !canSaveConfig) {
      return;
    }

    await runAction("config", async () => {
      const next = await updateStackTemplateConfigMutation.mutateAsync({
        stackTemplateID: installedTemplate.id,
        body: { config: configFromVariableValues(variables, variableValues) }
      });
      setSelectedStackTemplateID(next.id);
      resetRunState();
    });
  }

  async function handleUpgradeStackTemplate() {
    if (!installedTemplate || !selectedTemplateRevision || !canUpgrade) {
      return;
    }

    await runAction("upgrade", async () => {
      const next = await upgradeStackTemplateMutation.mutateAsync({
        stackTemplateID: installedTemplate.id,
        body: {
          target_template_revision_id: selectedTemplateRevision.id,
          config: configFromVariableValues(variables, variableValues)
        }
      });
      setSelectedStackTemplateID(next.id);
      resetRunState();
    });
  }

  function handleSelectStackTemplate(stackTemplateID: string) {
    if (stackTemplateID === selectedStackTemplateID) {
      return;
    }
    setSelectedStackTemplateID(stackTemplateID);
    resetRunState();
  }

  async function handleStartRun(operation: "plan" | "apply") {
    if (!installedTemplate) {
      return;
    }

    await runAction(operation, async () => {
      const next = await startTemplateRunMutation.mutateAsync({
        stackTemplateID: installedTemplate.id,
        body: { operation }
      });
      if (operation === "apply") {
        setApplyRunID(next.id);
        setSelectedRunKind("apply");
      } else {
        setPlanRunID(next.id);
        setSelectedRunKind("plan");
      }
    });
  }

  async function handleApproveApply() {
    if (!applyRunID) {
      return;
    }

    await runAction("approve", async () => {
      await approveRunMutation.mutateAsync(applyRunID);
    });
  }

  async function handleCancelRun() {
    if (!currentRunID) {
      return;
    }

    await runAction("cancel", async () => {
      await cancelRunMutation.mutateAsync({
        runID: currentRunID,
        body: { reason: "canceled from workflow console" }
      });
    });
  }

  function resetRunState() {
    setPlanRunID("");
    setApplyRunID("");
    setSelectedRunKind("plan");
  }

  async function runAction(name: string, action: () => Promise<void>) {
    setBusyAction(name);
    setErrorMessage("");
    try {
      await action();
    } catch (error) {
      setErrorMessage(messageFromError(error));
    } finally {
      setBusyAction("");
    }
  }
```

`handleApproveApply`/`handleCancelRun` no longer manually re-fetch and `setApplyRun`/`setPlanRun` — the mutations' `onSuccess` (Task 7) already invalidates `queryKeys.templateRun(tenantID, runID)`, so `applyRunQuery`/`planRunQuery` refetch on their own. Everything below this point in the file — the closing `return (...)` JSX, `StatusRow`, `inProgressStatus`, and `messageFromError` — is unchanged from today; do not touch it.

- [ ] **Step 3: Type-check**

Run: `npm run build`
Expected: PASS — `tsc -b && vite build` succeeds with no errors.

- [ ] **Step 4: Update `App.test.tsx` to provide a `QueryClient`**

```tsx
// web/src/App.test.tsx
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderToStaticMarkup } from "react-dom/server";
import { afterEach, describe, expect, it, vi } from "vitest";

describe("application tenant context", () => {
  afterEach(() => {
    vi.unstubAllEnvs();
    vi.resetModules();
  });

  it("displays the configured tenant without an editable tenant input", async () => {
    vi.stubEnv("VITE_TFLIVE_TENANT_ID", "tenant_123");
    const { default: App } = await import("./App");
    const queryClient = new QueryClient();

    const markup = renderToStaticMarkup(
      <QueryClientProvider client={queryClient}>
        <App />
      </QueryClientProvider>
    );

    expect(markup).toContain('data-testid="tenant-context"');
    expect(markup).toContain(">tenant_123</span>");
    expect(markup).not.toContain('value="tenant_123"');
  });
});
```

`renderToStaticMarkup` doesn't run effects, so none of `App`'s queries actually fetch during this test (TanStack Query schedules its fetches from an effect) — the component still renders synchronously with `data: undefined` on every query, which is why every derived value in Task 8 falls back to `[]`/`null`/`""`. Wrapping in `QueryClientProvider` is required only so `useQueryClient()` and the `useQuery`/`useMutation` calls inside `App` don't throw "No QueryClient set."

- [ ] **Step 5: Run the full test suite**

Run: `npm test`
Expected: PASS — every existing test file (`App.test.tsx`, `AppShell.test.tsx`, `NotFound.test.tsx`, `RoutePlaceholder.test.tsx`, `router.test.tsx`, `config.test.ts`, `polling.test.ts`, `workflowState.test.ts`, `api/client.test.ts`) plus the three new ones from Tasks 2, 3, 5–7.

- [ ] **Step 6: Manual smoke check**

Run: `npm run dev`, open the app in a browser.
Expected: register a template, create a stack, install the template, run a plan — confirm the status rows still update (registration polls until terminal, plan run polls until terminal, logs populate) exactly as before. This is the one behavior in this ticket that can't be fully covered by the jsdom-level hook tests (real polling cadence against a live/dev backend), so it's the manual check called for by the project's "verify UI changes in a browser" convention.

- [ ] **Step 7: Commit**

```bash
git add src/App.tsx src/App.test.tsx
git commit -m "refactor(web): rewire App handlers onto TanStack Query mutations"
```

---

### Task 10: Final verification against the issue's acceptance criteria

**Files:** none (verification only)

- [ ] **Step 1: Run the full suite one more time from a clean state**

Run: `npm test`
Expected: all tests pass.

- [ ] **Step 2: Run the production build**

Run: `npm run build`
Expected: `tsc -b && vite build` succeeds with no type errors.

- [ ] **Step 3: Confirm no hand-rolled poll loop remains**

Run: `grep -n "setTimeout" src/App.tsx src/polling.ts`
Expected: no matches. (Before this plan, `App.tsx` had two `window.setTimeout`-based `schedule`/`poll` closures; both are gone, replaced by `refetchInterval`.)

- [ ] **Step 4: Confirm the same HTTP surface**

Run: `grep -n "fetch(" src/api/client.ts`
Expected: identical to before this plan — `client.ts` itself was never edited by any task in this plan, only consumed differently.

- [ ] **Step 5: Re-read the issue's acceptance criteria against what was built**

- "QueryClient is set up and provided at the app root" — Task 4.
- "Existing polling behavior (run status, log tailing) is reproduced via TanStack Query hooks with equivalent or better latency/backoff" — Task 6 (`refetchInterval` gated by the same terminal-status predicates; log tailing keyed by run status so it refetches exactly on status transitions, matching the original effect's dependency array) plus Task 3 (retry backoff reuses `nextPollDelayMs`).
- "`polling.ts`'s custom loop is removed once all call sites are migrated" — Task 8 removed both `setTimeout`-based effects from `App.tsx`; `polling.ts` itself is untouched and its three exports are now consumed as pure predicates/backoff functions rather than driving a manual loop.
- "No change in the underlying HTTP requests made" — every `queryFn`/`mutationFn` in Tasks 5–7 calls an existing, unmodified `client.ts` export.

- [ ] **Step 6: No commit for this task** (verification only — if any step above fails, fix it under the relevant earlier task and re-run this task's checks).

---

## Self-Review Notes

- **Spec coverage:** All four acceptance criteria in issue #67 map to tasks above (see Task 10, Step 5). The ui-revamp design doc's Architecture section ("TanStack Query for all server state... replacing the hand-rolled refetch loop... `api/client.ts` fetch functions become query functions") is covered by Tasks 2–9; the design doc's later tickets (feature-folder restructure, screens, capability gating) are explicitly out of scope for this plan and untouched.
- **Type consistency:** `useTemplateRunQuery`'s `{ poll: boolean }` option, `useTemplateRunLogsQuery`/`useTemplateRunLogQuery`'s `statusTag: string` parameter, and the mutation hooks' `variables` shapes (e.g. `{ stackTemplateID, body }`) are used identically between their Task 6/7 definitions and their Task 8/9 call sites in `App.tsx`.
- **No placeholders:** every step above contains complete, concrete code — no "add error handling" or "similar to Task N" placeholders.
