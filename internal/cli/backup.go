package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	backupdomain "github.com/yyl1212/agent-studio/internal/backup"
	"github.com/yyl1212/agent-studio/internal/buildinfo"
	"github.com/yyl1212/agent-studio/internal/database"
)

type backupCommandDependencies struct {
	lookupEnv      func(string) (string, bool)
	openPool       func(context.Context, string) (*pgxpool.Pool, error)
	closePool      func(*pgxpool.Pool)
	runtimeVersion func() string
	create         func(context.Context, *pgxpool.Pool, backupdomain.CreateOptions) (backupdomain.Summary, error)
	inspect        func(context.Context, string) (backupdomain.Summary, error)
	dryRun         func(context.Context, *pgxpool.Pool, string) (backupdomain.RestorePlan, error)
	restore        func(context.Context, *pgxpool.Pool, string) (backupdomain.RestoreResult, error)
}

func backupCommand(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	return backupCommandWithDependencies(ctx, args, stdout, stderr, backupCommandDependencies{})
}

func backupCommandWithDependencies(ctx context.Context, args []string, stdout, stderr io.Writer, dependencies backupCommandDependencies) int {
	dependencies = defaultBackupCommandDependencies(dependencies)
	if len(args) == 3 && args[0] == "create" && args[1] == "--output" && args[2] != "" {
		databaseURL, ok := dependencies.lookupEnv("DATABASE_URL")
		if !ok || databaseURL == "" {
			_, _ = io.WriteString(stderr, "BACKUP_CREATE_FAILED: DATABASE_URL is required\n")
			return 1
		}
		pool, err := dependencies.openPool(ctx, databaseURL)
		if err != nil {
			_, _ = io.WriteString(stderr, "BACKUP_CREATE_FAILED: open source database\n")
			return 1
		}
		defer dependencies.closePool(pool)
		summary, err := dependencies.create(ctx, pool, backupdomain.CreateOptions{
			Output: args[2], RuntimeVersion: dependencies.runtimeVersion(),
		})
		if err != nil {
			writeBackupError(stderr, err, backupdomain.CodeCreateFailed, "create backup")
			return 1
		}
		if err := writeBackupSummary(stdout, summary); err != nil {
			_, _ = io.WriteString(stderr, "BACKUP_CREATE_FAILED: write backup summary\n")
			return 1
		}
		return 0
	}
	if len(args) >= 1 && args[0] == "restore" {
		mode, path, ok := parseRestoreArguments(args)
		if !ok {
			writeBackupRestoreUsage(stderr)
			return 2
		}
		databaseURL, present := dependencies.lookupEnv("DATABASE_URL")
		if !present || databaseURL == "" {
			_, _ = io.WriteString(stderr, "BACKUP_RESTORE_FAILED: DATABASE_URL is required\n")
			return 1
		}
		pool, err := dependencies.openPool(ctx, databaseURL)
		if err != nil {
			_, _ = io.WriteString(stderr, "BACKUP_RESTORE_FAILED: open target database\n")
			return 1
		}
		defer dependencies.closePool(pool)
		if mode == "dry-run" {
			plan, err := dependencies.dryRun(ctx, pool, path)
			if err != nil {
				writeBackupError(stderr, err, backupdomain.CodeRestoreFailed, "dry-run backup")
				return 1
			}
			if err := writeBackupRestorePlan(stdout, plan); err != nil {
				_, _ = io.WriteString(stderr, "BACKUP_RESTORE_FAILED: write dry-run summary\n")
				return 1
			}
			return 0
		}
		result, err := dependencies.restore(ctx, pool, path)
		if err != nil {
			writeBackupError(stderr, err, backupdomain.CodeRestoreFailed, "restore backup")
			return 1
		}
		if err := writeBackupRestoreResult(stdout, result); err != nil {
			_, _ = io.WriteString(stderr, "BACKUP_RESTORE_FAILED: write restore summary\n")
			return 1
		}
		return 0
	}

	jsonOutput := false
	path := ""
	if len(args) >= 1 && args[0] == "inspect" {
		switch {
		case len(args) == 2 && args[1] != "" && !strings.HasPrefix(args[1], "-"):
			path = args[1]
		case len(args) == 3 && args[1] == "--json" && args[2] != "":
			jsonOutput = true
			path = args[2]
		default:
			_, _ = io.WriteString(stderr, "backup inspect usage: backup inspect [--json] <path>\n")
			return 2
		}
		summary, err := dependencies.inspect(ctx, path)
		if err != nil {
			writeBackupError(stderr, err, backupdomain.CodeArchiveInvalid, "inspect backup")
			return 1
		}
		if jsonOutput {
			if err := json.NewEncoder(stdout).Encode(summary); err != nil {
				_, _ = io.WriteString(stderr, "BACKUP_ARCHIVE_INVALID: write backup summary\n")
				return 1
			}
			return 0
		}
		if err := writeBackupSummary(stdout, summary); err != nil {
			_, _ = io.WriteString(stderr, "BACKUP_ARCHIVE_INVALID: write backup summary\n")
			return 1
		}
		return 0
	}

	_, _ = io.WriteString(stderr, "backup usage: backup create --output <path> | backup inspect [--json] <path> | backup restore --dry-run <path> | backup restore --confirm-empty-instance <path>\n")
	return 2
}

