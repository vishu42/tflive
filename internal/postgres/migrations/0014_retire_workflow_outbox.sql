insert into work_queue (kind, resource_key, payload, actor_subject, tenant_id)
select
	'start_template_run',
	'run:' || run.tenant_id || '/' || run.id,
	jsonb_build_object(
		'RunID', run.id,
		'TenantID', run.tenant_id,
		'StackTemplateID', run.stack_template_id,
		'Operation', run.operation,
		'SelectedRef', run.selected_ref,
		'WorkspaceName', run.workspace_name,
		'RepoOwner', revision.repo_owner,
		'RepoName', revision.repo_name,
		'RootPath', revision.root_path,
		'ConfigJSON', run.config_json
	),
	run.trigger_actor,
	run.tenant_id
from workflow_outbox outbox
join template_runs run on run.id = outbox.aggregate_id
join template_revisions revision on revision.id = run.template_revision_id
where outbox.event_type = 'start_template_run'
	and outbox.processed_at is null
on conflict (kind, resource_key) where processed_at is null do nothing;

drop table workflow_outbox;
