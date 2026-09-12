# One Authorization Package Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace `internal/authz`, `internal/openfga` and `internal/authorizer` with one package that embeds OpenFGA in-process, writes tuples inside the caller's Postgres transaction, and exposes plain `(bool, error)` answers with no provider-neutral port.

**Architecture:** `internal/authorization` owns everything — the model DSL, the embedded `openfga.Server`, a datastore wrapper that routes writes onto a `pgx.Tx` taken from the context, and the handful of functions callers actually use. No `Authorizer` interface, no neutral request/result types, no adapter layer, no HTTP client. Because tuple writes join the caller's transaction, the three authorization queue kinds and their handlers are deleted and stack creation becomes synchronous.

**Tech Stack:** Go, `github.com/openfga/openfga` (embedded — *not* the HTTP SDK), `pgx/v5`, `squirrel`, Postgres, testify.

**Background:** `docs/superpowers/specs/2026-09-12-embedded-openfga-considerations.md` records the options that were evaluated and rejected. This plan is self-contained; the spec is only needed to understand *why* other designs were not chosen.

---

## Scope

### In scope

1. **One package for everything authorization.** `internal/authz`, `internal/openfga`, `internal/authorizer` and the root `openfga/` package collapse into `internal/authorization`.
2. **Single commit for any action spanning the app database and an OpenFGA write.** Stack creation and stack role changes commit the domain row, the audit event and the tuples in one transaction.
3. **`POST /v1/stacks` becomes synchronous.** It returns a usable stack; no `provisioning` state, no polling.
4. **The queue-based path for OpenFGA writes is removed** — all three kinds (`grant_stack_owner`, `mark_stack_ready`, `reconcile_stack_grant`), their handlers, specs, payloads and key functions.

### Consequences that follow, also in scope

5. **The OpenFGA container is retired**, along with `openfga-migrate`, `openfga-provision`, `cmd/openfga-provisioner`, `Dockerfile.openfga-provisioner` and `docker-compose.app.yaml`. Result: one `docker compose up`, no ID-pinning step.
6. **The worker loses all authorization code.** Queue handlers drop from 7 to 4; it never imports the authorization package.
7. **The provider-neutral port is deleted** — `authz.Authorizer`, `SubjectGrantLister`, `Relationships`, the neutral request/result types, `classify()`, and the `ErrUnavailable`/`ErrTimeout`/`ErrMalformedResponse`/`ErrWriteUnconfirmed` vocabulary with `authz.HTTPStatus`.
8. **`docs/openapi.yaml` is updated** — the `StackStatus` enum, the queue-driven creation note, and the queued-work example.
9. **A `make differential-test` target** so the guard on the copied write path can actually be run.

### Out of scope

- **Dropping `stacks.status`.** Decided 2026-09-12: keep the column, the domain constant `StackStatusReady`, and the API field. Only the `provisioning` value retires. The column becomes single-valued and that is accepted; dropping it later is trivial if it is still single-valued.
- **Anything touching the web frontend.** Verified: nothing in `web/src` reads `provisioning`.
- **The other five queue kinds** — `start_template_run`, `start_template_sync`, `signal_run_approval`, `signal_run_cancellation`, `notify_user` — and the queue machinery itself. All stay.
- **Temporal, workflows, activities, the runner.** Verified to reference no authorization at all.
- **Data migration of existing tuples.** Pre-production, disposable state. OpenFGA's tables move from the `openfga` database into the app database; local data is discarded.
- **Changing the authorization model.** `authorization-model.fga` moves package but its content is untouched.

### Non-goals

- **Supporting a second authorization provider.** The port is being deleted *because* this is a non-goal. Do not reintroduce an interface "for testability" — tests construct a real engine over `memory.New()`.
- **Preserving backward compatibility** of the API response shape or the queue table contents. No users; `AGENTS.md`-era "frozen contract" comments on the deleted queue keys do not apply once the kind is gone.

### Explicitly rejected — do not re-propose

| Rejected | Why |
|---|---|
| `github.com/openfga/go-sdk` | OpenAPI-generated HTTP client (`net/http` only, no `pkg/server`, no `storage.OpenFGADatastore`). Cannot join a `pgx.Tx`, so it keeps the container, the queue, the handlers and the dual write — ~975 fewer lines deleted than embedding. Simplicity comes from deleting the port, not from the SDK. |
| Plain embedding without the datastore wrapper | `postgres.Datastore.write` calls `s.primaryDB.BeginTx` and commits. Same pool, same database, same process — still two commits. |
| Grants in our own table + contextual tuples | Mechanically sound (`storagewrappers.CombinedTupleReader` unions them) and deletes ~300 more lines, but moves graph traversal out of OpenFGA and into our query. Rejected on direction. |
| Two-phase commit | Unreachable — their code calls `txn.Commit()` directly, so it needs the same interception *plus* prepared-transaction operations. |
| Synchronous write with the outbox kept as repair | Not atomic. Retained only as the fallback if the differential test proves unmaintainable. |

---

## Decision log

