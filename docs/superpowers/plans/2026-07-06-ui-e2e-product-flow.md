# UI E2E Product Flow Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the missing stack/install backend APIs and a Vite React workflow UI that drives template registration through plan, apply, approval, status polling, and log inspection.

**Architecture:** Keep the Go API as the only browser-facing backend boundary. Add stack persistence and stack-template installation behind the existing `internal/app` service/repository pattern, then add a separate `web/` React app that talks to `/v1/*` through a Vite proxy and uses polling helpers for state updates.

**Tech Stack:** Go `net/http`, pgx/Postgres embedded migrations, React 18, TypeScript, Vite, Vitest, lucide-react, CSS modules by convention through plain `src/styles.css`.

---

## Reference Documents

- Design spec: `docs/superpowers/specs/2026-07-06-ui-e2e-product-flow-design.md`
- Existing API entrypoint: `internal/api/server.go`
- Existing app service: `internal/app/service.go`
- Existing Postgres store: `internal/postgres/repositories.go`
- Existing run/read/log API tests: `internal/api/server_test.go`

## File Map

Create:

- `internal/postgres/migrations/0004_stacks.sql` - creates the durable `stacks` table and tenant/slug uniqueness.
- `web/package.json` - frontend package metadata, scripts, dependencies, and dev dependencies.
- `web/index.html` - Vite HTML entrypoint.
- `web/tsconfig.json` - TypeScript compiler config for the frontend.
- `web/tsconfig.node.json` - TypeScript config for Vite config.
- `web/vite.config.ts` - Vite React plugin, test config, and API proxy.
- `web/src/main.tsx` - React root mounting.
- `web/src/App.tsx` - workflow orchestration and page layout.
- `web/src/api/types.ts` - browser-facing API response/request types.
- `web/src/api/client.ts` - typed API helpers and error handling.
- `web/src/api/client.test.ts` - Vitest tests for API helper paths and error handling.
- `web/src/polling.ts` - terminal-state and retry-delay helpers.
- `web/src/polling.test.ts` - Vitest tests for polling helper behavior.
- `web/src/styles.css` - app layout, controls, status chips, log viewer styling.

Modify:

- `.gitignore` - ignore `web/node_modules` and `web/dist`.
- `internal/traits/traits.go` - add stack JSON fields and tenant/created fields needed by app/API/store.
- `internal/app/service.go` - add stack interfaces, commands, ID generators, use cases, validation helpers, and workspace-name helpers.
- `internal/app/service_test.go` - add app-level TDD coverage and recording fakes for stack creation and template installation.
- `internal/postgres/repositories.go` - add stack/template read and stack-template creation methods.
- `internal/postgres/store_test.go` - add migration and repository tests for stacks and installed templates.
- `internal/api/server.go` - add stack routes, request/response structs, and error mapping.
- `internal/api/server_test.go` - add API handler tests and extend test dependencies.
- `cmd/megagega-api/main.go` - wire stack repository interfaces into `app.Service`.
- `cmd/megagega-api/main_test.go` - assert stack dependencies are wired.

## Scope Note

This plan is one vertical product slice. Backend stack APIs and frontend workflow are coupled because the UI cannot prove the full product path without stack creation and installation.

---

### Task 1: Add Stack Creation To The App Service

**Files:**
- Modify: `internal/traits/traits.go`
- Modify: `internal/app/service.go`
- Test: `internal/app/service_test.go`

- [ ] **Step 1: Write failing app tests for stack creation**

Add these tests to `internal/app/service_test.go` near the existing service use-case tests:

```go
func TestCreateStackDerivesSlugAndPersistsStack(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 7, 6, 13, 30, 0, 0, time.UTC)
	stacks := &recordingStackRepository{}
	service := NewService(Service{
		Stacks:   stacks,
		StackIDs: fixedStackIDGenerator{id: traits.StackID("stack_123")},
		Clock:    fixedClock{now: now},
	})

	stack, err := service.CreateStack(ctx, CreateStackCommand{
		TenantID: traits.TenantID("tenant_123"),
		Name:     "Acme Prod",
		Tags: map[string]string{
			"env": "prod",
		},
		DefaultCredentialIDs: []traits.CredentialSetID{traits.CredentialSetID("credential_123")},
		Actor:                traits.UserID("user_123"),
	})
	if err != nil {
		t.Fatalf("CreateStack returned error: %v", err)
	}

	if stack.ID != traits.StackID("stack_123") {
		t.Fatalf("stack ID = %q, want stack_123", stack.ID)
	}
	if stack.Slug != "acme-prod" {
		t.Fatalf("slug = %q, want acme-prod", stack.Slug)
	}
	if stack.CreatedBy != traits.UserID("user_123") {
		t.Fatalf("created by = %q, want user_123", stack.CreatedBy)
	}
	if !stack.CreatedAt.Equal(now) {
		t.Fatalf("created at = %v, want %v", stack.CreatedAt, now)
	}
	if stacks.created.ID != stack.ID {
		t.Fatalf("persisted stack ID = %q, want %q", stacks.created.ID, stack.ID)
	}
	if stacks.created.Tags["env"] != "prod" {
		t.Fatalf("persisted tags = %#v", stacks.created.Tags)
	}
	if len(stacks.created.DefaultCredentialIDs) != 1 || stacks.created.DefaultCredentialIDs[0] != traits.CredentialSetID("credential_123") {
		t.Fatalf("default credential IDs = %#v", stacks.created.DefaultCredentialIDs)
	}
}

func TestCreateStackRejectsMissingActor(t *testing.T) {
	t.Parallel()

	service := NewService(Service{
		Stacks:   &recordingStackRepository{},
		StackIDs: fixedStackIDGenerator{id: traits.StackID("stack_123")},
	})

	_, err := service.CreateStack(context.Background(), CreateStackCommand{
		TenantID: traits.TenantID("tenant_123"),
		Name:     "Acme Prod",
	})
	if !errors.Is(err, ErrInvalidCommand) {
		t.Fatalf("error = %v, want ErrInvalidCommand", err)
	}
}

func TestCreateStackReturnsDuplicateSlugConflict(t *testing.T) {
	t.Parallel()

	stacks := &recordingStackRepository{createErr: ErrDuplicateStackSlug}
	service := NewService(Service{
		Stacks:   stacks,
		StackIDs: fixedStackIDGenerator{id: traits.StackID("stack_123")},
		Clock:    fixedClock{now: time.Now()},
	})

	_, err := service.CreateStack(context.Background(), CreateStackCommand{
		TenantID: traits.TenantID("tenant_123"),
		Name:     "Acme Prod",
		Slug:     "acme-prod",
		Actor:    traits.UserID("user_123"),
	})
	if !errors.Is(err, ErrDuplicateStackSlug) {
		t.Fatalf("error = %v, want ErrDuplicateStackSlug", err)
	}
}
```

Add these recording helpers near the existing fakes in `internal/app/service_test.go`:

```go
type recordingStackRepository struct {
	created         traits.Stack
	stack           traits.Stack
	view            StackView
	gotTenantID     traits.TenantID
	gotStackID      traits.StackID
	createErr       error
	getErr          error
	getViewErr      error
}

func (repository *recordingStackRepository) CreateStack(_ context.Context, stack traits.Stack) error {
	if repository.createErr != nil {
		return repository.createErr
	}
	repository.created = stack
	return nil
}

func (repository *recordingStackRepository) GetStack(_ context.Context, tenantID traits.TenantID, stackID traits.StackID) (traits.Stack, error) {
	repository.gotTenantID = tenantID
	repository.gotStackID = stackID
	if repository.getErr != nil {
		return traits.Stack{}, repository.getErr
	}
	return repository.stack, nil
}

func (repository *recordingStackRepository) GetStackWithTemplates(_ context.Context, tenantID traits.TenantID, stackID traits.StackID) (StackView, error) {
	repository.gotTenantID = tenantID
	repository.gotStackID = stackID
	if repository.getViewErr != nil {
		return StackView{}, repository.getViewErr
	}
	return repository.view, nil
}

type fixedStackIDGenerator struct {
	id traits.StackID
}

func (generator fixedStackIDGenerator) NewStackID() traits.StackID {
	return generator.id
}
```

- [ ] **Step 2: Run the failing app tests**

Run:

```bash
go test ./internal/app -run 'TestCreateStack' -count=1
```

Expected: build fails with errors for undefined `Service.Stacks`, `Service.StackIDs`, `CreateStack`, `StackView`, and `ErrDuplicateStackSlug`.

- [ ] **Step 3: Extend shared stack traits**

In `internal/traits/traits.go`, replace the current `Stack` and `StackTemplate` structs with:

```go
// Stack is a logical infrastructure composition.
type Stack struct {
	ID                   StackID           `json:"id"`
	TenantID             TenantID          `json:"tenant_id"`
	Name                 string            `json:"name"`
	Slug                 string            `json:"slug"`
	Tags                 map[string]string `json:"tags"`
	DefaultCredentialIDs []CredentialSetID `json:"default_credential_ids"`
	CreatedBy            UserID            `json:"created_by"`
	CreatedAt            time.Time         `json:"created_at"`
}

// StackTemplate is one template installed into one stack.
type StackTemplate struct {
	ID               StackTemplateID       `json:"id"`
	TenantID         TenantID              `json:"tenant_id"`
	StackID          StackID               `json:"stack_id"`
	TemplateID       TemplateID            `json:"template_id"`
	SelectedRef      string                `json:"selected_ref"`
	WorkspaceName    string                `json:"workspace_name"`
	ConfigJSON       json.RawMessage       `json:"config_json"`
	LastAppliedRunID TemplateRunID         `json:"last_applied_run_id"`
	LastAppliedRef   string                `json:"last_applied_ref"`
	LastAppliedAt    time.Time             `json:"last_applied_at,omitempty"`
	Lifecycle        StackTemplateLifecycle `json:"lifecycle"`
}
```

Run `gofmt` after editing:

```bash
gofmt -w internal/traits/traits.go
```

- [ ] **Step 4: Implement stack creation contracts and use case**

In `internal/app/service.go`, add this error near the existing app errors:

```go
ErrDuplicateStackSlug = errors.New("duplicate stack slug")
```

Add these interfaces near the repository interfaces:

```go
// StackRepository persists and reads tenant-owned stacks.
type StackRepository interface {
	CreateStack(ctx context.Context, stack traits.Stack) error
	GetStack(ctx context.Context, tenantID traits.TenantID, stackID traits.StackID) (traits.Stack, error)
	GetStackWithTemplates(ctx context.Context, tenantID traits.TenantID, stackID traits.StackID) (StackView, error)
}

// StackIDGenerator creates Stack identifiers.
type StackIDGenerator interface {
	NewStackID() traits.StackID
}
```

Add `StackView` near the command types:

```go
// StackView returns one stack with its installed templates.
type StackView struct {
	Stack     traits.Stack
	Templates []traits.StackTemplate
}
```

Add fields to `Service`:

```go
Stacks   StackRepository
StackIDs StackIDGenerator
```

Add this default generator in `NewService` after registration IDs:

```go
if service.StackIDs == nil {
	service.StackIDs = randomStackIDGenerator{}
}
```

Add `GetStackCommand` near the existing command structs:

```go
// GetStackCommand asks the app to read one stack and its installed templates.
type GetStackCommand struct {
	TenantID traits.TenantID
	StackID  traits.StackID
}
```

Add these use cases and helpers near the existing app methods:

```go
// CreateStack creates a tenant-owned infrastructure stack.
func (service *Service) CreateStack(ctx context.Context, command CreateStackCommand) (traits.Stack, error) {
	if err := validateCreateStackCommand(command); err != nil {
		return traits.Stack{}, err
	}

	slug := strings.TrimSpace(command.Slug)
	if slug == "" {
		slug = slugFromName(command.Name)
	}

	stack := traits.Stack{
		ID:                   service.StackIDs.NewStackID(),
		TenantID:             command.TenantID,
		Name:                 strings.TrimSpace(command.Name),
		Slug:                 slug,
		Tags:                 cloneStringMap(command.Tags),
		DefaultCredentialIDs: append([]traits.CredentialSetID(nil), command.DefaultCredentialIDs...),
		CreatedBy:            command.Actor,
		CreatedAt:            service.Clock.Now(),
	}

	if err := service.Stacks.CreateStack(ctx, stack); err != nil {
		return traits.Stack{}, fmt.Errorf("create stack: %w", err)
	}

	return stack, nil
}

// GetStack returns one tenant-owned stack with installed templates.
func (service *Service) GetStack(ctx context.Context, command GetStackCommand) (StackView, error) {
	if err := validateGetStackCommand(command); err != nil {
		return StackView{}, err
	}

	view, err := service.Stacks.GetStackWithTemplates(ctx, command.TenantID, command.StackID)
	if err != nil {
		return StackView{}, fmt.Errorf("get stack: %w", err)
	}
	return view, nil
}

func validateCreateStackCommand(command CreateStackCommand) error {
	switch {
	case command.TenantID == "":
		return fmt.Errorf("%w: tenant id is required", ErrInvalidCommand)
	case strings.TrimSpace(command.Name) == "":
		return fmt.Errorf("%w: stack name is required", ErrInvalidCommand)
	case strings.TrimSpace(command.Slug) != "" && !validStackSlug(command.Slug):
		return fmt.Errorf("%w: stack slug is invalid", ErrInvalidCommand)
	case strings.TrimSpace(command.Slug) == "" && slugFromName(command.Name) == "":
		return fmt.Errorf("%w: stack slug is invalid", ErrInvalidCommand)
	case command.Actor == "":
		return fmt.Errorf("%w: actor is required", ErrInvalidCommand)
	default:
		return nil
	}
}

func validateGetStackCommand(command GetStackCommand) error {
	switch {
	case command.TenantID == "":
		return fmt.Errorf("%w: tenant id is required", ErrInvalidCommand)
	case command.StackID == "":
		return fmt.Errorf("%w: stack id is required", ErrInvalidCommand)
	default:
		return nil
	}
}

func slugFromName(name string) string {
	var builder strings.Builder
	lastHyphen := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			builder.WriteRune(r)
			lastHyphen = false
		default:
			if !lastHyphen && builder.Len() > 0 {
				builder.WriteByte('-')
				lastHyphen = true
			}
		}
	}
	return strings.Trim(builder.String(), "-")
}

func validStackSlug(slug string) bool {
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return false
	}
	for _, r := range slug {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '-':
		default:
			return false
		}
	}
	return true
}

func cloneStringMap(input map[string]string) map[string]string {
	if len(input) == 0 {
		return map[string]string{}
	}
	cloned := make(map[string]string, len(input))
	for key, value := range input {
		cloned[key] = value
	}
	return cloned
}
```

