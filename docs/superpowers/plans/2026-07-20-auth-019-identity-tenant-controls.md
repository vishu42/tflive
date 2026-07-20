# AUTH-019: Replace placeholder identity and tenant controls — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `GET /v1/me` backend endpoint and wire the frontend to derive identity, global capabilities, and tenant from the server instead of OIDC token claims.

**Architecture:** A new `internal/auth/me.go` defines the `MeResponse` type. `internal/api/server.go` adds a `handleMe` handler registered as `GET /v1/me` (authenticated, non-tenant-scoped). The frontend adds `getMe()` to the API client and `useMeQuery()` hook, then replumbs `OidcAuthProvider` to call `/v1/me` instead of `convertUserToMe()`.

**Tech Stack:** Go 1.22+ stdlib HTTP, TanStack Query, React, Vitest, TypeScript

## Global Constraints

- Go version: 1.22+ (uses pattern-style `http.ServeMux` routes)
- Backend test patterns: use `internal/api/server_test.go` conventions — `httptest.NewServer` or direct handler testing, `authn.Principal` injected into request context
- Frontend test patterns: `@testing-library/react`, `vitest` with `jsdom`, follow existing component test conventions
- No placeholder `"user_123"` in production source (tests may use it as mock data)
- Tenant ID comes from server state (`/v1/me`) not only env vars
- `fetchWithAuth` already handles 401 → silent refresh → retry → redirect

---

## File Structure

| File | Action | Responsibility |
|------|--------|---------------|
| `internal/auth/me.go` | CREATE | `MeResponse` type, `GlobalCapabilities` type, `meFromPrincipal()` mapping function |
| `internal/api/server.go` | MODIFY | Add `handleMe` handler, register `GET /v1/me` route |
| `internal/api/server_test.go` | MODIFY | Add integration tests for `/v1/me` |
| `web/src/auth/types.ts` | MODIFY | Add `tenantID` to `Me` interface |
| `web/src/api/queryKeys.ts` | MODIFY | Add `me` query key |
| `web/src/api/client.ts` | MODIFY | Export `getMe()` function |
| `web/src/auth/useMeQuery.ts` | CREATE | TanStack Query hook wrapping `getMe()` |
| `web/src/auth/useMeQuery.test.ts` | CREATE | Hook tests |
| `web/src/auth/OidcAuthProvider.tsx` | MODIFY | Replace `convertUserToMe()` with `useMeQuery` |
| `web/src/auth/__mocks__/OidcAuthProvider.tsx` | MODIFY | Add `email` and `tenantID` to mock data |
| `web/src/auth/convertUser.ts` | DELETE | No longer needed |
| `web/src/auth/convertUser.test.ts` | DELETE | No longer needed |
| `web/src/api/client.test.ts` | MODIFY | Add actor override proof test |

---

### Task 1: Backend MeResponse type and mapping (`internal/auth/me.go`)

**Files:**
- Create: `internal/auth/me.go`

**Interfaces:**
- Produces: `MeResponse` struct, `GlobalCapabilities` struct, `func meFromPrincipal(principal authn.Principal, tenantID traits.TenantID) MeResponse`

- [ ] **Step 1: Write the file**

```go
package auth

import (
	"slices"

	"github.com/vishu42/tflive/internal/authn"
	"github.com/vishu42/tflive/internal/traits"
)

// MeResponse is the identity envelope returned by GET /v1/me.
type MeResponse struct {
	Sub                string             `json:"sub"`
	DisplayName        string             `json:"displayName"`
	Email              string             `json:"email,omitempty"`
	GlobalCapabilities GlobalCapabilities `json:"globalCapabilities"`
	TenantID           string             `json:"tenantID"`
}

// GlobalCapabilities encodes coarse-grained permissions derived
// from the principal's Keycloak realm roles.
type GlobalCapabilities struct {
	IsPlatformAdmin bool `json:"isPlatformAdmin"`
	CanCreateStack  bool `json:"canCreateStack"`
}

// meFromPrincipal maps the authenticated principal to a MeResponse,
// deriving global capabilities from realm roles.
func MeFromPrincipal(principal authn.Principal, tenantID traits.TenantID) MeResponse {
	return MeResponse{
		Sub:                principal.Subject,
		DisplayName:        principal.Name,
		Email:              principal.Email,
		GlobalCapabilities: capabilitiesFromRoles(principal.RealmRoles),
		TenantID:           string(tenantID),
	}
}

func capabilitiesFromRoles(roles []string) GlobalCapabilities {
	var caps GlobalCapabilities
	for _, role := range roles {
		switch role {
		case "platform-admin":
			caps.IsPlatformAdmin = true
		case "stack-creator":
			caps.CanCreateStack = true
		}
	}
	_ = slices.Contains(roles, "unused-import-guard")
	return caps
}
```

