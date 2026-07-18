# Configured Single-Tenant Boundary Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Enforce the configured tenant before every tenant-scoped API handler and replace the browser-editable tenant with validated deployment configuration.

**Architecture:** The API server receives `cfg.Security.TenantID` and registers all tenant routes through one wrapper that returns a generic `404` before handler execution when the path tenant differs. The React app resolves one validated `VITE_TFLIVE_TENANT_ID`, displays it as read-only runtime context, and passes it to the existing API client functions.

**Tech Stack:** Go 1.24.1, `net/http`, React 18, TypeScript 5.6, Vite 8, Vitest 4.

## Global Constraints

- Authentication remains outside the tenant guard: unauthenticated `/v1` requests return `401` before tenant evaluation.
- Every one of the sixteen existing tenant-scoped routes uses the same exact configured-tenant comparison.
- Authenticated missing, malformed, encoded-separator, and cross-tenant paths cannot reach request decoding, application services, repositories, workflows, logs, artifacts, or future authorization adapters.
- Captured tenant mismatches return `404` with `{"error":"not_found","message":"resource not found"}` and never disclose resource existence.
- `TFLIVE_TENANT_ID` remains the authoritative backend value; `VITE_TFLIVE_TENANT_ID` must match it operationally.
- `tenant_123` is permitted only as the frontend local-development fallback.
- Do not add a new API endpoint, database migration, authorization-model change, or frontend dependency.
- Do not modify actor identity behavior; AUTH-008 owns removal of the editable Actor control and request actor fields.
- Do not log tenant mismatch details, bearer tokens, principals, or configuration secrets.

## File Map

- `internal/api/server.go`: store the configured tenant, register guarded tenant routes, and emit the non-disclosing response.
- `internal/api/server_test.go`: cover all route families, malformed paths, authentication order, and matching-tenant behavior.
- `cmd/api/main.go`: pass validated runtime tenant configuration into the authenticated server.
- `cmd/api/main_test.go`: prove startup wiring installs the configured boundary.
- `web/src/config.ts`: resolve and validate the frontend tenant setting.
- `web/src/config.test.ts`: test development fallback, production requirements, trimming, and syntax.
- `web/src/vite-env.d.ts`: type `VITE_TFLIVE_TENANT_ID` through Vite's environment declarations.
- `web/src/App.tsx`: consume the configured constant and remove editable tenant state.
- `web/src/App.test.tsx`: server-render the shell and prove tenant context is not an input.
- `web/src/styles.css`: style read-only runtime context alongside the remaining Actor control.
- `.env.example`: show matching backend and frontend local tenant values.
- `docs/authentication.md`: document the request boundary and deployment-value relationship.
- `docs/sprint/authn_and_authz/README.md`: mark AUTH-009 done only after verification passes.

---

### Task 1: Enforce Tenant At The API Boundary

**Files:**
- Modify: `internal/api/server.go:14`
- Modify: `internal/api/server_test.go:1`
- Modify: `cmd/api/main.go:177`
- Modify: `cmd/api/main_test.go:1`

**Interfaces:**
- Consumes: `config.APIConfig.Security.TenantID traits.TenantID`, existing `authn.Verifier`, existing `writeError` response helper.
- Produces: `NewServer(service *app.Service, tenantID traits.TenantID) *Server`, `NewAuthenticatedServer(service *app.Service, verifier authn.Verifier, tenantID traits.TenantID) *Server`, and guarded tenant-route registration.

- [ ] **Step 1: Write failing route-boundary tests**

Add the shared test tenant after imports in `internal/api/server_test.go`:

```go
const configuredTenantID = traits.TenantID("tenant_123")
```

Add a complete route matrix. The deliberately malformed body proves mutation
handlers do not decode before tenant rejection; GET handlers would panic on the
nil service if the guard allowed them through.

