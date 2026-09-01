package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/vishu42/tflive/internal/authn"
	"github.com/vishu42/tflive/internal/authz"
	"github.com/vishu42/tflive/internal/queue"
	"github.com/vishu42/tflive/internal/traits"
)

const keycloakSubject = "6fdb4b4c-2a8f-4cf7-945f-38f67f6a0e91"

func authenticatedContext() context.Context {
	return authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: keycloakSubject})
}

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

func TestAuthenticatedActorRejectsEmptySubject(t *testing.T) {
	t.Parallel()

	ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{})
	if _, err := authenticatedActor(ctx); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("error = %v, want ErrUnauthenticated", err)
	}
}

func TestCreateStackDerivesSlugAndPersistsStack(t *testing.T) {
	t.Parallel()

	ctx := authenticatedContext()
	now := time.Date(2026, 7, 6, 13, 30, 0, 0, time.UTC)
	stacks := &recordingStackRepository{}
	service := NewService(Service{
		Stacks:     stacks,
		Work:       newRecordingWork(stacks),
		Authorizer: &recordingAuthorizer{tiers: testPlatformAuthorizer()},
		StackIDs:   fixedStackIDGenerator{id: traits.StackID("stack_123")},
		Clock:      fixedClock{now: now},
	})

	stack, err := service.CreateStack(ctx, CreateStackCommand{
		TenantID: traits.TenantID("tenant_123"),
		Name:     "Acme Prod",
		Tags: map[string]string{
			"env": "prod",
		},
		DefaultCredentialIDs: []traits.CredentialSetID{traits.CredentialSetID("credential_123")},
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
	if stack.CreatedBy != traits.UserID(keycloakSubject) {
		t.Fatalf("created by = %q, want %q", stack.CreatedBy, keycloakSubject)
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

func TestCreateStackReturnsDuplicateSlugConflict(t *testing.T) {
	t.Parallel()

	stacks := &recordingStackRepository{createErr: ErrDuplicateStackSlug}
	authorizer := &recordingAuthorizer{tiers: testPlatformAuthorizer()}
	service := NewService(Service{
		Stacks:     stacks,
		Work:       newRecordingWork(stacks),
		Authorizer: authorizer,
		StackIDs:   fixedStackIDGenerator{id: traits.StackID("stack_123")},
		Clock:      fixedClock{now: time.Now()},
	})

	_, err := service.CreateStack(authenticatedContext(), CreateStackCommand{
		TenantID: traits.TenantID("tenant_123"),
		Name:     "Acme Prod",
		Slug:     "acme-prod",
	})
	if !errors.Is(err, ErrDuplicateStackSlug) {
		t.Fatalf("error = %v, want ErrDuplicateStackSlug", err)
	}
	if authorizer.calls != 0 {
		t.Fatalf("owner writes = %d, want 0", authorizer.calls)
	}
}

func TestCreateStackRejectsInvalidTagKey(t *testing.T) {
	t.Parallel()

	service := NewService(Service{
		Authorizer: testPlatformAuthorizer(),
		Stacks:     &recordingStackRepository{},
		Work:       newRecordingWork(&recordingStackRepository{}),
		StackIDs:   fixedStackIDGenerator{id: traits.StackID("stack_123")},
	})

	_, err := service.CreateStack(authenticatedContext(), CreateStackCommand{
		TenantID: traits.TenantID("tenant_123"),
		Name:     "Acme Prod",
		Tags: map[string]string{
			"bad key": "prod",
		},
	})
	if !errors.Is(err, ErrInvalidCommand) {
		t.Fatalf("error = %v, want ErrInvalidCommand", err)
	}
}

func TestCreateStackRejectsEmptyDefaultCredentialID(t *testing.T) {
	t.Parallel()

	service := NewService(Service{
		Authorizer: testPlatformAuthorizer(),
		Stacks:     &recordingStackRepository{},
		Work:       newRecordingWork(&recordingStackRepository{}),
		StackIDs:   fixedStackIDGenerator{id: traits.StackID("stack_123")},
	})

	_, err := service.CreateStack(authenticatedContext(), CreateStackCommand{
		TenantID: traits.TenantID("tenant_123"),
		Name:     "Acme Prod",
		DefaultCredentialIDs: []traits.CredentialSetID{
			traits.CredentialSetID(""),
		},
	})
	if !errors.Is(err, ErrInvalidCommand) {
		t.Fatalf("error = %v, want ErrInvalidCommand", err)
	}
}

func TestGetStackPassesTenantAndIDAndNormalizesNilTemplates(t *testing.T) {
	t.Parallel()

	stacks := &recordingStackRepository{
		view: StackView{
			Stack: traits.Stack{
				ID:       traits.StackID("stack_123"),
				TenantID: traits.TenantID("tenant_123"),
				Name:     "Acme Prod",
				Slug:     "acme-prod",
			},
			Templates: nil,
		},
	}
	service := NewService(Service{Stacks: stacks, Authorizer: &permissionAuthorizer{allowed: true}})
	ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: keycloakSubject})

	view, err := service.GetStack(ctx, GetStackCommand{
		TenantID: traits.TenantID("tenant_123"),
		StackID:  traits.StackID("stack_123"),
	})
	if err != nil {
		t.Fatalf("GetStack returned error: %v", err)
	}

	if stacks.gotTenantID != traits.TenantID("tenant_123") {
		t.Fatalf("tenant lookup = %q, want tenant_123", stacks.gotTenantID)
	}
	if stacks.gotStackID != traits.StackID("stack_123") {
		t.Fatalf("stack lookup = %q, want stack_123", stacks.gotStackID)
	}
	if view.Templates == nil {
		t.Fatal("templates = nil, want empty slice")
	}
	if len(view.Templates) != 0 {
		t.Fatalf("len(templates) = %d, want 0", len(view.Templates))
	}
}

func TestTemplateDisplayName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		repoName string
		rootPath string
		want     string
	}{
		{name: "root path dot", repoName: "infra-templates", rootPath: ".", want: "infra-templates"},
		{name: "empty root path", repoName: "infra-templates", rootPath: "", want: "infra-templates"},
		{name: "subdirectory", repoName: "infra-templates", rootPath: "modules/vpc", want: "infra-templates/modules/vpc"},
		{name: "nested subdirectory", repoName: "my-repo", rootPath: "modules/network/vpc", want: "my-repo/modules/network/vpc"},
		{name: "trims whitespace", repoName: "  my-repo  ", rootPath: ".", want: "my-repo"},
		{name: "cleans path", repoName: "my-repo", rootPath: "modules/../modules/vpc", want: "my-repo/modules/vpc"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := templateDisplayName(tt.repoName, tt.rootPath)
			if got != tt.want {
				t.Errorf("templateDisplayName(%q, %q) = %q, want %q", tt.repoName, tt.rootPath, got, tt.want)
			}
		})
	}
}

func TestGetStackResolvesTemplateDisplayName(t *testing.T) {
	t.Parallel()

	stacks := &recordingStackRepository{
		view: StackView{
			Stack: traits.Stack{
				ID:       traits.StackID("stack_123"),
				TenantID: traits.TenantID("tenant_123"),
				Name:     "Acme Prod",
				Slug:     "acme-prod",
			},
			Templates: []StackTemplateView{
				{StackTemplate: traits.StackTemplate{
					ID:                        traits.StackTemplateID("stack_template_1"),
					DesiredTemplateRevisionID: traits.TemplateRevisionID("rev_1"),
				}},
				{StackTemplate: traits.StackTemplate{
					ID:                        traits.StackTemplateID("stack_template_2"),
					DesiredTemplateRevisionID: traits.TemplateRevisionID("rev_2"),
				}},
				{StackTemplate: traits.StackTemplate{
					ID:                        traits.StackTemplateID("stack_template_3"),
					DesiredTemplateRevisionID: traits.TemplateRevisionID("rev_missing"),
				}},
			},
		},
	}
	revisions := &recordingTemplateRepository{
		templates: []traits.TemplateRevision{
			{ID: traits.TemplateRevisionID("rev_1"), RepoName: "infra-templates", RootPath: "modules/vpc", SourceRef: "v2.0.0"},
			{ID: traits.TemplateRevisionID("rev_2"), RepoName: "my-repo", RootPath: ".", SourceRef: "main"},
		},
	}
	service := NewService(Service{Stacks: stacks, TemplateRevisions: revisions, Authorizer: &permissionAuthorizer{allowed: true}})
	ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: keycloakSubject})

	view, err := service.GetStack(ctx, GetStackCommand{
		TenantID: traits.TenantID("tenant_123"),
		StackID:  traits.StackID("stack_123"),
	})
	if err != nil {
		t.Fatalf("GetStack returned error: %v", err)
	}

	if len(view.Templates) != 3 {
		t.Fatalf("len(templates) = %d, want 3", len(view.Templates))
	}
	if got := view.Templates[0].DisplayName; got != "infra-templates/modules/vpc" {
		t.Errorf("template[0].DisplayName = %q, want infra-templates/modules/vpc", got)
	}
	// The ref is resolved from the desired revision on every read rather than
	// stored on the component, so it cannot drift from the revision in use.
	if got := view.Templates[0].SourceRef; got != "v2.0.0" {
		t.Errorf("template[0].SourceRef = %q, want v2.0.0", got)
	}
	if got := view.Templates[1].SourceRef; got != "main" {
		t.Errorf("template[1].SourceRef = %q, want main", got)
	}
	// An unresolvable revision leaves it empty rather than guessing.
	if got := view.Templates[2].SourceRef; got != "" {
		t.Errorf("template[2].SourceRef = %q, want empty", got)
	}
	if got := view.Templates[1].DisplayName; got != "my-repo" {
		t.Errorf("template[1].DisplayName = %q, want my-repo", got)
	}
	if got := view.Templates[2].DisplayName; got != "" {
		t.Errorf("template[2].DisplayName = %q, want empty (no matching revision)", got)
	}
}

func TestListStacksPassesTenantAndNormalizesNilStacks(t *testing.T) {
	t.Parallel()

	stacks := &recordingStackRepository{list: nil}
	service := NewService(Service{Stacks: stacks, Authorizer: &permissionAuthorizer{}})
	ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: keycloakSubject})

	got, err := service.ListStacks(ctx, ListStacksCommand{
		TenantID: traits.TenantID("tenant_123"),
	})
	if err != nil {
		t.Fatalf("ListStacks returned error: %v", err)
	}

	if stacks.gotListTenantID != traits.TenantID("tenant_123") {
		t.Fatalf("tenant list lookup = %q, want tenant_123", stacks.gotListTenantID)
	}
	if got == nil {
		t.Fatal("stacks = nil, want empty slice")
	}
	if len(got) != 0 {
		t.Fatalf("len(stacks) = %d, want 0", len(got))
	}
}

func TestListTemplateRevisionsPassesTenantAndNormalizesNilTemplateRevisions(t *testing.T) {
	t.Parallel()

	templates := &recordingTemplateRepository{templates: nil}
	service := NewService(Service{TemplateRevisions: templates, Authorizer: testPlatformAuthorizer()})

	got, err := service.ListTemplateRevisions(authenticatedContext(), ListTemplateRevisionsCommand{
		TenantID: traits.TenantID("tenant_123"),
	})
	if err != nil {
		t.Fatalf("ListTemplateRevisions returned error: %v", err)
	}

	if templates.gotListTenantID != traits.TenantID("tenant_123") {
		t.Fatalf("tenant list lookup = %q, want tenant_123", templates.gotListTenantID)
	}
	if got == nil {
		t.Fatal("templates = nil, want empty slice")
	}
	if len(got) != 0 {
		t.Fatalf("len(templates) = %d, want 0", len(got))
	}
}

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
		template: traits.TemplateRevision{
			ID:               traits.TemplateRevisionID("template_123"),
			TenantID:         traits.TenantID("tenant_123"),
			SourceTemplateID: traits.SourceTemplateID("source_template_vpc"),
			SourceRef:        "main",
			Status:           traits.TemplateRevisionActive,
		},
		variables: []traits.TemplateVariable{
			{Name: "region", Required: true},
			{Name: "cidr", Required: false, HasDefault: true},
		},
	}
	installer := &recordingStackTemplateInstaller{}
	service := NewService(Service{
		Authorizer:               &permissionAuthorizer{allowed: true},
		Stacks:                   stacks,
		Work:                     newRecordingWork(stacks),
		TemplateRevisionMetadata: templates,
		TemplateRevisions:        templates,
		StackTemplateInstaller:   installer,
		StackTemplateIDs:         fixedStackTemplateIDGenerator{id: traits.StackTemplateID("stack_template_a1b2c3d4")},
	})

	stackTemplate, err := service.AddTemplateToStack(authenticatedContext(), AddTemplateToStackCommand{
		TenantID:           traits.TenantID("tenant_123"),
		StackID:            traits.StackID("stack_123"),
		TemplateRevisionID: traits.TemplateRevisionID("template_123"),
		ComponentKey:       "primary-vpc",
		ConfigJSON:         json.RawMessage(`{"region":"us-east-1"}`),
	})
	if err != nil {
		t.Fatalf("AddTemplateToStack returned error: %v", err)
	}

	if stackTemplate.ID != traits.StackTemplateID("stack_template_a1b2c3d4") {
		t.Fatalf("stack template revision ID = %q, want stack_template_a1b2c3d4", stackTemplate.ID)
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
	if stackTemplate.CreatedBy != traits.UserID(keycloakSubject) {
		t.Fatalf("created by = %q, want %q", stackTemplate.CreatedBy, keycloakSubject)
	}
	if stackTemplate.ComponentKey != "primary-vpc" {
		t.Fatalf("component key = %q, want primary-vpc", stackTemplate.ComponentKey)
	}
	if stackTemplate.SourceTemplateID != traits.SourceTemplateID("source_template_vpc") {
		t.Fatalf("source template ID = %q, want source_template_vpc", stackTemplate.SourceTemplateID)
	}
	if stackTemplate.DesiredTemplateRevisionID != traits.TemplateRevisionID("template_123") {
		t.Fatalf("desired template revision ID = %q, want template_123", stackTemplate.DesiredTemplateRevisionID)
	}
	if string(stackTemplate.DesiredConfigJSON) != `{"region":"us-east-1"}` {
		t.Fatalf("desired config json = %s", stackTemplate.DesiredConfigJSON)
	}
	if installer.created.CreatedBy != traits.UserID(keycloakSubject) {
		t.Fatalf("persisted created by = %q, want %q", installer.created.CreatedBy, keycloakSubject)
	}
	if string(installer.created.InstalledConfigJSON) != `{"region":"us-east-1"}` {
		t.Fatalf("config json = %s", installer.created.InstalledConfigJSON)
	}
	if installer.created.SourceTemplateID != traits.SourceTemplateID("source_template_vpc") {
		t.Fatalf("persisted source template ID = %q, want source_template_vpc", installer.created.SourceTemplateID)
	}
	if installer.created.DesiredTemplateRevisionID != traits.TemplateRevisionID("template_123") {
		t.Fatalf("persisted desired template revision ID = %q, want template_123", installer.created.DesiredTemplateRevisionID)
	}
	if string(installer.created.DesiredConfigJSON) != `{"region":"us-east-1"}` {
		t.Fatalf("persisted desired config json = %s", installer.created.DesiredConfigJSON)
	}
}

