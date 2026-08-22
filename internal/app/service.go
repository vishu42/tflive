package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vishu42/tflive/internal/authn"
	"github.com/vishu42/tflive/internal/authz"
	"github.com/vishu42/tflive/internal/queue"
	"github.com/vishu42/tflive/internal/traits"
)

var (
	ErrUnauthenticated                    = errors.New("unauthenticated")
	ErrInvalidCommand                     = errors.New("invalid command")
	ErrDuplicateStackSlug                 = errors.New("duplicate stack slug")
	ErrDuplicateStackTemplateComponentKey = errors.New("duplicate stack template component key")
	ErrNotFound                           = errors.New("not found")
	ErrTemplateNotInstallable             = errors.New("template is not installable")
	ErrStackTemplateConfigInvalid         = errors.New("stack template config is invalid")
	ErrStackTemplateUpgradeInvalid        = errors.New("stack template upgrade is invalid")
	ErrStackTemplateNotRunnable           = errors.New("stack template is not runnable")
	ErrStackTemplatePlanStale             = errors.New("stack template plan does not match desired state")
	ErrRunNotApprovable                   = errors.New("run is not approvable")
	ErrRunNotCancelable                   = errors.New("run is not cancelable")
	ErrSelfApprovalForbidden              = errors.New("self-approval forbidden")
	ErrLastOwner                          = errors.New("cannot remove the last stack owner")
	ErrCredentialNameInvalid              = errors.New("credential name is invalid")
	ErrDuplicateCredentialName            = errors.New("duplicate credential name")
	ErrCredentialEncryptionUnavailable    = errors.New("credential encryption is unavailable")
)

func authenticatedActor(ctx context.Context) (traits.UserID, error) {
	principal, ok := authn.PrincipalFromContext(ctx)
	if !ok || principal.Subject == "" {
		return "", ErrUnauthenticated
	}
	return traits.UserID(principal.Subject), nil
}

// ErrForbidden indicates an authenticated actor lacks permission for an operation.
var ErrForbidden = errors.New("forbidden")

func canCreateStack(principal authn.Principal) bool {
	for _, role := range principal.RealmRoles {
		if role == "platform-admin" || role == "stack-creator" {
			return true
		}
	}
	return false
}

// StackRepository persists and reads tenant-owned stacks.
type StackPageCursor struct {
	CreatedAt time.Time
	ID        traits.StackID
}

// TxRepo is the transaction-scoped repository surface handed to a unit of
// work. It is deliberately tiny and grows only when a new dual-write point
// appears; every other repository method keeps opening its own transaction.
type TxRepo interface {
	CreateStack(ctx context.Context, stack traits.Stack) error
	AppendAuditEvent(ctx context.Context, event traits.SecurityAuditEvent) error
	CreateTemplateRun(ctx context.Context, run traits.TemplateRun) error
	CreateTemplateRegistration(ctx context.Context, registration traits.TemplateRegistration) error
	ApproveTemplateRun(ctx context.Context, approval traits.TemplateRunApproval) error
	RequestTemplateRunCancellation(ctx context.Context, cancellation traits.TemplateRunCancellation) error
}

// UnitOfWork commits a domain write and a queued intent atomically. This is
// what makes the queue an outbox rather than a second system to dual-write to.
type UnitOfWork interface {
	InTx(ctx context.Context, fn func(TxRepo, queue.Enqueuer) error) error
}

type StackRepository interface {
	CreateStack(ctx context.Context, stack traits.Stack) error
	GetStack(ctx context.Context, tenantID traits.TenantID, stackID traits.StackID) (traits.Stack, error)
	GetStackWithTemplates(ctx context.Context, tenantID traits.TenantID, stackID traits.StackID) (StackView, error)
	ListStacks(ctx context.Context, tenantID traits.TenantID) ([]traits.Stack, error)
	ListStacksPage(ctx context.Context, tenantID traits.TenantID, after *StackPageCursor, limit int) ([]traits.Stack, error)
}

// StackTemplateRepository reads installed template state for use cases.
type StackTemplateRepository interface {
	GetStackTemplate(ctx context.Context, tenantID traits.TenantID, id traits.StackTemplateID) (traits.StackTemplate, error)
	UpdateStackTemplateConfig(ctx context.Context, tenantID traits.TenantID, id traits.StackTemplateID, configJSON json.RawMessage) (traits.StackTemplate, error)
	UpdateStackTemplateDesiredRevision(ctx context.Context, tenantID traits.TenantID, id traits.StackTemplateID, templateRevisionID traits.TemplateRevisionID, configJSON json.RawMessage) (traits.StackTemplate, error)
}

// CredentialRepository persists encrypted, scope-owned Terraform credentials.
type CredentialRepository interface {
	CreateCredential(ctx context.Context, credential traits.CredentialSet) error
	ListCredentials(ctx context.Context, tenantID traits.TenantID, scope traits.CredentialScope, ownerID string) ([]traits.CredentialSet, error)
	DeleteCredential(ctx context.Context, tenantID traits.TenantID, id traits.CredentialSetID) error
	ListCredentialsForStackTemplate(ctx context.Context, tenantID traits.TenantID, stackTemplateID traits.StackTemplateID) ([]traits.CredentialSet, error)
}

// CredentialEncryptor protects credential values before persistence.
type CredentialEncryptor interface {
	Encrypt(string) (string, error)
}

// TemplateRunRepository persists TemplateRun records and run decisions.
type TemplateRunRepository interface {
	CreateTemplateRun(ctx context.Context, run traits.TemplateRun) error
	GetTemplateRun(ctx context.Context, tenantID traits.TenantID, runID traits.TemplateRunID) (traits.TemplateRun, error)
	// ListTemplateRuns returns tenant-owned runs for one stack template, most recent first.
	ListTemplateRuns(ctx context.Context, tenantID traits.TenantID, stackTemplateID traits.StackTemplateID) ([]traits.TemplateRun, error)
	// ApproveTemplateRun records approval only when the tenant-owned run is waiting for approval.
	ApproveTemplateRun(ctx context.Context, approval traits.TemplateRunApproval) error
	// RequestTemplateRunCancellation records cancellation only when the tenant-owned run can still stop.
	RequestTemplateRunCancellation(ctx context.Context, cancellation traits.TemplateRunCancellation) error
	// ReconcileTemplateRunCancellation marks a persisted cancellation request failed when its workflow is closed.
	ReconcileTemplateRunCancellation(ctx context.Context, tenantID traits.TenantID, runID traits.TemplateRunID, errorSummary string) error
}

// TemplateRegistrationRepository persists template registration attempts.
type TemplateRegistrationRepository interface {
	CreateTemplateRegistration(ctx context.Context, registration traits.TemplateRegistration) error
	GetTemplateRegistration(ctx context.Context, tenantID traits.TenantID, id traits.TemplateRegistrationID) (traits.TemplateRegistration, error)
	RecordTemplateRegistrationStatus(ctx context.Context, input traits.TemplateRegistrationStatusActivityInput) error
}

// TemplateRevisionRepository persists immutable template metadata and inferred variables.
type TemplateRevisionRepository interface {
	UpsertTemplateRevisionWithVariables(ctx context.Context, templateRevision traits.TemplateRevision, variables []traits.TemplateVariable) (traits.TemplateRevision, error)
	ListTemplateRevisions(ctx context.Context, tenantID traits.TenantID) ([]traits.TemplateRevision, error)
	GetTemplateRevisionVariables(ctx context.Context, tenantID traits.TenantID, templateRevisionID traits.TemplateRevisionID) ([]traits.TemplateVariable, error)
}

