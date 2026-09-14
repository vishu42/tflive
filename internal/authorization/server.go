package authorization

import (
	"context"
	"fmt"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/openfga/openfga/pkg/server"
	"github.com/openfga/openfga/pkg/storage"
)

// defaultStoreName is the OpenFGA store this application adopts when none is
// named. The name is what bootstrap reconciles against, so it is configuration
// rather than an identifier anyone has to record.
const defaultStoreName = "tflive"

// Authorization answers authorization questions against an in-process OpenFGA.
//
// There is no interface over this type. There is one provider and there will
// only ever be one, so a port would buy nothing and cost a layer. Tests
// construct a real Authorization over an in-memory datastore rather than a fake,
// which is why the model's own tier ordering is exercised rather than stubbed.
type Authorization struct {
	server    *server.Server
	store     storage.OpenFGADatastore
	storeID   string
	modelID   string
	closeOnce sync.Once
}

// New starts an embedded OpenFGA over the application's pool and resolves the
// store and authorization model this process will use.
//
// The schema must already be migrated: Migrate is the caller's to run, beside
// the application's own migrations, because it needs a DSN rather than a pool
// and because a constructor that silently migrates hides the one step an
// operator most needs to see.
//
// The pool is borrowed, not owned: Close shuts down the server and datastore
// and leaves the pool to whoever created it, because the application is still
// using it for everything else.
//
//	pool, "tflive" → *Authorization with a resolved store and model
//	nil pool       → nil, error
//	a second call against the same database adopts the same store and model
func New(ctx context.Context, pool *pgxpool.Pool, storeName string) (*Authorization, error) {
	if pool == nil {
		return nil, fmt.Errorf("authorization: pool is required")
	}
	if err := requireMigratedSchema(ctx, pool); err != nil {
		return nil, err
	}
	datastore, err := newDatastore(pool)
	if err != nil {
		return nil, err
	}
	return NewWithDatastore(ctx, datastore, storeName)
}

// NewWithDatastore builds an Authorization over any OpenFGA datastore.
//
// It exists so tests -- in this package and in every package that depends on
// authorization -- can run a real engine over memory.New() instead of a fake.
// That is what makes the missing interface a non-issue: there is nothing to
// stub, because the real thing costs a few microseconds to construct and
// answers from the actual model rather than from a test's idea of it.
//
// Production code calls New, which supplies the transaction-aware Postgres
// datastore.
func NewWithDatastore(ctx context.Context, datastore storage.OpenFGADatastore, storeName string) (*Authorization, error) {
	if storeName == "" {
		storeName = defaultStoreName
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

// StoreID and ModelID name what this process resolved at startup. They exist
// for diagnostics: nothing configures them, and nothing else needs them.
func (auth *Authorization) StoreID() string { return auth.storeID }
func (auth *Authorization) ModelID() string { return auth.modelID }

// Close shuts the server down. It is safe to call more than once, because New
// closes on a failed bootstrap and callers also defer it.
func (auth *Authorization) Close() {
	auth.closeOnce.Do(func() {
		auth.server.Close()
		auth.store.Close()
	})
}
