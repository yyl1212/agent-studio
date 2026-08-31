package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/yyl1212/agent-studio/internal/database"
)

func TestDryRunPlansMigrationsWithoutMutatingEmptyDatabase(t *testing.T) {
	archivePath := referenceArchivePath(t, 6)
	pool := openUnmigratedTarget(t)

	plan, err := DryRun(context.Background(), pool, archivePath)
	if err != nil {
		t.Fatal(err)
	}
	if plan.TargetMigrationVersion != 0 || plan.LatestMigrationVersion != 6 ||
		!slices.Equal(plan.PendingMigrations, []int64{1, 2, 3, 4, 5, 6}) || !plan.TargetEmpty {
		t.Fatalf("plan=%+v", plan)
	}
	if plan.Archive.Path != archivePath || plan.Archive.MigrationVersion != 6 {
		t.Fatalf("archive=%+v", plan.Archive)
	}
	assertTargetVersionAndBusinessRows(t, pool, 0, 0)
}

func TestDryRunUsesExclusiveLeaseSessionWithSingleConnection(t *testing.T) {
	pool := openUnmigratedTargetWithMaxConns(t, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	plan, err := DryRun(ctx, pool, referenceArchivePath(t, 6))
	if err != nil {
		t.Fatal(err)
	}
	if plan.TargetMigrationVersion != 0 || !plan.TargetEmpty {
		t.Fatalf("plan=%+v", plan)
	}
	assertTargetVersionAndBusinessRows(t, pool, 0, 0)
}

func TestDryRunFailsWhenExclusiveLeaseConnectionIsTerminated(t *testing.T) {
	pool := openUnmigratedTarget(t)
	archivePath := referenceArchivePath(t, 6)
	leaseBackend := make(chan int32, 1)
	hookFailure := make(chan error, 1)
	continuePreflight := make(chan struct{})
	defer func() {
		select {
		case <-continuePreflight:
		default:
			close(continuePreflight)
		}
	}()
	result := make(chan error, 1)
	go func() {
		_, err := dryRunWithHooks(context.Background(), pool, archivePath, dryRunHooks{
			afterLeaseTransaction: func(ctx context.Context, transaction pgx.Tx) {
				var backendPID int32
				if err := transaction.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&backendPID); err != nil {
					hookFailure <- err
					return
				}
				leaseBackend <- backendPID
				<-continuePreflight
			},
		})
		result <- err
	}()

	var backendPID int32
	select {
	case backendPID = <-leaseBackend:
	case err := <-hookFailure:
		t.Fatal(err)
	case <-time.After(10 * time.Second):
		t.Fatal("dry-run did not expose its lease transaction")
	}
	var terminated bool
	if err := pool.QueryRow(context.Background(), `SELECT pg_terminate_backend($1)`, backendPID).Scan(&terminated); err != nil || !terminated {
		t.Fatalf("terminate backend=%t err=%v", terminated, err)
	}
	close(continuePreflight)
	select {
	case err := <-result:
		if CodeOf(err) != CodeRestoreFailed {
			t.Fatalf("code=%q err=%v", CodeOf(err), err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("dry-run did not stop after losing its maintenance lease")
	}
	assertTargetVersionAndBusinessRows(t, pool, 0, 0)
}

func TestDryRunPlansOnlyMissingMigrations(t *testing.T) {
	archivePath := referenceArchivePath(t, 6)
	for _, targetVersion := range []int64{3, 6} {
		t.Run(fmt.Sprintf("target migration %d", targetVersion), func(t *testing.T) {
			pool := openUnmigratedTarget(t)
			setTargetMigrationVersion(t, pool, targetVersion)

			plan, err := DryRun(context.Background(), pool, archivePath)
			if err != nil {
				t.Fatal(err)
			}
			wantPending := []int64{4, 5, 6}
			if targetVersion == 6 {
				wantPending = nil
			}
			if plan.TargetMigrationVersion != targetVersion || !slices.Equal(plan.PendingMigrations, wantPending) || !plan.TargetEmpty {
				t.Fatalf("plan=%+v", plan)
			}
			assertTargetVersionAndBusinessRows(t, pool, targetVersion, 0)
		})
	}
}

func TestDryRunCompatibilityRejectsUnsupportedMigrationPairsAndNewerRuntime(t *testing.T) {
	t.Run("archive migration newer than runtime", func(t *testing.T) {
		_, err := DryRun(context.Background(), openUnmigratedTarget(t), referenceArchivePath(t, 7))
		if CodeOf(err) != CodeRuntimeTooOld {
			t.Fatalf("code=%q err=%v", CodeOf(err), err)
		}
	})

	t.Run("target migration newer than runtime", func(t *testing.T) {
		pool := openUnmigratedTarget(t)
		if _, err := pool.Exec(context.Background(), `CREATE TABLE schema_migrations(version bigint PRIMARY KEY)`); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(context.Background(), `INSERT INTO schema_migrations(version) VALUES(7)`); err != nil {
			t.Fatal(err)
		}
		_, err := DryRun(context.Background(), pool, referenceArchivePath(t, 6))
		if CodeOf(err) != CodeRuntimeTooOld {
			t.Fatalf("code=%q err=%v", CodeOf(err), err)
		}
	})

	t.Run("older migration without registered importer", func(t *testing.T) {
		_, err := DryRun(context.Background(), openUnmigratedTarget(t), referenceArchivePath(t, 3))
		if CodeOf(err) != CodeFormatUnsupported {
			t.Fatalf("code=%q err=%v", CodeOf(err), err)
		}
	})

	t.Run("unsupported api version", func(t *testing.T) {
		_, err := DryRun(context.Background(), openUnmigratedTarget(t), unsupportedAPIArchivePath(t))
		if CodeOf(err) != CodeFormatUnsupported {
			t.Fatalf("code=%q err=%v", CodeOf(err), err)
		}
	})
}

func TestDryRunRejectsNonEmptyBusinessTargetWithoutPersistentWrites(t *testing.T) {
	pool := openUnmigratedTarget(t)
	setTargetMigrationVersion(t, pool, 6)
	insertBackupFixture(t, pool)

	_, err := DryRun(context.Background(), pool, referenceArchivePath(t, 6))
	if CodeOf(err) != CodeTargetNotEmpty {
		t.Fatalf("code=%q err=%v", CodeOf(err), err)
	}
	assertTargetVersionAndBusinessRows(t, pool, 6, 8)
}

func TestDryRunRejectsSharedMaintenanceLeases(t *testing.T) {
	archivePath := referenceArchivePath(t, 6)
	for _, name := range []string{"api shared lease", "backup shared lease"} {
		t.Run(name, func(t *testing.T) {
			pool := openUnmigratedTarget(t)
			lease, err := database.TryShared(context.Background(), pool)
			if err != nil {
				t.Fatal(err)
			}
			defer lease.Release(context.Background())

			_, err = DryRun(context.Background(), pool, archivePath)
			if CodeOf(err) != CodeAPIRunning {
				t.Fatalf("code=%q err=%v", CodeOf(err), err)
			}
			assertTargetVersionAndBusinessRows(t, pool, 0, 0)
		})
	}
}

func TestDryRunReturnsSafeArchiveAndCancellationErrors(t *testing.T) {
	t.Run("corrupt archive", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "corrupt.asbak")
		if err := os.WriteFile(path, []byte("not a zip"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := DryRun(context.Background(), openUnmigratedTarget(t), path)
		if CodeOf(err) != CodeArchiveInvalid {
			t.Fatalf("code=%q err=%v", CodeOf(err), err)
		}
	})

	t.Run("cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := DryRun(ctx, openUnmigratedTarget(t), referenceArchivePath(t, 6))
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err=%v", err)
		}
	})

	t.Run("safe filename error", func(t *testing.T) {
		const password = "sentinel-password-must-not-leak"
		_, err := DryRun(context.Background(), openUnmigratedTarget(t), filepath.Join(t.TempDir(), password+".asbak"))
		if CodeOf(err) != CodeArchiveInvalid || err == nil || strings.Contains(err.Error(), password) {
			t.Fatalf("unsafe error=%v", err)
		}
	})
}