Add the random generator near the existing ID generators:

```go
type randomStackIDGenerator struct{}

func (randomStackIDGenerator) NewStackID() traits.StackID {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return traits.StackID(fmt.Sprintf("stack_%d", time.Now().UTC().UnixNano()))
	}
	return traits.StackID("stack_" + hex.EncodeToString(bytes[:]))
}
```

- [ ] **Step 5: Run app tests**

Run:

```bash
go test ./internal/app -run 'TestCreateStack' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit stack creation app changes**

Run:

```bash
git add internal/traits/traits.go internal/app/service.go internal/app/service_test.go
git commit -m "feat: add stack creation use case"
```

---

### Task 2: Add Template Installation To The App Service

**Files:**
- Modify: `internal/app/service.go`
- Modify: `internal/app/service_test.go`

- [ ] **Step 1: Write failing tests for template installation**

Add these tests to `internal/app/service_test.go`:

```go
func TestAddTemplateToStackValidatesVariablesAndPersistsStackTemplate(t *testing.T) {
	t.Parallel()

	stacks := &recordingStackRepository{
		stack: traits.Stack{
			ID:       traits.StackID("stack_123"),
			TenantID: traits.TenantID("tenant_123"),
			Name:     "Acme Prod",
			Slug:     "acme-prod",
		},
	}
	templates := &recordingTemplateRepository{
		template: traits.Template{
			ID:        traits.TemplateID("template_123"),
			TenantID:  traits.TenantID("tenant_123"),
			SourceRef: "main",
			Status:    traits.TemplateActive,
		},
		variables: []traits.TemplateVariable{
			{Name: "region", Required: true},
			{Name: "cidr", Required: false, HasDefault: true},
		},
	}
	installer := &recordingStackTemplateInstaller{}
	service := NewService(Service{
		Stacks:                 stacks,
		TemplateMetadata:       templates,
		Templates:              templates,
		StackTemplateInstaller: installer,
		StackTemplateIDs:       fixedStackTemplateIDGenerator{id: traits.StackTemplateID("stack_template_a1b2c3d4")},
	})

	stackTemplate, err := service.AddTemplateToStack(context.Background(), AddTemplateToStackCommand{
		TenantID:    traits.TenantID("tenant_123"),
		StackID:     traits.StackID("stack_123"),
		TemplateID:  traits.TemplateID("template_123"),
		SelectedRef: "main",
		ConfigJSON:  json.RawMessage(`{"region":"us-east-1"}`),
		Actor:       traits.UserID("user_123"),
	})
	if err != nil {
		t.Fatalf("AddTemplateToStack returned error: %v", err)
	}

	if stackTemplate.ID != traits.StackTemplateID("stack_template_a1b2c3d4") {
		t.Fatalf("stack template ID = %q, want stack_template_a1b2c3d4", stackTemplate.ID)
	}
	if stackTemplate.TenantID != traits.TenantID("tenant_123") {
		t.Fatalf("tenant ID = %q, want tenant_123", stackTemplate.TenantID)
	}
	if stackTemplate.WorkspaceName != "meg_acme_prod_a1b2c3d4" {
		t.Fatalf("workspace name = %q, want meg_acme_prod_a1b2c3d4", stackTemplate.WorkspaceName)
	}
	if stackTemplate.Lifecycle != traits.StackTemplateActive {
		t.Fatalf("lifecycle = %q, want active", stackTemplate.Lifecycle)
	}
	if string(installer.created.ConfigJSON) != `{"region":"us-east-1"}` {
		t.Fatalf("config json = %s", installer.created.ConfigJSON)
	}
}

func TestAddTemplateToStackRejectsMissingRequiredVariable(t *testing.T) {
	t.Parallel()

	service := NewService(Service{
		Stacks: &recordingStackRepository{
			stack: traits.Stack{ID: traits.StackID("stack_123"), TenantID: traits.TenantID("tenant_123"), Slug: "acme-prod"},
		},
		TemplateMetadata: &recordingTemplateRepository{
			template:  traits.Template{ID: traits.TemplateID("template_123"), TenantID: traits.TenantID("tenant_123"), Status: traits.TemplateActive},
		},
		Templates: &recordingTemplateRepository{
			variables: []traits.TemplateVariable{{Name: "region", Required: true}},
		},
		StackTemplateInstaller: &recordingStackTemplateInstaller{},
		StackTemplateIDs:       fixedStackTemplateIDGenerator{id: traits.StackTemplateID("stack_template_a1b2c3d4")},
	})

	_, err := service.AddTemplateToStack(context.Background(), AddTemplateToStackCommand{
		TenantID:    traits.TenantID("tenant_123"),
		StackID:     traits.StackID("stack_123"),
		TemplateID:  traits.TemplateID("template_123"),
		SelectedRef: "main",
		ConfigJSON:  json.RawMessage(`{}`),
		Actor:       traits.UserID("user_123"),
	})
	if !errors.Is(err, ErrStackTemplateConfigInvalid) {
		t.Fatalf("error = %v, want ErrStackTemplateConfigInvalid", err)
	}
}

func TestAddTemplateToStackRejectsUnknownVariable(t *testing.T) {
	t.Parallel()

	service := NewService(Service{
		Stacks: &recordingStackRepository{
			stack: traits.Stack{ID: traits.StackID("stack_123"), TenantID: traits.TenantID("tenant_123"), Slug: "acme-prod"},
		},
		TemplateMetadata: &recordingTemplateRepository{
			template:  traits.Template{ID: traits.TemplateID("template_123"), TenantID: traits.TenantID("tenant_123"), Status: traits.TemplateActive},
		},
		Templates: &recordingTemplateRepository{
			variables: []traits.TemplateVariable{{Name: "region", Required: true}},
		},
		StackTemplateInstaller: &recordingStackTemplateInstaller{},
		StackTemplateIDs:       fixedStackTemplateIDGenerator{id: traits.StackTemplateID("stack_template_a1b2c3d4")},
	})

	_, err := service.AddTemplateToStack(context.Background(), AddTemplateToStackCommand{
		TenantID:    traits.TenantID("tenant_123"),
		StackID:     traits.StackID("stack_123"),
		TemplateID:  traits.TemplateID("template_123"),
		SelectedRef: "main",
		ConfigJSON:  json.RawMessage(`{"region":"us-east-1","extra":"nope"}`),
		Actor:       traits.UserID("user_123"),
	})
	if !errors.Is(err, ErrStackTemplateConfigInvalid) {
		t.Fatalf("error = %v, want ErrStackTemplateConfigInvalid", err)
	}
}

func TestAddTemplateToStackRejectsInactiveTemplate(t *testing.T) {
	t.Parallel()

	service := NewService(Service{
		Stacks: &recordingStackRepository{
			stack: traits.Stack{ID: traits.StackID("stack_123"), TenantID: traits.TenantID("tenant_123"), Slug: "acme-prod"},
		},
		TemplateMetadata: &recordingTemplateRepository{
			template: traits.Template{ID: traits.TemplateID("template_123"), TenantID: traits.TenantID("tenant_123"), Status: traits.TemplateInvalid},
		},
		Templates:              &recordingTemplateRepository{},
		StackTemplateInstaller: &recordingStackTemplateInstaller{},
		StackTemplateIDs:       fixedStackTemplateIDGenerator{id: traits.StackTemplateID("stack_template_a1b2c3d4")},
	})

	_, err := service.AddTemplateToStack(context.Background(), AddTemplateToStackCommand{
		TenantID:    traits.TenantID("tenant_123"),
		StackID:     traits.StackID("stack_123"),
		TemplateID:  traits.TemplateID("template_123"),
		SelectedRef: "main",
		ConfigJSON:  json.RawMessage(`{}`),
		Actor:       traits.UserID("user_123"),
	})
	if !errors.Is(err, ErrTemplateNotInstallable) {
		t.Fatalf("error = %v, want ErrTemplateNotInstallable", err)
	}
}
```

Add these fakes:

```go
type recordingStackTemplateInstaller struct {
	created   traits.StackTemplate
	createErr error
}

func (installer *recordingStackTemplateInstaller) CreateStackTemplate(_ context.Context, stackTemplate traits.StackTemplate) error {
	if installer.createErr != nil {
		return installer.createErr
	}
	installer.created = stackTemplate
	return nil
}

type fixedStackTemplateIDGenerator struct {
	id traits.StackTemplateID
}

func (generator fixedStackTemplateIDGenerator) NewStackTemplateID() traits.StackTemplateID {
	return generator.id
}
```

Extend `recordingTemplateRepository` with:

```go
gotGetTemplateTenantID traits.TenantID
gotGetTemplateID       traits.TemplateID
getTemplateErr         error
```

and add:

```go
func (repository *recordingTemplateRepository) GetTemplate(_ context.Context, tenantID traits.TenantID, templateID traits.TemplateID) (traits.Template, error) {
	repository.gotGetTemplateTenantID = tenantID
	repository.gotGetTemplateID = templateID
	if repository.getTemplateErr != nil {
		return traits.Template{}, repository.getTemplateErr
	}
	return repository.template, nil
}
```

- [ ] **Step 2: Run the failing app tests**

Run:

```bash
go test ./internal/app -run 'TestAddTemplateToStack' -count=1
```

Expected: build fails with undefined `TemplateMetadata`, `StackTemplateInstaller`, `StackTemplateIDs`, `GetTemplate`, `ErrStackTemplateConfigInvalid`, and `ErrTemplateNotInstallable`.

- [ ] **Step 3: Implement template install contracts**

In `internal/app/service.go`, add errors:

```go
ErrTemplateNotInstallable     = errors.New("template is not installable")
ErrStackTemplateConfigInvalid = errors.New("stack template config is invalid")
```

Add this interface near `TemplateRepository`:

```go
// TemplateMetadataRepository reads immutable template metadata.
type TemplateMetadataRepository interface {
	GetTemplate(ctx context.Context, tenantID traits.TenantID, templateID traits.TemplateID) (traits.Template, error)
}
```

Add interfaces:

```go
// StackTemplateInstaller persists installed stack templates.
type StackTemplateInstaller interface {
	CreateStackTemplate(ctx context.Context, stackTemplate traits.StackTemplate) error
}

// StackTemplateIDGenerator creates StackTemplate identifiers.
type StackTemplateIDGenerator interface {
	NewStackTemplateID() traits.StackTemplateID
}
```

Add fields to `Service`:

```go
TemplateMetadata       TemplateMetadataRepository
StackTemplateInstaller StackTemplateInstaller
StackTemplateIDs       StackTemplateIDGenerator
```

Add this default generator in `NewService`:

```go
if service.StackTemplateIDs == nil {
	service.StackTemplateIDs = randomStackTemplateIDGenerator{}
}
```

- [ ] **Step 4: Implement `AddTemplateToStack`**

In `internal/app/service.go`, add:

```go
func (service *Service) AddTemplateToStack(ctx context.Context, command AddTemplateToStackCommand) (traits.StackTemplate, error) {
	if err := validateAddTemplateToStackCommand(command); err != nil {
		return traits.StackTemplate{}, err
	}

	stack, err := service.Stacks.GetStack(ctx, command.TenantID, command.StackID)
	if err != nil {
		return traits.StackTemplate{}, fmt.Errorf("get stack: %w", err)
	}

	template, err := service.TemplateMetadata.GetTemplate(ctx, command.TenantID, command.TemplateID)
	if err != nil {
		return traits.StackTemplate{}, fmt.Errorf("get template: %w", err)
	}
	if template.Status != traits.TemplateActive {
		return traits.StackTemplate{}, fmt.Errorf("%w: status is %q", ErrTemplateNotInstallable, template.Status)
	}

	variables, err := service.Templates.GetTemplateVariables(ctx, command.TenantID, command.TemplateID)
	if err != nil {
		return traits.StackTemplate{}, fmt.Errorf("get template variables: %w", err)
	}
	configJSON, err := validateTemplateConfig(command.ConfigJSON, variables)
	if err != nil {
		return traits.StackTemplate{}, err
	}

	id := service.StackTemplateIDs.NewStackTemplateID()
	stackTemplate := traits.StackTemplate{
		ID:            id,
		TenantID:      command.TenantID,
		StackID:       command.StackID,
		TemplateID:    command.TemplateID,
		SelectedRef:   strings.TrimSpace(command.SelectedRef),
		WorkspaceName: workspaceName(stack.Slug, id),
		ConfigJSON:    configJSON,
		Lifecycle:     traits.StackTemplateActive,
	}

	if err := service.StackTemplateInstaller.CreateStackTemplate(ctx, stackTemplate); err != nil {
		return traits.StackTemplate{}, fmt.Errorf("create stack template: %w", err)
	}
	return stackTemplate, nil
}

