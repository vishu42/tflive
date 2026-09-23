package app

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/vishu42/tflive/internal/authn"
	"github.com/vishu42/tflive/internal/authorization"
	"github.com/vishu42/tflive/internal/domain"
	"github.com/vishu42/tflive/internal/queue"
)

const keycloakSubject = "6fdb4b4c-2a8f-4cf7-945f-38f67f6a0e91"

func authenticatedContext() context.Context {
	return authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: keycloakSubject})
}

func TestActorMutationsRejectMissingPrincipal(t *testing.T) {
	t.Parallel()

	service := NewService(Service{
		TemplateRuns: &recordingTemplateRunRepository{}})
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
		{name: "discard run", call: func() error {
			return service.DiscardRun(context.Background(), DiscardRunCommand{})
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
		Stacks:        stacks,
		Work:          newRecordingWork(stacks),
		Authorization: testPlatformAuthorizer(t),
		StackIDs:      fixedStackIDGenerator{id: domain.StackID("stack_new")},
		Clock:         fixedClock{now: now},
	})

	stack, err := service.CreateStack(ctx, CreateStackCommand{
		TenantID: domain.TenantID("tenant_123"),
		Name:     "Acme Prod",
		Tags: map[string]string{
			"env": "prod",
		},
		DefaultCredentialIDs: []domain.CredentialSetID{domain.CredentialSetID("credential_123")},
	})
	if err != nil {
		t.Fatalf("CreateStack returned error: %v", err)
	}

	if stack.ID != domain.StackID("stack_new") {
		t.Fatalf("stack ID = %q, want stack_new", stack.ID)
	}
	if stack.Slug != "acme-prod" {
		t.Fatalf("slug = %q, want acme-prod", stack.Slug)
	}
	if stack.CreatedBy != domain.UserID(keycloakSubject) {
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
	if len(stacks.created.DefaultCredentialIDs) != 1 || stacks.created.DefaultCredentialIDs[0] != domain.CredentialSetID("credential_123") {
		t.Fatalf("default credential IDs = %#v", stacks.created.DefaultCredentialIDs)
	}
}

func TestCreateStackReturnsDuplicateSlugConflict(t *testing.T) {
	t.Parallel()

	stacks := &recordingStackRepository{createErr: ErrDuplicateStackSlug}
	authorizer := testPlatformAuthorizer(t)
	service := NewService(Service{
		Stacks:        stacks,
		Work:          newRecordingWork(stacks),
		Authorization: authorizer,
		StackIDs:      fixedStackIDGenerator{id: domain.StackID("stack_new")},
		Clock:         fixedClock{now: time.Now()},
	})

	_, err := service.CreateStack(authenticatedContext(), CreateStackCommand{
		TenantID: domain.TenantID("tenant_123"),
		Name:     "Acme Prod",
		Slug:     "acme-prod",
	})
	if !errors.Is(err, ErrDuplicateStackSlug) {
		t.Fatalf("error = %v, want ErrDuplicateStackSlug", err)
	}
	// A rejected creation leaves no owner behind. Under a real transaction the
	// rollback guarantees it; here the in-memory engine shows the same outcome
	// because the grant is written after the row that failed.
	grants, err := authorizer.ListGrants(context.Background(), mustStackObject(t, "stack_new"))
	if err != nil {
		t.Fatalf("ListGrants() error = %v", err)
	}
	if len(grants) != 0 {
		t.Fatalf("grants = %#v, want none after a rejected creation", grants)
	}
}

func TestCreateStackRejectsInvalidTagKey(t *testing.T) {
	t.Parallel()

	service := NewService(Service{
		Authorization: testPlatformAuthorizer(t),
		Stacks:        &recordingStackRepository{},
		Work:          newRecordingWork(&recordingStackRepository{}),
		StackIDs:      fixedStackIDGenerator{id: domain.StackID("stack_new")},
	})

	_, err := service.CreateStack(authenticatedContext(), CreateStackCommand{
		TenantID: domain.TenantID("tenant_123"),
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
		Authorization: testPlatformAuthorizer(t),
		Stacks:        &recordingStackRepository{},
		Work:          newRecordingWork(&recordingStackRepository{}),
		StackIDs:      fixedStackIDGenerator{id: domain.StackID("stack_new")},
	})

	_, err := service.CreateStack(authenticatedContext(), CreateStackCommand{
		TenantID: domain.TenantID("tenant_123"),
		Name:     "Acme Prod",
		DefaultCredentialIDs: []domain.CredentialSetID{
			domain.CredentialSetID(""),
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
			Stack: domain.Stack{
				ID:       domain.StackID("stack_123"),
				TenantID: domain.TenantID("tenant_123"),
				Name:     "Acme Prod",
				Slug:     "acme-prod",
			},
			Templates: nil,
		},
	}
	service := NewService(Service{Stacks: stacks, Authorization: testPlatformAuthorizer(t)})
	ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: keycloakSubject})

	view, err := service.GetStack(ctx, GetStackCommand{
		TenantID: domain.TenantID("tenant_123"),
		StackID:  domain.StackID("stack_123"),
	})
	if err != nil {
		t.Fatalf("GetStack returned error: %v", err)
	}

	if stacks.gotTenantID != domain.TenantID("tenant_123") {
		t.Fatalf("tenant lookup = %q, want tenant_123", stacks.gotTenantID)
	}
	if stacks.gotStackID != domain.StackID("stack_123") {
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
			Stack: domain.Stack{
				ID:       domain.StackID("stack_123"),
				TenantID: domain.TenantID("tenant_123"),
				Name:     "Acme Prod",
				Slug:     "acme-prod",
			},
			Templates: []StackTemplateView{
				{StackTemplate: domain.StackTemplate{
					ID:                        domain.StackTemplateID("stack_template_1"),
					DesiredTemplateRevisionID: domain.TemplateRevisionID("rev_1"),
				}},
				{StackTemplate: domain.StackTemplate{
					ID:                        domain.StackTemplateID("stack_template_2"),
					DesiredTemplateRevisionID: domain.TemplateRevisionID("rev_2"),
				}},
				{StackTemplate: domain.StackTemplate{
					ID:                        domain.StackTemplateID("stack_template_3"),
					DesiredTemplateRevisionID: domain.TemplateRevisionID("rev_missing"),
				}},
			},
		},
	}
	revisions := &recordingTemplateRepository{
		templates: []domain.TemplateRevision{
			{ID: domain.TemplateRevisionID("rev_1"), RepoName: "infra-templates", RootPath: "modules/vpc", SourceRef: "v2.0.0"},
			{ID: domain.TemplateRevisionID("rev_2"), RepoName: "my-repo", RootPath: ".", SourceRef: "main"},
		},
	}
	service := NewService(Service{Stacks: stacks, TemplateRevisions: revisions, Authorization: testPlatformAuthorizer(t)})
	ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: keycloakSubject})

	view, err := service.GetStack(ctx, GetStackCommand{
		TenantID: domain.TenantID("tenant_123"),
		StackID:  domain.StackID("stack_123"),
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
	service := NewService(Service{Stacks: stacks, Authorization: newTestAuthorization(t)})
	ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: keycloakSubject})

	got, err := service.ListStacks(ctx, ListStacksCommand{
		TenantID: domain.TenantID("tenant_123"),
	})
	if err != nil {
		t.Fatalf("ListStacks returned error: %v", err)
	}

	if stacks.gotListTenantID != domain.TenantID("tenant_123") {
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
	service := NewService(Service{TemplateRevisions: templates, Authorization: testPlatformAuthorizer(t)})

	got, err := service.ListTemplateRevisions(authenticatedContext(), ListTemplateRevisionsCommand{
		TenantID: domain.TenantID("tenant_123"),
	})
	if err != nil {
		t.Fatalf("ListTemplateRevisions returned error: %v", err)
	}

	if templates.gotListTenantID != domain.TenantID("tenant_123") {
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
		stack: domain.Stack{
			ID:       domain.StackID("stack_123"),
			TenantID: domain.TenantID("tenant_123"),
			Name:     "Acme Prod",
			Slug:     "acme-prod",
		},
	}
	templates := &recordingTemplateRepository{
		template: domain.TemplateRevision{
			ID:               domain.TemplateRevisionID("template_123"),
			TenantID:         domain.TenantID("tenant_123"),
			SourceTemplateID: domain.SourceTemplateID("source_template_vpc"),
			SourceRef:        "main",
			Status:           domain.TemplateRevisionActive,
		},
		variables: []domain.TemplateVariable{
			{Name: "region", Required: true},
			{Name: "cidr", Required: false, HasDefault: true},
		},
	}
	installer := &recordingStackTemplateInstaller{}
	service := NewService(Service{
		Authorization:            testPlatformAuthorizer(t),
		Stacks:                   stacks,
		Work:                     newRecordingWork(stacks),
		TemplateRevisionMetadata: templates,
		TemplateRevisions:        templates,
		StackTemplateInstaller:   installer,
		StackTemplateIDs:         fixedStackTemplateIDGenerator{id: domain.StackTemplateID("stack_template_a1b2c3d4")},
	})

	stackTemplate, err := service.AddTemplateToStack(authenticatedContext(), AddTemplateToStackCommand{
		TenantID:           domain.TenantID("tenant_123"),
		StackID:            domain.StackID("stack_123"),
		TemplateRevisionID: domain.TemplateRevisionID("template_123"),
		ComponentKey:       "primary-vpc",
		ConfigJSON:         json.RawMessage(`{"region":"us-east-1"}`),
	})
	if err != nil {
		t.Fatalf("AddTemplateToStack returned error: %v", err)
	}

	if stackTemplate.ID != domain.StackTemplateID("stack_template_a1b2c3d4") {
		t.Fatalf("stack template revision ID = %q, want stack_template_a1b2c3d4", stackTemplate.ID)
	}
	if stackTemplate.TenantID != domain.TenantID("tenant_123") {
		t.Fatalf("tenant ID = %q, want tenant_123", stackTemplate.TenantID)
	}
	if stackTemplate.WorkspaceName != "meg_acme_prod_a1b2c3d4" {
		t.Fatalf("workspace name = %q, want meg_acme_prod_a1b2c3d4", stackTemplate.WorkspaceName)
	}
	if stackTemplate.Lifecycle != domain.StackTemplateActive {
		t.Fatalf("lifecycle = %q, want active", stackTemplate.Lifecycle)
	}
	if stackTemplate.CreatedBy != domain.UserID(keycloakSubject) {
		t.Fatalf("created by = %q, want %q", stackTemplate.CreatedBy, keycloakSubject)
	}
	if stackTemplate.ComponentKey != "primary-vpc" {
		t.Fatalf("component key = %q, want primary-vpc", stackTemplate.ComponentKey)
	}
	if stackTemplate.SourceTemplateID != domain.SourceTemplateID("source_template_vpc") {
		t.Fatalf("source template ID = %q, want source_template_vpc", stackTemplate.SourceTemplateID)
	}
	if stackTemplate.DesiredTemplateRevisionID != domain.TemplateRevisionID("template_123") {
		t.Fatalf("desired template revision ID = %q, want template_123", stackTemplate.DesiredTemplateRevisionID)
	}
	if string(stackTemplate.DesiredConfigJSON) != `{"region":"us-east-1"}` {
		t.Fatalf("desired config json = %s", stackTemplate.DesiredConfigJSON)
	}
	if installer.created.CreatedBy != domain.UserID(keycloakSubject) {
		t.Fatalf("persisted created by = %q, want %q", installer.created.CreatedBy, keycloakSubject)
	}
	if string(installer.created.InstalledConfigJSON) != `{"region":"us-east-1"}` {
		t.Fatalf("config json = %s", installer.created.InstalledConfigJSON)
	}
	if installer.created.SourceTemplateID != domain.SourceTemplateID("source_template_vpc") {
		t.Fatalf("persisted source template ID = %q, want source_template_vpc", installer.created.SourceTemplateID)
	}
	if installer.created.DesiredTemplateRevisionID != domain.TemplateRevisionID("template_123") {
		t.Fatalf("persisted desired template revision ID = %q, want template_123", installer.created.DesiredTemplateRevisionID)
	}
	if string(installer.created.DesiredConfigJSON) != `{"region":"us-east-1"}` {
		t.Fatalf("persisted desired config json = %s", installer.created.DesiredConfigJSON)
	}
}

func TestAddTemplateToStackRejectsMissingRequiredVariable(t *testing.T) {
	t.Parallel()

	service := NewService(Service{
		Authorization: testPlatformAuthorizer(t),
		Stacks: &recordingStackRepository{
			stack: domain.Stack{ID: domain.StackID("stack_123"), TenantID: domain.TenantID("tenant_123"), Slug: "acme-prod"},
		},
		TemplateRevisionMetadata: &recordingTemplateRepository{
			template: domain.TemplateRevision{ID: domain.TemplateRevisionID("template_123"), TenantID: domain.TenantID("tenant_123"), Status: domain.TemplateRevisionActive},
		},
		TemplateRevisions: &recordingTemplateRepository{
			variables: []domain.TemplateVariable{{Name: "region", Required: true}},
		},
		StackTemplateInstaller: &recordingStackTemplateInstaller{},
		StackTemplateIDs:       fixedStackTemplateIDGenerator{id: domain.StackTemplateID("stack_template_a1b2c3d4")},
	})

	_, err := service.AddTemplateToStack(authenticatedContext(), AddTemplateToStackCommand{
		TenantID:           domain.TenantID("tenant_123"),
		StackID:            domain.StackID("stack_123"),
		TemplateRevisionID: domain.TemplateRevisionID("template_123"),
		ConfigJSON:         json.RawMessage(`{}`),
	})
	if !errors.Is(err, ErrStackTemplateConfigInvalid) {
		t.Fatalf("error = %v, want ErrStackTemplateConfigInvalid", err)
	}
}

