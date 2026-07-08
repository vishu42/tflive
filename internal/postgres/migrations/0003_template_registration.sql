create table template_revisions (
	id text primary key,
	tenant_id text not null,
	repo_owner text not null,
	repo_name text not null,
	source_ref text not null,
	resolved_commit_sha text not null,
	root_path text not null,
	name text not null,
	description text not null default '',
	tags_json jsonb not null default '[]'::jsonb,
	status text not null,
	created_at timestamptz not null default now(),
	constraint template_revisions_status_check check (
		status in ('pending_validation', 'validating', 'active', 'invalid')
	)
);

create unique index template_revisions_source_identity_idx on template_revisions (
	tenant_id,
	repo_owner,
	repo_name,
	root_path,
	resolved_commit_sha
);
create index template_revisions_tenant_id_id_idx on template_revisions (tenant_id, id);

create table template_registrations (
	id text primary key,
	tenant_id text not null,
	repo_owner text not null,
	repo_name text not null,
	source_ref text not null,
	root_path text not null,
	status text not null,
	template_revision_id text not null default '',
	resolved_commit_sha text not null default '',
	requested_by text not null,
	requested_at timestamptz not null,
	completed_at timestamptz,
	error_summary text not null default '',
	constraint template_registrations_status_check check (
		status in ('pending', 'running', 'completed', 'invalid', 'failed')
	)
);

create index template_registrations_tenant_id_id_idx on template_registrations (tenant_id, id);
create index template_registrations_tenant_id_status_idx on template_registrations (tenant_id, status);

create table template_variables (
	template_revision_id text not null references template_revisions (id) on delete cascade,
	name text not null,
	type_expression text not null default '',
	description text not null default '',
	required boolean not null,
	has_default boolean not null,
	sensitive boolean not null,
	has_validation boolean not null,
	primary key (template_revision_id, name)
);
