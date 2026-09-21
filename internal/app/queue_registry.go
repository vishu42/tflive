package app

import (
	"github.com/vishu42/tflive/internal/queue"
)

// NewQueueRegistry builds the handlers the control plane's queue loop delivers.
//
// Every one of them turns a committed intent into a Temporal call. None touch
// authorization: granting the founding owner, reconciling a role change and
// flipping a stack to ready used to be queued because a tuple write could not
// commit with the domain write that caused it; it can now, so all three happen
// in the API's transaction.
func NewQueueRegistry(dispatcher WorkflowDispatcher, reconciler TemplateRunCancellationReconciler) (*queue.Registry, error) {
	return queue.NewRegistry(
		NewStartTemplateRunHandler(dispatcher),
		NewStartTemplateSyncHandler(dispatcher),
		NewStartTemplateApplyHandler(dispatcher),
		NewSignalRunCancellationHandler(dispatcher, reconciler),
	)
}
