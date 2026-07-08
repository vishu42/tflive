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

alter table template_revisions add column source_template_id text not null default '';

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
	'source_template_' || substr(md5(tr.tenant_id || chr(0) || tr.repo_owner || chr(0) || tr.repo_name || chr(0) || tr.root_path || chr(0) || tr.source_ref), 1, 32),
	tr.tenant_id,
	tr.repo_owner,
	tr.repo_name,
	tr.source_ref,
	tr.root_path,
	(
		select t2.id
		from template_revisions t2
		where t2.tenant_id = tr.tenant_id
			and t2.repo_owner = tr.repo_owner
			and t2.repo_name = tr.repo_name
			and t2.root_path = tr.root_path
			and t2.source_ref = tr.source_ref
		order by t2.created_at desc, t2.id desc
		limit 1
	),
	min(tr.created_at),
	max(tr.created_at)
from template_revisions tr
group by tr.tenant_id, tr.repo_owner, tr.repo_name, tr.root_path, tr.source_ref;

update template_revisions tr
set source_template_id = source_templates.id
from source_templates
where tr.tenant_id = source_templates.tenant_id
	and tr.repo_owner = source_templates.repo_owner
	and tr.repo_name = source_templates.repo_name
	and tr.root_path = source_templates.root_path
	and tr.source_ref = source_templates.source_ref;

create index template_revisions_tenant_id_source_template_id_idx on template_revisions (tenant_id, source_template_id);
create unique index template_revisions_source_revision_idx on template_revisions (tenant_id, source_template_id, resolved_commit_sha);
drop index template_revisions_source_identity_idx;

alter table stack_templates add column component_key text not null default '';
alter table stack_templates add column source_template_id text not null default '';
alter table stack_templates add column desired_template_revision_id text not null default '';
alter table stack_templates add column last_applied_template_revision_id text not null default '';
alter table stack_templates add column desired_config_json jsonb not null default '{}'::jsonb;

update stack_templates
set
	component_key = case
		when stack_templates.component_key <> '' then stack_templates.component_key
		else stack_templates.id
	end,
	source_template_id = tr.source_template_id,
	desired_template_revision_id = stack_templates.template_revision_id,
	last_applied_template_revision_id = case
		when stack_templates.last_applied_run_id <> '' then stack_templates.template_revision_id
		else ''
	end,
	desired_config_json = stack_templates.config_json
from template_revisions tr
where stack_templates.tenant_id = tr.tenant_id
	and stack_templates.template_revision_id = tr.id;

create unique index stack_templates_active_component_key_idx on stack_templates (
	tenant_id,
	stack_id,
	component_key
) where lifecycle = 'active';

alter table template_runs add column template_revision_id text not null default '';
alter table template_runs add column source_template_id text not null default '';
alter table template_runs add column config_json jsonb not null default '{}'::jsonb;
