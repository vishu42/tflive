# Capability-Gating Primitives — Design

## Context

This implements ticket 5 of the UI revamp breakdown (`docs/superpowers/specs/2026-07-18-ui-revamp-design.md`, "Capability gate mechanism" section) as GitHub issue #69 (UI-005). Its dependencies are done: #66 defined the `Me`/`StackCapabilities`/`Stack.effectiveCapabilities` contract (`web/src/auth/types.ts`, `web/src/api/types.ts`), and #68 added `useAuth()` / `AuthContext` / `MockAuthProvider`. The stack detail route already reads through `useStackQuery(tenantID, stackId)` (TanStack Query), so per-stack capabilities are already sitting in the query cache — this ticket just exposes them and gates UI on them.

## Goal

Add the two primitives the parent spec named, plus wire them onto the routes that are already reserved for them:

- `useStackCapabilities(stackId)` — reads `effectiveCapabilities` off the existing stack query, no new fetch.
- `<RequireCapability>` — gates both whole routes and individual components off the same boolean data.
- Route guards applied to `stacks/new`, `stacks/:stackId` (and its child routes), and `stacks/:stackId/access` in `router.tsx`.

## Non-goals

- The full 401/403/404/503 error-boundary system (parent spec ticket 6, a separate issue) — this ticket only needs a `canView`-fails-like-404 case and a "not permitted" case, both narrowly scoped here.
- Real backend data — `effectiveCapabilities` on real API responses ships with AUTH-017; until then the mock user/mock stack fixtures are the only source of truth (parent spec's ticket 16 swaps this later).
- Any new screens — the routes this wraps still render `RoutePlaceholder` until tickets 7-13 extract real screens.

## Design

### `useStackCapabilities`

New file `web/src/auth/useStackCapabilities.ts`:

```ts
export function useStackCapabilities(stackId: string): StackCapabilities | undefined {
  return useStackQuery(tenantID, stackId).data?.stack.effectiveCapabilities;
}
```

`tenantID` is imported from `config.ts` internally (the same pattern `AppShell` and `App.tsx` already use for a module-level tenant constant), so the public signature matches the ticket's `useStackCapabilities(stackId)` exactly — callers never pass a tenant ID. Note `useStackQuery` resolves to a `StackView` (`{ stack, templates }`, see `web/src/api/types.ts`), not a bare `Stack`, so `effectiveCapabilities` is read off `data.stack`, not `data` directly.

### `<RequireCapability>`

New file `web/src/auth/RequireCapability.tsx`. One component, driven by a discriminated `capability` prop and a `mode` prop.

**Capability source** (which data the boolean comes from):
- Global capabilities (`isPlatformAdmin`, `canCreateStack`) are read from `useAuth().me?.globalCapabilities`.
- Stack capabilities (`canView`, `canOperate`, `canApprove`, `canManageAccess`) are read via `useStackCapabilities`, using a `stackId` prop if given, otherwise falling back to `useParams().stackId` from the current route. This fallback is why the parent spec's code sketch never passes `stackId` to `<RequireCapability>` — it's used inside a `/stacks/:stackId/...` route where the param is already in scope. A `stackId` prop is provided for the rarer case of gating a component outside that route tree (e.g. a per-row action in a stacks list).

**Mode** (how a denial renders):
- `mode="route"` — used as a route element wrapping `<Outlet/>`. On success, renders `<Outlet/>`. On failure: `canView` renders the existing `<NotFound/>` component verbatim (identical markup/copy to a real 404 — no distinguishing signal, per AUTH-013's no-leak rule); any other capability renders a new small `<AccessDenied/>` component with an explicit "not permitted" message (safe here because the resource's existence is already implied by reaching this route).
- `mode="gate"` (default) — wraps `children`. Renders `children` if allowed; otherwise renders `fallback` if one was passed, else `null` (fails closed by default — a caller opts into an explanation by passing `fallback`).

**Loading**: while the underlying source is unresolved (`useAuth().status === "loading"` for global capabilities, or `useStackQuery(...).isLoading` for stack capabilities), both modes render `null`. This avoids a flash of denied or allowed content before data arrives.

`AccessDenied` is a new, minimal file `web/src/app/AccessDenied.tsx`, sibling to the existing `web/src/app/NotFound.tsx`, following the same structure (a `<section>` with a `data-testid`). It is intentionally narrow — just the "not permitted" copy needed here — since the full error-boundary component set is a separate ticket.

### Route wiring

In `web/src/app/router.tsx`, wrap the reserved routes per the parent spec's route-map table:

- `stacks/new` → `<RequireCapability capability="canCreateStack" mode="route">`
- `stacks/:stackId`, `stacks/:stackId/template`, `stacks/:stackId/runs`, `stacks/:stackId/runs/:runId` → each nested under a `stacks/:stackId` layout route gated with `<RequireCapability capability="canView" mode="route">`, so the guard runs once per stack rather than once per child route.
- `stacks/:stackId/access` → `<RequireCapability capability="canManageAccess" mode="route">` (nested inside the `canView`-gated layout, since access management still requires being able to view the stack)

Routes with `none` in the gate column (`/stacks`, `/templates`) are unchanged.

## Testing

Vitest unit tests (existing setup, colocated `*.test.tsx` files):

- `RequireCapability.test.tsx` — all four gate outcomes: allowed (renders children/Outlet), denied-with-reason (`fallback` rendered in gate mode, `AccessDenied` rendered in route mode for a non-`canView` capability), denied-silent (`null` in gate mode with no `fallback`, `NotFound` in route mode for `canView`), loading (`null` in both modes).
- `useStackCapabilities.test.ts` (or `.tsx` if a query-client wrapper is needed) — returns `undefined` before the stack query resolves, returns the cached `effectiveCapabilities` once it does, without triggering a second network call.
- `router.test.tsx` — extend existing route-config assertions to confirm the new routes are wrapped with `RequireCapability` where the table requires it.

## Open questions

None — this scope is narrow and fully determined by the already-approved parent spec.
