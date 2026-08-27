package postgres

import (
	"context"
	"testing"

	"github.com/yyl1212/agent-studio/apps/api/internal/observability"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

type fixedPoolSnapshot struct {
	max      int32
	acquired int32
	idle     int32
}

func (snapshot fixedPoolSnapshot) MaxConns() int32      { return snapshot.max }
func (snapshot fixedPoolSnapshot) AcquiredConns() int32 { return snapshot.acquired }
func (snapshot fixedPoolSnapshot) IdleConns() int32     { return snapshot.idle }

type fixedPoolStatsSource struct {
	snapshot poolSnapshot
}

func (source fixedPoolStatsSource) Snapshot() poolSnapshot { return source.snapshot }

func TestRegisterPoolMetricsMapsStatesAndUnregisters(t *testing.T) {
	reader := metric.NewManualReader()
	meterProvider := metric.NewMeterProvider(metric.WithReader(reader))
	t.Cleanup(func() { _ = meterProvider.Shutdown(context.Background()) })
	store := &Store{poolStatsSource: fixedPoolStatsSource{snapshot: fixedPoolSnapshot{max: 12, acquired: 3, idle: 7}}}
	registration, err := store.RegisterPoolMetrics(observability.Providers{MeterProvider: meterProvider})
	if err != nil {
		t.Fatal(err)
	}

	points := collectPoolConnectionPoints(t, reader)
	want := map[string]int64{"max": 12, "acquired": 3, "idle": 7}
	if len(points) != len(want) {
		t.Fatalf("points=%v", points)
	}
	for state, value := range want {
		if points[state] != value {
			t.Fatalf("state %s=%d, want=%d; all=%v", state, points[state], value, points)
		}
	}

	if err := registration.Unregister(); err != nil {
		t.Fatal(err)
	}
	if points = collectPoolConnectionPoints(t, reader); len(points) != 0 {
		t.Fatalf("points after unregister=%v", points)
	}
}

func collectPoolConnectionPoints(t *testing.T, reader *metric.ManualReader) map[string]int64 {
	t.Helper()
	var collected metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &collected); err != nil {
		t.Fatal(err)
	}
	points := make(map[string]int64)
	for _, scope := range collected.ScopeMetrics {
		for _, current := range scope.Metrics {
			if current.Name != "agent_studio.postgres.pool.connections" {
				t.Fatalf("unexpected metric=%q", current.Name)
			}
			gauge, ok := current.Data.(metricdata.Gauge[int64])
			if !ok {
				t.Fatalf("metric data=%T", current.Data)
			}
			for _, point := range gauge.DataPoints {
				attributes := point.Attributes.ToSlice()
				if len(attributes) != 1 || string(attributes[0].Key) != "state" {
					t.Fatalf("attributes=%#v", attributes)
				}
				points[attributes[0].Value.AsString()] = point.Value
			}
		}
	}
	return points
}
