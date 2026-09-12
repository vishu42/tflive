# Embedding OpenFGA: what it actually costs, and what it actually fixes

Status: analysis, no code written. Supersedes the 2026-08-04 scoping that was
deferred. Every constraint below was verified against
`github.com/openfga/openfga@v1.19.0` in the module cache, not recalled.

## The headline: one of the two stated outcomes is not reachable by embedding

The goal was stated as two outcomes:

1. one `docker compose up` to spin up the app
2. no dual write for transactions spanning OpenFGA and the Postgres commit

**Outcome 1 is straightforwardly reachable.** Outcome 2 is not reachable by
embedding alone, and the premise behind it is inaccurate in a way that changes
what the work should be.

### There is no dual write today

`CreateStack` (`internal/app/service.go:621`) commits the stack row, the audit
event, and the `grant_stack_owner` queue intent in **one** transaction, through
`UnitOfWork.InTx`. The interface says so in as many words
(`internal/app/service.go:73`):

> UnitOfWork commits a domain write and a queued intent atomically. This is
> what makes the queue an outbox rather than a second system to dual-write to.

That is the textbook fix for dual writes, and it is already in place. What the
codebase has is a **transactional outbox**, not a dual write.

### Embedding does not let a tuple write join our transaction

The datastore interface OpenFGA's server is built on takes no transaction and
offers no context escape hatch (`pkg/storage/storage.go:280`):

```go
Write(ctx context.Context, store string, d Deletes, w Writes, opts ...TupleWriteOption) error
```

And the Postgres implementation opens and commits its own transaction
(`pkg/storage/postgres/postgres.go`, `func (s *Datastore) write`):

```go
txn, err := s.primaryDB.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
...
defer func() { _ = txn.Rollback(ctx) }()
```

So even with the tuples in our database, on our `*pgxpool.Pool`, in our process,
a tuple write is a **second transaction**. Embedding removes the network hop and
the separate container. It does not make `stacks` and `tuple` commit together.

### What is actually being felt

The real complaint is the *consequence* of the outbox, and it is legitimate:
`CreateStack` returns a stack in `provisioning`, the owner tuple lands later from
the worker, and `mark_stack_ready` flips the status afterwards. The API is
eventually consistent for its most basic operation. That is the "async api"
pain, and it is worth fixing — but it is an outbox-latency problem, not a
dual-write problem, and naming it correctly is what makes the options below
distinguishable.

## Two separable goals

**Goal A — one command.** Retire the `openfga`, `openfga-migrate`, and
`openfga-provision` services; fold `docker-compose.app.yaml` back into
`docker-compose.yaml`. The overlay exists *only* to let you record
`OPENFGA_STORE_ID`/`OPENFGA_MODEL_ID` between two phases — that ID-pinning dance
is the second half of "starting up the app is a two step process". Embedding
plus in-process auto-bootstrap kills both. Low risk, entirely mechanical.

**Goal B — synchronous stack creation.** Needs a decision, and the three options
differ by roughly a factor of three in cost.

### B1 — Synchronous in-process write, outbox retained as repair  (rejected)

Call the embedded server inside the request, after the app transaction commits.
Keep `grant_stack_owner` enqueued as today, so a crash between the two still
heals. `GrantStackOwner` already reads before writing and returns nil when the
grant is present, so the queue replay is a no-op in the happy path.

Not atomic. The window between the two commits is microseconds against the same
database rather than an HTTP round trip to another container, and the existing
repair path covers it. Stack creation returns `ready`.

Rejected on 2026-09-12: the user's goal is a single commit, and B2 turned out to
be cheap enough to deliver it. Kept here because it is the fallback if the
differential test in Task 4 proves unmaintainable.

### B2 — True single commit via a custom datastore  ← CHOSEN

Wrap `*postgres.Datastore`, pull a `pgx.Tx` out of the context in `Write`, and
execute the tuple and changelog statements on our transaction.

**Verified by a compiling spike on 2026-09-12**, not estimated. An earlier draft
of this document called it "~150 lines of hand-written SQL pinned to their
internal schema" and did not recommend it. That was wrong, and the correction is
the reason this is now the chosen option: every primitive the copy needs is
exported.

- Planning: `sqlcommon.MakeTupleLockKeys`, `sqlcommon.GetDeleteWriteChangelogItems`,
  `sqlcommon.WriteData`, `sqlcommon.TupleLockKey`
