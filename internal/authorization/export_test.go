package authorization

import "github.com/jackc/pgx/v5/pgxpool"

// NewDatastoreForTest exposes the transaction-aware datastore on its own, so
// the storage tests can exercise Write's two paths without booting a server.
func NewDatastoreForTest(pool *pgxpool.Pool) (*Datastore, error) {
	return newDatastore(pool)
}
