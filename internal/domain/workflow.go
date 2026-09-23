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

	// TemplatePlanWorkflowName plans a run. TemplateApplyWorkflowName applies a
	// plan someone approved, or applies an auto-approved run with no plan.
	TemplatePlanWorkflowName  = "TemplatePlanWorkflow"
	TemplateApplyWorkflowName = "TemplateApplyWorkflow"
	TemplateSyncWorkflowName  = "TemplateSyncWorkflow"

	RecordTemplateRunStatusActivityName          = "RecordTemplateRunStatus"
	RecordTemplateRunLogActivityName             = "RecordTemplateRunLog"
	RecordTemplateRunStepActivityName            = "RecordTemplateRunStep"
	RecordTemplateRunEventActivityName           = "RecordTemplateRunEvent"
	RecordTemplateRegistrationStatusActivityName = "RecordTemplateRegistrationStatus"
	RecordTemplateRegistrationStepActivityName   = "RecordTemplateRegistrationStep"
	PrepareWorkspaceActivityName                 = "PrepareWorkspace"
	FetchSourceActivityName                      = "FetchSource"
	RunTerraformActivityName                     = "RunTerraform"
	SyncTemplateActivityName                     = "SyncTemplate"
	SealSourceTokenActivityName                  = "SealSourceToken"
	SealRunCredentialsActivityName               = "SealRunCredentials"
	ReleaseRunKeyActivityName                    = "ReleaseRunKey"
	CleanupWorkspaceActivityName                 = "CleanupWorkspace"
	SealPlanKeyActivityName                      = "SealPlanKey"
	UploadPlanActivityName                       = "UploadPlan"
	DownloadPlanActivityName                     = "DownloadPlan"
	FinishPlanActivityName                       = "FinishPlan"
	BeginApplyActivityName                       = "BeginApply"

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
	// full Terraform timeout expires.
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
// The apply workflow takes the same input: it re-fetches the same commit and
// applies the plan saved under the same run.
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
	// AutoApprove makes an apply run apply straight away, with no saved plan
	// and no approval. Only an apply run takes it.
	AutoApprove bool
	// TerraformTimeout bounds each Terraform command this run issues. It is
	// stamped by the dispatcher from deployment configuration so the value a
	// run was started with stays visible in its workflow history; zero means
	// DefaultTerraformTimeout.
	TerraformTimeout time.Duration
}

// TemplateRunStatusActivityInput asks the control plane to move a run along
// its lifecycle.
type TemplateRunStatusActivityInput struct {
	RunID           TemplateRunID
	TenantID        TenantID
	StackTemplateID StackTemplateID
	Operation       OperationType
	Status          TemplateRunStatus
	ErrorSummary    string
}

// TemplateRunStepActivityInput records the step a running run has started.
type TemplateRunStepActivityInput struct {
	RunID    TemplateRunID
	TenantID TenantID
	Step     TemplateRunStep
}

// TemplateRunEventActivityInput records something a running run did to its
// stack template.
type TemplateRunEventActivityInput struct {
	RunID           TemplateRunID
	TenantID        TenantID
	StackTemplateID StackTemplateID
	Operation       OperationType
	Event           TemplateRunEvent
	// Summary, when set, records the run's change counts with the event. An
	// auto-approved apply has no plan to count, so it records what the apply
	// itself reported with TemplateRunApplied.
	Summary *PlanSummary
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

// RunPhase is the phase of a run a Terraform command belongs to. An approved
// run has both, and both run init and workspace selection, so it keeps their
// logs apart.
type RunPhase string

const (
	RunPhasePlan  RunPhase = "plan"
	RunPhaseApply RunPhase = "apply"
)

// RunTerraformActivityInput asks the executor to run one Terraform subprocess command.
type RunTerraformActivityInput struct {
	RunID           TemplateRunID
	TenantID        TenantID
	StackTemplateID StackTemplateID
	WorkspacePath   string
	TerraformPath   string
	WorkspaceName   string
	Command         TerraformCommandType
	// RunPhase names the phase Command runs in, which picks its log.
	RunPhase   RunPhase
	ConfigJSON json.RawMessage
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
	// HasChanges and Summary describe a plan: whether it would change
	// anything, and what. Zero for every other command.
	HasChanges bool
	Summary    PlanSummary
}

// CleanupWorkspaceActivityInput asks the executor to delete a run's workspace
// before its session ends, and with DeletePlan also the run's saved plan.
type CleanupWorkspaceActivityInput struct {
	TenantID      TenantID
	RunID         TemplateRunID
	WorkspacePath string
	DeletePlan    bool
}

// SealPlanKeyActivityInput asks the control plane for the key a run's saved
// plan is encrypted with, sealed to the run's key on the executor now holding
// it. Create makes the key on the plan phase; the apply phase only reads it.
type SealPlanKeyActivityInput struct {
	TenantID  TenantID
	RunID     TemplateRunID
	PublicKey []byte
	Create    bool
}

// SealPlanKeyActivityOutput carries the sealed plan key.
type SealPlanKeyActivityOutput struct {
	SealedPlanKey []byte
}

// PlanArtifactActivityInput asks the executor to upload a run's saved plan
// from TerraformPath, or to download it back there.
type PlanArtifactActivityInput struct {
	TenantID      TenantID
	RunID         TemplateRunID
	TerraformPath string
	SealedPlanKey []byte
}

// PlanOutcome is what happens to a run once its plan has finished.
type PlanOutcome string

const (
	// PlanOutcomeNoChanges: nothing to apply, so the run completes.
	PlanOutcomeNoChanges PlanOutcome = "no_changes"
	// PlanOutcomeWaiting: the plan waits for someone to approve it.
	PlanOutcomeWaiting PlanOutcome = "waiting"
	// PlanOutcomePlanned: a plan run's plan had changes. A plan run only
	// plans, so the run completes with the changes recorded.
	PlanOutcomePlanned PlanOutcome = "planned"
)

// FinishPlanActivityInput records a finished plan and decides what follows.
type FinishPlanActivityInput struct {
	TenantID        TenantID
	RunID           TemplateRunID
	StackTemplateID StackTemplateID
	Operation       OperationType
	HasChanges      bool
	Summary         PlanSummary
}

// BeginApplyActivityInput claims a run for its apply phase: an approved run,
// or, with AutoApprove, a queued one that never had a plan to approve.
type BeginApplyActivityInput struct {
	TenantID    TenantID
	RunID       TemplateRunID
	AutoApprove bool
}

// BeginApplyActivityOutput reports whether the claim won. It loses when the
// plan was discarded after it was approved and before its apply began.
type BeginApplyActivityOutput struct {
	Claimed bool
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

// TemplateRegistrationStepActivityInput records the step a running sync has
// started.
type TemplateRegistrationStepActivityInput struct {
	RegistrationID TemplateRegistrationID
	TenantID       TenantID
	Step           TemplateRegistrationStep
}
