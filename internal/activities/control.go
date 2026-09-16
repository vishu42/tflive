package activities

import (
	"context"
	"fmt"

	"github.com/vishu42/tflive/internal/domain"
	"github.com/vishu42/tflive/internal/runseal"
)

// ControlStore is everything the control activities read and write.
type ControlStore interface {
	StatusRecorder
	LogMetadataRecorder
	CredentialReader
	CredentialDecryptor
}

// LogMetadataRecorder persists the metadata row for an uploaded phase log.
type LogMetadataRecorder interface {
	RecordTemplateRunLog(ctx context.Context, log domain.TemplateRunLog) error
}

// ControlActivities are the template-run activities that belong to the control
// plane. They write product state or handle secrets, so they run in the API
// process on the control queue, never next to tenant Terraform.
type ControlActivities struct {
	store  ControlStore
	tokens GitHubTokenSource
}

// NewControlActivities wires the control activities. tokens may be nil, in
// which case sources are fetched unauthenticated.
func NewControlActivities(store ControlStore, tokens GitHubTokenSource) *ControlActivities {
	return &ControlActivities{store: store, tokens: tokens}
}

// RecordTemplateRunStatus records one workflow status transition.
//
// Workflows call this as an activity because database writes are side effects
// and cannot run inside deterministic workflow code.
func (activities *ControlActivities) RecordTemplateRunStatus(ctx context.Context, input domain.TemplateRunStatusActivityInput) error {
	if err := activities.store.RecordTemplateRunStatus(ctx, input); err != nil {
		return fmt.Errorf("record template run status: %w", err)
	}
	return nil
}

// RecordTemplateRunLog records the metadata for a phase log the executor has
// already uploaded. The row is keyed by run and phase, so a retry upserts.
func (activities *ControlActivities) RecordTemplateRunLog(ctx context.Context, log domain.TemplateRunLog) error {
	if err := activities.store.RecordTemplateRunLog(ctx, log); err != nil {
		return fmt.Errorf("record template run log metadata: %w", err)
	}
	return nil
}

// SealRunCredentials decrypts a StackTemplate's credentials and seals them to
// the run's key, so they cross Temporal as ciphertext only the executor
// holding that run can open.
func (activities *ControlActivities) SealRunCredentials(ctx context.Context, input domain.SealRunCredentialsActivityInput) (domain.SealRunCredentialsActivityOutput, error) {
	environment, err := resolveCredentialEnvironment(ctx, activities.store, activities.store, input.TenantID, input.StackTemplateID)
	if err != nil {
		return domain.SealRunCredentialsActivityOutput{}, fmt.Errorf("resolve terraform credentials: %w", err)
	}
	sealed, err := runseal.Seal(input.PublicKey, environment)
	if err != nil {
		return domain.SealRunCredentialsActivityOutput{}, fmt.Errorf("seal terraform credentials: %w", err)
	}
	return domain.SealRunCredentialsActivityOutput{SealedEnvironment: sealed}, nil
}

// SealSourceToken resolves an installation token for a repository and seals
// it to the run's key.
//
// Resolving is best effort, as it was when the executor did it: a public
// repository needs no token, and a missing installation is indistinguishable
// from one. So a failed lookup seals the empty token and returns the hint the
// fetch appends if it then fails unauthenticated.
func (activities *ControlActivities) SealSourceToken(ctx context.Context, input domain.SealSourceTokenActivityInput) (domain.SealSourceTokenActivityOutput, error) {
	var token string
	var hint string
	if activities.tokens != nil {
		resolved, err := activities.tokens.Token(ctx, input.RepoOwner, input.RepoName)
		if err != nil {
			hint = unauthenticatedFetchHint(err, input.RepoOwner, input.RepoName)
		} else {
			token = resolved
		}
	}
	sealed, err := runseal.Seal(input.PublicKey, token)
	if err != nil {
		return domain.SealSourceTokenActivityOutput{}, fmt.Errorf("seal source token: %w", err)
	}
	return domain.SealSourceTokenActivityOutput{SealedToken: sealed, FetchHint: hint}, nil
}
