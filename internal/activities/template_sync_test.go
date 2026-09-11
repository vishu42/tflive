package activities

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vishu42/tflive/internal/domain"
	"github.com/vishu42/tflive/internal/githubapp"
	gitrunner "github.com/vishu42/tflive/internal/runner"
)

func TestRecordTemplateRegistrationStatusDelegatesToRecorder(t *testing.T) {
	t.Parallel()

	store := &recordingTemplateSyncStore{}
	activities := NewTemplateSyncActivities(store)
	input := domain.TemplateRegistrationStatusActivityInput{
		RegistrationID: domain.TemplateRegistrationID("template_registration_123"),
		TenantID:       domain.TenantID("tenant_123"),
		Status:         domain.TemplateRegistrationRunning,
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

	output, err := activities.SyncTemplate(context.Background(), domain.TemplateSyncActivityInput{
		RegistrationID: domain.TemplateRegistrationID("template_registration_123"),
		TenantID:       domain.TenantID("tenant_123"),
		RepoOwner:      "acme",
		RepoName:       "infra-templates",
		SourceRef:      "v0.0.1",
		RootPath:       "modules/vpc",
	})
	if err != nil {
		t.Fatalf("SyncTemplate returned error: %v", err)
	}

	if output.Status != domain.TemplateRegistrationCompleted {
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

	output, err := activities.SyncTemplate(context.Background(), domain.TemplateSyncActivityInput{
		TenantID:  domain.TenantID("tenant_123"),
		RepoOwner: "acme",
		RepoName:  "infra-templates",
		SourceRef: "v0.0.1",
		RootPath:  "../secret",
	})
	if err != nil {
		t.Fatalf("SyncTemplate returned error: %v", err)
	}
	if output.Status != domain.TemplateRegistrationInvalid {
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

	output, err := activities.SyncTemplate(context.Background(), domain.TemplateSyncActivityInput{
		TenantID:  domain.TenantID("tenant_123"),
		RepoOwner: "acme",
		RepoName:  "infra-templates",
		SourceRef: "v0.0.1",
		RootPath:  ".",
	})
	if err != nil {
		t.Fatalf("SyncTemplate returned error: %v", err)
	}
	if output.Status != domain.TemplateRegistrationCompleted {
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

	output, err := activities.SyncTemplate(context.Background(), domain.TemplateSyncActivityInput{
		TenantID:  domain.TenantID("tenant_123"),
		RepoOwner: "acme",
		RepoName:  "infra-templates",
		SourceRef: "v0.0.1",
		RootPath:  "modules/db",
	})
	if err != nil {
		t.Fatalf("SyncTemplate returned error: %v", err)
	}
	if output.Status != domain.TemplateRegistrationInvalid {
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
		upsertTemplate: domain.TemplateRevision{
			ID:                domain.TemplateRevisionID("template_existing"),
			TenantID:          domain.TenantID("tenant_123"),
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

	output, err := activities.SyncTemplate(context.Background(), domain.TemplateSyncActivityInput{
		TenantID:  domain.TenantID("tenant_123"),
		RepoOwner: "acme",
		RepoName:  "infra-templates",
		SourceRef: "v0.0.1",
		RootPath:  "modules/vpc",
	})
	if err != nil {
		t.Fatalf("SyncTemplate returned error: %v", err)
	}
	if output.TemplateRevisionID != domain.TemplateRevisionID("template_existing") {
		t.Fatalf("template revision ID = %q, want template_existing", output.TemplateRevisionID)
	}
}

type recordingTemplateSyncStore struct {
	statusInput    domain.TemplateRegistrationStatusActivityInput
	statusErr      error
	template       domain.TemplateRevision
	variables      []domain.TemplateVariable
	upsertTemplate domain.TemplateRevision
	upsertErr      error
}

func (store *recordingTemplateSyncStore) RecordTemplateRegistrationStatus(_ context.Context, input domain.TemplateRegistrationStatusActivityInput) error {
	if store.statusErr != nil {
		return store.statusErr
	}
	store.statusInput = input
	return nil
}

func (store *recordingTemplateSyncStore) UpsertTemplateRevisionWithVariables(_ context.Context, template domain.TemplateRevision, variables []domain.TemplateVariable) (domain.TemplateRevision, error) {
	store.template = template
	store.variables = variables
	if store.upsertErr != nil {
		return domain.TemplateRevision{}, store.upsertErr
	}
	if store.upsertTemplate.ID != "" {
		return store.upsertTemplate, nil
	}
	return template, nil
}

type recordingGitRunner struct {
	repoURL    string
	ref        string
	dest       string
	commitSHA  string
	credential gitrunner.GitCredential
	populate   func(string) error
	err        error
}

func (runner *recordingGitRunner) Clone(ctx context.Context, repoURL string, ref string, dest string, credential gitrunner.GitCredential) error {
	_ = ctx
	runner.credential = credential
	runner.repoURL = repoURL
	runner.ref = ref
	runner.dest = dest
	if runner.err != nil {
		return runner.err
	}
	if runner.populate != nil {
		return runner.populate(dest)
	}
	return nil
}

// CheckoutCommit exists to satisfy runner.GitRunner and fails loudly: template
// sync is the path that resolves a ref to a commit in the first place, so it
// has no commit to check out and must clone the ref.
func (runner *recordingGitRunner) CheckoutCommit(context.Context, string, string, string, gitrunner.GitCredential) error {
	return errors.New("template sync must clone a ref, not check out a commit")
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

// The credential must reach the clone. Without this the whole feature is a
// no-op that still passes every other test.
func TestSyncTemplatePassesInstallationTokenToClone(t *testing.T) {
	t.Parallel()

	store := &recordingTemplateSyncStore{}
	gitRunner := &recordingGitRunner{
		commitSHA: "abc123",
		populate: func(repoPath string) error {
			return os.MkdirAll(filepath.Join(repoPath, "modules", "vpc"), 0o700)
		},
	}
	activities := NewTemplateSyncActivities(
		store,
		WithTemplateSyncGitRunner(gitRunner),
		WithTemplateSyncTempRoot(t.TempDir()),
		WithTemplateSyncTokenSource(stubTokenSource{token: "ghs_minted"}),
	)

	if _, err := activities.SyncTemplate(context.Background(), domain.TemplateSyncActivityInput{
		TenantID:  domain.TenantID("tenant_123"),
		RepoOwner: "acme",
		RepoName:  "private-infra",
		SourceRef: "main",
		RootPath:  "modules/vpc",
	}); err != nil {
		t.Fatalf("SyncTemplate returned error: %v", err)
	}

	if gitRunner.credential != gitrunner.NewGitCredential("ghs_minted") {
		t.Fatal("clone did not receive the installation credential")
	}
}

// A missing installation is not itself a failure -- see the doc comment on
// repoCredential's call site in SyncTemplate. The clone still goes ahead
// unauthenticated, and only once that clone fails for its own reason does the
// missing installation become the user's problem. That failure must say what
// to do, and must be classified non-retryable -- retrying an uninstalled App
// four times helps nobody.
func TestSyncTemplateReportsUninstalledApp(t *testing.T) {
	t.Parallel()

	store := &recordingTemplateSyncStore{}
	cloneErr := errors.New("authentication required")
	activities := NewTemplateSyncActivities(
		store,
		WithTemplateSyncGitRunner(&recordingGitRunner{commitSHA: "abc123", err: cloneErr}),
		WithTemplateSyncTempRoot(t.TempDir()),
		WithTemplateSyncTokenSource(stubTokenSource{err: githubapp.ErrAppNotInstalled}),
	)

	output, err := activities.SyncTemplate(context.Background(), domain.TemplateSyncActivityInput{
		TenantID:  domain.TenantID("tenant_123"),
		RepoOwner: "acme",
		RepoName:  "private-infra",
		SourceRef: "main",
		RootPath:  "modules/vpc",
	})
	if err != nil {
		t.Fatalf("SyncTemplate returned error: %v", err)
	}
	if output.Status != domain.TemplateRegistrationInvalid {
		t.Fatalf("status = %q, want %q", output.Status, domain.TemplateRegistrationInvalid)
	}
	if !strings.Contains(output.ErrorSummary, cloneErr.Error()) {
		t.Fatalf("summary = %q, want the underlying clone failure", output.ErrorSummary)
	}
	if !strings.Contains(output.ErrorSummary, "not installed") {
		t.Fatalf("summary = %q, want it to name the missing installation", output.ErrorSummary)
	}
	if !strings.Contains(output.ErrorSummary, "acme/private-infra") {
		t.Fatalf("summary = %q, want it to name the repository", output.ErrorSummary)
	}
}

// This is the regression the finding was about: configuring a GitHub App must
// not break registering a public repository the App was never installed on.
// A 404 from the installation lookup cannot be told apart from that case, so
// SyncTemplate must fall back to an unauthenticated clone and succeed exactly
// as it would with no token source configured at all.
func TestSyncTemplateSucceedsWithZeroCredentialWhenAppNotInstalled(t *testing.T) {
	t.Parallel()

	store := &recordingTemplateSyncStore{}
	gitRunner := &recordingGitRunner{
		commitSHA: "abc123",
		populate: func(repoPath string) error {
			return os.MkdirAll(filepath.Join(repoPath, "modules", "vpc"), 0o700)
		},
	}
	activities := NewTemplateSyncActivities(
		store,
		WithTemplateSyncGitRunner(gitRunner),
		WithTemplateSyncTempRoot(t.TempDir()),
		WithTemplateSyncTokenSource(stubTokenSource{err: githubapp.ErrAppNotInstalled}),
	)

	output, err := activities.SyncTemplate(context.Background(), domain.TemplateSyncActivityInput{
		TenantID:  domain.TenantID("tenant_123"),
		RepoOwner: "hashicorp",
		RepoName:  "terraform-aws-modules",
		SourceRef: "main",
		RootPath:  "modules/vpc",
	})
	if err != nil {
		t.Fatalf("SyncTemplate returned error: %v", err)
	}
	if output.Status != domain.TemplateRegistrationCompleted {
		t.Fatalf("status = %q, want completed; summary = %q", output.Status, output.ErrorSummary)
	}
	if gitRunner.credential != (gitrunner.GitCredential{}) {
		t.Fatal("clone received a non-zero credential for a repo the App is not installed on")
	}
}

// A GitHub API failure that is not a missing installation -- a 503, a rate
// limit, a timeout -- is no more fatal than a missing one. A public repository
// never needed the token the lookup failed to produce, so the clone goes ahead
// unauthenticated and succeeds.
func TestSyncTemplateSucceedsWithZeroCredentialWhenTokenLookupFails(t *testing.T) {
	t.Parallel()

	gitRunner := &recordingGitRunner{
		commitSHA: "abc123",
		populate: func(repoPath string) error {
			return os.MkdirAll(filepath.Join(repoPath, "modules", "vpc"), 0o700)
		},
	}
	activities := NewTemplateSyncActivities(
		&recordingTemplateSyncStore{},
		WithTemplateSyncGitRunner(gitRunner),
		WithTemplateSyncTempRoot(t.TempDir()),
		WithTemplateSyncTokenSource(stubTokenSource{err: errors.New("resolve installation: unexpected status 503 Service Unavailable")}),
	)

	output, err := activities.SyncTemplate(context.Background(), domain.TemplateSyncActivityInput{
		TenantID:  domain.TenantID("tenant_123"),
		RepoOwner: "hashicorp",
		RepoName:  "terraform-aws-modules",
		SourceRef: "main",
		RootPath:  "modules/vpc",
	})
	if err != nil {
		t.Fatalf("SyncTemplate returned error: %v", err)
	}
	if output.Status != domain.TemplateRegistrationCompleted {
		t.Fatalf("status = %q, want completed; summary = %q", output.Status, output.ErrorSummary)
	}
	if gitRunner.credential != (gitrunner.GitCredential{}) {
		t.Fatal("clone received a non-zero credential after the token lookup failed")
	}
}

// A clone that failed while the token lookup was also failing is an
// infrastructure failure, not a bad registration, so it must come back as an
// error rather than a terminal Invalid outcome. Reporting it as an outcome
// tells Temporal the activity succeeded and silently strips the four attempts
// syncRetryPolicy grants -- which is exactly the retry Temporal exists to own.
//
// The message must still name what actually went wrong: blaming a missing
// installation would send an operator to check an installation that is fine.
func TestSyncTemplateRetriesWhenTokenLookupFailedAndCloneFailed(t *testing.T) {
	t.Parallel()

	cloneErr := errors.New("authentication required")
	lookupErr := errors.New("resolve installation: unexpected status 503 Service Unavailable")
	activities := NewTemplateSyncActivities(
		&recordingTemplateSyncStore{},
		WithTemplateSyncGitRunner(&recordingGitRunner{commitSHA: "abc123", err: cloneErr}),
		WithTemplateSyncTempRoot(t.TempDir()),
		WithTemplateSyncTokenSource(stubTokenSource{err: lookupErr}),
	)

	output, err := activities.SyncTemplate(context.Background(), domain.TemplateSyncActivityInput{
		TenantID:  domain.TenantID("tenant_123"),
		RepoOwner: "acme",
		RepoName:  "private-infra",
		SourceRef: "main",
		RootPath:  "modules/vpc",
	})
	if err == nil {
		t.Fatalf("SyncTemplate returned nil error; want a retryable failure (status = %q, summary = %q)", output.Status, output.ErrorSummary)
	}
	if !errors.Is(err, cloneErr) {
		t.Fatalf("error = %v, want it to wrap the underlying clone failure", err)
	}
	if !strings.Contains(err.Error(), "503") {
		t.Fatalf("error = %v, want it to name the token lookup failure", err)
	}
	if strings.Contains(err.Error(), "not installed") {
		t.Fatalf("error = %v, must not blame a missing installation for an API failure", err)
	}
}

// The ordinary failure -- a repository that does not exist, a ref that does not
// -- has no credential trouble behind it and must stay a terminal Invalid.
// Classifying it as retryable would spend four attempts and minutes of backoff
// on a typo, and would report a user's mistake as an infrastructure failure.
func TestSyncTemplateReportsPlainCloneFailureAsInvalid(t *testing.T) {
	t.Parallel()

	cloneErr := errors.New("repository not found")
	activities := NewTemplateSyncActivities(
		&recordingTemplateSyncStore{},
		WithTemplateSyncGitRunner(&recordingGitRunner{commitSHA: "abc123", err: cloneErr}),
		WithTemplateSyncTempRoot(t.TempDir()),
	)

	output, err := activities.SyncTemplate(context.Background(), domain.TemplateSyncActivityInput{
		TenantID:  domain.TenantID("tenant_123"),
		RepoOwner: "acme",
		RepoName:  "no-such-repo",
		SourceRef: "main",
		RootPath:  "modules/vpc",
	})
	if err != nil {
		t.Fatalf("SyncTemplate returned error %v; want a terminal invalid outcome", err)
	}
	if output.Status != domain.TemplateRegistrationInvalid {
		t.Fatalf("status = %q, want %q", output.Status, domain.TemplateRegistrationInvalid)
	}
	if !strings.Contains(output.ErrorSummary, cloneErr.Error()) {
		t.Fatalf("summary = %q, want the underlying clone failure", output.ErrorSummary)
	}
	// No token source was configured, so there is no credential story to tell.
	if strings.Contains(output.ErrorSummary, "could not resolve") || strings.Contains(output.ErrorSummary, "not installed") {
		t.Fatalf("summary = %q, want no credential hint when no token source is configured", output.ErrorSummary)
	}
}

// An owner or repo carrying URL metacharacters must be rejected at the boundary
// rather than folded into a request URL.
func TestSyncTemplateRejectsUnsafeRepoIdentifiers(t *testing.T) {
	t.Parallel()

	activities := NewTemplateSyncActivities(
		&recordingTemplateSyncStore{},
		WithTemplateSyncGitRunner(&recordingGitRunner{commitSHA: "abc123"}),
		WithTemplateSyncTempRoot(t.TempDir()),
	)

	for _, owner := range []string{"acme/evil", "acme?x=1", "acme#frag", "acme corp", "acme\nx"} {
		output, err := activities.SyncTemplate(context.Background(), domain.TemplateSyncActivityInput{
			TenantID:  domain.TenantID("tenant_123"),
			RepoOwner: owner,
			RepoName:  "infra",
			SourceRef: "main",
			RootPath:  ".",
		})
		if err != nil {
			t.Fatalf("SyncTemplate(%q) returned error: %v", owner, err)
		}
		if output.Status != domain.TemplateRegistrationInvalid {
			t.Fatalf("SyncTemplate(%q) status = %q, want invalid", owner, output.Status)
		}
	}
}

type stubTokenSource struct {
	token string
	err   error
}

func (source stubTokenSource) Token(context.Context, string, string) (string, error) {
	return source.token, source.err
}

// The hint's three branches, exercised directly. The wrapped case is the one
// that matters: InstallationForRepo returns ErrAppNotInstalled wrapped with the
// repository name, so matching the sentinel by identity rather than errors.Is
// would silently route every uninstalled App to the transient-failure message.
func TestUnauthenticatedFetchHint(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name   string
		cause  error
		want   string
		absent string
	}{
		{
			name:  "authenticated fetch says nothing",
			cause: nil,
		},
		{
			name:   "bare sentinel asks for an installation",
			cause:  githubapp.ErrAppNotInstalled,
			want:   "not installed",
			absent: "could not resolve",
		},
		{
			// The shape InstallationForRepo actually returns.
			name:   "wrapped sentinel asks for an installation",
			cause:  fmt.Errorf("%w: acme/private-infra", githubapp.ErrAppNotInstalled),
			want:   "not installed",
			absent: "could not resolve",
		},
		{
			name:   "any other failure reads as transient",
			cause:  errors.New("resolve installation: unexpected status 503 Service Unavailable"),
			want:   "likely transient",
			absent: "not installed",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			hint := unauthenticatedFetchHint(testCase.cause, "acme", "private-infra")

			if testCase.want == "" {
				if hint != "" {
					t.Fatalf("hint = %q, want empty", hint)
				}
				return
			}
			if !strings.Contains(hint, testCase.want) {
				t.Fatalf("hint = %q, want it to contain %q", hint, testCase.want)
			}
			if !strings.Contains(hint, "acme/private-infra") {
				t.Fatalf("hint = %q, want it to name the repository", hint)
			}
			if testCase.absent != "" && strings.Contains(hint, testCase.absent) {
				t.Fatalf("hint = %q, must not contain %q", hint, testCase.absent)
			}
		})
	}
}
