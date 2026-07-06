package logsink

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vishu42/megagega/internal/traits"
)

func TestFileSinkWritesAndAppendsPhaseLog(t *testing.T) {
	t.Parallel()

	workspacePath := t.TempDir()
	sink := NewFileSink(workspacePath)

	first, err := sink.OpenPhase("plan")
	if err != nil {
		t.Fatalf("OpenPhase returned error: %v", err)
	}
	if _, err := first.Write([]byte("first line\n")); err != nil {
		t.Fatalf("write first log: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close first log: %v", err)
	}

	second, err := sink.OpenPhase("plan")
	if err != nil {
		t.Fatalf("OpenPhase returned error: %v", err)
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

	_, err := sink.OpenPhase("../plan")
	if err == nil {
		t.Fatal("OpenPhase returned nil error, want unsafe phase error")
	}
	if !strings.Contains(err.Error(), "safe path") {
		t.Fatalf("error = %q, want safe path context", err.Error())
	}
}

func TestPhaseForTerraformCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		command traits.TerraformCommandType
		want    string
	}{
		{command: traits.TerraformCommandInit, want: "init"},
		{command: traits.TerraformCommandSelectWorkspace, want: "workspace"},
		{command: traits.TerraformCommandPlan, want: "plan"},
		{command: traits.TerraformCommandApply, want: "apply"},
	}

	for _, tt := range tests {
		got, err := PhaseForTerraformCommand(tt.command)
		if err != nil {
			t.Fatalf("PhaseForTerraformCommand(%q) returned error: %v", tt.command, err)
		}
		if got != tt.want {
			t.Fatalf("PhaseForTerraformCommand(%q) = %q, want %q", tt.command, got, tt.want)
		}
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
		traits.TenantID("tenant_123"),
		traits.TemplateRunID("run_123"),
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

	path, err := RunWorkspacePath(runRoot, traits.TenantID("tenant_123"), traits.TemplateRunID("run_123"))
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

	_, err := RunWorkspacePath(t.TempDir(), traits.TenantID("tenant_123"), traits.TemplateRunID("../run"))
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

	_, err := reader.ReadTemplateRunLog(context.Background(), traits.TenantID("../tenant"), traits.TemplateRunID("run_123"), "plan")
	if err == nil {
		t.Fatal("ReadTemplateRunLog returned nil error for unsafe tenant")
	}
	if !strings.Contains(err.Error(), "safe path") {
		t.Fatalf("error = %q, want safe path context", err.Error())
	}

	_, err = reader.ReadTemplateRunLog(context.Background(), traits.TenantID("tenant_123"), traits.TemplateRunID("run_123"), "../plan")
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
		traits.TenantID("tenant_123"),
		traits.TemplateRunID("run_123"),
		"plan",
	)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("error = %v, want os.ErrNotExist", err)
	}
}
