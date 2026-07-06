package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vishu42/megagega/internal/traits"
)

var (
	ErrInvalidCommand             = errors.New("invalid command")
	ErrDuplicateStackSlug         = errors.New("duplicate stack slug")
	ErrNotFound                   = errors.New("not found")
	ErrTemplateNotInstallable     = errors.New("template is not installable")
	ErrStackTemplateConfigInvalid = errors.New("stack template config is invalid")
	ErrStackTemplateNotRunnable   = errors.New("stack template is not runnable")
	ErrRunNotApprovable           = errors.New("run is not approvable")
	ErrRunNotCancelable           = errors.New("run is not cancelable")
)

// StackRepository persists and reads tenant-owned stacks.
type StackRepository interface {
	CreateStack(ctx context.Context, stack traits.Stack) error
	GetStack(ctx context.Context, tenantID traits.TenantID, stackID traits.StackID) (traits.Stack, error)
	GetStackWithTemplates(ctx context.Context, tenantID traits.TenantID, stackID traits.StackID) (StackView, error)
	ListStacks(ctx context.Context, tenantID traits.TenantID) ([]traits.Stack, error)
}

// StackTemplateRepository reads installed template state for use cases.
type StackTemplateRepository interface {
	GetStackTemplate(ctx context.Context, tenantID traits.TenantID, id traits.StackTemplateID) (traits.StackTemplate, error)
}

// TemplateRunRepository persists TemplateRun records and run decisions.
type TemplateRunRepository interface {
	CreateTemplateRun(ctx context.Context, run traits.TemplateRun) error
	GetTemplateRun(ctx context.Context, tenantID traits.TenantID, runID traits.TemplateRunID) (traits.TemplateRun, error)
	// ApproveTemplateRun records approval only when the tenant-owned run is waiting for approval.
	ApproveTemplateRun(ctx context.Context, approval traits.TemplateRunApproval) error
	// RequestTemplateRunCancellation records cancellation only when the tenant-owned run can still stop.
	RequestTemplateRunCancellation(ctx context.Context, cancellation traits.TemplateRunCancellation) error
}

// TemplateRegistrationRepository persists template registration attempts.
type TemplateRegistrationRepository interface {
	CreateTemplateRegistration(ctx context.Context, registration traits.TemplateRegistration) error
	GetTemplateRegistration(ctx context.Context, tenantID traits.TenantID, id traits.TemplateRegistrationID) (traits.TemplateRegistration, error)
	RecordTemplateRegistrationStatus(ctx context.Context, input traits.TemplateRegistrationStatusActivityInput) error
}

// TemplateRepository persists immutable template metadata and inferred variables.
type TemplateRepository interface {
	UpsertTemplateWithVariables(ctx context.Context, template traits.Template, variables []traits.TemplateVariable) (traits.Template, error)
	ListTemplates(ctx context.Context, tenantID traits.TenantID) ([]traits.Template, error)
	GetTemplateVariables(ctx context.Context, tenantID traits.TenantID, templateID traits.TemplateID) ([]traits.TemplateVariable, error)
}

// TemplateMetadataRepository reads immutable template metadata.
type TemplateMetadataRepository interface {
	GetTemplate(ctx context.Context, tenantID traits.TenantID, templateID traits.TemplateID) (traits.Template, error)
}

// TemplateRunLogReader reads persisted log bodies for tenant-owned TemplateRuns.
type TemplateRunLogReader interface {
	ReadTemplateRunLog(ctx context.Context, log traits.TemplateRunLog) ([]byte, error)
}

// TemplateRunLogRepository reads persisted log metadata for tenant-owned TemplateRuns.
type TemplateRunLogRepository interface {
	GetTemplateRunLog(ctx context.Context, tenantID traits.TenantID, runID traits.TemplateRunID, phase string) (traits.TemplateRunLog, error)
	ListTemplateRunLogs(ctx context.Context, tenantID traits.TenantID, runID traits.TemplateRunID) ([]traits.TemplateRunLog, error)
}

