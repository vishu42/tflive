import { createBrowserRouter, createMemoryRouter } from "react-router-dom";
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

// createBrowserRouter touches `document` as soon as it's called, which blows
// up when this module is evaluated under Vitest's node environment (used by
// router.test.tsx to read `routeConfig`) or any other non-browser context.
// Fall back to a memory router there; both factories return the same Router
// type, so the browser (and Task 4's main.tsx) always gets a real
// createBrowserRouter(routeConfig) instance.
export const router =
  typeof document === "undefined" ? createMemoryRouter(routeConfig) : createBrowserRouter(routeConfig);
