package authorization

import (
	"context"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"
	openfgav1 "github.com/openfga/api/proto/openfga/v1"

	"github.com/openfga/openfga/pkg/storage"
	"github.com/openfga/openfga/pkg/storage/postgres"
	"github.com/openfga/openfga/pkg/storage/sqlcommon"
	tupleUtils "github.com/openfga/openfga/pkg/tuple"
)

// This file is write.go's counterpart for the reads OpenFGA makes in the middle
// of a unit of work: transcriptions of postgres.Datastore's ReadAuthorizationModel
// and ReadPage, run on the caller's transaction instead of the pool.
//
// Both are needed because a transaction already holds a connection. A read that
// goes to the pool needs a second one, and once every pooled connection is held
// by a transaction waiting for another, no request can finish. Keep them close
// mirrors of upstream for the same reason as write.go.

// readAuthorizationModelOnTx mirrors postgres.Datastore.ReadAuthorizationModel.
// OpenFGA calls it to validate every write, so without it a Grant inside a
// transaction deadlocks on the model lookup before it reaches Write.
func readAuthorizationModelOnTx(ctx context.Context, tx pgx.Tx, store, modelID string) (*openfgav1.AuthorizationModel, error) {
	stmt, args, err := sq.StatementBuilder.PlaceholderFormat(sq.Dollar).
		Select("authorization_model_id", "schema_version", "type", "type_definition", "serialized_protobuf").
		From("authorization_model").
		Where(sq.Eq{
			"store":                  store,
			"authorization_model_id": modelID,
		}).ToSql()
	if err != nil {
		return nil, postgres.HandleSQLError(err)
	}

	rows, err := tx.Query(ctx, stmt, args...)
	if err != nil {
		return nil, postgres.HandleSQLError(err)
	}
	defer rows.Close()
	model, err := sqlcommon.ConstructAuthorizationModelFromSQLRows(pgxRows{rows})
	if err != nil {
		return nil, postgres.HandleSQLError(err)
	}
	return model, nil
}

// readPageOnTx mirrors postgres.Datastore.ReadPage, with upstream's read helper
// inlined for the paginated case. ListGrants reaches it through the server's
// Read, which is how the role change reads grants before it rewrites them. On
// the transaction it also sees the unit of work's own uncommitted tuples.
func readPageOnTx(ctx context.Context, tx pgx.Tx, store string, filter storage.ReadFilter, options storage.ReadPageOptions) ([]*openfgav1.Tuple, string, error) {
	sb := sq.StatementBuilder.PlaceholderFormat(sq.Dollar).
		Select(
			"store", "object_type", "object_id", "relation",
			"_user",
			"condition_name", "condition_context", "ulid", "inserted_at",
		).
		From("tuple").
		Where(sq.Eq{"store": store}).
		OrderBy("ulid")

	objectType, objectID := tupleUtils.SplitObject(filter.Object)
	if objectType != "" {
		sb = sb.Where(sq.Eq{"object_type": objectType})
	}
	if objectID != "" {
		sb = sb.Where(sq.Eq{"object_id": objectID})
	}
	if filter.Relation != "" {
		sb = sb.Where(sq.Eq{"relation": filter.Relation})
	}
	if filter.User != "" {
		userType, userID, _ := tupleUtils.ToUserParts(filter.User)
		if userID != "" {
			sb = sb.Where(sq.Eq{"_user": filter.User})
		} else {
			sb = sb.Where(sq.Like{"_user": userType + ":%"})
		}
	}

	if len(filter.Conditions) > 0 {
		sb = sb.Where(sq.Eq{"COALESCE(condition_name, '')": filter.Conditions})
	}

	if options.Pagination.From != "" {
		sb = sb.Where(sq.GtOrEq{"ulid": options.Pagination.From})
	}
	if options.Pagination.PageSize != 0 {
		sb = sb.Limit(uint64(options.Pagination.PageSize + 1)) //nolint:gosec // transcribed from OpenFGA; + 1 is used to determine whether to return a continuation token.
	}

	getRows, err := sqlcommon.NewRowGetter(txConnector{tx: tx}, sb)
	if err != nil {
		return nil, "", postgres.HandleSQLError(err)
	}
	iter := sqlcommon.NewSQLTupleIterator(getRows, postgres.HandleSQLError)
	defer iter.Stop()

	return iter.ToArray(ctx, options.Pagination)
}
