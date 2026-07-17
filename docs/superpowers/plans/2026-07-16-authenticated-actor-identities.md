# Authenticated Actor Identities Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the authenticated Keycloak `sub` the only identity used for mutation attribution, while rejecting every browser-supplied actor override.

**Architecture:** Authentication middleware continues to attach `authn.Principal` to the request context. Application services resolve the subject from that context and write it to existing domain records and Temporal signals; actor fields disappear from application commands and HTTP request schemas. Strict JSON decoding rejects removed identity fields, while Postgres and workflow payload shapes remain compatible.

**Tech Stack:** Go 1.24.1, `net/http`, Go `encoding/json`, existing `internal/authn` and `internal/app` packages, Temporal Go SDK, PostgreSQL/pgx, React 18, TypeScript 5.6, Vitest 4, Vite 8.

## Global Constraints

- The immutable Keycloak subject (`sub`) attached to the request context is the only source for created-by, requested-by, trigger, cancellation, and approval identities.
- Requests containing `actor`, `requested_by`, `trigger_actor`, `approved_by`, or other undeclared keys return `400 Bad Request`; they are never silently ignored.
- Existing Postgres actor columns and Temporal payload fields retain their current names and compatible string values; no database migration is added.
- Access tokens, authorization headers, and sensitive claims never enter application commands, persistence records, workflow inputs, signals, errors, or logs.
- Remove the editable Actor control now; `/v1/me`, tenant locking, authenticated identity display, logout, and session UX remain assigned to AUTH-019.
- Follow red-green-refactor for production behavior and prefix every shell command with `rtk`.

---

### Task 1: Enforce Authenticated Subjects Across Backend Mutations

**Files:**
- Modify: `internal/app/service.go:13-244,294-683,754-905`
- Modify: `internal/app/service_test.go:1-1091`
- Modify: `internal/api/server.go:1-455,563-580`
- Modify: `internal/api/server_test.go:1-1087`

**Interfaces:**
- Consumes: `authn.PrincipalFromContext(ctx context.Context) (authn.Principal, bool)` from AUTH-007.
- Produces: `app.ErrUnauthenticated`; actor-free mutation command structs; strict HTTP request decoding; `traits.UserID(principal.Subject)` in all created records and workflow signals.
- Preserves: `traits.TemplateRegistration.RequestedBy`, `traits.Stack.CreatedBy`, `traits.StackTemplate.CreatedBy`, `traits.TemplateRun.TriggerActor`, `traits.TemplateRunApproval.ApprovedBy`, `traits.TemplateRunCancellation.RequestedBy`, `traits.ApprovalSignal.ApprovedBy`, and `traits.CancelSignal.RequestedBy`.

- [ ] **Step 1: Write application-service tests that require a request principal**

Add the `internal/authn` import, one subject constant, and a context helper to `internal/app/service_test.go`:

```go
const keycloakSubject = "6fdb4b4c-2a8f-4cf7-945f-38f67f6a0e91"

func authenticatedContext() context.Context {
	return authn.ContextWithPrincipal(context.Background(), authn.Principal{
		Subject: keycloakSubject,
	})
}
```

For every affected success and domain-error test, pass `authenticatedContext()` instead of `context.Background()` and omit these command fields: `Actor`, `RequestedBy`, `TriggerActor`, and `ApprovedBy`. Update attribution assertions to expect `traits.UserID(keycloakSubject)` in created stacks, template registrations, stack templates, runs, approvals, cancellations, and approval/cancellation signals.

Delete `TestCreateStackRejectsMissingActor` and `TestApproveRunRejectsMissingActor`; the new fail-closed table replaces them with the real invariant, a required authenticated principal.

Cover these existing test families explicitly:

```text
TestCreateStack*
TestAddTemplateToStack*
TestUpdateStackTemplateConfig*
TestUpgradeStackTemplate*
TestStartTemplateRun*
TestRegisterTemplate*
TestApproveRun*
TestCancelRun*
```

Add a fail-closed table test that calls all eight operations without a principal:

