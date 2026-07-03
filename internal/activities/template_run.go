package activities

import (
	"context"
	"fmt"

	"github.com/vishu42/megagega/internal/traits"
)

type StatusRecorder interface {
	RecordTemplateRunStatus(context.Context, traits.TemplateRunStatusActivityInput) error
}

type TemplateRunActivities struct {
	recorder StatusRecorder
}

func NewTemplateRunActivities(recorder StatusRecorder) *TemplateRunActivities {
	return &TemplateRunActivities{recorder: recorder}
}

func (activities *TemplateRunActivities) RecordTemplateRunStatus(ctx context.Context, input traits.TemplateRunStatusActivityInput) error {
	if err := activities.recorder.RecordTemplateRunStatus(ctx, input); err != nil {
		return fmt.Errorf("record template run status: %w", err)
	}

	return nil
}
