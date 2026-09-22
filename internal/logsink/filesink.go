package logsink

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/vishu42/tflive/internal/domain"
)

type FileSink struct {
	workspacePath string
}

type LocalReader struct {
	runRoot string
}

// NewFileSink returns a local filesystem-backed sink rooted at workspacePath.
func NewFileSink(workspacePath string) FileSink {
	return FileSink{workspacePath: workspacePath}
}

// NewLocalReader returns a local filesystem-backed reader rooted at runRoot.
func NewLocalReader(runRoot string) LocalReader {
	return LocalReader{runRoot: runRoot}
}

// RunWorkspacePath returns the local workspace path for one tenant-owned run.
func RunWorkspacePath(runRoot string, tenantID domain.TenantID, runID domain.TemplateRunID) (string, error) {
	if strings.TrimSpace(runRoot) == "" {
		return "", fmt.Errorf("run root is required")
	}
	if !safePathComponent(string(tenantID)) || !safePathComponent(string(runID)) {
		return "", fmt.Errorf("tenant ID and run ID must be safe path components")
	}
	return filepath.Join(runRoot, string(tenantID), string(runID)), nil
}

// LogFileExtension ends every log file name. A log's phase, the name it is
// stored and read under, is its file name without it.
const LogFileExtension = ".log"

// Open opens the append-only log file with this name, such as plan-init.log.
func (sink FileSink) Open(fileName string) (io.WriteCloser, error) {
	if strings.TrimSpace(sink.workspacePath) == "" {
		return nil, fmt.Errorf("workspace path is required")
	}
	if !safePathComponent(fileName) {
		return nil, fmt.Errorf("log file name must be a safe path component")
	}

	logsPath := filepath.Join(sink.workspacePath, "logs")
	if err := os.MkdirAll(logsPath, 0o700); err != nil {
		return nil, fmt.Errorf("create log directory: %w", err)
	}

	path := filepath.Join(logsPath, fileName)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open phase log: %w", err)
	}
	return file, nil
}

// FileNameForTerraformCommand maps a Terraform subprocess command to its log
// file name. Init and workspace selection run in both the plan and the apply
// phase, so their logs are named for the phase: plan-init.log, apply-init.log.
func FileNameForTerraformCommand(command domain.TerraformCommandType, runPhase domain.RunPhase) (string, error) {
	if runPhase != domain.RunPhasePlan && runPhase != domain.RunPhaseApply {
		return "", fmt.Errorf("unsupported run phase %q", runPhase)
	}
	switch command {
	case domain.TerraformCommandInit:
		return string(runPhase) + "-init" + LogFileExtension, nil
	case domain.TerraformCommandSelectWorkspace:
		return string(runPhase) + "-workspace" + LogFileExtension, nil
	case domain.TerraformCommandPlan, domain.TerraformCommandPlanDestroy:
		return "plan" + LogFileExtension, nil
	case domain.TerraformCommandApply, domain.TerraformCommandApplyAutoApprove:
		return "apply" + LogFileExtension, nil
	case domain.TerraformCommandDestroy:
		return "destroy" + LogFileExtension, nil
	default:
		return "", fmt.Errorf("unsupported terraform command %q", command)
	}
}

// ReadTemplateRunLog reads one tenant/run phase log from the local run root.
func (reader LocalReader) ReadTemplateRunLog(_ context.Context, tenantID domain.TenantID, runID domain.TemplateRunID, phase string) ([]byte, error) {
	workspacePath, err := RunWorkspacePath(reader.runRoot, tenantID, runID)
	if err != nil {
		return nil, err
	}
	if !safePathComponent(phase) {
		return nil, fmt.Errorf("phase must be a safe path component")
	}

	path := filepath.Join(workspacePath, "logs", phase+LogFileExtension)
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read phase log: %w", err)
	}
	return content, nil
}

// safePathComponent reports whether component can be used as one path segment.
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
