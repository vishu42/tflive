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
			'cancel_requested',
			'canceling',
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
	) not valid;
