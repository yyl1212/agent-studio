package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	backupdomain "github.com/yyl1212/agent-studio/internal/backup"
)

func TestBackupCommandCreateRequiresDatabaseURL(t *testing.T) {
	var stderr bytes.Buffer
	code := backupCommandWithDependencies(context.Background(), []string{"create", "--output", "snapshot.asbak"}, io.Discard, &stderr, backupCommandDependencies{
		lookupEnv: func(string) (string, bool) { return "", false },
	})
	if code != 1 || stderr.String() != "BACKUP_CREATE_FAILED: DATABASE_URL is required\n" {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}

func TestBackupCommandCreateUsesSafeOutput(t *testing.T) {
	secret := "postgres://user:secret@example/database"
	var stderr bytes.Buffer
	code := backupCommandWithDependencies(context.Background(), []string{"create", "--output", "snapshot.asbak"}, io.Discard, &stderr, backupCommandDependencies{
		lookupEnv: func(string) (string, bool) { return secret, true },
		openPool: func(context.Context, string) (*pgxpool.Pool, error) {
			return nil, errors.New(secret)
		},
	})
	if code != 1 || strings.Contains(stderr.String(), secret) || stderr.String() != "BACKUP_CREATE_FAILED: open source database\n" {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}

func TestBackupCommandCreateRoutesOptionsAndPrintsHumanSummary(t *testing.T) {
	var stdout bytes.Buffer
	called := false
	code := backupCommandWithDependencies(context.Background(), []string{"create", "--output", "snapshot.asbak"}, &stdout, io.Discard, backupCommandDependencies{
		lookupEnv:      func(name string) (string, bool) { return "postgres://safe", name == "DATABASE_URL" },
		openPool:       func(context.Context, string) (*pgxpool.Pool, error) { return nil, nil },
		closePool:      func(*pgxpool.Pool) {},
		runtimeVersion: func() string { return "0.5.0-test" },
		create: func(_ context.Context, _ *pgxpool.Pool, options backupdomain.CreateOptions) (backupdomain.Summary, error) {
			called = true
			if options.Output != "snapshot.asbak" || options.RuntimeVersion != "0.5.0-test" {
				t.Fatalf("options=%+v", options)
			}
			return backupSummaryFixture("snapshot.asbak"), nil
		},
	})
	want := "backup: \"snapshot.asbak\"\nformat: agent-studio.dev/backup/v1alpha1\nruntime: 0.5.0-test\nmigration: 6\nrecords: 3\ncompressed: 4096\nchecksum: sha256:" + strings.Repeat("a", 64) + "\n"
	if code != 0 || !called || stdout.String() != want {
		t.Fatalf("code=%d called=%t stdout=%q", code, called, stdout.String())
	}
}

func TestBackupInspectNeverReadsDatabaseEnvironment(t *testing.T) {
	var stdout bytes.Buffer
	lookedUp := false
	code := backupCommandWithDependencies(context.Background(), []string{"inspect", "line\nbreak.asbak"}, &stdout, io.Discard, backupCommandDependencies{
		lookupEnv: func(string) (string, bool) { lookedUp = true; return "", false },
		inspect: func(_ context.Context, path string) (backupdomain.Summary, error) {
			if path != "line\nbreak.asbak" {
				t.Fatalf("path=%q", path)
			}
			return backupSummaryFixture(path), nil
		},
	})
	if code != 0 || lookedUp || !strings.HasPrefix(stdout.String(), "backup: \"line\\nbreak.asbak\"\n") {
		t.Fatalf("code=%d lookedUp=%t stdout=%q", code, lookedUp, stdout.String())
	}
}

func TestBackupInspectJSONOutput(t *testing.T) {
	var stdout bytes.Buffer
	code := backupCommandWithDependencies(context.Background(), []string{"inspect", "--json", "snapshot.asbak"}, &stdout, io.Discard, backupCommandDependencies{
		inspect: func(context.Context, string) (backupdomain.Summary, error) {
			return backupSummaryFixture("snapshot.asbak"), nil
		},
	})
	if code != 0 || !strings.Contains(stdout.String(), `"apiVersion":"agent-studio.dev/backup/v1alpha1"`) || !strings.HasSuffix(stdout.String(), "\n") {
		t.Fatalf("code=%d stdout=%q", code, stdout.String())
	}
}

func TestBackupCommandMapsContextCancellationToStableCodes(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		deps backupCommandDependencies
		want string
	}{
		{name: "create", args: []string{"create", "--output", "snapshot.asbak"}, deps: backupCommandDependencies{
			lookupEnv: func(string) (string, bool) { return "postgres://safe", true },
			openPool:  func(context.Context, string) (*pgxpool.Pool, error) { return nil, nil }, closePool: func(*pgxpool.Pool) {},
			create: func(context.Context, *pgxpool.Pool, backupdomain.CreateOptions) (backupdomain.Summary, error) {
				return backupdomain.Summary{}, context.Canceled
			},
		}, want: "BACKUP_CREATE_FAILED: create backup\n"},
		{name: "inspect", args: []string{"inspect", "snapshot.asbak"}, deps: backupCommandDependencies{
			inspect: func(context.Context, string) (backupdomain.Summary, error) {
				return backupdomain.Summary{}, context.Canceled
			},
		}, want: "BACKUP_ARCHIVE_INVALID: inspect backup\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stderr bytes.Buffer
			if code := backupCommandWithDependencies(context.Background(), test.args, io.Discard, &stderr, test.deps); code != 1 || stderr.String() != test.want {
				t.Fatalf("code=%d stderr=%q", code, stderr.String())
			}
		})
	}
}

func TestBackupCommandRejectsInvalidArguments(t *testing.T) {
	for _, args := range [][]string{
		nil, {"create"}, {"create", "--output"}, {"create", "--unknown", "x"},
		{"inspect"}, {"inspect", "--json"}, {"inspect", "one", "two"}, {"restore", "x"},
	} {
		var stderr bytes.Buffer
		if code := backupCommandWithDependencies(context.Background(), args, io.Discard, &stderr, backupCommandDependencies{}); code != 2 {
			t.Fatalf("args=%v code=%d stderr=%q", args, code, stderr.String())
		}
	}
}

func backupSummaryFixture(path string) backupdomain.Summary {
	return backupdomain.Summary{
		Path: path, APIVersion: backupdomain.APIVersion, CreatedAt: time.Now().UTC(), RuntimeVersion: "0.5.0-test",
		MigrationVersion: 6, DatasetDigest: "sha256:" + strings.Repeat("a", 64), CompressedBytes: 4096,
		Tables: []backupdomain.TableManifest{{Name: backupdomain.TableWorkflows, Records: 1}, {Name: backupdomain.TableRuns, Records: 2}},
	}
}
