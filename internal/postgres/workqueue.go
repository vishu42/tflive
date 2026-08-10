package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/vishu42/tflive/internal/queue"
)

// pgxExecutor is satisfied by both *pgxpool.Pool and pgx.Tx, so enqueue works
// standalone or inside a caller's transaction.
type pgxExecutor interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// enqueueReconcileSQL coalesces: the newest desired state wins and the revision
// bump fences any worker already in flight on this row.
const enqueueReconcileSQL = `
	insert into work_queue (kind, resource_key, payload, actor_subject, tenant_id)
	values ($1, $2, $3, $4, $5)
	on conflict (kind, resource_key) where processed_at is null
	do update set payload  = excluded.payload,
	              revision = work_queue.revision + 1
`

// enqueueJobSQL keeps distinct work: re-enqueueing the same key is a no-op, so
// an accidental double submit cannot duplicate the work.
const enqueueJobSQL = `
	insert into work_queue (kind, resource_key, payload, actor_subject, tenant_id)
	values ($1, $2, $3, $4, $5)
	on conflict (kind, resource_key) where processed_at is null
	do nothing
`

// Enqueue writes work intents. Use the transaction-bound enqueuer from InTx
// when the intent must commit atomically with a domain write.
func (store *Store) Enqueue(ctx context.Context, requests ...queue.Request) error {
	return enqueueRequests(ctx, store.pool, store.queueSpecs, requests...)
}

func enqueueRequests(ctx context.Context, exec pgxExecutor, specs *queue.SpecRegistry, requests ...queue.Request) error {
	if len(requests) == 0 {
		return nil
	}
	if specs == nil {
		return fmt.Errorf("enqueue work: no queue specs configured")
	}
	for _, request := range requests {
		resolved, err := specs.Resolve(request)
		if err != nil {
			return fmt.Errorf("resolve work request: %w", err)
		}
		statement := enqueueReconcileSQL
		if resolved.Mode == queue.ModeJob {
			statement = enqueueJobSQL
		}
		if _, err := exec.Exec(ctx, statement,
			string(resolved.Kind),
			resolved.Key,
			[]byte(resolved.Payload),
			resolved.ActorSubject,
			resolved.TenantID,
		); err != nil {
			return fmt.Errorf("enqueue %s work: %w", resolved.Kind, err)
		}
	}
	return nil
}

// claimWorkSQL leases ready rows. Rows another worker already locked are
// skipped rather than waited on, so claims scale across workers instead of
// serialising behind the first one.
//
// Every timestamp comes from the database, never from the caller. Lease expiry
// is the one judgement several workers must agree on, and comparing a stored
// deadline against each worker's own clock makes that agreement depend on their
// clocks being in step.
const claimWorkSQL = `
	with candidate as (
		select id from work_queue
		where processed_at is null
		  and available_at <= now()
		  and (claimed_until is null or claimed_until <= now())
		  and ($3::text[] is null or kind = any($3))
		order by available_at, id
		for update skip locked
		limit $2
	), claimed as (
		update work_queue q
		   set claimed_until = now() + $1::interval, attempts = attempts + 1
		  from candidate
		 where q.id = candidate.id
	 returning q.id, q.kind, q.resource_key, q.payload, q.revision,
	           q.actor_subject, q.tenant_id, q.attempts
	) select id, kind, resource_key, payload, revision, actor_subject, tenant_id, attempts
	    from claimed
`

// Claim leases up to limit ready rows for the given duration. A nil kinds slice
// claims every kind.
func (store *Store) Claim(ctx context.Context, lease time.Duration, limit int, kinds []queue.Kind) ([]queue.Item, error) {
	var kindFilter []string
	if len(kinds) > 0 {
		kindFilter = make([]string, len(kinds))
		for index, kind := range kinds {
			kindFilter[index] = string(kind)
		}
	}

	rows, err := store.pool.Query(ctx, claimWorkSQL, lease, limit, kindFilter)
	if err != nil {
		return nil, fmt.Errorf("claim work: %w", err)
	}
	defer rows.Close()

	var items []queue.Item
	for rows.Next() {
		var item queue.Item
		var kind string
		var payload []byte
		if err := rows.Scan(&item.ID, &kind, &item.Key, &payload, &item.Revision, &item.ActorSubject, &item.TenantID, &item.Attempts); err != nil {
			return nil, fmt.Errorf("scan claimed work: %w", err)
		}
		item.Kind = queue.Kind(kind)
		item.Payload = append(json.RawMessage(nil), payload...)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate claimed work: %w", err)
	}
	return items, nil
}