func validateAddTemplateToStackCommand(command AddTemplateToStackCommand) error {
	switch {
	case command.TenantID == "":
		return fmt.Errorf("%w: tenant id is required", ErrInvalidCommand)
	case command.StackID == "":
		return fmt.Errorf("%w: stack id is required", ErrInvalidCommand)
	case command.TemplateID == "":
		return fmt.Errorf("%w: template id is required", ErrInvalidCommand)
	case strings.TrimSpace(command.SelectedRef) == "":
		return fmt.Errorf("%w: selected ref is required", ErrInvalidCommand)
	case command.Actor == "":
		return fmt.Errorf("%w: actor is required", ErrInvalidCommand)
	default:
		return nil
	}
}

func validateTemplateConfig(raw json.RawMessage, variables []traits.TemplateVariable) (json.RawMessage, error) {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}

	var config map[string]json.RawMessage
	if err := json.Unmarshal(raw, &config); err != nil {
		return nil, fmt.Errorf("%w: config must be a JSON object", ErrStackTemplateConfigInvalid)
	}
	if config == nil {
		return nil, fmt.Errorf("%w: config must be a JSON object", ErrStackTemplateConfigInvalid)
	}

	known := make(map[string]traits.TemplateVariable, len(variables))
	for _, variable := range variables {
		known[variable.Name] = variable
	}

	for name, value := range config {
		if _, ok := known[name]; !ok {
			return nil, fmt.Errorf("%w: unknown variable %q", ErrStackTemplateConfigInvalid, name)
		}
		if string(value) == "null" {
			return nil, fmt.Errorf("%w: variable %q must not be null", ErrStackTemplateConfigInvalid, name)
		}
	}

	for _, variable := range variables {
		if variable.Required {
			value, ok := config[variable.Name]
			if !ok || string(value) == "null" {
				return nil, fmt.Errorf("%w: required variable %q is missing", ErrStackTemplateConfigInvalid, variable.Name)
			}
		}
	}

	normalized, err := json.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("marshal stack template config: %w", err)
	}
	return normalized, nil
}

func workspaceName(stackSlug string, stackTemplateID traits.StackTemplateID) string {
	normalizedSlug := normalizeWorkspacePart(stackSlug)
	if normalizedSlug == "" {
		normalizedSlug = "stack"
	}

	id := string(stackTemplateID)
	shortID := id
	if len(shortID) > 8 {
		shortID = shortID[len(shortID)-8:]
	}

	const prefix = "meg_"
	const separator = "_"
	const maxLength = 90
	maxSlugLength := maxLength - len(prefix) - len(separator) - len(shortID)
	if maxSlugLength < 1 {
		maxSlugLength = 1
	}
	if len(normalizedSlug) > maxSlugLength {
		normalizedSlug = strings.Trim(normalizedSlug[:maxSlugLength], "_")
	}
	if normalizedSlug == "" {
		normalizedSlug = "stack"
	}
	return prefix + normalizedSlug + separator + shortID
}

func normalizeWorkspacePart(value string) string {
	var builder strings.Builder
	lastUnderscore := false
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			builder.WriteRune(r)
			lastUnderscore = false
		default:
			if !lastUnderscore && builder.Len() > 0 {
				builder.WriteByte('_')
				lastUnderscore = true
			}
		}
	}
	return strings.Trim(builder.String(), "_")
}
```

Add the generator:

```go
type randomStackTemplateIDGenerator struct{}

func (randomStackTemplateIDGenerator) NewStackTemplateID() traits.StackTemplateID {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return traits.StackTemplateID(fmt.Sprintf("stack_template_%d", time.Now().UTC().UnixNano()))
	}
	return traits.StackTemplateID("stack_template_" + hex.EncodeToString(bytes[:]))
}
```

- [ ] **Step 5: Run app tests**

Run:

```bash
go test ./internal/app -run 'TestCreateStack|TestAddTemplateToStack' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit template installation app changes**

Run:

```bash
git add internal/app/service.go internal/app/service_test.go
git commit -m "feat: add stack template install use case"
```

---

### Task 3: Add Postgres Persistence For Stacks And Installed Templates

**Files:**
- Create: `internal/postgres/migrations/0004_stacks.sql`
- Modify: `internal/postgres/repositories.go`
- Modify: `internal/postgres/store_test.go`

- [ ] **Step 1: Write failing Postgres tests**

In `internal/postgres/store_test.go`, add `app.StackRepository`, `app.StackTemplateInstaller`, and `app.TemplateMetadataRepository` assertions to the existing interface assertion block:

```go
_ app.StackRepository               = (*Store)(nil)
_ app.StackTemplateInstaller        = (*Store)(nil)
_ app.TemplateMetadataRepository    = (*Store)(nil)
```

Add `"stacks"` to the `TestMigrateAppliesSchema` table list.

Add these tests after the template variable tests:

```go
func TestCreateAndGetStack(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)
	createdAt := time.Date(2026, 7, 6, 13, 30, 0, 123456000, time.UTC)

	stack := traits.Stack{
		ID:                   traits.StackID("stack_123"),
		TenantID:             traits.TenantID("tenant_123"),
		Name:                 "Acme Prod",
		Slug:                 "acme-prod",
		Tags:                 map[string]string{"env": "prod"},
		DefaultCredentialIDs: []traits.CredentialSetID{traits.CredentialSetID("credential_123")},
		CreatedBy:            traits.UserID("user_123"),
		CreatedAt:            createdAt,
	}

	if err := store.CreateStack(ctx, stack); err != nil {
		t.Fatalf("CreateStack returned error: %v", err)
	}

	got, err := store.GetStack(ctx, traits.TenantID("tenant_123"), traits.StackID("stack_123"))
	if err != nil {
		t.Fatalf("GetStack returned error: %v", err)
	}

	if got.ID != stack.ID || got.TenantID != stack.TenantID || got.Name != stack.Name || got.Slug != stack.Slug || got.CreatedBy != stack.CreatedBy {
		t.Fatalf("stack = %#v, want %#v", got, stack)
	}
	if got.Tags["env"] != "prod" {
		t.Fatalf("tags = %#v", got.Tags)
	}
	if len(got.DefaultCredentialIDs) != 1 || got.DefaultCredentialIDs[0] != traits.CredentialSetID("credential_123") {
		t.Fatalf("default credential IDs = %#v", got.DefaultCredentialIDs)
	}
	if !got.CreatedAt.Equal(createdAt) {
		t.Fatalf("created at = %v, want %v", got.CreatedAt, createdAt)
	}
}

func TestCreateStackReturnsDuplicateSlugConflict(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)

	first := traits.Stack{
		ID:        traits.StackID("stack_123"),
		TenantID:  traits.TenantID("tenant_123"),
		Name:      "Acme Prod",
		Slug:      "acme-prod",
		CreatedBy: traits.UserID("user_123"),
		CreatedAt: time.Now().UTC(),
	}
	if err := store.CreateStack(ctx, first); err != nil {
		t.Fatalf("CreateStack first returned error: %v", err)
	}

	second := first
	second.ID = traits.StackID("stack_456")
	err := store.CreateStack(ctx, second)
	if !errors.Is(err, app.ErrDuplicateStackSlug) {
		t.Fatalf("error = %v, want app.ErrDuplicateStackSlug", err)
	}
}

func TestGetTemplateReturnsTenantScopedTemplate(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)
	createdAt := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)
	template := traits.Template{
		ID:                traits.TemplateID("template_123"),
		TenantID:          traits.TenantID("tenant_123"),
		RepoOwner:         "acme",
		RepoName:          "infra-templates",
		SourceRef:         "main",
		ResolvedCommitSHA: "abc123",
		RootPath:          ".",
		Name:              "vpc",
		Tags:              []string{"network"},
		Status:            traits.TemplateActive,
		CreatedAt:         createdAt,
	}
	if _, err := store.UpsertTemplateWithVariables(ctx, template, nil); err != nil {
		t.Fatalf("UpsertTemplateWithVariables returned error: %v", err)
	}

	got, err := store.GetTemplate(ctx, traits.TenantID("tenant_123"), traits.TemplateID("template_123"))
	if err != nil {
		t.Fatalf("GetTemplate returned error: %v", err)
	}
	if got.ID != traits.TemplateID("template_123") || got.Status != traits.TemplateActive || got.Tags[0] != "network" {
		t.Fatalf("template = %#v", got)
	}

	_, err = store.GetTemplate(ctx, traits.TenantID("tenant_456"), traits.TemplateID("template_123"))
	if !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("other tenant error = %v, want app.ErrNotFound", err)
	}
}

func TestCreateStackTemplateAndGetStackWithTemplates(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)

	stack := traits.Stack{
		ID:        traits.StackID("stack_123"),
		TenantID:  traits.TenantID("tenant_123"),
		Name:      "Acme Prod",
		Slug:      "acme-prod",
		CreatedBy: traits.UserID("user_123"),
		CreatedAt: time.Now().UTC(),
	}
	if err := store.CreateStack(ctx, stack); err != nil {
		t.Fatalf("CreateStack returned error: %v", err)
	}

	stackTemplate := traits.StackTemplate{
		ID:            traits.StackTemplateID("stack_template_123"),
		TenantID:      traits.TenantID("tenant_123"),
		StackID:       traits.StackID("stack_123"),
		TemplateID:    traits.TemplateID("template_123"),
		SelectedRef:   "main",
		WorkspaceName: "meg_acme_prod_late_123",
		ConfigJSON:    json.RawMessage(`{"region":"us-east-1"}`),
		Lifecycle:     traits.StackTemplateActive,
	}
	if err := store.CreateStackTemplate(ctx, stackTemplate); err != nil {
		t.Fatalf("CreateStackTemplate returned error: %v", err)
	}

	view, err := store.GetStackWithTemplates(ctx, traits.TenantID("tenant_123"), traits.StackID("stack_123"))
	if err != nil {
		t.Fatalf("GetStackWithTemplates returned error: %v", err)
	}
	if view.Stack.ID != traits.StackID("stack_123") {
		t.Fatalf("view stack ID = %q, want stack_123", view.Stack.ID)
	}
	if len(view.Templates) != 1 {
		t.Fatalf("len(view.Templates) = %d, want 1", len(view.Templates))
	}
	if view.Templates[0].TenantID != traits.TenantID("tenant_123") {
		t.Fatalf("template tenant ID = %q, want tenant_123", view.Templates[0].TenantID)
	}
	if string(view.Templates[0].ConfigJSON) != `{"region": "us-east-1"}` {
		t.Fatalf("template config = %s", view.Templates[0].ConfigJSON)
	}
}
```

Add `encoding/json` to the test imports.

- [ ] **Step 2: Run failing Postgres tests**

Run:

```bash
go test ./internal/postgres -run 'TestMigrateAppliesSchema|TestCreateAndGetStack|TestCreateStackReturnsDuplicateSlugConflict|TestGetTemplateReturnsTenantScopedTemplate|TestCreateStackTemplateAndGetStackWithTemplates' -count=1
```

Expected with `MEGAGEGA_POSTGRES_TEST_DSN` unset: SKIP for integration tests after compile succeeds. Expected with the DSN set before implementation: build fails because repository methods and migration are missing.

- [ ] **Step 3: Add the stacks migration**

Create `internal/postgres/migrations/0004_stacks.sql`:

```sql
create table stacks (
	id text primary key,
	tenant_id text not null,
	name text not null,
	slug text not null,
	tags_json jsonb not null default '{}'::jsonb,
	default_credential_ids_json jsonb not null default '[]'::jsonb,
	created_by text not null,
	created_at timestamptz not null
);

create unique index stacks_tenant_id_slug_idx on stacks (tenant_id, slug);
create index stacks_tenant_id_id_idx on stacks (tenant_id, id);
```

- [ ] **Step 4: Implement Postgres repository methods**

In `internal/postgres/repositories.go`, add `github.com/jackc/pgx/v5/pgconn` to imports.

Add these methods before the existing `GetStackTemplate` method:

