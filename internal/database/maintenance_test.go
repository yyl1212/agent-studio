package database

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMaintenanceLeaseCompatibility(t *testing.T) {
	pool := openIsolatedPool(t)
	first, err := TryShared(context.Background(), pool)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Release(context.Background())
	second, err := TryShared(context.Background(), pool)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Release(context.Background())
	if _, err := TryExclusive(context.Background(), pool); !errors.Is(err, ErrMaintenanceBusy) {
		t.Fatalf("TryExclusive() error = %v; want ErrMaintenanceBusy", err)
	}
}

func TestExclusiveDowngradesToSharedLockOnSameSession(t *testing.T) {
	pool := openIsolatedPool(t)
	lease, err := TryExclusive(context.Background(), pool)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release(context.Background())
	var backendPID int32
	if err := lease.conn.QueryRow(context.Background(), "SELECT pg_backend_pid()").Scan(&backendPID); err != nil {
		t.Fatal(err)
	}
	if err := lease.Downgrade(context.Background()); err != nil {
		t.Fatal(err)
	}
	var shared, exclusive int
	if err := pool.QueryRow(context.Background(), `SELECT
		count(*) FILTER (WHERE mode='ShareLock'),
		count(*) FILTER (WHERE mode='ExclusiveLock')
		FROM pg_locks WHERE pid=$1 AND locktype='advisory'`, backendPID).Scan(&shared, &exclusive); err != nil {
		t.Fatal(err)
	}
	if shared != 1 || exclusive != 0 {
		t.Fatalf("maintenance locks = shared:%d exclusive:%d; want 1,0", shared, exclusive)
	}
	if _, err := TryExclusive(context.Background(), pool); !errors.Is(err, ErrMaintenanceBusy) {
		t.Fatalf("TryExclusive() after downgrade error = %v; want ErrMaintenanceBusy", err)
	}
}

func TestMaintenanceReleaseIsIdempotentAndUnlocks(t *testing.T) {
	pool := openIsolatedPool(t)
	lease, err := TryShared(context.Background(), pool)
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.Release(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := lease.Release(context.Background()); err != nil {
		t.Fatalf("second Release() error = %v", err)
	}
	exclusive, err := TryExclusive(context.Background(), pool)
	if err != nil {
		t.Fatal(err)
	}
	defer exclusive.Release(context.Background())
}

func TestMaintenanceReleaseAfterConnectionCloseDoesNotLeakLock(t *testing.T) {
	pool := openIsolatedPool(t)
	lease, err := TryExclusive(context.Background(), pool)
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.conn.Conn().Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := lease.Release(context.Background()); err == nil {
		t.Fatal("Release() error = nil after connection close")
	}
	replacement, err := TryExclusive(context.Background(), pool)
	if err != nil {
		t.Fatalf("TryExclusive() after connection close error = %v", err)
	}
	defer replacement.Release(context.Background())
}

func TestMaintenanceReleaseWithCancelledContextDoesNotReturnLockedSessionToPool(t *testing.T) {
	pool := openIsolatedPool(t)
	lease, err := TryExclusive(context.Background(), pool)
	if err != nil {
		t.Fatal(err)
	}
	var backendPID int32
	if err := lease.conn.QueryRow(context.Background(), "SELECT pg_backend_pid()").Scan(&backendPID); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := lease.Release(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Release() error = %v; want context.Canceled", err)
	}
	var locks int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM pg_locks WHERE pid=$1 AND locktype='advisory'", backendPID).Scan(&locks); err != nil {
		t.Fatal(err)
	}
	if locks != 0 {
		t.Fatalf("backend %d retained %d advisory locks", backendPID, locks)
	}
}

func TestMaintenanceAcquireHonorsCancelledContext(t *testing.T) {
	pool := openIsolatedPool(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := TryShared(ctx, pool); !errors.Is(err, context.Canceled) {
		t.Fatalf("TryShared() error = %v; want context.Canceled", err)
	}
	lease, err := TryExclusive(context.Background(), pool)
	if err != nil {
		t.Fatalf("cancelled acquire leaked a lock: %v", err)
	}
	defer lease.Release(context.Background())
}

func TestMaintenanceLeaseMigrateRequiresLiveExclusiveLease(t *testing.T) {
	pool := openIsolatedPool(t)
	shared, err := TryShared(context.Background(), pool)
	if err != nil {
		t.Fatal(err)
	}
	if err := shared.Migrate(context.Background()); err == nil {
		t.Fatal("shared Migrate() error = nil")
	}
	if err := shared.Release(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := shared.Migrate(context.Background()); err == nil {
		t.Fatal("released Migrate() error = nil")
	}
}

func TestPrepareRuntimeRejectsSchemaNewerThanRuntime(t *testing.T) {
	pool := openIsolatedPool(t)
	if _, err := pool.Exec(context.Background(), `CREATE TABLE schema_migrations (
		version bigint PRIMARY KEY,name text NOT NULL,applied_at timestamptz NOT NULL DEFAULT now()
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), "INSERT INTO schema_migrations(version,name) VALUES(8,'future')"); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareRuntime(context.Background(), pool); !errors.Is(err, ErrSchemaTooNew) {
		t.Fatalf("PrepareRuntime() error = %v; want ErrSchemaTooNew", err)
	}
}

func TestPrepareRuntimeUsesSharedLeaseForCurrentSchema(t *testing.T) {
	pool := openIsolatedPool(t)
	if err := Migrate(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	lease, err := PrepareRuntime(context.Background(), pool)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release(context.Background())
	second, err := TryShared(context.Background(), pool)
	if err != nil {
		t.Fatalf("TryShared() with runtime lease error = %v", err)
	}
	defer second.Release(context.Background())
	if _, err := TryExclusive(context.Background(), pool); !errors.Is(err, ErrMaintenanceBusy) {
		t.Fatalf("TryExclusive() error = %v; want ErrMaintenanceBusy", err)
	}
}

func TestPrepareRuntimeSignalsLostMaintenanceConnection(t *testing.T) {
	pool := openIsolatedPool(t)
	if err := Migrate(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	lease, err := PrepareRuntime(context.Background(), pool)
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.conn.Conn().Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-lease.Lost():
		if err == nil {
			t.Fatal("lost error is nil")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("maintenance connection loss was not signaled")
	}
	exclusive, err := TryExclusive(context.Background(), pool)
	if err != nil {
		t.Fatalf("exclusive lock after loss: %v", err)
	}
	defer exclusive.Release(context.Background())
}

func TestPrepareRuntimeMigratesOutdatedSchemaBeforeDowngrade(t *testing.T) {
	pool := openIsolatedPool(t)
	lease, err := PrepareRuntime(context.Background(), pool)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release(context.Background())
	current, err := CurrentVersion(context.Background(), pool)
	if err != nil || current != 7 {
		t.Fatalf("CurrentVersion() = %d, %v; want 7, nil", current, err)
	}
	second, err := TryShared(context.Background(), pool)
	if err != nil {
		t.Fatalf("TryShared() after migration error = %v", err)
	}
	defer second.Release(context.Background())
	if _, err := TryExclusive(context.Background(), pool); !errors.Is(err, ErrMaintenanceBusy) {
		t.Fatalf("TryExclusive() error = %v; want ErrMaintenanceBusy", err)
	}
}
