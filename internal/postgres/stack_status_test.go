package postgres

import (
	"context"
	"errors"
	"path"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/vishu42/tflive/internal/app"
	"github.com/vishu42/tflive/internal/domain"
)

func statusTestStack(id, slug string, status domain.StackStatus) domain.Stack {
	return domain.Stack{
		ID:        domain.StackID(id),
		TenantID:  domain.TenantID("tenant_123"),
		Name:      "Acme " + slug,
		Slug:      slug,
		Status:    status,
		Tags:      map[string]string{},
		CreatedBy: domain.UserID("user_123"),
		CreatedAt: time.Date(2026, 8, 6, 13, 30, 0, 0, time.UTC),
	}
}

func TestCreateStackRoundTripsProvisioningStatus(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)

	if err := store.CreateStack(ctx, statusTestStack("stack_provisioning", "provisioning", domain.StackStatusProvisioning)); err != nil {
		t.Fatalf("CreateStack returned error: %v", err)
	}
	// A caller that says nothing about provisioning gets the terminal status.
	if err := store.CreateStack(ctx, statusTestStack("stack_default", "default", "")); err != nil {
		t.Fatalf("CreateStack returned error: %v", err)
	}

	provisioning, err := store.GetStack(ctx, "tenant_123", "stack_provisioning")
	if err != nil {
		t.Fatalf("GetStack returned error: %v", err)
	}
	if provisioning.Status != domain.StackStatusProvisioning {
		t.Fatalf("status = %q, want %q", provisioning.Status, domain.StackStatusProvisioning)
	}

	defaulted, err := store.GetStack(ctx, "tenant_123", "stack_default")
	if err != nil {
		t.Fatalf("GetStack returned error: %v", err)
	}
	if defaulted.Status != domain.StackStatusReady {
		t.Fatalf("status = %q, want %q", defaulted.Status, domain.StackStatusReady)
	}
}

func TestMarkStackReadyIsIdempotent(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)

	if err := store.CreateStack(ctx, statusTestStack("stack_123", "acme-prod", domain.StackStatusProvisioning)); err != nil {
		t.Fatalf("CreateStack returned error: %v", err)
	}

	for attempt := 0; attempt < 2; attempt++ {
		if err := store.MarkStackReady(ctx, "tenant_123", "stack_123"); err != nil {
			t.Fatalf("MarkStackReady attempt %d returned error: %v", attempt, err)
		}
		stack, err := store.GetStack(ctx, "tenant_123", "stack_123")
		if err != nil {
			t.Fatalf("GetStack returned error: %v", err)
		}
		if stack.Status != domain.StackStatusReady {
			t.Fatalf("status after attempt %d = %q, want %q", attempt, stack.Status, domain.StackStatusReady)
		}
	}
}

func TestMarkStackReadyIsTenantScoped(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openMigratedTestPool(t, ctx)
	store := NewStore(pool)

	if err := store.CreateStack(ctx, statusTestStack("stack_123", "acme-prod", domain.StackStatusProvisioning)); err != nil {
		t.Fatalf("CreateStack returned error: %v", err)
	}

	if err := store.MarkStackReady(ctx, "tenant_other", "stack_123"); !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}
	stack, err := store.GetStack(ctx, "tenant_123", "stack_123")
	if err != nil {
		t.Fatalf("GetStack returned error: %v", err)
	}
	if stack.Status != domain.StackStatusProvisioning {
		t.Fatalf("status = %q, want it untouched by another tenant", stack.Status)
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

func TestStackStatusMigrationBackfillsFromPendingQueueRows(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := openTestPool(t, ctx)
	migrateThrough(t, ctx, pool, "0012_work_queue")

	for _, seed := range []struct {
		id   string
		slug string
	}{
		{id: "stack_pending", slug: "pending"},
		{id: "stack_delivered", slug: "delivered"},
		{id: "stack_untouched", slug: "untouched"},
	} {
		if _, err := pool.Exec(ctx, `
			insert into stacks (id, tenant_id, name, slug, tags_json, default_credential_ids_json, created_by, created_at)
			values ($1, 'tenant_123', $2, $2, '{}'::jsonb, '[]'::jsonb, 'user_123', now())
		`, seed.id, seed.slug); err != nil {
			t.Fatalf("seed stack %s: %v", seed.id, err)
		}
	}

	if _, err := pool.Exec(ctx, `
		insert into work_queue (kind, resource_key, payload, processed_at)
		values ('reconcile_stack_grant', 'stack:stack_pending/user:abc', '{}'::jsonb, null),
		       ('reconcile_stack_grant', 'stack:stack_delivered/user:abc', '{}'::jsonb, now())
	`); err != nil {
		t.Fatalf("seed work queue: %v", err)
	}

	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	store := NewStore(pool)
	for _, want := range []struct {
		id     string
		status domain.StackStatus
	}{
		{id: "stack_pending", status: domain.StackStatusProvisioning},
		{id: "stack_delivered", status: domain.StackStatusReady},
		{id: "stack_untouched", status: domain.StackStatusReady},
	} {
		stack, err := store.GetStack(ctx, "tenant_123", domain.StackID(want.id))
		if err != nil {
			t.Fatalf("GetStack(%s) returned error: %v", want.id, err)
		}
		if stack.Status != want.status {
			t.Fatalf("%s status = %q, want %q", want.id, stack.Status, want.status)
		}
	}
}