func writeBackupError(writer io.Writer, err error, fallback backupdomain.Code, operation string) {
	code := backupdomain.CodeOf(err)
	if code == "" {
		code = fallback
	}
	_, _ = fmt.Fprintf(writer, "%s: %s\n", code, operation)
}

func defaultBackupCommandDependencies(dependencies backupCommandDependencies) backupCommandDependencies {
	if dependencies.lookupEnv == nil {
		dependencies.lookupEnv = os.LookupEnv
	}
	if dependencies.openPool == nil {
		dependencies.openPool = database.OpenPool
	}
	if dependencies.closePool == nil {
		dependencies.closePool = func(pool *pgxpool.Pool) {
			if pool != nil {
				pool.Close()
			}
		}
	}
	if dependencies.runtimeVersion == nil {
		dependencies.runtimeVersion = func() string { return buildinfo.Current().Version }
	}
	if dependencies.create == nil {
		dependencies.create = backupdomain.Create
	}
	if dependencies.inspect == nil {
		dependencies.inspect = backupdomain.Inspect
	}
	if dependencies.dryRun == nil {
		dependencies.dryRun = backupdomain.DryRun
	}
	if dependencies.restore == nil {
		dependencies.restore = backupdomain.Restore
	}
	return dependencies
}

func parseRestoreArguments(args []string) (string, string, bool) {
	if len(args) != 3 || (args[1] != "--dry-run" && args[1] != "--confirm-empty-instance") || args[2] == "" {
		return "", "", false
	}
	flags := flag.NewFlagSet("backup restore", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	dryRun := flags.Bool("dry-run", false, "")
	confirmEmptyInstance := flags.Bool("confirm-empty-instance", false, "")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 1 || (*dryRun == *confirmEmptyInstance) {
		return "", "", false
	}
	if *dryRun {
		return "dry-run", flags.Arg(0), true
	}
	return "restore", flags.Arg(0), true
}

func writeBackupRestoreUsage(writer io.Writer) {
	_, _ = io.WriteString(writer, "backup restore usage: backup restore --dry-run <path> | backup restore --confirm-empty-instance <path>\n")
}

func writeBackupRestorePlan(writer io.Writer, plan backupdomain.RestorePlan) error {
	if _, err := fmt.Fprintf(writer, "format: %s\narchive-migration: %d\ntarget-migration: %d\nlatest-migration: %d\npending-migrations: %s\n",
		plan.Archive.APIVersion, plan.Archive.MigrationVersion, plan.TargetMigrationVersion, plan.LatestMigrationVersion,
		formatMigrationList(plan.PendingMigrations)); err != nil {
		return err
	}
	if err := writeBackupTableCounts(writer, plan.Archive.Tables); err != nil {
		return err
	}
	_, err := fmt.Fprintf(writer, "target-empty: %t\n", plan.TargetEmpty)
	return err
}

func writeBackupRestoreResult(writer io.Writer, result backupdomain.RestoreResult) error {
	if _, err := fmt.Fprintf(writer, "format: %s\narchive-migration: %d\ncommitted-migration: %d\n",
		result.Summary.APIVersion, result.Summary.MigrationVersion, result.MigrationVersion); err != nil {
		return err
	}
	var records uint64
	for _, name := range backupdomain.TableOrder {
		records += result.Tables[name]
	}
	if _, err := fmt.Fprintf(writer, "records: %d\n", records); err != nil {
		return err
	}
	for _, name := range backupdomain.TableOrder {
		if count, ok := result.Tables[name]; ok {
			if _, err := fmt.Fprintf(writer, "%s: %d\n", name, count); err != nil {
				return err
			}
		}
	}
	_, err := io.WriteString(writer, "restored: true\n")
	return err
}

func writeBackupTableCounts(writer io.Writer, tables []backupdomain.TableManifest) error {
	counts := make(map[backupdomain.TableName]uint64, len(tables))
	var records uint64
	for _, table := range tables {
		counts[table.Name] = table.Records
		records += table.Records
	}
	if _, err := fmt.Fprintf(writer, "records: %d\n", records); err != nil {
		return err
	}
	for _, name := range backupdomain.TableOrder {
		if count, ok := counts[name]; ok {
			if _, err := fmt.Fprintf(writer, "%s: %d\n", name, count); err != nil {
				return err
			}
		}
	}
	return nil
}

func formatMigrationList(versions []int64) string {
	if len(versions) == 0 {
		return "none"
	}
	values := make([]string, len(versions))
	for index, version := range versions {
		values[index] = strconv.FormatInt(version, 10)
	}
	return strings.Join(values, ",")
}

func writeBackupSummary(writer io.Writer, summary backupdomain.Summary) error {
	var records uint64
	for _, table := range summary.Tables {
		records += table.Records
	}
	_, err := fmt.Fprintf(writer, "backup: %s\nformat: %s\nruntime: %s\nmigration: %d\nrecords: %d\ncompressed: %d\nchecksum: %s\n",
		strconv.Quote(summary.Path), summary.APIVersion, summary.RuntimeVersion, summary.MigrationVersion,
		records, summary.CompressedBytes, summary.DatasetDigest)
	return err
}
