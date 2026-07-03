// Package traits contains shared product and workflow traits.
package traits

import (
	"encoding/json"
	"time"
)

// ID is the common storage shape for platform identifiers.
type ID string

// Valid reports whether the ID is non-empty.
func (id ID) Valid() bool {
	return id != ""
}

type (
	TenantID        ID
	UserID          ID
	TemplateID      ID
	StackID         ID
	StackTemplateID ID
	TemplateRunID   ID
	StackRunID      ID
	CredentialSetID ID
)

// OperationType identifies a Terraform operation supported by the platform.
type OperationType string

const (
	OperationPlan    OperationType = "plan"
	OperationApply   OperationType = "apply"
	OperationDestroy OperationType = "destroy"
)

// Valid reports whether the operation is one of the supported operation types.
func (operation OperationType) Valid() bool {
	switch operation {
	case OperationPlan, OperationApply, OperationDestroy:
		return true
	default:
		return false
	}
}

// TemplateStatus identifies template registration and validation state.
type TemplateStatus string

const (
	TemplatePendingValidation TemplateStatus = "pending_validation"
	TemplateValidating        TemplateStatus = "validating"
	TemplateActive            TemplateStatus = "active"
	TemplateInvalid           TemplateStatus = "invalid"
)

// Valid reports whether the status is one of the supported template states.
func (status TemplateStatus) Valid() bool {
	switch status {
	case TemplatePendingValidation, TemplateValidating, TemplateActive, TemplateInvalid:
		return true
	default:
		return false
	}
}

// StackTemplateLifecycle identifies whether an installed template can run.
type StackTemplateLifecycle string

const (
	StackTemplateActive     StackTemplateLifecycle = "active"
	StackTemplateDestroying StackTemplateLifecycle = "destroying"
	StackTemplateDestroyed  StackTemplateLifecycle = "destroyed"
	StackTemplateFailed     StackTemplateLifecycle = "failed"
	StackTemplateOrphaned   StackTemplateLifecycle = "orphaned"
)

// Valid reports whether the lifecycle is one of the supported states.
func (lifecycle StackTemplateLifecycle) Valid() bool {
	switch lifecycle {
	case StackTemplateActive, StackTemplateDestroying, StackTemplateDestroyed, StackTemplateFailed, StackTemplateOrphaned:
		return true
	default:
		return false
	}
}

// TemplateRunStatus identifies the lifecycle phase of a TemplateRun.
type TemplateRunStatus string

const (
	TemplateRunQueued            TemplateRunStatus = "queued"
	TemplateRunLocked            TemplateRunStatus = "locked"
	TemplateRunWorkspacePrepared TemplateRunStatus = "workspace_prepared"
	TemplateRunSourceFetched     TemplateRunStatus = "source_fetched"
	TemplateRunInit              TemplateRunStatus = "init"
	TemplateRunWorkspaceSelected TemplateRunStatus = "workspace_selected"
	TemplateRunPlanned           TemplateRunStatus = "planned"
	TemplateRunWaitingApproval   TemplateRunStatus = "waiting_approval"
	TemplateRunApproved          TemplateRunStatus = "approved"
	TemplateRunApplyStarted      TemplateRunStatus = "apply_started"
	TemplateRunApplied           TemplateRunStatus = "applied"
	TemplateRunDestroyStarted    TemplateRunStatus = "destroy_started"
	TemplateRunDestroyed         TemplateRunStatus = "destroyed"
	TemplateRunCancelRequested   TemplateRunStatus = "cancel_requested"
	TemplateRunCanceling         TemplateRunStatus = "canceling"
	TemplateRunCanceled          TemplateRunStatus = "canceled"
	TemplateRunLockReleased      TemplateRunStatus = "lock_released"
	TemplateRunCompleted         TemplateRunStatus = "completed"
	TemplateRunFailed            TemplateRunStatus = "failed"
)

// Valid reports whether the status is one of the supported run states.
func (status TemplateRunStatus) Valid() bool {
	switch status {
	case TemplateRunQueued,
		TemplateRunLocked,
		TemplateRunWorkspacePrepared,
		TemplateRunSourceFetched,
		TemplateRunInit,
		TemplateRunWorkspaceSelected,
		TemplateRunPlanned,
		TemplateRunWaitingApproval,
		TemplateRunApproved,
		TemplateRunApplyStarted,
		TemplateRunApplied,
		TemplateRunDestroyStarted,
		TemplateRunDestroyed,
		TemplateRunCancelRequested,
		TemplateRunCanceling,
		TemplateRunCanceled,
		TemplateRunLockReleased,
		TemplateRunCompleted,
		TemplateRunFailed:
		return true
	default:
		return false
	}
}

// Terminal reports whether a run status represents no further workflow work.
func (status TemplateRunStatus) Terminal() bool {
	switch status {
	case TemplateRunCompleted, TemplateRunFailed, TemplateRunCanceled:
		return true
	default:
		return false
	}
}

// Tenant is an account boundary for product records.
type Tenant struct {
	ID   TenantID
	Name string
}