func TestAddTemplateToStackRejectsMissingRequiredVariable(t *testing.T) {
	t.Parallel()

	service := NewService(Service{
		Authorizer: &permissionAuthorizer{allowed: true},
		Stacks: &recordingStackRepository{
			stack: traits.Stack{ID: traits.StackID("stack_123"), TenantID: traits.TenantID("tenant_123"), Slug: "acme-prod"},
		},
		TemplateRevisionMetadata: &recordingTemplateRepository{
			template: traits.TemplateRevision{ID: traits.TemplateRevisionID("template_123"), TenantID: traits.TenantID("tenant_123"), Status: traits.TemplateRevisionActive},
		},
		TemplateRevisions: &recordingTemplateRepository{
			variables: []traits.TemplateVariable{{Name: "region", Required: true}},
		},
		StackTemplateInstaller: &recordingStackTemplateInstaller{},
		StackTemplateIDs:       fixedStackTemplateIDGenerator{id: traits.StackTemplateID("stack_template_a1b2c3d4")},
	})

	_, err := service.AddTemplateToStack(authenticatedContext(), AddTemplateToStackCommand{
		TenantID:           traits.TenantID("tenant_123"),
		StackID:            traits.StackID("stack_123"),
		TemplateRevisionID: traits.TemplateRevisionID("template_123"),
		ConfigJSON:         json.RawMessage(`{}`),
	})
	if !errors.Is(err, ErrStackTemplateConfigInvalid) {
		t.Fatalf("error = %v, want ErrStackTemplateConfigInvalid", err)
	}
}

func TestAddTemplateToStackRejectsUnknownVariable(t *testing.T) {
	t.Parallel()

	service := NewService(Service{
		Authorizer: &permissionAuthorizer{allowed: true},
		Stacks: &recordingStackRepository{
			stack: traits.Stack{ID: traits.StackID("stack_123"), TenantID: traits.TenantID("tenant_123"), Slug: "acme-prod"},
		},
		TemplateRevisionMetadata: &recordingTemplateRepository{
			template: traits.TemplateRevision{ID: traits.TemplateRevisionID("template_123"), TenantID: traits.TenantID("tenant_123"), Status: traits.TemplateRevisionActive},
		},
		TemplateRevisions: &recordingTemplateRepository{
			variables: []traits.TemplateVariable{{Name: "region", Required: true}},
		},
		StackTemplateInstaller: &recordingStackTemplateInstaller{},
		StackTemplateIDs:       fixedStackTemplateIDGenerator{id: traits.StackTemplateID("stack_template_a1b2c3d4")},
	})

	_, err := service.AddTemplateToStack(authenticatedContext(), AddTemplateToStackCommand{
		TenantID:           traits.TenantID("tenant_123"),
		StackID:            traits.StackID("stack_123"),
		TemplateRevisionID: traits.TemplateRevisionID("template_123"),
		ConfigJSON:         json.RawMessage(`{"region":"us-east-1","extra":"nope"}`),
	})
	if !errors.Is(err, ErrStackTemplateConfigInvalid) {
		t.Fatalf("error = %v, want ErrStackTemplateConfigInvalid", err)
	}
}

func TestAddTemplateToStackRejectsInactiveTemplate(t *testing.T) {
	t.Parallel()

	service := NewService(Service{
		Authorizer: &permissionAuthorizer{allowed: true},
		Stacks: &recordingStackRepository{
			stack: traits.Stack{ID: traits.StackID("stack_123"), TenantID: traits.TenantID("tenant_123"), Slug: "acme-prod"},
		},
		TemplateRevisionMetadata: &recordingTemplateRepository{
			template: traits.TemplateRevision{ID: traits.TemplateRevisionID("template_123"), TenantID: traits.TenantID("tenant_123"), Status: traits.TemplateRevisionInvalid},
		},
		TemplateRevisions:      &recordingTemplateRepository{},
		StackTemplateInstaller: &recordingStackTemplateInstaller{},
		StackTemplateIDs:       fixedStackTemplateIDGenerator{id: traits.StackTemplateID("stack_template_a1b2c3d4")},
	})

	_, err := service.AddTemplateToStack(authenticatedContext(), AddTemplateToStackCommand{
		TenantID:           traits.TenantID("tenant_123"),
		StackID:            traits.StackID("stack_123"),
		TemplateRevisionID: traits.TemplateRevisionID("template_123"),
		ConfigJSON:         json.RawMessage(`{}`),
	})
	if !errors.Is(err, ErrTemplateNotInstallable) {
		t.Fatalf("error = %v, want ErrTemplateNotInstallable", err)
	}
}

func TestStartTemplateRunCreatesQueuedRunWithoutDispatchingWorkflow(t *testing.T) {
	t.Parallel()

	ctx := authenticatedContext()
	now := time.Date(2026, 7, 2, 9, 30, 0, 0, time.UTC)

	stackTemplates := &recordingStackTemplateRepository{
		stackTemplate: traits.StackTemplate{
			ID:                        traits.StackTemplateID("stack_template_123"),
			StackID:                   traits.StackID("stack_123"),
			SourceTemplateID:          traits.SourceTemplateID("source_template_vpc"),
			DesiredTemplateRevisionID: traits.TemplateRevisionID("template_rev_2"),
			DesiredConfigJSON:         json.RawMessage(`{"region":"us-east-1"}`),
			WorkspaceName:             "mtp_acme_prod_vpc_a13f9c",
			Lifecycle:                 traits.StackTemplateActive,
			// An apply needs a plan that still describes desired state.
			LastPlannedRunID:              traits.TemplateRunID("run_plan_1"),
			LastPlannedTemplateRevisionID: traits.TemplateRevisionID("template_rev_2"),
			LastPlannedConfigJSON:         json.RawMessage(`{"region":"us-east-1"}`),
		},
	}
	templates := &recordingTemplateRepository{
		template: traits.TemplateRevision{
			ID:               traits.TemplateRevisionID("template_rev_2"),
			TenantID:         traits.TenantID("tenant_123"),
			SourceTemplateID: traits.SourceTemplateID("source_template_vpc"),
			RepoOwner:        "acme",
			RepoName:         "infra-templates",
			// The run's ref comes from here now, not from the component.
			SourceRef:         "main",
			ResolvedCommitSHA: "sha-2",
			RootPath:          "modules/vpc",
			Status:            traits.TemplateRevisionActive,
		},
	}
	runs := &recordingTemplateRunRepository{run: traits.TemplateRun{ID: "run_123", TenantID: "tenant_123", StackTemplateID: "stack_template_123"}}
	work := &recordingUnitOfWork{templateRuns: runs}

	service := NewService(Service{
		Authorizer:               &permissionAuthorizer{allowed: true},
		Work:                     work,
		StackTemplates:           stackTemplates,
		TemplateRuns:             runs,
		TemplateRevisionMetadata: templates,
		RunIDs:                   fixedTemplateRunIDGenerator{runID: traits.TemplateRunID("run_123")},
		Clock:                    fixedClock{now: now},
	})

	run, err := service.StartTemplateRun(ctx, StartTemplateRunCommand{
		TenantID:        traits.TenantID("tenant_123"),
		StackTemplateID: traits.StackTemplateID("stack_template_123"),
		Operation:       traits.OperationApply,
	})
	if err != nil {
		t.Fatalf("StartTemplateRun returned error: %v", err)
	}

	if run.ID != traits.TemplateRunID("run_123") {
		t.Fatalf("run.ID = %q, want run_123", run.ID)
	}

	if run.Status != traits.TemplateRunQueued {
		t.Fatalf("run.Status = %q, want %q", run.Status, traits.TemplateRunQueued)
	}
	if run.TriggerActor != traits.UserID(keycloakSubject) {
		t.Fatalf("run.TriggerActor = %q, want %q", run.TriggerActor, keycloakSubject)
	}

	if run.WorkspaceName != "mtp_acme_prod_vpc_a13f9c" {
		t.Fatalf("run.WorkspaceName = %q", run.WorkspaceName)
	}

	if run.SelectedRef != "main" {
		t.Fatalf("run.SelectedRef = %q, want main", run.SelectedRef)
	}

	if run.TemplateRevisionID != traits.TemplateRevisionID("template_rev_2") {
		t.Fatalf("run.TemplateRevisionID = %q, want template_rev_2", run.TemplateRevisionID)
	}

	if run.SourceTemplateID != traits.SourceTemplateID("source_template_vpc") {
		t.Fatalf("run.SourceTemplateID = %q, want source_template_vpc", run.SourceTemplateID)
	}

	if string(run.ConfigJSON) != `{"region":"us-east-1"}` {
		t.Fatalf("run.ConfigJSON = %s", run.ConfigJSON)
	}

	if run.ResolvedCommitSHA != "sha-2" {
		t.Fatalf("run.ResolvedCommitSHA = %q, want sha-2", run.ResolvedCommitSHA)
	}

	if !run.StartedAt.Equal(now) {
		t.Fatalf("run.StartedAt = %v, want %v", run.StartedAt, now)
	}

	if stackTemplates.gotTenantID != traits.TenantID("tenant_123") {
		t.Fatalf("stack template lookup tenant = %q", stackTemplates.gotTenantID)
	}

	if runs.created.ID != run.ID {
		t.Fatalf("created run ID = %q, want %q", runs.created.ID, run.ID)
	}

	if len(work.requests) != 1 || work.requests[0].Kind != KindStartTemplateRun {
		t.Fatalf("queued requests = %#v, want one start_template_run request", work.requests)
	}

	if templates.gotGetTemplateTenantID != traits.TenantID("tenant_123") {
		t.Fatalf("template lookup tenant = %q, want tenant_123", templates.gotGetTemplateTenantID)
	}

	if templates.gotGetTemplateRevisionID != traits.TemplateRevisionID("template_rev_2") {
		t.Fatalf("template revision lookup ID = %q, want template_rev_2", templates.gotGetTemplateRevisionID)
	}

}

func TestUpdateStackTemplateConfigValidatesDesiredRevisionVariables(t *testing.T) {
	t.Parallel()

	stackTemplates := &recordingStackTemplateRepository{
		stackTemplate: traits.StackTemplate{
			ID:                        traits.StackTemplateID("stack_template_123"),
			TenantID:                  traits.TenantID("tenant_123"),
			StackID:                   traits.StackID("stack_123"),
			DesiredTemplateRevisionID: traits.TemplateRevisionID("template_rev_2"),
			Lifecycle:                 traits.StackTemplateActive,
		},
	}
	templates := &recordingTemplateRepository{
		variables: []traits.TemplateVariable{
			{Name: "region", Required: true},
		},
	}
	service := NewService(Service{
		Authorizer:        &permissionAuthorizer{allowed: true},
		StackTemplates:    stackTemplates,
		TemplateRevisions: templates,
	})

	updated, err := service.UpdateStackTemplateConfig(authenticatedContext(), UpdateStackTemplateConfigCommand{
		TenantID:        traits.TenantID("tenant_123"),
		StackTemplateID: traits.StackTemplateID("stack_template_123"),
		ConfigJSON:      json.RawMessage(`{"region":"us-east-1"}`),
	})
	if err != nil {
		t.Fatalf("UpdateStackTemplateConfig returned error: %v", err)
	}

	if templates.gotVariablesTemplateRevisionID != traits.TemplateRevisionID("template_rev_2") {
		t.Fatalf("variables template revision ID = %q, want template_rev_2", templates.gotVariablesTemplateRevisionID)
	}
	if string(stackTemplates.gotConfigJSON) != `{"region":"us-east-1"}` {
		t.Fatalf("updated config = %s", stackTemplates.gotConfigJSON)
	}
	if string(updated.DesiredConfigJSON) != `{"region":"us-east-1"}` {
		t.Fatalf("returned desired config = %s", updated.DesiredConfigJSON)
	}
}

func TestUpdateStackTemplateConfigRejectsMissingDesiredRevision(t *testing.T) {
	t.Parallel()

	stackTemplates := &recordingStackTemplateRepository{
		stackTemplate: traits.StackTemplate{
			ID:        traits.StackTemplateID("stack_template_123"),
			TenantID:  traits.TenantID("tenant_123"),
			Lifecycle: traits.StackTemplateActive,
		},
	}
	service := NewService(Service{
		Authorizer:        &permissionAuthorizer{allowed: true},
		StackTemplates:    stackTemplates,
		TemplateRevisions: &recordingTemplateRepository{},
	})

	_, err := service.UpdateStackTemplateConfig(authenticatedContext(), UpdateStackTemplateConfigCommand{
		TenantID:        traits.TenantID("tenant_123"),
		StackTemplateID: traits.StackTemplateID("stack_template_123"),
		ConfigJSON:      json.RawMessage(`{}`),
	})
	if !errors.Is(err, ErrStackTemplateConfigInvalid) {
		t.Fatalf("error = %v, want ErrStackTemplateConfigInvalid", err)
	}
	if stackTemplates.gotConfigJSON != nil {
		t.Fatalf("updated config = %s, want no update", stackTemplates.gotConfigJSON)
	}
}

