package activities

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/vishu42/megagega/internal/traits"
)

type StatusRecorder interface {
	RecordTemplateRunStatus(context.Context, traits.TemplateRunStatusActivityInput) error
}

type TemplateRunActivities struct {
	recorder StatusRecorder
	runRoot  string
}

func NewTemplateRunActivities(recorder StatusRecorder, runRoot string) *TemplateRunActivities {
	return &TemplateRunActivities{
		recorder: recorder,
		runRoot:  runRoot,
	}
}

func (activities *TemplateRunActivities) RecordTemplateRunStatus(ctx context.Context, input traits.TemplateRunStatusActivityInput) error {
	if err := activities.recorder.RecordTemplateRunStatus(ctx, input); err != nil {
		return fmt.Errorf("record template run status: %w", err)
	}

	return nil
}

func (activities *TemplateRunActivities) PrepareWorkspace(ctx context.Context, input traits.PrepareWorkspaceActivityInput) (traits.PrepareWorkspaceActivityOutput, error) {
	if strings.TrimSpace(activities.runRoot) == "" {
		return traits.PrepareWorkspaceActivityOutput{}, fmt.Errorf("run root is required")
	}
	if !safePathComponent(string(input.TenantID)) || !safePathComponent(string(input.RunID)) {
		return traits.PrepareWorkspaceActivityOutput{}, fmt.Errorf("tenant ID and run ID must be safe path components")
	}

	workspacePath := filepath.Join(activities.runRoot, string(input.TenantID), string(input.RunID))
	if err := os.MkdirAll(workspacePath, 0o700); err != nil {
		return traits.PrepareWorkspaceActivityOutput{}, fmt.Errorf("prepare workspace directory: %w", err)
	}

	return traits.PrepareWorkspaceActivityOutput{WorkspacePath: workspacePath}, nil
}

func safePathComponent(component string) bool {
	if strings.TrimSpace(component) == "" {
		return false
	}
	if filepath.IsAbs(component) {
		return false
	}
	cleaned := filepath.Clean(component)
	return cleaned == component && component != "." && component != ".." && filepath.Base(component) == component
}
