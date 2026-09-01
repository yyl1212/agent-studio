package database

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	databaseTestMutex sync.Mutex
	databaseTestID    atomic.Uint64
)

func TestMigrationVersionsAndEmptyDatabase(t *testing.T) {
	pool := openIsolatedPool(t)
	ctx := context.Background()

	gotCurrent, err := CurrentVersion(ctx, pool)
	if err != nil || gotCurrent != 0 {
		t.Fatalf("CurrentVersion() = %d, %v; want 0, nil", gotCurrent, err)
	}
	gotLatest, err := LatestVersion()
	if err != nil || gotLatest != 7 {
		t.Fatalf("LatestVersion() = %d, %v; want 7, nil", gotLatest, err)
	}
	if err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	gotCurrent, err = CurrentVersion(ctx, pool)
	if err != nil || gotCurrent != gotLatest {
		t.Fatalf("CurrentVersion() = %d, %v; want %d, nil", gotCurrent, err, gotLatest)
	}
}

func TestCurrentVersionRejectsMigrationGap(t *testing.T) {
	pool := openIsolatedPool(t)
	ctx := context.Background()
	if err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "DELETE FROM schema_migrations WHERE version=5"); err != nil {
		t.Fatal(err)
	}
	if _, err := CurrentVersion(ctx, pool); !errors.Is(err, ErrSchemaIncomplete) {
		t.Fatalf("CurrentVersion() error=%v; want ErrSchemaIncomplete", err)
	}
}

func TestValidateCurrentSchemaChecksRequiredColumns(t *testing.T) {
	tests := []struct {
		name   string
		damage string
	}{
		{name: "existing run column", damage: "ALTER TABLE runs DROP COLUMN agent_request_key CASCADE"},
		{name: "durable run column", damage: "ALTER TABLE runs DROP COLUMN execution_protocol CASCADE"},
		{name: "private payload table", damage: "DROP TABLE run_payloads"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool := openIsolatedPool(t)
			ctx := context.Background()
			if err := Migrate(ctx, pool); err != nil {
				t.Fatal(err)
			}
			if err := ValidateCurrentSchema(ctx, pool); err != nil {
				t.Fatalf("valid schema error=%v", err)
			}
			if _, err := pool.Exec(ctx, tt.damage); err != nil {
				t.Fatal(err)
			}
			if err := ValidateCurrentSchema(ctx, pool); !errors.Is(err, ErrSchemaIncomplete) {
				t.Fatalf("damaged schema error=%v; want ErrSchemaIncomplete", err)
			}
		})
	}
}

