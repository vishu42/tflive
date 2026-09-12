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

// This file is a deliberate transcription of postgres.Datastore.write with the
// BeginTx and Commit removed, so a tuple write can join a transaction the
// application already has open. Keep it a close mirror of upstream rather than
// a tidier equivalent: the two are diffed by hand when OpenFGA is upgraded, and
// differential_test.go is what catches the divergence if they drift.
//
// See https://github.com/openfga/openfga/issues/3302 -- if upstream exports the
// helpers this file reimplements, most of it can be deleted.

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
	for _, item := range items {
		existing[tupleUtils.TupleKeyToString(item.GetKey())] = item
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