| Date | Decision |
|---|---|
| 2026-09-12 | Embed OpenFGA in-process; do not use go-sdk. |
| 2026-09-12 | Achieve atomicity with a datastore wrapper that reads a `pgx.Tx` from the context. Verified by a compiling spike. |
| 2026-09-12 | Delete the provider port entirely. One concrete package, no interface. |
| 2026-09-12 | Delete the error vocabulary including `ErrUnavailable`. Verified nothing branches on it and the web client renders the same state for any non-401. `ErrInvalidInput` survives — four real control-flow uses. |
| 2026-09-12 | Remove all three authorization queue kinds, not just the creation one. |
| 2026-09-12 | Keep `stacks.status`; retire only the `provisioning` value. |
| 2026-09-12 | The worker gets no embedded server — it has no authorization work left. (Reverses an earlier "second embedded server" decision.) |
| 2026-09-12 | Filed [openfga/openfga#3302](https://github.com/openfga/openfga/issues/3302) asking upstream to export the four helpers. **Check it before Task 2** — if it landed, `write.go` shrinks from ~130 lines to ~30. |

---

## Global Constraints

- **Go directive floor is `go 1.25.7`.** OpenFGA v1.19.0+ declares it. `toolchain go1.25.14` and all four Dockerfiles (`golang:1.25.14-alpine3.23`) already clear it.
- **Pin `github.com/openfga/openfga` to an exact version.** `go mod tidy` drifts to v1.20.0; the source review behind this plan was v1.19.0.
- **OpenFGA tables live in the app database**, on the app's existing `*pgxpool.Pool`. A tuple write can only join our transaction if it is the same pool and the same database.
- **Denial is `false, nil`. Every returned error means "could not determine".** This is what keeps a failure from reading as a refusal: callers reach `ErrForbidden` only when `err == nil && !allowed`, so an error short-circuits before a 403 is reachable. The guarantee is in the signature, not in a sentinel.
- **The package exports no error sentinel for provider failures.** Authorization failures fall through to `server.go`'s default 500. With OpenFGA embedded there is no separate service to report as unavailable.
- **Table names OpenFGA owns:** `tuple`, `authorization_model`, `store`, `assertion`, `changelog`. Verified not to collide with ours. Its migrations are plain SQL with no `+goose StatementBegin` blocks, so our own runner applies them — **do not add goose.**
- **Integration tests must use a unique store ID per run.** They write real rows to a real database; a fixed ID fails on the second run with a duplicate-tuple error.
- **Commit style:** Conventional Commits per `AGENTS.md`. **The repo has no CI**; `go test ./...` from the root is the only safety net.

---

## Chunks and their outcomes

| Chunk | Tasks | Outcome — what is true when it is done | Verify by | Time |
|---|---|---|---|---|
| **A** | 1 | OpenFGA is a dependency. Nothing else changed. | `go test ./...` green | 1h |
| **B** | 2, 3 | **Proven: a tuple write rolls back with a Postgres transaction.** Nothing wired. | `make differential-test` | 6h |
| **C** | 4, 5, 6 | `internal/authorization` answers every authorization question in-process. Old packages still present. | The new package's suite | 2d |
| **D** | 7, 8 | **All three old packages are gone.** App and API call the new package directly. Container idle. | `docker compose stop openfga`, app still works | 1d |
| **E** | 9, 10 | **Stack creation and role changes are one commit.** Queue kinds gone, worker has no authorization code. | Create a stack → `"ready"` | 1d |
| **F** | 11 | **One `docker compose up`.** | `down -v` then `up -d --build` | 4h |

**Chunk B is the go/no-go.** A compiling spike exists so risk is low, but everything after assumes a tuple write can join a transaction.

**Chunk E cannot be implemented before D.** Deleting the outbox before transactional writes exist leaves the stack row and the tuple committing separately with no repair path — strictly worse than today.

---

## File Structure

**`internal/authorization/` (new, ~1,400 production lines replacing ~3,300):**

| File | Responsibility |
|---|---|
| `authorization.go` | What callers use: `Can`, `CanAll`, `ListGrants`, `Grant`, `Revoke`. |
| `relations.go` | `Relation`, `Object`, `Subject`, `Grant`, and the grantable/structural classification. Moved from `internal/authz`. |
| `server.go` | Lifecycle: migrations, datastore, `NewServerWithOpts`, `Close`. |
| `bootstrap.go` | Store and model resolution, with the ambiguity refusals kept. |
| `datastore.go` | `Datastore` wrapper embedding `*postgres.Datastore`, overriding `Write`. |
| `write.go` | `writeOnTx` — OpenFGA's write path with BeginTx/Commit removed. **The only file coupled to OpenFGA's schema.** |
| `migrate.go` | Applies OpenFGA's embedded migrations through our runner. |
| `tx.go` | `WithTx` / `TxFrom` and the `sqlcommon.Connector` shims over `pgx.Tx`. |
| `model.go` + `authorization-model.fga` | The DSL, moved from the root `openfga/` package. |

**Deleted entirely:** `internal/authz/`, `internal/openfga/`, `internal/authorizer/`, `openfga/`, `cmd/openfga-provisioner/`, `Dockerfile.openfga-provisioner`, `docker-compose.app.yaml`, `internal/app/grant_stack_owner_handler.go`, `internal/app/mark_stack_ready_handler.go`, `internal/app/stack_provisioning.go`.

**Modified:** `go.mod`, `Makefile`, `internal/app/authorization.go`, `internal/app/service.go`, `internal/api/server.go`, `internal/postgres/unitofwork.go`, `cmd/api/main.go`, `cmd/worker/main.go`, `internal/config/auth.go`, `internal/domain/stack.go`, `docker-compose.yaml`, `deploy/postgres/init.sh`, `.env.example`, `README.md`, `docs/openapi.yaml`, `docs/architecture.md`.

---

## The shape of the new API

Specified up front because it is the point of the change.

```go
package authorization

// Authorization answers authorization questions against an in-process OpenFGA.
//
// There is no interface over this type. There is one provider and there will
// only ever be one, so a port would buy nothing and cost a layer. Tests
// construct a real Authorization over memory.New() rather than a fake.
type Authorization struct {
	server    *server.Server
	store     *Datastore
	storeID   string
	modelID   string
	closeOnce sync.Once
}

// This package exports no sentinel for a failed answer. A denial is
// (false, nil); anything else is an ordinary wrapped error, which the API
// renders as 500. Callers reach ErrForbidden only when err is nil and allowed
// is false, so a failure can never be mistaken for a refusal -- that guarantee
// is in the signature, not in an error value.

func New(ctx context.Context, pool *pgxpool.Pool, storeName string) (*Authorization, error)
func (a *Authorization) Can(ctx context.Context, subject string, relation Relation, object Object) (bool, error)
func (a *Authorization) CanAll(ctx context.Context, subject string, checks []Check) ([]bool, error)
func (a *Authorization) ListGrants(ctx context.Context, object Object) ([]Grant, error)
func (a *Authorization) Grant(ctx context.Context, grants ...Grant) error
func (a *Authorization) Revoke(ctx context.Context, grants ...Grant) error
func (a *Authorization) Close()
```

`Grant` and `Revoke` are transactional: called under a context carrying a
`pgx.Tx`, they land in that transaction.

### How the transaction reaches the datastore

Verified against v1.19.0 — the context flows unbroken, with no
`context.Background()`, `WithoutCancel` or goroutine detachment in the path:

```
Work.InTx(ctx, fn)                       internal/postgres/unitofwork.go
│   tx := pool.Begin(ctx)                ← one transaction starts
│   fn(authorization.WithTx(ctx, tx), …) ← tx goes onto the context
│
├─ repo.CreateStack(ctx, stack)          INSERT INTO stacks      ─ on tx
├─ repo.AppendAuditEvent(ctx, …)         INSERT INTO audit       ─ on tx
└─ Authorization.Grant(ctx, owner, parent)
   └─ server.Write(ctx, …)               pkg/server/write.go:22
      └─ WriteCommand.Execute(ctx, …)
         └─ datastore.Write(ctx, …)      commands/write.go:96
            └─ OUR OVERRIDE: TxFrom(ctx) → found
               writeOnTx → SELECT … FOR UPDATE, INSERT tuple,
                           INSERT changelog                      ─ on tx
│
└─ tx.Commit(ctx)                        ← all of it, one commit
```

`Server.Write` does derive the context (`tracer.Start`,
`ContextWithRPCInfo`) but both wrap with `context.WithValue`, preserving values.

---

# Chunk A

### Task 1: Add the dependency without wiring it

**Files:** Modify `go.mod:3`

**Interfaces:** Consumes nothing. Produces `github.com/openfga/openfga` importable at a pinned version.

- [ ] **Step 1: Check the upstream issue first**

Open [openfga/openfga#3302](https://github.com/openfga/openfga/issues/3302). If the four helpers (`selectExistingRowsForWrite`, `executeDeleteTuples`, `executeWriteTuples`, `executeInsertChanges`) have been exported, Task 2 shrinks from ~130 lines to ~30 — pin that version instead and adapt Task 2 accordingly.

- [ ] **Step 2: Bump the go directive.** In `go.mod`, change `go 1.25.0` to `go 1.25.7`. Leave `toolchain go1.25.14`.

- [ ] **Step 3: Add the dependency, pinned**

```bash
go get github.com/openfga/openfga@v1.19.0
go mod tidy
grep 'openfga/openfga' go.mod
```

Expected `github.com/openfga/openfga v1.19.0`. If tidy moved it to v1.20.0, re-pin — the version is a deliberate choice, not whatever resolves.

- [ ] **Step 4: Verify the existing suite is unaffected**

```bash
go build ./... && go test ./...
```

Expected PASS. Nothing imports the dependency yet, so a failure means the MVS-forced `pgx` bump (5.7.6 → 5.10.x) broke something. Fix it here, where it is cheap to diagnose.

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum
git commit -m "build: add embedded OpenFGA dependency and raise go directive to 1.25.7"
```

---

# Chunk B

### Task 2: Transaction carrier, tx-aware datastore, migrations

**Files:**
- Create: `internal/authorization/tx.go`, `datastore.go`, `write.go`, `migrate.go`
- Test: `internal/authorization/datastore_test.go`
- Modify: `Makefile`

**Interfaces:**
- Consumes: nothing from this repository
- Produces: `WithTx`, `TxFrom`, `newDatastore(pool) (*Datastore, error)`, `Migrate(ctx, pool) error`, `*Datastore` satisfying `storage.OpenFGADatastore`

All four files below were validated by a compiling, vetting spike.

- [ ] **Step 1: Write `internal/authorization/tx.go`**

```go
// Package authorization owns everything this application knows about
// authorization: the model, the embedded OpenFGA server that evaluates it, the
// storage that persists its tuples, and the questions callers ask.
//
// There is deliberately no provider port. There is one provider and there will
// only ever be one, so an interface would add a layer without adding a choice.
package authorization

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/openfga/openfga/pkg/storage/sqlcommon"
)

// txKey is a private type so no other package can write a value this one would
// read back as a transaction.
type txKey struct{}

// WithTx marks ctx as running inside tx. Every OpenFGA write made under the
// returned context commits and rolls back with the caller's work.
func WithTx(ctx context.Context, tx pgx.Tx) context.Context {
	return context.WithValue(ctx, txKey{}, tx)
}

// TxFrom recovers the transaction WithTx installed.
//
//	WithTx(ctx, tx)      → tx, true
//	context.Background() → nil, false
func TxFrom(ctx context.Context) (pgx.Tx, bool) {
	tx, ok := ctx.Value(txKey{}).(pgx.Tx)
	return tx, ok
}

// txConnector presents a pgx.Tx through the Connector interface OpenFGA's
// row-getter expects. Upstream has an equivalent for its pool
// (pgxPoolConnector) but does not export it.
type txConnector struct{ tx pgx.Tx }

func (c txConnector) Connect(context.Context) (sqlcommon.Connection, error) {
	return txConnection{tx: c.tx}, nil
}

type txConnection struct{ tx pgx.Tx }

func (c txConnection) Query(ctx context.Context, stmt string, args ...any) (sqlcommon.Rows, error) {
	rows, err := c.tx.Query(ctx, stmt, args...)
	if err != nil {
		return nil, err
	}
	return pgxRows{rows}, nil
}

// Close is a no-op: the transaction belongs to the caller, and closing it here
// would end their unit of work halfway through.
func (c txConnection) Close() error { return nil }

// pgxRows adapts pgx.Rows to sqlcommon.Rows. pgx's Close returns nothing while
// sqlcommon's must return an error; upstream writes the same shim for its own
// pool path (pgxRowsWrapper).
type pgxRows struct{ pgx.Rows }

func (r pgxRows) Close() error {
	r.Rows.Close()
	return r.Rows.Err()
}

var (
	_ sqlcommon.Connector  = txConnector{}
	_ sqlcommon.Connection = txConnection{}
	_ sqlcommon.Rows       = pgxRows{}
)
```

- [ ] **Step 2: Write `internal/authorization/write.go`**

**This file is a deliberate transcription of `postgres.Datastore.write` with the
BeginTx and Commit removed.** Keep it a close mirror rather than a tidier
equivalent, so the two can be diffed when OpenFGA is upgraded. Task 3 is its
only guard.

```go
package authorization

import (
	"context"
	"errors"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"
	openfgav1 "github.com/openfga/api/proto/openfga/v1"

	"github.com/openfga/openfga/pkg/storage"
	"github.com/openfga/openfga/pkg/storage/postgres"
	"github.com/openfga/openfga/pkg/storage/sqlcommon"
	tupleUtils "github.com/openfga/openfga/pkg/tuple"
)

// Column lists duplicated from OpenFGA's own inserts. These and the two table
// names are the entire coupling surface of this package. If an upstream
// migration adds a column, differential_test.go is what catches it.
var (
	tupleColumns = []string{
		"store", "object_type", "object_id", "relation", "_user",
		"user_type", "condition_name", "condition_context", "ulid", "inserted_at",
	}
	changelogColumns = []string{
		"store", "object_type", "object_id", "relation", "_user",
		"condition_name", "condition_context", "operation", "ulid", "inserted_at",
	}
)

// writeOnTx mirrors postgres.Datastore.write with the BeginTx and Commit
// removed: the caller owns the transaction, so the tuple write lands or is
// discarded with the rest of their unit of work.
func writeOnTx(
	ctx context.Context,
	tx pgx.Tx,
	store string,
	deletes storage.Deletes,
	writes storage.Writes,
	opts storage.TupleWriteOptions,
	now time.Time,
) error {
	lockKeys := sqlcommon.MakeTupleLockKeys(deletes, writes)
	if len(lockKeys) == 0 {
		return nil
	}

	existing := map[string]*openfgav1.Tuple{}
	if err := selectForUpdate(ctx, tx, store, lockKeys, existing); err != nil {
		return err
	}

	deleteConditions, writeItems, changeLogItems, err := sqlcommon.GetDeleteWriteChangelogItems(
		store, existing,
		sqlcommon.WriteData{Deletes: deletes, Writes: writes, Opts: opts, Now: now},
	)
	if err != nil {
		return err
	}
	if err := execDeletes(ctx, tx, store, deleteConditions); err != nil {
		return err
	}
	if err := execInserts(ctx, tx, "tuple", tupleColumns, writeItems); err != nil {
		return err
	}
	return execInserts(ctx, tx, "changelog", changelogColumns, changeLogItems)
}

// selectForUpdate takes point locks on every row the write touches, which is
// what makes concurrent writes to the same tuple serialize rather than race.
// It is also what closes the read-then-write window in the last-owner guard.
func selectForUpdate(ctx context.Context, tx pgx.Tx, store string, keys []sqlcommon.TupleLockKey, existing map[string]*openfgav1.Tuple) error {
	inExpr, args := sqlcommon.BuildRowConstructorIN(keys)
	sb := sq.StatementBuilder.PlaceholderFormat(sq.Dollar).
		Select(sqlcommon.SQLIteratorColumns()...).
		From("tuple").
		Where(sq.Eq{"store": store}).
		Where(sq.Expr("(object_type, object_id, relation, _user, user_type) IN "+inExpr, args...)).
		Suffix("FOR UPDATE")

	getRows, err := sqlcommon.NewRowGetter(txConnector{tx: tx}, sb)
	if err != nil {
		return postgres.HandleSQLError(err)
	}
	iter := sqlcommon.NewSQLTupleIterator(getRows, postgres.HandleSQLError)
	defer iter.Stop()

	items, _, err := iter.ToArray(ctx, storage.PaginationOptions{PageSize: len(keys)})
	if err != nil {
		return err
	}
	for _, t := range items {
		existing[tupleUtils.TupleKeyToString(t.GetKey())] = t
	}
	return nil
}

// execDeletes removes tuples in batches. Deleting fewer rows than planned means
// another writer removed one between our read and our delete: a conflict, not a
// success. The caller must not be told the state they asked for is now true.
func execDeletes(ctx context.Context, tx pgx.Tx, store string, conds sq.Or) error {
	stbl := sq.StatementBuilder.PlaceholderFormat(sq.Dollar)
	for start, total := 0, len(conds); start < total; start += storage.DefaultMaxTuplesPerWrite {
		end := min(start+storage.DefaultMaxTuplesPerWrite, total)
		batch := conds[start:end]
		stmt, args, err := stbl.Delete("tuple").Where(sq.Eq{"store": store}).Where(batch).ToSql()
		if err != nil {
			return postgres.HandleSQLError(err)
		}
		res, err := tx.Exec(ctx, stmt, args...)
		if err != nil {
			return postgres.HandleSQLError(err)
		}
		if res.RowsAffected() != int64(len(batch)) {
			return storage.ErrWriteConflictOnDelete
		}
	}
	return nil
}

// execInserts writes tuple or changelog rows in batches. A unique-constraint
// violation on the tuple table is the insert-side mirror of the delete conflict
// above: someone else inserted the row we planned to add.
//
// One function for both tables: upstream has two near-identical ones differing
// only in table and columns.
func execInserts(ctx context.Context, tx pgx.Tx, table string, columns []string, items [][]any) error {
	stbl := sq.StatementBuilder.PlaceholderFormat(sq.Dollar)
	for start, total := 0, len(items); start < total; start += storage.DefaultMaxTuplesPerWrite {
		end := min(start+storage.DefaultMaxTuplesPerWrite, total)
		builder := stbl.Insert(table).Columns(columns...)
		for _, item := range items[start:end] {
			builder = builder.Values(item...)
		}
		stmt, args, err := builder.ToSql()
		if err != nil {
			return postgres.HandleSQLError(err)
		}
		if _, err := tx.Exec(ctx, stmt, args...); err != nil {
			dberr := postgres.HandleSQLError(err)
			if table == "tuple" && errors.Is(dberr, storage.ErrCollision) {
				return storage.ErrWriteConflictOnInsert
			}
			return dberr
		}
	}
	return nil
}
```

- [ ] **Step 3: Write `internal/authorization/datastore.go`**

```go
package authorization

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/openfga/openfga/pkg/storage"
	"github.com/openfga/openfga/pkg/storage/postgres"
	"github.com/openfga/openfga/pkg/storage/sqlcommon"
)

