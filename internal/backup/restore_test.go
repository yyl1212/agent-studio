package backup

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/yyl1212/agent-studio/internal/database"
)

func TestRestoreRoundTripPreservesAllDomainData(t *testing.T) {
	source, target := openRestorePools(t, 0)
	seedCompleteRestoreFixture(t, source)
	archivePath, created := createRestoreArchive(t, source)

	result, err := Restore(context.Background(), target, archivePath)
	if err != nil {
		t.Fatal(err)
	}
	if result.MigrationVersion != 7 || result.Summary.DatasetDigest != created.DatasetDigest || result.Tables[TableRuns] != 3 {
		t.Fatalf("result=%+v", result)
	}
	assertRestoredArchiveEqual(t, target, created)
	assertRestoredSpecialValues(t, target)
	assertRestoredDurableState(t, target)
}

func TestRestoreV1Alpha1PreservesCancellationAndRequiresRecoveryForRunning(t *testing.T) {
	data := newReferenceFixtureData()
	data.runs[0].Status = "running"
	data.runs[1].Status = "cancelling"
	path := filepath.Join(t.TempDir(), "legacy-active.asbak")
	if _, err := WriteArchive(context.Background(), path, manifestFixture(time.Now().UTC()), referenceFixtureWriters(t, data)); err != nil {
		t.Fatal(err)
	}
	target := openUnmigratedTarget(t)
	if _, err := Restore(context.Background(), target, path); err != nil {
		t.Fatal(err)
	}
	var runningStatus, reason string
	var requestedAt time.Time
	if err := target.QueryRow(context.Background(), `SELECT status,recovery_reason,recovery_requested_at FROM runs WHERE id=$1`, data.runs[0].ID).
		Scan(&runningStatus, &reason, &requestedAt); err != nil {
		t.Fatal(err)
	}
	var cancellingStatus string
	if err := target.QueryRow(context.Background(), `SELECT status FROM runs WHERE id=$1`, data.runs[1].ID).Scan(&cancellingStatus); err != nil {
		t.Fatal(err)
	}
	if runningStatus != "recovery_required" || reason != "legacy_active_run" || requestedAt.IsZero() || cancellingStatus != "cancelling" {
		t.Fatalf("running=%s reason=%s requestedAt=%s cancelling=%s", runningStatus, reason, requestedAt, cancellingStatus)
	}
}

func TestRestoreAndDryRunPublicErrorsRemoveUnsafeUnwrapCauses(t *testing.T) {
	const secret = "sentinel-connection-url"
	missingPath := filepath.Join(t.TempDir(), secret+".asbak")
	for _, test := range []struct {
		name string
		call func() error
	}{
		{name: "restore", call: func() error {
			_, err := Restore(context.Background(), nil, missingPath)
			return err
		}},
		{name: "dry run", call: func() error {
			_, err := DryRun(context.Background(), nil, missingPath)
			return err
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := test.call()
			if CodeOf(err) != CodeArchiveInvalid {
				t.Fatalf("code=%q err=%v", CodeOf(err), err)
			}
			assertErrorChainOmits(t, err, secret)
		})
	}
}

func TestPublicBackupErrorSanitizationPreservesCodesAndContextOnly(t *testing.T) {
	const secret = "postgres://user:password@database.example/private"
	unsafe := errors.New(secret)
	joined := errors.Join(
		Wrap(CodeArchiveInvalid, "close backup table", unsafe),
		Wrap(CodeRestoreFailed, "copy backup table", unsafe),
	)
	sanitized := sanitizePublicBackupError(joined)
	codes := backupCodesInChain(sanitized)
	if CodeOf(sanitized) != CodeArchiveInvalid || codes[CodeArchiveInvalid] != 1 || codes[CodeRestoreFailed] != 1 {
		t.Fatalf("primary=%q codes=%v err=%v", CodeOf(sanitized), codes, sanitized)
	}
	assertErrorChainOmits(t, sanitized, secret)

	for _, contextErr := range []error{context.Canceled, context.DeadlineExceeded} {
		if got := sanitizePublicBackupError(contextErr); got != contextErr {
			t.Fatalf("context error=%v got=%v", contextErr, got)
		}
	}

	restoreContext, cancel := context.WithCancelCause(context.Background())
	cancel(unsafe)
	err := normalizeRestoreContextError(context.Background(), restoreContext, unsafe)
	if CodeOf(err) != CodeRestoreFailed {
		t.Fatalf("code=%q err=%v", CodeOf(err), err)
	}
	assertErrorChainOmits(t, err, secret)
}

