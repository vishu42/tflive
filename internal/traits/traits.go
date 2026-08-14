// Package traits contains shared product and workflow traits.
package traits

import (
	"encoding/json"
	"reflect"
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

// CredentialScope identifies the resource that owns one encrypted credential.
type CredentialScope string

const (
	CredentialScopeStack         CredentialScope = "stack"
	CredentialScopeStackTemplate CredentialScope = "stack_template"
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
	TerraformCommandDestroy         TerraformCommandType = "destroy"
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
	TemplateRunWorkspaceSelected TemplateRunStatus = "workspace_selected"
	TemplateRunWaitingApproval   TemplateRunStatus = "waiting_approval"
	TemplateRunApproved          TemplateRunStatus = "approved"
	TemplateRunCancelRequested   TemplateRunStatus = "cancel_requested"
	TemplateRunCanceling         TemplateRunStatus = "canceling"
	TemplateRunCanceled          TemplateRunStatus = "canceled"
	TemplateRunLockReleased      TemplateRunStatus = "lock_released"
	TemplateRunCompleted         TemplateRunStatus = "completed"
	TemplateRunFailed            TemplateRunStatus = "failed"

	// init statues
	TemplateRunInitStarted  TemplateRunStatus = "init_started"
	TemplateRunInitFinished TemplateRunStatus = "init_finished"

	// plan statues
	TemplateRunPlanStarted  TemplateRunStatus = "plan_started"
	TemplateRunPlanFinished TemplateRunStatus = "plan_finished"

	// apply statues
	TemplateRunApplyStarted  TemplateRunStatus = "apply_started"
	TemplateRunApplyFinished TemplateRunStatus = "apply_finished"

	// destroy statues
	TemplateRunDestroyStarted  TemplateRunStatus = "destroy_started"
	TemplateRunDestroyFinished TemplateRunStatus = "destroy_finished"
)

// Valid reports whether the status is one of the supported run states.
func (status TemplateRunStatus) Valid() bool {
	switch status {
	case TemplateRunQueued,
		TemplateRunLocked,
		TemplateRunWorkspacePrepared,
		TemplateRunSourceFetched,
		TemplateRunWorkspaceSelected,
		TemplateRunWaitingApproval,
		TemplateRunApproved,
		TemplateRunCancelRequested,
		TemplateRunCanceling,
		TemplateRunCanceled,
		TemplateRunLockReleased,
		TemplateRunCompleted,
		TemplateRunFailed,
		TemplateRunInitStarted,
		TemplateRunInitFinished,
		TemplateRunPlanStarted,
		TemplateRunPlanFinished,
		TemplateRunApplyStarted,
		TemplateRunApplyFinished,
		TemplateRunDestroyStarted,
		TemplateRunDestroyFinished:
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

// StackStatus reports whether a stack has finished provisioning. Creation
// enqueues the owner grant rather than writing it inline, so the stack exists
// before anyone can act on it; the status makes that window visible instead of
// leaving the caller to guess why access is missing.
type StackStatus string

const (
	StackStatusProvisioning StackStatus = "provisioning"
	StackStatusReady        StackStatus = "ready"
)

// Stack is a logical infrastructure composition.
type Stack struct {
	ID       StackID           `json:"id"`
	TenantID TenantID          `json:"tenant_id"`
	Name     string            `json:"name"`
	Slug     string            `json:"slug"`
	Status   StackStatus       `json:"status"`
	Tags     map[string]string `json:"tags"`
	// DefaultCredentialIDs is retained for backward-compatible reads; new credentials are scope-owned records.
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
	// ComponentKey is the human/stable key unique among active installs in a stack.
	ComponentKey string `json:"component_key"`
	// SourceTemplateID is the stable source identity shared by all revisions.
	SourceTemplateID SourceTemplateID `json:"source_template_id"`
	// DesiredTemplateRevisionID is the template revision to use for the next run.
	DesiredTemplateRevisionID TemplateRevisionID `json:"desired_template_revision_id"`
	// LastAppliedTemplateRevisionID is the template revision from the last successful apply.
	LastAppliedTemplateRevisionID TemplateRevisionID `json:"last_applied_template_revision_id"`
	// WorkspaceName is the Terraform workspace used for this component.
	WorkspaceName string `json:"workspace_name"`
	// ConfigJSON is the install-time config and legacy fallback for desired config.
	ConfigJSON json.RawMessage `json:"config_json"`
	// DesiredConfigJSON is the config to snapshot into the next run.
	DesiredConfigJSON json.RawMessage `json:"desired_config_json"`
	// LastAppliedRunID is the run that last applied this component successfully.
	LastAppliedRunID TemplateRunID `json:"last_applied_run_id"`
	// LastAppliedConfigJSON is the config the last successful apply ran with.
	// Nil means no apply has been recorded with one; an empty object is a
	// legitimate value, so absence cannot be spelled '{}'.
	LastAppliedConfigJSON json.RawMessage `json:"last_applied_config_json,omitempty"`
	// LastAppliedAt is when the last successful apply completed.
	LastAppliedAt time.Time `json:"last_applied_at,omitempty"`
	// LastPlannedRunID is the latest plan run that completed for this component.
	LastPlannedRunID TemplateRunID `json:"last_planned_run_id"`
	// LastPlannedTemplateRevisionID is the revision that plan ran against.
	LastPlannedTemplateRevisionID TemplateRevisionID `json:"last_planned_template_revision_id"`
	// LastPlannedConfigJSON is the config that plan ran with. Nil like
	// LastAppliedConfigJSON, and for the same reason.
	LastPlannedConfigJSON json.RawMessage `json:"last_planned_config_json,omitempty"`
	// LastPlannedAt is when that plan completed.
	LastPlannedAt time.Time `json:"last_planned_at,omitempty"`
	// CreatedBy is the user that installed this component.
	CreatedBy UserID `json:"created_by"`
	// Lifecycle determines whether this component can run or is being removed.
	Lifecycle StackTemplateLifecycle `json:"lifecycle"`
	// DisplayName is a human-readable label derived from template metadata when resolving a view.
	// It is not persisted and may be empty for templates created outside the view path.
	DisplayName string `json:"-"`
	// SourceRef is the desired revision's ref, resolved when a view is built.
	// It is not persisted: a ref belongs to the revision, and a copy kept here
	// would go stale the moment the desired revision changed. Like DisplayName
	// it may be empty for templates built outside the view path.
	SourceRef string `json:"-"`
}

// PlanState answers "will the thing that was reviewed be the thing that runs?".
// It is the safety gate on apply: only PlanMatches means the completed plan
// still describes desired state.
type PlanState string

const (
	// PlanNone means no plan has completed for this component.
	PlanNone PlanState = "none"
	// PlanStale means a plan completed but desired has moved since.
	PlanStale PlanState = "stale"
	// PlanMatches means the completed plan is exactly what would run now.
	PlanMatches PlanState = "matches"
)

// LiveState answers "is anything pending?" by comparing desired against what
// the last apply put live. It gates no action on its own; it is what lets the
// UI distinguish an unapplied edit from a steady state.
type LiveState string

const (
	// LiveNever means nothing has been applied yet.
	LiveNever LiveState = "never"
	// LiveDiffers means desired has moved away from what is live.
	LiveDiffers LiveState = "differs"
	// LiveMatches means the last apply's intent equals desired. It does not
	// claim reality matches desired — that is drift, and needs a refresh.
	LiveMatches LiveState = "matches"
)

// DesiredConfig is the config a run started now would snapshot: the desired
// config, falling back to the install-time config for rows written before
// desired_config_json existed. Comparisons have to use this rather than
// DesiredConfigJSON directly, or they compare against something other than what
// would actually run.
func (stackTemplate StackTemplate) DesiredConfig() json.RawMessage {
	if len(stackTemplate.DesiredConfigJSON) > 0 {
		return stackTemplate.DesiredConfigJSON
	}
	return stackTemplate.ConfigJSON
}

// PlanState reports whether the latest completed plan still describes desired
// state. A recorded plan whose config is missing reports PlanStale: the point of
// the gate is to refuse an apply nobody can vouch for, so anything unverifiable
// fails closed.
func (stackTemplate StackTemplate) PlanState() PlanState {
	if stackTemplate.LastPlannedRunID == "" {
		return PlanNone
	}
	if snapshotMatchesDesired(stackTemplate.LastPlannedTemplateRevisionID, stackTemplate.LastPlannedConfigJSON, stackTemplate) {
		return PlanMatches
	}
	return PlanStale
}

// LiveState reports whether desired state has been applied. It fails closed the
// same way PlanState does — an unverifiable apply reads as pending work rather
// than as a steady state.
func (stackTemplate StackTemplate) LiveState() LiveState {
	if stackTemplate.LastAppliedRunID == "" {
		return LiveNever
	}
	if snapshotMatchesDesired(stackTemplate.LastAppliedTemplateRevisionID, stackTemplate.LastAppliedConfigJSON, stackTemplate) {
		return LiveMatches
	}
	return LiveDiffers
}

// snapshotMatchesDesired compares a recorded (revision, config) pair against
// desired. Both halves matter: a revision-only change leaves the config
// identical on both sides, and on an all-optional template both sides are '{}',
// so a config-only check would pass while the module version changed underneath.
func snapshotMatchesDesired(revisionID TemplateRevisionID, configJSON json.RawMessage, stackTemplate StackTemplate) bool {
	if revisionID != stackTemplate.DesiredTemplateRevisionID {
		return false
	}
	if configJSON == nil {
		return false
	}
	return sameJSONConfig(configJSON, stackTemplate.DesiredConfig())
}

// sameJSONConfig compares two configs by parsed structure. Comparing the bytes
// would be wrong: these are jsonb columns, and Postgres orders object keys by
// length then bytewise where Go sorts them alphabetically, so two identical
// configs can differ byte-for-byte and the user would be told to re-plan forever.
// Unmarshalling into any yields map[string]any/[]any/float64/string/bool/nil,
// and reflect.DeepEqual on maps ignores key order. Unparseable input reports
// false so callers fail closed.
func sameJSONConfig(left json.RawMessage, right json.RawMessage) bool {
	var leftValue, rightValue any
	if err := json.Unmarshal(defaultConfigJSON(left), &leftValue); err != nil {
		return false
	}
	if err := json.Unmarshal(defaultConfigJSON(right), &rightValue); err != nil {
		return false
	}
	return reflect.DeepEqual(leftValue, rightValue)
}

// defaultConfigJSON treats an empty desired config as the empty object it is
// persisted as, so a template installed with no config compares equal to a run
// that snapshotted '{}'.
func defaultConfigJSON(configJSON json.RawMessage) json.RawMessage {
	if len(configJSON) == 0 {
		return json.RawMessage(`{}`)
	}
	return configJSON
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
	ID              CredentialSetID // Stable identifier used by API deletion and audit references.
	TenantID        TenantID        // Tenant that owns the encrypted credential.
	StackID         StackID         // Stack owner; empty when StackTemplateID is populated.
	StackTemplateID StackTemplateID // Optional template owner for template-specific overrides.
	Name            string          // Provider environment variable name.
	Ciphertext      string          // Encrypted value; plaintext must never be persisted here.
	CreatedAt       time.Time       // Creation timestamp used for metadata display and auditing.
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
	// SelectedRef is the ref the component was installed from. It is carried
	// for provenance and log context only — what the run executes is
	// ResolvedCommitSHA, because a ref can move between planning and applying.
	SelectedRef string
	// ResolvedCommitSHA is the commit the desired revision resolved to, and the
	// exact source a run checks out.
	ResolvedCommitSHA string
	WorkspaceName     string
	RepoOwner         string
	RepoName          string
	RootPath          string
	ConfigJSON        json.RawMessage
}

// TemplateRunStatusActivityInput asks the worker to persist one run status transition.
type TemplateRunStatusActivityInput struct {
	RunID           TemplateRunID
	TenantID        TenantID
	StackTemplateID StackTemplateID
	Operation       OperationType
	Status          TemplateRunStatus
	ErrorSummary    string
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
	// SourceRef is the ref the component was installed from. It is only used
	// when ResolvedCommitSHA is absent, which is true solely for runs queued
	// before the commit was threaded through.
	SourceRef string
	// ResolvedCommitSHA is the exact commit to check out.
	ResolvedCommitSHA string
	RootPath          string
}

// FetchSourceActivityOutput identifies the Terraform module directory within the cloned source.
type FetchSourceActivityOutput struct {
	TerraformPath string
}

// RunTerraformActivityInput asks the worker to run one Terraform subprocess command.
type RunTerraformActivityInput struct {
	RunID           TemplateRunID
	TenantID        TenantID
	StackTemplateID StackTemplateID
	WorkspacePath   string
	TerraformPath   string
	WorkspaceName   string
	Command         TerraformCommandType
	ConfigJSON      json.RawMessage
	Environment     map[string]string
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

// AuditAction identifies a security-relevant authorization event.
type AuditAction string

const (
	AuditActionGrant                AuditAction = "grant"
	AuditActionRevoke               AuditAction = "revoke"
	AuditActionRoleChange           AuditAction = "role_change"
	AuditActionFailedAccessAttempt  AuditAction = "failed_access_attempt"
	AuditActionSelfApprovalRejected AuditAction = "self_approval_rejected"
	AuditActionApprovalGranted      AuditAction = "approval_granted"
)

// AuditOutcome reports whether the audited operation succeeded or failed.
type AuditOutcome string

const (
	AuditOutcomeSuccess AuditOutcome = "success"
	AuditOutcomeFailure AuditOutcome = "failure"
)

// SecurityAuditEvent is one append-only authorization audit record.
type SecurityAuditEvent struct {
	ID            int64        `json:"id"`
	RecordedAt    time.Time    `json:"recorded_at"`
	ActorSubject  string       `json:"actor_subject"`
	Action        AuditAction  `json:"action"`
	TargetUser    string       `json:"target_user"`
	TenantID      TenantID     `json:"tenant_id"`
	StackID       StackID      `json:"stack_id"`
	OldRole       string       `json:"old_role"`
	NewRole       string       `json:"new_role"`
	Outcome       AuditOutcome `json:"outcome"`
	CorrelationID string       `json:"correlation_id"`
}
