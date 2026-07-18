# OpenFGA Authorization Port Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement a provider-neutral authorization port and a fail-closed OpenFGA HTTP adapter for AUTH-010.

**Architecture:** `internal/authz` defines validated canonical identifiers, role and permission values, port requests/results, and typed error classification. `internal/openfga` retains its dependency-free HTTP client and adds an adapter that serializes OpenFGA request shapes with the configured store/model IDs, validates responses, and maps every transport/protocol failure into an `authz` failure. No route authorization or service wiring is introduced.

**Tech Stack:** Go 1.24.1 standard library (`context`, `errors`, `net/http`, `net/http/httptest`, `encoding/json`); the existing dependency-free OpenFGA REST client; OpenFGA HTTP APIs.

## Global Constraints

- Do not add an OpenFGA SDK dependency; `internal/authz` must not import `internal/openfga` or provider types.
- Construct authorization identifiers only through `authz.SubjectFromKeycloakSub` and `authz.StackFromID`; they return exactly `user:<sub>` and `stack:<id>`.
- Accept only the four writable direct roles: `owner`, `operator`, `approver`, `viewer`; derived permissions are check/list inputs and never mutation relations.
- Pass `OPENFGA_STORE_ID` and `OPENFGA_MODEL_ID` explicitly on every OpenFGA relationship query/command that supports the model ID.
- Preserve the existing bounded response body, secret-redaction, URL escaping, and caller-deadline behavior in `internal/openfga.Client`.
- A denial is a normal decision. Timeout, cancellation, upstream `429`/`5xx`, transport errors, invalid media types, malformed JSON, incomplete valid JSON, and invalid returned identifiers are dependency/protocol failures and must never allow access.
- Do not cache positive authorization decisions.
- Keep this ticket to the port/adapter/error contract, adapter tests, documentation, and backlog status. Do not wire API handlers, service use cases, global-role bypasses, initial owner writes, durable reconciliation, audit records, or access-management endpoints.
- Run every shell command through `rtk`.

---

## File Structure

- Create: `internal/authz/authorization.go` — canonical IDs, role/permission constants, port types, port interface, and typed error categories.
- Create: `internal/authz/authorization_test.go` — pure application contract and error-classification tests.
- Create: `internal/openfga/authorization_adapter.go` — OpenFGA-specific implementation of `authz.Authorizer` over the existing client.
- Create: `internal/openfga/authorization_adapter_test.go` — HTTP contract and failure-mode tests for the adapter.
- Modify: `internal/openfga/client.go` — expose a status-bearing, redacted error from `doJSON` so the adapter can classify unavailable responses without matching strings.
- Modify: `internal/openfga/client_test.go` — preserve existing redaction behavior and assert the status-bearing error is inspectable with `errors.As`.
- Modify: `docs/authentication.md` — document pinned model IDs, confirmation consistency, and fail-closed adapter behavior.
- Modify: `docs/sprint/authn_and_authz/README.md` — mark AUTH-010 `Done` only after the suite is green.

## Port Contract

```go
package authz

type Subject string
type Stack string
type Role string
type Permission string

const (
	RoleOwner    Role = "owner"
	RoleOperator Role = "operator"
	RoleApprover Role = "approver"
	RoleViewer   Role = "viewer"

	PermissionView         Permission = "can_view"
	PermissionOperate      Permission = "can_operate"
	PermissionApprove      Permission = "can_approve"
	PermissionManageAccess Permission = "can_manage_access"
)

type Check struct { Subject Subject; Stack Stack; Permission Permission; HigherConsistency bool }
type Decision struct { Check Check; Allowed bool }
type Grant struct { Subject Subject; Stack Stack; Role Role }
type Mutation struct { Grants []Grant; Confirm bool }

type Authorizer interface {
	Check(context.Context, Check) (Decision, error)
	BatchCheck(context.Context, []Check) ([]Decision, error)
	ListAccessibleStacks(context.Context, Subject, Permission) ([]Stack, error)
	ListGrants(context.Context, Stack) ([]Grant, error)
	WriteRelationships(context.Context, Mutation) error
	DeleteRelationships(context.Context, Mutation) error
}
```