func TestRestoreUsesExclusiveLeaseSessionWithSingleConnection(t *testing.T) {
	source, target := openRestorePools(t, 1)
	seedCompleteRestoreFixture(t, source)
	archivePath, _ := createRestoreArchive(t, source)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if _, err := Restore(ctx, target, archivePath); err != nil {
		t.Fatal(err)
	}
	assertBusinessRows(t, target, 9)
}

func TestRestoreInjectedFailuresRollbackAllBusinessTables(t *testing.T) {
	for _, table := range TableOrder {
		t.Run(string(table), func(t *testing.T) {
			source, target := openRestorePools(t, 0)
			seedCompleteRestoreFixture(t, source)
			archivePath, _ := createRestoreArchive(t, source)
			sentinel := errors.New("injected restore failure")

			_, err := restoreWithHooks(context.Background(), target, archivePath, restoreHooks{
				afterTable: func(restored TableName) error {
					if restored == table {
						return sentinel
					}
					return nil
				},
			})
			if !errors.Is(err, sentinel) {
				t.Fatalf("err=%v", err)
			}
			assertBusinessRows(t, target, 0)
		})
	}
}

func TestRestoreConstraintFailureDuringRunsRollsBackEarlierTables(t *testing.T) {
	_, target := openRestorePools(t, 0)
	data := newReferenceFixtureData()
	duplicateRetry := data.runs[2]
	duplicateRetry.ID = "00000000-0000-0000-0000-000000000804"
	duplicateRetry.StartedAt = duplicateRetry.StartedAt.Add(time.Second)
	data.runs = append(data.runs, duplicateRetry)
	archivePath := filepath.Join(t.TempDir(), "duplicate-retry.asbak")
	if _, err := WriteArchive(context.Background(), archivePath, manifestFixture(time.Now().UTC()), referenceFixtureWriters(t, data)); err != nil {
		t.Fatal(err)
	}

	_, err := Restore(context.Background(), target, archivePath)
	if CodeOf(err) != CodeRestoreFailed {
		t.Fatalf("code=%q err=%v", CodeOf(err), err)
	}
	assertBusinessRows(t, target, 0)
}

func TestRestoreDatabaseFailureDoesNotExposeRecordBody(t *testing.T) {
	_, target := openRestorePools(t, 0)
	data := newReferenceFixtureData()
	const secret = "sentinel-record-body-must-not-leak"
	data.workflows[1].Name = secret
	data.workflows[1].Slug = data.workflows[0].Slug
	archivePath := filepath.Join(t.TempDir(), "duplicate-slug.asbak")
	if _, err := WriteArchive(context.Background(), archivePath, manifestFixture(time.Now().UTC()), referenceFixtureWriters(t, data)); err != nil {
		t.Fatal(err)
	}

	_, err := Restore(context.Background(), target, archivePath)
	if CodeOf(err) != CodeRestoreFailed || err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("code=%q err=%v", CodeOf(err), err)
	}
	assertBusinessRows(t, target, 0)
}