Wait — the `slices.Contains` guard is ugly and wrong. Let me use a simpler approach without importing slices just for the guard.

Actually, I should just check `role == "platform-admin"` directly. No need for `slices`. Let me rewrite:

```go
package auth

import (
	"github.com/vishu42/tflive/internal/authn"
	"github.com/vishu42/tflive/internal/traits"
)

// MeResponse is the identity envelope returned by GET /v1/me.
type MeResponse struct {
	Sub                string             `json:"sub"`
	DisplayName        string             `json:"displayName"`
	Email              string             `json:"email,omitempty"`
	GlobalCapabilities GlobalCapabilities `json:"globalCapabilities"`
	TenantID           string             `json:"tenantID"`
}

// GlobalCapabilities encodes coarse-grained permissions derived
// from the principal's Keycloak realm roles.
type GlobalCapabilities struct {
	IsPlatformAdmin bool `json:"isPlatformAdmin"`
	CanCreateStack  bool `json:"canCreateStack"`
}

// MeFromPrincipal maps the authenticated principal to a MeResponse,
// deriving global capabilities from realm roles.
func MeFromPrincipal(principal authn.Principal, tenantID traits.TenantID) MeResponse {
	return MeResponse{
		Sub:                principal.Subject,
		DisplayName:        principal.Name,
		Email:              principal.Email,
		GlobalCapabilities: capabilitiesFromRoles(principal.RealmRoles),
		TenantID:           string(tenantID),
	}
}

func capabilitiesFromRoles(roles []string) GlobalCapabilities {
	var caps GlobalCapabilities
	for _, role := range roles {
		if role == "platform-admin" {
			caps.IsPlatformAdmin = true
		}
		if role == "stack-creator" {
			caps.CanCreateStack = true
		}
	}
	return caps
}
```

- [ ] **Step 2: Verify compilation**

```bash
go build ./internal/auth/
```

- [ ] **Step 3: Commit**

```bash
git add internal/auth/me.go
git commit -m "feat(auth): add MeResponse type and MeFromPrincipal mapping"
```

---

### Task 2: Add GET /v1/me handler and route (`internal/api/server.go`)

**Files:**
- Modify: `internal/api/server.go`

**Interfaces:**
- Consumes: `auth.MeFromPrincipal(principal authn.Principal, tenantID traits.TenantID) MeResponse` (Task 1)
- Produces: `func (server *Server) handleMe(response http.ResponseWriter, request *http.Request)` — registered as `GET /v1/me`

- [ ] **Step 1: Add the import**

In `internal/api/server.go`, add `"github.com/vishu42/tflive/internal/auth"` to the import block (lines 4-17):

```go
import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/vishu42/tflive/internal/app"
	"github.com/vishu42/tflive/internal/auth"
	"github.com/vishu42/tflive/internal/authn"
	"github.com/vishu42/tflive/internal/authz"
	"github.com/vishu42/tflive/internal/traits"
)
```

- [ ] **Step 2: Register the route**

In `NewServer()`, add the route registration after the health route (line 35) and before the template routes:

```go
// Identity route.
// Returns the authenticated principal's identity, global capabilities, and configured tenant.
server.mux.HandleFunc("GET /v1/me", server.handleMe)
```

Place it right after line 35 (`server.mux.HandleFunc("GET /healthz", server.handleHealth)`).

