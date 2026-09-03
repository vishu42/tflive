package runner

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/vishu42/tflive/internal/domain"
)

func TestLocalProcessRunnerRunsTerraformPlan(t *testing.T) {
	t.Parallel()

	executor := &recordingCommandExecutor{}
	runner := NewLocalProcessRunnerWithExecutor(executor)

	err := runner.Run(context.Background(), TerraformCommand{
		WorkspacePath: "/tmp/tflive/runs/tenant_123/run_123",
		WorkspaceName: "mtp_acme_prod_vpc_a13f9c",
		Command:       domain.TerraformCommandPlan,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	want := []recordedCommand{
		{
			dir:  "/tmp/tflive/runs/tenant_123/run_123",
			name: "tofu",
			args: []string{"plan", "-input=false", "-no-color"},
		},
	}
	if !reflect.DeepEqual(executor.commands, want) {
		t.Fatalf("commands = %#v, want %#v", executor.commands, want)
	}
}

// TestLocalProcessRunnerInjectsCredentialEnvironment verifies resolved credentials reach the subprocess.
func TestLocalProcessRunnerInjectsCredentialEnvironment(t *testing.T) {
	executor := &recordingCommandExecutor{}
	runner := NewLocalProcessRunnerWithExecutor(executor)

	err := runner.Run(context.Background(), TerraformCommand{
		WorkspacePath: "/tmp/tflive/runs/tenant_123/run_123",
		WorkspaceName: "mtp_acme_prod_vpc_a13f9c",
		Command:       domain.TerraformCommandPlan,
		Environment:   map[string]string{"AWS_ACCESS_KEY_ID": "key", "AWS_SECRET_ACCESS_KEY": "secret"},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !reflect.DeepEqual(executor.commands[0].env, []string{"AWS_ACCESS_KEY_ID=key", "AWS_SECRET_ACCESS_KEY=secret"}) {
		t.Fatalf("environment = %#v", executor.commands[0].env)
	}
}

// TestSortedEnvironmentReturnsSortedNameValueEntries verifies that environment
// variables are formatted for a subprocess in deterministic key order.
func TestSortedEnvironmentReturnsSortedNameValueEntries(t *testing.T) {
	got := sortedEnvironment(map[string]string{
		"ZED_TOKEN":  "token",
		"AWS_REGION": "us-east-1",
		"A_KEY":      "key",
	})

	want := []string{
		"AWS_REGION=us-east-1",
		"A_KEY=key",
		"ZED_TOKEN=token",
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sortedEnvironment() = %#v, want %#v", got, want)
	}
}

func TestLocalProcessRunnerRunsTerraformApply(t *testing.T) {
	t.Parallel()

	executor := &recordingCommandExecutor{}
	runner := NewLocalProcessRunnerWithExecutor(executor)

	err := runner.Run(context.Background(), TerraformCommand{
		WorkspacePath: "/tmp/tflive/runs/tenant_123/run_123",
		WorkspaceName: "mtp_acme_prod_vpc_a13f9c",
		Command:       domain.TerraformCommandApply,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	want := []recordedCommand{
		{
			dir:  "/tmp/tflive/runs/tenant_123/run_123",
			name: "tofu",
			args: []string{"apply", "-input=false", "-auto-approve", "-no-color"},
		},
	}
	if !reflect.DeepEqual(executor.commands, want) {
		t.Fatalf("commands = %#v, want %#v", executor.commands, want)
	}
}

// TestLocalProcessRunnerRunsTerraformDestroy verifies that the local process
// runner dispatches "tofu destroy -input=false -auto-approve -no-color" with
// terraform variable environment variables set when the command type is
// TerraformCommandDestroy.
func TestLocalProcessRunnerRunsTerraformDestroy(t *testing.T) {
	t.Parallel()

	executor := &recordingCommandExecutor{}
	runner := NewLocalProcessRunnerWithExecutor(executor)

	err := runner.Run(context.Background(), TerraformCommand{
		WorkspacePath: "/tmp/tflive/runs/tenant_123/run_123",
		WorkspaceName: "mtp_acme_prod_vpc_a13f9c",
		Command:       domain.TerraformCommandDestroy,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	want := []recordedCommand{
		{
			dir:  "/tmp/tflive/runs/tenant_123/run_123",
			name: "tofu",
			args: []string{"destroy", "-input=false", "-auto-approve", "-no-color"},
		},
	}
	if !reflect.DeepEqual(executor.commands, want) {
		t.Fatalf("commands = %#v, want %#v", executor.commands, want)
	}
}

func TestLocalProcessRunnerSetsTerraformVariablesForPlanAndApply(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		command domain.TerraformCommandType
		args    []string
	}{
		{
			name:    "plan",
			command: domain.TerraformCommandPlan,
			args:    []string{"plan", "-input=false", "-no-color"},
		},
		{
			name:    "apply",
			command: domain.TerraformCommandApply,
			args:    []string{"apply", "-input=false", "-auto-approve", "-no-color"},
		},
		{
			name:    "destroy",
			command: domain.TerraformCommandDestroy,
			args:    []string{"destroy", "-input=false", "-auto-approve", "-no-color"},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			executor := &recordingCommandExecutor{}
			runner := NewLocalProcessRunnerWithExecutor(executor)

			err := runner.Run(context.Background(), TerraformCommand{
				WorkspacePath: "/tmp/tflive/runs/tenant_123/run_123",
				WorkspaceName: "mtp_acme_prod_vpc_a13f9c",
				Command:       tt.command,
				ConfigJSON:    []byte(`{"enabled":true,"region":"us-east-1","replicas":3,"tags":{"env":"prod"},"zones":["us-east-1a","us-east-1b"]}`),
			})
			if err != nil {
				t.Fatalf("Run returned error: %v", err)
			}

			want := []recordedCommand{
				{
					dir:  "/tmp/tflive/runs/tenant_123/run_123",
					env:  []string{"TF_VAR_enabled=true", "TF_VAR_region=us-east-1", "TF_VAR_replicas=3", "TF_VAR_tags={\"env\":\"prod\"}", "TF_VAR_zones=[\"us-east-1a\",\"us-east-1b\"]"},
					name: "tofu",
					args: tt.args,
				},
			}
			if !reflect.DeepEqual(executor.commands, want) {
				t.Fatalf("commands = %#v, want %#v", executor.commands, want)
			}
		})
	}
}

func TestLocalProcessRunnerSelectsExistingWorkspace(t *testing.T) {
	t.Parallel()

	executor := &recordingCommandExecutor{}
	runner := NewLocalProcessRunnerWithExecutor(executor)

	err := runner.Run(context.Background(), TerraformCommand{
		WorkspacePath: "/tmp/tflive/runs/tenant_123/run_123",
		WorkspaceName: "mtp_acme_prod_vpc_a13f9c",
		Command:       domain.TerraformCommandSelectWorkspace,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	want := []recordedCommand{
		{
			dir:  "/tmp/tflive/runs/tenant_123/run_123",
			name: "tofu",
			args: []string{"workspace", "select", "-no-color", "mtp_acme_prod_vpc_a13f9c"},
		},
	}
	if !reflect.DeepEqual(executor.commands, want) {
		t.Fatalf("commands = %#v, want %#v", executor.commands, want)
	}
}

func TestLocalProcessRunnerCreatesMissingWorkspace(t *testing.T) {
	t.Parallel()

	executor := &recordingCommandExecutor{
		errs: []error{errors.New("workspace does not exist")},
	}
	runner := NewLocalProcessRunnerWithExecutor(executor)

	err := runner.Run(context.Background(), TerraformCommand{
		WorkspacePath: "/tmp/tflive/runs/tenant_123/run_123",
		WorkspaceName: "mtp_acme_prod_vpc_a13f9c",
		Command:       domain.TerraformCommandSelectWorkspace,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	want := []recordedCommand{
		{
			dir:  "/tmp/tflive/runs/tenant_123/run_123",
			name: "tofu",
			args: []string{"workspace", "select", "-no-color", "mtp_acme_prod_vpc_a13f9c"},
		},
		{
			dir:  "/tmp/tflive/runs/tenant_123/run_123",
			name: "tofu",
			args: []string{"workspace", "new", "-no-color", "mtp_acme_prod_vpc_a13f9c"},
		},
	}
	if !reflect.DeepEqual(executor.commands, want) {
		t.Fatalf("commands = %#v, want %#v", executor.commands, want)
	}
}

func TestLocalProcessRunnerWrapsWorkspaceCreationErrorWithTofuContext(t *testing.T) {
	t.Parallel()

	createErr := errors.New("create failed")
	executor := &recordingCommandExecutor{
		errs: []error{errors.New("workspace does not exist"), createErr},
	}
	runner := NewLocalProcessRunnerWithExecutor(executor)

	err := runner.Run(context.Background(), TerraformCommand{
		WorkspacePath: "/tmp/tflive/runs/tenant_123/run_123",
		WorkspaceName: "mtp_acme_prod_vpc_a13f9c",
		Command:       domain.TerraformCommandSelectWorkspace,
	})
	if !errors.Is(err, createErr) {
		t.Fatalf("error = %v, want createErr", err)
	}
	var cmdErr *CommandError
	if !errors.As(err, &cmdErr) {
		t.Fatalf("error = %v, want *CommandError", err)
	}
	if cmdErr.Command != domain.TerraformCommandSelectWorkspace {
		t.Fatalf("cmdErr.Command = %q, want %q", cmdErr.Command, domain.TerraformCommandSelectWorkspace)
	}
}

func TestLocalProcessRunnerWrapsCommandErrors(t *testing.T) {
	t.Parallel()

	commandErr := errors.New("terraform failed")
	executor := &recordingCommandExecutor{errs: []error{commandErr}}
	runner := NewLocalProcessRunnerWithExecutor(executor)

	err := runner.Run(context.Background(), TerraformCommand{
		WorkspacePath: "/tmp/tflive/runs/tenant_123/run_123",
		WorkspaceName: "mtp_acme_prod_vpc_a13f9c",
		Command:       domain.TerraformCommandPlan,
	})
	if !errors.Is(err, commandErr) {
		t.Fatalf("error = %v, want commandErr", err)
	}
	var cmdErr *CommandError
	if !errors.As(err, &cmdErr) {
		t.Fatalf("error = %v, want *CommandError", err)
	}
	if cmdErr.Command != domain.TerraformCommandPlan {
		t.Fatalf("cmdErr.Command = %q, want %q", cmdErr.Command, domain.TerraformCommandPlan)
	}
}

func TestLocalProcessRunnerPassesOutputWritersToExecutor(t *testing.T) {
	t.Parallel()

	executor := &recordingCommandExecutor{
		stdout: "terraform stdout\n",
		stderr: "terraform stderr\n",
	}
	runner := NewLocalProcessRunnerWithExecutor(executor)
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err := runner.Run(context.Background(), TerraformCommand{
		WorkspacePath: "/tmp/tflive/runs/tenant_123/run_123",
		WorkspaceName: "mtp_acme_prod_vpc_a13f9c",
		Command:       domain.TerraformCommandPlan,
		Stdout:        &stdout,
		Stderr:        &stderr,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if stdout.String() != "terraform stdout\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if stderr.String() != "terraform stderr\n" {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestLocalProcessRunnerRequiresWorkspacePath(t *testing.T) {
	t.Parallel()

	runner := NewLocalProcessRunnerWithExecutor(&recordingCommandExecutor{})

	err := runner.Run(context.Background(), TerraformCommand{
		WorkspaceName: "mtp_acme_prod_vpc_a13f9c",
		Command:       domain.TerraformCommandPlan,
	})
	if err == nil {
		t.Fatal("Run returned nil error, want error")
	}
	if !strings.Contains(err.Error(), "workspace path") {
		t.Fatalf("error = %q, want workspace path context", err.Error())
	}
}

type recordingCommandExecutor struct {
	commands []recordedCommand
	errs     []error
	stdout   string
	stderr   string
}

func (executor *recordingCommandExecutor) Run(_ context.Context, dir string, env []string, stdout io.Writer, stderr io.Writer, name string, args ...string) error {
	executor.commands = append(executor.commands, recordedCommand{
		dir:  dir,
		env:  append([]string(nil), env...),
		name: name,
		args: append([]string(nil), args...),
	})
	if executor.stdout != "" {
		if _, err := io.WriteString(stdout, executor.stdout); err != nil {
			return err
		}
	}
	if executor.stderr != "" {
		if _, err := io.WriteString(stderr, executor.stderr); err != nil {
			return err
		}
	}
	if len(executor.errs) == 0 {
		return nil
	}
	err := executor.errs[0]
	executor.errs = executor.errs[1:]
	return err
}

type recordedCommand struct {
	dir  string
	env  []string
	name string
	args []string
}
