package runner

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// GitRunner is the subprocess boundary for source repository operations.
type GitRunner interface {
	Clone(ctx context.Context, repoURL string, ref string, dest string) error
	ResolveHead(ctx context.Context, repoPath string) (string, error)
}

// GitCommandExecutor runs a git command and returns combined stdout/stderr.
type GitCommandExecutor interface {
	CombinedOutput(ctx context.Context, dir string, name string, args ...string) ([]byte, error)
}

type LocalGitRunner struct {
	executor GitCommandExecutor
}

// NewLocalGitRunner returns a runner backed by the local git executable.
func NewLocalGitRunner() *LocalGitRunner {
	return NewLocalGitRunnerWithExecutor(osExecGitCommandExecutor{})
}

// NewLocalGitRunnerWithExecutor returns a Git runner backed by executor.
func NewLocalGitRunnerWithExecutor(executor GitCommandExecutor) *LocalGitRunner {
	return &LocalGitRunner{executor: executor}
}

func (runner *LocalGitRunner) Clone(ctx context.Context, repoURL string, ref string, dest string) error {
	output, err := runner.executor.CombinedOutput(ctx, "", "git", "clone", "--depth", "1", "--branch", ref, repoURL, dest)
	if err != nil {
		return fmt.Errorf("git clone: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func (runner *LocalGitRunner) ResolveHead(ctx context.Context, repoPath string) (string, error) {
	output, err := runner.executor.CombinedOutput(ctx, "", "git", "-C", repoPath, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("git rev-parse HEAD: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output)), nil
}

type osExecGitCommandExecutor struct{}

func (osExecGitCommandExecutor) CombinedOutput(ctx context.Context, dir string, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	return cmd.CombinedOutput()
}
