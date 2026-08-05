package queue

import (
	"encoding/json"
	"testing"
)

func TestItemRevisionIsNumeric(t *testing.T) {
	t.Parallel()

	item := Item{ID: 7, Revision: 3, Attempts: 2, Payload: json.RawMessage(`{}`)}
	if item.Revision+1 != 4 {
		t.Fatalf("Revision must support arithmetic, got %d", item.Revision)
	}
}

func TestStateConstants(t *testing.T) {
	t.Parallel()

	if StatePending == StateDelivered {
		t.Fatal("StatePending and StateDelivered must differ")
	}
	if StatePending != "pending" || StateDelivered != "delivered" {
		t.Fatalf("unexpected state values: %q %q", StatePending, StateDelivered)
	}
}

func TestModeConstantsAreDistinct(t *testing.T) {
	t.Parallel()

	if ModeReconcile == ModeJob {
		t.Fatal("ModeReconcile and ModeJob must differ")
	}
}
