-- Saved plans (#249). A run is now plan -> approve -> apply the saved plan:
-- the plan phase writes an encrypted plan file to the artifact store and waits
-- for approval without holding an executor, and approving it starts a second
-- workflow that applies exactly that file. See
-- docs/superpowers/specs/2026-09-21-saved-plan-flow-design.md.
--
-- tflive is pre-production, so this converts in place rather than migrating
-- behaviour: runs of the old shape are closed out, and the old approval queue
-- kind is dropped.

-- Runs started under the old flow cannot finish under the new one: an old
-- apply run parks on a workflow signal nothing sends any more, and no old run
-- has a saved plan to apply. Close every unfinished run.
update template_runs
set
	status = 'failed',
	error_summary = case
		when error_summary = '' then 'closed out by migration 0023: started before saved plans'
		else error_summary
	end,
	completed_at = coalesce(completed_at, now())
where status not in ('completed', 'failed', 'canceled');

-- The approval used to be a workflow signal; it now starts the apply
-- workflow. Pending signals have no run left to reach.
delete from work_queue where kind = 'signal_run_approval';

alter table template_runs
	-- The trigger actor asked for the plan to apply without a second person.
	add column auto_approve boolean not null default false,
	-- The key the saved plan is encrypted with, itself encrypted with the
	-- control plane's key. Null once the run is terminal: dropping the key is
	-- what makes a saved plan left in the artifact store unreadable.
	add column plan_artifact_dek text,
	-- What the saved plan would do, counted by the executor from
	-- `tofu show -json`. Null until a plan with changes finishes.
	add column plan_add integer,
	add column plan_change integer,
	add column plan_destroy integer;

-- "Planned" used to mean the latest completed plan run, which gated starting
-- an apply. It now means the plan waiting for approval, which gates approving
-- it. The columns point at that run while it waits and are cleared when it
-- becomes terminal, so a discarded plan stops counting as reviewed.
alter table stack_templates rename column last_planned_run_id to pending_plan_run_id;
alter table stack_templates rename column last_planned_template_revision_id to pending_plan_template_revision_id;
alter table stack_templates rename column last_planned_config_json to pending_plan_config_json;
alter table stack_templates rename column last_planned_at to pending_plan_at;

-- No run is waiting after the close-out above, so nothing is pending.
update stack_templates
set
	pending_plan_run_id = '',
	pending_plan_template_revision_id = '',
	pending_plan_config_json = null,
	pending_plan_at = null;