```go
func (store *Store) CreateStack(ctx context.Context, stack traits.Stack) error {
	tagsJSON, err := json.Marshal(stack.Tags)
	if err != nil {
		return fmt.Errorf("marshal stack tags: %w", err)
	}
	if tagsJSON == nil {
		tagsJSON = []byte("{}")
	}

	credentialIDsJSON, err := json.Marshal(stack.DefaultCredentialIDs)
	if err != nil {
		return fmt.Errorf("marshal default credential IDs: %w", err)
	}
	if credentialIDsJSON == nil {
		credentialIDsJSON = []byte("[]")
	}

	_, err = store.pool.Exec(ctx, `
		insert into stacks (
			id,
			tenant_id,
			name,
			slug,
			tags_json,
			default_credential_ids_json,
			created_by,
			created_at
		) values ($1, $2, $3, $4, $5::jsonb, $6::jsonb, $7, $8)
	`,
		stack.ID,
		stack.TenantID,
		stack.Name,
		stack.Slug,
		tagsJSON,
		credentialIDsJSON,
		stack.CreatedBy,
		stack.CreatedAt,
	)
	if duplicateConstraint(err, "stacks_tenant_id_slug_idx") {
		return app.ErrDuplicateStackSlug
	}
	if err != nil {
		return fmt.Errorf("create stack: %w", err)
	}
	return nil
}

func (store *Store) GetStack(ctx context.Context, tenantID traits.TenantID, stackID traits.StackID) (traits.Stack, error) {
	stack, err := store.getStack(ctx, tenantID, stackID)
	if err != nil {
		return traits.Stack{}, err
	}
	return stack, nil
}

func (store *Store) GetStackWithTemplates(ctx context.Context, tenantID traits.TenantID, stackID traits.StackID) (app.StackView, error) {
	stack, err := store.getStack(ctx, tenantID, stackID)
	if err != nil {
		return app.StackView{}, err
	}

	rows, err := store.pool.Query(ctx, `
		select
			id,
			tenant_id,
			stack_id,
			template_id,
			selected_ref,
			workspace_name,
			config_json,
			last_applied_run_id,
			last_applied_ref,
			last_applied_at,
			lifecycle
		from stack_templates
		where tenant_id = $1
			and stack_id = $2
		order by id
	`, tenantID, stackID)
	if err != nil {
		return app.StackView{}, fmt.Errorf("get stack templates: %w", err)
	}
	defer rows.Close()

	var templates []traits.StackTemplate
	for rows.Next() {
		stackTemplate, err := scanStackTemplate(rows)
		if err != nil {
			return app.StackView{}, err
		}
		templates = append(templates, stackTemplate)
	}
	if err := rows.Err(); err != nil {
		return app.StackView{}, fmt.Errorf("iterate stack templates: %w", err)
	}

	return app.StackView{Stack: stack, Templates: templates}, nil
}

func (store *Store) CreateStackTemplate(ctx context.Context, stackTemplate traits.StackTemplate) error {
	result, err := store.pool.Exec(ctx, `
		insert into stack_templates (
			id,
			tenant_id,
			stack_id,
			template_id,
			selected_ref,
			workspace_name,
			config_json,
			last_applied_run_id,
			last_applied_ref,
			last_applied_at,
			lifecycle
		)
		select $1, $2, $3, $4, $5, $6, $7::jsonb, $8, $9, $10, $11
		where exists (
			select 1
			from stacks
			where tenant_id = $2
				and id = $3
		)
	`,
		stackTemplate.ID,
		stackTemplate.TenantID,
		stackTemplate.StackID,
		stackTemplate.TemplateID,
		stackTemplate.SelectedRef,
		stackTemplate.WorkspaceName,
		stackTemplate.ConfigJSON,
		stackTemplate.LastAppliedRunID,
		stackTemplate.LastAppliedRef,
		nullTime(stackTemplate.LastAppliedAt),
		stackTemplate.Lifecycle,
	)
	if err != nil {
		return fmt.Errorf("create stack template: %w", err)
	}
	if result.RowsAffected() == 0 {
		return app.ErrNotFound
	}
	return nil
}

func (store *Store) GetTemplate(ctx context.Context, tenantID traits.TenantID, templateID traits.TemplateID) (traits.Template, error) {
	var template traits.Template
	var tagsJSON []byte
	err := store.pool.QueryRow(ctx, `
		select
			id,
			tenant_id,
			repo_owner,
			repo_name,
			source_ref,
			resolved_commit_sha,
			root_path,
			name,
			description,
			tags_json,
			status,
			created_at
		from templates
		where tenant_id = $1
			and id = $2
	`, tenantID, templateID).Scan(
		&template.ID,
		&template.TenantID,
		&template.RepoOwner,
		&template.RepoName,
		&template.SourceRef,
		&template.ResolvedCommitSHA,
		&template.RootPath,
		&template.Name,
		&template.Description,
		&tagsJSON,
		&template.Status,
		&template.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return traits.Template{}, app.ErrNotFound
	}
	if err != nil {
		return traits.Template{}, fmt.Errorf("get template: %w", err)
	}
	if err := json.Unmarshal(tagsJSON, &template.Tags); err != nil {
		return traits.Template{}, fmt.Errorf("unmarshal template tags: %w", err)
	}
	return template, nil
}
```

Add helper functions near existing helpers:

```go
type stackTemplateScanner interface {
	Scan(dest ...any) error
}

func scanStackTemplate(scanner stackTemplateScanner) (traits.StackTemplate, error) {
	var stackTemplate traits.StackTemplate
	var configJSON []byte
	var lastAppliedAt sql.NullTime

	if err := scanner.Scan(
		&stackTemplate.ID,
		&stackTemplate.TenantID,
		&stackTemplate.StackID,
		&stackTemplate.TemplateID,
		&stackTemplate.SelectedRef,
		&stackTemplate.WorkspaceName,
		&configJSON,
		&stackTemplate.LastAppliedRunID,
		&stackTemplate.LastAppliedRef,
		&lastAppliedAt,
		&stackTemplate.Lifecycle,
	); err != nil {
		return traits.StackTemplate{}, fmt.Errorf("scan stack template: %w", err)
	}
	stackTemplate.ConfigJSON = configJSON
	if lastAppliedAt.Valid {
		stackTemplate.LastAppliedAt = lastAppliedAt.Time
	}
	return stackTemplate, nil
}

func (store *Store) getStack(ctx context.Context, tenantID traits.TenantID, stackID traits.StackID) (traits.Stack, error) {
	var stack traits.Stack
	var tagsJSON []byte
	var credentialIDsJSON []byte

	err := store.pool.QueryRow(ctx, `
		select
			id,
			tenant_id,
			name,
			slug,
			tags_json,
			default_credential_ids_json,
			created_by,
			created_at
		from stacks
		where tenant_id = $1
			and id = $2
	`, tenantID, stackID).Scan(
		&stack.ID,
		&stack.TenantID,
		&stack.Name,
		&stack.Slug,
		&tagsJSON,
		&credentialIDsJSON,
		&stack.CreatedBy,
		&stack.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return traits.Stack{}, app.ErrNotFound
	}
	if err != nil {
		return traits.Stack{}, fmt.Errorf("get stack: %w", err)
	}
	if err := json.Unmarshal(tagsJSON, &stack.Tags); err != nil {
		return traits.Stack{}, fmt.Errorf("unmarshal stack tags: %w", err)
	}
	if err := json.Unmarshal(credentialIDsJSON, &stack.DefaultCredentialIDs); err != nil {
		return traits.Stack{}, fmt.Errorf("unmarshal stack credential IDs: %w", err)
	}
	return stack, nil
}

func duplicateConstraint(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == constraint
}
```

Update the existing `GetStackTemplate` query to select `tenant_id`, and replace the manual scan body with `scanStackTemplate`.

- [ ] **Step 5: Run Postgres tests**

Run:

```bash
go test ./internal/postgres -count=1
```

Expected without `MEGAGEGA_POSTGRES_TEST_DSN`: PASS with integration tests skipped. Expected with DSN set: PASS.

- [ ] **Step 6: Commit Postgres stack persistence**

Run:

```bash
git add internal/postgres/migrations/0004_stacks.sql internal/postgres/repositories.go internal/postgres/store_test.go
git commit -m "feat: persist stacks and stack templates"
```

---

### Task 4: Add Stack HTTP API Routes

**Files:**
- Modify: `internal/api/server.go`
- Modify: `internal/api/server_test.go`

- [ ] **Step 1: Write failing API tests**

In `internal/api/server_test.go`, add tests near the existing route tests:

```go
func TestCreateStackCallsService(t *testing.T) {
	t.Parallel()

	createdAt := time.Date(2026, 7, 6, 13, 30, 0, 0, time.UTC)
	deps := newAPITestDependencies()
	deps.stackID = traits.StackID("stack_123")
	deps.now = createdAt
	server := NewServer(deps.service())
	response := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/tenants/tenant_123/stacks",
		strings.NewReader(`{"name":"Acme Prod","tags":{"env":"prod"},"default_credential_ids":["credential_123"],"actor":"user_123"}`),
	)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusCreated, response.Body.String())
	}
	if deps.stacks.created.TenantID != traits.TenantID("tenant_123") {
		t.Fatalf("tenant id = %q, want tenant_123", deps.stacks.created.TenantID)
	}
	if deps.stacks.created.Slug != "acme-prod" {
		t.Fatalf("slug = %q, want acme-prod", deps.stacks.created.Slug)
	}

	var body stackResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.ID != "stack_123" {
		t.Fatalf("response id = %q, want stack_123", body.ID)
	}
	if body.Tags["env"] != "prod" {
		t.Fatalf("response tags = %#v", body.Tags)
	}
}

func TestGetStackReturnsStackView(t *testing.T) {
	t.Parallel()

	deps := newAPITestDependencies()
	deps.stacks.view = app.StackView{
		Stack: traits.Stack{
			ID:       traits.StackID("stack_123"),
			TenantID: traits.TenantID("tenant_123"),
			Name:     "Acme Prod",
			Slug:     "acme-prod",
			Tags:     map[string]string{"env": "prod"},
		},
		Templates: []traits.StackTemplate{
			{
				ID:            traits.StackTemplateID("stack_template_123"),
				TenantID:      traits.TenantID("tenant_123"),
				StackID:       traits.StackID("stack_123"),
				TemplateID:    traits.TemplateID("template_123"),
				SelectedRef:   "main",
				WorkspaceName: "meg_acme_prod_late_123",
				ConfigJSON:    json.RawMessage(`{"region":"us-east-1"}`),
				Lifecycle:     traits.StackTemplateActive,
			},
		},
	}
	server := NewServer(deps.service())
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v1/tenants/tenant_123/stacks/stack_123", nil)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusOK, response.Body.String())
	}
	if deps.stacks.gotStackID != traits.StackID("stack_123") {
		t.Fatalf("stack lookup = %q, want stack_123", deps.stacks.gotStackID)
	}

	var body stackViewResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Stack.ID != "stack_123" {
		t.Fatalf("stack id = %q, want stack_123", body.Stack.ID)
	}
	if len(body.Templates) != 1 {
		t.Fatalf("len(templates) = %d, want 1", len(body.Templates))
	}
	if body.Templates[0].Config["region"] != "us-east-1" {
		t.Fatalf("template config = %#v", body.Templates[0].Config)
	}
}

func TestAddTemplateToStackCallsService(t *testing.T) {
	t.Parallel()

	deps := newAPITestDependencies()
	deps.stacks.stack = traits.Stack{ID: traits.StackID("stack_123"), TenantID: traits.TenantID("tenant_123"), Slug: "acme-prod"}
	deps.templates.template = traits.Template{ID: traits.TemplateID("template_123"), TenantID: traits.TenantID("tenant_123"), Status: traits.TemplateActive}
	deps.templates.variables = []traits.TemplateVariable{{Name: "region", Required: true}}
	deps.stackTemplateID = traits.StackTemplateID("stack_template_a1b2c3d4")
	server := NewServer(deps.service())
	response := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/tenants/tenant_123/stacks/stack_123/templates",
		strings.NewReader(`{"template_id":"template_123","selected_ref":"main","config":{"region":"us-east-1"},"actor":"user_123"}`),
	)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusCreated, response.Body.String())
	}
	if deps.stackTemplateInstaller.created.StackID != traits.StackID("stack_123") {
		t.Fatalf("stack id = %q, want stack_123", deps.stackTemplateInstaller.created.StackID)
	}

	var body stackTemplateResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.ID != "stack_template_a1b2c3d4" {
		t.Fatalf("response id = %q, want stack_template_a1b2c3d4", body.ID)
	}
	if body.Config["region"] != "us-east-1" {
		t.Fatalf("response config = %#v", body.Config)
	}
}
```

Extend `apiTestDependencies`:

```go
stacks                 recordingStackRepository
stackTemplateInstaller recordingStackTemplateInstaller
stackID                traits.StackID
stackTemplateID        traits.StackTemplateID
```

Set defaults in `newAPITestDependencies`:

```go
stackID:         traits.StackID("stack_123"),
stackTemplateID: traits.StackTemplateID("stack_template_123"),
```

Pass these fields into `app.NewService`:

```go
Stacks:                 &deps.stacks,
TemplateMetadata:       &deps.templates,
StackTemplateInstaller: &deps.stackTemplateInstaller,
StackIDs:               fixedStackIDGenerator{id: deps.stackID},
StackTemplateIDs:       fixedStackTemplateIDGenerator{id: deps.stackTemplateID},
```

Copy the app test fakes for stack repository, stack-template installer, and fixed ID generators into `server_test.go`.

- [ ] **Step 2: Run failing API tests**

Run:

```bash
go test ./internal/api -run 'TestCreateStack|TestGetStack|TestAddTemplateToStack' -count=1
```

Expected: build fails because routes and response types are missing.

- [ ] **Step 3: Add stack routes and handlers**

In `internal/api/server.go`, add routes after template registration routes:

```go
// Stack routes.
// Creates a logical infrastructure stack.
server.mux.HandleFunc("POST /v1/tenants/{tenant_id}/stacks", server.handleCreateStack)
// Reads one stack with installed templates.
server.mux.HandleFunc("GET /v1/tenants/{tenant_id}/stacks/{stack_id}", server.handleGetStack)
// Installs a registered template into a stack.
server.mux.HandleFunc("POST /v1/tenants/{tenant_id}/stacks/{stack_id}/templates", server.handleAddTemplateToStack)
```

Add handlers:

```go
func (server *Server) handleCreateStack(response http.ResponseWriter, request *http.Request) {
	var body createStackRequest
	if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return
	}

	credentialIDs := make([]traits.CredentialSetID, 0, len(body.DefaultCredentialIDs))
	for _, id := range body.DefaultCredentialIDs {
		credentialIDs = append(credentialIDs, traits.CredentialSetID(id))
	}

	stack, err := server.service.CreateStack(request.Context(), app.CreateStackCommand{
		TenantID:             traits.TenantID(request.PathValue("tenant_id")),
		Name:                 body.Name,
		Slug:                 body.Slug,
		Tags:                 body.Tags,
		DefaultCredentialIDs: credentialIDs,
		Actor:                traits.UserID(body.Actor),
	})
	if err != nil {
		writeAppError(response, err)
		return
	}

	writeJSON(response, http.StatusCreated, newStackResponse(stack))
}

func (server *Server) handleGetStack(response http.ResponseWriter, request *http.Request) {
	view, err := server.service.GetStack(request.Context(), app.GetStackCommand{
		TenantID: traits.TenantID(request.PathValue("tenant_id")),
		StackID:  traits.StackID(request.PathValue("stack_id")),
	})
	if err != nil {
		writeAppError(response, err)
		return
	}

	writeJSON(response, http.StatusOK, newStackViewResponse(view))
}

func (server *Server) handleAddTemplateToStack(response http.ResponseWriter, request *http.Request) {
	var body addTemplateToStackRequest
	if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return
	}

	config := body.Config
	if config == nil {
		config = map[string]any{}
	}
	configJSON, err := json.Marshal(config)
	if err != nil {
		writeError(response, http.StatusBadRequest, "invalid_request", "config must be a JSON object")
		return
	}

	stackTemplate, err := server.service.AddTemplateToStack(request.Context(), app.AddTemplateToStackCommand{
		TenantID:    traits.TenantID(request.PathValue("tenant_id")),
		StackID:     traits.StackID(request.PathValue("stack_id")),
		TemplateID:  traits.TemplateID(body.TemplateID),
		SelectedRef: body.SelectedRef,
		ConfigJSON:  configJSON,
		Actor:       traits.UserID(body.Actor),
	})
	if err != nil {
		writeAppError(response, err)
		return
	}

	writeJSON(response, http.StatusCreated, newStackTemplateResponse(stackTemplate))
}
```

- [ ] **Step 4: Add request and response types**

In `internal/api/server.go`, add:

```go
type createStackRequest struct {
	Name                 string            `json:"name"`
	Slug                 string            `json:"slug"`
	Tags                 map[string]string `json:"tags"`
	DefaultCredentialIDs []string          `json:"default_credential_ids"`
	Actor                string            `json:"actor"`
}

type addTemplateToStackRequest struct {
	TemplateID  string         `json:"template_id"`
	SelectedRef string         `json:"selected_ref"`
	Config      map[string]any `json:"config"`
	Actor       string         `json:"actor"`
}

type stackViewResponse struct {
	Stack     stackResponse           `json:"stack"`
	Templates []stackTemplateResponse `json:"templates"`
}

type stackResponse struct {
	ID                   string            `json:"id"`
	TenantID             string            `json:"tenant_id"`
	Name                 string            `json:"name"`
	Slug                 string            `json:"slug"`
	Tags                 map[string]string `json:"tags"`
	DefaultCredentialIDs []string          `json:"default_credential_ids"`
	CreatedBy            string            `json:"created_by"`
	CreatedAt            string            `json:"created_at"`
}

type stackTemplateResponse struct {
	ID               string         `json:"id"`
	StackID          string         `json:"stack_id"`
	TemplateID       string         `json:"template_id"`
	SelectedRef      string         `json:"selected_ref"`
	WorkspaceName    string         `json:"workspace_name"`
	Config           map[string]any `json:"config"`
	LastAppliedRunID string         `json:"last_applied_run_id"`
	LastAppliedRef   string         `json:"last_applied_ref"`
	LastAppliedAt    string         `json:"last_applied_at,omitempty"`
	Lifecycle        string         `json:"lifecycle"`
}

func newStackViewResponse(view app.StackView) stackViewResponse {
	templates := make([]stackTemplateResponse, 0, len(view.Templates))
	for _, stackTemplate := range view.Templates {
		templates = append(templates, newStackTemplateResponse(stackTemplate))
	}
	return stackViewResponse{
		Stack:     newStackResponse(view.Stack),
		Templates: templates,
	}
}

func newStackResponse(stack traits.Stack) stackResponse {
	credentialIDs := make([]string, 0, len(stack.DefaultCredentialIDs))
	for _, id := range stack.DefaultCredentialIDs {
		credentialIDs = append(credentialIDs, string(id))
	}
	return stackResponse{
		ID:                   string(stack.ID),
		TenantID:             string(stack.TenantID),
		Name:                 stack.Name,
		Slug:                 stack.Slug,
		Tags:                 stack.Tags,
		DefaultCredentialIDs: credentialIDs,
		CreatedBy:            string(stack.CreatedBy),
		CreatedAt:            stack.CreatedAt.Format(time.RFC3339Nano),
	}
}

func newStackTemplateResponse(stackTemplate traits.StackTemplate) stackTemplateResponse {
	var config map[string]any
	if len(stackTemplate.ConfigJSON) > 0 {
		_ = json.Unmarshal(stackTemplate.ConfigJSON, &config)
	}
	if config == nil {
		config = map[string]any{}
	}

	response := stackTemplateResponse{
		ID:               string(stackTemplate.ID),
		StackID:          string(stackTemplate.StackID),
		TemplateID:       string(stackTemplate.TemplateID),
		SelectedRef:      stackTemplate.SelectedRef,
		WorkspaceName:    stackTemplate.WorkspaceName,
		Config:           config,
		LastAppliedRunID: string(stackTemplate.LastAppliedRunID),
		LastAppliedRef:   stackTemplate.LastAppliedRef,
		Lifecycle:        string(stackTemplate.Lifecycle),
	}
	if !stackTemplate.LastAppliedAt.IsZero() {
		response.LastAppliedAt = stackTemplate.LastAppliedAt.Format(time.RFC3339Nano)
	}
	return response
}
```

Add `time` to imports.

Update `writeAppError` conflict mapping:

```go
case errors.Is(err, app.ErrStackTemplateNotRunnable),
	errors.Is(err, app.ErrRunNotApprovable),
	errors.Is(err, app.ErrRunNotCancelable),
	errors.Is(err, app.ErrDuplicateStackSlug),
	errors.Is(err, app.ErrTemplateNotInstallable),
	errors.Is(err, app.ErrStackTemplateConfigInvalid):
	writeError(response, http.StatusConflict, "conflict", err.Error())
```

- [ ] **Step 5: Run API tests**

Run:

```bash
go test ./internal/api -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit stack API routes**

Run:

```bash
git add internal/api/server.go internal/api/server_test.go
git commit -m "feat: add stack api routes"
```

---

### Task 5: Wire Stack Dependencies In The API Command

**Files:**
- Modify: `cmd/megagega-api/main.go`
- Modify: `cmd/megagega-api/main_test.go`

- [ ] **Step 1: Write failing wiring assertions**

In `cmd/megagega-api/main_test.go`, add assertions to `TestRunWiresTemporalDispatcher`:

```go
if deps.service.Stacks != deps.store {
	t.Fatal("service Stacks is not the store")
}
if deps.service.StackTemplateInstaller != deps.store {
	t.Fatal("service StackTemplateInstaller is not the store")
}
if deps.service.TemplateMetadata != deps.store {
	t.Fatal("service TemplateMetadata is not the store")
}
```

Extend the `recordingStore` type at the bottom of the file with methods matching `app.StackRepository`, `app.StackTemplateInstaller`, and `app.TemplateMetadataRepository`:

```go
func (recordingStore) CreateStack(context.Context, traits.Stack) error {
	return nil
}

func (recordingStore) GetStack(context.Context, traits.TenantID, traits.StackID) (traits.Stack, error) {
	return traits.Stack{}, nil
}

func (recordingStore) GetStackWithTemplates(context.Context, traits.TenantID, traits.StackID) (app.StackView, error) {
	return app.StackView{}, nil
}

func (recordingStore) CreateStackTemplate(context.Context, traits.StackTemplate) error {
	return nil
}

func (recordingStore) GetTemplate(context.Context, traits.TenantID, traits.TemplateID) (traits.Template, error) {
	return traits.Template{}, nil
}
```

- [ ] **Step 2: Run the failing command test**

Run:

```bash
go test ./cmd/megagega-api -run TestRunWiresTemporalDispatcher -count=1
```

Expected: FAIL because `appRepositories` and service wiring do not include stack dependencies and template metadata reads.

- [ ] **Step 3: Wire the dependencies**

In `cmd/megagega-api/main.go`, extend `appRepositories`:

```go
type appRepositories interface {
	app.StackRepository
	app.StackTemplateRepository
	app.StackTemplateInstaller
	app.TemplateRunRepository
	app.TemplateRegistrationRepository
	app.TemplateMetadataRepository
	app.TemplateRepository
	app.TemplateRunLogRepository
}
```

In `runWithDependencies`, pass the store into the new service fields:

```go
service, err := deps.newService(app.Service{
	Stacks:                 store,
	StackTemplates:         store,
	StackTemplateInstaller: store,
	TemplateRuns:           store,
	TemplateRegistrations:  store,
	TemplateMetadata:       store,
	Templates:              store,
	TemplateRunLogs:        logReader,
	TemplateRunLogMetadata: store,
	Workflows:              dispatcher,
})
```

- [ ] **Step 4: Run command tests**

Run:

```bash
go test ./cmd/megagega-api -count=1
```

Expected: PASS.

- [ ] **Step 5: Run all Go tests**

Run:

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 6: Commit API wiring**

Run:

```bash
git add cmd/megagega-api/main.go cmd/megagega-api/main_test.go
git commit -m "feat: wire stack api dependencies"
```

---

### Task 6: Scaffold The Vite React Frontend

**Files:**
- Modify: `.gitignore`
- Create: `web/package.json`
- Create: `web/index.html`
- Create: `web/tsconfig.json`
- Create: `web/tsconfig.node.json`
- Create: `web/vite.config.ts`
- Create: `web/src/main.tsx`
- Create: `web/src/App.tsx`
- Create: `web/src/styles.css`

- [ ] **Step 1: Add frontend ignores**

Append to `.gitignore`:

```gitignore
web/node_modules/
web/dist/
```

- [ ] **Step 2: Create package metadata**

Create `web/package.json`:

```json
{
  "name": "megagega-web",
  "private": true,
  "version": "0.0.0",
  "type": "module",
  "scripts": {
    "dev": "vite --host 127.0.0.1",
    "build": "tsc -b && vite build",
    "test": "vitest run"
  },
  "dependencies": {
    "lucide-react": "^0.468.0",
    "react": "^18.3.1",
    "react-dom": "^18.3.1"
  },
  "devDependencies": {
    "@types/react": "^18.3.12",
    "@types/react-dom": "^18.3.1",
    "@vitejs/plugin-react": "^4.3.4",
    "typescript": "^5.6.3",
    "vite": "^5.4.11",
    "vitest": "^2.1.5"
  }
}
```

- [ ] **Step 3: Create Vite and TypeScript config**

Create `web/tsconfig.json`:

```json
{
  "compilerOptions": {
    "target": "ES2020",
    "useDefineForClassFields": true,
    "lib": ["DOM", "DOM.Iterable", "ES2020"],
    "allowJs": false,
    "skipLibCheck": true,
    "esModuleInterop": true,
    "allowSyntheticDefaultImports": true,
    "strict": true,
    "forceConsistentCasingInFileNames": true,
    "module": "ESNext",
    "moduleResolution": "Node",
    "resolveJsonModule": true,
    "isolatedModules": true,
    "noEmit": true,
    "jsx": "react-jsx"
  },
  "include": ["src"],
  "references": [{ "path": "./tsconfig.node.json" }]
}
```

Create `web/tsconfig.node.json`:

```json
{
  "compilerOptions": {
    "composite": true,
    "skipLibCheck": true,
    "module": "ESNext",
    "moduleResolution": "Node",
    "allowSyntheticDefaultImports": true
  },
  "include": ["vite.config.ts"]
}
```

Create `web/vite.config.ts`:

```ts
import react from "@vitejs/plugin-react";
import { defineConfig } from "vitest/config";

export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      "/healthz": "http://localhost:8081",
      "/v1": "http://localhost:8081"
    }
  },
  test: {
    environment: "node"
  }
});
```

- [ ] **Step 4: Create the app entrypoint**

Create `web/index.html`:

```html
<!doctype html>
<html lang="en">
  <head>
    <meta charset="UTF-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <title>Megagega</title>
  </head>
  <body>
    <div id="root"></div>
    <script type="module" src="/src/main.tsx"></script>
  </body>
