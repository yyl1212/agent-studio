package backup

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
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
	afterSnapshot   func()
	snapshotBackend func(int32)
	afterTables     func()
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

	current, err := lease.CurrentVersion(ctx)
	if err != nil {
		if errors.Is(err, database.ErrSchemaIncomplete) {
			return Summary{}, Wrap(CodeSchemaNotCurrent, "source schema is not current", nil)
		}
		return Summary{}, Wrap(CodeCreateFailed, "read source migration version", err)
	}
	latest, err := database.LatestVersion()
	if err != nil {
		return Summary{}, Wrap(CodeCreateFailed, "read runtime migration version", err)
	}
	if current != latest {
		return Summary{}, Wrap(CodeSchemaNotCurrent, "source schema is not current", nil)
	}
	if err := lease.ValidateCurrentSchema(ctx); err != nil {
		return Summary{}, wrapSchemaValidationError(err)
	}

	transaction, err := lease.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return Summary{}, Wrap(CodeCreateFailed, "begin source snapshot", err)
	}
	defer func() { _ = transaction.Rollback(context.Background()) }()
	var createdAt time.Time
	if err := transaction.QueryRow(ctx, "SELECT transaction_timestamp()").Scan(&createdAt); err != nil {
		return Summary{}, Wrap(CodeCreateFailed, "read source snapshot time", err)
	}
	if hooks.snapshotBackend != nil {
		var backendPID int32
		if err := transaction.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&backendPID); err != nil {
			return Summary{}, Wrap(CodeCreateFailed, "read source snapshot backend", err)
		}
		hooks.snapshotBackend(backendPID)
	}
	if hooks.afterSnapshot != nil {
		hooks.afterSnapshot()
	}
	writers, err := postgresTableWriters(ctx, transaction)
	if err != nil {
		return Summary{}, err
	}
	lost := lease.MonitorConnectionLoss()
	archiveContext, stopMonitoring := monitorLeaseLoss(ctx, lost)
	defer stopMonitoring()

	summary, err := writeArchive(archiveContext, options.Output, Manifest{
		APIVersion: APIVersion, CreatedAt: createdAt.UTC(), RuntimeVersion: options.RuntimeVersion,
		DatabaseMigrationVersion: current, IncludesRuns: true,
	}, writers, func(publishContext context.Context) error {
		if hooks.afterTables != nil {
			hooks.afterTables()
		}
		var alive int
		if err := transaction.QueryRow(publishContext, "SELECT 1").Scan(&alive); err != nil {
			return Wrap(CodeCreateFailed, "verify source snapshot before publish", err)
		}
		return nil
	})
	if err != nil && ctx.Err() == nil {
		if cause := context.Cause(archiveContext); cause != nil {
			return Summary{}, Wrap(CodeCreateFailed, "source maintenance lease lost", cause)
		}
	}
	return summary, err
}

func monitorLeaseLoss(ctx context.Context, lost <-chan error) (context.Context, func()) {
	archiveContext, cancelArchive := context.WithCancelCause(ctx)
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		select {
		case cause := <-lost:
			cancelArchive(cause)
		case <-stop:
		}
	}()
	var stopOnce sync.Once
	return archiveContext, func() {
		stopOnce.Do(func() {
			close(stop)
			<-done
			cancelArchive(nil)
		})
	}
}

func wrapSchemaValidationError(err error) error {
	if errors.Is(err, database.ErrSchemaIncomplete) {
		return Wrap(CodeSchemaNotCurrent, "source schema is not current", nil)
	}
	return Wrap(CodeCreateFailed, "validate source schema", err)
}

func Inspect(ctx context.Context, path string) (Summary, error) {
	archive, err := OpenArchive(ctx, path)
	if err != nil {
		return Summary{}, err
	}
	tableOrder, _ := tableOrderForVersion(archive.manifest.APIVersion)
	for _, name := range tableOrder {
		if err := archive.ReadTable(ctx, name, func(raw json.RawMessage) error {
			return validateTableRecordForVersion(archive.manifest.APIVersion, name, raw)
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
