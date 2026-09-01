package postgres

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/yyl1212/agent-studio/apps/api/migrations"
)

func TestOpenRedactsInvalidDatabaseURL(t *testing.T) {
	const secret = "sentinel-password"
	_, err := Open(context.Background(), "postgres://agent:"+secret+"@[")
	if err == nil {
		t.Fatal("Open() error = nil")
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "postgres://") {
		t.Fatalf("Open() exposed database URL: %v", err)
	}
}

func TestReadyReportsVersionForEmptySchema(t *testing.T) {
	store := openUnmigratedTestStore(t)
	err := store.Ready(context.Background())
	if err == nil || err.Error() != "database migration version 0, want 7" {
		t.Fatalf("Ready() error = %v", err)
	}
}

func TestMigrateVersionSixRunsToDurableProtocol(t *testing.T) {
	store := openUnmigratedTestStore(t)
	applyTestMigrationsThroughVersionSix(t, store)

	const workflowID = "10000000-0000-4000-8000-000000000001"
	if _, err := store.pool.Exec(context.Background(), `
		INSERT INTO workflows(id,name,slug,description,draft_graph,draft_revision,agent_presentation)
		VALUES($1,'durable migration','durable-migration','{}','{}',1,'{}')`, workflowID); err != nil {
		t.Fatal(err)
	}
	runs := []struct {
		id     string
		status string
	}{
		{id: "20000000-0000-4000-8000-000000000001", status: "running"},
		{id: "20000000-0000-4000-8000-000000000002", status: "cancelling"},
		{id: "20000000-0000-4000-8000-000000000003", status: "completed"},
	}
	for _, run := range runs {
		if _, err := store.pool.Exec(context.Background(), `
			INSERT INTO runs(id,workflow_id,draft_revision,graph_snapshot,mode,status,input,started_at)
			VALUES($1,$2,1,'{}','test',$3,'{}',clock_timestamp())`, run.id, workflowID, run.status); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.pool.Exec(context.Background(), `
		INSERT INTO run_events(run_id,sequence,type,node_id,status,data_bytes,timestamp)
		VALUES($1,1,'node.started','node-1','running',0,clock_timestamp())`, runs[0].id); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(context.Background(), `
		INSERT INTO node_runs(id,run_id,node_id,node_type,status)
		VALUES('30000000-0000-4000-8000-000000000001',$1,'node-1','echo','running')`, runs[0].id); err != nil {
		t.Fatal(err)
	}

	if err := store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}

	want := map[string]struct {
		status string
		reason *string
	}{
		runs[0].id: {status: "recovery_required", reason: stringPointer("legacy_active_run")},
		runs[1].id: {status: "cancelling"},
		runs[2].id: {status: "completed"},
	}
	rows, err := store.pool.Query(context.Background(), `
		SELECT id::text,status,recovery_reason,execution_protocol
		FROM runs ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, status string
		var reason *string
		var protocol int16
		if err := rows.Scan(&id, &status, &reason, &protocol); err != nil {
			t.Fatal(err)
		}
		expected, ok := want[id]
		if !ok {
			t.Fatalf("unexpected migrated run %s", id)
		}
		if status != expected.status || !equalOptionalString(reason, expected.reason) || protocol != 0 {
			t.Fatalf("migrated run %s = status %q reason %v protocol %d; want status %q reason %v protocol 0", id, status, reason, protocol, expected.status, expected.reason)
		}
		delete(want, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(want) != 0 {
		t.Fatalf("missing migrated runs: %v", want)
	}

	var eventAttempt *int
	if err := store.pool.QueryRow(context.Background(), `SELECT node_attempt FROM run_events WHERE run_id=$1 AND sequence=1`, runs[0].id).Scan(&eventAttempt); err != nil {
		t.Fatal(err)
	}
	if eventAttempt == nil || *eventAttempt != 1 {
		t.Fatalf("event node attempt = %v; want 1", eventAttempt)
	}
	var nodeAttempt int
	if err := store.pool.QueryRow(context.Background(), `SELECT attempt FROM node_runs WHERE run_id=$1 AND node_id='node-1'`, runs[0].id).Scan(&nodeAttempt); err != nil {
		t.Fatal(err)
	}
	if nodeAttempt != 1 {
		t.Fatalf("node run attempt = %d; want 1", nodeAttempt)
	}
}

func applyTestMigrationsThroughVersionSix(t *testing.T, store *Store) {
	t.Helper()
	ctx := context.Background()
	if _, err := store.pool.Exec(ctx, `CREATE TABLE schema_migrations (
		version bigint PRIMARY KEY,
		name text NOT NULL,
		applied_at timestamptz NOT NULL DEFAULT now()
	)`); err != nil {
		t.Fatal(err)
	}
	names := []string{
		"000001_initial.sql",
		"000002_run_debug.sql",
		"000003_management.sql",
		"000004_run_recovery.sql",
		"000005_agent_page.sql",
		"000006_workflow_version_governance.sql",
	}
	for index, name := range names {
		contents, err := migrations.Files.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		tx, err := store.pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, string(contents), pgx.QueryExecModeSimpleProtocol); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatalf("apply %s: %v", name, err)
		}
		if _, err := tx.Exec(ctx, "INSERT INTO schema_migrations(version,name) VALUES($1,$2)", index+1, name); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatal(err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
}

func stringPointer(value string) *string {
	return &value
}

func equalOptionalString(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func openUnmigratedTestStore(t *testing.T) *Store {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	databaseTestMutex.Lock()
	admin, err := Open(context.Background(), databaseURL)
	if err != nil {
		databaseTestMutex.Unlock()
		t.Fatal(err)
	}
	schema := fmt.Sprintf("store_ready_%d", fixtureSequence.Add(1))
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.pool.Exec(context.Background(), "CREATE SCHEMA "+identifier); err != nil {
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
	store, err = Open(context.Background(), parsedURL.String())
	if err != nil {
		t.Fatal(err)
	}
	return store
}