// Template is a GitHub-sourced Terraform template.
// Template contains github ref of the template
type Template struct {
	ID                   TemplateID
	TenantID             TenantID
	GitHubInstallationID int64
	RepoOwner            string
	RepoName             string
	DefaultRef           string
	RootPath             string
	Name                 string
	Description          string
	Tags                 []string
	Status               TemplateStatus
}

// TemplateVariable is inferred from Terraform root module variables.
// TemplateVariable is the template variable inferred from root terraform module variables
type TemplateVariable struct {
	TemplateID     TemplateID
	Name           string
	TypeExpression string
	Description    string
	Required       bool
	HasDefault     bool
	Sensitive      bool
	HasValidation  bool
}

// Stack is a logical infrastructure composition.
// stack is logical infrastructure composition
type Stack struct {
	ID                   StackID
	TenantID             TenantID
	Name                 string
	Slug                 string
	Tags                 map[string]string
	DefaultCredentialIDs []CredentialSetID
}

// StackTemplate is one template installed into one stack.
// stacktemplate is one template installed into one stack
type StackTemplate struct {
	ID               StackTemplateID
	StackID          StackID
	TemplateID       TemplateID
	SelectedRef      string
	WorkspaceName    string
	ConfigJSON       json.RawMessage
	LastAppliedRunID TemplateRunID
	LastAppliedRef   string
	LastAppliedAt    time.Time
	Lifecycle        StackTemplateLifecycle
}

// TemplateRun is one Terraform operation against a StackTemplate.
// TemplateRun is one terraform operation against a StackTemplate
type TemplateRun struct {
	ID                TemplateRunID     `json:"id"`
	TenantID          TenantID          `json:"tenant_id"`
	StackTemplateID   StackTemplateID   `json:"stack_template_id"`
	Operation         OperationType     `json:"operation"`
	SelectedRef       string            `json:"selected_ref"`
	ResolvedCommitSHA string            `json:"resolved_commit_sha"`
	WorkspaceName     string            `json:"workspace_name"`
	BackendType       string            `json:"backend_type"`
	BackendConfigHash string            `json:"backend_config_hash"`
	Status            TemplateRunStatus `json:"status"`
	TriggerActor      UserID            `json:"trigger_actor"`
	StartedAt         time.Time         `json:"started_at"`
	CompletedAt       time.Time         `json:"completed_at,omitempty"`
	ErrorSummary      string            `json:"error_summary"`
}

// TemplateRunApproval records who approved a waiting run.
// TemplateRunApproval records who approved a waiting run.
type TemplateRunApproval struct {
	RunID      TemplateRunID
	TenantID   TenantID
	ApprovedBy UserID
	ApprovedAt time.Time
}

// TemplateRunCancellation records who requested a run cancellation.
// TemplateRunCancellation records who requested a run cancellation.
type TemplateRunCancellation struct {
	RunID       TemplateRunID
	TenantID    TenantID
	RequestedBy UserID
	Reason      string
	RequestedAt time.Time
}

// StackRun represents a coordinated multi-template operation.
// StackRun represents a coordinated multi-template operation.
type StackRun struct {
	ID        StackRunID
	TenantID  TenantID
	StackID   StackID
	Operation OperationType
	Status    TemplateRunStatus
}

// CredentialSet is a reference to runtime execution credentials.
type CredentialSet struct {
	ID             CredentialSetID
	TenantID       TenantID
	Name           string
	ProviderType   string
	SecretStoreRef string
}

const (
	TemplateRunWorkflowName  = "TemplateRunWorkflow"
	TemplateSyncWorkflowName = "TemplateSyncWorkflow"
	StackRunWorkflowName     = "StackRunWorkflow"

	ApprovalSignalName = "approval"
	CancelSignalName   = "cancel"

	RecordTemplateRunStatusActivityName = "RecordTemplateRunStatus"
)

// TemplateRunWorkflowInput starts one Terraform operation for one StackTemplate.
type TemplateRunWorkflowInput struct {
	RunID           TemplateRunID
	TenantID        TenantID
	StackTemplateID StackTemplateID
	Operation       OperationType
	SelectedRef     string
	WorkspaceName   string
}

// TemplateRunStatusActivityInput asks the worker to persist one run status transition.
type TemplateRunStatusActivityInput struct {
	RunID           TemplateRunID
	TenantID        TenantID
	StackTemplateID StackTemplateID
	Operation       OperationType
	Status          TemplateRunStatus
}

// TemplateSyncWorkflowInput starts template metadata sync for a GitHub template.
type TemplateSyncWorkflowInput struct {
	TemplateID           TemplateID
	TenantID             TenantID
	RepoOwner            string
	RepoName             string
	SelectedRef          string
	RootPath             string
	GitHubInstallationID int64
}

// ApprovalSignal records an approval actor for a waiting apply run.
type ApprovalSignal struct {
	ApprovedBy UserID
}

// CancelSignal records a cancel actor and reason for a running workflow.
type CancelSignal struct {
	RequestedBy UserID
	Reason      string
}
