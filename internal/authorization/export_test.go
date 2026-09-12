package authorization

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/openfga/openfga/pkg/storage"
)

// NewDatastoreForTest exposes the transaction-aware datastore on its own, so
// the storage tests can exercise Write's two paths without booting a server.
func NewDatastoreForTest(pool *pgxpool.Pool) (*Datastore, error) {
	return newDatastore(pool)
}

// NewWithDatastore builds an Authorization over any datastore, so tests can run
// a real engine over memory.New() instead of a fake. That is the whole reason
// this package needs no interface: the real thing is cheap enough to construct.
func NewWithDatastore(ctx context.Context, datastore storage.OpenFGADatastore, storeName string) (*Authorization, error) {
	return newWithDatastore(ctx, datastore, storeName)
}