func TestUpgradeStackTemplateCarriesForwardCompatibleConfig(t *testing.T) {
	t.Parallel()

	stackTemplates := &recordingStackTemplateRepository{
		stackTemplate: traits.StackTemplate{
			ID:                        traits.StackTemplateID("stack_template_123"),
			TenantID:                  traits.TenantID("tenant_123"),
			SourceTemplateID:          traits.SourceTemplateID("source_template_vpc"),
			DesiredTemplateRevisionID: traits.TemplateRevisionID("template_rev_1"),
			DesiredConfigJSON:         json.RawMessage(`{"region":"us-east-1","removed":"old"}`),
			Lifecycle:                 traits.StackTemplateActive,
		},
	}
	templates := &recordingTemplateRepository{
		template: traits.TemplateRevision{
			ID:               traits.TemplateRevisionID("template_rev_2"),
			TenantID:         traits.TenantID("tenant_123"),
			SourceTemplateID: traits.SourceTemplateID("source_template_vpc"),
			Status:           traits.TemplateRevisionActive,
		},
		variables: []traits.TemplateVariable{
			{Name: "region", Required: true},
			{Name: "size", HasDefault: true},
		},
	}
	service := NewService(Service{
		Authorizer:               &permissionAuthorizer{allowed: true},
		StackTemplates:           stackTemplates,
		TemplateRevisionMetadata: templates,
		TemplateRevisions:        templates,
	})

	updated, err := service.UpgradeStackTemplate(authenticatedContext(), UpgradeStackTemplateCommand{
		TenantID:                 traits.TenantID("tenant_123"),
		StackTemplateID:          traits.StackTemplateID("stack_template_123"),
		TargetTemplateRevisionID: traits.TemplateRevisionID("template_rev_2"),
	})
	if err != nil {
		t.Fatalf("UpgradeStackTemplate returned error: %v", err)
	}

	if stackTemplates.gotDesiredTemplateRevisionID != traits.TemplateRevisionID("template_rev_2") {
		t.Fatalf("updated desired template revision ID = %q, want template_rev_2", stackTemplates.gotDesiredTemplateRevisionID)
	}
	if string(stackTemplates.gotConfigJSON) != `{"region":"us-east-1"}` {
		t.Fatalf("carried config = %s, want region only", stackTemplates.gotConfigJSON)
	}
	if updated.DesiredTemplateRevisionID != traits.TemplateRevisionID("template_rev_2") {
		t.Fatalf("returned desired template revision ID = %q, want template_rev_2", updated.DesiredTemplateRevisionID)
	}
}

func TestUpgradeStackTemplateRejectsDifferentSourceTemplate(t *testing.T) {
	t.Parallel()

	stackTemplates := &recordingStackTemplateRepository{
		stackTemplate: traits.StackTemplate{
			ID:               traits.StackTemplateID("stack_template_123"),
			TenantID:         traits.TenantID("tenant_123"),
			SourceTemplateID: traits.SourceTemplateID("source_template_vpc"),
			Lifecycle:        traits.StackTemplateActive,
		},
	}
	templates := &recordingTemplateRepository{
		template: traits.TemplateRevision{
			ID:               traits.TemplateRevisionID("template_rev_2"),
			TenantID:         traits.TenantID("tenant_123"),
			SourceTemplateID: traits.SourceTemplateID("source_template_db"),
			Status:           traits.TemplateRevisionActive,
		},
	}
	service := NewService(Service{
		Authorizer:               &permissionAuthorizer{allowed: true},
		StackTemplates:           stackTemplates,
		TemplateRevisionMetadata: templates,
		TemplateRevisions:        templates,
	})

	_, err := service.UpgradeStackTemplate(authenticatedContext(), UpgradeStackTemplateCommand{
		TenantID:                 traits.TenantID("tenant_123"),
		StackTemplateID:          traits.StackTemplateID("stack_template_123"),
		TargetTemplateRevisionID: traits.TemplateRevisionID("template_rev_2"),
	})
	if !errors.Is(err, ErrStackTemplateUpgradeInvalid) {
		t.Fatalf("error = %v, want ErrStackTemplateUpgradeInvalid", err)
	}
}

func TestStartTemplateRunRejectsInvalidOperation(t *testing.T) {
	t.Parallel()

	service := NewService(Service{
		StackTemplates: &recordingStackTemplateRepository{},
		TemplateRuns:   &recordingTemplateRunRepository{},
		RunIDs:         fixedTemplateRunIDGenerator{runID: traits.TemplateRunID("run_123")},
		Clock:          fixedClock{now: time.Now()},
	})

	_, err := service.StartTemplateRun(authenticatedContext(), StartTemplateRunCommand{
		TenantID:        traits.TenantID("tenant_123"),
		StackTemplateID: traits.StackTemplateID("stack_template_123"),
		Operation:       traits.OperationType("refresh"),
	})
	if !errors.Is(err, ErrInvalidCommand) {
		t.Fatalf("error = %v, want ErrInvalidCommand", err)
	}
}

func TestStartTemplateRunRejectsInactiveStackTemplate(t *testing.T) {
	t.Parallel()

	stackTemplates := &recordingStackTemplateRepository{
		stackTemplate: traits.StackTemplate{
			ID:            traits.StackTemplateID("stack_template_123"),
			WorkspaceName: "mtp_acme_prod_vpc_a13f9c",
			Lifecycle:     traits.StackTemplateDestroyed,
		},
	}
	runs := &recordingTemplateRunRepository{}
	service := NewService(Service{
		Authorizer:     &permissionAuthorizer{allowed: true},
		StackTemplates: stackTemplates,
		TemplateRuns:   runs,
		RunIDs:         fixedTemplateRunIDGenerator{runID: traits.TemplateRunID("run_123")},
		Clock:          fixedClock{now: time.Now()},
	})

	_, err := service.StartTemplateRun(authenticatedContext(), StartTemplateRunCommand{
		TenantID:        traits.TenantID("tenant_123"),
		StackTemplateID: traits.StackTemplateID("stack_template_123"),
		Operation:       traits.OperationApply,
	})
	if !errors.Is(err, ErrStackTemplateNotRunnable) {
		t.Fatalf("error = %v, want ErrStackTemplateNotRunnable", err)
	}

	if runs.created.ID != "" {
		t.Fatalf("created run ID = %q, want no persisted run", runs.created.ID)
	}

}

// The bug this closes: saving config leaves the completed plan as the latest
// run, so the old "is the latest run a completed plan?" check stayed true and
// the apply then snapshotted the edited config instead of the reviewed one.
func TestStartTemplateRunRejectsApplyWhosePlanNoLongerMatchesDesired(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		stackTemplate traits.StackTemplate
	}{
		{
			name: "config saved after the plan",
			stackTemplate: traits.StackTemplate{
				LastPlannedRunID:              traits.TemplateRunID("run_plan_1"),
				LastPlannedTemplateRevisionID: traits.TemplateRevisionID("template_123"),
				LastPlannedConfigJSON:         json.RawMessage(`{"region":"us-east-1"}`),
			},
		},
		{
			name: "revision changed after the plan",
			stackTemplate: traits.StackTemplate{
				LastPlannedRunID:              traits.TemplateRunID("run_plan_1"),
				LastPlannedTemplateRevisionID: traits.TemplateRevisionID("template_122"),
				LastPlannedConfigJSON:         json.RawMessage(`{"region":"eu-west-1"}`),
			},
		},
		{
			name:          "never planned at all",
			stackTemplate: traits.StackTemplate{},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			stackTemplate := test.stackTemplate
			stackTemplate.ID = traits.StackTemplateID("stack_template_123")
			stackTemplate.DesiredTemplateRevisionID = traits.TemplateRevisionID("template_123")
			stackTemplate.DesiredConfigJSON = json.RawMessage(`{"region":"eu-west-1"}`)
			stackTemplate.WorkspaceName = "mtp_acme_prod_vpc_a13f9c"
			stackTemplate.Lifecycle = traits.StackTemplateActive

			runs := &recordingTemplateRunRepository{}
			service := NewService(Service{
				Authorizer:     &permissionAuthorizer{allowed: true},
				StackTemplates: &recordingStackTemplateRepository{stackTemplate: stackTemplate},
				TemplateRuns:   runs,
				TemplateRevisionMetadata: &recordingTemplateRepository{
					template: traits.TemplateRevision{ID: traits.TemplateRevisionID("template_123"), Status: traits.TemplateRevisionActive},
				},
				RunIDs: fixedTemplateRunIDGenerator{runID: traits.TemplateRunID("run_123")},
				Clock:  fixedClock{now: time.Now()},
			})

			_, err := service.StartTemplateRun(authenticatedContext(), StartTemplateRunCommand{
				TenantID:        traits.TenantID("tenant_123"),
				StackTemplateID: traits.StackTemplateID("stack_template_123"),
				Operation:       traits.OperationApply,
			})
			if !errors.Is(err, ErrStackTemplatePlanStale) {
				t.Fatalf("error = %v, want ErrStackTemplatePlanStale", err)
			}
			if runs.created.ID != "" {
				t.Fatalf("created run ID = %q, want no persisted run", runs.created.ID)
			}
		})
	}
}

// The component does not own a ref. It used to keep the one chosen at install
// and stamp it onto every run, which stayed stale after a revision change: a
// component installed from main and moved to a v2.0.0 revision kept reporting
// main. The run takes the ref from the revision it is actually running.
func TestStartTemplateRunStampsTheRefOfTheRevisionBeingRun(t *testing.T) {
	t.Parallel()

	runs := &recordingTemplateRunRepository{}
	work := &recordingUnitOfWork{templateRuns: runs}
	service := NewService(Service{
		Work:         work,
		Authorizer:   &permissionAuthorizer{allowed: true},
		TemplateRuns: runs,
		StackTemplates: &recordingStackTemplateRepository{stackTemplate: traits.StackTemplate{
			ID:                        traits.StackTemplateID("stack_template_123"),
			DesiredTemplateRevisionID: traits.TemplateRevisionID("template_123"),
			WorkspaceName:             "mtp_acme_prod_vpc_a13f9c",
			Lifecycle:                 traits.StackTemplateActive,
		}},
		TemplateRevisionMetadata: &recordingTemplateRepository{
			template: traits.TemplateRevision{
				ID:                traits.TemplateRevisionID("template_123"),
				Status:            traits.TemplateRevisionActive,
				SourceRef:         "v2.0.0",
				ResolvedCommitSHA: "sha_v2",
			},
		},
		RunIDs: fixedTemplateRunIDGenerator{runID: traits.TemplateRunID("run_123")},
		Clock:  fixedClock{now: time.Now()},
	})

	run, err := service.StartTemplateRun(authenticatedContext(), StartTemplateRunCommand{
		TenantID:        traits.TenantID("tenant_123"),
		StackTemplateID: traits.StackTemplateID("stack_template_123"),
		Operation:       traits.OperationPlan,
	})
	if err != nil {
		t.Fatalf("StartTemplateRun returned error: %v", err)
	}
	if run.SelectedRef != "v2.0.0" {
		t.Fatalf("run.SelectedRef = %q, want v2.0.0", run.SelectedRef)
	}
	// The ref and the commit describe the same revision, which is the property
	// that was impossible to guarantee while the component carried its own ref.
	if run.ResolvedCommitSHA != "sha_v2" {
		t.Fatalf("run.ResolvedCommitSHA = %q, want sha_v2", run.ResolvedCommitSHA)
	}
}

// The gate is on apply alone — re-planning is how a stale plan gets fixed, so
// gating plan would leave the template stuck.
func TestStartTemplateRunAllowsPlanWhenThePlanIsStale(t *testing.T) {
	t.Parallel()

	runs := &recordingTemplateRunRepository{}
	work := &recordingUnitOfWork{templateRuns: runs}
	service := NewService(Service{
		Work:         work,
		Authorizer:   &permissionAuthorizer{allowed: true},
		TemplateRuns: runs,
		StackTemplates: &recordingStackTemplateRepository{stackTemplate: traits.StackTemplate{
			ID:                            traits.StackTemplateID("stack_template_123"),
			DesiredTemplateRevisionID:     traits.TemplateRevisionID("template_123"),
			DesiredConfigJSON:             json.RawMessage(`{"region":"eu-west-1"}`),
			WorkspaceName:                 "mtp_acme_prod_vpc_a13f9c",
			Lifecycle:                     traits.StackTemplateActive,
			LastPlannedRunID:              traits.TemplateRunID("run_plan_1"),
			LastPlannedTemplateRevisionID: traits.TemplateRevisionID("template_123"),
			LastPlannedConfigJSON:         json.RawMessage(`{"region":"us-east-1"}`),
		}},
		TemplateRevisionMetadata: &recordingTemplateRepository{
			template: traits.TemplateRevision{ID: traits.TemplateRevisionID("template_123"), Status: traits.TemplateRevisionActive},
		},
		RunIDs: fixedTemplateRunIDGenerator{runID: traits.TemplateRunID("run_123")},
		Clock:  fixedClock{now: time.Now()},
	})

	run, err := service.StartTemplateRun(authenticatedContext(), StartTemplateRunCommand{
		TenantID:        traits.TenantID("tenant_123"),
		StackTemplateID: traits.StackTemplateID("stack_template_123"),
		Operation:       traits.OperationPlan,
	})
	if err != nil {
		t.Fatalf("StartTemplateRun returned error: %v", err)
	}
	if run.ID != traits.TemplateRunID("run_123") {
		t.Fatalf("run.ID = %q, want run_123", run.ID)
	}
}

func TestStartTemplateRunRejectsMissingDesiredRevision(t *testing.T) {
	t.Parallel()

	stackTemplates := &recordingStackTemplateRepository{
		stackTemplate: traits.StackTemplate{
			ID:            traits.StackTemplateID("stack_template_123"),
			WorkspaceName: "mtp_acme_prod_vpc_a13f9c",
			Lifecycle:     traits.StackTemplateActive,
		},
	}
	runs := &recordingTemplateRunRepository{}
	service := NewService(Service{
		Authorizer:     &permissionAuthorizer{allowed: true},
		StackTemplates: stackTemplates,
		TemplateRuns:   runs,
		TemplateRevisionMetadata: &recordingTemplateRepository{
			template: traits.TemplateRevision{
				ID:     traits.TemplateRevisionID("template_123"),
				Status: traits.TemplateRevisionActive,
			},
		},
		RunIDs: fixedTemplateRunIDGenerator{runID: traits.TemplateRunID("run_123")},
		Clock:  fixedClock{now: time.Now()},
	})

	_, err := service.StartTemplateRun(authenticatedContext(), StartTemplateRunCommand{
		TenantID:        traits.TenantID("tenant_123"),
		StackTemplateID: traits.StackTemplateID("stack_template_123"),
		Operation:       traits.OperationApply,
	})
	if !errors.Is(err, ErrStackTemplateNotRunnable) {
		t.Fatalf("error = %v, want ErrStackTemplateNotRunnable", err)
	}
	if runs.created.ID != "" {
		t.Fatalf("created run ID = %q, want no persisted run", runs.created.ID)
	}
}

