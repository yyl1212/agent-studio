package backup

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/yyl1212/agent-studio/internal/database"
)

var (
	backupDatabaseMutex sync.Mutex
	backupDatabaseID    atomic.Uint64
)

func TestCreateAndInspectPostgresSnapshot(t *testing.T) {
	pool := openBackupPool(t)
	insertBackupFixture(t, pool)
	output := filepath.Join(t.TempDir(), "snapshot.asbak")

	summary, err := Create(context.Background(), pool, CreateOptions{Output: output, RuntimeVersion: "0.5.0-test"})
	if err != nil {
		t.Fatal(err)
	}
	inspected, err := Inspect(context.Background(), output)
	if err != nil {
		t.Fatal(err)
	}
	if summary.DatasetDigest != inspected.DatasetDigest || inspected.MigrationVersion != 6 {
		t.Fatalf("created=%+v inspected=%+v", summary, inspected)
	}
	wantCounts := map[TableName]uint64{
		TableWorkflows: 1, TableWorkflowVersions: 1, TableRuns: 3,
		TableNodeRuns: 1, TableRunEvents: 1, TableWorkflowDraftCheckpoints: 1,
	}
	for _, table := range inspected.Tables {
		if table.Records != wantCounts[table.Name] {
			t.Fatalf("table %s records=%d", table.Name, table.Records)
		}
	}

	archive, err := OpenArchive(context.Background(), output)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	var runIDs []string
	if err := archive.ReadTable(context.Background(), TableRuns, func(raw json.RawMessage) error {
		record, err := decodeRunRecord(raw)
		if err == nil {
			runIDs = append(runIDs, record.ID)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	wantRunIDs := []string{backupRun1, backupRun2, backupRun3}
	if fmt.Sprint(runIDs) != fmt.Sprint(wantRunIDs) {
		t.Fatalf("run order=%v want=%v", runIDs, wantRunIDs)
	}
}

func TestCreateUsesOneReadOnlyRepeatableReadSnapshot(t *testing.T) {
	pool := openBackupPool(t)
	insertMinimalWorkflow(t, pool, backupWorkflow1, "first")
	output := filepath.Join(t.TempDir(), "snapshot.asbak")
	started := make(chan struct{})
	continueExport := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		_, err := createWithHooks(context.Background(), pool, CreateOptions{Output: output, RuntimeVersion: "0.5.0-test"}, createHooks{
			afterSnapshot: func() { close(started); <-continueExport },
		})
		result <- err
	}()
	<-started
	insertMinimalWorkflow(t, pool, backupWorkflow2, "second")
	close(continueExport)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	summary, err := Inspect(context.Background(), output)
	if err != nil {
		t.Fatal(err)
	}
	if got := tableRecordCount(summary, TableWorkflows); got != 1 {
		t.Fatalf("workflows=%d", got)
	}
}

func TestCreateMaintenanceAndSchemaGuards(t *testing.T) {
	t.Run("shared runtime lease permits create", func(t *testing.T) {
		pool := openBackupPool(t)
		lease, err := database.TryShared(context.Background(), pool)
		if err != nil {
			t.Fatal(err)
		}
		defer lease.Release(context.Background())
		_, err = Create(context.Background(), pool, CreateOptions{Output: filepath.Join(t.TempDir(), "shared.asbak"), RuntimeVersion: "test"})
		if err != nil {
			t.Fatal(err)
		}
	})

	t.Run("exclusive maintenance rejects create", func(t *testing.T) {
		pool := openBackupPool(t)
		lease, err := database.TryExclusive(context.Background(), pool)
		if err != nil {
			t.Fatal(err)
		}
		defer lease.Release(context.Background())
		_, err = Create(context.Background(), pool, CreateOptions{Output: filepath.Join(t.TempDir(), "exclusive.asbak"), RuntimeVersion: "test"})
		if CodeOf(err) != CodeCreateFailed {
			t.Fatalf("code=%q err=%v", CodeOf(err), err)
		}
	})

	t.Run("outdated schema rejects create", func(t *testing.T) {
		pool := openBackupPool(t)
		if _, err := pool.Exec(context.Background(), "DELETE FROM schema_migrations WHERE version=6"); err != nil {
			t.Fatal(err)
		}
		_, err := Create(context.Background(), pool, CreateOptions{Output: filepath.Join(t.TempDir(), "old.asbak"), RuntimeVersion: "test"})
		if CodeOf(err) != CodeSchemaNotCurrent {
			t.Fatalf("code=%q err=%v", CodeOf(err), err)
		}
	})
}

func TestOrderRunsRejectsMissingParentsAndCycles(t *testing.T) {
	now := time.Now().UTC()
	for _, test := range []struct {
		name string
		runs []runReference
	}{
		{name: "missing", runs: []runReference{{ID: backupRun1, SourceRunID: pointer(backupRun2), StartedAt: now}}},
		{name: "cycle", runs: []runReference{
			{ID: backupRun1, SourceRunID: pointer(backupRun2), StartedAt: now},
			{ID: backupRun2, RetryOfRunID: pointer(backupRun1), StartedAt: now},
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := orderRuns(test.runs); CodeOf(err) != CodeReferenceInvalid {
				t.Fatalf("code=%q err=%v", CodeOf(err), err)
			}
		})
	}
}

const (
	backupWorkflow1 = "00000000-0000-0000-0000-000000000101"
	backupWorkflow2 = "00000000-0000-0000-0000-000000000102"
	backupVersion1  = "00000000-0000-0000-0000-000000000201"
	backupRun1      = "00000000-0000-0000-0000-000000000301"
	backupRun2      = "00000000-0000-0000-0000-000000000302"
	backupRun3      = "00000000-0000-0000-0000-000000000303"
	backupNode1     = "00000000-0000-0000-0000-000000000401"
	backupRetryKey  = "00000000-0000-0000-0000-000000000501"
)

func openBackupPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	backupDatabaseMutex.Lock()
	admin, err := database.OpenPool(context.Background(), databaseURL)
	if err != nil {
		backupDatabaseMutex.Unlock()
		t.Fatal(err)
	}
	schema := fmt.Sprintf("backup_test_%d", backupDatabaseID.Add(1))
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(context.Background(), "CREATE SCHEMA "+identifier); err != nil {
		admin.Close()
		backupDatabaseMutex.Unlock()
		t.Fatal(err)
	}
	var pool *pgxpool.Pool
	t.Cleanup(func() {
		if pool != nil {
			pool.Close()
		}
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+identifier+" CASCADE")
		admin.Close()
		backupDatabaseMutex.Unlock()
	})
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("options", "-csearch_path="+schema)
	parsed.RawQuery = query.Encode()
	pool, err = database.OpenPool(context.Background(), parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	return pool
}

func insertMinimalWorkflow(t *testing.T, pool *pgxpool.Pool, id, slug string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `INSERT INTO workflows(
		id,name,slug,description,draft_graph,draft_revision,agent_presentation
	) VALUES($1,$2,$3,'','{}'::jsonb,1,'{}'::jsonb)`, id, slug, slug); err != nil {
		t.Fatal(err)
	}
}