// TemplateRevisionMetadataRepository reads immutable template metadata.
type TemplateRevisionMetadataRepository interface {
	GetTemplateRevision(ctx context.Context, tenantID traits.TenantID, templateRevisionID traits.TemplateRevisionID) (traits.TemplateRevision, error)
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

// AuditRepository appends security audit events. It exposes no update or
// delete methods, enforcing append-only semantics through the interface.
type AuditRepository interface {
	AppendAuditEvent(ctx context.Context, event traits.SecurityAuditEvent) error
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

type ListStackGrantsCommand struct {
	TenantID traits.TenantID
	StackID  traits.StackID
}

type GrantView struct {
	UserSub     string `json:"userSub"`
	Role        string `json:"role"`
	DisplayName string `json:"displayName"`
	Email       string `json:"email"`
}

type ListStackGrantsResult struct {
	Grants []GrantView
}

type AssignStackRoleCommand struct {
	TenantID traits.TenantID
	StackID  traits.StackID
	UserSub  string
	Role     string
}

type RevokeStackRoleCommand struct {
	TenantID traits.TenantID
	StackID  traits.StackID
	UserSub  string
}

// Service owns app use cases and the dependencies wired by cmd packages.
type Service struct {
	Authorizer               authz.Authorizer
	Work                     UnitOfWork
	Stacks                   StackRepository
	StackStatuses            StackStatusRepository
	StackTemplates           StackTemplateRepository
	Credentials              CredentialRepository
	CredentialEncryptor      CredentialEncryptor
	TemplateRuns             TemplateRunRepository
	TemplateRegistrations    TemplateRegistrationRepository
	TemplateRevisionMetadata TemplateRevisionMetadataRepository
	TemplateRevisions        TemplateRevisionRepository
	StackTemplateInstaller   StackTemplateInstaller
	TemplateRunLogs          TemplateRunLogReader
	TemplateRunLogMetadata   TemplateRunLogRepository
	Workflows                WorkflowDispatcher
	StackIDs                 StackIDGenerator
	StackTemplateIDs         StackTemplateIDGenerator
	CredentialIDs            CredentialSetIDGenerator
	RunIDs                   TemplateRunIDGenerator
	RegistrationIDs          TemplateRegistrationIDGenerator
	Clock                    Clock
	Audit                    AuditRepository
	UserDirectory            UserDirectory
}

// CredentialSetIDGenerator creates credential identifiers.
type CredentialSetIDGenerator interface {
	// NewCredentialSetID returns a fresh identifier for a credential record.
	NewCredentialSetID() traits.CredentialSetID
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
	if service.CredentialIDs == nil {
		service.CredentialIDs = randomCredentialSetIDGenerator{}
	}

	return &service
}

func (service *Service) auditError(ctx context.Context, event traits.SecurityAuditEvent) {
	if service.Audit == nil {
		return
	}
	if err := service.Audit.AppendAuditEvent(ctx, event); err != nil {
		log.Printf("security audit write failed: action=%s actor=%s outcome=%s err=%v",
			event.Action, event.ActorSubject, event.Outcome, err)
	}
}

// RegisterTemplateCommand asks the app to register a Terraform template source.
type RegisterTemplateCommand struct {
	TenantID  traits.TenantID
	RepoOwner string
	RepoName  string
	SourceRef string
	RootPath  string
}

// CreateStackCommand asks the app to create a logical infrastructure stack.
type CreateStackCommand struct {
	TenantID             traits.TenantID
	Name                 string
	Slug                 string
	Tags                 map[string]string
	DefaultCredentialIDs []traits.CredentialSetID
}

// StackView returns one stack with its installed templates.
type StackView struct {
	Stack        traits.Stack
	Templates    []StackTemplateView
	Capabilities StackCapabilities
}

// StackTemplateView is one installed component plus the labels a read resolves
// for it. Those labels are not component state: they are properties of the
// desired revision, looked up per read so they cannot drift from the revision
// in use. They live here rather than on traits.StackTemplate so the persisted
// record has no fields that are only ever populated on one path.
type StackTemplateView struct {
	traits.StackTemplate
	// DisplayName is a human-readable label derived from template metadata.
	DisplayName string
	// SourceRef is the desired revision's ref.
	SourceRef string
}

// AddTemplateToStackCommand asks the app to install one template into a stack.
type AddTemplateToStackCommand struct {
	TenantID           traits.TenantID
	StackID            traits.StackID
	TemplateRevisionID traits.TemplateRevisionID
	ComponentKey       string
	WorkspaceName      string
	ConfigJSON         json.RawMessage
}

// StartTemplateRunCommand asks the app to start one Terraform operation.
type StartTemplateRunCommand struct {
	TenantID        traits.TenantID
	StackTemplateID traits.StackTemplateID
	Operation       traits.OperationType
}

// CreateCredentialCommand asks the app to create one encrypted credential in a scope.
type CreateCredentialCommand struct {
	TenantID        traits.TenantID
	Scope           traits.CredentialScope
	StackID         traits.StackID
	StackTemplateID traits.StackTemplateID
	Name            string
	Value           string
}

// ListCredentialsCommand asks the app for non-sensitive credential metadata.
type ListCredentialsCommand struct {
	TenantID        traits.TenantID
	Scope           traits.CredentialScope
	StackID         traits.StackID
	StackTemplateID traits.StackTemplateID
}

// DeleteCredentialCommand asks the app to remove one scoped credential.
type DeleteCredentialCommand struct {
	TenantID        traits.TenantID
	CredentialID    traits.CredentialSetID
	StackID         traits.StackID
	StackTemplateID traits.StackTemplateID
}

// CredentialMetadata is the safe API representation of a credential.
type CredentialMetadata struct {
	ID        traits.CredentialSetID `json:"id"`
	Name      string                 `json:"name"`
	Scope     traits.CredentialScope `json:"scope"`
	StackID   traits.StackID         `json:"stack_id,omitempty"`
	Template  traits.StackTemplateID `json:"stack_template_id,omitempty"`
	CreatedAt time.Time              `json:"created_at"`
}

// UpdateStackTemplateConfigCommand asks the app to edit desired config before a run.
type UpdateStackTemplateConfigCommand struct {
	TenantID        traits.TenantID
	StackTemplateID traits.StackTemplateID
	ConfigJSON      json.RawMessage
}

// UpgradeStackTemplateCommand asks the app to point one install at another revision.
type UpgradeStackTemplateCommand struct {
	TenantID                 traits.TenantID
	StackTemplateID          traits.StackTemplateID
	TargetTemplateRevisionID traits.TemplateRevisionID
	ConfigJSON               json.RawMessage
}

// ApproveRunCommand asks the app to approve a waiting run.
type ApproveRunCommand struct {
	TenantID traits.TenantID
	RunID    traits.TemplateRunID
}

// CancelRunCommand asks the app to cancel a running run.
type CancelRunCommand struct {
	TenantID traits.TenantID
	RunID    traits.TemplateRunID
	Reason   string
}

// GetStackCommand asks the app to read one stack and its installed templates.
type GetStackCommand struct {
	TenantID traits.TenantID
	StackID  traits.StackID
}

// SearchUsersCommand asks the app to search realm users via the directory.
type SearchUsersCommand struct {
	TenantID traits.TenantID
	Query    string
	First    int
	Max      int
}

// ListStacksCommand asks the app to list tenant-owned stacks.
type ListStacksCommand struct {
	TenantID traits.TenantID
}

// ListTemplateRevisionsCommand asks the app to list tenant-owned registered template revisions.
type ListTemplateRevisionsCommand struct {
	TenantID traits.TenantID
}

// GetTemplateRunCommand asks the app to read one tenant-owned run.
type GetTemplateRunCommand struct {
	TenantID traits.TenantID
	RunID    traits.TemplateRunID
}

// ListTemplateRunsCommand asks the app to list tenant-owned runs for one stack template.
type ListTemplateRunsCommand struct {
	TenantID        traits.TenantID
	StackTemplateID traits.StackTemplateID
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

// GetTemplateRevisionVariablesCommand asks the app to read inferred variables for one tenant-owned template revision.
type GetTemplateRevisionVariablesCommand struct {
	TenantID           traits.TenantID
	TemplateRevisionID traits.TemplateRevisionID
}

// RegisterTemplate creates a pending registration attempt and dispatches its sync workflow.
func (service *Service) RegisterTemplate(ctx context.Context, command RegisterTemplateCommand) (traits.TemplateRegistration, error) {
	if err := requireTemplateCatalogAccess(ctx); err != nil {
		return traits.TemplateRegistration{}, err
	}
	actor, err := authenticatedActor(ctx)
	if err != nil {
		return traits.TemplateRegistration{}, err
	}
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
		RequestedBy: actor,
		RequestedAt: service.Clock.Now(),
	}

	input := traits.TemplateSyncWorkflowInput{
		RegistrationID: registration.ID,
		TenantID:       registration.TenantID,
		RepoOwner:      registration.RepoOwner,
		RepoName:       registration.RepoName,
		SourceRef:      registration.SourceRef,
		RootPath:       registration.RootPath,
	}
	payload, err := json.Marshal(StartTemplateSyncPayload(input))
	if err != nil {
		return traits.TemplateRegistration{}, fmt.Errorf("encode start template sync payload: %w", err)
	}
	if err := service.Work.InTx(ctx, func(repository TxRepo, enqueuer queue.Enqueuer) error {
		if err := repository.CreateTemplateRegistration(ctx, registration); err != nil {
			return err
		}
		return enqueuer.Enqueue(ctx, queue.Request{
			Kind:         KindStartTemplateSync,
			Payload:      payload,
			ActorSubject: string(actor),
			TenantID:     string(registration.TenantID),
		})
	}); err != nil {
		return traits.TemplateRegistration{}, fmt.Errorf("create template registration: %w", err)
	}

	return registration, nil
}

// CreateStack creates a tenant-owned infrastructure stack.
func (service *Service) CreateStack(ctx context.Context, command CreateStackCommand) (traits.Stack, error) {
	principal, ok := authn.PrincipalFromContext(ctx)
	if !ok || principal.Subject == "" {
		return traits.Stack{}, ErrUnauthenticated
	}
	if !canCreateStack(principal) {
		return traits.Stack{}, ErrForbidden
	}
	actor := traits.UserID(principal.Subject)
	if err := validateCreateStackCommand(command); err != nil {
		return traits.Stack{}, err
	}
	if service.Authorizer == nil {
		return traits.Stack{}, fmt.Errorf("%w: authorization not configured", authz.ErrUnavailable)
	}
	if service.Work == nil {
		return traits.Stack{}, fmt.Errorf("%w: unit of work not configured", authz.ErrUnavailable)
	}
	// Validate the identity now so a malformed subject fails the request rather
	// than the handler, where it would retry forever.
	if _, err := authz.SubjectFromOIDCSub(principal.Subject); err != nil {
		return traits.Stack{}, fmt.Errorf("create owner subject: %w", err)
	}

	slug := strings.TrimSpace(command.Slug)
	if slug == "" {
		slug = slugFromName(command.Name)
	}

	stack := traits.Stack{
		ID:       service.StackIDs.NewStackID(),
		TenantID: command.TenantID,
		Name:     strings.TrimSpace(command.Name),
		Slug:     slug,
		// The owner tuple lands from the queue, not from this request, so the
		// stack is recorded as not yet usable rather than silently returned as
		// if it were.
		Status:               traits.StackStatusProvisioning,
		Tags:                 cloneStringMap(command.Tags),
		DefaultCredentialIDs: append([]traits.CredentialSetID(nil), command.DefaultCredentialIDs...),
		CreatedBy:            actor,
		CreatedAt:            service.Clock.Now(),
	}
	if _, err := authz.ObjectFromID(authz.TypeStack, string(stack.ID)); errors.Is(err, authz.ErrInvalidInput) {
		return traits.Stack{}, fmt.Errorf("%w: generated stack ID is invalid", authz.ErrMalformedResponse)
	} else if err != nil {
		return traits.Stack{}, fmt.Errorf("create owner stack: %w", err)
	}

	// grant_stack_owner, not reconcile_stack_grant: creation is an event, and its
	// ModeJob "do nothing" on a duplicate key keeps the founding owner from
	// being overwritten by any later enqueue on the same stack.
	payload, err := json.Marshal(GrantStackOwnerPayload{
		StackID:  string(stack.ID),
		TenantID: string(command.TenantID),
		Subject:  principal.Subject,
	})
	if err != nil {
		return traits.Stack{}, fmt.Errorf("encode grant stack owner payload: %w", err)
	}

	auditEvent := traits.SecurityAuditEvent{
		ActorSubject:  string(actor),
		Action:        traits.AuditActionGrant,
		TargetUser:    string(actor),
		TenantID:      command.TenantID,
		StackID:       stack.ID,
		NewRole:       "owner",
		Outcome:       traits.AuditOutcomeSuccess,
		CorrelationID: "",
	}

	// The stack row and the provisioning intent commit together, so a crash can
	// never leave a stack whose creator has no access and no record of the
	// intent.
	if err := service.Work.InTx(ctx, func(repository TxRepo, enqueuer queue.Enqueuer) error {
		if err := repository.CreateStack(ctx, stack); err != nil {
			return err
		}
		if err := repository.AppendAuditEvent(ctx, auditEvent); err != nil {
			return err
		}
		return enqueuer.Enqueue(ctx, queue.Request{
			Kind:         KindGrantStackOwner,
			Payload:      payload,
			ActorSubject: principal.Subject,
			TenantID:     string(command.TenantID),
		})
	}); err != nil {
		return traits.Stack{}, fmt.Errorf("create stack: %w", err)
	}

	return stack, nil
}

// AddTemplateToStack validates one template install and persists the tenant-owned stack template.
func (service *Service) AddTemplateToStack(ctx context.Context, command AddTemplateToStackCommand) (traits.StackTemplate, error) {
	actor, err := authenticatedActor(ctx)
	if err != nil {
		return traits.StackTemplate{}, err
	}
	if err := validateAddTemplateToStackCommand(command); err != nil {
		return traits.StackTemplate{}, err
	}

	if err := authorizeStack(ctx, service.Authorizer, command.StackID, authz.RelationCanOperate, ErrForbidden); err != nil {
		service.auditError(ctx, traits.SecurityAuditEvent{
			ActorSubject: string(actor),
			Action:       traits.AuditActionFailedAccessAttempt,
			TenantID:     command.TenantID,
			StackID:      command.StackID,
			Outcome:      traits.AuditOutcomeFailure,
		})
		return traits.StackTemplate{}, err
	}
	stack, err := service.Stacks.GetStack(ctx, command.TenantID, command.StackID)
	if err != nil {
		return traits.StackTemplate{}, fmt.Errorf("get stack: %w", err)
	}

	templateRevision, err := service.TemplateRevisionMetadata.GetTemplateRevision(ctx, command.TenantID, command.TemplateRevisionID)
	if err != nil {
		return traits.StackTemplate{}, fmt.Errorf("get template revision: %w", err)
	}
	if templateRevision.Status != traits.TemplateRevisionActive {
		return traits.StackTemplate{}, fmt.Errorf("%w: status is %q", ErrTemplateNotInstallable, templateRevision.Status)
	}

	variables, err := service.TemplateRevisions.GetTemplateRevisionVariables(ctx, command.TenantID, command.TemplateRevisionID)
	if err != nil {
		return traits.StackTemplate{}, fmt.Errorf("get template revision variables: %w", err)
	}
	configJSON, err := validateTemplateConfig(command.ConfigJSON, variables)
	if err != nil {
		return traits.StackTemplate{}, err
	}

	id := service.StackTemplateIDs.NewStackTemplateID()
	stackTemplate := traits.StackTemplate{
		ID:                        id,
		TenantID:                  command.TenantID,
		StackID:                   command.StackID,
		ComponentKey:              componentKey(command.ComponentKey, id),
		SourceTemplateID:          templateRevision.SourceTemplateID,
		DesiredTemplateRevisionID: command.TemplateRevisionID,
		WorkspaceName:             workspaceName(stack.Slug, id),
		InstalledConfigJSON:       configJSON,
		DesiredConfigJSON:         configJSON,
		CreatedBy:                 actor,
		Lifecycle:                 traits.StackTemplateActive,
	}

	if err := service.StackTemplateInstaller.CreateStackTemplate(ctx, stackTemplate); err != nil {
		return traits.StackTemplate{}, fmt.Errorf("create stack template: %w", err)
	}
	return stackTemplate, nil
}

// StartTemplateRun creates a queued run. The repository persists its workflow
// dispatch intent atomically for an asynchronous dispatcher to process.
func (service *Service) StartTemplateRun(ctx context.Context, command StartTemplateRunCommand) (traits.TemplateRun, error) {
	actor, err := authenticatedActor(ctx)
	if err != nil {
		return traits.TemplateRun{}, err
	}
	if err := validateStartTemplateRunCommand(command); err != nil {
		return traits.TemplateRun{}, err
	}

	stackTemplate, err := service.authorizedStackTemplate(ctx, command.TenantID, command.StackTemplateID, authz.RelationCanOperate, ErrForbidden)
	if err != nil {
		service.auditError(ctx, traits.SecurityAuditEvent{
			ActorSubject: string(actor),
			Action:       traits.AuditActionFailedAccessAttempt,
			TenantID:     command.TenantID,
			StackID:      "",
			Outcome:      traits.AuditOutcomeFailure,
		})
		return traits.TemplateRun{}, fmt.Errorf("get stack template: %w", err)
	}
	if stackTemplate.Lifecycle != traits.StackTemplateActive {
		return traits.TemplateRun{}, fmt.Errorf("%w: lifecycle is %q", ErrStackTemplateNotRunnable, stackTemplate.Lifecycle)
	}
	// TODO: Reject active StackTemplate records with a missing WorkspaceName,
	// or enforce that invariant with a Postgres CHECK constraint.
	desiredTemplateRevisionID := stackTemplate.DesiredTemplateRevisionID
	if desiredTemplateRevisionID == "" {
		return traits.TemplateRun{}, fmt.Errorf("%w: desired template revision is required", ErrStackTemplateNotRunnable)
	}
	desiredConfigJSON := stackTemplate.DesiredConfig()

	// Reviewing a plan is the approval gate, so an apply may only run what that
	// plan described. Saving config and changing revision both move desired
	// without touching runs, which leaves a completed plan in place describing
	// something else; the run below would then snapshot current desired rather
	// than the reviewed snapshot. Requiring a matching plan also makes
	// plan-before-apply a rule instead of a web-client convention — the API
	// accepted an apply that had never been planned at all.
	if command.Operation == traits.OperationApply {
		if planState := stackTemplate.PlanState(); planState != traits.PlanMatches {
			return traits.TemplateRun{}, fmt.Errorf("%w: plan state is %q", ErrStackTemplatePlanStale, planState)
		}
	}

	templateRevision, err := service.TemplateRevisionMetadata.GetTemplateRevision(ctx, command.TenantID, desiredTemplateRevisionID)
	if err != nil {
		return traits.TemplateRun{}, fmt.Errorf("get template revision metadata: %w", err)
	}
	sourceTemplateID := stackTemplate.SourceTemplateID
	if sourceTemplateID == "" {
		sourceTemplateID = templateRevision.SourceTemplateID
	}

	run := traits.TemplateRun{
		ID:                 service.RunIDs.NewTemplateRunID(),
		TenantID:           command.TenantID,
		StackTemplateID:    command.StackTemplateID,
		TemplateRevisionID: desiredTemplateRevisionID,
		SourceTemplateID:   sourceTemplateID,
		Operation:          command.Operation,
		// The ref comes from the revision being run, not from the component:
		// the component's install-time ref does not change when the desired
		// revision does, so it would misreport what this run is against.
		SelectedRef:       templateRevision.SourceRef,
		ResolvedCommitSHA: templateRevision.ResolvedCommitSHA,
		WorkspaceName:     stackTemplate.WorkspaceName,
		ConfigJSON:        desiredConfigJSON,
		Status:            traits.TemplateRunQueued,
		TriggerActor:      actor,
		StartedAt:         service.Clock.Now(),
	}

	input := traits.TemplateRunWorkflowInput{
		RunID:             run.ID,
		TenantID:          run.TenantID,
		StackTemplateID:   run.StackTemplateID,
		Operation:         run.Operation,
		SelectedRef:       run.SelectedRef,
		ResolvedCommitSHA: run.ResolvedCommitSHA,
		WorkspaceName:     run.WorkspaceName,
		RepoOwner:         templateRevision.RepoOwner,
		RepoName:          templateRevision.RepoName,
		RootPath:          templateRevision.RootPath,
		ConfigJSON:        run.ConfigJSON,
	}
	payload, err := json.Marshal(StartTemplateRunPayload(input))
	if err != nil {
		return traits.TemplateRun{}, fmt.Errorf("encode start template run payload: %w", err)
	}
	if err := service.Work.InTx(ctx, func(repository TxRepo, enqueuer queue.Enqueuer) error {
		if err := repository.CreateTemplateRun(ctx, run); err != nil {
			return err
		}
		return enqueuer.Enqueue(ctx, queue.Request{
			Kind:         KindStartTemplateRun,
			Payload:      payload,
			ActorSubject: string(actor),
			TenantID:     string(run.TenantID),
		})
	}); err != nil {
		return traits.TemplateRun{}, fmt.Errorf("create template run: %w", err)
	}

	return run, nil
}

// CreateCredential creates one encrypted environment variable in a Stack or StackTemplate scope.
func (service *Service) CreateCredential(ctx context.Context, command CreateCredentialCommand) (CredentialMetadata, error) {
	_, err := authenticatedActor(ctx)
	if err != nil {
		return CredentialMetadata{}, err
	}
	if service.Credentials == nil || service.CredentialEncryptor == nil {
		return CredentialMetadata{}, ErrCredentialEncryptionUnavailable
	}
	if err := validateCreateCredentialCommand(command); err != nil {
		return CredentialMetadata{}, err
	}
	var stackID traits.StackID
	var templateID traits.StackTemplateID
	switch command.Scope {
	case traits.CredentialScopeStack:
		stackID = command.StackID
		if err := authorizeStack(ctx, service.Authorizer, stackID, authz.RelationCanManageAccess, ErrForbidden); err != nil {
			return CredentialMetadata{}, err
		}
	case traits.CredentialScopeStackTemplate:
		templateID = command.StackTemplateID
		stackTemplate, err := service.StackTemplates.GetStackTemplate(ctx, command.TenantID, templateID)
		if err != nil {
			return CredentialMetadata{}, err
		}
		stackID = stackTemplate.StackID
		if err := authorizeStack(ctx, service.Authorizer, stackID, authz.RelationCanManageAccess, ErrForbidden); err != nil {
			return CredentialMetadata{}, err
		}
	default:
		return CredentialMetadata{}, fmt.Errorf("%w: credential scope is invalid", ErrInvalidCommand)
	}
	ciphertext, err := service.CredentialEncryptor.Encrypt(command.Value)
	if err != nil {
		return CredentialMetadata{}, fmt.Errorf("encrypt credential: %w", err)
	}
	credential := traits.CredentialSet{
		ID:              service.CredentialIDs.NewCredentialSetID(),
		TenantID:        command.TenantID,
		StackID:         stackID,
		StackTemplateID: templateID,
		Name:            strings.TrimSpace(command.Name),
		Ciphertext:      ciphertext,
		CreatedAt:       service.Clock.Now(),
	}
	if err := service.Credentials.CreateCredential(ctx, credential); err != nil {
		return CredentialMetadata{}, err
	}
	return credentialMetadata(credential), nil
}

// ListCredentials returns only credential names and metadata.
func (service *Service) ListCredentials(ctx context.Context, command ListCredentialsCommand) ([]CredentialMetadata, error) {
	if _, err := authenticatedActor(ctx); err != nil {
		return nil, err
	}
	if service.Credentials == nil {
		return nil, ErrCredentialEncryptionUnavailable
	}
	if err := validateListCredentialsCommand(command); err != nil {
		return nil, err
	}
	ownerID := string(command.StackID)
	if command.Scope == traits.CredentialScopeStackTemplate {
		ownerID = string(command.StackTemplateID)
		stackTemplate, err := service.StackTemplates.GetStackTemplate(ctx, command.TenantID, command.StackTemplateID)
		if err != nil {
			return nil, err
		}
		if err := authorizeStack(ctx, service.Authorizer, stackTemplate.StackID, authz.RelationCanManageAccess, ErrForbidden); err != nil {
			return nil, err
		}
	} else if err := authorizeStack(ctx, service.Authorizer, command.StackID, authz.RelationCanManageAccess, ErrForbidden); err != nil {
		return nil, err
	}
	credentials, err := service.Credentials.ListCredentials(ctx, command.TenantID, command.Scope, ownerID)
	if err != nil {
		return nil, err
	}
	metadata := make([]CredentialMetadata, 0, len(credentials))
	for _, credential := range credentials {
		metadata = append(metadata, credentialMetadata(credential))
	}
	return metadata, nil
}

// DeleteCredential deletes a scoped credential. Rotation is delete-and-recreate.
func (service *Service) DeleteCredential(ctx context.Context, command DeleteCredentialCommand) error {
	if _, err := authenticatedActor(ctx); err != nil {
		return err
	}
	if service.Credentials == nil {
		return ErrCredentialEncryptionUnavailable
	}
	if command.TenantID == "" || command.CredentialID == "" {
		return fmt.Errorf("%w: tenant and credential IDs are required", ErrInvalidCommand)
	}
	var stackID traits.StackID
	if command.StackID != "" {
		stackID = command.StackID
	} else if service.StackTemplates != nil && command.StackTemplateID != "" {
		stackTemplate, err := service.StackTemplates.GetStackTemplate(ctx, command.TenantID, command.StackTemplateID)
		if err != nil {
			return err
		}
		stackID = stackTemplate.StackID
	} else {
		return fmt.Errorf("%w: credential scope is required", ErrInvalidCommand)
	}
	if err := authorizeStack(ctx, service.Authorizer, stackID, authz.RelationCanManageAccess, ErrForbidden); err != nil {
		return err
	}
	scope := traits.CredentialScopeStack
	ownerID := string(command.StackID)
	if command.StackTemplateID != "" {
		scope = traits.CredentialScopeStackTemplate
		ownerID = string(command.StackTemplateID)
	}
	credentials, err := service.Credentials.ListCredentials(ctx, command.TenantID, scope, ownerID)
	if err != nil {
		return err
	}
	found := false
	for _, credential := range credentials {
		if credential.ID == command.CredentialID {
			found = true
			break
		}
	}
	if !found {
		return ErrNotFound
	}
	return service.Credentials.DeleteCredential(ctx, command.TenantID, command.CredentialID)
}

// credentialMetadata strips ciphertext and exposes only safe credential metadata to API callers.
func credentialMetadata(credential traits.CredentialSet) CredentialMetadata {
	return CredentialMetadata{ID: credential.ID, Name: credential.Name, Scope: credentialScope(credential), StackID: credential.StackID, Template: credential.StackTemplateID, CreatedAt: credential.CreatedAt}
}

// credentialScope derives the public scope from the populated owner column.
func credentialScope(credential traits.CredentialSet) traits.CredentialScope {
	if credential.StackTemplateID != "" {
		return traits.CredentialScopeStackTemplate
	}
	return traits.CredentialScopeStack
}

// UpdateStackTemplateConfig validates and saves desired config for an installed template.
func (service *Service) UpdateStackTemplateConfig(ctx context.Context, command UpdateStackTemplateConfigCommand) (traits.StackTemplate, error) {
	actor, err := authenticatedActor(ctx)
	if err != nil {
		return traits.StackTemplate{}, err
	}
	if err := validateUpdateStackTemplateConfigCommand(command); err != nil {
		return traits.StackTemplate{}, err
	}

	stackTemplate, err := service.authorizedStackTemplate(ctx, command.TenantID, command.StackTemplateID, authz.RelationCanOperate, ErrForbidden)
	if err != nil {
		service.auditError(ctx, traits.SecurityAuditEvent{
			ActorSubject: string(actor),
			Action:       traits.AuditActionFailedAccessAttempt,
			TenantID:     command.TenantID,
			Outcome:      traits.AuditOutcomeFailure,
		})
		return traits.StackTemplate{}, fmt.Errorf("get stack template: %w", err)
	}
	if stackTemplate.Lifecycle != traits.StackTemplateActive {
		return traits.StackTemplate{}, fmt.Errorf("%w: lifecycle is %q", ErrStackTemplateUpgradeInvalid, stackTemplate.Lifecycle)
	}

	desiredTemplateRevisionID := stackTemplate.DesiredTemplateRevisionID
	if desiredTemplateRevisionID == "" {
		return traits.StackTemplate{}, fmt.Errorf("%w: desired template revision is required", ErrStackTemplateConfigInvalid)
	}
	variables, err := service.TemplateRevisions.GetTemplateRevisionVariables(ctx, command.TenantID, desiredTemplateRevisionID)
	if err != nil {
		return traits.StackTemplate{}, fmt.Errorf("get template revision variables: %w", err)
	}
	configJSON, err := validateTemplateConfig(command.ConfigJSON, variables)
	if err != nil {
		return traits.StackTemplate{}, err
	}

	updated, err := service.StackTemplates.UpdateStackTemplateConfig(ctx, command.TenantID, command.StackTemplateID, configJSON)
	if err != nil {
		return traits.StackTemplate{}, fmt.Errorf("update stack template config: %w", err)
	}
	return updated, nil
}

// UpgradeStackTemplate changes desired revision for an existing installed template.
func (service *Service) UpgradeStackTemplate(ctx context.Context, command UpgradeStackTemplateCommand) (traits.StackTemplate, error) {
	actor, err := authenticatedActor(ctx)
	if err != nil {
		return traits.StackTemplate{}, err
	}
	if err := validateUpgradeStackTemplateCommand(command); err != nil {
		return traits.StackTemplate{}, err
	}

	stackTemplate, err := service.authorizedStackTemplate(ctx, command.TenantID, command.StackTemplateID, authz.RelationCanOperate, ErrForbidden)
	if err != nil {
		service.auditError(ctx, traits.SecurityAuditEvent{
			ActorSubject: string(actor),
			Action:       traits.AuditActionFailedAccessAttempt,
			TenantID:     command.TenantID,
			Outcome:      traits.AuditOutcomeFailure,
		})
		return traits.StackTemplate{}, fmt.Errorf("get stack template: %w", err)
	}
	if stackTemplate.Lifecycle != traits.StackTemplateActive {
		return traits.StackTemplate{}, fmt.Errorf("%w: lifecycle is %q", ErrStackTemplateUpgradeInvalid, stackTemplate.Lifecycle)
	}

	targetRevision, err := service.TemplateRevisionMetadata.GetTemplateRevision(ctx, command.TenantID, command.TargetTemplateRevisionID)
	if err != nil {
		return traits.StackTemplate{}, fmt.Errorf("get target template revision: %w", err)
	}
	if targetRevision.Status != traits.TemplateRevisionActive {
		return traits.StackTemplate{}, fmt.Errorf("%w: status is %q", ErrTemplateNotInstallable, targetRevision.Status)
	}
	if stackTemplate.SourceTemplateID != "" && targetRevision.SourceTemplateID != "" && stackTemplate.SourceTemplateID != targetRevision.SourceTemplateID {
		return traits.StackTemplate{}, fmt.Errorf("%w: source template mismatch", ErrStackTemplateUpgradeInvalid)
	}

	variables, err := service.TemplateRevisions.GetTemplateRevisionVariables(ctx, command.TenantID, command.TargetTemplateRevisionID)
	if err != nil {
		return traits.StackTemplate{}, fmt.Errorf("get target template revision variables: %w", err)
	}
	configJSON := command.ConfigJSON
	if len(configJSON) == 0 {
		configJSON, err = carryForwardConfig(stackTemplate.DesiredConfig(), variables)
		if err != nil {
			return traits.StackTemplate{}, err
		}
	}
	configJSON, err = validateTemplateConfig(configJSON, variables)
	if err != nil {
		return traits.StackTemplate{}, err
	}

	updated, err := service.StackTemplates.UpdateStackTemplateDesiredRevision(ctx, command.TenantID, command.StackTemplateID, targetRevision.ID, configJSON)
	if err != nil {
		return traits.StackTemplate{}, fmt.Errorf("update stack template desired revision: %w", err)
	}
	return updated, nil
}

// creatorMayViewProvisioningStack covers the window between CreateStack
// returning 201 and the owner tuple landing, during which the creator has no
// grant in OpenFGA and would get a 404 on the stack they just made. Because
// there is no inline delivery, this is the only thing covering that window, so
// it is load-bearing rather than cosmetic.
//
// It is deliberately narrow: view only, the creator only, and only while the
// stack is still provisioning. Operating on the stack, adding templates and
// starting runs keep going through OpenFGA, because the stack really is not
// ready. Once the status flips to ready the exception stops applying, so it is
// not a second authorization source in steady state.
// It never turns a denial into a failure: any error reading the stack leaves
// the original denial in place.
func (service *Service) creatorMayViewProvisioningStack(ctx context.Context, command GetStackCommand, denied error) bool {
	if !errors.Is(denied, ErrNotFound) {
		// The check itself failed rather than denying; that is not this
		// exception's business.
		return false
	}
	principal, ok := authn.PrincipalFromContext(ctx)
	if !ok || principal.Subject == "" {
		return false
	}
	stack, err := service.Stacks.GetStack(ctx, command.TenantID, command.StackID)
	if err != nil || stack.Status != traits.StackStatusProvisioning {
		return false
	}
	return string(stack.CreatedBy) == principal.Subject
}

// GetStack returns one tenant-owned stack with installed templates.
func (service *Service) GetStack(ctx context.Context, command GetStackCommand) (StackView, error) {
	if err := validateGetStackCommand(command); err != nil {
		return StackView{}, err
	}
	if err := authorizeStack(ctx, service.Authorizer, command.StackID, authz.RelationCanView, ErrNotFound); err != nil {
		if !service.creatorMayViewProvisioningStack(ctx, command, err) {
			return StackView{}, err
		}
	}

	view, err := service.Stacks.GetStackWithTemplates(ctx, command.TenantID, command.StackID)
	if err != nil {
		return StackView{}, fmt.Errorf("get stack: %w", err)
	}
	if view.Templates == nil {
		view.Templates = []StackTemplateView{}
	}

	if len(view.Templates) > 0 {
		revisions, err := service.TemplateRevisions.ListTemplateRevisions(ctx, command.TenantID)
		if err != nil {
			return StackView{}, fmt.Errorf("list template revisions: %w", err)
		}
		byID := make(map[traits.TemplateRevisionID]traits.TemplateRevision, len(revisions))
		for _, rev := range revisions {
			byID[rev.ID] = rev
		}
		for i := range view.Templates {
			rev, ok := byID[view.Templates[i].DesiredTemplateRevisionID]
			if !ok {
				continue
			}
			view.Templates[i].DisplayName = templateDisplayName(rev.RepoName, rev.RootPath)
			view.Templates[i].SourceRef = rev.SourceRef
		}
	}

	caps, err := ResolveStackCapabilities(ctx, service.Authorizer, command.StackID)
	if err != nil {
		return StackView{}, fmt.Errorf("resolve stack capabilities: %w", err)
	}
	view.Capabilities = caps
	return view, nil
}

// ListStacks returns tenant-owned stacks ordered for UI selection.
func (service *Service) ListStacks(ctx context.Context, command ListStacksCommand) ([]traits.Stack, error) {
	if err := validateListStacksCommand(command); err != nil {
		return nil, err
	}

	stacks, err := listAccessibleStacks(ctx, service.Authorizer, service.Stacks, command.TenantID)
	if err != nil {
		return nil, fmt.Errorf("list stacks: %w", err)
	}
	if stacks == nil {
		return []traits.Stack{}, nil
	}
	return stacks, nil
}

// ListTemplateRevisions returns tenant-owned registered template revisions ordered for UI selection.
func (service *Service) ListTemplateRevisions(ctx context.Context, command ListTemplateRevisionsCommand) ([]traits.TemplateRevision, error) {
	if err := validateListTemplateRevisionsCommand(command); err != nil {
		return nil, err
	}
	if err := requireTemplateCatalogAccess(ctx); err != nil {
		return nil, err
	}

	templateRevisions, err := service.TemplateRevisions.ListTemplateRevisions(ctx, command.TenantID)
	if err != nil {
		return nil, fmt.Errorf("list template revisions: %w", err)
	}
	if templateRevisions == nil {
		return []traits.TemplateRevision{}, nil
	}
	return templateRevisions, nil
}

// GetTemplateRegistration returns one tenant-owned registration attempt.
func (service *Service) GetTemplateRegistration(ctx context.Context, command GetTemplateRegistrationCommand) (traits.TemplateRegistration, error) {
	if err := validateGetTemplateRegistrationCommand(command); err != nil {
		return traits.TemplateRegistration{}, err
	}
	if err := requireTemplateCatalogAccess(ctx); err != nil {
		return traits.TemplateRegistration{}, err
	}

	registration, err := service.TemplateRegistrations.GetTemplateRegistration(ctx, command.TenantID, command.RegistrationID)
	if err != nil {
		return traits.TemplateRegistration{}, fmt.Errorf("get template registration: %w", err)
	}

	return registration, nil
}

// SearchUsers queries the user directory for realm users matching the given criteria.
func (service *Service) SearchUsers(ctx context.Context, command SearchUsersCommand) ([]DirectoryUser, error) {
	principal, ok := authn.PrincipalFromContext(ctx)
	if !ok || principal.Subject == "" {
		return nil, ErrUnauthenticated
	}
	if !isPlatformAdmin(principal) {
		return nil, ErrForbidden
	}
	if err := validateSearchUsersCommand(command); err != nil {
		return nil, err
	}
	if service.UserDirectory == nil {
		return nil, ErrDirectoryUnavailable
	}
	users, err := service.UserDirectory.SearchUsers(ctx, command.Query, command.First, command.Max)
	if err != nil {
		return nil, err
	}
	if users == nil {
		return []DirectoryUser{}, nil
	}
	return users, nil
}

func validateSearchUsersCommand(command SearchUsersCommand) error {
	switch {
	case command.TenantID == "":
		return fmt.Errorf("%w: tenant id is required", ErrInvalidCommand)
	case strings.TrimSpace(command.Query) == "":
		return fmt.Errorf("%w: query is required", ErrInvalidCommand)
	case command.First < 0:
		return fmt.Errorf("%w: first must be non-negative", ErrInvalidCommand)
	case command.Max < 1 || command.Max > 50:
		return fmt.Errorf("%w: max must be between 1 and 50", ErrInvalidCommand)
	default:
		return nil
	}
}

// GetTemplateRevisionVariables returns inferred variables for one tenant-owned template revision.
func (service *Service) GetTemplateRevisionVariables(ctx context.Context, command GetTemplateRevisionVariablesCommand) ([]traits.TemplateVariable, error) {
	if err := validateGetTemplateRevisionVariablesCommand(command); err != nil {
		return nil, err
	}
	if err := requireTemplateCatalogAccess(ctx); err != nil {
		return nil, err
	}

	variables, err := service.TemplateRevisions.GetTemplateRevisionVariables(ctx, command.TenantID, command.TemplateRevisionID)
	if err != nil {
		return nil, fmt.Errorf("get template revision variables: %w", err)
	}
	if variables == nil {
		return []traits.TemplateVariable{}, nil
	}

	return variables, nil
}

func (service *Service) ListStackGrants(ctx context.Context, command ListStackGrantsCommand) (ListStackGrantsResult, error) {
	if _, err := requireAuthorizer(ctx, service.Authorizer); err != nil {
		return ListStackGrantsResult{}, err
	}
	if err := authorizeStack(ctx, service.Authorizer, command.StackID, authz.RelationCanManageAccess, ErrForbidden); err != nil {
		return ListStackGrantsResult{}, err
	}
	stack, err := authz.ObjectFromID(authz.TypeStack, string(command.StackID))
	if err != nil {
		return ListStackGrantsResult{}, fmt.Errorf("list grants stack: %w", err)
	}
	result, err := service.Authorizer.ListGrants(ctx, authz.ListGrantsRequest{Object: stack})
	if err != nil {
		return ListStackGrantsResult{}, err
	}

	grants := make([]GrantView, 0, len(result.Grants))
	for _, grant := range result.Grants {
		userSub := strings.TrimPrefix(grant.Subject().String(), "user:")
		role := grant.Relation().String()
		gv := GrantView{UserSub: userSub, Role: role}

		if service.UserDirectory != nil {
			user, getErr := service.UserDirectory.GetUser(ctx, userSub)
			if getErr == nil && user != nil {
				gv.DisplayName = displayNameFromUser(*user)
				gv.Email = user.Email
			}
		}
		if gv.DisplayName == "" {
			gv.DisplayName = userSub
		}
		grants = append(grants, gv)
	}

	return ListStackGrantsResult{Grants: grants}, nil
}

func displayNameFromUser(user DirectoryUser) string {
	if user.FirstName != "" || user.LastName != "" {
		return strings.TrimSpace(user.FirstName + " " + user.LastName)
	}
	return user.Username
}

// listGrantsForStack reads every direct role assignment on one stack. Structural
// tuples such as parent edges are not grants and never appear in the result.
//
//	stack:abc with alice=owner, bob=viewer
//	  → ListGrantsResult{Grants: [{user:alice, owner}, {user:bob, viewer}]}, nil
//	stack:abc with no grants  → ListGrantsResult{}, nil
func (service *Service) listGrantsForStack(ctx context.Context, object authz.Object) (authz.ListGrantsResult, error) {
	return service.Authorizer.ListGrants(ctx, authz.ListGrantsRequest{Object: object})
}

func (service *Service) AssignStackRole(ctx context.Context, command AssignStackRoleCommand) (GrantView, error) {
	principal, err := requirePrincipal(ctx)
	if err != nil {
		return GrantView{}, err
	}
	if err := authorizeStack(ctx, service.Authorizer, command.StackID, authz.RelationCanManageAccess, ErrForbidden); err != nil {
		return GrantView{}, err
	}

	// Reject an unknown role here rather than letting the handler retry it
	// forever against a payload it can never parse.
	if _, err := authz.GrantRelation(command.Role); err != nil {
		return GrantView{}, fmt.Errorf("%w: %v", ErrInvalidCommand, err)
	}

	if service.Work == nil {
		return GrantView{}, fmt.Errorf("%w: unit of work not configured", authz.ErrUnavailable)
	}

	if service.UserDirectory != nil {
		user, getErr := service.UserDirectory.GetUser(ctx, command.UserSub)
		if getErr != nil || user == nil {
			return GrantView{}, fmt.Errorf("%w: target user not found in directory", ErrInvalidCommand)
		}
	}

	stack, err := authz.ObjectFromID(authz.TypeStack, string(command.StackID))
	if err != nil {
		return GrantView{}, fmt.Errorf("assign role stack: %w", err)
	}
	subject, err := authz.SubjectFromOIDCSub(command.UserSub)
	if err != nil {
		return GrantView{}, fmt.Errorf("%w: invalid user sub", ErrInvalidCommand)
	}

	currentGrants, err := service.listGrantsForStack(ctx, stack)
	if err != nil {
		return GrantView{}, err
	}

	var currentRole string
	for _, g := range currentGrants.Grants {
		if g.Subject().String() == subject.String() {
			currentRole = g.Relation().String()
			break
		}
	}

	if currentRole == "owner" && command.Role != "owner" {
		ownerCount := 0
		for _, g := range currentGrants.Grants {
			if g.Relation() == authz.RelationOwner {
				ownerCount++
			}
		}
		if ownerCount == 1 {
			return GrantView{}, fmt.Errorf("%w: assign another owner before changing this role", ErrLastOwner)
		}
	}

	// The old delete-then-write pair is gone. The handler converges to the
	// desired role, so there is no intermediate window in which a crash leaves
	// the user holding no role at all.
	payload, err := json.Marshal(authz.GrantPayload{
		StackID: string(command.StackID),
		Subject: command.UserSub,
		Role:    command.Role,
	})
	if err != nil {
		return GrantView{}, fmt.Errorf("encode grant payload: %w", err)
	}

	if err := service.Work.InTx(ctx, func(repository TxRepo, enqueuer queue.Enqueuer) error {
		if err := repository.AppendAuditEvent(ctx, traits.SecurityAuditEvent{
			ActorSubject: principal.Subject,
			Action:       traits.AuditActionGrant,
			TargetUser:   command.UserSub,
			TenantID:     command.TenantID,
			StackID:      command.StackID,
			OldRole:      currentRole,
			NewRole:      command.Role,
			Outcome:      traits.AuditOutcomeSuccess,
		}); err != nil {
			return err
		}
		return enqueuer.Enqueue(ctx, queue.Request{
			Kind:         authz.KindReconcileStackGrant,
			Payload:      payload,
			ActorSubject: principal.Subject,
			TenantID:     string(command.TenantID),
		})
	}); err != nil {
		return GrantView{}, fmt.Errorf("assign stack role: %w", err)
	}

	return GrantView{UserSub: command.UserSub, Role: command.Role}, nil
}

func (service *Service) RevokeStackRole(ctx context.Context, command RevokeStackRoleCommand) error {
	principal, err := requirePrincipal(ctx)
	if err != nil {
		return err
	}
	if err := authorizeStack(ctx, service.Authorizer, command.StackID, authz.RelationCanManageAccess, ErrForbidden); err != nil {
		return err
	}

	stack, err := authz.ObjectFromID(authz.TypeStack, string(command.StackID))
	if err != nil {
		return fmt.Errorf("revoke role stack: %w", err)
	}
	if service.Work == nil {
		return fmt.Errorf("%w: unit of work not configured", authz.ErrUnavailable)
	}
	subject, err := authz.SubjectFromOIDCSub(command.UserSub)
	if err != nil {
		return fmt.Errorf("%w: invalid user sub", ErrInvalidCommand)
	}

	currentGrants, err := service.listGrantsForStack(ctx, stack)
	if err != nil {
		return err
	}

	var targetRole string
	ownerCount := 0
	for _, g := range currentGrants.Grants {
		if g.Relation() == authz.RelationOwner {
			ownerCount++
		}
		if g.Subject().String() == subject.String() {
			targetRole = g.Relation().String()
		}
	}

	if targetRole == "owner" && ownerCount == 1 {
		return fmt.Errorf("%w: cannot remove the last owner; assign another owner first", ErrLastOwner)
	}

	// An empty desired role means "no access"; the handler removes whatever the
	// subject currently holds on this stack.
	payload, err := json.Marshal(authz.GrantPayload{
		StackID: string(command.StackID),
		Subject: command.UserSub,
		Role:    "",
	})
	if err != nil {
		return fmt.Errorf("encode revoke payload: %w", err)
	}

	if err := service.Work.InTx(ctx, func(repository TxRepo, enqueuer queue.Enqueuer) error {
		if err := repository.AppendAuditEvent(ctx, traits.SecurityAuditEvent{
			ActorSubject: principal.Subject,
			Action:       traits.AuditActionRevoke,
			TargetUser:   command.UserSub,
			TenantID:     command.TenantID,
			StackID:      command.StackID,
			OldRole:      targetRole,
			Outcome:      traits.AuditOutcomeSuccess,
		}); err != nil {
			return err
		}
		return enqueuer.Enqueue(ctx, queue.Request{
			Kind:         authz.KindReconcileStackGrant,
			Payload:      payload,
			ActorSubject: principal.Subject,
			TenantID:     string(command.TenantID),
		})
	}); err != nil {
		return fmt.Errorf("revoke stack role: %w", err)
	}

	return nil
}

// ApproveRun records an approval decision and signals the waiting workflow.
func (service *Service) ApproveRun(ctx context.Context, command ApproveRunCommand) error {
	actor, err := authenticatedActor(ctx)
	if err != nil {
		return err
	}
	if err := validateApproveRunCommand(command); err != nil {
		return err
	}

	_, err = service.authorizedTemplateRun(ctx, command.TenantID, command.RunID, authz.RelationCanApprove, ErrForbidden)
	if err != nil {
		return err
	}

	approval := traits.TemplateRunApproval{
		RunID:      command.RunID,
		TenantID:   command.TenantID,
		ApprovedBy: actor,
		ApprovedAt: service.Clock.Now(),
	}

	signal := traits.ApprovalSignal{ApprovedBy: actor}
	payload, err := json.Marshal(SignalRunApprovalPayload{
		TenantID: command.TenantID,
		RunID:    command.RunID,
		Signal:   signal,
	})
	if err != nil {
		return fmt.Errorf("encode run approval payload: %w", err)
	}
	auditEvent := traits.SecurityAuditEvent{
		ActorSubject:  string(actor),
		Action:        traits.AuditActionApprovalGranted,
		TenantID:      command.TenantID,
		Outcome:       traits.AuditOutcomeSuccess,
		CorrelationID: "",
	}
	if err := service.Work.InTx(ctx, func(repository TxRepo, enqueuer queue.Enqueuer) error {
		if err := repository.ApproveTemplateRun(ctx, approval); err != nil {
			return err
		}
		if err := repository.AppendAuditEvent(ctx, auditEvent); err != nil {
			return err
		}
		return enqueuer.Enqueue(ctx, queue.Request{
			Kind:         KindSignalRunApproval,
			Payload:      payload,
			ActorSubject: string(actor),
			TenantID:     string(command.TenantID),
		})
	}); err != nil {
		return fmt.Errorf("approve template run: %w", err)
	}

	return nil
}

// CancelRun records a cancellation request and signals the running workflow.
func (service *Service) CancelRun(ctx context.Context, command CancelRunCommand) error {
	actor, err := authenticatedActor(ctx)
	if err != nil {
		return err
	}
	if err := validateCancelRunCommand(command); err != nil {
		return err
	}

	if _, err := service.authorizedTemplateRun(ctx, command.TenantID, command.RunID, authz.RelationCanOperate, ErrForbidden); err != nil {
		return err
	}
	cancellation := traits.TemplateRunCancellation{
		RunID:       command.RunID,
		TenantID:    command.TenantID,
		RequestedBy: actor,
		Reason:      command.Reason,
		RequestedAt: service.Clock.Now(),
	}

	signal := traits.CancelSignal{
		RequestedBy: actor,
		Reason:      command.Reason,
	}
	payload, err := json.Marshal(SignalRunCancellationPayload{
		TenantID: command.TenantID,
		RunID:    command.RunID,
		Signal:   signal,
	})
	if err != nil {
		return fmt.Errorf("encode run cancellation payload: %w", err)
	}
	if err := service.Work.InTx(ctx, func(repository TxRepo, enqueuer queue.Enqueuer) error {
		if err := repository.RequestTemplateRunCancellation(ctx, cancellation); err != nil {
			return err
		}
		return enqueuer.Enqueue(ctx, queue.Request{
			Kind:         KindSignalRunCancellation,
			Payload:      payload,
			ActorSubject: string(actor),
			TenantID:     string(command.TenantID),
		})
	}); err != nil {
		return fmt.Errorf("request template run cancellation: %w", err)
	}

	return nil
}

// GetTemplateRun returns one tenant-owned run.
func (service *Service) GetTemplateRun(ctx context.Context, command GetTemplateRunCommand) (traits.TemplateRun, error) {
	if err := validateGetTemplateRunCommand(command); err != nil {
		return traits.TemplateRun{}, err
	}

	run, err := service.authorizedTemplateRun(ctx, command.TenantID, command.RunID, authz.RelationCanView, ErrNotFound)
	if err != nil {
		return traits.TemplateRun{}, fmt.Errorf("get template run: %w", err)
	}

	return run, nil
}

// ListTemplateRuns returns tenant-owned runs for one stack template, most recent first,
// visible to anyone who can view the owning stack.
func (service *Service) ListTemplateRuns(ctx context.Context, command ListTemplateRunsCommand) ([]traits.TemplateRun, error) {
	if err := validateListTemplateRunsCommand(command); err != nil {
		return nil, err
	}

	if _, err := service.authorizedStackTemplate(ctx, command.TenantID, command.StackTemplateID, authz.RelationCanView, ErrNotFound); err != nil {
		return nil, fmt.Errorf("list template runs: %w", err)
	}

	runs, err := service.TemplateRuns.ListTemplateRuns(ctx, command.TenantID, command.StackTemplateID)
	if err != nil {
		return nil, fmt.Errorf("list template runs: %w", err)
	}
	if runs == nil {
		return []traits.TemplateRun{}, nil
	}

	return runs, nil
}

// GetTemplateRunLog returns one phase log after checking that the run belongs to the tenant.
func (service *Service) GetTemplateRunLog(ctx context.Context, command GetTemplateRunLogCommand) ([]byte, error) {
	if err := validateGetTemplateRunLogCommand(command); err != nil {
		return nil, err
	}

	if _, err := service.authorizedTemplateRun(ctx, command.TenantID, command.RunID, authz.RelationCanView, ErrNotFound); err != nil {
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

	if _, err := service.authorizedTemplateRun(ctx, command.TenantID, command.RunID, authz.RelationCanView, ErrNotFound); err != nil {
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
	default:
		return nil
	}
}

// validateCreateCredentialCommand checks required IDs, scope, variable name, and value before encryption.
func validateCreateCredentialCommand(command CreateCredentialCommand) error {
	switch {
	case command.TenantID == "":
		return fmt.Errorf("%w: tenant id is required", ErrInvalidCommand)
	case !validCredentialName(command.Name):
		return ErrCredentialNameInvalid
	case command.Value == "":
		return fmt.Errorf("%w: credential value is required", ErrInvalidCommand)
	case command.Scope == traits.CredentialScopeStack && command.StackID == "":
		return fmt.Errorf("%w: stack id is required", ErrInvalidCommand)
	case command.Scope == traits.CredentialScopeStackTemplate && command.StackTemplateID == "":
		return fmt.Errorf("%w: stack template id is required", ErrInvalidCommand)
	case command.Scope != traits.CredentialScopeStack && command.Scope != traits.CredentialScopeStackTemplate:
		return fmt.Errorf("%w: credential scope is invalid", ErrInvalidCommand)
	default:
		return nil
	}
}

// validateListCredentialsCommand checks the owner required to list one credential scope.
func validateListCredentialsCommand(command ListCredentialsCommand) error {
	if command.TenantID == "" {
		return fmt.Errorf("%w: tenant id is required", ErrInvalidCommand)
	}
	if command.Scope == traits.CredentialScopeStack && command.StackID == "" {
		return fmt.Errorf("%w: stack id is required", ErrInvalidCommand)
	}
	if command.Scope == traits.CredentialScopeStackTemplate && command.StackTemplateID == "" {
		return fmt.Errorf("%w: stack template id is required", ErrInvalidCommand)
	}
	if command.Scope != traits.CredentialScopeStack && command.Scope != traits.CredentialScopeStackTemplate {
		return fmt.Errorf("%w: credential scope is invalid", ErrInvalidCommand)
	}
	return nil
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

func validateListTemplateRevisionsCommand(command ListTemplateRevisionsCommand) error {
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
	case command.TemplateRevisionID == "":
		return fmt.Errorf("%w: template revision id is required", ErrInvalidCommand)
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
	default:
		return nil
	}
}

func validateUpdateStackTemplateConfigCommand(command UpdateStackTemplateConfigCommand) error {
	switch {
	case command.TenantID == "":
		return fmt.Errorf("%w: tenant id is required", ErrInvalidCommand)
	case command.StackTemplateID == "":
		return fmt.Errorf("%w: stack template id is required", ErrInvalidCommand)
	case len(command.ConfigJSON) == 0:
		return fmt.Errorf("%w: config is required", ErrInvalidCommand)
	default:
		return nil
	}
}

func validateUpgradeStackTemplateCommand(command UpgradeStackTemplateCommand) error {
	switch {
	case command.TenantID == "":
		return fmt.Errorf("%w: tenant id is required", ErrInvalidCommand)
	case command.StackTemplateID == "":
		return fmt.Errorf("%w: stack template id is required", ErrInvalidCommand)
	case command.TargetTemplateRevisionID == "":
		return fmt.Errorf("%w: target template revision id is required", ErrInvalidCommand)
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

func validateGetTemplateRevisionVariablesCommand(command GetTemplateRevisionVariablesCommand) error {
	switch {
	case command.TenantID == "":
		return fmt.Errorf("%w: tenant id is required", ErrInvalidCommand)
	case command.TemplateRevisionID == "":
		return fmt.Errorf("%w: template revision id is required", ErrInvalidCommand)
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

func validateListTemplateRunsCommand(command ListTemplateRunsCommand) error {
	switch {
	case command.TenantID == "":
		return fmt.Errorf("%w: tenant id is required", ErrInvalidCommand)
	case command.StackTemplateID == "":
		return fmt.Errorf("%w: stack template id is required", ErrInvalidCommand)
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

// carryForwardConfig keeps the values of variables the target revision still
// declares. It reads desired config only: the install-time config used to be a
// fallback here, but desired_config_json cannot be empty on a persisted row, so
// that branch was unreachable and only made it look as though install-time
// config were a second source of desired state.
func carryForwardConfig(desiredConfigJSON json.RawMessage, variables []traits.TemplateVariable) (json.RawMessage, error) {
	raw := desiredConfigJSON
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}

	var current map[string]json.RawMessage
	if err := json.Unmarshal(raw, &current); err != nil {
		return nil, fmt.Errorf("%w: current config must be a JSON object", ErrStackTemplateConfigInvalid)
	}
	if current == nil {
		current = map[string]json.RawMessage{}
	}

	carried := make(map[string]json.RawMessage)
	for _, variable := range variables {
		value, ok := current[variable.Name]
		if ok {
			carried[variable.Name] = value
		}
	}

	normalized, err := json.Marshal(carried)
	if err != nil {
		return nil, fmt.Errorf("marshal carried stack template config: %w", err)
	}
	return normalized, nil
}

func componentKey(value string, stackTemplateID traits.StackTemplateID) string {
	value = strings.TrimSpace(value)
	if value != "" {
		return value
	}
	return string(stackTemplateID)
}

func templateDisplayName(repoName, rootPath string) string {
	if rootPath == "" || rootPath == "." {
		return strings.TrimSpace(repoName)
	}
	return strings.TrimSpace(repoName) + "/" + filepath.Clean(rootPath)
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

// validCredentialName accepts Terraform/provider-style environment variable names.
func validCredentialName(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 256 {
		return false
	}
	for index, char := range name {
		if (char >= 'A' && char <= 'Z') || (char >= 'a' && char <= 'z') || (index > 0 && char >= '0' && char <= '9') || char == '_' {
			continue
		}
		return false
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

type randomCredentialSetIDGenerator struct{}

// NewCredentialSetID creates a random identifier for a persisted credential record.
func (randomCredentialSetIDGenerator) NewCredentialSetID() traits.CredentialSetID {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return traits.CredentialSetID(fmt.Sprintf("credential_%d", time.Now().UTC().UnixNano()))
	}
	return traits.CredentialSetID("credential_" + hex.EncodeToString(bytes[:]))
}