func TestStartTemplateRunUsesDefaultRunIDGenerator(t *testing.T) {
	t.Parallel()

	stackTemplates := &recordingStackTemplateRepository{
		stackTemplate: traits.StackTemplate{
			ID:                        traits.StackTemplateID("stack_template_123"),
			DesiredTemplateRevisionID: traits.TemplateRevisionID("template_123"),
			WorkspaceName:             "mtp_acme_prod_vpc_a13f9c",
			Lifecycle:                 traits.StackTemplateActive,
		},
	}
	runs := &recordingTemplateRunRepository{}
	service := NewService(Service{
		Authorizer:     &permissionAuthorizer{allowed: true},
		Work:           &recordingUnitOfWork{templateRuns: runs},
		StackTemplates: stackTemplates,
		TemplateRuns:   runs,
		TemplateRevisionMetadata: &recordingTemplateRepository{
			template: traits.TemplateRevision{
				ID:        traits.TemplateRevisionID("template_123"),
				TenantID:  traits.TenantID("tenant_123"),
				RepoOwner: "acme",
				RepoName:  "infra-templates",
				RootPath:  ".",
				Status:    traits.TemplateRevisionActive,
			},
		},
		Clock: fixedClock{now: time.Now()},
	})

	run, err := service.StartTemplateRun(authenticatedContext(), StartTemplateRunCommand{
		TenantID:        traits.TenantID("tenant_123"),
		StackTemplateID: traits.StackTemplateID("stack_template_123"),
		Operation:       traits.OperationPlan,
	})
	if err != nil {
		t.Fatalf("StartTemplateRun returned error: %v", err)
	}

	if run.ID == "" {
		t.Fatal("run.ID is empty")
	}
	if runs.created.ID != run.ID {
		t.Fatalf("created run ID = %q, want %q", runs.created.ID, run.ID)
	}
}

func TestRegisterTemplateCreatesPendingRegistrationAndDispatchesWorkflow(t *testing.T) {
	t.Parallel()

	ctx := authenticatedContext()
	now := time.Date(2026, 7, 6, 10, 30, 0, 0, time.UTC)
	registrations := &recordingTemplateRegistrationRepository{}
	work := &recordingUnitOfWork{templateRegistrations: registrations}

	service := NewService(Service{
		Authorizer:            testPlatformAuthorizer(),
		Work:                  work,
		TemplateRegistrations: registrations,
		RegistrationIDs:       fixedTemplateRegistrationIDGenerator{id: traits.TemplateRegistrationID("template_registration_123")},
		Clock:                 fixedClock{now: now},
	})

	registration, err := service.RegisterTemplate(ctx, RegisterTemplateCommand{
		TenantID:  traits.TenantID("tenant_123"),
		RepoOwner: "acme",
		RepoName:  "infra-templates",
		SourceRef: "v0.0.1",
		RootPath:  "modules/vpc",
	})
	if err != nil {
		t.Fatalf("RegisterTemplate returned error: %v", err)
	}

	if registration.ID != traits.TemplateRegistrationID("template_registration_123") {
		t.Fatalf("registration.ID = %q, want template_registration_123", registration.ID)
	}
	if registration.Status != traits.TemplateRegistrationPending {
		t.Fatalf("registration.Status = %q, want %q", registration.Status, traits.TemplateRegistrationPending)
	}
	if registration.RequestedBy != traits.UserID(keycloakSubject) {
		t.Fatalf("registration.RequestedBy = %q, want %q", registration.RequestedBy, keycloakSubject)
	}
	if !registration.RequestedAt.Equal(now) {
		t.Fatalf("registration.RequestedAt = %v, want %v", registration.RequestedAt, now)
	}
	if registrations.created != registration {
		t.Fatalf("created registration = %#v, want %#v", registrations.created, registration)
	}
	if len(work.requests) != 1 || work.requests[0].Kind != KindStartTemplateSync {
		t.Fatalf("queued requests = %#v, want one start_template_sync request", work.requests)
	}
}

func TestRegisterTemplateRejectsMissingSourceRef(t *testing.T) {
	t.Parallel()

	service := NewService(Service{
		Authorizer:            testPlatformAuthorizer(),
		TemplateRegistrations: &recordingTemplateRegistrationRepository{},
	})

	_, err := service.RegisterTemplate(authenticatedContext(), RegisterTemplateCommand{
		TenantID:  traits.TenantID("tenant_123"),
		RepoOwner: "acme",
		RepoName:  "infra-templates",
		RootPath:  "modules/vpc",
	})
	if !errors.Is(err, ErrInvalidCommand) {
		t.Fatalf("error = %v, want ErrInvalidCommand", err)
	}
}

func TestRegisterTemplateDoesNotDispatchWhenPersistenceFails(t *testing.T) {
	t.Parallel()

	persistErr := errors.New("database unavailable")
	registrations := &recordingTemplateRegistrationRepository{createErr: persistErr}
	work := &recordingUnitOfWork{templateRegistrations: registrations}
	service := NewService(Service{
		Authorizer:            testPlatformAuthorizer(),
		Work:                  work,
		TemplateRegistrations: registrations,
		RegistrationIDs:       fixedTemplateRegistrationIDGenerator{id: traits.TemplateRegistrationID("template_registration_123")},
	})

	_, err := service.RegisterTemplate(authenticatedContext(), RegisterTemplateCommand{
		TenantID:  traits.TenantID("tenant_123"),
		RepoOwner: "acme",
		RepoName:  "infra-templates",
		SourceRef: "v0.0.1",
		RootPath:  "modules/vpc",
	})
	if !errors.Is(err, persistErr) {
		t.Fatalf("error = %v, want wrapped persistence error", err)
	}
	if len(work.requests) != 0 {
		t.Fatalf("queued requests = %#v, want none", work.requests)
	}
}

func TestApproveRunRecordsApprovalAndSignalsWorkflow(t *testing.T) {
	t.Parallel()

	ctx := authenticatedContext()
	now := time.Date(2026, 7, 2, 10, 15, 0, 0, time.UTC)
	runs := &recordingTemplateRunRepository{run: traits.TemplateRun{
		ID:              "run_123",
		TenantID:        "tenant_123",
		StackTemplateID: "stack_template_123",
		TriggerActor:    traits.UserID("different-user"),
	}}
	work := &recordingUnitOfWork{templateRuns: runs}

	service := NewService(Service{
		Authorizer:     &permissionAuthorizer{allowed: true},
		Work:           work,
		TemplateRuns:   runs,
		StackTemplates: &recordingStackTemplateRepository{stackTemplate: traits.StackTemplate{ID: "stack_template_123", TenantID: "tenant_123", StackID: "stack_123"}},
		Clock:          fixedClock{now: now},
	})

	err := service.ApproveRun(ctx, ApproveRunCommand{
		TenantID: traits.TenantID("tenant_123"),
		RunID:    traits.TemplateRunID("run_123"),
	})
	if err != nil {
		t.Fatalf("ApproveRun returned error: %v", err)
	}

	if runs.approval.RunID != traits.TemplateRunID("run_123") {
		t.Fatalf("approval run ID = %q, want run_123", runs.approval.RunID)
	}

	if runs.approval.TenantID != traits.TenantID("tenant_123") {
		t.Fatalf("approval tenant ID = %q, want tenant_123", runs.approval.TenantID)
	}

	if runs.approval.ApprovedBy != traits.UserID(keycloakSubject) {
		t.Fatalf("approval actor = %q, want %q", runs.approval.ApprovedBy, keycloakSubject)
	}

	if !runs.approval.ApprovedAt.Equal(now) {
		t.Fatalf("approval time = %v, want %v", runs.approval.ApprovedAt, now)
	}

	if len(work.requests) != 1 || work.requests[0].Kind != KindSignalRunApproval {
		t.Fatalf("queued requests = %#v, want one signal_run_approval request", work.requests)
	}
}

func TestApproveRunDoesNotSignalWhenRunIsNotApprovable(t *testing.T) {
	t.Parallel()

	runs := &recordingTemplateRunRepository{run: traits.TemplateRun{ID: "run_123", TenantID: "tenant_123", StackTemplateID: "stack_template_123"}, approvalErr: ErrRunNotApprovable}
	work := &recordingUnitOfWork{templateRuns: runs}

	service := NewService(Service{
		Authorizer:     &permissionAuthorizer{allowed: true},
		Work:           work,
		TemplateRuns:   runs,
		StackTemplates: &recordingStackTemplateRepository{stackTemplate: traits.StackTemplate{ID: "stack_template_123", TenantID: "tenant_123", StackID: "stack_123"}},
		Clock:          fixedClock{now: time.Now()},
	})

	err := service.ApproveRun(authenticatedContext(), ApproveRunCommand{
		TenantID: traits.TenantID("tenant_123"),
		RunID:    traits.TemplateRunID("run_123"),
	})
	if !errors.Is(err, ErrRunNotApprovable) {
		t.Fatalf("error = %v, want ErrRunNotApprovable", err)
	}

	if len(work.requests) != 0 {
		t.Fatalf("queued requests = %#v, want none", work.requests)
	}
}

func TestApproveRunAllowsSelfApproval(t *testing.T) {
	t.Parallel()

	ctx := authenticatedContext()
	runs := &recordingTemplateRunRepository{run: traits.TemplateRun{
		ID:              "run_123",
		TenantID:        "tenant_123",
		StackTemplateID: "stack_template_123",
		TriggerActor:    traits.UserID(keycloakSubject),
	}}
	work := &recordingUnitOfWork{templateRuns: runs}
	audit := &recordingAuditRepository{}

	service := NewService(Service{
		Authorizer:     &permissionAuthorizer{allowed: true},
		Work:           work,
		TemplateRuns:   runs,
		StackTemplates: &recordingStackTemplateRepository{stackTemplate: traits.StackTemplate{ID: "stack_template_123", TenantID: "tenant_123", StackID: "stack_123"}},
		Audit:          audit,
	})

	err := service.ApproveRun(ctx, ApproveRunCommand{
		TenantID: traits.TenantID("tenant_123"),
		RunID:    traits.TemplateRunID("run_123"),
	})
	if err != nil {
		t.Fatalf("error = %v, want nil", err)
	}

	if runs.approval.RunID == "" {
		t.Fatalf("approval was not recorded, want approval")
	}

	if len(work.requests) != 1 || work.requests[0].Kind != KindSignalRunApproval {
		t.Fatalf("queued requests = %#v, want one approval signal", work.requests)
	}
}

func TestApproveRunSelfApprovalWorksForPlatformAdmins(t *testing.T) {
	t.Parallel()

	ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: keycloakSubject})
	runs := &recordingTemplateRunRepository{run: traits.TemplateRun{
		ID:              "run_123",
		TenantID:        "tenant_123",
		StackTemplateID: "stack_template_123",
		TriggerActor:    traits.UserID(keycloakSubject),
	}}
	work := &recordingUnitOfWork{templateRuns: runs}
	audit := &recordingAuditRepository{}

	service := NewService(Service{
		Authorizer:     &permissionAuthorizer{allowed: true},
		Work:           work,
		TemplateRuns:   runs,
		StackTemplates: &recordingStackTemplateRepository{stackTemplate: traits.StackTemplate{ID: "stack_template_123", TenantID: "tenant_123", StackID: "stack_123"}},
		Audit:          audit,
	})

	err := service.ApproveRun(ctx, ApproveRunCommand{
		TenantID: traits.TenantID("tenant_123"),
		RunID:    traits.TemplateRunID("run_123"),
	})
	if err != nil {
		t.Fatalf("error = %v, want nil", err)
	}

	if len(work.requests) != 1 || work.requests[0].Kind != KindSignalRunApproval {
		t.Fatalf("queued requests = %#v, want one approval signal", work.requests)
	}
}

func TestApproveRunAuditsSuccessfulApproval(t *testing.T) {
	t.Parallel()

	ctx := authenticatedContext()
	now := time.Date(2026, 7, 2, 10, 15, 0, 0, time.UTC)
	runs := &recordingTemplateRunRepository{run: traits.TemplateRun{
		ID:              "run_123",
		TenantID:        "tenant_123",
		StackTemplateID: "stack_template_123",
		TriggerActor:    traits.UserID("different-user"),
	}}
	work := &recordingUnitOfWork{templateRuns: runs}
	audit := &recordingAuditRepository{}

	service := NewService(Service{
		Authorizer:     &permissionAuthorizer{allowed: true},
		Work:           work,
		TemplateRuns:   runs,
		StackTemplates: &recordingStackTemplateRepository{stackTemplate: traits.StackTemplate{ID: "stack_template_123", TenantID: "tenant_123", StackID: "stack_123"}},
		Clock:          fixedClock{now: now},
		Audit:          audit,
	})

	err := service.ApproveRun(ctx, ApproveRunCommand{
		TenantID: traits.TenantID("tenant_123"),
		RunID:    traits.TemplateRunID("run_123"),
	})
	if err != nil {
		t.Fatalf("ApproveRun returned error: %v", err)
	}

	if len(work.audits) != 1 {
		t.Fatalf("audit events = %d, want 1", len(work.audits))
	}
	if work.audits[0].Action != traits.AuditActionApprovalGranted {
		t.Fatalf("audit action = %q, want %q", work.audits[0].Action, traits.AuditActionApprovalGranted)
	}
	if work.audits[0].Outcome != traits.AuditOutcomeSuccess {
		t.Fatalf("audit outcome = %q, want %q", work.audits[0].Outcome, traits.AuditOutcomeSuccess)
	}
}

func TestCancelRunRecordsCancellationAndQueuesSignal(t *testing.T) {
	t.Parallel()

	ctx := authenticatedContext()
	now := time.Date(2026, 7, 2, 10, 45, 0, 0, time.UTC)
	runs := &recordingTemplateRunRepository{run: traits.TemplateRun{ID: "run_123", TenantID: "tenant_123", StackTemplateID: "stack_template_123"}}
	work := &recordingUnitOfWork{templateRuns: runs}

	service := NewService(Service{
		Authorizer:     &permissionAuthorizer{allowed: true},
		Work:           work,
		TemplateRuns:   runs,
		StackTemplates: &recordingStackTemplateRepository{stackTemplate: traits.StackTemplate{ID: "stack_template_123", StackID: "stack_123"}},
		Clock:          fixedClock{now: now},
	})

	err := service.CancelRun(ctx, CancelRunCommand{
		TenantID: traits.TenantID("tenant_123"),
		RunID:    traits.TemplateRunID("run_123"),
		Reason:   "superseded by a newer run",
	})
	if err != nil {
		t.Fatalf("CancelRun returned error: %v", err)
	}

	if runs.cancellation.RunID != traits.TemplateRunID("run_123") {
		t.Fatalf("cancellation run ID = %q, want run_123", runs.cancellation.RunID)
	}

	if runs.cancellation.RequestedBy != traits.UserID(keycloakSubject) {
		t.Fatalf("cancellation actor = %q, want %q", runs.cancellation.RequestedBy, keycloakSubject)
	}

	if runs.cancellation.Reason != "superseded by a newer run" {
		t.Fatalf("cancellation reason = %q", runs.cancellation.Reason)
	}

	if !runs.cancellation.RequestedAt.Equal(now) {
		t.Fatalf("cancellation time = %v, want %v", runs.cancellation.RequestedAt, now)
	}

	if len(work.requests) != 1 || work.requests[0].Kind != KindSignalRunCancellation {
		t.Fatalf("queued requests = %#v, want one cancellation signal", work.requests)
	}
}

