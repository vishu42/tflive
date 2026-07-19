# Shared Error & Session Boundaries — Design

## Context

This implements ticket 6 of the UI revamp breakdown (`docs/superpowers/specs/2026-07-18-ui-revamp-design.md`, "Error handling" section) as GitHub issue #70 (UI-006). Its dependencies are done: #67 added the TanStack Query data layer (`web/src/api/queries.ts`, `web/src/app/queryClient.ts`), and #68 added `useAuth()` / `AuthContext` / `MockAuthProvider`. Issue #69 (UI-005, capability-gating primitives) already built `<AccessDenied/>` and `<NotFound/>` (`web/src/app/AccessDenied.tsx`, `web/src/app/NotFound.tsx`) for the client-side, pre-fetch capability gate — its design doc explicitly scoped the full 401/403/404/503 error-boundary system out as "a separate issue" (this one).

Where #69's `<RequireCapability>` prevents a denied request from ever firing (gating on cached `effectiveCapabilities`), this ticket handles the case #69 didn't: an actual API response comes back 401/403/404/503, because client-side state was stale or the request wasn't gated at all. Per the parent spec: "the API is the real enforcement... this is the honest fallback if client state is stale."

## Goal

- A fourth shared screen, `<ServiceUnavailable/>`, alongside the existing `<AccessDenied/>` and `<NotFound/>`.
- One classifier hook, `useQueryErrorBoundary(error)`, that maps a query's `error` (a `web/src/api/client.ts` `ApiRequestError`, or anything else) to the right component per the parent spec's status table, so no screen re-implements this mapping.
- A `401` case additionally resets the mock session via `useAuth().logout()` — the closest a mock provider can get to "reauth path" ahead of AUTH-018's real Keycloak redirect.

## Non-goals

