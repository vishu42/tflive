package domain

// Payloads and names crossing the Temporal boundary.

import (
	"encoding/json"
	"time"
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
	SealSourceTokenActivityName                  = "SealSourceToken"
	SealRunCredentialsActivityName               = "SealRunCredentials"
	ReleaseRunKeyActivityName                    = "ReleaseRunKey"

	// TerraformCommandFailedErrorType marks a RunTerraform failure whose
	// ApplicationError details carry the uploaded log's TemplateRunLog.
	TerraformCommandFailedErrorType = "TerraformCommandFailed"
)

const (
	// DefaultTerraformTimeout bounds one Terraform command -- not one run. A
	// run issues several (init, workspace select, plan, apply), and each gets
	// the full budget.
	//
	// It is deliberately generous: an apply that creates a managed database or
	// a cluster legitimately runs for tens of minutes, and a run killed
	// mid-apply leaves state the next run has to reconcile. Deployments that
	// know their templates are shorter than this lower it with
	// TFLIVE_TERRAFORM_TIMEOUT rather than living with a default that fails
	// honest work.
	DefaultTerraformTimeout = 45 * time.Minute

	// TerraformHeartbeatInterval is how often a running Terraform command
	// reports liveness, and TerraformHeartbeatTimeout is how long Temporal
	// waits for the next report before failing the activity.
	//
	// The pair is the only thing that makes a lost executor visible before the
	// full Terraform timeout expires, and the only channel by which a cancel
	// signal reaches a running command: Temporal delivers activity
	// cancellation on the heartbeat response, so an activity that never
	// heartbeats can never be canceled.
	//
	// The slack between them is deliberately wide -- six intervals. Terraform
	// commands are not retried, so a heartbeat lost to a GC pause or a blip in
	// the Temporal frontend would kill a run in the middle of an apply, which
	// is far more expensive than noticing a dead executor two minutes later
	// instead of forty seconds later.
	TerraformHeartbeatInterval = 20 * time.Second
	TerraformHeartbeatTimeout  = 6 * TerraformHeartbeatInterval
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
	// TerraformTimeout bounds each Terraform command this run issues. It is
	// stamped by the dispatcher from deployment configuration so the value a
	// run was started with stays visible in its workflow history; zero means
	// DefaultTerraformTimeout.
	TerraformTimeout time.Duration
}

// TemplateRunStatusActivityInput asks the control plane to persist one run status transition.
type TemplateRunStatusActivityInput struct {
	RunID           TemplateRunID
	TenantID        TenantID
	StackTemplateID StackTemplateID
	Operation       OperationType
	Status          TemplateRunStatus
	ErrorSummary    string
}

// PrepareWorkspaceActivityInput asks the executor to create a local run workspace.
type PrepareWorkspaceActivityInput struct {
	RunID    TemplateRunID
	TenantID TenantID
}

// PrepareWorkspaceActivityOutput identifies the prepared local run workspace.
type PrepareWorkspaceActivityOutput struct {
	WorkspacePath string
	// PublicKey is the run's sealing key. Its private half never leaves the
	// executor that prepared the workspace.
	PublicKey []byte
}

// RunKeyID names a run's key on the executor holding it.
func RunKeyID(tenantID TenantID, runID TemplateRunID) string {
	return string(tenantID) + "/" + string(runID)
}

// SealSourceTokenActivityInput asks the control plane for a repository token
// sealed to the run's key.
type SealSourceTokenActivityInput struct {
	RepoOwner string
	RepoName  string
	PublicKey []byte
}

// SealSourceTokenActivityOutput carries the sealed token, which is an empty
// string when none could be resolved. FetchHint explains why, for a fetch that
// then fails unauthenticated.
type SealSourceTokenActivityOutput struct {
	SealedToken []byte
	FetchHint   string
}

// SealRunCredentialsActivityInput asks the control plane for a StackTemplate's
// decrypted credentials sealed to the run's key.
type SealRunCredentialsActivityInput struct {
	TenantID        TenantID
	StackTemplateID StackTemplateID
	PublicKey       []byte
}

// SealRunCredentialsActivityOutput carries the sealed environment map.
type SealRunCredentialsActivityOutput struct {
	SealedEnvironment []byte
}

// ReleaseRunKeyActivityInput asks the executor to drop a finished run's key.
type ReleaseRunKeyActivityInput struct {
	TenantID TenantID
	RunID    TemplateRunID
}

// FetchSourceActivityInput asks the executor to clone a template source into a prepared run workspace.
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
	// SealedToken is the repository token sealed to the run's key.
	SealedToken []byte
	FetchHint   string
}

// FetchSourceActivityOutput identifies the Terraform module directory within the cloned source.
type FetchSourceActivityOutput struct {
	TerraformPath string
}

// RunTerraformActivityInput asks the executor to run one Terraform subprocess command.
type RunTerraformActivityInput struct {
	RunID           TemplateRunID
	TenantID        TenantID
	StackTemplateID StackTemplateID
	WorkspacePath   string
	TerraformPath   string
	WorkspaceName   string
	Command         TerraformCommandType
	ConfigJSON      json.RawMessage
	// SealedEnvironment is the run's credentials sealed to its key.
	SealedEnvironment []byte
	// Environment is the opened credentials, filled in on the executor. It is
	// excluded from JSON so it can never be written into workflow history.
	Environment map[string]string `json:"-"`
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

// TemplateSyncActivityInput asks the control plane to sync one template registration source.
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

// TemplateRegistrationStatusActivityInput asks the control plane to persist one registration status transition.
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
