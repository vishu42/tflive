# AUTH-018 Keycloak OIDC Sign-in — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the `MockAuthProvider` with real Keycloak OIDC authentication using `oidc-client-ts`, keeping tokens in memory only, injecting Bearer tokens into API calls, and handling sign-in, logout, refresh, and error states.

**Architecture:** Custom React OIDC provider wrapping `oidc-client-ts`'s `UserManager`. Tokens live in a custom `StateStore` that stores in a module-level `Map` (never localStorage/sessionStorage). The provider derives `Me` identity from id_token claims + access token realm roles. Refresh happens via `signinSilent()` with `useRefreshToken: true`. Logout uses `signoutRedirect()` to terminate the Keycloak session.

**Tech Stack:** React 18, TypeScript 5.6, Vite 8, vitest 4, oidc-client-ts, react-router-dom 6.30, @tanstack/react-query 5

## Global Constraints

- Remove mock auth completely (`MockAuthProvider`, `mockUsers`, `VITE_TFLIVE_MOCK_USER_ROLE`)
- Tokens never touch localStorage or sessionStorage
- PKCE S256, Authorization Code flow, public client (no client secret)
- `useRefreshToken: true` for silent refresh (no iframe-based silent renew)
- `/auth/callback` handles both signin and signout redirect callbacks
- Existing `AuthContext` interface (`login`, `logout`, `me`, `status`) preserved; `"error"` added to `AuthStatus`

---

### Task 1: Install oidc-client-ts and extend AuthContext with error status

**Files:**
- Modify: `web/src/auth/AuthContext.ts:4`
- Modify: `web/package.json`

**Interfaces:**
- Produces: `AuthStatus = "loading" | "authenticated" | "unauthenticated" | "error"` for all downstream consumers

- [ ] **Step 1: Install oidc-client-ts**

```bash
npm install oidc-client-ts
```

Workdir: `web`

- [ ] **Step 2: Verify install**

```bash
node -e "require('oidc-client-ts'); console.log('ok')"
```

Workdir: `web`  
Expected: prints `ok`

- [ ] **Step 3: Add `"error"` to `AuthStatus` type in AuthContext.ts**

File: `web/src/auth/AuthContext.ts`

Change line 4 from:
```ts
export type AuthStatus = "loading" | "authenticated" | "unauthenticated";
```
to:
```ts
export type AuthStatus = "loading" | "authenticated" | "unauthenticated" | "error";
```

- [ ] **Step 4: Verify existing tests still pass with the type change**

```bash
npx vitest run
```