func TestAddTemplateToStackRejectsUnknownVariable(t *testing.T) {
	t.Parallel()

	service := NewService(Service{
		Authorization: testPlatformAuthorizer(t),
		Stacks: &recordingStackRepository{
			stack: domain.Stack{ID: domain.StackID("stack_123"), TenantID: domain.TenantID("tenant_123"), Slug: "acme-prod"},
		},
		TemplateRevisionMetadata: &recordingTemplateRepository{
			template: domain.TemplateRevision{ID: domain.TemplateRevisionID("template_123"), TenantID: domain.TenantID("tenant_123"), Status: domain.TemplateRevisionActive},
		},
		TemplateRevisions: &recordingTemplateRepository{
			variables: []domain.TemplateVariable{{Name: "region", Required: true}},
		},
		StackTemplateInstaller: &recordingStackTemplateInstaller{},
		StackTemplateIDs:       fixedStackTemplateIDGenerator{id: domain.StackTemplateID("stack_template_a1b2c3d4")},
	})

	_, err := service.AddTemplateToStack(authenticatedContext(), AddTemplateToStackCommand{
		TenantID:           domain.TenantID("tenant_123"),
		StackID:            domain.StackID("stack_123"),
		TemplateRevisionID: domain.TemplateRevisionID("template_123"),
		ConfigJSON:         json.RawMessage(`{"region":"us-east-1","extra":"nope"}`),
	})
	if !errors.Is(err, ErrStackTemplateConfigInvalid) {
		t.Fatalf("error = %v, want ErrStackTemplateConfigInvalid", err)
	}
}

