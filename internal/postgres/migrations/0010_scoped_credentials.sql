create table scoped_credentials (
	id text primary key,
	tenant_id text not null,
	stack_id text references stacks(id) on delete cascade,
	stack_template_id text references stack_templates(id) on delete cascade,
	name text not null,
	ciphertext text not null,
	created_at timestamptz not null,
	check ((stack_id is not null) <> (stack_template_id is not null))
);

create unique index scoped_credentials_stack_name_idx
on scoped_credentials (tenant_id, stack_id, name)
where stack_id is not null;

create unique index scoped_credentials_template_name_idx
on scoped_credentials (tenant_id, stack_template_id, name)
where stack_template_id is not null;

create index scoped_credentials_tenant_id_idx on scoped_credentials (tenant_id, id);