```go
func TestActorMutationsRejectMissingPrincipal(t *testing.T) {
	t.Parallel()

	service := NewService(Service{})
	tests := []struct {
		name string
		call func() error
	}{
		{name: "register template", call: func() error {
			_, err := service.RegisterTemplate(context.Background(), RegisterTemplateCommand{})
			return err
		}},
		{name: "create stack", call: func() error {
			_, err := service.CreateStack(context.Background(), CreateStackCommand{})
			return err
		}},
		{name: "add template", call: func() error {
			_, err := service.AddTemplateToStack(context.Background(), AddTemplateToStackCommand{})
			return err
		}},
		{name: "update config", call: func() error {
			_, err := service.UpdateStackTemplateConfig(context.Background(), UpdateStackTemplateConfigCommand{})
			return err
		}},
		{name: "upgrade template", call: func() error {
			_, err := service.UpgradeStackTemplate(context.Background(), UpgradeStackTemplateCommand{})
			return err
		}},
		{name: "start run", call: func() error {
			_, err := service.StartTemplateRun(context.Background(), StartTemplateRunCommand{})
			return err
		}},
		{name: "approve run", call: func() error {
			return service.ApproveRun(context.Background(), ApproveRunCommand{})
		}},
		{name: "cancel run", call: func() error {
			return service.CancelRun(context.Background(), CancelRunCommand{})
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.call(); !errors.Is(err, ErrUnauthenticated) {
				t.Fatalf("error = %v, want ErrUnauthenticated", err)
			}
		})
	}
}
```

Also add a direct helper test for an explicitly stored empty subject so a present-but-invalid principal fails closed:

```go
func TestAuthenticatedActorRejectsEmptySubject(t *testing.T) {
	t.Parallel()

	ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{})
	if _, err := authenticatedActor(ctx); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("error = %v, want ErrUnauthenticated", err)
	}
}
```

- [ ] **Step 2: Run the application tests and verify RED**

Run:

```bash
rtk go test ./internal/app -run 'Test(CreateStack|AddTemplateToStack|UpdateStackTemplateConfig|UpgradeStackTemplate|StartTemplateRun|RegisterTemplate|ApproveRun|CancelRun|ActorMutations)' -count=1
```

Expected: FAIL because `ErrUnauthenticated` and authenticated-subject derivation do not exist, and existing validators still require command actor fields.

- [ ] **Step 3: Derive the actor inside the application service**

Import `internal/authn`, add the error to the existing application error block, and add the helper in `internal/app/service.go`:

```go
ErrUnauthenticated = errors.New("unauthenticated")

func authenticatedActor(ctx context.Context) (traits.UserID, error) {
	principal, ok := authn.PrincipalFromContext(ctx)
	if !ok || principal.Subject == "" {
		return "", ErrUnauthenticated
	}
	return traits.UserID(principal.Subject), nil
}
```

At the beginning of all eight affected service methods, before command validation or dependency calls, resolve the actor. Use the correct return shape for each method:

```go
actor, err := authenticatedActor(ctx)
if err != nil {
	return traits.Stack{}, err
}
```

Use `traits.TemplateRegistration{}` for `RegisterTemplate`, `traits.Stack{}` for `CreateStack`, `traits.StackTemplate{}` for `AddTemplateToStack`, `UpdateStackTemplateConfig`, and `UpgradeStackTemplate`, and `traits.TemplateRun{}` for `StartTemplateRun`. `ApproveRun` and `CancelRun` return `err` directly.

Because configuration updates and upgrades require authentication but do not yet persist a new actor, use the non-binding form in those two methods:

```go
if _, err := authenticatedActor(ctx); err != nil {
	return traits.StackTemplate{}, err
}
```

For `ApproveRun` and `CancelRun`, return `err` directly. Assign `actor` to every domain destination in the mapping below:

```text
RegisterTemplate              registration.RequestedBy
CreateStack                   stack.CreatedBy
AddTemplateToStack            stackTemplate.CreatedBy
UpdateStackTemplateConfig     principal required; no actor column written
UpgradeStackTemplate          principal required; no actor column written
StartTemplateRun              run.TriggerActor
ApproveRun                    approval.ApprovedBy and ApprovalSignal.ApprovedBy
CancelRun                     cancellation.RequestedBy and CancelSignal.RequestedBy
```

Remove the actor-required cases from all eight command validators. Keep the obsolete command identity fields only until Step 7 so `internal/api` remains compilable during the API RED cycle.

- [ ] **Step 4: Run the focused application tests and verify GREEN**

Run:

```bash
rtk go test ./internal/app -count=1
```

Expected: PASS with every application-service attribution assertion using `keycloakSubject` and missing/empty principals rejected before dependencies are called.

- [ ] **Step 5: Write API tests for actor-free schemas and spoof rejection**

Add a request helper to `internal/api/server_test.go`:

