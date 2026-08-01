package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/vishu42/tflive/internal/traits"
)

type TerraformCommand struct {
	WorkspacePath string
	WorkspaceName string
	Command       traits.TerraformCommandType
	ConfigJSON    json.RawMessage
	// Environment contains resolved provider credentials for this subprocess only.
	Environment map[string]string
	Stdout      io.Writer
	Stderr      io.Writer
}

type LocalProcessRunner struct {
	executor CommandExecutor
}

const terraformExecutable = "tofu"

// NewLocalProcessRunner returns a runner that executes OpenTofu with os/exec.
//
// Commands run in the requested workspace directory, inherit the process
// context for cancellation, and write to the command's configured output
// writers or to the process stdout/stderr when no writers are configured.
func NewLocalProcessRunner() *LocalProcessRunner {
	return NewLocalProcessRunnerWithExecutor(osExecCommandExecutor{})
}

// NewLocalProcessRunnerWithExecutor returns a runner backed by executor.
//
// Tests and alternate adapters can provide a custom executor to observe command
// construction or to avoid spawning real OpenTofu subprocesses.
func NewLocalProcessRunnerWithExecutor(executor CommandExecutor) *LocalProcessRunner {
	return &LocalProcessRunner{executor: executor}
}

// Run validates input and executes the requested Terraform-compatible command.
//
// The runner owns the concrete OpenTofu CLI arguments for each supported
// command type. Workspace selection is special-cased so a missing workspace is
// created with `tofu workspace new` after `tofu workspace select`
// fails.
func (runner *LocalProcessRunner) Run(ctx context.Context, input TerraformCommand) error {
	if strings.TrimSpace(input.WorkspacePath) == "" {
		return fmt.Errorf("workspace path is required")
	}
	if strings.TrimSpace(input.WorkspaceName) == "" {
		return fmt.Errorf("workspace name is required")
	}

	switch input.Command {
	case traits.TerraformCommandInit:
		return runner.run(ctx, input, sortedEnvironment(input.Environment), "init", "-input=false", "-no-color")
	case traits.TerraformCommandSelectWorkspace:
		return runner.selectWorkspace(ctx, input)
	case traits.TerraformCommandPlan:
		return runner.runWithTerraformVariables(ctx, input, "plan", "-input=false", "-no-color")
	case traits.TerraformCommandApply:
		return runner.runWithTerraformVariables(ctx, input, "apply", "-input=false", "-auto-approve", "-no-color")
	case traits.TerraformCommandDestroy:
		return runner.runWithTerraformVariables(ctx, input, "destroy", "-input=false", "-auto-approve", "-no-color")
	default:
		return fmt.Errorf("unsupported terraform command %q", input.Command)
	}
}

// selectWorkspace selects the requested Terraform workspace or creates it.
//
// OpenTofu exits non-zero when the workspace does not exist, so this method
// treats a select failure as the signal to run `tofu workspace new`. If the
// creation also fails, the returned error includes context for both attempts.
func (runner *LocalProcessRunner) selectWorkspace(ctx context.Context, input TerraformCommand) error {
	stdout, stderr := outputWriters(input)
	err := runner.executor.Run(
		ctx,
		input.WorkspacePath,
		sortedEnvironment(input.Environment),
		stdout,
		stderr,
		terraformExecutable,
		"workspace",
		"select",
		"-no-color",
		input.WorkspaceName,
	)
	if err == nil {
		return nil
	}

	if err := runner.executor.Run(
		ctx,
		input.WorkspacePath,
		sortedEnvironment(input.Environment),
		stdout,
		stderr,
		terraformExecutable,
		"workspace",
		"new",
		"-no-color",
		input.WorkspaceName,
	); err != nil {
		return &CommandError{Command: traits.TerraformCommandSelectWorkspace, Err: fmt.Errorf("select or new: %w", err)}
	}
	return nil
}

// run executes a non-workspace OpenTofu command with shared error wrapping.
//
// A failure is wrapped in CommandError (using input.Command) so callers can
// identify which command failed via errors.As instead of parsing the error
// text. args are the exact OpenTofu CLI arguments passed to the command
// executor.
func (runner *LocalProcessRunner) run(ctx context.Context, input TerraformCommand, env []string, args ...string) error {
	stdout, stderr := outputWriters(input)
	err := runner.executor.Run(ctx, input.WorkspacePath, env, stdout, stderr, terraformExecutable, args...)
	if err != nil {
		return &CommandError{Command: input.Command, Err: err}
	}
	return nil
}

func (runner *LocalProcessRunner) runWithTerraformVariables(ctx context.Context, input TerraformCommand, args ...string) error {
	env, err := terraformVariableEnv(input.ConfigJSON)
	if err != nil {
		return &CommandError{Command: input.Command, Err: fmt.Errorf("variables: %w", err)}
	}
	credentialEnv := sortedEnvironment(input.Environment)
	return runner.run(ctx, input, append(credentialEnv, env...), args...)
}

// sortedEnvironment converts credential variables into deterministic NAME=value process entries.
func sortedEnvironment(values map[string]string) []string {
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	env := make([]string, 0, len(keys))
	for _, key := range keys {
		env = append(env, key+"="+values[key])
	}
	return env
}

func terraformVariableEnv(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}

	var config map[string]json.RawMessage
	if err := json.Unmarshal(raw, &config); err != nil {
		return nil, fmt.Errorf("decode config JSON: %w", err)
	}
	if config == nil {
		return nil, fmt.Errorf("config must be a JSON object")
	}
	if len(config) == 0 {
		return nil, nil
	}

	keys := make([]string, 0, len(config))
	for key := range config {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	env := make([]string, 0, len(keys))
	for _, key := range keys {
		value, err := terraformVariableValue(config[key])
		if err != nil {
			return nil, fmt.Errorf("variable %q: %w", key, err)
		}
		env = append(env, "TF_VAR_"+key+"="+value)
	}
	return env, nil
}

func terraformVariableValue(raw json.RawMessage) (string, error) {
	trimmed := strings.TrimSpace(string(raw))
	if strings.HasPrefix(trimmed, `"`) {
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return "", err
		}
		return value, nil
	}
	return trimmed, nil
}

// outputWriters returns the command-specific output writers with process defaults.
//
// Callers can pass writers to capture OpenTofu output in logs. When either
// writer is nil, the runner preserves the CLI-like default of writing to the
// current process stdout or stderr.
func outputWriters(input TerraformCommand) (io.Writer, io.Writer) {
	stdout := input.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	stderr := input.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	return stdout, stderr
}
