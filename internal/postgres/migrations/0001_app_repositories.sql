create table stack_templates (
	id text primary key,
	tenant_id text not null,
	stack_id text not null,
	template_id text not null,
	selected_ref text not null,
	workspace_name text not null,
	config_json jsonb not null default '{}'::jsonb,
	last_applied_run_id text not null default '',
	last_applied_ref text not null default '',
	last_applied_at timestamptz,
	lifecycle text not null,
	constraint stack_templates_lifecycle_check check (
		lifecycle in ('active', 'destroying', 'destroyed', 'failed', 'orphaned')
	)
);

create index stack_templates_tenant_id_id_idx on stack_templates (tenant_id, id);

create table template_runs (
	id text primary key,
	tenant_id text not null,
	stack_template_id text not null,
	operation text not null,
	selected_ref text not null,
	resolved_commit_sha text not null default '',
	workspace_name text not null,
	backend_type text not null default '',
	backend_config_hash text not null default '',
	status text not null,
	trigger_actor text not null,
	started_at timestamptz,
	completed_at timestamptz,
	error_summary text not null default '',
	cancellation_requested_by text not null default '',
	cancellation_reason text not null default '',
	cancellation_requested_at timestamptz,
	constraint template_runs_operation_check check (
		operation in ('plan', 'apply', 'destroy')
	),
	constraint template_runs_status_check check (
		status in (
			'queued',
			'locked',
			'workspace_prepared',
			'source_fetched',
			'init',
			'workspace_selected',
			'planned',
			'waiting_approval',
			'approved',
			'apply_started',
			'applied',
			'destroy_started',
			'destroyed',
			'cancel_requested',
			'canceling',
			'canceled',
			'lock_released',
			'completed',
			'failed'
		)
	)
);

create index template_runs_tenant_id_id_idx on template_runs (tenant_id, id);
create index template_runs_tenant_id_stack_template_id_idx on template_runs (tenant_id, stack_template_id);
create index template_runs_tenant_id_status_idx on template_runs (tenant_id, status);

create table template_run_approvals (
	run_id text primary key references template_runs (id) on delete cascade,
	tenant_id text not null,
	approved_by text not null,
	approved_at timestamptz not null
);

create index template_run_approvals_tenant_id_run_id_idx on template_run_approvals (tenant_id, run_id);
