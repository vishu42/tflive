package logsink

import (
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
