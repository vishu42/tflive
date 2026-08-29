import { createBrowserRouter, createMemoryRouter, redirect } from "react-router-dom";
import type { RouteObject } from "react-router-dom";
import AppShell from "./AppShell";
import NotFound from "./NotFound";
import RoutePlaceholder from "./RoutePlaceholder";
import RequireCapability from "../auth/RequireCapability";
import OidcAuthProvider from "../auth/OidcAuthProvider";
import CallbackPage from "../auth/CallbackPage";
import StacksListScreen from "../features/stacks/StacksListScreen";
import StackDetailShell from "../features/stacks/StackDetailShell";
import StackTemplateScreen from "../features/stacks/StackTemplateScreen";
import AddStackTemplateScreen from "../features/stacks/AddStackTemplateScreen";
import UpgradeStackTemplateScreen from "../features/stacks/UpgradeStackTemplateScreen";
import EnvironmentScreen from "../features/stacks/EnvironmentScreen";
import TemplateRegistryScreen from "../features/templates/TemplateRegistryScreen";
import TemplateRegistrationScreen from "../features/templates/TemplateRegistrationScreen";
import RunDetailScreen from "../features/runs/RunDetailScreen";
import StackAccessScreen from "../features/stacks/StackAccessScreen";
import CreateStackScreen from "../features/stacks/CreateStackScreen";
// The design system gallery is a development-only route, and deliberately a
// sibling of "/" rather than a child: everything under "/" renders inside
// OidcAuthProvider, which cannot resolve without Keycloak, so a nested
// styleguide would never mount locally.
//
// It is imported dynamically inside the DEV branch rather than statically at
// the top of this file. A static import would tree-shake its JavaScript out
// of production but NOT its stylesheet — CSS imports are side effects and
// survive tree-shaking, which shipped ~3KB of gallery layout to every user.
// With the import inside a statically-false branch, Rollup drops the chunk
// entirely and no CSS is emitted.
const devRoutes: RouteObject[] = import.meta.env.DEV
  ? [
      {
        path: "/styleguide",
        lazy: async () => ({ Component: (await import("../dev/StyleGuide")).default })
      }
    ]
  : [];

// "/" is not a screen — the design spec's route map has no row for it — but
// it is where a bare sign-in and a manually typed origin both land, so it has
// to send the visitor somewhere. This is a loader redirect rather than a
// <Navigate> element so react-router resolves it before rendering anything:
// the shell never flashes an empty index.
//
// Routes still rendering RoutePlaceholder are reserved
// slots for upcoming tickets. Routes with a capability in the parent spec's
// route map are wrapped in a <RequireCapability mode="route"> layout route —
// see docs/superpowers/specs/2026-07-19-capability-gating-primitives-design.md.
export const routeConfig: RouteObject[] = [
  ...devRoutes,
  {
    path: "/",
    element: <OidcAuthProvider />,
    children: [
      {
        element: <AppShell />,
        children: [
          { index: true, loader: () => redirect("/stacks") },
          { path: "stacks", element: <StacksListScreen /> },
          {
            path: "stacks/new",
            element: <RequireCapability capability="canCreateStack" mode="route" />,
            children: [{ index: true, element: <CreateStackScreen /> }]
          },
          {
            path: "stacks/:stackId",
            element: <RequireCapability capability="canView" mode="route" />,
            children: [
              {
                element: <StackDetailShell />,
                children: [
                  { index: true, element: <RoutePlaceholder title="Stack overview" /> },
                  { path: "template", element: <StackTemplateScreen /> },
                  {
                    path: "template/new",
                    element: <RequireCapability capability="canOperate" mode="route" />,
                    children: [{ index: true, element: <AddStackTemplateScreen /> }]
                  },
                  {
                    path: "template/:stackTemplateId/upgrade",
                    element: <RequireCapability capability="canOperate" mode="route" />,
                    children: [{ index: true, element: <UpgradeStackTemplateScreen /> }]
                  },
                  {
                    path: "environment",
                    element: <RequireCapability capability="canManageAccess" mode="route" />,
                    children: [{ index: true, element: <EnvironmentScreen /> }]
                  },
                  // Runs render inline on the template screen (issue #130);
                  // only the run detail route remains, reached by clicking a
                  // run in that screen's history.
                  { path: "runs/:runId", element: <RunDetailScreen /> },
                  {
                    path: "access",
                    element: <RequireCapability capability="canManageAccess" mode="route" />,
                    children: [{ index: true, element: <StackAccessScreen /> }]
                  }
                ]
              }
            ]
          },
          { path: "templates", element: <TemplateRegistryScreen /> },
          { path: "templates/new", element: <TemplateRegistrationScreen /> },
          { path: "auth/callback", element: <CallbackPage /> },
          { path: "*", element: <NotFound /> }
        ]
      }
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
