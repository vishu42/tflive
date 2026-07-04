package activities

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/vishu42/megagega/internal/runner"
	"github.com/vishu42/megagega/internal/traits"
)

func TestRecordTemplateRunStatusDelegatesToRecorder(t *testing.T) {
	t.Parallel()

	recorder := &recordingStatusRecorder{}
	activities := NewTemplateRunActivities(recorder, t.TempDir())
	input := traits.TemplateRunStatusActivityInput{
		RunID:           traits.TemplateRunID("run_123"),
		TenantID:        traits.TenantID("tenant_123"),
		StackTemplateID: traits.StackTemplateID("stack_template_123"),
		Operation:       traits.OperationApply,
		Status:          traits.TemplateRunPlanned,
	}

	if err := activities.RecordTemplateRunStatus(context.Background(), input); err != nil {
		t.Fatalf("RecordTemplateRunStatus returned error: %v", err)
	}

	if !reflect.DeepEqual(recorder.input, input) {
		t.Fatalf("recorded input = %#v, want %#v", recorder.input, input)
	}
}

func TestRecordTemplateRunStatusWrapsRecorderError(t *testing.T) {
	t.Parallel()

	recorderErr := errors.New("database unavailable")
	activities := NewTemplateRunActivities(&recordingStatusRecorder{err: recorderErr}, t.TempDir())

	err := activities.RecordTemplateRunStatus(context.Background(), traits.TemplateRunStatusActivityInput{
		RunID:    traits.TemplateRunID("run_123"),
		TenantID: traits.TenantID("tenant_123"),
		Status:   traits.TemplateRunFailed,
	})
	if !errors.Is(err, recorderErr) {
		t.Fatalf("error = %v, want recorderErr", err)
	}
	if !strings.Contains(err.Error(), "record template run status") {
		t.Fatalf("error = %q, want status context", err.Error())
	}
}

