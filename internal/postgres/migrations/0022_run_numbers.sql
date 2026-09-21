-- A human-readable number per stack template: Run #1, #2, ... The random id
-- stays the primary key; it is what Temporal workflow ids, log keys and foreign
-- keys use.
--
-- There is no counter column. createTemplateRun assigns max(run_number) + 1 in
-- the insert itself, and that cannot hand out a duplicate in practice: two
-- concurrent inserts for one stack template are already serialized by
-- template_runs_in_flight_idx (0021), so at most one of them commits. The unique
-- index below is the backstop if that rule ever loosens, and it also serves the
-- max() lookup.
--
-- Existing runs are numbered in start order. tflive is pre-production, so this
-- is a one-shot renumbering rather than a compatibility path.
alter table template_runs add column run_number integer;

update template_runs
set run_number = numbered.run_number
from (
	select
		tenant_id,
		id,
		row_number() over (
			partition by tenant_id, stack_template_id
			order by started_at nulls last, id
		) as run_number
	from template_runs
) as numbered
where template_runs.tenant_id = numbered.tenant_id
	and template_runs.id = numbered.id;

alter table template_runs alter column run_number set not null;

create unique index template_runs_run_number_idx
	on template_runs (tenant_id, stack_template_id, run_number);