// WorkflowDispatcher starts workflows and sends workflow signals.
type WorkflowDispatcher interface {
	StartTemplateRun(ctx context.Context, input traits.TemplateRunWorkflowInput) error
	StartTemplateSync(ctx context.Context, input traits.TemplateSyncWorkflowInput) error
	ApproveTemplateRun(ctx context.Context, tenantID traits.TenantID, runID traits.TemplateRunID, signal traits.ApprovalSignal) error
	CancelTemplateRun(ctx context.Context, tenantID traits.TenantID, runID traits.TemplateRunID, signal traits.CancelSignal) error
}

// TemplateRunIDGenerator creates TemplateRun identifiers.
type TemplateRunIDGenerator interface {
	NewTemplateRunID() traits.TemplateRunID
}

// TemplateRegistrationIDGenerator creates TemplateRegistration identifiers.
type TemplateRegistrationIDGenerator interface {
	NewTemplateRegistrationID() traits.TemplateRegistrationID
}

// StackIDGenerator creates Stack identifiers.
type StackIDGenerator interface {
	NewStackID() traits.StackID
}

// StackTemplateInstaller persists installed stack templates.
type StackTemplateInstaller interface {
	CreateStackTemplate(ctx context.Context, stackTemplate traits.StackTemplate) error
}

// StackTemplateIDGenerator creates StackTemplate identifiers.
type StackTemplateIDGenerator interface {
	NewStackTemplateID() traits.StackTemplateID
}

// Clock provides current time for app use cases.
type Clock interface {
	Now() time.Time
}

// Service owns app use cases and the dependencies wired by cmd packages.
type Service struct {
	Stacks                 StackRepository
	StackTemplates         StackTemplateRepository
	TemplateRuns           TemplateRunRepository
	TemplateRegistrations  TemplateRegistrationRepository
	TemplateMetadata       TemplateMetadataRepository
	Templates              TemplateRepository
	StackTemplateInstaller StackTemplateInstaller
	TemplateRunLogs        TemplateRunLogReader
	TemplateRunLogMetadata TemplateRunLogRepository
	Workflows              WorkflowDispatcher
	StackIDs               StackIDGenerator
	StackTemplateIDs       StackTemplateIDGenerator
	RunIDs                 TemplateRunIDGenerator
	RegistrationIDs        TemplateRegistrationIDGenerator
	Clock                  Clock
}

// NewService creates a Service from app-owned dependencies.
func NewService(service Service) *Service {
	// TODO: Validate required dependencies here once cmd wires real adapters so startup
	// fails cleanly instead of panicking on first use.
	clock := service.Clock
	if clock == nil {
		clock = systemClock{}
	}
	service.Clock = clock
	if service.RunIDs == nil {
		service.RunIDs = randomTemplateRunIDGenerator{}
	}
	if service.RegistrationIDs == nil {
		service.RegistrationIDs = randomTemplateRegistrationIDGenerator{}
	}
	if service.StackIDs == nil {
		service.StackIDs = randomStackIDGenerator{}
	}
	if service.StackTemplateIDs == nil {
		service.StackTemplateIDs = randomStackTemplateIDGenerator{}
	}

	return &service
}

// RegisterTemplateCommand asks the app to register a Terraform template source.
type RegisterTemplateCommand struct {
	TenantID    traits.TenantID
	RepoOwner   string
	RepoName    string
	SourceRef   string
	RootPath    string
	RequestedBy traits.UserID
}

// CreateStackCommand asks the app to create a logical infrastructure stack.
type CreateStackCommand struct {
	TenantID             traits.TenantID
	Name                 string
	Slug                 string
	Tags                 map[string]string
	DefaultCredentialIDs []traits.CredentialSetID
	Actor                traits.UserID
}

// StackView returns one stack with its installed templates.
type StackView struct {
	Stack     traits.Stack
	Templates []traits.StackTemplate
}

// AddTemplateToStackCommand asks the app to install one template into a stack.
type AddTemplateToStackCommand struct {
	TenantID      traits.TenantID
	StackID       traits.StackID
	TemplateID    traits.TemplateID
	SelectedRef   string
	WorkspaceName string
	ConfigJSON    json.RawMessage
	Actor         traits.UserID
}