// Datastore is OpenFGA's Postgres datastore with one behaviour added: a write
// made under a context carrying a pgx.Tx runs on that transaction.
//
// Everything else -- reads, models, stores, assertions, the changelog -- is
// upstream's, inherited by embedding. Only Write is overridden, because only
// Write needs to be atomic with a domain change.
type Datastore struct {
	*postgres.Datastore
}

// newDatastore builds a datastore over an existing pool. The pool is already
// sized by the application, so the datastore must not resize it.
func newDatastore(pool *pgxpool.Pool) (*Datastore, error) {
	if pool == nil {
		return nil, fmt.Errorf("authorization: pool is required")
	}
	inner, err := postgres.NewWithDB(pool, nil, sqlcommon.NewConfig())
	if err != nil {
		return nil, fmt.Errorf("authorization: build datastore: %w", err)
	}
	return &Datastore{Datastore: inner}, nil
}

// Write applies a tuple mutation, on the caller's transaction when there is one
// and on its own when there is not.
//
// Both paths are load-bearing. The transactional path is what makes stack
// creation a single commit; the fall-through is what lets bootstrap.SeedRoot
// write root's tuple at startup, where no domain transaction exists.
//
//	WithTx(ctx, tx), writes → rows on tx, visible only after the caller commits
//	bare ctx, writes        → upstream's own transaction, committed immediately
func (store *Datastore) Write(
	ctx context.Context,
	storeID string,
	deletes storage.Deletes,
	writes storage.Writes,
	opts ...storage.TupleWriteOption,
) error {
	tx, ok := TxFrom(ctx)
	if !ok {
		return store.Datastore.Write(ctx, storeID, deletes, writes, opts...)
	}
	return writeOnTx(ctx, tx, storeID, deletes, writes, storage.NewTupleWriteOptions(opts...), time.Now().UTC())
}

// Datastore must remain a complete OpenFGA datastore, or it cannot be handed to
// server.WithDatastore.
var _ storage.OpenFGADatastore = (*Datastore)(nil)
```

- [ ] **Step 4: Write `internal/authorization/migrate.go`**

```go
package authorization

