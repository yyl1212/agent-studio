package database

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/yyl1212/agent-studio/apps/api/migrations"
)

var ErrSchemaIncomplete = errors.New("database schema is incomplete")

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
	var applied []int64
	if err := query.QueryRow(ctx, "SELECT COALESCE(array_agg(version ORDER BY version),ARRAY[]::bigint[]) FROM schema_migrations").Scan(&applied); err != nil {
		return 0, fmt.Errorf("read schema migration version: %w", err)
	}
	if len(applied) == 0 {
		return 0, nil
	}
	version := applied[len(applied)-1]
	latest, err := LatestVersion()
	if err != nil {
		return 0, err
	}
	if version > latest {
		return version, nil
	}
	files, err := loadMigrations()
	if err != nil {
		return 0, err
	}
	expected := make([]int64, 0, len(files))
	for _, file := range files {
		if file.version <= version {
			expected = append(expected, file.version)
		}
	}
	if len(applied) != len(expected) {
		return 0, fmt.Errorf("%w: migration records", ErrSchemaIncomplete)
	}
	for index := range expected {
		if applied[index] != expected[index] {
			return 0, fmt.Errorf("%w: migration records", ErrSchemaIncomplete)
		}
	}
	return version, nil
}

func ValidateCurrentSchema(ctx context.Context, query RowQuerier) error {
	current, err := CurrentVersion(ctx, query)
	if err != nil {
		return err
	}
	latest, err := LatestVersion()
	if err != nil {
		return err
	}
	if current != latest {
		return ErrSchemaIncomplete
	}
	var complete bool
	if err := query.QueryRow(ctx, `SELECT NOT EXISTS (
		SELECT * FROM (VALUES
			('workflows','id'),('workflows','name'),('workflows','slug'),('workflows','description'),
			('workflows','draft_graph'),('workflows','draft_revision'),('workflows','published_version_id'),
			('workflows','created_at'),('workflows','updated_at'),('workflows','archived_at'),('workflows','agent_presentation'),
			('workflow_versions','id'),('workflow_versions','workflow_id'),('workflow_versions','version'),
			('workflow_versions','graph'),('workflow_versions','input_schema'),('workflow_versions','created_at'),
			('workflow_versions','agent_presentation'),
			('runs','id'),('runs','workflow_id'),('runs','workflow_version_id'),('runs','draft_revision'),
			('runs','graph_snapshot'),('runs','mode'),('runs','status'),('runs','input'),('runs','output'),
			('runs','error'),('runs','started_at'),('runs','ended_at'),('runs','source_run_id'),
			('runs','source_node_id'),('runs','retry_of_run_id'),('runs','retry_key'),
			('runs','input_redacted_paths'),('runs','cancel_requested_at'),('runs','heartbeat_at'),('runs','agent_request_key'),
			('runs','execution_protocol'),('runs','lease_owner'),('runs','lease_token'),('runs','lease_expires_at'),
			('runs','recovery_reason'),('runs','recovery_requested_at'),
			('node_runs','id'),('node_runs','run_id'),('node_runs','node_id'),('node_runs','node_type'),
			('node_runs','status'),('node_runs','input'),('node_runs','output'),('node_runs','error'),
			('node_runs','started_at'),('node_runs','ended_at'),('node_runs','attempt'),
			('run_events','run_id'),('run_events','sequence'),('run_events','type'),('run_events','node_id'),
			('run_events','status'),('run_events','input'),('run_events','output'),('run_events','active_ports'),
			('run_events','error'),('run_events','input_redacted_paths'),('run_events','output_redacted_paths'),
			('run_events','data_bytes'),('run_events','timestamp'),('run_events','node_attempt'),
			('run_payloads','run_id'),('run_payloads','sequence'),('run_payloads','kind'),('run_payloads','node_id'),
			('run_payloads','node_attempt'),('run_payloads','execution_protocol'),('run_payloads','cipher_version'),
			('run_payloads','ciphertext'),('run_payloads','created_at'),
			('workflow_draft_checkpoints','workflow_id'),('workflow_draft_checkpoints','source_revision'),
			('workflow_draft_checkpoints','restored_revision'),('workflow_draft_checkpoints','graph'),
			('workflow_draft_checkpoints','agent_presentation'),('workflow_draft_checkpoints','restored_from_version_id'),
			('workflow_draft_checkpoints','created_at')
		) expected(table_name,column_name)
		EXCEPT
		SELECT table_name,column_name FROM information_schema.columns WHERE table_schema=current_schema()
	)`).Scan(&complete); err != nil {
		return fmt.Errorf("probe database schema: %w", err)
	}
	if !complete {
		return ErrSchemaIncomplete
	}
	return nil
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