// StartTemplateRunCommand asks the app to start one Terraform operation.
type StartTemplateRunCommand struct {
	TenantID        traits.TenantID
	StackTemplateID traits.StackTemplateID
	Operation       traits.OperationType
	TriggerActor    traits.UserID
}

// ApproveRunCommand asks the app to approve a waiting run.
type ApproveRunCommand struct {
	TenantID   traits.TenantID
	RunID      traits.TemplateRunID
	ApprovedBy traits.UserID
}

// CancelRunCommand asks the app to cancel a running run.
type CancelRunCommand struct {
	TenantID    traits.TenantID
	RunID       traits.TemplateRunID
	RequestedBy traits.UserID
	Reason      string
}

// GetStackCommand asks the app to read one stack and its installed templates.
type GetStackCommand struct {
	TenantID traits.TenantID
	StackID  traits.StackID
}

// ListStacksCommand asks the app to list tenant-owned stacks.
type ListStacksCommand struct {
	TenantID traits.TenantID
}

// ListTemplatesCommand asks the app to list tenant-owned registered templates.
type ListTemplatesCommand struct {
	TenantID traits.TenantID
}

// GetTemplateRunCommand asks the app to read one tenant-owned run.
type GetTemplateRunCommand struct {
	TenantID traits.TenantID
	RunID    traits.TemplateRunID
}

// GetTemplateRunLogCommand asks the app to read one tenant-owned run phase log.
type GetTemplateRunLogCommand struct {
	TenantID traits.TenantID
	RunID    traits.TemplateRunID
	Phase    string
}

// ListTemplateRunLogsCommand asks the app to list tenant-owned run log metadata.
type ListTemplateRunLogsCommand struct {
	TenantID traits.TenantID
	RunID    traits.TemplateRunID
}

// GetTemplateRegistrationCommand asks the app to read one tenant-owned registration attempt.
type GetTemplateRegistrationCommand struct {
	TenantID       traits.TenantID
	RegistrationID traits.TemplateRegistrationID
}

// GetTemplateVariablesCommand asks the app to read inferred variables for one tenant-owned template.
type GetTemplateVariablesCommand struct {
	TenantID   traits.TenantID
	TemplateID traits.TemplateID
}

// RegisterTemplate creates a pending registration attempt and dispatches its sync workflow.
func (service *Service) RegisterTemplate(ctx context.Context, command RegisterTemplateCommand) (traits.TemplateRegistration, error) {
	if err := validateRegisterTemplateCommand(command); err != nil {
		return traits.TemplateRegistration{}, err
	}

	registration := traits.TemplateRegistration{
		ID:          service.RegistrationIDs.NewTemplateRegistrationID(),
		TenantID:    command.TenantID,
		RepoOwner:   strings.TrimSpace(command.RepoOwner),
		RepoName:    strings.TrimSpace(command.RepoName),
		SourceRef:   strings.TrimSpace(command.SourceRef),
		RootPath:    filepath.Clean(strings.TrimSpace(command.RootPath)),
		Status:      traits.TemplateRegistrationPending,
		RequestedBy: command.RequestedBy,
		RequestedAt: service.Clock.Now(),
	}

	if err := service.TemplateRegistrations.CreateTemplateRegistration(ctx, registration); err != nil {
		return traits.TemplateRegistration{}, fmt.Errorf("create template registration: %w", err)
	}

	input := traits.TemplateSyncWorkflowInput{
		RegistrationID: registration.ID,
		TenantID:       registration.TenantID,
		RepoOwner:      registration.RepoOwner,
		RepoName:       registration.RepoName,
		SourceRef:      registration.SourceRef,
		RootPath:       registration.RootPath,
	}
	if err := service.Workflows.StartTemplateSync(ctx, input); err != nil {
		return traits.TemplateRegistration{}, fmt.Errorf("start template sync workflow: %w", err)
	}

	return registration, nil
}

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

// AddTemplateToStack validates one template install and persists the tenant-owned stack template.
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

