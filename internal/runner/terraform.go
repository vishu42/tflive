package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/vishu42/tflive/internal/domain"
)

type TerraformCommand struct {
	WorkspacePath string
	WorkspaceName string
	Command       domain.TerraformCommandType
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

// PlanFileName is the saved plan inside the Terraform root module directory.
// The plan phase writes it, the executor uploads it, and the apply phase puts
// it back at the same path before applying it.
const PlanFileName = "tfplan"

// Result reports what a command found out. A plan and an auto-approved apply
// fill it in.
type Result struct {
	// HasChanges is whether the plan would change anything, read from
	// `-detailed-exitcode`: 0 means no changes, 2 means changes. For an
	// auto-approved apply, whether it changed anything.
	HasChanges bool
	// Summary counts the changes, from `tofu show -json` on the saved plan, or
	// from an auto-approved apply's "Apply complete!" line. Zero when there are
	// none.
	Summary domain.PlanSummary
}

// Run validates input and executes the requested Terraform-compatible command.
//
// The runner owns the concrete OpenTofu CLI arguments for each supported
// command type. Workspace selection is special-cased so a missing workspace is
// created with `tofu workspace new` after `tofu workspace select`
// fails.
//
// A plan saves itself to PlanFileName. Apply and destroy both apply that saved
// plan and nothing else: what was approved is exactly what runs, the variables
// come from inside the plan, and tofu refuses the file outright if the state
// has moved since it was written. An auto-approved apply has no saved plan, so
// it plans and applies in one command, with the run's variables.
func (runner *LocalProcessRunner) Run(ctx context.Context, input TerraformCommand) (Result, error) {
	if strings.TrimSpace(input.WorkspacePath) == "" {
		return Result{}, fmt.Errorf("workspace path is required")
	}
	if strings.TrimSpace(input.WorkspaceName) == "" {
		return Result{}, fmt.Errorf("workspace name is required")
	}

	switch input.Command {
	case domain.TerraformCommandInit:
		return Result{}, runner.run(ctx, input, sortedEnvironment(input.Environment), "init", "-input=false", "-no-color")
	case domain.TerraformCommandSelectWorkspace:
		return Result{}, runner.selectWorkspace(ctx, input)
	case domain.TerraformCommandPlan, domain.TerraformCommandPlanDestroy:
		return runner.plan(ctx, input)
	case domain.TerraformCommandApply, domain.TerraformCommandDestroy:
		return Result{}, runner.run(ctx, input, sortedEnvironment(input.Environment), "apply", "-input=false", "-auto-approve", "-no-color", PlanFileName)
	case domain.TerraformCommandApplyAutoApprove:
		return runner.applyAutoApprove(ctx, input)
	default:
		return Result{}, fmt.Errorf("unsupported terraform command %q", input.Command)
	}
}

// plan runs `tofu plan` into PlanFileName and, when it has changes, counts
// them. With -detailed-exitcode tofu exits 2 for a successful plan that has
// changes, so exit 2 is success here, not failure.
func (runner *LocalProcessRunner) plan(ctx context.Context, input TerraformCommand) (Result, error) {
	args := []string{"plan", "-input=false", "-no-color", "-detailed-exitcode", "-out=" + PlanFileName}
	if input.Command == domain.TerraformCommandPlanDestroy {
		args = append(args, "-destroy")
	}
	err := runner.runWithTerraformVariables(ctx, input, args...)
	if err == nil {
		return Result{}, nil
	}
	var exitErr interface{ ExitCode() int }
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 2 {
		return Result{}, err
	}

	summary, err := runner.summarize(ctx, input)
	if err != nil {
		return Result{}, err
	}
	return Result{HasChanges: true, Summary: summary}, nil
}

// applyAutoApprove plans and applies in one command, with no saved plan, and
// counts what the apply did from its closing "Apply complete!" line. The
// output still goes to the command's writers; the counts are read from a copy.
func (runner *LocalProcessRunner) applyAutoApprove(ctx context.Context, input TerraformCommand) (Result, error) {
	var output bytes.Buffer
	stdout, _ := outputWriters(input)
	input.Stdout = io.MultiWriter(stdout, &output)
	if err := runner.runWithTerraformVariables(ctx, input, "apply", "-input=false", "-auto-approve", "-no-color"); err != nil {
		return Result{}, err
	}
	summary := summarizeApplyOutput(output.String())
	return Result{HasChanges: summary != domain.PlanSummary{}, Summary: summary}, nil
}

// applyCompleteLine is tofu's closing line for a successful apply, such as
// "Apply complete! Resources: 1 imported, 2 added, 0 changed, 1 destroyed."
var applyCompleteLine = regexp.MustCompile(`Apply complete! Resources: ([^\n]*)`)

// applyCount is one "N verb" count on that line.
var applyCount = regexp.MustCompile(`(\d+) (added|changed|destroyed)`)

// summarizeApplyOutput counts an apply's changes from its "Apply complete!"
// line. Counts it does not track, such as imported, are ignored, and output
// without the line counts as no changes.
func summarizeApplyOutput(output string) domain.PlanSummary {
	var summary domain.PlanSummary
	line := applyCompleteLine.FindStringSubmatch(output)
	if line == nil {
		return summary
	}
	for _, count := range applyCount.FindAllStringSubmatch(line[1], -1) {
		n, err := strconv.Atoi(count[1])
		if err != nil {
			continue
		}
		switch count[2] {
		case "added":
			summary.Add = n
		case "changed":
			summary.Change = n
		case "destroyed":
			summary.Destroy = n
		}
	}
	return summary
}

// summarize reads the saved plan back as JSON and counts its resource changes.
// The JSON holds variable values and sensitive attributes in plaintext, so it
// is parsed here and only the counts leave the executor; it is never written
// to the log.
func (runner *LocalProcessRunner) summarize(ctx context.Context, input TerraformCommand) (domain.PlanSummary, error) {
	var stdout, stderr bytes.Buffer
	if err := runner.executor.Run(ctx, input.WorkspacePath, sortedEnvironment(input.Environment), &stdout, &stderr, terraformExecutable, "show", "-json", "-no-color", PlanFileName); err != nil {
		return domain.PlanSummary{}, &CommandError{Command: input.Command, Err: fmt.Errorf("show saved plan: %w: %s", err, strings.TrimSpace(stderr.String()))}
	}
	summary, err := summarizePlanJSON(stdout.Bytes())
	if err != nil {
		return domain.PlanSummary{}, &CommandError{Command: input.Command, Err: err}
	}
	return summary, nil
}

// summarizePlanJSON counts a `tofu show -json` plan's resource changes the way
// tofu's own "Plan: N to add, N to change, N to destroy" line does: a
// replacement is one add and one destroy, and reads and no-ops count as
// nothing.
func summarizePlanJSON(body []byte) (domain.PlanSummary, error) {
	var plan struct {
		ResourceChanges []struct {
			Change struct {
				Actions []string `json:"actions"`
			} `json:"change"`
		} `json:"resource_changes"`
	}
	if err := json.Unmarshal(body, &plan); err != nil {
		return domain.PlanSummary{}, fmt.Errorf("decode saved plan: %w", err)
	}
	var summary domain.PlanSummary
	for _, resourceChange := range plan.ResourceChanges {
		for _, action := range resourceChange.Change.Actions {
			switch action {
			case "create":
				summary.Add++
			case "update":
				summary.Change++
			case "delete":
				summary.Destroy++
			}
		}
	}
	return summary, nil
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
		return &CommandError{Command: domain.TerraformCommandSelectWorkspace, Err: fmt.Errorf("select or new: %w", err)}
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
