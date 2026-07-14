package activities

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vishu42/tflive/internal/traits"
)

func TestRecordTemplateRegistrationStatusDelegatesToRecorder(t *testing.T) {
	t.Parallel()

	store := &recordingTemplateSyncStore{}
	activities := NewTemplateSyncActivities(store)
	input := traits.TemplateRegistrationStatusActivityInput{
		RegistrationID: traits.TemplateRegistrationID("template_registration_123"),
		TenantID:       traits.TenantID("tenant_123"),
		Status:         traits.TemplateRegistrationRunning,
	}

	if err := activities.RecordTemplateRegistrationStatus(context.Background(), input); err != nil {
		t.Fatalf("RecordTemplateRegistrationStatus returned error: %v", err)
	}
	if store.statusInput != input {
		t.Fatalf("status input = %#v, want %#v", store.statusInput, input)
	}
}

func TestSyncTemplateClonesPublicRepoAndPersistsMetadata(t *testing.T) {
	t.Parallel()

	store := &recordingTemplateSyncStore{}
	gitRunner := &recordingGitRunner{
		commitSHA: "abc123",
		populate: func(repoPath string) error {
			root := filepath.Join(repoPath, "modules", "vpc")
			if err := os.MkdirAll(root, 0o700); err != nil {
				return err
			}
			writeFile(t, filepath.Join(root, "template.yaml"), "name: vpc\ndescription: Creates a VPC\ntags:\n  - network\n  - aws\n")
			writeFile(t, filepath.Join(root, "variables.tf"), `
variable "region" {
  type        = string
  description = "AWS region"
}

variable "cidr" {
  type    = string
  default = "10.0.0.0/16"
  validation {
    condition     = length(var.cidr) > 0
    error_message = "cidr is required"
  }
}
`)
			return nil
		},
	}
	activities := NewTemplateSyncActivities(
		store,
		WithTemplateSyncGitRunner(gitRunner),
		WithTemplateSyncTempRoot(t.TempDir()),
	)

	output, err := activities.SyncTemplate(context.Background(), traits.TemplateSyncActivityInput{
		RegistrationID: traits.TemplateRegistrationID("template_registration_123"),
		TenantID:       traits.TenantID("tenant_123"),
		RepoOwner:      "acme",
		RepoName:       "infra-templates",
		SourceRef:      "v0.0.1",
		RootPath:       "modules/vpc",
	})
	if err != nil {
		t.Fatalf("SyncTemplate returned error: %v", err)
	}

	if output.Status != traits.TemplateRegistrationCompleted {
		t.Fatalf("status = %q, want completed", output.Status)
	}
	if output.ResolvedCommitSHA != "abc123" {
		t.Fatalf("resolved sha = %q, want abc123", output.ResolvedCommitSHA)
	}
	if gitRunner.repoURL != "https://github.com/acme/infra-templates.git" {
		t.Fatalf("repo URL = %q", gitRunner.repoURL)
	}
	if gitRunner.ref != "v0.0.1" {
		t.Fatalf("ref = %q, want v0.0.1", gitRunner.ref)
	}
	if store.template.Name != "vpc" {
		t.Fatalf("template name = %q, want vpc", store.template.Name)
	}
	if store.template.ResolvedCommitSHA != "abc123" {
		t.Fatalf("template sha = %q, want abc123", store.template.ResolvedCommitSHA)
	}
	if len(store.variables) != 2 {
		t.Fatalf("len(variables) = %d, want 2", len(store.variables))
	}
	if store.variables[0].Name != "cidr" || !store.variables[0].HasDefault || !store.variables[0].HasValidation {
		t.Fatalf("cidr variable = %#v", store.variables[0])
	}
	if store.variables[1].Name != "region" || !store.variables[1].Required || store.variables[1].Description != "AWS region" {
		t.Fatalf("region variable = %#v", store.variables[1])
	}
}

func TestSyncTemplateRejectsUnsafeRootPath(t *testing.T) {
	t.Parallel()

	store := &recordingTemplateSyncStore{}
	gitRunner := &recordingGitRunner{commitSHA: "abc123"}
	activities := NewTemplateSyncActivities(
		store,
		WithTemplateSyncGitRunner(gitRunner),
		WithTemplateSyncTempRoot(t.TempDir()),
	)

	output, err := activities.SyncTemplate(context.Background(), traits.TemplateSyncActivityInput{
		TenantID:  traits.TenantID("tenant_123"),
		RepoOwner: "acme",
		RepoName:  "infra-templates",
		SourceRef: "v0.0.1",
		RootPath:  "../secret",
	})
	if err != nil {
		t.Fatalf("SyncTemplate returned error: %v", err)
	}
	if output.Status != traits.TemplateRegistrationInvalid {
		t.Fatalf("status = %q, want invalid", output.Status)
	}
	if !strings.Contains(output.ErrorSummary, "root path") {
		t.Fatalf("error summary = %q, want root path context", output.ErrorSummary)
	}
}