</html>
```

Create `web/src/main.tsx`:

```tsx
import React from "react";
import ReactDOM from "react-dom/client";
import App from "./App";
import "./styles.css";

ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>
);
```

Create `web/src/App.tsx`:

```tsx
export default function App() {
  return (
    <main className="app-shell">
      <section className="workspace">
        <header className="workspace-header">
          <div>
            <p className="eyebrow">Megagega</p>
            <h1>Terraform workflow console</h1>
          </div>
          <div className="runtime-fields">
            <label>
              Tenant
              <input defaultValue="tenant_123" aria-label="Tenant ID" />
            </label>
            <label>
              Actor
              <input defaultValue="user_123" aria-label="Actor ID" />
            </label>
          </div>
        </header>
        <div className="empty-panel">
          Frontend scaffold ready. Workflow components arrive in the next tasks.
        </div>
      </section>
    </main>
  );
}
```

Create `web/src/styles.css`:

```css
:root {
  color: #18202f;
  background: #eef2f7;
  font-family:
    Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI",
    sans-serif;
  font-synthesis: none;
  text-rendering: optimizeLegibility;
}

* {
  box-sizing: border-box;
}

body {
  margin: 0;
  min-width: 320px;
  min-height: 100vh;
}

button,
input,
textarea,
select {
  font: inherit;
}

.app-shell {
  min-height: 100vh;
  padding: 24px;
}

.workspace {
  width: min(1280px, 100%);
  margin: 0 auto;
}

.workspace-header {
  display: flex;
  align-items: flex-end;
  justify-content: space-between;
  gap: 24px;
  margin-bottom: 20px;
}

.eyebrow {
  margin: 0 0 4px;
  color: #5b6475;
  font-size: 0.78rem;
  font-weight: 700;
  text-transform: uppercase;
}

h1 {
  margin: 0;
  font-size: clamp(1.7rem, 3vw, 2.4rem);
  letter-spacing: 0;
}

.runtime-fields {
  display: grid;
  grid-template-columns: repeat(2, minmax(140px, 1fr));
  gap: 12px;
}

label {
  display: grid;
  gap: 6px;
  color: #485267;
  font-size: 0.85rem;
  font-weight: 650;
}

input,
textarea,
select {
  min-height: 38px;
  border: 1px solid #c9d1df;
  border-radius: 6px;
  padding: 8px 10px;
  color: #18202f;
  background: #ffffff;
}

.empty-panel {
  border: 1px solid #cfd7e4;
  border-radius: 8px;
  padding: 24px;
  background: #ffffff;
  color: #465268;
}

@media (max-width: 760px) {
  .app-shell {
    padding: 16px;
  }

  .workspace-header {
    align-items: stretch;
    flex-direction: column;
  }

  .runtime-fields {
    grid-template-columns: 1fr;
  }
}
```

- [ ] **Step 5: Install frontend dependencies**

Run:

```bash
cd web && npm install
```

Expected: creates `web/package-lock.json` and installs dependencies.

- [ ] **Step 6: Build frontend scaffold**

Run:

```bash
cd web && npm run build
```

Expected: PASS and Vite writes `web/dist`.

- [ ] **Step 7: Commit frontend scaffold**

Run:

```bash
git add .gitignore web/package.json web/package-lock.json web/index.html web/tsconfig.json web/tsconfig.node.json web/vite.config.ts web/src/main.tsx web/src/App.tsx web/src/styles.css
git commit -m "feat: scaffold react web app"
```

---

### Task 7: Add Frontend API Client And Polling Helpers

**Files:**
- Create: `web/src/api/types.ts`
- Create: `web/src/api/client.ts`
- Create: `web/src/api/client.test.ts`
- Create: `web/src/polling.ts`
- Create: `web/src/polling.test.ts`

- [ ] **Step 1: Add browser-facing API types**

Create `web/src/api/types.ts`:

```ts
export type TemplateRegistrationStatus =
  | "pending"
  | "running"
  | "completed"
  | "invalid"
  | "failed";

export type TemplateRunStatus =
  | "queued"
  | "locked"
  | "workspace_prepared"
  | "source_fetched"
  | "init"
  | "workspace_selected"
  | "planned"
  | "waiting_approval"
  | "approved"
  | "apply_started"
  | "applied"
  | "destroy_started"
  | "destroyed"
  | "cancel_requested"
  | "canceling"
  | "canceled"
  | "lock_released"
  | "completed"
  | "failed";

export type Operation = "plan" | "apply" | "destroy";

export interface ApiErrorBody {
  error: string;
  message: string;
}

export interface TemplateRegistration {
  id: string;
  tenant_id: string;
  repo_owner: string;
  repo_name: string;
  source_ref: string;
  root_path: string;
  status: TemplateRegistrationStatus;
  template_id: string;
  resolved_commit_sha: string;
  requested_by: string;
  requested_at: string;
  completed_at?: string;
  error_summary: string;
}

export interface TemplateVariable {
  template_id: string;
  name: string;
  type_expression: string;
  description: string;
  required: boolean;
  has_default: boolean;
  sensitive: boolean;
  has_validation: boolean;
}

export interface Stack {
  id: string;
  tenant_id: string;
  name: string;
  slug: string;
  tags: Record<string, string>;
  default_credential_ids: string[];
  created_by: string;
  created_at: string;
}

export interface StackTemplate {
  id: string;
  stack_id: string;
  template_id: string;
  selected_ref: string;
  workspace_name: string;
  config: Record<string, unknown>;
  last_applied_run_id: string;
  last_applied_ref: string;
  last_applied_at?: string;
  lifecycle: string;
}

export interface StackView {
  stack: Stack;
  templates: StackTemplate[];
}

export interface TemplateRun {
  id: string;
  tenant_id: string;
  stack_template_id: string;
  operation: Operation;
  selected_ref: string;
  resolved_commit_sha: string;
  workspace_name: string;
  backend_type: string;
  backend_config_hash: string;
  status: TemplateRunStatus;
  trigger_actor: string;
  started_at: string;
  completed_at?: string;
  error_summary: string;
}

export interface TemplateRunLog {
  tenant_id: string;
  run_id: string;
  phase: string;
  object_key: string;
  content_type: string;
  size_bytes: number;
  uploaded_at: string;
}
```

- [ ] **Step 2: Write failing API client tests**

Create `web/src/api/client.test.ts`:

```ts
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  addTemplateToStack,
  ApiRequestError,
  approveRun,
  cancelRun,
  createStack,
  getTemplateRunLog,
  registerTemplate,
  startTemplateRun
} from "./client";

describe("api client", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("posts template registration to the tenant route", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(jsonResponse({ id: "template_registration_123" }));

    await registerTemplate("tenant_123", {
      repo_owner: "acme",
      repo_name: "infra",
      source_ref: "main",
      root_path: ".",
      requested_by: "user_123"
    });

    expect(fetchMock).toHaveBeenCalledWith(
      "/v1/tenants/tenant_123/templates",
      expect.objectContaining({
        method: "POST",
        body: JSON.stringify({
          repo_owner: "acme",
          repo_name: "infra",
          source_ref: "main",
          root_path: ".",
          requested_by: "user_123"
        })
      })
    );
  });

  it("posts stack creation with actor and tags", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(jsonResponse({ id: "stack_123" }));

    await createStack("tenant_123", {
      name: "Acme Prod",
      slug: "",
      tags: { env: "prod" },
      default_credential_ids: [],
      actor: "user_123"
    });

    expect(fetchMock).toHaveBeenCalledWith(
      "/v1/tenants/tenant_123/stacks",
      expect.objectContaining({ method: "POST" })
    );
  });

  it("posts template install config", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(jsonResponse({ id: "stack_template_123" }));

    await addTemplateToStack("tenant_123", "stack_123", {
      template_id: "template_123",
      selected_ref: "main",
      config: { region: "us-east-1" },
      actor: "user_123"
    });

    expect(fetchMock).toHaveBeenCalledWith(
      "/v1/tenants/tenant_123/stacks/stack_123/templates",
      expect.objectContaining({ method: "POST" })
    );
  });

  it("posts run operations, approvals, and cancellations", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(jsonResponse({ id: "run_123" }));

  await startTemplateRun("tenant_123", "stack_template_123", { operation: "apply", trigger_actor: "user_123" });
  await approveRun("tenant_123", "run_123", { approved_by: "user_123" });
  await cancelRun("tenant_123", "run_456", { requested_by: "user_123", reason: "manual stop" });

    expect(fetchMock).toHaveBeenNthCalledWith(
      1,
      "/v1/tenants/tenant_123/stack-templates/stack_template_123/runs",
      expect.objectContaining({ method: "POST" })
    );
    expect(fetchMock).toHaveBeenNthCalledWith(
      2,
      "/v1/tenants/tenant_123/template-runs/run_123/approval",
      expect.objectContaining({ method: "POST" })
    );
    expect(fetchMock).toHaveBeenNthCalledWith(
      3,
      "/v1/tenants/tenant_123/template-runs/run_456/cancellation",
      expect.objectContaining({ method: "POST" })
    );
  });

  it("reads log bodies as text", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(textResponse("plan output\n"));

    const body = await getTemplateRunLog("tenant_123", "run_123", "plan");

    expect(body).toBe("plan output\n");
    expect(fetchMock).toHaveBeenCalledWith("/v1/tenants/tenant_123/template-runs/run_123/logs/plan", expect.objectContaining({ method: "GET" }));
  });

  it("throws ApiRequestError with backend message", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(jsonResponse({ error: "invalid_request", message: "bad input" }, 400));

    await expect(createStack("tenant_123", {
      name: "",
      slug: "",
      tags: {},
      default_credential_ids: [],
      actor: "user_123"
  })).rejects.toMatchObject({
    status: 400,
    code: "invalid_request",
    message: "bad input"
  });
  });
});

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "content-type": "application/json" }
  });
}

function textResponse(body: string, status = 200): Response {
  return new Response(body, {
    status,
    headers: { "content-type": "text/plain; charset=utf-8" }
  });
}
```

- [ ] **Step 3: Implement API client**

Create `web/src/api/client.ts`:

```ts
import type {
  ApiErrorBody,
  Operation,
  Stack,
  StackTemplate,
  StackView,
  TemplateRegistration,
  TemplateRun,
  TemplateRunLog,
  TemplateVariable
} from "./types";

export class ApiRequestError extends Error {
  constructor(
    public readonly status: number,
    public readonly code: string,
    message: string
  ) {
    super(message);
    this.name = "ApiRequestError";
  }
}

interface RegisterTemplateRequest {
  repo_owner: string;
  repo_name: string;
  source_ref: string;
  root_path: string;
  requested_by: string;
}

interface CreateStackRequest {
  name: string;
  slug: string;
  tags: Record<string, string>;
  default_credential_ids: string[];
  actor: string;
}

interface AddTemplateToStackRequest {
  template_id: string;
  selected_ref: string;
  config: Record<string, unknown>;
  actor: string;
}

interface StartRunRequest {
  operation: Operation;
  trigger_actor: string;
}

interface ApproveRunRequest {
  approved_by: string;
}

interface CancelRunRequest {
  requested_by: string;
  reason: string;
}

export function registerTemplate(tenantID: string, body: RegisterTemplateRequest): Promise<TemplateRegistration> {
  return requestJSON(`/v1/tenants/${encodeURIComponent(tenantID)}/templates`, {
    method: "POST",
    body: JSON.stringify(body)
  });
}

export function getTemplateRegistration(tenantID: string, registrationID: string): Promise<TemplateRegistration> {
  return requestJSON(`/v1/tenants/${encodeURIComponent(tenantID)}/template-registrations/${encodeURIComponent(registrationID)}`);
}

export function getTemplateVariables(tenantID: string, templateID: string): Promise<TemplateVariable[]> {
  return requestJSON(`/v1/tenants/${encodeURIComponent(tenantID)}/templates/${encodeURIComponent(templateID)}/variables`);
}

export function createStack(tenantID: string, body: CreateStackRequest): Promise<Stack> {
  return requestJSON(`/v1/tenants/${encodeURIComponent(tenantID)}/stacks`, {
    method: "POST",
    body: JSON.stringify(body)
  });
}

export function getStack(tenantID: string, stackID: string): Promise<StackView> {
  return requestJSON(`/v1/tenants/${encodeURIComponent(tenantID)}/stacks/${encodeURIComponent(stackID)}`);
}

export function addTemplateToStack(tenantID: string, stackID: string, body: AddTemplateToStackRequest): Promise<StackTemplate> {
  return requestJSON(`/v1/tenants/${encodeURIComponent(tenantID)}/stacks/${encodeURIComponent(stackID)}/templates`, {
    method: "POST",
    body: JSON.stringify(body)
  });
}

export function startTemplateRun(tenantID: string, stackTemplateID: string, body: StartRunRequest): Promise<TemplateRun> {
  return requestJSON(`/v1/tenants/${encodeURIComponent(tenantID)}/stack-templates/${encodeURIComponent(stackTemplateID)}/runs`, {
    method: "POST",
    body: JSON.stringify(body)
  });
}

export function getTemplateRun(tenantID: string, runID: string): Promise<TemplateRun> {
  return requestJSON(`/v1/tenants/${encodeURIComponent(tenantID)}/template-runs/${encodeURIComponent(runID)}`);
}

