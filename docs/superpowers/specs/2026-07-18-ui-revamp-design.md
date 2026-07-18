# tflive UI Revamp — Design

## Context

The current frontend (`web/`) is a React 18 + TypeScript + Vite single-page app good for testing but not production-ready:

- No router — one 846-line `App.tsx` with all panels (templates, stacks, runs, logs, an "IDs" debug panel) as conditional sections in a single component.
- No state/data-fetching library — hand-rolled `useState`/`useEffect` plus a custom `polling.ts`.
- No component library or design system — flat `web/src`, plain CSS in `styles.css`.
- No real identity — `config.ts` hardcodes a dev tenant fallback (`tenant_123`); tests reference a fixture actor (`user_123`); there is no `/v1/me` call.

In parallel, `docs/sprint/authn_and_authz/README.md` is delivering Keycloak authentication and OpenFGA authorization. As of this writing, AUTH-001 through AUTH-010 are `Done` (design, infra, realm, OpenFGA model, backend config, token verification, auth middleware, actor-identity removal, tenant enforcement, authorization port/adapter). Everything the frontend directly consumes — AUTH-011 through AUTH-021, including `/v1/me`, effective capabilities, Keycloak sign-in, and the stack Access UI — is still `Not Started`.

## Goal

Rebuild the frontend as a routed, componentized, production-shaped application designed against the **post-auth-sprint** contract, so that:

- The UI can be built and tested today against a defined, mocked identity/capability contract.
- AUTH-018–021 slot into reserved routes/interfaces without duplicated work — this revamp does not implement Keycloak sign-in, `/v1/me`, or the Access management screen's contents; it defines where they go and what shape they consume.
- No new screens or visual redesign are in scope. Existing functionality (templates, stacks, runs, logs) is restructured, not reimagined. The current CSS/visual style is preserved.

## Non-goals

- Visual/UX redesign (new look and feel). Out of scope; a possible later effort.
- Implementing AUTH-018 (Keycloak sign-in), AUTH-019 (`/v1/me` wiring), AUTH-020 (backend-driven permission data), or AUTH-021 (Access management screen contents) — those remain owned by the auth sprint.
- New screens beyond current functionality plus the auth sprint's reserved slots (no audit log viewer, no platform-admin dashboard, no profile page).
- New end-to-end test tooling — AUTH-023 owns end-to-end auth journeys once the relevant screens exist.

## Architecture

- **Routing**: React Router v6, data-router mode (`createBrowserRouter`). A top-level `<AppShell>` layout route wraps authenticated routes with nav, an identity/logout menu, and a fixed (non-editable) tenant indicator.
- **Data layer**: TanStack Query for all server state (stacks, templates, runs, logs), replacing the hand-rolled refetch loop in `polling.ts` with query `refetchInterval`. Existing `api/client.ts` fetch functions become query functions; the transport layer is not rewritten.
- **Folder structure** (feature-based):

```
web/src/
  app/            AppShell, router config, providers
  auth/           AuthProvider (mock + real), useAuth, capability hooks
  features/
    stacks/       list, detail shell, create-stack
    templates/    registry screen
    runs/         run list, run detail, logs
    access/       stack-access route scaffold (thin, see below)
  api/            existing client.ts, types.ts (extended)
  shared/         error boundaries, layout primitives, debug panel
```

## Route map & screens

| Route | Screen | Capability gate |
|---|---|---|
| `/stacks` | Stacks list (authz-filtered) | none (always visible if signed in) |
| `/stacks/new` | Create stack | `canCreateStack` |
| `/stacks/:stackId` | Stack detail shell (tab nav: Overview / Template / Runs / Access) | `canView` |
| `/stacks/:stackId/template` | Installed template + variables | `canView` (edit actions need `canOperate`) |
| `/stacks/:stackId/runs` | Runs list | `canView` |
| `/stacks/:stackId/runs/:runId` | Run detail (plan/apply, per-phase logs) | `canView`; plan/apply need `canOperate`, approve needs `canApprove` |
| `/stacks/:stackId/access` | Access management | `canManageAccess` — **scaffold only**: route, nav tab, and capability gate reserved here; AUTH-021 builds the grant list/assign/revoke UI into this slot |
| `/templates` | Template registry (register/list revisions) | none (view) |
| `/auth/callback` | Keycloak redirect handler | reserved slot for AUTH-018, not built by this revamp |
| `*` | 404 | — |

This mirrors current functionality 1:1, plus reserved slots for what the auth sprint fills in. The debug "IDs" panel is not a route — it is a collapsible section in `AppShell`, rendered only when a debug flag (e.g. `import.meta.env.DEV` or `VITE_DEBUG`) is set, never in production builds.

## Capability gate mechanism

Capabilities are a relationship between `(user, stack)`, not a property of either alone. Global, resource-independent capabilities (`isPlatformAdmin`, `canCreateStack`) live on the authenticated user (`/v1/me`); per-stack capabilities live on the stack resource returned to that user, matching the AUTH-017 acceptance criteria split ("`/v1/me` returns... normalized global capabilities" vs "Stack responses expose effective capabilities"). This also avoids an `N+1`/staleness problem: a user-embedded `stackCapabilities` map would require front-loading or lazily fetching capabilities per stack; embedding on the stack response reuses data already being fetched.

The gate never computes permissions client-side — it only reads booleans the backend already decided.

