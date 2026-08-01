package runner

import (
	"bytes"
	"context"
	"strings"
)

// GitRunner is the subprocess boundary for source repository operations.
type GitRunner interface {
	Clone(ctx context.Context, repoURL string, ref string, dest string) error
	ResolveHead(ctx context.Context, repoPath string) (string, error)
}

// LocalGitRunner runs git as a subprocess via the shared CommandExecutor boundary.
type LocalGitRunner struct {
	executor CommandExecutor
}

// NewLocalGitRunner returns a runner backed by the local git executable.
func NewLocalGitRunner() *LocalGitRunner {
	return NewLocalGitRunnerWithExecutor(osExecCommandExecutor{})
}

// NewLocalGitRunnerWithExecutor returns a Git runner backed by executor.
func NewLocalGitRunnerWithExecutor(executor CommandExecutor) *LocalGitRunner {
	return &LocalGitRunner{executor: executor}
}

func (runner *LocalGitRunner) Clone(ctx context.Context, repoURL string, ref string, dest string) error {
	output, err := runner.combinedOutput(ctx, "git", "clone", "--depth", "1", "--branch", ref, repoURL, dest)
	if err != nil {
		return &GitCommandError{Command: GitCommandClone, Output: strings.TrimSpace(output), Err: err}
	}
	return nil
}

func (runner *LocalGitRunner) ResolveHead(ctx context.Context, repoPath string) (string, error) {
	output, err := runner.combinedOutput(ctx, "git", "-C", repoPath, "rev-parse", "HEAD")
	if err != nil {
		return "", &GitCommandError{Command: GitCommandResolveHead, Output: strings.TrimSpace(output), Err: err}
	}
	return strings.TrimSpace(output), nil
}

// combinedOutput runs a git command and returns its combined stdout/stderr,
// mirroring exec.Cmd.CombinedOutput on top of the shared CommandExecutor.
func (runner *LocalGitRunner) combinedOutput(ctx context.Context, name string, args ...string) (string, error) {
	var output bytes.Buffer
	err := runner.executor.Run(ctx, "", nil, &output, &output, name, args...)
	return output.String(), err
}
