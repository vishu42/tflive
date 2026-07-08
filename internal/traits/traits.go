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
	TenantID               ID
	UserID                 ID
	SourceTemplateID       ID
	TemplateRevisionID     ID
	TemplateRegistrationID ID
	StackID                ID
	StackTemplateID        ID
	TemplateRunID          ID
	StackRunID             ID
	CredentialSetID        ID
)

// OperationType identifies a Terraform operation supported by the platform.
type OperationType string

const (
	OperationPlan    OperationType = "plan"
	OperationApply   OperationType = "apply"
	OperationDestroy OperationType = "destroy"
)

// TerraformCommandType identifies one Terraform subprocess command run by a worker.
type TerraformCommandType string

const (
	TerraformCommandInit            TerraformCommandType = "init"
	TerraformCommandSelectWorkspace TerraformCommandType = "select_workspace"
	TerraformCommandPlan            TerraformCommandType = "plan"
	TerraformCommandApply           TerraformCommandType = "apply"
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

// TemplateRevisionStatus identifies template revision validation state.
type TemplateRevisionStatus string

const (
	TemplateRevisionPendingValidation TemplateRevisionStatus = "pending_validation"
	TemplateRevisionValidating        TemplateRevisionStatus = "validating"
	TemplateRevisionActive            TemplateRevisionStatus = "active"
	TemplateRevisionInvalid           TemplateRevisionStatus = "invalid"
)

// Valid reports whether the status is one of the supported template revision states.
func (status TemplateRevisionStatus) Valid() bool {
	switch status {
	case TemplateRevisionPendingValidation, TemplateRevisionValidating, TemplateRevisionActive, TemplateRevisionInvalid:
		return true
	default:
		return false
	}
}

// TemplateRegistrationStatus identifies the lifecycle of one template registration request.
type TemplateRegistrationStatus string

const (
	TemplateRegistrationPending   TemplateRegistrationStatus = "pending"
	TemplateRegistrationRunning   TemplateRegistrationStatus = "running"
	TemplateRegistrationCompleted TemplateRegistrationStatus = "completed"
	TemplateRegistrationInvalid   TemplateRegistrationStatus = "invalid"
	TemplateRegistrationFailed    TemplateRegistrationStatus = "failed"
)

// Valid reports whether the status is one of the supported registration states.
func (status TemplateRegistrationStatus) Valid() bool {
	switch status {
	case TemplateRegistrationPending,
		TemplateRegistrationRunning,
		TemplateRegistrationCompleted,
		TemplateRegistrationInvalid,
		TemplateRegistrationFailed:
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

// SourceTemplate is the stable logical identity tracked by template revisions.
type SourceTemplate struct {
	ID                       SourceTemplateID   `json:"id"`
	TenantID                 TenantID           `json:"tenant_id"`
	RepoOwner                string             `json:"repo_owner"`
	RepoName                 string             `json:"repo_name"`
	SourceRef                string             `json:"source_ref"`
	RootPath                 string             `json:"root_path"`
	LatestTemplateRevisionID TemplateRevisionID `json:"latest_template_revision_id"`
	CreatedAt                time.Time          `json:"created_at"`
	UpdatedAt                time.Time          `json:"updated_at"`
}

// TemplateRevision is one resolved GitHub-sourced Terraform template revision.
type TemplateRevision struct {
	ID                TemplateRevisionID     `json:"id"`
	TenantID          TenantID               `json:"tenant_id"`
	SourceTemplateID  SourceTemplateID       `json:"source_template_id"`
	RepoOwner         string                 `json:"repo_owner"`
	RepoName          string                 `json:"repo_name"`
	SourceRef         string                 `json:"source_ref"`
	ResolvedCommitSHA string                 `json:"resolved_commit_sha"`
	RootPath          string                 `json:"root_path"`
	Name              string                 `json:"name"`
	Description       string                 `json:"description"`
	Tags              []string               `json:"tags"`
	Status            TemplateRevisionStatus `json:"status"`
	CreatedAt         time.Time              `json:"created_at"`
}

// TemplateRegistration records one async template registration request.
type TemplateRegistration struct {
	ID                 TemplateRegistrationID     `json:"id"`
	TenantID           TenantID                   `json:"tenant_id"`
	RepoOwner          string                     `json:"repo_owner"`
	RepoName           string                     `json:"repo_name"`
	SourceRef          string                     `json:"source_ref"`
	RootPath           string                     `json:"root_path"`
	Status             TemplateRegistrationStatus `json:"status"`
	TemplateRevisionID TemplateRevisionID         `json:"template_revision_id"`
	ResolvedCommitSHA  string                     `json:"resolved_commit_sha"`
	RequestedBy        UserID                     `json:"requested_by"`
	RequestedAt        time.Time                  `json:"requested_at"`
	CompletedAt        time.Time                  `json:"completed_at,omitempty"`
	ErrorSummary       string                     `json:"error_summary"`
}

// TemplateVariable is inferred from Terraform root module variables.
type TemplateVariable struct {
	TemplateRevisionID TemplateRevisionID `json:"template_revision_id"`
	Name               string             `json:"name"`
	TypeExpression     string             `json:"type_expression"`
	Description        string             `json:"description"`
	Required           bool               `json:"required"`
	HasDefault         bool               `json:"has_default"`
	Sensitive          bool               `json:"sensitive"`
	HasValidation      bool               `json:"has_validation"`
}

// Stack is a logical infrastructure composition.
type Stack struct {
	ID                   StackID           `json:"id"`
	TenantID             TenantID          `json:"tenant_id"`
	Name                 string            `json:"name"`
	Slug                 string            `json:"slug"`
	Tags                 map[string]string `json:"tags"`
	DefaultCredentialIDs []CredentialSetID `json:"default_credential_ids"`
	CreatedBy            UserID            `json:"created_by"`
	CreatedAt            time.Time         `json:"created_at"`
}

// StackTemplate is one long-lived template install/component instance in a stack.
type StackTemplate struct {
	// ID uniquely identifies this installed component instance.
	ID StackTemplateID `json:"id"`
	// TenantID scopes the install to one tenant.
	TenantID TenantID `json:"tenant_id"`
	// StackID is the stack this component is installed into.
	StackID StackID `json:"stack_id"`
	// TemplateRevisionID is the install-time template revision.
	TemplateRevisionID TemplateRevisionID `json:"template_revision_id"`
	// ComponentKey is the human/stable key unique among active installs in a stack.
	ComponentKey string `json:"component_key"`
	// SourceTemplateID is the stable source identity shared by all revisions.
	SourceTemplateID SourceTemplateID `json:"source_template_id"`
	// DesiredTemplateRevisionID is the template revision to use for the next run.
	DesiredTemplateRevisionID TemplateRevisionID `json:"desired_template_revision_id"`
	// LastAppliedTemplateRevisionID is the template revision from the last successful apply.
	LastAppliedTemplateRevisionID TemplateRevisionID `json:"last_applied_template_revision_id"`
	// SelectedRef records the source ref selected when the component was installed.
	SelectedRef string `json:"selected_ref"`
	// WorkspaceName is the Terraform workspace used for this component.
	WorkspaceName string `json:"workspace_name"`
	// ConfigJSON is the install-time config and legacy fallback for desired config.
	ConfigJSON json.RawMessage `json:"config_json"`
	// DesiredConfigJSON is the config to snapshot into the next run.
	DesiredConfigJSON json.RawMessage `json:"desired_config_json"`
	// LastAppliedRunID is the run that last applied this component successfully.
	LastAppliedRunID TemplateRunID `json:"last_applied_run_id"`
	// LastAppliedRef is the source ref recorded by the last successful apply.
	LastAppliedRef string `json:"last_applied_ref"`
	// LastAppliedAt is when the last successful apply completed.
	LastAppliedAt time.Time `json:"last_applied_at,omitempty"`
	// CreatedBy is the user that installed this component.
	CreatedBy UserID `json:"created_by"`
	// Lifecycle determines whether this component can run or is being removed.
	Lifecycle StackTemplateLifecycle `json:"lifecycle"`
}

// TemplateRun is one Terraform operation against a StackTemplate.
// TemplateRun is one terraform operation against a StackTemplate
type TemplateRun struct {
	ID                 TemplateRunID      `json:"id"`
	TenantID           TenantID           `json:"tenant_id"`
	StackTemplateID    StackTemplateID    `json:"stack_template_id"`
	TemplateRevisionID TemplateRevisionID `json:"template_revision_id"`
	SourceTemplateID   SourceTemplateID   `json:"source_template_id"`
	Operation          OperationType      `json:"operation"`
	SelectedRef        string             `json:"selected_ref"`
	ResolvedCommitSHA  string             `json:"resolved_commit_sha"`
	WorkspaceName      string             `json:"workspace_name"`
	ConfigJSON         json.RawMessage    `json:"config_json"`
	BackendType        string             `json:"backend_type"`
	BackendConfigHash  string             `json:"backend_config_hash"`
	Status             TemplateRunStatus  `json:"status"`
	TriggerActor       UserID             `json:"trigger_actor"`
	StartedAt          time.Time          `json:"started_at"`
	CompletedAt        time.Time          `json:"completed_at,omitempty"`
	ErrorSummary       string             `json:"error_summary"`
}

// TemplateRunLog records the object-store location for one run phase log.
type TemplateRunLog struct {
	TenantID    TenantID      `json:"tenant_id"`
	RunID       TemplateRunID `json:"run_id"`
	Phase       string        `json:"phase"`
	ObjectKey   string        `json:"object_key"`
	ContentType string        `json:"content_type"`
	SizeBytes   int64         `json:"size_bytes"`
	UploadedAt  time.Time     `json:"uploaded_at"`
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

	RecordTemplateRunStatusActivityName          = "RecordTemplateRunStatus"
	RecordTemplateRegistrationStatusActivityName = "RecordTemplateRegistrationStatus"
	PrepareWorkspaceActivityName                 = "PrepareWorkspace"
	FetchSourceActivityName                      = "FetchSource"
	RunTerraformActivityName                     = "RunTerraform"
	SyncTemplateActivityName                     = "SyncTemplate"
)

// TemplateRunWorkflowInput starts one Terraform operation for one StackTemplate.
type TemplateRunWorkflowInput struct {
	RunID           TemplateRunID
	TenantID        TenantID
	StackTemplateID StackTemplateID
	Operation       OperationType
	SelectedRef     string
	WorkspaceName   string
	RepoOwner       string
	RepoName        string
	RootPath        string
}

// TemplateRunStatusActivityInput asks the worker to persist one run status transition.
type TemplateRunStatusActivityInput struct {
	RunID           TemplateRunID
	TenantID        TenantID
	StackTemplateID StackTemplateID
	Operation       OperationType
	Status          TemplateRunStatus
}

// PrepareWorkspaceActivityInput asks the worker to create a local run workspace.
type PrepareWorkspaceActivityInput struct {
	RunID    TemplateRunID
	TenantID TenantID
}

// PrepareWorkspaceActivityOutput identifies the prepared local run workspace.
type PrepareWorkspaceActivityOutput struct {
	WorkspacePath string
}

// FetchSourceActivityInput asks the worker to clone a template source into a prepared run workspace.
type FetchSourceActivityInput struct {
	RunID         TemplateRunID
	TenantID      TenantID
	WorkspacePath string
	RepoOwner     string
	RepoName      string
	SourceRef     string
	RootPath      string
}

// FetchSourceActivityOutput identifies the Terraform module directory within the cloned source.
type FetchSourceActivityOutput struct {
	TerraformPath string
}

// RunTerraformActivityInput asks the worker to run one Terraform subprocess command.
type RunTerraformActivityInput struct {
	RunID         TemplateRunID
	TenantID      TenantID
	WorkspacePath string
	TerraformPath string
	WorkspaceName string
	Command       TerraformCommandType
}

// TemplateSyncWorkflowInput starts template metadata sync for a public GitHub template.
type TemplateSyncWorkflowInput struct {
	RegistrationID TemplateRegistrationID
	TenantID       TenantID
	RepoOwner      string
	RepoName       string
	SourceRef      string
	RootPath       string
}

// TemplateSyncActivityInput asks the worker to sync one template registration source.
type TemplateSyncActivityInput struct {
	RegistrationID TemplateRegistrationID
	TenantID       TenantID
	RepoOwner      string
	RepoName       string
	SourceRef      string
	RootPath       string
}

// TemplateSyncActivityOutput reports the sync result for one registration source.
type TemplateSyncActivityOutput struct {
	Status             TemplateRegistrationStatus
	TemplateRevisionID TemplateRevisionID
	ResolvedCommitSHA  string
	ErrorSummary       string
}

// TemplateRegistrationStatusActivityInput asks the worker to persist one registration status transition.
type TemplateRegistrationStatusActivityInput struct {
	RegistrationID     TemplateRegistrationID
	TenantID           TenantID
	Status             TemplateRegistrationStatus
	TemplateRevisionID TemplateRevisionID
	ResolvedCommitSHA  string
	ErrorSummary       string
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
