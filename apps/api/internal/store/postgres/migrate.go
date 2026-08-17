package postgres

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"

	"agentstudio.local/api/migrations"
	"github.com/jackc/pgx/v5"
)

var migrationNamePattern = regexp.MustCompile(`^(\d{6})_.+\.sql$`)

type migrationFile struct {
	version int64
	name    string
	sql     string
}

func (store *Store) Migrate(ctx context.Context) error {
	if _, err := store.pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
        version bigint PRIMARY KEY,
        name text NOT NULL,
        applied_at timestamptz NOT NULL DEFAULT now()
    )`); err != nil {
		return fmt.Errorf("bootstrap migrations: %w", err)
	}
	files, err := loadMigrations()
	if err != nil {
		return err
	}
	for _, migration := range files {
		if err := store.applyMigration(ctx, migration); err != nil {
			return err
		}
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

func (store *Store) applyMigration(ctx context.Context, migration migrationFile) error {
	transaction, err := store.pool.Begin(ctx)
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
