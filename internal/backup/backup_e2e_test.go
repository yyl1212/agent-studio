package backup

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/yyl1212/agent-studio/internal/database"
)

var backupE2ESchemaName = regexp.MustCompile(`^backup_e2e_[0-9a-f]{24}$`)

// TestBackupRestoreE2E catches a regression where a valid complete instance
// archive either cannot be restored or loses a domain field during recovery.
func TestBackupRestoreE2E(t *testing.T) {
	if os.Getenv("BACKUP_E2E") != "1" {
		t.Skip("BACKUP_E2E is not enabled")
	}
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Fatal("TEST_DATABASE_URL is required")
	}

	source, target := openE2ESchemas(t, databaseURL)
	seedCompleteRestoreFixture(t, source)
	plaintext := []byte(`{"secret":"e2e"}`)
	ciphertext := sealE2EPayload(t, os.Getenv("RUN_PAYLOAD_ENCRYPTION_KEY"), plaintext)
	if _, err := source.Exec(context.Background(), `UPDATE run_payloads SET ciphertext=$1 WHERE run_id=$2 AND sequence=0 AND kind='run_input'`, ciphertext, backupRun1); err != nil {
		t.Fatal(err)
	}
	archivePath := filepath.Join(t.TempDir(), "instance.asbak")

	created, err := Create(context.Background(), source, CreateOptions{Output: archivePath, RuntimeVersion: "0.5.0-e2e"})
	if err != nil {
		t.Fatal(err)
	}
	inspected, err := Inspect(context.Background(), archivePath)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := DryRun(context.Background(), target, archivePath)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := Restore(context.Background(), target, archivePath)
	if err != nil {
		t.Fatal(err)
	}

	assertSummariesAgree(t, created, inspected, plan.Archive, restored.Summary)
	assertDomainSnapshotsEqual(t, source, target)
	var restoredCiphertext []byte
	if err := target.QueryRow(context.Background(), `SELECT ciphertext FROM run_payloads WHERE run_id=$1 AND sequence=0 AND kind='run_input'`, backupRun1).Scan(&restoredCiphertext); err != nil {
		t.Fatal(err)
	}
	if got := openE2EPayload(t, os.Getenv("RUN_PAYLOAD_ENCRYPTION_KEY"), restoredCiphertext); !reflect.DeepEqual(got, plaintext) {
		t.Fatalf("restored plaintext=%q want=%q", got, plaintext)
	}
}

func openE2ESchemas(t *testing.T, databaseURL string) (*pgxpool.Pool, *pgxpool.Pool) {
	t.Helper()
	backupDatabaseMutex.Lock()
	admin, err := database.OpenPool(context.Background(), databaseURL)
	if err != nil {
		backupDatabaseMutex.Unlock()
		t.Fatal(err)
	}

	sourceSchema := newBackupE2ESchemaName(t)
	targetSchema := newBackupE2ESchemaName(t)
	var source, target *pgxpool.Pool
	t.Cleanup(func() {
		if source != nil {
			source.Close()
		}
		if target != nil {
			target.Close()
		}
		cleanupBackupE2ESchema(t, admin, targetSchema)
		cleanupBackupE2ESchema(t, admin, sourceSchema)
		admin.Close()
		backupDatabaseMutex.Unlock()
	})
	for _, schema := range []string{sourceSchema, targetSchema} {
		if _, err := admin.Exec(context.Background(), "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
			t.Fatal(err)
		}
	}

	openSchema := func(schema string) *pgxpool.Pool {
		parsed, err := url.Parse(databaseURL)
		if err != nil {
			t.Fatal(err)
		}
		query := parsed.Query()
		query.Set("search_path", schema)
		parsed.RawQuery = query.Encode()
		pool, err := database.OpenPool(context.Background(), parsed.String())
		if err != nil {
			t.Fatal(err)
		}
		return pool
	}
	source = openSchema(sourceSchema)
	target = openSchema(targetSchema)
	if err := database.Migrate(context.Background(), source); err != nil {
		t.Fatal(err)
	}
	return source, target
}

func newBackupE2ESchemaName(t *testing.T) string {
	t.Helper()
	bytes := make([]byte, 12)
	if _, err := rand.Read(bytes); err != nil {
		t.Fatal(err)
	}
	return "backup_e2e_" + hex.EncodeToString(bytes)
}

func cleanupBackupE2ESchema(t *testing.T, admin *pgxpool.Pool, schema string) {
	t.Helper()
	if !backupE2ESchemaName.MatchString(schema) {
		t.Errorf("refusing to clean unexpected E2E schema %q", schema)
		return
	}
	if _, err := admin.Exec(context.Background(), "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE"); err != nil {
		t.Errorf("drop E2E schema %q: %v", schema, err)
	}
}

