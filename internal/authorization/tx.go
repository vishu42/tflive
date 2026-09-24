// Package authorization owns everything this application knows about
// authorization: the model, the embedded OpenFGA server that evaluates it, the
// storage that persists its tuples, and the questions callers ask.
//
// There is deliberately no provider port. There is one provider and there will
// only ever be one, so an interface would add a layer without adding a choice.
package authorization

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/openfga/openfga/pkg/storage/sqlcommon"
)

// txKey is a private type so no other package can write a value this one would
// read back as a transaction.
type txKey struct{}

// WithTx marks ctx as running inside tx. Every OpenFGA write made under the
// returned context commits and rolls back with the caller's work.
func WithTx(ctx context.Context, tx pgx.Tx) context.Context {
	return context.WithValue(ctx, txKey{}, tx)
}

// TxFrom recovers the transaction WithTx installed.
//
//	WithTx(ctx, tx)      → tx, true
//	context.Background() → nil, false
func TxFrom(ctx context.Context) (pgx.Tx, bool) {
	tx, ok := ctx.Value(txKey{}).(pgx.Tx)
	return tx, ok
}

// txConnector presents a pgx.Tx through the Connector interface OpenFGA's
// row-getter expects. Upstream has an equivalent for its pool
// (pgxPoolConnector) but does not export it.
type txConnector struct{ tx pgx.Tx }

func (c txConnector) Connect(context.Context) (sqlcommon.Connection, error) {
	return txConnection(c), nil
}

type txConnection struct{ tx pgx.Tx }

func (c txConnection) Query(ctx context.Context, stmt string, args ...any) (sqlcommon.Rows, error) {
	rows, err := c.tx.Query(ctx, stmt, args...)
	if err != nil {
		return nil, err
	}
	return pgxRows{rows}, nil
}

// Close is a no-op: the transaction belongs to the caller, and closing it here
// would end their unit of work halfway through.
func (c txConnection) Close() error { return nil }

// pgxRows adapts pgx.Rows to sqlcommon.Rows. pgx's Close returns nothing while
// sqlcommon's must return an error; upstream writes the same shim for its own
// pool path (pgxRowsWrapper).
type pgxRows struct{ pgx.Rows }

func (r pgxRows) Close() error {
	r.Rows.Close()
	return r.Err()
}

var (
	_ sqlcommon.Connector  = txConnector{}
	_ sqlcommon.Connection = txConnection{}
	_ sqlcommon.Rows       = pgxRows{}
)
