alter table stacks add column status text not null default 'ready'
	check (status in ('provisioning', 'ready'));

-- Existing stacks are ready: their owner tuples were written by the old
-- synchronous path. The exception is a stack whose grant is still queued.
update stacks set status = 'provisioning'
where exists (
	select 1 from work_queue
	 where processed_at is null
	   and ordering_key like 'stack:' || stacks.id || '%'
);