func TestCancelRunDoesNotReconcileWorkflowInline(t *testing.T) {
	t.Parallel()

	runs := &recordingTemplateRunRepository{run: traits.TemplateRun{ID: "run_123", TenantID: "tenant_123", StackTemplateID: "stack_template_123"}}
	work := &recordingUnitOfWork{templateRuns: runs}
	service := NewService(Service{
		Authorizer:     &permissionAuthorizer{allowed: true},
		Work:           work,
		TemplateRuns:   runs,
		StackTemplates: &recordingStackTemplateRepository{stackTemplate: traits.StackTemplate{ID: "stack_template_123", TenantID: "tenant_123", StackID: "stack_123"}},
		Clock:          fixedClock{now: time.Now()},
	})

	if err := service.CancelRun(authenticatedContext(), CancelRunCommand{TenantID: "tenant_123", RunID: "run_123"}); err != nil {
		t.Fatalf("CancelRun returned error: %v", err)
	}
	if runs.reconciledRunID != "" {
		t.Fatalf("reconciled run ID = %q, want no inline reconciliation", runs.reconciledRunID)
	}
}

func TestCancelRunDoesNotSignalWhenRunIsNotCancelable(t *testing.T) {
	t.Parallel()

	runs := &recordingTemplateRunRepository{run: traits.TemplateRun{ID: "run_123", TenantID: "tenant_123", StackTemplateID: "stack_template_123"}, cancellationErr: ErrRunNotCancelable}
	work := &recordingUnitOfWork{templateRuns: runs}

	service := NewService(Service{
		Authorizer:     &permissionAuthorizer{allowed: true},
		Work:           work,
		TemplateRuns:   runs,
		StackTemplates: &recordingStackTemplateRepository{stackTemplate: traits.StackTemplate{ID: "stack_template_123", TenantID: "tenant_123", StackID: "stack_123"}},
		Clock:          fixedClock{now: time.Now()},
	})

	err := service.CancelRun(authenticatedContext(), CancelRunCommand{
		TenantID: traits.TenantID("tenant_123"),
		RunID:    traits.TemplateRunID("run_123"),
	})
	if !errors.Is(err, ErrRunNotCancelable) {
		t.Fatalf("error = %v, want ErrRunNotCancelable", err)
	}

	if len(work.requests) != 0 {
		t.Fatalf("queued requests = %#v, want none", work.requests)
	}
}

func TestGetTemplateRunReturnsTenantScopedRun(t *testing.T) {
	t.Parallel()

	runs := &recordingTemplateRunRepository{
		run: traits.TemplateRun{
			ID:              traits.TemplateRunID("run_123"),
			TenantID:        traits.TenantID("tenant_123"),
			StackTemplateID: traits.StackTemplateID("stack_template_123"),
			Operation:       traits.OperationPlan,
			Status:          traits.TemplateRunCompleted,
		},
	}
	service := NewService(Service{
		Authorizer:     &permissionAuthorizer{allowed: true},
		TemplateRuns:   runs,
		StackTemplates: &recordingStackTemplateRepository{stackTemplate: traits.StackTemplate{ID: "stack_template_123", TenantID: "tenant_123", StackID: "stack_123"}},
	})
	ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: keycloakSubject})

	run, err := service.GetTemplateRun(ctx, GetTemplateRunCommand{
		TenantID: traits.TenantID("tenant_123"),
		RunID:    traits.TemplateRunID("run_123"),
	})
	if err != nil {
		t.Fatalf("GetTemplateRun returned error: %v", err)
	}

	if run.ID != traits.TemplateRunID("run_123") {
		t.Fatalf("run ID = %q, want run_123", run.ID)
	}
	if runs.gotGetTenantID != traits.TenantID("tenant_123") {
		t.Fatalf("tenant lookup = %q, want tenant_123", runs.gotGetTenantID)
	}
	if runs.gotGetRunID != traits.TemplateRunID("run_123") {
		t.Fatalf("run lookup = %q, want run_123", runs.gotGetRunID)
	}
}

func TestListTemplateRunsReturnsRunsScopedToStackTemplate(t *testing.T) {
	t.Parallel()

	runs := &recordingTemplateRunRepository{
		list: []traits.TemplateRun{
			{ID: traits.TemplateRunID("run_newer"), TenantID: traits.TenantID("tenant_123"), StackTemplateID: traits.StackTemplateID("stack_template_123"), Operation: traits.OperationApply, Status: traits.TemplateRunWaitingApproval},
			{ID: traits.TemplateRunID("run_older"), TenantID: traits.TenantID("tenant_123"), StackTemplateID: traits.StackTemplateID("stack_template_123"), Operation: traits.OperationPlan, Status: traits.TemplateRunCompleted},
		},
	}
	service := NewService(Service{
		Authorizer:     &permissionAuthorizer{allowed: true},
		TemplateRuns:   runs,
		StackTemplates: &recordingStackTemplateRepository{stackTemplate: traits.StackTemplate{ID: "stack_template_123", TenantID: "tenant_123", StackID: "stack_123"}},
	})
	ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: keycloakSubject})

	got, err := service.ListTemplateRuns(ctx, ListTemplateRunsCommand{
		TenantID:        traits.TenantID("tenant_123"),
		StackTemplateID: traits.StackTemplateID("stack_template_123"),
	})
	if err != nil {
		t.Fatalf("ListTemplateRuns returned error: %v", err)
	}

	if runs.gotListTenantID != traits.TenantID("tenant_123") {
		t.Fatalf("tenant lookup = %q, want tenant_123", runs.gotListTenantID)
	}
	if runs.gotListStackTemplateID != traits.StackTemplateID("stack_template_123") {
		t.Fatalf("stack template lookup = %q, want stack_template_123", runs.gotListStackTemplateID)
	}
	if len(got) != 2 || got[0].ID != traits.TemplateRunID("run_newer") {
		t.Fatalf("runs = %#v, want run_newer first", got)
	}
}

func TestListTemplateRunsNormalizesNilAndRequiresStackTemplateID(t *testing.T) {
	t.Parallel()

	service := NewService(Service{
		Authorizer:     &permissionAuthorizer{allowed: true},
		TemplateRuns:   &recordingTemplateRunRepository{},
		StackTemplates: &recordingStackTemplateRepository{stackTemplate: traits.StackTemplate{ID: "stack_template_123", TenantID: "tenant_123", StackID: "stack_123"}},
	})
	ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: keycloakSubject})

	got, err := service.ListTemplateRuns(ctx, ListTemplateRunsCommand{
		TenantID:        traits.TenantID("tenant_123"),
		StackTemplateID: traits.StackTemplateID("stack_template_123"),
	})
	if err != nil {
		t.Fatalf("ListTemplateRuns returned error: %v", err)
	}
	if got == nil {
		t.Fatal("runs = nil, want empty slice")
	}
	if len(got) != 0 {
		t.Fatalf("len(runs) = %d, want 0", len(got))
	}

	_, err = service.ListTemplateRuns(ctx, ListTemplateRunsCommand{TenantID: traits.TenantID("tenant_123")})
	if !errors.Is(err, ErrInvalidCommand) {
		t.Fatalf("error = %v, want ErrInvalidCommand", err)
	}
}

func TestGetTemplateRunRejectsMissingRunID(t *testing.T) {
	t.Parallel()

	service := NewService(Service{TemplateRuns: &recordingTemplateRunRepository{}})

	_, err := service.GetTemplateRun(context.Background(), GetTemplateRunCommand{
		TenantID: traits.TenantID("tenant_123"),
	})
	if !errors.Is(err, ErrInvalidCommand) {
		t.Fatalf("error = %v, want ErrInvalidCommand", err)
	}
}

func TestGetTemplateRegistrationReturnsTenantScopedRegistration(t *testing.T) {
	t.Parallel()

	registrations := &recordingTemplateRegistrationRepository{
		registration: traits.TemplateRegistration{
			ID:       traits.TemplateRegistrationID("template_registration_123"),
			TenantID: traits.TenantID("tenant_123"),
			Status:   traits.TemplateRegistrationCompleted,
		},
	}
	service := NewService(Service{TemplateRegistrations: registrations, Authorizer: testPlatformAuthorizer()})

	registration, err := service.GetTemplateRegistration(authenticatedContext(), GetTemplateRegistrationCommand{
		TenantID:       traits.TenantID("tenant_123"),
		RegistrationID: traits.TemplateRegistrationID("template_registration_123"),
	})
	if err != nil {
		t.Fatalf("GetTemplateRegistration returned error: %v", err)
	}

	if registration.ID != traits.TemplateRegistrationID("template_registration_123") {
		t.Fatalf("registration ID = %q, want template_registration_123", registration.ID)
	}
	if registrations.gotGetTenantID != traits.TenantID("tenant_123") {
		t.Fatalf("tenant lookup = %q, want tenant_123", registrations.gotGetTenantID)
	}
}

func TestGetTemplateRevisionVariablesReturnsTenantScopedVariables(t *testing.T) {
	t.Parallel()

	templates := &recordingTemplateRepository{
		variables: []traits.TemplateVariable{
			{
				TemplateRevisionID: traits.TemplateRevisionID("template_123"),
				Name:               "region",
				TypeExpression:     "string",
				Required:           true,
			},
		},
	}
	service := NewService(Service{TemplateRevisions: templates, Authorizer: testPlatformAuthorizer()})

	variables, err := service.GetTemplateRevisionVariables(authenticatedContext(), GetTemplateRevisionVariablesCommand{
		TenantID:           traits.TenantID("tenant_123"),
		TemplateRevisionID: traits.TemplateRevisionID("template_123"),
	})
	if err != nil {
		t.Fatalf("GetTemplateRevisionVariables returned error: %v", err)
	}

	if len(variables) != 1 {
		t.Fatalf("len(variables) = %d, want 1", len(variables))
	}
	if variables[0].Name != "region" {
		t.Fatalf("variable name = %q, want region", variables[0].Name)
	}
	if templates.gotVariablesTenantID != traits.TenantID("tenant_123") {
		t.Fatalf("tenant lookup = %q, want tenant_123", templates.gotVariablesTenantID)
	}
}

func TestGetTemplateRunLogChecksRunOwnershipBeforeReadingLog(t *testing.T) {
	t.Parallel()

	runs := &recordingTemplateRunRepository{
		run: traits.TemplateRun{
			ID:              traits.TemplateRunID("run_123"),
			TenantID:        traits.TenantID("tenant_123"),
			StackTemplateID: traits.StackTemplateID("stack_template_123"),
		},
	}
	logs := &recordingTemplateRunLogReader{content: []byte("plan output\n")}
	metadata := &recordingTemplateRunLogRepository{
		log: traits.TemplateRunLog{
			TenantID:  traits.TenantID("tenant_123"),
			RunID:     traits.TemplateRunID("run_123"),
			Phase:     "plan",
			ObjectKey: "tenants/tenant_123/runs/run_123/logs/plan.log",
		},
	}
	service := NewService(Service{
		Authorizer:             &permissionAuthorizer{allowed: true},
		TemplateRuns:           runs,
		StackTemplates:         &recordingStackTemplateRepository{stackTemplate: traits.StackTemplate{ID: "stack_template_123", StackID: "stack_123"}},
		TemplateRunLogs:        logs,
		TemplateRunLogMetadata: metadata,
	})

	content, err := service.GetTemplateRunLog(authenticatedContext(), GetTemplateRunLogCommand{
		TenantID: traits.TenantID("tenant_123"),
		RunID:    traits.TemplateRunID("run_123"),
		Phase:    "plan",
	})
	if err != nil {
		t.Fatalf("GetTemplateRunLog returned error: %v", err)
	}

	if string(content) != "plan output\n" {
		t.Fatalf("content = %q, want plan output", string(content))
	}
	if runs.gotGetRunID != traits.TemplateRunID("run_123") {
		t.Fatalf("run lookup = %q, want run_123", runs.gotGetRunID)
	}
	if logs.gotTenantID != traits.TenantID("tenant_123") {
		t.Fatalf("log tenant = %q, want tenant_123", logs.gotTenantID)
	}
	if logs.gotRunID != traits.TemplateRunID("run_123") {
		t.Fatalf("log run = %q, want run_123", logs.gotRunID)
	}
	if logs.gotPhase != "plan" {
		t.Fatalf("log phase = %q, want plan", logs.gotPhase)
	}
	if logs.gotObjectKey != "tenants/tenant_123/runs/run_123/logs/plan.log" {
		t.Fatalf("log object key = %q, want metadata object key", logs.gotObjectKey)
	}
	if metadata.gotGetPhase != "plan" {
		t.Fatalf("metadata phase = %q, want plan", metadata.gotGetPhase)
	}
}

func TestGetTemplateRunLogDoesNotReadLogWhenRunIsMissing(t *testing.T) {
	t.Parallel()

	runs := &recordingTemplateRunRepository{getErr: ErrNotFound}
	logs := &recordingTemplateRunLogReader{content: []byte("plan output\n")}
	service := NewService(Service{
		Authorizer:             &permissionAuthorizer{allowed: true},
		TemplateRuns:           runs,
		TemplateRunLogs:        logs,
		TemplateRunLogMetadata: &recordingTemplateRunLogRepository{},
	})

	_, err := service.GetTemplateRunLog(authenticatedContext(), GetTemplateRunLogCommand{
		TenantID: traits.TenantID("tenant_123"),
		RunID:    traits.TemplateRunID("run_123"),
		Phase:    "plan",
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}
	if logs.gotRunID != "" {
		t.Fatalf("log reader run ID = %q, want no log read", logs.gotRunID)
	}
}

func TestGetTemplateRunLogDoesNotReadObjectWhenMetadataIsMissing(t *testing.T) {
	t.Parallel()

	runs := &recordingTemplateRunRepository{
		run: traits.TemplateRun{
			ID:              traits.TemplateRunID("run_123"),
			TenantID:        traits.TenantID("tenant_123"),
			StackTemplateID: traits.StackTemplateID("stack_template_123"),
		},
	}
	logs := &recordingTemplateRunLogReader{content: []byte("plan output\n")}
	metadata := &recordingTemplateRunLogRepository{getErr: ErrNotFound}
	service := NewService(Service{
		Authorizer:             &permissionAuthorizer{allowed: true},
		TemplateRuns:           runs,
		StackTemplates:         &recordingStackTemplateRepository{stackTemplate: traits.StackTemplate{ID: "stack_template_123", TenantID: "tenant_123", StackID: "stack_123"}},
		TemplateRunLogs:        logs,
		TemplateRunLogMetadata: metadata,
	})

	ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: keycloakSubject})
	_, err := service.GetTemplateRunLog(ctx, GetTemplateRunLogCommand{
		TenantID: traits.TenantID("tenant_123"),
		RunID:    traits.TemplateRunID("run_123"),
		Phase:    "plan",
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}
	if logs.gotRunID != "" {
		t.Fatalf("log reader run ID = %q, want no object read", logs.gotRunID)
	}
}

