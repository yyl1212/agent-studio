package backup

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/yyl1212/agent-studio/internal/database"
)

type CreateOptions struct {
	Output         string
	RuntimeVersion string
}

type createHooks struct {
	afterSnapshot func()
}

func Create(ctx context.Context, pool *pgxpool.Pool, options CreateOptions) (Summary, error) {
	return createWithHooks(ctx, pool, options, createHooks{})
}

func createWithHooks(ctx context.Context, pool *pgxpool.Pool, options CreateOptions, hooks createHooks) (Summary, error) {
	lease, err := database.TryShared(ctx, pool)
	if err != nil {
		return Summary{}, Wrap(CodeCreateFailed, "source is under maintenance", err)
	}
	defer func() { _ = lease.Release(context.Background()) }()

	current, err := database.CurrentVersion(ctx, pool)
	if err != nil {
		return Summary{}, Wrap(CodeCreateFailed, "read source migration version", err)
	}
	latest, err := database.LatestVersion()
	if err != nil {
		return Summary{}, Wrap(CodeCreateFailed, "read runtime migration version", err)
	}
	if current != latest {
		return Summary{}, Wrap(CodeSchemaNotCurrent, "source schema is not current", nil)
	}

	transaction, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return Summary{}, Wrap(CodeCreateFailed, "begin source snapshot", err)
	}
	defer func() { _ = transaction.Rollback(context.Background()) }()
	var createdAt time.Time
	if err := transaction.QueryRow(ctx, "SELECT transaction_timestamp()").Scan(&createdAt); err != nil {
		return Summary{}, Wrap(CodeCreateFailed, "read source snapshot time", err)
	}
	if hooks.afterSnapshot != nil {
		hooks.afterSnapshot()
	}
	writers, err := postgresTableWriters(ctx, transaction)
	if err != nil {
		return Summary{}, err
	}
	return WriteArchive(ctx, options.Output, Manifest{
		APIVersion: APIVersion, CreatedAt: createdAt.UTC(), RuntimeVersion: options.RuntimeVersion,
		DatabaseMigrationVersion: current, IncludesRuns: true,
	}, writers)
}

func Inspect(ctx context.Context, path string) (Summary, error) {
	archive, err := OpenArchive(ctx, path)
	if err != nil {
		return Summary{}, err
	}
	for _, name := range TableOrder {
		if err := archive.ReadTable(ctx, name, func(raw json.RawMessage) error {
			return validateTableRecord(name, raw)
		}); err != nil {
			_ = archive.Close()
			return Summary{}, err
		}
	}
	summary := archive.Summary()
	if err := archive.Close(); err != nil {
		return Summary{}, Wrap(CodeArchiveInvalid, "close backup archive", err)
	}
	return summary, nil
}
