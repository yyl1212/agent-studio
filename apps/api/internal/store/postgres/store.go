package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
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
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database URL: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return &Store{pool: pool, poolStatsSource: pgxPoolStatsSource{pool: pool}}, nil
}

func (store *Store) Close() {
	store.pool.Close()
}

func mapNotFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
