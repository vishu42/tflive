package app

import (
	"testing"

	"github.com/vishu42/tflive/internal/authz"
	"github.com/vishu42/tflive/internal/queue"
)

func TestQueueSpecsContainsTheSevenSharedSpecs(t *testing.T) {
	specs := QueueSpecs()
	if len(specs) != 7 {
		t.Fatalf("len(QueueSpecs()) = %d, want 7", len(specs))
	}
	seen := make(map[queue.Kind]bool, len(specs))
	for _, spec := range specs {
		seen[spec.Kind] = true
	}
	for _, kind := range []queue.Kind{KindStartTemplateRun, KindStartTemplateSync, KindSignalRunApproval, KindSignalRunCancellation, KindGrantStackOwner, KindMarkStackReady, authz.KindReconcileStackGrant} {
		if !seen[kind] {
			t.Fatalf("QueueSpecs() missing %q", kind)
		}
	}
}