func TestMigrateUpgradesPreviousSchemaWithVersionGovernance(t *testing.T) {
	pool := openIsolatedPool(t)
	ctx := context.Background()

	if _, err := pool.Exec(ctx, `CREATE TABLE schema_migrations (
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
	if len(files) != 7 {
		t.Fatalf("migration files = %d; want 7", len(files))
	}
	for _, migration := range files[:2] {
		if err := applyMigration(ctx, pool, migration); err != nil {
			t.Fatal(err)
		}
	}
	var beforeColumn *string
	if err := pool.QueryRow(ctx, `SELECT data_type FROM information_schema.columns
		WHERE table_schema=current_schema() AND table_name='workflows' AND column_name='archived_at'`).Scan(&beforeColumn); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal(err)
	}
	if beforeColumn != nil {
		t.Fatalf("archived_at existed before management migration: %q", *beforeColumn)
	}
	if err := applyMigration(ctx, pool, files[2]); err != nil {
		t.Fatal(err)
	}

	const workflowID = "00000000-0000-0000-0000-000000000101"
	const otherWorkflowID = "00000000-0000-0000-0000-000000000102"
	const historicalRunID = "00000000-0000-0000-0000-000000000201"
	const historicalVersionID = "00000000-0000-0000-0000-000000000401"
	if _, err := pool.Exec(ctx, `INSERT INTO workflows(id,name,slug,draft_graph)
		VALUES($1,'Historical','historical','{}'::jsonb),($2,'Other','other','{}'::jsonb)`, workflowID, otherWorkflowID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO runs(
		id,workflow_id,draft_revision,graph_snapshot,mode,status,input,started_at
	) VALUES($1,$2,1,'{}'::jsonb,'test','running','{}'::jsonb,now())`, historicalRunID, workflowID); err != nil {
		t.Fatal(err)
	}
	if err := applyMigration(ctx, pool, files[3]); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO workflow_versions(id,workflow_id,version,graph,input_schema)
		VALUES($1,$2,1,'{}'::jsonb,'{"type":"object"}'::jsonb)`, historicalVersionID, workflowID); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}

	var workflowPresentation, versionPresentation []byte
	if err := pool.QueryRow(ctx, "SELECT agent_presentation FROM workflows WHERE id=$1", workflowID).Scan(&workflowPresentation); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "SELECT agent_presentation FROM workflow_versions WHERE id=$1", historicalVersionID).Scan(&versionPresentation); err != nil {
		t.Fatal(err)
	}
	for _, raw := range [][]byte{workflowPresentation, versionPresentation} {
		var got map[string]any
		if err := json.Unmarshal(raw, &got); err != nil || got["title"] != "Historical" || got["description"] != "" || got["accent"] != "indigo" || got["submitLabel"] != "运行 Agent" || got["resultMode"] != "auto" {
			t.Fatalf("presentation=%s error=%v", raw, err)
		}
	}
	assertColumnExists(t, pool, "runs", "agent_request_key")
	assertConstraintExists(t, pool, "runs_agent_request_key_mode_check")
	assertIndexExists(t, pool, "runs_agent_request_key_unique_idx")
	for _, column := range []string{"retry_of_run_id", "retry_key", "input_redacted_paths", "cancel_requested_at", "heartbeat_at"} {
		assertColumnExists(t, pool, "runs", column)
	}
	assertConstraintExists(t, pool, "runs_status_check")
	assertConstraintExists(t, pool, "runs_retry_workflow_fk")
	assertIndexExists(t, pool, "runs_retry_key_unique_idx")
	assertIndexExists(t, pool, "runs_active_heartbeat_idx")
	assertTableExists(t, pool, "workflow_draft_checkpoints")
	for _, column := range []string{"workflow_id", "source_revision", "restored_revision", "graph", "agent_presentation", "restored_from_version_id", "created_at"} {
		assertColumnExists(t, pool, "workflow_draft_checkpoints", column)
	}
	assertConstraintExists(t, pool, "workflow_draft_checkpoints_version_fk")

	var inputPaths []string
	if err := pool.QueryRow(ctx, "SELECT input_redacted_paths FROM runs WHERE id=$1", historicalRunID).Scan(&inputPaths); err != nil {
		t.Fatal(err)
	}
	if len(inputPaths) != 0 {
		t.Fatalf("historical input_redacted_paths = %v; want empty", inputPaths)
	}
	if _, err := pool.Exec(ctx, `UPDATE runs
		SET status='cancelling',recovery_reason=NULL,recovery_requested_at=NULL
		WHERE id=$1`, historicalRunID); err != nil {
		t.Fatalf("cancelling should be accepted: %v", err)
	}
	if _, err := pool.Exec(ctx, "UPDATE runs SET status='unknown' WHERE id=$1", historicalRunID); err == nil {
		t.Fatal("unknown status should be rejected")
	}
	if _, err := pool.Exec(ctx, `INSERT INTO runs(
		id,workflow_id,draft_revision,graph_snapshot,mode,status,input,started_at,retry_of_run_id,retry_key
	) VALUES('00000000-0000-0000-0000-000000000202',$1,1,'{}'::jsonb,'test','running','{}'::jsonb,now(),$2,'00000000-0000-0000-0000-000000000301')`, otherWorkflowID, historicalRunID); err == nil {
		t.Fatal("cross-workflow retry should be rejected")
	}
	var archivedType string
	if err := pool.QueryRow(ctx, `SELECT data_type FROM information_schema.columns
		WHERE table_schema=current_schema() AND table_name='workflows' AND column_name='archived_at'`).Scan(&archivedType); err != nil {
		t.Fatal(err)
	}
	if archivedType != "timestamp with time zone" {
		t.Fatalf("archived_at type = %q", archivedType)
	}
	for _, indexName := range []string{
		"workflows_management_idx", "runs_started_at_id_idx", "runs_workflow_started_at_id_idx",
		"runs_status_started_at_id_idx", "runs_mode_started_at_id_idx",
		"runs_workflow_status_started_at_id_idx", "runs_workflow_mode_started_at_id_idx",
	} {
		assertIndexExists(t, pool, indexName)
	}
}

func openIsolatedPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	databaseTestMutex.Lock()
	admin, err := OpenPool(context.Background(), databaseURL)
	if err != nil {
		databaseTestMutex.Unlock()
		t.Fatal(err)
	}
	schema := fmt.Sprintf("database_test_%d", databaseTestID.Add(1))
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(context.Background(), "CREATE SCHEMA "+identifier); err != nil {
		admin.Close()
		databaseTestMutex.Unlock()
		t.Fatal(err)
	}
	var pool *pgxpool.Pool
	t.Cleanup(func() {
		if pool != nil {
			pool.Close()
		}
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+identifier+" CASCADE")
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
	pool, err = OpenPool(context.Background(), parsedURL.String())
	if err != nil {
		t.Fatal(err)
	}
	return pool
}

func assertTableExists(t *testing.T, query RowQuerier, name string) {
	t.Helper()
	var table *string
	err := query.QueryRow(context.Background(), "SELECT to_regclass($1)::text", name).Scan(&table)
	if err != nil || table == nil || *table != name {
		t.Fatalf("table %s = %v, %v", name, table, err)
	}
}

func assertColumnExists(t *testing.T, query RowQuerier, table, column string) {
	t.Helper()
	var exists bool
	err := query.QueryRow(context.Background(), `SELECT EXISTS(
		SELECT 1 FROM information_schema.columns
		WHERE table_schema=current_schema() AND table_name=$1 AND column_name=$2
	)`, table, column).Scan(&exists)
	if err != nil || !exists {
		t.Fatalf("column %s.%s exists = %v, %v", table, column, exists, err)
	}
}

func assertConstraintExists(t *testing.T, query RowQuerier, name string) {
	t.Helper()
	var exists bool
	err := query.QueryRow(context.Background(), "SELECT EXISTS(SELECT 1 FROM pg_constraint WHERE conname=$1)", name).Scan(&exists)
	if err != nil || !exists {
		t.Fatalf("constraint %s exists = %v, %v", name, exists, err)
	}
}

func assertIndexExists(t *testing.T, query RowQuerier, name string) {
	t.Helper()
	var index *string
	err := query.QueryRow(context.Background(), "SELECT to_regclass($1)::text", name).Scan(&index)
	if err != nil || index == nil || *index != name {
		t.Fatalf("index %s = %v, %v", name, index, err)
	}
}
