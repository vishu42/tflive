package runner

import (
	"context"
	"errors"
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
}

func (executor *recordingTerraformCommandExecutor) Run(_ context.Context, dir string, name string, args ...string) error {
	executor.commands = append(executor.commands, recordedTerraformCommand{
		dir:  dir,
		name: name,
		args: append([]string(nil), args...),
	})
	if len(executor.errs) == 0 {
		return nil
	}
	err := executor.errs[0]
	executor.errs = executor.errs[1:]
	return err
}

type recordedTerraformCommand struct {
	dir  string
	name string
	args []string
}
