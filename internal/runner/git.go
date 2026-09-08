package runner

import (
	"bytes"
	"context"
	"strings"
)

// GitRunner is the subprocess boundary for source repository operations.
//
// Clone and CheckoutCommit take a credential because the remote may be private.
// It is a parameter rather than runner state on purpose: one runner serves every
// repository, and the token is scoped to a single one, so holding it would be
// both wrong and a data race. ResolveHead needs none -- it reads a local clone.
type GitRunner interface {
	Clone(ctx context.Context, repoURL string, ref string, dest string, credential GitCredential) error
	CheckoutCommit(ctx context.Context, repoURL string, commitSHA string, dest string, credential GitCredential) error
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

func (runner *LocalGitRunner) Clone(ctx context.Context, repoURL string, ref string, dest string, credential GitCredential) error {
	environment := gitEnvironment(credential)
	output, err := runner.combinedOutput(ctx, environment, "git", "clone", "--depth", "1", "--branch", ref, repoURL, dest)
	if err != nil {
		return newGitCommandError(GitCommandClone, output, credential, err)
	}
	return nil
}

// CheckoutCommit materialises one exact commit, which is what a run needs:
// a revision is identified by its resolved commit, and a ref is a moving
// target that can point somewhere else by the time the run executes.
//
// It cannot be a `git clone --branch` — that flag takes a branch or tag name
// and rejects a commit SHA — so this fetches the commit directly instead.
// The fetch stays shallow; only the one commit is transferred.
//
// The URL is passed to the fetch positionally rather than registered as a
// remote. Nothing reads remote.origin.url from this clone, and keeping it out
// of .git/config keeps the run workspace free of the repository URL -- which
// matters once that URL, or a credential alongside it, would otherwise persist
// in the directory Terraform executes from.
func (runner *LocalGitRunner) CheckoutCommit(ctx context.Context, repoURL string, commitSHA string, dest string, credential GitCredential) error {
	steps := []struct {
		command GitCommand
		args    []string
	}{
		{GitCommandInit, []string{"init", "--quiet", dest}},
		{GitCommandFetch, []string{"-C", dest, "fetch", "--depth", "1", repoURL, commitSHA}},
		{GitCommandCheckout, []string{"-C", dest, "checkout", "--quiet", "FETCH_HEAD"}},
	}
	// Every step gets the environment, not only the networked fetch. Restricting
	// it to the fetch would be correct today and silently wrong the moment a
	// checkout reaches the network -- which it does for a repository using
	// git-LFS, whose smudge filter fetches during checkout.
	environment := gitEnvironment(credential)
	for _, step := range steps {
		output, err := runner.combinedOutput(ctx, environment, "git", step.args...)
		if err != nil {
			return newGitCommandError(step.command, output, credential, err)
		}
	}
	return nil
}

func (runner *LocalGitRunner) ResolveHead(ctx context.Context, repoPath string) (string, error) {
	environment := gitEnvironment(GitCredential{})
	output, err := runner.combinedOutput(ctx, environment, "git", "-C", repoPath, "rev-parse", "HEAD")
	if err != nil {
		return "", newGitCommandError(GitCommandResolveHead, output, GitCredential{}, err)
	}
	return strings.TrimSpace(output), nil
}

// combinedOutput runs a git command and returns its combined stdout/stderr,
// mirroring exec.Cmd.CombinedOutput on top of the shared CommandExecutor.
func (runner *LocalGitRunner) combinedOutput(ctx context.Context, env []string, name string, args ...string) (string, error) {
	var output bytes.Buffer
	err := runner.executor.Run(ctx, "", env, &output, &output, name, args...)
	return output.String(), err
}
