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

	executor := &recordingGitCommandExecutor{}
	runner := NewLocalGitRunnerWithExecutor(executor)

	err := runner.Clone(context.Background(), "https://github.com/acme/infra-templates.git", "main", "/tmp/repo")
	if err != nil {
		t.Fatalf("Clone returned error: %v", err)
	}

	want := []recordedGitCommand{
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

	executor := &recordingGitCommandExecutor{output: []byte("abc123\n")}
	runner := NewLocalGitRunnerWithExecutor(executor)

	got, err := runner.ResolveHead(context.Background(), "/tmp/repo")
	if err != nil {
		t.Fatalf("ResolveHead returned error: %v", err)
	}

	if got != "abc123" {
		t.Fatalf("sha = %q, want abc123", got)
	}
	want := []recordedGitCommand{
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
	executor := &recordingGitCommandExecutor{
		output: []byte("fatal: repository not found\n"),
		err:    commandErr,
	}
	runner := NewLocalGitRunnerWithExecutor(executor)

	err := runner.Clone(context.Background(), "https://github.com/acme/missing.git", "main", "/tmp/repo")
	if !errors.Is(err, commandErr) {
		t.Fatalf("error = %v, want commandErr", err)
	}
	if !strings.Contains(err.Error(), "git clone") {
		t.Fatalf("error = %q, want git clone context", err.Error())
	}
	if !strings.Contains(err.Error(), "fatal: repository not found") {
		t.Fatalf("error = %q, want command output", err.Error())
	}
}

type recordingGitCommandExecutor struct {
	commands []recordedGitCommand
	output   []byte
	err      error
}

func (executor *recordingGitCommandExecutor) CombinedOutput(_ context.Context, dir string, name string, args ...string) ([]byte, error) {
	executor.commands = append(executor.commands, recordedGitCommand{
		dir:  dir,
		name: name,
		args: append([]string(nil), args...),
	})
	return executor.output, executor.err
}

type recordedGitCommand struct {
	dir  string
	name string
	args []string
}
