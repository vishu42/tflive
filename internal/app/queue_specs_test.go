package app

import (
	"testing"

	"github.com/vishu42/tflive/internal/queue"
)

// The three authorization kinds are gone: granting the founding owner,
// reconciling a role change and flipping a stack to ready were queued only
// because a tuple write could not commit with the domain write that caused it.
// It can now, so what remains is Temporal dispatch and notification.
func TestQueueSpecsContainsTheFourSharedSpecs(t *testing.T) {
	specs := QueueSpecs()
	if len(specs) != 4 {
		t.Fatalf("len(QueueSpecs()) = %d, want 4", len(specs))
	}
	seen := make(map[queue.Kind]bool, len(specs))
	for _, spec := range specs {
		seen[spec.Kind] = true
	}
	for _, kind := range []queue.Kind{KindStartTemplateRun, KindStartTemplateSync, KindSignalRunApproval, KindSignalRunCancellation} {
		if !seen[kind] {
			t.Fatalf("QueueSpecs() missing %q", kind)
		}
	}
	for _, retired := range []queue.Kind{"grant_stack_owner", "mark_stack_ready", "reconcile_stack_grant"} {
		if seen[retired] {
			t.Fatalf("QueueSpecs() still carries the retired kind %q", retired)
		}
	}
}

// Every shared spec needs a handler, or the queue loop reschedules that kind
// forever; and nothing else may be registered.
func TestNewQueueRegistryHandlesEverySharedSpec(t *testing.T) {
	registry, err := NewQueueRegistry(&recordingWorkflowIntentDispatcher{}, &recordingCancellationReconciler{})
	if err != nil {
		t.Fatalf("NewQueueRegistry returned error: %v", err)
	}
	got := registry.Kinds()
	specs := QueueSpecs()
	if len(got) != len(specs) {
		t.Fatalf("registered kinds = %v, want %d", got, len(specs))
	}
	registered := make(map[queue.Kind]bool, len(got))
	for _, kind := range got {
		registered[kind] = true
	}
	for _, spec := range specs {
		if !registered[spec.Kind] {
			t.Fatalf("no handler registered for %q", spec.Kind)
		}
	}
}
