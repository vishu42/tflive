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
	"google.golang.org/protobuf/proto"

	"github.com/vishu42/tflive/internal/authorization"
)

// TestTransactionalWriteMatchesUpstream writes the same tuples two ways -- once
// through our transactional path and once through OpenFGA's own -- and asserts
// the resulting rows are indistinguishable apart from the identifiers expected
// to differ.
//
// internal/authorization/write.go is a transcription of upstream's write path,
// and a transcription diverges silently: an upstream migration that adds a
// column keeps compiling here and starts writing rows that are subtly wrong.
// This is the only thing that catches that, and it catches it the day the
// dependency is bumped rather than months later.
//
// If it fails, re-read postgres.Datastore.write and bring writeOnTx back in
// step. Do not "fix" it by loosening the comparison.
func TestTransactionalWriteMatchesUpstream(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	store := newTestDatastore(t, pool)

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
	// Rolling back after a successful commit is a no-op. Without this, a failing
	// assertion leaves the transaction open on a pooled connection, and the
	// deferred pool.Close blocks forever instead of reporting the failure.
	defer tx.Rollback(ctx)
	require.NoError(t, store.Write(authorization.WithTx(ctx, tx), ourStore, nil, keys))
	require.NoError(t, tx.Commit(ctx))

	require.Equal(t,
		readRows(t, ctx, pool, "tuple", upstreamStore),
		readRows(t, ctx, pool, "tuple", ourStore),
		"tuple rows must be identical apart from store, ulid and inserted_at")
	require.Equal(t,
		readRows(t, ctx, pool, "changelog", upstreamStore),
		readRows(t, ctx, pool, "changelog", ourStore),
		"changelog rows must be identical apart from store, ulid and inserted_at")
}

// TestTransactionalDeleteMatchesUpstream is the delete-side mirror. Deletes go
// through a different statement and a different conflict check, so a write-only
// comparison would leave half the transcription unguarded.
func TestTransactionalDeleteMatchesUpstream(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	store := newTestDatastore(t, pool)

	upstreamStore, ourStore := newTestStoreID(t), newTestStoreID(t)

	seed := storage.Writes{
		tuple.NewTupleKey("stack:del", "owner", "user:alice"),
		tuple.NewTupleKey("stack:del", "viewer", "user:bob"),
	}
	doomed := storage.Deletes{
		{Object: "stack:del", Relation: "viewer", User: "user:bob"},
	}

	require.NoError(t, store.Write(ctx, upstreamStore, nil, seed))
	require.NoError(t, store.Write(ctx, upstreamStore, doomed, nil))

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	// Rolling back after a successful commit is a no-op. Without this, a failing
	// assertion leaves the transaction open on a pooled connection, and the
	// deferred pool.Close blocks forever instead of reporting the failure.
	defer tx.Rollback(ctx)
	require.NoError(t, store.Write(authorization.WithTx(ctx, tx), ourStore, nil, seed))
	require.NoError(t, store.Write(authorization.WithTx(ctx, tx), ourStore, doomed, nil))
	require.NoError(t, tx.Commit(ctx))

	require.Equal(t,
		readRows(t, ctx, pool, "tuple", upstreamStore),
		readRows(t, ctx, pool, "tuple", ourStore),
		"surviving tuple rows must be identical")
	require.Equal(t,
		readRows(t, ctx, pool, "changelog", upstreamStore),
		readRows(t, ctx, pool, "changelog", ourStore),
		"changelog must record the delete the same way")
}

// TestTransactionalReadPageMatchesUpstream guards read.go the way the tests
// above guard write.go: the same committed tuples, read once through upstream's
// ReadPage and once through our transaction, must come back identical -- for
// every filter shape upstream's query builder branches on, and across a page
// boundary, so the continuation token is compared too.
func TestTransactionalReadPageMatchesUpstream(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	store := newTestDatastore(t, pool)
	storeID := newTestStoreID(t)

	require.NoError(t, store.Write(ctx, storeID, nil, storage.Writes{
		tuple.NewTupleKey("stack:read", "owner", "user:alice"),
		tuple.NewTupleKey("stack:read", "viewer", "user:bob"),
		tuple.NewTupleKey("stack:read", "parent", "platform:tflive"),
		tuple.NewTupleKey("stack:other", "owner", "user:alice"),
	}))

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx)
	txCtx := authorization.WithTx(ctx, tx)

	filters := map[string]storage.ReadFilter{
		"object type only":    {Object: "stack:"},
		"object":              {Object: "stack:read"},
		"object and relation": {Object: "stack:read", Relation: "owner"},
		"exact user":          {Object: "stack:", User: "user:alice"},
		"user type":           {Object: "stack:read", User: "user:"},
		"no condition":        {Object: "stack:read", Conditions: []string{""}},
	}
	for name, filter := range filters {
		// Not 0: a zero page size returns no tuples and a token pointing back at
		// the first row, so paging never advances. The server always sets one.
		for _, pageSize := range []int{1, 100} {
			options := storage.ReadPageOptions{Pagination: storage.PaginationOptions{PageSize: pageSize}}
			for page := 0; ; page++ {
				require.Less(t, page, 10, "%s: paging did not terminate", name)
				wantTuples, wantToken, err := store.ReadPage(ctx, storeID, filter, options)
				require.NoError(t, err)
				gotTuples, gotToken, err := store.ReadPage(txCtx, storeID, filter, options)
				require.NoError(t, err)

				label := fmt.Sprintf("%s, page size %d, page %d", name, pageSize, page)
				require.Equal(t, wantToken, gotToken, "%s: continuation token", label)
				require.Len(t, gotTuples, len(wantTuples), "%s: tuple count", label)
				for i := range wantTuples {
					require.True(t, proto.Equal(wantTuples[i], gotTuples[i]), "%s: tuple %d", label, i)
				}
				if wantToken == "" {
					break
				}
				options.Pagination.From = wantToken
			}
		}
	}
}

// TestTransactionalReadAuthorizationModelMatchesUpstream is the model-side
// mirror, over the model bootstrap actually writes.
func TestTransactionalReadAuthorizationModelMatchesUpstream(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	auth, err := authorization.New(ctx, pool, newTestStoreName(t))
	require.NoError(t, err)
	t.Cleanup(auth.Close)
	store := newTestDatastore(t, pool)

	want, err := store.ReadAuthorizationModel(ctx, auth.StoreID(), auth.ModelID())
	require.NoError(t, err)

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx)
	got, err := store.ReadAuthorizationModel(authorization.WithTx(ctx, tx), auth.StoreID(), auth.ModelID())
	require.NoError(t, err)

	require.True(t, proto.Equal(want, got), "the model read on a transaction must match upstream's")
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
	require.NotEmpty(t, rendered, "store %s has no rows in %s to compare", storeID, table)
	return rendered
}