// StartTemplateRun creates a queued run and dispatches its workflow.
func (service *Service) StartTemplateRun(ctx context.Context, command StartTemplateRunCommand) (traits.TemplateRun, error) {
	if err := validateStartTemplateRunCommand(command); err != nil {
		return traits.TemplateRun{}, err
	}

	stackTemplate, err := service.StackTemplates.GetStackTemplate(ctx, command.TenantID, command.StackTemplateID)
	if err != nil {
		return traits.TemplateRun{}, fmt.Errorf("get stack template: %w", err)
	}
	if stackTemplate.Lifecycle != traits.StackTemplateActive {
		return traits.TemplateRun{}, fmt.Errorf("%w: lifecycle is %q", ErrStackTemplateNotRunnable, stackTemplate.Lifecycle)
	}
	// TODO: Reject active StackTemplate records with missing SelectedRef or WorkspaceName,
	// or enforce those invariants with Postgres CHECK constraints.

	run := traits.TemplateRun{
		ID:              service.RunIDs.NewTemplateRunID(),
		TenantID:        command.TenantID,
		StackTemplateID: command.StackTemplateID,
		Operation:       command.Operation,
		SelectedRef:     stackTemplate.SelectedRef,
		WorkspaceName:   stackTemplate.WorkspaceName,
		Status:          traits.TemplateRunQueued,
		TriggerActor:    command.TriggerActor,
		StartedAt:       service.Clock.Now(),
	}

	if err := service.TemplateRuns.CreateTemplateRun(ctx, run); err != nil {
		return traits.TemplateRun{}, fmt.Errorf("create template run: %w", err)
	}

	// TODO: Decide whether StartTemplateRun should write an outbox record with the run
	// so Temporal dispatch can be retried if this client call fails after persistence.
	input := traits.TemplateRunWorkflowInput{
		RunID:           run.ID,
		TenantID:        run.TenantID,
		StackTemplateID: run.StackTemplateID,
		Operation:       run.Operation,
		SelectedRef:     run.SelectedRef,
		WorkspaceName:   run.WorkspaceName,
	}
	if err := service.Workflows.StartTemplateRun(ctx, input); err != nil {
		return traits.TemplateRun{}, fmt.Errorf("start template run workflow: %w", err)
	}

	return run, nil
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
	if view.Templates == nil {
		view.Templates = []traits.StackTemplate{}
	}
	return view, nil
}

// ListStacks returns tenant-owned stacks ordered for UI selection.
func (service *Service) ListStacks(ctx context.Context, command ListStacksCommand) ([]traits.Stack, error) {
	if err := validateListStacksCommand(command); err != nil {
		return nil, err
	}

	stacks, err := service.Stacks.ListStacks(ctx, command.TenantID)
	if err != nil {
		return nil, fmt.Errorf("list stacks: %w", err)
	}
	if stacks == nil {
		return []traits.Stack{}, nil
	}
	return stacks, nil
}

// ListTemplates returns tenant-owned registered templates ordered for UI selection.
func (service *Service) ListTemplates(ctx context.Context, command ListTemplatesCommand) ([]traits.Template, error) {
	if err := validateListTemplatesCommand(command); err != nil {
		return nil, err
	}

	templates, err := service.Templates.ListTemplates(ctx, command.TenantID)
	if err != nil {
		return nil, fmt.Errorf("list templates: %w", err)
	}
	if templates == nil {
		return []traits.Template{}, nil
	}
	return templates, nil
}

// GetTemplateRegistration returns one tenant-owned registration attempt.
func (service *Service) GetTemplateRegistration(ctx context.Context, command GetTemplateRegistrationCommand) (traits.TemplateRegistration, error) {
	if err := validateGetTemplateRegistrationCommand(command); err != nil {
		return traits.TemplateRegistration{}, err
	}

	registration, err := service.TemplateRegistrations.GetTemplateRegistration(ctx, command.TenantID, command.RegistrationID)
	if err != nil {
		return traits.TemplateRegistration{}, fmt.Errorf("get template registration: %w", err)
	}

	return registration, nil
}

// GetTemplateVariables returns inferred variables for one tenant-owned template.
func (service *Service) GetTemplateVariables(ctx context.Context, command GetTemplateVariablesCommand) ([]traits.TemplateVariable, error) {
	if err := validateGetTemplateVariablesCommand(command); err != nil {
		return nil, err
	}

	variables, err := service.Templates.GetTemplateVariables(ctx, command.TenantID, command.TemplateID)
	if err != nil {
		return nil, fmt.Errorf("get template variables: %w", err)
	}
	if variables == nil {
		return []traits.TemplateVariable{}, nil
	}

	return variables, nil
}