func TestGetTemplateRunLogMapsMissingLogToNotFound(t *testing.T) {
	t.Parallel()

	runs := &recordingTemplateRunRepository{
		run: traits.TemplateRun{
			ID:              traits.TemplateRunID("run_123"),
			TenantID:        traits.TenantID("tenant_123"),
			StackTemplateID: traits.StackTemplateID("stack_template_123"),
		},
	}
	logs := &recordingTemplateRunLogReader{err: os.ErrNotExist}
	metadata := &recordingTemplateRunLogRepository{
		log: traits.TemplateRunLog{
			TenantID:  traits.TenantID("tenant_123"),
			RunID:     traits.TemplateRunID("run_123"),
			Phase:     "plan",
			ObjectKey: "tenants/tenant_123/runs/run_123/logs/plan.log",
		},
	}
	service := NewService(Service{
		Authorizer:             &permissionAuthorizer{allowed: true},
		TemplateRuns:           runs,
		StackTemplates:         &recordingStackTemplateRepository{stackTemplate: traits.StackTemplate{ID: "stack_template_123", StackID: "stack_123"}},
		TemplateRunLogs:        logs,
		TemplateRunLogMetadata: metadata,
	})

	_, err := service.GetTemplateRunLog(authenticatedContext(), GetTemplateRunLogCommand{
		TenantID: traits.TenantID("tenant_123"),
		RunID:    traits.TemplateRunID("run_123"),
		Phase:    "plan",
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}
}

func TestGetTemplateRunLogRejectsUnsafePhase(t *testing.T) {
	t.Parallel()

	service := NewService(Service{
		TemplateRuns:           &recordingTemplateRunRepository{},
		TemplateRunLogs:        &recordingTemplateRunLogReader{},
		TemplateRunLogMetadata: &recordingTemplateRunLogRepository{},
	})

	_, err := service.GetTemplateRunLog(context.Background(), GetTemplateRunLogCommand{
		TenantID: traits.TenantID("tenant_123"),
		RunID:    traits.TemplateRunID("run_123"),
		Phase:    "../plan",
	})
	if !errors.Is(err, ErrInvalidCommand) {
		t.Fatalf("error = %v, want ErrInvalidCommand", err)
	}
}

func TestListTemplateRunLogsChecksRunOwnershipBeforeListingMetadata(t *testing.T) {
	t.Parallel()

	runs := &recordingTemplateRunRepository{
		run: traits.TemplateRun{
			ID:       traits.TemplateRunID("run_123"),
			TenantID: traits.TenantID("tenant_123"),
		},
	}
	metadata := &recordingTemplateRunLogRepository{
		logs: []traits.TemplateRunLog{
			{
				TenantID:    traits.TenantID("tenant_123"),
				RunID:       traits.TemplateRunID("run_123"),
				Phase:       "init",
				ObjectKey:   "tenants/tenant_123/runs/run_123/logs/init.log",
				ContentType: "text/plain; charset=utf-8",
				SizeBytes:   12,
				UploadedAt:  time.Date(2026, 7, 6, 10, 15, 0, 0, time.UTC),
			},
		},
	}
	service := NewService(Service{
		Authorizer:             &permissionAuthorizer{allowed: true},
		TemplateRuns:           runs,
		StackTemplates:         &recordingStackTemplateRepository{stackTemplate: traits.StackTemplate{ID: "stack_template_123", TenantID: "tenant_123", StackID: "stack_123"}},
		TemplateRunLogMetadata: metadata,
	})

	logs, err := service.ListTemplateRunLogs(authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: keycloakSubject}), ListTemplateRunLogsCommand{
		TenantID: traits.TenantID("tenant_123"),
		RunID:    traits.TemplateRunID("run_123"),
	})
	if err != nil {
		t.Fatalf("ListTemplateRunLogs returned error: %v", err)
	}

	if len(logs) != 1 {
		t.Fatalf("len(logs) = %d, want 1", len(logs))
	}
	if metadata.gotListRunID != traits.TemplateRunID("run_123") {
		t.Fatalf("metadata run lookup = %q, want run_123", metadata.gotListRunID)
	}
}

type recordingAuditRepository struct {
	events []traits.SecurityAuditEvent
}

func (repository *recordingAuditRepository) AppendAuditEvent(_ context.Context, event traits.SecurityAuditEvent) error {
	repository.events = append(repository.events, event)
	return nil
}

type failingAuditRepository struct{}

func (failingAuditRepository) AppendAuditEvent(_ context.Context, _ traits.SecurityAuditEvent) error {
	return errors.New("database unavailable")
}

func TestCreateStackAuditsOwnerGrant(t *testing.T) {
	t.Parallel()

	ctx := authenticatedContext()
	now := time.Date(2026, 7, 6, 13, 30, 0, 0, time.UTC)
	work := newRecordingWork(&recordingStackRepository{})
	service := NewService(Service{
		Stacks:     &recordingStackRepository{},
		Work:       work,
		Authorizer: &recordingAuthorizer{tiers: testPlatformAuthorizer()},
		StackIDs:   fixedStackIDGenerator{id: traits.StackID("stack_123")},
		Clock:      fixedClock{now: now},
	})

	_, err := service.CreateStack(ctx, CreateStackCommand{
		TenantID: traits.TenantID("tenant_123"),
		Name:     "Acme Prod",
		Tags:     map[string]string{"env": "prod"},
	})
	if err != nil {
		t.Fatalf("CreateStack returned error: %v", err)
	}

	if len(work.audits) != 1 {
		t.Fatalf("audit events = %d, want 1", len(work.audits))
	}
	event := work.audits[0]
	if event.ActorSubject != keycloakSubject {
		t.Fatalf("actor_subject = %q, want %q", event.ActorSubject, keycloakSubject)
	}
	if event.Action != traits.AuditActionGrant {
		t.Fatalf("action = %q, want %q", event.Action, traits.AuditActionGrant)
	}
	if event.TargetUser != keycloakSubject {
		t.Fatalf("target_user = %q, want %q", event.TargetUser, keycloakSubject)
	}
	if event.TenantID != traits.TenantID("tenant_123") {
		t.Fatalf("tenant_id = %q, want tenant_123", event.TenantID)
	}
	if event.StackID != traits.StackID("stack_123") {
		t.Fatalf("stack_id = %q, want stack_123", event.StackID)
	}
	if event.NewRole != "owner" {
		t.Fatalf("new_role = %q, want owner", event.NewRole)
	}
	if event.Outcome != traits.AuditOutcomeSuccess {
		t.Fatalf("outcome = %q, want %q", event.Outcome, traits.AuditOutcomeSuccess)
	}
}

func TestCreateStackAuditWriteFailureDoesNotBlockMutation(t *testing.T) {
	t.Parallel()

	ctx := authenticatedContext()
	service := NewService(Service{
		Stacks:     &recordingStackRepository{},
		Work:       newRecordingWork(&recordingStackRepository{}),
		Authorizer: &recordingAuthorizer{tiers: testPlatformAuthorizer()},
		StackIDs:   fixedStackIDGenerator{id: traits.StackID("stack_123")},
		Clock:      fixedClock{now: time.Now()},
		Audit:      failingAuditRepository{},
	})

	stack, err := service.CreateStack(ctx, CreateStackCommand{
		TenantID: traits.TenantID("tenant_123"),
		Name:     "Acme Prod",
		Tags:     map[string]string{},
	})
	if err != nil {
		t.Fatalf("CreateStack returned error: %v", err)
	}
	if stack.ID != traits.StackID("stack_123") {
		t.Fatalf("stack ID = %q, want stack_123", stack.ID)
	}
}

func TestCreateStackWithNilAuditRepositoryDoesNotPanic(t *testing.T) {
	t.Parallel()

	ctx := authenticatedContext()
	service := NewService(Service{
		Stacks:     &recordingStackRepository{},
		Work:       newRecordingWork(&recordingStackRepository{}),
		Authorizer: &recordingAuthorizer{tiers: testPlatformAuthorizer()},
		StackIDs:   fixedStackIDGenerator{id: traits.StackID("stack_123")},
		Clock:      fixedClock{now: time.Now()},
		Audit:      nil,
	})

	_, err := service.CreateStack(ctx, CreateStackCommand{
		TenantID: traits.TenantID("tenant_123"),
		Name:     "Acme Prod",
		Tags:     map[string]string{},
	})
	if err != nil {
		t.Fatalf("CreateStack returned error: %v", err)
	}
}

type denyingAuthorizer struct{}

func (denyingAuthorizer) Check(_ context.Context, _ authz.CheckRequest) (authz.CheckResult, error) {
	return authz.CheckResult{Allowed: false}, nil
}
func (denyingAuthorizer) BatchCheck(_ context.Context, _ authz.BatchCheckRequest) (authz.BatchCheckResult, error) {
	return authz.BatchCheckResult{}, nil
}
func (denyingAuthorizer) ListGrants(_ context.Context, _ authz.ListGrantsRequest) (authz.ListGrantsResult, error) {
	return authz.ListGrantsResult{}, nil
}
func (denyingAuthorizer) WriteRelationships(_ context.Context, _ authz.Mutation) error {
	return nil
}
func (denyingAuthorizer) DeleteRelationships(_ context.Context, _ authz.Mutation) error {
	return nil
}

func TestAddTemplateToStackAuditsAuthorizationDenial(t *testing.T) {
	t.Parallel()

	audit := &recordingAuditRepository{}
	service := NewService(Service{
		Authorizer: &denyingAuthorizer{},
		Stacks: &recordingStackRepository{
			stack: traits.Stack{ID: "stack_123", TenantID: "tenant_123", Slug: "acme-prod"},
		},
		TemplateRevisionMetadata: &recordingTemplateRepository{
			template: traits.TemplateRevision{ID: "template_123", TenantID: "tenant_123", Status: traits.TemplateRevisionActive},
		},
		TemplateRevisions: &recordingTemplateRepository{
			variables: []traits.TemplateVariable{{Name: "region", Required: true, HasDefault: true}},
		},
		StackTemplateInstaller: &recordingStackTemplateInstaller{},
		StackTemplateIDs:       fixedStackTemplateIDGenerator{id: traits.StackTemplateID("stack_template_a1b2c3d4")},
		Audit:                  audit,
	})

	ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{
		Subject: keycloakSubject,
	})

	_, err := service.AddTemplateToStack(ctx, AddTemplateToStackCommand{
		TenantID:           traits.TenantID("tenant_123"),
		StackID:            traits.StackID("stack_123"),
		TemplateRevisionID: traits.TemplateRevisionID("template_123"),
		ConfigJSON:         json.RawMessage(`{"region":"us-east-1"}`),
	})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("error = %v, want ErrForbidden", err)
	}

	if len(audit.events) != 1 {
		t.Fatalf("audit events = %d, want 1", len(audit.events))
	}
	event := audit.events[0]
	if event.Action != traits.AuditActionFailedAccessAttempt {
		t.Fatalf("action = %q, want %q", event.Action, traits.AuditActionFailedAccessAttempt)
	}
	if event.Outcome != traits.AuditOutcomeFailure {
		t.Fatalf("outcome = %q, want %q", event.Outcome, traits.AuditOutcomeFailure)
	}
	if event.ActorSubject != keycloakSubject {
		t.Fatalf("actor_subject = %q, want %q", event.ActorSubject, keycloakSubject)
	}
}

type recordingStackTemplateRepository struct {
	stackTemplate                traits.StackTemplate
	gotTenantID                  traits.TenantID
	gotID                        traits.StackTemplateID
	gotConfigJSON                json.RawMessage
	gotDesiredTemplateRevisionID traits.TemplateRevisionID
}

func (repository *recordingStackTemplateRepository) GetStackTemplate(_ context.Context, tenantID traits.TenantID, id traits.StackTemplateID) (traits.StackTemplate, error) {
	repository.gotTenantID = tenantID
	repository.gotID = id
	stackTemplate := repository.stackTemplate
	if stackTemplate.StackID == "" {
		stackTemplate.StackID = "stack_123"
	}
	return stackTemplate, nil
}

func (repository *recordingStackTemplateRepository) UpdateStackTemplateConfig(_ context.Context, tenantID traits.TenantID, id traits.StackTemplateID, configJSON json.RawMessage) (traits.StackTemplate, error) {
	repository.gotTenantID = tenantID
	repository.gotID = id
	repository.gotConfigJSON = configJSON
	updated := repository.stackTemplate
	updated.DesiredConfigJSON = configJSON
	return updated, nil
}

func (repository *recordingStackTemplateRepository) UpdateStackTemplateDesiredRevision(_ context.Context, tenantID traits.TenantID, id traits.StackTemplateID, templateRevisionID traits.TemplateRevisionID, configJSON json.RawMessage) (traits.StackTemplate, error) {
	repository.gotTenantID = tenantID
	repository.gotID = id
	repository.gotDesiredTemplateRevisionID = templateRevisionID
	repository.gotConfigJSON = configJSON
	updated := repository.stackTemplate
	updated.DesiredTemplateRevisionID = templateRevisionID
	updated.DesiredConfigJSON = configJSON
	return updated, nil
}

