package domain

// Payloads and names crossing the Temporal boundary.

import (
	"encoding/json"
)

const (
	// ControlTaskQueue carries workflow tasks and every activity that touches
	// product state or secrets. Only the control plane polls it.
	ControlTaskQueue = "control"
	// ExecutionTaskQueue carries the activities that run next to tenant
	// Terraform. The executor polls it, and nothing that polls it holds a
	// database connection or a key.
	//
	// Both names are constants rather than configuration: the workflow names the
	// execution queue in its activity options, and a queue named by env on one
	// side and by code on the other fails silently, with tasks waiting on a
	// queue nobody polls. Environments are separated by Temporal namespace.
	ExecutionTaskQueue = "execution"

	TemplateRunWorkflowName  = "TemplateRunWorkflow"
	TemplateSyncWorkflowName = "TemplateSyncWorkflow"

	ApprovalSignalName = "approval"
	CancelSignalName   = "cancel"

	RecordTemplateRunStatusActivityName          = "RecordTemplateRunStatus"
	RecordTemplateRunLogActivityName             = "RecordTemplateRunLog"
	RecordTemplateRegistrationStatusActivityName = "RecordTemplateRegistrationStatus"
	PrepareWorkspaceActivityName                 = "PrepareWorkspace"
	FetchSourceActivityName                      = "FetchSource"
	RunTerraformActivityName                     = "RunTerraform"
	SyncTemplateActivityName                     = "SyncTemplate"

	// TerraformCommandFailedErrorType marks a RunTerraform failure whose
	// ApplicationError details carry the uploaded log's TemplateRunLog.
	TerraformCommandFailedErrorType = "TerraformCommandFailed"
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

// RunTerraformActivityOutput reports what a Terraform command left behind.
type RunTerraformActivityOutput struct {
	// Log describes the uploaded phase log. The executor has no database, so
	// the workflow records it through a control activity.
	Log TemplateRunLog
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
