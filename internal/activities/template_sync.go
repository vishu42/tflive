package activities

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/vishu42/tflive/internal/domain"
	"github.com/vishu42/tflive/internal/githubapp"
	"github.com/vishu42/tflive/internal/runner"
	"github.com/zclconf/go-cty/cty"
	"gopkg.in/yaml.v3"
)

// GitHubTokenSource resolves a short-lived token granting read access to one
// repository.
//
// The interface lives here, on the consumer, so the activity packages depend on
// the capability rather than on githubapp's concrete client -- and so tests can
// supply a token without an HTTP server.
type GitHubTokenSource interface {
	Token(ctx context.Context, owner string, repo string) (string, error)
}

type TemplateSyncStore interface {
	RecordTemplateRegistrationStatus(context.Context, domain.TemplateRegistrationStatusActivityInput) error
	UpsertTemplateRevisionWithVariables(context.Context, domain.TemplateRevision, []domain.TemplateVariable) (domain.TemplateRevision, error)
}

type TemplateSyncActivities struct {
	store    TemplateSyncStore
	git      runner.GitRunner
	tempRoot string
	tokens   GitHubTokenSource
}

type TemplateSyncOption func(*TemplateSyncActivities)

func WithTemplateSyncGitRunner(git runner.GitRunner) TemplateSyncOption {
	return func(activities *TemplateSyncActivities) {
		if git != nil {
			activities.git = git
		}
	}
}

func WithTemplateSyncTempRoot(tempRoot string) TemplateSyncOption {
	return func(activities *TemplateSyncActivities) {
		activities.tempRoot = tempRoot
	}
}

// WithTemplateSyncTokenSource authenticates source fetches against private
// repositories. Without it, only public repositories can be registered.
func WithTemplateSyncTokenSource(tokens GitHubTokenSource) TemplateSyncOption {
	return func(activities *TemplateSyncActivities) {
		activities.tokens = tokens
	}
}

func NewTemplateSyncActivities(store TemplateSyncStore, options ...TemplateSyncOption) *TemplateSyncActivities {
	activities := &TemplateSyncActivities{
		store: store,
		git:   runner.NewLocalGitRunner(),
	}
	for _, option := range options {
		option(activities)
	}
	return activities
}

func (activities *TemplateSyncActivities) RecordTemplateRegistrationStatus(ctx context.Context, input domain.TemplateRegistrationStatusActivityInput) error {
	if err := activities.store.RecordTemplateRegistrationStatus(ctx, input); err != nil {
		return fmt.Errorf("record template registration status: %w", err)
	}
	return nil
}

