package activities

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/vishu42/tflive/internal/logsink"
	"github.com/vishu42/tflive/internal/runner"
	"github.com/vishu42/tflive/internal/traits"
)

type StatusRecorder interface {
	// RecordTemplateRunStatus persists one workflow status transition for a run.
	RecordTemplateRunStatus(context.Context, traits.TemplateRunStatusActivityInput) error
}

// TerraformRunner is the activity-local boundary for running Terraform.
//
// The production implementation shells out to the OpenTofu CLI, while tests can
// provide a fake runner to verify activity behavior without starting external
// processes.
type TerraformRunner interface {
	// RunTerraform executes the Terraform command requested by the workflow.
	RunTerraform(context.Context, traits.RunTerraformActivityInput) error
}

type TemplateRunLogStore interface {
	PutTemplateRunLog(ctx context.Context, tenantID traits.TenantID, runID traits.TemplateRunID, phase string, body io.Reader) error
}

// TemplateRunActivities groups the activity handlers registered by the worker.
//
// Temporal invokes methods on this value when TemplateRunWorkflow schedules the
// matching activity names. The struct holds the dependencies those handlers need
// outside the deterministic workflow runtime: persistence, filesystem paths, and
// local OpenTofu execution.
type TemplateRunActivities struct {
	// recorder writes status changes back to the application store.
	recorder StatusRecorder
	// runRoot is the base directory under which per-tenant, per-run workspaces are created.
	runRoot string
	// terraformRunner executes Terraform-compatible commands for RunTerraform activity calls.
	terraformRunner TerraformRunner
	// git clones template source repositories into run workspaces.
	git runner.GitRunner
}

// NewTemplateRunActivities constructs the activity handler set registered by the worker.
//
// By default it wires a local OpenTofu-backed runner backed by real subprocess
// execution. Tests may pass a TerraformRunner override, which keeps the public
// constructor small while still allowing activity tests to avoid invoking the
// OpenTofu binary.
func NewTemplateRunActivities(recorder StatusRecorder, runRoot string, terraformRunners ...TerraformRunner) *TemplateRunActivities {
	return NewTemplateRunActivitiesWithLogStore(recorder, runRoot, nil, terraformRunners...)
}

func NewTemplateRunActivitiesWithLogStore(recorder StatusRecorder, runRoot string, logStore TemplateRunLogStore, terraformRunners ...TerraformRunner) *TemplateRunActivities {
	terraformRunner := TerraformRunner(localTerraformRunner{
		runner:   runner.NewLocalProcessRunner(),
		logStore: logStore,
	})
	if len(terraformRunners) > 0 {
		terraformRunner = terraformRunners[0]
	}

	return &TemplateRunActivities{
		recorder:        recorder,
		runRoot:         runRoot,
		terraformRunner: terraformRunner,
		git:             runner.NewLocalGitRunner(),
	}
}

// RecordTemplateRunStatus records a workflow status transition in durable storage.
//
// Workflows call this as an activity because database writes are side effects and
// cannot run directly inside Temporal workflow code. The input includes tenant
// and run identifiers so the store can update the correct run without relying on
// process-local state.
func (activities *TemplateRunActivities) RecordTemplateRunStatus(ctx context.Context, input traits.TemplateRunStatusActivityInput) error {
	if err := activities.recorder.RecordTemplateRunStatus(ctx, input); err != nil {
		return fmt.Errorf("record template run status: %w", err)
	}

	return nil
}

// PrepareWorkspace creates the filesystem workspace used by later Terraform activities.
//
// The workspace path is derived from the configured run root plus tenant and run
// IDs. Those IDs are validated as single safe path components before joining, so
// callers cannot escape the run root with absolute paths or parent-directory
// traversal. The resulting path is returned to the workflow and then passed back
// into RunTerraform activity calls.
func (activities *TemplateRunActivities) PrepareWorkspace(ctx context.Context, input traits.PrepareWorkspaceActivityInput) (traits.PrepareWorkspaceActivityOutput, error) {
	workspacePath, err := logsink.RunWorkspacePath(activities.runRoot, input.TenantID, input.RunID)
	if err != nil {
		return traits.PrepareWorkspaceActivityOutput{}, err
	}

	if err := os.MkdirAll(workspacePath, 0o700); err != nil {
		return traits.PrepareWorkspaceActivityOutput{}, fmt.Errorf("prepare workspace directory: %w", err)
	}

	return traits.PrepareWorkspaceActivityOutput{WorkspacePath: workspacePath}, nil
}

