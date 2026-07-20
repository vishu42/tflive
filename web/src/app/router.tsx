import { createBrowserRouter, createMemoryRouter } from "react-router-dom";
import type { RouteObject } from "react-router-dom";
import App from "../App";
import AppShell from "./AppShell";
import NotFound from "./NotFound";
import RoutePlaceholder from "./RoutePlaceholder";
import RequireCapability from "../auth/RequireCapability";
import StacksListScreen from "../features/stacks/StacksListScreen";

// The legacy console renders unchanged at "/" until the feature screens
// fully replace it; routes still rendering RoutePlaceholder are reserved
// slots for upcoming tickets. Routes with a capability in the parent spec's
// route map are wrapped in a <RequireCapability mode="route"> layout route —
// see docs/superpowers/specs/2026-07-19-capability-gating-primitives-design.md.
export const routeConfig: RouteObject[] = [
  {
    path: "/",
    element: <AppShell />,
    children: [
      { index: true, element: <App /> },
      { path: "stacks", element: <StacksListScreen /> },
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
