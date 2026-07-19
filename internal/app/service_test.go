package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/vishu42/tflive/internal/authn"
	"github.com/vishu42/tflive/internal/traits"
)

const keycloakSubject = "6fdb4b4c-2a8f-4cf7-945f-38f67f6a0e91"

func authenticatedContext() context.Context {
	return authn.ContextWithPrincipal(context.Background(), authn.Principal{
		Subject: keycloakSubject, RealmRoles: []string{"platform-admin"},
	})
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
		Authorizer: &recordingAuthorizer{},
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
	authorizer := &recordingAuthorizer{}
	service := NewService(Service{
		Stacks:     stacks,
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
		Stacks:   &recordingStackRepository{},
		StackIDs: fixedStackIDGenerator{id: traits.StackID("stack_123")},
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
		Stacks:   &recordingStackRepository{},
		StackIDs: fixedStackIDGenerator{id: traits.StackID("stack_123")},
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

func TestListStacksPassesTenantAndNormalizesNilStacks(t *testing.T) {
	t.Parallel()

	stacks := &recordingStackRepository{list: nil}
	service := NewService(Service{Stacks: stacks, Authorizer: &permissionAuthorizer{}})
	ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{Subject: keycloakSubject, RealmRoles: []string{"platform-admin"}})

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
	service := NewService(Service{TemplateRevisions: templates})

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
		Stacks:                   stacks,
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
		SelectedRef:        "main",
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
	if string(installer.created.ConfigJSON) != `{"region":"us-east-1"}` {
		t.Fatalf("config json = %s", installer.created.ConfigJSON)
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
		SelectedRef:        "main",
		ConfigJSON:         json.RawMessage(`{}`),
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
		SelectedRef:        "main",
		ConfigJSON:         json.RawMessage(`{"region":"us-east-1","extra":"nope"}`),
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
		SelectedRef:        "main",
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
			SelectedRef:               "main",
			WorkspaceName:             "mtp_acme_prod_vpc_a13f9c",
			Lifecycle:                 traits.StackTemplateActive,
		},
	}
	templates := &recordingTemplateRepository{
		template: traits.TemplateRevision{
			ID:                traits.TemplateRevisionID("template_rev_2"),
			TenantID:          traits.TenantID("tenant_123"),
			SourceTemplateID:  traits.SourceTemplateID("source_template_vpc"),
			RepoOwner:         "acme",
			RepoName:          "infra-templates",
			ResolvedCommitSHA: "sha-2",
			RootPath:          "modules/vpc",
			Status:            traits.TemplateRevisionActive,
		},
	}
	runs := &recordingTemplateRunRepository{run: traits.TemplateRun{ID: "run_123", TenantID: "tenant_123", StackTemplateID: "stack_template_123"}}
	workflows := &recordingWorkflowDispatcher{}

	service := NewService(Service{
		StackTemplates:           stackTemplates,
		TemplateRuns:             runs,
		TemplateRevisionMetadata: templates,
		Workflows:                workflows,
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

	if workflows.startTemplateRunCalls != 0 {
		t.Fatalf("workflow dispatch calls = %d, want 0", workflows.startTemplateRunCalls)
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
		Workflows:      &recordingWorkflowDispatcher{},
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
			SelectedRef:   "main",
			WorkspaceName: "mtp_acme_prod_vpc_a13f9c",
			Lifecycle:     traits.StackTemplateDestroyed,
		},
	}
	runs := &recordingTemplateRunRepository{}
	workflows := &recordingWorkflowDispatcher{}

	service := NewService(Service{
		StackTemplates: stackTemplates,
		TemplateRuns:   runs,
		Workflows:      workflows,
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

	if workflows.input.RunID != "" {
		t.Fatalf("workflow run ID = %q, want no workflow dispatch", workflows.input.RunID)
	}
}

func TestStartTemplateRunRejectsMissingDesiredRevision(t *testing.T) {
	t.Parallel()

	stackTemplates := &recordingStackTemplateRepository{
		stackTemplate: traits.StackTemplate{
			ID:            traits.StackTemplateID("stack_template_123"),
			SelectedRef:   "main",
			WorkspaceName: "mtp_acme_prod_vpc_a13f9c",
			Lifecycle:     traits.StackTemplateActive,
		},
	}
	runs := &recordingTemplateRunRepository{}
	workflows := &recordingWorkflowDispatcher{}

	service := NewService(Service{
		StackTemplates: stackTemplates,
		TemplateRuns:   runs,
		Workflows:      workflows,
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
	if workflows.input.RunID != "" {
		t.Fatalf("workflow run ID = %q, want no workflow dispatch", workflows.input.RunID)
	}
}

func TestStartTemplateRunUsesDefaultRunIDGenerator(t *testing.T) {
	t.Parallel()

	stackTemplates := &recordingStackTemplateRepository{
		stackTemplate: traits.StackTemplate{
			ID:                        traits.StackTemplateID("stack_template_123"),
			DesiredTemplateRevisionID: traits.TemplateRevisionID("template_123"),
			SelectedRef:               "main",
			WorkspaceName:             "mtp_acme_prod_vpc_a13f9c",
			Lifecycle:                 traits.StackTemplateActive,
		},
	}
	runs := &recordingTemplateRunRepository{}
	service := NewService(Service{
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
		Workflows: &recordingWorkflowDispatcher{},
		Clock:     fixedClock{now: time.Now()},
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
	workflows := &recordingWorkflowDispatcher{}

	service := NewService(Service{
		TemplateRegistrations: registrations,
		Workflows:             workflows,
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
	if workflows.syncInput.RegistrationID != registration.ID {
		t.Fatalf("workflow registration ID = %q, want %q", workflows.syncInput.RegistrationID, registration.ID)
	}
	if workflows.syncInput.SourceRef != "v0.0.1" {
		t.Fatalf("workflow source ref = %q, want v0.0.1", workflows.syncInput.SourceRef)
	}
}

func TestRegisterTemplateRejectsMissingSourceRef(t *testing.T) {
	t.Parallel()

	service := NewService(Service{
		TemplateRegistrations: &recordingTemplateRegistrationRepository{},
		Workflows:             &recordingWorkflowDispatcher{},
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
	workflows := &recordingWorkflowDispatcher{}
	service := NewService(Service{
		TemplateRegistrations: registrations,
		Workflows:             workflows,
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
	if workflows.syncInput.RegistrationID != "" {
		t.Fatalf("workflow registration ID = %q, want no dispatch", workflows.syncInput.RegistrationID)
	}
}

func TestApproveRunRecordsApprovalAndSignalsWorkflow(t *testing.T) {
	t.Parallel()

	ctx := authenticatedContext()
	now := time.Date(2026, 7, 2, 10, 15, 0, 0, time.UTC)
	runs := &recordingTemplateRunRepository{}
	workflows := &recordingWorkflowDispatcher{}

	service := NewService(Service{
		Authorizer:     &permissionAuthorizer{allowed: true},
		TemplateRuns:   runs,
		StackTemplates: &recordingStackTemplateRepository{stackTemplate: traits.StackTemplate{ID: "stack_template_123", TenantID: "tenant_123", StackID: "stack_123"}},
		Workflows:      workflows,
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

	if workflows.approvalRunID != traits.TemplateRunID("run_123") {
		t.Fatalf("workflow approval run ID = %q, want run_123", workflows.approvalRunID)
	}

	if workflows.approvalSignal.ApprovedBy != traits.UserID(keycloakSubject) {
		t.Fatalf("workflow approval actor = %q, want %q", workflows.approvalSignal.ApprovedBy, keycloakSubject)
	}
}

func TestApproveRunDoesNotSignalWhenRunIsNotApprovable(t *testing.T) {
	t.Parallel()

	runs := &recordingTemplateRunRepository{run: traits.TemplateRun{ID: "run_123", TenantID: "tenant_123", StackTemplateID: "stack_template_123"}, approvalErr: ErrRunNotApprovable}
	workflows := &recordingWorkflowDispatcher{}

	service := NewService(Service{
		Authorizer:     &permissionAuthorizer{allowed: true},
		TemplateRuns:   runs,
		StackTemplates: &recordingStackTemplateRepository{stackTemplate: traits.StackTemplate{ID: "stack_template_123", TenantID: "tenant_123", StackID: "stack_123"}},
		Workflows:      workflows,
		Clock:          fixedClock{now: time.Now()},
	})

	err := service.ApproveRun(authenticatedContext(), ApproveRunCommand{
		TenantID: traits.TenantID("tenant_123"),
		RunID:    traits.TemplateRunID("run_123"),
	})
	if !errors.Is(err, ErrRunNotApprovable) {
		t.Fatalf("error = %v, want ErrRunNotApprovable", err)
	}

	if workflows.approvalRunID != "" {
		t.Fatalf("workflow approval run ID = %q, want no workflow signal", workflows.approvalRunID)
	}
}

func TestCancelRunRecordsCancellationAndSignalsWorkflow(t *testing.T) {
	t.Parallel()

	ctx := authenticatedContext()
	now := time.Date(2026, 7, 2, 10, 45, 0, 0, time.UTC)
	runs := &recordingTemplateRunRepository{}
	workflows := &recordingWorkflowDispatcher{}

	service := NewService(Service{
		TemplateRuns: runs,
		Workflows:    workflows,
		Clock:        fixedClock{now: now},
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

	if workflows.cancelRunID != traits.TemplateRunID("run_123") {
		t.Fatalf("workflow cancel run ID = %q, want run_123", workflows.cancelRunID)
	}

	if workflows.cancelSignal.RequestedBy != traits.UserID(keycloakSubject) {
		t.Fatalf("workflow cancel actor = %q, want %q", workflows.cancelSignal.RequestedBy, keycloakSubject)
	}
}

func TestCancelRunDoesNotSignalWhenRunIsNotCancelable(t *testing.T) {
	t.Parallel()

	runs := &recordingTemplateRunRepository{run: traits.TemplateRun{ID: "run_123", TenantID: "tenant_123", StackTemplateID: "stack_template_123"}, cancellationErr: ErrRunNotCancelable}
	workflows := &recordingWorkflowDispatcher{}

	service := NewService(Service{
		Authorizer:     &permissionAuthorizer{allowed: true},
		TemplateRuns:   runs,
		StackTemplates: &recordingStackTemplateRepository{stackTemplate: traits.StackTemplate{ID: "stack_template_123", TenantID: "tenant_123", StackID: "stack_123"}},
		Workflows:      workflows,
		Clock:          fixedClock{now: time.Now()},
	})

	err := service.CancelRun(authenticatedContext(), CancelRunCommand{
		TenantID: traits.TenantID("tenant_123"),
		RunID:    traits.TemplateRunID("run_123"),
	})
	if !errors.Is(err, ErrRunNotCancelable) {
		t.Fatalf("error = %v, want ErrRunNotCancelable", err)
	}

	if workflows.cancelRunID != "" {
		t.Fatalf("workflow cancel run ID = %q, want no workflow signal", workflows.cancelRunID)
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
	service := NewService(Service{TemplateRegistrations: registrations})

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
	service := NewService(Service{TemplateRevisions: templates})

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
		TemplateRuns:           runs,
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
			ID:       traits.TemplateRunID("run_123"),
			TenantID: traits.TenantID("tenant_123"),
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
		TemplateRuns:           runs,
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
	return repository.stackTemplate, nil
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
	created         traits.TemplateRun
	run             traits.TemplateRun
	approval        traits.TemplateRunApproval
	cancellation    traits.TemplateRunCancellation
	gotGetTenantID  traits.TenantID
	gotGetRunID     traits.TemplateRunID
	getErr          error
	approvalErr     error
	cancellationErr error
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
