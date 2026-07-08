create table source_templates (
	id text primary key,
	tenant_id text not null,
	repo_owner text not null,
	repo_name text not null,
	source_ref text not null,
	root_path text not null,
	latest_template_revision_id text not null default '',
	created_at timestamptz not null default now(),
	updated_at timestamptz not null default now()
);

create unique index source_templates_identity_idx on source_templates (
	tenant_id,
	repo_owner,
	repo_name,
	root_path,
	source_ref
);
create index source_templates_tenant_id_id_idx on source_templates (tenant_id, id);

alter table templates add column source_template_id text not null default '';

insert into source_templates (
	id,
	tenant_id,
	repo_owner,
	repo_name,
	source_ref,
	root_path,
	latest_template_revision_id,
	created_at,
	updated_at
)
select
	'source_template_' || substr(md5(tenant_id || chr(0) || repo_owner || chr(0) || repo_name || chr(0) || root_path || chr(0) || source_ref), 1, 32),
	tenant_id,
	repo_owner,
	repo_name,
	source_ref,
	root_path,
	(
		select t2.id
		from templates t2
		where t2.tenant_id = templates.tenant_id
			and t2.repo_owner = templates.repo_owner
			and t2.repo_name = templates.repo_name
			and t2.root_path = templates.root_path
			and t2.source_ref = templates.source_ref
		order by t2.created_at desc, t2.id desc
		limit 1
	),
	min(created_at),
	max(created_at)
from templates
group by tenant_id, repo_owner, repo_name, root_path, source_ref;

update templates
set source_template_id = source_templates.id
from source_templates
where templates.tenant_id = source_templates.tenant_id
	and templates.repo_owner = source_templates.repo_owner
	and templates.repo_name = source_templates.repo_name
	and templates.root_path = source_templates.root_path
	and templates.source_ref = source_templates.source_ref;

create index templates_tenant_id_source_template_id_idx on templates (tenant_id, source_template_id);
create unique index templates_source_revision_idx on templates (tenant_id, source_template_id, resolved_commit_sha);
drop index templates_source_identity_idx;

alter table stack_templates add column component_key text not null default '';
alter table stack_templates add column source_template_id text not null default '';
alter table stack_templates add column desired_template_id text not null default '';
alter table stack_templates add column last_applied_template_id text not null default '';
alter table stack_templates add column desired_config_json jsonb not null default '{}'::jsonb;

update stack_templates
set
	component_key = case
		when stack_templates.component_key <> '' then stack_templates.component_key
		else stack_templates.id
	end,
	source_template_id = templates.source_template_id,
	desired_template_id = stack_templates.template_id,
	last_applied_template_id = case
		when stack_templates.last_applied_run_id <> '' then stack_templates.template_id
		else ''
	end,
	desired_config_json = stack_templates.config_json
from templates
where stack_templates.tenant_id = templates.tenant_id
	and stack_templates.template_id = templates.id;

create unique index stack_templates_active_component_key_idx on stack_templates (
	tenant_id,
	stack_id,
	component_key
) where lifecycle = 'active';

alter table template_runs add column template_id text not null default '';
alter table template_runs add column source_template_id text not null default '';
alter table template_runs add column config_json jsonb not null default '{}'::jsonb;
