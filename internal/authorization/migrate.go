package authorization

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/openfga/openfga/pkg/server/config"
	"github.com/openfga/openfga/pkg/storage/migrate"
)

// Migrate applies OpenFGA's schema migrations to the application database.
//
// OpenFGA exports RunMigrations precisely for embedders: it runs the same goose
// migrations the `openfga migrate` CLI does, so this process and that command
// can never disagree about what the schema is. It opens its own database/sql
// connection from the DSN rather than borrowing a pgxpool, which is why this
// takes a URL where every other entry point in the package takes a pool.
//
// The timeouts are not optional. A zero PingTimeout makes every ping context
// expire before it is used, and a zero Timeout means the retry policy never
// gives up -- together they hang startup with no error. OpenFGA's own defaults
// are used so this matches what the CLI would do.
func Migrate(databaseURL string) error {
	if databaseURL == "" {
		return fmt.Errorf("authorization: database url is required")
	}
	if err := migrate.RunMigrations(migrate.MigrationConfig{
		Engine:      "postgres",
		URI:         databaseURL,
		Timeout:     config.DefaultDatastorePingRetryMaxElapsedTime,
		PingTimeout: config.DefaultDatastorePingTimeout,
	}); err != nil {
		return fmt.Errorf("authorization: migrate: %w", err)
	}
	return nil
}

// requireMigratedSchema fails before the server starts when OpenFGA's tables
// are absent.
//
// New does not migrate, so it depends on a caller having done it. Without this
// check that dependency surfaces as OpenFGA's generic internal error -- the
// underlying "relation \"store\" does not exist" is lost crossing the service
// boundary, so nothing in the failure points at migrations.
//
//	migrated database   → nil
//	empty database      → an error naming Migrate
func requireMigratedSchema(ctx context.Context, pool *pgxpool.Pool) error {
	var migrated bool
	if err := pool.QueryRow(ctx,
		`select to_regclass('public.store') is not null`,
	).Scan(&migrated); err != nil {
		return fmt.Errorf("authorization: check schema: %w", err)
	}
	if !migrated {
		return fmt.Errorf("authorization: schema is not migrated; call Migrate first")
	}
	return nil
}
