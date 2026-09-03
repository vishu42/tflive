package activities

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/vishu42/tflive/internal/domain"
	"github.com/vishu42/tflive/internal/logsink"
	"github.com/vishu42/tflive/internal/runner"
)

type StatusRecorder interface {
	// RecordTemplateRunStatus persists one workflow status transition for a run.
	RecordTemplateRunStatus(context.Context, domain.TemplateRunStatusActivityInput) error
}

// TerraformRunner is the activity-local boundary for running Terraform.
//
// The production implementation shells out to the OpenTofu CLI, while tests can
// provide a fake runner to verify activity behavior without starting external
// processes.
type TerraformRunner interface {
	// RunTerraform executes the Terraform command requested by the workflow.
	RunTerraform(context.Context, domain.RunTerraformActivityInput) error
}

// TemplateRunLogStore persists output produced by a template run.
type TemplateRunLogStore interface {
	// PutTemplateRunLog stores the output for a run phase so it can be retrieved later.
	PutTemplateRunLog(ctx context.Context, tenantID domain.TenantID, runID domain.TemplateRunID, phase string, body io.Reader) error
}

type CredentialReader interface {
	// ListCredentialsForStackTemplate returns encrypted credentials inherited by a template.
	ListCredentialsForStackTemplate(context.Context, domain.TenantID, domain.StackTemplateID) ([]domain.CredentialSet, error)
}

type CredentialDecryptor interface {
	// Decrypt opens one credential only inside the worker execution boundary.
	Decrypt(string) (string, error)
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
	git                 runner.GitRunner
	credentialReader    CredentialReader
	credentialDecryptor CredentialDecryptor
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
	return NewTemplateRunActivitiesWithCredentials(recorder, runRoot, logStore, nil, nil, terraformRunners...)
}

// NewTemplateRunActivitiesWithCredentials wires runtime credential lookup and decryption into activities.
func NewTemplateRunActivitiesWithCredentials(recorder StatusRecorder, runRoot string, logStore TemplateRunLogStore, credentialReader CredentialReader, credentialDecryptor CredentialDecryptor, terraformRunners ...TerraformRunner) *TemplateRunActivities {
	terraformRunner := TerraformRunner(localTerraformRunner{
		runner:   runner.NewLocalProcessRunner(),
		logStore: logStore,
	})
	if len(terraformRunners) > 0 {
		terraformRunner = terraformRunners[0]
	}

	return &TemplateRunActivities{
		recorder:            recorder,
		runRoot:             runRoot,
		terraformRunner:     terraformRunner,
		git:                 runner.NewLocalGitRunner(),
		credentialReader:    credentialReader,
		credentialDecryptor: credentialDecryptor,
	}
}