func TestAddTemplateToStackRejectsInactiveTemplate(t *testing.T) {
	t.Parallel()

	service := NewService(Service{
		Authorization: testPlatformAuthorizer(t),
		Stacks: &recordingStackRepository{
			stack: domain.Stack{ID: domain.StackID("stack_123"), TenantID: domain.TenantID("tenant_123"), Slug: "acme-prod"},
		},
		TemplateRevisionMetadata: &recordingTemplateRepository{
			template: domain.TemplateRevision{ID: domain.TemplateRevisionID("template_123"), TenantID: domain.TenantID("tenant_123"), Status: domain.TemplateRevisionInvalid},
		},
		TemplateRevisions:      &recordingTemplateRepository{},
		StackTemplateInstaller: &recordingStackTemplateInstaller{},
		StackTemplateIDs:       fixedStackTemplateIDGenerator{id: domain.StackTemplateID("stack_template_a1b2c3d4")},
	})

	_, err := service.AddTemplateToStack(authenticatedContext(), AddTemplateToStackCommand{
		TenantID:           domain.TenantID("tenant_123"),
		StackID:            domain.StackID("stack_123"),
		TemplateRevisionID: domain.TemplateRevisionID("template_123"),
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
		stackTemplate: domain.StackTemplate{
			ID:                        domain.StackTemplateID("stack_template_123"),
			StackID:                   domain.StackID("stack_123"),
			SourceTemplateID:          domain.SourceTemplateID("source_template_vpc"),
			DesiredTemplateRevisionID: domain.TemplateRevisionID("template_rev_2"),
			DesiredConfigJSON:         json.RawMessage(`{"region":"us-east-1"}`),
			WorkspaceName:             "mtp_acme_prod_vpc_a13f9c",
			Lifecycle:                 domain.StackTemplateActive,
			// An apply needs a plan that still describes desired state.
			PendingPlanRunID:              domain.TemplateRunID("run_plan_1"),
			PendingPlanTemplateRevisionID: domain.TemplateRevisionID("template_rev_2"),
			PendingPlanConfigJSON:         json.RawMessage(`{"region":"us-east-1"}`),
		},
	}
	templates := &recordingTemplateRepository{
		template: domain.TemplateRevision{
			ID:               domain.TemplateRevisionID("template_rev_2"),
			TenantID:         domain.TenantID("tenant_123"),
			SourceTemplateID: domain.SourceTemplateID("source_template_vpc"),
			RepoOwner:        "acme",
			RepoName:         "infra-templates",
			// The run's ref comes from here now, not from the component.
			SourceRef:         "main",
			ResolvedCommitSHA: "sha-2",
			RootPath:          "modules/vpc",
			Status:            domain.TemplateRevisionActive,
		},
	}
	runs := &recordingTemplateRunRepository{run: domain.TemplateRun{ID: "run_123", TenantID: "tenant_123", StackTemplateID: "stack_template_123"}, createdRunNumber: 7}
	work := &recordingUnitOfWork{templateRuns: runs}

	service := NewService(Service{
		Authorization:            testPlatformAuthorizer(t),
		Work:                     work,
		StackTemplates:           stackTemplates,
		TemplateRuns:             runs,
		TemplateRevisionMetadata: templates,
		RunIDs:                   fixedTemplateRunIDGenerator{runID: domain.TemplateRunID("run_123")},
		Clock:                    fixedClock{now: now},
	})

	run, err := service.StartTemplateRun(ctx, StartTemplateRunCommand{
		TenantID:        domain.TenantID("tenant_123"),
		StackTemplateID: domain.StackTemplateID("stack_template_123"),
		Operation:       domain.OperationPlan,
	})
	if err != nil {
		t.Fatalf("StartTemplateRun returned error: %v", err)
	}

	if run.ID != domain.TemplateRunID("run_123") {
		t.Fatalf("run.ID = %q, want run_123", run.ID)
	}
	// The store assigns the number; the service hands it back unchanged.
	if run.RunNumber != 7 {
		t.Fatalf("run.RunNumber = %d, want 7", run.RunNumber)
	}

	if run.Status != domain.TemplateRunQueued {
		t.Fatalf("run.Status = %q, want %q", run.Status, domain.TemplateRunQueued)
	}
	if run.TriggerActor != domain.UserID(keycloakSubject) {
		t.Fatalf("run.TriggerActor = %q, want %q", run.TriggerActor, keycloakSubject)
	}

	if run.WorkspaceName != "mtp_acme_prod_vpc_a13f9c" {
		t.Fatalf("run.WorkspaceName = %q", run.WorkspaceName)
	}

	if run.SelectedRef != "main" {
		t.Fatalf("run.SelectedRef = %q, want main", run.SelectedRef)
	}

	if run.TemplateRevisionID != domain.TemplateRevisionID("template_rev_2") {
		t.Fatalf("run.TemplateRevisionID = %q, want template_rev_2", run.TemplateRevisionID)
	}

	if run.SourceTemplateID != domain.SourceTemplateID("source_template_vpc") {
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

	if stackTemplates.gotTenantID != domain.TenantID("tenant_123") {
		t.Fatalf("stack template lookup tenant = %q", stackTemplates.gotTenantID)
	}

	if runs.created.ID != run.ID {
		t.Fatalf("created run ID = %q, want %q", runs.created.ID, run.ID)
	}

	if len(work.requests) != 1 || work.requests[0].Kind != KindStartTemplateRun {
		t.Fatalf("queued requests = %#v, want one start_template_run request", work.requests)
	}

	if templates.gotGetTemplateTenantID != domain.TenantID("tenant_123") {
		t.Fatalf("template lookup tenant = %q, want tenant_123", templates.gotGetTemplateTenantID)
	}

	if templates.gotGetTemplateRevisionID != domain.TemplateRevisionID("template_rev_2") {
		t.Fatalf("template revision lookup ID = %q, want template_rev_2", templates.gotGetTemplateRevisionID)
	}

}

func TestUpdateStackTemplateConfigValidatesDesiredRevisionVariables(t *testing.T) {
	t.Parallel()

	stackTemplates := &recordingStackTemplateRepository{
		stackTemplate: domain.StackTemplate{
			ID:                        domain.StackTemplateID("stack_template_123"),
			TenantID:                  domain.TenantID("tenant_123"),
			StackID:                   domain.StackID("stack_123"),
			DesiredTemplateRevisionID: domain.TemplateRevisionID("template_rev_2"),
			Lifecycle:                 domain.StackTemplateActive,
		},
	}
	templates := &recordingTemplateRepository{
		variables: []domain.TemplateVariable{
			{Name: "region", Required: true},
		},
	}
	service := NewService(Service{
		TemplateRuns:      &recordingTemplateRunRepository{},
		Authorization:     testPlatformAuthorizer(t),
		StackTemplates:    stackTemplates,
		TemplateRevisions: templates,
	})

	updated, err := service.UpdateStackTemplateConfig(authenticatedContext(), UpdateStackTemplateConfigCommand{
		TenantID:        domain.TenantID("tenant_123"),
		StackTemplateID: domain.StackTemplateID("stack_template_123"),
		ConfigJSON:      json.RawMessage(`{"region":"us-east-1"}`),
	})
	if err != nil {
		t.Fatalf("UpdateStackTemplateConfig returned error: %v", err)
	}

	if templates.gotVariablesTemplateRevisionID != domain.TemplateRevisionID("template_rev_2") {
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
		stackTemplate: domain.StackTemplate{
			ID:        domain.StackTemplateID("stack_template_123"),
			TenantID:  domain.TenantID("tenant_123"),
			Lifecycle: domain.StackTemplateActive,
		},
	}
	service := NewService(Service{
		TemplateRuns:      &recordingTemplateRunRepository{},
		Authorization:     testPlatformAuthorizer(t),
		StackTemplates:    stackTemplates,
		TemplateRevisions: &recordingTemplateRepository{},
	})

	_, err := service.UpdateStackTemplateConfig(authenticatedContext(), UpdateStackTemplateConfigCommand{
		TenantID:        domain.TenantID("tenant_123"),
		StackTemplateID: domain.StackTemplateID("stack_template_123"),
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
		stackTemplate: domain.StackTemplate{
			ID:                        domain.StackTemplateID("stack_template_123"),
			TenantID:                  domain.TenantID("tenant_123"),
			SourceTemplateID:          domain.SourceTemplateID("source_template_vpc"),
			DesiredTemplateRevisionID: domain.TemplateRevisionID("template_rev_1"),
			DesiredConfigJSON:         json.RawMessage(`{"region":"us-east-1","removed":"old"}`),
			Lifecycle:                 domain.StackTemplateActive,
		},
	}
	templates := &recordingTemplateRepository{
		template: domain.TemplateRevision{
			ID:               domain.TemplateRevisionID("template_rev_2"),
			TenantID:         domain.TenantID("tenant_123"),
			SourceTemplateID: domain.SourceTemplateID("source_template_vpc"),
			Status:           domain.TemplateRevisionActive,
		},
		variables: []domain.TemplateVariable{
			{Name: "region", Required: true},
			{Name: "size", HasDefault: true},
		},
	}
	service := NewService(Service{
		TemplateRuns:             &recordingTemplateRunRepository{},
		Authorization:            testPlatformAuthorizer(t),
		StackTemplates:           stackTemplates,
		TemplateRevisionMetadata: templates,
		TemplateRevisions:        templates,
	})

	updated, err := service.UpgradeStackTemplate(authenticatedContext(), UpgradeStackTemplateCommand{
		TenantID:                 domain.TenantID("tenant_123"),
		StackTemplateID:          domain.StackTemplateID("stack_template_123"),
		TargetTemplateRevisionID: domain.TemplateRevisionID("template_rev_2"),
	})
	if err != nil {
		t.Fatalf("UpgradeStackTemplate returned error: %v", err)
	}

	if stackTemplates.gotDesiredTemplateRevisionID != domain.TemplateRevisionID("template_rev_2") {
		t.Fatalf("updated desired template revision ID = %q, want template_rev_2", stackTemplates.gotDesiredTemplateRevisionID)
	}
	if string(stackTemplates.gotConfigJSON) != `{"region":"us-east-1"}` {
		t.Fatalf("carried config = %s, want region only", stackTemplates.gotConfigJSON)
	}
	if updated.DesiredTemplateRevisionID != domain.TemplateRevisionID("template_rev_2") {
		t.Fatalf("returned desired template revision ID = %q, want template_rev_2", updated.DesiredTemplateRevisionID)
	}
}

func TestUpgradeStackTemplateRejectsDifferentSourceTemplate(t *testing.T) {
	t.Parallel()

	stackTemplates := &recordingStackTemplateRepository{
		stackTemplate: domain.StackTemplate{
			ID:               domain.StackTemplateID("stack_template_123"),
			TenantID:         domain.TenantID("tenant_123"),
			SourceTemplateID: domain.SourceTemplateID("source_template_vpc"),
			Lifecycle:        domain.StackTemplateActive,
		},
	}
	templates := &recordingTemplateRepository{
		template: domain.TemplateRevision{
			ID:               domain.TemplateRevisionID("template_rev_2"),
			TenantID:         domain.TenantID("tenant_123"),
			SourceTemplateID: domain.SourceTemplateID("source_template_db"),
			Status:           domain.TemplateRevisionActive,
		},
	}
	service := NewService(Service{
		TemplateRuns:             &recordingTemplateRunRepository{},
		Authorization:            testPlatformAuthorizer(t),
		StackTemplates:           stackTemplates,
		TemplateRevisionMetadata: templates,
		TemplateRevisions:        templates,
	})

	_, err := service.UpgradeStackTemplate(authenticatedContext(), UpgradeStackTemplateCommand{
		TenantID:                 domain.TenantID("tenant_123"),
		StackTemplateID:          domain.StackTemplateID("stack_template_123"),
		TargetTemplateRevisionID: domain.TemplateRevisionID("template_rev_2"),
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
		RunIDs:         fixedTemplateRunIDGenerator{runID: domain.TemplateRunID("run_123")},
		Clock:          fixedClock{now: time.Now()},
	})

	_, err := service.StartTemplateRun(authenticatedContext(), StartTemplateRunCommand{
		TenantID:        domain.TenantID("tenant_123"),
		StackTemplateID: domain.StackTemplateID("stack_template_123"),
		Operation:       domain.OperationType("refresh"),
	})
	if !errors.Is(err, ErrInvalidCommand) {
		t.Fatalf("error = %v, want ErrInvalidCommand", err)
	}
}

func TestStartTemplateRunRejectsInactiveStackTemplate(t *testing.T) {
	t.Parallel()

	stackTemplates := &recordingStackTemplateRepository{
		stackTemplate: domain.StackTemplate{
			ID:            domain.StackTemplateID("stack_template_123"),
			WorkspaceName: "mtp_acme_prod_vpc_a13f9c",
			Lifecycle:     domain.StackTemplateDestroyed,
		},
	}
	runs := &recordingTemplateRunRepository{}
	service := NewService(Service{
		Authorization:  testPlatformAuthorizer(t),
		StackTemplates: stackTemplates,
		TemplateRuns:   runs,
		RunIDs:         fixedTemplateRunIDGenerator{runID: domain.TemplateRunID("run_123")},
		Clock:          fixedClock{now: time.Now()},
	})

	_, err := service.StartTemplateRun(authenticatedContext(), StartTemplateRunCommand{
		TenantID:        domain.TenantID("tenant_123"),
		StackTemplateID: domain.StackTemplateID("stack_template_123"),
		Operation:       domain.OperationPlan,
	})
	if !errors.Is(err, ErrStackTemplateNotRunnable) {
		t.Fatalf("error = %v, want ErrStackTemplateNotRunnable", err)
	}

	if runs.created.ID != "" {
		t.Fatalf("created run ID = %q, want no persisted run", runs.created.ID)
	}

}

// Approving checks that the plan waiting for approval still describes desired
// state. Config and revision cannot change while a run is in flight, so this is
// the backstop behind that rule rather than the gate itself.
func TestApproveRunRejectsAPlanThatNoLongerMatchesDesired(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		stackTemplate domain.StackTemplate
	}{
		{
			name: "config changed after the plan",
			stackTemplate: domain.StackTemplate{
				PendingPlanRunID:              "run_123",
				PendingPlanTemplateRevisionID: "template_123",
				PendingPlanConfigJSON:         json.RawMessage(`{"region":"us-east-1"}`),
			},
		},
		{
			name: "revision changed after the plan",
			stackTemplate: domain.StackTemplate{
				PendingPlanRunID:              "run_123",
				PendingPlanTemplateRevisionID: "template_122",
				PendingPlanConfigJSON:         json.RawMessage(`{"region":"eu-west-1"}`),
			},
		},
		{
			name: "another run's plan is the pending one",
			stackTemplate: domain.StackTemplate{
				PendingPlanRunID:              "run_other",
				PendingPlanTemplateRevisionID: "template_123",
				PendingPlanConfigJSON:         json.RawMessage(`{"region":"eu-west-1"}`),
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			stackTemplate := test.stackTemplate
			stackTemplate.ID = "stack_template_123"
			stackTemplate.TenantID = "tenant_123"
			stackTemplate.StackID = "stack_123"
			stackTemplate.DesiredTemplateRevisionID = "template_123"
			stackTemplate.DesiredConfigJSON = json.RawMessage(`{"region":"eu-west-1"}`)

			runs := &recordingTemplateRunRepository{run: domain.TemplateRun{ID: "run_123", TenantID: "tenant_123", StackTemplateID: "stack_template_123", Status: domain.TemplateRunWaitingApproval}}
			work := &recordingUnitOfWork{templateRuns: runs}
			service := NewService(Service{
				Authorization:            testPlatformAuthorizer(t),
				Work:                     work,
				TemplateRuns:             runs,
				StackTemplates:           &recordingStackTemplateRepository{stackTemplate: stackTemplate},
				TemplateRevisionMetadata: &recordingTemplateRepository{},
			})

			err := service.ApproveRun(authenticatedContext(), ApproveRunCommand{TenantID: "tenant_123", RunID: "run_123"})
			if !errors.Is(err, ErrStackTemplatePlanStale) {
				t.Fatalf("error = %v, want ErrStackTemplatePlanStale", err)
			}
			if len(work.requests) != 0 || runs.approval.RunID != "" {
				t.Fatalf("requests = %#v, approval = %#v; want nothing recorded", work.requests, runs.approval)
			}
		})
	}
}

// Only a plan that is waiting can be approved: one already approved, applying
// or finished is refused before anything is written.
func TestApproveRunRejectsARunThatIsNotWaiting(t *testing.T) {
	t.Parallel()

	runs := &recordingTemplateRunRepository{run: domain.TemplateRun{ID: "run_123", TenantID: "tenant_123", StackTemplateID: "stack_template_123", Status: domain.TemplateRunRunning}}
	work := &recordingUnitOfWork{templateRuns: runs}
	service := NewService(Service{
		Authorization:            testPlatformAuthorizer(t),
		Work:                     work,
		TemplateRuns:             runs,
		StackTemplates:           approvableStackTemplates(),
		TemplateRevisionMetadata: &recordingTemplateRepository{},
	})

	err := service.ApproveRun(authenticatedContext(), ApproveRunCommand{TenantID: "tenant_123", RunID: "run_123"})
	if !errors.Is(err, ErrRunNotApprovable) {
		t.Fatalf("error = %v, want ErrRunNotApprovable", err)
	}
	if work.inTxCalls != 0 {
		t.Fatalf("transaction calls = %d, want none", work.inTxCalls)
	}
}

// Auto-approve is an approval given before the plan exists, so starting an
// auto-approved run takes approve access as well as operate access.
func TestStartTemplateRunAutoApproveRequiresApproveAccess(t *testing.T) {
	t.Parallel()

	stackTemplate := domain.StackTemplate{
		ID:                        "stack_template_123",
		StackID:                   "stack_123",
		DesiredTemplateRevisionID: "template_123",
		WorkspaceName:             "mtp_acme_prod_vpc_a13f9c",
		Lifecycle:                 domain.StackTemplateActive,
	}
	newService := func(t *testing.T, grant authorization.Relation) (*Service, *recordingTemplateRunRepository, *recordingUnitOfWork) {
		runs := &recordingTemplateRunRepository{}
		work := &recordingUnitOfWork{templateRuns: runs}
		authorizer := seedGrants(t, newPlatformAuthorizer(t), mustGrant(t, keycloakSubject, "stack_123", grant))
		return NewService(Service{
			Authorization:            authorizer,
			Work:                     work,
			StackTemplates:           &recordingStackTemplateRepository{stackTemplate: stackTemplate},
			TemplateRuns:             runs,
			TemplateRevisionMetadata: &recordingTemplateRepository{template: domain.TemplateRevision{ID: "template_123", Status: domain.TemplateRevisionActive}},
			RunIDs:                   fixedTemplateRunIDGenerator{runID: "run_123"},
		}), runs, work
	}
	command := StartTemplateRunCommand{TenantID: "tenant_123", StackTemplateID: "stack_template_123", Operation: domain.OperationApply, AutoApprove: true}

	operator, operatorRuns, _ := newService(t, authorization.RelationOperator)
	if _, err := operator.StartTemplateRun(authenticatedContext(), command); !errors.Is(err, ErrForbidden) {
		t.Fatalf("operator error = %v, want ErrForbidden", err)
	}
	if operatorRuns.created.ID != "" {
		t.Fatalf("operator created run %q, want none", operatorRuns.created.ID)
	}

	owner, ownerRuns, ownerWork := newService(t, authorization.RelationOwner)
	run, err := owner.StartTemplateRun(authenticatedContext(), command)
	if err != nil {
		t.Fatalf("owner error = %v", err)
	}
	if !run.AutoApprove || !ownerRuns.created.AutoApprove {
		t.Fatalf("run auto-approve = %v, persisted = %v; want both set", run.AutoApprove, ownerRuns.created.AutoApprove)
	}
	// It has no plan, so it goes straight to the apply workflow, and its
	// approval, given now, is audited now.
	if len(ownerWork.requests) != 1 || ownerWork.requests[0].Kind != KindStartTemplateApply {
		t.Fatalf("requests = %#v, want one start_template_apply", ownerWork.requests)
	}
	if len(ownerWork.audits) != 1 || ownerWork.audits[0].Action != domain.AuditActionApprovalGranted {
		t.Fatalf("audits = %#v, want one approval granted", ownerWork.audits)
	}
}

// Only an apply run can be auto-approved: a plan run applies nothing, and a
// destroy always waits for someone to approve its plan.
func TestStartTemplateRunRejectsAutoApproveOutsideApply(t *testing.T) {
	t.Parallel()

	service := NewService(Service{Authorization: testPlatformAuthorizer(t)})
	for _, operation := range []domain.OperationType{domain.OperationPlan, domain.OperationDestroy} {
		_, err := service.StartTemplateRun(authenticatedContext(), StartTemplateRunCommand{
			TenantID: "tenant_123", StackTemplateID: "stack_template_123", Operation: operation, AutoApprove: true,
		})
		if !errors.Is(err, ErrInvalidCommand) {
			t.Fatalf("%s error = %v, want ErrInvalidCommand", operation, err)
		}
	}
}

// A run snapshots desired state when it starts, so desired state cannot move
// while one is unfinished: the plan waiting for approval would stop being the
// plan of what the template says.
func TestConfigAndRevisionChangesWaitForTheRunInFlight(t *testing.T) {
	t.Parallel()

	stackTemplate := domain.StackTemplate{
		ID:                        "stack_template_123",
		StackID:                   "stack_123",
		SourceTemplateID:          "source_template_vpc",
		DesiredTemplateRevisionID: "template_123",
		Lifecycle:                 domain.StackTemplateActive,
	}
	runs := &recordingTemplateRunRepository{list: []domain.TemplateRun{{ID: "run_123", RunNumber: 4, Status: domain.TemplateRunWaitingApproval}}}
	service := NewService(Service{
		Authorization:            testPlatformAuthorizer(t),
		StackTemplates:           &recordingStackTemplateRepository{stackTemplate: stackTemplate},
		TemplateRuns:             runs,
		TemplateRevisions:        &recordingTemplateRepository{},
		TemplateRevisionMetadata: &recordingTemplateRepository{template: domain.TemplateRevision{ID: "template_124", SourceTemplateID: "source_template_vpc", Status: domain.TemplateRevisionActive}},
	})

	_, err := service.UpdateStackTemplateConfig(authenticatedContext(), UpdateStackTemplateConfigCommand{TenantID: "tenant_123", StackTemplateID: "stack_template_123", ConfigJSON: json.RawMessage(`{}`)})
	if !errors.Is(err, ErrTemplateRunInFlight) {
		t.Fatalf("config error = %v, want ErrTemplateRunInFlight", err)
	}
	_, err = service.UpgradeStackTemplate(authenticatedContext(), UpgradeStackTemplateCommand{TenantID: "tenant_123", StackTemplateID: "stack_template_123", TargetTemplateRevisionID: "template_124"})
	if !errors.Is(err, ErrTemplateRunInFlight) {
		t.Fatalf("upgrade error = %v, want ErrTemplateRunInFlight", err)
	}
}

// A plan waiting for approval, or approved but not yet applying, has no
// workflow running, so discarding it is only the write.
func TestDiscardRunDiscardsAWaitingPlan(t *testing.T) {
	t.Parallel()

	runs := &recordingTemplateRunRepository{run: domain.TemplateRun{ID: "run_123", TenantID: "tenant_123", StackTemplateID: "stack_template_123", Status: domain.TemplateRunWaitingApproval}}
	work := &recordingUnitOfWork{templateRuns: runs}
	service := NewService(Service{
		Authorization:  testPlatformAuthorizer(t),
		Work:           work,
		TemplateRuns:   runs,
		StackTemplates: approvableStackTemplates(),
	})

	if err := service.DiscardRun(authenticatedContext(), DiscardRunCommand{TenantID: "tenant_123", RunID: "run_123", Reason: "discard"}); err != nil {
		t.Fatalf("DiscardRun returned error: %v", err)
	}
	if runs.discarded.RunID != "run_123" || runs.discarded.RequestedBy != domain.UserID(keycloakSubject) {
		t.Fatalf("discarded = %#v, want run_123 by the caller", runs.discarded)
	}
	if len(work.requests) != 0 {
		t.Fatalf("queued = %#v, want nothing queued", work.requests)
	}
}

// The gate against concurrent runs is the template_runs_in_flight_idx unique
// index, not a check in this method, so the rejection arrives from the store
// through the transaction rather than from a branch above it. What the service
// owes is passage: the sentinel has to survive the InTx closure and the wrap
// around it, or the API cannot map it to its 409 and the user gets a 500.
func TestStartTemplateRunSurfacesTheStoresInFlightRejection(t *testing.T) {
	t.Parallel()

	runs := &recordingTemplateRunRepository{createErr: ErrTemplateRunInFlight}
	work := &recordingUnitOfWork{templateRuns: runs}
	service := NewService(Service{
		Work:          work,
		Authorization: testPlatformAuthorizer(t),
		TemplateRuns:  runs,
		StackTemplates: &recordingStackTemplateRepository{stackTemplate: domain.StackTemplate{
			ID:                        domain.StackTemplateID("stack_template_123"),
			DesiredTemplateRevisionID: domain.TemplateRevisionID("template_123"),
			WorkspaceName:             "mtp_acme_prod_vpc_a13f9c",
			Lifecycle:                 domain.StackTemplateActive,
		}},
		TemplateRevisionMetadata: &recordingTemplateRepository{
			template: domain.TemplateRevision{ID: domain.TemplateRevisionID("template_123"), Status: domain.TemplateRevisionActive},
		},
		RunIDs: fixedTemplateRunIDGenerator{runID: domain.TemplateRunID("run_123")},
		Clock:  fixedClock{now: time.Now()},
	})

	_, err := service.StartTemplateRun(authenticatedContext(), StartTemplateRunCommand{
		TenantID:        domain.TenantID("tenant_123"),
		StackTemplateID: domain.StackTemplateID("stack_template_123"),
		Operation:       domain.OperationPlan,
	})
	if !errors.Is(err, ErrTemplateRunInFlight) {
		t.Fatalf("error = %v, want ErrTemplateRunInFlight", err)
	}
	if runs.created.ID != "" {
		t.Fatalf("created run ID = %q, want no persisted run", runs.created.ID)
	}
	// The insert and the start intent share the transaction, so a refused run
	// must not leave a queue item that would dispatch a workflow for a run row
	// that does not exist.
	if len(work.requests) != 0 {
		t.Fatalf("queued requests = %#v, want none", work.requests)
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
		Work:          work,
		Authorization: testPlatformAuthorizer(t),
		TemplateRuns:  runs,
		StackTemplates: &recordingStackTemplateRepository{stackTemplate: domain.StackTemplate{
			ID:                        domain.StackTemplateID("stack_template_123"),
			DesiredTemplateRevisionID: domain.TemplateRevisionID("template_123"),
			WorkspaceName:             "mtp_acme_prod_vpc_a13f9c",
			Lifecycle:                 domain.StackTemplateActive,
		}},
		TemplateRevisionMetadata: &recordingTemplateRepository{
			template: domain.TemplateRevision{
				ID:                domain.TemplateRevisionID("template_123"),
				Status:            domain.TemplateRevisionActive,
				SourceRef:         "v2.0.0",
				ResolvedCommitSHA: "sha_v2",
			},
		},
		RunIDs: fixedTemplateRunIDGenerator{runID: domain.TemplateRunID("run_123")},
		Clock:  fixedClock{now: time.Now()},
	})

	run, err := service.StartTemplateRun(authenticatedContext(), StartTemplateRunCommand{
		TenantID:        domain.TenantID("tenant_123"),
		StackTemplateID: domain.StackTemplateID("stack_template_123"),
		Operation:       domain.OperationPlan,
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
		Work:          work,
		Authorization: testPlatformAuthorizer(t),
		TemplateRuns:  runs,
		StackTemplates: &recordingStackTemplateRepository{stackTemplate: domain.StackTemplate{
			ID:                            domain.StackTemplateID("stack_template_123"),
			DesiredTemplateRevisionID:     domain.TemplateRevisionID("template_123"),
			DesiredConfigJSON:             json.RawMessage(`{"region":"eu-west-1"}`),
			WorkspaceName:                 "mtp_acme_prod_vpc_a13f9c",
			Lifecycle:                     domain.StackTemplateActive,
			PendingPlanRunID:              domain.TemplateRunID("run_plan_1"),
			PendingPlanTemplateRevisionID: domain.TemplateRevisionID("template_123"),
			PendingPlanConfigJSON:         json.RawMessage(`{"region":"us-east-1"}`),
		}},
		TemplateRevisionMetadata: &recordingTemplateRepository{
			template: domain.TemplateRevision{ID: domain.TemplateRevisionID("template_123"), Status: domain.TemplateRevisionActive},
		},
		RunIDs: fixedTemplateRunIDGenerator{runID: domain.TemplateRunID("run_123")},
		Clock:  fixedClock{now: time.Now()},
	})

	run, err := service.StartTemplateRun(authenticatedContext(), StartTemplateRunCommand{
		TenantID:        domain.TenantID("tenant_123"),
		StackTemplateID: domain.StackTemplateID("stack_template_123"),
		Operation:       domain.OperationPlan,
	})
	if err != nil {
		t.Fatalf("StartTemplateRun returned error: %v", err)
	}
	if run.ID != domain.TemplateRunID("run_123") {
		t.Fatalf("run.ID = %q, want run_123", run.ID)
	}
}

func TestStartTemplateRunRejectsMissingDesiredRevision(t *testing.T) {
	t.Parallel()

	stackTemplates := &recordingStackTemplateRepository{
		stackTemplate: domain.StackTemplate{
			ID:            domain.StackTemplateID("stack_template_123"),
			WorkspaceName: "mtp_acme_prod_vpc_a13f9c",
			Lifecycle:     domain.StackTemplateActive,
		},
	}
	runs := &recordingTemplateRunRepository{}
	service := NewService(Service{
		Authorization:  testPlatformAuthorizer(t),
		StackTemplates: stackTemplates,
		TemplateRuns:   runs,
		TemplateRevisionMetadata: &recordingTemplateRepository{
			template: domain.TemplateRevision{
				ID:     domain.TemplateRevisionID("template_123"),
				Status: domain.TemplateRevisionActive,
			},
		},
		RunIDs: fixedTemplateRunIDGenerator{runID: domain.TemplateRunID("run_123")},
		Clock:  fixedClock{now: time.Now()},
	})

	_, err := service.StartTemplateRun(authenticatedContext(), StartTemplateRunCommand{
		TenantID:        domain.TenantID("tenant_123"),
		StackTemplateID: domain.StackTemplateID("stack_template_123"),
		Operation:       domain.OperationPlan,
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
		stackTemplate: domain.StackTemplate{
			ID:                        domain.StackTemplateID("stack_template_123"),
			DesiredTemplateRevisionID: domain.TemplateRevisionID("template_123"),
			WorkspaceName:             "mtp_acme_prod_vpc_a13f9c",
			Lifecycle:                 domain.StackTemplateActive,
		},
	}
	runs := &recordingTemplateRunRepository{}
	service := NewService(Service{
		Authorization:  testPlatformAuthorizer(t),
		Work:           &recordingUnitOfWork{templateRuns: runs},
		StackTemplates: stackTemplates,
		TemplateRuns:   runs,
		TemplateRevisionMetadata: &recordingTemplateRepository{
			template: domain.TemplateRevision{
				ID:        domain.TemplateRevisionID("template_123"),
				TenantID:  domain.TenantID("tenant_123"),
				RepoOwner: "acme",
				RepoName:  "infra-templates",
				RootPath:  ".",
				Status:    domain.TemplateRevisionActive,
			},
		},
		Clock: fixedClock{now: time.Now()},
	})

	run, err := service.StartTemplateRun(authenticatedContext(), StartTemplateRunCommand{
		TenantID:        domain.TenantID("tenant_123"),
		StackTemplateID: domain.StackTemplateID("stack_template_123"),
		Operation:       domain.OperationPlan,
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
		Authorization:         testPlatformAuthorizer(t),
		Work:                  work,
		TemplateRegistrations: registrations,
		RegistrationIDs:       fixedTemplateRegistrationIDGenerator{id: domain.TemplateRegistrationID("template_registration_123")},
		Clock:                 fixedClock{now: now},
	})

	registration, err := service.RegisterTemplate(ctx, RegisterTemplateCommand{
		TenantID:  domain.TenantID("tenant_123"),
		RepoOwner: "acme",
		RepoName:  "infra-templates",
		SourceRef: "v0.0.1",
		RootPath:  "modules/vpc",
	})
	if err != nil {
		t.Fatalf("RegisterTemplate returned error: %v", err)
	}

	if registration.ID != domain.TemplateRegistrationID("template_registration_123") {
		t.Fatalf("registration.ID = %q, want template_registration_123", registration.ID)
	}
	if registration.Status != domain.TemplateRegistrationPending {
		t.Fatalf("registration.Status = %q, want %q", registration.Status, domain.TemplateRegistrationPending)
	}
	if registration.RequestedBy != domain.UserID(keycloakSubject) {
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
		Authorization:         testPlatformAuthorizer(t),
		TemplateRegistrations: &recordingTemplateRegistrationRepository{},
	})

	_, err := service.RegisterTemplate(authenticatedContext(), RegisterTemplateCommand{
		TenantID:  domain.TenantID("tenant_123"),
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
		Authorization:         testPlatformAuthorizer(t),
		Work:                  work,
		TemplateRegistrations: registrations,
		RegistrationIDs:       fixedTemplateRegistrationIDGenerator{id: domain.TemplateRegistrationID("template_registration_123")},
	})

	_, err := service.RegisterTemplate(authenticatedContext(), RegisterTemplateCommand{
		TenantID:  domain.TenantID("tenant_123"),
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

func TestApproveRunRecordsApprovalAndQueuesTheApply(t *testing.T) {
	t.Parallel()

	ctx := authenticatedContext()
	now := time.Date(2026, 7, 2, 10, 15, 0, 0, time.UTC)
	runs := &recordingTemplateRunRepository{run: domain.TemplateRun{
		ID:              "run_123",
		TenantID:        "tenant_123",
		StackTemplateID: "stack_template_123",
		Status:          domain.TemplateRunWaitingApproval,
		TriggerActor:    domain.UserID("different-user"),
	}}
	work := &recordingUnitOfWork{templateRuns: runs}

	service := NewService(Service{
		Authorization:            testPlatformAuthorizer(t),
		Work:                     work,
		TemplateRuns:             runs,
		StackTemplates:           approvableStackTemplates(),
		TemplateRevisionMetadata: &recordingTemplateRepository{},
		Clock:                    fixedClock{now: now},
	})

	err := service.ApproveRun(ctx, ApproveRunCommand{
		TenantID: domain.TenantID("tenant_123"),
		RunID:    domain.TemplateRunID("run_123"),
	})
	if err != nil {
		t.Fatalf("ApproveRun returned error: %v", err)
	}

	if runs.approval.RunID != domain.TemplateRunID("run_123") {
		t.Fatalf("approval run ID = %q, want run_123", runs.approval.RunID)
	}

	if runs.approval.TenantID != domain.TenantID("tenant_123") {
		t.Fatalf("approval tenant ID = %q, want tenant_123", runs.approval.TenantID)
	}

	if runs.approval.ApprovedBy != domain.UserID(keycloakSubject) {
		t.Fatalf("approval actor = %q, want %q", runs.approval.ApprovedBy, keycloakSubject)
	}

	if !runs.approval.ApprovedAt.Equal(now) {
		t.Fatalf("approval time = %v, want %v", runs.approval.ApprovedAt, now)
	}

	if len(work.requests) != 1 || work.requests[0].Kind != KindStartTemplateApply {
		t.Fatalf("queued requests = %#v, want one signal_run_approval request", work.requests)
	}
}

func TestApproveRunQueuesNothingWhenRunIsNotApprovable(t *testing.T) {
	t.Parallel()

	runs := &recordingTemplateRunRepository{run: domain.TemplateRun{ID: "run_123", TenantID: "tenant_123", StackTemplateID: "stack_template_123", Status: domain.TemplateRunWaitingApproval}, approvalErr: ErrRunNotApprovable}
	work := &recordingUnitOfWork{templateRuns: runs}

	service := NewService(Service{
		Authorization:            testPlatformAuthorizer(t),
		Work:                     work,
		TemplateRuns:             runs,
		StackTemplates:           approvableStackTemplates(),
		TemplateRevisionMetadata: &recordingTemplateRepository{},
		Clock:                    fixedClock{now: time.Now()},
	})

	err := service.ApproveRun(authenticatedContext(), ApproveRunCommand{
		TenantID: domain.TenantID("tenant_123"),
		RunID:    domain.TemplateRunID("run_123"),
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
	runs := &recordingTemplateRunRepository{run: domain.TemplateRun{
		ID:              "run_123",
		TenantID:        "tenant_123",
		StackTemplateID: "stack_template_123",
		Status:          domain.TemplateRunWaitingApproval,
		TriggerActor:    domain.UserID(keycloakSubject),
	}}
	work := &recordingUnitOfWork{templateRuns: runs}
	audit := &recordingAuditRepository{}

	service := NewService(Service{
		Authorization:            testPlatformAuthorizer(t),
		Work:                     work,
		TemplateRuns:             runs,
		StackTemplates:           approvableStackTemplates(),
		TemplateRevisionMetadata: &recordingTemplateRepository{},
		Audit:                    audit,
	})

	err := service.ApproveRun(ctx, ApproveRunCommand{
		TenantID: domain.TenantID("tenant_123"),
		RunID:    domain.TemplateRunID("run_123"),
	})
	if err != nil {
		t.Fatalf("error = %v, want nil", err)
	}

	if runs.approval.RunID == "" {
		t.Fatalf("approval was not recorded, want approval")
	}

	if len(work.requests) != 1 || work.requests[0].Kind != KindStartTemplateApply {
		t.Fatalf("queued requests = %#v, want one approval signal", work.requests)
	}
}

func TestApproveRunSelfApprovalWorksForPlatformAdmins(t *testing.T) {
	t.Parallel()

	ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: keycloakSubject})
	runs := &recordingTemplateRunRepository{run: domain.TemplateRun{
		ID:              "run_123",
		TenantID:        "tenant_123",
		StackTemplateID: "stack_template_123",
		Status:          domain.TemplateRunWaitingApproval,
		TriggerActor:    domain.UserID(keycloakSubject),
	}}
	work := &recordingUnitOfWork{templateRuns: runs}
	audit := &recordingAuditRepository{}

	service := NewService(Service{
		Authorization:            testPlatformAuthorizer(t),
		Work:                     work,
		TemplateRuns:             runs,
		StackTemplates:           approvableStackTemplates(),
		TemplateRevisionMetadata: &recordingTemplateRepository{},
		Audit:                    audit,
	})

	err := service.ApproveRun(ctx, ApproveRunCommand{
		TenantID: domain.TenantID("tenant_123"),
		RunID:    domain.TemplateRunID("run_123"),
	})
	if err != nil {
		t.Fatalf("error = %v, want nil", err)
	}

	if len(work.requests) != 1 || work.requests[0].Kind != KindStartTemplateApply {
		t.Fatalf("queued requests = %#v, want one approval signal", work.requests)
	}
}

func TestApproveRunAuditsSuccessfulApproval(t *testing.T) {
	t.Parallel()

	ctx := authenticatedContext()
	now := time.Date(2026, 7, 2, 10, 15, 0, 0, time.UTC)
	runs := &recordingTemplateRunRepository{run: domain.TemplateRun{
		ID:              "run_123",
		TenantID:        "tenant_123",
		StackTemplateID: "stack_template_123",
		Status:          domain.TemplateRunWaitingApproval,
		TriggerActor:    domain.UserID("different-user"),
	}}
	work := &recordingUnitOfWork{templateRuns: runs}
	audit := &recordingAuditRepository{}

	service := NewService(Service{
		Authorization:            testPlatformAuthorizer(t),
		Work:                     work,
		TemplateRuns:             runs,
		StackTemplates:           approvableStackTemplates(),
		TemplateRevisionMetadata: &recordingTemplateRepository{},
		Clock:                    fixedClock{now: now},
		Audit:                    audit,
	})

	err := service.ApproveRun(ctx, ApproveRunCommand{
		TenantID: domain.TenantID("tenant_123"),
		RunID:    domain.TemplateRunID("run_123"),
	})
	if err != nil {
		t.Fatalf("ApproveRun returned error: %v", err)
	}

	if len(work.audits) != 1 {
		t.Fatalf("audit events = %d, want 1", len(work.audits))
	}
	if work.audits[0].Action != domain.AuditActionApprovalGranted {
		t.Fatalf("audit action = %q, want %q", work.audits[0].Action, domain.AuditActionApprovalGranted)
	}
	if work.audits[0].Outcome != domain.AuditOutcomeSuccess {
		t.Fatalf("audit outcome = %q, want %q", work.audits[0].Outcome, domain.AuditOutcomeSuccess)
	}
}

// A run with no plan waiting, such as one still planning or applying, has
// nothing to discard, and running runs cannot be stopped.
func TestDiscardRunRefusesARunWithNoPlanWaiting(t *testing.T) {
	t.Parallel()

	runs := &recordingTemplateRunRepository{run: domain.TemplateRun{ID: "run_123", TenantID: "tenant_123", StackTemplateID: "stack_template_123", Status: domain.TemplateRunRunning}}
	work := &recordingUnitOfWork{templateRuns: runs}

	service := NewService(Service{
		Authorization:  testPlatformAuthorizer(t),
		Work:           work,
		TemplateRuns:   runs,
		StackTemplates: &recordingStackTemplateRepository{stackTemplate: domain.StackTemplate{ID: "stack_template_123", TenantID: "tenant_123", StackID: "stack_123"}},
		Clock:          fixedClock{now: time.Now()},
	})

	err := service.DiscardRun(authenticatedContext(), DiscardRunCommand{
		TenantID: domain.TenantID("tenant_123"),
		RunID:    domain.TemplateRunID("run_123"),
	})
	if !errors.Is(err, ErrRunNotDiscardable) {
		t.Fatalf("error = %v, want ErrRunNotDiscardable", err)
	}
	if runs.discarded.RunID != "" {
		t.Fatalf("discarded = %#v, want nothing", runs.discarded)
	}

	if len(work.requests) != 0 {
		t.Fatalf("queued requests = %#v, want none", work.requests)
	}
}

func TestGetTemplateRunReturnsTenantScopedRun(t *testing.T) {
	t.Parallel()

	runs := &recordingTemplateRunRepository{
		run: domain.TemplateRun{
			ID:              domain.TemplateRunID("run_123"),
			TenantID:        domain.TenantID("tenant_123"),
			StackTemplateID: domain.StackTemplateID("stack_template_123"),
			Operation:       domain.OperationPlan,
			Status:          domain.TemplateRunCompleted,
		},
	}
	service := NewService(Service{
		Authorization:  testPlatformAuthorizer(t),
		TemplateRuns:   runs,
		StackTemplates: &recordingStackTemplateRepository{stackTemplate: domain.StackTemplate{ID: "stack_template_123", TenantID: "tenant_123", StackID: "stack_123"}},
	})
	ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: keycloakSubject})

	run, err := service.GetTemplateRun(ctx, GetTemplateRunCommand{
		TenantID: domain.TenantID("tenant_123"),
		RunID:    domain.TemplateRunID("run_123"),
	})
	if err != nil {
		t.Fatalf("GetTemplateRun returned error: %v", err)
	}

	if run.ID != domain.TemplateRunID("run_123") {
		t.Fatalf("run ID = %q, want run_123", run.ID)
	}
	if runs.gotGetTenantID != domain.TenantID("tenant_123") {
		t.Fatalf("tenant lookup = %q, want tenant_123", runs.gotGetTenantID)
	}
	if runs.gotGetRunID != domain.TemplateRunID("run_123") {
		t.Fatalf("run lookup = %q, want run_123", runs.gotGetRunID)
	}
}

func TestListTemplateRunsReturnsRunsScopedToStackTemplate(t *testing.T) {
	t.Parallel()

	runs := &recordingTemplateRunRepository{
		list: []domain.TemplateRun{
			{ID: domain.TemplateRunID("run_newer"), TenantID: domain.TenantID("tenant_123"), StackTemplateID: domain.StackTemplateID("stack_template_123"), Operation: domain.OperationPlan, Status: domain.TemplateRunWaitingApproval},
			{ID: domain.TemplateRunID("run_older"), TenantID: domain.TenantID("tenant_123"), StackTemplateID: domain.StackTemplateID("stack_template_123"), Operation: domain.OperationPlan, Status: domain.TemplateRunCompleted},
		},
	}
	service := NewService(Service{
		Authorization:  testPlatformAuthorizer(t),
		TemplateRuns:   runs,
		StackTemplates: &recordingStackTemplateRepository{stackTemplate: domain.StackTemplate{ID: "stack_template_123", TenantID: "tenant_123", StackID: "stack_123"}},
	})
	ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: keycloakSubject})

	got, err := service.ListTemplateRuns(ctx, ListTemplateRunsCommand{
		TenantID:        domain.TenantID("tenant_123"),
		StackTemplateID: domain.StackTemplateID("stack_template_123"),
	})
	if err != nil {
		t.Fatalf("ListTemplateRuns returned error: %v", err)
	}

	if runs.gotListTenantID != domain.TenantID("tenant_123") {
		t.Fatalf("tenant lookup = %q, want tenant_123", runs.gotListTenantID)
	}
	if runs.gotListStackTemplateID != domain.StackTemplateID("stack_template_123") {
		t.Fatalf("stack template lookup = %q, want stack_template_123", runs.gotListStackTemplateID)
	}
	if len(got) != 2 || got[0].ID != domain.TemplateRunID("run_newer") {
		t.Fatalf("runs = %#v, want run_newer first", got)
	}
}