func (activities *TemplateSyncActivities) SyncTemplate(ctx context.Context, input domain.TemplateSyncActivityInput) (domain.TemplateSyncActivityOutput, error) {
	rootPath, err := safeTemplateRootPath(input.RootPath)
	if err != nil {
		return invalidTemplateSyncOutput("%v", err), nil
	}

	workspace, err := os.MkdirTemp(activities.tempRoot, "tflive-template-sync-*")
	if err != nil {
		return domain.TemplateSyncActivityOutput{}, fmt.Errorf("create template sync workspace: %w", err)
	}
	defer os.RemoveAll(workspace)

	repoPath := filepath.Join(workspace, "repo")
	repoURL, err := gitHubRepoURL(input.RepoOwner, input.RepoName)
	if err != nil {
		return invalidTemplateSyncOutput("%v", err), nil
	}
	// ErrAppNotInstalled means the installation lookup 404d, and that endpoint
	// 404s for any repo the App has no installation covering -- a private repo
	// nobody granted access to, or a public repo nobody ever needed to install
	// the App on. Those two cases are indistinguishable from here, so treating
	// this as fatal would break every public registration once a GitHub App is
	// configured at all. Instead the fetch proceeds with the zero credential:
	// a public repo clones fine unauthenticated, and only a private one fails,
	// at which point appNotInstalledHint turns that failure into something
	// actionable.
	credential, err := repoCredential(ctx, activities.tokens, input.RepoOwner, input.RepoName)
	appNotInstalled := false
	if err != nil {
		if !errors.Is(err, githubapp.ErrAppNotInstalled) {
			return domain.TemplateSyncActivityOutput{}, fmt.Errorf("resolve github credential: %w", err)
		}
		appNotInstalled = true
	}
	if err := activities.git.Clone(ctx, repoURL, input.SourceRef, repoPath, credential); err != nil {
		return invalidTemplateSyncOutput("clone repository %s/%s at %q: %v%s", input.RepoOwner, input.RepoName, input.SourceRef, err, appNotInstalledHint(appNotInstalled, input.RepoOwner, input.RepoName)), nil
	}

	// resolve SHA of head
	resolvedSHA, err := activities.git.ResolveHead(ctx, repoPath)
	if err != nil {
		return invalidTemplateSyncOutput("resolve repository head: %v", err), nil
	}
	resolvedSHA = strings.TrimSpace(resolvedSHA)
	if resolvedSHA == "" {
		return invalidTemplateSyncOutput("resolve repository head: empty commit sha"), nil
	}

	templateRoot := filepath.Join(repoPath, rootPath)
	if err := ensureTemplateRoot(templateRoot); err != nil {
		return invalidTemplateSyncOutput("root path %q: %v", rootPath, err), nil
	}

	metadata, err := readTemplateMetadata(templateRoot, input.RepoName, rootPath)
	if err != nil {
		return invalidTemplateSyncOutput("read template metadata: %v", err), nil
	}

	sourceTemplateID := deterministicSourceTemplateID(input, rootPath)
	templateRevisionID := deterministicTemplateRevisionID(input, rootPath, resolvedSHA)
	variables, err := inferTemplateVariables(templateRoot, templateRevisionID)
	if err != nil {
		return invalidTemplateSyncOutput("infer template variables: %v", err), nil
	}
	if sensitive := sensitiveVariableNames(variables); len(sensitive) > 0 {
		return invalidTemplateSyncOutput("sensitive variables are not supported: %s", strings.Join(sensitive, ", ")), nil
	}

	templateRevision := domain.TemplateRevision{
		ID:                templateRevisionID,
		TenantID:          input.TenantID,
		SourceTemplateID:  sourceTemplateID,
		RepoOwner:         input.RepoOwner,
		RepoName:          input.RepoName,
		SourceRef:         input.SourceRef,
		ResolvedCommitSHA: resolvedSHA,
		RootPath:          rootPath,
		Name:              metadata.Name,
		Description:       metadata.Description,
		Tags:              metadata.Tags,
		Status:            domain.TemplateRevisionActive,
		CreatedAt:         time.Now().UTC(),
	}
	persisted, err := activities.store.UpsertTemplateRevisionWithVariables(ctx, templateRevision, variables)
	if err != nil {
		return domain.TemplateSyncActivityOutput{}, fmt.Errorf("persist synced template revision: %w", err)
	}

	return domain.TemplateSyncActivityOutput{
		Status:             domain.TemplateRegistrationCompleted,
		TemplateRevisionID: persisted.ID,
		ResolvedCommitSHA:  persisted.ResolvedCommitSHA,
	}, nil
}

// gitHubRepoURL builds the clone URL for owner/repo.
//
// The identifiers are validated rather than trusted: they arrive from an API
// request, and a value carrying a slash, a query, a fragment, or whitespace
// would produce a URL that is not the repository the caller named. The
// authority is fixed before the first path separator, so no value can redirect
// the request to another host -- but a malformed one still deserves a clear
// rejection at the boundary instead of an obscure git failure.
func gitHubRepoURL(owner string, repo string) (string, error) {
	if err := validateRepoIdentifier("repository owner", owner); err != nil {
		return "", err
	}
	if err := validateRepoIdentifier("repository name", repo); err != nil {
		return "", err
	}
	return fmt.Sprintf("https://github.com/%s/%s.git", owner, repo), nil
}

func validateRepoIdentifier(field string, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required", field)
	}
	if value != strings.TrimSpace(value) {
		return fmt.Errorf("%s %q must not have leading or trailing whitespace", field, value)
	}
	for _, character := range value {
		if character <= ' ' || character == '/' || character == '?' || character == '#' ||
			character == '@' || character == ':' || character == '\\' || character == 0x7f {
			return fmt.Errorf("%s %q contains an unsupported character", field, value)
		}
	}
	return nil
}

// repoCredential resolves the credential for one repository.
//
// It runs inside the activity, never in workflow code, so the token stays out
// of Temporal history -- the same boundary CredentialDecryptor already
// establishes for Terraform credentials. With no token source configured it
// returns the zero credential and the clone proceeds unauthenticated.
func repoCredential(ctx context.Context, tokens GitHubTokenSource, owner string, repo string) (runner.GitCredential, error) {
	if tokens == nil {
		return runner.GitCredential{}, nil
	}
	token, err := tokens.Token(ctx, owner, repo)
	if err != nil {
		return runner.GitCredential{}, err
	}
	return runner.NewGitCredential(token), nil
}