```go
func TestTenantScopedRoutesRejectOtherTenantBeforeHandler(t *testing.T) {
	t.Parallel()

	server := NewServer(nil, configuredTenantID)
	tests := []struct {
		name   string
		method string
		path   string
	}{
		{name: "register template", method: http.MethodPost, path: "/v1/tenants/tenant_other/template-revisions"},
		{name: "list template revisions", method: http.MethodGet, path: "/v1/tenants/tenant_other/template-revisions"},
		{name: "get template registration", method: http.MethodGet, path: "/v1/tenants/tenant_other/template-registrations/registration_123"},
		{name: "get template variables", method: http.MethodGet, path: "/v1/tenants/tenant_other/template-revisions/revision_123/variables"},
		{name: "create stack", method: http.MethodPost, path: "/v1/tenants/tenant_other/stacks"},
		{name: "list stacks", method: http.MethodGet, path: "/v1/tenants/tenant_other/stacks"},
		{name: "get stack", method: http.MethodGet, path: "/v1/tenants/tenant_other/stacks/stack_123"},
		{name: "install template", method: http.MethodPost, path: "/v1/tenants/tenant_other/stacks/stack_123/templates"},
		{name: "update template config", method: http.MethodPatch, path: "/v1/tenants/tenant_other/stack-templates/stack_template_123/config"},
		{name: "upgrade template", method: http.MethodPost, path: "/v1/tenants/tenant_other/stack-templates/stack_template_123/upgrade"},
		{name: "start run", method: http.MethodPost, path: "/v1/tenants/tenant_other/stack-templates/stack_template_123/runs"},
		{name: "get run", method: http.MethodGet, path: "/v1/tenants/tenant_other/template-runs/run_123"},
		{name: "list run logs", method: http.MethodGet, path: "/v1/tenants/tenant_other/template-runs/run_123/logs"},
		{name: "get run log artifact", method: http.MethodGet, path: "/v1/tenants/tenant_other/template-runs/run_123/logs/plan"},
		{name: "approve run", method: http.MethodPost, path: "/v1/tenants/tenant_other/template-runs/run_123/approval"},
		{name: "cancel run", method: http.MethodPost, path: "/v1/tenants/tenant_other/template-runs/run_123/cancellation"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			response := httptest.NewRecorder()
			request := httptest.NewRequest(test.method, test.path, strings.NewReader("{"))

			server.ServeHTTP(response, request)

			if response.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusNotFound, response.Body.String())
			}
			var body errorResponse
			if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if body.Error != "not_found" || body.Message != "resource not found" {
				t.Fatalf("body = %#v", body)
			}
		})
	}
}

func TestTenantBoundaryRejectsMissingAndMalformedPaths(t *testing.T) {
	t.Parallel()

	server := NewServer(nil, configuredTenantID)
	for _, path := range []string{
		"/v1/tenants/stacks",
		"/v1/tenants/-tenant/stacks",
		"/v1/tenants/tenant%2Fother/stacks",
	} {
		response := httptest.NewRecorder()
		server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusNotFound {
			t.Fatalf("%s status = %d, want %d", path, response.Code, http.StatusNotFound)
		}
	}
}

func TestAuthenticatedServerEvaluatesTenantAfterAuthentication(t *testing.T) {
	t.Parallel()

	server := NewAuthenticatedServer(nil, apiTestVerifier{}, configuredTenantID)
	path := "/v1/tenants/tenant_other/stacks"

	unauthenticated := httptest.NewRecorder()
	server.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodGet, path, nil))
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want %d", unauthenticated.Code, http.StatusUnauthorized)
	}

	authenticatedRequest := httptest.NewRequest(http.MethodGet, path, nil)
	authenticatedRequest.Header.Set("Authorization", "Bearer test-token")
	authenticated := httptest.NewRecorder()
	server.ServeHTTP(authenticated, authenticatedRequest)
	if authenticated.Code != http.StatusNotFound {
		t.Fatalf("authenticated status = %d, want %d", authenticated.Code, http.StatusNotFound)
	}
}
```

