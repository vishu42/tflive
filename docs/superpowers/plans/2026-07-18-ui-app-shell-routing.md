# UI App Shell & Routing Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Introduce React Router v6 in data-router mode with a top-level `AppShell` layout route (nav, identity/logout slot, static tenant indicator) covering every screen in the UI revamp's route map, and a 404 route for unknown paths — without changing any existing workflow-console behavior.

**Architecture:** `web/src/app/router.tsx` defines a `RouteObject[]` (`routeConfig`) with `AppShell` as the single layout route wrapping an `Outlet`. The index route (`/`) renders the existing `App` component unchanged, preserving today's single-page console exactly as-is. Every other row of the design spec's route map (`/stacks`, `/stacks/new`, `/stacks/:stackId`, its three sub-routes, `/templates`, `/auth/callback`) renders a shared `RoutePlaceholder` component, since the screens themselves are built in later tickets (UI-007 through UI-013). A `*` route renders `NotFound`. `main.tsx` swaps its direct `<App />` render for `<RouterProvider router={router} />`, where `router = createBrowserRouter(routeConfig)`.

**Tech Stack:** React 18, TypeScript, Vite, `react-router-dom` v6 (data-router APIs only — `createBrowserRouter`/`createMemoryRouter`, no `<BrowserRouter>`), Vitest (`environment: "node"`, tests use `react-dom/server`'s `renderToStaticMarkup`, matching the existing `App.test.tsx` convention rather than jsdom).

## Global Constraints

- `react-router-dom` must be v6 (data-router mode: `createBrowserRouter` and, in tests, `createMemoryRouter`) — never `<BrowserRouter>`/`<Routes>` JSX-config mode, and never v7.
- `AppShell` is the single top-level layout route wrapping every screen in the route map from `docs/superpowers/specs/2026-07-18-ui-revamp-design.md`, including the still-unmigrated legacy console at `/`.
- No screens are extracted from `App.tsx` in this ticket. `web/src/App.tsx` and `web/src/App.test.tsx` are not modified — UI-007 owns feature-folder extraction. Screens not yet built render via a shared `RoutePlaceholder`.
- The tenant indicator is always a read-only element (a `<span>`); never an editable control (`<input>`, `<select>`, `contenteditable`).
- Unknown paths render a dedicated `NotFound` screen via a `*` route.
- Preserve current visual style; no new CSS, component library, or visual redesign in this ticket.
- Tests run in Vitest's `node` environment (see `web/vite.config.ts`) — use `renderToStaticMarkup`, not `@testing-library/react`/jsdom.

---

### Task 1: Route Placeholder And 404 Screens

**Files:**
- Create: `web/src/app/RoutePlaceholder.tsx`
- Create: `web/src/app/RoutePlaceholder.test.tsx`
- Create: `web/src/app/NotFound.tsx`
- Create: `web/src/app/NotFound.test.tsx`

**Interfaces:**
- Consumes: nothing (pure presentational components, no router or config dependency).
- Produces: `RoutePlaceholder(props: { title: string }): JSX.Element` rendering `data-testid="route-placeholder"`; `NotFound(): JSX.Element` rendering `data-testid="route-not-found"`. Task 3's route tree renders these directly.

- [ ] **Step 1: Write the failing tests**

Create `web/src/app/RoutePlaceholder.test.tsx`:

```tsx
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import RoutePlaceholder from "./RoutePlaceholder";

describe("RoutePlaceholder", () => {
  it("renders the given title inside a placeholder region", () => {
    const markup = renderToStaticMarkup(<RoutePlaceholder title="Stacks" />);

    expect(markup).toContain('data-testid="route-placeholder"');
    expect(markup).toContain("Stacks");
    expect(markup).toContain("This screen has not been built yet.");
  });
});
```

Create `web/src/app/NotFound.test.tsx`:

```tsx
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import NotFound from "./NotFound";

describe("NotFound", () => {
  it("renders a 404 message", () => {
    const markup = renderToStaticMarkup(<NotFound />);

    expect(markup).toContain('data-testid="route-not-found"');
    expect(markup).toContain("Page not found");
  });
});
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd web && npx vitest run src/app/RoutePlaceholder.test.tsx src/app/NotFound.test.tsx`
Expected: FAIL — `Cannot find module './RoutePlaceholder'` / `Cannot find module './NotFound'`.

- [ ] **Step 3: Write the minimal implementations**

Create `web/src/app/RoutePlaceholder.tsx`:

```tsx
export default function RoutePlaceholder({ title }: { title: string }) {
  return (
    <section className="route-placeholder" data-testid="route-placeholder">
      <h1>{title}</h1>
      <p className="muted">This screen has not been built yet.</p>
    </section>
  );
}
```

Create `web/src/app/NotFound.tsx`:

```tsx
export default function NotFound() {
  return (
    <section className="route-not-found" data-testid="route-not-found">
      <h1>Page not found</h1>
      <p className="muted">The page you were looking for doesn't exist.</p>
    </section>
  );
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd web && npx vitest run src/app/RoutePlaceholder.test.tsx src/app/NotFound.test.tsx`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/app/RoutePlaceholder.tsx web/src/app/RoutePlaceholder.test.tsx web/src/app/NotFound.tsx web/src/app/NotFound.test.tsx
git commit -m "feat(ui): add route placeholder and 404 screens"
```

---

### Task 2: AppShell Layout Component

**Files:**
- Modify: `web/package.json`, `web/package-lock.json` (via `npm install`)
- Create: `web/src/app/AppShell.tsx`
- Create: `web/src/app/AppShell.test.tsx`

**Interfaces:**
- Consumes: `tenantID` from `web/src/config.ts` (`export const tenantID: string`); `Link`/`Outlet` from `react-router-dom`.
- Produces: `AppShell(): JSX.Element` — default export, a layout route element rendering nav (`Link` to `/stacks` and `/templates`), an identity slot (`data-testid="identity-menu"`, empty — reserved for AUTH-018), a static tenant indicator (`data-testid="shell-tenant-context"`), and an `<Outlet />` for routed content. Task 3 uses this as the top-level route's `element`.

- [ ] **Step 1: Install react-router-dom v6**

Run: `cd web && npm install react-router-dom@6`

Verify `web/package.json` now lists `"react-router-dom": "^6.30.4"` (or newer 6.x) under `dependencies`.

- [ ] **Step 2: Write the failing test**

Create `web/src/app/AppShell.test.tsx`:

```tsx
import { renderToStaticMarkup } from "react-dom/server";
import { createMemoryRouter, RouterProvider } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

describe("AppShell", () => {
  afterEach(() => {
    vi.unstubAllEnvs();
    vi.resetModules();
  });

  it("renders nav, an identity slot, a static tenant indicator, and routed content", async () => {
    vi.stubEnv("VITE_TFLIVE_TENANT_ID", "tenant_123");
    const { default: AppShell } = await import("./AppShell");

    const testRouter = createMemoryRouter(
      [
        {
          path: "/",
          element: <AppShell />,
          children: [{ index: true, element: <div data-testid="outlet-content">child</div> }]
        }
      ],
      { initialEntries: ["/"] }
    );

    const markup = renderToStaticMarkup(<RouterProvider router={testRouter} />);

    expect(markup).toContain('href="/stacks"');
    expect(markup).toContain('href="/templates"');
    expect(markup).toContain('data-testid="identity-menu"');
    expect(markup).toContain('data-testid="shell-tenant-context"');
    expect(markup).toContain(">tenant_123<");
    expect(markup).not.toContain("<input");
    expect(markup).toContain('data-testid="outlet-content"');
  });
});
```

- [ ] **Step 3: Run test to verify it fails**

Run: `cd web && npx vitest run src/app/AppShell.test.tsx`
Expected: FAIL — `Cannot find module './AppShell'`.

- [ ] **Step 4: Write the minimal implementation**

Create `web/src/app/AppShell.tsx`:

```tsx
import { Link, Outlet } from "react-router-dom";
import { tenantID } from "../config";

const navItems: { to: string; label: string }[] = [
  { to: "/stacks", label: "Stacks" },
  { to: "/templates", label: "Templates" }
];

export default function AppShell() {
  return (
    <div className="app-frame">
      <header className="app-frame-header">
        <nav className="app-nav" aria-label="Primary">
          {navItems.map((item) => (
            <Link key={item.to} to={item.to}>
              {item.label}
            </Link>
          ))}
        </nav>
        <div className="app-frame-identity">
          <div className="identity-menu" data-testid="identity-menu" />
          <div className="runtime-field">
            <span>Tenant</span>
            <span className="runtime-value" data-testid="shell-tenant-context">
              {tenantID}
            </span>
          </div>
        </div>
      </header>
      <main className="app-frame-content">
        <Outlet />
      </main>
    </div>
  );
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `cd web && npx vitest run src/app/AppShell.test.tsx`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add web/package.json web/package-lock.json web/src/app/AppShell.tsx web/src/app/AppShell.test.tsx
git commit -m "feat(ui): add AppShell layout with nav and tenant indicator"
```

---

### Task 3: Route Tree Covering The Full Route Map

**Files:**
- Create: `web/src/app/router.tsx`
- Create: `web/src/app/router.test.tsx`

**Interfaces:**
- Consumes: `AppShell` (Task 2), `RoutePlaceholder`/`NotFound` (Task 1), existing `App` default export from `web/src/App.tsx`, `RouteObject`/`createBrowserRouter` from `react-router-dom`.
- Produces: `routeConfig: RouteObject[]` (importable by tests without touching browser globals) and `router` (the `createBrowserRouter(routeConfig)` instance). Task 4's `main.tsx` imports `router`.

- [ ] **Step 1: Write the failing test**

Create `web/src/app/router.test.tsx`:

```tsx
import { renderToStaticMarkup } from "react-dom/server";
import { createMemoryRouter, RouterProvider } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

describe("routeConfig", () => {
  afterEach(() => {
    vi.unstubAllEnvs();
    vi.resetModules();
  });

  it("renders the existing workflow console unchanged at the index route", async () => {
    vi.stubEnv("VITE_TFLIVE_TENANT_ID", "tenant_123");
    const { routeConfig } = await import("./router");

    const testRouter = createMemoryRouter(routeConfig, { initialEntries: ["/"] });
    const markup = renderToStaticMarkup(<RouterProvider router={testRouter} />);

    expect(markup).toContain("Terraform workflow console");
    expect(markup).toContain('data-testid="tenant-context"');
  });

  it("renders a placeholder for every reserved screen in the route map", async () => {
    vi.stubEnv("VITE_TFLIVE_TENANT_ID", "tenant_123");
    const { routeConfig } = await import("./router");

    const reservedPaths = [
      "/stacks",
      "/stacks/new",
      "/stacks/stack_1",
      "/stacks/stack_1/template",
      "/stacks/stack_1/runs",
      "/stacks/stack_1/runs/run_1",
      "/stacks/stack_1/access",
      "/templates",
      "/auth/callback"
    ];

    for (const path of reservedPaths) {
      const testRouter = createMemoryRouter(routeConfig, { initialEntries: [path] });
      const markup = renderToStaticMarkup(<RouterProvider router={testRouter} />);
      expect(markup, `expected a placeholder at ${path}`).toContain('data-testid="route-placeholder"');
    }
  });

  it("renders the 404 screen for unknown paths", async () => {
    vi.stubEnv("VITE_TFLIVE_TENANT_ID", "tenant_123");
    const { routeConfig } = await import("./router");

    const testRouter = createMemoryRouter(routeConfig, { initialEntries: ["/nonexistent"] });
    const markup = renderToStaticMarkup(<RouterProvider router={testRouter} />);

    expect(markup).toContain('data-testid="route-not-found"');
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npx vitest run src/app/router.test.tsx`
Expected: FAIL — `Cannot find module './router'`.

- [ ] **Step 3: Write the minimal implementation**

Create `web/src/app/router.tsx`:

```tsx
import { createBrowserRouter } from "react-router-dom";
import type { RouteObject } from "react-router-dom";
import App from "../App";
import AppShell from "./AppShell";
import NotFound from "./NotFound";
import RoutePlaceholder from "./RoutePlaceholder";

// The legacy console renders unchanged at "/" until UI-007 extracts it into
// feature screens; every other route below is a reserved slot this ticket
// only scaffolds.
export const routeConfig: RouteObject[] = [
  {
    path: "/",
    element: <AppShell />,
    children: [
      { index: true, element: <App /> },
      { path: "stacks", element: <RoutePlaceholder title="Stacks" /> },
      { path: "stacks/new", element: <RoutePlaceholder title="Create stack" /> },
      { path: "stacks/:stackId", element: <RoutePlaceholder title="Stack detail" /> },
      { path: "stacks/:stackId/template", element: <RoutePlaceholder title="Stack template" /> },
      { path: "stacks/:stackId/runs", element: <RoutePlaceholder title="Runs" /> },
      { path: "stacks/:stackId/runs/:runId", element: <RoutePlaceholder title="Run detail" /> },
      { path: "stacks/:stackId/access", element: <RoutePlaceholder title="Access" /> },
      { path: "templates", element: <RoutePlaceholder title="Template registry" /> },
      { path: "auth/callback", element: <RoutePlaceholder title="Signing in" /> },
      { path: "*", element: <NotFound /> }
    ]
  }
];

export const router = createBrowserRouter(routeConfig);
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web && npx vitest run src/app/router.test.tsx`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/app/router.tsx web/src/app/router.test.tsx
git commit -m "feat(ui): define route tree for the app shell"
```

---

### Task 4: Wire main.tsx To The Router

**Files:**
- Modify: `web/src/main.tsx`

**Interfaces:**
- Consumes: `router` from `./app/router` (Task 3), `RouterProvider` from `react-router-dom`.
- Produces: the app's actual DOM mount, now routed. No other file consumes this.

- [ ] **Step 1: Update the entrypoint**

Modify `web/src/main.tsx`:

```tsx
import React from "react";
import ReactDOM from "react-dom/client";
import { RouterProvider } from "react-router-dom";
import { router } from "./app/router";
import "./styles.css";

ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <RouterProvider router={router} />
  </React.StrictMode>
);
```

- [ ] **Step 2: Run the full test suite**

Run: `cd web && npm test`
Expected: PASS — all existing suites (`App.test.tsx`, `config.test.ts`, `polling.test.ts`, `workflowState.test.ts`) plus the four new files from Tasks 1–3 pass unchanged.

- [ ] **Step 3: Run the production build to confirm the entrypoint type-checks**

Run: `cd web && npm run build`
Expected: PASS — `tsc -b && vite build` completes with no type errors.

- [ ] **Step 4: Commit**

```bash
git add web/src/main.tsx
git commit -m "feat(ui): mount the app through the data router"
```
