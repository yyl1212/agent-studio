package postgres

import (
	"context"
	"fmt"

	"github.com/yyl1212/agent-studio/internal/database"
)

func (store *Store) Migrate(ctx context.Context) error {
	return database.Migrate(ctx, store.pool)
}

func (store *Store) Ready(ctx context.Context) error {
	if err := store.pool.Ping(ctx); err != nil {
		return fmt.Errorf("ping database: %w", err)
	}
	current, err := database.CurrentVersion(ctx, store.pool)
	if err != nil {
		return fmt.Errorf("read migration readiness: %w", err)
	}
	latest, err := database.LatestVersion()
	if err != nil {
		return err
	}
	if current != latest {
		return fmt.Errorf("database migration version %d, want %d", current, latest)
	}
	return nil
}
