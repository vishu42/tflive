package postgres

import (
	"context"
	"path"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/vishu42/tflive/internal/domain"
)

func TestCreateStackDefaultsToReady(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)

	if err := store.CreateStack(ctx, domain.Stack{
		ID:        domain.StackID("stack_default"),
		TenantID:  domain.TenantID("tenant_123"),
		Name:      "Acme default",
		Slug:      "default",
		Tags:      map[string]string{},
		CreatedBy: domain.UserID("user_123"),
		CreatedAt: time.Date(2026, 8, 6, 13, 30, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("CreateStack returned error: %v", err)
	}

	stack, err := store.GetStack(ctx, "tenant_123", "stack_default")
	if err != nil {
		t.Fatalf("GetStack returned error: %v", err)
	}
	if stack.Status != domain.StackStatusReady {
		t.Fatalf("status = %q, want %q", stack.Status, domain.StackStatusReady)
	}
}

// migrateThrough applies migrations in order and stops after the named
// version, so a test can seed the state a later migration has to convert.
func migrateThrough(t *testing.T, ctx context.Context, pool *pgxpool.Pool, lastVersion string) {
	t.Helper()

	if _, err := pool.Exec(ctx, `
		create table if not exists schema_migrations (
			version text primary key,
			applied_at timestamptz not null default now()
		)
	`); err != nil {
		t.Fatalf("create schema migrations table: %v", err)
	}

	entries, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		t.Fatalf("read migrations: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && path.Ext(entry.Name()) == ".sql" {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		version := strings.TrimSuffix(name, ".sql")
		if version > lastVersion {
			return
		}
		if err := applyMigration(ctx, pool, version, path.Join("migrations", name)); err != nil {
			t.Fatalf("apply migration %s: %v", version, err)
		}
	}
}
