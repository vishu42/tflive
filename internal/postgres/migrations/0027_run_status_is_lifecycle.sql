-- A run's status is its lifecycle state and nothing else. Progress lives in
-- step (0026), and what a run does to its stack template is recorded as an
-- event rather than inferred from a status.
--
-- A run holding one of the old progress statuses was in flight under a
-- workflow that wrote them, and cannot finish under one that does not. tflive
-- is pre-production, so they are closed out, as 0021 and 0023 did.
--
-- Operators must terminate open template-run workflows before migrating. A
-- run still `queued` has no old-status row for this migration to catch, but
-- its workflow keeps running under the new workflow code, which replays it
-- nondeterministically -- the run is left stuck in flight rather than closed
-- out by anything here.
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

-- A destroy run this closes out may have been mid-destroy: its stack
-- template's lifecycle was set to 'destroying' (run_progress.go) and nothing
-- else would ever move it off that. This mirrors what
-- recordInterruptedDestroyLifecycle does when a destroy fails normally,
-- keyed on the same pre-close-out predicate, run before the close-out below
-- changes it.
update stack_templates st
set lifecycle = 'failed'
where st.lifecycle = 'destroying'
	and exists (
		select 1
		from template_runs tr
		where tr.tenant_id = st.tenant_id
			and tr.stack_template_id = st.id
			and tr.operation = 'destroy'
			and tr.status not in ('queued', 'running', 'waiting_approval', 'approved', 'completed', 'failed', 'canceled')
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