type recordingStackRepository struct {
	created         traits.Stack
	stack           traits.Stack
	list            []traits.Stack
	view            StackView
	gotTenantID     traits.TenantID
	gotStackID      traits.StackID
	gotListTenantID traits.TenantID
	gotListStackIDs []traits.StackID
	createErr       error
	getErr          error
	listErr         error
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

func (repository *recordingStackRepository) ListStacks(_ context.Context, tenantID traits.TenantID) ([]traits.Stack, error) {
	repository.gotListTenantID = tenantID
	if repository.listErr != nil {
		return nil, repository.listErr
	}
	return repository.list, nil
}

func (repository *recordingStackRepository) ListStacksPage(_ context.Context, tenantID traits.TenantID, after *StackPageCursor, limit int) ([]traits.Stack, error) {
	repository.gotListTenantID = tenantID
	if repository.listErr != nil {
		return nil, repository.listErr
	}
	start := 0
	if after != nil {
		for i, stack := range repository.list {
			if stack.ID == after.ID && stack.CreatedAt.Equal(after.CreatedAt) {
				start = i + 1
				break
			}
		}
	}
	end := min(start+limit, len(repository.list))
	return append([]traits.Stack(nil), repository.list[start:end]...), nil
}

type recordingTemplateRunRepository struct {
	created                traits.TemplateRun
	run                    traits.TemplateRun
	list                   []traits.TemplateRun
	approval               traits.TemplateRunApproval
	cancellation           traits.TemplateRunCancellation
	gotGetTenantID         traits.TenantID
	gotGetRunID            traits.TemplateRunID
	gotListTenantID        traits.TenantID
	gotListStackTemplateID traits.StackTemplateID
	getErr                 error
	approvalErr            error
	cancellationErr        error
	reconciledRunID        traits.TemplateRunID
	reconciledSummary      string
}

func (repository *recordingTemplateRunRepository) CreateTemplateRun(_ context.Context, run traits.TemplateRun) error {
	repository.created = run
	return nil
}

func (repository *recordingTemplateRunRepository) GetTemplateRun(_ context.Context, tenantID traits.TenantID, runID traits.TemplateRunID) (traits.TemplateRun, error) {
	repository.gotGetTenantID = tenantID
	repository.gotGetRunID = runID
	if repository.getErr != nil {
		return traits.TemplateRun{}, repository.getErr
	}
	return repository.run, nil
}

func (repository *recordingTemplateRunRepository) ListTemplateRuns(_ context.Context, tenantID traits.TenantID, stackTemplateID traits.StackTemplateID) ([]traits.TemplateRun, error) {
	repository.gotListTenantID = tenantID
	repository.gotListStackTemplateID = stackTemplateID
	return repository.list, nil
}

func (repository *recordingTemplateRunRepository) ApproveTemplateRun(_ context.Context, approval traits.TemplateRunApproval) error {
	if repository.approvalErr != nil {
		return repository.approvalErr
	}
	repository.approval = approval
	return nil
}

func (repository *recordingTemplateRunRepository) RequestTemplateRunCancellation(_ context.Context, cancellation traits.TemplateRunCancellation) error {
	if repository.cancellationErr != nil {
		return repository.cancellationErr
	}
	repository.cancellation = cancellation
	return nil
}

func (repository *recordingTemplateRunRepository) ReconcileTemplateRunCancellation(_ context.Context, _ traits.TenantID, runID traits.TemplateRunID, errorSummary string) error {
	repository.reconciledRunID = runID
	repository.reconciledSummary = errorSummary
	return nil
}

type recordingTemplateRunLogReader struct {
	content      []byte
	err          error
	gotTenantID  traits.TenantID
	gotRunID     traits.TemplateRunID
	gotPhase     string
	gotObjectKey string
}

func (reader *recordingTemplateRunLogReader) ReadTemplateRunLog(_ context.Context, log traits.TemplateRunLog) ([]byte, error) {
	reader.gotTenantID = log.TenantID
	reader.gotRunID = log.RunID
	reader.gotPhase = log.Phase
	reader.gotObjectKey = log.ObjectKey
	if reader.err != nil {
		return nil, reader.err
	}
	return reader.content, nil
}

type recordingTemplateRunLogRepository struct {
	log             traits.TemplateRunLog
	logs            []traits.TemplateRunLog
	gotGetTenantID  traits.TenantID
	gotGetRunID     traits.TemplateRunID
	gotGetPhase     string
	gotListTenantID traits.TenantID
	gotListRunID    traits.TemplateRunID
	getErr          error
	listErr         error
}

func (repository *recordingTemplateRunLogRepository) GetTemplateRunLog(_ context.Context, tenantID traits.TenantID, runID traits.TemplateRunID, phase string) (traits.TemplateRunLog, error) {
	repository.gotGetTenantID = tenantID
	repository.gotGetRunID = runID
	repository.gotGetPhase = phase
	if repository.getErr != nil {
		return traits.TemplateRunLog{}, repository.getErr
	}
	return repository.log, nil
}

func (repository *recordingTemplateRunLogRepository) ListTemplateRunLogs(_ context.Context, tenantID traits.TenantID, runID traits.TemplateRunID) ([]traits.TemplateRunLog, error) {
	repository.gotListTenantID = tenantID
	repository.gotListRunID = runID
	if repository.listErr != nil {
		return nil, repository.listErr
	}
	return repository.logs, nil
}

type recordingTemplateRegistrationRepository struct {
	created        traits.TemplateRegistration
	registration   traits.TemplateRegistration
	gotGetTenantID traits.TenantID
	gotGetID       traits.TemplateRegistrationID
	createErr      error
	getErr         error
	statusInput    traits.TemplateRegistrationStatusActivityInput
	statusErr      error
}

func (repository *recordingTemplateRegistrationRepository) CreateTemplateRegistration(_ context.Context, registration traits.TemplateRegistration) error {
	if repository.createErr != nil {
		return repository.createErr
	}
	repository.created = registration
	return nil
}

func (repository *recordingTemplateRegistrationRepository) GetTemplateRegistration(_ context.Context, tenantID traits.TenantID, id traits.TemplateRegistrationID) (traits.TemplateRegistration, error) {
	repository.gotGetTenantID = tenantID
	repository.gotGetID = id
	if repository.getErr != nil {
		return traits.TemplateRegistration{}, repository.getErr
	}
	return repository.registration, nil
}

func (repository *recordingTemplateRegistrationRepository) RecordTemplateRegistrationStatus(_ context.Context, input traits.TemplateRegistrationStatusActivityInput) error {
	if repository.statusErr != nil {
		return repository.statusErr
	}
	repository.statusInput = input
	return nil
}

type recordingTemplateRepository struct {
	template                       traits.TemplateRevision
	templates                      []traits.TemplateRevision
	variables                      []traits.TemplateVariable
	gotTemplate                    traits.TemplateRevision
	gotVariables                   []traits.TemplateVariable
	gotListTenantID                traits.TenantID
	gotGetTemplateTenantID         traits.TenantID
	gotGetTemplateRevisionID       traits.TemplateRevisionID
	gotVariablesTenantID           traits.TenantID
	gotVariablesTemplateRevisionID traits.TemplateRevisionID
	getTemplateErr                 error
	listErr                        error
	upsertErr                      error
	variablesErr                   error
}

func (repository *recordingTemplateRepository) UpsertTemplateRevisionWithVariables(_ context.Context, template traits.TemplateRevision, variables []traits.TemplateVariable) (traits.TemplateRevision, error) {
	repository.gotTemplate = template
	repository.gotVariables = variables
	if repository.upsertErr != nil {
		return traits.TemplateRevision{}, repository.upsertErr
	}
	if repository.template.ID != "" {
		return repository.template, nil
	}
	return template, nil
}

func (repository *recordingTemplateRepository) GetTemplateRevision(_ context.Context, tenantID traits.TenantID, templateRevisionID traits.TemplateRevisionID) (traits.TemplateRevision, error) {
	repository.gotGetTemplateTenantID = tenantID
	repository.gotGetTemplateRevisionID = templateRevisionID
	if repository.getTemplateErr != nil {
		return traits.TemplateRevision{}, repository.getTemplateErr
	}
	return repository.template, nil
}

func (repository *recordingTemplateRepository) ListTemplateRevisions(_ context.Context, tenantID traits.TenantID) ([]traits.TemplateRevision, error) {
	repository.gotListTenantID = tenantID
	if repository.listErr != nil {
		return nil, repository.listErr
	}
	return repository.templates, nil
}

func (repository *recordingTemplateRepository) GetTemplateRevisionVariables(_ context.Context, tenantID traits.TenantID, templateRevisionID traits.TemplateRevisionID) ([]traits.TemplateVariable, error) {
	repository.gotVariablesTenantID = tenantID
	repository.gotVariablesTemplateRevisionID = templateRevisionID
	if repository.variablesErr != nil {
		return nil, repository.variablesErr
	}
	return repository.variables, nil
}

type recordingWorkflowDispatcher struct {
	input                 traits.TemplateRunWorkflowInput
	startTemplateRunCalls int
	syncInput             traits.TemplateSyncWorkflowInput
	approvalRunID         traits.TemplateRunID
	approvalSignal        traits.ApprovalSignal
	cancelRunID           traits.TemplateRunID
	cancelSignal          traits.CancelSignal
	cancelErr             error
}

func (dispatcher *recordingWorkflowDispatcher) StartTemplateRun(_ context.Context, input traits.TemplateRunWorkflowInput) error {
	dispatcher.startTemplateRunCalls++
	dispatcher.input = input
	return nil
}

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

func (dispatcher *recordingWorkflowDispatcher) StartTemplateSync(_ context.Context, input traits.TemplateSyncWorkflowInput) error {
	dispatcher.syncInput = input
	return nil
}

func (dispatcher *recordingWorkflowDispatcher) ApproveTemplateRun(_ context.Context, _ traits.TenantID, runID traits.TemplateRunID, signal traits.ApprovalSignal) error {
	dispatcher.approvalRunID = runID
	dispatcher.approvalSignal = signal
	return nil
}

func (dispatcher *recordingWorkflowDispatcher) CancelTemplateRun(_ context.Context, _ traits.TenantID, runID traits.TemplateRunID, signal traits.CancelSignal) error {
	if dispatcher.cancelErr != nil {
		return dispatcher.cancelErr
	}
	dispatcher.cancelRunID = runID
	dispatcher.cancelSignal = signal
	return nil
}

type fixedTemplateRunIDGenerator struct {
	runID traits.TemplateRunID
}

type fixedStackTemplateIDGenerator struct {
	id traits.StackTemplateID
}

func (generator fixedStackTemplateIDGenerator) NewStackTemplateID() traits.StackTemplateID {
	return generator.id
}

func (generator fixedTemplateRunIDGenerator) NewTemplateRunID() traits.TemplateRunID {
	return generator.runID
}

type fixedTemplateRegistrationIDGenerator struct {
	id traits.TemplateRegistrationID
}

func (generator fixedTemplateRegistrationIDGenerator) NewTemplateRegistrationID() traits.TemplateRegistrationID {
	return generator.id
}

type fixedStackIDGenerator struct {
	id traits.StackID
}

func (generator fixedStackIDGenerator) NewStackID() traits.StackID {
	return generator.id
}

type fixedClock struct {
	now time.Time
}

func (clock fixedClock) Now() time.Time {
	return clock.now
}

// recordingUnitOfWork stands in for the Postgres unit of work. It applies
// writes immediately rather than staging them: rollback semantics are proven
// against a real database in internal/postgres/unitofwork_test.go, so here it
// only needs to record what the service asked for.
type recordingUnitOfWork struct {
	stacks                StackRepository
	templateRuns          TemplateRunRepository
	templateRegistrations TemplateRegistrationRepository
	audits                []traits.SecurityAuditEvent
	requests              []queue.Request
	err                   error
	inTxCalls             int
}

func newRecordingWork(stacks StackRepository) *recordingUnitOfWork {
	return &recordingUnitOfWork{stacks: stacks}
}

func (unit *recordingUnitOfWork) InTx(ctx context.Context, fn func(TxRepo, queue.Enqueuer) error) error {
	unit.inTxCalls++
	if unit.err != nil {
		return unit.err
	}
	return fn(unit, unit)
}

func (unit *recordingUnitOfWork) CreateStack(ctx context.Context, stack traits.Stack) error {
	if unit.stacks == nil {
		return nil
	}
	return unit.stacks.CreateStack(ctx, stack)
}

func (unit *recordingUnitOfWork) AppendAuditEvent(_ context.Context, event traits.SecurityAuditEvent) error {
	unit.audits = append(unit.audits, event)
	return nil
}

func (unit *recordingUnitOfWork) CreateTemplateRun(ctx context.Context, run traits.TemplateRun) error {
	if unit.templateRuns == nil {
		return nil
	}
	return unit.templateRuns.CreateTemplateRun(ctx, run)
}

func (unit *recordingUnitOfWork) CreateTemplateRegistration(ctx context.Context, registration traits.TemplateRegistration) error {
	if unit.templateRegistrations == nil {
		return nil
	}
	return unit.templateRegistrations.CreateTemplateRegistration(ctx, registration)
}

func (unit *recordingUnitOfWork) ApproveTemplateRun(ctx context.Context, approval traits.TemplateRunApproval) error {
	if unit.templateRuns == nil {
		return nil
	}
	return unit.templateRuns.ApproveTemplateRun(ctx, approval)
}

func (unit *recordingUnitOfWork) RequestTemplateRunCancellation(ctx context.Context, cancellation traits.TemplateRunCancellation) error {
	if unit.templateRuns == nil {
		return nil
	}
	return unit.templateRuns.RequestTemplateRunCancellation(ctx, cancellation)
}

func (unit *recordingUnitOfWork) Enqueue(_ context.Context, requests ...queue.Request) error {
	unit.requests = append(unit.requests, requests...)
	return nil
}

func TestRegisterTemplatePairsRegistrationWithSyncIntentInTransaction(t *testing.T) {
	t.Parallel()

	registrations := &recordingTemplateRegistrationRepository{}
	work := &recordingUnitOfWork{templateRegistrations: registrations}
	service := NewService(Service{
		Authorizer:            testPlatformAuthorizer(),
		Work:                  work,
		TemplateRegistrations: registrations,
		RegistrationIDs:       fixedTemplateRegistrationIDGenerator{id: "registration_123"},
		Clock:                 fixedClock{now: time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC)},
	})

	registration, err := service.RegisterTemplate(authenticatedContext(), RegisterTemplateCommand{
		TenantID: "tenant_123", RepoOwner: "acme", RepoName: "infra", SourceRef: "main", RootPath: "modules/vpc",
	})
	if err != nil {
		t.Fatalf("RegisterTemplate returned error: %v", err)
	}
	if work.inTxCalls != 1 || registrations.created.ID != registration.ID {
		t.Fatalf("transaction calls = %d, registration = %#v", work.inTxCalls, registrations.created)
	}
	if len(work.requests) != 1 || work.requests[0].Kind != KindStartTemplateSync || work.requests[0].ActorSubject != keycloakSubject || work.requests[0].TenantID != "tenant_123" {
		t.Fatalf("requests = %#v", work.requests)
	}
	var payload StartTemplateSyncPayload
	if err := json.Unmarshal(work.requests[0].Payload, &payload); err != nil {
		t.Fatalf("decode sync intent: %v", err)
	}
	if payload.RegistrationID != registration.ID || payload.TenantID != registration.TenantID || payload.RepoOwner != "acme" || payload.RepoName != "infra" || payload.SourceRef != "main" || payload.RootPath != "modules/vpc" {
		t.Fatalf("sync payload = %#v", payload)
	}
}

