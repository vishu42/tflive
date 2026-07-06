create table template_run_logs (
	tenant_id text not null,
	run_id text not null references template_runs (id) on delete cascade,
	phase text not null,
	object_key text not null,
	content_type text not null,
	size_bytes bigint not null,
	uploaded_at timestamptz not null,
	primary key (tenant_id, run_id, phase),
	constraint template_run_logs_phase_check check (
		phase in ('clone', 'init', 'workspace', 'plan', 'apply', 'destroy')
	),
	constraint template_run_logs_size_bytes_check check (size_bytes >= 0)
);

create index template_run_logs_tenant_id_run_id_idx on template_run_logs (tenant_id, run_id);