import (
	"context"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/openfga/openfga/assets"
)

// openfgaMigrationsTable tracks which of OpenFGA's migrations we have applied.
// Deliberately separate from the application's schema_migrations: the two
// sequences are versioned by different projects and must not share a counter.
const openfgaMigrationsTable = "openfga_schema_migrations"

// Migrate applies OpenFGA's embedded migrations to the application database.
//
// OpenFGA exports no migration runner, only a cobra command, so we apply the
// files ourselves. That is safe because they are plain SQL: none uses a
// "+goose StatementBegin" block, so splitting on the Up marker is sufficient
// and no goose dependency is needed.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, fmt.Sprintf(
		`create table if not exists %s (version text primary key, applied_at timestamptz not null default now())`,
		openfgaMigrationsTable,
	)); err != nil {
		return fmt.Errorf("create openfga migrations table: %w", err)
	}

	entries, err := fs.ReadDir(assets.EmbedMigrations, assets.PostgresMigrationDir)
	if err != nil {
		return fmt.Errorf("read openfga migrations: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		if err := applyOpenFGAMigration(ctx, pool, name); err != nil {
			return err
		}
	}
	return nil
}

func applyOpenFGAMigration(ctx context.Context, pool *pgxpool.Pool, name string) error {
	raw, err := fs.ReadFile(assets.EmbedMigrations, assets.PostgresMigrationDir+"/"+name)
	if err != nil {
		return fmt.Errorf("read openfga migration %s: %w", name, err)
	}
	up, err := gooseUpSection(string(raw))
	if err != nil {
		return fmt.Errorf("openfga migration %s: %w", name, err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin openfga migration %s: %w", name, err)
	}
	defer tx.Rollback(ctx)

	var applied bool
	if err := tx.QueryRow(ctx, fmt.Sprintf(
		`select exists (select 1 from %s where version = $1)`, openfgaMigrationsTable,
	), name).Scan(&applied); err != nil {
		return fmt.Errorf("check openfga migration %s: %w", name, err)
	}
	if applied {
		return nil
	}
	if _, err := tx.Exec(ctx, up); err != nil {
		return fmt.Errorf("apply openfga migration %s: %w", name, err)
	}
	if _, err := tx.Exec(ctx, fmt.Sprintf(
		`insert into %s (version) values ($1)`, openfgaMigrationsTable,
	), name); err != nil {
		return fmt.Errorf("record openfga migration %s: %w", name, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit openfga migration %s: %w", name, err)
	}
	return nil
}

// gooseUpSection returns the statements between the Up and Down markers.
//
// It refuses a file containing a StatementBegin block rather than mangling it:
// such a file needs a real goose parser, and failing loudly is how we find out
// an upgrade introduced one.
func gooseUpSection(content string) (string, error) {
	if strings.Contains(content, "+goose StatementBegin") {
		return "", fmt.Errorf("migration uses a goose StatementBegin block and cannot be applied by this runner")
	}
	_, after, found := strings.Cut(content, "-- +goose Up")
	if !found {
		return "", fmt.Errorf("migration has no +goose Up marker")
	}
	up, _, _ := strings.Cut(after, "-- +goose Down")
	if strings.TrimSpace(up) == "" {
		return "", fmt.Errorf("migration has an empty Up section")
	}
	return up, nil
}
```

- [ ] **Step 5: Write the tests**

Note `newTestStoreID` — a fixed store id would fail on the second run against
the same database with a duplicate-tuple error.

```go
package authorization_test

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/oklog/ulid/v2"
	"github.com/openfga/openfga/pkg/storage"
	"github.com/openfga/openfga/pkg/tuple"
	"github.com/stretchr/testify/require"

	"github.com/vishu42/tflive/internal/authorization"
)

// testPool gates every test in this file: without a database there is nothing
// worth asserting, because the deliverable is what Postgres does at commit.
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("tflive_POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set tflive_POSTGRES_TEST_DSN (or run `make differential-test`)")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return pool
}

// newTestStoreID keeps runs from colliding. These tests write real rows to a
// real database and never clean up, so a fixed id fails on the second run.
func newTestStoreID(t *testing.T) string {
	t.Helper()
	return ulid.Make().String()
}

func TestWriteRollsBackWithTheCallersTransaction(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	require.NoError(t, authorization.Migrate(ctx, pool))

	store, err := authorization.NewDatastoreForTest(pool)
	require.NoError(t, err)
	t.Cleanup(store.Close)

	storeID := newTestStoreID(t)
	key := tuple.NewTupleKey("stack:rollback", "owner", "user:alice")

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	require.NoError(t, store.Write(authorization.WithTx(ctx, tx), storeID, nil, storage.Writes{key}))
	require.NoError(t, tx.Rollback(ctx))

	// The rollback is the assertion: a write that opened its own transaction
	// would have committed and survived this.
	require.Empty(t, readTuples(t, ctx, store, storeID, "stack:rollback"),
		"a rolled-back tuple must not be visible")
}

func TestWriteCommitsWithTheCallersTransaction(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	require.NoError(t, authorization.Migrate(ctx, pool))

	store, err := authorization.NewDatastoreForTest(pool)
	require.NoError(t, err)
	t.Cleanup(store.Close)

	storeID := newTestStoreID(t)
	key := tuple.NewTupleKey("stack:commit", "owner", "user:alice")

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	require.NoError(t, store.Write(authorization.WithTx(ctx, tx), storeID, nil, storage.Writes{key}))
	require.NoError(t, tx.Commit(ctx))

	found := readTuples(t, ctx, store, storeID, "stack:commit")
	require.Len(t, found, 1)
	require.Equal(t, "user:alice", found[0].GetKey().GetUser())
}

func TestWriteWithoutATransactionStillWorks(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	require.NoError(t, authorization.Migrate(ctx, pool))

	store, err := authorization.NewDatastoreForTest(pool)
	require.NoError(t, err)
	t.Cleanup(store.Close)

	storeID := newTestStoreID(t)
	// No WithTx: this must fall through to upstream's own transaction, because
	// bootstrap.SeedRoot writes root's tuple with no domain transaction open.
	require.NoError(t, store.Write(ctx, storeID, nil,
		storage.Writes{tuple.NewTupleKey("stack:notx", "owner", "user:alice")}))

	require.Len(t, readTuples(t, ctx, store, storeID, "stack:notx"), 1)
}
```

Write `readTuples` as a small helper over `store.Read` plus `iter.ToArray`, and
add `NewDatastoreForTest` as an exported test-only wrapper around
`newDatastore` in `export_test.go`.

- [ ] **Step 6: Add the `make differential-test` target**

```makefile
# Points at the Compose Postgres from docker-compose.yaml. Override to run the
# integration tests somewhere else:
#
#   make differential-test TEST_DSN=postgres://…
TEST_DSN ?= postgres://tflive:tflive@localhost:55432/tflive_test?sslmode=disable

# internal/authorization/write.go is a transcription of OpenFGA's own write path
# with its BeginTx/Commit removed, so a tuple write can join our transaction.
# A transcription diverges silently: an upstream migration that adds a column
# keeps compiling here and starts writing rows that are subtly wrong.
#
# differential_test.go is the only thing that catches that, and it needs a real
# database -- without one it skips, and a skipped guard is no guard.
#
# Run it after every `go get github.com/openfga/openfga@...`.
differential-test: ## Run the authorization tests that need a real Postgres
	tflive_POSTGRES_TEST_DSN=$(TEST_DSN) go test ./internal/authorization/ -count=1 -v
```

Add `differential-test` to `.PHONY`, and widen the help column from `%-14s` to
`%-18s` so the new name is not truncated.

- [ ] **Step 7: Run the tests**

```bash
docker compose up -d postgres
make differential-test
```

Expected PASS. **Actually run it** — rollback behaviour is the entire deliverable of this chunk and a skipped test proves nothing.

- [ ] **Step 8: Commit**

```bash
git add internal/authorization/ Makefile
git commit -m "feat: write OpenFGA tuples on the caller's transaction"
```

---

### Task 3: Differential test against upstream's write path

**Files:** Create `internal/authorization/differential_test.go`

**Interfaces:** Consumes Task 2. Produces nothing — this task is purely a guard.

`write.go` is a transcription, so the failure mode is silent divergence after a
dependency bump, not a compile error. **Do not skip this task**; without it the
approach is unmaintainable, and it is the single cost of choosing atomicity.

- [ ] **Step 1: Write the test**

```go
package authorization_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/openfga/openfga/pkg/storage"
	"github.com/openfga/openfga/pkg/tuple"
	"github.com/stretchr/testify/require"

	"github.com/vishu42/tflive/internal/authorization"
)

