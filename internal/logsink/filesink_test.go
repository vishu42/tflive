package logsink

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vishu42/tflive/internal/domain"
)

func TestFileSinkWritesAndAppendsPhaseLog(t *testing.T) {
	t.Parallel()

	workspacePath := t.TempDir()
	sink := NewFileSink(workspacePath)

	first, err := sink.Open("plan.log")
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	if _, err := first.Write([]byte("first line\n")); err != nil {
		t.Fatalf("write first log: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close first log: %v", err)
	}

	second, err := sink.Open("plan.log")
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	if _, err := second.Write([]byte("second line\n")); err != nil {
		t.Fatalf("write second log: %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatalf("close second log: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(workspacePath, "logs", "plan.log"))
	if err != nil {
		t.Fatalf("read phase log: %v", err)
	}
	if string(got) != "first line\nsecond line\n" {
		t.Fatalf("log content = %q", string(got))
	}
}

func TestFileSinkRejectsUnsafePhase(t *testing.T) {
	t.Parallel()

	sink := NewFileSink(t.TempDir())

	_, err := sink.Open("../plan.log")
	if err == nil {
		t.Fatal("Open returned nil error, want unsafe phase error")
	}
	if !strings.Contains(err.Error(), "safe path") {
		t.Fatalf("error = %q, want safe path context", err.Error())
	}
}

func TestFileNameForTerraformCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		command  domain.TerraformCommandType
		runPhase domain.RunPhase
		want     string
	}{
		{command: domain.TerraformCommandInit, runPhase: domain.RunPhasePlan, want: "plan-init.log"},
		{command: domain.TerraformCommandSelectWorkspace, runPhase: domain.RunPhasePlan, want: "plan-workspace.log"},
		{command: domain.TerraformCommandPlan, runPhase: domain.RunPhasePlan, want: "plan.log"},
		{command: domain.TerraformCommandPlanDestroy, runPhase: domain.RunPhasePlan, want: "plan.log"},
		{command: domain.TerraformCommandInit, runPhase: domain.RunPhaseApply, want: "apply-init.log"},
		{command: domain.TerraformCommandSelectWorkspace, runPhase: domain.RunPhaseApply, want: "apply-workspace.log"},
		{command: domain.TerraformCommandApply, runPhase: domain.RunPhaseApply, want: "apply.log"},
		{command: domain.TerraformCommandApplyAutoApprove, runPhase: domain.RunPhaseApply, want: "apply.log"},
		{command: domain.TerraformCommandDestroy, runPhase: domain.RunPhaseApply, want: "destroy.log"},
	}

	for _, tt := range tests {
		got, err := FileNameForTerraformCommand(tt.command, tt.runPhase)
		if err != nil {
			t.Fatalf("FileNameForTerraformCommand(%q, %q) returned error: %v", tt.command, tt.runPhase, err)
		}
		if got != tt.want {
			t.Fatalf("FileNameForTerraformCommand(%q, %q) = %q, want %q", tt.command, tt.runPhase, got, tt.want)
		}
	}
}

func TestFileNameForTerraformCommandRejectsMissingRunPhase(t *testing.T) {
	t.Parallel()

	if _, err := FileNameForTerraformCommand(domain.TerraformCommandInit, ""); err == nil {
		t.Fatal("FileNameForTerraformCommand accepted an empty run phase")
	}
}

func TestLocalReaderReadsTenantRunPhaseLog(t *testing.T) {
	t.Parallel()

	runRoot := t.TempDir()
	logPath := filepath.Join(runRoot, "tenant_123", "run_123", "logs")
	if err := os.MkdirAll(logPath, 0o700); err != nil {
		t.Fatalf("create log directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(logPath, "plan.log"), []byte("plan output\n"), 0o600); err != nil {
		t.Fatalf("write phase log: %v", err)
	}

	content, err := NewLocalReader(runRoot).ReadTemplateRunLog(
		context.Background(),
		domain.TenantID("tenant_123"),
		domain.TemplateRunID("run_123"),
		"plan",
	)
	if err != nil {
		t.Fatalf("ReadTemplateRunLog returned error: %v", err)
	}
	if string(content) != "plan output\n" {
		t.Fatalf("content = %q, want plan output", string(content))
	}
}

func TestRunWorkspacePathMatchesWorkspaceLayout(t *testing.T) {
	t.Parallel()

	runRoot := t.TempDir()

	path, err := RunWorkspacePath(runRoot, domain.TenantID("tenant_123"), domain.TemplateRunID("run_123"))
	if err != nil {
		t.Fatalf("RunWorkspacePath returned error: %v", err)
	}

	want := filepath.Join(runRoot, "tenant_123", "run_123")
	if path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
}

func TestRunWorkspacePathRejectsUnsafePathComponents(t *testing.T) {
	t.Parallel()

	_, err := RunWorkspacePath(t.TempDir(), domain.TenantID("tenant_123"), domain.TemplateRunID("../run"))
	if err == nil {
		t.Fatal("RunWorkspacePath returned nil error for unsafe run ID")
	}
	if !strings.Contains(err.Error(), "safe path") {
		t.Fatalf("error = %q, want safe path context", err.Error())
	}
}

func TestLocalReaderRejectsUnsafePathComponents(t *testing.T) {
	t.Parallel()

	reader := NewLocalReader(t.TempDir())

	_, err := reader.ReadTemplateRunLog(context.Background(), domain.TenantID("../tenant"), domain.TemplateRunID("run_123"), "plan")
	if err == nil {
		t.Fatal("ReadTemplateRunLog returned nil error for unsafe tenant")
	}
	if !strings.Contains(err.Error(), "safe path") {
		t.Fatalf("error = %q, want safe path context", err.Error())
	}

	_, err = reader.ReadTemplateRunLog(context.Background(), domain.TenantID("tenant_123"), domain.TemplateRunID("run_123"), "../plan")
	if err == nil {
		t.Fatal("ReadTemplateRunLog returned nil error for unsafe phase")
	}
	if !strings.Contains(err.Error(), "safe path") {
		t.Fatalf("error = %q, want safe path context", err.Error())
	}
}

func TestLocalReaderReturnsNotExistForMissingLog(t *testing.T) {
	t.Parallel()

	_, err := NewLocalReader(t.TempDir()).ReadTemplateRunLog(
		context.Background(),
		domain.TenantID("tenant_123"),
		domain.TemplateRunID("run_123"),
		"plan",
	)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("error = %v, want os.ErrNotExist", err)
	}
}
