create table security_audit_log (
    id              bigserial primary key,
    recorded_at     timestamptz not null default now(),
    actor_subject   text not null,
    action          text not null,
    target_user     text not null default '',
    tenant_id       text not null,
    stack_id        text not null default '',
    old_role        text not null default '',
    new_role        text not null default '',
    outcome         text not null check (outcome in ('success', 'failure')),
    correlation_id  text not null default ''
);

create index security_audit_log_actor_idx
    on security_audit_log (actor_subject, recorded_at desc);

create index security_audit_log_tenant_idx
    on security_audit_log (tenant_id, recorded_at desc);

create index security_audit_log_stack_idx
    on security_audit_log (stack_id, recorded_at desc)
    where stack_id != '';
