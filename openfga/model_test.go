package openfgamodel_test

import (
	"os/exec"
	"strings"
	"testing"
)

// The DSL in authorization-model.fga is the source of truth and the JSON beside
// it is a generated artifact, so the two must not drift. scripts/openfga-model.sh
// owns the transform; this test is what makes `go test ./...` fail on a stale
// artifact.
func TestCommittedJSONMatchesDSL(t *testing.T) {
	t.Parallel()

	for _, tool := range []string{"fga", "python3"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed; install it to check the model artifact", tool)
		}
	}

	output, err := exec.Command("../scripts/openfga-model.sh", "check").CombinedOutput()
	if err != nil {
		t.Fatalf("scripts/openfga-model.sh check: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "matches") {
		t.Fatalf("unexpected check output:\n%s", output)
	}
}