- [ ] **Step 3: Add the handler method**

Add the following method before `handleTenantRoute` (after line 89, before line 91):

```go
// handleMe returns the authenticated principal's identity, global capabilities,
// and the configured tenant ID.
func (server *Server) handleMe(response http.ResponseWriter, request *http.Request) {
	principal, ok := authn.PrincipalFromContext(request.Context())
	if !ok || principal.Subject == "" {
		writeError(response, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}
	writeJSON(response, http.StatusOK, auth.MeFromPrincipal(principal, server.tenantID))
}
```

- [ ] **Step 4: Verify compilation**

```bash
go build ./internal/api/
```

- [ ] **Step 5: Commit**

```bash
git add internal/api/server.go
git commit -m "feat(api): add GET /v1/me handler with principal identity"
```

---

### Task 3: Backend tests for GET /v1/me (`internal/api/server_test.go`)

**Files:**
- Modify: `internal/api/server_test.go`

**Interfaces:**
- Consumes: `auth.MeResponse` (Task 1), `server.handleMe` (Task 2)

- [ ] **Step 1: Write the tests**

Add the following test functions at the end of `internal/api/server_test.go` (before the closing of the file):

```go
func TestMeReturnsIdentityWithGlobalCapabilities(t *testing.T) {
	t.Parallel()

	server := NewServer(app.NewService(app.Service{}), configuredTenantID)
	tests := []struct {
		name       string
		roles      []string
		wantAdmin  bool
		wantCreate bool
	}{
		{name: "platform admin", roles: []string{"platform-admin"}, wantAdmin: true, wantCreate: false},
		{name: "stack creator", roles: []string{"stack-creator"}, wantAdmin: false, wantCreate: true},
		{name: "both roles", roles: []string{"platform-admin", "stack-creator"}, wantAdmin: true, wantCreate: true},
		{name: "no roles", roles: nil, wantAdmin: false, wantCreate: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/v1/me", nil)
			ctx := authn.ContextWithPrincipal(request.Context(), authn.Principal{
				Subject:    apiKeycloakSubject,
				Name:       "Test User",
				Email:      "test@example.com",
				RealmRoles: test.roles,
			})
			request = request.WithContext(ctx)

			server.ServeHTTP(response, request)

			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusOK, response.Body.String())
			}

			var body struct {
				Sub         string `json:"sub"`
				DisplayName string `json:"displayName"`
				Email       string `json:"email"`
				GlobalCapabilities struct {
					IsPlatformAdmin bool `json:"isPlatformAdmin"`
					CanCreateStack  bool `json:"canCreateStack"`
				} `json:"globalCapabilities"`
				TenantID string `json:"tenantID"`
			}
			if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
				t.Fatalf("decode response: %v", err)
			}

			if body.Sub != apiKeycloakSubject {
				t.Errorf("sub = %q, want %q", body.Sub, apiKeycloakSubject)
			}
			if body.DisplayName != "Test User" {
				t.Errorf("displayName = %q, want %q", body.DisplayName, "Test User")
			}
			if body.Email != "test@example.com" {
				t.Errorf("email = %q, want %q", body.Email, "test@example.com")
			}
			if body.GlobalCapabilities.IsPlatformAdmin != test.wantAdmin {
				t.Errorf("isPlatformAdmin = %t, want %t", body.GlobalCapabilities.IsPlatformAdmin, test.wantAdmin)
			}
			if body.GlobalCapabilities.CanCreateStack != test.wantCreate {
				t.Errorf("canCreateStack = %t, want %t", body.GlobalCapabilities.CanCreateStack, test.wantCreate)
			}
			if body.TenantID != string(configuredTenantID) {
				t.Errorf("tenantID = %q, want %q", body.TenantID, string(configuredTenantID))
			}
		})
	}
}

func TestMeReturnsUnauthorizedWithoutPrincipal(t *testing.T) {
	t.Parallel()

	server := NewServer(app.NewService(app.Service{}), configuredTenantID)

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v1/me", nil)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusUnauthorized, response.Body.String())
	}
}

func TestAuthenticatedServerProtectsMeRoute(t *testing.T) {
	server := NewAuthenticatedServer(app.NewService(app.Service{}), apiTestVerifier{}, configuredTenantID)

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v1/me", nil)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusUnauthorized, response.Body.String())
	}
}
```