func insertBackupFixture(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 8, 29, 10, 0, 0, 0, time.UTC)
	insertMinimalWorkflow(t, pool, backupWorkflow1, "fixture")
	if _, err := pool.Exec(ctx, `INSERT INTO workflow_versions(
		id,workflow_id,version,graph,input_schema,agent_presentation,created_at
	) VALUES($1,$2,1,'{}'::jsonb,'{"type":"object"}'::jsonb,'{}'::jsonb,$3)`, backupVersion1, backupWorkflow1, now); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "UPDATE workflows SET published_version_id=$1 WHERE id=$2", backupVersion1, backupWorkflow1); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO runs(
		id,workflow_id,workflow_version_id,graph_snapshot,source_run_id,source_node_id,retry_of_run_id,retry_key,
		mode,status,input,input_redacted_paths,started_at
	) VALUES
		($1,$4,$5,NULL,NULL,NULL,NULL,NULL,'published','completed','{}'::jsonb,'{}'::text[],$7),
		($2,$4,NULL,'{}'::jsonb,$1,'start',NULL,NULL,'debug','completed','{}'::jsonb,'{}'::text[],$7 + interval '1 second'),
		($3,$4,$5,NULL,NULL,NULL,$1,$6,'published','completed','{}'::jsonb,'{}'::text[],$7 + interval '2 seconds')`,
		backupRun1, backupRun2, backupRun3, backupWorkflow1, backupVersion1, backupRetryKey, now); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO node_runs(id,run_id,node_id,node_type,status,started_at,ended_at)
		VALUES($1,$2,'start','start','completed',$3,$3)`, backupNode1, backupRun2, now); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO run_events(
		run_id,sequence,type,active_ports,input_redacted_paths,output_redacted_paths,data_bytes,timestamp
	) VALUES($1,1,'run.started','{}','{}','{}',0,$2)`, backupRun2, now); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO workflow_draft_checkpoints(
		workflow_id,source_revision,restored_revision,graph,agent_presentation,restored_from_version_id,created_at
	) VALUES($1,1,2,'{}','{}',$2,$3)`, backupWorkflow1, backupVersion1, now); err != nil {
		t.Fatal(err)
	}
}

func tableRecordCount(summary Summary, name TableName) uint64 {
	for _, table := range summary.Tables {
		if table.Name == name {
			return table.Records
		}
	}
	return 0
}
