package runner

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestLocalGitRunnerClonesRef(t *testing.T) {
	t.Parallel()

	executor := &recordingCommandExecutor{}
	runner := NewLocalGitRunnerWithExecutor(executor)

	err := runner.Clone(context.Background(), "https://github.com/acme/infra-templates.git", "main", "/tmp/repo")
	if err != nil {
		t.Fatalf("Clone returned error: %v", err)
	}

	want := []recordedCommand{
		{
			dir:  "",
			name: "git",
			args: []string{"clone", "--depth", "1", "--branch", "main", "https://github.com/acme/infra-templates.git", "/tmp/repo"},
		},
	}
	if !reflect.DeepEqual(executor.commands, want) {
		t.Fatalf("commands = %#v, want %#v", executor.commands, want)
	}
}

// A run must materialise one exact commit. `git clone --branch` cannot do it —
// that flag takes a branch or tag name, not a SHA — so the commit is fetched
// directly, and shallowly.
func TestLocalGitRunnerChecksOutExactCommit(t *testing.T) {
	t.Parallel()

	executor := &recordingCommandExecutor{}
	runner := NewLocalGitRunnerWithExecutor(executor)

	err := runner.CheckoutCommit(context.Background(), "https://github.com/acme/infra-templates.git", "a1b2c3d", "/tmp/repo")
	if err != nil {
		t.Fatalf("CheckoutCommit returned error: %v", err)
	}

	want := []recordedCommand{
		{name: "git", args: []string{"init", "--quiet", "/tmp/repo"}},
		{name: "git", args: []string{"-C", "/tmp/repo", "remote", "add", "origin", "https://github.com/acme/infra-templates.git"}},
		{name: "git", args: []string{"-C", "/tmp/repo", "fetch", "--depth", "1", "origin", "a1b2c3d"}},
		{name: "git", args: []string{"-C", "/tmp/repo", "checkout", "--quiet", "FETCH_HEAD"}},
	}
	if !reflect.DeepEqual(executor.commands, want) {
		t.Fatalf("commands = %#v, want %#v", executor.commands, want)
	}
}

func TestLocalGitRunnerReportsWhichCheckoutStepFailed(t *testing.T) {
	t.Parallel()

	commandErr := errors.New("exit status 128")
	executor := &recordingCommandExecutor{
		stdout: "fatal: could not read Username\n",
		// init and remote add succeed; the fetch is what fails.
		errs: []error{nil, nil, commandErr},
	}
	runner := NewLocalGitRunnerWithExecutor(executor)

	err := runner.CheckoutCommit(context.Background(), "https://github.com/acme/infra-templates.git", "a1b2c3d", "/tmp/repo")
	if !errors.Is(err, commandErr) {
		t.Fatalf("error = %v, want commandErr", err)
	}
	var cmdErr *GitCommandError
	if !errors.As(err, &cmdErr) {
		t.Fatalf("error = %v, want *GitCommandError", err)
	}
	if cmdErr.Command != GitCommandFetch {
		t.Fatalf("cmdErr.Command = %q, want %q", cmdErr.Command, GitCommandFetch)
	}
	// It stops at the failing step rather than running checkout on an empty repo.
	if len(executor.commands) != 3 {
		t.Fatalf("commands = %d, want 3", len(executor.commands))
	}
	if !strings.Contains(cmdErr.Output, "could not read Username") {
		t.Fatalf("cmdErr.Output = %q, want command output", cmdErr.Output)
	}
}

func TestLocalGitRunnerResolvesHead(t *testing.T) {
	t.Parallel()

	executor := &recordingCommandExecutor{stdout: "abc123\n"}
	runner := NewLocalGitRunnerWithExecutor(executor)

	got, err := runner.ResolveHead(context.Background(), "/tmp/repo")
	if err != nil {
		t.Fatalf("ResolveHead returned error: %v", err)
	}

	if got != "abc123" {
		t.Fatalf("sha = %q, want abc123", got)
	}
	want := []recordedCommand{
		{
			dir:  "",
			name: "git",
			args: []string{"-C", "/tmp/repo", "rev-parse", "HEAD"},
		},
	}
	if !reflect.DeepEqual(executor.commands, want) {
		t.Fatalf("commands = %#v, want %#v", executor.commands, want)
	}
}

func TestLocalGitRunnerWrapsCloneErrorsWithCommandOutput(t *testing.T) {
	t.Parallel()

	commandErr := errors.New("exit status 128")
	executor := &recordingCommandExecutor{
		stdout: "fatal: repository not found\n",
		errs:   []error{commandErr},
	}
	runner := NewLocalGitRunnerWithExecutor(executor)

	err := runner.Clone(context.Background(), "https://github.com/acme/missing.git", "main", "/tmp/repo")
	if !errors.Is(err, commandErr) {
		t.Fatalf("error = %v, want commandErr", err)
	}
	var cmdErr *GitCommandError
	if !errors.As(err, &cmdErr) {
		t.Fatalf("error = %v, want *GitCommandError", err)
	}
	if cmdErr.Command != GitCommandClone {
		t.Fatalf("cmdErr.Command = %q, want %q", cmdErr.Command, GitCommandClone)
	}
	if !strings.Contains(cmdErr.Output, "fatal: repository not found") {
		t.Fatalf("cmdErr.Output = %q, want command output", cmdErr.Output)
	}
}
