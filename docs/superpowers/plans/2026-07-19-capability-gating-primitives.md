# Capability-Gating Primitives Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement GitHub issue #69 (UI-005) — `useStackCapabilities(stackId)`, `<RequireCapability>` (route-guard and component-gate modes), and route guards wired onto the reserved stack routes in `router.tsx`.

**Architecture:** `useStackCapabilities` is a thin selector over the existing `useStackQuery` cache (no new fetch). `<RequireCapability>` is a single component with a `capability` prop (global keys read from `useAuth()`, stack keys read from `useStackCapabilities`, defaulting `stackId` to the current route's `useParams().stackId`) and a `mode` prop (`"gate"` wraps children/fallback, `"route"` renders `<Outlet/>` or an error screen). `router.tsx` nests the reserved stack routes under `canView`/`canCreateStack`/`canManageAccess` guards per the parent spec's route-map table.

**Tech Stack:** React 18, TypeScript 5, React Router v6 (data router), TanStack Query v5, Vitest + Testing Library (existing `web/` app).

## Global Constraints

- No component computes or overrides a permission — only ever reads booleans already present on `useAuth().me` or a stack query's `effectiveCapabilities` (spec: "The gate never computes permissions client-side").
- `canView` failures at the route level must render byte-identical output to a real 404 (reuse `<NotFound/>` directly, not a lookalike).
- Component-level gating fails closed by default: no `fallback` prop means render `null`, never render `children`.
- `useStackCapabilities` must not trigger a network call beyond the one `useStackQuery(tenantID, stackId)` already makes for that `stackId`.
- All new files follow the existing `web/src` feature-folder layout: gate/auth primitives in `web/src/auth/`, shared route-level screens in `web/src/app/`.
- Run tests with `npm test` from `web/`; run the TypeScript build with `npm run build` from `web/` before the final commit.

---

### Task 1: `AccessDenied` shared screen

**Files:**
- Create: `web/src/app/AccessDenied.tsx`
- Test: `web/src/app/AccessDenied.test.tsx`

**Interfaces:**
- Consumes: nothing new.
- Produces: `export default function AccessDenied(): JSX.Element` rendering a `<section data-testid="route-access-denied">` — consumed by Task 3's `<RequireCapability>` route mode for any non-`canView` denial.

- [ ] **Step 1: Write the failing test**

```tsx
// web/src/app/AccessDenied.test.tsx
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import AccessDenied from "./AccessDenied";

describe("AccessDenied", () => {
  it("renders a not-permitted message", () => {
    const markup = renderToStaticMarkup(<AccessDenied />);

    expect(markup).toContain('data-testid="route-access-denied"');
    expect(markup).toContain("Not permitted");
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npx vitest run src/app/AccessDenied.test.tsx`
Expected: FAIL — `Failed to resolve import "./AccessDenied"`.

- [ ] **Step 3: Write minimal implementation**

```tsx
// web/src/app/AccessDenied.tsx
export default function AccessDenied() {
  return (
    <section className="route-access-denied" data-testid="route-access-denied">
      <h1>Not permitted</h1>
      <p className="muted">You don't have permission to do this.</p>
    </section>
  );
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web && npx vitest run src/app/AccessDenied.test.tsx`
Expected: PASS (1 test).

- [ ] **Step 5: Commit**

```bash
git add web/src/app/AccessDenied.tsx web/src/app/AccessDenied.test.tsx
git commit -m "feat(web): add AccessDenied shared screen"
```

---

### Task 2: `useStackCapabilities` hook

**Files:**
- Create: `web/src/auth/useStackCapabilities.ts`
- Test: `web/src/auth/useStackCapabilities.test.tsx`

**Interfaces:**
- Consumes: `useStackQuery(tenantID, stackID)` from `web/src/api/queries.ts` (returns a `StackView`, i.e. `{ stack: Stack; templates: StackTemplate[] }` — `web/src/api/types.ts`); `tenantID` from `web/src/config.ts`; `StackCapabilities` from `web/src/auth/types.ts`.
- Produces: `export function useStackCapabilities(stackId: string): StackCapabilities | undefined` — consumed by Task 3's `<RequireCapability>`.

- [ ] **Step 1: Write the failing test**

```tsx
// web/src/auth/useStackCapabilities.test.tsx
// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { useStackCapabilities } from "./useStackCapabilities";
import { queryKeys } from "../api/queryKeys";
import type { StackView } from "../api/types";

function wrapper(queryClient: QueryClient) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
  };
}

function testQueryClient(): QueryClient {
  // staleTime: Infinity keeps seeded cache data from triggering a background
  // refetch on mount (the default staleTime: 0 would refetch stale data even
  // though it's already cached, defeating the "no additional fetch" assertions).
  return new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
}

function stackView(overrides: Partial<StackView["stack"]> = {}): StackView {
  return {
    stack: {
      id: "stack_1",
      tenant_id: "tenant_123",
      name: "Stack",
      slug: "stack",
      tags: {},
      default_credential_ids: [],
      created_by: "user_123",
      created_at: "2026-07-19T00:00:00Z",
      effectiveCapabilities: { canView: true, canOperate: false, canApprove: false, canManageAccess: false },
      ...overrides
    },
    templates: []
  };
}

describe("useStackCapabilities", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("returns undefined before the stack query has resolved", () => {
    const { result } = renderHook(() => useStackCapabilities("stack_1"), { wrapper: wrapper(testQueryClient()) });

    expect(result.current).toBeUndefined();
  });

  it("returns the cached effectiveCapabilities once the stack query resolves, without a second fetch", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify(stackView()), { status: 200, headers: { "content-type": "application/json" } })
    );
    const { result } = renderHook(() => useStackCapabilities("stack_1"), { wrapper: wrapper(testQueryClient()) });

    await waitFor(() =>
      expect(result.current).toEqual({ canView: true, canOperate: false, canApprove: false, canManageAccess: false })
    );
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("reads an already-cached stack query without triggering a fetch", () => {
    const fetchMock = vi.spyOn(globalThis, "fetch");
    const queryClient = testQueryClient();
    queryClient.setQueryData(queryKeys.stack("tenant_123", "stack_1"), stackView({ canView: true } as never));

    const { result } = renderHook(() => useStackCapabilities("stack_1"), { wrapper: wrapper(queryClient) });

    expect(result.current).toEqual({ canView: true, canOperate: false, canApprove: false, canManageAccess: false });
    expect(fetchMock).not.toHaveBeenCalled();
  });
});
```

Note: `useStackCapabilities` reads `tenantID` from `web/src/config.ts`, which resolves to `"tenant_123"` under Vitest's default (`import.meta.env.DEV` is true in the test runner) — matching the `"tenant_123"` used in `queryKeys.stack(...)` above. This matches the existing convention in `App.tsx`/`AppShell.tsx` of importing the module-level `tenantID` rather than threading it through props.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npx vitest run src/auth/useStackCapabilities.test.tsx`
Expected: FAIL — `Failed to resolve import "./useStackCapabilities"`.

- [ ] **Step 3: Write minimal implementation**

```ts
// web/src/auth/useStackCapabilities.ts
import { useStackQuery } from "../api/queries";
import { tenantID } from "../config";
import type { StackCapabilities } from "./types";

export function useStackCapabilities(stackId: string): StackCapabilities | undefined {
  return useStackQuery(tenantID, stackId).data?.stack.effectiveCapabilities;
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web && npx vitest run src/auth/useStackCapabilities.test.tsx`
Expected: PASS (3 tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/auth/useStackCapabilities.ts web/src/auth/useStackCapabilities.test.tsx
git commit -m "feat(web): add useStackCapabilities hook"
```

---

### Task 3: `<RequireCapability>` gate component

**Files:**
- Create: `web/src/auth/RequireCapability.tsx`
- Test: `web/src/auth/RequireCapability.test.tsx`

**Interfaces:**
- Consumes: `useAuth()` from `./AuthContext` (`{ me, status }`); `useStackCapabilities(stackId)` from Task 2; `NotFound` from `../app/NotFound`; `AccessDenied` from `../app/AccessDenied` (Task 1); `Outlet`, `useParams` from `react-router-dom`.
- Produces: `export default function RequireCapability(props: RequireCapabilityProps): JSX.Element | null` and `export interface RequireCapabilityProps { capability: CapabilityKey; stackId?: string; mode?: "gate" | "route"; children?: ReactNode; fallback?: ReactNode }` — consumed by Task 4's `router.tsx`.

- [ ] **Step 1: Write the failing test**

```tsx
// web/src/auth/RequireCapability.test.tsx
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { createMemoryRouter, MemoryRouter, RouterProvider } from "react-router-dom";
import { describe, expect, it } from "vitest";
import { AuthContext } from "./AuthContext";
import type { AuthContextValue } from "./AuthContext";
import RequireCapability from "./RequireCapability";
import { queryKeys } from "../api/queryKeys";
import type { StackView } from "../api/types";

function authValue(overrides: Partial<AuthContextValue> = {}): AuthContextValue {
  return {
    me: {
      sub: "user_1",
      displayName: "Test User",
      globalCapabilities: { isPlatformAdmin: false, canCreateStack: false }
    },
    status: "authenticated",
    login: () => {},
    logout: () => {},
    ...overrides
  };
}

function testQueryClient(): QueryClient {
  // staleTime: Infinity keeps seeded cache data from triggering a background
  // refetch on mount (the default staleTime: 0 would refetch stale data even
  // though it's already cached, defeating the "no additional fetch" assertions).
  return new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
}

function stackView(capabilities: Partial<StackView["stack"]["effectiveCapabilities"]>): StackView {
  return {
    stack: {
      id: "stack_1",
      tenant_id: "tenant_123",
      name: "Stack",
      slug: "stack",
      tags: {},
      default_credential_ids: [],
      created_by: "user_123",
      created_at: "2026-07-19T00:00:00Z",
      effectiveCapabilities: { canView: false, canOperate: false, canApprove: false, canManageAccess: false, ...capabilities }
    },
    templates: []
  };
}

function seededClient(stackId: string, capabilities: Partial<StackView["stack"]["effectiveCapabilities"]>): QueryClient {
  const queryClient = testQueryClient();
  queryClient.setQueryData(queryKeys.stack("tenant_123", stackId), stackView(capabilities));
  return queryClient;
}

function renderGate(
  ui: ReactElement,
  { auth = authValue(), queryClient = testQueryClient() }: { auth?: AuthContextValue; queryClient?: QueryClient } = {}
): string {
  return renderToStaticMarkup(
    <QueryClientProvider client={queryClient}>
      <AuthContext.Provider value={auth}>
        <MemoryRouter>{ui}</MemoryRouter>
      </AuthContext.Provider>
    </QueryClientProvider>
  );
}

describe("RequireCapability — gate mode (component-level)", () => {
  it("renders children when the global capability is allowed", () => {
    const markup = renderGate(
      <RequireCapability capability="canCreateStack">
        <span data-testid="protected">shown</span>
      </RequireCapability>,
      { auth: authValue({ me: { ...authValue().me!, globalCapabilities: { isPlatformAdmin: false, canCreateStack: true } } }) }
    );

    expect(markup).toContain('data-testid="protected"');
  });

  it("fails closed (renders nothing) when denied and no fallback is given", () => {
    const markup = renderGate(
      <RequireCapability capability="canCreateStack">
        <span data-testid="protected">shown</span>
      </RequireCapability>
    );

    expect(markup).toBe("");
  });

  it("renders the fallback when denied and a fallback is given", () => {
    const markup = renderGate(
      <RequireCapability capability="canCreateStack" fallback={<span data-testid="reason">Requires operator role</span>}>
        <span data-testid="protected">shown</span>
      </RequireCapability>
    );

    expect(markup).toContain('data-testid="reason"');
    expect(markup).not.toContain('data-testid="protected"');
  });

  it("renders nothing while the global capability is loading", () => {
    const markup = renderGate(
      <RequireCapability capability="canCreateStack" fallback={<span data-testid="reason">shown</span>}>
        <span data-testid="protected">shown</span>
      </RequireCapability>,
      { auth: authValue({ status: "loading", me: null }) }
    );

    expect(markup).toBe("");
  });

  it("reads a stack capability from the query cache using an explicit stackId, with no fetch", () => {
    const markup = renderGate(
      <RequireCapability capability="canOperate" stackId="stack_1">
        <span data-testid="protected">shown</span>
      </RequireCapability>,
      { queryClient: seededClient("stack_1", { canOperate: true }) }
    );

    expect(markup).toContain('data-testid="protected"');
  });

  it("falls back to the current route's stackId param when none is given explicitly", () => {
    const testRouter = createMemoryRouter(
      [
        {
          path: "/stacks/:stackId",
          element: (
            <RequireCapability capability="canOperate">
              <span data-testid="protected">shown</span>
            </RequireCapability>
          )
        }
      ],
      { initialEntries: ["/stacks/stack_1"] }
    );
    const markup = renderToStaticMarkup(
      <QueryClientProvider client={seededClient("stack_1", { canOperate: true })}>
        <AuthContext.Provider value={authValue()}>
          <RouterProvider router={testRouter} />
        </AuthContext.Provider>
      </QueryClientProvider>
    );

    expect(markup).toContain('data-testid="protected"');
  });

  it("renders nothing while a stack capability is loading (no cached data yet)", () => {
    const markup = renderGate(
      <RequireCapability capability="canOperate" stackId="stack_1">
        <span data-testid="protected">shown</span>
      </RequireCapability>
    );

    expect(markup).toBe("");
  });
});

describe("RequireCapability — route mode", () => {
  function renderRoute(
    capability: "canView" | "canManageAccess" | "canCreateStack",
    { auth = authValue(), queryClient = testQueryClient() }: { auth?: AuthContextValue; queryClient?: QueryClient } = {}
  ): string {
    const testRouter = createMemoryRouter(
      [
        {
          path: "/stacks/:stackId",
          element: <RequireCapability capability={capability} mode="route" />,
          children: [{ index: true, element: <span data-testid="protected-route">shown</span> }]
        }
      ],
      { initialEntries: ["/stacks/stack_1"] }
    );
    return renderToStaticMarkup(
      <QueryClientProvider client={queryClient}>
        <AuthContext.Provider value={auth}>
          <RouterProvider router={testRouter} />
        </AuthContext.Provider>
      </QueryClientProvider>
    );
  }

  it("renders the routed content when allowed", () => {
    const markup = renderRoute("canView", { queryClient: seededClient("stack_1", { canView: true }) });

    expect(markup).toContain('data-testid="protected-route"');
  });

  it("renders NotFound, identical to a real 404, when canView is denied", () => {
    const markup = renderRoute("canView", { queryClient: seededClient("stack_1", { canView: false }) });

    expect(markup).toContain('data-testid="route-not-found"');
    expect(markup).not.toContain('data-testid="route-access-denied"');
  });

  it("renders AccessDenied, not NotFound, when a non-canView capability is denied", () => {
    const markup = renderRoute("canManageAccess", { queryClient: seededClient("stack_1", { canView: true, canManageAccess: false }) });

    expect(markup).toContain('data-testid="route-access-denied"');
    expect(markup).not.toContain('data-testid="route-not-found"');
  });

  it("renders nothing while loading (no cached stack data yet)", () => {
    const markup = renderRoute("canView");

    expect(markup).toBe("");
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npx vitest run src/auth/RequireCapability.test.tsx`
Expected: FAIL — `Failed to resolve import "./RequireCapability"`.

- [ ] **Step 3: Write minimal implementation**

```tsx
// web/src/auth/RequireCapability.tsx
import type { ReactNode } from "react";
import { Outlet, useParams } from "react-router-dom";
import { useAuth } from "./AuthContext";
import { useStackCapabilities } from "./useStackCapabilities";
import type { Me } from "./types";
import type { StackCapabilities } from "./types";
import NotFound from "../app/NotFound";
import AccessDenied from "../app/AccessDenied";

type GlobalCapabilityKey = keyof Me["globalCapabilities"];
type StackCapabilityKey = keyof StackCapabilities;
export type CapabilityKey = GlobalCapabilityKey | StackCapabilityKey;

const GLOBAL_CAPABILITY_KEYS: readonly GlobalCapabilityKey[] = ["isPlatformAdmin", "canCreateStack"];

function isGlobalCapability(capability: CapabilityKey): capability is GlobalCapabilityKey {
  return (GLOBAL_CAPABILITY_KEYS as readonly string[]).includes(capability);
}

export interface RequireCapabilityProps {
  capability: CapabilityKey;
  stackId?: string;
  mode?: "gate" | "route";
  children?: ReactNode;
  fallback?: ReactNode;
}

type CapabilityState = "loading" | "allowed" | "denied";

function useCapabilityState(capability: CapabilityKey, stackId: string | undefined): CapabilityState {
  const { me, status } = useAuth();
  const params = useParams<{ stackId?: string }>();
  const resolvedStackId = stackId ?? params.stackId ?? "";
  const stackCapabilities = useStackCapabilities(resolvedStackId);

  if (isGlobalCapability(capability)) {
    if (status === "loading") {
      return "loading";
    }
    return me?.globalCapabilities[capability] ? "allowed" : "denied";
  }

  if (resolvedStackId === "" || stackCapabilities === undefined) {
    return "loading";
  }
  return stackCapabilities[capability] ? "allowed" : "denied";
}

export default function RequireCapability({ capability, stackId, mode = "gate", children, fallback }: RequireCapabilityProps) {
  const state = useCapabilityState(capability, stackId);

  // Loading is a distinct outcome from "denied" in both modes — never flash
  // denied/fallback content before the underlying auth/stack query resolves.
  if (state === "loading") {
    return null;
  }

  if (mode === "route") {
    if (state === "denied") {
      return capability === "canView" ? <NotFound /> : <AccessDenied />;
    }
    return <Outlet />;
  }

  if (state === "denied") {
    return <>{fallback ?? null}</>;
  }
  return <>{children}</>;
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web && npx vitest run src/auth/RequireCapability.test.tsx`
Expected: PASS (11 tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/auth/RequireCapability.tsx web/src/auth/RequireCapability.test.tsx
git commit -m "feat(web): add RequireCapability gate/route component"
```

---

### Task 4: Wire route guards into `router.tsx`

**Files:**
- Modify: `web/src/app/router.tsx`
- Modify: `web/src/app/router.test.tsx`

**Interfaces:**
- Consumes: `RequireCapability` from `../auth/RequireCapability` (Task 3); existing `AppShell`, `NotFound`, `RoutePlaceholder`.
- Produces: updated `routeConfig` — no new exports beyond the existing `routeConfig`/`router`.

- [ ] **Step 1: Write the failing test**

Replace the existing `"renders a placeholder for every reserved screen in the route map"` test in `web/src/app/router.test.tsx` with the version below, and add the new gating tests after it. Leave the other two existing tests (`"renders the existing workflow console unchanged..."` and `"renders the 404 screen for unknown paths"`) untouched.

```tsx
// web/src/app/router.test.tsx (replace the second `it(...)` block, then append the new describe block)
  it("renders a placeholder for every reserved screen a signed-in operator can reach", async () => {
    vi.stubEnv("VITE_TFLIVE_TENANT_ID", "tenant_123");
    const { routeConfig } = await import("./router");
    const { default: MockAuthProvider } = await import("../auth/MockAuthProvider");
    const { QueryClient, QueryClientProvider } = await import("@tanstack/react-query");
    const { queryKeys } = await import("../api/queryKeys");

    // The default mock role ("operator") has canCreateStack: true (see auth/mockUsers.ts),
    // so /stacks/new needs no seeded query data — it resolves from useAuth() alone.
    const ungatedAndGloballyAllowedPaths = ["/stacks", "/stacks/new", "/templates", "/auth/callback"];
    for (const path of ungatedAndGloballyAllowedPaths) {
      const testRouter = createMemoryRouter(routeConfig, { initialEntries: [path] });
      const markup = renderToStaticMarkup(
        <QueryClientProvider client={new QueryClient()}>
          <MockAuthProvider>
            <RouterProvider router={testRouter} />
          </MockAuthProvider>
        </QueryClientProvider>
      );
      expect(markup, `expected a placeholder at ${path}`).toContain('data-testid="route-placeholder"');
    }

    // canView-gated stack routes resolve once the stack query cache has effectiveCapabilities.
    // retry: false + staleTime: Infinity: this test seeds the cache directly and
    // must never let TanStack Query's default refetch-on-mount fire a real,
    // unmocked fetch() for stale data — see the identical rationale in
    // useStackCapabilities.test.tsx (Task 2) and RequireCapability.test.tsx (Task 3).
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
    queryClient.setQueryData(queryKeys.stack("tenant_123", "stack_1"), {
      stack: {
        id: "stack_1",
        tenant_id: "tenant_123",
        name: "Stack",
        slug: "stack",
        tags: {},
        default_credential_ids: [],
        created_by: "user_123",
        created_at: "2026-07-19T00:00:00Z",
        effectiveCapabilities: { canView: true, canOperate: true, canApprove: true, canManageAccess: true }
      },
      templates: []
    });
    const stackScopedPaths = [
      "/stacks/stack_1",
      "/stacks/stack_1/template",
      "/stacks/stack_1/runs",
      "/stacks/stack_1/runs/run_1",
      "/stacks/stack_1/access"
    ];
    for (const path of stackScopedPaths) {
      const testRouter = createMemoryRouter(routeConfig, { initialEntries: [path] });
      const markup = renderToStaticMarkup(
        <QueryClientProvider client={queryClient}>
          <MockAuthProvider>
            <RouterProvider router={testRouter} />
          </MockAuthProvider>
        </QueryClientProvider>
      );
      expect(markup, `expected a placeholder at ${path}`).toContain('data-testid="route-placeholder"');
    }
  });

  it("renders a 404, not a permission leak, when canView is denied for a stack route", async () => {
    vi.stubEnv("VITE_TFLIVE_TENANT_ID", "tenant_123");
    const { routeConfig } = await import("./router");
    const { default: MockAuthProvider } = await import("../auth/MockAuthProvider");
    const { QueryClient, QueryClientProvider } = await import("@tanstack/react-query");
    const { queryKeys } = await import("../api/queryKeys");

    // retry: false + staleTime: Infinity: this test seeds the cache directly and
    // must never let TanStack Query's default refetch-on-mount fire a real,
    // unmocked fetch() for stale data — see the identical rationale in
    // useStackCapabilities.test.tsx (Task 2) and RequireCapability.test.tsx (Task 3).
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
    queryClient.setQueryData(queryKeys.stack("tenant_123", "stack_1"), {
      stack: {
        id: "stack_1",
        tenant_id: "tenant_123",
        name: "Stack",
        slug: "stack",
        tags: {},
        default_credential_ids: [],
        created_by: "user_123",
        created_at: "2026-07-19T00:00:00Z",
        effectiveCapabilities: { canView: false, canOperate: false, canApprove: false, canManageAccess: false }
      },
      templates: []
    });

    const testRouter = createMemoryRouter(routeConfig, { initialEntries: ["/stacks/stack_1"] });
    const markup = renderToStaticMarkup(
      <QueryClientProvider client={queryClient}>
        <MockAuthProvider>
          <RouterProvider router={testRouter} />
        </MockAuthProvider>
      </QueryClientProvider>
    );

    expect(markup).toContain('data-testid="route-not-found"');
  });

  it("renders AccessDenied for /stacks/:stackId/access when canManageAccess is denied but canView is allowed", async () => {
    vi.stubEnv("VITE_TFLIVE_TENANT_ID", "tenant_123");
    const { routeConfig } = await import("./router");
    const { default: MockAuthProvider } = await import("../auth/MockAuthProvider");
    const { QueryClient, QueryClientProvider } = await import("@tanstack/react-query");
    const { queryKeys } = await import("../api/queryKeys");

    // retry: false + staleTime: Infinity: this test seeds the cache directly and
    // must never let TanStack Query's default refetch-on-mount fire a real,
    // unmocked fetch() for stale data — see the identical rationale in
    // useStackCapabilities.test.tsx (Task 2) and RequireCapability.test.tsx (Task 3).
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
    queryClient.setQueryData(queryKeys.stack("tenant_123", "stack_1"), {
      stack: {
        id: "stack_1",
        tenant_id: "tenant_123",
        name: "Stack",
        slug: "stack",
        tags: {},
        default_credential_ids: [],
        created_by: "user_123",
        created_at: "2026-07-19T00:00:00Z",
        effectiveCapabilities: { canView: true, canOperate: false, canApprove: false, canManageAccess: false }
      },
      templates: []
    });

    const testRouter = createMemoryRouter(routeConfig, { initialEntries: ["/stacks/stack_1/access"] });
    const markup = renderToStaticMarkup(
      <QueryClientProvider client={queryClient}>
        <MockAuthProvider>
          <RouterProvider router={testRouter} />
        </MockAuthProvider>
      </QueryClientProvider>
    );

    expect(markup).toContain('data-testid="route-access-denied"');
  });

  it("renders AccessDenied at /stacks/new for a role without canCreateStack", async () => {
    vi.stubEnv("VITE_TFLIVE_TENANT_ID", "tenant_123");
    vi.stubEnv("VITE_TFLIVE_MOCK_USER_ROLE", "viewer");
    const { routeConfig } = await import("./router");
    const { default: MockAuthProvider } = await import("../auth/MockAuthProvider");
    const { QueryClient, QueryClientProvider } = await import("@tanstack/react-query");

    const testRouter = createMemoryRouter(routeConfig, { initialEntries: ["/stacks/new"] });
    const markup = renderToStaticMarkup(
      <QueryClientProvider client={new QueryClient()}>
        <MockAuthProvider>
          <RouterProvider router={testRouter} />
        </MockAuthProvider>
      </QueryClientProvider>
    );

    expect(markup).toContain('data-testid="route-access-denied"');
  });
```

Also add this import at the top of `router.test.tsx` (needed by the replaced test and re-used by the new ones — the dynamic `await import(...)` calls above are for modules already re-imported per existing file convention after `vi.resetModules()`; `React` itself and the static imports at the top of the file stay as they are):

No new static imports are required — `QueryClient`/`QueryClientProvider` and `queryKeys` are imported dynamically inside each test via `await import(...)`, matching this file's existing convention of dynamically importing modules that read `import.meta.env` at module-eval time (`router.tsx`, `MockAuthProvider.tsx`) after `vi.stubEnv`/`vi.resetModules()`.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npx vitest run src/app/router.test.tsx`
Expected: FAIL — the new/updated assertions fail because `router.tsx` doesn't yet gate any routes (e.g. `/stacks/stack_1` still renders `route-placeholder` unconditionally, so the "denied → 404" and "denied → AccessDenied" tests fail; `/stacks/new` for the viewer role still renders `route-placeholder` instead of `route-access-denied`).

- [ ] **Step 3: Write minimal implementation**

```tsx
// web/src/app/router.tsx
import { createBrowserRouter, createMemoryRouter } from "react-router-dom";
import type { RouteObject } from "react-router-dom";
import App from "../App";
import AppShell from "./AppShell";
import NotFound from "./NotFound";
import RoutePlaceholder from "./RoutePlaceholder";
import RequireCapability from "../auth/RequireCapability";

// The legacy console renders unchanged at "/" until UI-007 extracts it into
// feature screens; every other route below is a reserved slot this ticket
// only scaffolds. Routes with a capability in the parent spec's route map
// are wrapped in a <RequireCapability mode="route"> layout route — see
// docs/superpowers/specs/2026-07-19-capability-gating-primitives-design.md.
export const routeConfig: RouteObject[] = [
  {
    path: "/",
    element: <AppShell />,
    children: [
      { index: true, element: <App /> },
      { path: "stacks", element: <RoutePlaceholder title="Stacks" /> },
      {
        path: "stacks/new",
        element: <RequireCapability capability="canCreateStack" mode="route" />,
        children: [{ index: true, element: <RoutePlaceholder title="Create stack" /> }]
      },
      {
        path: "stacks/:stackId",
        element: <RequireCapability capability="canView" mode="route" />,
        children: [
          { index: true, element: <RoutePlaceholder title="Stack detail" /> },
          { path: "template", element: <RoutePlaceholder title="Stack template" /> },
          { path: "runs", element: <RoutePlaceholder title="Runs" /> },
          { path: "runs/:runId", element: <RoutePlaceholder title="Run detail" /> },
          {
            path: "access",
            element: <RequireCapability capability="canManageAccess" mode="route" />,
            children: [{ index: true, element: <RoutePlaceholder title="Access" /> }]
          }
        ]
      },
      { path: "templates", element: <RoutePlaceholder title="Template registry" /> },
      { path: "auth/callback", element: <RoutePlaceholder title="Signing in" /> },
      { path: "*", element: <NotFound /> }
    ]
  }
];

// createBrowserRouter touches `document` as soon as it's called, which blows
// up when this module is evaluated under Vitest's node environment (used by
// router.test.tsx to read `routeConfig`) or any other non-browser context.
// Fall back to a memory router there; both factories return the same Router
// type, so the browser (and Task 4's main.tsx) always gets a real
// createBrowserRouter(routeConfig) instance.
export const router =
  typeof document === "undefined" ? createMemoryRouter(routeConfig) : createBrowserRouter(routeConfig);
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web && npx vitest run src/app/router.test.tsx`
Expected: PASS (6 tests).

Then run the full suite and the TypeScript build to confirm nothing else regressed:

Run: `cd web && npm test`
Expected: all test files PASS.

Run: `cd web && npm run build`
Expected: exits 0 (no type errors).

- [ ] **Step 5: Commit**

```bash
git add web/src/app/router.tsx web/src/app/router.test.tsx
git commit -m "feat(web): wire capability route guards into router"
```