// TestTransactionalWriteMatchesUpstream writes the same tuples two ways -- once
// through our transactional path and once through OpenFGA's own -- and asserts
// the resulting rows are indistinguishable apart from the identifiers expected
// to differ.
//
// This is the test that catches an upstream schema or write-path change after a
// dependency bump. If it fails, re-read postgres.Datastore.write and bring
// writeOnTx back in step; do not "fix" it by loosening the comparison.
func TestTransactionalWriteMatchesUpstream(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	require.NoError(t, authorization.Migrate(ctx, pool))

	store, err := authorization.NewDatastoreForTest(pool)
	require.NoError(t, err)
	t.Cleanup(store.Close)

	upstreamStore, ourStore := newTestStoreID(t), newTestStoreID(t)

	keys := storage.Writes{
		tuple.NewTupleKey("stack:diff", "owner", "user:alice"),
		tuple.NewTupleKey("stack:diff", "viewer", "user:bob"),
		tuple.NewTupleKey("stack:diff", "parent", "platform:tflive"),
	}

	// Upstream path: no transaction on the context, so Write falls through.
	require.NoError(t, store.Write(ctx, upstreamStore, nil, keys))

	// Our path: the same tuples, on a transaction we commit ourselves.
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	require.NoError(t, store.Write(authorization.WithTx(ctx, tx), ourStore, nil, keys))
	require.NoError(t, tx.Commit(ctx))

	require.Equal(t,
		readRows(t, ctx, pool, "tuple", upstreamStore),
		readRows(t, ctx, pool, "tuple", ourStore),
		"tuple rows must be identical apart from store and ulid")
	require.Equal(t,
		readRows(t, ctx, pool, "changelog", upstreamStore),
		readRows(t, ctx, pool, "changelog", ourStore),
		"changelog rows must be identical apart from store and ulid")
}

// readRows returns every column of a table except those expected to differ
// between two separate writes: the store id chosen by the test, the ULID minted
// per row, and the insertion timestamp.
//
// The column list comes from information_schema rather than a literal, so when
// an upstream migration adds a column this test starts comparing it
// automatically instead of silently ignoring exactly what it exists to catch.
func readRows(t *testing.T, ctx context.Context, pool *pgxpool.Pool, table, storeID string) []string {
	t.Helper()

	rows, err := pool.Query(ctx, `
		select column_name from information_schema.columns
		where table_name = $1 and column_name not in ('store', 'ulid', 'inserted_at')
		order by column_name`, table)
	require.NoError(t, err)
	var columns []string
	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))
		columns = append(columns, name)
	}
	rows.Close()
	require.NoError(t, rows.Err())
	require.NotEmpty(t, columns, "table %s has no comparable columns", table)

	query := fmt.Sprintf(
		`select %s from %s where store = $1 order by object_type, object_id, relation, _user`,
		strings.Join(columns, ", "), table,
	)
	dataRows, err := pool.Query(ctx, query, storeID)
	require.NoError(t, err)
	defer dataRows.Close()

	var rendered []string
	for dataRows.Next() {
		values, err := dataRows.Values()
		require.NoError(t, err)
		rendered = append(rendered, fmt.Sprint(values...))
	}
	require.NoError(t, dataRows.Err())
	return rendered
}
```

- [ ] **Step 2: Run it.** `make differential-test` → PASS.

- [ ] **Step 3: Prove the test actually bites**

Temporarily drop `"condition_name"` from `tupleColumns` in `write.go`, re-run,
expect FAIL. Restore, re-run, expect PASS.

A guard nobody has watched fail is not a guard.

- [ ] **Step 4: Document it in `AGENTS.md`.** Add a line under the testing section: `make differential-test` needs a running Postgres and must be run after any OpenFGA version bump.

- [ ] **Step 5: Commit**

```bash
git add internal/authorization/differential_test.go AGENTS.md
git commit -m "test: assert the transactional write matches upstream row for row"
```

---

# Chunk C

### Task 4: Move the vocabulary and the model

**Files:**
- Create: `internal/authorization/relations.go`, `model.go`, `authorization-model.fga`
- Test: `internal/authorization/relations_test.go`, `model_test.go`

**Interfaces:**
- Consumes: nothing
- Produces: `Relation`, `Object`, `Subject`, `Grant`, `ObjectFromID`, `SubjectFromOIDCSub`, `NewRelation`, `NewGrant`, `NewStructuralRelationship`, `PlatformSubject`, `PlatformObject`, `ErrInvalidInput`, and the relation constants

**Move, do not rewrite.** `internal/authz/relations.go` is good code whose only
sin was living in a package that also defined a port. The tuple-safety rules in
`newIdentifier` — rejecting `:`, `#`, `*` — are security-relevant and must
survive unchanged.

- [ ] **Step 1: Move the files**

```bash
git mv internal/authz/relations.go internal/authorization/relations.go
git mv internal/authz/relations_test.go internal/authorization/relations_test.go
git mv openfga/authorization-model.fga internal/authorization/authorization-model.fga
git mv openfga/model.go internal/authorization/model.go
git mv openfga/model_test.go internal/authorization/model_test.go
```

- [ ] **Step 2: Move the identifier types**

From `internal/authz/authorization.go`, move into `relations.go`: `identifier`,
`Object`, `Subject`, `ObjectType`, `Grant`, `ObjectFromID`,
`SubjectFromOIDCSub`, `NewGrant`, `NewStructuralRelationship`,
`PlatformSubject`, `PlatformID`, `safeTupleToken`, `ErrInvalidInput`, and their
tests.

**Leave behind** — these are the port and die in Task 7: `Authorizer`,
`SubjectGrantLister`, `CheckRequest`, `CheckResult`, `BatchCheckRequest`,
`BatchCheckResult`, `ListGrantsRequest`, `ListGrantsResult`,
`ListSubjectGrantsRequest`, `Mutation`, `NewMutation`, `HTTPStatus`,
`ErrTimeout`, `ErrUnavailable`, `ErrMalformedResponse`, `ErrWriteUnconfirmed`.

`ErrInvalidInput` **does** move: it has four real control-flow uses
(`service.go:590`, `authorization.go:108`, `:173`, `:221`) where a malformed
stack ID must read as invalid input rather than a server fault.

- [ ] **Step 3: Fix package clauses and the embed path.** `package authorization` throughout. `model.go`'s `//go:embed authorization-model.fga` now resolves inside this package.

- [ ] **Step 4: Run the tests.** `go test ./internal/authorization/ -v` → PASS. The moved tests should pass unmodified apart from their package clause. **If one needs a behaviour change to pass, stop** — something was rewritten that should have been moved.

- [ ] **Step 5: Commit**

```bash
git add -A internal/authorization openfga internal/authz
git commit -m "refactor: move the authorization vocabulary and model into one package"
```

---

### Task 5: Server lifecycle and bootstrap

**Files:**
- Create: `internal/authorization/server.go`, `bootstrap.go`, `export_test.go`
- Test: `internal/authorization/server_test.go`

**Interfaces:**
- Consumes: Task 2 (`newDatastore`, `Migrate`), Task 4 (model)
- Produces: `New(ctx, pool, storeName) (*Authorization, error)`, `Close()`, `StoreID()`, `ModelID()`

One constructor does everything — migrate, build the datastore, start the
server, resolve store and model — so there is no multi-step wiring for a caller
to get wrong.

- [ ] **Step 1: Write the failing test**

```go
func TestNewIsIdempotentAcrossRestarts(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)

	first, err := authorization.New(ctx, pool, "tflive-test")
	require.NoError(t, err)
	firstStore, firstModel := first.StoreID(), first.ModelID()
	first.Close()

	// A restart must adopt the same store and model. If it does not, every boot
	// mints a new model id and existing tuples are evaluated against a model
	// nothing was written for.
	second, err := authorization.New(ctx, pool, "tflive-test")
	require.NoError(t, err)
	t.Cleanup(second.Close)
	require.Equal(t, firstStore, second.StoreID())
	require.Equal(t, firstModel, second.ModelID())
}

func TestNewRequiresAPool(t *testing.T) {
	_, err := authorization.New(context.Background(), nil, "tflive")
	require.Error(t, err)
}

func TestCloseIsSafeTwice(t *testing.T) {
	// New closes on a failed bootstrap and callers also defer it.
	auth, err := authorization.New(context.Background(), testPool(t), "tflive-test")
	require.NoError(t, err)
	auth.Close()
	auth.Close()
}
```

- [ ] **Step 2: Run and watch it fail.** `undefined: authorization.New`.

- [ ] **Step 3: Write `server.go`**