Update the existing matching-tenant tests to construct servers with
`configuredTenantID`. Use these exact call shapes throughout the file:

```go
NewServer(app.NewService(app.Service{}), configuredTenantID)
NewServer(deps.service(), configuredTenantID)
NewServer(newAPITestDependencies().service(), configuredTenantID)
NewAuthenticatedServer(app.NewService(app.Service{}), apiTestVerifier{}, configuredTenantID)
```

- [ ] **Step 2: Write a failing startup-wiring test**

Add `"net/http/httptest"` to `cmd/api/main_test.go`, then add:

```go
func TestRunWiresConfiguredTenantBoundary(t *testing.T) {
	t.Parallel()

	values := apiTestValues()
	values["TFLIVE_TENANT_ID"] = "tenant_configured"
	deps := newRecordingAPIDependencies(t)
	if err := runWithDependencies(context.Background(), apiTestGetenv(values), deps.apiDependencies); err != nil {
		t.Fatalf("runWithDependencies returned error: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/v1/tenants/tenant_other/stacks", nil)
	request.Header.Set("Authorization", "Bearer test-token")
	response := httptest.NewRecorder()
	deps.serverHandler.ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusNotFound, response.Body.String())
	}
	var body struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Error != "not_found" || body.Message != "resource not found" {
		t.Fatalf("body = %#v", body)
	}
}
```

- [ ] **Step 3: Run tests to verify the new API does not exist**

Run:

```bash
rtk go test ./internal/api ./cmd/api -run 'TenantBoundary|TenantScopedRoutes|RunWiresConfiguredTenantBoundary' -count=1
```

Expected: FAIL to compile because `NewServer` and `NewAuthenticatedServer`
do not yet accept `traits.TenantID`.

- [ ] **Step 4: Implement the guarded server boundary**

Extend `Server` and both constructors in `internal/api/server.go`:

```go
type Server struct {
	service  *app.Service
	tenantID traits.TenantID
	mux      *http.ServeMux
	handler  http.Handler
}

func NewServer(service *app.Service, tenantID traits.TenantID) *Server {
	server := &Server{
		service:  service,
		tenantID: tenantID,
		mux:      http.NewServeMux(),
	}

	server.mux.HandleFunc("GET /healthz", server.handleHealth)

	server.handleTenantRoute("POST /v1/tenants/{tenant_id}/template-revisions", server.handleRegisterTemplate)
	server.handleTenantRoute("GET /v1/tenants/{tenant_id}/template-revisions", server.handleListTemplateRevisions)
	server.handleTenantRoute("GET /v1/tenants/{tenant_id}/template-registrations/{registration_id}", server.handleGetTemplateRegistration)
	server.handleTenantRoute("GET /v1/tenants/{tenant_id}/template-revisions/{template_revision_id}/variables", server.handleGetTemplateRevisionVariables)
	server.handleTenantRoute("POST /v1/tenants/{tenant_id}/stacks", server.handleCreateStack)
	server.handleTenantRoute("GET /v1/tenants/{tenant_id}/stacks", server.handleListStacks)
	server.handleTenantRoute("GET /v1/tenants/{tenant_id}/stacks/{stack_id}", server.handleGetStack)
	server.handleTenantRoute("POST /v1/tenants/{tenant_id}/stacks/{stack_id}/templates", server.handleAddTemplateToStack)
	server.handleTenantRoute("PATCH /v1/tenants/{tenant_id}/stack-templates/{stack_template_id}/config", server.handleUpdateStackTemplateConfig)
	server.handleTenantRoute("POST /v1/tenants/{tenant_id}/stack-templates/{stack_template_id}/upgrade", server.handleUpgradeStackTemplate)
	server.handleTenantRoute("POST /v1/tenants/{tenant_id}/stack-templates/{stack_template_id}/runs", server.handleStartTemplateRun)
	server.handleTenantRoute("GET /v1/tenants/{tenant_id}/template-runs/{run_id}", server.handleGetTemplateRun)
	server.handleTenantRoute("GET /v1/tenants/{tenant_id}/template-runs/{run_id}/logs", server.handleListTemplateRunLogs)
	server.handleTenantRoute("GET /v1/tenants/{tenant_id}/template-runs/{run_id}/logs/{phase}", server.handleGetTemplateRunLog)
	server.handleTenantRoute("POST /v1/tenants/{tenant_id}/template-runs/{run_id}/approval", server.handleApproveRun)
	server.handleTenantRoute("POST /v1/tenants/{tenant_id}/template-runs/{run_id}/cancellation", server.handleCancelRun)

	server.handler = server.mux
	return server
}

func NewAuthenticatedServer(service *app.Service, verifier authn.Verifier, tenantID traits.TenantID) *Server {
	server := NewServer(service, tenantID)
	server.handler = authn.RequireAuthentication(verifier, "/healthz")(server.mux)
	return server
}

func (server *Server) handleTenantRoute(pattern string, handler http.HandlerFunc) {
	server.mux.Handle(pattern, server.requireConfiguredTenant(handler))
}

func (server *Server) requireConfiguredTenant(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if traits.TenantID(request.PathValue("tenant_id")) != server.tenantID {
			writeError(response, http.StatusNotFound, "not_found", "resource not found")
			return
		}
		next.ServeHTTP(response, request)
	})
}
```

