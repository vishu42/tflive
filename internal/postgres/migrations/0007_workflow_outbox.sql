create table workflow_outbox (
	id text primary key,
	event_type text not null check (event_type in ('start_template_run')),
	aggregate_id text not null references template_runs (id) on delete cascade,
	available_at timestamptz not null default now(),
	claimed_until timestamptz,
	attempts integer not null default 0 check (attempts >= 0),
	processed_at timestamptz,
	last_error text not null default '',
	created_at timestamptz not null default now()
);

create index workflow_outbox_pending_idx
	on workflow_outbox (available_at, id)
	where processed_at is null;

insert into workflow_outbox (id, event_type, aggregate_id)
select
	'template-run/' || tenant_id || '/' || id,
	'start_template_run',
	id
from template_runs
where status = 'queued'
on conflict (id) do nothing;
