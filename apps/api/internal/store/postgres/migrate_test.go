package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestMigrateUpgradesPreviousSchemaWithRunRecovery(t *testing.T) {
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
	if len(files) != 5 {
		t.Fatalf("migration files=%d, want 5", len(files))
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
	if err := store.applyMigration(ctx, files[2]); err != nil {
		t.Fatal(err)
	}
	const workflowID = "00000000-0000-0000-0000-000000000101"
	const otherWorkflowID = "00000000-0000-0000-0000-000000000102"
	const historicalRunID = "00000000-0000-0000-0000-000000000201"
	const historicalVersionID = "00000000-0000-0000-0000-000000000401"
	if _, err := store.pool.Exec(ctx, `INSERT INTO workflows(id,name,slug,draft_graph)
		VALUES($1,'Historical','historical','{}'::jsonb),($2,'Other','other','{}'::jsonb)`, workflowID, otherWorkflowID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO runs(
		id,workflow_id,draft_revision,graph_snapshot,mode,status,input,started_at
	) VALUES($1,$2,1,'{}'::jsonb,'test','running','{}'::jsonb,now())`, historicalRunID, workflowID); err != nil {
		t.Fatal(err)
	}
	if err := store.applyMigration(ctx, files[3]); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO workflow_versions(id,workflow_id,version,graph,input_schema)
		VALUES($1,$2,1,'{}'::jsonb,'{"type":"object"}'::jsonb)`, historicalVersionID, workflowID); err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var workflowPresentation, versionPresentation []byte
	if err := store.pool.QueryRow(ctx, "SELECT agent_presentation FROM workflows WHERE id=$1", workflowID).Scan(&workflowPresentation); err != nil {
		t.Fatal(err)
	}
	if err := store.pool.QueryRow(ctx, "SELECT agent_presentation FROM workflow_versions WHERE id=$1", historicalVersionID).Scan(&versionPresentation); err != nil {
		t.Fatal(err)
	}
	for _, raw := range [][]byte{workflowPresentation, versionPresentation} {
		var got map[string]any
		if err := json.Unmarshal(raw, &got); err != nil || got["title"] != "Historical" || got["description"] != "" || got["accent"] != "indigo" || got["submitLabel"] != "运行 Agent" || got["resultMode"] != "auto" {
			t.Fatalf("presentation=%s error=%v", raw, err)
		}
	}
	assertColumnExists(t, store.pool, "runs", "agent_request_key")
	assertConstraintExists(t, store.pool, "runs_agent_request_key_mode_check")
	assertIndexExists(t, store.pool, "runs_agent_request_key_unique_idx")
	for _, column := range []string{
		"retry_of_run_id", "retry_key", "input_redacted_paths", "cancel_requested_at", "heartbeat_at",
	} {
		assertColumnExists(t, store.pool, "runs", column)
	}
	assertConstraintExists(t, store.pool, "runs_status_check")
	assertConstraintExists(t, store.pool, "runs_retry_workflow_fk")
	assertIndexExists(t, store.pool, "runs_retry_key_unique_idx")
	assertIndexExists(t, store.pool, "runs_active_heartbeat_idx")
	var inputPaths []string
	if err := store.pool.QueryRow(ctx, "SELECT input_redacted_paths FROM runs WHERE id=$1", historicalRunID).Scan(&inputPaths); err != nil {
		t.Fatal(err)
	}
	if len(inputPaths) != 0 {
		t.Fatalf("historical input_redacted_paths=%v, want empty", inputPaths)
	}
	if _, err := store.pool.Exec(ctx, "UPDATE runs SET status='cancelling' WHERE id=$1", historicalRunID); err != nil {
		t.Fatalf("cancelling should be accepted: %v", err)
	}
	if _, err := store.pool.Exec(ctx, "UPDATE runs SET status='unknown' WHERE id=$1", historicalRunID); err == nil {
		t.Fatal("unknown status should be rejected")
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO runs(
		id,workflow_id,draft_revision,graph_snapshot,mode,status,input,started_at,retry_of_run_id,retry_key
	) VALUES('00000000-0000-0000-0000-000000000202',$1,1,'{}'::jsonb,'test','running','{}'::jsonb,now(),$2,'00000000-0000-0000-0000-000000000301')`, otherWorkflowID, historicalRunID); err == nil {
		t.Fatal("cross-workflow retry should be rejected")
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
		assertIndexExists(t, store.pool, indexName)
	}
}

func assertColumnExists(t *testing.T, pool interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, table, column string) {
	t.Helper()
	var exists bool
	err := pool.QueryRow(context.Background(), `SELECT EXISTS(
		SELECT 1 FROM information_schema.columns
		WHERE table_schema=current_schema() AND table_name=$1 AND column_name=$2
	)`, table, column).Scan(&exists)
	if err != nil || !exists {
		t.Fatalf("column %s.%s exists=%v err=%v", table, column, exists, err)
	}
}

func assertConstraintExists(t *testing.T, pool interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, name string) {
	t.Helper()
	var exists bool
	err := pool.QueryRow(context.Background(), "SELECT EXISTS(SELECT 1 FROM pg_constraint WHERE conname=$1)", name).Scan(&exists)
	if err != nil || !exists {
		t.Fatalf("constraint %s exists=%v err=%v", name, exists, err)
	}
}

func assertIndexExists(t *testing.T, pool interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, name string) {
	t.Helper()
	var index *string
	err := pool.QueryRow(context.Background(), "SELECT to_regclass($1)::text", name).Scan(&index)
	if err != nil || index == nil || *index != name {
		t.Fatalf("index %s=%v err=%v", name, index, err)
	}
}