func TestRestoreCancellationRollsBackAllBusinessTables(t *testing.T) {
	source, target := openRestorePools(t, 0)
	seedCompleteRestoreFixture(t, source)
	archivePath, _ := createRestoreArchive(t, source)
	ctx, cancel := context.WithCancel(context.Background())

	_, err := restoreWithHooks(ctx, target, archivePath, restoreHooks{
		afterTable: func(table TableName) error {
			if table == TableWorkflows {
				cancel()
			}
			return nil
		},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
	assertBusinessRows(t, target, 0)
}

func TestRestoreRejectsTargetThatBecomesNonEmptyAfterPreflight(t *testing.T) {
	source, target := openRestorePools(t, 0)
	seedCompleteRestoreFixture(t, source)
	archivePath, _ := createRestoreArchive(t, source)
	if err := database.Migrate(context.Background(), target); err != nil {
		t.Fatal(err)
	}

	_, err := restoreWithHooks(context.Background(), target, archivePath, restoreHooks{
		afterPreflight: func() error {
			insertMinimalWorkflow(t, target, backupWorkflow2, "late-writer")
			return nil
		},
	})
	if CodeOf(err) != CodeTargetNotEmpty {
		t.Fatalf("code=%q err=%v", CodeOf(err), err)
	}
	assertBusinessRows(t, target, 1)
}

func TestRestorePathReplacementConsumesOriginalOpenArchive(t *testing.T) {
	source, target := openRestorePools(t, 0)
	seedCompleteRestoreFixture(t, source)
	archivePath, created := createRestoreArchive(t, source)
	replacement := referenceArchivePath(t, 6)

	_, err := restoreWithHooks(context.Background(), target, archivePath, restoreHooks{
		afterPreflight: func() error { return os.Rename(replacement, archivePath) },
	})
	if err != nil {
		t.Fatal(err)
	}
	assertRestoredArchiveEqual(t, target, created)
}

func TestRestoreSameInodeMutationFailsDigestAndRollsBack(t *testing.T) {
	source, target := openRestorePools(t, 0)
	seedCompleteRestoreFixture(t, source)
	archivePath, _ := createRestoreArchive(t, source)
	openedInfo, err := os.Stat(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	mutationOffset := compressedTableByteOffset(t, archivePath, TableWorkflows)

	_, err = restoreWithHooks(context.Background(), target, archivePath, restoreHooks{
		afterPreflight: func() error {
			file, openErr := os.OpenFile(archivePath, os.O_RDWR, 0)
			if openErr != nil {
				return openErr
			}
			defer file.Close()
			currentInfo, statErr := file.Stat()
			if statErr != nil {
				return statErr
			}
			if !os.SameFile(openedInfo, currentInfo) {
				return errors.New("archive inode changed")
			}
			var value [1]byte
			if _, readErr := file.ReadAt(value[:], mutationOffset); readErr != nil {
				return readErr
			}
			value[0] ^= 0xff
			_, writeErr := file.WriteAt(value[:], mutationOffset)
			return writeErr
		},
	})
	if CodeOf(err) != CodeChecksumMismatch {
		t.Fatalf("code=%q err=%v", CodeOf(err), err)
	}
	assertBusinessRows(t, target, 0)
}

func compressedTableByteOffset(t *testing.T, path string, table TableName) int64 {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	reader, err := zip.NewReader(file, info.Size())
	if err != nil {
		t.Fatal(err)
	}
	tableArchivePath, _ := tablePath(table)
	for _, entry := range reader.File {
		if entry.Name != tableArchivePath {
			continue
		}
		offset, err := entry.DataOffset()
		if err != nil {
			t.Fatal(err)
		}
		if entry.CompressedSize64 == 0 {
			t.Fatal("table compressed payload is empty")
		}
		return offset + int64(entry.CompressedSize64/2)
	}
	t.Fatalf("table entry %s not found", table)
	return 0
}

func TestRestoreLeaseConnectionLossReturnsSafeFailureAndRollsBack(t *testing.T) {
	source, target := openRestorePools(t, 0)
	seedCompleteRestoreFixture(t, source)
	archivePath, _ := createRestoreArchive(t, source)

	_, err := restoreWithHooks(context.Background(), target, archivePath, restoreHooks{
		afterTable: func(table TableName) error {
			if table != TableWorkflows {
				return nil
			}
			var applicationName string
			if queryErr := target.QueryRow(context.Background(), `SHOW application_name`).Scan(&applicationName); queryErr != nil {
				return queryErr
			}
			var backendPID int32
			if queryErr := target.QueryRow(context.Background(), `SELECT lock.pid FROM pg_locks lock
				JOIN pg_stat_activity activity ON activity.pid=lock.pid
				WHERE lock.locktype='advisory' AND lock.classid=0 AND lock.objid=918273645
				AND lock.granted AND lock.mode='ExclusiveLock' AND activity.application_name=$1`, applicationName).Scan(&backendPID); queryErr != nil {
				return queryErr
			}
			var terminated bool
			if queryErr := target.QueryRow(context.Background(), `SELECT pg_terminate_backend($1)`, backendPID).Scan(&terminated); queryErr != nil {
				return queryErr
			}
			if !terminated {
				return errors.New("maintenance backend was not terminated")
			}
			return nil
		},
	})
	if CodeOf(err) != CodeRestoreFailed || err == nil || strings.Contains(err.Error(), os.Getenv("TEST_DATABASE_URL")) {
		t.Fatalf("code=%q err=%v", CodeOf(err), err)
	}
	assertErrorChainOmits(t, err, "database maintenance connection closed")
	assertBusinessRows(t, target, 0)
}

func openRestorePools(t *testing.T, targetMaxConns int32) (*pgxpool.Pool, *pgxpool.Pool) {
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
	id := backupDatabaseID.Add(1)
	sourceSchema := fmt.Sprintf("restore_source_%d", id)
	targetSchema := fmt.Sprintf("restore_target_%d", id)
	for _, schema := range []string{sourceSchema, targetSchema} {
		if _, err := admin.Exec(context.Background(), "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
			admin.Close()
			backupDatabaseMutex.Unlock()
			t.Fatal(err)
		}
	}
	openSchema := func(schema string, maxConns int32) *pgxpool.Pool {
		parsed, err := url.Parse(databaseURL)
		if err != nil {
			t.Fatal(err)
		}
		query := parsed.Query()
		query.Set("options", "-csearch_path="+schema)
		query.Set("application_name", "agent_studio_"+schema)
		if maxConns > 0 {
			query.Set("pool_max_conns", fmt.Sprint(maxConns))
		}
		parsed.RawQuery = query.Encode()
		pool, err := database.OpenPool(context.Background(), parsed.String())
		if err != nil {
			t.Fatal(err)
		}
		return pool
	}
	source := openSchema(sourceSchema, 0)
	target := openSchema(targetSchema, targetMaxConns)
	if err := database.Migrate(context.Background(), source); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		source.Close()
		target.Close()
		for _, schema := range []string{sourceSchema, targetSchema} {
			_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		}
		admin.Close()
		backupDatabaseMutex.Unlock()
	})
	return source, target
}

func seedCompleteRestoreFixture(t *testing.T, source *pgxpool.Pool) {
	t.Helper()
	insertBackupFixture(t, source)
	ctx := context.Background()
	precise := time.Date(2026, 8, 29, 10, 0, 0, 123456000, time.UTC)
	if _, err := source.Exec(ctx, `UPDATE workflows SET
		description='round trip',draft_graph='{"nodes":[]}'::jsonb,draft_revision=7,
		created_at=$1::timestamptz,updated_at=$1::timestamptz + interval '1 microsecond',archived_at=$1::timestamptz + interval '2 microseconds',
		agent_presentation='{"title":"restore"}'::jsonb WHERE id=$2`, precise, backupWorkflow1); err != nil {
		t.Fatal(err)
	}
	if _, err := source.Exec(ctx, `UPDATE workflow_versions SET graph='{"nodes":[]}'::jsonb,
		input_schema='{"type":"object","required":[]}'::jsonb,created_at=$1::timestamptz + interval '3 microseconds',
		agent_presentation='{"title":"published"}'::jsonb WHERE id=$2`, precise, backupVersion1); err != nil {
		t.Fatal(err)
	}
	if _, err := source.Exec(ctx, `UPDATE runs SET status='running',input='{"secret":"x"}'::jsonb,
		output='null'::jsonb,input_redacted_paths=ARRAY['/secret'],started_at=$1::timestamptz + interval '4 microseconds',
		heartbeat_at=$1::timestamptz + interval '5 microseconds',execution_protocol=1,lease_owner='fixture-worker',
		lease_token=9,lease_expires_at=$1::timestamptz + interval '1 hour' WHERE id=$2`, precise, backupRun1); err != nil {
		t.Fatal(err)
	}
	if _, err := source.Exec(ctx, `INSERT INTO run_payloads(
		run_id,sequence,kind,node_id,node_attempt,execution_protocol,cipher_version,ciphertext,created_at
	) VALUES($1,0,'run_input',NULL,NULL,1,1,$2,$3)`, backupRun1, bytes.Repeat([]byte{0, 1, 2, 0xff}, 32), precise.Add(10*time.Microsecond)); err != nil {
		t.Fatal(err)
	}
	if _, err := source.Exec(ctx, `UPDATE runs SET status='cancelling',input_redacted_paths='{}'::text[],
		cancel_requested_at=$1::timestamptz + interval '6 microseconds',heartbeat_at=$1::timestamptz + interval '7 microseconds' WHERE id=$2`, precise, backupRun2); err != nil {
		t.Fatal(err)
	}
	if _, err := source.Exec(ctx, `UPDATE node_runs SET status='running',input='null'::jsonb,output='[]'::jsonb,
		started_at=$1::timestamptz + interval '8 microseconds',ended_at=NULL WHERE id=$2`, precise, backupNode1); err != nil {
		t.Fatal(err)
	}
	if _, err := source.Exec(ctx, `UPDATE run_events SET input='null'::jsonb,output='[]'::jsonb,
		active_ports=ARRAY['out'],input_redacted_paths='{}'::text[],output_redacted_paths=ARRAY['/token'],
		data_bytes=17,timestamp=$1::timestamptz + interval '9 microseconds' WHERE run_id=$2 AND sequence=1`, precise, backupRun2); err != nil {
		t.Fatal(err)
	}
}

func createRestoreArchive(t *testing.T, source *pgxpool.Pool) (string, Summary) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "complete.asbak")
	summary, err := Create(context.Background(), source, CreateOptions{Output: path, RuntimeVersion: "0.5.0-test"})
	if err != nil {
		t.Fatal(err)
	}
	return path, summary
}

