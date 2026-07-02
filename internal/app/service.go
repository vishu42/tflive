package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/vishu42/megagega/internal/traits"
)

var (
	ErrInvalidCommand           = errors.New("invalid command")
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
	// ApproveTemplateRun records approval only when the tenant-owned run is waiting for approval.
	ApproveTemplateRun(ctx context.Context, approval traits.TemplateRunApproval) error
	// RequestTemplateRunCancellation records cancellation only when the tenant-owned run can still stop.
	RequestTemplateRunCancellation(ctx context.Context, cancellation traits.TemplateRunCancellation) error
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
	StackTemplates StackTemplateRepository
	TemplateRuns   TemplateRunRepository
	Workflows      WorkflowDispatcher
	RunIDs         TemplateRunIDGenerator
	Clock          Clock
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

type systemClock struct{}

func (systemClock) Now() time.Time {
	return time.Now().UTC()
}