// Complete marks an item delivered. It reports false when the revision moved
// while the item was in flight, meaning a newer intent coalesced onto the row
// and must run instead of being overwritten by this stale success.
func (store *Store) Complete(ctx context.Context, id, revision int64) (bool, error) {
	tag, err := store.pool.Exec(ctx, `
		update work_queue
		   set processed_at = now(), claimed_until = null, last_error = ''
		 where id = $1 and revision = $2 and processed_at is null
	`, id, revision)
	if err != nil {
		return false, fmt.Errorf("complete work: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// Reschedule returns an item to the pool, clearing its lease. The delay is
// applied to the database clock for the same reason Claim reads it there.
func (store *Store) Reschedule(ctx context.Context, id int64, delay time.Duration, lastErr string) error {
	if _, err := store.pool.Exec(ctx, `
		update work_queue
		   set claimed_until = null, available_at = now() + $2::interval, last_error = $3
		 where id = $1 and processed_at is null
	`, id, delay, lastErr); err != nil {
		return fmt.Errorf("reschedule work: %w", err)
	}
	return nil
}

// pruneLockKeyExpr derives the prune advisory lock id from the current schema.
//
// Advisory locks live in one namespace per database, but a work_queue table
// belongs to a schema. A constant would make every deployment sharing a
// database — and every test running against its own schema — contend for one
// lock and block each other's pruning for no reason. Hashing the schema name
// scopes the lock to the table it actually protects.
const pruneLockKeyExpr = `('x' || substr(md5(current_schema() || '.work_queue.prune'), 1, 16))::bit(64)::bigint`

// Prune deletes rows processed longer ago than retention, keeping the pending
// indexes small.
//
// Only one worker prunes per round. Every controller runs the same maintenance
// loop, so without this they would all delete the same rows on the same
// schedule; a transaction-scoped advisory lock elects one of them for the round
// and releases at commit, with no lease or leader table to keep alive. A worker
// that loses the race reports zero rows and no error — it did not fail, it
// simply had nothing to do.
func (store *Store) Prune(ctx context.Context, retention time.Duration) (int64, error) {
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin prune: %w", err)
	}
	defer tx.Rollback(ctx)

	var acquired bool
	if err := tx.QueryRow(ctx, `select pg_try_advisory_xact_lock(`+pruneLockKeyExpr+`)`).Scan(&acquired); err != nil {
		return 0, fmt.Errorf("lock prune: %w", err)
	}
	if !acquired {
		return 0, nil
	}

	tag, err := tx.Exec(ctx, `
		delete from work_queue
		 where processed_at is not null
		   and processed_at < now() - $1::interval
	`, retention)
	if err != nil {
		return 0, fmt.Errorf("prune work: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit prune: %w", err)
	}
	return tag.RowsAffected(), nil
}

// ListByActor returns the items a caller queued. There is no permission concept
// beyond this: a caller only ever sees their own items.
//
// Attempts and LastError are surfaced deliberately. Retry is forever, so a
// stuck item stays pending indefinitely and those fields are the only way to
// tell "queued two seconds ago" from "failing for an hour".
func (store *Store) ListByActor(ctx context.Context, tenantID, actorSubject string, limit int) ([]queue.Status, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := store.pool.Query(ctx, `
		select kind, attempts, last_error, created_at, processed_at
		  from work_queue
		 where tenant_id = $1 and actor_subject = $2
		 order by created_at desc
		 limit $3
	`, tenantID, actorSubject, limit)
	if err != nil {
		return nil, fmt.Errorf("list work by actor: %w", err)
	}
	defer rows.Close()

	statuses := []queue.Status{}
	for rows.Next() {
		var status queue.Status
		var kind string
		var processedAt *time.Time
		if err := rows.Scan(&kind, &status.Attempts, &status.LastError, &status.CreatedAt, &processedAt); err != nil {
			return nil, fmt.Errorf("scan work status: %w", err)
		}
		status.Kind = queue.Kind(kind)
		status.State = queue.StatePending
		if processedAt != nil {
			status.State = queue.StateDelivered
			status.DeliveredAt = processedAt
		}
		statuses = append(statuses, status)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate work statuses: %w", err)
	}
	return statuses, nil
}
