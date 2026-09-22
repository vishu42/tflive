package domain

// One Terraform operation against a stack template, and its lifecycle.

import (
	"encoding/json"
	"slices"
	"time"
)

// OperationType identifies what a run is for, as the Terraform CLI would name
// it. A plan run only plans: it shows what would change and ends. An apply run
// saves a plan of the desired state and applies it once someone approves it,
// or, started with auto-approve, applies without a saved plan at all. A
// destroy run saves a plan to destroy everything and applies it once
// approved; it has no auto-approve.
type OperationType string

const (
	OperationPlan    OperationType = "plan"
	OperationApply   OperationType = "apply"
	OperationDestroy OperationType = "destroy"
)

// TerraformCommandType identifies one Terraform subprocess command run by an executor.
type TerraformCommandType string

const (
	TerraformCommandInit            TerraformCommandType = "init"
	TerraformCommandSelectWorkspace TerraformCommandType = "select_workspace"
	TerraformCommandPlan            TerraformCommandType = "plan"
	// TerraformCommandPlanDestroy is a destroy run's plan: a plan to destroy
	// everything the template manages. It records the same statuses and log as
	// TerraformCommandPlan.
	TerraformCommandPlanDestroy TerraformCommandType = "plan_destroy"
	// TerraformCommandApply and TerraformCommandDestroy both apply the run's
	// saved plan. They stay two commands so each records its own statuses:
	// destroy_started and destroy_finished drive the stack template lifecycle.
	TerraformCommandApply   TerraformCommandType = "apply"
	TerraformCommandDestroy TerraformCommandType = "destroy"
	// TerraformCommandApplyAutoApprove is an auto-approved apply run's only
	// Terraform step besides setup: it plans and applies in one go, with no
	// saved plan, so it is the one apply that takes the run's variables. It
	// records the same statuses and log as TerraformCommandApply.
	TerraformCommandApplyAutoApprove TerraformCommandType = "apply_auto_approve"
)

// PlanSummary counts the resource changes in a saved plan. A replacement
// counts once as an add and once as a destroy, as Terraform's own summary line
// does.
type PlanSummary struct {
	Add     int `json:"add"`
	Change  int `json:"change"`
	Destroy int `json:"destroy"`
}

// Valid reports whether the operation is one of the supported operation types.
func (operation OperationType) Valid() bool {
	switch operation {
	case OperationPlan, OperationApply, OperationDestroy:
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

// AllTemplateRunStatuses is every status a run may hold, in lifecycle order.
//
// It exists because two things have to reason about the whole vocabulary rather
// than one status at a time: Valid below, and the template_runs_in_flight_idx
// test, which asserts the index's terminal-status predicate agrees with
// Terminal for every status there is.
//
// A status added to the constants above but not to this slice would be rejected
// by Valid and never reach that index test, so TestTemplateRunStatusValid reads
// the constants back out of this file and fails if the two disagree.
var AllTemplateRunStatuses = []TemplateRunStatus{
	TemplateRunQueued,
	TemplateRunLocked,
	TemplateRunWorkspacePrepared,
	TemplateRunSourceFetched,
	TemplateRunWorkspaceSelected,
	TemplateRunWaitingApproval,
	TemplateRunApproved,
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
	TemplateRunDestroyFinished,
}

// Valid reports whether the status is one of the supported run states.
func (status TemplateRunStatus) Valid() bool {
	return slices.Contains(AllTemplateRunStatuses, status)
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

// TemplateRun is one Terraform operation against a StackTemplate.
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
	// RunNumber counts runs within one stack template, from 1. It is what
	// people see and what URLs carry; ID stays the identity everywhere else.
	RunNumber int `json:"run_number"`
	// AutoApprove means the trigger actor asked an apply run to apply straight
	// away, with no saved plan and without waiting for anyone to approve it.
	AutoApprove bool `json:"auto_approve"`
	// PlanSummary is what the saved plan would change. Nil until a plan with
	// changes has finished.
	PlanSummary *PlanSummary `json:"plan_summary"`
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

// TemplateRunDiscard records who threw away a plan waiting for approval, and
// why. A discarded run ends canceled.
type TemplateRunDiscard struct {
	RunID       TemplateRunID
	TenantID    TenantID
	RequestedBy UserID
	Reason      string
	RequestedAt time.Time
}