// FetchSource clones the template source into the prepared run workspace.
//
// The full repository is cloned under a stable "source" directory, while the
// returned TerraformPath points at the configured template root inside that
// clone. Keeping WorkspacePath separate lets log files remain run-scoped even
// when Terraform executes from a nested module directory.
func (activities *TemplateRunActivities) FetchSource(ctx context.Context, input traits.FetchSourceActivityInput) (traits.FetchSourceActivityOutput, error) {
	rootPath, err := safeTemplateRootPath(input.RootPath)
	if err != nil {
		return traits.FetchSourceActivityOutput{}, fmt.Errorf("source root path: %w", err)
	}
	if strings.TrimSpace(input.WorkspacePath) == "" {
		return traits.FetchSourceActivityOutput{}, fmt.Errorf("workspace path is required")
	}

	sourcePath := filepath.Join(input.WorkspacePath, "source")
	git := activities.git
	if git == nil {
		git = runner.NewLocalGitRunner()
	}
	if err := git.Clone(ctx, publicGitHubRepoURL(input.RepoOwner, input.RepoName), input.SourceRef, sourcePath); err != nil {
		return traits.FetchSourceActivityOutput{}, fmt.Errorf("clone source: %w", err)
	}

	terraformPath := filepath.Clean(filepath.Join(sourcePath, rootPath))
	if err := ensureTemplateRoot(terraformPath); err != nil {
		return traits.FetchSourceActivityOutput{}, fmt.Errorf("source root %q: %w", rootPath, err)
	}

	return traits.FetchSourceActivityOutput{TerraformPath: terraformPath}, nil
}

// RunTerraform executes one Terraform phase requested by TemplateRunWorkflow.
//
// This activity is intentionally thin: it keeps Temporal-specific error context
// here and delegates command selection, log handling, and subprocess execution to
// the configured TerraformRunner implementation.
func (activities *TemplateRunActivities) RunTerraform(ctx context.Context, input traits.RunTerraformActivityInput) error {
	if err := activities.terraformRunner.RunTerraform(ctx, input); err != nil {
		return fmt.Errorf("run terraform: %w", err)
	}

	return nil
}

// safePathComponent reports whether component can be used as one path segment.
//
// The check rejects blank values, absolute paths, cleaned path rewrites, current
// or parent directory references, and nested paths. That keeps tenant and run IDs
// usable as directory names without letting them influence parent directories.
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

// localTerraformRunner adapts the shared runner package to the activity interface.
//
// It adds activity-specific concerns around Terraform-compatible execution, such as mapping
// workflow command types to log phases and opening the per-workspace log file
// before delegating to runner.LocalProcessRunner.
type localTerraformRunner struct {
	// runner owns Terraform CLI argument construction and subprocess execution.
	runner   *runner.LocalProcessRunner
	logStore TemplateRunLogStore
}

// RunTerraform writes command output to the workspace log file and runs OpenTofu.
//
// The log phase is derived from the Terraform command so each phase writes to a
// predictable file under the workspace logs directory. Stdout and stderr share
// the same writer for now, preserving command output ordering in a single phase
// log. The log file is closed after the command completes, and close errors are
// surfaced only when the command itself succeeded.
func (localRunner localTerraformRunner) RunTerraform(ctx context.Context, input traits.RunTerraformActivityInput) error {
	phase, err := logsink.PhaseForTerraformCommand(input.Command)
	if err != nil {
		return err
	}

	writer, err := logsink.NewFileSink(input.WorkspacePath).OpenPhase(phase)
	if err != nil {
		return fmt.Errorf("open terraform log: %w", err)
	}

	runErr := localRunner.runner.Run(ctx, runner.TerraformCommand{
		WorkspacePath: terraformPath(input),
		WorkspaceName: input.WorkspaceName,
		Command:       input.Command,
		ConfigJSON:    input.ConfigJSON,
		Stdout:        writer,
		Stderr:        writer,
	})
	closeErr := writer.Close()
	if closeErr != nil {
		return fmt.Errorf("close terraform log: %w", closeErr)
	}
	if localRunner.logStore != nil {
		file, err := os.Open(filepath.Join(input.WorkspacePath, "logs", phase+".log"))
		if err != nil {
			return fmt.Errorf("open terraform log for upload: %w", err)
		}
		uploadErr := localRunner.logStore.PutTemplateRunLog(ctx, input.TenantID, input.RunID, phase, file)
		closeErr := file.Close()
		if uploadErr != nil {
			return fmt.Errorf("upload terraform log: %w", uploadErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close terraform log after upload: %w", closeErr)
		}
	}
	if runErr != nil {
		return runErr
	}
	return nil
}

func terraformPath(input traits.RunTerraformActivityInput) string {
	if strings.TrimSpace(input.TerraformPath) != "" {
		return input.TerraformPath
	}
	return input.WorkspacePath
}