`SubjectFromKeycloakSub`, `StackFromID`, `Role.Valid`, and `Permission.Valid` are the only validation entry points. Export sentinel errors `ErrInvalidInput`, `ErrTimeout`, `ErrUnavailable`, `ErrMalformedResponse`, and `ErrWriteUnconfirmed`; adapter errors wrap exactly one applicable sentinel so `errors.Is` is stable. `HTTPStatus(err)` returns `(503, "authorization_unavailable", true)` for timeout, unavailable, and malformed-response errors, and `(503, "authorization_write_unconfirmed", true)` for unconfirmed writes; it returns `(_, _, false)` for denial and invalid input. Future handlers use this mapping rather than embedding provider behavior.

### Task 1: Establish the provider-neutral authorization contract

**Files:**
- Create: `internal/authz/authorization.go`
- Create: `internal/authz/authorization_test.go`

**Interfaces:**
- Produces: every type and method in the Port Contract section.
- Consumed by: `internal/openfga/authorization_adapter.go` only; no existing application service changes.

- [ ] **Step 1: Write the failing contract tests**

```go
func TestCanonicalIdentifiers(t *testing.T) {
	subject, err := SubjectFromKeycloakSub("kc-sub-123")
	if err != nil || subject != Subject("user:kc-sub-123") { t.Fatalf("SubjectFromKeycloakSub() = %q, %v", subject, err) }
	stack, err := StackFromID("stack-123")
	if err != nil || stack != Stack("stack:stack-123") { t.Fatalf("StackFromID() = %q, %v", stack, err) }
}

func TestCanonicalIdentifiersRejectUnsafeAndPrefixedValues(t *testing.T) {
	for _, input := range []string{"", " ", "user:already", "stack:already", "bad\nsubject"} {
		if _, err := SubjectFromKeycloakSub(input); !errors.Is(err, ErrInvalidInput) { t.Fatalf("subject %q error = %v", input, err) }
		if _, err := StackFromID(input); !errors.Is(err, ErrInvalidInput) { t.Fatalf("stack %q error = %v", input, err) }
	}
}

func TestOnlyDirectRolesAndDerivedPermissionsAreValid(t *testing.T) {
	if !RoleOwner.Valid() || !PermissionView.Valid() { t.Fatal("known values must validate") }
	if Role("can_view").Valid() || Permission("owner").Valid() { t.Fatal("roles and permissions must not overlap") }
}

func TestHTTPStatusMapsOnlyAuthorizationDependencyFailures(t *testing.T) {
	status, code, ok := HTTPStatus(fmt.Errorf("check: %w", ErrUnavailable))
	if !ok || status != http.StatusServiceUnavailable || code != "authorization_unavailable" { t.Fatalf("HTTPStatus() = %d, %q, %t", status, code, ok) }
	if _, _, ok := HTTPStatus(ErrInvalidInput); ok { t.Fatal("invalid input must not map to an availability response") }
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `rtk go test ./internal/authz -run 'TestCanonical|TestOnly' -count=1`

Expected: FAIL because package `internal/authz` and its constructors do not exist.

- [ ] **Step 3: Implement the complete contract**

```go
var (
	ErrInvalidInput       = errors.New("invalid authorization input")
	ErrTimeout            = errors.New("authorization timeout")
	ErrUnavailable        = errors.New("authorization unavailable")
	ErrMalformedResponse  = errors.New("malformed authorization response")
	ErrWriteUnconfirmed   = errors.New("authorization write unconfirmed")
)

func SubjectFromKeycloakSub(sub string) (Subject, error) {
	value, err := canonicalIdentifier("user", sub)
	return Subject(value), err
}
func StackFromID(id string) (Stack, error) {
	value, err := canonicalIdentifier("stack", id)
	return Stack(value), err
}

