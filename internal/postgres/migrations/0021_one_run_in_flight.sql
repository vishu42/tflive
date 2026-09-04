-- At most one unfinished run per stack template, enforced by the database.
--
-- The rule existed only in the React client, so two tabs or any direct API call
-- could start a second run while one was still going. Two concurrent applies then
-- raced last-write-wins on last_applied_run_id, last_applied_template_revision_id
-- and last_applied_config_json, which let LiveState() and PlanState() report a
-- snapshot no single run produced. See #231.
--
-- This is a unique index rather than a check in StartTemplateRun on purpose. A
-- read-then-insert leaves a window where both requests see no active run and both
-- insert; the index is evaluated by the insert itself, so a second in-flight row
-- structurally cannot exist. Same reasoning as work_queue_pending_key_idx (0012).
--
-- The predicate lists the terminal statuses, and must stay equal to
-- domain.TemplateRunStatus.Terminal(). Nothing enforces that from SQL, so the
-- postgres test walks domain.AllTemplateRunStatuses and asserts the index rejects
-- a second run for exactly the non-terminal ones.

-- Runs left non-terminal by a failure that predates the workflow recording it
-- (#157) would be duplicate keys the index cannot be built over, and a stack
-- template holding one could never start another run. tflive is pre-production,
-- so they are closed out here rather than migrated.
update template_runs
set
	status = 'failed',
	error_summary = case
		when error_summary = '' then 'closed out by migration 0021: run was left unfinished by an earlier failure path'
		else error_summary
	end,
	completed_at = coalesce(completed_at, now())
where status not in ('completed', 'failed', 'canceled');

create unique index template_runs_in_flight_idx
	on template_runs (tenant_id, stack_template_id)
	where status not in ('completed', 'failed', 'canceled');
