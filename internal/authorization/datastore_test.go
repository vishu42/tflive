package authorization_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	"github.com/openfga/openfga/pkg/storage"
	"github.com/openfga/openfga/pkg/tuple"
	"github.com/stretchr/testify/require"

	"github.com/vishu42/tflive/internal/authorization"
)

// testPool gates every test in this file: without a database there is nothing
// worth asserting here, because the deliverable is what Postgres does at
// commit time.
//
// Migrating here rather than in newTestDatastore is where the DSN is: Migrate
// takes a URL, not a pool, because OpenFGA's runner opens its own connection.
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("tflive_POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set tflive_POSTGRES_TEST_DSN (or run `make differential-test`)")
	}
	require.NoError(t, authorization.Migrate(dsn))
	pool, err := pgxpool.New(context.Background(), dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return pool
}

// newTestStoreID keeps runs from colliding. These tests write real rows to a
// real database and never clean up, so a fixed id would fail on the second run
// with a duplicate-tuple error.
func newTestStoreID(t *testing.T) string {
	t.Helper()
	raw := make([]byte, 16)
	_, err := rand.Read(raw)
	require.NoError(t, err)
	return "test-" + hex.EncodeToString(raw)
}

// newTestStoreName is newTestStoreID's counterpart for tests that go through
// bootstrap, which resolves a store by name rather than by id.
//
// A fixed name persists across runs, so any change to how models compare
// leaves that store holding two versions of the same model -- which bootstrap
// then correctly refuses as ambiguous, failing every later run until the rows
// are deleted by hand. One name per test keeps two New calls within a test on
// the same store, which is what the restart test needs.
func newTestStoreName(t *testing.T) string {
	t.Helper()
	return "test-" + hex.EncodeToString(randomBytes(t, 8))
}

func newTestDatastore(t *testing.T, pool *pgxpool.Pool) *authorization.Datastore {
	t.Helper()
	store, err := authorization.NewDatastoreForTest(pool)
	require.NoError(t, err)
	t.Cleanup(store.Close)
	return store
}

// readTuples returns every tuple stored against one object, draining the
// iterator to exhaustion. storage.TupleIterator is the generic Iterator[T]
// interface, which has no ToArray -- that lives on the concrete SQL iterator.
func readTuples(t *testing.T, ctx context.Context, store *authorization.Datastore, storeID, object string) []*openfgav1.Tuple {
	t.Helper()
	iter, err := store.Read(ctx, storeID, storage.ReadFilter{Object: object}, storage.ReadOptions{})
	require.NoError(t, err)
	defer iter.Stop()

	var found []*openfgav1.Tuple
	for {
		next, err := iter.Next(ctx)
		if errors.Is(err, storage.ErrIteratorDone) {
			return found
		}
		require.NoError(t, err)
		found = append(found, next)
	}
}

func TestWriteRollsBackWithTheCallersTransaction(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	store := newTestDatastore(t, pool)
	storeID := newTestStoreID(t)

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	// Rolling back after a successful commit is a no-op. Without this, a failing
	// assertion leaves the transaction open on a pooled connection, and the
	// deferred pool.Close blocks forever instead of reporting the failure.
	defer tx.Rollback(ctx)
	require.NoError(t, store.Write(authorization.WithTx(ctx, tx), storeID, nil,
		storage.Writes{tuple.NewTupleKey("stack:rollback", "owner", "user:alice")}))
	require.NoError(t, tx.Rollback(ctx))

	// The rollback is the assertion: a write that opened its own transaction
	// would have committed and survived this.
	require.Empty(t, readTuples(t, ctx, store, storeID, "stack:rollback"),
		"a rolled-back tuple must not be visible")
}

func TestWriteCommitsWithTheCallersTransaction(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	store := newTestDatastore(t, pool)
	storeID := newTestStoreID(t)

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	// Rolling back after a successful commit is a no-op. Without this, a failing
	// assertion leaves the transaction open on a pooled connection, and the
	// deferred pool.Close blocks forever instead of reporting the failure.
	defer tx.Rollback(ctx)
	require.NoError(t, store.Write(authorization.WithTx(ctx, tx), storeID, nil,
		storage.Writes{tuple.NewTupleKey("stack:commit", "owner", "user:alice")}))
	require.NoError(t, tx.Commit(ctx))

	found := readTuples(t, ctx, store, storeID, "stack:commit")
	require.Len(t, found, 1)
	require.Equal(t, "user:alice", found[0].GetKey().GetUser())
}

func TestWriteWithoutATransactionStillWorks(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	store := newTestDatastore(t, pool)
	storeID := newTestStoreID(t)

	// No WithTx: this must fall through to upstream's own transaction, because
	// bootstrap.SeedRoot writes root's tuple with no domain transaction open.
	require.NoError(t, store.Write(ctx, storeID, nil,
		storage.Writes{tuple.NewTupleKey("stack:notx", "owner", "user:alice")}))

	require.Len(t, readTuples(t, ctx, store, storeID, "stack:notx"), 1)
}

// TestWriteIsVisibleInsideItsOwnTransaction proves the write really is on the
// caller's transaction rather than merely coincident with it: the rows must be
// readable through that transaction before any commit has happened.
func TestWriteIsVisibleInsideItsOwnTransaction(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	store := newTestDatastore(t, pool)
	storeID := newTestStoreID(t)

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx)

	require.NoError(t, store.Write(authorization.WithTx(ctx, tx), storeID, nil,
		storage.Writes{tuple.NewTupleKey("stack:visible", "owner", "user:alice")}))

	var count int
	require.NoError(t, tx.QueryRow(ctx,
		`select count(*) from tuple where store = $1`, storeID).Scan(&count))
	require.Equal(t, 1, count, "the row must be visible on the transaction that wrote it")

	// And invisible to anyone outside it, until the commit that never comes.
	require.Empty(t, readTuples(t, ctx, store, storeID, "stack:visible"),
		"an uncommitted row must not be visible outside its transaction")
}

// unmigratedTestPool is testPool's counterpart: a database with no schema at
// all, which is the state the New guard exists to report.
func unmigratedTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("tflive_POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set tflive_POSTGRES_TEST_DSN (or run `make differential-test`)")
	}
	admin, err := pgxpool.New(context.Background(), dsn)
	require.NoError(t, err)
	defer admin.Close()

	name := "unmigrated_" + hex.EncodeToString(randomBytes(t, 8))
	_, err = admin.Exec(context.Background(), "create database "+name)
	require.NoError(t, err)
	t.Cleanup(func() {
		cleanup, err := pgxpool.New(context.Background(), dsn)
		if err != nil {
			return
		}
		defer cleanup.Close()
		_, _ = cleanup.Exec(context.Background(), "drop database if exists "+name+" with (force)")
	})

	pool, err := pgxpool.New(context.Background(), replaceDatabase(t, dsn, name))
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return pool
}

func randomBytes(t *testing.T, size int) []byte {
	t.Helper()
	raw := make([]byte, size)
	_, err := rand.Read(raw)
	require.NoError(t, err)
	return raw
}

// replaceDatabase swaps the database name in a DSN, leaving everything else --
// credentials, host, sslmode -- exactly as the caller configured it.
func replaceDatabase(t *testing.T, dsn, name string) string {
	t.Helper()
	parsed, err := url.Parse(dsn)
	require.NoError(t, err)
	parsed.Path = "/" + name
	return parsed.String()
}