- [ ] **Step 2: Run the tests**

```bash
go test ./internal/api/ -run "TestMe" -v -count=1
```

Expected: 7 subtests PASS (4 role combinations + no-principal + auth server gate)

- [ ] **Step 3: Run all backend tests to verify no regressions**

```bash
go test ./internal/... -count=1
```

- [ ] **Step 4: Commit**

```bash
git add internal/api/server_test.go
git commit -m "test(api): add GET /v1/me integration tests"
```

---

### Task 4: Add tenantID to Me type and query key (`web/src/auth/types.ts`, `web/src/api/queryKeys.ts`)

**Files:**
- Modify: `web/src/auth/types.ts`
- Modify: `web/src/api/queryKeys.ts`

**Interfaces:**
- Produces: `Me.tenantID` field, `queryKeys.me` query key

- [ ] **Step 1: Add tenantID to Me interface**

In `web/src/auth/types.ts`, add `tenantID` field:

```typescript
export interface Me {
  sub: string;
  displayName: string;
  email?: string;
  tenantID?: string;
  globalCapabilities: {
    isPlatformAdmin: boolean;
    canCreateStack: boolean;
  };
}
```

- [ ] **Step 2: Add me query key**

In `web/src/api/queryKeys.ts`, add `me` to the exported object:

```typescript
export const queryKeys = {
  me: ["me"] as const,
  // ... existing keys unchanged ...
```

The full file after edit:

```typescript
export const queryKeys = {
  me: ["me"] as const,
  stacks: (tenantID: string) => ["stacks", tenantID] as const,
  templateRevisions: (tenantID: string) => ["templateRevisions", tenantID] as const,
  stack: (tenantID: string, stackID: string) => ["stack", tenantID, stackID] as const,
  templateRegistration: (tenantID: string, registrationID: string) =>
    ["templateRegistration", tenantID, registrationID] as const,
  templateRevisionVariables: (tenantID: string, templateRevisionID: string) =>
    ["templateRevisionVariables", tenantID, templateRevisionID] as const,
  templateRun: (tenantID: string, runID: string) => ["templateRun", tenantID, runID] as const,
  templateRunLogs: (tenantID: string, runID: string, statusTag: string) =>
    ["templateRunLogs", tenantID, runID, statusTag] as const,
  templateRunLog: (tenantID: string, runID: string, phase: string, statusTag: string) =>
    ["templateRunLog", tenantID, runID, phase, statusTag] as const
};
```

- [ ] **Step 3: Verify TypeScript compilation**

```bash
npx tsc --noEmit
```

- [ ] **Step 4: Commit**

```bash
git add web/src/auth/types.ts web/src/api/queryKeys.ts
git commit -m "feat(web): add tenantID to Me type and me query key"
```

---

### Task 5: Export getMe() from API client (`web/src/api/client.ts`)

**Files:**
- Modify: `web/src/api/client.ts`

**Interfaces:**
- Consumes: `Me` type from `web/src/auth/types.ts` (Task 4)
- Produces: `export function getMe(): Promise<Me>` — calls `requestJSON("/v1/me")`

- [ ] **Step 1: Add import and export function**

In `web/src/api/client.ts`, add the import after line 1:

```typescript
import type { Me } from "../auth/types";
```

Place it right after the existing `getUserManager` import. The import block at the top becomes:

```typescript
import { getUserManager } from "../auth/userManager";
import type { Me } from "../auth/types";
import type {
  ApiErrorBody,
  Operation,
  Stack,
  StackTemplate,
  StackView,
  TemplateRevision,
  TemplateRegistration,
  TemplateRun,
  TemplateRunLog,
  TemplateVariable
} from "./types";
```

Then add the `getMe` function before `registerTemplate` (after the `CancelRunRequest` interface, around line 63):

```typescript
export function getMe(): Promise<Me> {
  return requestJSON("/v1/me");
}
```