// appNotInstalledHint appends actionable guidance to a fetch failure that
// happened while unauthenticated because the GitHub App has no installation
// covering owner/repo. The hint rides along with the failure that actually
// occurred, rather than being raised on its own, because a missing
// installation alone is not an error -- see the doc comment at repoCredential's
// call sites. It only becomes worth mentioning once a fetch has already failed
// for some other reason, at which point it is the one thing an operator can
// act on.
func appNotInstalledHint(appNotInstalled bool, owner string, repo string) string {
	if !appNotInstalled {
		return ""
	}
	return fmt.Sprintf("; the tflive GitHub App is not installed on %s/%s, so this fetch was unauthenticated -- if the repository is private, ask an organization admin to install the App", owner, repo)
}

func invalidTemplateSyncOutput(format string, args ...any) domain.TemplateSyncActivityOutput {
	return domain.TemplateSyncActivityOutput{
		Status:       domain.TemplateRegistrationInvalid,
		ErrorSummary: fmt.Sprintf(format, args...),
	}
}

func safeTemplateRootPath(rootPath string) (string, error) {
	rootPath = strings.TrimSpace(rootPath)
	if rootPath == "" {
		return "", errors.New("root path is required")
	}
	rootPath = filepath.Clean(rootPath)
	if filepath.IsAbs(rootPath) || rootPath == ".." || strings.HasPrefix(rootPath, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("root path %q must stay within the repository", rootPath)
	}
	return rootPath, nil
}

func ensureTemplateRoot(templateRoot string) error {
	info, err := os.Stat(templateRoot)
	if errors.Is(err, os.ErrNotExist) {
		return errors.New("directory does not exist")
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("is not a directory")
	}
	return nil
}

type parsedTemplateMetadata struct {
	Name        string
	Description string
	Tags        []string
}

func readTemplateMetadata(root string, repoName string, rootPath string) (parsedTemplateMetadata, error) {
	metadata := parsedTemplateMetadata{Name: fallbackTemplateName(repoName, rootPath)}
	path := filepath.Join(root, "template.yaml")
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return metadata, nil
	}
	if err != nil {
		return parsedTemplateMetadata{}, err
	}

	var fileMetadata struct {
		Name        string   `yaml:"name"`
		Description string   `yaml:"description"`
		Tags        []string `yaml:"tags"`
	}
	if err := yaml.Unmarshal(body, &fileMetadata); err != nil {
		return parsedTemplateMetadata{}, err
	}
	if name := strings.TrimSpace(fileMetadata.Name); name != "" {
		metadata.Name = name
	}
	metadata.Description = strings.TrimSpace(fileMetadata.Description)
	metadata.Tags = cleanTags(fileMetadata.Tags)
	return metadata, nil
}

func fallbackTemplateName(repoName string, rootPath string) string {
	base := filepath.Base(filepath.Clean(rootPath))
	if base == "." || base == string(filepath.Separator) || base == "" {
		return repoName
	}
	return base
}

func cleanTags(tags []string) []string {
	cleaned := make([]string, 0, len(tags))
	seen := make(map[string]struct{}, len(tags))
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		if _, ok := seen[tag]; ok {
			continue
		}
		seen[tag] = struct{}{}
		cleaned = append(cleaned, tag)
	}
	return cleaned
}

func inferTemplateVariables(root string, templateRevisionID domain.TemplateRevisionID) ([]domain.TemplateVariable, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}

	parser := hclparse.NewParser()
	variablesByName := map[string]domain.TemplateVariable{}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".tf" {
			continue
		}
		path := filepath.Join(root, entry.Name())
		source, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		file, diags := parser.ParseHCL(source, path)
		if diags.HasErrors() {
			return nil, fmt.Errorf("%s: %s", entry.Name(), diags.Error())
		}

		content, _, diags := file.Body.PartialContent(variableFileSchema())
		if diags.HasErrors() {
			return nil, fmt.Errorf("%s: %s", entry.Name(), diags.Error())
		}
		for _, block := range content.Blocks {
			variable, err := parseVariableBlock(block, source, templateRevisionID)
			if err != nil {
				return nil, fmt.Errorf("%s variable %q: %w", entry.Name(), block.Labels[0], err)
			}
			if _, exists := variablesByName[variable.Name]; exists {
				return nil, fmt.Errorf("duplicate variable %q", variable.Name)
			}
			variablesByName[variable.Name] = variable
		}
	}

	variables := make([]domain.TemplateVariable, 0, len(variablesByName))
	for _, variable := range variablesByName {
		variables = append(variables, variable)
	}
	sort.Slice(variables, func(i int, j int) bool {
		return variables[i].Name < variables[j].Name
	})
	return variables, nil
}

