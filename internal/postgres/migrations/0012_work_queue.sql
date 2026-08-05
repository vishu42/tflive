create table work_queue (
	id            bigserial primary key,
	kind          text not null,
	ordering_key  text not null,
	payload       jsonb not null,
	revision      bigint not null default 1,
	actor_subject text not null default '',
	tenant_id     text not null default '',
	available_at  timestamptz not null default now(),
	claimed_until timestamptz,
	attempts      integer not null default 0 check (attempts >= 0),
	last_error    text not null default '',
	created_at    timestamptz not null default now(),
	processed_at  timestamptz
);

-- Load-bearing: at most one pending row per (kind, ordering_key). This gives
-- coalescing for reconcile kinds and per-key mutual exclusion for every kind,
-- because a second worker cannot claim a row that structurally cannot exist.
create unique index work_queue_pending_key_idx
	on work_queue (kind, ordering_key)
	where processed_at is null;

create index work_queue_ready_idx
	on work_queue (available_at, id)
	where processed_at is null;

create index work_queue_actor_idx
	on work_queue (tenant_id, actor_subject, created_at desc);

-- Backfill undelivered authorization intents. authorization_outbox stores
-- prefixed identifiers; the payload carries raw ids and the ordering key
-- carries the prefixed forms, matching the handler's key derivation. A revoke
-- becomes an empty role, which the handler reads as "no access".
insert into work_queue (kind, ordering_key, payload, actor_subject, tenant_id)
select
	'reconcile_stack_grant',
	stack || '/' || subject,
	jsonb_build_object(
		'stack_id', replace(stack, 'stack:', ''),
		'subject', replace(subject, 'user:', ''),
		'role', case when operation = 'grant' then role else '' end
	),
	'',
	''
from authorization_outbox
where processed_at is null and failed_at is null
on conflict (kind, ordering_key) where processed_at is null do nothing;
