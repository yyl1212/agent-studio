package cli

import (
	"context"
	"encoding/json"
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

	_, _ = io.WriteString(stderr, "backup usage: backup create --output <path> | backup inspect [--json] <path>\n")
	return 2
}

func writeBackupError(writer io.Writer, err error, fallback backupdomain.Code, operation string) {
	if backupdomain.CodeOf(err) == "" {
		err = backupdomain.Wrap(fallback, operation, err)
	}
	_, _ = fmt.Fprintln(writer, err)
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
	return dependencies
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