// ApproveRun records an approval decision and signals the waiting workflow.
func (service *Service) ApproveRun(ctx context.Context, command ApproveRunCommand) error {
	if err := validateApproveRunCommand(command); err != nil {
		return err
	}

	approval := traits.TemplateRunApproval{
		RunID:      command.RunID,
		TenantID:   command.TenantID,
		ApprovedBy: command.ApprovedBy,
		ApprovedAt: service.Clock.Now(),
	}

	if err := service.TemplateRuns.ApproveTemplateRun(ctx, approval); err != nil {
		return fmt.Errorf("approve template run: %w", err)
	}

	// TODO: Consider an outbox-backed approval signal so a persisted approval cannot
	// be lost if the Temporal signal call fails.
	signal := traits.ApprovalSignal{ApprovedBy: command.ApprovedBy}
	if err := service.Workflows.ApproveTemplateRun(ctx, command.TenantID, command.RunID, signal); err != nil {
		return fmt.Errorf("approve template run workflow: %w", err)
	}

	return nil
}

// CancelRun records a cancellation request and signals the running workflow.
func (service *Service) CancelRun(ctx context.Context, command CancelRunCommand) error {
	if err := validateCancelRunCommand(command); err != nil {
		return err
	}

	cancellation := traits.TemplateRunCancellation{
		RunID:       command.RunID,
		TenantID:    command.TenantID,
		RequestedBy: command.RequestedBy,
		Reason:      command.Reason,
		RequestedAt: service.Clock.Now(),
	}

	if err := service.TemplateRuns.RequestTemplateRunCancellation(ctx, cancellation); err != nil {
		return fmt.Errorf("request template run cancellation: %w", err)
	}

	// TODO: Consider an outbox-backed cancellation signal so a persisted cancel request
	// cannot be lost if the Temporal signal call fails.
	signal := traits.CancelSignal{
		RequestedBy: command.RequestedBy,
		Reason:      command.Reason,
	}
	if err := service.Workflows.CancelTemplateRun(ctx, command.TenantID, command.RunID, signal); err != nil {
		return fmt.Errorf("cancel template run workflow: %w", err)
	}

	return nil
}

// GetTemplateRun returns one tenant-owned run.
func (service *Service) GetTemplateRun(ctx context.Context, command GetTemplateRunCommand) (traits.TemplateRun, error) {
	if err := validateGetTemplateRunCommand(command); err != nil {
		return traits.TemplateRun{}, err
	}

	run, err := service.TemplateRuns.GetTemplateRun(ctx, command.TenantID, command.RunID)
	if err != nil {
		return traits.TemplateRun{}, fmt.Errorf("get template run: %w", err)
	}

	return run, nil
}

// GetTemplateRunLog returns one phase log after checking that the run belongs to the tenant.
func (service *Service) GetTemplateRunLog(ctx context.Context, command GetTemplateRunLogCommand) ([]byte, error) {
	if err := validateGetTemplateRunLogCommand(command); err != nil {
		return nil, err
	}

	if _, err := service.GetTemplateRun(ctx, GetTemplateRunCommand{
		TenantID: command.TenantID,
		RunID:    command.RunID,
	}); err != nil {
		return nil, err
	}
	log, err := service.TemplateRunLogMetadata.GetTemplateRunLog(ctx, command.TenantID, command.RunID, command.Phase)
	if err != nil {
		return nil, fmt.Errorf("get template run log metadata: %w", err)
	}

	content, err := service.TemplateRunLogs.ReadTemplateRunLog(ctx, log)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("read template run log: %w", ErrNotFound)
		}
		return nil, fmt.Errorf("read template run log: %w", err)
	}

	return content, nil
}