Retain the existing route comments around the corresponding registrations.
Update every `NewServer` and `NewAuthenticatedServer` call in
`internal/api/server_test.go` to one of the four exact call shapes from Step 1.

Pass the validated tenant in `cmd/api/main.go`:

```go
handler := api.NewAuthenticatedServer(service, verifier, cfg.Security.TenantID)
```

- [ ] **Step 5: Run focused and package tests**

Run:

```bash
rtk go test ./internal/api ./cmd/api -run 'Tenant|AuthenticatedServer|RunWiresConfiguredTenantBoundary' -count=1
rtk go test ./internal/api ./cmd/api -count=1
```

Expected: PASS in both packages. Existing matching-tenant tests continue to
prove handlers receive `tenant_123`; the new tests prove every mismatch stops
before handler execution.

- [ ] **Step 6: Commit the backend boundary**

```bash
rtk git add internal/api/server.go internal/api/server_test.go cmd/api/main.go cmd/api/main_test.go
rtk git commit -m "fix: enforce configured tenant boundary"
```

---

### Task 2: Replace Editable Frontend Tenant State

**Files:**
- Create: `web/src/config.ts`
- Create: `web/src/config.test.ts`
- Create: `web/src/vite-env.d.ts`
- Create: `web/src/App.test.tsx`
- Modify: `web/src/App.tsx:1`
- Modify: `web/src/styles.css:65`

**Interfaces:**
- Consumes: `import.meta.env.VITE_TFLIVE_TENANT_ID`, `import.meta.env.DEV`, existing API client tenant arguments.
- Produces: `resolveTenantID(rawTenantID: string | undefined, development: boolean): string` and `tenantID: string`.

- [ ] **Step 1: Write failing frontend configuration tests**

Create `web/src/config.test.ts`:

```ts
import { describe, expect, it } from "vitest";
import { resolveTenantID } from "./config";

describe("tenant configuration", () => {
  it("trims and returns an explicitly configured tenant", () => {
    expect(resolveTenantID("  tenant_prod-1  ", false)).toBe("tenant_prod-1");
  });

  it("uses tenant_123 only for missing local-development configuration", () => {
    expect(resolveTenantID(undefined, true)).toBe("tenant_123");
    expect(resolveTenantID("   ", true)).toBe("tenant_123");
  });

  it("requires an explicit production tenant", () => {
    expect(() => resolveTenantID(undefined, false)).toThrow("VITE_TFLIVE_TENANT_ID is required");
    expect(() => resolveTenantID("   ", false)).toThrow("VITE_TFLIVE_TENANT_ID is required");
  });

  it.each(["-tenant", "tenant/value", "tenant value", "tenant!", "a".repeat(129)])(
    "rejects malformed tenant %s",
    (value) => {
      expect(() => resolveTenantID(value, false)).toThrow("VITE_TFLIVE_TENANT_ID must start");
    }
  );
});
```