// RecordTemplateRunStatus records a workflow status transition in durable storage.
//
// Workflows call this as an activity because database writes are side effects and
// cannot run directly inside Temporal workflow code. The input includes tenant
// and run identifiers so the store can update the correct run without relying on
// process-local state.
func (activities *TemplateRunActivities) RecordTemplateRunStatus(ctx context.Context, input domain.TemplateRunStatusActivityInput) error {
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
func (activities *TemplateRunActivities) PrepareWorkspace(ctx context.Context, input domain.PrepareWorkspaceActivityInput) (domain.PrepareWorkspaceActivityOutput, error) {
	workspacePath, err := logsink.RunWorkspacePath(activities.runRoot, input.TenantID, input.RunID)
	if err != nil {
		return domain.PrepareWorkspaceActivityOutput{}, err
	}

	if err := os.MkdirAll(workspacePath, 0o700); err != nil {
		return domain.PrepareWorkspaceActivityOutput{}, fmt.Errorf("prepare workspace directory: %w", err)
	}

	return domain.PrepareWorkspaceActivityOutput{WorkspacePath: workspacePath}, nil
}

// FetchSource clones the template source into the prepared run workspace.
//
// The full repository is cloned under a stable "source" directory, while the
// returned TerraformPath points at the configured template root inside that
// clone. Keeping WorkspacePath separate lets log files remain run-scoped even
// when Terraform executes from a nested module directory.
func (activities *TemplateRunActivities) FetchSource(ctx context.Context, input domain.FetchSourceActivityInput) (domain.FetchSourceActivityOutput, error) {
	rootPath, err := safeTemplateRootPath(input.RootPath)
	if err != nil {
		return domain.FetchSourceActivityOutput{}, fmt.Errorf("source root path: %w", err)
	}
	if strings.TrimSpace(input.WorkspacePath) == "" {
		return domain.FetchSourceActivityOutput{}, fmt.Errorf("workspace path is required")
	}

	sourcePath := filepath.Join(input.WorkspacePath, "source")
	git := activities.git
	if git == nil {
		git = runner.NewLocalGitRunner()
	}
	repoURL := publicGitHubRepoURL(input.RepoOwner, input.RepoName)
	// The commit is what the revision means, so it is what runs. Checking out a
	// ref would let the source move between a plan and the apply that was
	// approved against it, and would ignore the revision entirely once an
	// upgrade pointed the component somewhere the installed ref never reached.
	//
	// The ref remains the fallback only for runs queued before the commit was
	// threaded through; those payloads have no commit to check out. Once such a
	// queue has drained the branch is dead, and with it the last path by which a
	// run resolves its own source.
	if commitSHA := strings.TrimSpace(input.ResolvedCommitSHA); commitSHA != "" {
		if err := git.CheckoutCommit(ctx, repoURL, commitSHA, sourcePath); err != nil {
			return domain.FetchSourceActivityOutput{}, fmt.Errorf("checkout source commit %s: %w", commitSHA, err)
		}
	} else if err := git.Clone(ctx, repoURL, input.SourceRef, sourcePath); err != nil {
		return domain.FetchSourceActivityOutput{}, fmt.Errorf("clone source: %w", err)
	}

	terraformPath := filepath.Clean(filepath.Join(sourcePath, rootPath))
	if err := ensureTemplateRoot(terraformPath); err != nil {
		return domain.FetchSourceActivityOutput{}, fmt.Errorf("source root %q: %w", rootPath, err)
	}

	return domain.FetchSourceActivityOutput{TerraformPath: terraformPath}, nil
}

// RunTerraform executes one Terraform phase requested by TemplateRunWorkflow.
//
// This activity resolves and decrypts credentials only at worker runtime, then
// delegates command selection, log handling, and subprocess execution to the
// configured TerraformRunner implementation. It never puts plaintext credentials
// into workflow input or API responses.
func (activities *TemplateRunActivities) RunTerraform(ctx context.Context, input domain.RunTerraformActivityInput) error {
	if activities.credentialReader != nil {
		if activities.credentialDecryptor == nil {
			return fmt.Errorf("resolve terraform credentials: decryptor is unavailable")
		}
		environment, err := resolveCredentialEnvironment(ctx, activities.credentialReader, activities.credentialDecryptor, input.TenantID, input.StackTemplateID)
		if err != nil {
			return fmt.Errorf("resolve terraform credentials: %w", err)
		}
		input.Environment = environment
	}
	if err := activities.terraformRunner.RunTerraform(ctx, input); err != nil {
		return fmt.Errorf("run terraform: %w", err)
	}

	return nil
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
func (localRunner localTerraformRunner) RunTerraform(ctx context.Context, input domain.RunTerraformActivityInput) error {
	phase, err := logsink.PhaseForTerraformCommand(input.Command)
	if err != nil {
		return err
	}

	writer, err := logsink.NewFileSink(input.WorkspacePath).OpenPhase(phase)
	if err != nil {
		return fmt.Errorf("open terraform log: %w", err)
	}

	redactingWriter := newRedactingWriter(writer, credentialValues(input.Environment))
	runErr := localRunner.runner.Run(ctx, runner.TerraformCommand{
		WorkspacePath: terraformPath(input),
		WorkspaceName: input.WorkspaceName,
		Command:       input.Command,
		ConfigJSON:    input.ConfigJSON,
		Environment:   input.Environment,
		Stdout:        redactingWriter,
		Stderr:        redactingWriter,
	})
	closeErr := redactingWriter.Close()
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

type redactingWriter struct {
	writer  io.WriteCloser
	secrets []string
}

// newRedactingWriter wraps a log destination and replaces exact credential values.
func newRedactingWriter(writer io.WriteCloser, secrets []string) *redactingWriter {
	return &redactingWriter{writer: writer, secrets: secrets}
}

// Write redacts configured secrets before forwarding command output to the destination.
func (writer *redactingWriter) Write(body []byte) (int, error) {
	redacted := append([]byte(nil), body...)
	for _, secret := range writer.secrets {
		redacted = bytes.ReplaceAll(redacted, []byte(secret), []byte("******"))
	}
	_, err := writer.writer.Write(redacted)
	if err != nil {
		return 0, err
	}
	return len(body), nil
}

// Close releases the wrapped log destination.
func (writer *redactingWriter) Close() error { return writer.writer.Close() }

// credentialValues extracts non-empty values so Terraform logs can be scrubbed.
func credentialValues(environment map[string]string) []string {
	secrets := make([]string, 0, len(environment))
	for _, value := range environment {
		if value != "" {
			secrets = append(secrets, value)
		}
	}
	sort.Slice(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
	return secrets
}

// resolveCredentialEnvironment decrypts inherited Stack credentials and applies StackTemplate overrides.
func resolveCredentialEnvironment(ctx context.Context, reader CredentialReader, decryptor CredentialDecryptor, tenantID domain.TenantID, stackTemplateID domain.StackTemplateID) (map[string]string, error) {
	credentials, err := reader.ListCredentialsForStackTemplate(ctx, tenantID, stackTemplateID)
	if err != nil {
		return nil, err
	}
	environment := make(map[string]string, len(credentials))
	for _, credential := range credentials {
		if credential.StackTemplateID != "" {
			continue
		}
		value, err := decryptor.Decrypt(credential.Ciphertext)
		if err != nil {
			return nil, fmt.Errorf("decrypt %q: %w", credential.Name, err)
		}
		environment[credential.Name] = value
	}
	for _, credential := range credentials {
		if credential.StackTemplateID == "" {
			continue
		}
		value, err := decryptor.Decrypt(credential.Ciphertext)
		if err != nil {
			return nil, fmt.Errorf("decrypt %q: %w", credential.Name, err)
		}
		environment[credential.Name] = value
	}
	return environment, nil
}

func terraformPath(input domain.RunTerraformActivityInput) string {
	if strings.TrimSpace(input.TerraformPath) != "" {
		return input.TerraformPath
	}
	return input.WorkspacePath
}
