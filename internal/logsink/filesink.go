package logsink

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/vishu42/tflive/internal/traits"
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
func RunWorkspacePath(runRoot string, tenantID traits.TenantID, runID traits.TemplateRunID) (string, error) {
	if strings.TrimSpace(runRoot) == "" {
		return "", fmt.Errorf("run root is required")
	}
	if !safePathComponent(string(tenantID)) || !safePathComponent(string(runID)) {
		return "", fmt.Errorf("tenant ID and run ID must be safe path components")
	}
	return filepath.Join(runRoot, string(tenantID), string(runID)), nil
}

// OpenPhase opens an append-only log file for a single run phase.
func (sink FileSink) OpenPhase(phase string) (io.WriteCloser, error) {
	if strings.TrimSpace(sink.workspacePath) == "" {
		return nil, fmt.Errorf("workspace path is required")
	}
	if !safePathComponent(phase) {
		return nil, fmt.Errorf("phase must be a safe path component")
	}

	logsPath := filepath.Join(sink.workspacePath, "logs")
	if err := os.MkdirAll(logsPath, 0o700); err != nil {
		return nil, fmt.Errorf("create log directory: %w", err)
	}

	path := filepath.Join(logsPath, phase+".log")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open phase log: %w", err)
	}
	return file, nil
}

// PhaseForTerraformCommand maps a Terraform subprocess command to its log phase.
func PhaseForTerraformCommand(command traits.TerraformCommandType) (string, error) {
	switch command {
	case traits.TerraformCommandInit:
		return "init", nil
	case traits.TerraformCommandSelectWorkspace:
		return "workspace", nil
	case traits.TerraformCommandPlan:
		return "plan", nil
	case traits.TerraformCommandApply:
		return "apply", nil
	default:
		return "", fmt.Errorf("unsupported terraform command %q", command)
	}
}

// ReadTemplateRunLog reads one tenant/run phase log from the local run root.
func (reader LocalReader) ReadTemplateRunLog(_ context.Context, tenantID traits.TenantID, runID traits.TemplateRunID, phase string) ([]byte, error) {
	workspacePath, err := RunWorkspacePath(reader.runRoot, tenantID, runID)
	if err != nil {
		return nil, err
	}
	if !safePathComponent(phase) {
		return nil, fmt.Errorf("phase must be a safe path component")
	}

	path := filepath.Join(workspacePath, "logs", phase+".log")
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