- [ ] **Step 2: Write a failing read-only shell test**

Create `web/src/App.test.tsx`:

```tsx
import { renderToStaticMarkup } from "react-dom/server";
import { afterEach, describe, expect, it, vi } from "vitest";

describe("application tenant context", () => {
  afterEach(() => {
    vi.unstubAllEnvs();
    vi.resetModules();
  });

  it("displays the configured tenant without an editable tenant input", async () => {
    vi.stubEnv("VITE_TFLIVE_TENANT_ID", "tenant_123");
    const { default: App } = await import("./App");

    const markup = renderToStaticMarkup(<App />);

    expect(markup).toContain('data-testid="tenant-context"');
    expect(markup).toContain(">tenant_123</span>");
    expect(markup).not.toContain('value="tenant_123"');
  });
});
```

- [ ] **Step 3: Run the focused frontend tests to verify failure**

From `web/`, run:

```bash
rtk npm test -- src/config.test.ts src/App.test.tsx
```

Expected: FAIL because `./config` does not exist and the current application
still renders `tenant_123` in an editable input.

- [ ] **Step 4: Implement typed tenant configuration**

Create `web/src/vite-env.d.ts`:

```ts
/// <reference types="vite/client" />

interface ImportMetaEnv {
  readonly VITE_TFLIVE_TENANT_ID?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}
```

Create `web/src/config.ts`:

```ts
const localTenantID = "tenant_123";
const tenantIDPattern = /^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$/;

export function resolveTenantID(rawTenantID: string | undefined, development: boolean): string {
  const value = rawTenantID?.trim() ?? "";
  if (value === "") {
    if (development) {
      return localTenantID;
    }
    throw new Error("VITE_TFLIVE_TENANT_ID is required");
  }
  if (!tenantIDPattern.test(value)) {
    throw new Error(
      "VITE_TFLIVE_TENANT_ID must start with an ASCII alphanumeric character, contain only ASCII alphanumerics, underscore, or hyphen, and be at most 128 characters"
    );
  }
  return value;
}

export const tenantID = resolveTenantID(
  import.meta.env.VITE_TFLIVE_TENANT_ID,
  import.meta.env.DEV
);
```

- [ ] **Step 5: Remove editable tenant state and render context**

Import the configured value in `web/src/App.tsx`:

```ts
import { tenantID } from "./config";
```

Delete this state declaration:

```ts
const [tenantID, setTenantID] = useState("tenant_123");
```

Replace only the Tenant label/input in the header; leave the Actor control for
AUTH-008:

```tsx
<div className="runtime-field">
  <span>Tenant</span>
  <span className="runtime-value" data-testid="tenant-context">{tenantID}</span>
</div>
<label>
  Actor
  <input value={actor} onChange={(event) => setActor(event.target.value)} />
</label>
```

Add matching styles in `web/src/styles.css` after `.runtime-fields`:

```css
.runtime-field {
  display: grid;
  gap: 6px;
  color: #536064;
  font-size: 0.85rem;
  font-weight: 650;
}

.runtime-value {
  min-height: 38px;
  display: flex;
  align-items: center;
  border: 1px solid #c8cfc4;
  border-radius: 6px;
  padding: 8px 10px;
  color: #202833;
  background: #eef1ea;
}
```

Keep the existing `tenantID` arguments and effect dependencies. They now refer
to a module constant rather than browser state, so no API-client signature or
request-path behavior changes.

- [ ] **Step 6: Run focused and full frontend verification**

From `web/`, run:

```bash
rtk npm test -- src/config.test.ts src/App.test.tsx
rtk npm test
VITE_TFLIVE_TENANT_ID=tenant_123 rtk npm run build
```

