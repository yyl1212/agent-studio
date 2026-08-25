package postgres

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestMigrateUpgradesPreviousSchemaWithManagementColumns(t *testing.T) {
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
	if len(files) != 3 {
		t.Fatalf("migration files=%d, want 3", len(files))
	}
	if err := store.applyMigration(ctx, files[0]); err != nil {
		t.Fatal(err)
	}
	if err := store.applyMigration(ctx, files[1]); err != nil {
		t.Fatal(err)
	}
	var beforeColumn *string
	if err := store.pool.QueryRow(ctx, `SELECT data_type FROM information_schema.columns
		WHERE table_schema=current_schema() AND table_name='workflows' AND column_name='archived_at'`).Scan(&beforeColumn); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal(err)
	}
	if beforeColumn != nil {
		t.Fatalf("archived_at existed before management migration: %q", *beforeColumn)
	}
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var archivedType string
	if err := store.pool.QueryRow(ctx, `SELECT data_type FROM information_schema.columns
		WHERE table_schema=current_schema() AND table_name='workflows' AND column_name='archived_at'`).Scan(&archivedType); err != nil {
		t.Fatal(err)
	}
	if archivedType != "timestamp with time zone" {
		t.Fatalf("archived_at type=%q", archivedType)
	}
	for _, indexName := range []string{
		"workflows_management_idx",
		"runs_started_at_id_idx",
		"runs_workflow_started_at_id_idx",
		"runs_status_started_at_id_idx",
		"runs_mode_started_at_id_idx",
		"runs_workflow_status_started_at_id_idx",
		"runs_workflow_mode_started_at_id_idx",
	} {
		var index *string
		if err := store.pool.QueryRow(ctx, "SELECT to_regclass($1)::text", indexName).Scan(&index); err != nil {
			t.Fatal(err)
		}
		if index == nil || *index != indexName {
			t.Fatalf("index %q after upgrade=%v", indexName, index)
		}
	}
}
