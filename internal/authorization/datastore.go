package authorization

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	openfgav1 "github.com/openfga/api/proto/openfga/v1"

	"github.com/openfga/openfga/pkg/storage"
	"github.com/openfga/openfga/pkg/storage/postgres"
	"github.com/openfga/openfga/pkg/storage/sqlcommon"
)

// Datastore is OpenFGA's Postgres datastore with one behaviour added: a call
// made under a context carrying a pgx.Tx runs on that transaction.
//
// Everything else -- stores, assertions, the changelog, the reads Check uses --
// is upstream's, inherited by embedding. Three methods are overridden:
//
//	Write                  → must be atomic with a domain change
//	ReadAuthorizationModel → OpenFGA reads the model to validate every Write
//	ReadPage               → ListGrants reads inside the role-change transaction
//
// The two reads are there because a transaction already holds a connection. A
// read that went to the pool would need a second, and with every connection
// held by a transaction waiting for one, the API deadlocks -- permanently,
// because OpenFGA strips cancellation from the contexts it hands a datastore.
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

// ReadAuthorizationModel loads a model, on the caller's transaction when there
// is one. OpenFGA reads the model to validate every write, so this is on the
// path of every Grant.
//
//	WithTx(ctx, tx) → query on tx, no second connection taken
//	bare ctx        → upstream's, on the pool
func (store *Datastore) ReadAuthorizationModel(ctx context.Context, storeID, modelID string) (*openfgav1.AuthorizationModel, error) {
	tx, ok := TxFrom(ctx)
	if !ok {
		return store.Datastore.ReadAuthorizationModel(ctx, storeID, modelID)
	}
	return readAuthorizationModelOnTx(ctx, tx, storeID, modelID)
}

// ReadPage reads one page of tuples, on the caller's transaction when there is
// one. The server's Read -- and so ListGrants -- is built on it.
//
//	WithTx(ctx, tx) → query on tx, sees the transaction's uncommitted tuples
//	bare ctx        → upstream's, on the pool
func (store *Datastore) ReadPage(ctx context.Context, storeID string, filter storage.ReadFilter, options storage.ReadPageOptions) ([]*openfgav1.Tuple, string, error) {
	tx, ok := TxFrom(ctx)
	if !ok {
		return store.Datastore.ReadPage(ctx, storeID, filter, options)
	}
	return readPageOnTx(ctx, tx, storeID, filter, options)
}

// Close releases the datastore without closing the pool.
//
// Upstream's Close calls primaryDB.Close() unconditionally, which would shut
// down the application's shared pool -- the one serving every repository, the
// queue and the session store. The pool is borrowed here, not owned, so closing
// it is the caller's business and never ours.
//
// Nothing else is leaked by skipping it: the only other thing upstream's Close
// does is unregister a Prometheus collector, and that collector is created only
// when Config.ExportMetrics is set, which newDatastore does not set.
func (store *Datastore) Close() {}

// Datastore must remain a complete OpenFGA datastore, or it cannot be handed to
// server.WithDatastore.
var _ storage.OpenFGADatastore = (*Datastore)(nil)
