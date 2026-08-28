package database

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/yyl1212/agent-studio/apps/api/migrations"
)

var migrationNamePattern = regexp.MustCompile(`^(\d{6})_.+\.sql$`)

type RowQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

type MigrationDB interface {
	RowQuerier
	Begin(context.Context) (pgx.Tx, error)
}

type migrationFile struct {
	version int64
	name    string
	sql     string
}

func LatestVersion() (int64, error) {
	files, err := loadMigrations()
	if err != nil {
		return 0, err
	}
	if len(files) == 0 {
		return 0, nil
	}
	return files[len(files)-1].version, nil
}

func CurrentVersion(ctx context.Context, query RowQuerier) (int64, error) {
	var table *string
	if err := query.QueryRow(ctx, "SELECT to_regclass('schema_migrations')::text").Scan(&table); err != nil {
		return 0, fmt.Errorf("find schema migrations: %w", err)
	}
	if table == nil {
		return 0, nil
	}
	var version int64
	if err := query.QueryRow(ctx, "SELECT COALESCE(MAX(version),0) FROM schema_migrations").Scan(&version); err != nil {
		return 0, fmt.Errorf("read schema migration version: %w", err)
	}
	return version, nil
}

func Migrate(ctx context.Context, database MigrationDB) error {
	if err := bootstrapMigrations(ctx, database); err != nil {
		return err
	}
	files, err := loadMigrations()
	if err != nil {
		return err
	}
	for _, migration := range files {
		if err := applyMigration(ctx, database, migration); err != nil {
			return err
		}
	}
	return nil
}

func bootstrapMigrations(ctx context.Context, database MigrationDB) error {
	transaction, err := database.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin migration bootstrap: %w", err)
	}
	defer transaction.Rollback(ctx)
	if _, err := transaction.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
        version bigint PRIMARY KEY,
        name text NOT NULL,
        applied_at timestamptz NOT NULL DEFAULT now()
    )`); err != nil {
		return fmt.Errorf("bootstrap migrations: %w", err)
	}
	if err := transaction.Commit(ctx); err != nil {
		return fmt.Errorf("commit migration bootstrap: %w", err)
	}
	return nil
}

func loadMigrations() ([]migrationFile, error) {
	entries, err := migrations.Files.ReadDir(".")
	if err != nil {
		return nil, fmt.Errorf("read embedded migrations: %w", err)
	}
	files := make([]migrationFile, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := filepath.Base(entry.Name())
		matches := migrationNamePattern.FindStringSubmatch(name)
		if matches == nil {
			continue
		}
		version, err := strconv.ParseInt(matches[1], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse migration version %s: %w", name, err)
		}
		content, err := migrations.Files.ReadFile(name)
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", name, err)
		}
		files = append(files, migrationFile{version: version, name: name, sql: string(content)})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].version < files[j].version })
	return files, nil
}

func applyMigration(ctx context.Context, database MigrationDB, migration migrationFile) error {
	transaction, err := database.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin migration %s: %w", migration.name, err)
	}
	defer transaction.Rollback(ctx)
	if _, err := transaction.Exec(ctx, "SELECT pg_advisory_xact_lock(87421031)"); err != nil {
		return fmt.Errorf("lock migration %s: %w", migration.name, err)
	}
	var applied bool
	if err := transaction.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version=$1)", migration.version).Scan(&applied); err != nil {
		return fmt.Errorf("check migration %s: %w", migration.name, err)
	}
	if applied {
		return transaction.Commit(ctx)
	}
	if _, err := transaction.Exec(ctx, migration.sql, pgx.QueryExecModeSimpleProtocol); err != nil {
		return fmt.Errorf("execute migration %s: %w", migration.name, err)
	}
	if _, err := transaction.Exec(ctx, "INSERT INTO schema_migrations(version,name) VALUES($1,$2)", migration.version, migration.name); err != nil {
		return fmt.Errorf("record migration %s: %w", migration.name, err)
	}
	if err := transaction.Commit(ctx); err != nil {
		return fmt.Errorf("commit migration %s: %w", migration.name, err)
	}
	return nil
}
