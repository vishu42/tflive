create table stacks (
	id text primary key,
	tenant_id text not null,
	name text not null,
	slug text not null,
	tags_json jsonb not null default '{}'::jsonb,
	default_credential_ids_json jsonb not null default '[]'::jsonb,
	created_by text not null,
	created_at timestamptz not null
);

create unique index stacks_tenant_id_slug_idx on stacks (tenant_id, slug);
create index stacks_tenant_id_id_idx on stacks (tenant_id, id);
