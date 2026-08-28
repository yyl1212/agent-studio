package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	"github.com/yyl1212/agent-studio/internal/database"
)

var (
	ErrNotFound         = domain.ErrNotFound
	ErrRevisionConflict = domain.ErrRevisionConflict
)

type Store struct {
	pool            *pgxpool.Pool
	poolStatsSource poolStatsSource
}

func Open(ctx context.Context, databaseURL string) (*Store, error) {
	pool, err := database.OpenPool(ctx, databaseURL)
	if err != nil {
		return nil, err
	}
	return &Store{pool: pool, poolStatsSource: pgxPoolStatsSource{pool: pool}}, nil
}

func (store *Store) Close() {
	store.pool.Close()
}

func (store *Store) PrepareRuntime(ctx context.Context) (*database.MaintenanceLease, error) {
	return database.PrepareRuntime(ctx, store.pool)
}

func mapNotFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