- Query building: `sqlcommon.BuildRowConstructorIN`, `sqlcommon.SQLIteratorColumns`,
  `sqlcommon.NewRowGetter`, `sqlcommon.NewSQLTupleIterator`
- Errors and options: `postgres.HandleSQLError`, `storage.NewTupleWriteOptions`,
  `storage.ErrWriteConflictOnDelete`, `storage.ErrWriteConflictOnInsert`,
  `storage.DefaultMaxTuplesPerWrite`
- Transaction plumbing: `sqlcommon.Connector`, `sqlcommon.Connection`,
  `sqlcommon.Rows` are exported interfaces, so a `pgx.Tx`-backed connector is
  ours to write (~20 lines)

So the work is OpenFGA's own builders over OpenFGA's own exported helpers, with
the BeginTx/Commit removed and the caller's transaction used instead. Only the
`tuple` and `changelog` column lists are duplicated. The spike compiles and vets
clean, and satisfies `storage.OpenFGADatastore`, so it drops straight into
`server.WithDatastore`.

The one friction point: `pgx.Rows.Close()` returns nothing while
`sqlcommon.Rows.Close()` must return an error. A 10-line shim fixes it — the
same shim upstream writes for its own pool path (`pgxRowsWrapper`).

Residual risk is divergence, not correctness: if upstream changes the write path
or the schema, our copy goes stale silently. Task 4's differential test is the
mitigation and is not optional.

### B3 — Embed only, leave the outbox alone  (subsumed)

Goal A without Goal B. Zero correctness risk, does not address the async
complaint at all. Worth noting only because it is the natural first PR: Goal A
and Goal B are independently shippable, and A is where all the compose-level
simplification lives.

## The worker question, which is the one real architectural decision

The worker uses the authorizer for both `StackGrantHandler` and
`GrantStackOwnerHandler` (`cmd/worker/main.go:271,293`). Embedded in the API
only, the worker cannot reach it in-process. Three ways out:

- **(a) Move the authz queue handlers into the API.** The API already carries the
  queue store. This was counter-proposed during the 2026-08-04 scoping and left
  undecided. It shrinks the worker to Temporal-only work, which is arguably
  where it belongs, but it is a topology change on top of everything else.
- **(b) Embed a second server in the worker over the same database.** OpenFGA
  holds no authoritative state outside its datastore — running several servers
  against one Postgres is its normal horizontal-scaling deployment. This
  preserves the current topology exactly and collapses the decision to nothing.
  Costs the worker binary the same +20-30 MB and a second set of in-process
  caches, which are per-process and DB-backed, so divergence is not a
  correctness concern.
- **(c) Internal HTTP endpoint on the API.** Reintroduces the network hop this
  change exists to remove. Rejected.

**(b) is the cheapest and was not in the option set the last time this was
scoped.** It is what I would do unless there is an independent reason to want
the worker slimmed.

## Verified constraints