Expected: both test commands PASS and the production TypeScript/Vite build
completes successfully without adding dependencies.

- [ ] **Step 7: Commit the frontend tenant context**

```bash
rtk git add web/src/config.ts web/src/config.test.ts web/src/vite-env.d.ts web/src/App.tsx web/src/App.test.tsx web/src/styles.css
rtk git commit -m "fix: lock frontend tenant context"
```

---

### Task 3: Document And Verify AUTH-009

**Files:**
- Modify: `.env.example:55`
- Modify: `docs/authentication.md:126`
- Modify: `docs/sprint/authn_and_authz/README.md:126`

**Interfaces:**
- Consumes: completed backend and frontend behavior from Tasks 1 and 2.
- Produces: operational configuration guidance and completed AUTH-009 backlog status.

- [ ] **Step 1: Document matching deployment values**

Add the frontend value beside the backend tenant in `.env.example`:

```dotenv
TFLIVE_ENVIRONMENT=development
TFLIVE_TENANT_ID=tenant_123
VITE_TFLIVE_TENANT_ID=tenant_123
OIDC_ISSUER_URL=http://localhost:8082/realms/tflive
```

Add this row to the API runtime security table in `docs/authentication.md`:

```markdown
| `VITE_TFLIVE_TENANT_ID` | No | Frontend build-time tenant context; must exactly match `TFLIVE_TENANT_ID`; local development falls back to `tenant_123` |
```

Add this boundary description after the table:

```markdown
`TFLIVE_TENANT_ID` is the authoritative security boundary. Every authenticated
tenant-scoped route compares its `{tenant_id}` path value with that configured
tenant before decoding a body or accessing application services, repositories,
logs, artifacts, or authorization data. Missing, malformed, and mismatched
tenant paths return `404` without disclosing whether a referenced resource
exists.

The React application reads `VITE_TFLIVE_TENANT_ID` as non-editable runtime
context. Deployments must set it to the same value as `TFLIVE_TENANT_ID`; a
mismatch is safe but prevents tenant-scoped requests from succeeding.
```

- [ ] **Step 2: Run complete repository verification**

From the repository root, run:

```bash
rtk go test ./...
rtk git diff --check
```

From `web/`, run:

```bash
rtk npm test
VITE_TFLIVE_TENANT_ID=tenant_123 rtk npm run build
```

Expected: all Go packages PASS, all Vitest tests PASS, the production build
succeeds, and `git diff --check` emits no output.

- [ ] **Step 3: Mark AUTH-009 done after verification**

In `docs/sprint/authn_and_authz/README.md`, change only the AUTH-009 status cell:

```markdown
| AUTH-009 | Backend AuthN | Enforce the configured single-tenant boundary | Prevent callers from selecting an arbitrary tenant through the URL. | Every tenant-scoped route validates `{tenant_id}` against the configured tenant before accessing repositories or authorization data.<br>A mismatched tenant does not disclose whether resources exist.<br>List, detail, mutation, run, log, and artifact routes follow the same rule.<br>The frontend no longer treats tenant ID as an editable user identity choice.<br>Tests cover matching, missing, malformed, and cross-tenant paths. | AUTH-005, AUTH-007 | P0 | Done |
```

- [ ] **Step 4: Confirm completion diff and sensitive-value hygiene**

Run:

```bash
rtk rg -n 'AUTH-009.*Done|VITE_TFLIVE_TENANT_ID|configured tenant' .env.example docs/authentication.md docs/sprint/authn_and_authz/README.md
rtk git diff --check
rtk git status --short
```

Expected: AUTH-009 is `Done`, both tenant variables are documented, the
boundary text is present, no whitespace errors appear, and only intended task
files are modified.

- [ ] **Step 5: Commit documentation and backlog completion**

```bash
rtk git add .env.example docs/authentication.md docs/sprint/authn_and_authz/README.md
rtk git commit -m "docs: complete configured tenant boundary"
```
