package workflows

import "go.temporal.io/sdk/workflow"

// Recorder records the step a workflow's work is on, for the people watching
// it. S is the workflow's step vocabulary.
//
// A recorder is bound to the control plane when it is made, and holds that
// context itself: the context a step's work runs on may be an executor
// session, and a record scheduled on it would run on the executor.
type Recorder[S ~string] interface {
	Record(step S) error
}

// ExecuteStep records step, then schedules activity on ctx: it is
// workflow.ExecuteActivity for the activity that is the step. Work that only
// prepares a step, such as sealing a secret on the control plane for the
// executor, runs before it with workflow.ExecuteActivity. Only a step with no
// activity calls Record itself.
//
// The record completes before the activity is scheduled, so a slow step shows
// as itself rather than as the step before it. A failed record fails the step
// rather than let the status lie.
func ExecuteStep[S ~string](ctx workflow.Context, r Recorder[S], step S, activity any, args ...any) workflow.Future {
	if err := r.Record(step); err != nil {
		future, settable := workflow.NewFuture(ctx)
		settable.SetError(err)
		return future
	}
	return workflow.ExecuteActivity(ctx, activity, args...)
}