| Constraint | Status |
|---|---|
| `go` directive floor | **v1.19.0 requires go 1.25.7**; our go.mod says `go 1.25.0`. One-line bump. `toolchain go1.25.14` and all four Dockerfiles (`golang:1.25.14-alpine3.23`) already satisfy it. |
| `postgres.NewWithDB(primary, secondary *pgxpool.Pool, cfg)` | Exists; accepts our existing pool. Pass `nil` secondary. |
| Server construction | `server.NewServerWithOpts(server.WithDatastore(ds), server.WithLogger(...))`. Datastore is the only required option (`server.go:939`). |
| Migrations | OpenFGA exports no `RunMigration`, only a cobra command — we run them. **But**: `assets/migrations/postgres/` is 6 files, plain SQL, **no `+goose StatementBegin` blocks**. Our own `internal/postgres/migrate.go` runner can apply them directly. The earlier spike's goose dependency is avoidable. |
| Table collisions | None. OpenFGA creates `tuple`, `authorization_model`, `store`, `assertion`, `changelog`; we own `stacks`, `work_queue`, `users`, `sessions`, and 13 others. Version trackers differ too (`schema_migrations` vs goose's). Safe to share one database. |
| Dependency weight | Module count 98 → ~280. MVS forces `pgx/v5` 5.7.6 → 5.10.x. API binary 36.5 MB → expect +20-30 MB. |
| Test datastore | `pkg/storage/memory` exists — `memory.New()`. The adapter suite gains fidelity by running a real engine instead of `httptest` fixtures. |
| CI safety net | Still none — `.github/` is absent. Every claim below rests on the local suite. |

## Work breakdown

Sizes are current line counts of the files involved, not estimates of lines
written.

**PR 1 — dependency and embedded lifecycle.** Bump `go` directive to 1.25.7.
`go get github.com/openfga/openfga@v1.19.0`. New package (`internal/fgaserver`)
owning: OpenFGA migrations over the app pool, `postgres.NewWithDB`,
`NewServerWithOpts`, `Close`. Nothing wired yet.

**PR 2 — the adapter rewrite.** The bulk of the work.
`internal/authorizer/adapter.go` (585) and `adapter_test.go` (945). Swap
`*openfga.Client` for an interface satisfied by `*server.Server`, over
`openfgav1` protos instead of HTTP JSON. The hard part is `classify()`
(`adapter.go:557`): its fail-closed guarantees are currently expressed in HTTP
status codes (429/5xx → `ErrUnavailable`, 4xx → `ErrMalformedResponse`) and must
be re-derived against gRPC status codes and OpenFGA's typed errors. Every
fail-closed branch has to be re-established deliberately — this is the one place
where a sloppy translation turns a dependency failure into a grant.

**PR 3 — in-process bootstrap.** `internal/openfga/provisioner.go` (94) keeps its
ambiguity checks (>1 store named `tflive` → fail) but gets a server-backed
`Backend` instead of `*Client`. Store and model resolve at API startup;
`OPENFGA_STORE_ID`/`OPENFGA_MODEL_ID` leave the environment. Retire
`cmd/openfga-provisioner` (86 + 145 test) and `Dockerfile.openfga-provisioner`.

**PR 4 — delete the HTTP client.** `internal/openfga/client.go` (430),
`client_test.go` (380), `relationships.go` (190), `live_test.go` (155) become
dead. `config.go` (106) loses `OPENFGA_API_URL` and friends;
`internal/config/auth.go` (408) loses most of `OpenFGAConfig`. `model.go` (173)
and the `.fga` DSL are unaffected — the model stays the source of truth.

**PR 5 — wiring.** `cmd/api/main.go` (363) + `main_test.go` (837),
`cmd/worker/main.go` (313) + `main_test.go` (648), per the worker decision above.
`internal/bootstrap/root.go` (200) seeds root's tuples at boot and now depends on
the embedded server being up before `SeedRoot` runs — a startup ordering
constraint that replaces a container ordering constraint.

**PR 6 — compose and env.** Delete three services, fold the overlay in, drop the
`:?` guards, drop `OPENFGA_DB_*` and the openfga role/db from
`deploy/postgres/init.sh`, update `.env.example`.

**PR 7 — Goal B**, if taken: synchronous grant in `CreateStack`, stack returns
`ready`, outbox retained as repair.

**PR 8 — docs.** `README.md` (the "Pinning the OpenFGA identifiers" section
disappears), `docs/architecture.md`, `docs/development.md`,
`docs/authentication.md`.

Prior estimate was 3-5 focused days across ~7 PRs. That still holds, with the
caveat that PR 2 is more than half of it and the goose work has dropped out.

## Option C, considered and rejected

Storing grants in our own table and passing them to Check as **contextual
tuples**, with OpenFGA on `memory.New()` as a pure evaluator. Mechanically sound
— `storagewrappers.CombinedTupleReader` unions request tuples over the datastore
and the graph resolver cannot tell them apart — and it deletes ~300 lines of the
adapter. Rejected by the user on direction: it moves traversal responsibility out
of OpenFGA and onto our query, which is the job OpenFGA exists to do. Recorded so
it is not re-proposed.

## Decisions taken

1. **Goal B → B2.** Single commit via a tx-aware datastore wrapper.
2. **Worker → a second embedded server** over the same database. OpenFGA holds no
   authoritative state outside its datastore, so multiple servers on one Postgres
   is its normal deployment. Preserves the current topology.
3. **Tuples live in the app database** (`tflive_test`), on the app's `*pgxpool.Pool`.
   This is a hard prerequisite: a tuple write can only join our transaction if it
   is the same pool and the same database. `deploy/postgres/init.sh` sheds the
   `openfga` role and database.
4. **Pin `github.com/openfga/openfga` explicitly.** `go mod tidy` resolves to
   v1.20.0; the source review behind this document was v1.19.0.