- [ ] **Step 2: Verify TypeScript compilation**

```bash
npx tsc --noEmit
```

- [ ] **Step 3: Commit**

```bash
git add web/src/api/client.ts
git commit -m "feat(web): add getMe() API client function"
```

---

### Task 6: Create useMeQuery hook (`web/src/auth/useMeQuery.ts`)

**Files:**
- Create: `web/src/auth/useMeQuery.ts`

**Interfaces:**
- Consumes: `getMe()` from `web/src/api/client.ts` (Task 5), `queryKeys.me` from `web/src/api/queryKeys.ts` (Task 4)
- Produces: `export function useMeQuery(options?)` — TanStack Query hook returning `UseQueryResult<Me>`

- [ ] **Step 1: Write the file**

```typescript
import { useQuery } from "@tanstack/react-query";
import type { UseQueryOptions } from "@tanstack/react-query";
import { getMe } from "../api/client";
import { queryKeys } from "../api/queryKeys";
import type { Me } from "./types";

export function useMeQuery(options?: Partial<UseQueryOptions<Me>>) {
  return useQuery<Me>({
    queryKey: queryKeys.me,
    queryFn: getMe,
    staleTime: Infinity,
    retry: false,
    ...options,
  });
}
```

- [ ] **Step 2: Verify TypeScript compilation**

```bash
npx tsc --noEmit
```

- [ ] **Step 3: Commit**

```bash
git add web/src/auth/useMeQuery.ts
git commit -m "feat(web): add useMeQuery hook for GET /v1/me"
```

---

### Task 7: Write useMeQuery tests (`web/src/auth/useMeQuery.test.ts`)

**Files:**
- Create: `web/src/auth/useMeQuery.test.ts`

**Interfaces:**
- Consumes: `useMeQuery()` (Task 6)

- [ ] **Step 1: Write the test file**

```typescript
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { useMeQuery } from "./useMeQuery";

function wrapper({ children }: { children: React.ReactNode }) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return (
    <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  );
}

describe("useMeQuery", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("returns MeResponse data on success", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(
        JSON.stringify({
          sub: "user_1",
          displayName: "Alice",
          email: "alice@example.com",
          globalCapabilities: { isPlatformAdmin: true, canCreateStack: false },
          tenantID: "tenant_123",
        }),
        { status: 200, headers: { "content-type": "application/json" } }
      )
    );

    const { result } = renderHook(() => useMeQuery(), { wrapper });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));

    expect(result.current.data).toEqual({
      sub: "user_1",
      displayName: "Alice",
      email: "alice@example.com",
      globalCapabilities: { isPlatformAdmin: true, canCreateStack: false },
      tenantID: "tenant_123",
    });
  });

  it("returns error on 401 response", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(
        JSON.stringify({ error: "unauthorized", message: "authentication required" }),
        { status: 401, headers: { "content-type": "application/json" } }
      )
    );

    const { result } = renderHook(() => useMeQuery(), { wrapper });

    await waitFor(() => expect(result.current.isError).toBe(true));
    expect(result.current.error).toBeTruthy();
  });

  it("does not fetch when disabled", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({}), { status: 200, headers: { "content-type": "application/json" } })
    );

    const { result } = renderHook(() => useMeQuery({ enabled: false }), { wrapper });

    expect(result.current.isLoading).toBe(false);
    expect(result.current.isPending).toBe(true);
    expect(fetchMock).not.toHaveBeenCalled();
  });
});
```

- [ ] **Step 2: Run the tests**

```bash
npx vitest run src/auth/useMeQuery.test.ts
```

Expected: 3 tests PASS

- [ ] **Step 3: Commit**

```bash
git add web/src/auth/useMeQuery.test.ts
git commit -m "test(web): add useMeQuery hook tests"
```

---

### Task 8: Update OidcAuthProvider to use useMeQuery (`web/src/auth/OidcAuthProvider.tsx`)

**Files:**
- Modify: `web/src/auth/OidcAuthProvider.tsx`