```ts
type StackCapabilities = { canView: boolean; canOperate: boolean; canApprove: boolean; canManageAccess: boolean };

function useStackCapabilities(stackId: string): StackCapabilities | undefined {
  return useStackQuery(stackId).data?.effectiveCapabilities;
}

<RequireCapability capability="canOperate" fallback={<DisabledButton reason="Requires operator role" />}>
  <PlanButton stackId={stackId} />
</RequireCapability>
```

Two gating layers, same data source:

- **Route-level** (`<RequireCapability>` as a layout route): guards whole screens (`/stacks/new`, `/stacks/:id/access`). A `canView` failure renders the same "not found" UI as an actual 404 — never "access denied, this exists" — matching AUTH-013's no-leak requirement. A `canCreateStack`/`canManageAccess` failure (resource visibly exists, action isn't permitted) can show an explicit "not permitted" message, since that doesn't leak anything.
- **Component-level** (`<RequireCapability>` wrapping a button/panel): reads the already-cached stack query field, no extra fetch. Fails closed by default (renders nothing), with an explicit "explain why disabled" variant for actions like Plan/Apply, per AUTH-020's UX requirement.

**Mock vs real**: the mock `AuthProvider` and mocked stack responses populate `effectiveCapabilities` with dev-configurable values (e.g. a local role switcher). When AUTH-013/017 ship, only the data source changes — real API responses populate the same field. Gate components, hooks, and every screen built against them are unchanged. No authorization logic ever runs in the browser, consistent with the auth sprint's core principle: the API is the source of enforcement, frontend visibility is a usability aid only.

## Identity & capability contract

This is the interface the frontend commits to now and the backend implements for AUTH-017:

```ts
// GET /v1/me
interface Me {
  sub: string;              // Keycloak subject — immutable identity key
  displayName: string;
  email?: string;
  globalCapabilities: {
    isPlatformAdmin: boolean;
    canCreateStack: boolean;
  };
}

// Attached to every stack resource (list + detail)
interface StackCapabilities {
  canView: boolean;
  canOperate: boolean;
  canApprove: boolean;
  canManageAccess: boolean;
}

interface Stack {
  // ...existing fields...
  effectiveCapabilities: StackCapabilities;
}
```

Constraints: the frontend never receives raw tokens or role names, only pre-resolved booleans. Tenant ID is dropped as a client concern entirely — no field, no control; it is implicit in the configured deployment (AUTH-009/019).

## Error handling

Four distinct states, handled once in a shared query-error classifier rather than per-screen:

| Status | Meaning | UI behavior |
|---|---|---|
| `401` | Not authenticated / token expired | Redirect to Keycloak sign-in (reserved for AUTH-018's real flow; mock provider simulates by resetting to signed-out state) |
| `403` on an action | Authenticated but lacks capability | Inline "not permitted" message — should be rare since components are already gated, but the API is the real enforcement so this is the honest fallback if client state is stale |
| `404` on a protected resource | Doesn't exist *or* not visible to you | Generic "not found" — identical rendering for both cases, no distinguishing signal (AUTH-013's no-leak rule) |
| `503` | OpenFGA/Keycloak dependency down | Distinct banner: "Authorization service unavailable — try again shortly." Fails closed, never silently grants access |

Implemented as shared components (`<AccessDenied/>`, `<NotFound/>`, `<ServiceUnavailable/>`) plus a TanStack Query error-boundary hook mapping status → component, used by every route.

## Testing

- **Component/unit** (Vitest, existing setup): capability-gate rendering across all four gate outcomes (allowed, denied-with-reason, denied-silent, loading); error-state components render correctly per status code; existing `App.test.tsx` coverage is redistributed into feature folders as screens are extracted, not dropped.
- No new e2e tooling is introduced by this revamp. AUTH-023 owns end-to-end auth journeys once AUTH-018/021 exist.

## Relationship to the auth sprint

This revamp builds around AUTH-018 (Keycloak sign-in), AUTH-019 (`/v1/me` + tenant wiring), AUTH-020 (permission-aware UI data), and AUTH-021 (stack Access UI) rather than duplicating them. It defines and mocks the contract those tickets will fulfill, reserves their routes/UI slots, and ends with a narrow integration ticket to swap mocks for real data once they land.

## Ticket breakdown

Foundation, then screens, then polish, then an integration checkpoint:

**Foundation**
1. Routing & app shell (React Router, layout, nav, 404)
2. Identity & capability contract (the TS types above, documented)
3. TanStack Query data layer (replaces `polling.ts`)
4. AuthProvider abstraction + mock implementation
5. Capability-gating primitives (`useStackCapabilities`, `<RequireCapability>`, route guards)
6. Shared error/session boundaries (401/403/404/503)

**Screens** (feature-based extraction from `App.tsx`)
7. Feature-folder restructure (pure refactor, no behavior change)
8. Stacks list screen
9. Template registry screen
10. Stack detail shell (tab nav)
11. Stack template & variables screen
12. Runs list & run detail screens (plan/apply/logs)
13. Stack Access route scaffold — route + nav tab + capability gate only; AUTH-021 builds the grant UI into this slot

**Polish**
14. Dev-only debug panel (gated IDs panel)
15. Test coverage for gating/error states

**Integration checkpoint**
16. Swap mock auth/capabilities for real APIs — depends on AUTH-017/018/019/020 landing; scoped narrowly since gate components don't change, only the data source

These become individual GitHub issues in the implementation plan.