// ListTemplateRunLogs returns persisted log metadata after checking that the run belongs to the tenant.
func (service *Service) ListTemplateRunLogs(ctx context.Context, command ListTemplateRunLogsCommand) ([]traits.TemplateRunLog, error) {
	if err := validateGetTemplateRunCommand(GetTemplateRunCommand{
		TenantID: command.TenantID,
		RunID:    command.RunID,
	}); err != nil {
		return nil, err
	}

	if _, err := service.GetTemplateRun(ctx, GetTemplateRunCommand{
		TenantID: command.TenantID,
		RunID:    command.RunID,
	}); err != nil {
		return nil, err
	}

	logs, err := service.TemplateRunLogMetadata.ListTemplateRunLogs(ctx, command.TenantID, command.RunID)
	if err != nil {
		return nil, fmt.Errorf("list template run logs: %w", err)
	}
	if logs == nil {
		return []traits.TemplateRunLog{}, nil
	}

	return logs, nil
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
	case !validStackTags(command.Tags):
		return fmt.Errorf("%w: tags are invalid", ErrInvalidCommand)
	case !validCredentialSetIDs(command.DefaultCredentialIDs):
		return fmt.Errorf("%w: default credential ids are invalid", ErrInvalidCommand)
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

func validateListStacksCommand(command ListStacksCommand) error {
	if command.TenantID == "" {
		return fmt.Errorf("%w: tenant id is required", ErrInvalidCommand)
	}
	return nil
}

func validateListTemplatesCommand(command ListTemplatesCommand) error {
	if command.TenantID == "" {
		return fmt.Errorf("%w: tenant id is required", ErrInvalidCommand)
	}
	return nil
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

func validateStartTemplateRunCommand(command StartTemplateRunCommand) error {
	switch {
	case command.TenantID == "":
		return fmt.Errorf("%w: tenant id is required", ErrInvalidCommand)
	case command.StackTemplateID == "":
		return fmt.Errorf("%w: stack template id is required", ErrInvalidCommand)
	case !command.Operation.Valid():
		return fmt.Errorf("%w: operation is unsupported", ErrInvalidCommand)
	case command.TriggerActor == "":
		return fmt.Errorf("%w: trigger actor is required", ErrInvalidCommand)
	default:
		return nil
	}
}

func validateRegisterTemplateCommand(command RegisterTemplateCommand) error {
	switch {
	case command.TenantID == "":
		return fmt.Errorf("%w: tenant id is required", ErrInvalidCommand)
	case !validGitHubPathComponent(command.RepoOwner):
		return fmt.Errorf("%w: repo owner is required", ErrInvalidCommand)
	case !validGitHubPathComponent(command.RepoName):
		return fmt.Errorf("%w: repo name is required", ErrInvalidCommand)
	case strings.TrimSpace(command.SourceRef) == "":
		return fmt.Errorf("%w: source ref is required", ErrInvalidCommand)
	case !validTemplateRootPath(command.RootPath):
		return fmt.Errorf("%w: root path is invalid", ErrInvalidCommand)
	case command.RequestedBy == "":
		return fmt.Errorf("%w: requested by is required", ErrInvalidCommand)
	default:
		return nil
	}
}

func validateApproveRunCommand(command ApproveRunCommand) error {
	switch {
	case command.TenantID == "":
		return fmt.Errorf("%w: tenant id is required", ErrInvalidCommand)
	case command.RunID == "":
		return fmt.Errorf("%w: run id is required", ErrInvalidCommand)
	case command.ApprovedBy == "":
		return fmt.Errorf("%w: approved by is required", ErrInvalidCommand)
	default:
		return nil
	}
}

func validateCancelRunCommand(command CancelRunCommand) error {
	switch {
	case command.TenantID == "":
		return fmt.Errorf("%w: tenant id is required", ErrInvalidCommand)
	case command.RunID == "":
		return fmt.Errorf("%w: run id is required", ErrInvalidCommand)
	case command.RequestedBy == "":
		return fmt.Errorf("%w: requested by is required", ErrInvalidCommand)
	default:
		return nil
	}
}

func validateGetTemplateRegistrationCommand(command GetTemplateRegistrationCommand) error {
	switch {
	case command.TenantID == "":
		return fmt.Errorf("%w: tenant id is required", ErrInvalidCommand)
	case command.RegistrationID == "":
		return fmt.Errorf("%w: template registration id is required", ErrInvalidCommand)
	default:
		return nil
	}
}

func validateGetTemplateVariablesCommand(command GetTemplateVariablesCommand) error {
	switch {
	case command.TenantID == "":
		return fmt.Errorf("%w: tenant id is required", ErrInvalidCommand)
	case command.TemplateID == "":
		return fmt.Errorf("%w: template id is required", ErrInvalidCommand)
	default:
		return nil
	}
}

func validateGetTemplateRunCommand(command GetTemplateRunCommand) error {
	switch {
	case command.TenantID == "":
		return fmt.Errorf("%w: tenant id is required", ErrInvalidCommand)
	case command.RunID == "":
		return fmt.Errorf("%w: run id is required", ErrInvalidCommand)
	default:
		return nil
	}
}

func validateGetTemplateRunLogCommand(command GetTemplateRunLogCommand) error {
	if err := validateGetTemplateRunCommand(GetTemplateRunCommand{
		TenantID: command.TenantID,
		RunID:    command.RunID,
	}); err != nil {
		return err
	}
	if !validLogPhase(command.Phase) {
		return fmt.Errorf("%w: log phase is unsupported", ErrInvalidCommand)
	}
	return nil
}

func validLogPhase(phase string) bool {
	switch phase {
	case "clone", "init", "workspace", "plan", "apply", "destroy":
		return true
	default:
		return false
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

func validStackTags(tags map[string]string) bool {
	for key, value := range tags {
		if !validTagKey(key) || !validTagValue(value) {
			return false
		}
	}
	return true
}

func validTagKey(key string) bool {
	key = strings.TrimSpace(key)
	if key == "" {
		return false
	}
	for _, r := range key {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.', r == '/', r == ':':
		default:
			return false
		}
	}
	return true
}

func validTagValue(value string) bool {
	for _, r := range strings.TrimSpace(value) {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func validCredentialSetIDs(ids []traits.CredentialSetID) bool {
	for _, id := range ids {
		if strings.TrimSpace(string(id)) == "" {
			return false
		}
	}
	return true
}

func validGitHubPathComponent(component string) bool {
	component = strings.TrimSpace(component)
	if component == "" {
		return false
	}
	for _, r := range component {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.':
		default:
			return false
		}
	}
	return true
}

func validTemplateRootPath(rootPath string) bool {
	rootPath = strings.TrimSpace(rootPath)
	if rootPath == "" || filepath.IsAbs(rootPath) {
		return false
	}
	cleaned := filepath.Clean(rootPath)
	return cleaned != ".." && !strings.HasPrefix(cleaned, ".."+string(filepath.Separator))
}

type systemClock struct{}

func (systemClock) Now() time.Time {
	return time.Now().UTC()
}

type randomTemplateRunIDGenerator struct{}

func (randomTemplateRunIDGenerator) NewTemplateRunID() traits.TemplateRunID {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return traits.TemplateRunID(fmt.Sprintf("run_%d", time.Now().UTC().UnixNano()))
	}
	return traits.TemplateRunID("run_" + hex.EncodeToString(bytes[:]))
}

type randomTemplateRegistrationIDGenerator struct{}

func (randomTemplateRegistrationIDGenerator) NewTemplateRegistrationID() traits.TemplateRegistrationID {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return traits.TemplateRegistrationID(fmt.Sprintf("template_registration_%d", time.Now().UTC().UnixNano()))
	}
	return traits.TemplateRegistrationID("template_registration_" + hex.EncodeToString(bytes[:]))
}

type randomStackIDGenerator struct{}

func (randomStackIDGenerator) NewStackID() traits.StackID {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return traits.StackID(fmt.Sprintf("stack_%d", time.Now().UTC().UnixNano()))
	}
	return traits.StackID("stack_" + hex.EncodeToString(bytes[:]))
}

type randomStackTemplateIDGenerator struct{}

func (randomStackTemplateIDGenerator) NewStackTemplateID() traits.StackTemplateID {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return traits.StackTemplateID(fmt.Sprintf("stack_template_%d", time.Now().UTC().UnixNano()))
	}
	return traits.StackTemplateID("stack_template_" + hex.EncodeToString(bytes[:]))
}