**Interfaces:**
- Consumes: `useMeQuery()` (Task 6)
- Removes: `convertUserToMe` import and all calls to it
- Produces: Provider that calls `/v1/me` after OIDC auth resolves

- [ ] **Step 1: Rewrite the file**

```typescript
import { useCallback, useEffect, useState } from "react";
import { Outlet, useLocation, useNavigate } from "react-router-dom";
import { AuthContext } from "./AuthContext";
import { getUserManager } from "./userManager";
import { useMeQuery } from "./useMeQuery";

export default function OidcAuthProvider() {
  const [oidcResolved, setOidcResolved] = useState(false);
  const [status, setStatus] = useState<"loading" | "unauthenticated" | "error">("loading");
  const location = useLocation();
  const navigate = useNavigate();

  const { data: me, error: meError, isLoading: meLoading } = useMeQuery({ enabled: oidcResolved });

  useEffect(() => {
    const isCallbackPath = location.pathname === "/auth/callback";

    if (isCallbackPath) {
      const isSignoutCallback = location.search.includes("state=") && !location.search.includes("code=");

      if (isSignoutCallback) {
        getUserManager().signoutRedirectCallback()
          .then(() => {
            setStatus("unauthenticated");
            getUserManager().signinRedirect();
          })
          .catch(() => setStatus("error"));
      } else {
        getUserManager().signinRedirectCallback()
          .then((user) => {
            setOidcResolved(true);
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
          setOidcResolved(true);
        } else if (user?.expired && user.refresh_token) {
          getUserManager().signinSilent()
            .then((refreshedUser) => {
              if (refreshedUser) {
                setOidcResolved(true);
              } else {
                setStatus("unauthenticated");
                getUserManager().signinRedirect();
              }
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
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const login = useCallback(() => {
    getUserManager().signinRedirect();
  }, []);

  const logout = useCallback(() => {
    getUserManager().signoutRedirect();
  }, []);

  if (status === "loading" || meLoading) {
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

  if (meError) {
    login();
    return null;
  }

  if (!me) {
    return null;
  }

  return (
    <AuthContext.Provider value={{ me, status: "authenticated", login, logout }}>
      <Outlet />
    </AuthContext.Provider>
  );
}
```

- [ ] **Step 2: Verify TypeScript compilation**

```bash
npx tsc --noEmit
```

- [ ] **Step 3: Commit**

```bash
git add web/src/auth/OidcAuthProvider.tsx
git commit -m "feat(web): wire OidcAuthProvider to use /v1/me via useMeQuery"
```

---

### Task 9: Update OidcAuthProvider mock (`web/src/auth/__mocks__/OidcAuthProvider.tsx`)

**Files:**
- Modify: `web/src/auth/__mocks__/OidcAuthProvider.tsx`

**Interfaces:**
- Produces: Updated mock `me` object with `email` and `tenantID` fields

- [ ] **Step 1: Update the mock**

```typescript
import { Outlet } from "react-router-dom";
import { AuthContext } from "../AuthContext";

export default function OidcAuthProvider() {
  return (
    <AuthContext.Provider
      value={{
        me: {
          sub: "test",
          displayName: "Test",
          email: "test@example.com",
          tenantID: "tenant_123",
          globalCapabilities: { isPlatformAdmin: false, canCreateStack: true },
        },
        status: "authenticated" as const,
        login: () => {},
        logout: () => {},
      }}
    >
      <Outlet />
    </AuthContext.Provider>
  );
}
```

- [ ] **Step 2: Commit**

```bash
git add web/src/auth/__mocks__/OidcAuthProvider.tsx
git commit -m "fix(web): add email and tenantID to OidcAuthProvider mock"
```

---

### Task 10: Delete convertUser files

**Files:**
- Delete: `web/src/auth/convertUser.ts`
- Delete: `web/src/auth/convertUser.test.ts`

**Interfaces:**
- Removes: `convertUserToMe()` function — no longer imported anywhere after Task 8

- [ ] **Step 1: Verify no other file imports convertUser**

```bash
rg "convertUser" web/src/ --include='*.ts' --include='*.tsx'
```