func canonicalIdentifier(kind, value string) (string, error) {
	if value == "" || strings.Contains(value, ":") || strings.IndexFunc(value, unicode.IsSpace) >= 0 || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return "", fmt.Errorf("%w: invalid %s identifier", ErrInvalidInput, kind)
	}
	return kind + ":" + value, nil
}
```

Define the role/permission constants, `Valid` methods, request/result structs, `Authorizer`, `Mutation`, and `HTTPStatus` exactly as listed in the Port Contract.

- [ ] **Step 4: Run the package tests**

Run: `rtk go test ./internal/authz -count=1`

Expected: PASS.

- [ ] **Step 5: Commit the independently testable contract**

```bash
rtk git add internal/authz/authorization.go internal/authz/authorization_test.go
rtk git commit -m "feat(authz): add authorization port contract"
```

### Task 2: Add classified HTTP failures and single/batch checks

**Files:**
- Modify: `internal/openfga/client.go`
- Modify: `internal/openfga/client_test.go`
- Create: `internal/openfga/authorization_adapter.go`
- Create: `internal/openfga/authorization_adapter_test.go`

**Interfaces:**
- Consumes: `authz.Authorizer`, `authz.Check`, `authz.Decision`, error sentinels, and `Config.ValidateVerify` from Task 1.
- Produces: `func NewAuthorizationAdapter(Config) (*AuthorizationAdapter, error)` and `Check`/`BatchCheck` implementations.

- [ ] **Step 1: Write failing HTTP contract tests**

```go
func TestAuthorizationAdapterCheckUsesConfiguredModelAndReturnsDecision(t *testing.T) {
	adapter, requests := testAuthorizationAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/stores/store-id/check" { t.Fatalf("request = %s %s", r.Method, r.URL.Path) }
		var body map[string]any; json.NewDecoder(r.Body).Decode(&body)
		if body["authorization_model_id"] != "model-id" { t.Fatalf("body = %#v", body) }
		w.Header().Set("Content-Type", "application/json"); io.WriteString(w, `{"allowed":true}`)
	})
	decision, err := adapter.Check(context.Background(), authz.Check{Subject: mustSubject(t, "alice"), Stack: mustStack(t, "one"), Permission: authz.PermissionView})
	if err != nil || !decision.Allowed || *requests != 1 { t.Fatalf("Check() = %#v, %v", decision, err) }
}

func TestAuthorizationAdapterCheckDistinguishesDenialAndFailures(t *testing.T) {
	tests := []struct{ name, contentType, body string; status int; want error; allowed bool }{
		{"denied", "application/json", `{"allowed":false}`, http.StatusOK, nil, false},
		{"unavailable", "application/json", `{}`, http.StatusServiceUnavailable, authz.ErrUnavailable, false},
		{"rate limited", "application/json", `{}`, http.StatusTooManyRequests, authz.ErrUnavailable, false},
		{"wrong media type", "text/plain", `{"allowed":true}`, http.StatusOK, authz.ErrMalformedResponse, false},
		{"invalid JSON", "application/json", `{`, http.StatusOK, authz.ErrMalformedResponse, false},
		{"missing allowed", "application/json", `{}`, http.StatusOK, authz.ErrMalformedResponse, false},
	}
	for _, test := range tests { t.Run(test.name, func(t *testing.T) {
		adapter := adapterForResponse(t, test.status, test.contentType, test.body)
		decision, err := adapter.Check(context.Background(), viewCheck(t))
		if test.want != nil { if !errors.Is(err, test.want) { t.Fatalf("Check() error = %v, want %v", err, test.want) }; return }
		if err != nil || decision.Allowed != test.allowed { t.Fatalf("Check() = %#v, %v", decision, err) }
	}) }
}