func openUnmigratedTarget(t *testing.T) *pgxpool.Pool {
	return openUnmigratedTargetWithMaxConns(t, 0)
}

func openUnmigratedTargetWithMaxConns(t *testing.T, maxConns int32) *pgxpool.Pool {
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
	schema := fmt.Sprintf("preflight_test_%d", backupDatabaseID.Add(1))
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(context.Background(), "CREATE SCHEMA "+identifier); err != nil {
		admin.Close()
		backupDatabaseMutex.Unlock()
		t.Fatal(err)
	}
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("options", "-csearch_path="+schema)
	if maxConns > 0 {
		query.Set("pool_max_conns", fmt.Sprint(maxConns))
	}
	parsed.RawQuery = query.Encode()
	pool, err := database.OpenPool(context.Background(), parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+identifier+" CASCADE")
		admin.Close()
		backupDatabaseMutex.Unlock()
	})
	return pool
}

func referenceArchivePath(t *testing.T, migrationVersion int64) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), fmt.Sprintf("migration-%d.asbak", migrationVersion))
	manifest := manifestFixture(time.Now().UTC())
	manifest.DatabaseMigrationVersion = migrationVersion
	if _, err := WriteArchive(context.Background(), path, manifest, referenceFixtureWriters(t, newReferenceFixtureData())); err != nil {
		t.Fatal(err)
	}
	return path
}

func setTargetMigrationVersion(t *testing.T, pool *pgxpool.Pool, version int64) {
	t.Helper()
	if err := database.Migrate(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	if version < 6 {
		if _, err := pool.Exec(context.Background(), `DELETE FROM schema_migrations WHERE version > $1`, version); err != nil {
			t.Fatal(err)
		}
	}
}

func assertTargetVersionAndBusinessRows(t *testing.T, pool *pgxpool.Pool, wantVersion int64, wantRows int64) {
	t.Helper()
	gotVersion, err := database.CurrentVersion(context.Background(), pool)
	if err != nil || gotVersion != wantVersion {
		t.Fatalf("current=%d err=%v want=%d", gotVersion, err, wantVersion)
	}
	var rows int64
	for _, name := range TableOrder {
		var exists *string
		if err := pool.QueryRow(context.Background(), `SELECT to_regclass($1)::text`, string(name)).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if exists == nil {
			continue
		}
		var count int64
		if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM "+pgx.Identifier{string(name)}.Sanitize()).Scan(&count); err != nil {
			t.Fatal(err)
		}
		rows += count
	}
	if rows != wantRows {
		t.Fatalf("business rows=%d want=%d", rows, wantRows)
	}
}

func unsupportedAPIArchivePath(t *testing.T) string {
	t.Helper()
	manifest := manifestFixture(time.Now().UTC())
	manifest.APIVersion = "agent-studio.dev/backup/unsupported"
	body, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	entries := []zipFixtureEntry{{name: manifestPath, body: body, mode: 0o600}, {name: checksumsPath, body: []byte("x\n"), mode: 0o600}}
	for _, name := range TableOrder {
		path, _ := tablePath(name)
		entries = append(entries, zipFixtureEntry{name: path, body: []byte("{}\n"), mode: 0o600})
	}
	return writeZipFixture(t, entries)
}
