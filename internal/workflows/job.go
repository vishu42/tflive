package workflows

// job is work a workflow runs as ordered, recorded steps, in the GitHub
// Actions sense: a workflow has jobs, and a job runs its steps on one worker.
// S is the workflow's step vocabulary, and W is what the steps work through:
// an executor session for a template run, the control-queue context for a
// template sync.
//
// A workflow's own job type embeds job and adds the methods only that
// workflow has, the way net.TCPConn embeds net.conn: runJob adds Terraform,
// events and logs, syncJob adds the sync. A job has no status write: only the
// workflow that opened it moves its lifecycle along.
type job[S ~string, W any] struct {
	recordStep func(S) error
	worker     W
}

// step records s, then does its work. It is the one path a job's code takes to
// its worker, so work runs inside a named step, and no step is recorded after
// its work has started. Code holding a job never uses j.worker directly.
func (j *job[S, W]) step(s S, work func(W) error) error {
	if err := j.recordStep(s); err != nil {
		return err
	}
	return work(j.worker)
}
