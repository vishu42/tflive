package domain

// The template catalogue: sources, revisions, registrations, variables.

import (
	"slices"
	"time"
)

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

// TemplateRegistrationStep is what a running sync is doing right now, for the
// people watching it. It is not a status: nothing branches on it. It is
// recorded when a step starts and kept when the sync ends, so a failed sync
// still says where it failed. The zero value is a sync that has not started a
// step.
type TemplateRegistrationStep string

const (
	// TemplateRegistrationStepSyncing is cloning the repository, parsing the
	// template it holds and saving the revision it describes.
	TemplateRegistrationStepSyncing TemplateRegistrationStep = "syncing"
)

// AllTemplateRegistrationSteps is every step a sync may record, in the order a
// sync takes them. The step check constraint in the migrations lists the same
// values.
var AllTemplateRegistrationSteps = []TemplateRegistrationStep{
	TemplateRegistrationStepSyncing,
}

// Valid reports whether the step is one a sync may record. The zero value is
// not: it is what a registration has before its sync records one.
func (step TemplateRegistrationStep) Valid() bool {
	return slices.Contains(AllTemplateRegistrationSteps, step)
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
	Step               TemplateRegistrationStep   `json:"step"`
	TemplateRevisionID TemplateRevisionID         `json:"template_revision_id"`
	ResolvedCommitSHA  string                     `json:"resolved_commit_sha"`
	RequestedBy        UserID                     `json:"requested_by"`
	RequestedAt        time.Time                  `json:"requested_at"`
	CompletedAt        time.Time                  `json:"completed_at"`
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