func TestListTemplateRunsNormalizesNilAndRequiresStackTemplateID(t *testing.T) {
	t.Parallel()

	service := NewService(Service{
		Authorization:  testPlatformAuthorizer(t),
		TemplateRuns:   &recordingTemplateRunRepository{},
		StackTemplates: &recordingStackTemplateRepository{stackTemplate: domain.StackTemplate{ID: "stack_template_123", TenantID: "tenant_123", StackID: "stack_123"}},
	})
	ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: keycloakSubject})

	got, err := service.ListTemplateRuns(ctx, ListTemplateRunsCommand{
		TenantID:        domain.TenantID("tenant_123"),
		StackTemplateID: domain.StackTemplateID("stack_template_123"),
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

	_, err = service.ListTemplateRuns(ctx, ListTemplateRunsCommand{TenantID: domain.TenantID("tenant_123")})
	if !errors.Is(err, ErrInvalidCommand) {
		t.Fatalf("error = %v, want ErrInvalidCommand", err)
	}
}

func TestGetTemplateRunRejectsMissingRunID(t *testing.T) {
	t.Parallel()

	service := NewService(Service{TemplateRuns: &recordingTemplateRunRepository{}})

	_, err := service.GetTemplateRun(context.Background(), GetTemplateRunCommand{
		TenantID: domain.TenantID("tenant_123"),
	})
	if !errors.Is(err, ErrInvalidCommand) {
		t.Fatalf("error = %v, want ErrInvalidCommand", err)
	}
}

func TestGetTemplateRegistrationReturnsTenantScopedRegistration(t *testing.T) {
	t.Parallel()

	registrations := &recordingTemplateRegistrationRepository{
		registration: domain.TemplateRegistration{
			ID:       domain.TemplateRegistrationID("template_registration_123"),
			TenantID: domain.TenantID("tenant_123"),
			Status:   domain.TemplateRegistrationCompleted,
		},
	}
	service := NewService(Service{TemplateRegistrations: registrations, Authorization: testPlatformAuthorizer(t)})

	registration, err := service.GetTemplateRegistration(authenticatedContext(), GetTemplateRegistrationCommand{
		TenantID:       domain.TenantID("tenant_123"),
		RegistrationID: domain.TemplateRegistrationID("template_registration_123"),
	})
	if err != nil {
		t.Fatalf("GetTemplateRegistration returned error: %v", err)
	}

	if registration.ID != domain.TemplateRegistrationID("template_registration_123") {
		t.Fatalf("registration ID = %q, want template_registration_123", registration.ID)
	}
	if registrations.gotGetTenantID != domain.TenantID("tenant_123") {
		t.Fatalf("tenant lookup = %q, want tenant_123", registrations.gotGetTenantID)
	}
}

func TestGetTemplateRevisionVariablesReturnsTenantScopedVariables(t *testing.T) {
	t.Parallel()

	templates := &recordingTemplateRepository{
		variables: []domain.TemplateVariable{
			{
				TemplateRevisionID: domain.TemplateRevisionID("template_123"),
				Name:               "region",
				TypeExpression:     "string",
				Required:           true,
			},
		},
	}
	service := NewService(Service{TemplateRevisions: templates, Authorization: testPlatformAuthorizer(t)})

	variables, err := service.GetTemplateRevisionVariables(authenticatedContext(), GetTemplateRevisionVariablesCommand{
		TenantID:           domain.TenantID("tenant_123"),
		TemplateRevisionID: domain.TemplateRevisionID("template_123"),
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
	if templates.gotVariablesTenantID != domain.TenantID("tenant_123") {
		t.Fatalf("tenant lookup = %q, want tenant_123", templates.gotVariablesTenantID)
	}
}

func TestGetTemplateRunLogChecksRunOwnershipBeforeReadingLog(t *testing.T) {
	t.Parallel()

	runs := &recordingTemplateRunRepository{
		run: domain.TemplateRun{
			ID:              domain.TemplateRunID("run_123"),
			TenantID:        domain.TenantID("tenant_123"),
			StackTemplateID: domain.StackTemplateID("stack_template_123"),
		},
	}
	logs := &recordingTemplateRunLogReader{content: []byte("plan output\n")}
	metadata := &recordingTemplateRunLogRepository{
		log: domain.TemplateRunLog{
			TenantID:  domain.TenantID("tenant_123"),
			RunID:     domain.TemplateRunID("run_123"),
			Phase:     "plan",
			ObjectKey: "tenants/tenant_123/runs/run_123/logs/plan.log",
		},
	}
	service := NewService(Service{
		Authorization:          testPlatformAuthorizer(t),
		TemplateRuns:           runs,
		StackTemplates:         &recordingStackTemplateRepository{stackTemplate: domain.StackTemplate{ID: "stack_template_123", StackID: "stack_123"}},
		TemplateRunLogs:        logs,
		TemplateRunLogMetadata: metadata,
	})

	content, err := service.GetTemplateRunLog(authenticatedContext(), GetTemplateRunLogCommand{
		TenantID: domain.TenantID("tenant_123"),
		RunID:    domain.TemplateRunID("run_123"),
		Phase:    "plan",
	})
	if err != nil {
		t.Fatalf("GetTemplateRunLog returned error: %v", err)
	}

	if string(content) != "plan output\n" {
		t.Fatalf("content = %q, want plan output", string(content))
	}
	if runs.gotGetRunID != domain.TemplateRunID("run_123") {
		t.Fatalf("run lookup = %q, want run_123", runs.gotGetRunID)
	}
	if logs.gotTenantID != domain.TenantID("tenant_123") {
		t.Fatalf("log tenant = %q, want tenant_123", logs.gotTenantID)
	}
	if logs.gotRunID != domain.TemplateRunID("run_123") {
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
		Authorization:          testPlatformAuthorizer(t),
		TemplateRuns:           runs,
		TemplateRunLogs:        logs,
		TemplateRunLogMetadata: &recordingTemplateRunLogRepository{},
	})

	_, err := service.GetTemplateRunLog(authenticatedContext(), GetTemplateRunLogCommand{
		TenantID: domain.TenantID("tenant_123"),
		RunID:    domain.TemplateRunID("run_123"),
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
		run: domain.TemplateRun{
			ID:              domain.TemplateRunID("run_123"),
			TenantID:        domain.TenantID("tenant_123"),
			StackTemplateID: domain.StackTemplateID("stack_template_123"),
		},
	}
	logs := &recordingTemplateRunLogReader{content: []byte("plan output\n")}
	metadata := &recordingTemplateRunLogRepository{getErr: ErrNotFound}
	service := NewService(Service{
		Authorization:          testPlatformAuthorizer(t),
		TemplateRuns:           runs,
		StackTemplates:         &recordingStackTemplateRepository{stackTemplate: domain.StackTemplate{ID: "stack_template_123", TenantID: "tenant_123", StackID: "stack_123"}},
		TemplateRunLogs:        logs,
		TemplateRunLogMetadata: metadata,
	})

	ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: keycloakSubject})
	_, err := service.GetTemplateRunLog(ctx, GetTemplateRunLogCommand{
		TenantID: domain.TenantID("tenant_123"),
		RunID:    domain.TemplateRunID("run_123"),
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
		run: domain.TemplateRun{
			ID:              domain.TemplateRunID("run_123"),
			TenantID:        domain.TenantID("tenant_123"),
			StackTemplateID: domain.StackTemplateID("stack_template_123"),
		},
	}
	logs := &recordingTemplateRunLogReader{err: os.ErrNotExist}
	metadata := &recordingTemplateRunLogRepository{
		log: domain.TemplateRunLog{
			TenantID:  domain.TenantID("tenant_123"),
			RunID:     domain.TemplateRunID("run_123"),
			Phase:     "plan",
			ObjectKey: "tenants/tenant_123/runs/run_123/logs/plan.log",
		},
	}
	service := NewService(Service{
		Authorization:          testPlatformAuthorizer(t),
		TemplateRuns:           runs,
		StackTemplates:         &recordingStackTemplateRepository{stackTemplate: domain.StackTemplate{ID: "stack_template_123", StackID: "stack_123"}},
		TemplateRunLogs:        logs,
		TemplateRunLogMetadata: metadata,
	})

	_, err := service.GetTemplateRunLog(authenticatedContext(), GetTemplateRunLogCommand{
		TenantID: domain.TenantID("tenant_123"),
		RunID:    domain.TemplateRunID("run_123"),
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
		TenantID: domain.TenantID("tenant_123"),
		RunID:    domain.TemplateRunID("run_123"),
		Phase:    "../plan",
	})
	if !errors.Is(err, ErrInvalidCommand) {
		t.Fatalf("error = %v, want ErrInvalidCommand", err)
	}
}

func TestListTemplateRunLogsChecksRunOwnershipBeforeListingMetadata(t *testing.T) {
	t.Parallel()

	runs := &recordingTemplateRunRepository{
		run: domain.TemplateRun{
			ID:       domain.TemplateRunID("run_123"),
			TenantID: domain.TenantID("tenant_123"),
		},
	}
	metadata := &recordingTemplateRunLogRepository{
		logs: []domain.TemplateRunLog{
			{
				TenantID:    domain.TenantID("tenant_123"),
				RunID:       domain.TemplateRunID("run_123"),
				Phase:       "init",
				ObjectKey:   "tenants/tenant_123/runs/run_123/logs/init.log",
				ContentType: "text/plain; charset=utf-8",
				SizeBytes:   12,
				UploadedAt:  time.Date(2026, 7, 6, 10, 15, 0, 0, time.UTC),
			},
		},
	}
	service := NewService(Service{
		Authorization:          testPlatformAuthorizer(t),
		TemplateRuns:           runs,
		StackTemplates:         &recordingStackTemplateRepository{stackTemplate: domain.StackTemplate{ID: "stack_template_123", TenantID: "tenant_123", StackID: "stack_123"}},
		TemplateRunLogMetadata: metadata,
	})

	logs, err := service.ListTemplateRunLogs(authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: keycloakSubject}), ListTemplateRunLogsCommand{
		TenantID: domain.TenantID("tenant_123"),
		RunID:    domain.TemplateRunID("run_123"),
	})
	if err != nil {
		t.Fatalf("ListTemplateRunLogs returned error: %v", err)
	}

	if len(logs) != 1 {
		t.Fatalf("len(logs) = %d, want 1", len(logs))
	}
	if metadata.gotListRunID != domain.TemplateRunID("run_123") {
		t.Fatalf("metadata run lookup = %q, want run_123", metadata.gotListRunID)
	}
}