```go
const apiKeycloakSubject = "6fdb4b4c-2a8f-4cf7-945f-38f67f6a0e91"

func authenticatedRequest(method, target string, body io.Reader) *http.Request {
	request := httptest.NewRequest(method, target, body)
	ctx := authn.ContextWithPrincipal(request.Context(), authn.Principal{
		Subject: apiKeycloakSubject,
	})
	return request.WithContext(ctx)
}
```

Use this helper for every mutation-handler test. Remove actor properties from successful request bodies and assert application-created records use `traits.UserID(apiKeycloakSubject)`.

Use these canonical bodies:

```json
{"operation":"plan"}
{"repo_owner":"acme","repo_name":"infra-templates","source_ref":"v0.0.1","root_path":"modules/vpc"}
{"name":"Acme Prod","tags":{"env":"prod"},"default_credential_ids":["credential_123"]}
{"template_revision_id":"template_123","selected_ref":"main","config":{"region":"us-east-1"}}
{"config":{"region":"us-west-2"}}
{"target_template_revision_id":"template_rev_2"}
{}
{"reason":"testing"}
```

Add a spoof-rejection table covering every legacy spelling plus an equivalent server-produced identity name:

```go
func TestMutationRequestsRejectIdentityOverrides(t *testing.T) {
	tests := []struct {
		name   string
		path   string
		body   string
	}{
		{name: "actor", path: "/v1/tenants/tenant_123/stacks", body: `{"name":"Acme","actor":"spoofed"}`},
		{name: "requested by", path: "/v1/tenants/tenant_123/template-revisions", body: `{"repo_owner":"acme","repo_name":"infra","source_ref":"main","root_path":".","requested_by":"spoofed"}`},
		{name: "trigger actor", path: "/v1/tenants/tenant_123/stack-templates/stack_template_123/runs", body: `{"operation":"plan","trigger_actor":"spoofed"}`},
		{name: "approved by", path: "/v1/tenants/tenant_123/template-runs/run_123/approval", body: `{"approved_by":"spoofed"}`},
		{name: "created by", path: "/v1/tenants/tenant_123/stacks", body: `{"name":"Acme","created_by":"spoofed"}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := NewServer(newAPITestDependencies().service())
			response := httptest.NewRecorder()
			request := authenticatedRequest(http.MethodPost, test.path, strings.NewReader(test.body))

			server.ServeHTTP(response, request)

			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusBadRequest, response.Body.String())
			}
		})
	}
}
```

Add one direct `NewServer` test with a valid mutation body but no principal and assert `401`, code `unauthorized`, and no mutation.

- [ ] **Step 6: Run the API tests and verify RED**

Run:

```bash
rtk go test ./internal/api -run 'Test(StartTemplateRun|RegisterTemplate|CreateStack|AddTemplateToStack|UpdateStackTemplateConfig|UpgradeStackTemplate|ApproveRun|CancelRun|MutationRequests)' -count=1
```

Expected: FAIL because `json.Decoder` still accepts unknown legacy fields, handlers still model actor properties, and `writeAppError` does not map `ErrUnauthenticated` to `401`.

- [ ] **Step 7: Implement strict actor-free HTTP commands**

Import `io` in `internal/api/server.go` and add one decoder used by all eight mutation handlers:

```go
func decodeRequestBody(response http.ResponseWriter, request *http.Request, destination any) bool {
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_json", "request body must match the expected JSON schema")
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(response, http.StatusBadRequest, "invalid_json", "request body must contain one JSON object")
		return false
	}
	return true
}
```

Replace each direct `json.NewDecoder(...).Decode(&body)` block with:

```go
if !decodeRequestBody(response, request, &body) {
	return
}
```

Remove identity fields from `registerTemplateRequest`, `createStackRequest`, `addTemplateToStackRequest`, `updateStackTemplateConfigRequest`, `upgradeStackTemplateRequest`, `startTemplateRunRequest`, `approveRunRequest`, and `cancelRunRequest`. Keep `approveRunRequest` as an empty struct so `{}` is the canonical approval payload.

Remove all actor assignments from API command construction. Then remove `RequestedBy`, `Actor`, `TriggerActor`, and `ApprovedBy` from the eight mutation command structs in `internal/app/service.go`.

Map application authentication failures in `writeAppError`:

```go
case errors.Is(err, app.ErrUnauthenticated):
	writeError(response, http.StatusUnauthorized, "unauthorized", "authentication required")
