-- A run's status is its lifecycle state and nothing else. Progress lives in
-- step (0026), and what a run does to its stack template is recorded as an
-- event rather than inferred from a status.
--
-- A run holding one of the old progress statuses was in flight under a
-- workflow that wrote them, and cannot finish under one that does not. tflive
-- is pre-production, so they are closed out, as 0021 and 0023 did.
--
-- A run this closes out may hold a saved plan: it stops being reachable once
-- the run is failed below, so its key must go, and it must stop being any
-- stack template's pending plan -- the same two things releaseRunPlan does
-- for every other path that makes a run terminal
-- (internal/postgres/repositories.go). Clearing the pending plan runs first,
-- keyed on the same predicate the close-out below uses, so it still sees the
-- run's pre-close-out status.
update stack_templates
set
	pending_plan_run_id = '',
	pending_plan_template_revision_id = '',
	pending_plan_config_json = null,
	pending_plan_at = null
where pending_plan_run_id in (
	select id
	from template_runs
	where status not in ('queued', 'running', 'waiting_approval', 'approved', 'completed', 'failed', 'canceled')
);

update template_runs
set
	status = 'failed',
	error_summary = case
		when error_summary = '' then 'closed out by migration 0027: run was in flight when run statuses became lifecycle only'
		else error_summary
	end,
	completed_at = coalesce(completed_at, now()),
	plan_artifact_dek = null
where status not in ('queued', 'running', 'waiting_approval', 'approved', 'completed', 'failed', 'canceled');

alter table template_runs
	drop constraint template_runs_status_check;

-- Must stay equal to domain.AllTemplateRunStatuses.
alter table template_runs
	add constraint template_runs_status_check check (
		status in (
			'queued',
			'running',
			'waiting_approval',
			'approved',
			'completed',
			'failed',
			'canceled'
		)
	);