type recordingAuditRepository struct {
	events []domain.SecurityAuditEvent
}

func (repository *recordingAuditRepository) AppendAuditEvent(_ context.Context, event domain.SecurityAuditEvent) error {
	repository.events = append(repository.events, event)
	return nil
}

type failingAuditRepository struct{}

func (failingAuditRepository) AppendAuditEvent(_ context.Context, _ domain.SecurityAuditEvent) error {
	return errors.New("database unavailable")
}

func TestCreateStackAuditsOwnerGrant(t *testing.T) {
	t.Parallel()

	ctx := authenticatedContext()
	now := time.Date(2026, 7, 6, 13, 30, 0, 0, time.UTC)
	work := newRecordingWork(&recordingStackRepository{})
	service := NewService(Service{
		Stacks:        &recordingStackRepository{},
		Work:          work,
		Authorization: testPlatformAuthorizer(t),
		StackIDs:      fixedStackIDGenerator{id: domain.StackID("stack_new")},
		Clock:         fixedClock{now: now},
	})

	_, err := service.CreateStack(ctx, CreateStackCommand{
		TenantID: domain.TenantID("tenant_123"),
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
	if event.Action != domain.AuditActionGrant {
		t.Fatalf("action = %q, want %q", event.Action, domain.AuditActionGrant)
	}
	if event.TargetUser != keycloakSubject {
		t.Fatalf("target_user = %q, want %q", event.TargetUser, keycloakSubject)
	}
	if event.TenantID != domain.TenantID("tenant_123") {
		t.Fatalf("tenant_id = %q, want tenant_123", event.TenantID)
	}
	if event.StackID != domain.StackID("stack_new") {
		t.Fatalf("stack_id = %q, want stack_new", event.StackID)
	}
	if event.NewRole != "owner" {
		t.Fatalf("new_role = %q, want owner", event.NewRole)
	}
	if event.Outcome != domain.AuditOutcomeSuccess {
		t.Fatalf("outcome = %q, want %q", event.Outcome, domain.AuditOutcomeSuccess)
	}
}

func TestCreateStackAuditWriteFailureDoesNotBlockMutation(t *testing.T) {
	t.Parallel()

	ctx := authenticatedContext()
	service := NewService(Service{
		Stacks:        &recordingStackRepository{},
		Work:          newRecordingWork(&recordingStackRepository{}),
		Authorization: testPlatformAuthorizer(t),
		StackIDs:      fixedStackIDGenerator{id: domain.StackID("stack_new")},
		Clock:         fixedClock{now: time.Now()},
		Audit:         failingAuditRepository{},
	})

	stack, err := service.CreateStack(ctx, CreateStackCommand{
		TenantID: domain.TenantID("tenant_123"),
		Name:     "Acme Prod",
		Tags:     map[string]string{},
	})
	if err != nil {
		t.Fatalf("CreateStack returned error: %v", err)
	}
	if stack.ID != domain.StackID("stack_new") {
		t.Fatalf("stack ID = %q, want stack_new", stack.ID)
	}
}

func TestCreateStackWithNilAuditRepositoryDoesNotPanic(t *testing.T) {
	t.Parallel()

	ctx := authenticatedContext()
	service := NewService(Service{
		Stacks:        &recordingStackRepository{},
		Work:          newRecordingWork(&recordingStackRepository{}),
		Authorization: testPlatformAuthorizer(t),
		StackIDs:      fixedStackIDGenerator{id: domain.StackID("stack_new")},
		Clock:         fixedClock{now: time.Now()},
		Audit:         nil,
	})

	_, err := service.CreateStack(ctx, CreateStackCommand{
		TenantID: domain.TenantID("tenant_123"),
		Name:     "Acme Prod",
		Tags:     map[string]string{},
	})
	if err != nil {
		t.Fatalf("CreateStack returned error: %v", err)
	}
}

func TestAddTemplateToStackAuditsAuthorizationDenial(t *testing.T) {
	t.Parallel()

	audit := &recordingAuditRepository{}
	service := NewService(Service{
		Authorization: newTestAuthorization(t),
		Stacks: &recordingStackRepository{
			stack: domain.Stack{ID: "stack_123", TenantID: "tenant_123", Slug: "acme-prod"},
		},
		TemplateRevisionMetadata: &recordingTemplateRepository{
			template: domain.TemplateRevision{ID: "template_123", TenantID: "tenant_123", Status: domain.TemplateRevisionActive},
		},
		TemplateRevisions: &recordingTemplateRepository{
			variables: []domain.TemplateVariable{{Name: "region", Required: true, HasDefault: true}},
		},
		StackTemplateInstaller: &recordingStackTemplateInstaller{},
		StackTemplateIDs:       fixedStackTemplateIDGenerator{id: domain.StackTemplateID("stack_template_a1b2c3d4")},
		Audit:                  audit,
	})

	ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{
		Subject: keycloakSubject,
	})

	_, err := service.AddTemplateToStack(ctx, AddTemplateToStackCommand{
		TenantID:           domain.TenantID("tenant_123"),
		StackID:            domain.StackID("stack_123"),
		TemplateRevisionID: domain.TemplateRevisionID("template_123"),
		ConfigJSON:         json.RawMessage(`{"region":"us-east-1"}`),
	})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("error = %v, want ErrForbidden", err)
	}

	if len(audit.events) != 1 {
		t.Fatalf("audit events = %d, want 1", len(audit.events))
	}
	event := audit.events[0]
	if event.Action != domain.AuditActionFailedAccessAttempt {
		t.Fatalf("action = %q, want %q", event.Action, domain.AuditActionFailedAccessAttempt)
	}
	if event.Outcome != domain.AuditOutcomeFailure {
		t.Fatalf("outcome = %q, want %q", event.Outcome, domain.AuditOutcomeFailure)
	}
	if event.ActorSubject != keycloakSubject {
		t.Fatalf("actor_subject = %q, want %q", event.ActorSubject, keycloakSubject)
	}
}

