package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/vishu42/megagega/internal/traits"
)

var (
	ErrInvalidCommand           = errors.New("invalid command")
	ErrNotFound                 = errors.New("not found")
	ErrStackTemplateNotRunnable = errors.New("stack template is not runnable")
	ErrRunNotApprovable         = errors.New("run is not approvable")
	ErrRunNotCancelable         = errors.New("run is not cancelable")
)

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
	ApproveTemplateRun(ctx context.Context, tenantID traits.TenantID, runID traits.TemplateRunID, signal traits.ApprovalSignal) error
	CancelTemplateRun(ctx context.Context, tenantID traits.TenantID, runID traits.TemplateRunID, signal traits.CancelSignal) error
}

// TemplateRunIDGenerator creates TemplateRun identifiers.
type TemplateRunIDGenerator interface {
	NewTemplateRunID() traits.TemplateRunID
}

// Clock provides current time for app use cases.
type Clock interface {
	Now() time.Time
}

// Service owns app use cases and the dependencies wired by cmd packages.
type Service struct {
	StackTemplates         StackTemplateRepository
	TemplateRuns           TemplateRunRepository
	TemplateRunLogs        TemplateRunLogReader
	TemplateRunLogMetadata TemplateRunLogRepository
	Workflows              WorkflowDispatcher
	RunIDs                 TemplateRunIDGenerator
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

	return &service
}

// RegisterTemplateCommand asks the app to register a Terraform template source.
type RegisterTemplateCommand struct {
	TenantID             traits.TenantID
	GitHubInstallationID int64
	RepoOwner            string
	RepoName             string
	DefaultRef           string
	RootPath             string
	Name                 string
	Description          string
	Tags                 []string
	Actor                traits.UserID
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
