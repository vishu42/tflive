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
	"github.com/vishu42/tflive/internal/runner"
	"github.com/vishu42/tflive/internal/traits"
	"github.com/zclconf/go-cty/cty"
	"gopkg.in/yaml.v3"
)

type TemplateSyncStore interface {
	RecordTemplateRegistrationStatus(context.Context, traits.TemplateRegistrationStatusActivityInput) error
	UpsertTemplateRevisionWithVariables(context.Context, traits.TemplateRevision, []traits.TemplateVariable) (traits.TemplateRevision, error)
}

type TemplateSyncActivities struct {
	store    TemplateSyncStore
	git      runner.GitRunner
	tempRoot string
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

func (activities *TemplateSyncActivities) RecordTemplateRegistrationStatus(ctx context.Context, input traits.TemplateRegistrationStatusActivityInput) error {
	if err := activities.store.RecordTemplateRegistrationStatus(ctx, input); err != nil {
		return fmt.Errorf("record template registration status: %w", err)
	}
	return nil
}

func (activities *TemplateSyncActivities) SyncTemplate(ctx context.Context, input traits.TemplateSyncActivityInput) (traits.TemplateSyncActivityOutput, error) {
	rootPath, err := safeTemplateRootPath(input.RootPath)
	if err != nil {
		return invalidTemplateSyncOutput("%v", err), nil
	}

	workspace, err := os.MkdirTemp(activities.tempRoot, "tflive-template-sync-*")
	if err != nil {
		return traits.TemplateSyncActivityOutput{}, fmt.Errorf("create template sync workspace: %w", err)
	}
	defer os.RemoveAll(workspace)

	repoPath := filepath.Join(workspace, "repo")
	repoURL := publicGitHubRepoURL(input.RepoOwner, input.RepoName)
	if err := activities.git.Clone(ctx, repoURL, input.SourceRef, repoPath); err != nil {
		return invalidTemplateSyncOutput("clone public repository: %v", err), nil
	}

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

	templateRevision := traits.TemplateRevision{
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
		Status:            traits.TemplateRevisionActive,
		CreatedAt:         time.Now().UTC(),
	}
	persisted, err := activities.store.UpsertTemplateRevisionWithVariables(ctx, templateRevision, variables)
	if err != nil {
		return traits.TemplateSyncActivityOutput{}, fmt.Errorf("persist synced template revision: %w", err)
	}

	return traits.TemplateSyncActivityOutput{
		Status:             traits.TemplateRegistrationCompleted,
		TemplateRevisionID: persisted.ID,
		ResolvedCommitSHA:  persisted.ResolvedCommitSHA,
	}, nil
}

func publicGitHubRepoURL(owner string, repo string) string {
	return fmt.Sprintf("https://github.com/%s/%s.git", owner, repo)
}

func invalidTemplateSyncOutput(format string, args ...any) traits.TemplateSyncActivityOutput {
	return traits.TemplateSyncActivityOutput{
		Status:       traits.TemplateRegistrationInvalid,
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

func inferTemplateVariables(root string, templateRevisionID traits.TemplateRevisionID) ([]traits.TemplateVariable, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}

	parser := hclparse.NewParser()
	variablesByName := map[string]traits.TemplateVariable{}
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

	variables := make([]traits.TemplateVariable, 0, len(variablesByName))
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

func parseVariableBlock(block *hcl.Block, source []byte, templateRevisionID traits.TemplateRevisionID) (traits.TemplateVariable, error) {
	name := block.Labels[0]
	content, _, diags := block.Body.PartialContent(variableBlockSchema())
	if diags.HasErrors() {
		return traits.TemplateVariable{}, errors.New(diags.Error())
	}

	variable := traits.TemplateVariable{
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
			return traits.TemplateVariable{}, fmt.Errorf("description: %w", err)
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
			return traits.TemplateVariable{}, fmt.Errorf("sensitive: %w", err)
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

func sensitiveVariableNames(variables []traits.TemplateVariable) []string {
	var names []string
	for _, variable := range variables {
		if variable.Sensitive {
			names = append(names, variable.Name)
		}
	}
	sort.Strings(names)
	return names
}

func deterministicTemplateRevisionID(input traits.TemplateSyncActivityInput, rootPath string, resolvedSHA string) traits.TemplateRevisionID {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		string(input.TenantID),
		input.RepoOwner,
		input.RepoName,
		rootPath,
		resolvedSHA,
	}, "\x00")))
	return traits.TemplateRevisionID("template_" + hex.EncodeToString(sum[:16]))
}

func deterministicSourceTemplateID(input traits.TemplateSyncActivityInput, rootPath string) traits.SourceTemplateID {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		string(input.TenantID),
		input.RepoOwner,
		input.RepoName,
		rootPath,
		input.SourceRef,
	}, "\x00")))
	return traits.SourceTemplateID("source_template_" + hex.EncodeToString(sum[:16]))
}