type recordingStackTemplateRepository struct {
	stackTemplate                domain.StackTemplate
	gotTenantID                  domain.TenantID
	gotID                        domain.StackTemplateID
	gotConfigJSON                json.RawMessage
	gotDesiredTemplateRevisionID domain.TemplateRevisionID
}

func (repository *recordingStackTemplateRepository) GetStackTemplate(_ context.Context, tenantID domain.TenantID, id domain.StackTemplateID) (domain.StackTemplate, error) {
	repository.gotTenantID = tenantID
	repository.gotID = id
	stackTemplate := repository.stackTemplate
	if stackTemplate.StackID == "" {
		stackTemplate.StackID = "stack_123"
	}
	return stackTemplate, nil
}

func (repository *recordingStackTemplateRepository) UpdateStackTemplateConfig(_ context.Context, tenantID domain.TenantID, id domain.StackTemplateID, configJSON json.RawMessage) (domain.StackTemplate, error) {
	repository.gotTenantID = tenantID
	repository.gotID = id
	repository.gotConfigJSON = configJSON
	updated := repository.stackTemplate
	updated.DesiredConfigJSON = configJSON
	return updated, nil
}

func (repository *recordingStackTemplateRepository) UpdateStackTemplateDesiredRevision(_ context.Context, tenantID domain.TenantID, id domain.StackTemplateID, templateRevisionID domain.TemplateRevisionID, configJSON json.RawMessage) (domain.StackTemplate, error) {
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
	created         domain.Stack
	stack           domain.Stack
	list            []domain.Stack
	view            StackView
	gotTenantID     domain.TenantID
	gotStackID      domain.StackID
	gotListTenantID domain.TenantID
	createErr       error
	getErr          error
	listErr         error
	getViewErr      error
}

func (repository *recordingStackRepository) CreateStack(_ context.Context, stack domain.Stack) error {
	if repository.createErr != nil {
		return repository.createErr
	}
	repository.created = stack
	return nil
}

func (repository *recordingStackRepository) GetStack(_ context.Context, tenantID domain.TenantID, stackID domain.StackID) (domain.Stack, error) {
	repository.gotTenantID = tenantID
	repository.gotStackID = stackID
	if repository.getErr != nil {
		return domain.Stack{}, repository.getErr
	}
	return repository.stack, nil
}

func (repository *recordingStackRepository) GetStackWithTemplates(_ context.Context, tenantID domain.TenantID, stackID domain.StackID) (StackView, error) {
	repository.gotTenantID = tenantID
	repository.gotStackID = stackID
	if repository.getViewErr != nil {
		return StackView{}, repository.getViewErr
	}
	return repository.view, nil
}

func (repository *recordingStackRepository) ListStacks(_ context.Context, tenantID domain.TenantID) ([]domain.Stack, error) {
	repository.gotListTenantID = tenantID
	if repository.listErr != nil {
		return nil, repository.listErr
	}
	return repository.list, nil
}

func (repository *recordingStackRepository) ListStacksPage(_ context.Context, tenantID domain.TenantID, after *StackPageCursor, limit int) ([]domain.Stack, error) {
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
	return append([]domain.Stack(nil), repository.list[start:end]...), nil
}

type recordingTemplateRunRepository struct {
	created                domain.TemplateRun
	run                    domain.TemplateRun
	list                   []domain.TemplateRun
	approval               domain.TemplateRunApproval
	gotGetTenantID         domain.TenantID
	gotGetRunID            domain.TemplateRunID
	gotListTenantID        domain.TenantID
	gotListStackTemplateID domain.StackTemplateID
	getErr                 error
	createErr              error
	approvalErr            error
	discarded              domain.TemplateRunDiscard
	// createdRunNumber is what CreateTemplateRun reports it assigned.
	createdRunNumber int
}

