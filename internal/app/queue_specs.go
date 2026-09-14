package app

import (
	"github.com/vishu42/tflive/internal/queue"
)

// QueueSpecs returns every queue contract shared by API producers and workers.
//
// Authorization work is absent by design. Granting the founding owner,
// reconciling a role change and flipping a stack to ready were all queued once,
// because a tuple write could not commit with the domain write that caused it.
// It can now, so those three kinds were retired rather than reimplemented: the
// grant is part of the same transaction as the stack row.
func QueueSpecs() []queue.Spec {
	return []queue.Spec{
		StartTemplateRunSpec,
		StartTemplateSyncSpec,
		SignalRunApprovalSpec,
		SignalRunCancellationSpec,
	}
}
