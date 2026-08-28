package database

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrMaintenanceBusy = errors.New("database maintenance lock is busy")
	ErrSchemaTooNew    = errors.New("database schema is newer than this runtime")

	errMaintenanceLeaseMode     = errors.New("exclusive maintenance lease required")
	errMaintenanceLeaseReleased = errors.New("database maintenance lease is released")
)

const maintenanceLockKey int64 = 918273645

type lockMode uint8

const (
	lockModeShared lockMode = iota + 1
	lockModeExclusive
)

type MaintenanceLease struct {
	conn     *pgxpool.Conn
	mode     lockMode
	released atomic.Bool
}

func TryShared(ctx context.Context, pool *pgxpool.Pool) (*MaintenanceLease, error) {
	return tryMaintenanceLock(ctx, pool, lockModeShared)
}

func TryExclusive(ctx context.Context, pool *pgxpool.Pool) (*MaintenanceLease, error) {
	return tryMaintenanceLock(ctx, pool, lockModeExclusive)
}

func tryMaintenanceLock(ctx context.Context, pool *pgxpool.Pool, mode lockMode) (*MaintenanceLease, error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire database maintenance connection: %w", err)
	}
	query := "SELECT pg_try_advisory_lock_shared($1)"
	if mode == lockModeExclusive {
		query = "SELECT pg_try_advisory_lock($1)"
	}
	var acquired bool
	if err := conn.QueryRow(ctx, query, maintenanceLockKey).Scan(&acquired); err != nil {
		discardMaintenanceConnection(conn)
		return nil, fmt.Errorf("acquire database maintenance lock: %w", err)
	}
	if !acquired {
		conn.Release()
		return nil, ErrMaintenanceBusy
	}
	return &MaintenanceLease{conn: conn, mode: mode}, nil
}

func PrepareRuntime(ctx context.Context, pool *pgxpool.Pool) (*MaintenanceLease, error) {
	current, err := CurrentVersion(ctx, pool)
	if err != nil {
		return nil, fmt.Errorf("read database migration version: %w", err)
	}
	latest, err := LatestVersion()
	if err != nil {
		return nil, fmt.Errorf("read runtime migration version: %w", err)
	}
	switch {
	case current > latest:
		return nil, ErrSchemaTooNew
	case current == latest:
		lease, err := TryShared(ctx, pool)
		if err != nil {
			return nil, err
		}
		confirmed, err := CurrentVersion(ctx, lease.conn)
		if err != nil || confirmed != latest {
			_ = lease.Release(context.Background())
			return nil, errors.New("database migration version changed during startup")
		}
		return lease, nil
	default:
		lease, err := TryExclusive(ctx, pool)
		if err != nil {
			return nil, err
		}
		confirmed, err := CurrentVersion(ctx, lease.conn)
		if err != nil {
			_ = lease.Release(context.Background())
			return nil, fmt.Errorf("confirm database migration version: %w", err)
		}
		if confirmed > latest {
			_ = lease.Release(context.Background())
			return nil, ErrSchemaTooNew
		}
		if confirmed < latest {
			if err := lease.Migrate(ctx); err != nil {
				_ = lease.Release(context.Background())
				return nil, err
			}
		}
		if err := lease.Downgrade(ctx); err != nil {
			_ = lease.Release(context.Background())
			return nil, err
		}
		return lease, nil
	}
}

func (lease *MaintenanceLease) Migrate(ctx context.Context) error {
	if lease.released.Load() {
		return errMaintenanceLeaseReleased
	}
	if lease.mode != lockModeExclusive {
		return errMaintenanceLeaseMode
	}
	return Migrate(ctx, lease.conn)
}

func (lease *MaintenanceLease) Downgrade(ctx context.Context) error {
	if lease.released.Load() {
		return errMaintenanceLeaseReleased
	}
	if lease.mode != lockModeExclusive {
		return errMaintenanceLeaseMode
	}
	var shared bool
	if err := lease.conn.QueryRow(ctx, "SELECT pg_try_advisory_lock_shared($1)", maintenanceLockKey).Scan(&shared); err != nil {
		lease.invalidate()
		return fmt.Errorf("acquire shared database maintenance lock: %w", err)
	}
	if !shared {
		lease.invalidate()
		return errors.New("acquire shared database maintenance lock")
	}
	var unlocked bool
	if err := lease.conn.QueryRow(ctx, "SELECT pg_advisory_unlock($1)", maintenanceLockKey).Scan(&unlocked); err != nil {
		lease.invalidate()
		return fmt.Errorf("release exclusive database maintenance lock: %w", err)
	}
	if !unlocked {
		lease.invalidate()
		return errors.New("release exclusive database maintenance lock")
	}
	lease.mode = lockModeShared
	return nil
}

func (lease *MaintenanceLease) Release(ctx context.Context) error {
	if lease.released.Swap(true) {
		return nil
	}
	query := "SELECT pg_advisory_unlock_shared($1)"
	if lease.mode == lockModeExclusive {
		query = "SELECT pg_advisory_unlock($1)"
	}
	var unlocked bool
	if err := lease.conn.QueryRow(ctx, query, maintenanceLockKey).Scan(&unlocked); err != nil {
		discardMaintenanceConnection(lease.conn)
		return fmt.Errorf("release database maintenance lock: %w", err)
	}
	if !unlocked {
		discardMaintenanceConnection(lease.conn)
		return errors.New("release database maintenance lock")
	}
	lease.conn.Release()
	return nil
}

func (lease *MaintenanceLease) invalidate() {
	if lease.released.Swap(true) {
		return
	}
	discardMaintenanceConnection(lease.conn)
}

func discardMaintenanceConnection(conn *pgxpool.Conn) {
	_ = conn.Conn().Close(context.Background())
	conn.Release()
}
