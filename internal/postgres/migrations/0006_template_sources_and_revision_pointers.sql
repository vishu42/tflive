do $$
begin
	if to_regclass('template_revisions') is null and to_regclass('templates') is not null then
		alter table templates rename to template_revisions;
	end if;

	if exists (
		select 1
		from information_schema.columns
		where table_schema = current_schema()
			and table_name = 'template_registrations'
			and column_name = 'template_id'
	) and not exists (
		select 1
		from information_schema.columns
		where table_schema = current_schema()
			and table_name = 'template_registrations'
			and column_name = 'template_revision_id'
	) then
		alter table template_registrations rename column template_id to template_revision_id;
	end if;

	if exists (
		select 1
		from information_schema.columns
		where table_schema = current_schema()
			and table_name = 'template_variables'
			and column_name = 'template_id'
	) and not exists (
		select 1
		from information_schema.columns
		where table_schema = current_schema()
			and table_name = 'template_variables'
			and column_name = 'template_revision_id'
	) then
		alter table template_variables rename column template_id to template_revision_id;
	end if;

	if exists (
		select 1
		from information_schema.columns
		where table_schema = current_schema()
			and table_name = 'stack_templates'
			and column_name = 'template_id'
	) and not exists (
		select 1
		from information_schema.columns
		where table_schema = current_schema()
			and table_name = 'stack_templates'
			and column_name = 'template_revision_id'
	) then
		alter table stack_templates rename column template_id to template_revision_id;
	end if;

	if exists (
		select 1
		from information_schema.columns
		where table_schema = current_schema()
			and table_name = 'stack_templates'
			and column_name = 'desired_template_id'
	) and not exists (
		select 1
		from information_schema.columns
		where table_schema = current_schema()
			and table_name = 'stack_templates'
			and column_name = 'desired_template_revision_id'
	) then
		alter table stack_templates rename column desired_template_id to desired_template_revision_id;
	end if;

	if exists (
		select 1
		from information_schema.columns
		where table_schema = current_schema()
			and table_name = 'stack_templates'
			and column_name = 'last_applied_template_id'
	) and not exists (
		select 1
		from information_schema.columns
		where table_schema = current_schema()
			and table_name = 'stack_templates'
			and column_name = 'last_applied_template_revision_id'
	) then
		alter table stack_templates rename column last_applied_template_id to last_applied_template_revision_id;
	end if;

	if exists (
		select 1
		from information_schema.columns
		where table_schema = current_schema()
			and table_name = 'template_runs'
			and column_name = 'template_id'
	) and not exists (
		select 1
		from information_schema.columns
		where table_schema = current_schema()
			and table_name = 'template_runs'
			and column_name = 'template_revision_id'
	) then
		alter table template_runs rename column template_id to template_revision_id;
	end if;
end $$;

create table if not exists source_templates (
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

create unique index if not exists source_templates_identity_idx on source_templates (
	tenant_id,
	repo_owner,
	repo_name,
	root_path,
	source_ref
);
create index if not exists source_templates_tenant_id_id_idx on source_templates (tenant_id, id);

alter table template_revisions add column if not exists source_template_id text not null default '';

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
	'source_template_' || substr(md5(tr.tenant_id || chr(31) || tr.repo_owner || chr(31) || tr.repo_name || chr(31) || tr.root_path || chr(31) || tr.source_ref), 1, 32),
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
group by tr.tenant_id, tr.repo_owner, tr.repo_name, tr.root_path, tr.source_ref
on conflict (id) do update
set
	latest_template_revision_id = excluded.latest_template_revision_id,
	updated_at = excluded.updated_at;

update template_revisions tr
set source_template_id = source_templates.id
from source_templates
where tr.tenant_id = source_templates.tenant_id
	and tr.repo_owner = source_templates.repo_owner
	and tr.repo_name = source_templates.repo_name
	and tr.root_path = source_templates.root_path
	and tr.source_ref = source_templates.source_ref;

create index if not exists template_revisions_tenant_id_source_template_id_idx on template_revisions (tenant_id, source_template_id);
create unique index if not exists template_revisions_source_revision_idx on template_revisions (tenant_id, source_template_id, resolved_commit_sha);
drop index if exists template_revisions_source_identity_idx;
drop index if exists templates_source_identity_idx;

alter table stack_templates add column if not exists component_key text not null default '';
alter table stack_templates add column if not exists source_template_id text not null default '';
alter table stack_templates add column if not exists desired_template_revision_id text not null default '';
alter table stack_templates add column if not exists last_applied_template_revision_id text not null default '';
alter table stack_templates add column if not exists desired_config_json jsonb not null default '{}'::jsonb;

do $$
begin
	if exists (
		select 1
		from information_schema.columns
		where table_schema = current_schema()
			and table_name = 'stack_templates'
			and column_name = 'template_revision_id'
	) then
		execute $backfill$
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
				and stack_templates.template_revision_id = tr.id
		$backfill$;
	else
		update stack_templates
		set
			component_key = case
				when stack_templates.component_key <> '' then stack_templates.component_key
				else stack_templates.id
			end,
			desired_config_json = stack_templates.config_json
		where stack_templates.desired_config_json = '{}'::jsonb;
	end if;
end $$;

create unique index if not exists stack_templates_active_component_key_idx on stack_templates (
	tenant_id,
	stack_id,
	component_key
) where lifecycle = 'active';

alter table template_runs add column if not exists template_revision_id text not null default '';
alter table template_runs add column if not exists source_template_id text not null default '';
alter table template_runs add column if not exists config_json jsonb not null default '{}'::jsonb;
