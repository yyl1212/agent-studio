package postgres

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestMigrateUpgradesInitialSchemaWithDebugRunEvents(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	databaseTestMutex.Lock()
	ctx := context.Background()
	admin, err := Open(ctx, databaseURL)
	if err != nil {
		databaseTestMutex.Unlock()
		t.Fatal(err)
	}
	schema := fmt.Sprintf("migration_upgrade_%d", fixtureSequence.Add(1))
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.pool.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		admin.Close()
		databaseTestMutex.Unlock()
		t.Fatal(err)
	}
	var store *Store
	t.Cleanup(func() {
		if store != nil {
			store.Close()
		}
		_, _ = admin.pool.Exec(context.Background(), "DROP SCHEMA "+identifier+" CASCADE")
		admin.Close()
		databaseTestMutex.Unlock()
	})
	parsedURL, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsedURL.Query()
	query.Set("options", "-csearch_path="+schema)
	parsedURL.RawQuery = query.Encode()
	store, err = Open(ctx, parsedURL.String())
	if err != nil {
		t.Fatal(err)
	}
	var currentSchema string
	if err := store.pool.QueryRow(ctx, "SELECT current_schema()").Scan(&currentSchema); err != nil {
		t.Fatal(err)
	}
	if currentSchema != schema {
		t.Fatalf("current schema=%q, want %q", currentSchema, schema)
	}
	if _, err := store.pool.Exec(ctx, `CREATE TABLE schema_migrations (
		version bigint PRIMARY KEY,
		name text NOT NULL,
		applied_at timestamptz NOT NULL DEFAULT now()
	)`); err != nil {
		t.Fatal(err)
	}
	files, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("migration files=%d, want 2", len(files))
	}
	if err := store.applyMigration(ctx, files[0]); err != nil {
		t.Fatal(err)
	}
	var before *string
	if err := store.pool.QueryRow(ctx, "SELECT to_regclass('run_events')::text").Scan(&before); err != nil {
		t.Fatal(err)
	}
	if before != nil {
		t.Fatalf("run_events existed before upgrade: %q", *before)
	}
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var after *string
	if err := store.pool.QueryRow(ctx, "SELECT to_regclass('run_events')::text").Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after == nil || *after != "run_events" {
		t.Fatalf("run_events after upgrade=%v", after)
	}
}
