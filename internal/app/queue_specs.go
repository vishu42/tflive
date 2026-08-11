package app

import (
	"github.com/vishu42/tflive/internal/authz"
	"github.com/vishu42/tflive/internal/queue"
)

// QueueSpecs returns every queue contract shared by API producers and workers.
func QueueSpecs() []queue.Spec {
	return []queue.Spec{
		StartTemplateRunSpec,
		StartTemplateSyncSpec,
		SignalRunApprovalSpec,
		SignalRunCancellationSpec,
		GrantStackOwnerSpec,
		MarkStackReadySpec,
		authz.StackGrantSpec,
	}
}
