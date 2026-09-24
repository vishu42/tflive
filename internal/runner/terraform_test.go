package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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

	_, err := runner.Run(context.Background(), TerraformCommand{
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
			args: []string{"plan", "-input=false", "-no-color", "-detailed-exitcode", "-out=tfplan"},
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

	_, err := runner.Run(context.Background(), TerraformCommand{
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

	_, err := runner.Run(context.Background(), TerraformCommand{
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
			args: []string{"apply", "-input=false", "-auto-approve", "-no-color", "tfplan"},
		},
	}
	if !reflect.DeepEqual(executor.commands, want) {
		t.Fatalf("commands = %#v, want %#v", executor.commands, want)
	}
}

// TestLocalProcessRunnerRunsTerraformDestroy verifies that a destroy run's
// apply phase applies its saved plan too: the plan was made with -destroy, so
// applying it is the destroy, and running `tofu destroy` would plan again.
func TestLocalProcessRunnerRunsTerraformDestroy(t *testing.T) {
	t.Parallel()

	executor := &recordingCommandExecutor{}
	runner := NewLocalProcessRunnerWithExecutor(executor)

	_, err := runner.Run(context.Background(), TerraformCommand{
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
			args: []string{"apply", "-input=false", "-auto-approve", "-no-color", "tfplan"},
		},
	}
	if !reflect.DeepEqual(executor.commands, want) {
		t.Fatalf("commands = %#v, want %#v", executor.commands, want)
	}
}

// Variables go to whatever plans and nowhere after it: a saved plan carries
// the values it was made with, and applying it must not be able to change
// them. An auto-approved apply plans as it applies, so it takes them.
func TestLocalProcessRunnerSetsTerraformVariablesOnlyWhenPlanning(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		command domain.TerraformCommandType
		args    []string
		env     []string
	}{
		{
			name:    "plan",
			command: domain.TerraformCommandPlan,
			args:    []string{"plan", "-input=false", "-no-color", "-detailed-exitcode", "-out=tfplan"},
			env:     []string{"TF_VAR_enabled=true", "TF_VAR_region=us-east-1", "TF_VAR_replicas=3", "TF_VAR_tags={\"env\":\"prod\"}", "TF_VAR_zones=[\"us-east-1a\",\"us-east-1b\"]"},
		},
		{
			name:    "destroy plan",
			command: domain.TerraformCommandPlanDestroy,
			args:    []string{"plan", "-input=false", "-no-color", "-detailed-exitcode", "-out=tfplan", "-destroy"},
			env:     []string{"TF_VAR_enabled=true", "TF_VAR_region=us-east-1", "TF_VAR_replicas=3", "TF_VAR_tags={\"env\":\"prod\"}", "TF_VAR_zones=[\"us-east-1a\",\"us-east-1b\"]"},
		},
		{
			name:    "apply",
			command: domain.TerraformCommandApply,
			args:    []string{"apply", "-input=false", "-auto-approve", "-no-color", "tfplan"},
		},
		{
			name:    "destroy",
			command: domain.TerraformCommandDestroy,
			args:    []string{"apply", "-input=false", "-auto-approve", "-no-color", "tfplan"},
		},
		{
			name:    "auto-approved apply",
			command: domain.TerraformCommandApplyAutoApprove,
			args:    []string{"apply", "-input=false", "-auto-approve", "-no-color"},
			env:     []string{"TF_VAR_enabled=true", "TF_VAR_region=us-east-1", "TF_VAR_replicas=3", "TF_VAR_tags={\"env\":\"prod\"}", "TF_VAR_zones=[\"us-east-1a\",\"us-east-1b\"]"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			executor := &recordingCommandExecutor{}
			runner := NewLocalProcessRunnerWithExecutor(executor)

			_, err := runner.Run(context.Background(), TerraformCommand{
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
					env:  tt.env,
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

// An auto-approved apply has no saved plan to count, so its counts come from
// the "Apply complete!" line, and its output still reaches the log.
func TestLocalProcessRunnerCountsAnAutoApprovedApplyFromItsOutput(t *testing.T) {
	t.Parallel()

	executor := &recordingCommandExecutor{stdout: "aws_s3_bucket.logs: Creating...\n\nApply complete! Resources: 1 imported, 2 added, 1 changed, 3 destroyed.\n"}
	runner := NewLocalProcessRunnerWithExecutor(executor)
	var log bytes.Buffer

	result, err := runner.Run(context.Background(), TerraformCommand{
		WorkspacePath: "/tmp/tflive/runs/tenant_123/run_123",
		WorkspaceName: "mtp_acme_prod_vpc_a13f9c",
		Command:       domain.TerraformCommandApplyAutoApprove,
		Stdout:        &log,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	want := Result{HasChanges: true, Summary: domain.PlanSummary{Add: 2, Change: 1, Destroy: 3}}
	if result != want {
		t.Fatalf("result = %#v, want %#v", result, want)
	}
	if log.String() != executor.stdout {
		t.Fatalf("log = %q, want the apply output", log.String())
	}
}

func TestSummarizeApplyOutput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		output string
		want   domain.PlanSummary
	}{
		{name: "changes", output: "Apply complete! Resources: 2 added, 1 changed, 0 destroyed.\n", want: domain.PlanSummary{Add: 2, Change: 1}},
		{name: "no changes", output: "No changes.\n\nApply complete! Resources: 0 added, 0 changed, 0 destroyed.\n"},
		{name: "no summary line", output: "Error: something\n"},
		{name: "counts after the line are not its", output: "Apply complete! Resources: 0 added, 0 changed, 0 destroyed.\nOutputs:\nx = \"5 added\"\n"},
	}
	for _, tt := range tests {
		if got := summarizeApplyOutput(tt.output); got != tt.want {
			t.Fatalf("%s: summarizeApplyOutput = %#v, want %#v", tt.name, got, tt.want)
		}
	}
}

// exitCodeError stands in for *exec.ExitError, whose exit code is how
// -detailed-exitcode reports a plan with changes.
type exitCodeError struct{ code int }

func (err exitCodeError) Error() string { return fmt.Sprintf("exit status %d", err.code) }
func (err exitCodeError) ExitCode() int { return err.code }

// Exit 2 from a -detailed-exitcode plan is a plan with changes, not a failure.
// The runner then reads the saved plan back and counts what it would do.
func TestLocalProcessRunnerCountsTheChangesOfAPlanThatHasThem(t *testing.T) {
	t.Parallel()

	executor := &scriptedCommandExecutor{steps: []scriptedStep{
		{err: exitCodeError{code: 2}},
		{stdout: `{"resource_changes":[
			{"change":{"actions":["create"]}},
			{"change":{"actions":["create"]}},
			{"change":{"actions":["update"]}},
			{"change":{"actions":["delete","create"]}},
			{"change":{"actions":["read"]}},
			{"change":{"actions":["no-op"]}}
		]}`},
	}}
	runner := NewLocalProcessRunnerWithExecutor(executor)

	result, err := runner.Run(context.Background(), TerraformCommand{
		WorkspacePath: "/tmp/tflive/runs/tenant_123/run_123",
		WorkspaceName: "mtp_acme_prod_vpc_a13f9c",
		Command:       domain.TerraformCommandPlan,
		Environment:   map[string]string{"AWS_REGION": "us-east-1"},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	want := Result{HasChanges: true, Summary: domain.PlanSummary{Add: 3, Change: 1, Destroy: 1}}
	if result != want {
		t.Fatalf("result = %#v, want %#v", result, want)
	}
	show := executor.commands[1]
	if !reflect.DeepEqual(show.args, []string{"show", "-json", "-no-color", "tfplan"}) {
		t.Fatalf("show args = %#v", show.args)
	}
	if !reflect.DeepEqual(show.env, []string{"AWS_REGION=us-east-1"}) {
		t.Fatalf("show env = %#v, want the credentials and no variables", show.env)
	}
}

func TestLocalProcessRunnerReportsAPlanWithoutChangesAsSuch(t *testing.T) {
	t.Parallel()

	executor := &scriptedCommandExecutor{steps: []scriptedStep{{}}}
	runner := NewLocalProcessRunnerWithExecutor(executor)

	result, err := runner.Run(context.Background(), TerraformCommand{
		WorkspacePath: "/tmp/tflive/runs/tenant_123/run_123",
		WorkspaceName: "mtp_acme_prod_vpc_a13f9c",
		Command:       domain.TerraformCommandPlan,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result != (Result{}) {
		t.Fatalf("result = %#v, want no changes", result)
	}
	if len(executor.commands) != 1 {
		t.Fatalf("commands = %#v, want only the plan", executor.commands)
	}
}

// Any exit code but 0 and 2 is a failed plan, even though 2 is not.
func TestLocalProcessRunnerFailsAPlanThatExitsOne(t *testing.T) {
	t.Parallel()

	executor := &scriptedCommandExecutor{steps: []scriptedStep{{err: exitCodeError{code: 1}}}}
	runner := NewLocalProcessRunnerWithExecutor(executor)

	_, err := runner.Run(context.Background(), TerraformCommand{
		WorkspacePath: "/tmp/tflive/runs/tenant_123/run_123",
		WorkspaceName: "mtp_acme_prod_vpc_a13f9c",
		Command:       domain.TerraformCommandPlan,
	})
	var cmdErr *CommandError
	if !errors.As(err, &cmdErr) || cmdErr.Command != domain.TerraformCommandPlan {
		t.Fatalf("error = %v, want a plan CommandError", err)
	}
}

func TestLocalProcessRunnerSelectsExistingWorkspace(t *testing.T) {
	t.Parallel()

	executor := &recordingCommandExecutor{}
	runner := NewLocalProcessRunnerWithExecutor(executor)

	_, err := runner.Run(context.Background(), TerraformCommand{
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

	_, err := runner.Run(context.Background(), TerraformCommand{
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

	_, err := runner.Run(context.Background(), TerraformCommand{
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

	_, err := runner.Run(context.Background(), TerraformCommand{
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

	_, err := runner.Run(context.Background(), TerraformCommand{
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

	_, err := runner.Run(context.Background(), TerraformCommand{
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

// scriptedCommandExecutor plays back one step per command, for commands whose
// outputs differ: a plan's exit code and then its saved plan's JSON.
type scriptedCommandExecutor struct {
	steps    []scriptedStep
	commands []recordedCommand
}

type scriptedStep struct {
	stdout string
	err    error
}

func (executor *scriptedCommandExecutor) Run(_ context.Context, dir string, env []string, stdout io.Writer, _ io.Writer, name string, args ...string) error {
	executor.commands = append(executor.commands, recordedCommand{dir: dir, env: append([]string(nil), env...), name: name, args: append([]string(nil), args...)})
	if len(executor.steps) == 0 {
		return nil
	}
	step := executor.steps[0]
	executor.steps = executor.steps[1:]
	if step.stdout != "" {
		if _, err := io.WriteString(stdout, step.stdout); err != nil {
			return err
		}
	}
	return step.err
}

type recordedCommand struct {
	dir  string
	env  []string
	name string
	args []string
}