func variableFileSchema() *hcl.BodySchema {
	return &hcl.BodySchema{
		Blocks: []hcl.BlockHeaderSchema{
			{Type: "variable", LabelNames: []string{"name"}},
		},
	}
}

func variableBlockSchema() *hcl.BodySchema {
	return &hcl.BodySchema{
		Attributes: []hcl.AttributeSchema{
			{Name: "type"},
			{Name: "description"},
			{Name: "default"},
			{Name: "sensitive"},
		},
		Blocks: []hcl.BlockHeaderSchema{
			{Type: "validation"},
		},
	}
}

func parseVariableBlock(block *hcl.Block, source []byte, templateRevisionID domain.TemplateRevisionID) (domain.TemplateVariable, error) {
	name := block.Labels[0]
	content, _, diags := block.Body.PartialContent(variableBlockSchema())
	if diags.HasErrors() {
		return domain.TemplateVariable{}, errors.New(diags.Error())
	}

	variable := domain.TemplateVariable{
		TemplateRevisionID: templateRevisionID,
		Name:               name,
		Required:           true,
	}
	if attr, ok := content.Attributes["type"]; ok {
		variable.TypeExpression = strings.TrimSpace(string(attr.Expr.Range().SliceBytes(source)))
	}
	if attr, ok := content.Attributes["description"]; ok {
		description, err := hclStringValue(attr)
		if err != nil {
			return domain.TemplateVariable{}, fmt.Errorf("description: %w", err)
		}
		variable.Description = description
	}
	if _, ok := content.Attributes["default"]; ok {
		variable.HasDefault = true
		variable.Required = false
	}
	if attr, ok := content.Attributes["sensitive"]; ok {
		sensitive, err := hclBoolValue(attr)
		if err != nil {
			return domain.TemplateVariable{}, fmt.Errorf("sensitive: %w", err)
		}
		variable.Sensitive = sensitive
	}
	for _, nested := range content.Blocks {
		if nested.Type == "validation" {
			variable.HasValidation = true
			break
		}
	}
	return variable, nil
}

func hclStringValue(attr *hcl.Attribute) (string, error) {
	value, diags := attr.Expr.Value(nil)
	if diags.HasErrors() {
		return "", errors.New(diags.Error())
	}
	if value.Type() != cty.String {
		return "", fmt.Errorf("must be a string, got %s", value.Type().FriendlyName())
	}
	return value.AsString(), nil
}

func hclBoolValue(attr *hcl.Attribute) (bool, error) {
	value, diags := attr.Expr.Value(nil)
	if diags.HasErrors() {
		return false, errors.New(diags.Error())
	}
	if value.Type() != cty.Bool {
		return false, fmt.Errorf("must be a bool, got %s", value.Type().FriendlyName())
	}
	return value.True(), nil
}

func sensitiveVariableNames(variables []domain.TemplateVariable) []string {
	var names []string
	for _, variable := range variables {
		if variable.Sensitive {
			names = append(names, variable.Name)
		}
	}
	sort.Strings(names)
	return names
}

// template_4b93175b6dcb1641073fcd42475912a3
func deterministicTemplateRevisionID(input domain.TemplateSyncActivityInput, rootPath string, resolvedSHA string) domain.TemplateRevisionID {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		string(input.TenantID),
		input.RepoOwner,
		input.RepoName,
		rootPath,
		resolvedSHA,
	}, "\x00")))
	return domain.TemplateRevisionID("template_" + hex.EncodeToString(sum[:16]))
}

// deterministicSourceTemplateID derives a stable SourceTemplateID from the tenant, repo,
// root path, and source ref, so re-syncing the same source always resolves to the same ID
// instead of minting a duplicate row.
// e.g. tenant_123/acme/templates/templates/web/main -> source_template_106bb033369f88d7820a14c2ec059296
// source_template_327520eeba5223257d8fb1d2d2a39f6d
func deterministicSourceTemplateID(input domain.TemplateSyncActivityInput, rootPath string) domain.SourceTemplateID {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		string(input.TenantID),
		input.RepoOwner,
		input.RepoName,
		rootPath,
		input.SourceRef,
	}, "\x00")))
	return domain.SourceTemplateID("source_template_" + hex.EncodeToString(sum[:16]))
}