```go
const defaultStoreName = "tflive"

// New starts an embedded OpenFGA over the application's pool and resolves the
// store and authorization model this process will use.
//
// The pool is borrowed, not owned: Close shuts down the server and datastore
// and leaves the pool to whoever created it, because the application is still
// using it for everything else.
func New(ctx context.Context, pool *pgxpool.Pool, storeName string) (*Authorization, error) {
	if pool == nil {
		return nil, fmt.Errorf("authorization: pool is required")
	}
	if storeName == "" {
		storeName = defaultStoreName
	}
	if err := Migrate(ctx, pool); err != nil {
		return nil, fmt.Errorf("authorization: migrate: %w", err)
	}
	datastore, err := newDatastore(pool)
	if err != nil {
		return nil, err
	}
	engine, err := server.NewServerWithOpts(server.WithDatastore(datastore))
	if err != nil {
		datastore.Close()
		return nil, fmt.Errorf("authorization: start server: %w", err)
	}
	auth := &Authorization{server: engine, store: datastore}
	if err := auth.bootstrap(ctx, storeName); err != nil {
		auth.Close()
		return nil, err
	}
	return auth, nil
}

func (a *Authorization) StoreID() string { return a.storeID }
func (a *Authorization) ModelID() string { return a.modelID }

// Close is safe to call more than once: New closes on a failed bootstrap and
// callers also defer it.
func (a *Authorization) Close() {
	a.closeOnce.Do(func() {
		a.server.Close()
		a.store.Close()
	})
}
```

- [ ] **Step 4: Write `bootstrap.go`**

Port `internal/openfga/provisioner.go`'s `Bootstrap` onto the server. It is 94
lines and **its refusals are the valuable part** — keep all four:

- more than one store named `storeName` → error, never pick one
- more than one model matching the repository's → error, selection is ambiguous
- a matching model found → adopt it, write nothing
- no match → write a new model version

Keep the pagination loops from the deleted `client.go` (`ListStores`,
`ListAuthorizationModels`), including the repeated-continuation-token guard that
prevents an infinite loop. Model comparison uses the moved `ModelsEqual`;
conversion between the repository's JSON model and
`openfgav1.AuthorizationModel` goes through `protojson`.

Also port `internal/openfga/provisioner_test.go` (347 lines) — it covers the
ambiguity refusals and is the evidence this was ported rather than rewritten.

- [ ] **Step 5: Write `export_test.go`** exposing `NewDatastoreForTest = newDatastore` and a `NewWithDatastore` for tests that want `memory.New()`.

- [ ] **Step 6: Run the tests.** `make differential-test` → PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/authorization/ && git commit -m "feat: start and bootstrap OpenFGA in-process"
```

---

### Task 6: The callable API

**Files:**
- Create: `internal/authorization/authorization.go`
- Test: `internal/authorization/authorization_test.go`

**Interfaces:**
- Consumes: Tasks 2, 4, 5
- Produces: `Can`, `CanAll`, `ListGrants`, `Grant`, `Revoke`, `type Check struct`

This replaces `internal/authorizer/adapter.go` (585 lines) with roughly 250.
Gone: `classify()` (every failure is just a wrapped error), `confirm()` (nothing
to confirm inside a transaction), `validMutation` (variadic args are already
typed), `ListSubjectGrants` (its only caller was the deleted queue handler).

**Four things must survive verbatim**, because they are what makes an answer
trustworthy:

1. **Batch answers matched by correlation ID, never by position.** Ranging the response map attributes answers to the wrong subjects.
2. **Chunking at `MaxChecksPerBatchCheck` (50).** The 13-stack case, issue #220.
3. **`grantFromReadTuple`'s three-way classification.** A structural `parent` edge is skipped; a derived or unknown relation is corruption and fails the call; a tuple for another object fails the call.
4. **Denial is `false, nil`; everything else is an error.**

- [ ] **Step 1: Write the failing tests**

Tests run against a real engine over `memory.New()` — no Postgres, and far
higher fidelity than the hand-written fakes they replace.

```go
func newTestAuthorization(t *testing.T) *authorization.Authorization {
	t.Helper()
	auth, err := authorization.NewWithDatastore(context.Background(), memory.New(), "tflive-test")
	require.NoError(t, err)
	t.Cleanup(auth.Close)
	return auth
}

func TestCanReflectsTheModelsTierOrdering(t *testing.T) {
	auth := newTestAuthorization(t)
	ctx := context.Background()
	require.NoError(t, auth.Grant(ctx, platformGrant(t, "alice", authorization.RelationAdmin)))

	// admin satisfies can_edit through the tier chain, which is the model's
	// business -- re-tiering must never touch Go code.
	allowed, err := auth.Can(ctx, "alice", authorization.RelationCanCreateStack, authorization.PlatformObject)
	require.NoError(t, err)
	require.True(t, allowed)
}

func TestCanReturnsFalseNilForADenial(t *testing.T) {
	auth := newTestAuthorization(t)
	allowed, err := auth.Can(context.Background(), "nobody",
		authorization.RelationCanCreateStack, authorization.PlatformObject)
	require.NoError(t, err, "a denial is an answer, not a failure")
	require.False(t, allowed)
}

func TestCanAllPreservesInputOrderAcrossChunks(t *testing.T) {
	// 52 checks exercises the two-chunk path (50 + 2) that issue #220 found.
	auth := newTestAuthorization(t)
	ctx := context.Background()
	checks := make([]authorization.Check, 52)
	for i := range checks {
		checks[i] = authorization.Check{
			Relation: authorization.RelationCanView,
			Object:   stackObject(t, fmt.Sprintf("s%02d", i)),
		}
	}
	require.NoError(t, auth.Grant(ctx, stackGrant(t, "alice", "s07", authorization.RelationOwner)))

	results, err := auth.CanAll(ctx, "alice", checks)
	require.NoError(t, err)
	require.Len(t, results, 52)
	for i, allowed := range results {
		require.Equal(t, i == 7, allowed, "answer %d landed against the wrong question", i)
	}
}

func TestListGrantsSkipsStructuralEdgesButFailsOnCorruption(t *testing.T) {
	auth := newTestAuthorization(t)
	ctx := context.Background()
	require.NoError(t, auth.Grant(ctx,
		stackGrant(t, "alice", "one", authorization.RelationOwner),
		structuralParent(t, "one"),
	))

	grants, err := auth.ListGrants(ctx, stackObject(t, "one"))
	require.NoError(t, err)
	require.Len(t, grants, 1, "the parent edge is structure, not access")
	require.Equal(t, "user:alice", grants[0].Subject().String())
}
```

- [ ] **Step 2: Run and watch them fail.** `undefined: authorization.Can`.

- [ ] **Step 3: Write `Can` and `CanAll`**

```go
// Can answers one permission question.
//
// It answers only allowed or denied; every other outcome is an error, never a
// decision, so a dependency failure can never read as a grant.
//
//	alice may view stack one → true, nil
//	alice may not view it    → false, nil
//	OpenFGA cannot answer    → false, error (never a decision)
func (a *Authorization) Can(ctx context.Context, subject string, relation Relation, object Object) (bool, error) {
	sub, err := SubjectFromOIDCSub(subject)
	if err != nil {
		return false, fmt.Errorf("invalid subject: %w", err)
	}
	response, err := a.server.Check(ctx, &openfgav1.CheckRequest{
		StoreId:              a.storeID,
		AuthorizationModelId: a.modelID,
		TupleKey: &openfgav1.CheckRequestTupleKey{
			User: sub.String(), Relation: relation.String(), Object: object.String(),
		},
	})
	if err != nil {
		return false, fmt.Errorf("check %s on %s: %w", relation, object, err)
	}
	if response == nil {
		return false, fmt.Errorf("check %s on %s returned no response", relation, object)
	}
	return response.GetAllowed(), nil
}
```

`CanAll` chunks at 50 and drives its result loop from the **input** slice,
indexing the response map by correlation ID. Port that loop verbatim from
`adapter.go:96-128`.

- [ ] **Step 4: Write `ListGrants`, `Grant` and `Revoke`**

`ListGrants` keeps the pagination loop and `grantFromReadTuple` from
`adapter.go:151-205` and `:496-556`.

```go
// Grant adds direct role assignments.
//
// Under a context carrying a transaction the write lands in it, so the grant
// commits with whatever domain change caused it. There is nothing to confirm
// afterwards: it lands, or the caller's transaction is rolled back.
func (a *Authorization) Grant(ctx context.Context, grants ...Grant) error {
	keys, err := writeTupleKeys(grants)
	if err != nil {
		return err
	}
	if _, err := a.server.Write(ctx, &openfgav1.WriteRequest{
		StoreId:              a.storeID,
		AuthorizationModelId: a.modelID,
		Writes:               &openfgav1.WriteRequestWrites{TupleKeys: keys},
	}); err != nil {
		return fmt.Errorf("grant: %w", err)
	}
	return nil
}
```

`Revoke` mirrors it with `WriteRequestDeletes`, which takes
`[]*TupleKeyWithoutCondition` — hence two key-rendering helpers.

- [ ] **Step 5: Run the tests.** `go test ./internal/authorization/ -v` → PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/authorization/ && git commit -m "feat: answer authorization questions without a provider port"
```