func TestAuthorizationAdapterBatchCheckRejectsMissingOrUnknownCorrelationResults(t *testing.T) {
	for _, body := range []string{`{"result":{"0":{"allowed":true}}}`, `{"result":{"0":{"allowed":true},"1":{"allowed":false},"extra":{"allowed":true}}}`} {
		adapter := adapterForResponse(t, http.StatusOK, "application/json", body)
		_, err := adapter.BatchCheck(context.Background(), []authz.Check{viewCheck(t), operateCheck(t)})
		if !errors.Is(err, authz.ErrMalformedResponse) { t.Fatalf("BatchCheck() error = %v", err) }
	}
}
```

- [ ] **Step 2: Run the focused tests to verify they fail**

Run: `rtk go test ./internal/openfga -run 'TestAuthorizationAdapter(Check|BatchCheck)' -count=1`

Expected: FAIL because `NewAuthorizationAdapter` does not exist.

- [ ] **Step 3: Implement classified client errors and check operations**

```go
type HTTPStatusError struct { StatusCode int; Status string; Body string }
func (e *HTTPStatusError) Error() string { return fmt.Sprintf("unexpected HTTP status %s: %s", e.Status, e.Body) }

func NewAuthorizationAdapter(cfg Config) (*AuthorizationAdapter, error) {
	if err := cfg.ValidateVerify(); err != nil { return nil, fmt.Errorf("validate OpenFGA authorization config: %w", err) }
	return &AuthorizationAdapter{client: NewClient(cfg), storeID: cfg.StoreID, modelID: cfg.ModelID}, nil
}

func (adapter *AuthorizationAdapter) Check(ctx context.Context, check authz.Check) (authz.Decision, error) {
	if err := validCheck(check); err != nil { return authz.Decision{}, err }
	var response struct { Allowed *bool `json:"allowed"` }
	err := adapter.client.doJSON(ctx, http.MethodPost, adapter.client.endpoint("stores", adapter.storeID, "check"), nil,
		map[string]any{"authorization_model_id": adapter.modelID, "tuple_key": tuple(check.Subject, string(check.Permission), check.Stack), "consistency": consistency(check.HigherConsistency)}, &response, http.StatusOK)
	if err != nil { return authz.Decision{}, adapter.classify(err) }
	if response.Allowed == nil { return authz.Decision{}, fmt.Errorf("%w: check allowed is missing", authz.ErrMalformedResponse) }
	return authz.Decision{Check: check, Allowed: *response.Allowed}, nil
}
```

Change `Client.doJSON` to return `*HTTPStatusError` for non-accepted responses while retaining the currently redacted and bounded body. `classify` must use `errors.Is` for `context.DeadlineExceeded` and `context.Canceled`, `errors.As` for `*HTTPStatusError`, classify only `429` and `5xx` as `ErrUnavailable`, and classify all response decoding/shape errors as `ErrMalformedResponse`. Batch requests use `/stores/{store}/batch-check`, stable correlation IDs `"0"`, `"1"`, and a response map keyed by those IDs; return decisions in input order only after every result validates.

- [ ] **Step 4: Run focused and regression tests**

Run: `rtk go test ./internal/openfga -run 'Test(Client|AuthorizationAdapter(Check|BatchCheck))' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit checks and failure classification**

```bash
rtk git add internal/openfga/client.go internal/openfga/client_test.go internal/openfga/authorization_adapter.go internal/openfga/authorization_adapter_test.go
rtk git commit -m "feat(authz): add OpenFGA checks adapter"
```

### Task 3: Implement accessible-stack and grant reads

**Files:**
- Modify: `internal/openfga/authorization_adapter.go`
- Modify: `internal/openfga/authorization_adapter_test.go`

**Interfaces:**
- Consumes: validated `authz.Subject`, `authz.Stack`, `authz.Permission`, and direct `authz.Role` values from Task 1.
- Produces: `ListAccessibleStacks(ctx, subject, permission)` and `ListGrants(ctx, stack)`.

- [ ] **Step 1: Write failing list-operation tests**

