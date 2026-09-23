-- A run's step is what it is doing right now, for the people watching it:
-- fetching source, initializing, planning. It is not its status. Status is the
-- run's lifecycle, and decides what may happen to the run next; nothing
-- branches on the step. It is written when a step starts and kept when the
-- run ends, so a failed run still says where it failed. '' is a run that has
-- not started a step.
--
-- The list must stay equal to domain.AllTemplateRunSteps.
alter table template_runs
	add column step text not null default ''
	constraint template_runs_step_check check (
		step in (
			'',
			'waiting_for_executor',
			'preparing_workspace',
			'fetching_source',
			'restoring_plan',
			'initializing',
			'selecting_workspace',
			'planning',
			'saving_plan',
			'applying'
		)
	);

-- running is the state a run is in while a workflow works on it. It joins the
-- old statuses here; 0027 removes those once nothing writes them.
alter table template_runs
	drop constraint template_runs_status_check;

alter table template_runs
	add constraint template_runs_status_check check (
		status in (
			'queued',
			'running',
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