func TestPrepareWorkspaceCreatesRunDirectory(t *testing.T) {
	t.Parallel()

	runRoot := t.TempDir()
	activities := NewTemplateRunActivities(&recordingStatusRecorder{}, runRoot)
	input := traits.PrepareWorkspaceActivityInput{
		RunID:    traits.TemplateRunID("run_123"),
		TenantID: traits.TenantID("tenant_123"),
	}

	output, err := activities.PrepareWorkspace(context.Background(), input)
	if err != nil {
		t.Fatalf("PrepareWorkspace returned error: %v", err)
	}

	wantPath := filepath.Join(runRoot, "tenant_123", "run_123")
	if output.WorkspacePath != wantPath {
		t.Fatalf("WorkspacePath = %q, want %q", output.WorkspacePath, wantPath)
	}
	info, err := os.Stat(wantPath)
	if err != nil {
		t.Fatalf("stat workspace path: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("workspace path %q is not a directory", wantPath)
	}
}

func TestPrepareWorkspaceRejectsEmptyRoot(t *testing.T) {
	t.Parallel()

	activities := NewTemplateRunActivities(&recordingStatusRecorder{}, "")

	_, err := activities.PrepareWorkspace(context.Background(), traits.PrepareWorkspaceActivityInput{
		RunID:    traits.TemplateRunID("run_123"),
		TenantID: traits.TenantID("tenant_123"),
	})
	if err == nil {
		t.Fatal("PrepareWorkspace returned nil error, want error")
	}
	if !strings.Contains(err.Error(), "run root") {
		t.Fatalf("error = %q, want run root context", err.Error())
	}
}

func TestPrepareWorkspaceRejectsUnsafePathComponents(t *testing.T) {
	t.Parallel()

	activities := NewTemplateRunActivities(&recordingStatusRecorder{}, t.TempDir())

	_, err := activities.PrepareWorkspace(context.Background(), traits.PrepareWorkspaceActivityInput{
		RunID:    traits.TemplateRunID("run_123"),
		TenantID: traits.TenantID("../tenant_123"),
	})
	if err == nil {
		t.Fatal("PrepareWorkspace returned nil error, want error")
	}
	if !strings.Contains(err.Error(), "safe path") {
		t.Fatalf("error = %q, want safe path context", err.Error())
	}
}

func TestRunTerraformDelegatesToRunner(t *testing.T) {
	t.Parallel()

	runner := &recordingTerraformRunner{}
	activities := NewTemplateRunActivities(&recordingStatusRecorder{}, t.TempDir(), runner)
	input := traits.RunTerraformActivityInput{
		RunID:         traits.TemplateRunID("run_123"),
		TenantID:      traits.TenantID("tenant_123"),
		WorkspacePath: "/tmp/megagega/runs/tenant_123/run_123",
		WorkspaceName: "mtp_acme_prod_vpc_a13f9c",
		Command:       traits.TerraformCommandPlan,
	}

	if err := activities.RunTerraform(context.Background(), input); err != nil {
		t.Fatalf("RunTerraform returned error: %v", err)
	}

	if !reflect.DeepEqual(runner.input, input) {
		t.Fatalf("runner input = %#v, want %#v", runner.input, input)
	}
}

func TestLocalTerraformRunnerWritesCommandLogFile(t *testing.T) {
	t.Parallel()

	workspacePath := t.TempDir()
	executor := &recordingCommandExecutor{
		stdout: "plan stdout\n",
		stderr: "plan stderr\n",
	}
	terraformRunner := localTerraformRunner{
		runner: runner.NewLocalProcessRunnerWithExecutor(executor),
	}

	err := terraformRunner.RunTerraform(context.Background(), traits.RunTerraformActivityInput{
		RunID:         traits.TemplateRunID("run_123"),
		TenantID:      traits.TenantID("tenant_123"),
		WorkspacePath: workspacePath,
		WorkspaceName: "mtp_acme_prod_vpc_a13f9c",
		Command:       traits.TerraformCommandPlan,
	})
	if err != nil {
		t.Fatalf("RunTerraform returned error: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(workspacePath, "logs", "plan.log"))
	if err != nil {
		t.Fatalf("read plan log: %v", err)
	}
	if string(got) != "plan stdout\nplan stderr\n" {
		t.Fatalf("plan log = %q", string(got))
	}
}

func TestRunTerraformWrapsRunnerError(t *testing.T) {
	t.Parallel()

	runnerErr := errors.New("terraform failed")
	activities := NewTemplateRunActivities(&recordingStatusRecorder{}, t.TempDir(), &recordingTerraformRunner{err: runnerErr})

	err := activities.RunTerraform(context.Background(), traits.RunTerraformActivityInput{
		RunID:         traits.TemplateRunID("run_123"),
		TenantID:      traits.TenantID("tenant_123"),
		WorkspacePath: "/tmp/megagega/runs/tenant_123/run_123",
		WorkspaceName: "mtp_acme_prod_vpc_a13f9c",
		Command:       traits.TerraformCommandApply,
	})
	if !errors.Is(err, runnerErr) {
		t.Fatalf("error = %v, want runnerErr", err)
	}
	if !strings.Contains(err.Error(), "run terraform") {
		t.Fatalf("error = %q, want run terraform context", err.Error())
	}
}

type recordingStatusRecorder struct {
	input traits.TemplateRunStatusActivityInput
	err   error
}

func (recorder *recordingStatusRecorder) RecordTemplateRunStatus(_ context.Context, input traits.TemplateRunStatusActivityInput) error {
	recorder.input = input
	return recorder.err
}

type recordingTerraformRunner struct {
	input traits.RunTerraformActivityInput
	err   error
}

func (runner *recordingTerraformRunner) RunTerraform(_ context.Context, input traits.RunTerraformActivityInput) error {
	runner.input = input
	return runner.err
}

type recordingCommandExecutor struct {
	stdout string
	stderr string
}

func (executor *recordingCommandExecutor) Run(_ context.Context, _ string, stdout io.Writer, stderr io.Writer, _ string, _ ...string) error {
	if _, err := io.WriteString(stdout, executor.stdout); err != nil {
		return err
	}
	if _, err := io.WriteString(stderr, executor.stderr); err != nil {
		return err
	}
	return nil
}