Workdir: `web`  
Expected: all existing tests pass (MockAuthProvider's `status` values were already a subset)

- [ ] **Step 5: Commit**

```bash
git add web/package.json web/package-lock.json web/src/auth/AuthContext.ts
git commit -m "chore(web): install oidc-client-ts and add error status to AuthContext"
```

---

### Task 2: Create InMemoryStore (oidc-client-ts StateStore adapter)

**Files:**
- Create: `web/src/auth/InMemoryStore.ts`
- Create: `web/src/auth/InMemoryStore.test.ts`

**Interfaces:**
- Produces: `InMemoryStore` class implementing oidc-client-ts `StateStore` interface with `set(key, value)`, `get(key)`, `remove(key)`, `getAllKeys()`

- [ ] **Step 1: Write the test file**

```ts
// web/src/auth/InMemoryStore.test.ts
import { describe, expect, it } from "vitest";
import { InMemoryStore } from "./InMemoryStore";

describe("InMemoryStore", () => {
  it("stores and retrieves a value", async () => {
    const store = new InMemoryStore();
    await store.set("key1", "value1");

    const result = await store.get("key1");
    expect(result).toBe("value1");
  });

  it("returns null for a missing key", async () => {
    const store = new InMemoryStore();
    const result = await store.get("missing");
    expect(result).toBeNull();
  });

  it("removes a key and returns the old value", async () => {
    const store = new InMemoryStore();
    await store.set("key1", "value1");

    const removed = await store.remove("key1");
    expect(removed).toBe("value1");
    expect(await store.get("key1")).toBeNull();
  });

  it("returns null when removing a missing key", async () => {
    const store = new InMemoryStore();
    const removed = await store.remove("missing");
    expect(removed).toBeNull();
  });

  it("getAllKeys returns all stored keys", async () => {
    const store = new InMemoryStore();
    await store.set("a", "1");
    await store.set("b", "2");

    const keys = await store.getAllKeys();
    expect(keys).toContain("a");
    expect(keys).toContain("b");
    expect(keys).toHaveLength(2);
  });

  it("stores and gets JSON-serialized User data", async () => {
    const store = new InMemoryStore();
    const userJson = JSON.stringify({ sub: "user_1", access_token: "at" });

    await store.set("user", userJson);
    const result = await store.get("user");

    expect(JSON.parse(result!)).toEqual({ sub: "user_1", access_token: "at" });
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

```bash
npx vitest run src/auth/InMemoryStore.test.ts
```

Workdir: `web`  
Expected: FAIL — module not found

- [ ] **Step 3: Implement InMemoryStore**

```ts
// web/src/auth/InMemoryStore.ts
import type { StateStore } from "oidc-client-ts";

const store = new Map<string, string>();

export class InMemoryStore implements StateStore {
  async set(key: string, value: string): Promise<void> {
    store.set(key, value);
  }

  async get(key: string): Promise<string | null> {
    return store.get(key) ?? null;
  }

  async remove(key: string): Promise<string | null> {
    const value = store.get(key) ?? null;
    store.delete(key);
    return value;
  }

  async getAllKeys(): Promise<string[]> {
    return Array.from(store.keys());
  }
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
npx vitest run src/auth/InMemoryStore.test.ts
```

Workdir: `web`  
Expected: 6 passed

- [ ] **Step 5: Commit**

```bash
git add web/src/auth/InMemoryStore.ts web/src/auth/InMemoryStore.test.ts
git commit -m "feat(web): add in-memory StateStore adapter for oidc-client-ts"
```

---

### Task 3: Create OIDC configuration

**Files:**
- Create: `web/src/auth/oidcConfig.ts`

**Interfaces:**
- Produces: `oidcConfig` object with `authority`, `client_id`, `redirect_uri`, `post_logout_redirect_uri`, `response_type`, `scope`, `useRefreshToken`

- [ ] **Step 1: Implement oidcConfig**

```ts
// web/src/auth/oidcConfig.ts
export const oidcConfig = {
  authority: import.meta.env.VITE_OIDC_ISSUER ?? "http://localhost:8082/realms/tflive",
  client_id: import.meta.env.VITE_OIDC_CLIENT_ID ?? "tflive-web",
  redirect_uri: import.meta.env.VITE_OIDC_REDIRECT_URI ?? "http://localhost:5173/auth/callback",
  post_logout_redirect_uri: import.meta.env.VITE_OIDC_REDIRECT_URI ?? "http://localhost:5173/auth/callback",
  response_type: "code",
  scope: "openid profile email",
  useRefreshToken: true,
  loadUserInfo: false,
} as const;
```

- [ ] **Step 2: Commit**

```bash
git add web/src/auth/oidcConfig.ts
git commit -m "feat(web): add OIDC configuration module"
```

---

### Task 4: Create UserManager singleton

**Files:**
- Create: `web/src/auth/userManager.ts`
- Create: `web/src/auth/userManager.test.ts`

**Interfaces:**
- Produces: `getUserManager(): UserManager` singleton with in-memory store

- [ ] **Step 1: Write the test file**

```ts
// web/src/auth/userManager.test.ts
import { describe, expect, it } from "vitest";
import { getUserManager } from "./userManager";

describe("getUserManager", () => {
  it("returns the same instance on repeated calls", () => {
    const first = getUserManager();
    const second = getUserManager();
    expect(first).toBe(second);
  });

  it("returns a UserManager with the configured authority", async () => {
    const manager = getUserManager();
    // settings are available on the instance
    expect(manager.settings.authority).toContain("tflive");
  });

  it("uses PKCE (response_type is code)", () => {
    const manager = getUserManager();
    expect(manager.settings.response_type).toBe("code");
  });

  it("enables refresh token usage", () => {
    const manager = getUserManager();
    expect(manager.settings.useRefreshToken).toBe(true);
  });

  it("does not load user info from /userinfo endpoint", () => {
    const manager = getUserManager();
    expect(manager.settings.loadUserInfo).toBe(false);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

```bash
npx vitest run src/auth/userManager.test.ts
```

Workdir: `web`  
Expected: FAIL — module not found

- [ ] **Step 3: Implement userManager singleton**

```ts
// web/src/auth/userManager.ts
import { UserManager, WebStorageStateStore } from "oidc-client-ts";
import { InMemoryStore } from "./InMemoryStore";
import { oidcConfig } from "./oidcConfig";

let userManager: UserManager | null = null;

export function getUserManager(): UserManager {
  if (!userManager) {
    userManager = new UserManager({
      ...oidcConfig,
      userStore: new WebStorageStateStore({ store: new InMemoryStore() }),
    });
  }
  return userManager;
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
npx vitest run src/auth/userManager.test.ts
```

Workdir: `web`  
Expected: 5 passed

- [ ] **Step 5: Commit**

```bash
git add web/src/auth/userManager.ts web/src/auth/userManager.test.ts
git commit -m "feat(web): add UserManager singleton with in-memory store"
```

---

### Task 5: Create convertUser utility (User → Me mapping)

**Files:**
- Create: `web/src/auth/convertUser.ts`
- Create: `web/src/auth/convertUser.test.ts`

**Interfaces:**
- Consumes: `Me` from `types.ts`, `User` from `oidc-client-ts`
- Produces: `convertUserToMe(user: User): Me` — maps OIDC `User` profile and realm roles to `Me` contract

- [ ] **Step 1: Write the test file**

```ts
// web/src/auth/convertUser.test.ts
import { describe, expect, it } from "vitest";
import type { User } from "oidc-client-ts";
import { convertUserToMe } from "./convertUser";

function user(overrides: Partial<{
  sub: string;
  preferred_username: string;
  email: string;
  realmRoles: string[];
}> = {}): User {
  const { sub, preferred_username, email, realmRoles } = {
    sub: "user-abc-123",
    preferred_username: "ada",
    email: "ada@test.local",
    realmRoles: [],
    ...overrides,
  };
  return {
    id_token: "",
    session_state: null,
    access_token: "at",
    refresh_token: "rt",
    token_type: "Bearer",
    scope: "openid",
    profile: {
      sub,
      preferred_username,
      email,
      realm_access: { roles: realmRoles },
    } as Record<string, unknown>,
    expires_at: Date.now() / 1000 + 300,
    state: null,
  } as unknown as User;
}

describe("convertUserToMe", () => {
  it("maps sub from the token profile", () => {
    const result = convertUserToMe(user({ sub: "kc-001" }));
    expect(result.sub).toBe("kc-001");
  });

  it("uses preferred_username as displayName", () => {
    const result = convertUserToMe(user({ preferred_username: "alice" }));
    expect(result.displayName).toBe("alice");
  });

  it("falls back to sub when preferred_username is missing", () => {
    const u = user({ preferred_username: undefined as unknown as string, sub: "kc-no-name" });
    const result = convertUserToMe(u);
    expect(result.displayName).toBe("kc-no-name");
  });

  it("maps email from the token profile", () => {
    const result = convertUserToMe(user({ email: "alice@test.local" }));
    expect(result.email).toBe("alice@test.local");
  });

  it("resolves platform-admin capability when realm role is present", () => {
    const result = convertUserToMe(user({ realmRoles: ["platform-admin"] }));
    expect(result.globalCapabilities.isPlatformAdmin).toBe(true);
    expect(result.globalCapabilities.canCreateStack).toBe(true);
  });

  it("resolves stack-creator capability when only that realm role is present", () => {
    const result = convertUserToMe(user({ realmRoles: ["stack-creator"] }));
    expect(result.globalCapabilities.isPlatformAdmin).toBe(false);
    expect(result.globalCapabilities.canCreateStack).toBe(true);
  });

  it("returns false for all capabilities when no realm roles are present", () => {
    const result = convertUserToMe(user());
    expect(result.globalCapabilities.isPlatformAdmin).toBe(false);
    expect(result.globalCapabilities.canCreateStack).toBe(false);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

```bash
npx vitest run src/auth/convertUser.test.ts
```

Workdir: `web`  
Expected: FAIL — module not found

- [ ] **Step 3: Implement convertUserToMe**

```ts
// web/src/auth/convertUser.ts
import type { User } from "oidc-client-ts";
import type { Me } from "./types";

export function convertUserToMe(user: User): Me {
  const profile = user.profile as Record<string, unknown>;
  const realmAccess = profile.realm_access as { roles?: string[] } | undefined;
  const roles = realmAccess?.roles ?? [];

  return {
    sub: profile.sub as string,
    displayName: (profile.preferred_username as string) ?? (profile.sub as string),
    email: profile.email as string | undefined,
    globalCapabilities: {
      isPlatformAdmin: roles.includes("platform-admin"),
      canCreateStack: roles.includes("stack-creator") || roles.includes("platform-admin"),
    },
  };
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
npx vitest run src/auth/convertUser.test.ts
```

Workdir: `web`  
Expected: 7 passed

- [ ] **Step 5: Commit**

```bash
git add web/src/auth/convertUser.ts web/src/auth/convertUser.test.ts
git commit -m "feat(web): add convertUserToMe for OIDC User to Me mapping"
```

---

### Task 6: Create OidcAuthProvider (replaces MockAuthProvider)

**Files:**
- Create: `web/src/auth/OidcAuthProvider.tsx`
- Create: `web/src/auth/OidcAuthProvider.test.tsx`

**Architecture note:** `OidcAuthProvider` is a **layout route** (renders `<Outlet />`). It lives inside the router so it can use `useLocation()`. It wraps `AuthContext.Provider` around its `<Outlet />`.

**Interfaces:**
- Consumes: `AuthContext`, `AuthContextValue`, `AuthStatus` from `AuthContext.ts`, `Me` from `types.ts`, `convertUserToMe` from `convertUser.ts`, `getUserManager` from `userManager.ts`
- Produces: `OidcAuthProvider` layout component wrapping `AuthContext.Provider` with OIDC lifecycle + `<Outlet />`

- [ ] **Step 1: Write the test file**

```ts
// web/src/auth/OidcAuthProvider.test.tsx
// @vitest-environment jsdom
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi, beforeEach, afterEach } from "vitest";
import type { ReactNode } from "react";

const mockGetUser = vi.fn();
const mockSigninRedirect = vi.fn();
const mockSigninRedirectCallback = vi.fn();
const mockSignoutRedirect = vi.fn();
const mockSignoutRedirectCallback = vi.fn();
const mockSigninSilent = vi.fn();

let currentPathname = "/";
let currentSearch = "";

vi.mock("./userManager", () => ({
  getUserManager: () => ({
    getUser: mockGetUser,
    signinRedirect: mockSigninRedirect,
    signinRedirectCallback: mockSigninRedirectCallback,
    signoutRedirect: mockSignoutRedirect,
    signoutRedirectCallback: mockSignoutRedirectCallback,
    signinSilent: mockSigninSilent,
    settings: { authority: "http://localhost:8082/realms/tflive" },
  }),
}));

const mockNavigate = vi.fn();

vi.mock("react-router-dom", async (importOriginal) => {
  const actual = await importOriginal<typeof import("react-router-dom")>();
  return {
    ...actual,
    useLocation: () => ({ pathname: currentPathname, search: currentSearch }),
    useNavigate: () => mockNavigate,
  };
});

function userFixture() {
  return {
    id_token: "",
    session_state: null,
    access_token: "at-fixture",
    refresh_token: "rt-fixture",
    token_type: "Bearer",
    scope: "openid",
    profile: {
      sub: "user-1",
      preferred_username: "testuser",
      email: "test@local",
      realm_access: { roles: ["platform-admin"] },
    },
    expires_at: Date.now() / 1000 + 300,
    state: null,
  };
}

describe("OidcAuthProvider", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    currentPathname = "/";
    currentSearch = "";
  });

  afterEach(() => {
    vi.resetModules();
  });

  function renderProvider(children: React.ReactNode): string {
    const { createMemoryRouter, RouterProvider } = require("react-router-dom");
    const router = createMemoryRouter(
      [
        {
          path: "/",
          element: children,
          children: [{ index: true, element: <div /> }],
        },
        {
          path: "/stacks",
          element: children,
          children: [{ index: true, element: <div data-testid="child">hello</div> }],
        },
        {
          path: "/auth/callback",
          element: children,
          children: [{ index: true, element: <div /> }],
        },
      ],
      { initialEntries: [{ pathname: currentPathname, search: currentSearch }] }
    );
    return renderToStaticMarkup(<RouterProvider router={router} />);
  }

  async function importProvider() {
    const mod = await import("./OidcAuthProvider");
    return mod.default;
  }

  it("renders children via Outlet when user is already authenticated", async () => {
    mockGetUser.mockResolvedValue(userFixture());
    currentPathname = "/stacks";

    const OidcAuthProvider = await importProvider();
    const markup = renderProvider(<OidcAuthProvider />);

    expect(markup).toContain('data-testid="child"');
    expect(mockSigninRedirect).not.toHaveBeenCalled();
  });

  it("triggers signin redirect when no user is found", async () => {
    mockGetUser.mockResolvedValue(null);
    mockSigninRedirect.mockResolvedValue(undefined);

    const OidcAuthProvider = await importProvider();
    renderProvider(<OidcAuthProvider />);

    expect(mockSigninRedirect).toHaveBeenCalled();
  });

  it("renders error UI when getUser fails (Keycloak unreachable)", async () => {
    mockGetUser.mockRejectedValue(new Error("Network error"));

    const OidcAuthProvider = await importProvider();
    const markup = renderProvider(<OidcAuthProvider />);

    expect(markup).toContain('data-testid="auth-error"');
    expect(markup).toContain('data-testid="auth-retry-button"');
  });

  it("handles signin callback, then navigates to original route", async () => {
    currentPathname = "/auth/callback";
    currentSearch = "?code=abc&state=xyz";
    mockSigninRedirectCallback.mockResolvedValue(userFixture());

    const OidcAuthProvider = await importProvider();
    renderProvider(<OidcAuthProvider />);

    expect(mockSigninRedirectCallback).toHaveBeenCalled();
    expect(mockNavigate).toHaveBeenCalled();
  });

  it("handles signout callback, then redirects to signin", async () => {
    currentPathname = "/auth/callback";
    currentSearch = "?state=xyz";
    mockSignoutRedirectCallback.mockResolvedValue({} as never);
    mockSigninRedirect.mockResolvedValue(undefined);

    const OidcAuthProvider = await importProvider();
    renderProvider(<OidcAuthProvider />);

    expect(mockSignoutRedirectCallback).toHaveBeenCalled();
    expect(mockSigninRedirect).toHaveBeenCalled();
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

```bash
npx vitest run src/auth/OidcAuthProvider.test.ts
```

Workdir: `web`  
Expected: FAIL — module not found

- [ ] **Step 3: Implement OidcAuthProvider**

```tsx
// web/src/auth/OidcAuthProvider.tsx
import { useCallback, useEffect, useState } from "react";
import { Outlet, useLocation, useNavigate } from "react-router-dom";
import { AuthContext } from "./AuthContext";
import type { AuthStatus } from "./AuthContext";
import type { Me } from "./types";
import { getUserManager } from "./userManager";
import { convertUserToMe } from "./convertUser";

export default function OidcAuthProvider() {
  const [me, setMe] = useState<Me | null>(null);
  const [status, setStatus] = useState<AuthStatus>("loading");
  const location = useLocation();
  const navigate = useNavigate();

  useEffect(() => {
    const isCallbackPath = location.pathname === "/auth/callback";

    if (isCallbackPath) {
      const isSignoutCallback = !location.search.includes("code=");

      if (isSignoutCallback) {
        getUserManager().signoutRedirectCallback()
          .then(() => {
            setMe(null);
            setStatus("unauthenticated");
            getUserManager().signinRedirect();
          })
          .catch(() => setStatus("error"));
      } else {
        getUserManager().signinRedirectCallback()
          .then((user) => {
            setMe(convertUserToMe(user));
            setStatus("authenticated");
            // Navigate to the original requested route (or / as fallback).
            // oidc-client-ts stores the original URL in session; we use / as default.
            const target = (user.state as string) ?? "/stacks";
            navigate(target, { replace: true });
          })
          .catch(() => setStatus("error"));
      }
      return;
    }

    getUserManager().getUser()
      .then((user) => {
        if (user && !user.expired) {
          setMe(convertUserToMe(user));
          setStatus("authenticated");
        } else if (user?.expired && user.refresh_token) {
          getUserManager().signinSilent()
            .then((refreshedUser) => {
              setMe(convertUserToMe(refreshedUser));
              setStatus("authenticated");
            })
            .catch(() => {
              setStatus("unauthenticated");
              getUserManager().signinRedirect();
            });
        } else {
          setStatus("unauthenticated");
          getUserManager().signinRedirect();
        }
      })
      .catch(() => setStatus("error"));
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  const login = useCallback(() => {
    getUserManager().signinRedirect();
  }, []);

  const logout = useCallback(() => {
    getUserManager().signoutRedirect();
  }, []);

  if (status === "loading") {
    return null;
  }

  if (status === "error") {
    return (
      <div data-testid="auth-error">
        <p>Authentication failed. The identity service may be unavailable.</p>
        <button type="button" onClick={login} data-testid="auth-retry-button">
          Retry
        </button>
      </div>
    );
  }

  return (
    <AuthContext.Provider value={{ me, status, login, logout }}>
      <Outlet />
    </AuthContext.Provider>
  );
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
npx vitest run src/auth/OidcAuthProvider.test.ts
```

Workdir: `web`  
Expected: 5 passed

- [ ] **Step 5: Commit**

```bash
git add web/src/auth/OidcAuthProvider.tsx web/src/auth/OidcAuthProvider.test.tsx
git commit -m "feat(web): add OidcAuthProvider with sign-in/sign-out lifecycle"
```

---

### Task 7: Create CallbackPage

**Files:**
- Create: `web/src/auth/CallbackPage.tsx`
- Create: `web/src/auth/CallbackPage.test.tsx`

**Interfaces:**
- Produces: `CallbackPage` component that renders a loading state during OIDC callback processing

- [ ] **Step 1: Write the test file**

```ts
// web/src/auth/CallbackPage.test.tsx
// @vitest-environment jsdom
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import CallbackPage from "./CallbackPage";

describe("CallbackPage", () => {
  it("renders a signing-in indicator", () => {
    const markup = renderToStaticMarkup(<CallbackPage />);

    expect(markup).toContain("Signing in");
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

```bash
npx vitest run src/auth/CallbackPage.test.ts
```

Workdir: `web`  
Expected: FAIL — module not found

- [ ] **Step 3: Implement CallbackPage**

```tsx
// web/src/auth/CallbackPage.tsx
export default function CallbackPage() {
  return <div data-testid="callback-loading">Signing in...</div>;
}
```

The actual OIDC callback processing happens in `OidcAuthProvider`'s `useEffect` when it detects the `/auth/callback` path. `CallbackPage` just renders a brief loading indicator while the provider completes the flow and redirects.

- [ ] **Step 4: Run test to verify it passes**

```bash
npx vitest run src/auth/CallbackPage.test.ts
```

Workdir: `web`  
Expected: 1 passed

- [ ] **Step 5: Commit**

```bash
git add web/src/auth/CallbackPage.tsx web/src/auth/CallbackPage.test.tsx
git commit -m "feat(web): add CallbackPage for OIDC redirect handling"
```

---

### Task 8: Wire OidcAuthProvider as layout route, CallbackPage into router

**Files:**
- Modify: `web/src/main.tsx:5,13-19` — remove MockAuthProvider wrap
- Modify: `web/src/app/router.tsx` — add OidcAuthProvider as root layout, wire CallbackPage

**Architecture change:** OidcAuthProvider moves from being a provider wrapper around RouterProvider to being a **layout route** inside the router. This gives it access to `useLocation()` and `useNavigate()`.

**Interfaces:**
- Consumes: `OidcAuthProvider` from `auth/OidcAuthProvider`, `CallbackPage` from `auth/CallbackPage`

- [ ] **Step 1: Update main.tsx — remove MockAuthProvider, keep only QueryClientProvider + RouterProvider**

Change `web/src/main.tsx`:

Replace the entire file content:
```tsx
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

(Removed the `import MockAuthProvider` and the `<MockAuthProvider>` wrapper — OidcAuthProvider is now a route element.)

- [ ] **Step 2: Update router.tsx — add OidcAuthProvider as root layout**

In `web/src/app/router.tsx`:

Add imports at top:
```ts
import OidcAuthProvider from "../auth/OidcAuthProvider";
import CallbackPage from "../auth/CallbackPage";
```

Change the route config to nest everything under OidcAuthProvider:

```ts
export const routeConfig: RouteObject[] = [
  {
    path: "/",
    element: <OidcAuthProvider />,
    children: [
      {
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
              {
                element: <StackDetailShell />,
                children: [
                  { index: true, element: <RoutePlaceholder title="Stack overview" /> },
                  { path: "template", element: <RoutePlaceholder title="Stack template" /> },
                  { path: "runs", element: <RoutePlaceholder title="Runs" /> },
                  { path: "runs/:runId", element: <RoutePlaceholder title="Run detail" /> },
                  {
                    path: "access",
                    element: <RequireCapability capability="canManageAccess" mode="route" />,
                    children: [{ index: true, element: <RoutePlaceholder title="Access" /> }]
                  }
                ]
              }
            ]
          },
          { path: "templates", element: <TemplateRegistryScreen /> },
          { path: "auth/callback", element: <CallbackPage /> },
          { path: "*", element: <NotFound /> }
        ]
      }
    ]
  }
];
```

- [ ] **Step 3: Commit**

```bash
git add web/src/main.tsx web/src/app/router.tsx
git commit -m "feat(web): wire OidcAuthProvider as layout route and CallbackPage"
```

---

### Task 9: Inject Bearer token into API client

**Files:**
- Modify: `web/src/api/client.ts:151-178` (internal `fetch` wrapper to inject auth header)
- Create: `web/src/api/client.test.ts` (add new test cases)

**Interfaces:**
- Consumes: `getUserManager` from `auth/userManager`

- [ ] **Step 1: Write additional test cases**

Append to `web/src/api/client.test.ts` (after line 219):

```ts
// web/src/api/client.test.ts — ADD the following at the bottom of the file, before the helper functions

import { getUserManager } from "../auth/userManager";

vi.mock("../auth/userManager", () => ({
  getUserManager: () => ({
    getUser: vi.fn(),
    signinSilent: vi.fn(),
    signinRedirect: vi.fn(),
  }),
}));

describe("api client — auth header injection", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("adds Authorization header with current access token", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(jsonResponse([{ id: "stack_1" }]));
    const user = { access_token: "test-token-abc", expires_at: Date.now() / 1000 + 300 };
    vi.mocked(getUserManager().getUser).mockResolvedValue(user as never);

    await listStacks("tenant_123");

    expect(fetchMock).toHaveBeenCalledWith(
      "/v1/tenants/tenant_123/stacks",
      expect.objectContaining({
        headers: expect.any(Headers),
      })
    );
    const callHeaders = fetchMock.mock.calls[0][1]?.headers as Headers;
    expect(callHeaders.get("authorization")).toBe("Bearer test-token-abc");
  });

  it("skips auth header when no user is available", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(jsonResponse([{ id: "stack_1" }]));
    vi.mocked(getUserManager().getUser).mockResolvedValue(null);

    await listStacks("tenant_123");

    const callHeaders = fetchMock.mock.calls[0][1]?.headers as Headers;
    expect(callHeaders.get("authorization")).toBeNull();
  });

  it("refreshes token and retries when API returns 401", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch")
      .mockResolvedValueOnce(new Response(JSON.stringify({ error: "unauthorized", message: "expired" }), {
        status: 401,
        headers: { "content-type": "application/json" },
      }))
      .mockResolvedValueOnce(new Response(null, { status: 401 })) // second attempt (the retry)
      .mockResolvedValueOnce(jsonResponse([{ id: "stack_2" }])); // after redirect would happen

    const user = { access_token: "old-token", expires_at: Date.now() / 1000 + 300, refresh_token: "rt-1" };
    vi.mocked(getUserManager().getUser).mockResolvedValue(user as never);

    const refreshedUser = { access_token: "new-token", expires_at: Date.now() / 1000 + 300 };
    vi.mocked(getUserManager().signinSilent).mockResolvedValue(refreshedUser as never);

    // On 401, the client will: refresh -> retry. We expect at least the refresh call.
    try {
      await listStacks("tenant_123");
    } catch {
      // expected: the retry also fails in this test because the third mock is irrelevant to the retry URL
    }

    expect(getUserManager().signinSilent).toHaveBeenCalled();
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

```bash
npx vitest run src/api/client.test.ts
```

Workdir: `web`  
Expected: FAIL — new tests fail because auth header is not injected

- [ ] **Step 3: Implement auth header injection in client.ts**

Modify the internal `requestJSON`, `requestText`, and `requestNoContent` functions in `web/src/api/client.ts` to inject the Bearer token and handle 401 with refresh:

Add import at top:
```ts
import { getUserManager } from "../auth/userManager";
```

Replace the three internal functions (lines 151-178):

```ts
async function authHeaders(): Promise<HeadersInit> {
  const user = await getUserManager().getUser();
  if (!user?.access_token) {
    return {};
  }
  return { authorization: `Bearer ${user.access_token}` };
}

async function fetchWithAuth(path: string, init: RequestInit): Promise<Response> {
  const headers = new Headers(init.headers);
  if (!headers.has("content-type")) {
    headers.set("content-type", "application/json");
  }

  const authHdrs = await authHeaders();
  for (const [key, value] of Object.entries(authHdrs)) {
    headers.set(key, value);
  }

  const response = await fetch(path, { method: "GET", ...init, headers });

  if (response.status === 401) {
    try {
      await getUserManager().signinSilent();
      const retryUser = await getUserManager().getUser();
      if (retryUser?.access_token) {
        const retryHeaders = new Headers(init.headers);
        if (!retryHeaders.has("content-type")) {
          retryHeaders.set("content-type", "application/json");
        }
        retryHeaders.set("authorization", `Bearer ${retryUser.access_token}`);
        return fetch(path, { method: "GET", ...init, headers: retryHeaders });
      }
    } catch {
      getUserManager().signinRedirect();
      // Return the original 401 response so the caller can handle it
      return response;
    }
  }

  return response;
}

async function requestJSON<T>(path: string, init: RequestInit = {}): Promise<T> {
  const response = await fetchWithAuth(path, init);
  await throwForError(response);
  return response.json() as Promise<T>;
}

async function requestText(path: string, init: RequestInit = {}): Promise<string> {
  const response = await fetchWithAuth(path, init);
  await throwForError(response);
  return response.text();
}

async function requestNoContent(path: string, init: RequestInit = {}): Promise<void> {
  const response = await fetchWithAuth(path, init);
  await throwForError(response);
}
```

Remove the old `withJSONHeaders` function (lines 168-178) since its logic is now inline in `fetchWithAuth`.

- [ ] **Step 4: Run tests**

```bash
npx vitest run src/api/client.test.ts
```

Workdir: `web`  
Expected: all client tests pass (existing + new auth tests)

- [ ] **Step 5: Commit**

```bash
git add web/src/api/client.ts web/src/api/client.test.ts
git commit -m "feat(web): inject Bearer token into API calls with 401 refresh"
```

---

### Task 10: Update AppShell for loading/error states

**Files:**
- Modify: `web/src/app/AppShell.tsx:11-47`
- Modify: `web/src/shared/queryErrorBoundary.tsx:21-29`

**Interfaces:**
- Consumes: `useAuth` from `auth/AuthContext` (now with `"error"` status)

- [ ] **Step 1: Update AppShell to handle loading and error states**

Change the identity section in `web/src/app/AppShell.tsx`:

Replace lines 11-47:
```tsx
export default function AppShell() {
  const { me, logout, status } = useAuth();

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
          <div className="identity-menu" data-testid="identity-menu">
            {status === "loading" && (
              <span data-testid="identity-loading">Loading...</span>
            )}
            {me && (
              <>
                <span data-testid="identity-display-name">{me.displayName}</span>
                <button type="button" data-testid="logout-button" onClick={logout}>
                  Log out
                </button>
              </>
            )}
          </div>
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

- [ ] **Step 2: Update queryErrorBoundary — add `"error"` to status union type usage if needed**

No changes needed in `queryErrorBoundary.tsx` — it only reads `logout` and doesn't check `status`. The `logout()` function from `OidcAuthProvider` calls `signoutRedirect()` which is the correct behavior for a 401.

- [ ] **Step 3: Commit**

```bash
git add web/src/app/AppShell.tsx
git commit -m "feat(web): add loading state to AppShell identity section"
```

---

### Task 11: Migrate all existing tests to AuthContext.Provider and delete mock auth files

**Files:**
- Modify: `web/src/app/AppShell.test.tsx` — replace MockAuthProvider with AuthContext.Provider
- Modify: `web/src/app/router.test.tsx` — replace MockAuthProvider with AuthContext.Provider
- Modify: `web/src/features/stacks/StacksListScreen.test.tsx` — replace MockAuthProvider
- Modify: `web/src/features/stacks/StackDetailShell.test.tsx` — replace MockAuthProvider
- Modify: `web/src/features/templates/TemplateRegistryScreen.test.tsx` — replace MockAuthProvider
- Modify: `web/src/shared/queryErrorBoundary.test.tsx` — remove MockAuthProvider e2e test
- Delete: `web/src/auth/MockAuthProvider.tsx`
- Delete: `web/src/auth/MockAuthProvider.test.tsx`
- Delete: `web/src/auth/mockUsers.ts`
- Delete: `web/src/auth/mockUsers.test.ts`

**Interfaces:**
- Produces: `createTestAuthProvider(value)` helper — reusable test wrapper that replaces `MockAuthProvider` pattern

- [ ] **Step 1: Create test auth helper in existing test location**

Add to `web/src/auth/AuthContext.ts` (or use inline helpers in each test, since each test already has `authValue()` helper). The `authValue()` helper already exists in `RequireCapability.test.tsx` and `queryErrorBoundary.test.tsx`. We'll create a reusable one.

Actually, since each test file currently import `MockAuthProvider` differently (some with dynamic import, some static), the simplest approach: every test that used `MockAuthProvider` wrapper switches to using `AuthContext.Provider` directly with an `authValue()` helper. The `authValue` pattern already exists and works.

- [ ] **Step 2: Update AppShell.test.tsx**

Replace the entire file content:

```ts
// @vitest-environment jsdom
import { renderToStaticMarkup } from "react-dom/server";
import { createMemoryRouter, RouterProvider } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import { AuthContext } from "../auth/AuthContext";
import type { AuthContextValue } from "../auth/AuthContext";

function authValue(): AuthContextValue {
  return {
    me: {
      sub: "user_1",
      displayName: "Otto Operator",
      globalCapabilities: { isPlatformAdmin: false, canCreateStack: true },
    },
    status: "authenticated",
    login: () => {},
    logout: () => {},
  };
}

function TestAuthWrapper({ children }: { children: ReactNode }) {
  return <AuthContext.Provider value={authValue()}>{children}</AuthContext.Provider>;
}

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

    const markup = renderToStaticMarkup(
      <TestAuthWrapper>
        <RouterProvider router={testRouter} />
      </TestAuthWrapper>
    );

    expect(markup).toContain('href="/stacks"');
    expect(markup).toContain('href="/templates"');
    expect(markup).toContain('data-testid="identity-menu"');
    expect(markup).toContain('data-testid="shell-tenant-context"');
    expect(markup).toContain(">tenant_123<");
    expect(markup).not.toContain("<input");
    expect(markup).toContain('data-testid="outlet-content"');
  });

  it("displays the user's display name and a logout control", async () => {
    vi.stubEnv("VITE_TFLIVE_TENANT_ID", "tenant_123");
    const { default: AppShell } = await import("./AppShell");

    const testRouter = createMemoryRouter(
      [{ path: "/", element: <AppShell />, children: [{ index: true, element: <div /> }] }],
      { initialEntries: ["/"] }
    );

    const markup = renderToStaticMarkup(
      <TestAuthWrapper>
        <RouterProvider router={testRouter} />
      </TestAuthWrapper>
    );

    expect(markup).toContain("Otto Operator");
    expect(markup).toContain('data-testid="logout-button"');
  });
});
```

- [ ] **Step 3: Update router.test.tsx**

Replace all `<MockAuthProvider>` usage patterns with `<AuthContext.Provider value={authValue()}>` wrapper. The file is large (280 lines). Key pattern: everywhere `MockAuthProvider` is used, replace with:

```tsx
<AuthContext.Provider value={authValue({ me: { ...authValue().me!, globalCapabilities: { isPlatformAdmin: false, canCreateStack: true } } })}>
  <RouterProvider router={testRouter} />
</AuthContext.Provider>
```

Add import: `import { AuthContext } from "../auth/AuthContext";` and `import type { AuthContextValue } from "../auth/AuthContext";`

Add `authValue` helper (same as in RequireCapability.test.tsx):
```ts
function authValue(overrides: Partial<AuthContextValue> = {}): AuthContextValue {
  return {
    me: { sub: "user_1", displayName: "Test User", globalCapabilities: { isPlatformAdmin: false, canCreateStack: false } },
    status: "authenticated",
    login: () => {},
    logout: () => {},
    ...overrides,
  };
}
```

Remove all `const { default: MockAuthProvider } = await import("../auth/MockAuthProvider");` lines.

- [ ] **Step 4: Update StacksListScreen.test.tsx**

Replace lines 5-7 and 34-39: use `AuthContext.Provider` wrapper instead of `MockAuthProvider`.

- [ ] **Step 5: Update StackDetailShell.test.tsx**

Replace lines 10-12 and 33-36: use `AuthContext.Provider` wrapper instead of `MockAuthProvider`.

- [ ] **Step 6: Update TemplateRegistryScreen.test.tsx**

Replace lines 5-7 and 56-61: use `AuthContext.Provider` wrapper instead of `MockAuthProvider`.

- [ ] **Step 7: Update queryErrorBoundary.test.tsx**

Remove the `import MockAuthProvider` line (line 8). Remove the end-to-end test "end-to-end with the real MockAuthProvider..." (lines 110-125). This test was specific to MockAuthProvider's state change behavior.

- [ ] **Step 8: Delete mock auth files**

```bash
rm web/src/auth/MockAuthProvider.tsx
rm web/src/auth/MockAuthProvider.test.tsx
rm web/src/auth/mockUsers.ts
rm web/src/auth/mockUsers.test.ts
```

- [ ] **Step 9: Run full test suite to verify migration**

```bash
npx vitest run
```

Workdir: `web`  
Expected: all tests pass

- [ ] **Step 10: Commit**

```bash
git add web/src/
git commit -m "test(web): migrate tests from MockAuthProvider to AuthContext.Provider; remove mock auth files"
```

---

### Task 12: Final verification

**Files:** All

- [ ] **Step 1: Run TypeScript type check**

```bash
npx tsc -b --noEmit
```

Workdir: `web`  
Expected: no errors

- [ ] **Step 2: Run full test suite**

```bash
npx vitest run
```

Workdir: `web`  
Expected: all tests pass

- [ ] **Step 3: Run production build**

```bash
npm run build
```

Workdir: `web`  
Expected: build succeeds

- [ ] **Step 4: Verify no secrets in codebase**

```bash
git grep -i "password\|secret\|token" web/src/ | grep -v "test\|mock\|\.env\|ACCESS_TOKEN\|refresh_token\|access_token"
```

Expected: no output (no hardcoded secrets in source)

- [ ] **Step 5: Commit final verification**

```bash
git commit --allow-empty -m "chore(web): final verification — typecheck, tests, and build pass"
```
