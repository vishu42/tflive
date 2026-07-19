# Shared Error & Session Boundaries Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement GitHub issue #70 (UI-006) — a `<ServiceUnavailable/>` shared screen, a `useQueryErrorBoundary(error)` hook that classifies a TanStack Query error by HTTP status (401/403/404/503) and returns the matching shared component, and a stability fix in `MockAuthProvider` the hook's `401` case depends on.

**Architecture:** `classifyQueryError` is a pure function mapping an `ApiRequestError`'s `status` to a handled status or `null`. `useQueryErrorBoundary` wraps it: for `401` it fires `useAuth().logout()` from a `useEffect` and returns `null`; for `403`/`404`/`503` it returns the matching existing/new screen component; anything else returns `null` and is left to each screen's existing error handling. The `401` effect depends on `logout`, which requires `MockAuthProvider` to hand out a stable `logout` reference (fixed via `useCallback`) to avoid a re-render loop.

**Tech Stack:** React 18, TypeScript 5, TanStack Query v5, Vitest + Testing Library (existing `web/` app).

## Global Constraints

- No component computes or overrides a permission or an error status beyond what `ApiRequestError.status` already carries — this hook only classifies and renders, per the parent spec ("Error handling" table in `docs/superpowers/specs/2026-07-18-ui-revamp-design.md`).
- `404` on a protected resource must render byte-identical output to a capability-denied `canView` route (reuse `<NotFound/>` directly — see `docs/superpowers/specs/2026-07-19-error-session-boundaries-design.md`, Non-goals).
- The classifier is implemented once (`web/src/shared/queryErrorBoundary.tsx`) and is the only place status→component mapping happens; no per-screen duplication (issue #70 acceptance criteria).
- `503` must never render allowed/success content — it always renders `<ServiceUnavailable/>`, distinct from `<AccessDenied/>`/`<NotFound/>`.
- All new files follow the existing `web/src` feature-folder layout: the new classifier hook goes in a new `web/src/shared/` folder (first use of it — matches the parent spec's target structure); the new screen component goes in `web/src/app/`, sibling to `AccessDenied.tsx`/`NotFound.tsx`.
- Run tests with `npm test` from `web/`; run the TypeScript build with `npm run build` from `web/` before the final commit.

---

### Task 1: `ServiceUnavailable` shared screen

**Files:**
- Create: `web/src/app/ServiceUnavailable.tsx`
- Test: `web/src/app/ServiceUnavailable.test.tsx`

**Interfaces:**
- Consumes: nothing new.
- Produces: `export default function ServiceUnavailable(): JSX.Element` rendering a `<section data-testid="route-service-unavailable">` — consumed by Task 3's `useQueryErrorBoundary` for a `503`.

- [ ] **Step 1: Write the failing test**

```tsx
// web/src/app/ServiceUnavailable.test.tsx
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import ServiceUnavailable from "./ServiceUnavailable";

describe("ServiceUnavailable", () => {
  it("renders the authorization-service-unavailable banner", () => {
    const markup = renderToStaticMarkup(<ServiceUnavailable />);

    expect(markup).toContain('data-testid="route-service-unavailable"');
    expect(markup).toContain("Authorization service unavailable — try again shortly.");
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npx vitest run src/app/ServiceUnavailable.test.tsx`
Expected: FAIL — `Failed to resolve import "./ServiceUnavailable"`.

- [ ] **Step 3: Write minimal implementation**

```tsx
// web/src/app/ServiceUnavailable.tsx
export default function ServiceUnavailable() {
  return (
    <section className="route-service-unavailable" data-testid="route-service-unavailable">
      <h1>Authorization service unavailable</h1>
      <p className="muted">Authorization service unavailable — try again shortly.</p>
    </section>
  );
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web && npx vitest run src/app/ServiceUnavailable.test.tsx`
Expected: PASS (1 test).

- [ ] **Step 5: Commit**

```bash
git add web/src/app/ServiceUnavailable.tsx web/src/app/ServiceUnavailable.test.tsx
git commit -m "feat(web): add ServiceUnavailable shared screen"
```

---

### Task 2: Stabilize `MockAuthProvider`'s `login`/`logout` identity

**Files:**
- Modify: `web/src/auth/MockAuthProvider.tsx`
- Modify: `web/src/auth/MockAuthProvider.test.tsx`

**Interfaces:**
- Consumes: nothing new (existing `AuthContext`, `resolveMockUser`).
- Produces: no signature change — `login`/`logout` keep the same `() => void` type, only their reference identity across re-renders changes. Consumed by Task 3's `useQueryErrorBoundary`, which depends on `logout` in a `useEffect` dependency array.

- [ ] **Step 1: Write the failing test**

Add this test to the existing `describe("MockAuthProvider", ...)` block in `web/src/auth/MockAuthProvider.test.tsx` (after the existing `"login restores the mock user after logout"` test):

```tsx
  it("keeps login/logout stable across re-renders, including across a logout/login cycle", () => {
    const { result, rerender } = renderHook(() => useAuth(), { wrapper });

    const { login: loginBeforeRerender, logout: logoutBeforeRerender } = result.current;
    rerender();
    expect(result.current.login).toBe(loginBeforeRerender);
    expect(result.current.logout).toBe(logoutBeforeRerender);

    act(() => result.current.logout());
    expect(result.current.logout).toBe(logoutBeforeRerender);

    act(() => result.current.login());
    expect(result.current.login).toBe(loginBeforeRerender);
  });
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npx vitest run src/auth/MockAuthProvider.test.tsx`
Expected: FAIL — `login`/`logout` are new function references after `rerender()` (and after the `logout()`/`login()` state transitions), so the `toBe` identity assertions fail.

- [ ] **Step 3: Write minimal implementation**

```tsx
// web/src/auth/MockAuthProvider.tsx
import { useCallback, useMemo, useState } from "react";
import type { ReactNode } from "react";
import { AuthContext } from "./AuthContext";
import type { AuthContextValue, AuthStatus } from "./AuthContext";
import { resolveMockUser } from "./mockUsers";
import type { Me } from "./types";

export default function MockAuthProvider({ children }: { children: ReactNode }) {
  const mockUser = useMemo(() => resolveMockUser(import.meta.env.VITE_TFLIVE_MOCK_USER_ROLE), []);
  const [session, setSession] = useState<{ me: Me | null; status: AuthStatus }>({
    me: mockUser,
    status: "authenticated"
  });

  const login = useCallback(() => setSession({ me: mockUser, status: "authenticated" }), [mockUser]);
  const logout = useCallback(() => setSession({ me: null, status: "unauthenticated" }), []);

  const value: AuthContextValue = {
    me: session.me,
    status: session.status,
    login,
    logout
  };

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web && npx vitest run src/auth/MockAuthProvider.test.tsx`
Expected: PASS (5 tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/auth/MockAuthProvider.tsx web/src/auth/MockAuthProvider.test.tsx
git commit -m "fix(web): stabilize MockAuthProvider login/logout identity"
```

---

### Task 3: `useQueryErrorBoundary` classifier hook

**Files:**
- Create: `web/src/shared/queryErrorBoundary.tsx`
- Test: `web/src/shared/queryErrorBoundary.test.tsx`

**Interfaces:**
- Consumes: `ApiRequestError` from `../api/client`; `useAuth` from `../auth/AuthContext`; `AccessDenied` from `../app/AccessDenied`; `NotFound` from `../app/NotFound`; `ServiceUnavailable` from `../app/ServiceUnavailable` (Task 1); stable `logout` from `MockAuthProvider` (Task 2).
- Produces: `export type QueryErrorStatus = 401 | 403 | 404 | 503`, `export function classifyQueryError(error: unknown): QueryErrorStatus | null`, `export function useQueryErrorBoundary(error: unknown): ReactNode | null` — available to any future screen via `useQueryErrorBoundary(someQuery.error)`.

- [ ] **Step 1: Write the failing test**

```tsx
// web/src/shared/queryErrorBoundary.test.tsx
// @vitest-environment jsdom
import { renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import { AuthContext } from "../auth/AuthContext";
import type { AuthContextValue } from "../auth/AuthContext";
import MockAuthProvider from "../auth/MockAuthProvider";
import { useAuth } from "../auth/AuthContext";
import { ApiRequestError } from "../api/client";
import { classifyQueryError, useQueryErrorBoundary } from "./queryErrorBoundary";
import AccessDenied from "../app/AccessDenied";
import NotFound from "../app/NotFound";
import ServiceUnavailable from "../app/ServiceUnavailable";

function authValue(overrides: Partial<AuthContextValue> = {}): AuthContextValue {
  return {
    me: { sub: "s1", displayName: "Test User", globalCapabilities: { isPlatformAdmin: false, canCreateStack: false } },
    status: "authenticated",
    login: () => {},
    logout: () => {},
    ...overrides
  };
}

function wrapper(auth: AuthContextValue) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return <AuthContext.Provider value={auth}>{children}</AuthContext.Provider>;
  };
}

describe("classifyQueryError", () => {
  it.each([401, 403, 404, 503] as const)("classifies a %d ApiRequestError", (status) => {
    expect(classifyQueryError(new ApiRequestError(status, "code", "message"))).toBe(status);
  });

  it("returns null for an unhandled status", () => {
    expect(classifyQueryError(new ApiRequestError(500, "server_error", "boom"))).toBeNull();
  });

  it("returns null for non-ApiRequestError values", () => {
    expect(classifyQueryError(new Error("boom"))).toBeNull();
    expect(classifyQueryError(undefined)).toBeNull();
    expect(classifyQueryError(null)).toBeNull();
  });
});

describe("useQueryErrorBoundary", () => {
  it("returns null and does not call logout for a non-error", () => {
    const logout = vi.fn();
    const { result } = renderHook(() => useQueryErrorBoundary(undefined), { wrapper: wrapper(authValue({ logout })) });

    expect(result.current).toBeNull();
    expect(logout).not.toHaveBeenCalled();
  });

  it("renders AccessDenied for a 403 without calling logout", () => {
    const logout = vi.fn();
    const { result } = renderHook(() => useQueryErrorBoundary(new ApiRequestError(403, "forbidden", "nope")), {
      wrapper: wrapper(authValue({ logout }))
    });

    expect(result.current).toEqual(<AccessDenied />);
    expect(logout).not.toHaveBeenCalled();
  });

  it("renders NotFound for a 404", () => {
    const { result } = renderHook(() => useQueryErrorBoundary(new ApiRequestError(404, "not_found", "nope")), {
      wrapper: wrapper(authValue())
    });

    expect(result.current).toEqual(<NotFound />);
  });

  it("renders ServiceUnavailable for a 503", () => {
    const { result } = renderHook(() => useQueryErrorBoundary(new ApiRequestError(503, "unavailable", "down")), {
      wrapper: wrapper(authValue())
    });

    expect(result.current).toEqual(<ServiceUnavailable />);
  });

  it("returns null for an unrecognized status without calling logout", () => {
    const logout = vi.fn();
    const { result } = renderHook(() => useQueryErrorBoundary(new ApiRequestError(500, "server_error", "boom")), {
      wrapper: wrapper(authValue({ logout }))
    });

    expect(result.current).toBeNull();
    expect(logout).not.toHaveBeenCalled();
  });

  it("triggers logout exactly once for a 401, even across re-renders with the same error", async () => {
    const logout = vi.fn();
    const { result, rerender } = renderHook(({ error }: { error: unknown }) => useQueryErrorBoundary(error), {
      initialProps: { error: new ApiRequestError(401, "unauthorized", "expired") as unknown },
      wrapper: wrapper(authValue({ logout }))
    });

    await waitFor(() => expect(logout).toHaveBeenCalledTimes(1));
    expect(result.current).toBeNull();

    rerender({ error: new ApiRequestError(401, "unauthorized", "expired") });
    expect(logout).toHaveBeenCalledTimes(1);
  });

  it("end-to-end with the real MockAuthProvider: a 401 flips status to unauthenticated without looping", async () => {
    function useHarness(error: unknown) {
      const auth = useAuth();
      const boundary = useQueryErrorBoundary(error);
      return { auth, boundary };
    }

    const realWrapper = ({ children }: { children: ReactNode }) => <MockAuthProvider>{children}</MockAuthProvider>;
    const { result } = renderHook(() => useHarness(new ApiRequestError(401, "unauthorized", "expired")), {
      wrapper: realWrapper
    });

    await waitFor(() => expect(result.current.auth.status).toBe("unauthenticated"));
    expect(result.current.auth.me).toBeNull();
    expect(result.current.boundary).toBeNull();
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npx vitest run src/shared/queryErrorBoundary.test.tsx`
Expected: FAIL — `Failed to resolve import "./queryErrorBoundary"`.

- [ ] **Step 3: Write minimal implementation**

```tsx
// web/src/shared/queryErrorBoundary.tsx
import { useEffect } from "react";
import type { ReactNode } from "react";
import { ApiRequestError } from "../api/client";
import { useAuth } from "../auth/AuthContext";
import AccessDenied from "../app/AccessDenied";
import NotFound from "../app/NotFound";
import ServiceUnavailable from "../app/ServiceUnavailable";

export type QueryErrorStatus = 401 | 403 | 404 | 503;

const HANDLED_STATUSES: readonly QueryErrorStatus[] = [401, 403, 404, 503];

export function classifyQueryError(error: unknown): QueryErrorStatus | null {
  if (!(error instanceof ApiRequestError)) {
    return null;
  }
  return (HANDLED_STATUSES as readonly number[]).includes(error.status) ? (error.status as QueryErrorStatus) : null;
}

export function useQueryErrorBoundary(error: unknown): ReactNode | null {
  const { logout } = useAuth();
  const status = classifyQueryError(error);

  useEffect(() => {
    if (status === 401) {
      logout();
    }
  }, [status, logout]);

  switch (status) {
    case 401:
      return null;
    case 403:
      return <AccessDenied />;
    case 404:
      return <NotFound />;
    case 503:
      return <ServiceUnavailable />;
    default:
      return null;
  }
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web && npx vitest run src/shared/queryErrorBoundary.test.tsx`
Expected: PASS (12 tests).

Then run the full suite and the TypeScript build to confirm nothing else regressed:

Run: `cd web && npm test`
Expected: all test files PASS.

Run: `cd web && npm run build`
Expected: exits 0 (no type errors).

- [ ] **Step 5: Commit**

```bash
git add web/src/shared/queryErrorBoundary.tsx web/src/shared/queryErrorBoundary.test.tsx
git commit -m "feat(web): add useQueryErrorBoundary classifier hook"
```
