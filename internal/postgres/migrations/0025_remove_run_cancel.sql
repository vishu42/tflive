-- Canceling a run in flight is removed; discarding a plan waiting for approval
-- stays, and a discarded run still ends canceled. cancel_requested and
-- canceling were only ever held by a run whose cancel signal was on its way,
-- so any left are closed out as canceled.

update template_runs
set status = 'canceled', completed_at = coalesce(completed_at, now())
where status in ('cancel_requested', 'canceling');

delete from work_queue where kind = 'signal_run_cancellation';

alter table template_runs
	drop constraint template_runs_status_check;

alter table template_runs
	add constraint template_runs_status_check check (
		status in (
			'queued',
			'locked',
			'workspace_prepared',
			'source_fetched',
			'workspace_selected',
			'waiting_approval',
			'approved',
			'canceled',
			'lock_released',
			'completed',
			'failed',
			'init_started',
			'init_finished',
			'plan_started',
			'plan_finished',
			'apply_started',
			'apply_finished',
			'destroy_started',
			'destroy_finished'
		)
	);
