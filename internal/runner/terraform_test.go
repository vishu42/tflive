package runner

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/vishu42/megagega/internal/traits"
)

func TestLocalProcessRunnerRunsTerraformPlan(t *testing.T) {
	t.Parallel()

	executor := &recordingTerraformCommandExecutor{}
	runner := NewLocalProcessRunnerWithExecutor(executor)

	err := runner.Run(context.Background(), TerraformCommand{
		WorkspacePath: "/tmp/megagega/runs/tenant_123/run_123",
		WorkspaceName: "mtp_acme_prod_vpc_a13f9c",
		Command:       traits.TerraformCommandPlan,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	want := []recordedTerraformCommand{
		{
			dir:  "/tmp/megagega/runs/tenant_123/run_123",
			name: "terraform",
			args: []string{"plan", "-input=false", "-no-color"},
		},
	}
	if !reflect.DeepEqual(executor.commands, want) {
		t.Fatalf("commands = %#v, want %#v", executor.commands, want)
	}
}

func TestLocalProcessRunnerRunsTerraformApply(t *testing.T) {
	t.Parallel()

	executor := &recordingTerraformCommandExecutor{}
	runner := NewLocalProcessRunnerWithExecutor(executor)

	err := runner.Run(context.Background(), TerraformCommand{
		WorkspacePath: "/tmp/megagega/runs/tenant_123/run_123",
		WorkspaceName: "mtp_acme_prod_vpc_a13f9c",
		Command:       traits.TerraformCommandApply,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	want := []recordedTerraformCommand{
		{
			dir:  "/tmp/megagega/runs/tenant_123/run_123",
			name: "terraform",
			args: []string{"apply", "-input=false", "-auto-approve", "-no-color"},
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
		command traits.TerraformCommandType
		args    []string
	}{
		{
			name:    "plan",
			command: traits.TerraformCommandPlan,
			args:    []string{"plan", "-input=false", "-no-color"},
		},
		{
			name:    "apply",
			command: traits.TerraformCommandApply,
			args:    []string{"apply", "-input=false", "-auto-approve", "-no-color"},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			executor := &recordingTerraformCommandExecutor{}
			runner := NewLocalProcessRunnerWithExecutor(executor)

			err := runner.Run(context.Background(), TerraformCommand{
				WorkspacePath: "/tmp/megagega/runs/tenant_123/run_123",
				WorkspaceName: "mtp_acme_prod_vpc_a13f9c",
				Command:       tt.command,
				ConfigJSON:    []byte(`{"enabled":true,"region":"us-east-1","replicas":3,"tags":{"env":"prod"},"zones":["us-east-1a","us-east-1b"]}`),
			})
			if err != nil {
				t.Fatalf("Run returned error: %v", err)
			}

			want := []recordedTerraformCommand{
				{
					dir:  "/tmp/megagega/runs/tenant_123/run_123",
					env:  []string{"TF_VAR_enabled=true", "TF_VAR_region=us-east-1", "TF_VAR_replicas=3", "TF_VAR_tags={\"env\":\"prod\"}", "TF_VAR_zones=[\"us-east-1a\",\"us-east-1b\"]"},
					name: "terraform",
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

	executor := &recordingTerraformCommandExecutor{}
	runner := NewLocalProcessRunnerWithExecutor(executor)

	err := runner.Run(context.Background(), TerraformCommand{
		WorkspacePath: "/tmp/megagega/runs/tenant_123/run_123",
		WorkspaceName: "mtp_acme_prod_vpc_a13f9c",
		Command:       traits.TerraformCommandSelectWorkspace,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	want := []recordedTerraformCommand{
		{
			dir:  "/tmp/megagega/runs/tenant_123/run_123",
			name: "terraform",
			args: []string{"workspace", "select", "-no-color", "mtp_acme_prod_vpc_a13f9c"},
		},
	}
	if !reflect.DeepEqual(executor.commands, want) {
		t.Fatalf("commands = %#v, want %#v", executor.commands, want)
	}
}

func TestLocalProcessRunnerCreatesMissingWorkspace(t *testing.T) {
	t.Parallel()

	executor := &recordingTerraformCommandExecutor{
		errs: []error{errors.New("workspace does not exist")},
	}
	runner := NewLocalProcessRunnerWithExecutor(executor)

	err := runner.Run(context.Background(), TerraformCommand{
		WorkspacePath: "/tmp/megagega/runs/tenant_123/run_123",
		WorkspaceName: "mtp_acme_prod_vpc_a13f9c",
		Command:       traits.TerraformCommandSelectWorkspace,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	want := []recordedTerraformCommand{
		{
			dir:  "/tmp/megagega/runs/tenant_123/run_123",
			name: "terraform",
			args: []string{"workspace", "select", "-no-color", "mtp_acme_prod_vpc_a13f9c"},
		},
		{
			dir:  "/tmp/megagega/runs/tenant_123/run_123",
			name: "terraform",
			args: []string{"workspace", "new", "-no-color", "mtp_acme_prod_vpc_a13f9c"},
		},
	}
	if !reflect.DeepEqual(executor.commands, want) {
		t.Fatalf("commands = %#v, want %#v", executor.commands, want)
	}
}

func TestLocalProcessRunnerWrapsCommandErrors(t *testing.T) {
	t.Parallel()

	commandErr := errors.New("terraform failed")
	executor := &recordingTerraformCommandExecutor{errs: []error{commandErr}}
	runner := NewLocalProcessRunnerWithExecutor(executor)

	err := runner.Run(context.Background(), TerraformCommand{
		WorkspacePath: "/tmp/megagega/runs/tenant_123/run_123",
		WorkspaceName: "mtp_acme_prod_vpc_a13f9c",
		Command:       traits.TerraformCommandPlan,
	})
	if !errors.Is(err, commandErr) {
		t.Fatalf("error = %v, want commandErr", err)
	}
	if !strings.Contains(err.Error(), "terraform plan") {
		t.Fatalf("error = %q, want terraform plan context", err.Error())
	}
}

func TestLocalProcessRunnerPassesOutputWritersToExecutor(t *testing.T) {
	t.Parallel()

	executor := &recordingTerraformCommandExecutor{
		stdout: "terraform stdout\n",
		stderr: "terraform stderr\n",
	}
	runner := NewLocalProcessRunnerWithExecutor(executor)
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err := runner.Run(context.Background(), TerraformCommand{
		WorkspacePath: "/tmp/megagega/runs/tenant_123/run_123",
		WorkspaceName: "mtp_acme_prod_vpc_a13f9c",
		Command:       traits.TerraformCommandPlan,
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

	runner := NewLocalProcessRunnerWithExecutor(&recordingTerraformCommandExecutor{})

	err := runner.Run(context.Background(), TerraformCommand{
		WorkspaceName: "mtp_acme_prod_vpc_a13f9c",
		Command:       traits.TerraformCommandPlan,
	})
	if err == nil {
		t.Fatal("Run returned nil error, want error")
	}
	if !strings.Contains(err.Error(), "workspace path") {
		t.Fatalf("error = %q, want workspace path context", err.Error())
	}
}

type recordingTerraformCommandExecutor struct {
	commands []recordedTerraformCommand
	errs     []error
	stdout   string
	stderr   string
}

func (executor *recordingTerraformCommandExecutor) Run(_ context.Context, dir string, env []string, stdout io.Writer, stderr io.Writer, name string, args ...string) error {
	executor.commands = append(executor.commands, recordedTerraformCommand{
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

type recordedTerraformCommand struct {
	dir  string
	env  []string
	name string
	args []string
}
