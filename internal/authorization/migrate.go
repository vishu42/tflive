package authorization

import (
	"context"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/openfga/openfga/assets"
)

// openfgaMigrationsTable tracks which of OpenFGA's migrations we have applied.
// Deliberately separate from the application's schema_migrations: the two
// sequences are versioned by different projects and must not share a counter.
const openfgaMigrationsTable = "openfga_schema_migrations"

// Migrate applies OpenFGA's embedded migrations to the application database.
//
// OpenFGA exports no migration runner, only a cobra command, so we apply the
// files ourselves. That is tractable because the files are plain SQL -- none
// uses a "+goose StatementBegin" block -- so no goose dependency is needed.
//
// Two of goose's annotations still have to be honoured. A file marked
// "+goose NO TRANSACTION" must run outside one, because it uses CREATE INDEX
// CONCURRENTLY, which Postgres refuses inside a transaction block. Those files
// are written to be idempotent (IF NOT EXISTS / IF EXISTS) precisely because a
// partial application cannot be rolled back.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return fmt.Errorf("authorization: pool is required")
	}
	if _, err := pool.Exec(ctx, fmt.Sprintf(
		`create table if not exists %s (version text primary key, applied_at timestamptz not null default now())`,
		openfgaMigrationsTable,
	)); err != nil {
		return fmt.Errorf("create openfga migrations table: %w", err)
	}

	entries, err := fs.ReadDir(assets.EmbedMigrations, assets.PostgresMigrationDir)
	if err != nil {
		return fmt.Errorf("read openfga migrations: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		if err := applyOpenFGAMigration(ctx, pool, name); err != nil {
			return err
		}
	}
	return nil
}

func applyOpenFGAMigration(ctx context.Context, pool *pgxpool.Pool, name string) error {
	raw, err := fs.ReadFile(assets.EmbedMigrations, assets.PostgresMigrationDir+"/"+name)
	if err != nil {
		return fmt.Errorf("read openfga migration %s: %w", name, err)
	}
	content := string(raw)
	up, err := gooseUpSection(content)
	if err != nil {
		return fmt.Errorf("openfga migration %s: %w", name, err)
	}
	if noTransaction(content) {
		return applyOutsideTransaction(ctx, pool, name, up)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin openfga migration %s: %w", name, err)
	}
	defer tx.Rollback(ctx)

	var applied bool
	if err := tx.QueryRow(ctx, fmt.Sprintf(
		`select exists (select 1 from %s where version = $1)`, openfgaMigrationsTable,
	), name).Scan(&applied); err != nil {
		return fmt.Errorf("check openfga migration %s: %w", name, err)
	}
	if applied {
		return nil
	}
	if _, err := tx.Exec(ctx, up); err != nil {
		return fmt.Errorf("apply openfga migration %s: %w", name, err)
	}
	if _, err := tx.Exec(ctx, fmt.Sprintf(
		`insert into %s (version) values ($1)`, openfgaMigrationsTable,
	), name); err != nil {
		return fmt.Errorf("record openfga migration %s: %w", name, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit openfga migration %s: %w", name, err)
	}
	return nil
}

// noTransaction reports whether goose marked this migration as one that must
// not run inside a transaction.
//
//	"-- +goose NO TRANSACTION\n-- +goose Up\nCREATE INDEX CONCURRENTLY …" → true
//	"-- +goose Up\nCREATE TABLE …"                                        → false
func noTransaction(content string) bool {
	return strings.Contains(content, "+goose NO TRANSACTION")
}

// applyOutsideTransaction runs a NO TRANSACTION migration one statement at a
// time, directly on the pool.
//
// One at a time matters: pgx sends a multi-statement Exec as a simple query,
// which Postgres wraps in an implicit transaction -- the very thing CREATE
// INDEX CONCURRENTLY refuses. Recording the version is a separate write, so a
// crash between the two re-runs the migration; that is safe because every
// statement in such a file is written IF NOT EXISTS or IF EXISTS.
func applyOutsideTransaction(ctx context.Context, pool *pgxpool.Pool, name, up string) error {
	var applied bool
	if err := pool.QueryRow(ctx, fmt.Sprintf(
		`select exists (select 1 from %s where version = $1)`, openfgaMigrationsTable,
	), name).Scan(&applied); err != nil {
		return fmt.Errorf("check openfga migration %s: %w", name, err)
	}
	if applied {
		return nil
	}
	for _, statement := range splitStatements(up) {
		if _, err := pool.Exec(ctx, statement); err != nil {
			return fmt.Errorf("apply openfga migration %s: %w", name, err)
		}
	}
	if _, err := pool.Exec(ctx, fmt.Sprintf(
		`insert into %s (version) values ($1) on conflict do nothing`, openfgaMigrationsTable,
	), name); err != nil {
		return fmt.Errorf("record openfga migration %s: %w", name, err)
	}
	return nil
}

// splitStatements breaks a migration body on semicolons.
//
// Naive by design, and safe only because it is applied to OpenFGA's own
// migrations, which are DDL with no semicolons inside string literals or
// dollar-quoted bodies. A file that needed more than this would carry a
// StatementBegin block, which gooseUpSection refuses outright.
func splitStatements(body string) []string {
	var statements []string
	for _, part := range strings.Split(body, ";") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			statements = append(statements, trimmed)
		}
	}
	return statements
}

// gooseUpSection returns the statements between the Up and Down markers.
//
// It refuses a file containing a StatementBegin block rather than mangling it:
// such a file needs a real goose parser, and failing loudly is how we find out
// an upgrade introduced one.
//
//	"-- +goose Up\nCREATE …\n-- +goose Down\nDROP …" → "CREATE …", nil
//	a file with +goose StatementBegin                → "", error
//	a file with no Up marker                         → "", error
func gooseUpSection(content string) (string, error) {
	if strings.Contains(content, "+goose StatementBegin") {
		return "", fmt.Errorf("migration uses a goose StatementBegin block and cannot be applied by this runner")
	}
	_, after, found := strings.Cut(content, "-- +goose Up")
	if !found {
		return "", fmt.Errorf("migration has no +goose Up marker")
	}
	up, _, _ := strings.Cut(after, "-- +goose Down")
	if strings.TrimSpace(up) == "" {
		return "", fmt.Errorf("migration has an empty Up section")
	}
	return up, nil
}
