-- Records the (revision, config) snapshot of the last completed plan alongside
-- the one already recorded for the last apply, so "does the reviewed plan still
-- describe what would run?" is a single-row comparison instead of a scan over
-- run history.
--
-- Both config columns are nullable on purpose. '{}' is a legitimate config — it
-- is exactly what an all-optional template looks like — so a not-null default
-- would make "never planned" and "planned with an empty config" the same value.
-- Presence is carried by the run id columns, which follow the existing
-- last_applied_run_id convention of '' for absent.
alter table stack_templates
	add column if not exists last_applied_config_json jsonb,
	add column if not exists last_planned_run_id text not null default '',
	add column if not exists last_planned_template_revision_id text not null default '',
	add column if not exists last_planned_config_json jsonb,
	add column if not exists last_planned_at timestamptz;

-- Applied config comes from the run the template already points at.
update stack_templates template
set last_applied_config_json = run.config_json
from template_runs run
where run.tenant_id = template.tenant_id
	and run.id = template.last_applied_run_id
	and template.last_applied_run_id <> ''
	and template.last_applied_config_json is null;

-- Planned comes from each template's latest completed plan run, ordered the same
-- way ListTemplateRuns orders them so "latest" means the same thing here as it
-- does everywhere else.
with latest_plan as (
	select distinct on (run.tenant_id, run.stack_template_id)
		run.tenant_id,
		run.stack_template_id,
		run.id,
		run.template_revision_id,
		run.config_json,
		run.completed_at
	from template_runs run
	where run.operation = 'plan'
		and run.status = 'completed'
	order by run.tenant_id, run.stack_template_id, run.started_at desc nulls last, run.id desc
)
update stack_templates template
set
	last_planned_run_id = latest_plan.id,
	last_planned_template_revision_id = latest_plan.template_revision_id,
	last_planned_config_json = latest_plan.config_json,
	last_planned_at = latest_plan.completed_at
from latest_plan
where latest_plan.tenant_id = template.tenant_id
	and latest_plan.stack_template_id = template.id
	and template.last_planned_run_id = '';
