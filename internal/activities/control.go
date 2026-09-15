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
}

func NewControlActivities(runs StatusRecorder) *ControlActivities {
	return &ControlActivities{runs: runs}
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