func (repository *recordingTemplateRunRepository) CreateTemplateRun(_ context.Context, run domain.TemplateRun) (int, error) {
	if repository.createErr != nil {
		return 0, repository.createErr
	}
	repository.created = run
	return repository.createdRunNumber, nil
}

func (repository *recordingTemplateRunRepository) GetTemplateRun(_ context.Context, tenantID domain.TenantID, runID domain.TemplateRunID) (domain.TemplateRun, error) {
	repository.gotGetTenantID = tenantID
	repository.gotGetRunID = runID
	if repository.getErr != nil {
		return domain.TemplateRun{}, repository.getErr
	}
	return repository.run, nil
}

func (repository *recordingTemplateRunRepository) ListTemplateRuns(_ context.Context, tenantID domain.TenantID, stackTemplateID domain.StackTemplateID) ([]domain.TemplateRun, error) {
	repository.gotListTenantID = tenantID
	repository.gotListStackTemplateID = stackTemplateID
	return repository.list, nil
}

func (repository *recordingTemplateRunRepository) ApproveTemplateRun(_ context.Context, approval domain.TemplateRunApproval) error {
	if repository.approvalErr != nil {
		return repository.approvalErr
	}
	repository.approval = approval
	return nil
}

// DiscardTemplateRun discards only a run waiting for approval or approved, as
// the real store does.
func (repository *recordingTemplateRunRepository) DiscardTemplateRun(_ context.Context, discard domain.TemplateRunDiscard) (bool, error) {
	if repository.run.Status != domain.TemplateRunWaitingApproval && repository.run.Status != domain.TemplateRunApproved {
		return false, nil
	}
	repository.discarded = discard
	return true, nil
}

type recordingTemplateRunLogReader struct {
	content      []byte
	err          error
	gotTenantID  domain.TenantID
	gotRunID     domain.TemplateRunID
	gotPhase     string
	gotObjectKey string
}

func (reader *recordingTemplateRunLogReader) ReadTemplateRunLog(_ context.Context, log domain.TemplateRunLog) ([]byte, error) {
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
	log             domain.TemplateRunLog
	logs            []domain.TemplateRunLog
	gotGetTenantID  domain.TenantID
	gotGetRunID     domain.TemplateRunID
	gotGetPhase     string
	gotListTenantID domain.TenantID
	gotListRunID    domain.TemplateRunID
	getErr          error
	listErr         error
}

func (repository *recordingTemplateRunLogRepository) GetTemplateRunLog(_ context.Context, tenantID domain.TenantID, runID domain.TemplateRunID, phase string) (domain.TemplateRunLog, error) {
	repository.gotGetTenantID = tenantID
	repository.gotGetRunID = runID
	repository.gotGetPhase = phase
	if repository.getErr != nil {
		return domain.TemplateRunLog{}, repository.getErr
	}
	return repository.log, nil
}

func (repository *recordingTemplateRunLogRepository) ListTemplateRunLogs(_ context.Context, tenantID domain.TenantID, runID domain.TemplateRunID) ([]domain.TemplateRunLog, error) {
	repository.gotListTenantID = tenantID
	repository.gotListRunID = runID
	if repository.listErr != nil {
		return nil, repository.listErr
	}
	return repository.logs, nil
}

type recordingTemplateRegistrationRepository struct {
	created        domain.TemplateRegistration
	registration   domain.TemplateRegistration
	gotGetTenantID domain.TenantID
	gotGetID       domain.TemplateRegistrationID
	createErr      error
	getErr         error
	statusInput    domain.TemplateRegistrationStatusActivityInput
	statusErr      error
}

func (repository *recordingTemplateRegistrationRepository) CreateTemplateRegistration(_ context.Context, registration domain.TemplateRegistration) error {
	if repository.createErr != nil {
		return repository.createErr
	}
	repository.created = registration
	return nil
}

func (repository *recordingTemplateRegistrationRepository) GetTemplateRegistration(_ context.Context, tenantID domain.TenantID, id domain.TemplateRegistrationID) (domain.TemplateRegistration, error) {
	repository.gotGetTenantID = tenantID
	repository.gotGetID = id
	if repository.getErr != nil {
		return domain.TemplateRegistration{}, repository.getErr
	}
	return repository.registration, nil
}

func (repository *recordingTemplateRegistrationRepository) RecordTemplateRegistrationStatus(_ context.Context, input domain.TemplateRegistrationStatusActivityInput) error {
	if repository.statusErr != nil {
		return repository.statusErr
	}
	repository.statusInput = input
	return nil
}

// approvableStackTemplates is a stack template whose pending plan is run_123
// and still describes desired state, which is what ApproveRun requires.
func approvableStackTemplates() *recordingStackTemplateRepository {
	return &recordingStackTemplateRepository{stackTemplate: domain.StackTemplate{
		ID:                    "stack_template_123",
		TenantID:              "tenant_123",
		StackID:               "stack_123",
		DesiredConfigJSON:     json.RawMessage(`{}`),
		PendingPlanRunID:      "run_123",
		PendingPlanConfigJSON: json.RawMessage(`{}`),
	}}
}

type recordingTemplateRepository struct {
	template                       domain.TemplateRevision
	templates                      []domain.TemplateRevision
	variables                      []domain.TemplateVariable
	gotTemplate                    domain.TemplateRevision
	gotVariables                   []domain.TemplateVariable
	gotListTenantID                domain.TenantID
	gotGetTemplateTenantID         domain.TenantID
	gotGetTemplateRevisionID       domain.TemplateRevisionID
	gotVariablesTenantID           domain.TenantID
	gotVariablesTemplateRevisionID domain.TemplateRevisionID
	getTemplateErr                 error
	listErr                        error
	upsertErr                      error
	variablesErr                   error
}

func (repository *recordingTemplateRepository) UpsertTemplateRevisionWithVariables(_ context.Context, template domain.TemplateRevision, variables []domain.TemplateVariable) (domain.TemplateRevision, error) {
	repository.gotTemplate = template
	repository.gotVariables = variables
	if repository.upsertErr != nil {
		return domain.TemplateRevision{}, repository.upsertErr
	}
	if repository.template.ID != "" {
		return repository.template, nil
	}
	return template, nil
}

func (repository *recordingTemplateRepository) GetTemplateRevision(_ context.Context, tenantID domain.TenantID, templateRevisionID domain.TemplateRevisionID) (domain.TemplateRevision, error) {
	repository.gotGetTemplateTenantID = tenantID
	repository.gotGetTemplateRevisionID = templateRevisionID
	if repository.getTemplateErr != nil {
		return domain.TemplateRevision{}, repository.getTemplateErr
	}
	return repository.template, nil
}

func (repository *recordingTemplateRepository) ListTemplateRevisions(_ context.Context, tenantID domain.TenantID) ([]domain.TemplateRevision, error) {
	repository.gotListTenantID = tenantID
	if repository.listErr != nil {
		return nil, repository.listErr
	}
	return repository.templates, nil
}

func (repository *recordingTemplateRepository) GetTemplateRevisionVariables(_ context.Context, tenantID domain.TenantID, templateRevisionID domain.TemplateRevisionID) ([]domain.TemplateVariable, error) {
	repository.gotVariablesTenantID = tenantID
	repository.gotVariablesTemplateRevisionID = templateRevisionID
	if repository.variablesErr != nil {
		return nil, repository.variablesErr
	}
	return repository.variables, nil
}

type recordingWorkflowDispatcher struct {
	input                 domain.TemplateRunWorkflowInput
	startTemplateRunCalls int
	syncInput             domain.TemplateSyncWorkflowInput
	approvalRunID         domain.TemplateRunID
}

func (dispatcher *recordingWorkflowDispatcher) StartTemplateRun(_ context.Context, input domain.TemplateRunWorkflowInput) error {
	dispatcher.startTemplateRunCalls++
	dispatcher.input = input
	return nil
}

type recordingStackTemplateInstaller struct {
	created   domain.StackTemplate
	createErr error
}

func (installer *recordingStackTemplateInstaller) CreateStackTemplate(_ context.Context, stackTemplate domain.StackTemplate) error {
	if installer.createErr != nil {
		return installer.createErr
	}
	installer.created = stackTemplate
	return nil
}

func (dispatcher *recordingWorkflowDispatcher) StartTemplateSync(_ context.Context, input domain.TemplateSyncWorkflowInput) error {
	dispatcher.syncInput = input
	return nil
}

// StartTemplateApply records a direct start, which the service never makes:
// approval queues the start instead, in the approval's transaction.
func (dispatcher *recordingWorkflowDispatcher) StartTemplateApply(_ context.Context, input domain.TemplateRunWorkflowInput) error {
	dispatcher.approvalRunID = input.RunID
	return nil
}

type fixedTemplateRunIDGenerator struct {
	runID domain.TemplateRunID
}

type fixedStackTemplateIDGenerator struct {
	id domain.StackTemplateID
}

func (generator fixedStackTemplateIDGenerator) NewStackTemplateID() domain.StackTemplateID {
	return generator.id
}

func (generator fixedTemplateRunIDGenerator) NewTemplateRunID() domain.TemplateRunID {
	return generator.runID
}

type fixedTemplateRegistrationIDGenerator struct {
	id domain.TemplateRegistrationID
}

func (generator fixedTemplateRegistrationIDGenerator) NewTemplateRegistrationID() domain.TemplateRegistrationID {
	return generator.id
}

type fixedStackIDGenerator struct {
	id domain.StackID
}

func (generator fixedStackIDGenerator) NewStackID() domain.StackID {
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
	audits                []domain.SecurityAuditEvent
	requests              []queue.Request
	err                   error
	inTxCalls             int
}

func newRecordingWork(stacks StackRepository) *recordingUnitOfWork {
	return &recordingUnitOfWork{stacks: stacks}
}

func (unit *recordingUnitOfWork) InTx(ctx context.Context, fn func(context.Context, TxRepo, queue.Enqueuer) error) error {
	unit.inTxCalls++
	if unit.err != nil {
		return unit.err
	}
	// No transaction is installed on the context: these tests run against an
	// in-memory engine, where an authorization write takes the datastore's
	// non-transactional path. What is atomic here is asserted against Postgres
	// in internal/authorization's own suite.
	return fn(ctx, unit, unit)
}

func (unit *recordingUnitOfWork) CreateStack(ctx context.Context, stack domain.Stack) error {
	if unit.stacks == nil {
		return nil
	}
	return unit.stacks.CreateStack(ctx, stack)
}

func (unit *recordingUnitOfWork) AppendAuditEvent(_ context.Context, event domain.SecurityAuditEvent) error {
	unit.audits = append(unit.audits, event)
	return nil
}

func (unit *recordingUnitOfWork) CreateTemplateRun(ctx context.Context, run domain.TemplateRun) (int, error) {
	if unit.templateRuns == nil {
		return 0, nil
	}
	return unit.templateRuns.CreateTemplateRun(ctx, run)
}

func (unit *recordingUnitOfWork) CreateTemplateRegistration(ctx context.Context, registration domain.TemplateRegistration) error {
	if unit.templateRegistrations == nil {
		return nil
	}
	return unit.templateRegistrations.CreateTemplateRegistration(ctx, registration)
}

func (unit *recordingUnitOfWork) ApproveTemplateRun(ctx context.Context, approval domain.TemplateRunApproval) error {
	if unit.templateRuns == nil {
		return nil
	}
	return unit.templateRuns.ApproveTemplateRun(ctx, approval)
}

func (unit *recordingUnitOfWork) DiscardTemplateRun(ctx context.Context, discard domain.TemplateRunDiscard) (bool, error) {
	discarder, ok := unit.templateRuns.(interface {
		DiscardTemplateRun(context.Context, domain.TemplateRunDiscard) (bool, error)
	})
	if !ok {
		return false, nil
	}
	return discarder.DiscardTemplateRun(ctx, discard)
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
		Authorization:         testPlatformAuthorizer(t),
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
		Work:          work,
		Authorization: testPlatformAuthorizer(t),
		TemplateRuns:  runs,
		StackTemplates: &recordingStackTemplateRepository{stackTemplate: domain.StackTemplate{
			ID: "stack_template_123", TenantID: "tenant_123", SourceTemplateID: "source_123", DesiredTemplateRevisionID: "revision_123", DesiredConfigJSON: json.RawMessage(`{"region":"us-east-1"}`), WorkspaceName: "workspace", Lifecycle: domain.StackTemplateActive,
			PendingPlanRunID: "run_plan_1", PendingPlanTemplateRevisionID: "revision_123", PendingPlanConfigJSON: json.RawMessage(`{"region":"us-east-1"}`),
		}},
		TemplateRevisionMetadata: &recordingTemplateRepository{template: domain.TemplateRevision{ID: "revision_123", TenantID: "tenant_123", SourceTemplateID: "source_123", RepoOwner: "acme", RepoName: "infra", SourceRef: "main", ResolvedCommitSHA: "sha_123", RootPath: "modules/vpc"}},
		RunIDs:                   fixedTemplateRunIDGenerator{runID: "run_123"},
		Clock:                    fixedClock{now: time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC)},
	})

	run, err := service.StartTemplateRun(authenticatedContext(), StartTemplateRunCommand{TenantID: "tenant_123", StackTemplateID: "stack_template_123", Operation: domain.OperationPlan})
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
	// The executor checks out this commit. Without it in the payload the run would
	// fall back to the installed ref, which is a moving target and is never
	// updated when the desired revision changes.
	if payload.ResolvedCommitSHA != "sha_123" {
		t.Fatalf("payload.ResolvedCommitSHA = %q, want sha_123", payload.ResolvedCommitSHA)
	}
}