```

- [ ] **Step 8: Format and run backend tests to verify GREEN**

Run:

```bash
rtk gofmt -w internal/app/service.go internal/app/service_test.go internal/api/server.go internal/api/server_test.go
rtk go test ./internal/app ./internal/api ./cmd/api -count=1
```

Expected: PASS. API success tests show the context subject reaches application records; spoof requests return `400`; missing principals return `401`; command structs no longer expose actor fields.

- [ ] **Step 9: Commit the backend security boundary**

```bash
rtk git add internal/app/service.go internal/app/service_test.go internal/api/server.go internal/api/server_test.go
rtk git commit -m "feat: derive mutation actors from authenticated subject"
```

---

### Task 2: Remove Browser Actor Inputs and Payload Fields

**Files:**
- Modify: `web/src/api/client.ts:25-160`
- Modify: `web/src/api/client.test.ts:1-211`
- Modify: `web/src/App.tsx:54-575`

**Interfaces:**
- Consumes: actor-free HTTP request schemas from Task 1.
- Produces: actor-free TypeScript request types and serialized bodies; `approveRun(tenantID: string, runID: string): Promise<void>`; cancellation bodies containing only `reason`.
- Preserves: server-produced attribution properties in `web/src/api/types.ts`.

- [ ] **Step 1: Update frontend tests to assert actor-free payloads**

In `web/src/api/client.test.ts`, remove identity properties from all client calls and expected JSON bodies. The combined run-action calls become:

```ts
await startTemplateRun("tenant_123", "stack_template_123", { operation: "apply" });
await approveRun("tenant_123", "run_123");
await cancelRun("tenant_123", "run_456", { reason: "manual stop" });
```

Assert the recorded bodies are exactly:

```ts
expect(JSON.parse(String(fetchMock.mock.calls[0][1]?.body))).toEqual({ operation: "apply" });
expect(JSON.parse(String(fetchMock.mock.calls[1][1]?.body))).toEqual({});
expect(JSON.parse(String(fetchMock.mock.calls[2][1]?.body))).toEqual({ reason: "manual stop" });
```

Update registration, stack creation, template installation, config update, and upgrade expectations so none contains `actor` or `requested_by`.

- [ ] **Step 2: Run frontend tests and build to verify RED**

Run from `web/`:

```bash
rtk npm test
rtk npm run build
```

Expected: tests fail on the approval body and the TypeScript build fails because the existing request interfaces still require legacy identity properties and `approveRun` still requires a body argument.

- [ ] **Step 3: Remove actor properties from frontend request interfaces and call sites**

In `web/src/api/client.ts`:

- delete `requested_by` from `RegisterTemplateRequest`;
- delete `actor` from `CreateStackRequest`, `AddTemplateToStackRequest`, `UpdateStackTemplateConfigRequest`, and `UpgradeStackTemplateRequest`;
- delete `trigger_actor` from `StartRunRequest`;
- delete `ApproveRunRequest` entirely;
- delete `requested_by` from `CancelRunRequest`; and
- change approval to own its empty body:

```ts
export function approveRun(tenantID: string, runID: string): Promise<void> {
  return requestNoContent(`/v1/tenants/${encodeURIComponent(tenantID)}/template-runs/${encodeURIComponent(runID)}/approval`, {
    method: "POST",
    body: JSON.stringify({})
  });
}
```

In `web/src/App.tsx`, delete:

```ts
const [actor, setActor] = useState("user_123");
```

Remove the Actor label/input from `runtime-fields`. Remove every identity property from the calls to `registerTemplate`, `createStack`, `addTemplateToStack`, `updateStackTemplateConfig`, `upgradeStackTemplate`, `startTemplateRun`, `approveRun`, and `cancelRun`. Keep the cancellation reason.

- [ ] **Step 4: Run frontend tests and build to verify GREEN**

Run from `web/`:

```bash
rtk npm test
rtk npm run build
```

Expected: all Vitest tests pass and the production TypeScript/Vite build exits 0.

- [ ] **Step 5: Commit frontend request hardening**

```bash
rtk git add web/src/api/client.ts web/src/api/client.test.ts web/src/App.tsx
rtk git commit -m "fix: remove browser actor overrides"
```

---

### Task 3: Prove Workflow and Persistence Compatibility, Then Close AUTH-008

**Files:**
- Modify: `internal/temporal/dispatcher_test.go:94-157,195-233`
- Modify: `internal/workflows/template_run_workflow_test.go:109-248`
- Modify: `internal/postgres/store_test.go:1153-1314,1543-1709`
- Modify: `docs/sprint/authn_and_authz/README.md:71`

**Interfaces:**
- Consumes: authenticated subject records and signals produced by Task 1.
- Produces: compatibility evidence that opaque Keycloak subjects survive Temporal and Postgres unchanged; sprint backlog status `Done`.
- Preserves: all existing database columns, migrations, Temporal signal names, and signal field names.

- [ ] **Step 1: Update compatibility fixtures to realistic Keycloak subjects**

Use these opaque values rather than browser-placeholder user IDs in the focused tests:

```go
const requesterSubject = traits.UserID("6fdb4b4c-2a8f-4cf7-945f-38f67f6a0e91")
const approverSubject = traits.UserID("cb4afba6-d18d-496f-80ce-8a50b94f09be")
```

Update Temporal dispatcher tests to assert `ApprovalSignal.ApprovedBy == approverSubject` and `CancelSignal.RequestedBy == requesterSubject`. Update workflow signal fixtures to use the same values.

Update focused Postgres tests so:

```text
TestCreateTemplateRunPersistsRunFields             TriggerActor = requesterSubject
TestApproveTemplateRunApprovesWaitingRun           ApprovedBy = approverSubject
TestRequestTemplateRunCancellationMarksCancelableRun RequestedBy = requesterSubject
```

Keep the existing direct SQL reads and equality assertions. This is a compatibility/characterization update: no production persistence or workflow code changes are expected.

- [ ] **Step 2: Run workflow and persistence compatibility tests**

Run:

```bash
rtk go test ./internal/temporal ./internal/workflows -count=1
rtk go test ./internal/postgres -run 'TestCreateTemplateRunPersistsRunFields|TestApproveTemplateRunApprovesWaitingRun|TestRequestTemplateRunCancellationMarksCancelableRun' -count=1
```

Expected: PASS. Without `tflive_POSTGRES_TEST_DSN`, Postgres integration tests may report SKIP; with the repository's test DSN they must PASS and round-trip the exact subject strings.

- [ ] **Step 3: Run the complete verification suite before changing backlog status**

Run from the repository root:

```bash
rtk go test ./... -count=1
```

Run from `web/`:

```bash
rtk npm test
rtk npm run build
```

Expected: all Go packages pass, all Vitest tests pass, and the frontend production build exits 0. DSN-gated integration tests may skip only when their documented environment variable is absent.

- [ ] **Step 4: Verify legacy identity fields are absent from browser/API input schemas**

Run:

```bash
rtk rg -n 'json:"(actor|requested_by|trigger_actor|approved_by)"' internal/api/server.go
rtk rg -n 'actor|requested_by|trigger_actor|approved_by' web/src/api/client.ts web/src/App.tsx
```

Expected: both searches return no matches. Server-produced response/domain models and database compatibility fields are intentionally outside these input-schema searches.

- [ ] **Step 5: Mark AUTH-008 Done**

In `docs/sprint/authn_and_authz/README.md`, change only the AUTH-008 status cell:

```text
| AUTH-008 | ... | P0 | Done |
```

Leave dependencies and later ticket statuses unchanged.

- [ ] **Step 6: Re-run final verification after the documentation change**

Run:

```bash
rtk git diff --check
rtk go test ./... -count=1
```

Run from `web/`:

```bash
rtk npm test
rtk npm run build
```

Expected: clean diff check, all backend and frontend tests pass, and the build exits 0.

- [ ] **Step 7: Commit compatibility evidence and backlog completion**

```bash
rtk git add internal/temporal/dispatcher_test.go internal/workflows/template_run_workflow_test.go internal/postgres/store_test.go docs/sprint/authn_and_authz/README.md
rtk git commit -m "test: verify authenticated actor propagation"
```

---

## Final Requirement Checklist

- [ ] Browser request schemas contain no caller-controlled actor identity properties.
- [ ] Strict decoding rejects every legacy actor override before application code runs.
- [ ] All affected application services require a principal and use its immutable subject.
- [ ] Created-by, requested-by, trigger, cancellation, approval, and workflow signal identities equal the authenticated subject.
- [ ] Existing Postgres columns and Temporal schemas remain unchanged and accept opaque subject strings.
- [ ] No access token, authorization header, password, secret, or usable credential is stored or logged.
- [ ] Application, API, workflow, persistence, frontend, full Go, and production frontend build checks pass.
- [ ] AUTH-008 is `Done`; dependencies and later backlog tickets remain unchanged.