func TestStartTemplateRunPairsRunWithStartIntentInTransaction(t *testing.T) {
	t.Parallel()

	runs := &recordingTemplateRunRepository{}
	work := &recordingUnitOfWork{templateRuns: runs}
	service := NewService(Service{
		Work:         work,
		Authorizer:   &permissionAuthorizer{allowed: true},
		TemplateRuns: runs,
		StackTemplates: &recordingStackTemplateRepository{stackTemplate: traits.StackTemplate{
			ID: "stack_template_123", TenantID: "tenant_123", SourceTemplateID: "source_123", DesiredTemplateRevisionID: "revision_123", DesiredConfigJSON: json.RawMessage(`{"region":"us-east-1"}`), WorkspaceName: "workspace", Lifecycle: traits.StackTemplateActive,
			LastPlannedRunID: "run_plan_1", LastPlannedTemplateRevisionID: "revision_123", LastPlannedConfigJSON: json.RawMessage(`{"region":"us-east-1"}`),
		}},
		TemplateRevisionMetadata: &recordingTemplateRepository{template: traits.TemplateRevision{ID: "revision_123", TenantID: "tenant_123", SourceTemplateID: "source_123", RepoOwner: "acme", RepoName: "infra", SourceRef: "main", ResolvedCommitSHA: "sha_123", RootPath: "modules/vpc"}},
		RunIDs:                   fixedTemplateRunIDGenerator{runID: "run_123"},
		Clock:                    fixedClock{now: time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC)},
	})

	run, err := service.StartTemplateRun(authenticatedContext(), StartTemplateRunCommand{TenantID: "tenant_123", StackTemplateID: "stack_template_123", Operation: traits.OperationApply})
	if err != nil {
		t.Fatalf("StartTemplateRun returned error: %v", err)
	}
	if work.inTxCalls != 1 || runs.created.ID != run.ID {
		t.Fatalf("transaction calls = %d, run = %#v", work.inTxCalls, runs.created)
	}
	if len(work.requests) != 1 || work.requests[0].Kind != KindStartTemplateRun || work.requests[0].ActorSubject != keycloakSubject || work.requests[0].TenantID != "tenant_123" {
		t.Fatalf("requests = %#v", work.requests)
	}
	var payload StartTemplateRunPayload
	if err := json.Unmarshal(work.requests[0].Payload, &payload); err != nil {
		t.Fatalf("decode start intent: %v", err)
	}
	if payload.RunID != run.ID || payload.TenantID != run.TenantID || payload.StackTemplateID != run.StackTemplateID || payload.Operation != run.Operation || payload.SelectedRef != run.SelectedRef || payload.WorkspaceName != run.WorkspaceName || payload.RepoOwner != "acme" || payload.RepoName != "infra" || payload.RootPath != "modules/vpc" || string(payload.ConfigJSON) != `{"region":"us-east-1"}` {
		t.Fatalf("start payload = %#v", payload)
	}
	// The worker checks out this commit. Without it in the payload the run would
	// fall back to the installed ref, which is a moving target and is never
	// updated when the desired revision changes.
	if payload.ResolvedCommitSHA != "sha_123" {
		t.Fatalf("payload.ResolvedCommitSHA = %q, want sha_123", payload.ResolvedCommitSHA)
	}
}

func TestApproveRunPairsApprovalAuditAndSignalIntentInTransaction(t *testing.T) {
	t.Parallel()

	runs := &recordingTemplateRunRepository{run: traits.TemplateRun{ID: "run_123", TenantID: "tenant_123", StackTemplateID: "stack_template_123"}}
	work := &recordingUnitOfWork{templateRuns: runs}
	workflows := &recordingWorkflowDispatcher{}
	service := NewService(Service{
		Work:           work,
		Authorizer:     &permissionAuthorizer{allowed: true},
		TemplateRuns:   runs,
		StackTemplates: &recordingStackTemplateRepository{stackTemplate: traits.StackTemplate{ID: "stack_template_123", TenantID: "tenant_123", StackID: "stack_123"}},
		Workflows:      workflows,
		Clock:          fixedClock{now: time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC)},
	})

	if err := service.ApproveRun(authenticatedContext(), ApproveRunCommand{TenantID: "tenant_123", RunID: "run_123"}); err != nil {
		t.Fatalf("ApproveRun returned error: %v", err)
	}
	if work.inTxCalls != 1 || runs.approval.RunID != "run_123" || len(work.audits) != 1 {
		t.Fatalf("transaction calls = %d, approval = %#v, audits = %#v", work.inTxCalls, runs.approval, work.audits)
	}
	if len(work.requests) != 1 || work.requests[0].Kind != KindSignalRunApproval || work.requests[0].ActorSubject != keycloakSubject || work.requests[0].TenantID != "tenant_123" || workflows.approvalRunID != "" {
		t.Fatalf("requests = %#v, direct approval = %q", work.requests, workflows.approvalRunID)
	}
	var payload SignalRunApprovalPayload
	if err := json.Unmarshal(work.requests[0].Payload, &payload); err != nil {
		t.Fatalf("decode approval intent: %v", err)
	}
	if payload.TenantID != "tenant_123" || payload.RunID != "run_123" || payload.Signal.ApprovedBy != keycloakSubject {
		t.Fatalf("approval payload = %#v", payload)
	}
}

func TestCancelRunPairsCancellationWithSignalIntentInTransaction(t *testing.T) {
	t.Parallel()

	runs := &recordingTemplateRunRepository{run: traits.TemplateRun{ID: "run_123", TenantID: "tenant_123", StackTemplateID: "stack_template_123"}}
	work := &recordingUnitOfWork{templateRuns: runs}
	workflows := &recordingWorkflowDispatcher{}
	service := NewService(Service{
		Work:           work,
		Authorizer:     &permissionAuthorizer{allowed: true},
		TemplateRuns:   runs,
		StackTemplates: &recordingStackTemplateRepository{stackTemplate: traits.StackTemplate{ID: "stack_template_123", TenantID: "tenant_123", StackID: "stack_123"}},
		Workflows:      workflows,
		Clock:          fixedClock{now: time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC)},
	})

	if err := service.CancelRun(authenticatedContext(), CancelRunCommand{TenantID: "tenant_123", RunID: "run_123", Reason: "superseded"}); err != nil {
		t.Fatalf("CancelRun returned error: %v", err)
	}
	if work.inTxCalls != 1 || runs.cancellation.RunID != "run_123" {
		t.Fatalf("transaction calls = %d, cancellation = %#v", work.inTxCalls, runs.cancellation)
	}
	if len(work.requests) != 1 || work.requests[0].Kind != KindSignalRunCancellation || work.requests[0].ActorSubject != keycloakSubject || work.requests[0].TenantID != "tenant_123" || workflows.cancelRunID != "" {
		t.Fatalf("requests = %#v, direct cancellation = %q", work.requests, workflows.cancelRunID)
	}
	var payload SignalRunCancellationPayload
	if err := json.Unmarshal(work.requests[0].Payload, &payload); err != nil {
		t.Fatalf("decode cancellation intent: %v", err)
	}
	if payload.TenantID != "tenant_123" || payload.RunID != "run_123" || payload.Signal.RequestedBy != keycloakSubject || payload.Signal.Reason != "superseded" {
		t.Fatalf("cancellation payload = %#v", payload)
	}
}

func adminContext() context.Context {
	return authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: "admin_123"})
}

func TestAssignStackRoleEnqueuesDesiredRoleWithoutCallingOpenFGA(t *testing.T) {
	t.Parallel()

	authorizer := &recordingAuthorizer{tiers: testPlatformAuthorizer()}
	work := newRecordingWork(nil)
	users := &fakeUserRepository{users: []UserProfile{{Sub: "user_456", DisplayName: "Casey Jones", Email: "casey@example.com"}}}
	service := NewService(Service{Work: work, Authorizer: authorizer, Users: users, Clock: fixedClock{now: time.Now()}})

	view, err := service.AssignStackRole(adminContext(), AssignStackRoleCommand{
		TenantID: traits.TenantID("tenant_123"),
		StackID:  traits.StackID("stack_abc"),
		UserSub:  "user_456",
		Role:     "operator",
	})
	if err != nil {
		t.Fatalf("AssignStackRole returned error: %v", err)
	}
	if view.Role != "operator" || view.UserSub != "user_456" {
		t.Fatalf("view = %#v", view)
	}

	// The delete-then-write pair is gone: no OpenFGA mutation at request time.
	if authorizer.calls != 0 {
		t.Fatalf("authorization write calls = %d, want 0", authorizer.calls)
	}
	if len(work.requests) != 1 {
		t.Fatalf("enqueued %d requests, want 1", len(work.requests))
	}

	var payload authz.GrantPayload
	if err := json.Unmarshal(work.requests[0].Payload, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload.StackID != "stack_abc" || payload.Subject != "user_456" || payload.Role != "operator" {
		t.Fatalf("payload = %#v", payload)
	}
	if len(work.audits) != 1 || work.audits[0].NewRole != "operator" {
		t.Fatalf("audits = %#v, want one grant event written in the same transaction", work.audits)
	}
}

func TestAssignStackRoleEnqueuesMatchingRole(t *testing.T) {
	t.Parallel()

	stack, err := authz.ObjectFromID(authz.TypeStack, "stack_abc")
	if err != nil {
		t.Fatalf("ObjectFromID: %v", err)
	}
	subject, err := authz.SubjectFromOIDCSub("user_456")
	if err != nil {
		t.Fatalf("SubjectFromOIDCSub: %v", err)
	}
	existing, err := authz.NewGrant(subject, stack, authz.RelationOperator)
	if err != nil {
		t.Fatalf("NewGrant: %v", err)
	}

	authorizer := &recordingAuthorizer{grants: []authz.Grant{existing}, tiers: testPlatformAuthorizer()}
	work := newRecordingWork(nil)
	users := &fakeUserRepository{users: []UserProfile{{Sub: "user_456", DisplayName: "Casey Jones", Email: "casey@example.com"}}}
	service := NewService(Service{Work: work, Authorizer: authorizer, Users: users, Clock: fixedClock{now: time.Now()}})

	if _, err := service.AssignStackRole(adminContext(), AssignStackRoleCommand{
		TenantID: traits.TenantID("tenant_123"),
		StackID:  traits.StackID("stack_abc"),
		UserSub:  "user_456",
		Role:     "operator",
	}); err != nil {
		t.Fatalf("AssignStackRole returned error: %v", err)
	}

	if len(work.requests) != 1 {
		t.Fatalf("enqueued %d requests, want 1", len(work.requests))
	}
	var payload authz.GrantPayload
	if err := json.Unmarshal(work.requests[0].Payload, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload.Role != "operator" {
		t.Fatalf("payload role = %q, want operator", payload.Role)
	}
}

func TestRevokeStackRoleEnqueuesEmptyRole(t *testing.T) {
	t.Parallel()

	stack, err := authz.ObjectFromID(authz.TypeStack, "stack_abc")
	if err != nil {
		t.Fatalf("ObjectFromID: %v", err)
	}
	subject, err := authz.SubjectFromOIDCSub("user_456")
	if err != nil {
		t.Fatalf("SubjectFromOIDCSub: %v", err)
	}
	existing, err := authz.NewGrant(subject, stack, authz.RelationOperator)
	if err != nil {
		t.Fatalf("NewGrant: %v", err)
	}

	authorizer := &recordingAuthorizer{grants: []authz.Grant{existing}, tiers: testPlatformAuthorizer()}
	work := newRecordingWork(nil)
	users := &fakeUserRepository{users: []UserProfile{{Sub: "user_456", DisplayName: "Casey Jones", Email: "casey@example.com"}}}
	service := NewService(Service{Work: work, Authorizer: authorizer, Users: users, Clock: fixedClock{now: time.Now()}})

	if err := service.RevokeStackRole(adminContext(), RevokeStackRoleCommand{
		TenantID: traits.TenantID("tenant_123"),
		StackID:  traits.StackID("stack_abc"),
		UserSub:  "user_456",
	}); err != nil {
		t.Fatalf("RevokeStackRole returned error: %v", err)
	}

	if len(work.requests) != 1 {
		t.Fatalf("enqueued %d requests, want 1", len(work.requests))
	}
	var payload authz.GrantPayload
	if err := json.Unmarshal(work.requests[0].Payload, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload.Role != "" {
		t.Fatalf("payload role = %q, want empty — an empty role means no access", payload.Role)
	}
	if len(work.audits) != 1 || work.audits[0].OldRole != "operator" {
		t.Fatalf("audits = %#v, want one revoke event recording the old role", work.audits)
	}
}

func TestRevokeStackRoleEnqueuesEmptyRoleWhenGrantIsAbsent(t *testing.T) {
	t.Parallel()

	work := newRecordingWork(nil)
	service := NewService(Service{
		Work:       work,
		Authorizer: &recordingAuthorizer{tiers: testPlatformAuthorizer()},
		Clock:      fixedClock{now: time.Now()},
	})

	if err := service.RevokeStackRole(adminContext(), RevokeStackRoleCommand{
		TenantID: traits.TenantID("tenant_123"),
		StackID:  traits.StackID("stack_abc"),
		UserSub:  "user_456",
	}); err != nil {
		t.Fatalf("RevokeStackRole returned error: %v", err)
	}

	if len(work.requests) != 1 {
		t.Fatalf("enqueued %d requests, want 1", len(work.requests))
	}
	var payload authz.GrantPayload
	if err := json.Unmarshal(work.requests[0].Payload, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload.Role != "" {
		t.Fatalf("payload role = %q, want empty", payload.Role)
	}
}

func TestAssignStackRoleWithoutUnitOfWorkFails(t *testing.T) {
	t.Parallel()

	service := NewService(Service{Authorizer: &recordingAuthorizer{tiers: testPlatformAuthorizer()}, Clock: fixedClock{now: time.Now()}})

	_, err := service.AssignStackRole(adminContext(), AssignStackRoleCommand{
		TenantID: traits.TenantID("tenant_123"),
		StackID:  traits.StackID("stack_abc"),
		UserSub:  "user_456",
		Role:     "operator",
	})
	if !errors.Is(err, authz.ErrUnavailable) {
		t.Fatalf("error = %v, want ErrUnavailable", err)
	}
}
