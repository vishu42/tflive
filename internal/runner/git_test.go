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
