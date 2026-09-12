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