export function listTemplateRunLogs(tenantID: string, runID: string): Promise<TemplateRunLog[]> {
  return requestJSON(`/v1/tenants/${encodeURIComponent(tenantID)}/template-runs/${encodeURIComponent(runID)}/logs`);
}

export function getTemplateRunLog(tenantID: string, runID: string, phase: string): Promise<string> {
  return requestText(`/v1/tenants/${encodeURIComponent(tenantID)}/template-runs/${encodeURIComponent(runID)}/logs/${encodeURIComponent(phase)}`);
}

export function approveRun(tenantID: string, runID: string, body: ApproveRunRequest): Promise<void> {
  return requestNoContent(`/v1/tenants/${encodeURIComponent(tenantID)}/template-runs/${encodeURIComponent(runID)}/approval`, {
    method: "POST",
    body: JSON.stringify(body)
  });
}

export function cancelRun(tenantID: string, runID: string, body: CancelRunRequest): Promise<void> {
  return requestNoContent(`/v1/tenants/${encodeURIComponent(tenantID)}/template-runs/${encodeURIComponent(runID)}/cancellation`, {
    method: "POST",
    body: JSON.stringify(body)
  });
}

async function requestJSON<T>(path: string, init: RequestInit = {}): Promise<T> {
  const response = await fetch(path, withJSONHeaders(init));
  await throwForError(response);
  return response.json() as Promise<T>;
}

async function requestText(path: string, init: RequestInit = {}): Promise<string> {
  const response = await fetch(path, withJSONHeaders(init));
  await throwForError(response);
  return response.text();
}

async function requestNoContent(path: string, init: RequestInit = {}): Promise<void> {
  const response = await fetch(path, withJSONHeaders(init));
  await throwForError(response);
}

function withJSONHeaders(init: RequestInit): RequestInit {
  return {
    ...init,
    headers: {
      "content-type": "application/json",
      ...(init.headers ?? {})
    }
  };
}

async function throwForError(response: Response): Promise<void> {
  if (response.ok) {
    return;
  }

  let body: ApiErrorBody = { error: "request_failed", message: response.statusText || "request failed" };
  if (response.headers.get("content-type")?.includes("application/json")) {
    body = (await response.json()) as ApiErrorBody;
  }
  throw new ApiRequestError(response.status, body.error, body.message);
}
```

- [ ] **Step 4: Write and implement polling helper tests**

Create `web/src/polling.test.ts`:

```ts
import { describe, expect, it } from "vitest";
import { isTerminalRegistrationStatus, isTerminalRunStatus, nextPollDelayMs } from "./polling";

describe("polling helpers", () => {
  it("identifies terminal registration states", () => {
    expect(isTerminalRegistrationStatus("completed")).toBe(true);
    expect(isTerminalRegistrationStatus("invalid")).toBe(true);
    expect(isTerminalRegistrationStatus("failed")).toBe(true);
    expect(isTerminalRegistrationStatus("running")).toBe(false);
  });

  it("identifies terminal run states", () => {
    expect(isTerminalRunStatus("completed")).toBe(true);
    expect(isTerminalRunStatus("failed")).toBe(true);
    expect(isTerminalRunStatus("canceled")).toBe(true);
    expect(isTerminalRunStatus("waiting_approval")).toBe(false);
  });

  it("backs off after repeated failures without exceeding five seconds", () => {
    expect(nextPollDelayMs(0)).toBe(1500);
    expect(nextPollDelayMs(1)).toBe(2000);
    expect(nextPollDelayMs(10)).toBe(5000);
  });
});
```

Create `web/src/polling.ts`:

```ts
import type { TemplateRegistrationStatus, TemplateRunStatus } from "./api/types";

export function isTerminalRegistrationStatus(status: TemplateRegistrationStatus): boolean {
  return status === "completed" || status === "invalid" || status === "failed";
}

export function isTerminalRunStatus(status: TemplateRunStatus): boolean {
  return status === "completed" || status === "failed" || status === "canceled";
}

export function nextPollDelayMs(failureCount: number): number {
  return Math.min(1500 + failureCount * 500, 5000);
}
```

- [ ] **Step 5: Run frontend tests**

Run:

```bash
cd web && npm test
```

Expected: PASS.

- [ ] **Step 6: Run frontend build**

Run:

```bash
cd web && npm run build
```

Expected: PASS.

- [ ] **Step 7: Commit frontend API client**

Run:

```bash
git add web/src/api/types.ts web/src/api/client.ts web/src/api/client.test.ts web/src/polling.ts web/src/polling.test.ts
git commit -m "feat: add web api client"
```

---

### Task 8: Build The Workflow UI

**Files:**
- Modify: `web/src/App.tsx`
- Modify: `web/src/styles.css`

- [ ] **Step 1: Replace the scaffold with workflow state and handlers**

In `web/src/App.tsx`, replace the scaffold with a component that imports:

```tsx
import { CheckCircle2, CircleStop, Loader2, Play, RefreshCw, Send, ShieldCheck, SquareTerminal, XCircle } from "lucide-react";
import { FormEvent, useEffect, useState } from "react";
import {
  addTemplateToStack,
  ApiRequestError,
  approveRun,
  cancelRun,
  createStack,
  getTemplateRegistration,
  getTemplateRun,
  getTemplateRunLog,
  getTemplateVariables,
  listTemplateRunLogs,
  registerTemplate,
  startTemplateRun
} from "./api/client";
import type { Stack, StackTemplate, TemplateRegistration, TemplateRun, TemplateRunLog, TemplateVariable } from "./api/types";
import { isTerminalRegistrationStatus, isTerminalRunStatus, nextPollDelayMs } from "./polling";
```

Add local state:

```tsx
const [tenantID, setTenantID] = useState("tenant_123");
const [actor, setActor] = useState("user_123");
const [repoOwner, setRepoOwner] = useState("hashicorp");
const [repoName, setRepoName] = useState("");
const [sourceRef, setSourceRef] = useState("main");
const [rootPath, setRootPath] = useState(".");
const [registration, setRegistration] = useState<TemplateRegistration | null>(null);
const [variables, setVariables] = useState<TemplateVariable[]>([]);
const [variableValues, setVariableValues] = useState<Record<string, string>>({});
const [stackName, setStackName] = useState("Acme Prod");
const [stackSlug, setStackSlug] = useState("");
const [stack, setStack] = useState<Stack | null>(null);
const [installedTemplate, setInstalledTemplate] = useState<StackTemplate | null>(null);
const [planRun, setPlanRun] = useState<TemplateRun | null>(null);
const [applyRun, setApplyRun] = useState<TemplateRun | null>(null);
const [selectedRunKind, setSelectedRunKind] = useState<"plan" | "apply">("plan");
const [logs, setLogs] = useState<TemplateRunLog[]>([]);
const [selectedPhase, setSelectedPhase] = useState("plan");
const [logBody, setLogBody] = useState("");
const [busyAction, setBusyAction] = useState("");
const [errorMessage, setErrorMessage] = useState("");
```

Add helpers:

```tsx
const currentRun = selectedRunKind === "apply" ? applyRun : planRun;
const canInstall = registration?.status === "completed" && registration.template_id && stack;
const canPlan = installedTemplate && !planRun;
const canApply = installedTemplate && planRun?.status === "completed" && !applyRun;
const canApprove = applyRun?.status === "waiting_approval";
const canCancel = currentRun && !isTerminalRunStatus(currentRun.status);
```

- [ ] **Step 2: Add polling effects**

In `web/src/App.tsx`, add effects:

```tsx
useEffect(() => {
  if (!registration || isTerminalRegistrationStatus(registration.status)) {
    return;
  }

  let canceled = false;
  let failureCount = 0;
  let timer: number | undefined;

  const schedule = () => {
    timer = window.setTimeout(poll, nextPollDelayMs(failureCount));
  };

  const poll = async () => {
    if (canceled) {
      return;
    }
    try {
      const next = await getTemplateRegistration(tenantID, registration.id);
      if (!canceled) {
        setRegistration(next);
        failureCount = 0;
        if (!isTerminalRegistrationStatus(next.status)) {
          schedule();
        }
      }
    } catch (error) {
      if (!canceled) {
        failureCount += 1;
        setErrorMessage(messageFromError(error));
        schedule();
      }
    }
  };

  schedule();
  return () => {
    canceled = true;
    if (timer) {
      window.clearTimeout(timer);
    }
  };
}, [registration, tenantID]);

useEffect(() => {
  if (registration?.status !== "completed" || !registration.template_id) {
    return;
  }

  getTemplateVariables(tenantID, registration.template_id)
    .then((nextVariables) => {
      setVariables(nextVariables);
      setVariableValues((current) => {
        const next = { ...current };
        for (const variable of nextVariables) {
          if (!(variable.name in next)) {
            next[variable.name] = "";
          }
        }
        return next;
      });
    })
    .catch((error) => setErrorMessage(messageFromError(error)));
}, [registration?.status, registration?.template_id, tenantID]);

useEffect(() => {
  const run = currentRun;
  if (!run || isTerminalRunStatus(run.status)) {
    return;
  }

  let canceled = false;
  let failureCount = 0;
  let timer: number | undefined;

  const schedule = () => {
    timer = window.setTimeout(poll, nextPollDelayMs(failureCount));
  };

  const poll = async () => {
    if (canceled) {
      return;
    }
    try {
      const next = await getTemplateRun(tenantID, run.id);
      if (!canceled) {
        if (next.operation === "apply") {
          setApplyRun(next);
        } else {
          setPlanRun(next);
        }
        failureCount = 0;
        if (!isTerminalRunStatus(next.status)) {
          schedule();
        }
      }
    } catch (error) {
      if (!canceled) {
        failureCount += 1;
        setErrorMessage(messageFromError(error));
        schedule();
      }
    }
  };

  schedule();
  return () => {
    canceled = true;
    if (timer) {
      window.clearTimeout(timer);
    }
  };
}, [currentRun?.id, currentRun?.status, tenantID]);

useEffect(() => {
  if (!currentRun) {
    setLogs([]);
    setLogBody("");
    return;
  }

  listTemplateRunLogs(tenantID, currentRun.id)
    .then((nextLogs) => {
      setLogs(nextLogs);
      if (nextLogs.length > 0 && !nextLogs.some((log) => log.phase === selectedPhase)) {
        setSelectedPhase(nextLogs[0].phase);
      }
    })
    .catch(() => setLogs([]));
}, [currentRun?.id, currentRun?.status, tenantID, selectedPhase]);

useEffect(() => {
  if (!currentRun || !selectedPhase) {
    setLogBody("");
    return;
  }

  getTemplateRunLog(tenantID, currentRun.id, selectedPhase)
    .then(setLogBody)
    .catch(() => setLogBody(""));
}, [currentRun?.id, currentRun?.status, selectedPhase, tenantID]);
```

- [ ] **Step 3: Add submit handlers**

Add handlers:

```tsx
async function handleRegister(event: FormEvent) {
  event.preventDefault();
  await runAction("register", async () => {
    const next = await registerTemplate(tenantID, {
      repo_owner: repoOwner,
      repo_name: repoName,
      source_ref: sourceRef,
      root_path: rootPath,
      requested_by: actor
    });
    setRegistration(next);
    setVariables([]);
    setStack(null);
    setInstalledTemplate(null);
    setPlanRun(null);
    setApplyRun(null);
  });
}

async function handleCreateStack(event: FormEvent) {
  event.preventDefault();
  await runAction("stack", async () => {
    const next = await createStack(tenantID, {
      name: stackName,
      slug: stackSlug,
      tags: {},
      default_credential_ids: [],
      actor
    });
    setStack(next);
  });
}

async function handleInstallTemplate() {
  if (!stack || !registration?.template_id) {
    return;
  }

  await runAction("install", async () => {
    const config = Object.fromEntries(
      variables
        .map((variable) => [variable.name, variableValues[variable.name] ?? ""])
        .filter(([, value]) => String(value).trim() !== "")
    );
    const next = await addTemplateToStack(tenantID, stack.id, {
      template_id: registration.template_id,
      selected_ref: registration.source_ref,
      config,
      actor
    });
    setInstalledTemplate(next);
  });
}

async function handleStartRun(operation: "plan" | "apply") {
  if (!installedTemplate) {
    return;
  }

  await runAction(operation, async () => {
    const next = await startTemplateRun(tenantID, installedTemplate.id, {
      operation,
      trigger_actor: actor
    });
    if (operation === "apply") {
      setApplyRun(next);
      setSelectedRunKind("apply");
    } else {
      setPlanRun(next);
      setSelectedRunKind("plan");
    }
  });
}

async function handleApproveApply() {
  if (!applyRun) {
    return;
  }

  await runAction("approve", async () => {
    await approveRun(tenantID, applyRun.id, { approved_by: actor });
    const next = await getTemplateRun(tenantID, applyRun.id);
    setApplyRun(next);
  });
}

async function handleCancelRun() {
  if (!currentRun) {
    return;
  }

  await runAction("cancel", async () => {
    await cancelRun(tenantID, currentRun.id, {
      requested_by: actor,
      reason: "canceled from workflow console"
    });
    const next = await getTemplateRun(tenantID, currentRun.id);
    if (next.operation === "apply") {
      setApplyRun(next);
    } else {
      setPlanRun(next);
    }
  });
}