```go
func TestAuthorizationAdapterListsAccessibleStacksWithConfiguredModel(t *testing.T) {
	adapter := adapterForHandler(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/stores/store-id/list-objects" { t.Fatalf("path = %q", r.URL.Path) }
		var body map[string]any; json.NewDecoder(r.Body).Decode(&body)
		if !reflect.DeepEqual(body, map[string]any{"authorization_model_id":"model-id", "type":"stack", "relation":"can_view", "user":"user:alice"}) { t.Fatalf("body = %#v", body) }
		w.Header().Set("Content-Type", "application/json"); io.WriteString(w, `{"objects":["stack:one","stack:two"]}`)
	})
	stacks, err := adapter.ListAccessibleStacks(context.Background(), mustSubject(t, "alice"), authz.PermissionView)
	if err != nil || !reflect.DeepEqual(stacks, []authz.Stack{"stack:one", "stack:two"}) { t.Fatalf("ListAccessibleStacks() = %#v, %v", stacks, err) }
}

func TestAuthorizationAdapterRejectsInvalidListObjects(t *testing.T) {
	for _, body := range []string{`{"objects":["stack:"]}`, `{"objects":["user:alice"]}`, `{"objects":["stack:one","stack:one"]}`, `{`, `{}`} {
		adapter := adapterForResponse(t, http.StatusOK, "application/json", body)
		_, err := adapter.ListAccessibleStacks(context.Background(), mustSubject(t, "alice"), authz.PermissionView)
		if !errors.Is(err, authz.ErrMalformedResponse) { t.Fatalf("body %q error = %v", body, err) }
	}
}

func TestAuthorizationAdapterListsOnlyDirectRoleGrantsAcrossPages(t *testing.T) {
	adapter := adapterForHandler(t, pagedGrantHandler(t))
	grants, err := adapter.ListGrants(context.Background(), mustStack(t, "one"))
	want := []authz.Grant{{Subject:"user:alice", Stack:"stack:one", Role:authz.RoleOwner}, {Subject:"user:bob", Stack:"stack:one", Role:authz.RoleViewer}}
	if err != nil || !reflect.DeepEqual(grants, want) { t.Fatalf("ListGrants() = %#v, %v", grants, err) }
}
```

- [ ] **Step 2: Run the focused tests to verify they fail**

Run: `rtk go test ./internal/openfga -run 'TestAuthorizationAdapter(ListAccessibleStacks|ListsOnlyDirectRoleGrantsAcrossPages|RejectsInvalidListObjects)' -count=1`

Expected: FAIL because the list methods do not exist.

- [ ] **Step 3: Implement the list operations**

```go
func (adapter *AuthorizationAdapter) ListAccessibleStacks(ctx context.Context, subject authz.Subject, permission authz.Permission) ([]authz.Stack, error) {
	if !validSubject(subject) || !permission.Valid() { return nil, fmt.Errorf("%w: list stacks", authz.ErrInvalidInput) }
	var response struct { Objects *[]string `json:"objects"` }
	err := adapter.post(ctx, "list-objects", map[string]any{"authorization_model_id": adapter.modelID, "type": "stack", "relation": permission, "user": subject}, &response)
	if err != nil { return nil, err }
	return parseUniqueStacks(*response.Objects)
}
```

Implement `ListGrants` with paginated `POST /stores/{store}/read` requests containing `{tuple_key:{object:<canonical stack>},page_size:100}` and, after the first page, `continuation_token`. `Read` returns direct stored tuples rather than inferred permissions, so it does not require a model ID. Parse only `{tuples:[{key:{user,relation,object}}],continuation_token}` values: rebuild subjects through `SubjectFromKeycloakSub`, require the exact requested stack, accept only direct roles, reject duplicates and repeated tokens, sort by subject then role, and return `ErrMalformedResponse` for any incomplete or noncanonical tuple. Do not query derived permissions.

- [ ] **Step 4: Run the list-operation tests**

Run: `rtk go test ./internal/openfga -run 'TestAuthorizationAdapter(ListAccessibleStacks|ListsOnlyDirectRoleGrantsAcrossPages|RejectsInvalidListObjects)' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit reads**

```bash
rtk git add internal/openfga/authorization_adapter.go internal/openfga/authorization_adapter_test.go
rtk git commit -m "feat(authz): add OpenFGA authorization reads"
```

### Task 4: Implement idempotent relationship mutations and confirmation

**Files:**
- Modify: `internal/openfga/authorization_adapter.go`
- Modify: `internal/openfga/authorization_adapter_test.go`

**Interfaces:**
- Consumes: `authz.Mutation{Grants, Confirm}` and direct grant rules from Task 1.
- Produces: `WriteRelationships` and `DeleteRelationships`, including `ErrWriteUnconfirmed` behavior.

- [ ] **Step 1: Write failing mutation tests**

```go
func TestAuthorizationAdapterRelationshipWritesAreIdempotent(t *testing.T) {
	adapter := adapterForHandler(t, duplicateWriteThenConfirmedHandler(t, true))
	if err := adapter.WriteRelationships(context.Background(), authz.Mutation{Grants: []authz.Grant{ownerGrant(t)}}); err != nil { t.Fatalf("WriteRelationships() error = %v", err) }
}

func TestAuthorizationAdapterRelationshipDeletesAreIdempotent(t *testing.T) {
	adapter := adapterForHandler(t, duplicateWriteThenConfirmedHandler(t, false))
	if err := adapter.DeleteRelationships(context.Background(), authz.Mutation{Grants: []authz.Grant{ownerGrant(t)}}); err != nil { t.Fatalf("DeleteRelationships() error = %v", err) }
}

func TestAuthorizationAdapterConfirmationUsesHigherConsistency(t *testing.T) {
	adapter := adapterForHandler(t, confirmedWriteHandler(t, true))
	if err := adapter.WriteRelationships(context.Background(), authz.Mutation{Grants: []authz.Grant{ownerGrant(t)}, Confirm: true}); err != nil { t.Fatalf("WriteRelationships() error = %v", err) }
}

func TestAuthorizationAdapterUnconfirmedAndInvalidMutationsFailClosed(t *testing.T) {
	adapter := adapterForHandler(t, confirmedWriteHandler(t, false))
	err := adapter.WriteRelationships(context.Background(), authz.Mutation{Grants: []authz.Grant{ownerGrant(t)}, Confirm: true})
	if !errors.Is(err, authz.ErrWriteUnconfirmed) { t.Fatalf("unconfirmed write error = %v", err) }
	_, err = adapter.Check(context.Background(), authz.Check{Subject: mustSubject(t, "alice"), Stack: mustStack(t, "one"), Permission: authz.Permission("owner")})
	if !errors.Is(err, authz.ErrInvalidInput) { t.Fatalf("derived-role check error = %v", err) }
}
```

- [ ] **Step 2: Run the focused tests to verify they fail**

Run: `rtk go test ./internal/openfga -run 'TestAuthorizationAdapter(Relationship|Confirmation|Unconfirmed)' -count=1`

Expected: FAIL because mutation methods do not exist.

- [ ] **Step 3: Implement safe mutation delivery**

```go
func (adapter *AuthorizationAdapter) WriteRelationships(ctx context.Context, mutation authz.Mutation) error {
	if err := validMutation(mutation); err != nil { return err }
	if err := adapter.write(ctx, mutation.Grants, nil); err != nil {
		matches, confirmErr := adapter.confirm(ctx, mutation.Grants, true)
		if confirmErr != nil { return confirmErr }
		if !matches { return err }
	}
	if mutation.Confirm {
		matches, err := adapter.confirm(ctx, mutation.Grants, true)
		if err != nil { return err }
		if !matches { return fmt.Errorf("%w: grants not visible", authz.ErrWriteUnconfirmed) }
	}
	return nil
}

func (adapter *AuthorizationAdapter) DeleteRelationships(ctx context.Context, mutation authz.Mutation) error {
	if err := validMutation(mutation); err != nil { return err }
	if err := adapter.write(ctx, nil, mutation.Grants); err != nil {
		matches, confirmErr := adapter.confirm(ctx, mutation.Grants, false)
		if confirmErr != nil { return confirmErr }
		if !matches { return err }
	}
	if mutation.Confirm {
		matches, err := adapter.confirm(ctx, mutation.Grants, false)
		if err != nil { return err }
		if !matches { return fmt.Errorf("%w: grants still visible", authz.ErrWriteUnconfirmed) }
	}
	return nil
}
```

`write` posts to `/stores/{store}/write` with `authorization_model_id` and only one of `writes` or `deletes`. Before posting, de-duplicate exact grants and reject an empty mutation, an overlap, a noncanonical subject/stack, or non-direct role with `ErrInvalidInput`. For a provider rejection, run the higher-consistency confirmation check before classifying the original error: if the desired state is already true/false, return success; otherwise return the classified error. `confirm(ctx, grants, expected)` returns `(bool, error)` and issues bounded batch-check requests with `consistency:"HIGHER_CONSISTENCY"`; a false result is converted by the caller to `ErrWriteUnconfirmed`, while dependency/protocol errors retain their stricter classified error.

- [ ] **Step 4: Run mutation and complete adapter tests**

Run: `rtk go test ./internal/openfga -run 'TestAuthorizationAdapter' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit mutations**

```bash
rtk git add internal/openfga/authorization_adapter.go internal/openfga/authorization_adapter_test.go
rtk git commit -m "feat(authz): add confirmed OpenFGA relationship mutations"
```

### Task 5: Document behavior and complete verification

**Files:**
- Modify: `docs/authentication.md`
- Modify: `docs/sprint/authn_and_authz/README.md`

**Interfaces:**
- Consumes: completed port behavior from Tasks 1–4.
- Produces: operational documentation and accurate sprint status.

- [ ] **Step 1: Add the documentation assertions as a review checklist**

```markdown
- The API always sends the explicit configured OpenFGA store and immutable model IDs; it never discovers a latest model at runtime.
- Direct role writes and deletes can request higher-consistency confirmation. A confirmation timeout or negative confirmation is returned as `authorization_write_unconfirmed` and may be retried safely.
- Explicit OpenFGA denial is distinct from an OpenFGA timeout, unavailable service, or malformed response. The latter failures fail closed and map to `503 authorization_unavailable` when an authorization decision is required.
```

- [ ] **Step 2: Verify the documentation check fails before editing**

Run: `rtk rg -n 'authorization_write_unconfirmed|higher-consistency confirmation|never discovers a latest model' docs/authentication.md`

Expected: no matches.

- [ ] **Step 3: Update documentation and backlog**

Add the three assertions above to the OpenFGA authorization section of `docs/authentication.md`. Change AUTH-010's status in the sprint backlog from `Not Started` to `Done`; do not change dependency rows or unrelated ticket status.

- [ ] **Step 4: Run focused, complete, and static verification**

Run: `rtk go test ./internal/authz ./internal/openfga -count=1`

Expected: PASS.

Run: `rtk go test ./...`

Expected: PASS.

Run: `rtk git diff --check`

Expected: no output.

- [ ] **Step 5: Commit the documentation and status**

```bash
rtk git add docs/authentication.md docs/sprint/authn_and_authz/README.md
rtk git commit -m "docs: complete OpenFGA authorization adapter"
```

## Plan Self-Review

- Spec coverage: Tasks 1–4 cover the provider-neutral port, canonical IDs, configured model IDs, checks, batch checks, object listing, grants, idempotent writes/deletes, higher consistency, and every stated failure class. Task 5 covers operational documentation and backlog status.
- Scope: no task modifies `internal/app`, `internal/api`, `cmd/api`, persistence, workflow, or frontend code; later AUTH-011 through AUTH-017 remain separate.
- Type consistency: `authz.Authorizer`, `authz.Check`, `authz.Decision`, `authz.Grant`, and `authz.Mutation` are defined before the adapter tasks that consume them. The adapter constructor and every method use those exact names.
- Verification: each component has a red/green command, every task ends with a commit, and the final task runs the complete Go suite plus whitespace validation.