func assertRestoredArchiveEqual(t *testing.T, target *pgxpool.Pool, want Summary) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "restored.asbak")
	got, err := Create(context.Background(), target, CreateOptions{Output: path, RuntimeVersion: "0.5.0-test"})
	if err != nil {
		t.Fatal(err)
	}
	if got.APIVersion != want.APIVersion || len(got.Tables) != len(want.Tables) {
		t.Fatalf("restored=%+v want=%+v", got, want)
	}
}

func assertRestoredDurableState(t *testing.T, target *pgxpool.Pool) {
	t.Helper()
	var owner *string
	var token int64
	var expires *time.Time
	if err := target.QueryRow(context.Background(), `SELECT lease_owner,lease_token,lease_expires_at FROM runs WHERE id=$1`, backupRun1).
		Scan(&owner, &token, &expires); err != nil {
		t.Fatal(err)
	}
	var ciphertext []byte
	if err := target.QueryRow(context.Background(), `SELECT ciphertext FROM run_payloads WHERE run_id=$1 AND sequence=0 AND kind='run_input'`, backupRun1).
		Scan(&ciphertext); err != nil {
		t.Fatal(err)
	}
	if owner != nil || expires != nil || token != 0 || !bytes.Equal(ciphertext, bytes.Repeat([]byte{0, 1, 2, 0xff}, 32)) {
		t.Fatalf("owner=%v token=%d expires=%v ciphertext=%x", owner, token, expires, ciphertext)
	}
}

