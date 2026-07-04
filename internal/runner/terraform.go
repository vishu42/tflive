package runner

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/vishu42/megagega/internal/traits"
)

type TerraformCommand struct {
	WorkspacePath string
	WorkspaceName string
	Command       traits.TerraformCommandType
}

type CommandExecutor interface {
	Run(ctx context.Context, dir string, name string, args ...string) error
}

type LocalProcessRunner struct {
	executor CommandExecutor
}

func NewLocalProcessRunner() *LocalProcessRunner {
	return NewLocalProcessRunnerWithExecutor(osExecCommandExecutor{})
}

func NewLocalProcessRunnerWithExecutor(executor CommandExecutor) *LocalProcessRunner {
	return &LocalProcessRunner{executor: executor}
}

func (runner *LocalProcessRunner) Run(ctx context.Context, input TerraformCommand) error {
	if strings.TrimSpace(input.WorkspacePath) == "" {
		return fmt.Errorf("workspace path is required")
	}
	if strings.TrimSpace(input.WorkspaceName) == "" {
		return fmt.Errorf("workspace name is required")
	}

	switch input.Command {
	case traits.TerraformCommandInit:
		return runner.run(ctx, input.WorkspacePath, "terraform init", "init", "-input=false", "-no-color")
	case traits.TerraformCommandSelectWorkspace:
		return runner.selectWorkspace(ctx, input)
	case traits.TerraformCommandPlan:
		return runner.run(ctx, input.WorkspacePath, "terraform plan", "plan", "-input=false", "-no-color")
	case traits.TerraformCommandApply:
		return runner.run(ctx, input.WorkspacePath, "terraform apply", "apply", "-input=false", "-auto-approve", "-no-color")
	default:
		return fmt.Errorf("unsupported terraform command %q", input.Command)
	}
}

func (runner *LocalProcessRunner) selectWorkspace(ctx context.Context, input TerraformCommand) error {
	err := runner.executor.Run(
		ctx,
		input.WorkspacePath,
		"terraform",
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
		"terraform",
		"workspace",
		"new",
		"-no-color",
		input.WorkspaceName,
	); err != nil {
		return fmt.Errorf("terraform workspace select or new: %w", err)
	}
	return nil
}

func (runner *LocalProcessRunner) run(ctx context.Context, dir string, label string, args ...string) error {
	if err := runner.executor.Run(ctx, dir, "terraform", args...); err != nil {
		return fmt.Errorf("%s: %w", label, err)
	}
	return nil
}

type osExecCommandExecutor struct{}

func (osExecCommandExecutor) Run(ctx context.Context, dir string, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