async function runAction(name: string, action: () => Promise<void>) {
  setBusyAction(name);
  setErrorMessage("");
  try {
    await action();
  } catch (error) {
    setErrorMessage(messageFromError(error));
  } finally {
    setBusyAction("");
  }
}

function messageFromError(error: unknown): string {
  if (error instanceof ApiRequestError) {
    return error.message;
  }
  if (error instanceof Error) {
    return error.message;
  }
  return "Request failed";
}
```

- [ ] **Step 4: Render the workflow**

Render:

```tsx
return (
  <main className="app-shell">
    <section className="workspace">
      <header className="workspace-header">
        <div>
          <p className="eyebrow">Megagega</p>
          <h1>Terraform workflow console</h1>
        </div>
        <div className="runtime-fields">
          <label>
            Tenant
            <input value={tenantID} onChange={(event) => setTenantID(event.target.value)} />
          </label>
          <label>
            Actor
            <input value={actor} onChange={(event) => setActor(event.target.value)} />
          </label>
        </div>
      </header>

      {errorMessage && <div className="alert">{errorMessage}</div>}

      <div className="workflow-grid">
        <section className="panel">
          <h2>Template</h2>
          <form className="form-grid" onSubmit={handleRegister}>
            <label>Owner<input value={repoOwner} onChange={(event) => setRepoOwner(event.target.value)} /></label>
            <label>Repository<input value={repoName} onChange={(event) => setRepoName(event.target.value)} /></label>
            <label>Ref<input value={sourceRef} onChange={(event) => setSourceRef(event.target.value)} /></label>
            <label>Root path<input value={rootPath} onChange={(event) => setRootPath(event.target.value)} /></label>
            <button className="primary-button" disabled={busyAction === "register"} type="submit">
              {busyAction === "register" ? <Loader2 size={16} className="spin" /> : <Send size={16} />}
              Register
            </button>
          </form>
          <StatusRow label="Registration" value={registration?.status ?? "not started"} />
          {registration?.error_summary && <p className="error-text">{registration.error_summary}</p>}
        </section>

        <section className="panel">
          <h2>Stack</h2>
          <form className="form-grid" onSubmit={handleCreateStack}>
            <label>Name<input value={stackName} onChange={(event) => setStackName(event.target.value)} /></label>
            <label>Slug<input value={stackSlug} onChange={(event) => setStackSlug(event.target.value)} placeholder="derived from name" /></label>
            <button className="primary-button" disabled={busyAction === "stack"} type="submit">
              {busyAction === "stack" ? <Loader2 size={16} className="spin" /> : <CheckCircle2 size={16} />}
              Create stack
            </button>
          </form>
          <StatusRow label="Stack" value={stack?.slug ?? "not created"} />
        </section>

        <section className="panel wide">
          <h2>Variables</h2>
          {variables.length === 0 ? (
            <p className="muted">Variables appear after registration completes.</p>
          ) : (
            <div className="variable-grid">
              {variables.map((variable) => (
                <label key={variable.name}>
                  {variable.name}{variable.required ? " *" : ""}
                  <input
                    value={variableValues[variable.name] ?? ""}
                    onChange={(event) => setVariableValues((current) => ({ ...current, [variable.name]: event.target.value }))}
                    placeholder={variable.type_expression || "value"}
                  />
                </label>
              ))}
            </div>
          )}
          <button className="secondary-button" disabled={!canInstall || busyAction === "install"} onClick={handleInstallTemplate} type="button">
            {busyAction === "install" ? <Loader2 size={16} className="spin" /> : <ShieldCheck size={16} />}
            Install template
          </button>
          <StatusRow label="Installed template" value={installedTemplate?.workspace_name ?? "not installed"} />
        </section>

        <section className="panel">
          <h2>Runs</h2>
          <div className="button-row">
            <button className="primary-button" disabled={!canPlan || busyAction === "plan"} onClick={() => handleStartRun("plan")} type="button">
              <Play size={16} />
              Plan
            </button>
            <button className="primary-button" disabled={!canApply || busyAction === "apply"} onClick={() => handleStartRun("apply")} type="button">
              <Play size={16} />
              Apply
            </button>
            <button className="secondary-button" disabled={!canApprove || busyAction === "approve"} onClick={handleApproveApply} type="button">
              <ShieldCheck size={16} />
              Approve
            </button>
            <button className="secondary-button" disabled={!canCancel || busyAction === "cancel"} onClick={handleCancelRun} type="button">
              <CircleStop size={16} />
              Cancel
            </button>
          </div>
          <div className="run-tabs">
            <button className={selectedRunKind === "plan" ? "active" : ""} onClick={() => setSelectedRunKind("plan")} type="button">Plan</button>
            <button className={selectedRunKind === "apply" ? "active" : ""} onClick={() => setSelectedRunKind("apply")} type="button">Apply</button>
          </div>
          <StatusRow label="Current run" value={currentRun?.status ?? "not started"} />
          {currentRun?.error_summary && <p className="error-text">{currentRun.error_summary}</p>}
        </section>

        <section className="panel wide log-panel">
          <h2><SquareTerminal size={18} /> Logs</h2>
          <div className="phase-row">
            {logs.map((log) => (
              <button className={selectedPhase === log.phase ? "active" : ""} key={log.phase} onClick={() => setSelectedPhase(log.phase)} type="button">
                {log.phase}
              </button>
            ))}
          </div>
          <pre>{logBody || "No log body available yet."}</pre>
        </section>

        <section className="panel wide">
          <h2>IDs</h2>
          <dl className="id-grid">
            <dt>Registration</dt><dd>{registration?.id ?? "-"}</dd>
            <dt>Template</dt><dd>{registration?.template_id ?? "-"}</dd>
            <dt>Stack</dt><dd>{stack?.id ?? "-"}</dd>
            <dt>Stack template</dt><dd>{installedTemplate?.id ?? "-"}</dd>
            <dt>Plan run</dt><dd>{planRun?.id ?? "-"}</dd>
            <dt>Apply run</dt><dd>{applyRun?.id ?? "-"}</dd>
          </dl>
        </section>
      </div>
    </section>
  </main>
);
```

Add the child component below `App`:

```tsx
function StatusRow({ label, value }: { label: string; value: string }) {
  const icon = value === "failed" || value === "invalid" ? <XCircle size={15} /> : value === "not started" ? <RefreshCw size={15} /> : <CheckCircle2 size={15} />;
  return (
    <div className="status-row">
      <span>{label}</span>
      <strong>{icon}{value}</strong>
    </div>
  );
}
```

- [ ] **Step 5: Replace styles with the workflow layout**

Update `web/src/styles.css` to include these classes while preserving the base reset from Task 6:

```css
.workflow-grid {
  display: grid;
  grid-template-columns: minmax(280px, 0.85fr) minmax(320px, 1.15fr);
  gap: 16px;
}

.panel {
  border: 1px solid #cfd7e4;
  border-radius: 8px;
  padding: 18px;
  background: #ffffff;
  box-shadow: 0 1px 2px rgb(21 31 48 / 0.05);
}

.panel.wide {
  grid-column: 1 / -1;
}

.panel h2 {
  display: flex;
  align-items: center;
  gap: 8px;
  margin: 0 0 14px;
  font-size: 1rem;
}

.form-grid,
.variable-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(160px, 1fr));
  gap: 12px;
}

.primary-button,
.secondary-button {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  min-height: 38px;
  border-radius: 6px;
  padding: 8px 12px;
  cursor: pointer;
}

.primary-button {
  border: 1px solid #2563eb;
  color: #ffffff;
  background: #2563eb;
}

.secondary-button {
  border: 1px solid #b8c3d4;
  color: #243045;
  background: #f8fafc;
}

button:disabled {
  cursor: not-allowed;
  opacity: 0.55;
}

.button-row,
.phase-row,
.run-tabs {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin-bottom: 14px;
}

.run-tabs button,
.phase-row button {
  border: 1px solid #c9d1df;
  border-radius: 999px;
  padding: 6px 10px;
  color: #44506a;
  background: #ffffff;
  cursor: pointer;
}

.run-tabs button.active,
.phase-row button.active {
  border-color: #2563eb;
  color: #1d4ed8;
  background: #eff6ff;
}

.status-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-top: 14px;
  border-top: 1px solid #e5eaf2;
  padding-top: 12px;
  color: #5b6475;
  font-size: 0.88rem;
}

.status-row strong {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  color: #18202f;
  font-weight: 750;
}

.alert,
.error-text {
  color: #991b1b;
}

.alert {
  border: 1px solid #fecaca;
  border-radius: 8px;
  margin-bottom: 16px;
  padding: 12px 14px;
  background: #fef2f2;
}

.muted {
  color: #667085;
}

.log-panel pre {
  min-height: 260px;
  max-height: 460px;
  overflow: auto;
  border-radius: 8px;
  margin: 0;
  padding: 14px;
  color: #d9e2f1;
  background: #111827;
  font-size: 0.84rem;
  line-height: 1.5;
  white-space: pre-wrap;
}

.id-grid {
  display: grid;
  grid-template-columns: max-content minmax(0, 1fr);
  gap: 8px 16px;
  margin: 0;
}

.id-grid dt {
  color: #667085;
}

.id-grid dd {
  margin: 0;
  min-width: 0;
  overflow-wrap: anywhere;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", monospace;
}

.spin {
  animation: spin 0.9s linear infinite;
}

@keyframes spin {
  to {
    transform: rotate(360deg);
  }
}

@media (max-width: 920px) {
  .workflow-grid,
  .form-grid,
  .variable-grid {
    grid-template-columns: 1fr;
  }
}
```

- [ ] **Step 6: Build frontend**

Run:

```bash
cd web && npm run build
```

Expected: PASS.

- [ ] **Step 7: Commit workflow UI**

Run:

```bash
git add web/src/App.tsx web/src/styles.css
git commit -m "feat: add e2e workflow console"
```

---

### Task 9: Add Developer Commands And Final Verification

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Document local UI commands**

Add this section to `README.md` after the architecture overview:

````markdown
## Local UI Development

Run the backend API and the Vite UI as separate processes.

```text
API: http://localhost:8081
UI:  http://localhost:5173
```

Start backend dependencies:

```bash
docker compose up app-postgres temporal-postgres temporal temporal-ui
```

Start the API with the same environment used by local smoke tests:

```bash
DATABASE_URL='postgres://megagega:megagega@localhost:55432/megagega_test?sslmode=disable' \
TEMPORAL_ADDRESS='localhost:7233' \
go run ./cmd/megagega-api
```

Start the worker in another shell:

```bash
DATABASE_URL='postgres://megagega:megagega@localhost:55432/megagega_test?sslmode=disable' \
TEMPORAL_ADDRESS='localhost:7233' \
go run ./cmd/megagega-worker
```

Start the UI:

```bash
cd web
npm install
npm run dev
```

The Vite dev server proxies `/v1/*` and `/healthz` to the Go API.
````

- [ ] **Step 2: Run Go tests**

Run:

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 3: Run frontend tests**

Run:

```bash
cd web && npm test
```

Expected: PASS.

- [ ] **Step 4: Run frontend build**

Run:

```bash
cd web && npm run build
```

Expected: PASS.

- [ ] **Step 5: Check working tree**

Run:

```bash
git status --short
```

Expected: only `README.md` modified after prior task commits.

- [ ] **Step 6: Commit documentation**

Run:

```bash
git add README.md
git commit -m "docs: add local ui development commands"
```

---

### Task 10: Manual E2E Smoke Test

**Files:**
- No source edits unless the smoke test exposes a bug.

- [ ] **Step 1: Start local dependencies**

Run:

```bash
docker compose up app-postgres temporal-postgres temporal temporal-ui
```

Expected: Postgres and Temporal services become healthy. Keep this process running.

- [ ] **Step 2: Start API**

In a second shell, run:

```bash
DATABASE_URL='postgres://megagega:megagega@localhost:55432/megagega_test?sslmode=disable' \
TEMPORAL_ADDRESS='localhost:7233' \
go run ./cmd/megagega-api
```

Expected: logs include `api listening on`.

- [ ] **Step 3: Start worker**

In a third shell, run:

```bash
DATABASE_URL='postgres://megagega:megagega@localhost:55432/megagega_test?sslmode=disable' \
TEMPORAL_ADDRESS='localhost:7233' \
go run ./cmd/megagega-worker
```

Expected: process stays running and polls Temporal.

- [ ] **Step 4: Start UI**

In a fourth shell, run:

```bash
cd web && npm run dev
```

Expected: Vite serves `http://127.0.0.1:5173`.

- [ ] **Step 5: Exercise the browser workflow**

Use a public Terraform template repository that has no sensitive variables. Fill in tenant `tenant_123` and actor `user_123`, register the template, wait for completion, fill required variables, create the stack, install the template, start a plan, inspect logs, start apply, approve apply, and inspect terminal status.

Expected:

- Registration reaches `completed`.
- Variables render after completion.
- Stack creation returns a stack ID.
- Template installation returns a stack-template ID and workspace name.
- Plan run reaches `completed` or `failed` with visible log/status details.
- Apply run reaches `waiting_approval`, approval can be submitted, and polling continues after approval.

- [ ] **Step 6: Record smoke result**

If the smoke test passes, add no code. Check the working tree:

```bash
git status --short
```

Expected: clean working tree. If the smoke test exposes a bug, stop this task, diagnose the bug with `superpowers:systematic-debugging`, write a failing test for the specific bug, and make a focused fix commit after that test passes.