---

# Chunk D

### Task 7: Point the application at the new package and delete the old three

**Files:**
- Modify: `internal/app/authorization.go` (437), `internal/app/service.go`, `internal/api/server.go:1111`, `internal/bootstrap/root.go`
- Delete: `internal/authz/`, `internal/openfga/`, `internal/authorizer/`, `openfga/`

**Interfaces:** Consumes Task 6. Produces `app.Service.Authorization *authorization.Authorization` replacing `Authorizer authz.Authorizer`.

- [ ] **Step 1: Replace the field and the helpers**

```go
// Before: build CheckRequest, call Check, unwrap CheckResult
// After:
func authorizePlatform(ctx context.Context, auth *authorization.Authorization, relation authorization.Relation) error {
	principal, err := requirePrincipal(ctx)
	if err != nil {
		return err
	}
	if auth == nil {
		return fmt.Errorf("authorization not configured")
	}
	allowed, err := auth.Can(ctx, principal.Subject, relation, authorization.PlatformObject)
	if err != nil {
		return err
	}
	if !allowed {
		return ErrForbidden
	}
	return nil
}
```

The rule the old `requirePrincipalAndAuthorizer` comment states must survive: a
nil authorization is an error, not `ErrForbidden`, because "I cannot tell" and
"you may not" are different answers and only one of them is true.

- [ ] **Step 2: Delete the 503 mapping**

Remove the `authz.HTTPStatus` branch at `internal/api/server.go:1111` entirely.
Authorization failures fall through to the existing default case and render 500.

Nothing is lost: the 403 risk is handled by the `(bool, error)` signature, not
by this branch; nothing branched on the sentinels; and the web client renders
the same retry state for any non-401, so 503-vs-500 was never observable. With
OpenFGA embedded there is no separate service to report as unavailable.

- [ ] **Step 3: Delete the old packages**

```bash
git rm -r internal/authz internal/openfga internal/authorizer openfga
go build ./... 2>&1 | head -40
```

Fix every reported reference. The compiler is the checklist.

- [ ] **Step 4: Rewrite the app-level test fakes**

`internal/app` tests currently fake `authz.Authorizer`. There is no interface
now, so they construct a real `*authorization.Authorization` over
`memory.New()`. This deletes fake code and raises fidelity — the tests exercise
the real model, including the tier ordering they previously stubbed.

- [ ] **Step 5: Run the full suite.** `go test ./...` → PASS except `cmd/`, fixed in Task 8.

- [ ] **Step 6: Commit**

```bash
git commit -am "refactor: replace the authorization port with one concrete package"
```

---

### Task 8: Wiring

**Files:** Modify `cmd/api/main.go`, `cmd/worker/main.go`, `internal/config/auth.go`

**Interfaces:** Consumes Task 7. Produces both binaries booting with no `OPENFGA_*` variables but `OPENFGA_STORE_NAME`.

Ordering in `cmd/api`: pool → app migrations → `authorization.New` (runs its own
migrations and bootstraps) → service → `SeedRoot`, which writes root's tuple and
therefore cannot run before authorization exists.

- [ ] **Step 1: Write the failing test**

```go
func TestAPIStartsWithNoOpenFGAEnvironment(t *testing.T) {
	// The two-phase startup is gone: there are no identifiers to pin.
	env := baseTestEnv(t)
	delete(env, "OPENFGA_API_URL")
	delete(env, "OPENFGA_STORE_ID")
	delete(env, "OPENFGA_MODEL_ID")
	require.NoError(t, runWithDependencies(testContext(t), envGetter(env), testDependencies(t)))
}
```

- [ ] **Step 2: Rewire `cmd/api/main.go`**

```go
	auth, err := deps.newAuthorization(ctx, pool, cfg.Security.OpenFGA.StoreName)
	if err != nil {
		return fmt.Errorf("start authorization: %w", err)
	}
	defer auth.Close()
```

Dependency field becomes
`newAuthorization func(context.Context, *pgxpool.Pool, string) (*authorization.Authorization, error)`.

- [ ] **Step 3: Strip authorization from `cmd/worker/main.go`**

Remove `workerAuthorizer` (`main.go:55`), `newAuthorizationAdapter`, and the
`app.NewService(...)` call inside `newQueueRegistry`. Verified:
`internal/activities`, `internal/workflows` and `internal/runner` reference no
authorization at all, so **the worker never imports the new package.**

If the compiler objects because the handlers still exist, do Task 9 first or
stage both in one commit.

- [ ] **Step 4: Trim the config.** `OpenFGAConfig` keeps only `StoreName`. Delete `OPENFGA_API_URL`, `OPENFGA_STORE_ID`, `OPENFGA_MODEL_ID`, `OPENFGA_API_TOKEN` loading and their tests.

- [ ] **Step 5: Run the full suite.** `go test ./...` → PASS, all packages.

- [ ] **Step 6: Commit**

```bash
git add cmd/ internal/config/ && git commit -m "feat: boot the API with embedded authorization"
```

---

# Chunk E

### Task 9: Delete the authorization queue path

**Files:**
- Delete: `internal/app/grant_stack_owner_handler.go` (+test), `mark_stack_ready_handler.go` (+test), `stack_provisioning.go` (+test)
- Modify: `internal/app/queue_specs.go`, `internal/domain/stack.go`, `internal/postgres/repositories.go:502`

**Interfaces:** Consumes Task 8. Produces a queue registry with 5 kinds instead of 8.

~930 lines deleted. All three kinds go — role changes have the same shape as
creation.

**Do not over-delete.** The queue itself stays, with five unrelated kinds:
`start_template_run`, `start_template_sync`, `signal_run_approval`,
`signal_run_cancellation`, `notify_user`.

- [ ] **Step 1: Delete the files and specs**

```bash
git rm internal/app/grant_stack_owner_handler.go internal/app/grant_stack_owner_handler_test.go \
       internal/app/mark_stack_ready_handler.go internal/app/mark_stack_ready_handler_test.go \
       internal/app/stack_provisioning.go internal/app/stack_provisioning_test.go
```

Drop `GrantStackOwnerSpec` and `MarkStackReadySpec` from `queue_specs.go`;
`StackGrantSpec` went with `internal/authz` in Task 7.

- [ ] **Step 2: Retire the provisioning status, keep the column**

Delete `domain.StackStatusProvisioning`, `Service.StackStatuses`,
`app.StackStatusRepository` and `Store.MarkStackReady`.

**Keep `domain.StackStatusReady`, the `stacks.status` column, and the `status`
field on the API response** (decided 2026-09-12). Update the `StackStatus` doc
comment at `internal/domain/stack.go:9`, which says "reports whether a stack has
finished provisioning" — nothing provisions any more.

- [ ] **Step 3: Add the migration**

```sql
-- internal/postgres/migrations/0022_retire_stack_provisioning_status.sql
update stacks set status = 'ready' where status = 'provisioning';
```

The column stays; only the value retires. Pre-production with disposable state,
so nothing more is owed — no constraint change, no enum type to alter.

- [ ] **Step 4: Run the suite.** Failures should be confined to `CreateStack` and the role-change methods, fixed in Task 10. Anything else is a regression from this task.

- [ ] **Step 5: Commit**

```bash
git commit -am "refactor: delete the authorization queue kinds and their handlers"
```

---

### Task 10: One commit for creation and role changes

**Files:** Modify `internal/postgres/unitofwork.go:58`, `internal/app/service.go` (7 `InTx` call sites; the authorization ones are 622, 1395, 1480)

**Interfaces:** Consumes Tasks 2, 6, 9. Produces `InTx(ctx, func(ctx, TxRepo, Enqueuer) error) error`.

- [ ] **Step 1: Write the failing tests**

```go
func TestCreateStackReturnsAReadyStack(t *testing.T) {
	stack, err := newTestService(t).CreateStack(authenticatedContext(t, "user-1"),
		CreateStackCommand{TenantID: "tenant-1", Name: "one"})
	require.NoError(t, err)
	require.Equal(t, domain.StackStatusReady, stack.Status)
}

func TestCreateStackWritesNoQueueWork(t *testing.T) {
	service, queue := newTestServiceWithQueue(t)
	_, err := service.CreateStack(authenticatedContext(t, "user-1"),
		CreateStackCommand{TenantID: "tenant-1", Name: "one"})
	require.NoError(t, err)
	require.Empty(t, queue.Enqueued(), "the owner grant is part of the commit, not a follow-up")
}

func TestCreateStackRollsBackTheStackWhenTheGrantFails(t *testing.T) {
	// The point of the whole change: a stack whose owner grant could not be
	// written must not exist, rather than existing unreachable.
	service, store := newTestServiceWithFailingAuthorization(t)
	_, err := service.CreateStack(authenticatedContext(t, "user-1"),
		CreateStackCommand{TenantID: "tenant-1", Name: "one"})
	require.Error(t, err)
	require.Empty(t, store.Stacks(), "no stack row may survive a failed grant")
}

func TestAssignStackRoleReadsCurrentGrantsInsideTheTransaction(t *testing.T) {
	// The last-owner guard reads grants and then acts on the count. With the
	// read outside the transaction, two concurrent demotions can both see two
	// owners, both pass the guard, and leave the stack with none.
	// ... seed two owners, run two concurrent demotions ...
	require.Error(t, secondErr, "the second demotion must be refused or serialized")
	require.NotEmpty(t, ownersAfter, "a stack must never end with zero owners")
}
```