- Wiring the hook into `App.tsx` or any route. `App.tsx` is a 648-line legacy component slated for feature-folder extraction in tickets 7-13; per the parent spec, this revamp "restructures, not reimagines" it. Screens extracted in tickets 8-13 call `useQueryErrorBoundary(query.error)` themselves as they're built — same precedent as #69, which wired `<RequireCapability>` into `router.tsx` while every wrapped route still rendered `RoutePlaceholder`.
- Any real Keycloak redirect UI — reserved for AUTH-018. The `401` case here only resets `MockAuthProvider` to a signed-out state; nothing yet renders a sign-in screen for `status === "unauthenticated"` (also AUTH-018's job).
- Retry-policy changes in `web/src/app/queryClient.ts`. The parent spec's error table doesn't mention retry behavior, and 401/403/404 responses are already terminal within TanStack Query's normal retry window — not addressed here to keep this ticket to the classifier + components.
- Moving the existing `<AccessDenied/>`/`<NotFound/>` out of `web/src/app/` into a `shared/` folder. The parent spec's target folder structure names `shared/` for error boundaries, but #69 already placed these two in `app/`; relocating them is an unrelated refactor with its own blast radius (import updates across `RequireCapability.tsx`, `router.tsx`, and their tests). This ticket adds `<ServiceUnavailable/>` as a third sibling in `app/` to match, and introduces `web/src/shared/` only for the new classifier hook, which has no prior home.

## Design

### `<ServiceUnavailable/>`

New file `web/src/app/ServiceUnavailable.tsx`, sibling to `AccessDenied.tsx`/`NotFound.tsx`, same structure (a `<section>` with a `data-testid`):

```tsx
export default function ServiceUnavailable() {
  return (
    <section className="route-service-unavailable" data-testid="route-service-unavailable">
      <h1>Authorization service unavailable</h1>
      <p className="muted">Authorization service unavailable — try again shortly.</p>
    </section>
  );
}
```

Copy matches the parent spec's exact banner text ("Authorization service unavailable — try again shortly.").

### `MockAuthProvider` stability fix

`useQueryErrorBoundary`'s `401` handling calls `logout()` from a `useEffect`, with the effect's dependency array including `logout` (see below — omitting it would rely on a stale closure). Today, `MockAuthProvider` (`web/src/auth/MockAuthProvider.tsx`) creates new `login`/`logout` function references on every render:

```tsx
const value: AuthContextValue = {
  me: session.me,
  status: session.status,
  login: () => setSession({ me: mockUser, status: "authenticated" }),
  logout: () => setSession({ me: null, status: "unauthenticated" })
};
```

Because `setSession` always receives a fresh object (never bails out via `Object.is`), calling `logout()` triggers a re-render, which recreates `value` (and `logout`) with a new identity, which re-fires any effect that depends on `logout` — an infinite loop the moment any consumer (like this ticket's hook) puts `logout` in a dependency array. This is fixed by wrapping `login`/`logout` in `useCallback` so their identity is stable across re-renders:

```tsx
const login = useCallback(() => setSession({ me: mockUser, status: "authenticated" }), [mockUser]);
const logout = useCallback(() => setSession({ me: null, status: "unauthenticated" }), []);
```

No existing test asserts on function identity, so this is behavior-preserving for everything already built on `useAuth()`.

### `useQueryErrorBoundary`

New file `web/src/shared/queryErrorBoundary.tsx`, exporting a pure classifier and the hook:

```tsx
export type QueryErrorStatus = 401 | 403 | 404 | 503;

export function classifyQueryError(error: unknown): QueryErrorStatus | null {
  if (!(error instanceof ApiRequestError)) {
    return null;
  }
  return HANDLED_STATUSES.includes(error.status as QueryErrorStatus) ? (error.status as QueryErrorStatus) : null;
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
      return null; // reauth path is the logout() side effect; AUTH-018 renders the real sign-in screen
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

Mapping to the parent spec's table:

| Status | Component | Notes |
|---|---|---|
| `401` | none (`null`) | Side effect: `logout()` resets the mock session to signed-out. |
| `403` | `<AccessDenied/>` | Same component #69 uses for a non-`canView` route denial — an inline "not permitted" message either way. |
| `404` | `<NotFound/>` | Identical rendering to a capability-denied `canView`, per the no-leak rule — reusing the same component guarantees this by construction. |
| `503` | `<ServiceUnavailable/>` | New component above. |
| anything else (non-`ApiRequestError`, or an unhandled status like `500`) | `null` | Not this hook's concern — screens keep their existing generic error handling (e.g. `App.tsx`'s `messageFromError`) for statuses outside this table. |

`classifyQueryError` is exported separately (not just inlined in the hook) so it's independently unit-testable without mounting a component tree, and so a screen that needs the raw status (not just the rendered component) can call it directly.

**Why a hook, not a `<QueryErrorResetBoundary>`/React error boundary:** none of the existing `useQuery` calls set `throwOnError`, so query errors surface via the `error` field, not a thrown exception. Matching that, `useQueryErrorBoundary` takes the `error` value a screen already has (`someQuery.error`) and returns what to render in its place — no change to how queries report errors today.

## Testing

Vitest unit tests (existing setup, colocated `*.test.tsx` files):

- `ServiceUnavailable.test.tsx` — renders the `data-testid` and banner copy, mirroring `AccessDenied.test.tsx`.
- `MockAuthProvider.test.tsx` — extend with a case asserting `login`/`logout` keep the same reference across re-renders (the regression this ticket's hook would otherwise hit).
- `queryErrorBoundary.test.tsx` — `classifyQueryError` for all four handled statuses, an unhandled status (e.g. `500`), and non-`ApiRequestError` values (`undefined`, a plain `Error`); `useQueryErrorBoundary` (via `renderHook` under `@vitest-environment jsdom`, per the existing `useStackCapabilities.test.tsx`/`RequireCapability.test.tsx` pattern) for each status's rendered output, that `403`/`404`/`503` never call `logout`, and that a `401` calls `logout` exactly once even across re-renders with the same error (proving the `MockAuthProvider` fix actually prevents the loop) — plus one end-to-end case using the real `MockAuthProvider` (not a stub `AuthContext.Provider`) to confirm `status` flips to `"unauthenticated"` and the effect settles rather than looping.

## Open questions

None — scope is narrow and fully determined by the parent spec's "Error handling" section and #69's precedent for where the sibling screens live.
