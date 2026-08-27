package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/yyl1212/agent-studio/apps/api/internal/observability"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

var errPoolMetricsUnavailable = errors.New("postgres pool metrics unavailable")

type poolSnapshot interface {
	MaxConns() int32
	AcquiredConns() int32
	IdleConns() int32
}

type poolStatsSource interface {
	Snapshot() poolSnapshot
}

type pgxPoolStatsSource struct {
	pool *pgxpool.Pool
}

func (source pgxPoolStatsSource) Snapshot() poolSnapshot {
	return source.pool.Stat()
}

func (store *Store) RegisterPoolMetrics(providers observability.Providers) (metric.Registration, error) {
	if store == nil || store.poolStatsSource == nil {
		return nil, errPoolMetricsUnavailable
	}
	meter := providers.Meter("agent-studio/postgres")
	connections, err := meter.Int64ObservableGauge("agent_studio.postgres.pool.connections")
	if err != nil {
		return nil, errPoolMetricsUnavailable
	}
	registration, err := meter.RegisterCallback(func(_ context.Context, observer metric.Observer) error {
		snapshot := store.poolStatsSource.Snapshot()
		observer.ObserveInt64(connections, int64(snapshot.MaxConns()), metric.WithAttributes(attribute.String("state", "max")))
		observer.ObserveInt64(connections, int64(snapshot.AcquiredConns()), metric.WithAttributes(attribute.String("state", "acquired")))
		observer.ObserveInt64(connections, int64(snapshot.IdleConns()), metric.WithAttributes(attribute.String("state", "idle")))
		return nil
	}, connections)
	if err != nil {
		return nil, errPoolMetricsUnavailable
	}
	return registration, nil
}