Expected: Only `convertUser.ts` and `convertUser.test.ts` themselves (no production imports remain).

- [ ] **Step 2: Delete the files**

```bash
rm web/src/auth/convertUser.ts web/src/auth/convertUser.test.ts
```

- [ ] **Step 3: Verify TypeScript compilation still passes**

```bash
npx tsc --noEmit
```

- [ ] **Step 4: Commit**

```bash
git add web/src/auth/convertUser.ts web/src/auth/convertUser.test.ts
git commit -m "refactor(web): remove convertUserToMe, replaced by /v1/me"
```

---

### Task 11: Actor override proof test (`web/src/api/client.test.ts`)

**Files:**
- Modify: `web/src/api/client.test.ts`

**Interfaces:**
- Consumes: All exported client functions from `web/src/api/client.ts`
- Produces: Test that verifies no request body includes actor identity fields

- [ ] **Step 1: Add the test**

Add the following test at the end of the describe block (before the closing `});`):

```typescript
  it("never includes actor identity fields in request bodies", async () => {
    const forbiddenFields = ["actor", "actor_id", "sub", "user_id", "on_behalf_of"];

    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(jsonResponse({ id: "ok" }));

    await registerTemplate("tenant_123", { repo_owner: "a", repo_name: "b", source_ref: "main", root_path: "." });
    await createStack("tenant_123", { name: "s", slug: "", tags: {}, default_credential_ids: [] });
    await addTemplateToStack("tenant_123", "stack_1", { template_revision_id: "rev_1", selected_ref: "main", config: {} });
    await updateStackTemplateConfig("tenant_123", "st_1", { config: {} });
    await upgradeStackTemplate("tenant_123", "st_1", { target_template_revision_id: "rev_2" });
    await startTemplateRun("tenant_123", "st_1", { operation: "plan" });
    await approveRun("tenant_123", "run_1");
    await cancelRun("tenant_123", "run_2", { reason: "stop" });

    for (const call of fetchMock.mock.calls) {
      const init = call[1] as RequestInit | undefined;
      if (init?.body && typeof init.body === "string") {
        const parsed = JSON.parse(init.body);
        for (const field of forbiddenFields) {
          const hasField = Object.prototype.hasOwnProperty.call(parsed, field);
          expect(hasField, `request body contains forbidden field "${field}": ${JSON.stringify(parsed)}`).toBe(false);
        }
      }
    }
  });
```

- [ ] **Step 2: Run the test**

```bash
npx vitest run src/api/client.test.ts --reporter=verbose
```

Expected: All tests PASS including the new "never includes actor identity fields in request bodies"

- [ ] **Step 3: Commit**

```bash
git add web/src/api/client.test.ts
git commit -m "test(web): prove request bodies exclude actor identity fields"
```

---

### Task 12: Run full test suite and verify

**Files:**
- None (verification only)

- [ ] **Step 1: Run backend tests**

```bash
go test ./internal/... -count=1
```

- [ ] **Step 2: Run frontend tests**

```bash
npm test
```

- [ ] **Step 3: Verify TypeScript compilation**

```bash
npx tsc --noEmit
```

- [ ] **Step 4: Confirm no `"user_123"` in production source**

```bash
rg '"user_123"' web/src/ --include='*.ts' --include='*.tsx' | grep -v '\.test\.'
```

Expected: No output (all matches are in test files only).

---

## Task Dependency Order

```
Task 1 (me.go types)
  └─ Task 2 (handleMe handler)
       └─ Task 3 (backend tests)
Task 4 (types.ts + queryKeys.ts) ─┐
Task 5 (getMe() client export)   ├─ Task 6 (useMeQuery hook)
                                  │     └─ Task 7 (useMeQuery tests)
                                  │           └─ Task 8 (OidcAuthProvider)
                                  │                 └─ Task 9 (mock update)
                                  │                       └─ Task 10 (delete convertUser)
Task 11 (actor override test) ────────────────── independent
Task 12 (full verification) ───────────────────── after all
```

Tasks 4, 5, and 11 can run in parallel. Tasks 1-3 (backend) are independent of 4-10 (frontend).