func assertRestoredSpecialValues(t *testing.T, target *pgxpool.Pool) {
	t.Helper()
	var status string
	var output *string
	var redacted []string
	var heartbeat time.Time
	if err := target.QueryRow(context.Background(), `SELECT status,output::text,input_redacted_paths,heartbeat_at
		FROM runs WHERE id=$1`, backupRun1).Scan(&status, &output, &redacted, &heartbeat); err != nil {
		t.Fatal(err)
	}
	wantHeartbeat := time.Date(2026, 8, 29, 10, 0, 0, 123461000, time.UTC)
	if status != "running" || output == nil || *output != "null" || len(redacted) != 1 || redacted[0] != "/secret" || !heartbeat.Equal(wantHeartbeat) {
		t.Fatalf("status=%s output=%v redacted=%v heartbeat=%s", status, output, redacted, heartbeat)
	}
	if err := target.QueryRow(context.Background(), `SELECT status,input_redacted_paths FROM runs WHERE id=$1`, backupRun2).Scan(&status, &redacted); err != nil {
		t.Fatal(err)
	}
	if status != "cancelling" || redacted == nil || len(redacted) != 0 {
		t.Fatalf("status=%s redacted=%v", status, redacted)
	}
	var graphObject, nodeInput, nodeOutput, eventInput, eventOutput *string
	if err := target.QueryRow(context.Background(), `SELECT graph_snapshot::text FROM runs WHERE id=$1`, backupRun2).Scan(&graphObject); err != nil {
		t.Fatal(err)
	}
	if err := target.QueryRow(context.Background(), `SELECT input::text,output::text FROM node_runs WHERE id=$1`, backupNode1).Scan(&nodeInput, &nodeOutput); err != nil {
		t.Fatal(err)
	}
	if err := target.QueryRow(context.Background(), `SELECT input::text,output::text FROM run_events WHERE run_id=$1 AND sequence=1`, backupRun2).Scan(&eventInput, &eventOutput); err != nil {
		t.Fatal(err)
	}
	if graphObject == nil || *graphObject != "{}" || nodeInput == nil || *nodeInput != "null" || nodeOutput == nil || *nodeOutput != "[]" ||
		eventInput == nil || *eventInput != "null" || eventOutput == nil || *eventOutput != "[]" {
		t.Fatalf("graph=%v nodeInput=%v nodeOutput=%v eventInput=%v eventOutput=%v", graphObject, nodeInput, nodeOutput, eventInput, eventOutput)
	}
}

func assertBusinessRows(t *testing.T, pool *pgxpool.Pool, want int64) {
	t.Helper()
	var total int64
	for _, table := range TableOrder {
		var relation *string
		if err := pool.QueryRow(context.Background(), `SELECT to_regclass($1)::text`, string(table)).Scan(&relation); err != nil {
			t.Fatal(err)
		}
		if relation == nil {
			continue
		}
		var count int64
		if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM "+pgx.Identifier{string(table)}.Sanitize()).Scan(&count); err != nil {
			t.Fatal(err)
		}
		total += count
	}
	if total != want {
		t.Fatalf("business rows=%d want=%d", total, want)
	}
}
