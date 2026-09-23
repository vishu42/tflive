-- A run's status is its lifecycle state and nothing else. Progress lives in
-- step (0026), and what a run does to its stack template is recorded as an
-- event rather than inferred from a status.
--
-- A run holding one of the old progress statuses was in flight under a
-- workflow that wrote them, and cannot finish under one that does not. tflive
-- is pre-production, so they are closed out, as 0021 and 0023 did.
update template_runs
set
	status = 'failed',
	error_summary = case
		when error_summary = '' then 'closed out by migration 0027: run was in flight when run statuses became lifecycle only'
		else error_summary
	end,
	completed_at = coalesce(completed_at, now())
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