func assertSummariesAgree(t *testing.T, summaries ...Summary) {
	t.Helper()
	for _, summary := range summaries[1:] {
		if !reflect.DeepEqual(summaries[0], summary) {
			t.Fatalf("backup summaries disagree: first=%+v other=%+v", summaries[0], summary)
		}
	}
}

func assertDomainSnapshotsEqual(t *testing.T, source, target *pgxpool.Pool) {
	t.Helper()
	for table, query := range backupE2EDomainSnapshotQueries {
		if got, want := backupE2EDomainSnapshot(t, target, query), backupE2EDomainSnapshot(t, source, query); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s domain snapshot differs: restored=%v source=%v", table, got, want)
		}
	}
}

var backupE2EDomainSnapshotQueries = map[TableName]string{
	TableWorkflows:                `SELECT row_to_json(snapshot)::text FROM (SELECT * FROM workflows ORDER BY id) snapshot`,
	TableWorkflowVersions:         `SELECT row_to_json(snapshot)::text FROM (SELECT * FROM workflow_versions ORDER BY workflow_id, version, id) snapshot`,
	TableRuns:                     `SELECT row_to_json(snapshot)::text FROM (SELECT id,workflow_id,workflow_version_id,draft_revision,graph_snapshot,mode,status,input,output,error,started_at,ended_at,source_run_id,source_node_id,retry_of_run_id,retry_key,input_redacted_paths,cancel_requested_at,heartbeat_at,agent_request_key,execution_protocol,recovery_reason,recovery_requested_at FROM runs ORDER BY id) snapshot`,
	TableNodeRuns:                 `SELECT row_to_json(snapshot)::text FROM (SELECT * FROM node_runs ORDER BY run_id, id) snapshot`,
	TableRunEvents:                `SELECT row_to_json(snapshot)::text FROM (SELECT * FROM run_events ORDER BY run_id, sequence) snapshot`,
	TableRunPayloads:              `SELECT row_to_json(snapshot)::text FROM (SELECT * FROM run_payloads ORDER BY run_id, sequence, kind) snapshot`,
	TableWorkflowDraftCheckpoints: `SELECT row_to_json(snapshot)::text FROM (SELECT * FROM workflow_draft_checkpoints ORDER BY workflow_id, source_revision) snapshot`,
}

func sealE2EPayload(t *testing.T, encodedKey string, plaintext []byte) []byte {
	t.Helper()
	aead := e2eAEAD(t, encodedKey)
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		t.Fatal(err)
	}
	authenticated := aead.Seal(nil, nonce, plaintext, e2ePayloadAAD())
	return append(append([]byte{1}, nonce...), authenticated...)
}

func openE2EPayload(t *testing.T, encodedKey string, envelope []byte) []byte {
	t.Helper()
	aead := e2eAEAD(t, encodedKey)
	if len(envelope) < 1+aead.NonceSize()+aead.Overhead() || envelope[0] != 1 {
		t.Fatal("invalid encrypted fixture envelope")
	}
	plaintext, err := aead.Open(nil, envelope[1:1+aead.NonceSize()], envelope[1+aead.NonceSize():], e2ePayloadAAD())
	if err != nil {
		t.Fatal(err)
	}
	return plaintext
}

func e2eAEAD(t *testing.T, encodedKey string) cipher.AEAD {
	t.Helper()
	key, err := base64.StdEncoding.DecodeString(encodedKey)
	if err != nil || len(key) != 32 {
		t.Fatal("RUN_PAYLOAD_ENCRYPTION_KEY must contain 32 Base64 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	return aead
}

func e2ePayloadAAD() []byte {
	values := [][]byte{[]byte(backupRun1), make([]byte, 8), []byte("run_input"), []byte{}, make([]byte, 8), make([]byte, 2)}
	binary.BigEndian.PutUint16(values[5], 1)
	var result []byte
	for _, value := range values {
		var size [4]byte
		binary.BigEndian.PutUint32(size[:], uint32(len(value)))
		result = append(result, size[:]...)
		result = append(result, value...)
	}
	return result
}

func backupE2EDomainSnapshot(t *testing.T, pool *pgxpool.Pool, query string) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var snapshot []string
	for rows.Next() {
		var record string
		if err := rows.Scan(&record); err != nil {
			t.Fatal(err)
		}
		snapshot = append(snapshot, record)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return snapshot
}
