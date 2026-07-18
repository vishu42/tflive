create table authorization_outbox (
	id text primary key,
	operation text not null check (operation in ('grant', 'revoke')),
	subject text not null,
	stack text not null,
	role text not null check (role in ('owner', 'operator', 'approver', 'viewer')),
	available_at timestamptz not null default now(),
	claimed_until timestamptz,
	attempts integer not null default 0 check (attempts >= 0),
	processed_at timestamptz,
	failed_at timestamptz,
	last_error text not null default '',
	created_at timestamptz not null default now()
);

create index authorization_outbox_pending_idx
	on authorization_outbox (available_at, id)
	where processed_at is null and failed_at is null;