- [ ] **Step 2: Change `InTx` to pass its context**

```go
// InTx runs fn inside one transaction, giving it a transaction-scoped
// repository, a transaction-bound enqueuer, and a context carrying the
// transaction itself. Returning an error rolls back everything written under
// any of the three.
//
// The context is what extends the unit of work beyond this package: an
// authorization write made under it lands in this same transaction, so a stack
// row and the grant that makes it reachable commit together or not at all.
func (store *Store) InTx(ctx context.Context, fn func(context.Context, app.TxRepo, queue.Enqueuer) error) error {
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin unit of work: %w", err)
	}
	defer tx.Rollback(ctx)
	if err := fn(authorization.WithTx(ctx, tx), &txRepo{tx: tx}, &txEnqueuer{tx: tx, specs: store.queueSpecs}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit unit of work: %w", err)
	}
	return nil
}
```

**The `context.Context` parameter is mandatory, not stylistic.** Keeping the old
signature compiles, passes, and silently preserves the bug: the closure would
capture the outer context, the transaction would never reach the authorization
write, and the tuple would commit separately.

Update all 7 call sites and the 4 test callbacks (`unitofwork_test.go` ×3,
`store_test.go:1707`). **Shadowing the outer `ctx` is intended** — everything
inside the closure should use the transactional one.

- [ ] **Step 3: Collapse `CreateStack`**

```go
	stack.Status = domain.StackStatusReady

	// The parent edge rides with the owner grant: a stack with one and not the
	// other is broken either way -- without owner its creator cannot reach it,
	// without parent no platform administrator can.
	owner, err := authorization.NewGrant(subject, object, authorization.RelationOwner)
	if err != nil {
		return domain.Stack{}, fmt.Errorf("build owner grant: %w", err)
	}
	parent, err := authorization.NewStructuralRelationship(
		authorization.PlatformSubject, object, authorization.RelationParent)
	if err != nil {
		return domain.Stack{}, fmt.Errorf("build stack parent edge: %w", err)
	}

	// One transaction: the stack row, the audit event, and the grants that make
	// the stack reachable. A failure in any of them leaves no trace of the
	// others, which is why the stack can be returned as ready.
	if err := service.Work.InTx(ctx, func(ctx context.Context, repository TxRepo, _ queue.Enqueuer) error {
		if err := repository.CreateStack(ctx, stack); err != nil {
			return err
		}
		if err := repository.AppendAuditEvent(ctx, auditEvent); err != nil {
			return err
		}
		return service.Authorization.Grant(ctx, owner, parent)
	}); err != nil {
		return domain.Stack{}, fmt.Errorf("create stack: %w", err)
	}
	return stack, nil
```

- [ ] **Step 4: Move the role-change delta reads inside their transactions**

`service.go:1358` and `:1449` call `listGrantsForStack` **before** `InTx` opens.
Move both inside, and write the grant/revoke in the same closure.

This is the TOCTOU fix. The last-owner guard at `:1371` reads a count and then
acts on it; outside the transaction two concurrent demotions can both observe
two owners, both pass, and leave the stack with none. Inside, the
`SELECT … FOR UPDATE` that `write.go` takes on the tuple rows holds until
commit, so the second demotion serializes behind the first, re-reads, and is
correctly refused.

- [ ] **Step 5: Run the full suite.** `go test ./...` → PASS.

- [ ] **Step 6: Commit**

```bash
git commit -am "feat: create stacks and change roles in one commit"
```

---

# Chunk F

### Task 11: One `docker compose up`

**Files:** `docker-compose.yaml`, delete `docker-compose.app.yaml`, `deploy/postgres/init.sh`, `.env.example`, `README.md`, `docs/openapi.yaml`, `docs/architecture.md`, `docs/development.md`, `docs/authentication.md`, delete `cmd/openfga-provisioner/` and `Dockerfile.openfga-provisioner`

- [ ] **Step 1: Delete the OpenFGA services and the provisioner**

```bash
git rm -r cmd/openfga-provisioner Dockerfile.openfga-provisioner docker-compose.app.yaml
```

Remove `openfga`, `openfga-migrate` and `openfga-provision` from
`docker-compose.yaml`.

- [ ] **Step 2: Merge the overlay**

Move `api`, `worker` and `web` into `docker-compose.yaml`, dropping every
`OPENFGA_*` variable and the `:?` guards that forced the two-phase startup.
`api` and `worker` now depend only on `postgres` (healthy), `temporal` (healthy)
and `keycloak-provision` (completed).

- [ ] **Step 3: Trim `deploy/postgres/init.sh`.** Remove the `openfga` role and database; the tables live in the app database now. Drop `OPENFGA_DB_*` from `.env.example` along with `OPENFGA_API_URL`, `OPENFGA_STORE_ID`, `OPENFGA_MODEL_ID`, `OPENFGA_API_TOKEN`.

- [ ] **Step 4: Verify end to end from clean**

```bash
docker compose down -v
docker compose up -d --build
docker compose ps
```

Expected: every service healthy, no `openfga*` service, no `.env` edit between
commands. **Run it** — this is the outcome the whole plan exists for.

- [ ] **Step 5: Verify a stack is born ready.** Create a stack through the UI at `localhost:5173` and confirm its status is `ready`, never `provisioning`.

- [ ] **Step 6: Update `docs/openapi.yaml`**

Three places reference the retired state:

- ~line 1641: `StackStatus` enum `[provisioning, ready]` becomes `[ready]`, and its description loses the "`provisioning` means the queue is still writing the stack's authorization edges" sentence
- ~line 523: the `POST /v1/stacks` description says "Creation is queue-driven: the response may carry `status: "provisioning"` while the authorization edges are written. Watch…" — creation is synchronous now, so this becomes a plain statement that the stack is usable when the call returns
- ~line 1271: the queued-work description names "stack provisioning" as an example of asynchronous work; pick a surviving kind instead

```bash
make docs-lint
```

Expected: clean.

- [ ] **Step 7: Update the prose docs.** Delete "Pinning the OpenFGA identifiers" from `README.md`. In `docs/architecture.md`, record that there is one authorization package with no port, that tuple writes are transactional with domain writes, and that `write.go` mirrors upstream deliberately and is guarded by `differential_test.go` via `make differential-test`.

- [ ] **Step 8: Commit**

```bash
git add -A && git commit -m "feat: bring the whole stack up with one docker compose command"
```

---

## Self-Review

**Scope coverage.** In-scope item 1 (one package) → Tasks 4–7. Item 2 (single
commit) → Tasks 2, 10. Item 3 (sync creation) → Task 10. Item 4 (queue removal)
→ Task 9. Item 5 (container retired) → Task 11. Item 6 (worker) → Task 8 Step 3.
Item 7 (port deleted) → Tasks 6, 7. Item 8 (openapi) → Task 11 Step 6. Item 9
(make target) → Task 2 Step 6.

**Deliberate gaps.** Task 5 Step 4 describes `bootstrap.go` by its four refusals
rather than reproducing 94 lines that already exist in
`internal/openfga/provisioner.go` — the instruction is to port, and the existing
code plus its 347-line suite is the specification. Task 6 Step 4 does the same
for `ListGrants`'s pagination and `grantFromReadTuple`, naming exact source
lines. An executor who cannot see those files must read them before starting.

**Type consistency.** `WithTx`/`TxFrom` (Task 2) used in Task 10.
`newDatastore`/`Migrate` (Task 2) used in Task 5. `Relation`/`Object`/`Subject`/
`Grant`/`ErrInvalidInput` (Task 4) used in Tasks 6, 7, 10.
`New`/`Close`/`StoreID`/`ModelID` (Task 5) used in Tasks 6, 8.
`Can`/`CanAll`/`ListGrants`/`Grant`/`Revoke` (Task 6) used in Tasks 7, 10.
`InTx(ctx, func(ctx, …))` (Task 10) replaces the signature at 7 call sites.

**Risk concentrated in three places.** Task 3 is the only guard against silent
divergence in `write.go`. Task 6 must keep denial as `false, nil` and every
failure as a returned error, or a dependency failure reads as a decision. Task
10 Step 4 is the TOCTOU fix, and getting it wrong leaves the bug while looking
fixed.