func TestApproveRunPairsApprovalAuditAndApplyIntentInTransaction(t *testing.T) {
	t.Parallel()

	runs := &recordingTemplateRunRepository{run: domain.TemplateRun{ID: "run_123", TenantID: "tenant_123", StackTemplateID: "stack_template_123", Status: domain.TemplateRunWaitingApproval}}
	work := &recordingUnitOfWork{templateRuns: runs}
	workflows := &recordingWorkflowDispatcher{}
	service := NewService(Service{
		Work:                     work,
		Authorization:            testPlatformAuthorizer(t),
		TemplateRuns:             runs,
		StackTemplates:           approvableStackTemplates(),
		TemplateRevisionMetadata: &recordingTemplateRepository{},
		Workflows:                workflows,
		Clock:                    fixedClock{now: time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC)},
	})

	if err := service.ApproveRun(authenticatedContext(), ApproveRunCommand{TenantID: "tenant_123", RunID: "run_123"}); err != nil {
		t.Fatalf("ApproveRun returned error: %v", err)
	}
	if work.inTxCalls != 1 || runs.approval.RunID != "run_123" || len(work.audits) != 1 {
		t.Fatalf("transaction calls = %d, approval = %#v, audits = %#v", work.inTxCalls, runs.approval, work.audits)
	}
	if len(work.requests) != 1 || work.requests[0].Kind != KindStartTemplateApply || work.requests[0].ActorSubject != keycloakSubject || work.requests[0].TenantID != "tenant_123" || workflows.approvalRunID != "" {
		t.Fatalf("requests = %#v, direct approval = %q", work.requests, workflows.approvalRunID)
	}
	var payload StartTemplateApplyPayload
	if err := json.Unmarshal(work.requests[0].Payload, &payload); err != nil {
		t.Fatalf("decode apply intent: %v", err)
	}
	if payload.TenantID != "tenant_123" || payload.RunID != "run_123" {
		t.Fatalf("apply payload = %#v", payload)
	}
}

func adminContext() context.Context {
	return authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: "admin_123"})
}

// The inverse of what this asserted before: the role change was queued because
// a tuple write could not commit with the audit event. It can now, so the write
// happens in the request and the queue is not involved.
func TestAssignStackRoleWritesTheGrantInTheSameTransaction(t *testing.T) {
	t.Parallel()

	authorizer := testPlatformAuthorizer(t)
	work := newRecordingWork(nil)
	users := &fakeUserRepository{users: []UserProfile{{Sub: "user_456", DisplayName: "Casey Jones", Email: "casey@example.com"}}}
	service := NewService(Service{Work: work, Authorization: authorizer, Users: users, Clock: fixedClock{now: time.Now()}})

	view, err := service.AssignStackRole(adminContext(), AssignStackRoleCommand{
		TenantID: domain.TenantID("tenant_123"),
		StackID:  domain.StackID("stack_abc"),
		UserSub:  "user_456",
		Role:     "operator",
	})
	if err != nil {
		t.Fatalf("AssignStackRole returned error: %v", err)
	}
	if view.Role != "operator" || view.UserSub != "user_456" {
		t.Fatalf("view = %#v", view)
	}
	if len(work.requests) != 0 {
		t.Fatalf("enqueued %d requests, want 0 -- the grant is part of the commit", len(work.requests))
	}

	// The role is in effect when the call returns, not eventually.
	allowed, err := authorizer.Can(context.Background(), "user_456", authorization.RelationCanOperate, mustStackObject(t, "stack_abc"))
	if err != nil {
		t.Fatalf("Can() error = %v", err)
	}
	if !allowed {
		t.Fatal("the assigned role is not in effect when AssignStackRole returns")
	}
}

// Re-assigning a role the subject already holds converges rather than failing.
// OpenFGA rejects a write of an existing tuple, so a naive implementation would
// error on a repeated request; the reconcile skips what is already held.
func TestAssignStackRoleIsIdempotentForAHeldRole(t *testing.T) {
	t.Parallel()

	authorizer := seedGrants(t, testPlatformAuthorizer(t), mustGrant(t, "user_456", "stack_abc", authorization.RelationOperator))
	work := newRecordingWork(nil)
	users := &fakeUserRepository{users: []UserProfile{{Sub: "user_456", DisplayName: "Casey Jones", Email: "casey@example.com"}}}
	service := NewService(Service{Work: work, Authorization: authorizer, Users: users, Clock: fixedClock{now: time.Now()}})

	if _, err := service.AssignStackRole(adminContext(), AssignStackRoleCommand{
		TenantID: domain.TenantID("tenant_123"),
		StackID:  domain.StackID("stack_abc"),
		UserSub:  "user_456",
		Role:     "operator",
	}); err != nil {
		t.Fatalf("AssignStackRole returned error: %v", err)
	}

	grants, err := authorizer.ListGrants(context.Background(), mustStackObject(t, "stack_abc"))
	if err != nil {
		t.Fatalf("ListGrants() error = %v", err)
	}
	if len(grants) != 1 || grants[0].Relation() != authorization.RelationOperator {
		t.Fatalf("grants = %#v, want exactly one operator grant", grants)
	}
}

// Changing a role replaces it rather than adding to it, in one transaction, so
// there is no window in which the subject holds both or neither.
func TestAssignStackRoleReplacesAStaleRole(t *testing.T) {
	t.Parallel()

	authorizer := seedGrants(t, testPlatformAuthorizer(t), mustGrant(t, "user_456", "stack_abc", authorization.RelationOperator))
	users := &fakeUserRepository{users: []UserProfile{{Sub: "user_456", DisplayName: "Casey Jones", Email: "casey@example.com"}}}
	service := NewService(Service{Work: newRecordingWork(nil), Authorization: authorizer, Users: users, Clock: fixedClock{now: time.Now()}})

	if _, err := service.AssignStackRole(adminContext(), AssignStackRoleCommand{
		TenantID: domain.TenantID("tenant_123"),
		StackID:  domain.StackID("stack_abc"),
		UserSub:  "user_456",
		Role:     "viewer",
	}); err != nil {
		t.Fatalf("AssignStackRole returned error: %v", err)
	}

	grants, err := authorizer.ListGrants(context.Background(), mustStackObject(t, "stack_abc"))
	if err != nil {
		t.Fatalf("ListGrants() error = %v", err)
	}
	if len(grants) != 1 || grants[0].Relation() != authorization.RelationViewer {
		t.Fatalf("grants = %#v, want exactly one viewer grant", grants)
	}
}

func TestRevokeStackRoleRemovesTheHeldRole(t *testing.T) {
	t.Parallel()

	authorizer := seedGrants(t, testPlatformAuthorizer(t), mustGrant(t, "user_456", "stack_abc", authorization.RelationOperator))
	service := NewService(Service{Work: newRecordingWork(nil), Authorization: authorizer, Clock: fixedClock{now: time.Now()}})

	if err := service.RevokeStackRole(adminContext(), RevokeStackRoleCommand{
		TenantID: domain.TenantID("tenant_123"),
		StackID:  domain.StackID("stack_abc"),
		UserSub:  "user_456",
	}); err != nil {
		t.Fatalf("RevokeStackRole returned error: %v", err)
	}

	grants, err := authorizer.ListGrants(context.Background(), mustStackObject(t, "stack_abc"))
	if err != nil {
		t.Fatalf("ListGrants() error = %v", err)
	}
	if len(grants) != 0 {
		t.Fatalf("grants = %#v, want none after a revoke", grants)
	}
}

// Revoking access the subject does not hold is not an error: they asked for the
// subject to have none, and the subject already has none.
func TestRevokeStackRoleIsANoOpWhenNothingIsHeld(t *testing.T) {
	t.Parallel()

	authorizer := testPlatformAuthorizer(t)
	service := NewService(Service{Work: newRecordingWork(nil), Authorization: authorizer, Clock: fixedClock{now: time.Now()}})

	if err := service.RevokeStackRole(adminContext(), RevokeStackRoleCommand{
		TenantID: domain.TenantID("tenant_123"),
		StackID:  domain.StackID("stack_abc"),
		UserSub:  "user_456",
	}); err != nil {
		t.Fatalf("RevokeStackRole returned error: %v", err)
	}
}

func mustGrant(t *testing.T, sub, stackID string, relation authorization.Relation) authorization.Grant {
	t.Helper()
	subject, err := authorization.SubjectFromOIDCSub(sub)
	if err != nil {
		t.Fatalf("SubjectFromOIDCSub: %v", err)
	}
	grant, err := authorization.NewGrant(subject, mustStackObject(t, stackID), relation)
	if err != nil {
		t.Fatalf("NewGrant: %v", err)
	}
	return grant
}

func TestAssignStackRoleWithoutUnitOfWorkFails(t *testing.T) {
	t.Parallel()

	service := NewService(Service{Authorization: testPlatformAuthorizer(t), Clock: fixedClock{now: time.Now()}})

	_, err := service.AssignStackRole(adminContext(), AssignStackRoleCommand{
		TenantID: domain.TenantID("tenant_123"),
		StackID:  domain.StackID("stack_abc"),
		UserSub:  "user_456",
		Role:     "operator",
	})
	if err == nil {
		t.Fatalf("error = %v, want ErrUnavailable", err)
	}
}

// TestDefaultIDGeneratorsMintPrefixedRandomIDs locks the identifier shape the
// five default generators share. They now differ only in the prefix handed to
// randomID, so a wrong prefix is the one mistake the collapse made possible and
// the one this test is here to catch.
func TestDefaultIDGeneratorsMintPrefixedRandomIDs(t *testing.T) {
	t.Parallel()

	generators := map[string]func() string{
		"run":                   func() string { return string(randomTemplateRunIDGenerator{}.NewTemplateRunID()) },
		"template_registration": func() string { return string(randomTemplateRegistrationIDGenerator{}.NewTemplateRegistrationID()) },
		"stack":                 func() string { return string(randomStackIDGenerator{}.NewStackID()) },
		"stack_template":        func() string { return string(randomStackTemplateIDGenerator{}.NewStackTemplateID()) },
		"credential":            func() string { return string(randomCredentialSetIDGenerator{}.NewCredentialSetID()) },
	}

	for prefix, generate := range generators {
		t.Run(prefix, func(t *testing.T) {
			t.Parallel()

			id := generate()
			suffix, found := strings.CutPrefix(id, prefix+"_")
			if !found {
				t.Fatalf("id = %q, want prefix %q", id, prefix+"_")
			}
			// 16 random bytes, hex-encoded.
			if decoded, err := hex.DecodeString(suffix); err != nil || len(decoded) != 16 {
				t.Fatalf("id = %q, want %q followed by 32 hex characters", id, prefix+"_")
			}
			if second := generate(); second == id {
				t.Fatalf("two %s ids are both %q, want distinct values", prefix, id)
			}
		})
	}
}

// Every mutating command that reaches a stack template through
// operableStackTemplate must record the refusal, not just refuse. Only
// AddTemplateToStack had such a test, so three of the four copies of the audit
// block this replaced were unguarded — dropping the audit call from any of them
// would have left the suite green.
func TestOperableStackTemplateCommandsAuditRefusals(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		call func(*Service, context.Context) error
	}{
		{"StartTemplateRun", func(service *Service, ctx context.Context) error {
			_, err := service.StartTemplateRun(ctx, StartTemplateRunCommand{
				TenantID:        domain.TenantID("tenant_123"),
				StackTemplateID: domain.StackTemplateID("stack_template_123"),
				Operation:       domain.OperationPlan,
			})
			return err
		}},
		{"UpdateStackTemplateConfig", func(service *Service, ctx context.Context) error {
			_, err := service.UpdateStackTemplateConfig(ctx, UpdateStackTemplateConfigCommand{
				TenantID:        domain.TenantID("tenant_123"),
				StackTemplateID: domain.StackTemplateID("stack_template_123"),
				ConfigJSON:      json.RawMessage(`{"region":"us-east-1"}`),
			})
			return err
		}},
		{"UpgradeStackTemplate", func(service *Service, ctx context.Context) error {
			_, err := service.UpgradeStackTemplate(ctx, UpgradeStackTemplateCommand{
				TenantID:                 domain.TenantID("tenant_123"),
				StackTemplateID:          domain.StackTemplateID("stack_template_123"),
				TargetTemplateRevisionID: domain.TemplateRevisionID("template_456"),
			})
			return err
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			audit := &recordingAuditRepository{}
			service := NewService(Service{
				TemplateRuns:  &recordingTemplateRunRepository{},
				Authorization: newTestAuthorization(t),
				StackTemplates: &recordingStackTemplateRepository{
					stackTemplate: domain.StackTemplate{
						ID:        domain.StackTemplateID("stack_template_123"),
						Lifecycle: domain.StackTemplateActive,
					},
				},
				Audit: audit,
				Clock: fixedClock{now: time.Now()},
			})

			err := test.call(service, authenticatedContext())
			if !errors.Is(err, ErrForbidden) {
				t.Fatalf("error = %v, want ErrForbidden", err)
			}

			if len(audit.events) != 1 {
				t.Fatalf("audit events = %d, want 1", len(audit.events))
			}
			event := audit.events[0]
			if event.Action != domain.AuditActionFailedAccessAttempt {
				t.Fatalf("action = %q, want %q", event.Action, domain.AuditActionFailedAccessAttempt)
			}
			if event.Outcome != domain.AuditOutcomeFailure {
				t.Fatalf("outcome = %q, want %q", event.Outcome, domain.AuditOutcomeFailure)
			}
			if event.ActorSubject != keycloakSubject {
				t.Fatalf("actor_subject = %q, want %q", event.ActorSubject, keycloakSubject)
			}
			if event.TenantID != domain.TenantID("tenant_123") {
				t.Fatalf("tenant_id = %q, want %q", event.TenantID, "tenant_123")
			}
		})
	}
}