func TestSyncTemplateSupportsRepositoryRootPath(t *testing.T) {
	t.Parallel()

	store := &recordingTemplateSyncStore{}
	gitRunner := &recordingGitRunner{
		commitSHA: "abc123",
		populate: func(repoPath string) error {
			if err := os.MkdirAll(repoPath, 0o700); err != nil {
				return err
			}
			writeFile(t, filepath.Join(repoPath, "variables.tf"), `variable "region" { type = string }`)
			return nil
		},
	}
	activities := NewTemplateSyncActivities(
		store,
		WithTemplateSyncGitRunner(gitRunner),
		WithTemplateSyncTempRoot(t.TempDir()),
	)

	output, err := activities.SyncTemplate(context.Background(), traits.TemplateSyncActivityInput{
		TenantID:  traits.TenantID("tenant_123"),
		RepoOwner: "acme",
		RepoName:  "infra-templates",
		SourceRef: "v0.0.1",
		RootPath:  ".",
	})
	if err != nil {
		t.Fatalf("SyncTemplate returned error: %v", err)
	}
	if output.Status != traits.TemplateRegistrationCompleted {
		t.Fatalf("status = %q, want completed", output.Status)
	}
	if store.template.RootPath != "." {
		t.Fatalf("root path = %q, want .", store.template.RootPath)
	}
	if store.template.Name != "infra-templates" {
		t.Fatalf("template name = %q, want repo name fallback", store.template.Name)
	}
}

func TestSyncTemplateRejectsSensitiveVariables(t *testing.T) {
	t.Parallel()

	store := &recordingTemplateSyncStore{}
	gitRunner := &recordingGitRunner{
		commitSHA: "abc123",
		populate: func(repoPath string) error {
			root := filepath.Join(repoPath, "modules", "db")
			if err := os.MkdirAll(root, 0o700); err != nil {
				return err
			}
			writeFile(t, filepath.Join(root, "variables.tf"), `
variable "password" {
  type      = string
  sensitive = true
}
`)
			return nil
		},
	}
	activities := NewTemplateSyncActivities(
		store,
		WithTemplateSyncGitRunner(gitRunner),
		WithTemplateSyncTempRoot(t.TempDir()),
	)

	output, err := activities.SyncTemplate(context.Background(), traits.TemplateSyncActivityInput{
		TenantID:  traits.TenantID("tenant_123"),
		RepoOwner: "acme",
		RepoName:  "infra-templates",
		SourceRef: "v0.0.1",
		RootPath:  "modules/db",
	})
	if err != nil {
		t.Fatalf("SyncTemplate returned error: %v", err)
	}
	if output.Status != traits.TemplateRegistrationInvalid {
		t.Fatalf("status = %q, want invalid", output.Status)
	}
	if !strings.Contains(output.ErrorSummary, "sensitive variables") {
		t.Fatalf("error summary = %q", output.ErrorSummary)
	}
	if store.template.ID != "" {
		t.Fatalf("template revision ID = %q, want no persisted template revision", store.template.ID)
	}
}

func TestSyncTemplateReturnsExistingImmutableTemplate(t *testing.T) {
	t.Parallel()

	store := &recordingTemplateSyncStore{
		upsertTemplate: traits.TemplateRevision{
			ID:                traits.TemplateRevisionID("template_existing"),
			TenantID:          traits.TenantID("tenant_123"),
			ResolvedCommitSHA: "abc123",
		},
	}
	gitRunner := &recordingGitRunner{
		commitSHA: "abc123",
		populate: func(repoPath string) error {
			root := filepath.Join(repoPath, "modules", "vpc")
			if err := os.MkdirAll(root, 0o700); err != nil {
				return err
			}
			writeFile(t, filepath.Join(root, "variables.tf"), `variable "region" { type = string }`)
			return nil
		},
	}
	activities := NewTemplateSyncActivities(
		store,
		WithTemplateSyncGitRunner(gitRunner),
		WithTemplateSyncTempRoot(t.TempDir()),
	)

	output, err := activities.SyncTemplate(context.Background(), traits.TemplateSyncActivityInput{
		TenantID:  traits.TenantID("tenant_123"),
		RepoOwner: "acme",
		RepoName:  "infra-templates",
		SourceRef: "v0.0.1",
		RootPath:  "modules/vpc",
	})
	if err != nil {
		t.Fatalf("SyncTemplate returned error: %v", err)
	}
	if output.TemplateRevisionID != traits.TemplateRevisionID("template_existing") {
		t.Fatalf("template revision ID = %q, want template_existing", output.TemplateRevisionID)
	}
}

type recordingTemplateSyncStore struct {
	statusInput    traits.TemplateRegistrationStatusActivityInput
	statusErr      error
	template       traits.TemplateRevision
	variables      []traits.TemplateVariable
	upsertTemplate traits.TemplateRevision
	upsertErr      error
}

func (store *recordingTemplateSyncStore) RecordTemplateRegistrationStatus(_ context.Context, input traits.TemplateRegistrationStatusActivityInput) error {
	if store.statusErr != nil {
		return store.statusErr
	}
	store.statusInput = input
	return nil
}

func (store *recordingTemplateSyncStore) UpsertTemplateRevisionWithVariables(_ context.Context, template traits.TemplateRevision, variables []traits.TemplateVariable) (traits.TemplateRevision, error) {
	store.template = template
	store.variables = variables
	if store.upsertErr != nil {
		return traits.TemplateRevision{}, store.upsertErr
	}
	if store.upsertTemplate.ID != "" {
		return store.upsertTemplate, nil
	}
	return template, nil
}

type recordingGitRunner struct {
	repoURL   string
	ref       string
	dest      string
	commitSHA string
	populate  func(string) error
}

func (runner *recordingGitRunner) Clone(ctx context.Context, repoURL string, ref string, dest string) error {
	_ = ctx
	runner.repoURL = repoURL
	runner.ref = ref
	runner.dest = dest
	if runner.populate != nil {
		return runner.populate(dest)
	}
	return nil
}

func (runner *recordingGitRunner) ResolveHead(context.Context, string) (string, error) {
	return runner.commitSHA, nil
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
