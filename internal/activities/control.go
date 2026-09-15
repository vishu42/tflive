package activities

import (
	"context"
	"fmt"

	"github.com/vishu42/tflive/internal/domain"
)

// ControlActivities are the template-run activities that belong to the control
// plane: they write product state, so they run in the API process on the
// control queue, never next to tenant Terraform.
type ControlActivities struct {
	runs StatusRecorder
	logs LogMetadataRecorder
}

// LogMetadataRecorder persists the metadata row for an uploaded phase log.
type LogMetadataRecorder interface {
	RecordTemplateRunLog(ctx context.Context, log domain.TemplateRunLog) error
}

func NewControlActivities(runs StatusRecorder, logs LogMetadataRecorder) *ControlActivities {
	return &ControlActivities{runs: runs, logs: logs}
}

// RecordTemplateRunStatus records one workflow status transition.
//
// Workflows call this as an activity because database writes are side effects
// and cannot run inside deterministic workflow code.
func (activities *ControlActivities) RecordTemplateRunStatus(ctx context.Context, input domain.TemplateRunStatusActivityInput) error {
	if err := activities.runs.RecordTemplateRunStatus(ctx, input); err != nil {
		return fmt.Errorf("record template run status: %w", err)
	}
	return nil
}

// RecordTemplateRunLog records the metadata for a phase log the executor has
// already uploaded. The row is keyed by run and phase, so a retry upserts.
func (activities *ControlActivities) RecordTemplateRunLog(ctx context.Context, log domain.TemplateRunLog) error {
	if err := activities.logs.RecordTemplateRunLog(ctx, log); err != nil {
		return fmt.Errorf("record template run log metadata: %w", err)
	}
	return nil
}
